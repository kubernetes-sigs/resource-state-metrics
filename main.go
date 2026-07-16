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

package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/KimMachineGun/automemlimit/memlimit"
	"github.com/kubernetes-sigs/resource-state-metrics/internal"
	v "github.com/kubernetes-sigs/resource-state-metrics/internal/version"
	clientset "github.com/kubernetes-sigs/resource-state-metrics/pkg/generated/clientset/versioned"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/options"
	"github.com/kubernetes-sigs/resource-state-metrics/pkg/signals"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

func main() {
	// Set up contextual logging.
	// Set up signals, so we can handle the shutdown signal gracefully.
	ctx := klog.NewContext(signals.SetupSignalHandler(), klog.NewKlogr())
	logger := klog.FromContext(ctx)

	// Set up flags.
	klog.InitFlags(flag.CommandLine)

	options := options.NewOptions(logger)
	options.Read()

	// Set GOMEMLIMIT based on memory quota.
	slogger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	limit, err := memlimit.SetGoMemLimitWithOpts(
		memlimit.WithLogger(slogger),
		memlimit.WithRatio(*options.RatioGOMEMLIMIT),
	)
	if err != nil {
		logger.Error(err, "Failed to set GOMEMLIMIT, skipping")
	} else {
		logger.V(1).Info("GOMEMLIMIT set", "limit", limit)
	}

	// Quit if only version flag is set.
	if *options.Version && flag.NFlag() == 1 {
		logger.V(0).Info("Version", "version", v.Version)
		os.Exit(0)
	}

	// Build client-sets.
	cfg, err := clientcmd.BuildConfigFromFlags(*options.MasterURL, *options.Kubeconfig)
	if err != nil {
		logger.Error(err, "Error building kubeconfig", "kubeconfig", *options.Kubeconfig)
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}

	kubeClientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		logger.Error(err, "Error building kubernetes clientset")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}

	rsmClientset, err := clientset.NewForConfig(cfg)
	if err != nil {
		logger.Error(err, "Error building resource-state-metrics clientset")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}

	dynamicClientset, err := dynamic.NewForConfig(cfg)
	if err != nil {
		logger.Error(err, "Error building dynamic clientset")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}

	discoveryClient := kubeClientset.Discovery()

	// Start the controller.
	c := internal.NewController(ctx, options, kubeClientset, rsmClientset, dynamicClientset, discoveryClient)
	if err = c.Run(ctx, *options.Workers); err != nil {
		logger.Error(err, "Error running controller")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}
}
