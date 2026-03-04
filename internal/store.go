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
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
)

// StoreType implements the k8s.io/client-go/tools/cache.StoreType interface.
// The cache.Reflector uses the cache.StoreType to operate on the store.metrics map with the various metric families and their metrics based on the associated object's events.
type StoreType struct {
	logger       klog.Logger
	mutex        sync.RWMutex
	metrics      map[types.UID][]string
	headers      []string
	celCostLimit uint64
	celTimeout   time.Duration

	cardinalityTracker *CardinalityTracker

	// synced is set to true after the reflector completes its initial list
	// via Replace, indicating that all pre-existing objects have been added.
	synced atomic.Bool

	Group    string
	Version  string
	Kind     string
	Families []*FamilyType
	Resolver v1alpha1.ResolverType
	Labels   []v1alpha1.Label
}

func newStore(
	logger klog.Logger,
	headers []string,
	families []*FamilyType,
	resolver v1alpha1.ResolverType,
	labels []v1alpha1.Label,
	celCostLimit uint64,
	celTimeout time.Duration,
) *StoreType {
	return &StoreType{
		logger:       logger,
		metrics:      map[types.UID][]string{},
		headers:      headers,
		Families:     families,
		Resolver:     resolver,
		Labels:       labels,
		celCostLimit: celCostLimit,
		celTimeout:   celTimeout,
	}
}

// Add is called when a new object is added, and it generates the associated metrics for the object and stores them in the store.metrics map.
func (s *StoreType) Add(objectI interface{}) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	unstructuredObject, err := convertToUnstructured(objectI)
	if err != nil {
		return err
	}

	result := s.generateMetricsForObject(unstructuredObject)
	s.metrics[unstructuredObject.GetUID()] = result.metrics

	// Update cardinality tracking if tracker is initialized
	if s.cardinalityTracker != nil {
		s.cardinalityTracker.Update(unstructuredObject.GetUID(), result.perFamily)
		// Check thresholds and update cutoff state
		s.checkAndApplyThresholds()
	}

	s.logger.V(2).Info("Add", "key", klog.KObj(unstructuredObject))

	return nil
}

// Update is called when an object is updated, and it updates the associated metrics in the store.
// In this context, since metrics are generated based on the current state of the object, we simply call Add to regenerate the metrics for the updated object.
func (s *StoreType) Update(objectI interface{}) error {
	s.logger.V(2).Info("Update", "defer", "Add")

	return s.Add(objectI)
}

// Delete is called when an object is deleted, and it removes the associated metrics from the store.
func (s *StoreType) Delete(objectI interface{}) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	object, err := meta.Accessor(objectI)
	if err != nil {
		return fmt.Errorf("error casting object interface: %w", err)
	}

	s.logger.V(2).Info("Delete", "key", klog.KObj(object))
	s.logger.V(4).Info("Delete", "metrics", s.metrics[object.GetUID()])
	delete(s.metrics, object.GetUID())

	if s.cardinalityTracker != nil {
		s.cardinalityTracker.Delete(object.GetUID())
		s.checkAndApplyThresholds()
	}

	return nil
}

// Replace is called when the reflector does a resync or starts up and lists all existing objects.
func (s *StoreType) Replace(items []interface{}, _ string) error {
	for _, item := range items {
		if err := s.Add(item); err != nil {
			s.logger.Error(err, "failed to add item during replace")
		}
	}

	s.synced.Store(true)

	return nil
}

// IsSynced returns true after the reflector has completed its initial list.
func (s *StoreType) IsSynced() bool {
	return s.synced.Load()
}

// Stub implementations for interface compatibility.

// List is not needed for our use case, so it returns nil.
func (s *StoreType) List() []interface{} { return nil }

// ListKeys is not needed for our use case, so it returns nil.
func (s *StoreType) ListKeys() []string { return nil }

// Get is not needed for our use case, so it returns nil and false.
func (s *StoreType) Get(_ interface{}) (interface{}, bool, error) { return nil, false, nil }

// GetByKey is not needed for our use case, so it returns nil and false.
func (s *StoreType) GetByKey(_ string) (interface{}, bool, error) { return nil, false, nil }

// Resync is not needed for our use case, so it does nothing and returns nil.
func (s *StoreType) Resync() error { return nil }

// GetCardinalityTracker returns the cardinality tracker for this store.
func (s *StoreType) GetCardinalityTracker() *CardinalityTracker {
	return s.cardinalityTracker
}

// SetCardinalityTracker sets the cardinality tracker for this store.
func (s *StoreType) SetCardinalityTracker(tracker *CardinalityTracker) {
	s.cardinalityTracker = tracker
}

// GetStoreIdentifier returns a unique identifier for this store (GVK).
func (s *StoreType) GetStoreIdentifier() string {
	return s.Group + "/" + s.Version + "/" + s.Kind
}

func convertToUnstructured(obj interface{}) (*unstructured.Unstructured, error) {
	unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("error converting object interface to unstructured: %w", err)
	}

	return &unstructured.Unstructured{Object: unstructuredMap}, nil
}

// metricsWithCardinality holds the generated metrics and their cardinality counts.
type metricsWithCardinality struct {
	metrics   []string
	perFamily map[string]int64
}

func (s *StoreType) generateMetricsForObject(obj *unstructured.Unstructured) metricsWithCardinality {
	result := metricsWithCardinality{
		metrics:   make([]string, len(s.Families)),
		perFamily: make(map[string]int64),
	}

	for i, family := range s.Families {
		inheritFamilyConfiguration(family, s)

		family.logger = s.logger
		metricStr, sampleCount := family.buildMetricString(obj)
		result.metrics[i] = metricStr
		result.perFamily[family.Name] = sampleCount

		s.logger.V(4).Info("Add", "family", family.Name, "metrics", metricStr, "sampleCount", sampleCount)
	}

	return result
}

func inheritFamilyConfiguration(f *FamilyType, s *StoreType) {
	if f.Resolver == v1alpha1.ResolverTypeNone {
		f.Resolver = s.Resolver
	}

	f.Labels = append(f.Labels, s.Labels...)
}

// checkAndApplyThresholds checks cardinality thresholds and applies cutoffs to families.
// This method should be called after any cardinality update (Add, Update, Delete).
func (s *StoreType) checkAndApplyThresholds() []ThresholdViolation {
	if s.cardinalityTracker == nil {
		return nil
	}

	violations := s.cardinalityTracker.CheckThresholds()

	for _, family := range s.Families {
		cutoff := s.cardinalityTracker.IsFamilyCutoff(family.Name)
		family.SetCutoff(cutoff)
	}

	return violations
}
