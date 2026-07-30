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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"
)

// UnstructuredResolver represents a resolver for unstructured objects.
type UnstructuredResolver struct {
	logger klog.Logger
}

// UnstructuredResolver implements the Resolver interface.
var _ Resolver = &UnstructuredResolver{}

// NewUnstructuredResolver returns a new unstructured resolver.
func NewUnstructuredResolver(logger klog.Logger) *UnstructuredResolver {
	return &UnstructuredResolver{logger: logger}
}

// ResolveComposite resolves the given query against the given unstructured object.
// NOTE: Resolutions resulting in composite values for label keys and values are not supported, owing to upstream
// limitations: https://github.com/kubernetes/apimachinery/blob/v0.31.0/pkg/apis/meta/v1/unstructured/helpers_test.go#L121.
func (ur *UnstructuredResolver) ResolveComposite(ctx context.Context, query string, obj map[string]interface{}) ([]ResolvedFamily, error) {
	logger := ur.logger.WithValues("query", query)

	// Support context cancellation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Swapped unstructuredObjectMap for obj to match signature
	gotResolved, found, err := unstructured.NestedFieldNoCopy(obj, strings.Split(query, ".")...)
	if err != nil {
		logger.V(1).Info("ignoring resolution for query", "info", err)

		return nil, fmt.Errorf("failed to resolve field path %q: %w", query, err)
	}

	if !found {
		logger.V(2).Info("query fell back to default mapping (field not found)", "query", query)

		return nil, fmt.Errorf("field path %q not found in object", query)
	}

	// Type assertion to convert interface{} safely into map[string]string
	labels := make(map[string]string)
	
	if mapRes, ok := gotResolved.(map[string]interface{}); ok {
		for k, v := range mapRes {
			labels[k] = fmt.Sprintf("%v", v)
		}
	} else if stringMap, ok := gotResolved.(map[string]string); ok {
		labels = stringMap
	} else {
		return nil, errors.New("resolved field is not a valid map type")
	}

	// Wrap in ResolvedFamily
	family := ResolvedFamily{
		Samples: []ResolvedSample{
			{
				Labels: labels,
				Value:  1.0,
			},
		},
	}

	return []ResolvedFamily{family}, nil
}

// ResolveScalar satisfies the Resolver interface for flat key-value resolutions.
func (ur *UnstructuredResolver) ResolveScalar(_ context.Context, query string, obj map[string]interface{}) (map[string]string, error) {
	fields := strings.Split(query, ".")

	val, found, err := unstructured.NestedFieldNoCopy(obj, fields...)
	if err != nil || !found || val == nil {
		return nil, fmt.Errorf("field %q not found or error traversing: %w", query, err)
	}

	// Safely format integers, floats, and booleans to strings to satisfy the interface
	return map[string]string{query: fmt.Sprintf("%v", val)}, nil
}

// SanitizeKey ensures unstructured keys conform to metric name standards.
func (ur *UnstructuredResolver) SanitizeKey(key string) string {
	// Unstructured keys are passed through; the main engine handles standard replacements
	return key
}

// SupportsUnderscoreExpansion dictates if this resolver handles matrix expansions.
func (ur *UnstructuredResolver) SupportsUnderscoreExpansion() bool {
	// Unstructured field paths typically do not support underscore matrix expansion
	return false
}
