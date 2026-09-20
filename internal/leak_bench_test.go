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
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// newCardinalityBenchController builds a Controller with just enough state
// to exercise processDelete/handleEvent's delete path, standalone from any
// other test file's fixtures, so this file can be dropped into either side
// of PR #67 unmodified to compare pre-fix vs post-fix behavior.
func newCardinalityBenchController() *Controller {
	return &Controller{
		globalCardinalityManager: NewGlobalCardinalityManager(0, 0, 0),
		metrics: metrics{
			resourcesMonitored:       prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "resources_monitored_info", Help: "test"}, []string{"namespace", "name"}),
			eventsProcessed:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "events_processed_total", Help: "test"}, []string{"namespace", "name", "event_type", "status"}),
			resourceCardinality:      prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "resource_cardinality", Help: "test"}, []string{"namespace", "name"}),
			resourceCardinalityLimit: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "resource_cardinality_limit", Help: "test"}, []string{"namespace", "name"}),
			storeCardinality:         prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "store_cardinality", Help: "test"}, []string{"namespace", "name", "store"}),
			storeCardinalityLimit:    prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "store_cardinality_limit", Help: "test"}, []string{"namespace", "name", "store"}),
			familyCardinality:        prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "family_cardinality", Help: "test"}, []string{"namespace", "name", "store", "family"}),
			familyCardinalityLimit:   prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "family_cardinality_limit", Help: "test"}, []string{"namespace", "name", "store", "family"}),
			duplicateStores:          prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "duplicate_stores", Help: "test"}, []string{"namespace", "name", "store"}),
			duplicateFamilies:        prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "duplicate_families", Help: "test"}, []string{"namespace", "name", "family"}),
			globalCardinality:        prometheus.NewGauge(prometheus.GaugeOpts{Name: "global_cardinality", Help: "test"}),
			globalCardinalityLimit:   prometheus.NewGauge(prometheus.GaugeOpts{Name: "global_cardinality_limit", Help: "test"}),
		},
	}
}

// seedResourceCardinality simulates what configurer.build + processAddOrUpdate
// leave behind for one RMM with one store and one family: a StoreType with a
// populated CardinalityTracker, plus the resourcesMonitored/storeCardinality/
// familyCardinality/duplicateStores/duplicateFamilies series that
// aggregateStoreCardinality+updateCardinalityMetrics produce for it.
func seedResourceCardinality(c *Controller, stores *sync.Map, namespace, name string, uid types.UID) *v1alpha1.ResourceMetricsMonitor {
	resource := &v1alpha1.ResourceMetricsMonitor{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, UID: uid},
	}

	store := &StoreType{Group: "example.com", Version: "v1", Kind: "Widget"}
	tracker := NewCardinalityTracker(1000, 0.8)
	tracker.SetFamilyThreshold("widget_info", 500)
	tracker.Update(uid, map[string]int64{"widget_info": 10})
	store.SetCardinalityTracker(tracker)

	storesList := []*StoreType{store}
	stores.Store(uid, storesList)

	agg := c.aggregateStoreCardinality(storesList, resource)
	c.updateCardinalityMetrics(resource, agg)

	storeID := store.GetStoreIdentifier()
	c.duplicateStores.WithLabelValues(namespace, name, storeID).Set(0)
	c.duplicateFamilies.WithLabelValues(namespace, name, "widget_info").Set(0)
	c.resourcesMonitored.WithLabelValues(namespace, name).Set(1)

	return resource
}

// TestDeleteCycle_NoLeak runs many create+delete cycles of distinct RMMs
// (distinct namespace/name/UID per iteration, mirroring a fleet of
// short-lived RMMs churning in a real cluster) directly through
// processDelete, then asserts every cardinality-related gauge is back to
// zero series. Before PR #67, processDelete never removed storeCardinality,
// storeCardinalityLimit, familyCardinality, familyCardinalityLimit,
// duplicateStores or duplicateFamilies, so each iteration left one more
// label combination behind permanently; this test fails against the
// pre-fix processDelete (see BenchmarkDeleteCycle for the routing half of
// the bug, fixed in handleEvent/syncHandler).
func TestDeleteCycle_NoLeak(t *testing.T) {
	t.Parallel()

	c := newCardinalityBenchController()

	var stores sync.Map

	const n = 500
	for i := range n {
		namespace, name := "default", fmt.Sprintf("rmm-%d", i)
		uid := types.UID(fmt.Sprintf("uid-%d", i))

		resource := seedResourceCardinality(c, &stores, namespace, name, uid)

		if err := c.processDelete(&stores, resource); err != nil {
			t.Fatalf("processDelete: %v", err)
		}
	}

	for name, vec := range map[string]*prometheus.GaugeVec{
		"store_cardinality":          c.storeCardinality,
		"store_cardinality_limit":    c.storeCardinalityLimit,
		"family_cardinality":         c.familyCardinality,
		"family_cardinality_limit":   c.familyCardinalityLimit,
		"duplicate_stores":           c.duplicateStores,
		"duplicate_families":         c.duplicateFamilies,
		"resource_cardinality":       c.resourceCardinality,
		"resource_cardinality_limit": c.resourceCardinalityLimit,
	} {
		if got := testutil.CollectAndCount(vec); got != 0 {
			t.Errorf("%s: %d leaked series remain after %d create+delete cycles, want 0", name, got, n)
		}
	}
}

