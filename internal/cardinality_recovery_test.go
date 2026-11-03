/*
Copyright The Kubernetes Authors.

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

	"k8s.io/apimachinery/pkg/types"
)

// Families that only have the store threshold must be served again once the store is back under it.
func TestCardinalityTrackerRecoversFromStoreCutoff(t *testing.T) {
	t.Parallel()

	tracker := NewCardinalityTracker(10, 0.8)

	tracker.Update(types.UID("a"), map[string]int64{"family": 12})
	tracker.CheckThresholds()

	if !tracker.IsFamilyCutoff("family") {
		t.Fatal("the family should be cut off while the store is over its threshold")
	}

	tracker.Delete(types.UID("a"))
	tracker.Update(types.UID("b"), map[string]int64{"family": 2})
	tracker.CheckThresholds()

	if tracker.IsFamilyCutoff("family") {
		t.Error("the family should recover once the store is under its threshold")
	}
}

// A family with its own threshold stays cut off while the store is over its threshold.
func TestCardinalityTrackerFamilyStaysCutoffWhileStoreIsOver(t *testing.T) {
	t.Parallel()

	tracker := NewCardinalityTracker(10, 0.8)
	tracker.SetFamilyThreshold("family", 100)

	tracker.Update(types.UID("a"), map[string]int64{"family": 12})
	tracker.CheckThresholds()

	if !tracker.IsFamilyCutoff("family") {
		t.Error("the family should stay cut off while the store is over its threshold")
	}

	tracker.Delete(types.UID("a"))
	tracker.CheckThresholds()

	if tracker.IsFamilyCutoff("family") {
		t.Error("the family should recover once the store is under its threshold")
	}
}

// Resources without a threshold of their own must recover once the global total is back under its threshold.
func TestGlobalCardinalityManagerRecoversFromGlobalCutoff(t *testing.T) {
	t.Parallel()

	manager := NewGlobalCardinalityManager(10, 0, 0.8)
	uid := types.UID("resource")

	manager.UpdateResource(uid, 12)
	manager.CheckThresholds(uid, 0)

	if !manager.IsResourceCutoff(uid) {
		t.Fatal("the resource should be cut off while the global total is over its threshold")
	}

	manager.UpdateResource(uid, 2)
	manager.CheckThresholds(uid, 0)

	if manager.IsResourceCutoff(uid) {
		t.Error("the resource should recover once the global total is under its threshold")
	}
}
