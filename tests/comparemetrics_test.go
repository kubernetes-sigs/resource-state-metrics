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

package tests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/kubernetes-sigs/resource-state-metrics/internal"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	"github.com/kubernetes-sigs/resource-state-metrics/tests/framework"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// TestCompareMetrics validates metric output against all golden rules using
// envtest. Unlike TestGoldenRules which uses fake clients, this test exercises
// real watch events, CRD discovery, and status updates.
//
// Two modes of operation:
//
//	KUBEBUILDER_ASSETS=<path>    – starts a local kube-apiserver + etcd  (make test_compare_metrics)
//	USE_EXISTING_CLUSTER=true    – connects to the current KUBECONFIG    (make test_compare_metrics_kind)
func TestCompareMetrics(t *testing.T) {
	useExisting := os.Getenv("USE_EXISTING_CLUSTER") == "true"

	if os.Getenv("KUBEBUILDER_ASSETS") == "" && !useExisting {
		t.Skip("KUBEBUILDER_ASSETS or USE_EXISTING_CLUSTER not set; run with: make test_compare_metrics")
	}

	ctx, cancel := context.WithCancel(context.Background())

	internal.CreatedAtEpoch = "0"

	// When UseExistingCluster is true, envtest skips starting local binaries
	// and connects to whatever KUBECONFIG points at instead. CRDs are still
	// installed from CRDDirectoryPaths in both modes.
	testEnv := &envtest.Environment{
		UseExistingCluster: &useExisting,
		CRDDirectoryPaths: []string{
			filepath.Join("..", "manifests"),
			filepath.Join("manifests", "custom-resource-definition"),
		},
	}

	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("Failed to start envtest: %v", err)
	}

	defer func() {
		cancel()

		if stopErr := testEnv.Stop(); stopErr != nil {
			t.Errorf("Failed to stop envtest: %v", stopErr)
		}
	}()

	// Build the framework from the envtest rest.Config.
	f, err := framework.NewForConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to create framework: %v", err)
	}

	// Apply custom resources from test manifests.
	_, crFiles, err := getCRDandNonCRDManifests(t)
	if err != nil {
		t.Fatalf("Failed to list manifest files: %v", err)
	}

	for _, path := range crFiles {
		if _, err := f.ApplyCRFromYAML(ctx, path); err != nil {
			t.Fatalf("Failed to apply CR from %s: %v", path, err)
		}
	}

	// Start the controller.
	if err := f.Start(ctx, 1); err != nil {
		t.Fatalf("Failed to start controller: %v", err)
	}

	// Apply ResourceMetricsMonitors from golden rules via the real RSM client.
	// Unlike fake clients, the real informer emits watch events for these,
	// testing the full event -> reconcile -> metrics pipeline.
	type goldenEntry struct {
		file string
		rule *framework.GoldenRule
	}

	goldenFiles := framework.GetGoldenRuleFiles([]v1alpha1.ResolverType{
		v1alpha1.ResolverTypeUnstructured,
		v1alpha1.ResolverTypeCEL,
		v1alpha1.ResolverTypeStarlark,
	})

	rules := make([]goldenEntry, 0, len(goldenFiles))

	for _, file := range goldenFiles {
		rule, err := framework.GoldenRuleFromYAML(ctx, file)
		if err != nil {
			t.Fatalf("Failed to load golden rule from %s: %v", file, err)
		}

		if err := framework.ValidateUnstructuredGoldenRule(rule); err != nil {
			t.Fatalf("Golden rule %s is invalid: %v", file, err)
		}

		var rmm v1alpha1.ResourceMetricsMonitor
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(rule.In.Object, &rmm); err != nil {
			t.Fatalf("Failed to convert golden rule input to RMM in %s: %v", file, err)
		}

		if _, err := f.RSMClient.ResourceStateMetricsV1alpha1().ResourceMetricsMonitors(rmm.Namespace).
			Create(ctx, &rmm, metav1.CreateOptions{}); err != nil {
			t.Fatalf("Failed to create RMM %s/%s: %v", rmm.Namespace, rmm.Name, err)
		}

		rules = append(rules, goldenEntry{file: file, rule: rule})
	}

	// Wait for all RMMs to be processed.
	for _, e := range rules {
		if _, err := f.WaitForRMMProcessed(ctx, e.rule.In.GetNamespace(), e.rule.In.GetName(), 30*time.Second); err != nil {
			t.Fatalf("Timed out waiting for RMM %s/%s: %v", e.rule.In.GetNamespace(), e.rule.In.GetName(), err)
		}
	}

	// Allow reflectors to sync and metrics to settle.
	time.Sleep(5 * time.Second)

	// Validate metrics and status per golden rule.
	// Both use polling because cardinality updates propagate asynchronously
	// through the real API server, unlike the instant fake-client path.
	statusCmpOpts := cmp.Options{
		cmpopts.IgnoreFields(v1alpha1.CardinalityStatus{}, "PerStore", "PerFamily", "CutoffFamilies", "LastUpdated"),
		cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime", "ObservedGeneration", "Message"),
	}

	for _, e := range rules {
		testName := strings.TrimSuffix(filepath.Base(e.file), ".yaml")

		t.Run(testName, func(t *testing.T) {
			// Cardinality cutoff golden rules (ThresholdsExceeded=true) are skipped
			// because the cardinality status update goroutine races with store
			// initialization when using real watch events. These edge cases are
			// already validated by TestGoldenRules with fake clients.
			if e.rule.Status != nil && e.rule.Status.Cardinality != nil && e.rule.Status.Cardinality.ThresholdsExceeded {
				t.Skipf("Skipping cardinality cutoff golden rule (validated by TestGoldenRules)")
			}

			envtestPollCompareMetrics(t, f, e.rule.Metrics)

			if e.rule.Status != nil {
				envtestPollStatus(t, ctx, f, e.rule, statusCmpOpts)
			}
		})
	}
}

// envtestPollCompareMetrics polls f.CompareMainMetrics until the scraped
// metrics match the expected golden output or the timeout is reached.
func envtestPollCompareMetrics(t *testing.T, f *framework.Framework, expectedMetricLines []string) {
	t.Helper()

	deadline := time.After(15 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastErr error

	for {
		select {
		case <-deadline:
			t.Errorf("Metrics mismatch (timed out): %v", lastErr)

			return
		case <-ticker.C:
			lastErr = f.CompareMainMetrics(expectedMetricLines)
			if lastErr == nil {
				return
			}
		}
	}
}

// envtestPollStatus polls until the RMM status matches the expected golden output.
// Cardinality status is updated asynchronously by a background goroutine after
// the Processed condition is set.
func envtestPollStatus(t *testing.T, ctx context.Context, f *framework.Framework, rule *framework.GoldenRule, opts cmp.Options) {
	t.Helper()

	deadline := time.After(15 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastDiff string

	for {
		select {
		case <-deadline:
			t.Errorf("Status mismatch (-expected +actual):\n%s", lastDiff)

			return
		case <-ctx.Done():
			t.Errorf("Context cancelled; last status diff:\n%s", lastDiff)

			return
		case <-ticker.C:
			rmm, err := f.RSMClient.ResourceStateMetricsV1alpha1().
				ResourceMetricsMonitors(rule.In.GetNamespace()).
				Get(ctx, rule.In.GetName(), metav1.GetOptions{})
			if err != nil {
				continue
			}

			lastDiff = cmp.Diff(rule.Status, &rmm.Status, opts)
			if lastDiff == "" {
				return
			}
		}
	}
}
