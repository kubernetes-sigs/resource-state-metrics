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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"
)

// UnstructuredResolver represents a resolver for unstructured objects.
type UnstructuredResolver struct {
	logger klog.Logger
}

// UnstructuredResolver implements the Resolver interface.
var _ Resolver = &UnstructuredResolver{}

// UnstructuredResolver implements the ExpressionResolver interface.
var _ ExpressionResolver = &UnstructuredResolver{}

// NewUnstructuredResolver returns a new unstructured resolver.
func NewUnstructuredResolver(logger klog.Logger) *UnstructuredResolver {
	return &UnstructuredResolver{logger: logger}
}

// Compile splits the dotted path once and rejects an empty path or an empty segment.
func (ur *UnstructuredResolver) Compile(expr string) (Expression, error) {
	path := strings.Split(expr, ".")
	for _, segment := range path {
		if segment == "" {
			return nil, fmt.Errorf("%w: empty segment in path %q", ErrInvalidExpression, expr)
		}
	}

	return &unstructuredExpression{path: path}, nil
}

// Traits declares no object binding, no sanitizer, no bounds and no expansion.
func (ur *UnstructuredResolver) Traits() ExpressionTraits {
	return ExpressionTraits{}
}

// unstructuredExpression is a compiled dotted path.
type unstructuredExpression struct {
	path []string
}

// Eval walks the path and returns the field as a scalar, using the %v string for a list or a map.
func (ue *unstructuredExpression) Eval(ctx context.Context, obj map[string]any) (Value, bool, error) {
	var (
		value Value
		found bool
	)

	err := bounded(ctx, 0, "unstructured", func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		got, ok, err := unstructured.NestedFieldNoCopy(obj, ue.path...)
		if err != nil {
			return err
		}

		if !ok {
			return nil
		}

		value = Value{Kind: KindScalar, Scalar: fmt.Sprintf("%v", got)}
		found = true

		return nil
	})
	if err != nil {
		return Value{}, false, err
	}

	return value, found, nil
}

// Resolve resolves the given query against the given unstructured object.
// Resolve is kept for the Resolver interface until the wrapper moves to Compile and Eval.
// NOTE: Resolutions resulting in composite values for label keys and values are not supported, owing to upstream
// limitations: https://github.com/kubernetes/apimachinery/blob/v0.31.0/pkg/apis/meta/v1/unstructured/helpers_test.go#L121.
func (ur *UnstructuredResolver) Resolve(query string, unstructuredObjectMap map[string]interface{}) map[string]string {
	logger := ur.logger.WithValues("query", query)

	expr, err := ur.Compile(query)
	if err != nil {
		logger.V(2).Info("query fell back to default mapping (field not found, will be skipped at write time)", "query", query)

		return map[string]string{query: query}
	}

	value, found, err := expr.Eval(context.Background(), unstructuredObjectMap)
	if err != nil {
		logger.V(1).Info("ignoring resolution for query", "info", err)
		logger.V(2).Info("query fell back to default mapping (will be skipped at write time)", "query", query)

		return map[string]string{query: query}
	}

	if !found {
		logger.V(2).Info("query fell back to default mapping (field not found, will be skipped at write time)", "query", query)

		return map[string]string{query: query}
	}

	return map[string]string{query: value.Scalar}
}
