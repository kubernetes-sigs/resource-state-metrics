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
package resolver

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"k8s.io/klog/v2"
)

func TestStarlarkResolver_BasicGeneration(t *testing.T) {
	t.Parallel()

	script := `
samples = [
    metric(labels={"name": obj["metadata"]["name"]}, value=1.0),
    metric(labels={"name": obj["metadata"]["name"], "status": "active"}, value=2.5),
]
families = [
    family(name="test_metric", help="Test metric", kind="gauge", samples=samples)
]
`

	obj := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":      "test-resource",
			"namespace": "test-ns",
		},
	}

	sg := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	families, err := sg.Resolve(obj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(families) != 1 {
		t.Fatalf("expected 1 family, got %d", len(families))
	}

	family := families[0]
	if family.Name != "test_metric" {
		t.Errorf("expected family name 'test_metric', got %q", family.Name)
	}

	if family.Help != "Test metric" {
		t.Errorf("expected help 'Test metric', got %q", family.Help)
	}

	if family.Kind != "gauge" {
		t.Errorf("expected kind 'gauge', got %q", family.Kind)
	}

	if len(family.Samples) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(family.Samples))
	}

	// Check first sample
	if family.Samples[0].Value != 1.0 {
		t.Errorf("expected sample 0 value 1.0, got %f", family.Samples[0].Value)
	}

	if family.Samples[0].Labels["name"] != "test-resource" {
		t.Errorf("expected sample 0 name label 'test-resource', got %q", family.Samples[0].Labels["name"])
	}

	// Check second sample
	if family.Samples[1].Value != 2.5 {
		t.Errorf("expected sample 1 value 2.5, got %f", family.Samples[1].Value)
	}

	if family.Samples[1].Labels["status"] != "active" {
		t.Errorf("expected sample 1 status label 'active', got %q", family.Samples[1].Labels["status"])
	}
}

func TestStarlarkResolver_QuantityConversion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		quantity string
		expected float64
	}{
		{"millicores", "100m", 0.1},
		{"cores", "2", 2.0},
		{"kilobytes", "1Ki", 1024.0},
		{"megabytes", "1Mi", 1048576.0},
		{"gigabytes", "1Gi", 1073741824.0},
		{"milliunits", "500m", 0.5},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			script := `
samples = [
    metric(labels={"resource": "cpu"}, value=quantity_to_float(obj["spec"]["value"]))
]
families = [
    family(name="resource_usage", help="Resource usage", kind="gauge", samples=samples)
]
`
			obj := map[string]interface{}{
				"spec": map[string]interface{}{
					"value": testCase.quantity,
				},
			}

			sg := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

			families, err := sg.Resolve(obj)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(families) != 1 || len(families[0].Samples) != 1 {
				t.Fatalf("expected 1 family with 1 sample")
			}

			got := families[0].Samples[0].Value
			if !cmp.Equal(got, testCase.expected) {
				t.Errorf("quantity %q: expected %f, got %f", testCase.quantity, testCase.expected, got)
			}
		})
	}
}

