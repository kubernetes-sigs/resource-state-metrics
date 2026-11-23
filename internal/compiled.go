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

package internal

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/resolver"
	"k8s.io/klog/v2"
)

// compiledLabel is a label whose expression was compiled once at load.
type compiledLabel struct {
	name string
	expr resolver.Expression
}

// compiledMetric is a metric whose label and value expressions were compiled once at load.
type compiledMetric struct {
	resolver v1alpha1.ResolverType
	traits   resolver.ExpressionTraits
	labels   []compiledLabel
	value    resolver.Expression
}

// compile compiles the family's script or every metric expression once, logging and skipping what does not compile.
func (f *FamilyType) compile() {
	f.compileOnce.Do(func() {
		logger := f.logger.WithValues("family", f.Name)

		if f.starlarkResolver != nil {
			script, err := f.starlarkResolver.Compile(f.Starlark.Script)
			if err != nil {
				logger.Error(err, "skipping family, the starlark script does not compile")

				return
			}

			f.script = script

			return
		}

		for i := range f.Metrics {
			metric := &f.Metrics[i]

			compiled, err := f.compileMetric(metric)
			if err != nil {
				logger.Error(err, "skipping metric", "value", metric.Value)

				continue
			}

			f.compiledMetrics = append(f.compiledMetrics, compiled)
		}
	})
}

// compileMetric compiles one metric's labels and value with the resolver the metric, the family or the store selected.
func (f *FamilyType) compileMetric(metric *v1alpha1.Metric) (compiledMetric, error) {
	kind, exprResolver, err := f.expressionResolver(metric.Resolver)
	if err != nil {
		return compiledMetric{}, err
	}

	compiled := compiledMetric{resolver: kind, traits: exprResolver.Traits()}

	for _, label := range inheritMetricLabels(f, metric) {
		expr, err := exprResolver.Compile(label.Value)
		if err != nil {
			return compiledMetric{}, fmt.Errorf("label %q: %w", label.Name, err)
		}

		compiled.labels = append(compiled.labels, compiledLabel{name: label.Name, expr: expr})
	}

	compiled.value, err = exprResolver.Compile(metric.Value)
	if err != nil {
		return compiledMetric{}, fmt.Errorf("value: %w", err)
	}

	return compiled, nil
}

// expressionResolver returns the resolver for a metric, the metric's own choice first, then the family's.
func (f *FamilyType) expressionResolver(metricResolver v1alpha1.ResolverType) (v1alpha1.ResolverType, resolver.ExpressionResolver, error) {
	kind := metricResolver
	if kind == v1alpha1.ResolverTypeNone {
		kind = f.Resolver
	}

	switch kind {
	case v1alpha1.ResolverTypeNone:
		return kind, nil, fmt.Errorf("no resolver specified for family %q: must set resolver at store, family, or metric level", f.Name)
	case v1alpha1.ResolverTypeUnstructured:
		return kind, resolver.NewUnstructuredResolver(f.logger), nil
	case v1alpha1.ResolverTypeCEL:
		costLimit := f.celCostLimit
		if costLimit == 0 {
			costLimit = uint64(resolver.CELDefaultCostLimit)
		}

		timeout := f.celTimeout
		if timeout == 0 {
			timeout = time.Duration(resolver.CELDefaultTimeout) * time.Second
		}

		return kind, resolver.NewCELResolver(f.logger, costLimit, timeout, f.celEvaluations, f.managedRMMNamespace, f.managedRMMName, f.Name), nil
	case v1alpha1.ResolverTypeStarlark:
		return kind, nil, fmt.Errorf("starlark resolver requires starlark config in family %q", f.Name)
	default:
		return kind, nil, fmt.Errorf("error resolving metric: unknown resolver %q", kind)
	}
}

// labelSet collects the flat labels and the expanded label sets of one metric evaluation.
type labelSet struct {
	keys     []string
	values   []string
	expanded map[string][]string
}

// add appends one flat label.
func (ls *labelSet) add(name, value string) {
	ls.keys = append(ls.keys, sanitizeKey(name))
	ls.values = append(ls.values, value)
}

// addValue flattens one label's value. A scalar is one label. A list expands into one sample per item under the label
// name, and an empty list or map into zero samples. A map adds one label per entry, keyed by the entry alone when the
// label name starts with "_" and by the label name plus the entry otherwise, nested maps are flattened the same way
// and a nested list expands under the label name.
func (ls *labelSet) addValue(name string, value resolver.Value) {
	key := sanitizeKey(name)

	switch value.Kind {
	case resolver.KindScalar:
		ls.add(name, value.Scalar)
	case resolver.KindList:
		ls.expanded[key] = append(ls.expanded[key], ls.flattenList(name, value.List)...)
	case resolver.KindMap:
		if len(value.Map) == 0 {
			ls.expanded[key] = nil

			return
		}

		ls.addMap(name, strings.HasPrefix(name, "_"), value.Map)
	case resolver.KindInvalid:
	}
}

