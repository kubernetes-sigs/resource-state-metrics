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

// newTestStore creates a StoreType with no families (metrics will be empty
// strings but the UID → metrics mapping will still be populated).
func newTestStore() *StoreType {
	return &StoreType{
		logger:  klog.Background(),
		metrics: map[types.UID][]string{},
	}
}

// TestStoreReplace_ClearsStaleEntries verifies that Replace atomically
// replaces the metrics map: objects absent from the replacement list (i.e.
// deleted from Kubernetes since the last reflector list) must not appear in
// the store after Replace returns.
func TestStoreReplace_ClearsStaleEntries(t *testing.T) {
	t.Parallel()

	store := newTestStore()

	objA := makeUnstructured("uid-a", "pod-a", "default")
	objB := makeUnstructured("uid-b", "pod-b", "default")
	objC := makeUnstructured("uid-c", "pod-c", "kube-system")

	// Populate the store with three objects via Add.
	if err := store.Add(objA); err != nil {
		t.Fatalf("Add(objA): %v", err)
	}

	if err := store.Add(objB); err != nil {
		t.Fatalf("Add(objB): %v", err)
	}

	if err := store.Add(objC); err != nil {
		t.Fatalf("Add(objC): %v", err)
	}

	if got, want := len(store.metrics), 3; got != want {
		t.Fatalf("before Replace: len(store.metrics) = %d, want %d", got, want)
	}

	// Replace with only objA and objB; objC has been deleted from Kubernetes.
	if err := store.Replace([]interface{}{objA, objB}, ""); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	store.mutex.RLock()
	defer store.mutex.RUnlock()

	if got, want := len(store.metrics), 2; got != want {
		t.Fatalf("after Replace: len(store.metrics) = %d, want %d", got, want)
	}

	if _, ok := store.metrics[types.UID("uid-c")]; ok {
		t.Error("after Replace: uid-c still present in store.metrics; stale entry was not cleared")
	}

	if _, ok := store.metrics[types.UID("uid-a")]; !ok {
		t.Error("after Replace: uid-a missing from store.metrics")
	}

	if _, ok := store.metrics[types.UID("uid-b")]; !ok {
		t.Error("after Replace: uid-b missing from store.metrics")
	}
}

// TestStoreReplace_EmptyListClearsAll verifies that Replace with an empty
// item list removes all metrics — e.g. when the watched resource type has
// been completely purged.
func TestStoreReplace_EmptyListClearsAll(t *testing.T) {
	t.Parallel()

	store := newTestStore()

	obj := makeUnstructured("uid-x", "pod-x", "default")
	if err := store.Add(obj); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := store.Replace([]interface{}{}, ""); err != nil {
		t.Fatalf("Replace(empty): %v", err)
	}

	store.mutex.RLock()
	defer store.mutex.RUnlock()

	if got := len(store.metrics); got != 0 {
		t.Fatalf("after Replace(empty): len(store.metrics) = %d, want 0", got)
	}
}

// TestStoreReplace_SyncedAfterReplace verifies that IsSynced returns true
// after a Replace call, matching the original behaviour.
func TestStoreReplace_SyncedAfterReplace(t *testing.T) {
	t.Parallel()

	store := newTestStore()

	if store.IsSynced() {
		t.Fatal("store should not be synced before Replace")
	}

	if err := store.Replace([]interface{}{}, ""); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	if !store.IsSynced() {
		t.Fatal("store should be synced after Replace")
	}
}

// TestStoreReplace_CardinalityTrackerReset verifies that the CardinalityTracker
// is reset and rebuilt from the replacement set, so counts for deleted objects
// are not carried forward.
func TestStoreReplace_CardinalityTrackerReset(t *testing.T) {
	t.Parallel()

	store := newTestStore()

	// Attach a cardinality tracker with a generous threshold.
	tracker := NewCardinalityTracker(10000, 0.8)
	store.SetCardinalityTracker(tracker)

	objA := makeUnstructured("uid-a", "pod-a", "default")
	objB := makeUnstructured("uid-b", "pod-b", "default")

	if err := store.Add(objA); err != nil {
		t.Fatalf("Add(objA): %v", err)
	}

	if err := store.Add(objB); err != nil {
		t.Fatalf("Add(objB): %v", err)
	}

	// Replace with only objA; objB is gone.
	if err := store.Replace([]interface{}{objA}, ""); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	// The tracker should have no entry for uid-b.
	tracker.mutex.RLock()
	defer tracker.mutex.RUnlock()

	if _, ok := tracker.perObject[types.UID("uid-b")]; ok {
		t.Error("after Replace: uid-b still tracked in CardinalityTracker; stale count was not cleared")
	}

	if _, ok := tracker.perObject[types.UID("uid-a")]; !ok {
		t.Error("after Replace: uid-a missing from CardinalityTracker")
	}
}
