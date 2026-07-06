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
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/iancoleman/strcase"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/metricutil"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/resolver"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/klog/v2"
)

const (
	// In convention with kube-state-metrics, we prefix all metrics with
	// `kube_customresource_` to explicitly denote that these are custom resource
	// user-generated metrics (and have no stability).
	kubeCustomResourcePrefix = "kube_customresource_"
	// expandedValueSentinel is a key used in resolvedExpandedLabelSet to carry
	// per-sample metric values when the value expression resolves to a list. The
	// NUL byte cannot appear in a Prometheus label name.
	expandedValueSentinel = "\x00"
)

// stringBuilderPool pools strings.Builder instances to reduce GC pressure
// during metric generation. It does so by cutting down on the number of
// allocations and deallocations of strings.Builder objects, which can be
// significant when generating a large number of metrics, especially in
// high-cardinality scenarios.
// listIndexRegex matches resolver keys of the form "fieldParent#N" used for list expansion.
var listIndexRegex = regexp.MustCompile(`.+#\d+`) //nolint:forbidigo // package-level

// nonWordRegex matches non-alphanumeric characters for label key sanitization.
var nonWordRegex = regexp.MustCompile(`\W`) //nolint:forbidigo // package-level

var stringBuilderPool = sync.Pool{
	New: func() interface{} {
		return &strings.Builder{}
	},
}

func getBuilder() *strings.Builder {
	b, ok := stringBuilderPool.Get().(*strings.Builder)
	if !ok {
		return &strings.Builder{}
	}

	return b
}

func putBuilder(b *strings.Builder) {
	b.Reset()
	stringBuilderPool.Put(b)
}

// MetricKind represents the OpenMetrics metric type for a family.
// See the whitepaper for the rationale behind these types:
// https://github.com/prometheus/OpenMetrics/blob/v1.0.0/specification/OpenMetrics.md
type MetricKind = metricutil.MetricKind

const (
	// MetricKindGauge represents an OM1 gauge: can be any float, including NaN
	// and negative. This was pinned to `gauge` to avoid ingestion issues
	// with different backends (see expfmt.MetricFamilyToOpenMetrics godoc) that
	// may not recognize all metrics under the OpenMetrics spec.
	// Refer https://github.com/kubernetes/kube-state-metrics/pull/2270 for more details.
	// Prometheus' OM1 implementation supports Counters, and it'd be nice to
	// allow users to make that distinction when resolving metrics. Info and
	// Stateset will need to be supported here as soon as they are in Prometheus.
	// Refer https://pkg.go.dev/github.com/prometheus/common@v0.67.5/expfmt#MetricFamilyToOpenMetrics for OM1 details.
	MetricKindGauge = metricutil.MetricKindGauge
	// MetricKindCounter represents an OM1 counter (*_total): monotonically increasing, non-NaN, non-negative.
	MetricKindCounter = metricutil.MetricKindCounter
	// MetricKindDefault is the default metric kind when not specified.
	MetricKindDefault = MetricKindGauge
)

// FamilyType represents a metric family.
type FamilyType struct {
	v1alpha1.Family

	logger              klog.Logger
	celCostLimit        uint64
	celTimeout          time.Duration
	celEvaluations      *prometheus.CounterVec
	managedRMMNamespace string
	managedRMMName      string
	createdAt           time.Time
	cutoff              atomic.Bool
	starlarkResolver    *resolver.StarlarkResolver
}

// SetCutoff sets the cutoff state for this family.
func (f *FamilyType) SetCutoff(cutoff bool) {
	f.cutoff.Store(cutoff)
}

// IsCutoff returns whether this family is currently cut off.
func (f *FamilyType) IsCutoff() bool {
	return f.cutoff.Load()
}

// generatePeripheralMetric generates peripheral metrics wherever applicable.
func generatePeripheralMetric(familyRawBuilder *strings.Builder, familyName string, kind MetricKind, createdAt time.Time) {
	// Emit a single _created sample at the end of the family, not once per
	// metric or per expanded sample. The reference implementation (client_python)
	// confirms _created carries no labels and is a family-level timestamp.
	// NOTE "_created" will forever be the only peripheral metric we generate,
	// since other (peripheral) ones listed in [1] are out of scope.
	// [1]:https://github.com/prometheus/client_python/blob/8673912276bdca7ddbca5d163eb11422b546bffb/prometheus_client/registry.py#L76-L80
	if kind == MetricKindCounter {
		createdSampleName := kubeCustomResourcePrefix + strings.TrimSuffix(familyName, "_total") + "_created"
		createdValue := fmt.Sprintf("%f", float64(createdAt.UnixNano())/1e9)

		familyRawBuilder.WriteString("# HELP " + createdSampleName + " Time at which " + kubeCustomResourcePrefix + familyName + " was created.")
		familyRawBuilder.WriteString("\n# TYPE " + createdSampleName + " " + string(MetricKindCounter))
		familyRawBuilder.WriteByte('\n')
		familyRawBuilder.WriteString(createdSampleName)
		familyRawBuilder.WriteByte(' ')
		familyRawBuilder.WriteString(createdValue)
	}
}

