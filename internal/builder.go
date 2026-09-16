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
	"fmt"
	"strconv"
	"time"

	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

// CreatedAtEpoch is the Unix timestamp (seconds) used for _created samples.
// Empty (default) means time.Now(). Set via -ldflags at build time or
// directly in tests for deterministic output.
// Build-time: make local CREATED_AT_EPOCH=0.
var CreatedAtEpoch = ""

// nowTime returns the time to stamp _created samples with.
// When CreatedAtEpoch is set it is parsed as a Unix timestamp; otherwise time.Now() is used.
func nowTime() time.Time {
	if CreatedAtEpoch != "" {
		if epoch, err := strconv.ParseInt(CreatedAtEpoch, 10, 64); err == nil {
			return time.Unix(epoch, 0)
		}
	}

	return time.Now()
}

// gvkr holds the GVK/R information for the custom resource that the store is built for.
type gvkr struct {
	schema.GroupVersionKind
	schema.GroupVersionResource
}

// buildStore builds a cache.store for the metrics store.
func buildStore(
	ctx context.Context,
	dynamicClientset dynamic.Interface,
	gvkWithR gvkr,
	metricFamilies []*FamilyType,
	labelSelector, fieldSelector string,
	resolver v1alpha1.ResolverType,
	labels []v1alpha1.Label,
	celCostLimit uint64,
	celTimeout time.Duration,
	celEvaluations *prometheus.CounterVec,
	namespace, name string,
	storeCardinalityLimit int64,
	warningRatio float64,
) *StoreType {
	logger := klog.FromContext(ctx)
	listerwatcher := buildLW(ctx, dynamicClientset, labelSelector, fieldSelector, gvkWithR.GroupVersionResource)
	// Propagate CEL limits, metrics, and RMM identity to all families before
	// store creation so that family.createdAt is set.
	familyCreatedAt := nowTime()

	for _, family := range metricFamilies {
		family.logger = logger.WithValues("family", family.Name)
		family.createdAt = familyCreatedAt
		family.celCostLimit = celCostLimit
		family.celTimeout = celTimeout
		family.celEvaluations = celEvaluations
		family.managedRMMNamespace = namespace
		family.managedRMMName = name
	}

	s := newStore(logger, metricFamilies, resolver, labels, celCostLimit, celTimeout)
	gvk := gvkWithR.GroupVersionKind
	s.Group = gvk.Group
	s.Version = gvk.Version
	s.Kind = gvk.Kind

	cardinalityTracker := NewCardinalityTracker(storeCardinalityLimit, warningRatio)

	for _, family := range metricFamilies {
		if family.CardinalityLimit > 0 {
			cardinalityTracker.SetFamilyThreshold(family.Name, family.CardinalityLimit)
		}
	}

	s.SetCardinalityTracker(cardinalityTracker)

	startReflector(ctx, listerwatcher, gvkWithR, s)

	return s
}

func startReflector(ctx context.Context, lw cache.ListerWatcher, gvkWithR gvkr, s *StoreType) {
	wrapper := &unstructured.Unstructured{}
	wrapper.SetGroupVersionKind(gvkWithR.GroupVersionKind)

	reflector := cache.NewReflectorWithOptions(lw, wrapper, s, cache.ReflectorOptions{
		Name: fmt.Sprintf("%#q reflector", gvkWithR.GroupVersionResource.String()),
	})

	go reflector.Run(ctx.Done())
}

func buildLW(
	ctx context.Context,
	dynamicClientset dynamic.Interface,
	labelSelector string,
	fieldSelector string,
	gvr schema.GroupVersionResource,
) cache.ListerWatcher {
	lwo := metav1.ListOptions{
		LabelSelector: labelSelector,
		FieldSelector: fieldSelector,
	}

	listWatcher := &cache.ListWatch{
		ListFunc: func(opts metav1.ListOptions) (runtime.Object, error) {
			opts.LabelSelector = lwo.LabelSelector
			opts.FieldSelector = lwo.FieldSelector

			o, err := dynamicClientset.Resource(gvr).List(ctx, opts)
			if err != nil {
				err = fmt.Errorf("error listing %s with options %v: %w", gvr.String(), opts, err)
			}

			return o, err
		},
		WatchFunc: func(opts metav1.ListOptions) (watch.Interface, error) {
			opts.LabelSelector = lwo.LabelSelector
			opts.FieldSelector = lwo.FieldSelector

			o, err := dynamicClientset.Resource(gvr).Watch(ctx, opts)
			if err != nil {
				err = fmt.Errorf("error watching %s with options %v: %w", gvr.String(), opts, err)
			}

			return o, err
		},
	}

	// Let the reflector know whether dynamicClientset actually supports
	// streaming watch-lists (e.g. fake clients used in tests do not), so it
	// doesn't hang waiting for an initial-events-end bookmark that will
	// never arrive.
	return cache.ToListWatcherWithWatchListSemantics(listWatcher, dynamicClientset)
}
