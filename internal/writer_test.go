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
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"k8s.io/apimachinery/pkg/types"
)

func familyProbeHeader() string {
	return "# HELP kube_customresource_probe Ready status\n# TYPE kube_customresource_probe gauge"
}

func familyProbeCounterHeader() string {
	return "# HELP kube_customresource_probe_sets_total Sets total\n# TYPE kube_customresource_probe_sets_total counter"
}

func TestMetricsWriter_writeAllTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		m        metricsWriter
		expected string
	}{
		{
			name:     "empty store",
			m:        metricsWriter{},
			expected: "",
		},
		{
			name: "non-empty store with same number of headers and metrics",
			m: metricsWriter{
				stores: []*StoreType{
					{
						headers: []string{"header1", "header2"},
						metrics: map[types.UID][]string{
							"uid1": {"metric1", "metric2"},
							"uid2": {"metric1", "metric2"},
						},
					},
				},
			},
			expected: "header1\nmetric1metric1header2\nmetric2metric2",
		},
		{
			name: "non-empty store with more number of headers than metrics",
			m: metricsWriter{
				stores: []*StoreType{
					{
						headers: []string{"header1", "header2", "header3"},
						metrics: map[types.UID][]string{
							"uid1": {"metric1", "metric2"},
							"uid2": {"metric1", "metric2", "metric3"},
						},
					},
				},
			},
			expected: "header1\nmetric1metric1header2\nmetric2metric2header3\nmetric3",
		},
		{
			name: "non-empty store with less number of headers than metrics",
			m: metricsWriter{
				stores: []*StoreType{
					{
						headers: []string{"header1"},
						metrics: map[types.UID][]string{
							"uid1": {"metric1", "metric2"},
							"uid2": {"metric1", "metric2"},
						},
					},
				},
			},
			expected: "header1\nmetric1metric1",
		},
		{
			name: "non-empty store with no headers",
			m: metricsWriter{
				stores: []*StoreType{
					{
						headers: []string{},
						metrics: map[types.UID][]string{
							"uid1": {"metric1", "metric1"},
							"uid2": {"metric1"},
						},
					},
				},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := &bytes.Buffer{}
			if err := tt.m.writeStores(w); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := w.String(); got != tt.expected {
				t.Fatalf("%s", cmp.Diff(got, tt.expected))
			}
		})
	}
}

