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
	"context"
	"errors"
	"sync"
	"time"

	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/resolver"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
)

// configure defines behaviours for working with configuration(s).
type configure interface {
	// build builds the given configuration.
	build(ctx context.Context, stores *sync.Map)
}

// configurer knows how to work with RMM configurations.
type configurer struct {
	configuration              v1alpha1.Configuration
	dynamicClientset           dynamic.Interface
	resourceDiscovery          ResourceDiscovery
	resource                   *v1alpha1.ResourceMetricsMonitor
	celCostLimit               uint64
	celTimeout                 time.Duration
	celEvaluations             *prometheus.CounterVec
	resourceCardinalityDefault int64
	cardinalityWarningRatio    float64
	starlarkTimeout            time.Duration
	starlarkMaxSteps           int
	duplicateStores            *prometheus.GaugeVec
	duplicateFamilies          *prometheus.GaugeVec
}

// Ensure configurer implements configure.
var _ configure = &configurer{}

// newConfigurer returns a new configurer.
func newConfigurer(
	dynamicClientset dynamic.Interface,
	resourceDiscovery ResourceDiscovery,
	resource *v1alpha1.ResourceMetricsMonitor,
	celCostLimit uint64,
	celTimeout time.Duration,
	celEvaluations *prometheus.CounterVec,
	resourceCardinalityDefault int64,
	cardinalityWarningRatio float64,
	starlarkTimeout time.Duration,
	starlarkMaxSteps int,
	duplicateStores *prometheus.GaugeVec,
	duplicateFamilies *prometheus.GaugeVec,
) *configurer {
	return &configurer{
		configuration:              resource.Spec.Configuration,
		dynamicClientset:           dynamicClientset,
		resourceDiscovery:          resourceDiscovery,
		resource:                   resource,
		celCostLimit:               celCostLimit,
		celTimeout:                 celTimeout,
		celEvaluations:             celEvaluations,
		resourceCardinalityDefault: resourceCardinalityDefault,
		cardinalityWarningRatio:    cardinalityWarningRatio,
		starlarkTimeout:            starlarkTimeout,
		starlarkMaxSteps:           starlarkMaxSteps,
		duplicateStores:            duplicateStores,
		duplicateFamilies:          duplicateFamilies,
	}
}

// storeKey returns a unique key for a store based on its GVK.
func storeKey(store *v1alpha1.Store) string {
	return store.Group + "/" + store.Version + "/" + store.Kind
}

// build constructs the metric stores from the parsed configuration.
func (c *configurer) build(ctx context.Context, stores *sync.Map) {
	logger := klog.FromContext(ctx)

	// First pass: expand all wildcards and collect store configs
	var allExpandedStores []v1alpha1.Store

	for i := range c.configuration.Stores {
		storeConfig := &c.configuration.Stores[i]

		// Expand wildcards using discovery
		expandedStores, err := c.resourceDiscovery.ExpandWildcards(storeConfig)
		if err != nil {
			logger.Error(err, "Failed to expand wildcard store config",
				"group", storeConfig.Group,
				"version", storeConfig.Version,
				"kind", storeConfig.Kind)

			continue
		}

		allExpandedStores = append(allExpandedStores, expandedStores...)
	}

	// Second pass: detect and handle duplicates
	seenStores := make(map[string]int)        // storeKey -> count
	seenFamilies := make(map[string][]string) // familyName -> []storeKeys
	deduplicatedStores := make([]v1alpha1.Store, 0, len(allExpandedStores))

	namespace := c.resource.GetNamespace()
	name := c.resource.GetName()

	for i := range allExpandedStores {
		store := &allExpandedStores[i]
		key := storeKey(store)

		if count, exists := seenStores[key]; exists {
			// Duplicate store detected
			logger.V(1).Info("Duplicate store detected, skipping",
				"store", key,
				"occurrence", count+1,
				"namespace", namespace,
				"name", name)
			seenStores[key] = count + 1

			continue
		}

		seenStores[key] = 1

		// Track family names for cross-store duplicate detection
		for _, family := range store.Families {
			seenFamilies[family.Name] = append(seenFamilies[family.Name], key)
		}

		deduplicatedStores = append(deduplicatedStores, *store)
	}

	// Set gauge metrics for duplicate stores. Always set so the value resets
	// to 0 when a previously-duplicate config is fixed.
	if c.duplicateStores != nil {
		for key, count := range seenStores {
			c.duplicateStores.WithLabelValues(namespace, name, key).Set(float64(count - 1))
		}
	}

	// Set gauge metrics for families shared across multiple stores.
	if c.duplicateFamilies != nil {
		for familyName, storeKeys := range seenFamilies {
			if len(storeKeys) > 1 {
				logger.V(1).Info("Family name used in multiple stores - metrics will have duplicate HELP/TYPE lines",
					"family", familyName,
					"stores", storeKeys,
					"namespace", namespace,
					"name", name)
			}

			c.duplicateFamilies.WithLabelValues(namespace, name, familyName).Set(float64(len(storeKeys) - 1))
		}
	}

	// Third pass: build the deduplicated stores
	builtStores := make([]*StoreType, 0, len(deduplicatedStores))
	for i := range deduplicatedStores {
		s := c.buildStoreFromConfig(ctx, &deduplicatedStores[i])
		builtStores = append(builtStores, s)
	}

	stores.Store(c.resource.GetUID(), builtStores)
}

