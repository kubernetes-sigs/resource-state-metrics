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
	"math"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestWriteMetricTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		resolvedLabelKeys   []string
		resolvedLabelValues []string
		expected            string
	}{
		{
			name:                "empty label keys and values",
			resolvedLabelKeys:   []string{},
			resolvedLabelValues: []string{},
			expected:            "{group=\"group\",version=\"version\",kind=\"kind\",name=\"test-name\",namespace=\"\"} 42.000000\n",
		},
		{
			name:                "multiple label keys and values",
			resolvedLabelKeys:   []string{"key1", "key2"},
			resolvedLabelValues: []string{"value1", "value2"},
			expected:            "{key1=\"value1\",key2=\"value2\",group=\"group\",version=\"version\",kind=\"kind\",name=\"test-name\",namespace=\"\"} 42.000000\n",
		},
		{
			name:                "escaped label values",
			resolvedLabelKeys:   []string{"key1"},
			resolvedLabelValues: []string{"value1\nvalue2"},
			expected:            "{key1=\"value1\\nvalue2\",group=\"group\",version=\"version\",kind=\"kind\",name=\"test-name\",namespace=\"\"} 42.000000\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var writer strings.Builder
			if err := writeMetricTo(&writer, "group", "version", "kind", "", "test-name", "42", tt.resolvedLabelKeys, tt.resolvedLabelValues, MetricKindDefault); err != nil {
				t.Fatal(err)
			}

			if got := writer.String(); got != tt.expected {
				t.Errorf("%s", cmp.Diff(got, tt.expected))
			}
		})
	}
}

func TestValidateValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		floatVal float64
		kind     MetricKind
		wantErr  bool
	}{
		{
			name:     "gauge allows NaN",
			floatVal: math.NaN(),
			kind:     MetricKindGauge,
			wantErr:  false,
		},
		{
			name:     "gauge allows negative value",
			floatVal: -1,
			kind:     MetricKindGauge,
			wantErr:  false,
		},
		{
			name:     "counter allows non-negative value",
			floatVal: 1,
			kind:     MetricKindCounter,
			wantErr:  false,
		},
		{
			name:     "counter rejects NaN",
			floatVal: math.NaN(),
			kind:     MetricKindCounter,
			wantErr:  true,
		},
		{
			name:     "counter rejects negative value",
			floatVal: -1,
			kind:     MetricKindCounter,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateValue(tt.floatVal, tt.kind)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateValue(%v, %v) error = %v, wantErr %v", tt.floatVal, tt.kind, err, tt.wantErr)
			}
		})
	}
}
