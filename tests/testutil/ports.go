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
	"errors"
	"fmt"
	"net"
	"time"
)

const portListenTimeout = 5 * time.Second

// getFreePorts returns distinct available ports by binding all of them before
// releasing any listener.
func getFreePorts(ctx context.Context, count int) ([]int, error) {
	listeners, ports, err := ListenOnFreePorts(ctx, count)
	if err != nil {
		return nil, err
	}

	closeListeners(listeners)

	return ports, nil
}

// ListenOnFreePorts binds distinct ephemeral ports and returns the open
// listeners together with their ports. The caller owns the listeners and must
// close them after use.
func ListenOnFreePorts(ctx context.Context, count int) ([]net.Listener, []int, error) {
	if count < 0 {
		return nil, nil, errors.New("port count cannot be negative")
	}

	ctx, cancel := context.WithTimeout(ctx, portListenTimeout)
	defer cancel()

	listeners := make([]net.Listener, 0, count)
	ports := make([]int, 0, count)

	var lc net.ListenConfig

	for range count {
		listener, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
		if err != nil {
			closeListeners(listeners)

			return nil, nil, fmt.Errorf("failed to listen on free port: %w", err)
		}

		listeners = append(listeners, listener)

		addr, ok := listener.Addr().(*net.TCPAddr)
		if !ok {
			closeListeners(listeners)

			return nil, nil, errors.New("unexpected address type")
		}

		ports = append(ports, addr.Port)
	}

	return listeners, ports, nil
}

func closeListeners(listeners []net.Listener) {
	for _, listener := range listeners {
		_ = listener.Close()
	}
}
