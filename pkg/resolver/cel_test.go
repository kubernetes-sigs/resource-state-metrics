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
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"k8s.io/klog/v2"
)

func TestNewCELResolver_Resolve(t *testing.T) {
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
			name:  "field exists and is a string",
			query: "o.fields.string",
			want: map[string]string{
				"o.fields.string": "bar",
			},
		},
		{
			name:  "field exists and is an integer",
			query: "o.fields.integer",
			want: map[string]string{
				"o.fields.integer": "1",
			},
		},
		{
			name:  "field exists and is a float",
			query: "o.fields.float",
			want: map[string]string{
				"o.fields.float": "1.1",
			},
		},
		{
			name:  "field exists and is a rune",
			query: "o.fields.rune",
			want: map[string]string{
				"o.fields.rune": "97",
			},
		},
		{
			name:  "field exists and is a boolean",
			query: "o.fields.boolean",
			want: map[string]string{
				"o.fields.boolean": "true",
			},
		},
		{
			name:  "field exists and is an array",
			query: "o.fields.array[1]",
			want: map[string]string{
				"o.fields.array[1]": "b",
			},
		},
		{
			name:  "field exists and is a slice",
			query: "o.fields.slice[1]",
			want: map[string]string{
				"o.fields.slice[1]": "b",
			},
		},
		{
			name:  "field exists and is a map",
			query: "o.fields.map.foo.bar",
			want: map[string]string{
				"o.fields.map.foo.bar": "baz",
			},
		},
		{
			name:  "field exists and is nil",
			query: "o.fields.nil",
			want: map[string]string{
				"o.fields.nil": "<nil>",
			},
		},
		{
			name:  "error traversing obj",
			query: "o.fields.string.bar",
			want: map[string]string{
				"o.fields.string.bar": "o.fields.string.bar",
			},
		},
		{
			name:  "field does not exist",
			query: "o.fields.bar",
			want: map[string]string{
				"o.fields.bar": "o.fields.bar",
			},
		},
		{
			name:  "intermediate field does not exist",
			query: "o.fields.fake.string",
			want: map[string]string{
				"o.fields.fake.string": "o.fields.fake.string",
			},
		},
		{
			name:  "intermediate field is null", // happens easily in YAML
			query: "o.fields.nil.foo",
			want: map[string]string{
				"o.fields.nil.foo": "o.fields.nil.foo",
			},
		},
	}

	cr := NewCELResolver(klog.NewKlogr(), 10e5, 5*time.Second, nil, "test-ns", "test-rmm", "test-family")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := cr.Resolve(tt.query, unstructuredObjectMap); !cmp.Equal(got, tt.want) {
				t.Errorf("%s", cmp.Diff(got, tt.want))
			}
		})
	}
}

func TestCELResolver_UnixSeconds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		obj   map[string]any
		query string
		want  map[string]string
	}{
		{
			name:  "parse RFC3339 timestamp",
			obj:   map[string]any{"timestamp": "2024-01-15T10:30:00Z"},
			query: `unixSeconds(o.timestamp)`,
			want:  map[string]string{`unixSeconds(o.timestamp)`: "1.7053146e+09"},
		},
		{
			name:  "parse RFC3339 with timezone",
			obj:   map[string]any{"timestamp": "2024-01-15T12:30:00+02:00"},
			query: `unixSeconds(o.timestamp)`,
			want:  map[string]string{`unixSeconds(o.timestamp)`: "1.7053146e+09"},
		},
		{
			name:  "empty string returns 0",
			obj:   map[string]any{"timestamp": ""},
			query: `unixSeconds(o.timestamp)`,
			want:  map[string]string{`unixSeconds(o.timestamp)`: "0"},
		},
		{
			name:  "invalid timestamp returns error",
			obj:   map[string]any{"timestamp": "not-a-timestamp"},
			query: `unixSeconds(o.timestamp)`,
			want:  map[string]string{`unixSeconds(o.timestamp)`: `unixSeconds(o.timestamp)`},
		},
	}

	cr := NewCELResolver(klog.NewKlogr(), 10e5, 5*time.Second, nil, "test-ns", "test-rmm", "test-family")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := cr.Resolve(tt.query, tt.obj); !cmp.Equal(got, tt.want) {
				t.Errorf("%s", cmp.Diff(got, tt.want))
			}
		})
	}
}

