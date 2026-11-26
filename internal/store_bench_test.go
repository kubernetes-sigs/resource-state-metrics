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
	"fmt"
	"os"
	"testing"

	"github.com/go-logr/logr"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"
)

// BenchmarkStoreAddUnstructured measures one batch of repeated object updates,
// including conversion, metric generation, and store cardinality tracking.
// The Foo fixture and spec.replicas query match the unstructured golden test.
func BenchmarkStoreAddUnstructured(b *testing.B) {
	manifest, err := os.ReadFile("../tests/manifests/custom-resource/custom-resource-foos.yaml")
	if err != nil {
		b.Fatal(err)
	}

	data, err := yaml.YAMLToJSON(manifest)
	if err != nil {
		b.Fatal(err)
	}

	var fixture unstructured.Unstructured
	if err := fixture.UnmarshalJSON(data); err != nil {
		b.Fatal(err)
	}

	for _, size := range []int{10, 100} {
		b.Run(fmt.Sprintf("objects=%d", size), func(b *testing.B) {
			family := &FamilyType{Family: v1alpha1.Family{
				Name:    "foos_replicas_total",
				Help:    "Total Foo replicas",
				Metrics: []v1alpha1.Metric{{Value: "spec.replicas"}},
			}}
			store := newStore(logr.Discard(), nil, []*FamilyType{family}, v1alpha1.ResolverTypeUnstructured, nil, 0, 0)
			store.SetCardinalityTracker(NewCardinalityTracker(0, 0.8))

			objects := make([]*unstructured.Unstructured, size)
			for i := range objects {
				obj := fixture.DeepCopy()
				name := fmt.Sprintf("test-sample-%d", i)
				obj.SetName(name)
				obj.SetUID(types.UID(name))

				objects[i] = obj
				if err := store.Add(obj); err != nil {
					b.Fatal(err)
				}
			}

			checkStoreBenchmarkMetrics(b, store, objects)

			b.ReportAllocs()

			for b.Loop() {
				for _, obj := range objects {
					if err := store.Add(obj); err != nil {
						b.Fatal(err)
					}
				}
			}

			checkStoreBenchmarkMetrics(b, store, objects)
		})
	}
}

func checkStoreBenchmarkMetrics(b *testing.B, store *StoreType, objects []*unstructured.Unstructured) {
	b.Helper()

	if len(store.metrics) != len(objects) {
		b.Fatalf("stored %d objects, want %d", len(store.metrics), len(objects))
	}

	for _, obj := range objects {
		want := fmt.Sprintf("kube_customresource_foos_replicas_total{group=\"samplecontroller.k8s.io\",version=\"v1alpha1\",kind=\"Foo\",name=%q,namespace=\"default\"} 1.000000\n", obj.GetName())
		got := store.metrics[obj.GetUID()]

		if len(got) != 1 || got[0] != want {
			b.Fatalf("metrics for %s = %v, want %q", obj.GetName(), got, want)
		}
	}
}
