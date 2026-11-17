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

import "context"

// Resolver defines behaviors for resolving a given expression.
type Resolver interface {

	// Resolve resolves the given expression.
	// NOTE: The returned map should have a single key:value (query:resolved[LabelValues,Value], of unit length) pair if the expression is resolved to a non-composite value.
	// NOTE: The returned list representations should follow a `list_name#index` format.
	Resolve(query string, unstructuredObjectMap map[string]interface{}) map[string]string
}

// ExpressionResolver compiles one expression at a time and declares what it supports.
type ExpressionResolver interface {
	// Compile parses the expression once, so ErrInvalidExpression surfaces at config load and not per object.
	Compile(expr string) (Expression, error)
	Traits() ExpressionTraits
}

// Expression is a compiled expression that can be evaluated against objects.
type Expression interface {
	// Eval returns the resolved value, whether the field was found, and any evaluation error.
	Eval(ctx context.Context, obj map[string]any) (Value, bool, error)
}

// FamilyResolver compiles one script that resolves whole metric families.
type FamilyResolver interface {
	// Compile parses the script once, so a bad script surfaces at config load.
	Compile(script string) (Script, error)
	Traits() Traits
}

// Script is a compiled script that can be evaluated against objects.
type Script interface {
	Eval(ctx context.Context, obj map[string]any) ([]ResolvedFamily, error)
}