func TestCELResolver_Quantity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		obj   map[string]any
		query string
		want  map[string]string
	}{
		{
			name:  "parse millicores",
			obj:   map[string]any{"cpu": "100m"},
			query: `quantity(o.cpu)`,
			want:  map[string]string{`quantity(o.cpu)`: "0.1"},
		},
		{
			name:  "parse cores",
			obj:   map[string]any{"cpu": "2"},
			query: `quantity(o.cpu)`,
			want:  map[string]string{`quantity(o.cpu)`: "2"},
		},
		{
			name:  "parse memory Ki",
			obj:   map[string]any{"memory": "128Ki"},
			query: `quantity(o.memory)`,
			want:  map[string]string{`quantity(o.memory)`: "131072"},
		},
		{
			name:  "parse memory Gi",
			obj:   map[string]any{"memory": "1Gi"},
			query: `quantity(o.memory)`,
			want:  map[string]string{`quantity(o.memory)`: "1.073741824e+09"},
		},
		{
			name:  "empty string returns 0",
			obj:   map[string]any{"cpu": ""},
			query: `quantity(o.cpu)`,
			want:  map[string]string{`quantity(o.cpu)`: "0"},
		},
		{
			name:  "invalid quantity returns error",
			obj:   map[string]any{"cpu": "not-a-quantity"},
			query: `quantity(o.cpu)`,
			want:  map[string]string{`quantity(o.cpu)`: `quantity(o.cpu)`},
		},
	}

	cr := NewCELResolver(klog.NewKlogr(), 10e5, 5*time.Second, nil, "test-ns", "test-rmm", "test-family")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := cr.Resolve(tt.query, tt.obj); !cmp.Equal(got, tt.want) {
				t.Errorf("%s", cmp.Diff(got, tt.want))
			}
		})
	}
}

func TestCELResolver_Now(t *testing.T) {
	t.Parallel()

	resolver := NewCELResolver(klog.NewKlogr(), 10e5, 5*time.Second, nil, "test-ns", "test-rmm", "test-family")

	t.Run("returns current unix seconds", func(t *testing.T) {
		t.Parallel()

		before := time.Now().Unix()
		got := resolver.Resolve(`now()`, map[string]any{})
		after := time.Now().Unix()

		if len(got) != 1 {
			t.Fatalf("expected 1 result, got %d", len(got))
		}

		for _, v := range got {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				t.Fatalf("expected numeric result, got %q: %v", v, err)
			}

			// Allow a 5-second slop around the call window — pinning to a
			// calendar date ages badly under clock skew or backdated runners.
			const delta = 5.0
			if f < float64(before)-delta || f > float64(after)+delta {
				t.Errorf("now() = %f, expected within [%d-%g, %d+%g]", f, before, delta, after, delta)
			}
		}
	})

	t.Run("compute duration since transition", func(t *testing.T) {
		t.Parallel()

		got := resolver.Resolve(`now() - unixSeconds(o.timestamp)`, map[string]any{"timestamp": "2024-01-15T10:30:00Z"})
		if len(got) != 1 {
			t.Fatalf("expected 1 result, got %d", len(got))
		}

		for _, v := range got {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				t.Fatalf("expected numeric result, got %q: %v", v, err)
			}

			// Duration since 2024-01-15 should be positive
			if f <= 0 {
				t.Errorf("expected positive duration, got %f", f)
			}
		}
	})
}

func TestCELResolver_LabelPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		obj   map[string]any
		query string
		want  map[string]string
	}{
		{
			name:  "prefix simple labels",
			obj:   map[string]any{"labels": map[string]any{"app": "test", "env": "prod"}},
			query: `labelPrefix(o.labels, "label_")`,
			want:  map[string]string{"label_app": "test", "label_env": "prod"},
		},
		{
			name:  "sanitize special characters",
			obj:   map[string]any{"labels": map[string]any{"app.kubernetes.io/name": "myapp", "env/type": "prod"}},
			query: `labelPrefix(o.labels, "label_")`,
			want:  map[string]string{"label_app_kubernetes_io_name": "myapp", "label_env_type": "prod"},
		},
		{
			name:  "empty map",
			obj:   map[string]any{"labels": map[string]any{}},
			query: `labelPrefix(o.labels, "label_")`,
			want:  map[string]string{},
		},
	}

	cr := NewCELResolver(klog.NewKlogr(), 10e5, 5*time.Second, nil, "test-ns", "test-rmm", "test-family")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := cr.Resolve(tt.query, tt.obj); !cmp.Equal(got, tt.want) {
				t.Errorf("%s", cmp.Diff(got, tt.want))
			}
		})
	}
}
