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
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/interpreter"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/metricutil"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/options"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"
)

var (
	CELDefaultCostLimit = options.CELDefaultCostLimit
	CELDefaultTimeout   = options.CELDefaultTimeout
)

// CELResolver represents a resolver for CEL expressions.
type CELResolver struct {
	logger                     klog.Logger
	costLimit                  uint64
	timeout                    time.Duration
	expressionEvaluationMetric *prometheus.CounterVec
	managedRMMNamespace        string
	managedRMMName             string
	familyName                 string
}

// CELResolver implements the Resolver interface.
var _ Resolver = &CELResolver{}

// NewCELResolver returns a new limits-aware CEL resolver.
func NewCELResolver(logger klog.Logger, costLimit uint64, timeout time.Duration, celEvaluations *prometheus.CounterVec, rmmNamespace, rmmName, familyName string) *CELResolver {
	return &CELResolver{
		logger:                     logger,
		costLimit:                  costLimit,
		timeout:                    timeout,
		expressionEvaluationMetric: celEvaluations,
		managedRMMNamespace:        rmmNamespace,
		managedRMMName:             rmmName,
		familyName:                 familyName,
	}
}

// costEstimator helps estimate the runtime cost of CEL queries.
type costEstimator struct{}

// costEstimator implements the ActualCostEstimator interface.
var _ interpreter.ActualCostEstimator = costEstimator{}

// CEL cost budgets for custom functions. Higher values reflect more complex operations.
const (
	celCostUnixSeconds uint64 = 10
	celCostQuantity    uint64 = 10
	celCostLabelPrefix uint64 = 20
	celCostNow         uint64 = 1
)

// CallCost sets the runtime cost for CEL queries on a per-function basis.
func (ce costEstimator) CallCost(function string, _ string, _ []ref.Val, _ ref.Val) *uint64 {
	customFunctionsCosts := map[string]uint64{
		"unixSeconds": celCostUnixSeconds,
		"quantity":    celCostQuantity,
		"labelPrefix": celCostLabelPrefix,
		"now":         celCostNow,
	}
	estimatedCost := 1 + customFunctionsCosts[function]

	return &estimatedCost
}

// Resolve resolves the given query against the given unstructured object.
func (cr *CELResolver) Resolve(query string, unstructuredObjectMap map[string]interface{}) map[string]string {
	logger := cr.logger.WithValues("query", query)

	type result struct {
		output map[string]string
		err    error
	}

	resultChan := make(chan result, 1)

	go func() {
		output, err := cr.resolveWithTimeout(query, unstructuredObjectMap, logger)
		resultChan <- result{output: output, err: err}
	}()

	timer := time.NewTimer(cr.timeout)
	defer timer.Stop()

	select {
	case res := <-resultChan:
		if res.err != nil {
			logger.V(1).Info("ignoring resolution for query", "info", res.err)

			if cr.expressionEvaluationMetric != nil {
				cr.expressionEvaluationMetric.WithLabelValues(cr.managedRMMNamespace, cr.managedRMMName, cr.familyName, "error").Inc()
			}

			return cr.defaultMapping(query)
		}

		if cr.expressionEvaluationMetric != nil {
			cr.expressionEvaluationMetric.WithLabelValues(cr.managedRMMNamespace, cr.managedRMMName, cr.familyName, "success").Inc()
		}

		return res.output
	case <-timer.C:
		logger.Error(fmt.Errorf("CEL query exceeded timeout of %v", cr.timeout), "ignoring resolution for query")

		if cr.expressionEvaluationMetric != nil {
			cr.expressionEvaluationMetric.WithLabelValues(cr.managedRMMNamespace, cr.managedRMMName, cr.familyName, "timeout").Inc()
		}

		return cr.defaultMapping(query)
	}
}

func (cr *CELResolver) resolveWithTimeout(query string, unstructuredObjectMap map[string]interface{}, logger klog.Logger) (map[string]string, error) {
	env, err := cr.createEnvironment()
	if err != nil {
		logger.Error(err, "ignoring resolution for query")

		return nil, err
	}

	ast, iss := env.Parse(query)
	if iss.Err() != nil {
		logger.Error(fmt.Errorf("error parsing CEL query: %w", iss.Err()), "ignoring resolution for query")

		return nil, iss.Err()
	}

	program, err := cr.compileProgram(env, ast)
	if err != nil {
		logger.Error(err, "ignoring resolution for query")

		return nil, err
	}

	out, evalDetails, err := cr.evaluateProgram(program, unstructuredObjectMap)
	cr.addCostLogging(logger, evalDetails)

	if err != nil {
		return nil, err
	}

	return cr.processResult(query, out), nil
}

