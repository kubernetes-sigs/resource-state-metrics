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

func familyHeader(name, help, kind string) string {
	return "# HELP " + name + " " + help + "\n# TYPE " + name + " " + kind
}

func familyFor(name string, createdAt time.Time) *FamilyType {
	return &FamilyType{Family: v1alpha1.Family{Name: name}, createdAt: createdAt}
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
						headers:  []string{"header1", "header2"},
						Families: []*FamilyType{familyFor("fam1", time.Time{}), familyFor("fam2", time.Time{})},
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
						headers:  []string{"header1", "header2", "header3"},
						Families: []*FamilyType{familyFor("fam1", time.Time{}), familyFor("fam2", time.Time{}), familyFor("fam3", time.Time{})},
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
						headers:  []string{"header1"},
						Families: []*FamilyType{familyFor("fam1", time.Time{})},
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

	for i := range tests {
		tt := tests[i]
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

func TestMetricsWriter_groupsFamiliesAcrossStores(t *testing.T) {
	t.Parallel()

	readyHeader := familyHeader("kube_customresource_ready", "Ready status", "gauge")
	syncedHeader := familyHeader("kube_customresource_synced", "Synced status", "gauge")
	readyFamily := familyFor("ready", time.Time{})
	syncedFamily := familyFor("synced", time.Time{})

	stores := []*StoreType{
		{
			headers:  []string{readyHeader, syncedHeader},
			Families: []*FamilyType{readyFamily, syncedFamily},
			metrics: map[types.UID][]string{
				"uid1": {
					"kube_customresource_ready{group=\"storage.example.org\",kind=\"Bucket\",name=\"bucket\"} 1\n",
					"kube_customresource_synced{group=\"storage.example.org\",kind=\"Bucket\",name=\"bucket\"} 1\n",
				},
			},
		},
		{
			headers:  []string{readyHeader, syncedHeader},
			Families: []*FamilyType{readyFamily, syncedFamily},
			metrics: map[types.UID][]string{
				"uid1": {
					"kube_customresource_ready{group=\"storage.example.org\",kind=\"BucketPolicy\",name=\"policy\"} 0\n",
					"kube_customresource_synced{group=\"storage.example.org\",kind=\"BucketPolicy\",name=\"policy\"} 1\n",
				},
			},
		},
	}

	w := &bytes.Buffer{}
	if err := newMetricsWriter(stores...).writeStores(w); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := readyHeader + "\n" +
		"kube_customresource_ready{group=\"storage.example.org\",kind=\"Bucket\",name=\"bucket\"} 1\n" +
		"kube_customresource_ready{group=\"storage.example.org\",kind=\"BucketPolicy\",name=\"policy\"} 0\n" +
		syncedHeader + "\n" +
		"kube_customresource_synced{group=\"storage.example.org\",kind=\"Bucket\",name=\"bucket\"} 1\n" +
		"kube_customresource_synced{group=\"storage.example.org\",kind=\"BucketPolicy\",name=\"policy\"} 1\n"

	if got := w.String(); got != expected {
		t.Fatalf("%s", cmp.Diff(got, expected))
	}
}

func TestMetricsWriter_writeStores_dedupHeader(t *testing.T) {
	t.Parallel()

	header := familyHeader("kube_customresource_probe", "Ready status", "gauge")
	counterHeader := familyHeader("kube_customresource_probe_sets_total", "Sets total", "counter")
	counterFamily := familyFor("probe_sets_total", time.Unix(1, 500000000))

	tests := []struct {
		name     string
		stores   []*StoreType
		expected string
	}{
		{
			name: "same family in two stores gets its header once",
			stores: []*StoreType{
				{
					headers:  []string{header},
					Families: []*FamilyType{familyFor("probe", time.Time{})},
					metrics: map[types.UID][]string{
						"uid1": {"kube_customresource_probe{kind=\"ProviderConfig\"} 0\n"},
					},
				},
				{
					headers:  []string{header},
					Families: []*FamilyType{familyFor("probe", time.Time{})},
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
					headers:  []string{header},
					Families: []*FamilyType{familyFor("probe", time.Time{})},
					metrics: map[types.UID][]string{
						"uid1": {"kube_customresource_probe{kind=\"K1\"} 1\n"},
					},
				},
				{
					headers:  []string{"# HELP kube_customresource_other Other status\n# TYPE kube_customresource_other gauge"},
					Families: []*FamilyType{familyFor("other", time.Time{})},
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
					headers:  []string{counterHeader},
					Families: []*FamilyType{counterFamily},
					metrics: map[types.UID][]string{
						"uid1": {"kube_customresource_probe_sets_total{kind=\"K1\"} 4\n"},
					},
				},
				{
					headers:  []string{counterHeader},
					Families: []*FamilyType{counterFamily},
					metrics: map[types.UID][]string{
						"uid1": {"kube_customresource_probe_sets_total{kind=\"K2\"} 5\n"},
					},
				},
			},
			expected: counterHeader + "\n" +
				"kube_customresource_probe_sets_total{kind=\"K1\"} 4\n" +
				"kube_customresource_probe_sets_total{kind=\"K2\"} 5\n" +
				"# HELP kube_customresource_probe_sets_created Time at which kube_customresource_probe_sets_total was created." +
				"\n# TYPE kube_customresource_probe_sets_created counter\n" +
				"kube_customresource_probe_sets_created 1.500000\n",
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

func TestMetricsWriter_exposition_parses(t *testing.T) {
	t.Parallel()

	header := familyHeader("kube_customresource_probe", "Ready status", "gauge")
	stores := []*StoreType{
		{
			headers:  []string{header},
			Families: []*FamilyType{familyFor("probe", time.Time{})},
			metrics: map[types.UID][]string{
				"uid1": {"kube_customresource_probe{kind=\"ProviderConfig\",name=\"default\"} 0\n"},
			},
		},
		{
			headers:  []string{header},
			Families: []*FamilyType{familyFor("probe", time.Time{})},
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

func TestMetricsWriter_createdHeaderDoesNotCollideWithFamilyName(t *testing.T) {
	t.Parallel()

	counterHeader := familyHeader("kube_customresource_store_sets_total", "Sets total", "counter")
	gaugeHeader := familyHeader("kube_customresource_store_sets_total_created", "Something else", "gauge")

	stores := []*StoreType{
		{
			headers:  []string{counterHeader},
			Families: []*FamilyType{familyFor("store_sets_total", time.Unix(1, 500000000))},
			metrics: map[types.UID][]string{
				"uid1": {"kube_customresource_store_sets_total{kind=\"K1\"} 4\n"},
			},
		},
		{
			headers:  []string{gaugeHeader},
			Families: []*FamilyType{familyFor("store_sets_total_created", time.Time{})},
			metrics: map[types.UID][]string{
				"uid1": {"kube_customresource_store_sets_total_created{kind=\"K2\"} 7\n"},
			},
		},
	}

	w := &bytes.Buffer{}
	if err := newMetricsWriter(stores...).writeStores(w); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := w.String()
	if n := strings.Count(out, "# HELP kube_customresource_store_sets_total_created "); n != 1 {
		t.Fatalf("expected the gauge family header to be written once, got %d in %q", n, out)
	}

	tp := expfmt.NewTextParser(model.LegacyValidation)

	if _, err := tp.TextToMetricFamilies(strings.NewReader(out)); err != nil {
		t.Fatalf("generated exposition does not parse: %v\n%s", err, out)
	}
}

func TestMetricsWriter_createdUsesEarliestCreationTime(t *testing.T) {
	t.Parallel()

	counterHeader := familyHeader("kube_customresource_probe_sets_total", "Sets total", "counter")

	stores := []*StoreType{
		{
			headers:  []string{counterHeader},
			Families: []*FamilyType{familyFor("probe_sets_total", time.Unix(2, 0))},
			metrics: map[types.UID][]string{
				"uid1": {"kube_customresource_probe_sets_total{kind=\"K1\"} 4\n"},
			},
		},
		{
			headers:  []string{counterHeader},
			Families: []*FamilyType{familyFor("probe_sets_total", time.Unix(1, 500000000))},
			metrics: map[types.UID][]string{
				"uid1": {"kube_customresource_probe_sets_total{kind=\"K2\"} 5\n"},
			},
		},
	}

	w := &bytes.Buffer{}
	if err := newMetricsWriter(stores...).writeStores(w); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := w.String()
	if n := strings.Count(out, "kube_customresource_probe_sets_created 1.500000"); n != 1 {
		t.Fatalf("expected the merged family to use the earliest creation time, got %d occurrences in %q", n, out)
	}
}
