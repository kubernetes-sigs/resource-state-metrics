SHELL := /bin/bash

ASSETS_DIR ?= assets
BOILERPLATE_GO_COMPLIANT ?= hack/boilerplate.go.txt
BOILERPLATE_YAML_COMPLIANT ?= hack/boilerplate.yaml.txt
BUILD_TAG ?= $(shell git describe --tags --exact-match 2>/dev/null || echo "latest")
CHECKMAKE ?= $(shell go env GOBIN)/checkmake
CHECKMAKE_VERSION ?= v0.3.2
CODE_GENERATOR_VERSION ?= v0.32.3
COMMON = github.com/prometheus/common
CONTROLLER_GEN ?= $(shell go env GOBIN)/controller-gen
CONTROLLER_GEN_APIS_DIR ?= pkg/apis
CONTROLLER_GEN_OUT_DIR ?= /tmp/resource-state-metrics/controller-gen
CONTROLLER_GEN_VERSION ?= v0.16.5
CREATED_AT_EPOCH ?=
GO ?= go
GOJSONTOYAML ?= $(shell go env GOBIN)/gojsontoyaml
GOJSONTOYAML_VERSION ?= v0.1.0
GOLANGCI_LINT ?= $(shell go env GOBIN)/golangci-lint
GOLANGCI_LINT_CONFIG ?= .golangci.yaml
GOLANGCI_LINT_VERSION ?= v2.10.1
GOLDEN_FILES = $(shell find tests/golden -type f -name "*.yaml")
GOLDEN_METRICS_FILE ?= tests/golden/metrics.txt
GO_FILES = $(shell find . -type d -name vendor -prune -o -type f -name "*.go" -print)
JSONNET ?= $(shell go env GOBIN)/jsonnet
JSONNETFMT ?= $(shell go env GOBIN)/jsonnetfmt
JSONNET_FILES = $(shell find jsonnet -type f -name "*.jsonnet" -o -name "*.libsonnet")
JSONNET_MANIFESTS_DIR ?= jsonnet/manifests
JSONNET_VERSION ?= v0.21.0
KUBECTL ?= kubectl
LOCAL_NAMESPACE ?= default
MAIN_METRICS_PORT ?= 9999
MARKDOWNFMT ?= $(shell go env GOBIN)/markdownfmt
MARKDOWNFMT_VERSION ?= v3.1.0
MD_FILES = $(shell find . \( -type d -name 'vendor' -o -type d -name $(patsubst %/,%,$(patsubst ./%,%,$(ASSETS_DIR))) \) -prune -o -type f -name "*.md" -print)
PIPX ?= pipx
PPROF_OPTIONS ?=
PPROF_PORT ?= 9998
PROJECT_NAME = resource-state-metrics
V ?= 4
VALE ?= vale
VALE_ARCH ?= $(if $(filter $(shell uname -m),arm64),macOS_arm64,Linux_64-bit)
VALE_STYLES_DIR ?= /tmp/.vale/styles
VALE_VERSION ?= 3.1.0
YAMLFMT ?= $(shell go env GOBIN)/yamlfmt
YAMLFMT_VERSION ?= v0.16.0
YAML_FILES = $(shell find . -type d -name vendor -prune -o -type d -name $(patsubst %/,%,$(patsubst ./%,%,$(ASSETS_DIR))) -prune -o \( -name "*.yaml" -o -name "*.yml" \) -print | grep -v "^./vendor" | grep -v "^./$(ASSETS_DIR)")
YQ ?= $(shell go env GOBIN)/yq
YQ_VERSION ?= v4.52.4

BRANCH = $(shell git rev-parse --abbrev-ref HEAD)
BUILD_DATE := $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
GIT_COMMIT = $(shell git rev-parse --short HEAD)
RUNNER = $(shell id -u -n)@$(shell hostname)
VERSION = $(shell cat VERSION)
LDFLAGS := -s -w \
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
setup:
	# Setup vale.
	@if [ ! -f $(ASSETS_DIR)/$(VALE) ]; then wget https://github.com/errata-ai/vale/releases/download/v$(VALE_VERSION)/vale_$(VALE_VERSION)_$(VALE_ARCH).tar.gz && \
    mkdir -p assets && tar -xvzf vale_$(VALE_VERSION)_$(VALE_ARCH).tar.gz -C $(ASSETS_DIR) && \
    rm vale_$(VALE_VERSION)_$(VALE_ARCH).tar.gz && \
    chmod +x $(ASSETS_DIR)/$(VALE); \
	fi
	# Setup yq.
	@$(GO) install github.com/mikefarah/yq/v4@$(YQ_VERSION)
	# Setup markdownfmt.
	@$(GO) install github.com/Kunde21/markdownfmt/v3/cmd/markdownfmt@$(MARKDOWNFMT_VERSION)
	# Setup golangci-lint.
	@$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	# Setup controller-gen.
	@$(GO) install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)
	# Setup code-generator.
	@$(GO) install k8s.io/code-generator/cmd/...@$(CODE_GENERATOR_VERSION)
	# Setup checkmake.
	@$(GO) install github.com/checkmake/checkmake/cmd/checkmake@$(CHECKMAKE_VERSION)
	# Setup jsonnet.
	@$(GO) install github.com/google/go-jsonnet/cmd/jsonnet@$(JSONNET_VERSION)
	@$(GO) install github.com/google/go-jsonnet/cmd/jsonnetfmt@$(JSONNET_VERSION)
	@$(GO) install github.com/brancz/gojsontoyaml@$(GOJSONTOYAML_VERSION)
	# Setup yamlfmt.
	@$(GO) install github.com/google/yamlfmt/cmd/yamlfmt@$(YAMLFMT_VERSION)
	# Setup pre-commit hooks.
	@$(PIPX) install pre-commit >/dev/null || \
		(printf "pipx is required to install pre-commit. Please install pipx, or an alternate pip package, for e.g., pip3, and run 'make setup' (with PIPX in the latter case, where pipx is not used) again.\n" && exit 1)
	@pre-commit install --hook-type commit-msg >/dev/null
	# Setup commit message template.
	@# --always-make: Ensure .gitmessage is always updated at setup.
	@$(MAKE) --always-make --no-print-directory -s .gitmessage
	@git config commit.template .gitmessage

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
	@$(GO) build -a -installsuffix cgo -ldflags "$(LDFLAGS)" -o $@

.PHONY: build
build: $(PROJECT_NAME)

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
	@go tool pprof ":$(PPROF_PORT)" $(PPROF_OPTIONS)

.PHONY: test_unit
test_unit:
	@$(GO) test -race $(shell go list ./... | \
		grep -v "/generated" | \
		grep -v "/signals" | \
		grep -v "/tests" | \
		grep -v "/version")

.PHONY: test_e2e
test_e2e:
	@$(GO) test -race ./tests/...

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

