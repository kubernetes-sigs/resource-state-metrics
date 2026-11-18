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
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitForHTTPOKRetriesUntilOK(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)

			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := WaitForHTTPOK(context.Background(), server.URL, 5*time.Second, time.Millisecond); err != nil {
		t.Fatalf("WaitForHTTPOK() error = %v", err)
	}

	if got := requests.Load(); got != 3 {
		t.Fatalf("WaitForHTTPOK() made %d requests, want 3", got)
	}
}

func TestWaitForHTTPOKTimesOutWhenNothingServes(t *testing.T) {
	t.Parallel()

	// The listener is bound but never served, so a TCP dial would succeed.
	listeners, _, err := ListenOnFreePorts(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListenOnFreePorts() error = %v", err)
	}
	defer closeListeners(listeners)

	url := "http://" + listeners[0].Addr().String() + "/healthz"

	err = WaitForHTTPOK(context.Background(), url, 50*time.Millisecond, 5*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForHTTPOK() error = %v, want context.DeadlineExceeded", err)
	}
}
