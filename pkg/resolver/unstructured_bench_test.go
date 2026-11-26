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
	"testing"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
)

// BenchmarkUnstructuredResolve measures field lookups on a representative
// resource object, including a missing field, which resolves to nil.
func BenchmarkUnstructuredResolve(b *testing.B) {
	obj := celBenchObject(0)
	// Keep verbose missing-field logging out of the measurement.
	resolver := NewUnstructuredResolver(logr.Discard())

	for _, testCase := range []struct {
		name  string
		query string
		want  map[string]string
	}{
		{name: "field", query: "spec.replicas", want: map[string]string{"spec.replicas": "3"}},
		{name: "missing", query: "status.unavailableReplicas", want: nil},
	} {
		b.Run(testCase.name, func(b *testing.B) {
			got := resolver.Resolve(testCase.query, obj)
			if !cmp.Equal(got, testCase.want) {
				b.Fatalf("Resolve(%q) = %#v, want %#v", testCase.query, got, testCase.want)
			}

			b.ReportAllocs()

			for b.Loop() {
				got = resolver.Resolve(testCase.query, obj)
			}

			if !cmp.Equal(got, testCase.want) {
				b.Fatalf("Resolve(%q) = %#v, want %#v", testCase.query, got, testCase.want)
			}
		})
	}
}
