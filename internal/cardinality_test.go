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

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/types"
)

func TestCardinalityTrackerUpdateReplacesAndDeletesObjectCounts(t *testing.T) {
	t.Parallel()

	tracker := NewCardinalityTracker(0, 0.8)
	uid := types.UID("object-1")

	tracker.Update(uid, map[string]int64{
		"family-a": 2,
		"family-b": 1,
	})

	if got, want := tracker.GetStoreTotal(), int64(3); got != want {
		t.Fatalf("GetStoreTotal() = %d, want %d", got, want)
	}

	tracker.Update(uid, map[string]int64{
		"family-a": 4,
		"family-c": 3,
	})

	if got, want := tracker.GetStoreTotal(), int64(7); got != want {
		t.Fatalf("GetStoreTotal() after replacement = %d, want %d", got, want)
	}

	wantFamilies := map[string]int64{
		"family-a": 4,
		"family-b": 0,
		"family-c": 3,
	}
	if diff := cmp.Diff(wantFamilies, tracker.GetAllFamilyCardinalities()); diff != "" {
		t.Fatalf("family cardinalities after replacement diff (-want +got):\n%s", diff)
	}

	tracker.Update(types.UID("object-2"), map[string]int64{"family-c": 2})
	tracker.Delete(uid)

	if got, want := tracker.GetStoreTotal(), int64(2); got != want {
		t.Fatalf("GetStoreTotal() after delete = %d, want %d", got, want)
	}

	wantFamilies = map[string]int64{
		"family-a": 0,
		"family-b": 0,
		"family-c": 2,
	}
	if diff := cmp.Diff(wantFamilies, tracker.GetAllFamilyCardinalities()); diff != "" {
		t.Fatalf("family cardinalities after delete diff (-want +got):\n%s", diff)
	}

	tracker.Delete(types.UID("missing"))

	if got, want := tracker.GetStoreTotal(), int64(2); got != want {
		t.Fatalf("GetStoreTotal() after missing delete = %d, want %d", got, want)
	}
}

func TestCardinalityTrackerThresholdsWarnAtRatioAndCutoffAboveLimit(t *testing.T) {
	t.Parallel()

	tracker := NewCardinalityTracker(10, 0.8)
	tracker.SetFamilyThreshold("family-a", 5)

	tracker.Update(types.UID("object-1"), map[string]int64{"family-a": 4})
	violations := tracker.CheckThresholds()
	assertViolation(t, violations, ThresholdLevelFamily, "family-a", SeverityWarning, 4, 5)

	if tracker.IsFamilyCutoff("family-a") {
		t.Fatalf("family-a is cut off at warning threshold")
	}

	tracker.Update(types.UID("object-1"), map[string]int64{"family-a": 5})
	violations = tracker.CheckThresholds()
	assertViolation(t, violations, ThresholdLevelFamily, "family-a", SeverityWarning, 5, 5)

	if tracker.IsFamilyCutoff("family-a") {
		t.Fatalf("family-a is cut off at exact threshold")
	}

	tracker.Update(types.UID("object-1"), map[string]int64{"family-a": 6})
	violations = tracker.CheckThresholds()
	assertViolation(t, violations, ThresholdLevelFamily, "family-a", SeverityCutoff, 6, 5)

	if !tracker.IsFamilyCutoff("family-a") {
		t.Fatalf("family-a is not cut off above threshold")
	}

	tracker.Update(types.UID("object-2"), map[string]int64{"family-b": 5})
	violations = tracker.CheckThresholds()
	assertViolation(t, violations, ThresholdLevelStore, "store", SeverityCutoff, 11, 10)

	if !tracker.IsFamilyCutoff("family-a") || !tracker.IsFamilyCutoff("family-b") {
		t.Fatalf("store cutoff did not cut off all families")
	}
}

func TestCardinalityTrackerStoreWarningDoesNotCutOff(t *testing.T) {
	t.Parallel()

	ct := NewCardinalityTracker(10, 0.8)
	ct.Update(types.UID("object-1"), map[string]int64{"family-a": 8})

	violations := ct.CheckThresholds()
	assertViolation(t, violations, ThresholdLevelStore, "store", SeverityWarning, 8, 10)

	if ct.IsFamilyCutoff("family-a") {
		t.Fatalf("family-a is cut off at store warning threshold")
	}
}

func TestCardinalityTrackerCutoffRecovery(t *testing.T) {
	t.Parallel()

	tracker := NewCardinalityTracker(5, 0.8)
	tracker.SetFamilyThreshold("family-a", 3)

	tracker.Update(types.UID("object-1"), map[string]int64{
		"family-a": 4,
		"family-b": 2,
	})
	violations := tracker.CheckThresholds()
	assertViolation(t, violations, ThresholdLevelFamily, "family-a", SeverityCutoff, 4, 3)
	assertViolation(t, violations, ThresholdLevelStore, "store", SeverityCutoff, 6, 5)

	if !tracker.IsFamilyCutoff("family-a") || !tracker.IsFamilyCutoff("family-b") {
		t.Fatalf("families are not cut off after threshold breach")
	}

	tracker.Update(types.UID("object-1"), map[string]int64{
		"family-a": 3,
		"family-b": 1,
	})
	violations = tracker.CheckThresholds()
	assertViolation(t, violations, ThresholdLevelFamily, "family-a", SeverityWarning, 3, 3)
	assertViolation(t, violations, ThresholdLevelStore, "store", SeverityWarning, 4, 5)

	if tracker.IsFamilyCutoff("family-a") || tracker.IsFamilyCutoff("family-b") {
		t.Fatalf("families remain cut off after recovery to threshold")
	}

	tracker.Update(types.UID("object-1"), map[string]int64{
		"family-a": 1,
		"family-b": 1,
	})

	if violations := tracker.CheckThresholds(); len(violations) != 0 {
		t.Fatalf("CheckThresholds() after recovery returned %v, want no violations", violations)
	}

	if tracker.IsFamilyCutoff("family-a") || tracker.IsFamilyCutoff("family-b") {
		t.Fatalf("families remain cut off below warning threshold")
	}
}

