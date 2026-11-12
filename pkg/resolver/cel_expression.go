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
	"strings"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/interpreter"
)

// CELResolver implements the ExpressionResolver interface.
var _ ExpressionResolver = &CELResolver{}

// errMissingField marks an evaluation that reached a field the object does not have.
var errMissingField = errors.New("missing field")

// Compile parses and plans the expression once, so a bad expression surfaces at config load.
func (cr *CELResolver) Compile(expr string) (Expression, error) {
	return cr.compile(expr)
}

// Traits declares the o binding, label key sanitization in labelPrefix, a timeout and a cost limit, and list and map results.
func (cr *CELResolver) Traits() ExpressionTraits {
	return ExpressionTraits{
		Traits: Traits{
			ObjectBinding: "o",
			Sanitization:  SanitizationSpec{LabelKeys: true},
			Bounds:        BoundsSpec{Timeout: true, CostLimit: true},
		},
		Lists: true,
		Maps:  true,
	}
}

// compile returns the concrete expression so Resolve can use the cel-go value directly.
func (cr *CELResolver) compile(expr string) (*celExpression, error) {
	env, err := getCELEnv()
	if err != nil {
		return nil, err
	}

	ast, iss := env.Parse(expr)
	if iss.Err() != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidExpression, iss.Err())
	}

	program, err := cr.compileProgram(env, ast)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidExpression, err)
	}

	return &celExpression{resolver: cr, query: expr, program: program}, nil
}

// celExpression is a compiled CEL program.
type celExpression struct {
	resolver *CELResolver
	query    string
	program  cel.Program
}

// Eval runs the program under the resolver's timeout and returns the result as a Value.
func (ce *celExpression) Eval(ctx context.Context, obj map[string]any) (Value, bool, error) {
	out, err := ce.eval(ctx, obj)
	if errors.Is(err, errMissingField) {
		return Value{}, false, nil
	}

	if err != nil {
		return Value{}, false, err
	}

	value, err := ce.celValue(out)
	if err != nil {
		return Value{}, false, err
	}

	return value, true, nil
}

// eval runs the program under bounded and keeps the raw cel-go value for Resolve.
func (ce *celExpression) eval(ctx context.Context, obj map[string]any) (ref.Val, error) {
	var out ref.Val

	err := bounded(ctx, ce.resolver.timeout, "cel", func(ctx context.Context) error {
		result, details, err := ce.resolver.evaluateProgram(ctx, ce.program, obj)
		ce.resolver.addCostLogging(ce.resolver.logger.WithValues("query", ce.query), details)

		if err != nil {
			return classifyCELError(err)
		}

		out = result

		return nil
	})

	return out, err
}

// classifyCELError maps a blown cost limit to ErrBudgetExceeded and a missing field to errMissingField.
func classifyCELError(err error) error {
	var cancelled interpreter.EvalCancelledError
	if errors.As(err, &cancelled) && cancelled.Cause == interpreter.CostLimitExceeded {
		return fmt.Errorf("%w: %w", ErrBudgetExceeded, err)
	}

	if strings.Contains(err.Error(), "no such key") {
		return fmt.Errorf("%w: %w", errMissingField, err)
	}

	return err
}

// celValue converts a cel-go result into a Value, with the same scalar formatting as processResult.
func (ce *celExpression) celValue(out ref.Val) (Value, error) {
	switch out.Type() {
	case types.BoolType, types.DoubleType, types.IntType, types.StringType, types.UintType:
		return Value{Kind: KindScalar, Scalar: fmt.Sprintf("%v", out.Value())}, nil
	case types.NullType:
		return Value{Kind: KindScalar, Scalar: "<nil>"}, nil
	case types.ListType, types.MapType:
		value, ok := ce.nativeValue(out.Value())
		if !ok {
			return Value{}, fmt.Errorf("unsupported %s value type %T", out.Type(), out.Value())
		}

		return value, nil
	default:
		return Value{}, fmt.Errorf("unsupported output type %q", out.Type())
	}
}

// nativeValue converts the Go value behind a list or map, skipping entries of a kind the old flattening skipped.
// nativeValue converts the Go value behind a list or map, skipping entries of a kind the old flattening skipped.
func (ce *celExpression) nativeValue(native any) (Value, bool) {
	switch native := native.(type) {
	case string, int, int64, uint, uint64, float64, bool:
		return Value{Kind: KindScalar, Scalar: fmt.Sprintf("%v", native)}, true
	case []any:
		list := make([]Value, 0, len(native))

		for i, item := range native {
			value, ok := ce.nativeValue(item)
			if !ok {
				ce.resolver.logger.V(1).Error(fmt.Errorf("encountered composite value %v at index %d, skipping", item, i), "ignoring entry for query", "query", ce.query)

				continue
			}

			list = append(list, value)
		}

		return Value{Kind: KindList, List: list}, true
	case []ref.Val:
		items := make([]any, len(native))
		for i, item := range native {
			items[i] = item.Value()
		}

		return ce.nativeValue(items)
	case map[string]any:
		m := make(map[string]Value, len(native))

		for k, item := range native {
			value, ok := ce.nativeValue(item)
			if !ok {
				ce.resolver.logger.V(1).Error(fmt.Errorf("encountered composite value %v at key %q, skipping", item, k), "ignoring entry for query", "query", ce.query)

				continue
			}

			m[k] = value
		}

		return Value{Kind: KindMap, Map: m}, true
	case map[string]string:
		m := make(map[string]Value, len(native))
		for k, item := range native {
			m[k] = Value{Kind: KindScalar, Scalar: item}
		}

		return Value{Kind: KindMap, Map: m}, true
	default:
		return Value{}, false
	}
}
