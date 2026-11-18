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
	"fmt"
	"strings"

	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
)

const minMetricTypeLineParts = 3

// CompareMetrics scrapes the given metrics endpoint and compares the metric
// families declared in expectedMetricLines against the expected output.
func CompareMetrics(url string, expectedMetricLines []string) error {
	expectedMetrics := strings.Join(expectedMetricLines, "\n") + "\n"

	var familyNames []string

	for _, line := range strings.Split(expectedMetrics, "\n") {
		if !strings.HasPrefix(line, "# TYPE ") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) >= minMetricTypeLineParts {
			familyNames = append(familyNames, parts[2])
		}
	}

	if err := promtestutil.ScrapeAndCompare(url, strings.NewReader(expectedMetrics), familyNames...); err != nil {
		return fmt.Errorf("metrics comparison failed: %w", err)
	}

	return nil
}
