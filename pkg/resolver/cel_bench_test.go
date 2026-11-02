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
package resolver

import (
	"fmt"
	"testing"
	"time"

	"k8s.io/klog/v2"
)

type celBenchQuery struct {
	name  string
	query string
}

// celBenchQueries returns one query per common shape: a plain field access
// and each of the custom functions registered in the CEL environment.
func celBenchQueries() []celBenchQuery {
	return []celBenchQuery{
		{name: "field", query: "o.spec.replicas"},
		{name: "quantity", query: "quantity(o.spec.memory)"},
		{name: "unixSeconds", query: "unixSeconds(o.metadata.creationTimestamp)"},
		{name: "labelPrefix", query: "labelPrefix(o.metadata.labels, 'label_')"},
	}
}

func celBenchObject(i int) map[string]any {
	return map[string]any{
		"metadata": map[string]any{
			"name":              fmt.Sprintf("obj-%d", i),
			"namespace":         "default",
			"creationTimestamp": "2024-01-15T10:30:00Z",
			"labels": map[string]any{
				"app":  "demo",
				"tier": "backend",
			},
		},
		"spec": map[string]any{
			"replicas": int64(3),
			"memory":   "512Mi",
		},
		"status": map[string]any{
			"readyReplicas": int64(2),
			"conditions": []any{
				map[string]any{"type": "Ready", "status": "True"},
			},
		},
	}
}

func newCELBenchResolver() *CELResolver {
	return NewCELResolver(klog.Background(), 10e5, 5*time.Second, nil, "ns", "rmm", "fam")
}

// BenchmarkCELResolve measures a single Resolve call per query shape.
func BenchmarkCELResolve(b *testing.B) {
	for _, q := range celBenchQueries() {
		b.Run(q.name, func(b *testing.B) {
			cr := newCELBenchResolver()
			obj := celBenchObject(0)

			b.ReportAllocs()

			for b.Loop() {
				_ = cr.Resolve(q.query, obj)
			}
		})
	}
}

// BenchmarkCELResolveParallel measures Resolve with concurrent callers sharing
// one resolver.
func BenchmarkCELResolveParallel(b *testing.B) {
	cr := newCELBenchResolver()
	obj := celBenchObject(0)

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = cr.Resolve("o.spec.replicas", obj)
		}
	})
}

// BenchmarkCELResolveResync approximates one resync of a store: 1000 objects,
// each resolved against every query in celBenchQueries.
func BenchmarkCELResolveResync(b *testing.B) {
	const objectCount = 1000

	cr := newCELBenchResolver()
	queries := celBenchQueries()

	objs := make([]map[string]any, objectCount)
	for i := range objs {
		objs[i] = celBenchObject(i)
	}

	b.ReportAllocs()

	for b.Loop() {
		for _, o := range objs {
			for _, q := range queries {
				_ = cr.Resolve(q.query, o)
			}
		}
	}
}
