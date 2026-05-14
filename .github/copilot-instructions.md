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
1. Treat command/target/path references in this file as snapshots of current repository state; verify they still match the repository before relying on them.
2. Read `Makefile` and `.github/workflows/validations.yaml` first to align local checks with CI.
3. Keep changes minimal and scoped. Avoid refactoring unrelated packages.
4. If API types or generated interfaces change, run `make codegen` and then `make verify_generated`.
5. Run targeted tests first, then run broader checks before finalizing.
6. After each PR merge, re-check this file against the current repository state and open a follow-up PR if any instruction here has drifted.

## Pull request descriptions and code review guidance

When creating pull request descriptions or reviewing PRs, use the following repository-adapted structure.

### PR description template
- **What changed**
  - Clear summary of modified files/components (`internal/`, `pkg/apis/`, `pkg/resolver/`, `tests/`, manifests, CI files).
  - Link related issue(s) or ticket(s).
- **Why**
  - Problem statement and user/business context.
  - Technical rationale for the chosen approach and notable alternatives.
- **Testing**
  - [ ] Unit tests (`make test_unit`) pass for impacted packages.
  - [ ] E2E tests (`make test_e2e`) pass for behavior changes.
  - [ ] Generated/code-style checks relevant to changes pass (`make verify_generated`, `make lint`).
  - [ ] Security/performance considerations were checked.
- **Breaking changes**
  - Note API/behavioral changes for `ResourceMetricsMonitor` or emitted metrics.
  - Include migration/rollout guidance for CRD, manifest, and alert/dashboard impacts when needed.

### Review focus areas
- **Security (🔒 / 🚨)**
  - Validate input handling from CRs/manifests and avoid information leakage in errors/logs.
  - Check authn/authz-sensitive code paths and ensure no hard-coded secrets.
- **Performance (⚡)**
  - Watch for expensive reconciliation loops, high-cardinality metrics, and unnecessary Kubernetes API calls.
  - Consider informer/cache usage and scalability impacts.
- **Testing (✅ / 💭)**
  - Ensure changed behavior is covered in unit or e2e tests and edge cases are exercised.
- **Documentation (📚)**
  - Confirm docs/manifests/instructions are updated when behavior, interfaces, or workflow expectations change.
- **Code quality (🧹)**
  - Keep functions focused, names clear, and error handling/logging consistent with existing patterns.

### Review style
- Be specific and constructive.
- Use this format where useful:
  - **Issue:** what needs attention
  - **Suggestion:** concrete change (with snippet if helpful)
  - **Why:** expected benefit/risk reduction
- Ask clarifying questions when intent is unclear.
- Prioritize recommendations that improve security, performance, reliability, and maintainability.

### Initial PR workflow-failure response (run once per PR unless explicitly asked again)

When a new PR is raised and workflows are failing because repository workflow conventions were not followed (for example, editing generated manifests directly or skipping required make targets), provide one actionable, step-by-step remediation comment:
1. Point to the exact failing workflow/job and error from logs.
2. Classify the root cause in repository terms (source files vs generated outputs, missing local validation, etc.).
3. Give concrete fix commands from repository root:
   - `make jsonnet_manifests && make manifests && make verify_generated` for manifest-generation drift.
   - `make codegen && make verify_generated` if generated client code is out of date.
   - `make build && make test_unit && make test_e2e` and `export PATH="$(go env GOPATH)/bin:$PATH" && make CHECKMAKE="$(go env GOPATH)/bin/checkmake" lint` for missed local checks.
4. Ask the author to commit regenerated artifacts and rerun CI.
5. Use the **Issue / Suggestion / Why** format, and include any security/performance/testing/doc follow-ups that apply.

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
