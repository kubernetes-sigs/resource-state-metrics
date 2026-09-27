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
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/resolver"
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

func Test_collectIndexedResolvedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		resolved map[string]string
		want     []string
	}{
		{
			name:     "empty map returns nil",
			resolved: map[string]string{},
			want:     nil,
		},
		{
			name: "collects indexed values in numeric suffix order",
			resolved: map[string]string{
				"field#1": "beta",
				"field#0": "alpha",
				"field#2": "gamma",
			},
			want: []string{"alpha", "beta", "gamma"},
		},
		{
			name: "preserves empty string values",
			resolved: map[string]string{
				"field#0": "",
				"field#1": "present",
			},
			want: []string{"", "present"},
		},
		{
			name: "stops at first missing index",
			resolved: map[string]string{
				"field#0": "alpha",
				"field#2": "gamma",
			},
			want: []string{"alpha"},
		},
		{
			name: "ignores non indexed keys",
			resolved: map[string]string{
				"field":   "scalar",
				"other#0": "alpha",
				"misc":    "value",
			},
			want: []string{"alpha"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := collectIndexedResolvedValues(tt.resolved)

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("collectIndexedResolvedValues() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

type fakeResolver struct {
	result map[string]string
}

func (f fakeResolver) Resolve(_ string, _ map[string]interface{}) map[string]string {
	return f.result
}

func TestResolveMetricValue(t *testing.T) {
	t.Parallel()

	valueExpr := "value-expression"

	type want struct {
		value    string
		ok       bool
		err      bool
		expanded []string
	}

	tests := []struct {
		name     string
		resolved map[string]string
		want     want
	}{
		{
			name:     "empty map skips silently without error",
			resolved: map[string]string{},
			want:     want{value: "", ok: false, err: false},
		},
		{
			name:     "scalar match returns the value",
			resolved: map[string]string{valueExpr: "3"},
			want:     want{value: "3", ok: true, err: false},
		},
		{
			name:     "expanded list is carried under the sentinel",
			resolved: map[string]string{"conditions#0": "1", "conditions#1": "0"},
			want:     want{value: "", ok: true, err: false, expanded: []string{"1", "0"}},
		},
		{
			name:     "non-empty map with no scalar or expanded match errors",
			resolved: map[string]string{"app": "foo"},
			want:     want{value: "", ok: false, err: true},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var resolverInstance resolver.Resolver = fakeResolver{result: testCase.resolved}

			expanded := map[string][]string{}

			value, ok, err := resolveMetricValue(resolverInstance, valueExpr, nil, expanded)

			if value != testCase.want.value {
				t.Errorf("value = %q, want %q", value, testCase.want.value)
			}

			if ok != testCase.want.ok {
				t.Errorf("ok = %v, want %v", ok, testCase.want.ok)
			}

			if gotErr := err != nil; gotErr != testCase.want.err {
				t.Errorf("err = %v, want error: %v", err, testCase.want.err)
			}

			if diff := cmp.Diff(testCase.want.expanded, expanded[expandedValueSentinel]); diff != "" {
				t.Errorf("expanded values mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestEscapeHelp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text unchanged", in: "Number of replicas.", want: "Number of replicas."},
		{name: "empty string unchanged", in: "", want: ""},
		{name: "single newline escaped", in: "line1\nline2", want: `line1\nline2`},
		{name: "single backslash escaped", in: `regex \d+`, want: `regex \\d+`},
		{name: "backslash then newline: backslash escaped first", in: "a\\\nb", want: `a\\\nb`},
		{name: "multiple newlines all escaped", in: "a\nb\nc", want: `a\nb\nc`},
		{name: "tab is left as-is", in: "a\tb", want: "a\tb"},
		{name: "unicode passes through", in: "héllo → wörld", want: "héllo → wörld"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := escapeHelp(tt.in)
			if got != tt.want {
				t.Errorf("escapeHelp(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFamilyType_buildHeaders_escapesHelp(t *testing.T) {
	t.Parallel()

	f := &FamilyType{
		Family: v1alpha1.Family{
			Name: "example",
			Help: "line1\nline2 with \\ backslash",
		},
	}

	got := f.buildHeaders()
	want := "# HELP kube_customresource_example line1\\nline2 with \\\\ backslash\n# TYPE kube_customresource_example gauge"

	if got != want {
		t.Errorf("buildHeaders() mismatch:\n got: %q\nwant: %q", got, want)
	}

	if strings.Count(got, "\n") != 1 {
		t.Errorf("expected exactly one literal newline in output, got %d", strings.Count(got, "\n"))
	}
}