func TestCardinalityTrackerDisabledThresholds(t *testing.T) {
	t.Parallel()

	ct := NewCardinalityTracker(0, 0.8)
	ct.SetFamilyThreshold("family-a", 0)
	ct.SetFamilyThreshold("family-b", -1)

	ct.Update(types.UID("object-1"), map[string]int64{
		"family-a": 100,
		"family-b": 100,
	})

	if violations := ct.CheckThresholds(); len(violations) != 0 {
		t.Fatalf("CheckThresholds() = %v, want no violations for disabled thresholds", violations)
	}

	if ct.IsFamilyCutoff("family-a") || ct.IsFamilyCutoff("family-b") {
		t.Fatalf("disabled thresholds cut off families")
	}
}

func TestCardinalityTrackerResetClearsDataButKeepsThresholds(t *testing.T) {
	t.Parallel()

	tracker := NewCardinalityTracker(3, 0.8)
	tracker.SetFamilyThreshold("family-a", 1)
	tracker.Update(types.UID("object-1"), map[string]int64{"family-a": 2})
	tracker.CheckThresholds()

	tracker.Reset()

	if got := tracker.GetStoreTotal(); got != 0 {
		t.Fatalf("GetStoreTotal() after Reset = %d, want 0", got)
	}

	if got := tracker.GetFamilyCardinality("family-a"); got != 0 {
		t.Fatalf("GetFamilyCardinality() after Reset = %d, want 0", got)
	}

	if cutoff := tracker.GetCutoffFamilies(); len(cutoff) != 0 {
		t.Fatalf("GetCutoffFamilies() after Reset = %v, want none", cutoff)
	}

	if got, want := tracker.GetStoreThreshold(), int64(3); got != want {
		t.Fatalf("GetStoreThreshold() after Reset = %d, want %d", got, want)
	}

	if got, want := tracker.GetFamilyThreshold("family-a"), int64(1); got != want {
		t.Fatalf("GetFamilyThreshold() after Reset = %d, want %d", got, want)
	}
}

func TestCardinalityTrackerUsesCopiedMaps(t *testing.T) {
	t.Parallel()

	tracker := NewCardinalityTracker(0, 0.8)
	tracker.SetFamilyThreshold("family-a", 5)

	counts := map[string]int64{"family-a": 2}
	tracker.Update(types.UID("object-1"), counts)
	counts["family-a"] = 100

	tracker.Delete(types.UID("object-1"))

	if got := tracker.GetStoreTotal(); got != 0 {
		t.Fatalf("GetStoreTotal() after deleting object with mutated source map = %d, want 0", got)
	}

	tracker.Update(types.UID("object-2"), map[string]int64{"family-a": 3})
	familyCards := tracker.GetAllFamilyCardinalities()
	familyCards["family-a"] = 100

	if got, want := tracker.GetFamilyCardinality("family-a"), int64(3); got != want {
		t.Fatalf("GetFamilyCardinality() after mutating returned cardinalities = %d, want %d", got, want)
	}

	thresholds := tracker.GetAllFamilyThresholds()
	thresholds["family-a"] = 100

	if got, want := tracker.GetFamilyThreshold("family-a"), int64(5); got != want {
		t.Fatalf("GetFamilyThreshold() after mutating returned thresholds = %d, want %d", got, want)
	}
}

func TestCardinalityTrackerFamilySpecificThresholds(t *testing.T) {
	t.Parallel()

	tracker := NewCardinalityTracker(200, 0.8)
	tracker.SetFamilyThreshold("family-a", 2)
	tracker.SetFamilyThreshold("family-b", 5)

	tracker.Update(types.UID("object-1"), map[string]int64{
		"family-a": 3,
		"family-b": 4,
		"family-c": 100,
	})

	violations := tracker.CheckThresholds()
	assertViolation(t, violations, ThresholdLevelFamily, "family-a", SeverityCutoff, 3, 2)
	assertViolation(t, violations, ThresholdLevelFamily, "family-b", SeverityWarning, 4, 5)
	assertNoViolation(t, violations, ThresholdLevelFamily, "family-c")

	if !tracker.IsFamilyCutoff("family-a") {
		t.Fatalf("family-a is not cut off")
	}

	if tracker.IsFamilyCutoff("family-b") {
		t.Fatalf("family-b is cut off at warning threshold")
	}

	if tracker.IsFamilyCutoff("family-c") {
		t.Fatalf("family-c is cut off without a family threshold")
	}
}

func assertViolation(t *testing.T, violations []ThresholdViolation, level ThresholdLevel, name string, severity ViolationSeverity, current, threshold int64) {
	t.Helper()

	for _, violation := range violations {
		if violation.Level == level && violation.Name == name {
			if violation.Severity != severity || violation.Current != current || violation.Threshold != threshold {
				t.Fatalf("violation %s/%s = %+v, want severity=%s current=%d threshold=%d", level, name, violation, severity, current, threshold)
			}

			return
		}
	}

	t.Fatalf("missing violation %s/%s in %+v", level, name, violations)
}

func assertNoViolation(t *testing.T, violations []ThresholdViolation, level ThresholdLevel, name string) {
	t.Helper()

	for _, violation := range violations {
		if violation.Level == level && violation.Name == name {
			t.Fatalf("unexpected violation %s/%s: %+v", level, name, violation)
		}
	}
}
