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
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"k8s.io/klog/v2"
)

// sharedObject is the object every shared case runs against.
var sharedObject = map[string]any{
	"metadata": map[string]any{
		"labels": map[string]any{"foo": "1", "bar": "2"},
	},
	"spec": map[string]any{
		"name":     "bar",
		"replicas": 3,
		"ratio":    1.5,
		"ready":    true,
		"owner":    nil,
		"cpu":      "500m",
		"tags":     []any{"a", "b"},
		"props":    map[string]any{"k": "v"},
	},
}

// sharedCase is one row of the shared resolver table. Each resolver gets its own expression for the row, an empty one
// means the row does not apply to that resolver, and a row that needs lists or maps is skipped for a resolver whose
// traits do not declare them.
type sharedCase struct {
	name         string
	cel          string
	unstructured string
	starlark     string
	needs        Kind
	want         Value
	wantFound    bool
	wantErr      error
	// starlarkWant overrides want for Starlark, whose str() spells booleans and None its own way.
	starlarkWant string
	// starlarkErr marks a row where Starlark fails while the expression resolvers do not.
	starlarkErr bool
}

var sharedCases = []sharedCase{
	{
		name: "string", cel: "o.spec.name", unstructured: "spec.name", starlark: `obj["spec"]["name"]`,
		want: scalarOf("bar"), wantFound: true,
	},
	{
		name: "int", cel: "o.spec.replicas", unstructured: "spec.replicas", starlark: `obj["spec"]["replicas"]`,
		want: scalarOf("3"), wantFound: true,
	},
	{
		name: "float", cel: "o.spec.ratio", unstructured: "spec.ratio", starlark: `obj["spec"]["ratio"]`,
		want: scalarOf("1.5"), wantFound: true,
	},
	{
		name: "bool", cel: "o.spec.ready", unstructured: "spec.ready", starlark: `obj["spec"]["ready"]`,
		want: scalarOf("true"), wantFound: true, starlarkWant: "True",
	},
	{
		name: "null", cel: "o.spec.owner", unstructured: "spec.owner", starlark: `obj["spec"]["owner"]`,
		want: scalarOf("<nil>"), wantFound: true, starlarkWant: "None",
	},
	{
		name: "missing field", cel: "o.spec.missing", unstructured: "spec.missing", starlark: `obj["spec"]["missing"]`,
		starlarkErr: true,
	},
	{
		name: "list", cel: "o.spec.tags", needs: KindList,
		want: Value{Kind: KindList, List: []Value{scalarOf("a"), scalarOf("b")}}, wantFound: true,
	},
	{
		name: "map", cel: "o.spec.props", needs: KindMap,
		want: Value{Kind: KindMap, Map: map[string]Value{"k": scalarOf("v")}}, wantFound: true,
	},
	{
		name: "label prefix", cel: "labelPrefix(o.metadata.labels, 'l_')", starlark: `label_prefix(obj["metadata"]["labels"], "l_")`, needs: KindMap,
		want: Value{Kind: KindMap, Map: map[string]Value{"l_foo": scalarOf("1"), "l_bar": scalarOf("2")}}, wantFound: true,
	},
	{
		name: "quantity", cel: "quantity(o.spec.cpu)", starlark: `quantity_to_float(obj["spec"]["cpu"])`,
		want: scalarOf("0.5"), wantFound: true,
	},
	{
		name: "empty quantity", cel: "quantity('')", starlark: `quantity_to_float("")`,
		want: scalarOf("0"), wantFound: true, starlarkErr: true,
	},
}

func scalarOf(s string) Value {
	return Value{Kind: KindScalar, Scalar: s}
}

func TestSharedCases_CEL(t *testing.T) {
	t.Parallel()

	cr := NewCELResolver(klog.NewKlogr(), 10e5, time.Second, nil, "ns", "rmm", "family")
	traits := cr.Traits()

	for _, row := range sharedCases {
		if row.cel == "" || !declares(traits, row.needs) {
			continue
		}

		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			expr, err := cr.Compile(row.cel)
			if err != nil {
				t.Fatalf("Compile(%q) unexpected error: %v", row.cel, err)
			}

			checkExpression(t, expr, row)
		})
	}
}

func TestSharedCases_Unstructured(t *testing.T) {
	t.Parallel()

	ur := NewUnstructuredResolver(klog.NewKlogr())
	traits := ur.Traits()

	for _, row := range sharedCases {
		if row.unstructured == "" || !declares(traits, row.needs) {
			continue
		}

		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			expr, err := ur.Compile(row.unstructured)
			if err != nil {
				t.Fatalf("Compile(%q) unexpected error: %v", row.unstructured, err)
			}

			checkExpression(t, expr, row)
		})
	}
}

func TestSharedCases_Starlark(t *testing.T) {
	t.Parallel()

	sr := NewStarlarkResolver(klog.NewKlogr(), "", time.Second, 100000)

	for _, row := range sharedCases {
		if row.starlark == "" {
			continue
		}

		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			got, err := runStarlark(t, sr, row)
			if row.starlarkErr {
				if err == nil {
					t.Fatalf("expected an error for %q, got %v", row.starlark, got)
				}

				return
			}

			if err != nil {
				t.Fatalf("Eval of %q unexpected error: %v", row.starlark, err)
			}

			want := starlarkWant(row)
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Starlark %q mismatch (-want +got):\n%s", row.starlark, diff)
			}
		})
	}
}

