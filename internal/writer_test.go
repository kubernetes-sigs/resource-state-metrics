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
	"bytes"
	"strings"
	"testing"

	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"k8s.io/apimachinery/pkg/types"
)

func strPtr(s string) *string { return &s }

func float64Ptr(f float64) *float64 { return &f }

func TestMetricsWriter_writeAllTo(t *testing.T) {
	t.Parallel()

	gaugeType := dto.MetricType_GAUGE

	tests := []struct {
		name            string
		m               metricsWriter
		expectedEmpty   bool
		expectedContain string
	}{
		{
			name:          "empty writer (no stores)",
			m:             metricsWriter{contentType: expfmt.NewFormat(expfmt.TypeTextPlain)},
			expectedEmpty: true,
		},
		{
			name: "store with no families produces no output",
			m: metricsWriter{
				contentType: expfmt.NewFormat(expfmt.TypeTextPlain),
				stores: []*StoreType{
					{
						Families: []*FamilyType{},
						metrics:  map[types.UID][]*dto.MetricFamily{},
					},
				},
			},
			expectedEmpty: true,
		},
		{
			name: "store with families but no metrics produces no output",
			m: metricsWriter{
				contentType: expfmt.NewFormat(expfmt.TypeTextPlain),
				stores: []*StoreType{
					{
						Families: []*FamilyType{
							{Family: v1alpha1.Family{Name: "test_metric", Help: "A test metric"}},
						},
						metrics: map[types.UID][]*dto.MetricFamily{
							"uid1": {nil},
						},
					},
				},
			},
			expectedEmpty: true,
		},
		{
			name: "single family with one object produces output",
			m: metricsWriter{
				contentType: expfmt.NewFormat(expfmt.TypeTextPlain),
				stores: []*StoreType{
					{
						Families: []*FamilyType{
							{Family: v1alpha1.Family{Name: "test_metric", Help: "A test metric"}},
						},
						metrics: map[types.UID][]*dto.MetricFamily{
							"uid1": {
								{
									Name: strPtr("kube_customresource_test_metric"),
									Help: strPtr("A test metric"),
									Type: &gaugeType,
									Metric: []*dto.Metric{
										{
											Gauge: &dto.Gauge{Value: float64Ptr(1)},
											Label: []*dto.LabelPair{
												{Name: strPtr("name"), Value: strPtr("obj1")},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedContain: "kube_customresource_test_metric",
		},
		{
			name: "single family with two objects merges output",
			m: metricsWriter{
				contentType: expfmt.NewFormat(expfmt.TypeTextPlain),
				stores: []*StoreType{
					{
						Families: []*FamilyType{
							{Family: v1alpha1.Family{Name: "test_metric", Help: "A test metric"}},
						},
						metrics: map[types.UID][]*dto.MetricFamily{
							"uid1": {
								{
									Name: strPtr("kube_customresource_test_metric"),
									Help: strPtr("A test metric"),
									Type: &gaugeType,
									Metric: []*dto.Metric{
										{
											Gauge: &dto.Gauge{Value: float64Ptr(1)},
											Label: []*dto.LabelPair{
												{Name: strPtr("name"), Value: strPtr("obj1")},
											},
										},
									},
								},
							},
							"uid2": {
								{
									Name: strPtr("kube_customresource_test_metric"),
									Help: strPtr("A test metric"),
									Type: &gaugeType,
									Metric: []*dto.Metric{
										{
											Gauge: &dto.Gauge{Value: float64Ptr(2)},
											Label: []*dto.LabelPair{
												{Name: strPtr("name"), Value: strPtr("obj2")},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedContain: "kube_customresource_test_metric",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := &bytes.Buffer{}
			if err := tt.m.writeStores(w); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got := w.String()

			if tt.expectedEmpty && got != "" {
				t.Fatalf("expected empty output, got: %q", got)
			}

			if tt.expectedContain != "" && !strings.Contains(got, tt.expectedContain) {
				t.Fatalf("expected output to contain %q, got: %q", tt.expectedContain, got)
			}
		})
	}
}
