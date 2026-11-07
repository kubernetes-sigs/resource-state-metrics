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
	"fmt"
	"io"
	"strings"
)

// metricsWriter writes metrics from a group of stores to an io.Writer.
type metricsWriter struct {
	stores []*StoreType
}

// newMetricsWriter creates a new metricsWriter.
func newMetricsWriter(stores ...*StoreType) *metricsWriter {
	return &metricsWriter{
		stores: stores,
	}
}

// familyKey extracts the metric family name from a "# HELP <name> ..." header
// line, so that headers of the same family can be detected across stores.
// It falls back to the raw header string when the line does not carry a
// family name.
func familyKey(header string) string {
	if rest, ok := strings.CutPrefix(header, "# HELP "); ok {
		if name, _, ok := strings.Cut(rest, " "); ok && name != "" {
			return name
		}
	}

	return header
}

// writeStores writes out metrics from the underlying stores to the given writer, per resource.
// It writes metrics so that the ones with the same name are grouped together when written out, and guarantees an exposition format that is safe to be ingested by Prometheus.
// Metric families shared by multiple stores, e.g. when a wildcard store expands to several kinds, get their
// HELP/TYPE header only once from the first store that declares them, and every store then contributes only its
// samples to that family.
func (m *metricsWriter) writeStores(writer io.Writer) error {
	if len(m.stores) == 0 {
		return nil
	}

	emittedHeaders := make(map[string]struct{})

	for _, store := range m.stores {
		store.mutex.RLock()
		err := m.writeFromStore(writer, store, emittedHeaders)
		store.mutex.RUnlock()

		if err != nil {
			return err
		}
	}

	return nil
}

// writeHeaderOnce writes header to writer only when its family was not emitted before,
// marking the family as emitted so that later stores write their samples without repeating the header.
func writeHeaderOnce(writer io.Writer, header string, emittedHeaders map[string]struct{}) error {
	key := familyKey(header)

	if _, emitted := emittedHeaders[key]; emitted {
		return nil
	}

	if err := writeHeader(writer, header); err != nil {
		return fmt.Errorf("error writing header: %w", err)
	}

	emittedHeaders[key] = struct{}{}

	return nil
}

// writePeripheralHeaderOnce writes the _created peripheral header of a counter family
// unless that header was already emitted by another store.
func writePeripheralHeaderOnce(writer io.Writer, family *FamilyType, emittedHeaders map[string]struct{}) error {
	peripheral := family.buildPeripheralHeader()

	if peripheral == "" {
		return nil
	}

	return writeHeaderOnce(writer, peripheral, emittedHeaders)
}

func (m *metricsWriter) writeFromStore(writer io.Writer, store *StoreType, emittedHeaders map[string]struct{}) error {
	for familyIdx, header := range store.headers {
		if err := writeHeaderOnce(writer, header, emittedHeaders); err != nil {
			return err
		}

		for _, metricFamilies := range store.metrics {
			if familyIdx >= len(metricFamilies) {
				continue
			}

			if err := writeMetricFamily(writer, metricFamilies[familyIdx]); err != nil {
				return err
			}
		}

		if familyIdx >= len(store.Families) {
			continue
		}

		if err := writePeripheralHeaderOnce(writer, store.Families[familyIdx], emittedHeaders); err != nil {
			return fmt.Errorf("error writing peripheral header: %w", err)
		}
	}

	return nil
}

func writeHeader(writer io.Writer, header string) error {
	if header != "" && header != "\n" {
		header += "\n"
	}

	_, err := writer.Write([]byte(header))

	return err
}

func writeMetricFamily(writer io.Writer, metric string) error {
	n, err := writer.Write([]byte(metric))
	if err != nil {
		return fmt.Errorf("error writing metric family after %d bytes: %w", n, err)
	}

	return nil
}