// buildMetricString returns the given family in its byte representation and the sample count.
// If the family is cut off due to cardinality limits, it returns an empty string and 0.
func (f *FamilyType) buildMetricString(unstructured *unstructured.Unstructured) (string, int64) {
	logger := f.logger.WithValues("family", f.Name)

	if f.IsCutoff() {
		logger.V(1).Info("Family is cut off due to cardinality limits, skipping metric generation")

		return "", 0
	}

	// Use Starlark resolver if configured
	if f.starlarkResolver != nil {
		return f.buildMetricStringFromStarlark(unstructured)
	}

	familyRawBuilder := getBuilder()
	defer putBuilder(familyRawBuilder)

	var sampleCount int64

	for i := range f.Metrics {
		metric := &f.Metrics[i]
		metricRawBuilder := getBuilder()

		// Combine metric labels with family labels
		metricLabels := inheritMetricLabels(f, metric)

		resolverInstance, err := f.resolver(metric.Resolver)
		if err != nil {
			logger.V(1).Error(fmt.Errorf("error resolving metric: %w", err), "skipping")
			putBuilder(metricRawBuilder)

			continue
		}

		resolvedLabelKeys, resolvedLabelValues, resolvedExpandedLabelSet := resolveLabels(metricLabels, resolverInstance, unstructured.Object)

		resolvedValue, ok := resolveMetricValue(resolverInstance, metric.Value, unstructured.Object, resolvedExpandedLabelSet)
		if !ok {
			logger.V(1).Error(fmt.Errorf("error resolving metric value %q", metric.Value), "skipping")
			putBuilder(metricRawBuilder)

			continue
		}

		samples, err := writeMetricSamplesWithCount(metricRawBuilder, f.Name, f.kind(), unstructured, resolvedLabelKeys, resolvedLabelValues, resolvedExpandedLabelSet, resolvedValue, logger)
		if err != nil {
			putBuilder(metricRawBuilder)

			continue
		}

		sampleCount += samples

		familyRawBuilder.WriteString(metricRawBuilder.String())
		putBuilder(metricRawBuilder)
	}

	return familyRawBuilder.String(), sampleCount
}

// buildMetricStringFromStarlark resolves metrics using the Starlark resolver.
func (f *FamilyType) buildMetricStringFromStarlark(unstr *unstructured.Unstructured) (string, int64) {
	logger := f.logger.WithValues("family", f.Name)

	families, err := f.starlarkResolver.Resolve(unstr.Object)
	if err != nil {
		logger.V(1).Error(err, "Starlark generation failed")

		return "", 0
	}

	if len(families) == 0 {
		return "", 0
	}

	familyRawBuilder := getBuilder()
	defer putBuilder(familyRawBuilder)

	var sampleCount int64

	for _, genFamily := range families {
		for _, sample := range genFamily.Samples {
			familyRawBuilder.WriteString(kubeCustomResourcePrefix + f.Name)

			// Build label string
			var labelKeys, labelValues []string
			for k, v := range sample.Labels {
				labelKeys = append(labelKeys, sanitizeKey(k))
				labelValues = append(labelValues, v)
			}

			sortLabels(labelKeys, labelValues)

			// Format the metric value
			valueStr := strconv.FormatFloat(sample.Value, 'f', -1, 64)

			if err := writeMetricTo(
				familyRawBuilder,
				unstr.GroupVersionKind().Group,
				unstr.GroupVersionKind().Version,
				unstr.GroupVersionKind().Kind,
				unstr.GetNamespace(),
				unstr.GetName(),
				valueStr,
				labelKeys, labelValues,
				f.kind(),
			); err != nil {
				logger.V(1).Error(err, "error writing Starlark-generated metric")

				continue
			}

			sampleCount++
		}
	}

	return familyRawBuilder.String(), sampleCount
}

