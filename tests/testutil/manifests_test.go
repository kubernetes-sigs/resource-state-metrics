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
	"path/filepath"
	"strings"
	"testing"
)

func TestGetCRDAndCRManifestsErrorsForMissingDirectory(t *testing.T) {
	t.Parallel()

	missingDir := filepath.Join(t.TempDir(), "missing")

	_, _, err := GetCRDAndCRManifests([]string{missingDir}, nil)
	if err == nil {
		t.Fatal("GetCRDAndCRManifests() error = nil, want missing-directory error")
	}

	if !strings.Contains(err.Error(), missingDir) {
		t.Errorf("error %q does not identify missing directory %q", err, missingDir)
	}
}
