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
// HELP/TYPE header only once and all of their samples are written together in store order. When the
// stores disagree on the HELP text of a shared family, the header of the first store is written and
// the configuration conflict is reported by the configurer.
// A counter family's synthetic _created metric is skipped when its published name is already used by
// a configured family, so that it cannot override that family's HELP/TYPE.
func (m *metricsWriter) writeStores(writer io.Writer) error {
	if len(m.stores) == 0 {
		return nil
	}

	emittedHeaders := make(map[string]struct{})

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

	skipCreated := skippedCreatedNames(families)

	for _, name := range familyOrder {
		if err := writeFamily(writer, name, families[name], emittedHeaders, skipCreated); err != nil {
			return err
		}
	}

	return nil
}

// skippedCreatedNames returns the counter families whose synthetic _created metric name is
// already used by another configured family. Emitting the synthetic metric would either
// suppress the real family's HELP/TYPE or attach its samples to the wrong family.
func skippedCreatedNames(families map[string][]familySnapshot) map[string]struct{} {
	skipped := make(map[string]struct{})

	for name := range families {
		if !strings.HasSuffix(name, "_total") {
			continue
		}

		if _, used := families[strings.TrimSuffix(name, "_total")+"_created"]; used {
			skipped[name] = struct{}{}
		}
	}

	return skipped
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

// writeHeaderOnce writes header to writer only when its family was not emitted before,
// marking the family as emitted so that a shared family header is written only once.
func writeHeaderOnce(writer io.Writer, familyName, header string, emittedHeaders map[string]struct{}) error {
	if _, emitted := emittedHeaders[familyName]; emitted {
		return nil
	}

	if err := writeHeader(writer, header); err != nil {
		return fmt.Errorf("error writing header: %w", err)
	}

	emittedHeaders[familyName] = struct{}{}

	return nil
}

// earliestFamily returns the snapshot whose family was created first, so that the
// merged family does not depend on the order in which the stores were written.
func earliestFamily(snapshots []familySnapshot) *familySnapshot {
	earliest := &snapshots[0]

	for idx := 1; idx < len(snapshots); idx++ {
		if snapshots[idx].family.createdAt.Before(earliest.family.createdAt) {
			earliest = &snapshots[idx]
		}
	}

	return earliest
}

// writeFamily writes the header of a family once, followed by the samples collected
// from every store and, for counters, the family's _created peripheral metric.
func writeFamily(writer io.Writer, familyName string, snapshots []familySnapshot, emittedHeaders, skipCreated map[string]struct{}) error {
	first := snapshots[0]

	if err := writeHeaderOnce(writer, familyName, first.header, emittedHeaders); err != nil {
		return err
	}

	for _, snapshot := range snapshots {
		for _, sample := range snapshot.samples {
			if err := writeMetricFamily(writer, sample); err != nil {
				return err
			}
		}
	}

	if _, skip := skipCreated[familyName]; skip {
		return nil
	}

	// Stores sharing a family are built separately, so their createdAt values can
	// differ slightly. Use the earliest one to keep the merged family's creation
	// time stable and independent of the store order.
	peripheral := earliestFamily(snapshots).family.buildPeripheralHeader()
	if peripheral == "" {
		return nil
	}

	// The _created sample is published as <name without the _total suffix>_created,
	// so the dedup key must use that name and not familyName + "_created", which
	// would collide with a family that is literally named that way.
	createdFamilyName := strings.TrimSuffix(familyName, "_total") + "_created"

	return writeHeaderOnce(writer, createdFamilyName, peripheral, emittedHeaders)
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