// inheritMetricAttributes applies family-level labels to the metric.
func inheritMetricLabels(f *FamilyType, metric *v1alpha1.Metric) []v1alpha1.Label {
	return append(metric.Labels, f.Labels...)
}

// resolveMetricValue resolves the value expression for a single metric. If the
// resolver returns a scalar, it is returned directly. If the resolver returns
// a list (indexed keys like "fieldParent#N"), the values are collected in
// order and stored in resolvedExpandedLabelSet under the sentinel key so that
// writeMetricSamples can emit one sample per element.
func resolveMetricValue(resolverInstance resolver.Resolver, valueExpr string, obj map[string]any, resolvedExpandedLabelSet map[string][]string) (string, bool) {
	resolvedValueMap := resolverInstance.Resolve(valueExpr, obj)
	if resolvedValue, found := resolvedValueMap[valueExpr]; found {
		return resolvedValue, true
	}

	expandedValues := collectIndexedResolvedValues(resolvedValueMap)

	if len(expandedValues) == 0 {
		return "", false
	}

	resolvedExpandedLabelSet[expandedValueSentinel] = expandedValues

	return "", true
}

// resolveLabels resolves label keys and values including handling of composite map/list structures.
// Labels with names starting with "_" are treated as map expansion markers: the map keys from the
// resolved value become label names directly (useful for converting k8s labels to Prometheus labels).
func resolveLabels(labels []v1alpha1.Label, resolverInstance resolver.Resolver, obj map[string]any) ([]string, []string, map[string][]string) {
	var (
		resolvedLabelKeys        []string
		resolvedLabelValues      []string
		resolvedExpandedLabelSet = make(map[string][]string)
	)

	for _, label := range labels {
		resolvedLabelset := resolverInstance.Resolve(label.Value, obj)
		// If the query is found in the resolved labelset, it means we are dealing with non-composite value(s).
		// For e.g., consider:
		// * `name: o.metadata.name` -> `o.metadata.name: foo`
		// * `v: o.spec.versions` -> `v#0: [v1, v2]` // no `o.spec.versions` in the resolved labelset
		if val, ok := resolvedLabelset[label.Value]; ok {
			resolvedLabelValues = append(resolvedLabelValues, val)
			resolvedLabelKeys = append(resolvedLabelKeys, sanitizeKey(label.Name))

			continue
		}

		// Check if this is a map expansion label (name starts with "_").
		// In this case, map keys become label names directly without concatenation.
		isMapExpansion := strings.HasPrefix(label.Name, "_")

		// Collect list-indexed values in deterministic suffix order (#0, #1, ...)
		// to match the order used by resolveMetricValue. Map iteration order is
		// non-deterministic and would decouple labels from their metric values.
		sanitizedName := sanitizeKey(label.Name)
		resolvedExpandedLabelSet[sanitizedName] = append(resolvedExpandedLabelSet[sanitizedName], collectIndexedResolvedValues(resolvedLabelset)...)

		// Process non-list entries (map keys, non-composite values).
		for k, v := range resolvedLabelset {
			if listIndexRegex.MatchString(k) {
				continue
			}

			resolvedLabelValues = append(resolvedLabelValues, v)

			if isMapExpansion {
				// Map expansion: use the map key directly as the label name
				resolvedLabelKeys = append(resolvedLabelKeys, sanitizeKey(k))
			} else {
				resolvedLabelKeys = append(resolvedLabelKeys, sanitizeKey(label.Name+k))
			}
		}
	}

	sortLabels(resolvedLabelKeys, resolvedLabelValues)

	return resolvedLabelKeys, resolvedLabelValues, resolvedExpandedLabelSet
}

// sortLabels sorts keys alphabetically and applies the same permutation to all
// parallel slices, so that positionally-correlated data stays aligned with its
// corresponding key after sorting. For example, given label keys
// ["Ready", "Degraded", "Progressing"] with label values
// ["True", "False", "Unknown"], sorting produces keys
// ["Degraded", "Progressing", "Ready"] and values ["False", "Unknown", "True"],
// preserving the pairing Degraded→"False", Progressing→"Unknown", Ready→"True".
func sortLabels(keys []string, parallel ...[]string) {
	indices := make([]int, len(keys))
	for i := range indices {
		indices[i] = i
	}

	slices.SortFunc(indices, func(a, b int) int {
		return strings.Compare(keys[a], keys[b])
	})

	reorder := func(s []string) {
		sorted := make([]string, len(s))
		for i, idx := range indices {
			sorted[i] = s[idx]
		}

		copy(s, sorted)
	}

	reorder(keys)

	for _, p := range parallel {
		if len(p) == len(keys) {
			reorder(p)
		}
	}
}

