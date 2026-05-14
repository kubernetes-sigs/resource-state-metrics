# Copilot instructions for `kubernetes-sigs/resource-state-metrics`

## Repository purpose and architecture
- This repository implements a Kubernetes controller that watches `ResourceMetricsMonitor` custom resources and exposes generated Prometheus metrics.
- Entry point: `main.go`.
- Core controller and metric generation flow are in `internal/`.
- API types are in `pkg/apis/resourcestatemetrics/v1alpha1/`.
- Generated clients/informers/listers are in `pkg/generated/` and should be updated via generated code tooling, not manual edits.
- Resolver implementations and resolver tests are in `pkg/resolver/`.
- End-to-end and golden rule tests are in `tests/`.

## Preferred workflow for cloud agents
1. Read `Makefile` and `.github/workflows/validations.yaml` first to align local checks with CI.
2. Keep changes minimal and scoped. Avoid refactoring unrelated packages.
3. If API types or generated interfaces change, run `make codegen` and then `make verify_generated`.
4. Run targeted tests first, then run broader checks before finalizing.

## Build, lint, and test commands

Run from the repository root.

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
- Do not edit files under `pkg/generated/` by hand.
- Keep resolver-specific behavior and tests grouped in `pkg/resolver/`.
- Extend or update tests in `tests/` when behavior changes.
- Conventional commit headers are required by hooks/CI (`build|chore|ci|docs|feat|fix|perf|refactor|revert|style|test`).

## Known environment issues and workarounds encountered during initial setup
1. `make setup` may fail at pre-commit hook installation when git has `core.hooksPath` configured:
   - Error: `Cowardly refusing to install hooks with core.hooksPath set.`
   - Workaround: run `git config --unset-all core.hooksPath` before `make setup`, or skip hook installation if only ephemeral CI-style checks are needed.
2. `make lint` may fail with `/bin/bash: line 1: Makefile: command not found` when `checkmake` is installed but not on `PATH`.
   - Workaround: prepend Go bin directory before linting:
   - `export PATH="$(go env GOPATH)/bin:$PATH"`

## High-signal files to inspect for most changes
- `main.go`
- `internal/controller.go`
- `internal/store.go`
- `pkg/options/options.go`
- `tests/framework/framework.go`
- `.github/workflows/validations.yaml`
