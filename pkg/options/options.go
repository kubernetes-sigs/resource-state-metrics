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
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"k8s.io/klog/v2"
)

// flagRegistration holds the registered flag values (shared across all Options instances).
var (
	flagOnce sync.Once
	// Registered flag values.
	registeredCardinalityWarningRatio    *float64
	registeredCELCostLimit               *uint64
	registeredCELTimeout                 *int
	registeredGlobalCardinalityLimit     *int64
	registeredKubeconfig                 *string
	registeredMainHost                   *string
	registeredMainPort                   *int
	registeredMasterURL                  *string
	registeredRatioGOMEMLIMIT            *float64
	registeredResourceCardinalityDefault *int64
	registeredSelfHost                   *string
	registeredSelfPort                   *int
	registeredStarlarkMaxSteps           *int
	registeredStarlarkTimeout            *int
	registeredVersion                    *bool
	registeredWorkers                    *int
)

const (
	cardinalityWarningRatioFlagName    = "cardinality-warning-ratio"
	celCostLimitFlagName               = "cel-cost-limit"
	celTimeoutFlagName                 = "cel-timeout-seconds"
	globalCardinalityLimitFlagName     = "global-cardinality-limit"
	kubeconfigFlagName                 = "kubeconfig"
	mainHostFlagName                   = "main-host"
	mainPortFlagName                   = "main-port"
	masterURLFlagName                  = "master"
	ratioGOMEMLIMITFlagName            = "ratio-gomemlimit"
	resourceCardinalityDefaultFlagName = "resource-cardinality-default"
	selfHostFlagName                   = "self-host"
	selfPortFlagName                   = "self-port"
	starlarkMaxStepsFlagName           = "starlark-max-steps"
	starlarkTimeoutFlagName            = "starlark-timeout-seconds"
	versionFlagName                    = "version"
	workersFlagName                    = "workers"

	CELDefaultCostLimit               = 1e5
	CELDefaultTimeout                 = 5
	DefaultGlobalCardinalityLimit     = 0      // unlimited
	DefaultListenHost                 = "::"   // any IPv4/6 host address
	DefaultResourceCardinalityDefault = 100000 // total allowed samples per RMM
	DefaultCardinalityWarningRatio    = 0.8
	DefaultMainPort                   = 9999
	DefaultSelfPort                   = 9998
	StarlarkDefaultMaxSteps           = 100000
	StarlarkDefaultTimeout            = 5

	minStarlarkMaxSteps = 100
)

// Options represents the command-line Options.
type Options struct {
	CardinalityWarningRatio    *float64
	CELCostLimit               *uint64
	CELTimeout                 *int
	GlobalCardinalityLimit     *int64
	Kubeconfig                 *string
	MainHost                   *string
	MainPort                   *int
	MasterURL                  *string
	RatioGOMEMLIMIT            *float64
	ResourceCardinalityDefault *int64
	SelfHost                   *string
	SelfPort                   *int
	StarlarkMaxSteps           *int
	StarlarkTimeout            *int
	Version                    *bool
	Workers                    *int

	logger klog.Logger
}

// NewOptions returns a new Options initialized with its default values.
func NewOptions(logger klog.Logger) *Options {
	return &Options{
		CardinalityWarningRatio:    valuePtr(DefaultCardinalityWarningRatio),
		CELCostLimit:               valuePtr[uint64](CELDefaultCostLimit),
		CELTimeout:                 valuePtr(CELDefaultTimeout),
		GlobalCardinalityLimit:     valuePtr[int64](DefaultGlobalCardinalityLimit),
		Kubeconfig:                 valuePtr(os.Getenv("KUBECONFIG")),
		MainHost:                   valuePtr(DefaultListenHost),
		MainPort:                   valuePtr(DefaultMainPort),
		MasterURL:                  valuePtr(os.Getenv("KUBERNETES_MASTER")),
		RatioGOMEMLIMIT:            valuePtr(0.9),
		ResourceCardinalityDefault: valuePtr[int64](DefaultResourceCardinalityDefault),
		SelfHost:                   valuePtr(DefaultListenHost),
		SelfPort:                   valuePtr(DefaultSelfPort),
		StarlarkMaxSteps:           valuePtr(StarlarkDefaultMaxSteps),
		StarlarkTimeout:            valuePtr(StarlarkDefaultTimeout),
		Version:                    valuePtr(false),
		Workers:                    valuePtr(2),
		logger:                     logger,
	}
}

func valuePtr[T any](value T) *T {
	return &value
}

