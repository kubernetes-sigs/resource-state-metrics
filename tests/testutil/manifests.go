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
	"os"
	"path/filepath"
	"strings"
)

// GetCRDAndCRManifests retrieves all CRD and non-CRD manifest file paths from the specified directories.
// Files whose base name starts with any of the ignoredPrefixes are skipped.
func GetCRDAndCRManifests(manifestDirs []string, ignoredPrefixes []string) ([]string, []string, error) {
	var (
		crdFiles []string
		crFiles  []string
	)

	ignored := make(map[string]struct{}, len(ignoredPrefixes))
	for _, p := range ignoredPrefixes {
		ignored[p] = struct{}{}
	}

	for _, manifestsDir := range manifestDirs {
		if _, statErr := os.Stat(manifestsDir); os.IsNotExist(statErr) {
			continue
		}

		walkErr := filepath.Walk(manifestsDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			base := filepath.Base(path)

			for prefix := range ignored {
				if strings.HasPrefix(base, prefix) {
					return nil
				}
			}

			if info.IsDir() || !strings.HasSuffix(base, ".yaml") {
				return nil
			}

			if strings.HasPrefix(base, "custom-resource-definition") {
				crdFiles = append(crdFiles, path)
			} else {
				crFiles = append(crFiles, path)
			}

			return nil
		})
		if walkErr != nil {
			return nil, nil, walkErr
		}
	}

	return crdFiles, crFiles, nil
}
