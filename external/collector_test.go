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
package external

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kube-state-metrics/v2/pkg/metric"
	metricsstore "k8s.io/kube-state-metrics/v2/pkg/metrics_store"
)

const testMetricHeader = "# HELP external_test_info Test metric"

// fakeCollector is a test double for the collectors interface.
// Register only counts calls so Build does not re-enter the CollectorsType
// mutex (the production example's Register targets the singleton and would
// deadlock if invoked under Build's lock).
type fakeCollector struct {
	t             *testing.T
	name          string
	objects       []*corev1.Pod
	kubeconfig    string
	buildCalls    int
	registerCalls int
}

func (f *fakeCollector) GVKR() gvkr { return gvkr{} }

func (f *fakeCollector) Register() {
	f.registerCalls++
}

func (f *fakeCollector) BuildCollector(_ context.Context, kubeconfig string) *metricsstore.MetricsStore {
	f.t.Helper()
	f.buildCalls++
	f.kubeconfig = kubeconfig

	gen := func(obj interface{}) []metric.FamilyInterface {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			return nil
		}

		return []metric.FamilyInterface{&metric.Family{
			Name: "external_test_info",
			Metrics: []*metric.Metric{
				{
					LabelKeys:   []string{"collector", "name"},
					LabelValues: []string{f.name, pod.Name},
					Value:       1,
				},
			},
		}}
	}

	store := metricsstore.NewMetricsStore([]string{testMetricHeader}, gen)
	for _, obj := range f.objects {
		if err := store.Add(obj); err != nil {
			f.t.Fatalf("store.Add(%q): %v", obj.Name, err)
		}
	}

	return store
}

func newTestPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			UID:       types.UID(name),
		},
	}
}

func emptyCollectors(t *testing.T) []*fakeCollector {
	t.Helper()

	return []*fakeCollector{{t: t, name: "Empty"}}
}

func singleCollector(t *testing.T) []*fakeCollector {
	t.Helper()

	return []*fakeCollector{{
		t:       t,
		name:    "Foo",
		objects: []*corev1.Pod{newTestPod("pod-a")},
	}}
}

func multipleCollectors(t *testing.T) []*fakeCollector {
	t.Helper()

	return []*fakeCollector{
		{t: t, name: "Foo", objects: []*corev1.Pod{newTestPod("pod-a")}},
		{t: t, name: "Bar", objects: []*corev1.Pod{newTestPod("pod-b")}},
	}
}

func assertBuiltState(
	t *testing.T,
	registry *CollectorsType,
	fakes []*fakeCollector,
	wantBuildCalls int,
	kubeconfig string,
) {
	t.Helper()

	if got := len(registry.builtCollectors); got != len(fakes) {
		t.Fatalf("len(builtCollectors) = %d, want %d", got, len(fakes))
	}

	for _, collector := range fakes {
		if collector.buildCalls != wantBuildCalls {
			t.Fatalf("collector %q buildCalls = %d, want %d", collector.name, collector.buildCalls, wantBuildCalls)
		}

		if collector.registerCalls != 1 {
			t.Fatalf("collector %q registerCalls = %d, want 1", collector.name, collector.registerCalls)
		}

		if kubeconfig != "" && collector.kubeconfig != kubeconfig {
			t.Fatalf("collector %q kubeconfig = %q, want %q", collector.name, collector.kubeconfig, kubeconfig)
		}
	}
}

func assertWriteOutput(t *testing.T, registry *CollectorsType, wantEmpty bool, wantSubstr []string) {
	t.Helper()

	var buf bytes.Buffer
	registry.Write(&buf)
	got := buf.String()

	if wantEmpty {
		if got != "" {
			t.Fatalf("Write() = %q, want empty", got)
		}

		return
	}

	for _, substr := range wantSubstr {
		if !strings.Contains(got, substr) {
			t.Fatalf("Write() missing %q\nfull output:\n%s", substr, got)
		}
	}
}

func TestCollectorsType_Register(t *testing.T) {
	t.Parallel()

	registry := &CollectorsType{}
	collector := &fakeCollector{t: t, name: "Foo"}

	registry.Register(collector)

	if got := len(registry.collectors); got != 1 {
		t.Fatalf("len(collectors) = %d, want 1", got)
	}

	if registry.collectors[0] != collector {
		t.Fatal("registered collector mismatch")
	}
}

func TestCollectorsType_SetKubeConfig(t *testing.T) {
	t.Parallel()

	registry := &CollectorsType{}
	got := registry.SetKubeConfig("/tmp/kubeconfig")

	if got != registry {
		t.Fatal("SetKubeConfig should return the receiver for chaining")
	}

	if registry.kubeconfig != "/tmp/kubeconfig" {
		t.Fatalf("kubeconfig = %q, want %q", registry.kubeconfig, "/tmp/kubeconfig")
	}
}

func TestCollectorsType_BuildAndWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		kubeconfig     string
		collectors     func(t *testing.T) []*fakeCollector
		wantSubstr     []string
		wantEmpty      bool
		wantBuildCalls int
	}{
		{
			name:      "no collectors",
			wantEmpty: true,
		},
		{
			name:           "collector with no objects yields empty output",
			collectors:     emptyCollectors,
			wantEmpty:      true,
			wantBuildCalls: 1,
		},
		{
			name:       "single collector writes its metrics",
			kubeconfig: "test-kubeconfig",
			collectors: singleCollector,
			wantSubstr: []string{
				testMetricHeader,
				`collector="Foo"`,
				`name="pod-a"`,
			},
			wantBuildCalls: 1,
		},
		{
			name:       "multiple collectors write all metrics",
			kubeconfig: "test-kubeconfig",
			collectors: multipleCollectors,
			wantSubstr: []string{
				`collector="Foo"`,
				`name="pod-a"`,
				`collector="Bar"`,
				`name="pod-b"`,
			},
			wantBuildCalls: 1,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			registry := &CollectorsType{}

			var fakes []*fakeCollector
			if testCase.collectors != nil {
				fakes = testCase.collectors(t)
			}

			for _, collector := range fakes {
				registry.Register(collector)
			}

			if testCase.kubeconfig != "" {
				registry.SetKubeConfig(testCase.kubeconfig)
			}

			registry.Build(context.Background())
			assertBuiltState(t, registry, fakes, testCase.wantBuildCalls, testCase.kubeconfig)
			assertWriteOutput(t, registry, testCase.wantEmpty, testCase.wantSubstr)
		})
	}
}

func TestGetCollectors(t *testing.T) {
	t.Parallel()

	first := GetCollectors()
	second := GetCollectors()

	if first == nil {
		t.Fatal("GetCollectors() returned nil")
	}

	if first != second {
		t.Fatal("GetCollectors() should return the same singleton instance")
	}
}
