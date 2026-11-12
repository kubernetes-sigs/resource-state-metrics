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

// familySnapshot captures the family metadata, its header and its metric samples
// for one store, so that metrics of the same family can be written together
// across stores without holding the store locks while writing.
type familySnapshot struct {
	family  *FamilyType
	header  string
	samples []string
}

// writeStores writes out metrics from the underlying stores to the given writer, per resource.
// It writes metrics so that the ones with the same name are grouped together when written out, and guarantees an exposition format that is safe to be ingested by Prometheus.
// Families shared by multiple stores, e.g. when a wildcard store expands to several kinds, get their
// HELP/TYPE header only once and all of their samples are written together in store order.
func (m *metricsWriter) writeStores(writer io.Writer) error {
	if len(m.stores) == 0 {
		return nil
	}

	var (
		familyOrder []string
		families    = make(map[string][]familySnapshot)
	)

	for _, store := range m.stores {
		store.mutex.RLock()
		snapshots := snapshotFamilies(store)
		store.mutex.RUnlock()

		for _, snapshot := range snapshots {
			name := snapshot.family.Name
			if _, seen := families[name]; !seen {
				familyOrder = append(familyOrder, name)
			}

			families[name] = append(families[name], snapshot)
		}
	}

	for _, name := range familyOrder {
		if err := writeFamily(writer, families[name]); err != nil {
			return err
		}
	}

	return nil
}

// snapshotFamilies copies the headers, metric samples and family metadata of a store
// so that the store lock is not held while the exposition is written.
func snapshotFamilies(store *StoreType) []familySnapshot {
	snapshots := make([]familySnapshot, 0, len(store.Families))

	for familyIdx, family := range store.Families {
		if familyIdx >= len(store.headers) {
			break
		}

		snapshot := familySnapshot{
			family: family,
			header: store.headers[familyIdx],
		}

		for _, metricFamilies := range store.metrics {
			if familyIdx >= len(metricFamilies) {
				continue
			}

			snapshot.samples = append(snapshot.samples, metricFamilies[familyIdx])
		}

		snapshots = append(snapshots, snapshot)
	}

	return snapshots
}

// writeFamily writes the header of a family once, followed by the samples collected
// from every store and, for counters, the family's _created peripheral metric.
func writeFamily(writer io.Writer, snapshots []familySnapshot) error {
	first := snapshots[0]

	if err := writeHeader(writer, first.header); err != nil {
		return fmt.Errorf("error writing header: %w", err)
	}

	for _, snapshot := range snapshots {
		for _, sample := range snapshot.samples {
			if err := writeMetricFamily(writer, sample); err != nil {
				return err
			}
		}
	}

	if peripheral := first.family.buildPeripheralHeader(); peripheral != "" {
		if err := writeHeader(writer, peripheral); err != nil {
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

	return err
}
