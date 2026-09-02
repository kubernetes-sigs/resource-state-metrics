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

/*
This test validates that deleting a ResourceMetricsMonitor tears down its
stores end-to-end: its series must stop being served on the main metrics
endpoint, and its resources_monitored_info telemetry gauge must be cleaned
up. This can't be expressed as a tests/golden/*.yaml rule, since golden rules
describe a single static input/output pair, not a create-then-delete
sequence.
*/

package tests

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	"github.com/kubernetes-sigs/resource-state-metrics/tests/framework"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/uuid"
)

// TestResourceMetricsMonitorDeletion verifies that deleting an RMM removes
// its series from the main metrics endpoint and cleans up its
// resources_monitored_info telemetry gauge.
func TestResourceMetricsMonitorDeletion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	const (
		namespace  = "default"
		name       = "delete-teardown-test"
		familyName = "rmm_delete_teardown_test"
	)

	rmm := &v1alpha1.ResourceMetricsMonitor{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "resource-state-metrics.instrumentation.k8s-sigs.io/v1alpha1",
			Kind:       "ResourceMetricsMonitor",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       uuid.NewUUID(),
		},
		Spec: v1alpha1.ResourceMetricsMonitorSpec{
			Configuration: v1alpha1.Configuration{
				Stores: []v1alpha1.Store{
					{
						Group:    "samplecontroller.k8s.io",
						Version:  "v1beta1",
						Kind:     "Bar",
						Resource: "bars",
						Resolver: v1alpha1.ResolverTypeUnstructured,
						Families: []v1alpha1.Family{
							{
								Name: familyName,
								Help: "Test metric verifying store teardown on RMM deletion.",
								Metrics: []v1alpha1.Metric{
									{
										Labels: []v1alpha1.Label{
											{Name: "name", Value: "metadata.name"},
										},
										Value: "1",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	f := framework.NewInforming(ctx, rmm)

	if err := applyCRDManifests(ctx, t, f); err != nil {
		t.Fatalf("Failed to apply CRD manifests: %v", err)
	}

	gvrToKindListMap := make(map[schema.GroupVersionResource]string)
	indexedCRDs := f.GetIndexedCRDs()

	for _, crd := range indexedCRDs {
		for _, version := range crd.Spec.Versions {
			gv := schema.GroupVersion{Group: crd.Spec.Group, Version: version.Name}

			f.AddToScheme(func(scheme *runtime.Scheme) {
				scheme.AddKnownTypes(gv, &unstructured.Unstructured{}, &unstructured.UnstructuredList{})
			})

			gvr := schema.GroupVersionResource{
				Group:    crd.Spec.Group,
				Version:  version.Name,
				Resource: crd.Spec.Names.Plural,
			}
			gvrToKindListMap[gvr] = crd.Spec.Names.Kind + "List"
		}
	}

	f.WithDynamicClient(gvrToKindListMap)

	if err := applyCRManifests(ctx, t, f); err != nil {
		t.Fatalf("Failed to apply CR manifests: %v", err)
	}

	if err := f.Start(ctx, 1); err != nil {
		t.Fatalf("Failed to start controller: %v", err)
	}

	// Wait for the controller to process the RMM and populate the store.
	time.Sleep(5 * framework.LongTimeInterval)

	metricsOutput, err := f.FetchMainMetrics(ctx)
	if err != nil {
		t.Fatalf("Failed to fetch main metrics: %v", err)
	}

	if !strings.Contains(metricsOutput, familyName) {
		t.Fatalf("Expected metric family %q to be present before deletion, got:\n%s", familyName, metricsOutput)
	}

	// Anchored on the metric name so it can't accidentally match an unrelated
	// series that happens to share the same namespace/name label values (e.g.
	// events_processed_total, which is a historical counter and is expected
	// to persist after deletion).
	resourcesMonitoredSeries := fmt.Sprintf("resource_state_metrics_resources_monitored_info{name=%q,namespace=%q}", name, namespace)

	telemetryBefore, err := f.FetchTelemetryMetrics(ctx)
	if err != nil {
		t.Fatalf("Failed to fetch telemetry metrics: %v", err)
	}

	if !strings.Contains(telemetryBefore, resourcesMonitoredSeries) {
		t.Fatalf("Expected resources_monitored_info to reference the RMM before deletion, got:\n%s", telemetryBefore)
	}

	if err := f.RSMClient.ResourceStateMetricsV1alpha1().ResourceMetricsMonitors(namespace).
		Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("Failed to delete RMM: %v", err)
	}

	waitUntilMainMetricsExclude(ctx, t, f, familyName)

	telemetryAfter, err := f.FetchTelemetryMetrics(ctx)
	if err != nil {
		t.Fatalf("Failed to fetch telemetry metrics: %v", err)
	}

	if strings.Contains(telemetryAfter, resourcesMonitoredSeries) {
		t.Fatalf("Expected resources_monitored_info to no longer reference the deleted RMM, got:\n%s", telemetryAfter)
	}
}

// waitUntilMainMetricsExclude polls the main metrics endpoint until it no
// longer contains needle, or fails the test on timeout.
func waitUntilMainMetricsExclude(ctx context.Context, t *testing.T, f *framework.Framework, needle string) {
	t.Helper()

	pollCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	ticker := time.NewTicker(framework.ShortTimeInterval)
	defer ticker.Stop()

	var lastOutput string

	for {
		select {
		case <-pollCtx.Done():
			t.Fatalf("Timed out waiting for %q to disappear from main metrics. Last output:\n%s", needle, lastOutput)

			return
		case <-ticker.C:
			output, err := f.FetchMainMetrics(ctx)
			if err != nil {
				t.Fatalf("Failed to fetch main metrics: %v", err)
			}

			lastOutput = output

			if !strings.Contains(output, needle) {
				return
			}
		}
	}
}
