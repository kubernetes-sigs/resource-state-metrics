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

package framework

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kubernetes-sigs/resource-state-metrics/internal"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	rsmclientset "github.com/kubernetes-sigs/resource-state-metrics/pkg/generated/clientset/versioned"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/options"
	"github.com/kubernetes-sigs/resource-state-metrics/tests/testutil"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/discovery"
	memorycache "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"
)

const (
	pollInterval     = 100 * time.Millisecond
	readyTimeout     = 30 * time.Second
	minTypeLineParts = 3
	localhostAddr    = "127.0.0.1"
)

// Framework provides utilities for envtest-based integration tests backed by
// real Kubernetes clients.
type Framework struct {
	Options   *options.Options
	RSMClient rsmclientset.Interface

	kubeClient      kubernetes.Interface
	dynamicClient   dynamic.Interface
	discoveryClient discovery.DiscoveryInterface
	restMapper      meta.RESTMapper
	controller      *internal.Controller
}

// NewForConfig creates a new Framework backed by real clients from the given
// rest.Config. This is intended for envtest-style tests where a real
// kube-apiserver is available.
func NewForConfig(_ context.Context, cfg *rest.Config) (*Framework, error) {
	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	rsmClient, err := rsmclientset.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create RSM client: %w", err)
	}

	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	discoClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client: %w", err)
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memorycache.NewMemCacheClient(discoClient))

	opts := options.NewOptions(klog.Background())
	opts.Read()

	return &Framework{
		Options:         opts,
		RSMClient:       rsmClient,
		kubeClient:      kubeClient,
		dynamicClient:   dynClient,
		discoveryClient: discoClient,
		restMapper:      mapper,
	}, nil
}

// Start configures workers and ports, creates the controller, launches it in a
// background goroutine, and waits for the main metrics port to accept
// connections before returning.
func (f *Framework) Start(ctx context.Context, workers int) error {
	f.Options.Workers = &workers

	mainPort, err := testutil.GetFreePort(ctx)
	if err != nil {
		return fmt.Errorf("failed to allocate main port: %w", err)
	}

	f.Options.MainPort = &mainPort

	selfPort, err := testutil.GetFreePort(ctx)
	if err != nil {
		return fmt.Errorf("failed to allocate self port: %w", err)
	}

	f.Options.SelfPort = &selfPort

	f.controller = internal.NewController(ctx, f.Options, f.kubeClient, f.RSMClient, f.dynamicClient, f.discoveryClient)

	go func() {
		if err := f.controller.Run(ctx, *f.Options.Workers); err != nil {
			klog.FromContext(ctx).Error(err, "controller exited with error")
		}
	}()

	if err := f.waitForMainPort(ctx); err != nil {
		return fmt.Errorf("controller failed to become ready: %w", err)
	}

	return nil
}

// ApplyCRFromYAML reads a YAML file, unmarshals it to an unstructured object,
// resolves the GVR via the REST mapper, and creates it using the dynamic client.
func (f *Framework) ApplyCRFromYAML(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read YAML file %s: %w", path, err)
	}

	obj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal(data, obj); err != nil {
		return fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	gvk := obj.GroupVersionKind()

	mapping, err := f.restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("failed to get REST mapping for %s: %w", gvk, err)
	}

	gvr := mapping.Resource
	ns := obj.GetNamespace()

	_, err = f.dynamicClient.Resource(gvr).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create resource %s/%s: %w", ns, obj.GetName(), err)
	}

	return nil
}

// CompareMainMetrics scrapes the main metrics endpoint and compares the result
// against expectedMetricLines, filtering to only the metric families declared
// in the expected text (identified by "# TYPE" lines). Returns nil on match.
func (f *Framework) CompareMainMetrics(expectedMetricLines []string) error {
	expectedMetrics := strings.Join(expectedMetricLines, "\n") + "\n"

	var familyNames []string

	for _, line := range strings.Split(expectedMetrics, "\n") {
		if strings.HasPrefix(line, "# TYPE ") {
			if parts := strings.Fields(line); len(parts) >= minTypeLineParts {
				familyNames = append(familyNames, parts[2])
			}
		}
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/metrics", *f.Options.MainPort)

	return promtestutil.ScrapeAndCompare(url, strings.NewReader(expectedMetrics), familyNames...)
}

// WaitForRMMProcessed polls the RSM client until the named ResourceMetricsMonitor
// has a Processed status condition, or the timeout expires.
func (f *Framework) WaitForRMMProcessed(ctx context.Context, namespace, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for RMM %s/%s to be processed: %w", namespace, name, ctx.Err())
		case <-ticker.C:
			rmm, err := f.RSMClient.ResourceStateMetricsV1alpha1().ResourceMetricsMonitors(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				continue
			}

			for _, cond := range rmm.Status.Conditions {
				if cond.Type == v1alpha1.ConditionType[v1alpha1.ConditionTypeProcessed] {
					return nil
				}
			}
		}
	}
}

// FetchMainMetrics performs an HTTP GET against the main metrics endpoint and
// returns the response body as a string.
func (f *Framework) FetchMainMetrics(ctx context.Context) (string, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/metrics", *f.Options.MainPort)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req) //nolint:gosec // URL is constructed from localhost only
	if err != nil {
		return "", fmt.Errorf("failed to fetch metrics: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(body), nil
}

// waitForMainPort polls the main metrics port until a TCP connection succeeds
// or 30 seconds elapse.
func (f *Framework) waitForMainPort(ctx context.Context) error {
	deadline := time.After(readyTimeout)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	port := *f.Options.MainPort

	for {
		select {
		case <-deadline:
			return errors.New("timed out waiting for main port to accept connections")
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			dialer := net.Dialer{Timeout: pollInterval}

			conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", localhostAddr, port))
			if err == nil {
				_ = conn.Close()

				return nil
			}
		}
	}
}
