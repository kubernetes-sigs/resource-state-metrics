// Copyright 2026 The Kubernetes resource-state-metrics Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

{
  prometheusAlerts+:: {
    groups+: [
      {
        name: 'resource-state-metrics',
        rules: [
          {
            alert: 'ResourceStateMetricsEventProcessingErrors',
            expr: |||
              (
                sum by (%(clusterLabel)s) (rate(resource_state_metrics_events_processed_total{%(resourceStateMetricsSelector)s, status="failed"}[5m]))
                /
                sum by (%(clusterLabel)s) (rate(resource_state_metrics_events_processed_total{%(resourceStateMetricsSelector)s}[5m]))
              ) > 0.1
            ||| % $._config,
            'for': '15m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'resource-state-metrics is experiencing event processing errors.',
              description: 'resource-state-metrics is failing to process more than 10%% of events. This may indicate issues with ResourceMetricsMonitor configurations or target resources.',
            },
          },
          {
            alert: 'ResourceStateMetricsCELEvaluationErrors',
            expr: |||
              (
                sum by (%(clusterLabel)s) (rate(resource_state_metrics_resolver_evaluations_total{%(resourceStateMetricsSelector)s, resolver="cel", result="error"}[5m]))
                /
                sum by (%(clusterLabel)s) (rate(resource_state_metrics_resolver_evaluations_total{%(resourceStateMetricsSelector)s, resolver="cel"}[5m]))
              ) > 0.1
            ||| % $._config,
            'for': '15m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'resource-state-metrics CEL expressions are failing.',
              description: 'More than 10%% of CEL expression evaluations are failing. This indicates issues with CEL queries in ResourceMetricsMonitor configurations.',
            },
          },
          {
            alert: 'ResourceStateMetricsCELEvaluationTimeouts',
            expr: |||
              sum by (%(clusterLabel)s) (rate(resource_state_metrics_resolver_evaluations_total{%(resourceStateMetricsSelector)s, resolver="cel", result="timeout"}[5m])) > 0
            ||| % $._config,
            'for': '15m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'resource-state-metrics CEL expressions are timing out.',
              description: 'CEL expression evaluations are timing out. Consider increasing the CEL timeout limit or simplifying the expressions.',
            },
          },
          {
            alert: 'ResourceStateMetricsHighRequestLatency',
            expr: |||
              histogram_quantile(0.99, sum by (%(clusterLabel)s, le) (rate(resource_state_metrics_http_request_duration_seconds_bucket{%(resourceStateMetricsSelector)s}[5m]))) > 10
            ||| % $._config,
            'for': '15m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'resource-state-metrics has high request latency.',
              description: 'The 99th percentile latency for resource-state-metrics metric requests exceeds 10 seconds. This may cause scrape timeouts and missing metrics.',
            },
          },
          {
            alert: 'ResourceStateMetricsDown',
            expr: |||
              absent(up{%(resourceStateMetricsSelector)s} == 1)
            ||| % $._config,
            'for': '15m',
            labels: {
              severity: 'critical',
            },
            annotations: {
              summary: 'resource-state-metrics is down.',
              description: 'resource-state-metrics has disappeared from Prometheus target discovery or is not responding.',
            },
          },
          {
            alert: 'ResourceStateMetricsMonitorFailing',
            expr: |||
              (
                sum by (%(clusterLabel)s, namespace, name) (rate(resource_state_metrics_events_processed_total{%(resourceStateMetricsSelector)s, status="failed"}[15m]))
                /
                sum by (%(clusterLabel)s, namespace, name) (rate(resource_state_metrics_events_processed_total{%(resourceStateMetricsSelector)s}[15m]))
              ) > 0.5
            ||| % $._config,
            'for': '30m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'A ResourceMetricsMonitor is persistently failing.',
              description: 'The ResourceMetricsMonitor {{ $labels.namespace }}/{{ $labels.name }} is failing more than 50%% of event processing attempts. Check the monitor configuration and target resource availability.',
            },
          },
          {
            alert: 'ResourceStateMetricsCardinalityExceeded',
            expr: |||
              sum by (%(clusterLabel)s, namespace, name) (
                increase(resource_state_metrics_cardinality_exceeded_total{%(resourceStateMetricsSelector)s}[15m])
              ) > 0
            ||| % $._config,
            'for': '5m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'A ResourceMetricsMonitor has exceeded cardinality limits.',
              description: 'The ResourceMetricsMonitor {{ $labels.namespace }}/{{ $labels.name }} has exceeded its configured cardinality thresholds. Metric generation may be cut off. Review the configuration to reduce label cardinality.',
            },
          },
          {
            alert: 'ResourceStateMetricsCardinalityApproachingLimit',
            expr: |||
              (
                resource_state_metrics_resource_cardinality{%(resourceStateMetricsSelector)s}
                /
                resource_state_metrics_resource_cardinality_limit{%(resourceStateMetricsSelector)s}
              ) > 0.8
              and
              resource_state_metrics_resource_cardinality_limit{%(resourceStateMetricsSelector)s} > 0
            ||| % $._config,
            'for': '15m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'A ResourceMetricsMonitor is approaching its cardinality limit.',
              description: 'The ResourceMetricsMonitor {{ $labels.namespace }}/{{ $labels.name }} is at {{ $value | humanizePercentage }} of its configured cardinality limit. Consider increasing the limit or reducing label cardinality before metrics are cut off.',
            },
          },
          {
            alert: 'ResourceStateMetricsFamilyCardinalityApproachingLimit',
            expr: |||
              (
                resource_state_metrics_family_cardinality{%(resourceStateMetricsSelector)s}
                /
                resource_state_metrics_family_cardinality_limit{%(resourceStateMetricsSelector)s}
              ) > 0.8
              and
              resource_state_metrics_family_cardinality_limit{%(resourceStateMetricsSelector)s} > 0
            ||| % $._config,
            'for': '15m',
            labels: {
              severity: 'info',
            },
            annotations: {
              summary: 'A metric family is approaching its cardinality limit.',
              description: 'The family "{{ $labels.family }}" in ResourceMetricsMonitor {{ $labels.namespace }}/{{ $labels.name }} is at {{ $value | humanizePercentage }} of its configured cardinality limit.',
            },
          },
          {
            alert: 'ResourceStateMetricsGlobalCardinalityApproachingLimit',
            expr: |||
              (
                resource_state_metrics_global_cardinality{%(resourceStateMetricsSelector)s}
                /
                resource_state_metrics_global_cardinality_limit{%(resourceStateMetricsSelector)s}
              ) > 0.8
              and
              resource_state_metrics_global_cardinality_limit{%(resourceStateMetricsSelector)s} > 0
            ||| % $._config,
            'for': '15m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'Global cardinality is approaching its limit.',
              description: 'Total cardinality across all ResourceMetricsMonitors is at {{ $value | humanizePercentage }} of the configured global limit. Review RMM configurations to reduce overall cardinality.',
            },
          },
          {
            alert: 'ResourceStateMetricsDuplicateStores',
            expr: |||
              resource_state_metrics_duplicate_stores{%(resourceStateMetricsSelector)s} > 0
            ||| % $._config,
            'for': '5m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'Duplicate stores detected in ResourceMetricsMonitor.',
              description: 'The ResourceMetricsMonitor {{ $labels.namespace }}/{{ $labels.name }} has duplicate store configurations for {{ $labels.store }}. This may occur when wildcard expansion overlaps with explicit store definitions. Duplicate stores are skipped but indicate a configuration issue.',
            },
          },
          {
            alert: 'ResourceStateMetricsDuplicateFamilies',
            expr: |||
              resource_state_metrics_duplicate_families{%(resourceStateMetricsSelector)s} > 0
            ||| % $._config,
            'for': '5m',
            labels: {
              severity: 'warning',
            },
            annotations: {
              summary: 'Duplicate family names across stores in ResourceMetricsMonitor.',
              description: 'The family "{{ $labels.family }}" in ResourceMetricsMonitor {{ $labels.namespace }}/{{ $labels.name }} appears in multiple stores. This causes duplicate HELP/TYPE lines in prometheus output which may cause scraping issues. Use unique family names per store or consolidate stores.',
            },
          },
        ],
      },
    ],
  },
}