func (cr *CELResolver) createEnvironment() (*cel.Env, error) {
	return cel.NewEnv(
		cel.CrossTypeNumericComparisons(true),
		cel.DefaultUTCTimeZone(true),
		cel.EagerlyValidateDeclarations(true),
		cel.Function("unixSeconds",
			cel.Overload("unixSeconds_string",
				[]*cel.Type{cel.StringType},
				cel.DoubleType,
				cel.UnaryBinding(unixSecondsBinding),
			),
		),
		cel.Function("quantity",
			cel.Overload("quantity_string",
				[]*cel.Type{cel.StringType},
				cel.DoubleType,
				cel.UnaryBinding(quantityBinding),
			),
		),
		cel.Function("labelPrefix",
			cel.Overload("labelPrefix_map_string",
				[]*cel.Type{cel.MapType(cel.StringType, cel.StringType), cel.StringType},
				cel.MapType(cel.StringType, cel.StringType),
				cel.BinaryBinding(labelPrefixBinding),
			),
		),
		cel.Function("now",
			cel.Overload("now_void",
				[]*cel.Type{},
				cel.DoubleType,
				cel.FunctionBinding(nowBinding),
			),
		),
	)
}

// unixSecondsBinding implements the logic for the unixSeconds function, which
// parses an RFC3339 timestamp string and returns its Unix seconds as a double.
// For e.g., unixSeconds("2024-01-15T10:30:00Z") returns 1705315800.0.
func unixSecondsBinding(arg ref.Val) ref.Val {
	s, ok := arg.Value().(string)
	if !ok {
		return types.NewErr("unixSeconds: expected string argument")
	}

	if s == "" {
		return types.Double(0)
	}

	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return types.NewErr("unixSeconds: failed to parse timestamp %q: %v", s, err)
	}

	return types.Double(float64(t.Unix()))
}

// quantityBinding implements the logic for the quantity function, which parses
// a Kubernetes resource quantity string and returns its value as a double.
// For e.g., quantity("100m") returns 0.1; quantity("1Gi") returns 1073741824.0.
func quantityBinding(arg ref.Val) ref.Val {
	s, ok := arg.Value().(string)
	if !ok {
		return types.NewErr("quantity: expected string argument")
	}

	if s == "" {
		return types.Double(0)
	}

	q, err := resource.ParseQuantity(s)
	if err != nil {
		return types.NewErr("quantity: failed to parse quantity %q: %v", s, err)
	}

	return types.Double(q.AsApproximateFloat64())
}

// labelPrefixBinding implements the logic for the labelPrefix function, which
// takes a map and a prefix string, and returns a new map with all keys
// prefixed and sanitized for Prometheus label compatibility. Keys are
// sanitized: non-alphanumeric characters (except _) are replaced with _.
// For e.g., `labelPrefix({"app": "test", "env/type": "prod"}, "label_")`
// returns `{"label_app": "test", "label_env_type": "prod"}`.
func labelPrefixBinding(lhs, rhs ref.Val) ref.Val {
	labels, ok := lhs.Value().(map[string]any)
	if !ok {
		if refMap, ok := lhs.Value().(map[ref.Val]ref.Val); ok {
			labels = make(map[string]any)

			for k, v := range refMap {
				if ks, ok := k.Value().(string); ok {
					labels[ks] = v.Value()
				}
			}
		} else {
			return types.NewErr("labelPrefix: expected map argument")
		}
	}

	prefix, ok := rhs.Value().(string)
	if !ok {
		return types.NewErr("labelPrefix: expected string prefix")
	}

	result := make(map[string]string)

	for k, v := range labels {
		sanitized := metricutil.SanitizeLabelKey(k)

		if vs, ok := v.(string); ok {
			result[prefix+sanitized] = vs
		} else {
			result[prefix+sanitized] = fmt.Sprintf("%v", v)
		}
	}

	return types.NewStringStringMap(types.DefaultTypeAdapter, result)
}

// nowBinding implements the logic for the now function, which returns the
// current time as Unix seconds (a double). This enables computing durations
// by subtracting a resource's lastTransitionTime from the current time,
// e.g., `now() - unixSeconds(o.status.conditions[0].lastTransitionTime)`.
// Named `now` rather than `timestamp` so we do not shadow CEL's built-in
// `timestamp(string)` cast.
func nowBinding(_ ...ref.Val) ref.Val {
	return types.Double(float64(time.Now().Unix()))
}

func (cr *CELResolver) compileProgram(env *cel.Env, ast *cel.Ast) (cel.Program, error) {
	return env.Program(
		ast,
		cel.CostLimit(cr.costLimit),
		cel.CostTracking(new(costEstimator)),
	)
}

