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
	"testing"

	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery/fake"
	fakeclientset "k8s.io/client-go/kubernetes/fake"
	"k8s.io/klog/v2"
)

func TestIsWildcard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pattern  string
		expected bool
	}{
		{"empty string", "", false},
		{"exact match", "pods", false},
		{"single wildcard", "*", true},
		{"prefix wildcard", "pod*", true},
		{"suffix wildcard", "*pods", true},
		{"middle wildcard", "po*ds", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := IsWildcard(tt.pattern); got != tt.expected {
				t.Errorf("IsWildcard(%q) = %v, want %v", tt.pattern, got, tt.expected)
			}
		})
	}
}

func TestHasWildcards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		store    *v1alpha1.Store
		expected bool
	}{
		{
			name: "no wildcards",
			store: &v1alpha1.Store{
				Group:    "apps",
				Version:  "v1",
				Kind:     "Deployment",
				Resource: "deployments",
			},
			expected: false,
		},
		{
			name: "wildcard in kind",
			store: &v1alpha1.Store{
				Group:    "apps",
				Version:  "v1",
				Kind:     "*",
				Resource: "deployments",
			},
			expected: true,
		},
		{
			name: "wildcard in group",
			store: &v1alpha1.Store{
				Group:    "*",
				Version:  "v1",
				Kind:     "Deployment",
				Resource: "deployments",
			},
			expected: true,
		},
		{
			name: "wildcard in version",
			store: &v1alpha1.Store{
				Group:    "apps",
				Version:  "*",
				Kind:     "Deployment",
				Resource: "deployments",
			},
			expected: true,
		},
		{
			name: "wildcard in resource",
			store: &v1alpha1.Store{
				Group:    "apps",
				Version:  "v1",
				Kind:     "Deployment",
				Resource: "*",
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := HasWildcards(tt.store); got != tt.expected {
				t.Errorf("HasWildcards() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestMatchesPattern(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pattern  string
		value    string
		expected bool
	}{
		{"empty pattern matches empty", "", "", true},
		{"empty pattern does not match non-empty", "", "anything", false},
		{"wildcard matches all", "*", "anything", true},
		{"exact match", "pods", "pods", true},
		{"exact no match", "pods", "deployments", false},
		{"prefix wildcard", "deploy*", "deployments", true},
		{"prefix wildcard no match", "deploy*", "pods", false},
		{"suffix wildcard", "*ments", "deployments", true},
		{"suffix wildcard no match", "*ments", "pods", false},
		{"middle wildcard", "de*ts", "deployments", true},
		{"middle wildcard no match", "de*ts", "pods", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := matchesPattern(tt.pattern, tt.value); got != tt.expected {
				t.Errorf("matchesPattern(%q, %q) = %v, want %v", tt.pattern, tt.value, got, tt.expected)
			}
		})
	}
}

func TestExpandWildcards(t *testing.T) {
	t.Parallel()

	logger := klog.Background()

	// Create a fake discovery client with some API resources
	fakeClient := fakeclientset.NewClientset()

	fakeDiscovery, ok := fakeClient.Discovery().(*fake.FakeDiscovery)
	if !ok {
		t.Fatal("failed to cast discovery client")
	}

	// Add some fake API resources
	fakeDiscovery.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "apps/v1",
			APIResources: []metav1.APIResource{
				{Name: "deployments", Kind: "Deployment", Namespaced: true},
				{Name: "replicasets", Kind: "ReplicaSet", Namespaced: true},
				{Name: "statefulsets", Kind: "StatefulSet", Namespaced: true},
				{Name: "deployments/status", Kind: "Deployment", Namespaced: true}, // subresource
			},
		},
		{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "pods", Kind: "Pod", Namespaced: true},
				{Name: "services", Kind: "Service", Namespaced: true},
			},
		},
		{
			GroupVersion: "batch/v1",
			APIResources: []metav1.APIResource{
				{Name: "jobs", Kind: "Job", Namespaced: true},
				{Name: "cronjobs", Kind: "CronJob", Namespaced: true},
			},
		},
		{
			// Create-only: not listable/watchable.
			GroupVersion: "authentication.k8s.io/v1",
			APIResources: []metav1.APIResource{
				{Name: "tokenreviews", Kind: "TokenReview", Namespaced: false, Verbs: []string{"create"}},
			},
		},
		{
			GroupVersion: "authorization.k8s.io/v1",
			APIResources: []metav1.APIResource{
				{Name: "subjectaccessreviews", Kind: "SubjectAccessReview", Namespaced: false, Verbs: []string{"create"}},
				{Name: "selfsubjectaccessreviews", Kind: "SelfSubjectAccessReview", Namespaced: false, Verbs: []string{"create"}},
				{Name: "localsubjectaccessreviews", Kind: "LocalSubjectAccessReview", Namespaced: true, Verbs: []string{"create"}},
				{Name: "selfsubjectrulesreviews", Kind: "SelfSubjectRulesReview", Namespaced: false, Verbs: []string{"create"}},
			},
		},
	}

	discovery := NewResourceDiscovery(fakeDiscovery, logger)

	tests := []struct {
		name          string
		store         *v1alpha1.Store
		expectedCount int
		checkFirst    func(*v1alpha1.Store) bool
	}{
		{
			name: "no wildcards returns single store",
			store: &v1alpha1.Store{
				Group:    "apps",
				Version:  "v1",
				Kind:     "Deployment",
				Resource: "deployments",
			},
			expectedCount: 1,
			checkFirst: func(s *v1alpha1.Store) bool {
				return s.Kind == "Deployment" && s.Resource == "deployments"
			},
		},
		{
			name: "wildcard kind in apps group",
			store: &v1alpha1.Store{
				Group:    "apps",
				Version:  "v1",
				Kind:     "*",
				Resource: "*",
			},
			expectedCount: 3, // Deployment, ReplicaSet, StatefulSet (subresources filtered)
		},
		{
			name: "wildcard kind in core group",
			store: &v1alpha1.Store{
				Group:    "",
				Version:  "v1",
				Kind:     "*",
				Resource: "*",
			},
			expectedCount: 2, // Pod, Service
		},
		{
			name: "wildcard group with specific kind pattern",
			store: &v1alpha1.Store{
				Group:    "*",
				Version:  "v1",
				Kind:     "Deployment",
				Resource: "deployments",
			},
			expectedCount: 1, // Only apps/v1/Deployment
		},
		{
			name: "prefix wildcard for kind",
			store: &v1alpha1.Store{
				Group:    "apps",
				Version:  "v1",
				Kind:     "Re*",
				Resource: "*",
			},
			expectedCount: 1, // ReplicaSet
		},
		{
			name: "wildcard sweep skips create-only resource in one group",
			store: &v1alpha1.Store{
				Group:    "authentication.k8s.io",
				Version:  "v1",
				Kind:     "*",
				Resource: "*",
			},
			expectedCount: 0, // tokenreviews is create-only, not list+watch
		},
		{
			name: "wildcard sweep skips all create-only resources in a group",
			store: &v1alpha1.Store{
				Group:    "authorization.k8s.io",
				Version:  "v1",
				Kind:     "*",
				Resource: "*",
			},
			expectedCount: 0, // all authorization.k8s.io review resources are create-only
		},
		{
			name: "explicitly named create-only resource is honoured",
			store: &v1alpha1.Store{
				Group:    "*", // wildcard group, but Kind/Resource named exactly
				Version:  "v1",
				Kind:     "TokenReview",
				Resource: "tokenreviews",
			},
			expectedCount: 1, // named explicitly, not verb-filtered
		},
		{
			name: "empty verbs are treated as unknown and not filtered",
			store: &v1alpha1.Store{
				Group:    "apps",
				Version:  "v1",
				Kind:     "*",
				Resource: "*",
			},
			expectedCount: 3, // apps resources carry no Verbs in fixtures; still expand
		},
		{
			name: "no wildcards returns store unchanged",
			store: &v1alpha1.Store{
				Group:    "apps",
				Version:  "v1",
				Kind:     "Deployment",
				Resource: "deployments",
			},
			expectedCount: 1,
			checkFirst: func(s *v1alpha1.Store) bool {
				return s.Kind == "Deployment" && s.Resource == "deployments"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expanded, err := discovery.ExpandWildcards(tt.store)
			if err != nil {
				t.Fatalf("ExpandWildcards() error = %v", err)
			}

			if len(expanded) != tt.expectedCount {
				t.Errorf("ExpandWildcards() returned %d stores, want %d", len(expanded), tt.expectedCount)

				for i, s := range expanded {
					t.Logf("  [%d] %s/%s/%s/%s", i, s.Group, s.Version, s.Kind, s.Resource)
				}
			}

			if tt.checkFirst != nil && len(expanded) > 0 {
				if !tt.checkFirst(&expanded[0]) {
					t.Errorf("First expanded store doesn't match expected: %+v", expanded[0])
				}
			}
		})
	}
}

func TestStoreKey(t *testing.T) {
	t.Parallel()

	store := &v1alpha1.Store{
		Group:   "apps",
		Version: "v1",
		Kind:    "Deployment",
	}

	key := storeKey(store)
	expected := "apps/v1/Deployment"

	if key != expected {
		t.Errorf("storeKey() = %q, want %q", key, expected)
	}
}