// flattenList returns the scalar items of a list in order, flattening nested lists and adding nested maps as labels.
func (ls *labelSet) flattenList(name string, list []resolver.Value) []string {
	var items []string

	for _, item := range list {
		switch item.Kind {
		case resolver.KindScalar:
			items = append(items, item.Scalar)
		case resolver.KindList:
			items = append(items, ls.flattenList(name, item.List)...)
		case resolver.KindMap:
			ls.addMap(name, strings.HasPrefix(name, "_"), item.Map)
		case resolver.KindInvalid:
		}
	}

	return items
}

// addMap adds the scalar entries of a map as labels in key order and expands nested lists under the label name.
func (ls *labelSet) addMap(name string, expand bool, entries map[string]resolver.Value) {
	key := sanitizeKey(name)

	for _, k := range slices.Sorted(maps.Keys(entries)) {
		item := entries[k]

		switch item.Kind {
		case resolver.KindScalar:
			if expand {
				ls.add(k, item.Scalar)
			} else {
				ls.add(name+k, item.Scalar)
			}
		case resolver.KindList:
			ls.expanded[key] = append(ls.expanded[key], ls.flattenList(name, item.List)...)
		case resolver.KindMap:
			ls.addMap(name, expand, item.Map)
		case resolver.KindInvalid:
		}
	}
}

// scalarItems returns the scalar items of a value list in order, flattening nested lists and ignoring anything else.
func scalarItems(list []resolver.Value) []string {
	var items []string

	for _, item := range list {
		switch item.Kind {
		case resolver.KindScalar:
			items = append(items, item.Scalar)
		case resolver.KindList:
			items = append(items, scalarItems(item.List)...)
		case resolver.KindMap, resolver.KindInvalid:
		}
	}

	return items
}

// evalLabels evaluates every label of a metric against one object. A failed or missing label is dropped and logged.
func (f *FamilyType) evalLabels(ctx context.Context, metric compiledMetric, obj map[string]any, logger klog.Logger) labelSet {
	set := labelSet{expanded: make(map[string][]string)}

	for _, label := range metric.labels {
		value, found, err := label.expr.Eval(ctx, obj)
		f.countEvaluation(metric.resolver, err)

		if err != nil {
			logger.V(1).Info("ignoring label, evaluation failed", "label", label.name, "info", err)

			continue
		}

		if !found {
			logger.V(2).Info("ignoring label, field not found", "label", label.name)

			continue
		}

		if !metric.declared(value.Kind) {
			logger.Error(fmt.Errorf("resolver %q returned a %s it did not declare", metric.resolver, value.Kind), "ignoring label", "label", label.name)

			continue
		}

		set.addValue(label.name, value)
	}

	sortLabels(set.keys, set.values)

	return set
}

// evalValue evaluates the metric value and returns the scalar, or the expanded values for a list, with ok false when
// there is no sample to write.
func (f *FamilyType) evalValue(ctx context.Context, metric compiledMetric, obj map[string]any, logger klog.Logger) (string, []string, bool, error) {
	value, found, err := metric.value.Eval(ctx, obj)
	f.countEvaluation(metric.resolver, err)

	if err != nil {
		return "", nil, false, err
	}

	if !found {
		logger.V(1).Info("skipping sample, value field not found")

		return "", nil, false, nil
	}

	if !metric.declared(value.Kind) {
		return "", nil, false, fmt.Errorf("resolver %q returned a %s it did not declare", metric.resolver, value.Kind)
	}

	switch value.Kind {
	case resolver.KindScalar:
		return value.Scalar, nil, true, nil
	case resolver.KindList:
		items := scalarItems(value.List)

		return "", items, len(items) > 0, nil
	case resolver.KindMap, resolver.KindInvalid:
	}

	return "", nil, false, fmt.Errorf("metric value resolved to a %s, a scalar or a list is needed", value.Kind)
}

// declared reports whether the metric's resolver said it can return this kind.
func (m compiledMetric) declared(kind resolver.Kind) bool {
	switch kind {
	case resolver.KindScalar:
		return true
	case resolver.KindList:
		return m.traits.Lists
	case resolver.KindMap:
		return m.traits.Maps
	case resolver.KindInvalid:
	}

	return false
}

// countEvaluation increments the resolver evaluation counter, and the deprecated CEL counter for a CEL metric.
func (f *FamilyType) countEvaluation(kind v1alpha1.ResolverType, err error) {
	result := "success"

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		result = "timeout"
	case err != nil:
		result = "error"
	}

	if f.resolverEvaluations != nil {
		f.resolverEvaluations.WithLabelValues(f.managedRMMNamespace, f.managedRMMName, f.Name, string(kind), result).Inc()
	}

	if kind == v1alpha1.ResolverTypeCEL && f.celEvaluations != nil {
		f.celEvaluations.WithLabelValues(f.managedRMMNamespace, f.managedRMMName, f.Name, result).Inc()
	}
}
