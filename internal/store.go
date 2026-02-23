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
	"fmt"
	"sync"
	"time"

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

	// Configuration fields unmarshalled from YAML
	Group     string `yaml:"group"`
	Version   string `yaml:"version"`
	Kind      string `yaml:"kind"`
	Resource  string `yaml:"resource"`
	Selectors struct {
		Label string `yaml:"label,omitempty"`
		Field string `yaml:"field,omitempty"`
	} `yaml:"selectors,omitempty"`
	Families    []*FamilyType `yaml:"families"`
	Resolver    ResolverType  `yaml:"resolver,omitempty"`
	LabelKeys   []string      `yaml:"labelKeys,omitempty"`
	LabelValues []string      `yaml:"labelValues,omitempty"`
}

func newStore(
	logger klog.Logger,
	headers []string,
	families []*FamilyType,
	resolver ResolverType,
	labelKeys []string, labelValues []string,
	celCostLimit uint64,
	celTimeout time.Duration,
) *StoreType {
	return &StoreType{
		logger:       logger,
		metrics:      map[types.UID][]string{},
		headers:      headers,
		Families:     families,
		Resolver:     resolver,
		LabelKeys:    labelKeys,
		LabelValues:  labelValues,
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

	metrics := s.generateMetricsForObject(unstructuredObject)
	s.metrics[unstructuredObject.GetUID()] = metrics
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

	return nil
}

// Replace is called when the reflector does a resync or starts up and lists all existing objects.
func (s *StoreType) Replace(items []interface{}, _ string) error {
	for _, item := range items {
		if err := s.Add(item); err != nil {
			s.logger.Error(err, "failed to add item during replace")
		}
	}

	return nil
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

func convertToUnstructured(obj interface{}) (*unstructured.Unstructured, error) {
	unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, fmt.Errorf("error converting object interface to unstructured: %w", err)
	}

	return &unstructured.Unstructured{Object: unstructuredMap}, nil
}

func (s *StoreType) generateMetricsForObject(obj *unstructured.Unstructured) []string {
	metrics := make([]string, len(s.Families))

	for i, family := range s.Families {
		inheritFamilyConfiguration(family, s)

		family.logger = s.logger
		metrics[i] = family.buildMetricString(obj)

		s.logger.V(4).Info("Add", "family", family.Name, "metrics", metrics[i])
	}

	return metrics
}

func inheritFamilyConfiguration(f *FamilyType, s *StoreType) {
	if f.Resolver == ResolverTypeNone {
		f.Resolver = s.Resolver
	}

	f.LabelKeys = append(f.LabelKeys, s.LabelKeys...)
	f.LabelValues = append(f.LabelValues, s.LabelValues...)
}
