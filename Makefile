SHELL := $(shell command -v bash 2>/dev/null || echo /bin/sh)

# OS / architecture detection (used by `make setup`)
# uname -s / -m report differently per OS; Git Bash/MSYS on Windows also
# expose uname, so this covers Linux, macOS, and Windows-via-Git-Bash/WSL.
UNAME_S := $(shell uname -s 2>/dev/null || echo Windows)
UNAME_M := $(shell uname -m 2>/dev/null || echo x86_64)
IS_WINDOWS := $(filter MINGW% MSYS% CYGWIN% Windows,$(UNAME_S))

ALL_ARCH ?= amd64 arm64
# A literal comma cannot appear in a $(subst) call because Make parses it as
# an argument separator before the function ever sees it.
comma := ,
DOCKER_BUILDX_CMD ?= docker buildx
GOBIN ?= $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN = $(shell go env GOPATH)/bin
endif
ASSETS_DIR ?= assets
BOILERPLATE_GO_COMPLIANT ?= hack/boilerplate.go.txt
BOILERPLATE_YAML_COMPLIANT ?= hack/boilerplate.yaml.txt
BUILD_TAG ?= $(shell git describe --tags --exact-match 2>/dev/null || echo "latest")
CHECKMAKE ?= $(GOBIN)/checkmake
CHECKMAKE_VERSION ?= v0.3.2
CODE_GENERATOR_VERSION ?= v0.32.3
COMMON = github.com/prometheus/common
CONTROLLER_GEN ?= $(GOBIN)/controller-gen
CONTROLLER_GEN_APIS_DIR ?= pkg/apis
CONTROLLER_GEN_OUT_DIR ?= /tmp/resource-state-metrics/controller-gen
CONTROLLER_GEN_VERSION ?= v0.16.5
CREATED_AT_EPOCH ?=
GO ?= go
GOJSONTOYAML ?= $(GOBIN)/gojsontoyaml
GOJSONTOYAML_VERSION ?= v0.1.0
GOLANGCI_LINT ?= $(GOBIN)/golangci-lint
GOLANGCI_LINT_CONFIG ?= .golangci.yaml
GOLANGCI_LINT_VERSION ?= v2.10.1
GOLDEN_FILES = $(shell find tests/golden -type f -name "*.yaml")
GOLDEN_METRICS_FILE ?= tests/golden/metrics.txt
GO_FILES = $(shell find . -type d -name vendor -prune -o -type f -name "*.go" -print)
JSONNET ?= $(GOBIN)/jsonnet
JSONNETFMT ?= $(GOBIN)/jsonnetfmt
JSONNET_FILES = $(shell find jsonnet -type f -name "*.jsonnet" -o -name "*.libsonnet")
JSONNET_MANIFESTS_DIR ?= jsonnet/manifests
JSONNET_VERSION ?= v0.21.0
KUBECTL ?= kubectl
LOCAL_NAMESPACE ?= default
MAIN_METRICS_PORT ?= 9999
MARKDOWNFMT ?= $(GOBIN)/markdownfmt
MARKDOWNFMT_VERSION ?= v3.1.0
MD_FILES = $(shell find . \( -type d -name 'vendor' -o -type d -name $(patsubst %/,%,$(patsubst ./%,%,$(ASSETS_DIR))) \) -prune -o -type f -name "*.md" -print)
PIPX ?= pipx
PPROF_OPTIONS ?=
PPROF_PORT ?= 9998
PROJECT_NAME = resource-state-metrics
REGISTRY ?= us-central1-docker.pkg.dev/k8s-staging-images/resource-state-metrics
TAG ?= $(BUILD_TAG)
V ?= 4
# Vale binary name, release-asset name, and archive format all depend on
# OS + arch. macOS/Linux use tar.gz; Windows releases ship as .zip and the
# binary is named vale.exe, not vale.
ifneq ($(IS_WINDOWS),)
VALE ?= vale.exe
VALE_ARCH ?= Windows_64-bit
VALE_EXT ?= zip
else ifeq ($(UNAME_S),Darwin)
VALE ?= vale
ifeq ($(UNAME_M),arm64)
VALE_ARCH ?= macOS_arm64
else
VALE_ARCH ?= macOS_64-bit
endif
VALE_EXT ?= tar.gz
else
VALE ?= vale
ifneq ($(filter $(UNAME_M),aarch64 arm64),)
VALE_ARCH ?= Linux_arm64
else
VALE_ARCH ?= Linux_64-bit
endif
VALE_EXT ?= tar.gz
endif
VALE_STYLES_DIR ?= /tmp/.vale/styles
VALE_VERSION ?= 3.1.0
YAMLFMT ?= $(GOBIN)/yamlfmt
YAMLFMT_VERSION ?= v0.16.0
YAML_FILES = $(shell find . -type d -name vendor -prune -o -type d -name $(patsubst %/,%,$(patsubst ./%,%,$(ASSETS_DIR))) -prune -o \( -name "*.yaml" -o -name "*.yml" \) -print | grep -v "^./vendor" | grep -v "^./$(ASSETS_DIR)")
YQ ?= $(GOBIN)/yq
YQ_VERSION ?= v4.52.4

