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
	"testing"
	"time"

	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
)

func TestWaitForRMMStatus(t *testing.T) {
	t.Parallel()

	expected := &v1alpha1.ResourceMetricsMonitorStatus{}

	err := WaitForRMMStatus(
		context.Background(),
		expected,
		func(context.Context) (*v1alpha1.ResourceMetricsMonitorStatus, error) {
			return &v1alpha1.ResourceMetricsMonitorStatus{}, nil
		},
		time.Second,
		time.Millisecond,
	)
	if err != nil {
		t.Fatalf("WaitForRMMStatus() error = %v", err)
	}
}

func TestWaitForRMMStatusHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := WaitForRMMStatus(
		ctx,
		&v1alpha1.ResourceMetricsMonitorStatus{},
		func(context.Context) (*v1alpha1.ResourceMetricsMonitorStatus, error) {
			t.Fatal("getStatus called after context cancellation")

			return &v1alpha1.ResourceMetricsMonitorStatus{}, nil
		},
		time.Second,
		time.Millisecond,
	)
	if err == nil {
		t.Fatal("WaitForRMMStatus() error = nil, want cancellation error")
	}
}
