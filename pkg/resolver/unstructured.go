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
	"strconv"
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

// Resolve resolves the given query against the given unstructured object.
// NOTE: Resolutions resulting in composite values for label keys and values are not supported, owing to upstream
// limitations: https://github.com/kubernetes/apimachinery/blob/v0.31.0/pkg/apis/meta/v1/unstructured/helpers_test.go#L121.
func (ur *UnstructuredResolver) Resolve(query string, unstructuredObjectMap map[string]interface{}) map[string]string {
	logger := ur.logger.WithValues("query", query)
	gotResolved, found, err := unstructured.NestedFieldNoCopy(unstructuredObjectMap, strings.Split(query, ".")...)

	if !found {
		return ur.unresolved(logger, query)
	}

	if err != nil {
		logger.V(1).Info("ignoring resolution for query", "info", err)

		return ur.unresolved(logger, query)
	}

	return map[string]string{query: fmt.Sprintf("%v", gotResolved)}
}

// unresolved handles a query that does not resolve to a field. A numeric literal
// (for example, "1") is returned as its own value; anything else returns an empty
// result, so the metric (or the label) of the object is skipped instead of being
// written with the text of the query as its value.
func (ur *UnstructuredResolver) unresolved(logger klog.Logger, query string) map[string]string {
	if _, err := strconv.ParseFloat(query, 64); err == nil {
		return map[string]string{query: query}
	}
	logger.V(2).Info("query did not resolve to a field, skipping")

	return map[string]string{}
}
