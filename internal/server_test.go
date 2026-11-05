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
package internal

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/kubernetes/fake"
)

func TestMainServer_ExternalEndpoint(t *testing.T) {
	t.Parallel()

	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "test_http_request_duration_seconds",
		Help: "Test histogram for main server request durations.",
	}, []string{"method", "code"})

	var stores sync.Map

	srv := newMainServer("127.0.0.1:0", "", &stores, duration).build(
		context.Background(),
		fake.NewSimpleClientset(),
		prometheus.NewRegistry(),
	)
	if srv == nil {
		t.Fatal("mainServer.build returned nil")
	}

	req := httptest.NewRequest(http.MethodGet, "/external", nil)
	// Prefer Prometheus text so an empty collector registry yields an empty
	// body instead of an OpenMetrics `# EOF` trailer.
	req.Header.Set("Accept", "text/plain")

	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /external status = %d, want %d", rec.Code, http.StatusOK)
	}

	contentType := rec.Header().Get("Content-Type")
	if contentType == "" {
		t.Fatal("GET /external missing Content-Type header")
	}

	if body := rec.Body.String(); body != "" {
		t.Fatalf("GET /external body = %q, want empty (no external collectors registered)", body)
	}
}
