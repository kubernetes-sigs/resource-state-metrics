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
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/kubernetes-sigs/resource-state-metrics/pkg/metricutil"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/options"
	startime "go.starlark.net/lib/time"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/klog/v2"
)

var (
	// StarlarkDefaultMaxSteps is the default max execution steps.
	StarlarkDefaultMaxSteps = options.StarlarkDefaultMaxSteps
	// StarlarkDefaultTimeout is the default timeout in seconds.
	StarlarkDefaultTimeout = options.StarlarkDefaultTimeout
)

// ResolvedSample represents a single metric sample with labels and value.
type ResolvedSample struct {
	Labels map[string]string
	Value  float64
}

// ResolvedFamily represents a metric family resolved by Starlark.
type ResolvedFamily struct {
	Name    string
	Help    string
	Kind    string
	Samples []ResolvedSample
}

// StarlarkResolver executes Starlark scripts to resolve metrics.
// Unlike CEL and unstructured resolvers that evaluate individual expressions,
// Starlark resolves complete metric families from a script.
type StarlarkResolver struct {
	logger   klog.Logger
	script   string
	timeout  time.Duration
	maxSteps int
}

// NewStarlarkResolver creates a new StarlarkResolver.
func NewStarlarkResolver(logger klog.Logger, script string, timeout time.Duration, maxSteps int) *StarlarkResolver {
	if timeout == 0 {
		timeout = time.Duration(StarlarkDefaultTimeout) * time.Second
	}

	if maxSteps == 0 {
		maxSteps = StarlarkDefaultMaxSteps
	}

	return &StarlarkResolver{
		logger:   logger,
		script:   script,
		timeout:  timeout,
		maxSteps: maxSteps,
	}
}

// Resolve executes the Starlark script with the given object and returns resolved families.
// NOTE: The following "go.starlark.net/syntax.FileOptions" are enabled for "go.starlark.net/starlark.ExecFileOptions" (script execution):
// * `GlobalReassign`: Allow reassigning global variables in the script.
// * `Set`: Allow using the `set` statement in the script.
// * `TopLevelControl`: Allow using control flow statements (if, for, while) at the top level of the script.
// * `While`: Allow using `while` loops in the script.
func (sr *StarlarkResolver) Resolve(obj map[string]interface{}) ([]ResolvedFamily, error) {
	type result struct {
		families []ResolvedFamily
		err      error
	}

	thread := &starlark.Thread{
		Name: "resource-state-metrics-starlark",
		Print: func(_ *starlark.Thread, msg string) {
			sr.logger.V(2).Info("Starlark printer", "message", msg)
		},
	}
	if sr.maxSteps > 0 {
		thread.SetMaxExecutionSteps(uint64(sr.maxSteps))
	}

	resultChan := make(chan result, 1)

	go func() {
		families, err := sr.resolveWithSteps(thread, obj)
		resultChan <- result{families: families, err: err}
	}()

	timer := time.NewTimer(sr.timeout)
	defer timer.Stop()

	select {
	case res := <-resultChan:
		return res.families, res.err
	case <-timer.C:
		thread.Cancel("timeout exceeded")

		return nil, fmt.Errorf("starlark script exceeded timeout of %v", sr.timeout)
	}
}

func (sr *StarlarkResolver) resolveWithSteps(thread *starlark.Thread, obj map[string]interface{}) ([]ResolvedFamily, error) {
	predeclared := starlark.StringDict{
		"quantity_to_float": starlark.NewBuiltin("quantity_to_float", quantityToFloat),
		"metric":            starlark.NewBuiltin("metric", metricBuiltin),
		"family":            starlark.NewBuiltin("family", familyBuiltin),
		"label_prefix":      starlark.NewBuiltin("label_prefix", labelPrefixBuiltin),
		// time module provides time.now(), time.parse_time(s), durations, etc.
		// See https://github.com/google/starlark-go/blob/master/lib/time/time.go.
		"time": startime.Module,
	}

	objValue, err := goToStarlark(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert object to Starlark: %w", err)
	}

	predeclared["obj"] = objValue

	globals, err := starlark.ExecFileOptions(&syntax.FileOptions{
		GlobalReassign:  true,
		Set:             true,
		TopLevelControl: true,
		While:           true,
	}, thread, "script.star", sr.script, predeclared)
	if err != nil {
		var evalErr *starlark.EvalError
		if errors.As(err, &evalErr) {
			return nil, fmt.Errorf("starlark execution error: %s", evalErr.Backtrace())
		}

		return nil, fmt.Errorf("starlark execution error: %w", err)
	}

	familiesVar, ok := globals["families"]
	if !ok {
		return nil, errors.New("starlark script must define a 'families' variable")
	}

	return extractFamilies(familiesVar)
}

