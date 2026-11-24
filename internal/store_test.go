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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
)

// makeUnstructured is a helper that builds a minimal Unstructured object.
func makeUnstructured(uid, name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
			"uid":       uid,
		},
	}}
}

// TestStore_ResourceCutoffEnforcement verifies that setting SetResourceCutoff(true)
// suppresses metric output for all families in the store, and setting SetResourceCutoff(false)
// restores metric output.
func TestStore_ResourceCutoffEnforcement(t *testing.T) {
	t.Parallel()

	fam := &FamilyType{
		Family: v1alpha1.Family{
			Name:     "resource_cutoff_family",
			Help:     "Test metric help",
			Resolver: v1alpha1.ResolverTypeUnstructured,
			Metrics: []v1alpha1.Metric{
				{Value: "1"},
			},
		},
	}

	store := newStore(
		klog.Background(),
		[]string{"# HELP resource_cutoff_family\n# TYPE resource_cutoff_family gauge"},
		[]*FamilyType{fam},
		v1alpha1.ResolverTypeUnstructured,
		nil,
		100000,
		5,
	)

	tracker := NewCardinalityTracker(1000, 0.8)
	store.SetCardinalityTracker(tracker)

	obj := makeUnstructured("uid-resource-cutoff", "pod-cutoff", "default")

	// 1. Initially resourceCutoff is false; Add generates metric output
	if err := store.Add(obj); err != nil {
		t.Fatalf("Add: %v", err)
	}

	store.mutex.RLock()
	metricsBefore := store.metrics[types.UID("uid-resource-cutoff")]
	store.mutex.RUnlock()

	if len(metricsBefore) == 0 || metricsBefore[0] == "" {
		t.Fatalf("expected non-empty metric string before cutoff, got %v", metricsBefore)
	}

	// 2. Set resourceCutoff to true and apply thresholds; metrics generated should be empty string ""
	store.SetResourceCutoff(true)
	store.checkAndApplyThresholds()

	if err := store.Add(obj); err != nil {
		t.Fatalf("Add during cutoff: %v", err)
	}

	store.mutex.RLock()
	metricsDuring := store.metrics[types.UID("uid-resource-cutoff")]
	store.mutex.RUnlock()

	if len(metricsDuring) > 0 && metricsDuring[0] != "" {
		t.Fatalf("expected empty metric string during resource cutoff, got %v", metricsDuring)
	}

	// 3. Clear resourceCutoff (false); metric output should resume
	store.SetResourceCutoff(false)
	store.checkAndApplyThresholds()

	if err := store.Add(obj); err != nil {
		t.Fatalf("Add after cutoff recovery: %v", err)
	}

	store.mutex.RLock()
	metricsAfter := store.metrics[types.UID("uid-resource-cutoff")]
	store.mutex.RUnlock()

	if len(metricsAfter) == 0 || metricsAfter[0] == "" {
		t.Fatalf("expected metric generation to resume after cutoff cleared, got %v", metricsAfter)
	}
}

// TestStoreRecoveryRestoresCachedMetrics exercises recovery for an object that
// receives no further events after its cached metrics were suppressed.
func TestStoreRecoveryRestoresCachedMetrics(t *testing.T) {
	t.Parallel()

	family := &FamilyType{
		Family: v1alpha1.Family{
			Name:     "recovery",
			Help:     "recovery",
			Resolver: v1alpha1.ResolverTypeUnstructured,
			Metrics: []v1alpha1.Metric{
				{Value: "1"},
			},
		},
	}

	store := newStore(klog.Background(), nil, []*FamilyType{family}, v1alpha1.ResolverTypeUnstructured, nil, 100000, 5)
	tracker := NewCardinalityTracker(1, 0.8)
	store.SetCardinalityTracker(tracker)

	objA := makeUnstructured("a", "a", "default")
	objB := makeUnstructured("b", "b", "default")

	for _, obj := range []*unstructured.Unstructured{objA, objB} {
		if err := store.Add(obj); err != nil {
			t.Fatal(err)
		}
	}

	if !tracker.IsFamilyCutoff(family.Name) {
		t.Fatal("expected store cutoff after adding two samples")
	}

	if err := store.Update(objA); err != nil {
		t.Fatal(err)
	}

	store.mutex.RLock()
	gotBefore := store.metrics[types.UID("a")]
	store.mutex.RUnlock()

	if len(gotBefore) != 1 || gotBefore[0] != "" {
		t.Fatalf("expected the update during cutoff to suppress a's cached metric, got %q", gotBefore)
	}

	if err := store.Delete(objB); err != nil {
		t.Fatal(err)
	}

	if tracker.IsFamilyCutoff(family.Name) {
		t.Error("family remains cut off after store cardinality recovered")
	}

	store.mutex.RLock()
	gotAfter := store.metrics[types.UID("a")]
	store.mutex.RUnlock()

	if len(gotAfter) != 1 || gotAfter[0] == "" {
		t.Errorf("surviving object's cached metric remains suppressed after recovery: %q", gotAfter)
	}
}
