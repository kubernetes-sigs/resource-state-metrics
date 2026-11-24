/*
Copyright 2026 The Kubernetes resource-state-metrics Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
Without WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package internal

import (
	"testing"

	"k8s.io/apimachinery/pkg/types"
)

// TestCardinalityTracker_StoreThresholdRecovery verifies that when a store's total
// cardinality exceeds the storeThreshold and then drops back below it (e.g. objects deleted),
// the cutoff state for store-level families is automatically cleared.
func TestCardinalityTracker_StoreThresholdRecovery(t *testing.T) {
	t.Parallel()

	tracker := NewCardinalityTracker(10, 0.8)
	famName := "test_metric_family"

	// 1. Add objects so store total exceeds threshold (15 > 10)
	tracker.Update(types.UID("obj-1"), map[string]int64{famName: 8})
	tracker.Update(types.UID("obj-2"), map[string]int64{famName: 7})

	violations := tracker.CheckThresholds()

	if !tracker.IsFamilyCutoff(famName) {
		t.Fatalf("expected family %q to be cut off when store total (15) > threshold (10)", famName)
	}

	hasCutoffViolation := false

	for _, v := range violations {
		if v.Level == ThresholdLevelStore && v.Severity == SeverityCutoff {
			hasCutoffViolation = true
		}
	}

	if !hasCutoffViolation {
		t.Errorf("expected store level SeverityCutoff violation, got violations: %+v", violations)
	}

	// 2. Delete obj-2 so store total drops back to 8 (below threshold 10)
	tracker.Delete(types.UID("obj-2"))

	violationsAfterDelete := tracker.CheckThresholds()

	if tracker.IsFamilyCutoff(famName) {
		t.Fatalf("expected family %q cutoff to recover when store total (8) <= threshold (10)", famName)
	}

	for _, v := range violationsAfterDelete {
		if v.Severity == SeverityCutoff {
			t.Errorf("expected no cutoff violations after recovery, got: %+v", v)
		}
	}
}

// TestGlobalCardinalityManager_GlobalThresholdRecovery verifies that when total global
// cardinality exceeds globalThreshold and then drops back below it, resource cutoffs recover.
func TestGlobalCardinalityManager_GlobalThresholdRecovery(t *testing.T) {
	t.Parallel()

	mgr := NewGlobalCardinalityManager(100, 1000, 0.8)
	uid1 := types.UID("rmm-1")
	uid2 := types.UID("rmm-2")

	// 1. Exceed global threshold (60 + 50 = 110 > 100)
	mgr.UpdateResource(uid1, 60)
	mgr.UpdateResource(uid2, 50)

	mgr.CheckThresholds(uid1, 0)
	mgr.CheckThresholds(uid2, 0)

	if !mgr.IsResourceCutoff(uid1) || !mgr.IsResourceCutoff(uid2) {
		t.Fatalf("expected both resources to be cut off when global total (110) > threshold (100)")
	}

	// 2. Reduce cardinality of rmm-2 so global total drops to 80 (<= 100)
	mgr.UpdateResource(uid2, 20)

	mgr.CheckThresholds(uid1, 0)
	mgr.CheckThresholds(uid2, 0)

	if mgr.IsResourceCutoff(uid1) {
		t.Errorf("expected rmm-1 cutoff to recover when global total (80) <= threshold (100)")
	}

	if mgr.IsResourceCutoff(uid2) {
		t.Errorf("expected rmm-2 cutoff to recover when global total (80) <= threshold (100)")
	}
}