// quantityToFloat parses a Kubernetes Quantity string and returns its float value.
func quantityToFloat(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var s string
	if err := starlark.UnpackArgs("quantity_to_float", args, kwargs, "s", &s); err != nil {
		return nil, err
	}

	q, err := resource.ParseQuantity(s)
	if err != nil {
		return nil, fmt.Errorf("invalid quantity %q: %w", s, err)
	}

	return starlark.Float(q.AsApproximateFloat64()), nil
}

// metricBuiltin creates a metric sample dict.
func metricBuiltin(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var labels *starlark.Dict

	var value starlark.Float
	if err := starlark.UnpackArgs("metric", args, kwargs, "labels", &labels, "value", &value); err != nil {
		return nil, err
	}

	result := starlark.NewDict(2)
	if err := result.SetKey(starlark.String("labels"), labels); err != nil {
		return nil, err
	}

	if err := result.SetKey(starlark.String("value"), value); err != nil {
		return nil, err
	}

	return result, nil
}

// familyBuiltin creates a metric family dict.
func familyBuiltin(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var name, help, kind string

	var samples *starlark.List
	if err := starlark.UnpackArgs("family", args, kwargs, "name", &name, "help", &help, "kind", &kind, "samples", &samples); err != nil {
		return nil, err
	}

	if !metricutil.IsValidMetricKind(kind) {
		return nil, fmt.Errorf("family kind must be one of %s, got %q", metricutil.SupportedMetricKindsString(), kind)
	}

	result := starlark.NewDict(4)
	if err := result.SetKey(starlark.String("name"), starlark.String(name)); err != nil {
		return nil, err
	}

	if err := result.SetKey(starlark.String("help"), starlark.String(help)); err != nil {
		return nil, err
	}

	if err := result.SetKey(starlark.String("kind"), starlark.String(kind)); err != nil {
		return nil, err
	}

	if err := result.SetKey(starlark.String("samples"), samples); err != nil {
		return nil, err
	}

	return result, nil
}

