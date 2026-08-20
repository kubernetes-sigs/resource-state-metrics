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

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

// metricsWriter writes metrics from a group of stores to an io.Writer.
type metricsWriter struct {
	stores      []*StoreType
	contentType expfmt.Format
}

// newMetricsWriter creates a new metricsWriter.
func newMetricsWriter(contentType expfmt.Format, stores ...*StoreType) *metricsWriter {
	return &metricsWriter{
		stores:      stores,
		contentType: contentType,
	}
}

// writeStores encodes metrics from all stores using the negotiated content type.
func (m *metricsWriter) writeStores(writer io.Writer) error {
	if len(m.stores) == 0 {
		return nil
	}

	enc := expfmt.NewEncoder(writer, m.contentType)

	for _, store := range m.stores {
		store.mutex.RLock()
		err := m.writeFromStore(enc, store)
		store.mutex.RUnlock()

		if err != nil {
			return err
		}
	}

	return nil
}

func (m *metricsWriter) writeFromStore(enc expfmt.Encoder, store *StoreType) error {
	for i, family := range store.Families {
		var allMetrics []*dto.Metric

		for _, perFamilyMetrics := range store.metrics {
			if i < len(perFamilyMetrics) && perFamilyMetrics[i] != nil {
				allMetrics = append(allMetrics, perFamilyMetrics[i].Metric...)
			}
		}

		if len(allMetrics) == 0 {
			continue
		}

		familyName := kubeCustomResourcePrefix + family.Name
		helpText := family.Help
		dtoType := dtoTypeFor(family.kind())
		mf := &dto.MetricFamily{
			Name:   &familyName,
			Help:   &helpText,
			Type:   &dtoType,
			Metric: allMetrics,
		}

		if err := enc.Encode(mf); err != nil {
			return fmt.Errorf("error encoding metric family %q: %w", family.Name, err)
		}

		if pf := family.buildPeripheralFamily(); pf != nil {
			if err := enc.Encode(pf); err != nil {
				return fmt.Errorf("error encoding peripheral family %q: %w", family.Name, err)
			}
		}
	}

	return nil
}
