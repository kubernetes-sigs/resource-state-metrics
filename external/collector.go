/*
Copyright 2025 The Kubernetes resource-state-metrics Authors.

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

	"k8s.io/apimachinery/pkg/runtime/schema"
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

type collectorsType struct {
	kubeconfig      string
	collectors      []collectors
	builtCollectors []*metricsstore.MetricsStore
}

func (ct *collectorsType) SetKubeConfig(kubeconfig string) *collectorsType {
	ct.kubeconfig = kubeconfig

	return ct
}

func (ct *collectorsType) Register(c collectors) {
	ct.collectors = append(ct.collectors, c)
}

func (ct *collectorsType) Build(ctx context.Context) {
	for _, c := range ct.collectors {
		ct.builtCollectors = append(ct.builtCollectors, c.BuildCollector(ctx, ct.kubeconfig))
		c.Register()
	}
}

func (ct *collectorsType) Write(w io.Writer) {
	for _, c := range ct.builtCollectors {
		mw := metricsstore.NewMetricsWriter(c)
		_ = mw.WriteAll(w)
	}
}

var collectorsInstance = &collectorsType{
	collectors: []collectors{
		// Add collectors below:
		// &clusterResourceQuotaCollector{}, // see ./clusterresourcequota.go.md
	},
}

//nolint:revive
func CollectorsGetter() *collectorsType {
	return collectorsInstance
}