// TestDeleteCycle_NoGoroutineLeak runs many create+delete cycles through the
// full handleEvent path (the production entry point for deleteEvent) and
// asserts the goroutine count doesn't grow. processAddOrUpdate spawns a
// background goroutine per resource to poll cardinality status, but the
// delete path added/fixed in PR #67 (processDelete, and handleEvent's
// deleteEvent branch) doesn't start any goroutines of its own, so this is
// mostly a guard against a future regression rather than evidence of a
// pre-fix leak — deliberately not run with t.Parallel, since
// runtime.NumGoroutine reflects the whole test binary and would be noisy
// alongside concurrently running subtests.
func TestDeleteCycle_NoGoroutineLeak(t *testing.T) {
	c := newCardinalityBenchController()

	runtime.GC()
	before := runtime.NumGoroutine()

	const n = 200
	for i := range n {
		var stores sync.Map

		namespace, name := "default", fmt.Sprintf("rmm-goroutine-%d", i)
		uid := types.UID(fmt.Sprintf("uid-goroutine-%d", i))

		resource := seedResourceCardinality(c, &stores, namespace, name, uid)

		if err := c.handleEvent(context.Background(), &stores, deleteEvent.String(), resource); err != nil {
			t.Fatalf("handleEvent: %v", err)
		}
	}

	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()

	if after > before {
		t.Errorf("goroutine count grew from %d to %d after %d create+delete cycles", before, after, n)
	}
}

// BenchmarkProcessDelete profiles just the cleanup step (stores.Delete +
// gauge teardown), which is safe to run unmodified against both sides of
// PR #67: unlike handleEvent, it never touches rsmClientset. This isolates
// the CPU/alloc cost PR #67 *adds* to a delete (the new per-store,
// per-family gauge cleanup loop) from the network calls it *removes*
// (below).
func BenchmarkProcessDelete(b *testing.B) {
	c := newCardinalityBenchController()

	b.ReportAllocs()

	for i := 0; b.Loop(); i++ {
		var stores sync.Map

		namespace, name := "default", fmt.Sprintf("rmm-%d", i)
		uid := types.UID(fmt.Sprintf("uid-%d", i))

		resource := seedResourceCardinality(c, &stores, namespace, name, uid)

		if err := c.processDelete(&stores, resource); err != nil {
			b.Fatalf("processDelete: %v", err)
		}
	}
}

// BenchmarkDeleteCycle profiles a full create+delete cycle through
// handleEvent (the routing entry point deleteEvent goes through in
// production). This only runs post-fix: pre-fix, handleEvent
// unconditionally calls validateAndPrepareResource first, which
// unconditionally dereferences rsmClientset — a hard, avoidable runtime
// dependency on a live API server that this offline benchmark deliberately
// has none of (see newCardinalityBenchController), so it panics rather than
// producing a number. That asymmetry is itself the point: pre-fix, there is
// no way to locally benchmark a delete's CPU cost at all, because the cost
// that dominates it is a network round trip, not CPU — which is also why a
// CPU pprof profile isn't the right instrument for what this PR removes.
func BenchmarkDeleteCycle(b *testing.B) {
	c := newCardinalityBenchController()

	b.ReportAllocs()

	for i := 0; b.Loop(); i++ {
		var stores sync.Map

		namespace, name := "default", fmt.Sprintf("rmm-%d", i)
		uid := types.UID(fmt.Sprintf("uid-%d", i))

		resource := seedResourceCardinality(c, &stores, namespace, name, uid)

		if err := c.handleEvent(context.Background(), &stores, deleteEvent.String(), resource); err != nil {
			b.Fatalf("handleEvent: %v", err)
		}
	}
}
