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
	"sync"
	"testing"

	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
)

// newTestController returns a Controller with just enough state initialized
// to exercise handleEvent's delete path: the metrics processDelete touches,
// and the cardinality manager. It deliberately leaves rsmClientset nil, since
// a correctly handled delete must never need to reach the API server.
func newTestController() *Controller {
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
		},
	}
}

// TestHandleEvent_DeleteTearsDownStore reproduces the "deleting an RMM never
// tears down its stores" issue: a store is seeded under a resource's UID,
// then a deleteEvent is fired through handleEvent for a resource the API
// server no longer has (rsmClientset is nil here, so any Get/UpdateStatus
// call would panic, exactly mirroring what a real NotFound would trigger).
// Before the fix, handleEvent always called validateAndPrepareResource
// first, which would fail for a deleted resource and return before
// processDelete ever ran, leaking the store.
func TestHandleEvent_DeleteTearsDownStore(t *testing.T) {
	t.Parallel()

	c := newTestController()

	uid := uuid.NewUUID()
	resource := &v1alpha1.ResourceMetricsMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "deleted-rmm",
			Namespace: "default",
			UID:       uid,
		},
	}

	var stores sync.Map
	stores.Store(uid, []*StoreType{})

	if err := c.handleEvent(context.Background(), &stores, deleteEvent.String(), resource); err != nil {
		t.Fatalf("handleEvent returned an error: %v", err)
	}

	if _, ok := stores.Load(uid); ok {
		t.Fatalf("store for UID %q still present after delete event; handleEvent did not tear it down", uid)
	}
}
