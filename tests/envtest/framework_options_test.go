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

package envtest

import (
	"context"
	"flag"
	"testing"

	"github.com/kubernetes-sigs/resource-state-metrics/tests/envtest/framework"
	"k8s.io/client-go/rest"
)

func TestNewForConfigUsesDefaultsWithoutRegisteringFlags(t *testing.T) {
	t.Parallel()

	if flag.Lookup("workers") != nil {
		t.Fatal("workers flag was registered before constructing the envtest framework")
	}

	f, err := framework.NewForConfig(context.Background(), &rest.Config{Host: "https://127.0.0.1:6443"})
	if err != nil {
		t.Fatalf("NewForConfig() error = %v", err)
	}

	if flag.Lookup("workers") != nil {
		t.Fatal("NewForConfig() registered command-line flags")
	}

	if got, want := *f.Options.Workers, 2; got != want {
		t.Errorf("default workers = %d, want %d", got, want)
	}

	if got, want := *f.Options.MainPort, 9999; got != want {
		t.Errorf("default main port = %d, want %d", got, want)
	}
}
