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
package external

import (
	"context"
	"io"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
	metricsstore "k8s.io/kube-state-metrics/v2/pkg/metrics_store"
)

// collectors defines behaviors to implement custom Go-based collectors for the "main" instance.
type gvkr struct {
	schema.GroupVersionKind
	schema.GroupVersionResource
}
type collectors interface {
	BuildCollector(ctx context.Context, kubeconfig string) *metricsstore.MetricsStore
	GVKR() gvkr
	Register()
}

// CollectorsType holds external collectors and manages their lifecycle.
type CollectorsType struct {
	mu              sync.RWMutex
	kubeconfig      string
	collectors      []collectors
	builtCollectors []builtCollector
}

// builtCollector pairs a built metrics store with the resource it exposes.
type builtCollector struct {
	resource string
	store    *metricsstore.MetricsStore
}

// SetKubeConfig sets the kubeconfig for the collectors.
func (ct *CollectorsType) SetKubeConfig(kubeconfig string) *CollectorsType {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	ct.kubeconfig = kubeconfig

	return ct
}

// Register adds a collector to the list.
func (ct *CollectorsType) Register(c collectors) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	ct.collectors = append(ct.collectors, c)
}

// Build initializes all registered collectors.
func (ct *CollectorsType) Build(ctx context.Context) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	logger := klog.FromContext(ctx)

	for _, c := range ct.collectors {
		ct.builtCollectors = append(ct.builtCollectors, builtCollector{
			resource: c.GVKR().Resource,
			store:    c.BuildCollector(ctx, ct.kubeconfig),
		})
		c.Register()
	}

	logger.V(0).Info("Registered external collectors", "collectors", ct.collectors)
}

// Write writes metrics from all built collectors to the writer.
func (ct *CollectorsType) Write(w io.Writer) {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	for _, bc := range ct.builtCollectors {
		mw := metricsstore.NewMetricsWriter(bc.resource, bc.store)
		_ = mw.WriteAll(w)
	}
}

var collectorsInstance = &CollectorsType{
	collectors: []collectors{
		// Add collectors below:
		// &clusterResourceQuotaCollector{}, // see ./clusterresourcequota.go.md
	},
}

// GetCollectors returns the singleton collectors instance.
func GetCollectors() *CollectorsType {
	return collectorsInstance
}
