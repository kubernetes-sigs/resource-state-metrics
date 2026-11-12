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
	"strings"
	"testing"
	"time"
)

func TestBounded(t *testing.T) {
	t.Parallel()

	t.Run("returns nil when fn succeeds", func(t *testing.T) {
		t.Parallel()

		err := bounded(t.Context(), time.Second, "cel", func(context.Context) error { return nil })
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("wraps the error fn returns", func(t *testing.T) {
		t.Parallel()

		err := bounded(t.Context(), time.Second, "cel", func(context.Context) error { return ErrBudgetExceeded })
		if !errors.Is(err, ErrBudgetExceeded) {
			t.Fatalf("expected ErrBudgetExceeded, got %v", err)
		}

		if !strings.HasPrefix(err.Error(), "cel: ") {
			t.Fatalf("expected the resolver name in the error, got %q", err.Error())
		}
	})

	t.Run("returns the deadline error when fn stops on the context", func(t *testing.T) {
		t.Parallel()

		err := bounded(t.Context(), 10*time.Millisecond, "cel", func(ctx context.Context) error {
			<-ctx.Done()

			return ctx.Err()
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected context.DeadlineExceeded, got %v", err)
		}
	})

	t.Run("sets no deadline when timeout is zero", func(t *testing.T) {
		t.Parallel()

		err := bounded(t.Context(), 0, "unstructured", func(ctx context.Context) error {
			if _, ok := ctx.Deadline(); ok {
				return errors.New("unexpected deadline")
			}

			return nil
		})
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("returns the parent cancellation", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		err := bounded(ctx, time.Second, "cel", func(ctx context.Context) error { return ctx.Err() })
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})
}