// Read reads the command-line flags and applies overrides, if any.
// This method is safe to call multiple times; flags are only registered once.
func (o *Options) Read() {
	// Register flags only once to avoid "flag redefined" panics when multiple
	// Options instances call Read() (e.g., in parallel tests).
	flagOnce.Do(func() {
		defaults := NewOptions(o.logger)

		//nolint:lll
		registeredCardinalityWarningRatio = flag.Float64(cardinalityWarningRatioFlagName, *defaults.CardinalityWarningRatio, "Ratio of cardinality threshold at which to emit warning conditions (0.0-1.0). Default 0.8 means warnings start at 80% of threshold.")
		//nolint:lll
		registeredCELCostLimit = flag.Uint64(celCostLimitFlagName, *defaults.CELCostLimit, "Maximum cost budget for CEL expression evaluation. CEL cost represents computational complexity: traversing an object field costs 1, invoking a function varies by complexity. This limit prevents runaway expressions from consuming excessive resources. Typical queries cost 100-10000; increase if legitimate queries hit the limit.")
		//nolint:lll
		registeredCELTimeout = flag.Int(celTimeoutFlagName, *defaults.CELTimeout, "Maximum time in seconds for CEL expression evaluation. This timeout enforces a wall-clock limit on query execution to prevent slow expressions from blocking metric generation. Increase if complex legitimate queries timeout.")
		//nolint:lll
		registeredGlobalCardinalityLimit = flag.Int64(globalCardinalityLimitFlagName, *defaults.GlobalCardinalityLimit, "Maximum total cardinality across all RMM resources. 0 means unlimited. When exceeded, all metric generation is cut off.")
		registeredKubeconfig = flag.String(kubeconfigFlagName, *defaults.Kubeconfig, "Path to a kubeconfig. Only required if out-of-cluster.")
		registeredMainHost = flag.String(mainHostFlagName, *defaults.MainHost, "Host to expose main metrics on.")
		registeredMainPort = flag.Int(mainPortFlagName, *defaults.MainPort, "Port to expose main metrics on.")
		registeredMasterURL = flag.String(masterURLFlagName, *defaults.MasterURL, "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
		registeredRatioGOMEMLIMIT = flag.Float64(ratioGOMEMLIMITFlagName, *defaults.RatioGOMEMLIMIT, "GOMEMLIMIT to memory quota ratio.")
		//nolint:lll
		registeredResourceCardinalityDefault = flag.Int64(resourceCardinalityDefaultFlagName, *defaults.ResourceCardinalityDefault, "Default cardinality limit per RMM resource. Can be overridden per-RMM via configuration YAML. 0 means unlimited.")
		registeredSelfHost = flag.String(selfHostFlagName, *defaults.SelfHost, "Host to expose self (telemetry) metrics on.")
		registeredSelfPort = flag.Int(selfPortFlagName, *defaults.SelfPort, "Port to expose self (telemetry) metrics on.")
		//nolint:lll
		registeredStarlarkMaxSteps = flag.Int(starlarkMaxStepsFlagName, *defaults.StarlarkMaxSteps, "Maximum number of Starlark execution steps. This limits computation to prevent infinite loops and runaway scripts. Increase if legitimate scripts hit the limit.")
		//nolint:lll
		registeredStarlarkTimeout = flag.Int(starlarkTimeoutFlagName, *defaults.StarlarkTimeout, "Maximum time in seconds for Starlark script execution. This timeout enforces a wall-clock limit to prevent slow scripts from blocking metric generation.")
		registeredVersion = flag.Bool(versionFlagName, *defaults.Version, "Print version information and quit")
		registeredWorkers = flag.Int(workersFlagName, *defaults.Workers, "Number of workers processing managed resources in the workqueue.")

		flag.Parse()

		// Respect overrides, this also helps in testing without setting the same defaults in a bunch of places.
		flag.VisitAll(func(f *flag.Flag) {
			// Don't override flags that have been set. Environment variables do not take precedence over command-line flags.
			if f.Value.String() != f.DefValue {
				err := validateFlag(f.Name, f.Value.String())
				if err != nil {
					panic(fmt.Sprintf("Invalid value for flag %s: %v", f.Name, err))
				}

				return
			}

			name := f.Name
			overriderForOptionName := `RSM_` + strings.ReplaceAll(strings.ToUpper(name), "-", "_")

			if value, ok := os.LookupEnv(overriderForOptionName); ok {
				klog.V(1).Info(fmt.Sprintf("Overriding flag %s with %s=%s", name, overriderForOptionName, value))

				err := flag.Set(name, value)
				if err != nil {
					panic(fmt.Sprintf("Failed to set flag %s to %s: %v", name, value, err))
				}
			}
		})
	})

	// Copy registered values to this Options instance
	o.CardinalityWarningRatio = registeredCardinalityWarningRatio
	o.CELCostLimit = registeredCELCostLimit
	o.CELTimeout = registeredCELTimeout
	o.GlobalCardinalityLimit = registeredGlobalCardinalityLimit
	o.Kubeconfig = registeredKubeconfig
	o.MainHost = registeredMainHost
	o.MainPort = registeredMainPort
	o.MasterURL = registeredMasterURL
	o.RatioGOMEMLIMIT = registeredRatioGOMEMLIMIT
	o.ResourceCardinalityDefault = registeredResourceCardinalityDefault
	o.SelfHost = registeredSelfHost
	o.SelfPort = registeredSelfPort
	o.StarlarkMaxSteps = registeredStarlarkMaxSteps
	o.StarlarkTimeout = registeredStarlarkTimeout
	o.Version = registeredVersion
	o.Workers = registeredWorkers
}

func validateFlag(name, value string) error {
	switch name {
	case celTimeoutFlagName:
		valueInt, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid value for %s: %w", name, err)
		}

		if valueInt <= 0 || valueInt > 300 {
			return fmt.Errorf("%s must be between 1 and 300 seconds", name)
		}
	case cardinalityWarningRatioFlagName:
		valueFloat, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("invalid value for %s: %w", name, err)
		}

		if valueFloat < 0 || valueFloat > 1.0 {
			return fmt.Errorf("%s must be between 0.0 and 1.0", name)
		}
	case starlarkTimeoutFlagName:
		valueInt, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid value for %s: %w", name, err)
		}

		if valueInt <= 0 || valueInt > 60 {
			return fmt.Errorf("%s must be between 1 and 60 seconds", name)
		}
	case starlarkMaxStepsFlagName:
		valueInt, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid value for %s: %w", name, err)
		}

		if valueInt < minStarlarkMaxSteps {
			return fmt.Errorf("%s must be at least %d", name, minStarlarkMaxSteps)
		}
	}

	return nil
}
