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
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func writeMetricTo(writer *strings.Builder, g, v, k, ns, name, resolvedValue string, resolvedLabelKeys, resolvedLabelValues []string, kind MetricKind) error {
	resolvedLabelKeys, resolvedLabelValues = appendAutoLabels(resolvedLabelKeys, resolvedLabelValues, g, v, k, ns, name)
	if err := writeLabels(writer, resolvedLabelKeys, resolvedLabelValues); err != nil {
		return err
	}

	return writeValue(writer, resolvedValue, kind)
}

// appendAutoLabels appends auto-injected labels: group, version, kind, name, namespace.
// For cluster-scoped resources, namespace is an empty string.
func appendAutoLabels(keys, values []string, g, v, k, ns, name string) ([]string, []string) {
	keys = append(keys, "group", "version", "kind", "name", "namespace")
	values = append(values, g, v, k, name, ns)

	return keys, values
}

func writeLabels(writer *strings.Builder, keys, values []string) error {
	if len(keys) == 0 {
		return nil
	}

	separator := "{"
	for i := range keys {
		writer.WriteString(separator)
		writer.WriteString(keys[i])
		writer.WriteString("=\"")

		n, err := strings.NewReplacer("\\", `\\`, "\n", `\n`, "\"", `\"`).WriteString(writer, values[i])
		if err != nil {
			return fmt.Errorf("error writing metric after %d bytes: %w", n, err)
		}

		writer.WriteString("\"")

		separator = ","
	}

	writer.WriteString("}")

	return nil
}

func writeValue(writer *strings.Builder, value string, kind MetricKind) error {
	writer.WriteByte(' ')

	floatVal, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("error parsing metric value %q as float64: %w", value, err)
	}

	err = validateValue(floatVal, kind)
	if err != nil {
		return fmt.Errorf("invalid metric value %f for kind %s: %w", floatVal, kind, err)
	}

	n, err := fmt.Fprintf(writer, "%f", floatVal)
	if err != nil {
		return fmt.Errorf("error writing (float64) metric value after %d bytes: %w", n, err)
	}

	writer.WriteByte('\n')

	return nil
}

func validateValue(floatVal float64, kind MetricKind) error {
	if kind == MetricKindCounter {
		if math.IsNaN(floatVal) {
			return errors.New("counter value cannot be NaN")
		}

		if floatVal < 0 {
			return fmt.Errorf("counter value %f cannot be negative", floatVal)
		}
	}

	return nil
}
