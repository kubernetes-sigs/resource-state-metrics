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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

// TestCELResolver_Timeout verifies that a query exceeding the configured
// timeout falls back to defaultMapping and does not hang or panic.
func TestCELResolver_Timeout(t *testing.T) {
	t.Parallel()

	// An extremely short timeout should force the timer branch in Resolve
	// to fire before resolveWithTimeout can complete, regardless of how
	// simple the query is.
	cr := NewCELResolver(klog.NewKlogr(), 10e5, 1*time.Nanosecond, nil, "test-ns", "test-rmm", "test-family")

	query := "o.fields.string"
	obj := map[string]interface{}{
		"fields": map[string]interface{}{
			"string": "bar",
		},
	}

	got := cr.Resolve(query, obj)
	want := map[string]string{query: query}

	if got[query] != want[query] {
		t.Errorf("expected fallback to default mapping on timeout, got %v, want %v", got, want)
	}
}

// TestCELResolver_CostLimitExceeded verifies that a query exceeding the
// configured cost limit falls back to defaultMapping instead of erroring out
// or panicking.
func TestCELResolver_CostLimitExceeded(t *testing.T) {
	t.Parallel()

	// A cost limit of 0 should be exceeded by essentially any expression,
	// including a plain field access.
	cr := NewCELResolver(klog.NewKlogr(), 0, 5*time.Second, nil, "test-ns", "test-rmm", "test-family")

	query := "o.fields.string"
	obj := map[string]interface{}{
		"fields": map[string]interface{}{
			"string": "bar",
		},
	}

	got := cr.Resolve(query, obj)
	want := map[string]string{query: query}

	if got[query] != want[query] {
		t.Errorf("expected fallback to default mapping on cost limit exceeded, got %v, want %v", got, want)
	}
}

// TestCELResolver_MalformedSyntax verifies that queries with genuinely
// broken CEL syntax (as opposed to valid syntax with a missing field) are
// caught by env.Parse and fall back to defaultMapping.
func TestCELResolver_MalformedSyntax(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
	}{
		{
			name:  "double dot",
			query: "o.fields..bar",
		},
		{
			name:  "unbalanced parens",
			query: "o.fields.map(x, x.bar",
		},
		{
			name:  "trailing operator",
			query: "o.fields.bar +",
		},
		{
			name:  "empty query",
			query: "",
		},
	}

	cr := NewCELResolver(klog.NewKlogr(), 10e5, 5*time.Second, nil, "test-ns", "test-rmm", "test-family")
	obj := map[string]interface{}{
		"fields": map[string]interface{}{
			"bar": "baz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := cr.Resolve(tt.query, obj)
			want := map[string]string{tt.query: tt.query}

			if got[tt.query] != want[tt.query] {
				t.Errorf("expected fallback to default mapping for malformed query %q, got %v", tt.query, got)
			}
		})
	}
}

// TestCELResolver_UnsupportedOutputType verifies that CEL result types not
// explicitly handled in processResult (e.g. Duration) fall back to
// defaultMapping rather than panicking or silently dropping data.
func TestCELResolver_UnsupportedOutputType(t *testing.T) {
	t.Parallel()

	cr := NewCELResolver(klog.NewKlogr(), 10e5, 5*time.Second, nil, "test-ns", "test-rmm", "test-family")

	query := `duration("1h")`
	obj := map[string]interface{}{}

	got := cr.Resolve(query, obj)
	want := map[string]string{query: query}

	if got[query] != want[query] {
		t.Errorf("expected fallback to default mapping for unsupported output type, got %v, want %v", got, want)
	}
}

