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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
