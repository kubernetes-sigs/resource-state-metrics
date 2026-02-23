/*
Copyright 2025 The Kubernetes resource-state-metrics Authors.

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
			expected:            "{group=\"group\",version=\"version\",kind=\"kind\"} 42.000000\n",
		},
		{
			name:                "multiple label keys and values",
			resolvedLabelKeys:   []string{"key1", "key2"},
			resolvedLabelValues: []string{"value1", "value2"},
			expected:            "{key1=\"value1\",key2=\"value2\",group=\"group\",version=\"version\",kind=\"kind\"} 42.000000\n",
		},
		{
			name:                "escaped label values",
			resolvedLabelKeys:   []string{"key1"},
			resolvedLabelValues: []string{"value1\nvalue2"},
			expected:            "{key1=\"value1\\nvalue2\",group=\"group\",version=\"version\",kind=\"kind\"} 42.000000\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var writer strings.Builder
			if err := writeMetricTo(&writer, "group", "version", "kind", "42", tt.resolvedLabelKeys, tt.resolvedLabelValues); err != nil {
				t.Fatal(err)
			}
			if got := writer.String(); got != tt.expected {
				t.Errorf("%s", cmp.Diff(got, tt.expected))
			}
		})
	}
}
