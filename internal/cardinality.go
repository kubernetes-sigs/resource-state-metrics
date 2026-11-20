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
	"maps"
	"sync"

	"k8s.io/apimachinery/pkg/types"
)

// ThresholdLevel represents the level at which a cardinality threshold applies.
type ThresholdLevel string

const (
	// ThresholdLevelFamily indicates a per-family cardinality threshold.
	ThresholdLevelFamily ThresholdLevel = "family"
	// ThresholdLevelStore indicates a per-store cardinality threshold.
	ThresholdLevelStore ThresholdLevel = "store"
	// ThresholdLevelResource indicates a per-RMM resource cardinality threshold.
	ThresholdLevelResource ThresholdLevel = "resource"
	// ThresholdLevelGlobal indicates a global cardinality threshold across all RMMs.
	ThresholdLevelGlobal ThresholdLevel = "global"
)

// ViolationSeverity represents the severity of a threshold violation.
// Note: Violations are only created when thresholds are breached (>=warningRatio).
// When cardinality is below the warning threshold, no violation is created.
type ViolationSeverity string

const (
	// SeverityWarning indicates cardinality is at warning level (≥80% of threshold, including exactly at threshold).
	// Metric generation continues but a warning condition is set.
	SeverityWarning ViolationSeverity = "warning"
	// SeverityCutoff indicates cardinality has strictly exceeded the threshold (>100%).
	// Metric generation is stopped until cardinality drops back to or below the threshold.
	SeverityCutoff ViolationSeverity = "cutoff"
)

// ThresholdViolation represents a cardinality threshold breach.
// Note: These are only created when cardinality >= warningRatio * threshold.
// Below that level, no violation exists and none is created.
type ThresholdViolation struct {
	Level        ThresholdLevel
	Name         string // Family name, store identifier, RMM name, or "global"
	Current      int64
	Threshold    int64
	Severity     ViolationSeverity
	StoreName    string
	RMMName      string
	RMMNamespace string
}

// CardinalityTracker tracks cardinality for a single store.
type CardinalityTracker struct {
	mutex          sync.RWMutex
	perFamily      map[string]int64               // family name -> count
	perObject      map[types.UID]map[string]int64 // uid -> family name -> count
	storeTotal     int64
	cutoffFamilies map[string]bool // families that are cut off

	storeThreshold  int64            // threshold for the entire store
	familyThreshold map[string]int64 // family name -> threshold
	warningRatio    float64          // ratio at which to warn (e.g., 0.8)
}

// NewCardinalityTracker creates a new CardinalityTracker with the given thresholds.
func NewCardinalityTracker(storeThreshold int64, warningRatio float64) *CardinalityTracker {
	return &CardinalityTracker{
		perFamily:       make(map[string]int64),
		perObject:       make(map[types.UID]map[string]int64),
		cutoffFamilies:  make(map[string]bool),
		familyThreshold: make(map[string]int64),
		storeThreshold:  storeThreshold,
		warningRatio:    warningRatio,
	}
}

// SetFamilyThreshold sets the cardinality threshold for a specific family.
func (ct *CardinalityTracker) SetFamilyThreshold(familyName string, threshold int64) {
	ct.mutex.Lock()
	defer ct.mutex.Unlock()

	ct.familyThreshold[familyName] = threshold
}

// Update updates cardinality counts when an object is added or updated.
// perFamilyCounts maps family name to the number of samples generated for that family.
func (ct *CardinalityTracker) Update(uid types.UID, perFamilyCounts map[string]int64) {
	ct.mutex.Lock()
	defer ct.mutex.Unlock()

	if oldCounts, exists := ct.perObject[uid]; exists {
		for family, count := range oldCounts {
			ct.perFamily[family] -= count
			ct.storeTotal -= count
		}
	}

	counts := maps.Clone(perFamilyCounts)

	ct.perObject[uid] = counts
	for family, count := range counts {
		ct.perFamily[family] += count
		ct.storeTotal += count
	}
}

// Delete removes cardinality counts when an object is deleted.
func (ct *CardinalityTracker) Delete(uid types.UID) {
	ct.mutex.Lock()
	defer ct.mutex.Unlock()

	if counts, exists := ct.perObject[uid]; exists {
		for family, count := range counts {
			ct.perFamily[family] -= count
			ct.storeTotal -= count
		}

		delete(ct.perObject, uid)
	}
}

// GetStoreTotal returns the total cardinality for this store.
func (ct *CardinalityTracker) GetStoreTotal() int64 {
	ct.mutex.RLock()
	defer ct.mutex.RUnlock()

	return ct.storeTotal
}

// GetFamilyCardinality returns the cardinality for a specific family.
func (ct *CardinalityTracker) GetFamilyCardinality(familyName string) int64 {
	ct.mutex.RLock()
	defer ct.mutex.RUnlock()

	return ct.perFamily[familyName]
}