// collectIndexedResolvedValues returns resolver values stored under numbered
// keys with #0, #1, ... suffixes, preserving empty string values.
func collectIndexedResolvedValues(resolved map[string]string) []string {
	var values []string

	for i := 0; ; i++ {
		suffix := "#" + strconv.Itoa(i)

		var match string

		found := false

		for k, v := range resolved {
			if strings.HasSuffix(k, suffix) {
				match = v
				found = true

				break
			}
		}

		if !found {
			break
		}

		values = append(values, match)
	}

	return values
}

// sanitizeKey converts a label key to snake_case and strips non-alphanumeric characters.
func sanitizeKey(s string) string {
	return strcase.ToSnake(nonWordRegex.ReplaceAllString(s, "_"))
}

// extractAndSortExpandedMetricValues extracts expanded values from the sentinel key and co-sorts them with labels to maintain index correspondence.
// The sentinel key is removed from the expanded map after extraction.
// If there is a length mismatch between the expanded values and labels, a warning is logged and values are not co-sorted to avoid misalignment.
// If multiple label keys are present, they are co-sorted together based on the anchor key (the lexicographically smallest label key) to maintain their relative order.
func extractAndSortExpandedMetricValues(expanded map[string][]string, logger klog.Logger) []string {
	// Extract per-sample values stored under the sentinel when the value
	// expression resolved to a list. The sentinel is not a real label.
	// NOTE that we do not want resolver-specific logic making its way into
	// non-resolver-specific code, however, this is general enough that it can be
	// reasonably justified as an implementation detail of how we handle value
	// lists across resolvers.
	expandedValues := expanded[expandedValueSentinel]
	delete(expanded, expandedValueSentinel)

	// Co-sort all expanded arrays (labels + values) to maintain index
	// correspondence. Without this, sorting label values independently
	// decouples them from their metric values.
	if len(expanded) == 0 {
		return expandedValues
	}

	var sortKey string
	for k := range expanded {
		if sortKey == "" || k < sortKey {
			sortKey = k
		}
	}

	anchor := expanded[sortKey]
	if len(expandedValues) != len(anchor) {
		logger.V(1).Info("Mismatch in expanded label and value counts, skipping expanded label sorting", "labelCount", len(anchor), "valueCount", len(expandedValues))

		return expandedValues
	}

	parallel := make([][]string, 0, len(expanded)+1)

	for k := range expanded {
		if k != sortKey {
			parallel = append(parallel, expanded[k])
		}
	}

	parallel = append(parallel, expandedValues)
	sortLabels(anchor, parallel...)

	return expandedValues
}

// writeMetricSamplesWithCount writes single or expanded metric values and returns the sample count.
func writeMetricSamplesWithCount(
	builder *strings.Builder,
	name string,
	kind MetricKind,
	raw *unstructured.Unstructured,
	keys, values []string,
	expanded map[string][]string,
	value string,
	logger klog.Logger,
) (int64, error) {
	expandedValues := extractAndSortExpandedMetricValues(expanded, logger)

	var sampleCount int64

	expandedValueIndex := 0
	writeMetric := func(k, v []string) error {
		builder.WriteString(kubeCustomResourcePrefix + name)

		currentValue := value
		if expandedValueIndex < len(expandedValues) {
			currentValue = expandedValues[expandedValueIndex]
		}

		expandedValueIndex++
		sampleCount++

		return writeMetricTo(
			builder,
			raw.GroupVersionKind().Group,
			raw.GroupVersionKind().Version,
			raw.GroupVersionKind().Kind,
			raw.GetNamespace(),
			raw.GetName(),
			currentValue,
			k, v,
			kind,
		)
	}

	if len(expanded) == 0 {
		if len(expandedValues) == 0 {
			if err := writeSingleSample(writeMetric, keys, values, logger); err != nil {
				return 0, err
			}

			return sampleCount, nil
		}
		// Value-only expansion: one sample per expanded value, same label set.
		for range expandedValues {
			if err := writeSingleSample(writeMetric, keys, values, logger); err != nil {
				return sampleCount, err
			}
		}

		return sampleCount, nil
	}

	if err := writeExpandedSamples(writeMetric, keys, values, expanded, logger); err != nil {
		return sampleCount, err
	}

	return sampleCount, nil
}

// writeSingleSample writes a single metric sample.
func writeSingleSample(writeFunc func([]string, []string) error, keys, values []string, logger klog.Logger) error {
	if err := writeFunc(keys, values); err != nil {
		logger.V(1).Error(fmt.Errorf("error writing metric: %w", err), "skipping")

		return err
	}

	return nil
}

