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
	"reflect"
	"testing"

	"github.com/google/cel-go/common/types"
	"go.starlark.net/starlark"
)

func TestLabelPrefixSanitizationConformance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		labels map[string]string
		prefix string
		want   map[string]string
	}{
		{
			name: "simple labels",
			labels: map[string]string{
				"app": "test",
				"env": "prod",
			},
			prefix: "label_",
			want: map[string]string{
				"label_app": "test",
				"label_env": "prod",
			},
		},
		{
			name: "special characters",
			labels: map[string]string{
				"app.kubernetes.io/name": "myapp",
				"env/type":               "prod",
				"foo-bar":                "value",
			},
			prefix: "label_",
			want: map[string]string{
				"label_app_kubernetes_io_name": "myapp",
				"label_env_type":               "prod",
				"label_foo_bar":                "value",
			},
		},
		{
			name: "camel case remains unchanged",
			labels: map[string]string{
				"envType": "production",
			},
			prefix: "label_",
			want: map[string]string{
				"label_envType": "production",
			},
		},
		{
			name: "leading digit is sanitized",
			labels: map[string]string{
				"9invalid": "value",
			},
			prefix: "label_",
			want: map[string]string{
				"label__invalid": "value",
			},
		},
		{
			name:   "empty labels",
			labels: map[string]string{},
			prefix: "label_",
			want:   map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotCEL := resolveCELLabelPrefix(t, tt.labels, tt.prefix)
			gotStarlark := resolveStarlarkLabelPrefix(t, tt.labels, tt.prefix)

			if !reflect.DeepEqual(gotCEL, tt.want) {
				t.Errorf("CEL label prefix result = %v, want %v", gotCEL, tt.want)
			}

			if !reflect.DeepEqual(gotStarlark, tt.want) {
				t.Errorf("Starlark label prefix result = %v, want %v", gotStarlark, tt.want)
			}

			if !reflect.DeepEqual(gotCEL, gotStarlark) {
				t.Errorf("resolver label prefix results differ: CEL = %v, Starlark = %v", gotCEL, gotStarlark)
			}
		})
	}
}

func resolveCELLabelPrefix(t *testing.T, labels map[string]string, prefix string) map[string]string {
	t.Helper()

	nativeLabels := make(map[string]any, len(labels))
	for key, value := range labels {
		nativeLabels[key] = value
	}

	result := labelPrefixBinding(
		types.NewStringInterfaceMap(types.DefaultTypeAdapter, nativeLabels),
		types.String(prefix),
	)

	nativeResult, err := result.ConvertToNative(reflect.TypeOf(map[string]string{}))
	if err != nil {
		t.Fatalf("converting CEL label prefix result: %v", err)
	}

	resolved, ok := nativeResult.(map[string]string)
	if !ok {
		t.Fatalf("CEL label prefix result has type %T, want map[string]string", nativeResult)
	}

	return resolved
}

func resolveStarlarkLabelPrefix(t *testing.T, labels map[string]string, prefix string) map[string]string {
	t.Helper()

	labelsDict := starlark.NewDict(len(labels))
	for key, value := range labels {
		if err := labelsDict.SetKey(starlark.String(key), starlark.String(value)); err != nil {
			t.Fatalf("building Starlark labels dict: %v", err)
		}
	}

	result, err := labelPrefixBuiltin(
		nil,
		nil,
		starlark.Tuple{labelsDict, starlark.String(prefix)},
		nil,
	)
	if err != nil {
		t.Fatalf("resolving Starlark label prefix: %v", err)
	}

	resultDict, ok := result.(*starlark.Dict)
	if !ok {
		t.Fatalf("Starlark label prefix result has type %T, want *starlark.Dict", result)
	}

	resolved := make(map[string]string, resultDict.Len())
	for _, item := range resultDict.Items() {
		key, ok := item[0].(starlark.String)
		if !ok {
			t.Fatalf("Starlark label key has type %T, want starlark.String", item[0])
		}

		value, ok := item[1].(starlark.String)
		if !ok {
			t.Fatalf("Starlark label value has type %T, want starlark.String", item[1])
		}

		resolved[string(key)] = string(value)
	}

	return resolved
}
