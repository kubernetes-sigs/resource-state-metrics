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

// GetFreePort returns an available port by briefly binding to port 0 (which lets the OS assign a free port).
func GetFreePort(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, portListenTimeout)
	defer cancel()

	var lc net.ListenConfig

	listener, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to listen on free port: %w", err)
	}

	defer listener.Close()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("unexpected address type")
	}

	return addr.Port, nil
}