func TestStarlarkResolver_NestedIteration(t *testing.T) {
	t.Parallel()

	// With TopLevelControl enabled, for loops work at top level
	script := `
samples = []
crq_name = obj["metadata"]["name"]

for ns_status in obj.get("status", {}).get("namespaces", []):
    ns_name = ns_status.get("namespace", "")
    status = ns_status.get("status", {})

    for resource, value in status.get("hard", {}).items():
        samples.append(metric(
            labels={"name": crq_name, "namespace": ns_name, "resource": resource, "type": "hard"},
            value=quantity_to_float(value)
        ))

    for resource, value in status.get("used", {}).items():
        samples.append(metric(
            labels={"name": crq_name, "namespace": ns_name, "resource": resource, "type": "used"},
            value=quantity_to_float(value)
        ))

families = [
    family(name="namespace_usage", help="Per-namespace quota breakdown", kind="gauge", samples=samples)
]
`

	obj := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "test-quota",
		},
		"status": map[string]interface{}{
			"namespaces": []interface{}{
				map[string]interface{}{
					"namespace": "ns-a",
					"status": map[string]interface{}{
						"hard": map[string]interface{}{
							"cpu":    "2",
							"memory": "4Gi",
						},
						"used": map[string]interface{}{
							"cpu":    "500m",
							"memory": "1Gi",
						},
					},
				},
				map[string]interface{}{
					"namespace": "ns-b",
					"status": map[string]interface{}{
						"hard": map[string]interface{}{
							"cpu": "1",
						},
						"used": map[string]interface{}{
							"cpu": "250m",
						},
					},
				},
			},
		},
	}

	sg := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	families, err := sg.Resolve(obj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(families) != 1 {
		t.Fatalf("expected 1 family, got %d", len(families))
	}

	// ns-a: 2 hard + 2 used = 4 samples
	// ns-b: 1 hard + 1 used = 2 samples
	// total = 6 samples
	if len(families[0].Samples) != 6 {
		t.Errorf("expected 6 samples, got %d", len(families[0].Samples))
	}
}

func TestStarlarkResolver_Timeout(t *testing.T) {
	t.Parallel()

	// With While enabled, we can use while True for an infinite loop
	script := `
while True:
    pass
families = []
`

	obj := map[string]interface{}{}

	// Use a very short timeout with high step limit so timeout triggers first
	sg := NewStarlarkResolver(klog.NewKlogr(), script, 100*time.Millisecond, 100000000)

	_, err := sg.Resolve(obj)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

func TestStarlarkResolver_StepLimit(t *testing.T) {
	t.Parallel()

	// With TopLevelControl enabled, for loops work at top level
	script := `
x = 0
for i in range(100000):
    x = x + 1
families = []
`

	obj := map[string]interface{}{}

	// Use a very low step limit
	sg := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 1000)

	_, err := sg.Resolve(obj)
	if err == nil {
		t.Fatal("expected step limit error, got nil")
	}

	if !strings.Contains(err.Error(), "starlark") {
		t.Errorf("expected starlark error, got: %v", err)
	}
}