BRANCH = $(shell git rev-parse --abbrev-ref HEAD)
BUILD_DATE := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
GIT_COMMIT = $(shell git rev-parse --short HEAD)
RUNNER = $(shell id -u -n)@$(shell hostname)
VERSION = $(shell cat VERSION)
GCFLAGS ?=
LDFLAGS ?= -s -w \
	-X ${COMMON}/version.Branch=${BRANCH} \
	-X ${COMMON}/version.BuildDate=${BUILD_DATE} \
	-X ${COMMON}/version.BuildUser=${RUNNER} \
	-X ${COMMON}/version.Revision=${GIT_COMMIT} \
	-X ${COMMON}/version.Version=v${VERSION} \
	$(if $(CREATED_AT_EPOCH),-X 'github.com/kubernetes-sigs/resource-state-metrics/internal.CreatedAtEpoch=$(CREATED_AT_EPOCH)')

.PHONY: all
all: lint $(PROJECT_NAME)

#########
# Setup #
#########

.PHONY: setup
setup: setup_vale setup_go_tools setup_precommit
	@$(MAKE) --always-make --no-print-directory -s .gitmessage
	@git config commit.template .gitmessage

.PHONY: setup_vale
setup_vale:
	# Setup vale
	@if [ ! -f $(ASSETS_DIR)/$(VALE) ]; then set -e; mkdir -p $(ASSETS_DIR); tmp=$$(mktemp -d); url="https://github.com/errata-ai/vale/releases/download/v$(VALE_VERSION)/vale_$(VALE_VERSION)_$(VALE_ARCH).$(VALE_EXT)"; archive="$$tmp/vale.$(VALE_EXT)"; if command -v curl >/dev/null 2>&1; then curl -fsSL -o "$$archive" "$$url"; elif command -v wget >/dev/null 2>&1; then wget -q -O "$$archive" "$$url"; else echo "Neither curl nor wget is installed. Please install one and re-run 'make setup'." >&2; exit 1; fi; if [ "$(VALE_EXT)" = "zip" ]; then command -v unzip >/dev/null 2>&1 || (echo "unzip is required to install vale on Windows." >&2 && exit 1); unzip -oq "$$archive" -d $(ASSETS_DIR); else tar -xzf "$$archive" -C $(ASSETS_DIR); fi; chmod +x $(ASSETS_DIR)/$(VALE) 2>/dev/null || true; rm -rf "$$tmp"; fi

.PHONY: setup_go_tools
setup_go_tools:
	# Setup Go tools
	@$(GO) install github.com/mikefarah/yq/v4@$(YQ_VERSION) && $(GO) install github.com/Kunde21/markdownfmt/v3/cmd/markdownfmt@$(MARKDOWNFMT_VERSION) && $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) && $(GO) install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION) && $(GO) install k8s.io/code-generator/cmd/...@$(CODE_GENERATOR_VERSION) && $(GO) install github.com/checkmake/checkmake/cmd/checkmake@$(CHECKMAKE_VERSION) && $(GO) install github.com/google/go-jsonnet/cmd/jsonnet@$(JSONNET_VERSION) && $(GO) install github.com/google/go-jsonnet/cmd/jsonnetfmt@$(JSONNET_VERSION) && $(GO) install github.com/brancz/gojsontoyaml@$(GOJSONTOYAML_VERSION) && $(GO) install github.com/google/yamlfmt/cmd/yamlfmt@$(YAMLFMT_VERSION)

.PHONY: setup_precommit
setup_precommit:
	# Setup pre-commits
	@if command -v $(PIPX) >/dev/null 2>&1; then $(PIPX) install pre-commit >/dev/null; elif command -v pre-commit >/dev/null 2>&1; then :; elif command -v pip3 >/dev/null 2>&1; then pip3 install --user pre-commit >/dev/null 2>&1 || pip3 install --user --break-system-packages pre-commit >/dev/null; elif command -v pip >/dev/null 2>&1; then pip install --user pre-commit >/dev/null 2>&1 || pip install --user --break-system-packages pre-commit >/dev/null; else echo "Could not find pipx, pre-commit, pip3, or pip. Install one of these and re-run 'make setup'." >&2; exit 1; fi
	@python3 -m pre_commit install --hook-type commit-msg >/dev/null 2>&1 || pre-commit install --hook-type commit-msg >/dev/null

