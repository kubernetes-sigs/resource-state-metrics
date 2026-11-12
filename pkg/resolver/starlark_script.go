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
	"maps"
	"strings"

	startime "go.starlark.net/lib/time"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// StarlarkResolver implements the FamilyResolver interface.
var _ FamilyResolver = &StarlarkResolver{}

// starlarkFileOptions enables global reassignment, set, top level control flow and while loops in scripts.
var starlarkFileOptions = &syntax.FileOptions{
	GlobalReassign:  true,
	Set:             true,
	TopLevelControl: true,
	While:           true,
}

// starlarkPredeclared is the fixed set of builtins every script sees, obj is added per object.
var starlarkPredeclared = starlark.StringDict{
	"quantity_to_float": starlark.NewBuiltin("quantity_to_float", quantityToFloat),
	"metric":            starlark.NewBuiltin("metric", metricBuiltin),
	"family":            starlark.NewBuiltin("family", familyBuiltin),
	"label_prefix":      starlark.NewBuiltin("label_prefix", labelPrefixBuiltin),
	"time":              startime.Module,
}

// Compile parses and compiles the script once, so a bad script surfaces at config load.
func (sr *StarlarkResolver) Compile(script string) (Script, error) {
	return sr.compile(script)
}

// Traits declares the obj binding, label key sanitization in label_prefix, a timeout and a step limit.
func (sr *StarlarkResolver) Traits() Traits {
	return Traits{
		ObjectBinding: "obj",
		Sanitization:  SanitizationSpec{LabelKeys: true},
		Bounds:        BoundsSpec{Timeout: true, StepLimit: true},
	}
}

// compile returns the concrete script so Resolve can reuse it.
func (sr *StarlarkResolver) compile(script string) (*starlarkScript, error) {
	isPredeclared := func(name string) bool {
		return name == "obj" || starlarkPredeclared.Has(name)
	}

	_, program, err := starlark.SourceProgramOptions(starlarkFileOptions, "script.star", script, isPredeclared)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidExpression, err)
	}

	return &starlarkScript{resolver: sr, program: program}, nil
}

// starlarkScript is a compiled Starlark program.
type starlarkScript struct {
	resolver *StarlarkResolver
	program  *starlark.Program
}

// Eval runs the script under the resolver's timeout and returns the families it defines.
func (ss *starlarkScript) Eval(ctx context.Context, obj map[string]any) ([]ResolvedFamily, error) {
	var families []ResolvedFamily

	err := bounded(ctx, ss.resolver.timeout, "starlark", func(ctx context.Context) error {
		result, err := ss.run(ctx, obj)
		if err != nil {
			return err
		}

		families = result

		return nil
	})

	return families, err
}

// run executes the program on one thread, cancelling it when the context ends, and maps a step limit to ErrBudgetExceeded.
func (ss *starlarkScript) run(ctx context.Context, obj map[string]any) ([]ResolvedFamily, error) {
	objValue, err := goToStarlark(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to convert object to Starlark: %w", err)
	}

	thread := &starlark.Thread{
		Name: "resource-state-metrics-starlark",
		Print: func(_ *starlark.Thread, msg string) {
			ss.resolver.logger.V(2).Info("Starlark printer", "message", msg)
		},
	}
	if ss.resolver.maxSteps > 0 {
		thread.SetMaxExecutionSteps(uint64(ss.resolver.maxSteps))
	}

	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			thread.Cancel("timeout exceeded")
		case <-done:
		}
	}()

	predeclared := maps.Clone(starlarkPredeclared)
	predeclared["obj"] = objValue

	globals, err := ss.program.Init(thread, predeclared)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if strings.Contains(err.Error(), "too many steps") {
			return nil, fmt.Errorf("%w: starlark execution error: %w", ErrBudgetExceeded, err)
		}

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
