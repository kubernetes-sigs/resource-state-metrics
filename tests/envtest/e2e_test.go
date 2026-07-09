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

package envtest

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/kubernetes-sigs/resource-state-metrics/internal"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	"github.com/kubernetes-sigs/resource-state-metrics/tests/envtest/framework"
	"github.com/kubernetes-sigs/resource-state-metrics/tests/testutil"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// TestE2EEnvtest validates metric output against all golden rules using
// envtest. Unlike TestGoldenRules which uses fake clients, this test exercises
// real watch events, CRD discovery, and status updates.
//
// Two modes of operation:
//
//	KUBEBUILDER_ASSETS=<path>    – starts a local kube-apiserver + etcd  (make test_e2e_envtest)
//	USE_EXISTING_CLUSTER=true    – connects to the current KUBECONFIG    (make test_e2e_envtest_kind)
//
//nolint:gocognit,cyclop
func TestE2EEnvtest(t *testing.T) {
	t.Parallel()

	// Mirror envtest's own parsing of USE_EXISTING_CLUSTER (strconv.ParseBool); the field is left
	// unset below so envtest remains the single source of truth.
	useExisting, _ := strconv.ParseBool(os.Getenv("USE_EXISTING_CLUSTER"))

	if os.Getenv("KUBEBUILDER_ASSETS") == "" && !useExisting {
		// Never let a missing envtest setup pass silently where coverage is expected (make targets, CI).
		if required, _ := strconv.ParseBool(os.Getenv("REQUIRE_ENVTEST")); required || os.Getenv("CI") != "" {
			t.Fatal("KUBEBUILDER_ASSETS or USE_EXISTING_CLUSTER not set; run with: make test_e2e_envtest")
		}

		t.Skip("KUBEBUILDER_ASSETS or USE_EXISTING_CLUSTER not set; run with: make test_e2e_envtest")
	}

	ctx, cancel := context.WithCancel(context.Background())

	internal.CreatedAtEpoch = "0"

	// When UseExistingCluster is true, envtest skips starting local binaries
	// and connects to whatever KUBECONFIG points at instead. CRDs are still
	// installed from CRDDirectoryPaths in both modes.
	testEnv := &envtest.Environment{
		ErrorIfCRDPathMissing: true,
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "manifests"),
			filepath.Join("..", "manifests", "custom-resource-definition"),
		},
	}

	// Registered before Start: envtest can fail after the control plane is already up
	// (e.g. CRD install), and the processes and temp dirs must still be torn down.
	// The controller must have exited before the API server goes away.
	var f *framework.Framework

	t.Cleanup(func() {
		cancel()

		if f != nil {
			if waitErr := f.WaitForControllerExit(30 * time.Second); waitErr != nil {
				t.Errorf("Controller did not shut down cleanly: %v", waitErr)
			}
		}

		if stopErr := testEnv.Stop(); stopErr != nil {
			t.Errorf("Failed to stop envtest: %v", stopErr)
		}
	})

	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("Failed to start envtest: %v", err)
	}

	// Build the framework from the envtest rest.Config.
	f, err = framework.NewForConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to create framework: %v", err)
	}

	// Apply custom resources from test manifests.
	_, crFiles, err := testutil.GetCRDAndCRManifests(
		[]string{"../manifests", "../../manifests"},
		[]string{"cluster-role", "deployment", "service", "service-account"},
	)
	if err != nil {
		t.Fatalf("Failed to list manifest files: %v", err)
	}

	for _, path := range crFiles {
		deleteCR, err := f.ApplyCRFromYAML(ctx, path)
		if err != nil {
			t.Fatalf("Failed to apply CR from %s: %v", path, err)
		}

		t.Cleanup(func() { ignoreNotFound(t, deleteCR(context.Background())) })
	}

	// Start the controller.
	if err := f.Start(ctx, 1); err != nil {
		t.Fatalf("Failed to start controller: %v", err)
	}

	// Apply ResourceMetricsMonitors from golden rules against the real API server.
	// Unlike fake clients, the real informer emits watch events for these,
	// testing the full event -> reconcile -> metrics pipeline.
	type goldenEntry struct {
		file string
		rule *testutil.GoldenRule
	}

	goldenFiles := testutil.GetGoldenRuleFiles("../golden", []v1alpha1.ResolverType{
		v1alpha1.ResolverTypeUnstructured,
		v1alpha1.ResolverTypeCEL,
		v1alpha1.ResolverTypeStarlark,
	})

	rules := make([]goldenEntry, 0, len(goldenFiles))

	for _, file := range goldenFiles {
		rulesFromFile, err := testutil.GoldenRulesFromYAML(ctx, file)
		if err != nil {
			t.Fatalf("Failed to load golden rule from %s: %v", file, err)
		}

		for _, rule := range rulesFromFile {
			if err := testutil.ValidateUnstructuredGoldenRule(rule); err != nil {
				t.Fatalf("Golden rule %s is invalid: %v", file, err)
			}

			cleanupRMM, err := f.ApplyUnstructured(ctx, rule.In)
			if err != nil {
				t.Fatalf("Failed to apply RMM %s/%s: %v", rule.In.GetNamespace(), rule.In.GetName(), err)
			}

			t.Cleanup(func() {
				if err := cleanupRMM(context.Background()); err != nil {
					t.Errorf("Failed to clean up RMM %s/%s: %v", rule.In.GetNamespace(), rule.In.GetName(), err)
				}
			})

			rules = append(rules, goldenEntry{file: file, rule: rule})
		}
	}

	// Wait for all RMMs to be processed.
	for _, e := range rules {
		if err := f.WaitForRMMProcessed(ctx, e.rule.In.GetNamespace(), e.rule.In.GetName(), 30*time.Second); err != nil {
			t.Fatalf("Timed out waiting for RMM %s/%s: %v", e.rule.In.GetNamespace(), e.rule.In.GetName(), err)
		}
	}

	// Validate metrics and status per golden rule.
	// Both use polling because cardinality updates propagate asynchronously
	// through the real API server, unlike the instant fake-client path.
	for _, e := range rules {
		t.Run(e.rule.Name, func(t *testing.T) {
			t.Parallel()

			envtestPollCompareMetrics(ctx, t, f, e.rule.Metrics)

			// Same contract as the fake suite: every golden rule must define its expected status.
			if e.rule.Status == nil {
				t.Fatalf("Golden rule %s has no expected status defined", e.rule.Name)
			}

			if err := testutil.WaitForRMMStatus(
				ctx,
				e.rule.Status,
				func(ctx context.Context) (*v1alpha1.ResourceMetricsMonitorStatus, error) {
					rmm, err := f.RSMClient.ResourceStateMetricsV1alpha1().
						ResourceMetricsMonitors(e.rule.In.GetNamespace()).
						Get(ctx, e.rule.In.GetName(), metav1.GetOptions{})
					if err != nil {
						return nil, err
					}

					return &rmm.Status, nil
				},
				15*time.Second,
				500*time.Millisecond,
			); err != nil {
				t.Errorf("Status mismatch: %v", err)
			}
		})
	}
}

// envtestPollCompareMetrics polls f.CompareMainMetrics until the scraped
// metrics match the expected golden output or the timeout is reached.
func envtestPollCompareMetrics(
	ctx context.Context,
	t *testing.T,
	f *framework.Framework,
	expectedMetricLines []string,
) {
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
		case <-ctx.Done():
			t.Errorf("Context cancelled; last metrics error: %v", lastErr)

			return
		case <-ticker.C:
			lastErr = f.CompareMainMetrics(expectedMetricLines)
			if lastErr == nil {
				return
			}
		}
	}
}

func ignoreNotFound(t *testing.T, err error) {
	t.Helper()

	if err != nil && !apierrors.IsNotFound(err) {
		t.Errorf("Cleanup failed: %v", err)
	}
}
