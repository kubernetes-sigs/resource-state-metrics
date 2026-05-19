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
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/pprof"
	"os"
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

// Build sets up the mainServer with the given gatherer.
func (s *mainServer) build(ctx context.Context, client kubernetes.Interface, _ prometheus.Gatherer) *http.Server {
	logger := klog.FromContext(ctx)
	mux := http.NewServeMux()

	// Handle the metrics path.
	var binarySemaphore sync.RWMutex

	metricsHandler := func(generator func(w http.ResponseWriter)) http.HandlerFunc {
		return func(writer http.ResponseWriter, request *http.Request) {
			binarySemaphore.RLock()
			defer binarySemaphore.RUnlock()

			// * The focus here is textual-only, in-line with a gauge-only philosophy.
			// * Prometheus may support `info`, `stateset`, `gaugehistogram`, etc.
			// OpenMetrics types in the future, but the focus here will remain the
			// same, i.e., we'll only ever push `gauge` metrics, and show the same in
			// their metadata.
			// * NOTE By opting-into OpenMetrics below, we are contractually
			// obligated to support expfmt.MetricFamilyToOpenMetrics conventions at
			// all times, for the parts that impact us (`gauge` metrics, in our
			// case).
			// Refer: https://pkg.go.dev/github.com/prometheus/common@v0.67.5/expfmt#MetricFamilyToOpenMetrics
			// * Negotiation can set content type to Protobuf as well, but we will
			// ignore that, and always respond with an OpenMetrics text format.
			contentType := expfmt.NegotiateIncludingOpenMetrics(request.Header)
			if contentType.FormatType() != expfmt.TypeOpenMetrics {
				contentType = expfmt.NewFormat(expfmt.TypeTextPlain)
			}

			writer.Header().Set("Content-Type", string(contentType))

			// Generate metrics.
			generator(writer)

			// Write the OpenMetrics EOF trailer if the negotiated content type is OpenMetrics
			if contentType.FormatType() == expfmt.TypeOpenMetrics {
				if _, err := expfmt.FinalizeOpenMetrics(writer); err != nil {
					logger.Error(err, "error writing OpenMetrics EOF trailer", "source", s.source)
				}
			}
		}
	}
	mux.Handle("/metrics", promhttp.InstrumentHandlerDuration(s.requestsDurationVec, metricsHandler(func(w http.ResponseWriter) {
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
	})))

	// Handle the external path.
	externalCollectors := external.GetCollectors().SetKubeConfig(s.kubeconfig)
	externalCollectors.Build(ctx)
	mux.Handle("/external", promhttp.InstrumentHandlerDuration(s.requestsDurationVec, metricsHandler(func(w http.ResponseWriter) {
		externalCollectors.Write(w)
	})))

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
