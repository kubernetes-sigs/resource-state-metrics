/*
Copyright 2026 The Kubernetes resource-state-metrics Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package internal

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/pprof"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kubernetes-sigs/resource-state-metrics/external"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/expfmt"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	// readHeaderTimeout bounds how long the server waits for request headers before closing the connection.
	readHeaderTimeout = 5 * time.Second
)

// server defines behaviours for a Prometheus-based exposition server.
type server interface {
	// Build sets up the server with the given gatherer.
	build(ctx context.Context, client kubernetes.Interface, gatherer prometheus.Gatherer) *http.Server
}

// selfServer implements the server interface, and exposes telemetry metrics.
type selfServer struct {
	promHTTPLogger
	// addr is the http.Server address to listen on.
	addr string
}

// mainServer implements the server interface, and exposes resource metrics.
type mainServer struct {
	promHTTPLogger
	// addr is the http.Server address to listen on.
	addr string
	// stores is the thread-safe map of currently active stores per resource.
	stores *sync.Map
	// requestsDurationVec is a histogram denoting the request durations for the metrics endpoint. The metric itself is
	// registered in the telemetry registry, and will be available along with all other main metrics, to not pollute the
	// resource metrics.
	requestsDurationVec prometheus.ObserverVec
	// Cluster configuration (needed for LW clients).
	kubeconfig string
}

// Ensure that selfServer implements the server interface.
var _ server = &selfServer{}

// Ensure that mainServer implements the server interface.
var _ server = &mainServer{}

// newSelfServer returns a new selfServer.
func newSelfServer(addr string) *selfServer {
	return &selfServer{
		promHTTPLogger: promHTTPLogger{"self"},
		addr:           addr,
	}
}

// newMainServer returns a new mainServer.
func newMainServer(addr, kubeconfig string, stores *sync.Map, requestsDurationVec prometheus.ObserverVec) *mainServer {
	return &mainServer{
		promHTTPLogger:      promHTTPLogger{"main"},
		addr:                addr,
		kubeconfig:          kubeconfig,
		stores:              stores,
		requestsDurationVec: requestsDurationVec,
	}
}

// Build sets up the selfServer with the given gatherer.
func (s *selfServer) build(ctx context.Context, client kubernetes.Interface, gatherer prometheus.Gatherer) *http.Server {
	logger := klog.FromContext(ctx)
	mux := http.NewServeMux()

	// Handle the pprof debug paths.
	mux.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index))
	mux.Handle("/debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline))
	mux.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
	mux.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
	mux.Handle("/debug/pprof/trace", http.HandlerFunc(pprof.Trace))

	// Handle the metrics path.
	registry, ok := gatherer.(*prometheus.Registry)
	if !ok {
		logger.Error(errors.New("failed to cast gatherer to *prometheus.Registry"), "cannot handle metrics")

		return nil
	}

	metricsHandler := promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{
		ErrorLog:      s.promHTTPLogger,
		ErrorHandling: promhttp.ContinueOnError,
		Registry:      registry,
	})
	mux.Handle("/metrics", metricsHandler)

	// Handle the readyz path.
	readyzProber := newReadyz(s.source)
	mux.Handle(readyzProber.text(), readyzProber.probe(ctx, logger, client))

	return &http.Server{
		ErrorLog:          log.New(os.Stdout, s.source, log.LstdFlags|log.Lshortfile),
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		Addr:              s.addr,
	}
}

func parseQuality(params []string) float64 {
	for _, param := range params {
		param = strings.TrimSpace(param)
		if strings.HasPrefix(strings.ToLower(param), "q=") {
			if q, err := strconv.ParseFloat(strings.TrimSpace(param[2:]), 64); err == nil {
				return q
			}
		}
	}

	return 1.0
}

func acceptsGzip(header http.Header) bool {
	gzipQuality := -1.0
	starQuality := -1.0

	for _, v := range header.Values("Accept-Encoding") {
		for _, clause := range strings.Split(v, ",") {
			clause = strings.TrimSpace(clause)
			if clause == "" {
				continue
			}

			parts := strings.Split(clause, ";")
			encoding := strings.ToLower(strings.TrimSpace(parts[0]))
			q := parseQuality(parts[1:])

			switch encoding {
			case "gzip":
				gzipQuality = q
			case "*":
				starQuality = q
			}
		}
	}

	if gzipQuality >= 0 {
		return gzipQuality > 0
	}

	return starQuality > 0
}

func createMetricsHandler(server *mainServer, logger klog.Logger, binarySemaphore *sync.RWMutex, generator func(w io.Writer)) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		binarySemaphore.RLock()
		defer binarySemaphore.RUnlock()

		writer.Header().Set("Vary", "Accept-Encoding")

		contentType := expfmt.NegotiateIncludingOpenMetrics(request.Header)
		if contentType.FormatType() != expfmt.TypeOpenMetrics {
			contentType = expfmt.NewFormat(expfmt.TypeTextPlain)
		}

		writer.Header().Set("Content-Type", string(contentType))

		var out io.Writer = writer

		if acceptsGzip(request.Header) {
			writer.Header().Set("Content-Encoding", "gzip")

			gz := gzip.NewWriter(writer)

			defer func() {
				if err := gz.Close(); err != nil {
					logger.Error(err, "error closing gzip writer", "source", server.source)
				}
			}()

			out = gz
		}

		// Generate metrics.
		generator(out)

		// Write the OpenMetrics EOF trailer if the negotiated content type is OpenMetrics
		if contentType.FormatType() == expfmt.TypeOpenMetrics {
			if _, err := expfmt.FinalizeOpenMetrics(out); err != nil {
				logger.Error(err, "error writing OpenMetrics EOF trailer", "source", server.source)
			}
		}
	}
}

// Build sets up the mainServer with the given gatherer.
func (s *mainServer) build(ctx context.Context, client kubernetes.Interface, _ prometheus.Gatherer) *http.Server {
	logger := klog.FromContext(ctx)
	mux := http.NewServeMux()

	var binarySemaphore sync.RWMutex

	hMetrics := createMetricsHandler(s, logger, &binarySemaphore, func(w io.Writer) {
		s.stores.Range(func(_, value any) bool {
			stores, ok := value.([]*StoreType)
			if !ok {
				logger.Error(errors.New("invalid store type in map"), "error writing metrics", "source", s.source)

				return true
			}

			err := newMetricsWriter(stores...).writeStores(w)
			if err != nil {
				logger.Error(err, "error writing metrics", "source", s.source)
			}

			return true
		})
	})

	if s.requestsDurationVec != nil {
		hMetrics = promhttp.InstrumentHandlerDuration(s.requestsDurationVec, hMetrics)
	}

	mux.Handle("/metrics", hMetrics)

	// Handle the external path.
	externalCollectors := external.GetCollectors().SetKubeConfig(s.kubeconfig)
	externalCollectors.Build(ctx)

	hExternal := createMetricsHandler(s, logger, &binarySemaphore, func(w io.Writer) {
		externalCollectors.Write(w)
	})

	if s.requestsDurationVec != nil {
		hExternal = promhttp.InstrumentHandlerDuration(s.requestsDurationVec, hExternal)
	}

	mux.Handle("/external", hExternal)

	// Handle the healthz path.
	healthzProber := newHealthz(s.source)
	mux.Handle(healthzProber.text(), healthzProber.probe(ctx, logger, client))

	// Handle the livez path.
	livezProber := newLivez(s.source)
	mux.Handle(livezProber.text(), livezProber.probe(ctx, logger, client))

	return &http.Server{
		ErrorLog:          log.New(os.Stdout, s.source, log.LstdFlags|log.Lshortfile),
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		Addr:              s.addr,
	}
}

// promHTTPLogger implements promhttp.Logger.
type promHTTPLogger struct {
	// source is the originating server for the log.
	source string
}

// Println logs on all errors received by promhttp.Logger.
func (l promHTTPLogger) Println(v ...interface{}) {
	klog.ErrorS(fmt.Errorf("%s", v), "err", "source", l.source)
}
