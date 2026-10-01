# `resource-state-metrics`

[![CI](https://github.com/kubernetes-sigs/resource-state-metrics/actions/workflows/validations.yaml/badge.svg)](https://github.com/kubernetes-sigs/resource-state-metrics/actions/workflows/validations.yaml) [![Go Reference](https://pkg.go.dev/badge/github.com/kubernetes-sigs/resource-state-metrics.svg)](https://pkg.go.dev/github.com/kubernetes-sigs/resource-state-metrics)

## Summary

`resource-state-metrics` is a Kubernetes controller that builds on Kube-State-Metrics' Custom Resource State's ideology and generates metrics for custom resources based on the configuration specified in its managed resource, `ResourceMetricsMonitor`.

## Development

Start developing by following these steps:

- Deploy the controller with `make local`.
- Test out your changes with `make apply_testdata`.
  - Telemetry metrics, by default, are exposed at `:9998/metrics`.
  - Resource metrics, by default, are exposed at `:9999/metrics`.
- Start an interactive `pprof` session with `make pprof`.

For more details, take a look at the [Makefile](Makefile) targets.

## Notes

- Garbage in, garbage out: Invalid configurations will generate invalid metrics. The exception to this being that certain checks that ensure metric structure are still present (for e.g., `value` should be a `float64`).
- Library support: The module is **never** intended to be used as a library, and as such, does not export any functions or types, with `pkg/` being an exception (for managed types and such).
- Metrics stability: There are no metrics [stability](https://kubernetes.io/blog/2021/04/23/kubernetes-release-1.21-metrics-stability-ga/) guarantees, as the metrics are user-generated.
- No middle-ware: The configuration is `unmarshal`led into a set of stores that the codebase directly operates on. There is no middle-ware that processes the configuration before it is used, in order to avoid unnecessary complexity. However, the expression(s) within the `value` and `labelValues` may need to be evaluated before being used, and as such, are exceptions.
- ~~Metric configurations only scale horizontally, i.e., one metric configuration cannot end up generating multiple metrics. Please define collectors for complex cases.~~ Multiple-metrics may be generated from a query resolution that targets a composite data structure. This also allows for recursively generating metrics for nested data structures as well.
- Non-turning-complete languages cannot express all possible metrics. For such cases, consider using a collector (`/external`). Such metrics are exposed through the `/external` endpoint of the "main" instance and defined in [`./external`](./external).
- The managed resource, `ResourceMetricsMonitor` is namespace-scoped, but, to keep in accordance with KubeStateMetrics' `CustomResourceState`, which allows for collecting metrics from cluster-wide resources, it is possible to omit the `field` and `label` selectors to achieve that result.
- [`client_python`](https://github.com/prometheus/client_python/blob/8673912276bdca7ddbca5d163eb11422b546bffb/prometheus_client/metrics.py)'s OpenMetrics implementation is the single source of truth upon which the various metric type implementations here are based on and tested against.

## TODO

#### Planned (in the following order)

##### GA

- [X] Meta-metrics for metric generation failures.
- [X] Utilize fake client-set for all e2e tests.
- [X] Add boilerplate headers automation.
- [X] ~~[`s/stores/generators`](https://github.com/kubernetes/enhancements/pull/4811#discussion_r2121842302)~~ Reverted: "stores" is clearer terminology
- [X] https://github.com/rexagod/resource-state-metrics/issues/1
- [X] https://github.com/rexagod/resource-state-metrics/issues/6
- [X] Print controller logs in the CI.
- [X] s/dependabot/renovate: https://github.com/kubernetes/org/issues/6167
- [X] Respect and keep up will all relevant metric types that are supported in Prometheus' OpenMetrics implementation.
- [X] Add `mixins`.
- [X] [Cardinality estimation, and control](https://github.com/rexagod/resource-state-metrics/issues/2)
  - [X] ~~Talk to Prom server to get an idea of relevant label-sets' cardinality?~~
  - [X] Use an offline-preferred approach with heuristics and internal context?
  - [X] This will need to be reflected in the resource status (and tested outside of golden rules).
- [X] Support testing status sub-resource in e2e tests (`.out`?).
- [X] Typed spec instead of the YAML blob currently used in the `ResourceMetricsMonitor` CRD.
- [X] [`Starlark`](https://github.com/google/starlark-go) resolver (for more demanding use-cases)
- [X] https://github.com/kubernetes-sigs/resource-state-metrics/issues/14 (GVK wildcard support)

##### Post-GA

- [ ] Register the repository on the K8s release machinery, also integrate the bot.
- [ ] Add golden rules covering all CRS constructs.
- [ ] Dynamic admission control for `ResourceMetricsMonitor` CRD.

#### Done

- [X] CEL expressions for metric generation (or [*unstructured.Unstructured](https://github.com/kubernetes/apimachinery/issues/181), if that suffices).
- [X] Conformance test(s) for Kube-State-Metrics' [Custom Resource State API](https://github.com/kubernetes/kube-state-metrics/blob/main/docs/metrics/extend/customresourcestate-metrics.md#multiple-metricskitchen-sink).
- [X] Benchmark(s) for Kube-State-Metrics' [Custom Resource State API](https://github.com/kubernetes/kube-state-metrics/blob/main/docs/metrics/extend/customresourcestate-metrics.md#multiple-metricskitchen-sink).
- [X] E2E tests covering the controller's basic functionality.
- [X] `s/CRSM/CRDMetrics`.
- [X] [Draft out a KEP](https://github.com/kubernetes/enhancements/issues/4785).
- [X] `s/CRDMetrics/ResourceStateMetrics`.
- [X] Make `ResourceMetricsMonitor` namespaced-scope. This allows for:
  - [X] per-namespace configuration (separate configurations between teams), and,
  - [X] ~~garbage collection (without `finalizers`), since currently the namespace-scoped deployment manages its cluster-scoped resources~~ `ResourceMetricsMonitor`s are user-managed, and should persist.

###### [License](./LICENSE)
