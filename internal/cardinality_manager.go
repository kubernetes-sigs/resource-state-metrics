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

// GlobalCardinalityManager tracks cardinality across *all* RMMs.
type GlobalCardinalityManager struct {
	mutex                    sync.RWMutex
	perResource              map[types.UID]int64 // RMM's UID -> RMM's cardinality
	globalTotal              int64
	globalThreshold          int64
	resourceDefaultThreshold int64
	warningRatio             float64
	cutoffResources          map[types.UID]bool // RMMs that are cut off due to exceeding thresholds
}

// NewGlobalCardinalityManager creates a new GlobalCardinalityManager.
func NewGlobalCardinalityManager(globalThreshold, resourceDefaultThreshold int64, warningRatio float64) *GlobalCardinalityManager {
	return &GlobalCardinalityManager{
		perResource:              make(map[types.UID]int64),
		globalThreshold:          globalThreshold,
		resourceDefaultThreshold: resourceDefaultThreshold,
		warningRatio:             warningRatio,
		cutoffResources:          make(map[types.UID]bool),
	}
}

// UpdateResource updates the cardinality for a specific RMM resource.
func (m *GlobalCardinalityManager) UpdateResource(uid types.UID, cardinality int64) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Subtract old value if exists
	if oldCardinality, exists := m.perResource[uid]; exists {
		m.globalTotal -= oldCardinality
	}

	// Add new value
	m.perResource[uid] = cardinality
	m.globalTotal += cardinality
}

// DeleteResource removes cardinality tracking for a deleted RMM.
func (m *GlobalCardinalityManager) DeleteResource(uid types.UID) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if cardinality, exists := m.perResource[uid]; exists {
		m.globalTotal -= cardinality
		delete(m.perResource, uid)
	}

	delete(m.cutoffResources, uid)
}

// GetResourceCardinality returns the total cardinality for a specific RMM.
func (m *GlobalCardinalityManager) GetResourceCardinality(uid types.UID) int64 {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return m.perResource[uid]
}

// GetGlobalTotal returns the total cardinality across all RMMs.
func (m *GlobalCardinalityManager) GetGlobalTotal() int64 {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return m.globalTotal
}

// GetResourceDefaultThreshold returns the default threshold for individual resources.
func (m *GlobalCardinalityManager) GetResourceDefaultThreshold() int64 {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return m.resourceDefaultThreshold
}

// GetGlobalThreshold returns the global cardinality threshold.
func (m *GlobalCardinalityManager) GetGlobalThreshold() int64 {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return m.globalThreshold
}

// GetWarningRatio returns the warning ratio.
func (m *GlobalCardinalityManager) GetWarningRatio() float64 {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return m.warningRatio
}

func (m *GlobalCardinalityManager) CheckThresholds(uid types.UID, resourceThreshold int64) []ThresholdViolation {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	var violations []ThresholdViolation

	resourceCardinality := m.perResource[uid]

	threshold := resourceThreshold
	if threshold <= 0 {
		threshold = m.resourceDefaultThreshold
	}

	if threshold > 0 {
		ratio := float64(resourceCardinality) / float64(threshold)

		switch {
		case ratio > 1.0:
			m.cutoffResources[uid] = true

			violations = append(violations, ThresholdViolation{
				Level:     ThresholdLevelResource,
				Name:      "resource",
				Current:   resourceCardinality,
				Threshold: threshold,
				Severity:  SeverityCutoff,
			})
		case ratio >= m.warningRatio:
			violations = append(violations, ThresholdViolation{
				Level:     ThresholdLevelResource,
				Name:      "resource",
				Current:   resourceCardinality,
				Threshold: threshold,
				Severity:  SeverityWarning,
			})
			m.cutoffResources[uid] = false
		default:
			m.cutoffResources[uid] = false
		}
	}

	m.checkGlobalThreshold(resourceThreshold, &violations)

	return violations
}

func (m *GlobalCardinalityManager) checkGlobalThreshold(resourceThreshold int64, violations *[]ThresholdViolation) {
	if m.globalThreshold <= 0 {
		return
	}

	ratio := float64(m.globalTotal) / float64(m.globalThreshold)
	if ratio > 1.0 {
		for r := range m.perResource {
			m.cutoffResources[r] = true
		}

		*violations = append(*violations, ThresholdViolation{
			Level:     ThresholdLevelGlobal,
			Name:      "global",
			Current:   m.globalTotal,
			Threshold: m.globalThreshold,
			Severity:  SeverityCutoff,
		})

		return
	}

	if ratio >= m.warningRatio {
		*violations = append(*violations, ThresholdViolation{
			Level:     ThresholdLevelGlobal,
			Name:      "global",
			Current:   m.globalTotal,
			Threshold: m.globalThreshold,
			Severity:  SeverityWarning,
		})
	}

	for r, cardinality := range m.perResource {
		resThresh := resourceThreshold
		if resThresh <= 0 {
			resThresh = m.resourceDefaultThreshold
		}

		if resThresh <= 0 || float64(cardinality)/float64(resThresh) <= 1.0 {
			m.cutoffResources[r] = false
		}
	}
}

// IsResourceCutoff returns whether a specific RMM resource is cut off.
func (m *GlobalCardinalityManager) IsResourceCutoff(uid types.UID) bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return m.cutoffResources[uid]
}

// SetResourceCutoff sets the cutoff state for an RMM resource.
func (m *GlobalCardinalityManager) SetResourceCutoff(uid types.UID, cutoff bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.cutoffResources[uid] = cutoff
}

// GetAllResourceCardinalities returns a copy of all resource cardinalities.
func (m *GlobalCardinalityManager) GetAllResourceCardinalities() map[types.UID]int64 {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	result := make(map[types.UID]int64, len(m.perResource))
	maps.Copy(result, m.perResource)

	return result
}