func TestMetricsWriter_writeStores_dedupHeader(t *testing.T) {
	t.Parallel()

	header := familyProbeHeader()
	counterHeader := familyProbeCounterHeader()
	counterFamily := FamilyType{Family: v1alpha1.Family{Name: "probe_sets_total"}, createdAt: time.Unix(1, 500000000)}

	tests := []struct {
		name     string
		stores   []*StoreType
		expected string
	}{
		{
			name: "same family in two stores gets its header once",
			stores: []*StoreType{
				{
					headers: []string{header},
					metrics: map[types.UID][]string{
						"uid1": {"kube_customresource_probe{kind=\"ProviderConfig\"} 0\n"},
					},
				},
				{
					headers: []string{header},
					metrics: map[types.UID][]string{
						"uid1": {"kube_customresource_probe{kind=\"ProviderConfigUsage\"} 1\n"},
					},
				},
			},
			expected: header + "\n" +
				"kube_customresource_probe{kind=\"ProviderConfig\"} 0\n" +
				"kube_customresource_probe{kind=\"ProviderConfigUsage\"} 1\n",
		},
		{
			name: "different families across stores keep their own headers",
			stores: []*StoreType{
				{
					headers: []string{header},
					metrics: map[types.UID][]string{
						"uid1": {"kube_customresource_probe{kind=\"K1\"} 1\n"},
					},
				},
				{
					headers: []string{"# HELP kube_customresource_other Other status\n# TYPE kube_customresource_other gauge"},
					metrics: map[types.UID][]string{
						"uid1": {"kube_customresource_other{kind=\"K2\"} 2\n"},
					},
				},
			},
			expected: header + "\n" +
				"kube_customresource_probe{kind=\"K1\"} 1\n" +
				"# HELP kube_customresource_other Other status\n" +
				"# TYPE kube_customresource_other gauge\n" +
				"kube_customresource_other{kind=\"K2\"} 2\n",
		},
		{
			name: "counter families share their _created peripheral header only once",
			stores: []*StoreType{
				{
					headers: []string{counterHeader},
					metrics: map[types.UID][]string{
						"uid1": {"kube_customresource_probe_sets_total{kind=\"K1\"} 4\n"},
					},
					Families: []*FamilyType{&counterFamily},
				},
				{
					headers: []string{counterHeader},
					metrics: map[types.UID][]string{
						"uid1": {"kube_customresource_probe_sets_total{kind=\"K2\"} 5\n"},
					},
					Families: []*FamilyType{&counterFamily},
				},
			},
			expected: counterHeader + "\n" +
				"kube_customresource_probe_sets_total{kind=\"K1\"} 4\n" +
				"# HELP kube_customresource_probe_sets_created Time at which kube_customresource_probe_sets_total was created." +
				"\n# TYPE kube_customresource_probe_sets_created counter\n" +
				"kube_customresource_probe_sets_created 1.500000\n" +
				"kube_customresource_probe_sets_total{kind=\"K2\"} 5\n",
		},
	}

	for i := range tests {
		tt := tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := &bytes.Buffer{}
			if err := newMetricsWriter(tt.stores...).writeStores(w); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := w.String(); got != tt.expected {
				t.Fatalf("%s", cmp.Diff(got, tt.expected))
			}
		})
	}
}

func TestFamilyKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		header   string
		expected string
	}{
		{
			"help header",
			"# HELP kube_customresource_probe Ready status",
			"kube_customresource_probe",
		},
		{
			"peripheral header",
			"# HELP kube_customresource_probe_created Time at which kube_customresource_probe_total was created.",
			"kube_customresource_probe_created",
		},
		{
			"not a help header",
			"header1",
			"header1",
		},
		{
			"empty",
			"",
			"",
		},
	}

	for i := range tests {
		tt := tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := familyKey(tt.header); got != tt.expected {
				t.Fatalf("familyKey(%q) = %q, want %q", tt.header, got, tt.expected)
			}
		})
	}
}

func TestMetricsWriter_exposition_parses(t *testing.T) {
	t.Parallel()

	header := familyProbeHeader()
	stores := []*StoreType{
		{
			headers: []string{header},
			metrics: map[types.UID][]string{
				"uid1": {"kube_customresource_probe{kind=\"ProviderConfig\",name=\"default\"} 0\n"},
			},
		},
		{
			headers: []string{header},
			metrics: map[types.UID][]string{
				"uid1": {"kube_customresource_probe{kind=\"ProviderConfigUsage\",name=\"2631db04\"} 1\n"},
			},
		},
	}

	w := &bytes.Buffer{}
	if err := newMetricsWriter(stores...).writeStores(w); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := w.String()
	if n := strings.Count(out, "# HELP kube_customresource_probe "); n != 1 {
		t.Fatalf("expected exactly one HELP header per family, got %d in %q", n, out)
	}

	tp := expfmt.NewTextParser(model.LegacyValidation)

	mfs, err := tp.TextToMetricFamilies(strings.NewReader(out))
	if err != nil {
		t.Fatalf("generated exposition does not parse: %v\n%s", err, out)
	}

	mf, ok := mfs["kube_customresource_probe"]
	if !ok {
		t.Fatalf("family kube_customresource_probe missing: %v", mfs)
	}

	if len(mf.GetMetric()) != 2 {
		t.Fatalf("expected both stores' samples in one family, got %d", len(mf.GetMetric()))
	}
}
