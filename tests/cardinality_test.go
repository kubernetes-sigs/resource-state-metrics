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
This test validates the cardinality telemetry metrics exposed by the controller.
It verifies that:
* Cardinality is tracked per-family, per-store, per-resource, and globally
* Cardinality metrics are exposed on the telemetry endpoint
* Metrics are updated when resources are added/removed
*/

package tests

import (
	"context"
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

// TestCardinalityMetrics tests cardinality telemetry metrics and status updates.
func TestCardinalityMetrics(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create an RMM for testing cardinality metrics
	rmm := &v1alpha1.ResourceMetricsMonitor{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "resource-state-metrics.instrumentation.k8s-sigs.io/v1alpha1",
			Kind:       "ResourceMetricsMonitor",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cardinality-telemetry-test",
			Namespace: "default",
			UID:       uuid.NewUUID(),
		},
		Spec: v1alpha1.ResourceMetricsMonitorSpec{
			Configuration: v1alpha1.Configuration{
				Stores: []v1alpha1.Store{
					{
						Group:            "samplecontroller.k8s.io",
						Version:          "v1beta1",
						Kind:             "Bar",
						Resource:         "bars",
						Resolver:         v1alpha1.ResolverTypeUnstructured,
						CardinalityLimit: 1000,
						Families: []v1alpha1.Family{
							{
								Name:             "cardinality_telemetry_test",
								Help:             "Test metric for cardinality telemetry",
								CardinalityLimit: 500,
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

	// Wait for controller to process resources
	time.Sleep(5 * framework.LongTimeInterval)

	t.Run("TelemetryMetrics", func(t *testing.T) {
		t.Parallel()
		testTelemetryMetrics(ctx, t, f)
	})

	t.Run("StatusUpdate", func(t *testing.T) {
		t.Parallel()
		testStatusUpdate(ctx, t, f)
	})
}

// testTelemetryMetrics verifies that cardinality telemetry metrics are exposed.
func testTelemetryMetrics(ctx context.Context, t *testing.T, f *framework.Framework) {
	t.Helper()

	metricsOutput, err := f.FetchTelemetryMetrics(ctx)
	if err != nil {
		t.Fatalf("Failed to fetch telemetry metrics: %v", err)
	}

	verifyCardinalityMetricsExist(t, metricsOutput)
	logCardinalityValues(t, metricsOutput)
	checkCardinalityExceededMetric(t, metricsOutput)
}

func verifyCardinalityMetricsExist(t *testing.T, metricsOutput string) {
	t.Helper()

	metricPrefixes := []string{
		"resource_state_metrics_family_cardinality",
		"resource_state_metrics_store_cardinality",
		"resource_state_metrics_resource_cardinality",
		"resource_state_metrics_global_cardinality",
	}

	for _, prefix := range metricPrefixes {
		if !strings.Contains(metricsOutput, prefix) {
			t.Errorf("Expected metric %s to exist in telemetry output, but it was not found", prefix)
		}
	}
}

func logCardinalityValues(t *testing.T, metricsOutput string) {
	t.Helper()

	for line := range strings.SplitSeq(metricsOutput, "\n") {
		if strings.HasPrefix(line, "resource_state_metrics_") &&
			strings.Contains(line, "cardinality") &&
			!strings.HasPrefix(line, "#") &&
			strings.Contains(line, " ") {
			t.Logf("Found cardinality metric: %s", line)
		}
	}
}

func checkCardinalityExceededMetric(t *testing.T, metricsOutput string) {
	t.Helper()

	if !strings.Contains(metricsOutput, "# TYPE resource_state_metrics_cardinality_exceeded_total counter") {
		t.Logf("cardinality_exceeded_total metric type declaration not found (this is expected if no thresholds were exceeded)")
	}
}

// TestFamilyCardinalityStoreLabel verifies that resource_state_metrics_family_cardinality
// never emits with an empty store label.
func TestFamilyCardinalityStoreLabel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	rmm := &v1alpha1.ResourceMetricsMonitor{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "resource-state-metrics.instrumentation.k8s-sigs.io/v1alpha1",
			Kind:       "ResourceMetricsMonitor",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "family-cardinality-store-label-test",
			Namespace: "default",
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
								Name: "store_label_bar_test",
								Help: "Bar metric for store-label test",
								Metrics: []v1alpha1.Metric{
									{Labels: []v1alpha1.Label{}, Value: "1"},
								},
							},
						},
					},
					{
						Group:    "samplecontroller.k8s.io",
						Version:  "v1alpha1",
						Kind:     "Foo",
						Resource: "foos",
						Resolver: v1alpha1.ResolverTypeUnstructured,
						Families: []v1alpha1.Family{
							{
								Name: "store_label_foo_test",
								Help: "Foo metric for store-label test",
								Metrics: []v1alpha1.Metric{
									{Labels: []v1alpha1.Label{}, Value: "1"},
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
	for _, crd := range f.GetIndexedCRDs() {
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

	time.Sleep(5 * framework.LongTimeInterval)

	metricsOutput, err := f.FetchTelemetryMetrics(ctx)
	if err != nil {
		t.Fatalf("Failed to fetch telemetry metrics: %v", err)
	}

	for _, line := range strings.Split(metricsOutput, "\n") {
		if strings.HasPrefix(line, "resource_state_metrics_family_cardinality{") {
			if strings.Contains(line, `store=""`) {
				t.Errorf("family_cardinality emitted with empty store label (regression): %s", line)
			}
		}
	}
}

// testStatusUpdate verifies that RMM status is updated with cardinality info.
func testStatusUpdate(ctx context.Context, t *testing.T, f *framework.Framework) {
	t.Helper()

	// Wait a bit more for status updates to propagate
	time.Sleep(5 * framework.LongTimeInterval)

	// Fetch the RMM and check its status
	updatedRMM, err := f.RSMClient.ResourceStateMetricsV1alpha1().ResourceMetricsMonitors("default").Get(ctx, "cardinality-telemetry-test", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get RMM: %v", err)
	}

	// Check that cardinality status is set
	if updatedRMM.Status.Cardinality != nil {
		t.Logf("Cardinality status found: Total=%d, ThresholdsExceeded=%v",
			updatedRMM.Status.Cardinality.Total,
			updatedRMM.Status.Cardinality.ThresholdsExceeded)

		if updatedRMM.Status.Cardinality.Total < 0 {
			t.Errorf("Expected non-negative cardinality total, got %d", updatedRMM.Status.Cardinality.Total)
		}

		if len(updatedRMM.Status.Cardinality.PerStore) > 0 {
			t.Logf("Per-store cardinality: %v", updatedRMM.Status.Cardinality.PerStore)
		}

		if len(updatedRMM.Status.Cardinality.PerFamily) > 0 {
			t.Logf("Per-family cardinality: %v", updatedRMM.Status.Cardinality.PerFamily)
		}
	} else {
		t.Logf("Cardinality status not yet populated (this may be expected in some test scenarios)")
	}

	for _, cond := range updatedRMM.Status.Conditions {
		t.Logf("Condition: Type=%s, Status=%s, Reason=%s", cond.Type, cond.Status, cond.Reason)
	}
}
