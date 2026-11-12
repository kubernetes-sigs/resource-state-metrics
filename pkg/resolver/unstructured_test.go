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
	"context"
	"errors"
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

func TestUnstructuredResolver_Compile(t *testing.T) {
	t.Parallel()

	resolver := NewUnstructuredResolver(klog.NewKlogr())

	for _, expr := range []string{"", "fields..string", ".fields"} {
		if _, err := resolver.Compile(expr); !errors.Is(err, ErrInvalidExpression) {
			t.Errorf("Compile(%q) error = %v, want ErrInvalidExpression", expr, err)
		}
	}

	if _, err := resolver.Compile("fields.string"); err != nil {
		t.Errorf("Compile(%q) unexpected error: %v", "fields.string", err)
	}
}

func TestUnstructuredResolver_Traits(t *testing.T) {
	t.Parallel()

	if diff := cmp.Diff(ExpressionTraits{}, NewUnstructuredResolver(klog.NewKlogr()).Traits()); diff != "" {
		t.Errorf("Traits() mismatch (-want +got):\n%s", diff)
	}
}

func TestUnstructuredResolver_Eval(t *testing.T) {
	t.Parallel()

	obj := map[string]interface{}{
		"fields": map[string]interface{}{
			"nil":    nil,
			"string": "bar",
			"slice":  []string{"a", "b", "c"},
			"map":    map[string]interface{}{"foo": "baz"},
		},
	}
	tests := []struct {
		name      string
		query     string
		want      Value
		wantFound bool
		wantErr   bool
	}{
		{name: "string", query: "fields.string", want: Value{Kind: KindScalar, Scalar: "bar"}, wantFound: true},
		{name: "nil is the <nil> text", query: "fields.nil", want: Value{Kind: KindScalar, Scalar: "<nil>"}, wantFound: true},
		{name: "slice is not expanded", query: "fields.slice", want: Value{Kind: KindScalar, Scalar: "[a b c]"}, wantFound: true},
		{name: "map is not expanded", query: "fields.map", want: Value{Kind: KindScalar, Scalar: "map[foo:baz]"}, wantFound: true},
		{name: "missing field", query: "fields.missing"},
		{name: "walking through a string", query: "fields.string.bar", wantErr: true},
	}

	resolver := NewUnstructuredResolver(klog.NewKlogr())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expr, err := resolver.Compile(tt.query)
			if err != nil {
				t.Fatalf("Compile(%q) unexpected error: %v", tt.query, err)
			}

			got, found, err := expr.Eval(t.Context(), obj)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Eval(%q) error = %v, wantErr %v", tt.query, err, tt.wantErr)
			}

			if found != tt.wantFound {
				t.Errorf("Eval(%q) found = %v, want %v", tt.query, found, tt.wantFound)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Eval(%q) mismatch (-want +got):\n%s", tt.query, diff)
			}
		})
	}
}

func TestUnstructuredResolver_EvalCancelled(t *testing.T) {
	t.Parallel()

	expr, err := NewUnstructuredResolver(klog.NewKlogr()).Compile("fields.string")
	if err != nil {
		t.Fatalf("Compile unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, _, err := expr.Eval(ctx, map[string]interface{}{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Eval error = %v, want context.Canceled", err)
	}
}