.gitmessage: hack/check-conventional-commit.sh
	@types=$$(grep 'ALLOWED_TYPES=' $< | cut -d'"' -f2 | tr '|' ' '); \
	printf '\n# type: <subject>\n#\n# <body>\n#\n# Allowed types: %s\n#' "$$types" > $@

##############
# Generating #
##############

.PHONY: manifests
manifests:
	@$(CONTROLLER_GEN) \
	rbac:headerFile=$(BOILERPLATE_YAML_COMPLIANT),roleName=$(PROJECT_NAME) crd:headerFile=$(BOILERPLATE_YAML_COMPLIANT) paths=./$(CONTROLLER_GEN_APIS_DIR)/... \
	output:rbac:artifacts:config=$(CONTROLLER_GEN_OUT_DIR) output:crd:dir=$(CONTROLLER_GEN_OUT_DIR) && \
	mv "$(CONTROLLER_GEN_OUT_DIR)/resource-state-metrics.instrumentation.k8s-sigs.io_resourcemetricsmonitors.yaml" "manifests/custom-resource-definition.yaml" && \
	mv "$(CONTROLLER_GEN_OUT_DIR)/role.yaml" "manifests/cluster-role.yaml"

.PHONY: codegen
codegen:
	@# Populate pkg/generated/.
	@./hack/update-codegen.sh

.PHONY: jsonnet_manifests
jsonnet_manifests: manifests
	@CONTROLLER_GEN_VERSION=$(CONTROLLER_GEN_VERSION) VERSION=$(VERSION) NAMESPACE=$(LOCAL_NAMESPACE) PROJECT_NAME=$(PROJECT_NAME) ./hack/generate-yamls-from-jsonnets.sh

.PHONY: generate
generate: manifests codegen jsonnet_manifests

#############
# Verifying #
#############

.PHONY: verify_codegen
verify_codegen:
	@./hack/verify-codegen.sh || (echo "Generated code is not up to date. Please run 'make codegen' to update it." && exit 1)

.PHONY: verify_manifests
verify_manifests: jsonnet_manifests
	@(git diff --exit-code $(JSONNET_MANIFESTS_DIR) manifests/ && echo "Manifests are up to date.") || (echo "Manifests are not up to date. Please run 'make jsonnet_manifests' to update them." && exit 1)

.PHONY: verify_generated
verify_generated: verify_codegen verify_manifests

.PHONY: verify
verify: lint test verify_generated

############
# Building #
############

.PHONY: image
image: $(PROJECT_NAME)
	@docker build -t $(PROJECT_NAME):$(BUILD_TAG) .

$(PROJECT_NAME): $(GO_FILES)
	$(GO) build -a -installsuffix cgo -ldflags "$(LDFLAGS)" -gcflags "$(GCFLAGS)" -o $@

.PHONY: build
build: $(PROJECT_NAME)

.PHONY: image-push
image-push:
	$(DOCKER_BUILDX_CMD) build --pull --push \
		--platform $(subst $(eval ) ,$(comma),$(addprefix linux/,$(ALL_ARCH))) \
		-t $(REGISTRY)/$(PROJECT_NAME):$(TAG) .

###########
# Running #
###########

.PHONY: load
load: image
	@kind load docker-image $(PROJECT_NAME):$(BUILD_TAG)

.PHONY: apply
apply: manifests delete
	# Applying manifests
	@$(KUBECTL) apply -f manifests
	# Applied manifests

.PHONY: delete
delete:
	# Deleting manifests
	@$(KUBECTL) delete --ignore-not-found -f manifests/
	# Deleted manifests

.PHONY: local
local: apply $(PROJECT_NAME)
	@$(KUBECTL) scale deployment $(PROJECT_NAME) --replicas=0 -n $(LOCAL_NAMESPACE) 2>/dev/null || true
	@./$(PROJECT_NAME) -v=$(V) -kubeconfig $(KUBECONFIG)

###########
# Testing #
###########

.PHONY: pprof
pprof:
	@$(GO) tool pprof ":$(PPROF_PORT)" $(PPROF_OPTIONS)

.PHONY: test_unit
test_unit:
	@$(GO) test -v -race $(shell go list ./... | \
		grep -v "/generated" | \
		grep -v "/signals" | \
		grep -v "/tests" | \
		grep -v "/version")

.PHONY: test_e2e
test_e2e:
	@$(GO) test -v -race ./tests/...

.PHONY: test
test: test_unit test_e2e

.PHONY: apply_testdata
apply_testdata: delete_testdata
	# Applying testdata
	@$(KUBECTL) apply -R -f tests/manifests/custom-resource-definition
	@$(KUBECTL) apply -R -f tests/manifests/custom-resource
	@$(YQ) '.in' $(GOLDEN_FILES) | $(KUBECTL) apply -f -
	# Applied testdata