func (c *configurer) buildStoreFromConfig(ctx context.Context, store *v1alpha1.Store) *StoreType {
	gvkWithR := buildGVKR(store)

	storeCardinalityLimit := store.CardinalityLimit
	if storeCardinalityLimit <= 0 {
		storeCardinalityLimit = c.resourceCardinalityDefault
	}

	// Build FamilyType slice directly from v1alpha1.Family (no conversion middleware)
	logger := klog.Background()

	families := make([]*FamilyType, len(store.Families))
	for idx := range store.Families {
		families[idx] = &FamilyType{
			Family:      store.Families[idx],
			storeLabels: store.Labels,
		}

		// Validate and instantiate StarlarkResolver
		if store.Families[idx].Resolver == v1alpha1.ResolverTypeStarlark {
			if store.Families[idx].Starlark == nil {
				logger.Error(errors.New("resolver is starlark but starlark config is missing"), "invalid family configuration", "family", store.Families[idx].Name)

				continue
			}

			starlarkCfg := store.Families[idx].Starlark
			timeout := c.starlarkTimeout
			maxSteps := c.starlarkMaxSteps

			// Allow per-family overrides
			if starlarkCfg.Timeout > 0 {
				timeout = time.Duration(starlarkCfg.Timeout) * time.Second
			}

			if starlarkCfg.MaxSteps > 0 {
				maxSteps = starlarkCfg.MaxSteps
			}

			families[idx].starlarkResolver = resolver.NewStarlarkResolver(
				logger.WithValues("family", store.Families[idx].Name),
				starlarkCfg.Script,
				timeout,
				maxSteps,
			)
		}
	}

	return buildStore(
		ctx,
		c.dynamicClientset,
		gvkWithR,
		families,
		store.Selectors.Label, store.Selectors.Field,
		store.Resolver,
		store.Labels,
		c.celCostLimit,
		c.celTimeout,
		c.celEvaluations,
		c.resource.GetNamespace(),
		c.resource.GetName(),
		storeCardinalityLimit,
		c.cardinalityWarningRatio,
	)
}

func buildGVKR(store *v1alpha1.Store) gvkr {
	return gvkr{
		GroupVersionKind: schema.GroupVersionKind{
			Group:   store.Group,
			Version: store.Version,
			Kind:    store.Kind,
		},
		GroupVersionResource: schema.GroupVersionResource{
			Group:    store.Group,
			Version:  store.Version,
			Resource: store.Resource,
		},
	}
}
