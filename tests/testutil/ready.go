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
	"fmt"
	"io"
	"net/http"
	"time"
)

const httpProbeTimeout = time.Second

// WaitForHTTPOK polls url until it responds with 200 OK or the timeout expires.
// Unlike a TCP dial, this only succeeds once a server is actually serving on a
// pre-bound listener.
func WaitForHTTPOK(ctx context.Context, url string, timeout, pollInterval time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := &http.Client{
		Timeout:   httpProbeTimeout,
		Transport: &http.Transport{DisableKeepAlives: true},
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		err := probeHTTPOK(ctx, client, url)
		if err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for %s to return 200 OK: %w (last error: %w)", url, ctx.Err(), err)
		case <-ticker.C:
		}
	}
}

func probeHTTPOK(ctx context.Context, client *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req) //nolint:gosec // test helper; callers pass localhost URLs only
	if err != nil {
		return fmt.Errorf("failed to fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}

	return nil
}
