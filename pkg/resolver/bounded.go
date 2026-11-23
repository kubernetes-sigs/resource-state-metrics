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
	"fmt"
	"time"
)

// bounded runs fn under a context that expires after timeout, with no deadline when timeout is zero, and cancels it on return.
// Errors are prefixed with the resolver name so a log line says which resolver failed.
func bounded(ctx context.Context, timeout time.Duration, resolver string, fn func(context.Context) error) error {
	var cancel context.CancelFunc

	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}

	defer cancel()

	if err := fn(ctx); err != nil {
		return fmt.Errorf("%s: %w", resolver, err)
	}

	return nil
}
