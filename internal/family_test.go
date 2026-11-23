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
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/resolver"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"
)

func TestFamilyType_rawFrom(t *testing.T) {
	t.Parallel()

	unstructuredWrapper := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]interface{}{
				"name":      "test-pod",
				"namespace": "test-namespace",
			},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Ready",
						"status": "False",
					},
					map[string]interface{}{
						"type":   "ContainersReady",
						"status": "False",
					},
					map[string]interface{}{
						"status": "True",
						"type":   "PodReadyToStartContainers",
					},
					map[string]interface{}{
						"status": "False",
						"type":   "PodScheduled",
					},
					map[string]interface{}{
						"status": "True",
						"type":   "Initialized",
					},
				},
			},
		},
	}
	tests := []struct {
		name     string
		family   *FamilyType
		expected string
	}{
		{
			name:     "empty family",
			family:   &FamilyType{},
			expected: ``,
		},
		{
			// name and namespace labels are auto-injected
			name: "non-empty family with CEL resolver",
			family: &FamilyType{
				Family: v1alpha1.Family{
					Name: "test_family",
					Help: "test_help",
					Metrics: []v1alpha1.Metric{
						{
							Labels:   []v1alpha1.Label{},
							Value:    "42",
							Resolver: v1alpha1.ResolverTypeCEL,
						},
					},
				},
			},
			expected: "kube_customresource_test_family{group=\"\",version=\"v1\",kind=\"Pod\",name=\"test-pod\",namespace=\"test-namespace\"} 42.000000\n",
		},
		{
			// A label whose expression fails to resolve is left out of the sample.
			name: "CEL label on a missing field",
			family: &FamilyType{
				Family: v1alpha1.Family{
					Name: "test_family",
					Help: "test_help",
					Metrics: []v1alpha1.Metric{
						{
							Labels:   []v1alpha1.Label{{Name: "missing", Value: "o.spec.doesNotExist"}},
							Value:    "42",
							Resolver: v1alpha1.ResolverTypeCEL,
						},
					},
				},
			},
			expected: "kube_customresource_test_family{group=\"\",version=\"v1\",kind=\"Pod\",name=\"test-pod\",namespace=\"test-namespace\"} 42.000000\n",
		},
		{
			// A label that resolves to an empty list emits no samples for the object.
			name: "CEL label on an empty list",
			family: &FamilyType{
				Family: v1alpha1.Family{
					Name: "test_family",
					Help: "test_help",
					Metrics: []v1alpha1.Metric{
						{
							Labels:   []v1alpha1.Label{{Name: "type", Value: "o.status.conditions.filter(c, c.type == 'DoesNotExist').map(c, c.type)"}},
							Value:    "42",
							Resolver: v1alpha1.ResolverTypeCEL,
						},
					},
				},
			},
			expected: "",
		},
		{
			// name and namespace labels are auto-injected
			name: "non-empty family with unstructured resolver",
			family: &FamilyType{
				Family: v1alpha1.Family{
					Name: "test_family",
					Help: "test_help",
					Metrics: []v1alpha1.Metric{
						{
							Labels:   []v1alpha1.Label{},
							Value:    "42",
							Resolver: v1alpha1.ResolverTypeUnstructured,
						},
					},
				},
			},
			expected: "kube_customresource_test_family{group=\"\",version=\"v1\",kind=\"Pod\",name=\"test-pod\",namespace=\"test-namespace\"} 42.000000\n",
		},
		{
			name: "non-empty family with no resolver (should error)",
			family: &FamilyType{
				Family: v1alpha1.Family{
					Name: "test_family",
					Help: "test_help",
					Metrics: []v1alpha1.Metric{
						{
							Labels:   []v1alpha1.Label{},
							Value:    "42",
							Resolver: v1alpha1.ResolverTypeNone,
						},
					},
				},
			},
			expected: "", // No resolver specified, should produce no metrics
		},
		{
			name: "extended Pod status conditions with CEL resolver",
			family: &FamilyType{
				Family: v1alpha1.Family{
					Name: "pod_status_conditions",
					Help: "Condition status for each pod instance",
					Metrics: []v1alpha1.Metric{
						{
							Labels: []v1alpha1.Label{
								{
									Name:  "type",
									Value: "o.status.conditions.map(c, c.type)",
								},
							},
							Value:    "o.status.conditions.map(c, int(c.status == 'True' ? 1 : 0))",
							Resolver: v1alpha1.ResolverTypeCEL,
						},
					},
				},
			},
			expected: strings.Join([]string{
				"kube_customresource_pod_status_conditions{type=\"ContainersReady\",group=\"\",version=\"v1\",kind=\"Pod\",name=\"test-pod\",namespace=\"test-namespace\"} 0.000000",
				"kube_customresource_pod_status_conditions{type=\"Initialized\",group=\"\",version=\"v1\",kind=\"Pod\",name=\"test-pod\",namespace=\"test-namespace\"} 1.000000",
				"kube_customresource_pod_status_conditions{type=\"PodReadyToStartContainers\",group=\"\",version=\"v1\",kind=\"Pod\",name=\"test-pod\",namespace=\"test-namespace\"} 1.000000",
				"kube_customresource_pod_status_conditions{type=\"PodScheduled\",group=\"\",version=\"v1\",kind=\"Pod\",name=\"test-pod\",namespace=\"test-namespace\"} 0.000000",
				"kube_customresource_pod_status_conditions{type=\"Ready\",group=\"\",version=\"v1\",kind=\"Pod\",name=\"test-pod\",namespace=\"test-namespace\"} 0.000000",
			}, "\n") + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			actual, sampleCount := tt.family.buildMetricString(unstructuredWrapper)
			if actual != tt.expected {
				t.Errorf("%s\n%s", actual, cmp.Diff(actual, tt.expected))
			}
			// Verify sample count is reasonable (should be at least 1 for non-empty results)
			if tt.expected != "" && sampleCount == 0 {
				t.Errorf("expected non-zero sample count for non-empty metric string")
			}
		})
	}
}