func TestStarlarkResolver_MultipleFamilies(t *testing.T) {
	t.Parallel()

	script := `
families = [
    family(
        name="metric_one",
        help="First metric",
        kind="gauge",
        samples=[metric(labels={"type": "one"}, value=1.0)]
    ),
    family(
        name="metric_two",
        help="Second metric",
        kind="counter",
        samples=[metric(labels={"type": "two"}, value=2.0)]
    ),
]
`

	obj := map[string]interface{}{}

	sg := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	families, err := sg.Resolve(obj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(families) != 2 {
		t.Fatalf("expected 2 families, got %d", len(families))
	}

	if families[0].Name != "metric_one" || families[0].Kind != "gauge" {
		t.Errorf("unexpected first family: %+v", families[0])
	}

	if families[1].Name != "metric_two" || families[1].Kind != "counter" {
		t.Errorf("unexpected second family: %+v", families[1])
	}
}

func TestStarlarkResolver_InvalidScript(t *testing.T) {
	t.Parallel()

	script := `
this is not valid starlark
`

	obj := map[string]interface{}{}

	sg := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	_, err := sg.Resolve(obj)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

func TestStarlarkResolver_MissingFamiliesVariable(t *testing.T) {
	t.Parallel()

	script := `
samples = []
`

	obj := map[string]interface{}{}

	sg := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	_, err := sg.Resolve(obj)
	if err == nil {
		t.Fatal("expected missing families error, got nil")
	}

	if !strings.Contains(err.Error(), "families") {
		t.Errorf("expected error about missing families, got: %v", err)
	}
}

func TestStarlarkResolver_InvalidFamilyKind(t *testing.T) {
	t.Parallel()

	script := `
families = [
    family(name="test", help="Test", kind="invalid", samples=[])
]
`

	obj := map[string]interface{}{}

	sg := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	_, err := sg.Resolve(obj)
	if err == nil {
		t.Fatal("expected invalid kind error, got nil")
	}

	if !strings.Contains(err.Error(), "gauge") || !strings.Contains(err.Error(), "counter") {
		t.Errorf("expected error about valid kinds, got: %v", err)
	}
}

func TestStarlarkResolver_EmptyFamilies(t *testing.T) {
	t.Parallel()

	script := `
families = []
`

	obj := map[string]interface{}{}

	sg := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	families, err := sg.Resolve(obj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(families) != 0 {
		t.Errorf("expected 0 families, got %d", len(families))
	}
}

func TestStarlarkResolver_ObjectAccess(t *testing.T) {
	t.Parallel()

	script := `
name = obj["metadata"]["name"]
namespace = obj["metadata"]["namespace"]
replicas = obj["spec"]["replicas"]
ready = obj["status"]["ready"]

samples = [
    metric(labels={"name": name, "namespace": namespace}, value=float(replicas)),
    metric(labels={"name": name, "namespace": namespace, "condition": "ready"}, value=float(ready)),
]
families = [
    family(name="object_info", help="Object info", kind="gauge", samples=samples)
]
`

	obj := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":      "my-deployment",
			"namespace": "production",
		},
		"spec": map[string]interface{}{
			"replicas": 3,
		},
		"status": map[string]interface{}{
			"ready": 2,
		},
	}

	sg := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	families, err := sg.Resolve(obj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(families) != 1 || len(families[0].Samples) != 2 {
		t.Fatalf("expected 1 family with 2 samples")
	}

	sample1 := families[0].Samples[0]
	if sample1.Labels["name"] != "my-deployment" || sample1.Labels["namespace"] != "production" {
		t.Errorf("unexpected labels: %v", sample1.Labels)
	}

	if sample1.Value != 3.0 {
		t.Errorf("expected value 3.0, got %f", sample1.Value)
	}
}

// FileOptions feature tests - verify each Starlark dialect option is enabled

func TestStarlarkResolver_FileOptions_TopLevelFor(t *testing.T) {
	t.Parallel()

	// TopLevelControl: true enables for loops at top level
	script := `
samples = []
for i in range(3):
    samples.append(metric(labels={"index": str(i)}, value=float(i)))

families = [family(name="test", help="test", kind="gauge", samples=samples)]
`

	obj := map[string]any{}
	sr := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	families, err := sr.Resolve(obj)
	if err != nil {
		t.Fatalf("top-level for loop should work: %v", err)
	}

	if len(families) != 1 || len(families[0].Samples) != 3 {
		t.Errorf("expected 1 family with 3 samples, got %d families with %d samples",
			len(families), len(families[0].Samples))
	}
}

func TestStarlarkResolver_FileOptions_TopLevelIf(t *testing.T) {
	t.Parallel()

	// TopLevelControl: true enables if statements at top level
	script := `
value = 0.0
if obj.get("enabled", False):
    value = 1.0

families = [family(name="test", help="test", kind="gauge", samples=[
    metric(labels={}, value=value)
])]
`

	obj := map[string]any{"enabled": true}
	sr := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	families, err := sr.Resolve(obj)
	if err != nil {
		t.Fatalf("top-level if should work: %v", err)
	}

	if families[0].Samples[0].Value != 1.0 {
		t.Errorf("expected value 1.0, got %f", families[0].Samples[0].Value)
	}
}

func TestStarlarkResolver_FileOptions_WhileLoop(t *testing.T) {
	t.Parallel()

	// While: true enables while loops
	script := `
samples = []
i = 0
while i < 3:
    samples.append(metric(labels={"index": str(i)}, value=float(i)))
    i = i + 1

families = [family(name="test", help="test", kind="gauge", samples=samples)]
`

	obj := map[string]any{}
	sr := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	families, err := sr.Resolve(obj)
	if err != nil {
		t.Fatalf("while loop should work: %v", err)
	}

	if len(families) != 1 || len(families[0].Samples) != 3 {
		t.Errorf("expected 1 family with 3 samples, got %d families with %d samples",
			len(families), len(families[0].Samples))
	}
}

func TestStarlarkResolver_FileOptions_GlobalReassign(t *testing.T) {
	t.Parallel()

	// GlobalReassign: true allows reassigning global variables
	script := `
x = 1
x = 2  # This would fail without GlobalReassign

families = [family(name="test", help="test", kind="gauge", samples=[
    metric(labels={}, value=float(x))
])]
`

	obj := map[string]any{}
	sr := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	families, err := sr.Resolve(obj)
	if err != nil {
		t.Fatalf("global reassignment should work: %v", err)
	}

	if families[0].Samples[0].Value != 2.0 {
		t.Errorf("expected value 2.0, got %f", families[0].Samples[0].Value)
	}
}

func TestStarlarkResolver_TimeNow(t *testing.T) {
	t.Parallel()

	// time.now() returns a time.time; .unix gives Unix seconds as an int.
	script := `
samples = [
    metric(labels={"type": "current"}, value=float(time.now().unix)),
]
families = [
    family(name="now_metric", help="Current time", kind="gauge", samples=samples)
]
`

	obj := map[string]interface{}{}

	sg := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	// Capture a wall-clock window around Resolve() to assert closely.
	before := time.Now().Unix()

	families, err := sg.Resolve(obj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	after := time.Now().Unix()

	if len(families) != 1 || len(families[0].Samples) != 1 {
		t.Fatalf("expected 1 family with 1 sample")
	}

	ts := families[0].Samples[0].Value
	// Allow a 5-second slop on either side to absorb scheduling jitter without
	// pinning to an arbitrary calendar date that ages badly.
	const delta = 5.0
	if ts < float64(before)-delta || ts > float64(after)+delta {
		t.Errorf("time.now().unix = %f, expected within [%d-%g, %d+%g]", ts, before, delta, after, delta)
	}
}

func TestStarlarkResolver_TimeDuration(t *testing.T) {
	t.Parallel()

	// Compute duration in seconds: time.now() - parse_time("2024-01-15T10:30:00Z")
	// yields a time.duration, whose .seconds attribute is a float.
	script := `
past = time.parse_time("2024-01-15T10:30:00Z")
duration = (time.now() - past).seconds
samples = [
    metric(labels={"kind": "test"}, value=duration),
]
families = [
    family(name="duration_metric", help="Duration since event", kind="gauge", samples=samples)
]
`

	obj := map[string]interface{}{}

	sg := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	families, err := sg.Resolve(obj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(families) != 1 || len(families[0].Samples) != 1 {
		t.Fatalf("expected 1 family with 1 sample")
	}

	duration := families[0].Samples[0].Value
	// Duration since 2024-01-15 should be positive.
	if duration <= 0 {
		t.Errorf("expected positive duration, got %f", duration)
	}
}

func TestStarlarkResolver_ParseTime(t *testing.T) {
	t.Parallel()

	// 2024-01-15T10:30:00Z = 1705314600 Unix seconds. .unix returns an int;
	// the test sample is a float so we cast at the script boundary.
	script := `
parsed = time.parse_time("2024-01-15T10:30:00Z")
samples = [metric(labels={"kind": "test"}, value=float(parsed.unix))]
families = [family(name="parsed_metric", help="Parsed timestamp", kind="gauge", samples=samples)]
`

	obj := map[string]interface{}{}

	sg := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	families, err := sg.Resolve(obj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := families[0].Samples[0].Value, 1705314600.0; got != want {
		t.Errorf("time.parse_time(...).unix returned %f, expected %f", got, want)
	}
}

func TestStarlarkResolver_ParseTimeRFC3339Nano(t *testing.T) {
	t.Parallel()

	// Kubernetes lastTransitionTime is often RFC-3339 with sub-second precision.
	// Sub-seconds are truncated when reading .unix (1705314600).
	script := `
parsed = time.parse_time("2024-01-15T10:30:00.123456789Z")
samples = [metric(labels={"kind": "test"}, value=float(parsed.unix))]
families = [family(name="parsed_metric", help="Parsed timestamp", kind="gauge", samples=samples)]
`

	obj := map[string]interface{}{}

	sg := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	families, err := sg.Resolve(obj)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := families[0].Samples[0].Value, 1705314600.0; got != want {
		t.Errorf("time.parse_time(...).unix returned %f, expected %f", got, want)
	}
}

func TestStarlarkResolver_ParseTimeInvalid(t *testing.T) {
	t.Parallel()

	// Malformed non-empty input is a real error — fail loudly so script bugs surface.
	script := `
parsed = time.parse_time("not-a-timestamp")
families = []
`

	obj := map[string]interface{}{}

	sg := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	_, err := sg.Resolve(obj)
	if err == nil {
		t.Fatal("expected error when parsing malformed timestamp, got nil")
	}
}

// TestStarlarkResolver_ConditionAgeFilter exercises the production use case:
// suppress a condition-derived metric until the condition has held its current
// status for at least `grace` seconds.
func TestStarlarkResolver_ConditionAgeFilter(t *testing.T) {
	t.Parallel()

	// past condition (well outside grace) -> emit; recent condition -> suppress.
	// time.parse_time errors on empty input, so guard before calling it — the
	// "no lastTransitionTime" case is "we don't know the age, skip the gate".
	script := `
grace = 90.0
samples = []
for c in obj["status"]["conditions"]:
    if c["type"] == "Synced" and c["status"] != "True":
        ts = c.get("lastTransitionTime", "")
        if not ts:
            continue
        age = (time.now() - time.parse_time(ts)).seconds
        if age < grace:
            continue
        samples.append(metric(labels={"reason": c["reason"]}, value=1.0))
families = [family(name="not_synced", help="not synced", kind="gauge", samples=samples)]
`

	now := time.Now()
	old := now.Add(-10 * time.Minute).UTC().Format(time.RFC3339Nano)
	fresh := now.Add(-10 * time.Second).UTC().Format(time.RFC3339Nano)

	makeObj := func(transitionTime string) map[string]interface{} {
		return map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":               "Synced",
						"status":             "False",
						"reason":             "ReconcileError",
						"lastTransitionTime": transitionTime,
					},
				},
			},
		}
	}

	sg := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	families, err := sg.Resolve(makeObj(old))
	if err != nil {
		t.Fatalf("unexpected error for old condition: %v", err)
	}

	if len(families[0].Samples) != 1 {
		t.Errorf("expected 1 sample for condition older than grace, got %d", len(families[0].Samples))
	}

	families, err = sg.Resolve(makeObj(fresh))
	if err != nil {
		t.Fatalf("unexpected error for fresh condition: %v", err)
	}

	if len(families[0].Samples) != 0 {
		t.Errorf("expected 0 samples for condition inside grace, got %d", len(families[0].Samples))
	}
}

func TestStarlarkResolver_FileOptions_Set(t *testing.T) {
	t.Parallel()

	// Set: true enables the set() builtin for deduplication
	script := `
# Create a set to deduplicate values using set() constructor
values = set([1, 2, 2, 3])  # Results in {1, 2, 3}

samples = []
for v in values:
    samples.append(metric(labels={}, value=float(v)))

families = [family(name="test", help="test", kind="gauge", samples=samples)]
`

	obj := map[string]any{}
	sr := NewStarlarkResolver(klog.NewKlogr(), script, 5*time.Second, 100000)

	families, err := sr.Resolve(obj)
	if err != nil {
		t.Fatalf("set() builtin should work: %v", err)
	}
	// Set should deduplicate, so we get 3 unique values
	if len(families[0].Samples) != 3 {
		t.Errorf("expected 3 samples (deduplicated set), got %d", len(families[0].Samples))
	}
}
