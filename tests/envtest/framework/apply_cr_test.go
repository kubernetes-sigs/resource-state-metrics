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

package framework

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

const widgetName = "fixture"

func newWidget(value string, uid types.UID) *unstructured.Unstructured {
	widget := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.test/v1",
		"kind":       "Widget",
		"metadata": map[string]any{
			"name":      widgetName,
			"namespace": "default",
		},
		"spec": map[string]any{"value": value},
	}}
	widget.SetUID(uid)

	return widget
}

// newWidgetFramework returns a Framework backed by a fake dynamic client that
// holds the given objects, and a client for the Widget resource.
func newWidgetFramework(objects ...runtime.Object) (*Framework, dynamic.ResourceInterface) {
	groupVersion := schema.GroupVersion{Group: "example.test", Version: "v1"}

	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), objects...)
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{groupVersion})
	mapper.Add(groupVersion.WithKind("Widget"), meta.RESTScopeNamespace)

	return &Framework{dynamicClient: client, restMapper: mapper},
		client.Resource(groupVersion.WithResource("widgets")).Namespace("default")
}

func widgetValue(ctx context.Context, t *testing.T, client dynamic.ResourceInterface) string {
	t.Helper()

	widget, err := client.Get(ctx, widgetName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed to get widget: %v", err)
	}

	value, _, _ := unstructured.NestedString(widget.Object, "spec", "value")

	return value
}

func TestApplyCRFromYAMLRestoresPreExistingObject(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	framework, client := newWidgetFramework(newWidget("original", "uid-original"))

	path := filepath.Join(t.TempDir(), "widget.yaml")

	manifest := []byte("apiVersion: example.test/v1\nkind: Widget\nmetadata:\n  name: fixture\n  namespace: default\nspec:\n  value: test\n")
	if err := os.WriteFile(path, manifest, 0o600); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	cleanup, err := framework.ApplyCRFromYAML(ctx, path)
	if err != nil {
		t.Fatalf("ApplyCRFromYAML() error = %v", err)
	}

	if got := widgetValue(ctx, t, client); got != "test" {
		t.Fatalf("updated spec value = %q, want %q", got, "test")
	}

	if err := cleanup(ctx); err != nil {
		t.Fatalf("cleanup error = %v", err)
	}

	if got := widgetValue(ctx, t, client); got != "original" {
		t.Fatalf("restored spec value = %q, want %q", got, "original")
	}
}

func TestApplyUnstructuredCleanupDeletesCreatedObject(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	framework, client := newWidgetFramework()

	cleanup, err := framework.ApplyUnstructured(ctx, newWidget("test", "uid-test"))
	if err != nil {
		t.Fatalf("ApplyUnstructured() error = %v", err)
	}

	if got := widgetValue(ctx, t, client); got != "test" {
		t.Fatalf("created spec value = %q, want %q", got, "test")
	}

	if err := cleanup(ctx); err != nil {
		t.Fatalf("cleanup error = %v", err)
	}

	if _, err := client.Get(ctx, widgetName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("Get() error = %v, want NotFound", err)
	}

	if err := cleanup(ctx); err != nil {
		t.Fatalf("cleanup of an already deleted object error = %v", err)
	}
}

func TestApplyUnstructuredCleanupKeepsReplacedObject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		existing []runtime.Object
	}{
		{name: "created by the apply"},
		{name: "pre-existing", existing: []runtime.Object{newWidget("original", "uid-test")}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			framework, client := newWidgetFramework(testCase.existing...)

			cleanup, err := framework.ApplyUnstructured(ctx, newWidget("test", "uid-test"))
			if err != nil {
				t.Fatalf("ApplyUnstructured() error = %v", err)
			}

			// Someone else deletes the object and creates a new one with the same name.
			if err := client.Delete(ctx, widgetName, metav1.DeleteOptions{}); err != nil {
				t.Fatalf("failed to delete widget: %v", err)
			}

			if _, err := client.Create(ctx, newWidget("replacement", "uid-replacement"), metav1.CreateOptions{}); err != nil {
				t.Fatalf("failed to create replacement widget: %v", err)
			}

			if err := cleanup(ctx); err == nil {
				t.Fatal("cleanup error = nil, want an error for a replaced object")
			}

			if got := widgetValue(ctx, t, client); got != "replacement" {
				t.Fatalf("spec value after cleanup = %q, want %q", got, "replacement")
			}
		})
	}
}