func Test_extractAndSortExpandedMetricValues(t *testing.T) {
	t.Parallel()

	logger := klog.Background()

	tests := []struct {
		name              string
		expanded          map[string][]string
		wantValues        []string
		wantExpandedAfter map[string][]string // expected state of expanded map after call
	}{
		{
			name:              "empty map returns nil",
			expanded:          map[string][]string{},
			wantValues:        nil,
			wantExpandedAfter: map[string][]string{},
		},
		{
			name: "sentinel only, no labels",
			expanded: map[string][]string{
				expandedValueSentinel: {"10", "20", "30"},
			},
			wantValues:        []string{"10", "20", "30"},
			wantExpandedAfter: map[string][]string{},
		},
		{
			name: "labels only, no sentinel",
			expanded: map[string][]string{
				"type": {"Ready", "Initialized"},
			},
			wantValues: nil,
			wantExpandedAfter: map[string][]string{
				"type": {"Ready", "Initialized"},
			},
		},
		{
			name: "co-sorts labels and values by anchor key",
			expanded: map[string][]string{
				expandedValueSentinel: {"1", "0"},
				"type":                {"Ready", "Initialized"},
			},
			wantValues: []string{"0", "1"},
			wantExpandedAfter: map[string][]string{
				"type": {"Initialized", "Ready"},
			},
		},
		{
			name: "multiple label keys co-sorted together",
			expanded: map[string][]string{
				expandedValueSentinel: {"100", "200", "300"},
				"node":                {"compute-2", "control-plane-1", "compute-1"},
				"zone":                {"us-east", "eu-west", "ap-south"},
			},
			wantValues: []string{"300", "100", "200"},
			wantExpandedAfter: map[string][]string{
				"node": {"compute-1", "compute-2", "control-plane-1"},
				"zone": {"ap-south", "us-east", "eu-west"},
			},
		},
		{
			name: "anchor key is the lexicographically smallest label key",
			expanded: map[string][]string{
				expandedValueSentinel: {"500", "100", "300"},
				// "app" < "node" < "zone" lexicographically, so "app" is the anchor.
				// Sorting by "app" values: ["beta", "alpha", "gamma"] -> indices [1,0,2]
				// produces: app=["alpha","beta","gamma"], node=["n2","n1","n3"], zone=["z2","z1","z3"], values=["100","500","300"]
				"zone": {"z1", "z2", "z3"},
				"node": {"n1", "n2", "n3"},
				"app":  {"beta", "alpha", "gamma"},
			},
			wantValues: []string{"100", "500", "300"},
			wantExpandedAfter: map[string][]string{
				"app":  {"alpha", "beta", "gamma"},
				"node": {"n2", "n1", "n3"},
				"zone": {"z2", "z1", "z3"},
			},
		},
		{
			name: "mismatched value and label counts logs warning",
			expanded: map[string][]string{
				expandedValueSentinel: {"1", "2"},
				"type":                {"Ready", "Initialized", "PodScheduled"},
			},
			// Values are NOT appended to parallel slices due to length mismatch
			wantValues: []string{"1", "2"},
			wantExpandedAfter: map[string][]string{
				"type": {"Ready", "Initialized", "PodScheduled"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := extractAndSortExpandedMetricValues(tt.expanded, logger)

			if diff := cmp.Diff(tt.wantValues, got); diff != "" {
				t.Errorf("returned values mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tt.wantExpandedAfter, tt.expanded); diff != "" {
				t.Errorf("expanded map state mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// fakeExpression returns a fixed result for every object.
type fakeExpression struct {
	value resolver.Value
	found bool
	err   error
}

func (fe fakeExpression) Eval(_ context.Context, _ map[string]any) (resolver.Value, bool, error) {
	return fe.value, fe.found, fe.err
}

func scalarValue(s string) resolver.Value {
	return resolver.Value{Kind: resolver.KindScalar, Scalar: s}
}

func listValue(items ...resolver.Value) resolver.Value {
	return resolver.Value{Kind: resolver.KindList, List: append([]resolver.Value{}, items...)}
}

func mapValue(entries map[string]resolver.Value) resolver.Value {
	return resolver.Value{Kind: resolver.KindMap, Map: entries}
}

func Test_labelSet_addValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		label        string
		value        resolver.Value
		wantKeys     []string
		wantValues   []string
		wantExpanded map[string][]string
	}{
		{
			name:       "scalar is one label",
			label:      "app",
			value:      scalarValue("web"),
			wantKeys:   []string{"app"},
			wantValues: []string{"web"},
		},
		{
			name:         "list expands under the label name",
			label:        "tag",
			value:        listValue(scalarValue("a"), scalarValue("b")),
			wantExpanded: map[string][]string{"tag": {"a", "b"}},
		},
		{
			name:         "empty list keeps the key with no values",
			label:        "tag",
			value:        listValue(),
			wantExpanded: map[string][]string{"tag": nil},
		},
		{
			name:       "underscore map uses the entries as labels",
			label:      "_",
			value:      mapValue(map[string]resolver.Value{"foo": scalarValue("1"), "bar": scalarValue("2")}),
			wantKeys:   []string{"bar", "foo"},
			wantValues: []string{"2", "1"},
		},
		{
			name:       "named map prefixes the entries with the label name",
			label:      "meta",
			value:      mapValue(map[string]resolver.Value{"foo": scalarValue("1")}),
			wantKeys:   []string{"metafoo"},
			wantValues: []string{"1"},
		},
		{
			name:         "empty map keeps the key with no values",
			label:        "_",
			value:        mapValue(map[string]resolver.Value{}),
			wantExpanded: map[string][]string{"_": nil},
		},
		{
			name:  "nested map is flattened and a nested list expands under the label name",
			label: "_",
			value: mapValue(map[string]resolver.Value{
				"plain": scalarValue("x"),
				"inner": mapValue(map[string]resolver.Value{"k": scalarValue("v")}),
				"items": listValue(scalarValue("1"), scalarValue("2")),
			}),
			wantKeys:     []string{"k", "plain"},
			wantValues:   []string{"v", "x"},
			wantExpanded: map[string][]string{"_": {"1", "2"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			set := labelSet{expanded: map[string][]string{}}
			set.addValue(test.label, test.value)
			sortLabels(set.keys, set.values)

			if diff := cmp.Diff(test.wantKeys, set.keys, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("keys mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(test.wantValues, set.values, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("values mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(test.wantExpanded, set.expanded, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("expanded mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFamilyType_evalValue(t *testing.T) {
	t.Parallel()

	expanding := resolver.ExpressionTraits{Lists: true, Maps: true}
	tests := []struct {
		name         string
		expr         fakeExpression
		traits       resolver.ExpressionTraits
		wantValue    string
		wantExpanded []string
		wantOK       bool
		wantErr      bool
	}{
		{name: "scalar", expr: fakeExpression{value: scalarValue("4"), found: true}, traits: expanding, wantValue: "4", wantOK: true},
		{name: "list expands", expr: fakeExpression{value: listValue(scalarValue("1"), scalarValue("2")), found: true}, traits: expanding, wantExpanded: []string{"1", "2"}, wantOK: true},
		{name: "empty list has no sample", expr: fakeExpression{value: listValue(), found: true}, traits: expanding},
		{name: "missing field has no sample", expr: fakeExpression{}, traits: expanding},
		{name: "evaluation error", expr: fakeExpression{err: errors.New("boom")}, traits: expanding, wantErr: true},
		{name: "map is an error", expr: fakeExpression{value: mapValue(map[string]resolver.Value{"a": scalarValue("1")}), found: true}, traits: expanding, wantErr: true},
		{name: "undeclared list is an error", expr: fakeExpression{value: listValue(scalarValue("1")), found: true}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			family := &FamilyType{}
			metric := compiledMetric{resolver: v1alpha1.ResolverTypeCEL, traits: test.traits, value: test.expr}

			value, expanded, ok, err := family.evalValue(t.Context(), metric, nil, klog.NewKlogr())
			if (err != nil) != test.wantErr {
				t.Fatalf("evalValue() error = %v, wantErr %v", err, test.wantErr)
			}

			if value != test.wantValue || ok != test.wantOK {
				t.Errorf("evalValue() = %q, %v, want %q, %v", value, ok, test.wantValue, test.wantOK)
			}

			if diff := cmp.Diff(test.wantExpanded, expanded, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("expanded mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFamilyType_compile(t *testing.T) {
	t.Parallel()

	family := &FamilyType{Family: v1alpha1.Family{
		Name:     "bars",
		Resolver: v1alpha1.ResolverTypeCEL,
		Metrics: []v1alpha1.Metric{
			{Value: "o.spec.replicas +"},
			{Value: "o.spec.replicas", Labels: []v1alpha1.Label{{Name: "app", Value: "o.spec.appId"}}},
		},
	}}
	family.compile()

	if len(family.compiledMetrics) != 1 || len(family.compiledMetrics[0].labels) != 1 {
		t.Errorf("compile() kept %d metrics, want 1 with 1 label", len(family.compiledMetrics))
	}

	script := &FamilyType{
		Family:           v1alpha1.Family{Name: "bars", Resolver: v1alpha1.ResolverTypeStarlark, Starlark: &v1alpha1.StarlarkConfig{Script: "families = ["}},
		starlarkResolver: resolver.NewStarlarkResolver(klog.NewKlogr(), "", time.Second, 1000),
	}
	script.compile()

	if script.script != nil {
		t.Error("compile() kept a script that does not parse")
	}
}

// A CEL expression that fails on an object, e.g. because a field is missing, must skip the metric silently instead of
// returning the text of the expression as the value.
func TestFamilyType_evalValueSkipsFailedCELExpression(t *testing.T) {
	t.Parallel()

	family := &FamilyType{Family: v1alpha1.Family{Name: "bars", Resolver: v1alpha1.ResolverTypeCEL}}

	metric, err := family.compileMetric(&v1alpha1.Metric{Value: "o.status.conditions.exists(c, c.type == 'Ready' && c.status == 'True') ? 1 : 0"})
	if err != nil {
		t.Fatalf("compileMetric unexpected error: %v", err)
	}

	objWithoutConditions := map[string]any{"metadata": map[string]any{"name": "foo"}}

	value, expanded, ok, err := family.evalValue(t.Context(), metric, objWithoutConditions, klog.NewKlogr())
	if err != nil || ok || value != "" || expanded != nil {
		t.Errorf("evalValue = (%q, %v, %v, %v), want to skip without a value or an error", value, expanded, ok, err)
	}
}

func TestFamilyType_countEvaluation(t *testing.T) {
	t.Parallel()

	labels := []string{"namespace", "name", "family", "result"}
	cel := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "cel_total", Help: "test"}, labels)
	all := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "resolver_total", Help: "test"}, append(labels[:3:3], "resolver", "result"))
	family := &FamilyType{Family: v1alpha1.Family{Name: "bars"}, celEvaluations: cel, resolverEvaluations: all, managedRMMNamespace: "ns", managedRMMName: "rmm"}

	family.countEvaluation(v1alpha1.ResolverTypeCEL, nil)
	family.countEvaluation(v1alpha1.ResolverTypeCEL, context.DeadlineExceeded)
	family.countEvaluation(v1alpha1.ResolverTypeStarlark, errors.New("boom"))
	family.countEvaluation(v1alpha1.ResolverTypeUnstructured, nil)

	checks := map[string]float64{
		"cel success":          testutil.ToFloat64(all.WithLabelValues("ns", "rmm", "bars", "cel", "success")),
		"cel timeout":          testutil.ToFloat64(all.WithLabelValues("ns", "rmm", "bars", "cel", "timeout")),
		"starlark error":       testutil.ToFloat64(all.WithLabelValues("ns", "rmm", "bars", "starlark", "error")),
		"unstructured success": testutil.ToFloat64(all.WithLabelValues("ns", "rmm", "bars", "unstructured", "success")),
		"old cel success":      testutil.ToFloat64(cel.WithLabelValues("ns", "rmm", "bars", "success")),
		"old cel timeout":      testutil.ToFloat64(cel.WithLabelValues("ns", "rmm", "bars", "timeout")),
		"old cel error":        testutil.ToFloat64(cel.WithLabelValues("ns", "rmm", "bars", "error")),
	}

	want := map[string]float64{"cel success": 1, "cel timeout": 1, "starlark error": 1, "unstructured success": 1, "old cel success": 1, "old cel timeout": 1, "old cel error": 0}
	if diff := cmp.Diff(want, checks); diff != "" {
		t.Errorf("counters mismatch (-want +got):\n%s", diff)
	}
}