.PHONY: delete_testdata
delete_testdata:
	# Deleting testdata
	-@$(KUBECTL) delete --ignore-not-found -R -f tests/manifests
	# Deleted testdata

.PHONY: golden_metrics
golden_metrics: $(GOLDEN_FILES)
	@$(YQ) --no-doc '.out.metrics[]' $(GOLDEN_FILES) > $(GOLDEN_METRICS_FILE)

.PHONY: compare_metrics
compare_metrics: golden_metrics
	@diff \
		<(sort $(GOLDEN_METRICS_FILE)) \
		<(curl -sf http://localhost:$(MAIN_METRICS_PORT)/metrics | grep -Ff $(GOLDEN_METRICS_FILE) | sort)

###########
# Linting #
###########

.PHONY: lint
lint: lint_makefile lint_yaml lint_md lint_go lint_jsonnet

.PHONY: lint_fix
lint_fix: lint_makefile lint_yaml_fix lint_md_fix lint_go_fix lint_jsonnet_fix

#####################
# Linting: Makefile #
#####################

checkmake: Makefile
	@$(CHECKMAKE) Makefile

.PHONY: lint_makefile
lint_makefile: checkmake

#################
# Linting: YAML #
#################

licensecheck_yaml: $(YAML_FILES)
	@./hack/fix-license-headers.sh --check $(YAML_FILES)

licensecheck_yaml_fix: $(YAML_FILES)
	@./hack/fix-license-headers.sh $(YAML_FILES)

yamlfmt: $(YAML_FILES)
	@$(YAMLFMT) -dry -quiet . || (echo "YAML files need formatting. Run 'make yamlfmt_fix' to fix." && exit 1)

yamlfmt_fix: $(YAML_FILES)
	@$(YAMLFMT) .

.PHONY: lint_yaml
lint_yaml: licensecheck_yaml yamlfmt

.PHONY: lint_yaml_fix
lint_yaml_fix: licensecheck_yaml_fix yamlfmt_fix

#####################
# Linting: Markdown #
#####################

vale: .vale.ini $(MD_FILES)
	@mkdir -p $(VALE_STYLES_DIR) && \
	$(ASSETS_DIR)/$(VALE) sync && \
	$(ASSETS_DIR)/$(VALE) $(MD_FILES)

markdownfmt: $(MD_FILES)
	@test -z "$(shell $(MARKDOWNFMT) -l $(MD_FILES))" || (echo "The following files need to be formatted with 'markdownfmt -w -gofmt':" $(shell $(MARKDOWNFMT) -l $(MD_FILES)) "" && exit 1)

markdownfmt_fix: $(MD_FILES)
	@for file in $(MD_FILES); do markdownfmt -w -gofmt $$file || exit 1; done

.PHONY: lint_md
lint_md: vale markdownfmt

.PHONY: lint_md_fix
lint_md_fix: vale markdownfmt_fix

###############
# Linting: Go #
###############

licensecheck_go: $(GO_FILES)
	@./hack/fix-license-headers.sh --check $(GO_FILES)

licensecheck_go_fix: $(GO_FILES)
	@./hack/fix-license-headers.sh $(GO_FILES)

golangci_lint: $(GO_FILES)
	@$(GOLANGCI_LINT) run -c $(GOLANGCI_LINT_CONFIG)

golangci_lint_fix: $(GO_FILES)
	@$(GOLANGCI_LINT) run --fix -c $(GOLANGCI_LINT_CONFIG)

.PHONY: lint_go
lint_go: licensecheck_go golangci_lint

.PHONY: lint_go_fix
lint_go_fix: licensecheck_go_fix golangci_lint_fix

####################
# Linting: Jsonnet #
####################

licensecheck_jsonnet: $(JSONNET_FILES)
	@./hack/fix-license-headers.sh --check $(JSONNET_FILES)

licensecheck_jsonnet_fix: $(JSONNET_FILES)
	@./hack/fix-license-headers.sh $(JSONNET_FILES)

jsonnetfmt: $(JSONNET_FILES)
	@test -z "$(shell $(JSONNETFMT) --test $(JSONNET_FILES) 2>&1)" || (echo "The following jsonnet files need to be formatted with 'jsonnetfmt -i':" && $(JSONNETFMT) --test $(JSONNET_FILES) && exit 1)

jsonnetfmt_fix: $(JSONNET_FILES)
	@$(JSONNETFMT) -i $(JSONNET_FILES)

.PHONY: lint_jsonnet
lint_jsonnet: licensecheck_jsonnet jsonnetfmt

.PHONY: lint_jsonnet_fix
lint_jsonnet_fix: licensecheck_jsonnet_fix jsonnetfmt_fix

###########
# Cleanup #
###########

.PHONY: clean
clean:
	@git clean -fxd