// checkExpression compares one Eval against the row.
func checkExpression(t *testing.T, expr Expression, row sharedCase) {
	t.Helper()

	got, found, err := expr.Eval(t.Context(), sharedObject)
	if !errors.Is(err, row.wantErr) {
		t.Fatalf("Eval error = %v, want %v", err, row.wantErr)
	}

	if found != row.wantFound {
		t.Errorf("Eval found = %v, want %v", found, row.wantFound)
	}

	if diff := cmp.Diff(row.want, got); diff != "" {
		t.Errorf("Eval mismatch (-want +got):\n%s", diff)
	}
}

// runStarlark puts the row's expression into a script. A map row becomes the sample's labels, anything else becomes
// the text of the single label "v".
func runStarlark(t *testing.T, sr *StarlarkResolver, row sharedCase) (map[string]string, error) {
	t.Helper()

	labels := fmt.Sprintf(`{"v": str(%s)}`, row.starlark)
	if row.needs == KindMap {
		labels = row.starlark
	}

	script := fmt.Sprintf(`
families = [
    family(name="f", help="h", kind="gauge", samples=[metric(labels=%s, value=1.0)]),
]
`, labels)

	compiled, err := sr.Compile(script)
	if err != nil {
		return nil, err
	}

	families, err := compiled.Eval(t.Context(), sharedObject)
	if err != nil {
		return nil, err
	}

	if len(families) != 1 || len(families[0].Samples) != 1 {
		return nil, fmt.Errorf("expected one family with one sample, got %v", families)
	}

	return families[0].Samples[0].Labels, nil
}

// starlarkWant is the labels map a row expects from Starlark.
func starlarkWant(row sharedCase) map[string]string {
	if row.needs == KindMap {
		want := make(map[string]string, len(row.want.Map))
		for k, v := range row.want.Map {
			want[k] = v.Scalar
		}

		return want
	}

	if row.starlarkWant != "" {
		return map[string]string{"v": row.starlarkWant}
	}

	return map[string]string{"v": row.want.Scalar}
}

// declares reports whether the traits allow the kind a row needs.
func declares(traits ExpressionTraits, needs Kind) bool {
	switch needs {
	case KindList:
		return traits.Lists
	case KindMap:
		return traits.Maps
	case KindScalar, KindInvalid:
		return true
	}

	return true
}

func TestSharedLimits(t *testing.T) {
	t.Parallel()

	t.Run("cel timeout", func(t *testing.T) {
		t.Parallel()

		cr := NewCELResolver(klog.NewKlogr(), math.MaxUint64, time.Minute, nil, "ns", "rmm", "family")

		expr, err := cr.Compile("o.items.all(a, o.items.all(b, o.items.all(c, c >= 0)))")
		if err != nil {
			t.Fatalf("Compile unexpected error: %v", err)
		}

		items := make([]any, 1000)
		for i := range items {
			items[i] = int64(i)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()

		if _, _, err := expr.Eval(ctx, map[string]any{"items": items}); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Eval error = %v, want context.DeadlineExceeded", err)
		}
	})

	t.Run("cel cost limit", func(t *testing.T) {
		t.Parallel()

		cr := NewCELResolver(klog.NewKlogr(), 1, time.Second, nil, "ns", "rmm", "family")

		expr, err := cr.Compile("o.spec.tags.map(a, o.spec.tags.map(b, a + b))")
		if err != nil {
			t.Fatalf("Compile unexpected error: %v", err)
		}

		if _, _, err := expr.Eval(t.Context(), sharedObject); !errors.Is(err, ErrBudgetExceeded) {
			t.Errorf("Eval error = %v, want ErrBudgetExceeded", err)
		}
	})

	t.Run("starlark step limit", func(t *testing.T) {
		t.Parallel()

		sr := NewStarlarkResolver(klog.NewKlogr(), "", time.Second, 100)

		compiled, err := sr.Compile("x = 0\nfor i in range(10000):\n    x = x + 1\nfamilies = []\n")
		if err != nil {
			t.Fatalf("Compile unexpected error: %v", err)
		}

		if _, err := compiled.Eval(t.Context(), sharedObject); !errors.Is(err, ErrBudgetExceeded) {
			t.Errorf("Eval error = %v, want ErrBudgetExceeded", err)
		}
	})

	t.Run("invalid expressions", func(t *testing.T) {
		t.Parallel()

		cr := NewCELResolver(klog.NewKlogr(), 10e5, time.Second, nil, "ns", "rmm", "family")
		if _, err := cr.Compile("o.spec.replicas +"); !errors.Is(err, ErrInvalidExpression) {
			t.Errorf("CEL Compile error = %v, want ErrInvalidExpression", err)
		}

		if _, err := NewUnstructuredResolver(klog.NewKlogr()).Compile("spec..name"); !errors.Is(err, ErrInvalidExpression) {
			t.Errorf("unstructured Compile error = %v, want ErrInvalidExpression", err)
		}

		if _, err := NewStarlarkResolver(klog.NewKlogr(), "", time.Second, 100).Compile("families = ["); !errors.Is(err, ErrInvalidExpression) {
			t.Errorf("Starlark Compile error = %v, want ErrInvalidExpression", err)
		}
	})
}
