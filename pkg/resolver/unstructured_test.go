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
package resolver

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/klog/v2"
)

func TestUnstructuredResolver_Resolve(t *testing.T) {
	t.Parallel()
	unstructuredObjectMap := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":      "test-deployment",
			"namespace": "test-namespace",
		},
		"fields": map[string]interface{}{
			"nil":     nil,
			"integer": 1,
			"string":  "bar",
			"array":   [3]string{"a", "b", "c"},
			"slice":   []string{"a", "b", "c"},
			"map": map[string]interface{}{
				"foo": map[string]interface{}{
					"bar": "baz",
				},
			},
			"float":   1.1,
			"rune":    'a',
			"boolean": true,
		},
	}
	tests := []struct {
		name  string
		query string
		want  map[string]string
	}{
		{
			name:  "field exists and is a integer literal",
			query: "fields.integer",
			want: map[string]string{
				"fields.integer": "1",
			},
		},
		{
			name:  "field exists and is a string literal",
			query: "fields.string",
			want: map[string]string{
				"fields.string": "bar",
			},
		},
		{
			name:  "field exists and is a float literal",
			query: "fields.float",
			want: map[string]string{
				"fields.float": "1.1",
			},
		},
		{
			name:  "field exists and is a rune literal",
			query: "fields.rune",
			want: map[string]string{
				"fields.rune": "97",
			},
		},
		{
			name:  "field exists and is a boolean literal",
			query: "fields.boolean",
			want: map[string]string{
				"fields.boolean": "true",
			},
		},
		{
			name:  "map syntax is not supported",
			query: "fields.map.foo.bar",
			want: map[string]string{
				"fields.map.foo.bar": "baz",
			},
		},
		// The following test-cases are taken from:
		// https://github.com/kubernetes/apimachinery/blob/v0.31.0/pkg/apis/meta/v1/unstructured/helpers_test.go#L64.
		{
			name:  "field exists and is nil",
			query: "fields.nil",
			want: map[string]string{
				"fields.nil": "<nil>",
			},
		},
		{
			name:  "error traversing obj",
			query: "fields.string.bar",
			want: map[string]string{
				"fields.string.bar": "fields.string.bar",
			},
		},
		{
			name:  "field does not exist",
			query: "fields.bar",
			want: map[string]string{
				"fields.bar": "fields.bar",
			},
		},
		{
			name:  "intermediate field does not exist",
			query: "fields.fake.string",
			want: map[string]string{
				"fields.fake.string": "fields.fake.string",
			},
		},
		{
			name:  "intermediate field is null", // happens easily in YAML
			query: "fields.nil.foo",
			want: map[string]string{
				"fields.nil.foo": "fields.nil.foo",
			},
		},
		{
			name:  "array syntax is not supported",
			query: "fields.array[1]",
			want: map[string]string{
				"fields.array[1]": "fields.array[1]",
			},
		},
		{
			name:  "slice syntax is not supported",
			query: "fields.slice[1]",
			want: map[string]string{
				"fields.slice[1]": "fields.slice[1]",
			},
		},
	}

	ur := NewUnstructuredResolver(klog.NewKlogr())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ur.Resolve(tt.query, unstructuredObjectMap); !cmp.Equal(got, tt.want) {
				t.Errorf("%s", cmp.Diff(got, tt.want))
			}
		})
	}
}
