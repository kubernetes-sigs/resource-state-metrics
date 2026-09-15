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
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"
)

// UnstructuredResolver represents a resolver for unstructured objects.
type UnstructuredResolver struct {
	logger klog.Logger
}

// UnstructuredResolver implements the ExpressionResolver interface.
var _ ExpressionResolver = &UnstructuredResolver{}

// NewUnstructuredResolver returns a new unstructured resolver.
func NewUnstructuredResolver(logger klog.Logger) *UnstructuredResolver {
	return &UnstructuredResolver{logger: logger}
}

// SupportsCompositeValues reports whether UnstructuredResolver supports composite values.
func (*UnstructuredResolver) SupportsCompositeValues() bool {
	return false
}

// Resolve resolves the given query against the given unstructured object.
// NOTE: Resolutions resulting in composite values for label keys and values are not supported, owing to upstream
// limitations: https://github.com/kubernetes/apimachinery/blob/v0.31.0/pkg/apis/meta/v1/unstructured/helpers_test.go#L121.
func (ur *UnstructuredResolver) Resolve(query string, unstructuredObjectMap map[string]interface{}) map[string]string {
	logger := ur.logger.WithValues("query", query)
	gotResolved, found, err := unstructured.NestedFieldNoCopy(unstructuredObjectMap, strings.Split(query, ".")...)

	if !found {
		logger.V(2).Info("query fell back to default mapping (field not found, will be skipped at write time)", "query", query)

		return map[string]string{query: query}
	}

	if err != nil {
		logger.V(1).Info("ignoring resolution for query", "info", err)
		logger.V(2).Info("query fell back to default mapping (will be skipped at write time)", "query", query)

		return map[string]string{query: query}
	}

	return map[string]string{query: fmt.Sprintf("%v", gotResolved)}
}