// TestCELResolver_ExpressionEvaluationMetric verifies that the success,
// error, and timeout paths in Resolve correctly increment the provided
// Prometheus counter, since existing tests all pass nil for this metric.
func TestCELResolver_ExpressionEvaluationMetric(t *testing.T) {
	t.Parallel()

	metric := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "test_cel_expression_evaluations_total",
		Help: "test metric",
	}, []string{"namespace", "name", "family", "status"})

	obj := map[string]interface{}{
		"fields": map[string]interface{}{
			"string": "bar",
		},
	}

	t.Run("success increments success counter", func(t *testing.T) {
		t.Parallel()

		resolver := NewCELResolver(klog.NewKlogr(), 10e5, 5*time.Second, metric, "ns-success", "rmm-success", "family-success")
		resolver.Resolve("o.fields.string", obj)

		got := testutil.ToFloat64(metric.WithLabelValues("ns-success", "rmm-success", "family-success", "success"))
		if got != 1 {
			t.Errorf("expected success counter to be 1, got %v", got)
		}
	})

	t.Run("parse error increments error counter", func(t *testing.T) {
		t.Parallel()

		resolver := NewCELResolver(klog.NewKlogr(), 10e5, 5*time.Second, metric, "ns-error", "rmm-error", "family-error")
		resolver.Resolve("o.fields..bar", obj)

		got := testutil.ToFloat64(metric.WithLabelValues("ns-error", "rmm-error", "family-error", "error"))
		if got != 1 {
			t.Errorf("expected error counter to be 1, got %v", got)
		}
	})

	t.Run("timeout increments timeout counter", func(t *testing.T) {
		t.Parallel()

		resolver := NewCELResolver(klog.NewKlogr(), 10e5, 1*time.Nanosecond, metric, "ns-timeout", "rmm-timeout", "family-timeout")
		resolver.Resolve("o.fields.string", obj)

		got := testutil.ToFloat64(metric.WithLabelValues("ns-timeout", "rmm-timeout", "family-timeout", "timeout"))
		if got != 1 {
			t.Errorf("expected timeout counter to be 1, got %v", got)
		}
	})
}

// TestCELResolver_NestedComposites verifies resolution of deeper composite
// structures than the single-level array/slice/map cases already covered:
// a list of maps, a map of lists, and a .map()/.filter() chain.
func TestCELResolver_NestedComposites(t *testing.T) {
	t.Parallel()

	resolver := NewCELResolver(klog.NewKlogr(), 10e5, 5*time.Second, nil, "test-ns", "test-rmm", "test-family")

	t.Run("list of maps", func(t *testing.T) {
		t.Parallel()

		obj := map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{"type": "Ready", "status": "True"},
				map[string]interface{}{"type": "Available", "status": "False"},
			},
		}

		got := resolver.Resolve("o.conditions", obj)

		if got["type"] == "" && got["status"] == "" {
			t.Errorf("expected resolved map keys from nested list-of-maps, got %v", got)
		}
	})

	t.Run("map of lists", func(t *testing.T) {
		t.Parallel()

		obj := map[string]interface{}{
			"groups": map[string]interface{}{
				"admins": []interface{}{"alice", "bob"},
			},
		}

		got := resolver.Resolve("o.groups", obj)

		found := false

		for k, v := range got {
			if v == "alice" || v == "bob" {
				found = true

				_ = k
			}
		}

		if !found {
			t.Errorf("expected resolved values from nested map-of-lists, got %v", got)
		}
	})

	t.Run("map chain over list", func(t *testing.T) {
		t.Parallel()

		obj := map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{"type": "Ready"},
				map[string]interface{}{"type": "Available"},
			},
		}

		got := resolver.Resolve("o.conditions.map(c, c.type)", obj)

		found := false

		for _, v := range got {
			if v == "Ready" || v == "Available" {
				found = true
			}
		}

		if !found {
			t.Errorf("expected resolved values from .map() chain, got %v", got)
		}
	})

	t.Run("filter then map chain", func(t *testing.T) {
		t.Parallel()

		obj := map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{"type": "Ready", "status": "True"},
				map[string]interface{}{"type": "Available", "status": "False"},
			},
		}

		got := resolver.Resolve(`o.conditions.filter(c, c.status == "True").map(c, c.type)`, obj)

		found := false

		for _, v := range got {
			if v == "Ready" {
				found = true
			}

			if v == "Available" {
				t.Errorf("filter should have excluded Available condition, got %v", got)
			}
		}

		if !found {
			t.Errorf("expected Ready condition to survive filter+map chain, got %v", got)
		}
	})
}