// writeExpandedSamples writes metric samples for list-based label values.
func writeExpandedSamples(writeFunc func([]string, []string) error, labelKeys, labelValues []string, expanded map[string][]string, logger klog.Logger) error {
	var seriesToGenerate int

	// Sort expanded keys to ensure deterministic label ordering.
	// Map iteration order is non-deterministic, so we must sort explicitly.
	for _, k := range slices.Sorted(maps.Keys(expanded)) {
		labelKeys = append(labelKeys, k)

		if len(expanded[k]) > seriesToGenerate {
			seriesToGenerate = len(expanded[k])
		}
	}

	for range seriesToGenerate {
		ephemeralLabelValues := labelValues
		// Don't iterate over the `expanded` map, as the order of keys is unstable.
		expansionKeys := labelKeys[len(labelKeys)-len(expanded):]
		for _, k := range expansionKeys {
			vs := expanded[k]
			if len(vs) == 0 {
				ephemeralLabelValues = append(ephemeralLabelValues, "")

				continue
			}

			ephemeralLabelValues = append(ephemeralLabelValues, vs[0])
			expanded[k] = vs[1:]
		}
		// Pass a copy of the label keys and values to avoid modifying the original slices.
		if err := writeFunc(slices.Clone(labelKeys), slices.Clone(ephemeralLabelValues)); err != nil {
			logger.V(1).Error(fmt.Errorf("error writing metric: %w", err), "skipping")

			return err
		}
	}

	return nil
}

func (f *FamilyType) resolver(inheritedResolver v1alpha1.ResolverType) (resolver.Resolver, error) {
	if inheritedResolver == v1alpha1.ResolverTypeNone {
		inheritedResolver = f.Resolver
	}

	switch inheritedResolver {
	case v1alpha1.ResolverTypeNone:
		return nil, fmt.Errorf("no resolver specified for family %q: must set resolver at store, family, or metric level", f.Name)
	case v1alpha1.ResolverTypeUnstructured:
		return resolver.NewUnstructuredResolver(f.logger), nil
	case v1alpha1.ResolverTypeCEL:
		costLimit := f.celCostLimit
		if costLimit == 0 {
			costLimit = uint64(resolver.CELDefaultCostLimit)
		}

		timeout := f.celTimeout
		if timeout == 0 {
			timeout = time.Duration(resolver.CELDefaultTimeout) * time.Second
		}

		return resolver.NewCELResolver(f.logger, costLimit, timeout, f.celEvaluations, f.managedRMMNamespace, f.managedRMMName, f.Name), nil
	case v1alpha1.ResolverTypeStarlark:
		// Starlark resolver uses a different resolution pattern (script-based).
		// If we reach here, it means the family has resolver=starlark without a starlark config.
		return nil, fmt.Errorf("starlark resolver requires starlark config in family %q", f.Name)
	default:
		return nil, fmt.Errorf("error resolving metric: unknown resolver %q", inheritedResolver)
	}
}

// buildHeaders generates the header for the given family.
// https://github.com/prometheus/OpenMetrics/blob/v1.0.0/specification/OpenMetrics.md#gauge:
// "Even if they only ever go in one direction, they might still be gauges and not counters."
//
// For now, we treat all metrics as guages, unless their family name ends
// with "_total", in which case we treat them as counters. This behavior will
// be revised once Info and Stateset metric types are supported in
// Prometheus.
// kind deduces the OpenMetrics metric type from the family name.
// A name ending with _total is treated as a counter; everything else is a gauge.
func (f *FamilyType) kind() MetricKind {
	if strings.HasSuffix(f.Name, "_total") {
		return MetricKindCounter
	}

	return MetricKindGauge
}

func (f *FamilyType) buildHeaders() string {
	header := strings.Builder{}
	header.WriteString("# HELP " + kubeCustomResourcePrefix + f.Name + " " + f.Help)
	header.WriteString("\n")
	header.WriteString("# TYPE " + kubeCustomResourcePrefix + f.Name + " " + string(f.kind()))

	return header.String()
}

// buildPeripheralHeader returns headers for peripheral metrics like _created, if applicable.
func (f *FamilyType) buildPeripheralHeader() string {
	if f.kind() != MetricKindCounter {
		return ""
	}

	var b strings.Builder

	generatePeripheralMetric(&b, f.Name, f.kind(), f.createdAt)

	return b.String()
}
