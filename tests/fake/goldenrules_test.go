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
This test performs general and conformance testing for ResourceMetricsMonitor
behavior as seen with the ResourceStateMetrics controller.

It verifies feature parity with KubeStateMetrics'
CustomResourceStateMetrics feature-set, by deploying a set of golden
ResourceMetricsMonitor configurations, each reflecting an existing
KubeStateMetrics' CustomResourceStateMetrics configuration, and validating
that:
* there are no errors, and,
* the expected metrics are emitted with the expected labelsets.

Certain behaviors may differ under the ResourceStateMetrics controller, owing
to them simply making more sense generally, and will be documented in their
respective golden configuration files.
*/

package fake

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kubernetes-sigs/resource-state-metrics/internal"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	"github.com/kubernetes-sigs/resource-state-metrics/tests/fake/framework"
	"github.com/kubernetes-sigs/resource-state-metrics/tests/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// TestGoldenRules tests all golden rules for all resolvers.
func TestGoldenRules(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Pre-load RMMs from golden rules to work around the fact that fake clients
	// don't emit watch events for objects created after informers start, so RMMs
	// must be pre-populated before the controller's informer initializes.
	// Pin CreatedAtEpoch so _created timestamps are deterministic across test runs.
	internal.CreatedAtEpoch = "0"

	initialRMMs, err := framework.LoadRMMsFromGoldenRules(ctx)
	if err != nil {
		t.Fatalf("Failed to load RMMs from golden rules: %v", err)
	}

	f := framework.NewInforming(ctx, initialRMMs...)

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

			// The dynamic client needs to know the List kind for each GVR to
			// properly handle list operations. This is typically the singular Kind
			// with "List" appended. This is also the reason why we aren't just
			// passing the updated scheme to the dynamic client, as it doesn't have
			// the necessary type information to derive the List kinds on its own.
			// Regardless, we still update the scheme for other clients that may need it.
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

	for _, resolverType := range []v1alpha1.ResolverType{
		v1alpha1.ResolverTypeUnstructured,
		v1alpha1.ResolverTypeCEL,
		v1alpha1.ResolverTypeStarlark,
	} {
		t.Run(string(resolverType), func(t *testing.T) {
			t.Parallel()
			testResolver(ctx, t, f, resolverType)
		})
	}
}

// applyCRDManifests applies only CRD manifests from the manifest directories.
func applyCRDManifests(ctx context.Context, t *testing.T, f *framework.Framework) error {
	t.Helper()

	crdFiles, _, err := testutil.GetCRDAndCRManifests(
		[]string{"../manifests", "../../manifests"},
		[]string{"cluster-role", "deployment", "service", "service-account"},
	)
	if err != nil {
		return fmt.Errorf("failed to get manifest files: %w", err)
	}

	for _, path := range crdFiles {
		if _, err := f.CreateCRDFromYAML(ctx, path); err != nil {
			return fmt.Errorf("failed to create CRD from %s: %w", path, err)
		}
	}

	return nil
}

// applyCRManifests applies only CR manifests (non-CRD) from the manifest directories.
func applyCRManifests(ctx context.Context, t *testing.T, f *framework.Framework) error {
	t.Helper()

	_, otherFiles, err := testutil.GetCRDAndCRManifests(
		[]string{"../manifests", "../../manifests"},
		[]string{"cluster-role", "deployment", "service", "service-account"},
	)
	if err != nil {
		return fmt.Errorf("failed to get manifest files: %w", err)
	}

	for _, path := range otherFiles {
		if _, err := f.ApplyCRFromYAML(ctx, path); err != nil {
			return fmt.Errorf("failed to apply CR from %s: %w", path, err)
		}
	}

	return nil
}

// testResolver tests all golden rules for a specific resolver.
func testResolver(ctx context.Context, t *testing.T, f *framework.Framework, resolverType v1alpha1.ResolverType) {
	t.Helper()

	files := testutil.GetGoldenRuleFiles("../golden", []v1alpha1.ResolverType{resolverType})

	if len(files) == 0 {
		t.Fatalf("No golden rule files found")

		return
	}

	for _, file := range files {
		goldenRules, err := testutil.GoldenRulesFromYAML(ctx, file)
		if err != nil {
			t.Fatalf("Failed to load golden rules from %s: %v", file, err)
		}

		for _, rule := range goldenRules {
			t.Run(rule.Name, func(t *testing.T) {
				testGoldenRule(ctx, t, f, rule)
			})
		}
	}
}

// testGoldenRule tests a single golden rule.
func testGoldenRule(ctx context.Context, t *testing.T, f *framework.Framework, goldenRule *testutil.GoldenRule) {
	t.Helper()

	if goldenRule.In == nil {
		t.Skipf("Golden rule has no input resource defined, skipping")

		return
	}

	// RMMs are pre-loaded when creating the framework, so only apply non-RMM resources
	if goldenRule.In != nil && goldenRule.In.GetKind() != testutil.ResourceMetricsMonitorKind {
		_, err := f.ApplyCRUnstructured(ctx, goldenRule.In)
		if err != nil {
			t.Fatalf("Failed to apply input resource: %v", err)
		}
	}

	// Wait for controller to process resources and reflectors to sync
	time.Sleep(5 * framework.LongTimeInterval)

	if len(goldenRule.Metrics) == 0 {
		t.Fatalf("Golden rule has no expected output metrics defined")
	}

	if err := f.CompareMainMetrics(goldenRule.Metrics); err != nil {
		t.Errorf("Metric comparison failed: %v", err)
	}

	if goldenRule.Status == nil {
		t.Fatalf("Golden rule has no expected status defined")
	}

	if err := testutil.WaitForRMMStatus(
		ctx,
		goldenRule.Status,
		func(ctx context.Context) (*v1alpha1.ResourceMetricsMonitorStatus, error) {
			rmm, err := f.RSMClient.ResourceStateMetricsV1alpha1().
				ResourceMetricsMonitors(goldenRule.In.GetNamespace()).
				Get(ctx, goldenRule.In.GetName(), metav1.GetOptions{})
			if err != nil {
				return nil, err
			}

			return &rmm.Status, nil
		},
		10*time.Second,
		framework.ShortTimeInterval,
	); err != nil {
		t.Errorf("Status mismatch: %v", err)
	}
}
