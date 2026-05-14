# Copilot instructions for `kubernetes-sigs/resource-state-metrics`

## Repository purpose and architecture
- This repository implements a Kubernetes controller that watches `ResourceMetricsMonitor` custom resources and exposes generated Prometheus metrics.
- Entry point: `/home/runner/work/resource-state-metrics/resource-state-metrics/main.go`.
- Core controller and metric generation flow are in `/home/runner/work/resource-state-metrics/resource-state-metrics/internal`.
- API types are in `/home/runner/work/resource-state-metrics/resource-state-metrics/pkg/apis/resourcestatemetrics/v1alpha1`.
- Generated clients/informers/listers are in `/home/runner/work/resource-state-metrics/resource-state-metrics/pkg/generated` and should be updated via generated code tooling, not manual edits.
- Resolver implementations and resolver tests are in `/home/runner/work/resource-state-metrics/resource-state-metrics/pkg/resolver`.
- End-to-end and golden rule tests are in `/home/runner/work/resource-state-metrics/resource-state-metrics/tests`.

## Preferred workflow for cloud agents
1. Read `/home/runner/work/resource-state-metrics/resource-state-metrics/Makefile` and `/home/runner/work/resource-state-metrics/resource-state-metrics/.github/workflows/validations.yaml` first to align local checks with CI.
2. Keep changes minimal and scoped. Avoid refactoring unrelated packages.
3. If API types or generated interfaces change, run `make codegen` and then `make verify_generated`.
4. Run targeted tests first, then run broader checks before finalizing.

## Build, lint, and test commands

Run from `/home/runner/work/resource-state-metrics/resource-state-metrics`.

- Build:
  - `make build`
- Unit tests:
  - `make test_unit`
- E2E tests (fake client-based, no kind cluster required):
  - `make test_e2e`
- Lint:
  - `export PATH="$(go env GOPATH)/bin:$PATH" && make lint`
- Full verification bundle (heavy):
  - `make verify` (runs lint + tests + generated asset verification)

## Generation and manifest commands
- `make manifests` regenerates CRD and RBAC manifests.
- `make codegen` regenerates `pkg/generated`.
- `make jsonnet_manifests` regenerates manifests from template sources.
- `make verify_generated` checks generated code and manifests are up to date.

## Coding and change conventions
- Follow existing Kubernetes-style Go patterns and klog-based structured logging.
- Do not edit files under `/home/runner/work/resource-state-metrics/resource-state-metrics/pkg/generated` by hand.
- Keep resolver-specific behavior and tests grouped in `/home/runner/work/resource-state-metrics/resource-state-metrics/pkg/resolver`.
- Extend or update tests in `/home/runner/work/resource-state-metrics/resource-state-metrics/tests` when behavior changes.
- Conventional commit headers are required by hooks/CI (`build|chore|ci|docs|feat|fix|perf|refactor|revert|style|test`).

## Known environment issues and workarounds encountered during initial setup
1. `make setup` may fail at pre-commit hook installation when git has `core.hooksPath` configured:
   - Error: `Cowardly refusing to install hooks with core.hooksPath set.`
   - Workaround: run `git config --unset-all core.hooksPath` before `make setup`, or skip hook installation if only ephemeral CI-style checks are needed.
2. `make lint` may fail with `/bin/bash: line 1: Makefile: command not found` when `checkmake` is installed but not on `PATH`.
   - Workaround: prepend Go bin directory before linting:
   - `export PATH="$(go env GOPATH)/bin:$PATH"`

## High-signal files to inspect for most changes
- `/home/runner/work/resource-state-metrics/resource-state-metrics/main.go`
- `/home/runner/work/resource-state-metrics/resource-state-metrics/internal/controller.go`
- `/home/runner/work/resource-state-metrics/resource-state-metrics/internal/store.go`
- `/home/runner/work/resource-state-metrics/resource-state-metrics/pkg/options/options.go`
- `/home/runner/work/resource-state-metrics/resource-state-metrics/tests/framework/framework.go`
- `/home/runner/work/resource-state-metrics/resource-state-metrics/.github/workflows/validations.yaml`
