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
	"testing"
)

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
