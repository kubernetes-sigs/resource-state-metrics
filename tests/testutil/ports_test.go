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

package testutil

import (
	"context"
	"net"
	"testing"
)

func TestGetFreePortsReturnsDistinctPorts(t *testing.T) {
	t.Parallel()

	ports, err := getFreePorts(context.Background(), 2)
	if err != nil {
		t.Fatalf("getFreePorts() error = %v", err)
	}

	if len(ports) != 2 {
		t.Fatalf("getFreePorts() returned %d ports, want 2", len(ports))
	}

	if ports[0] == ports[1] {
		t.Fatalf("getFreePorts() returned duplicate port %d", ports[0])
	}
}

func TestListenOnFreePortsKeepsListenersOpen(t *testing.T) {
	t.Parallel()

	listeners, ports, err := ListenOnFreePorts(context.Background(), 2)
	if err != nil {
		t.Fatalf("ListenOnFreePorts() error = %v", err)
	}
	defer closeListeners(listeners)

	if len(ports) != len(listeners) {
		t.Fatalf("ListenOnFreePorts() returned %d ports for %d listeners", len(ports), len(listeners))
	}

	var lc net.ListenConfig

	for _, listener := range listeners {
		second, err := lc.Listen(context.Background(), "tcp", listener.Addr().String())
		if err == nil {
			_ = second.Close()

			t.Errorf("port %s was available while its listener was still open", listener.Addr())
		}
	}
}
