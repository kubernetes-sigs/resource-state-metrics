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
package internal

import (
	"testing"

	"github.com/google/go-cmp/cmp"
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
			name: "non-empty family with CEL resolver",
			family: &FamilyType{
				Name: "test_family",
				Help: "test_help",
				Metrics: []*MetricType{
					{
						LabelKeys:   []string{"namespace", "name"},
						LabelValues: []string{"o.metadata.namespace", "o.metadata.name"},
						Value:       "42",
						Resolver:    ResolverTypeCEL,
					},
				},
			},
			expected: "kube_customresource_test_family{name=\"test-pod\",namespace=\"test-namespace\",group=\"\",version=\"v1\",kind=\"Pod\"} 42.000000\n",
		},
		{
			name: "non-empty family with unstructured resolver",
			family: &FamilyType{
				Name: "test_family",
				Help: "test_help",
				Metrics: []*MetricType{
					{
						LabelKeys:   []string{"namespace", "name"},
						LabelValues: []string{"metadata.namespace", "metadata.name"},
						Value:       "42",
						Resolver:    ResolverTypeUnstructured,
					},
				},
			},
			expected: "kube_customresource_test_family{name=\"test-pod\",namespace=\"test-namespace\",group=\"\",version=\"v1\",kind=\"Pod\"} 42.000000\n",
		},
		{
			name: "non-empty family with default (unstructured) resolver",
			family: &FamilyType{
				Name: "test_family",
				Help: "test_help",
				Metrics: []*MetricType{
					{
						LabelKeys:   []string{"namespace", "name"},
						LabelValues: []string{"metadata.namespace", "metadata.name"},
						Value:       "42",
						Resolver:    ResolverTypeNone,
					},
				},
			},
			expected: "kube_customresource_test_family{name=\"test-pod\",namespace=\"test-namespace\",group=\"\",version=\"v1\",kind=\"Pod\"} 42.000000\n",
		},
		{
			name: "non-empty family with no resolver (should default to unstructured)",
			family: &FamilyType{
				Name: "test_family",
				Help: "test_help",
				Metrics: []*MetricType{
					{
						LabelKeys:   []string{"namespace", "name"},
						LabelValues: []string{"metadata.namespace", "metadata.name"},
						Value:       "42",
					},
				},
			},
			expected: "kube_customresource_test_family{name=\"test-pod\",namespace=\"test-namespace\",group=\"\",version=\"v1\",kind=\"Pod\"} 42.000000\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			actual := tt.family.buildMetricString(unstructuredWrapper)
			if actual != tt.expected {
				t.Errorf("%s\n%s", actual, cmp.Diff(actual, tt.expected))
			}
		})
	}
}
