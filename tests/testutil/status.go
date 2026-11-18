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

package testutil

import (
	"context"
	"fmt"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WaitForRMMStatus polls getStatus until it matches expected or the timeout expires.
func WaitForRMMStatus(
	ctx context.Context,
	expected *v1alpha1.ResourceMetricsMonitorStatus,
	getStatus func(context.Context) (*v1alpha1.ResourceMetricsMonitorStatus, error),
	timeout time.Duration,
	pollInterval time.Duration,
) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var (
		lastDiff string
		lastErr  error
	)

	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf(
					"timed out waiting for ResourceMetricsMonitor status: %w (last get error: %w)",
					ctx.Err(),
					lastErr,
				)
			}

			return fmt.Errorf("timed out waiting for ResourceMetricsMonitor status: %w (last diff: %s)", ctx.Err(), lastDiff)
		case <-ticker.C:
			actual, err := getStatus(ctx)
			if err != nil {
				lastErr = err

				continue
			}

			lastErr = nil

			lastDiff = cmp.Diff(expected, actual, statusComparisonOptions())
			if lastDiff == "" {
				return nil
			}
		}
	}
}

func statusComparisonOptions() cmp.Options {
	return cmp.Options{
		cmpopts.IgnoreFields(v1alpha1.CardinalityStatus{}, "PerStore", "PerFamily", "CutoffFamilies", "LastUpdated"),
		cmpopts.IgnoreFields(metav1.Condition{}, "LastTransitionTime", "ObservedGeneration", "Message"),
	}
}