func (cr *CELResolver) evaluateProgram(program cel.Program, obj map[string]interface{}) (ref.Val, *cel.EvalDetails, error) {
	return program.Eval(map[string]interface{}{"o": obj})
}

func (cr *CELResolver) addCostLogging(logger klog.Logger, evalDetails *cel.EvalDetails) {
	logger = logger.WithValues("costLimit", cr.costLimit, "timeout", cr.timeout)
	if evalDetails != nil {
		logger = logger.WithValues("queryCost", *evalDetails.ActualCost())
	}

	logger.V(4).Info("CEL query runtime cost")
}

func (cr *CELResolver) processResult(query string, out ref.Val) map[string]string {
	// Derive a stable key prefix for list results. Strip from the first '('
	// (and the method name immediately before it) so that dots inside
	// arguments never corrupt the prefix. For example:
	//   o.spec.conditions.map(c, c.type)           → "conditions"
	//   o.spec.items.filter(x, ...).map(c, c.type) → "items"
	// Using the first '(' is correct: the result always belongs to the field
	// being iterated, regardless of how many chained calls follow.
	base := query
	if idx := strings.IndexByte(query, '('); idx > 0 {
		base = query[:idx] // drop arguments: "o.spec.conditions.map"
		if dot := strings.LastIndex(base, "."); dot >= 0 {
			base = base[:dot] // drop method name: "o.spec.conditions"
		}
	}

	resolvedFieldParent := base[strings.LastIndex(base, ".")+1:]

	switch out.Type() {
	case types.BoolType, types.DoubleType, types.IntType, types.StringType, types.UintType:
		return map[string]string{query: fmt.Sprintf("%v", out.Value())}
	case types.MapType:
		return cr.resolveMap(&out)
	case types.ListType:
		return cr.resolveList(&out, resolvedFieldParent)
	case types.NullType:
		return map[string]string{query: "<nil>"}
	default:
		cr.logger.Error(fmt.Errorf("unsupported output type %q", out.Type()), "ignoring resolution for query")

		return cr.defaultMapping(query)
	}
}

func (cr *CELResolver) resolveList(out *ref.Val, fieldParent string) map[string]string {
	m := map[string]string{}

	switch v := (*out).Value().(type) {
	case []interface{}:
		// Native Go slice; a list field from an unstructured object.
		cr.resolveListInner(v, m, fieldParent)
	case []ref.Val:
		// CEL-typed list; the result of a .map() or .filter() call.
		native := make([]interface{}, len(v))
		for i, elem := range v {
			native[i] = elem.Value()
		}

		cr.resolveListInner(native, m, fieldParent)
	default:
		cr.logger.V(1).Error(fmt.Errorf("unsupported list value type %T", (*out).Value()), "ignoring resolution for query")

		return nil
	}

	return m
}

func (cr *CELResolver) resolveMap(out *ref.Val) map[string]string {
	m := map[string]string{}

	switch outMap := (*out).Value().(type) {
	case map[string]interface{}:
		cr.resolveMapInner(outMap, m)
	case map[string]string:
		// Direct string-string map (from labelPrefix function)
		for k, v := range outMap {
			m[k] = v
		}
	default:
		cr.logger.V(1).Error(fmt.Errorf("error casting output to map, got %T", (*out).Value()), "ignoring resolution for query")

		return nil
	}

	return m
}

func (cr *CELResolver) resolveListInner(list []interface{}, out map[string]string, fieldParent string) {
	for i, v := range list {
		switch v := v.(type) {
		case string, int, int64, uint, uint64, float64, bool:
			out[fieldParent+"#"+strconv.Itoa(i)] = fmt.Sprintf("%v", v)
		case []interface{}:
			cr.resolveListInner(v, out, fieldParent)
		case map[string]interface{}:
			cr.resolveMapInner(v, out)
		default:
			cr.logger.V(1).Error(fmt.Errorf("encountered composite value %q at index %d, skipping", v, i), "ignoring resolution for query")
		}
	}
}

func (cr *CELResolver) resolveMapInner(m map[string]interface{}, out map[string]string) {
	for k, v := range m {
		switch v := v.(type) {
		case string, int, uint, float64, bool:
			out[k] = fmt.Sprintf("%v", v)
		case []interface{}:
			cr.resolveListInner(v, out, k)
		case map[string]interface{}:
			cr.resolveMapInner(v, out)
		default:
			cr.logger.V(1).Error(fmt.Errorf("encountered composite value %q at key %q, skipping", v, k), "ignoring resolution for query")
		}
	}
}

// defaultMapping returns the query itself as the resolved value, signalling a
// resolution failure.
func (cr *CELResolver) defaultMapping(query string) map[string]string {
	cr.logger.V(2).Info("query fell back to default mapping (will be skipped at write time)", "query", query)

	return map[string]string{query: query}
}