// GetAllFamilyCardinalities returns a copy of all family cardinalities.
func (ct *CardinalityTracker) GetAllFamilyCardinalities() map[string]int64 {
	ct.mutex.RLock()
	defer ct.mutex.RUnlock()

	result := make(map[string]int64, len(ct.perFamily))
	maps.Copy(result, ct.perFamily)

	return result
}

// CheckThresholds evaluates thresholds and returns any violations.
// It also manages cutoff state based on threshold breaches.
func (ct *CardinalityTracker) CheckThresholds() []ThresholdViolation {
	ct.mutex.Lock()
	defer ct.mutex.Unlock()

	var violations []ThresholdViolation

	for family, count := range ct.perFamily {
		threshold, hasThreshold := ct.familyThreshold[family]
		if !hasThreshold || threshold <= 0 {
			continue
		}

		ratio := float64(count) / float64(threshold)

		switch {
		case ratio > 1.0:
			ct.cutoffFamilies[family] = true

			violations = append(violations, ThresholdViolation{
				Level:     ThresholdLevelFamily,
				Name:      family,
				Current:   count,
				Threshold: threshold,
				Severity:  SeverityCutoff,
			})
		case ratio >= ct.warningRatio:
			violations = append(violations, ThresholdViolation{
				Level:     ThresholdLevelFamily,
				Name:      family,
				Current:   count,
				Threshold: threshold,
				Severity:  SeverityWarning,
			})
			// Below 100% of threshold; clear cutoff and recover
			ct.cutoffFamilies[family] = false
		default:
			// Below warning threshold; clear cutoff and recover
			ct.cutoffFamilies[family] = false
		}
	}

	if ct.storeThreshold <= 0 {
		return violations
	}

	ratio := float64(ct.storeTotal) / float64(ct.storeThreshold)
	if ratio > 1.0 {
		for family := range ct.perFamily {
			ct.cutoffFamilies[family] = true
		}

		violations = append(violations, ThresholdViolation{
			Level:     ThresholdLevelStore,
			Name:      "store",
			Current:   ct.storeTotal,
			Threshold: ct.storeThreshold,
			Severity:  SeverityCutoff,
		})

		return violations
	}

	if ratio >= ct.warningRatio {
		violations = append(violations, ThresholdViolation{
			Level:     ThresholdLevelStore,
			Name:      "store",
			Current:   ct.storeTotal,
			Threshold: ct.storeThreshold,
			Severity:  SeverityWarning,
		})
	}

	for family := range ct.perFamily {
		threshold, hasThreshold := ct.familyThreshold[family]
		if !hasThreshold || threshold <= 0 {
			ct.cutoffFamilies[family] = false
		}
	}

	return violations
}

// IsFamilyCutoff returns whether a specific family is cut off.
func (ct *CardinalityTracker) IsFamilyCutoff(familyName string) bool {
	ct.mutex.RLock()
	defer ct.mutex.RUnlock()

	return ct.cutoffFamilies[familyName]
}

// SetFamilyCutoff sets the cutoff state for a family.
func (ct *CardinalityTracker) SetFamilyCutoff(familyName string, cutoff bool) {
	ct.mutex.Lock()
	defer ct.mutex.Unlock()

	ct.cutoffFamilies[familyName] = cutoff
}

// GetCutoffFamilies returns a list of all families that are currently cut off.
func (ct *CardinalityTracker) GetCutoffFamilies() []string {
	ct.mutex.RLock()
	defer ct.mutex.RUnlock()

	var families []string

	for family, cutoff := range ct.cutoffFamilies {
		if cutoff {
			families = append(families, family)
		}
	}

	return families
}

// Reset clears all cardinality data. Used when the store is being rebuilt.
func (ct *CardinalityTracker) Reset() {
	ct.mutex.Lock()
	defer ct.mutex.Unlock()

	ct.perFamily = make(map[string]int64)
	ct.perObject = make(map[types.UID]map[string]int64)
	ct.storeTotal = 0
	ct.cutoffFamilies = make(map[string]bool)
}

// GetStoreThreshold returns the configured store-level cardinality threshold.
func (ct *CardinalityTracker) GetStoreThreshold() int64 {
	ct.mutex.RLock()
	defer ct.mutex.RUnlock()

	return ct.storeThreshold
}

// GetFamilyThreshold returns the configured threshold for a specific family.
func (ct *CardinalityTracker) GetFamilyThreshold(familyName string) int64 {
	ct.mutex.RLock()
	defer ct.mutex.RUnlock()

	return ct.familyThreshold[familyName]
}

// GetAllFamilyThresholds returns a copy of all configured family thresholds.
func (ct *CardinalityTracker) GetAllFamilyThresholds() map[string]int64 {
	ct.mutex.RLock()
	defer ct.mutex.RUnlock()

	result := make(map[string]int64, len(ct.familyThreshold))
	for k, v := range ct.familyThreshold {
		result[k] = v
	}

	return result
}
