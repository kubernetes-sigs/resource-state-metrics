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

package options

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"k8s.io/klog/v2"
)

// Tests utilizing t.Setenv cannot be run in t.Parallel().
func TestOptions_Read(t *testing.T) {
	// Define the command-line arguments.
	originalMainPortNumber := 4242
	os.Args = []string{
		"cmd",
		"--main-port", strconv.Itoa(originalMainPortNumber), // This will *not* be overridden as it was explicitly set.
	}

	// Override the --self-port flag with the RSM_SELF_PORT environment variable.
	overriddenSelfPortNumber := 5678
	t.Setenv("RSM_SELF_PORT", strconv.Itoa(overriddenSelfPortNumber))

	// Override the --main-port flag with the RSM_MAIN_PORT environment variable.
	overriddenMainPortNumber := 1234
	t.Setenv("RSM_MAIN_PORT", strconv.Itoa(overriddenMainPortNumber))

	// Check if the flags were overridden by their corresponding environment variables.
	o := NewOptions(klog.NewKlogr())
	o.Read()

	if *o.SelfPort != overriddenSelfPortNumber {
		t.Fatalf("expected %d, got %d", overriddenSelfPortNumber, *o.SelfPort)
	}

	if *o.MainPort != originalMainPortNumber {
		t.Fatalf("expected %d, got %d", originalMainPortNumber, *o.MainPort)
	}
}

func TestValidateFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		flag    string
		value   string
		wantErr string
	}{
		// cel-timeout-seconds
		{name: "cel timeout valid", flag: celTimeoutFlagName, value: "10", wantErr: ""},
		{name: "cel timeout lower bound", flag: celTimeoutFlagName, value: "1", wantErr: ""},
		{name: "cel timeout upper bound", flag: celTimeoutFlagName, value: "300", wantErr: ""},
		{name: "cel timeout zero", flag: celTimeoutFlagName, value: "0", wantErr: "must be between 1 and 300"},
		{name: "cel timeout negative", flag: celTimeoutFlagName, value: "-1", wantErr: "must be between 1 and 300"},
		{name: "cel timeout too large", flag: celTimeoutFlagName, value: "301", wantErr: "must be between 1 and 300"},
		{name: "cel timeout not a number", flag: celTimeoutFlagName, value: "abc", wantErr: "invalid value"},

		// cardinality-warning-ratio
		{name: "ratio valid", flag: cardinalityWarningRatioFlagName, value: "0.8", wantErr: ""},
		{name: "ratio zero", flag: cardinalityWarningRatioFlagName, value: "0", wantErr: ""},
		{name: "ratio one", flag: cardinalityWarningRatioFlagName, value: "1.0", wantErr: ""},
		{name: "ratio negative", flag: cardinalityWarningRatioFlagName, value: "-0.1", wantErr: "must be between 0.0 and 1.0"},
		{name: "ratio too large", flag: cardinalityWarningRatioFlagName, value: "1.1", wantErr: "must be between 0.0 and 1.0"},
		{name: "ratio not a number", flag: cardinalityWarningRatioFlagName, value: "abc", wantErr: "invalid value"},

		// starlark-timeout-seconds
		{name: "starlark timeout valid", flag: starlarkTimeoutFlagName, value: "5", wantErr: ""},
		{name: "starlark timeout lower bound", flag: starlarkTimeoutFlagName, value: "1", wantErr: ""},
		{name: "starlark timeout upper bound", flag: starlarkTimeoutFlagName, value: "60", wantErr: ""},
		{name: "starlark timeout zero", flag: starlarkTimeoutFlagName, value: "0", wantErr: "must be between 1 and 60"},
		{name: "starlark timeout too large", flag: starlarkTimeoutFlagName, value: "61", wantErr: "must be between 1 and 60"},
		{name: "starlark timeout not a number", flag: starlarkTimeoutFlagName, value: "abc", wantErr: "invalid value"},

		// starlark-max-steps
		{name: "starlark steps valid", flag: starlarkMaxStepsFlagName, value: "1000", wantErr: ""},
		{name: "starlark steps at minimum", flag: starlarkMaxStepsFlagName, value: "100", wantErr: ""},
		{name: "starlark steps below minimum", flag: starlarkMaxStepsFlagName, value: "99", wantErr: "must be at least"},
		{name: "starlark steps not a number", flag: starlarkMaxStepsFlagName, value: "abc", wantErr: "invalid value"},

		// unknown flag passthrough
		{name: "unknown flag returns nil", flag: "unknown-flag", value: "anything", wantErr: ""},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := validateFlag(testCase.flag, testCase.value)

			if testCase.wantErr == "" {
				if err != nil {
					t.Fatalf("validateFlag(%q, %q) unexpected error: %v", testCase.flag, testCase.value, err)
				}

				return
			}

			if err == nil {
				t.Fatalf("validateFlag(%q, %q) = nil, want error containing %q", testCase.flag, testCase.value, testCase.wantErr)
			}

			if !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("validateFlag(%q, %q) error = %q, want containing %q", testCase.flag, testCase.value, err, testCase.wantErr)
			}
		})
	}
}
