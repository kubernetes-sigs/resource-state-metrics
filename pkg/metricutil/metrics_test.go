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
package metricutil

import (
	"strings"
	"testing"
)

func TestIsValidMetricKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind string
		want bool
	}{
		{name: "gauge", kind: "gauge", want: true},
		{name: "counter", kind: "counter", want: true},
		{name: "empty", kind: "", want: false},
		{name: "unknown", kind: "histogram", want: false},
		{name: "case sensitive", kind: "Gauge", want: false},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := IsValidMetricKind(testCase.kind); got != testCase.want {
				t.Fatalf("IsValidMetricKind(%q) = %v, want %v", testCase.kind, got, testCase.want)
			}
		})
	}
}

func TestSupportedMetricKindsString(t *testing.T) {
	t.Parallel()

	got := SupportedMetricKindsString()

	for _, kind := range SupportedMetricKinds {
		quoted := "'" + string(kind) + "'"
		if !strings.Contains(got, quoted) {
			t.Fatalf("SupportedMetricKindsString() = %q, missing %s", got, quoted)
		}
	}
}

func TestSanitizeLabelKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "already valid", key: "app_name", want: "app_name"},
		{name: "slash becomes underscore", key: "env/type", want: "env_type"},
		{name: "dots and dashes", key: "app.kubernetes.io/name", want: "app_kubernetes_io_name"},
		{name: "leading digit", key: "1foo", want: "_foo"},
		{name: "empty", key: "", want: ""},
		{name: "only underscores", key: "___", want: "___"},
		{name: "spaces", key: "a b c", want: "a_b_c"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := SanitizeLabelKey(testCase.key); got != testCase.want {
				t.Fatalf("SanitizeLabelKey(%q) = %q, want %q", testCase.key, got, testCase.want)
			}
		})
	}
}