// labelPrefixBuiltin adds a prefix to all keys in a dict and sanitizes them.
// Example: label_prefix({"app": "test", "env/type": "prod"}, "label_") => {"label_app": "test", "label_env_type": "prod"}.
func labelPrefixBuiltin(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var labelsDict *starlark.Dict

	var prefix string
	if err := starlark.UnpackArgs("label_prefix", args, kwargs, "labels", &labelsDict, "prefix", &prefix); err != nil {
		return nil, err
	}

	result := starlark.NewDict(labelsDict.Len())

	for _, item := range labelsDict.Items() {
		key, ok := item[0].(starlark.String)
		if !ok {
			return nil, fmt.Errorf("label key must be a string, got %s", item[0].Type())
		}

		sanitized := metricutil.SanitizeLabelKey(string(key))
		if err := result.SetKey(starlark.String(prefix+sanitized), item[1]); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// goToStarlark converts a Go value to a Starlark value.
func goToStarlark(v interface{}) (starlark.Value, error) {
	switch val := v.(type) {
	case nil:
		return starlark.None, nil
	case bool:
		return starlark.Bool(val), nil
	case int:
		return starlark.MakeInt(val), nil
	case int64:
		return starlark.MakeInt64(val), nil
	case float64:
		return starlark.Float(val), nil
	case string:
		return starlark.String(val), nil
	case []interface{}:
		list := make([]starlark.Value, len(val))

		for i, item := range val {
			converted, err := goToStarlark(item)
			if err != nil {
				return nil, err
			}

			list[i] = converted
		}

		return starlark.NewList(list), nil
	case map[string]interface{}:
		dict := starlark.NewDict(len(val))

		for k, v := range val {
			key := starlark.String(k)

			value, err := goToStarlark(v)
			if err != nil {
				return nil, err
			}

			if err := dict.SetKey(key, value); err != nil {
				return nil, err
			}
		}

		return dict, nil
	default:
		return starlark.String(fmt.Sprintf("%v", val)), nil
	}
}

// extractFamilies extracts ResolvedFamily objects from the Starlark "families" variable.
func extractFamilies(familiesVar starlark.Value) ([]ResolvedFamily, error) {
	familiesList, ok := familiesVar.(*starlark.List)
	if !ok {
		return nil, fmt.Errorf("'families' must be a list, got %s", familiesVar.Type())
	}

	var result []ResolvedFamily

	iter := familiesList.Iterate()
	defer iter.Done()

	var familyVal starlark.Value
	for iter.Next(&familyVal) {
		family, err := extractFamily(familyVal)
		if err != nil {
			return nil, err
		}

		result = append(result, family)
	}

	return result, nil
}

// extractFamily extracts a single ResolvedFamily from a Starlark dict.
func extractFamily(val starlark.Value) (ResolvedFamily, error) {
	dict, ok := val.(*starlark.Dict)
	if !ok {
		return ResolvedFamily{}, fmt.Errorf("family must be a dict, got %s", val.Type())
	}

	name, err := getDictString(dict, "name")
	if err != nil {
		return ResolvedFamily{}, fmt.Errorf("family: %w", err)
	}

	help, err := getDictString(dict, "help")
	if err != nil {
		return ResolvedFamily{}, fmt.Errorf("family: %w", err)
	}

	kind, err := getDictString(dict, "kind")
	if err != nil {
		return ResolvedFamily{}, fmt.Errorf("family: %w", err)
	}

	samplesVal, found, err := dict.Get(starlark.String("samples"))
	if err != nil || !found {
		return ResolvedFamily{}, errors.New("family missing 'samples' field")
	}

	samples, err := extractSamples(samplesVal)
	if err != nil {
		return ResolvedFamily{}, fmt.Errorf("family %q: %w", name, err)
	}

	return ResolvedFamily{
		Name:    name,
		Help:    help,
		Kind:    kind,
		Samples: samples,
	}, nil
}

// extractSamples extracts ResolvedSample objects from a Starlark list.
func extractSamples(samplesVal starlark.Value) ([]ResolvedSample, error) {
	samplesList, ok := samplesVal.(*starlark.List)
	if !ok {
		return nil, fmt.Errorf("samples must be a list, got %s", samplesVal.Type())
	}

	var result []ResolvedSample

	iter := samplesList.Iterate()
	defer iter.Done()

	var sampleVal starlark.Value
	for iter.Next(&sampleVal) {
		sample, err := extractSample(sampleVal)
		if err != nil {
			return nil, err
		}

		result = append(result, sample)
	}

	return result, nil
}

// extractSample extracts a single ResolvedSample from a Starlark dict.
func extractSample(val starlark.Value) (ResolvedSample, error) {
	dict, ok := val.(*starlark.Dict)
	if !ok {
		return ResolvedSample{}, fmt.Errorf("sample must be a dict, got %s", val.Type())
	}

	labelsVal, found, err := dict.Get(starlark.String("labels"))
	if err != nil || !found {
		return ResolvedSample{}, errors.New("sample missing 'labels' field")
	}

	labels, err := extractLabels(labelsVal)
	if err != nil {
		return ResolvedSample{}, err
	}

	valueVal, found, err := dict.Get(starlark.String("value"))
	if err != nil || !found {
		return ResolvedSample{}, errors.New("sample missing 'value' field")
	}

	value, err := extractFloat(valueVal)
	if err != nil {
		return ResolvedSample{}, fmt.Errorf("sample value: %w", err)
	}

	return ResolvedSample{
		Labels: labels,
		Value:  value,
	}, nil
}

// extractLabels extracts a map[string]string from a Starlark dict.
func extractLabels(val starlark.Value) (map[string]string, error) {
	dict, ok := val.(*starlark.Dict)
	if !ok {
		return nil, fmt.Errorf("labels must be a dict, got %s", val.Type())
	}

	labels := make(map[string]string)

	for _, item := range dict.Items() {
		key, ok := item[0].(starlark.String)
		if !ok {
			return nil, fmt.Errorf("label key must be a string, got %s", item[0].Type())
		}

		value := item[1]
		switch v := value.(type) {
		case starlark.String:
			labels[string(key)] = string(v)
		case starlark.Int:
			i, _ := v.Int64()
			labels[string(key)] = strconv.FormatInt(i, 10)
		case starlark.Float:
			labels[string(key)] = strconv.FormatFloat(float64(v), 'g', -1, 64)
		case starlark.Bool:
			labels[string(key)] = strconv.FormatBool(bool(v))
		default:
			labels[string(key)] = value.String()
		}
	}

	return labels, nil
}

// extractFloat extracts a float64 from a Starlark value.
func extractFloat(val starlark.Value) (float64, error) {
	switch v := val.(type) {
	case starlark.Float:
		return float64(v), nil
	case starlark.Int:
		return float64(v.Float()), nil
	default:
		return 0, fmt.Errorf("expected number, got %s", val.Type())
	}
}

// getDictString extracts a string value from a Starlark dict.
func getDictString(dict *starlark.Dict, key string) (string, error) {
	val, found, err := dict.Get(starlark.String(key))
	if err != nil {
		return "", err
	}

	if !found {
		return "", fmt.Errorf("missing '%s' field", key)
	}

	s, ok := val.(starlark.String)
	if !ok {
		return "", fmt.Errorf("'%s' must be a string, got %s", key, val.Type())
	}

	return string(s), nil
}
