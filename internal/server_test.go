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
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func verifyGzipResponse(t *testing.T, resp *http.Response) {
	t.Helper()

	if contentEncoding := resp.Header.Get("Content-Encoding"); contentEncoding != "gzip" {
		t.Errorf("expected Content-Encoding 'gzip', got '%s'", contentEncoding)
	}

	gzReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}

	defer gzReader.Close()

	if _, err := io.ReadAll(gzReader); err != nil {
		t.Fatalf("failed to read decompressed body: %v", err)
	}
}

func verifyUncompressedResponse(t *testing.T, resp *http.Response) {
	t.Helper()

	if contentEncoding := resp.Header.Get("Content-Encoding"); contentEncoding == "gzip" {
		t.Errorf("did not expect Content-Encoding gzip, but got '%s'", contentEncoding)
	}

	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
}

func TestMainServerGzipCompression(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stores := &sync.Map{}
	server := newMainServer(":9999", "", stores, nil)

	httpServer := server.build(ctx, nil, nil)
	if httpServer == nil || httpServer.Handler == nil {
		t.Fatal("expected non-nil http.Server and handler")
	}

	tests := []struct {
		name           string
		path           string
		acceptEncoding string
		expectGzip     bool
	}{
		{
			name:           "metrics without gzip",
			path:           "/metrics",
			acceptEncoding: "",
			expectGzip:     false,
		},
		{
			name:           "metrics with gzip",
			path:           "/metrics",
			acceptEncoding: "gzip",
			expectGzip:     true,
		},
		{
			name:           "metrics with complex accept encoding including gzip",
			path:           "/metrics",
			acceptEncoding: "deflate, gzip;q=1.0, *;q=0.5",
			expectGzip:     true,
		},
		{
			name:           "external without gzip",
			path:           "/external",
			acceptEncoding: "",
			expectGzip:     false,
		},
		{
			name:           "external with gzip",
			path:           "/external",
			acceptEncoding: "gzip",
			expectGzip:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.acceptEncoding != "" {
				req.Header.Set("Accept-Encoding", tt.acceptEncoding)
			}

			rec := httptest.NewRecorder()
			httpServer.Handler.ServeHTTP(rec, req)

			resp := rec.Result()
			defer resp.Body.Close()

			if vary := resp.Header.Get("Vary"); vary != "Accept-Encoding" {
				t.Errorf("expected Vary header 'Accept-Encoding', got '%s'", vary)
			}

			if tt.expectGzip {
				verifyGzipResponse(t, resp)
			} else {
				verifyUncompressedResponse(t, resp)
			}
		})
	}
}
