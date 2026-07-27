SHELL := /bin/bash

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

COMMON = github.com/prometheus/common
CONTROLLER_GEN_APIS_DIR ?= pkg/apis
CONTROLLER_GEN_OUT_DIR ?= /tmp/resource-state-metrics/controller-gen
PKG = github.com/kubernetes-sigs/resource-state-metrics
CREATED_AT_EPOCH ?=
GO ?= go
GOLANGCI_LINT_CONFIG ?= .golangci.yaml
GOLDEN_FILES = $(shell find tests/golden -type f -name "*.yaml")
GOLDEN_METRICS_FILE ?= tests/golden/metrics.txt
GO_FILES = $(shell find . -type d -name vendor -prune -o -type f -name "*.go" -print)
JSONNET_FILES = $(shell find jsonnet -type f -name "*.jsonnet" -o -name "*.libsonnet")
JSONNET_MANIFESTS_DIR ?= jsonnet/manifests
KUBECTL ?= kubectl
LOCAL_NAMESPACE ?= default
MAIN_METRICS_PORT ?= 9999
MD_FILES = $(shell find . \( -type d -name 'vendor' -o -type d -name $(patsubst %/,%,$(patsubst ./%,%,$(ASSETS_DIR))) \) -prune -o -type f -name "*.md" -print)
PIPX ?= pipx
PPROF_OPTIONS ?=
PPROF_PORT ?= 9998
PROJECT_NAME = resource-state-metrics
REGISTRY ?= us-central1-docker.pkg.dev/k8s-staging-images/resource-state-metrics
TAG ?= $(BUILD_TAG)
V ?= 4

VALE_ARCH ?= $(if $(filter $(shell uname -m),arm64),macOS_arm64,Linux_64-bit)
VALE_STYLES_DIR ?= /tmp/.vale/styles
YAML_FILES = $(shell find . -type d -name vendor -prune -o -type d -name $(patsubst %/,%,$(patsubst ./%,%,$(ASSETS_DIR))) -prune -o \( -name "*.yaml" -o -name "*.yml" \) -print | grep -v "^./vendor" | grep -v "^./$(ASSETS_DIR)")

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Dependencies

PWD := $(shell pwd)

## Location to install dependencies to
LOCALBIN ?= $(PWD)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
CHECKMAKE ?= $(LOCALBIN)/checkmake
YQ ?= $(LOCALBIN)/yq
JSONNET ?= $(LOCALBIN)/jsonnet
JSONNETFMT ?= $(LOCALBIN)/jsonnetfmt
GOJSONTOYAML ?= $(LOCALBIN)/gojsontoyaml
MARKDOWNFMT ?= $(LOCALBIN)/markdownfmt
YAMLFMT ?= $(LOCALBIN)/yamlfmt
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
DEEPCOPY_GEN ?= $(LOCALBIN)/deepcopy-gen
CLIENT_GEN ?= $(LOCALBIN)/client-gen
LISTER_GEN ?= $(LOCALBIN)/lister-gen
INFORMER_GEN ?= $(LOCALBIN)/informer-gen
VALE ?= $(LOCALBIN)/vale

## Tool Versions
CHECKMAKE_VERSION ?= v0.3.2
YQ_VERSION ?= v4.52.4
JSONNET_VERSION ?= v0.21.0
JSONNETFMT_VERSION ?= v0.21.0
GOJSONTOYAML_VERSION ?= v0.1.0
MARKDOWNFMT_VERSION ?= v3.1.0
YAMLFMT_VERSION ?= v0.16.0
GOLANGCI_LINT_VERSION ?= v2.10.1
CONTROLLER_GEN_VERSION ?= v0.16.5
CODE_GENERATOR_VERSION ?= v0.36.2
VALE_VERSION ?= 3.1.0

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

.PHONY: setup-pre-commit
setup-pre-commit: ## Setup pre-commit hooks and commit message template.
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

.PHONY: checkmake
checkmake: $(CHECKMAKE) ## Download checkmake locally if necessary.
$(CHECKMAKE): $(LOCALBIN)
	$(call go-install-tool,$(CHECKMAKE),github.com/checkmake/checkmake/cmd/checkmake,$(CHECKMAKE_VERSION))

.PHONY: yq
yq: $(YQ) ## Download yq locally if necessary.
$(YQ): $(LOCALBIN)
	$(call go-install-tool,$(YQ),github.com/mikefarah/yq/v4,$(YQ_VERSION))

.PHONY: jsonnet
jsonnet: $(JSONNET) ## Download jsonnet locally if necessary.
$(JSONNET): $(LOCALBIN)
	$(call go-install-tool,$(JSONNET),github.com/google/go-jsonnet/cmd/jsonnet,$(JSONNET_VERSION))

.PHONY: jsonnetfmt
jsonnetfmt: $(JSONNETFMT) ## Download jsonnetfmt locally if necessary.
$(JSONNETFMT): $(LOCALBIN)
	$(call go-install-tool,$(JSONNETFMT),github.com/google/go-jsonnet/cmd/jsonnetfmt,$(JSONNETFMT_VERSION))

.PHONY: gojsontoyaml
gojsontoyaml: $(GOJSONTOYAML) ## Download gojsontoyaml locally if necessary.
$(GOJSONTOYAML): $(LOCALBIN)
	$(call go-install-tool,$(GOJSONTOYAML),github.com/brancz/gojsontoyaml,$(GOJSONTOYAML_VERSION))

.PHONY: markdownfmt
markdownfmt: $(MARKDOWNFMT) ## Download markdownfmt locally if necessary.
$(MARKDOWNFMT): $(LOCALBIN)
	$(call go-install-tool,$(MARKDOWNFMT),github.com/Kunde21/markdownfmt/v3/cmd/markdownfmt,$(MARKDOWNFMT_VERSION))

.PHONY: yamlfmt
yamlfmt: $(YAMLFMT) ## Download yamlfmt locally if necessary.
$(YAMLFMT): $(LOCALBIN)
	$(call go-install-tool,$(YAMLFMT),github.com/google/yamlfmt/cmd/yamlfmt,$(YAMLFMT_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_GEN_VERSION))

.PHONY: deepcopy-gen
deepcopy-gen: $(DEEPCOPY_GEN) ## Download deepcopy-gen locally if necessary.
$(DEEPCOPY_GEN): $(LOCALBIN)
	$(call go-install-tool,$(DEEPCOPY_GEN),k8s.io/code-generator/cmd/deepcopy-gen,$(CODE_GENERATOR_VERSION))

.PHONY: client-gen
client-gen: $(CLIENT_GEN) ## Download client-gen locally if necessary.
$(CLIENT_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CLIENT_GEN),k8s.io/code-generator/cmd/client-gen,$(CODE_GENERATOR_VERSION))

.PHONY: lister-gen
lister-gen: $(LISTER_GEN) ## Download lister-gen locally if necessary.
$(LISTER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(LISTER_GEN),k8s.io/code-generator/cmd/lister-gen,$(CODE_GENERATOR_VERSION))

.PHONY: informer-gen
informer-gen: $(INFORMER_GEN) ## Download informer-gen locally if necessary.
$(INFORMER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(INFORMER_GEN),k8s.io/code-generator/cmd/informer-gen,$(CODE_GENERATOR_VERSION))

.PHONY: vale
vale: $(VALE) ## Download vale locally if necessary.
$(VALE): $(LOCALBIN)
	echo $(VALE)
	@# Setup vale.
	@if [ ! -f $(VALE) ]; then wget https://github.com/errata-ai/vale/releases/download/v$(VALE_VERSION)/vale_$(VALE_VERSION)_$(VALE_ARCH).tar.gz && \
    tar -xvzf vale_$(VALE_VERSION)_$(VALE_ARCH).tar.gz -C $(LOCALBIN) && \
    rm vale_$(VALE_VERSION)_$(VALE_ARCH).tar.gz && \
    chmod +x $(VALE); \
	fi

##@ Generating

.PHONY: manifests
manifests: $(CONTROLLER_GEN) ## Generate manifests e.g. CRD, RBAC etc.
	@$(CONTROLLER_GEN) \
	rbac:headerFile=$(BOILERPLATE_YAML_COMPLIANT),roleName=$(PROJECT_NAME) crd:headerFile=$(BOILERPLATE_YAML_COMPLIANT) paths=./$(CONTROLLER_GEN_APIS_DIR)/... \
	output:rbac:artifacts:config=$(CONTROLLER_GEN_OUT_DIR) output:crd:dir=$(CONTROLLER_GEN_OUT_DIR) && \
	mv "$(CONTROLLER_GEN_OUT_DIR)/resource-state-metrics.instrumentation.k8s-sigs.io_resourcemetricsmonitors.yaml" "manifests/custom-resource-definition.yaml" && \
	mv "$(CONTROLLER_GEN_OUT_DIR)/role.yaml" "manifests/cluster-role.yaml"

.PHONY: codegen
codegen: $(DEEPCOPY_GEN) $(CLIENT_GEN) $(LISTER_GEN) $(INFORMER_GEN) ## Generate deepcopy, clientset, listers, and informers for the API group.
	@# Populate pkg/generated/deepcopy
	$(DEEPCOPY_GEN) \
	-v 0 \
	--output-file zz_generated.deepcopy.go \
	--go-header-file "$(PWD)/hack/boilerplate.go.txt" \
	$(PKG)/pkg/apis/resourcestatemetrics/v1alpha1

	@# Populate pkg/generated/clientset
	$(CLIENT_GEN) \
	-v 0 \
	--go-header-file "$(PWD)/hack/boilerplate.go.txt" \
	--output-dir $(PWD)/pkg/generated/clientset \
	--output-pkg $(PKG)/pkg/generated/clientset \
	--clientset-name versioned \
	--apply-configuration-package '' \
	--input-base $(PKG)/pkg/apis \
	--input resourcestatemetrics/v1alpha1

	@# Populate pkg/generated/listers
	$(LISTER_GEN) \
	-v 0 \
	--go-header-file "$(PWD)/hack/boilerplate.go.txt" \
	--output-dir $(PWD)/pkg/generated/listers \
	--output-pkg $(PKG)/pkg/generated/listers \
	$(PKG)/pkg/apis/resourcestatemetrics/v1alpha1

	@# Populate pkg/generated/informers
	$(INFORMER_GEN) \
	-v 0 \
	--go-header-file "$(PWD)/hack/boilerplate.go.txt" \
	--output-dir $(PWD)/pkg/generated/informers \
	--output-pkg $(PKG)/pkg/generated/informers \
	--versioned-clientset-package $(PKG)/pkg/generated/clientset/versioned \
	--listers-package $(PKG)/pkg/generated/listers \
	$(PKG)/pkg/apis/resourcestatemetrics/v1alpha1

.PHONY: jsonnet_manifests
jsonnet_manifests: $(JSONNET) $(GOJSONTOYAML) $(YQ) manifests ## Generate manifests from jsonnet files.
	@CONTROLLER_GEN_VERSION=$(CONTROLLER_GEN_VERSION) VERSION=$(VERSION) NAMESPACE=$(LOCAL_NAMESPACE) PROJECT_NAME=$(PROJECT_NAME) JSONNET=$(JSONNET) GOJSONTOYAML=$(GOJSONTOYAML) YQ=$(YQ) ./hack/generate-yamls-from-jsonnets.sh

.PHONY: generate
generate: manifests codegen jsonnet_manifests ## Run all code generation targets.

##@ Verifying

.PHONY: verify_codegen
verify_codegen: codegen ## Verify go generated files are up to date
	@if !(git diff --quiet HEAD pkg/generated); then \
		git diff pkg/generated; \
		echo "generated files are out of date, run make codegen"; exit 1; \
	fi

.PHONY: verify_manifests
verify_manifests: jsonnet_manifests ## Verify manifests generated from jsonnet files are up to date
	@(git diff --exit-code $(JSONNET_MANIFESTS_DIR) manifests/ && echo "Manifests are up to date.") || (echo "Manifests are not up to date. Please run 'make jsonnet_manifests' to update them." && exit 1)

.PHONY: verify_generated
verify_generated: verify_codegen verify_manifests ## Verify all generated files are up to date

.PHONY: verify
verify: lint test verify_generated ## Run all verification targets.

##@ Build

.PHONY: image
image: $(PROJECT_NAME) ## Build docker image for the project.
	@docker build -t $(PROJECT_NAME):$(BUILD_TAG) .

$(PROJECT_NAME): $(GO_FILES)
	$(GO) build -a -installsuffix cgo -ldflags "$(LDFLAGS)" -gcflags "$(GCFLAGS)" -o $@

.PHONY: build
build: $(PROJECT_NAME) ## Build the project binary

.PHONY: image-push
image-push: image ## Push docker image to the registry.
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
apply: manifests delete ## Apply manifests to the cluster.
	# Applying manifests
	@$(KUBECTL) apply -f manifests
	# Applied manifests

.PHONY: delete
delete: ## Delete manifests from the cluster.
	# Deleting manifests
	@$(KUBECTL) delete --ignore-not-found -f manifests/
	# Deleted manifests

.PHONY: local
local: apply $(PROJECT_NAME)
	@$(KUBECTL) scale deployment $(PROJECT_NAME) --replicas=0 -n $(LOCAL_NAMESPACE) 2>/dev/null || true
	@./$(PROJECT_NAME) -v=$(V) -kubeconfig $(KUBECONFIG)

##@ Testing

.PHONY: pprof
pprof: ## Run pprof for the project.
	@go tool pprof ":$(PPROF_PORT)" $(PPROF_OPTIONS)

.PHONY: test_unit
test_unit: ## Run unit tests.
	@$(GO) test -v -race $(shell go list ./... | \
		grep -v "/generated" | \
		grep -v "/signals" | \
		grep -v "/tests" | \
		grep -v "/version")

.PHONY: test_e2e
test_e2e: ## Run e2e tests.
	@$(GO) test -v -race ./tests/...

.PHONY: test
test: test_unit test_e2e ## Run all tests.

.PHONY: apply_testdata
apply_testdata: $(YQ) delete_testdata ### Apply testdata to the cluster.
	# Applying testdata
	@$(KUBECTL) apply -R -f tests/manifests/custom-resource-definition
	@$(KUBECTL) apply -R -f tests/manifests/custom-resource
	@$(YQ) '.in' $(GOLDEN_FILES) | $(KUBECTL) apply -f -
	# Applied testdata

.PHONY: delete_testdata
delete_testdata: ## Delete testdata from the cluster.
	# Deleting testdata
	-@$(KUBECTL) delete --ignore-not-found -R -f tests/manifests
	# Deleted testdata

.PHONY: golden_metrics
golden_metrics: $(YQ) $(GOLDEN_FILES)
	@$(YQ) --no-doc '.out.metrics[]' $(GOLDEN_FILES) > $(GOLDEN_METRICS_FILE)

.PHONY: compare_metrics
compare_metrics: golden_metrics ## Compare metrics from the running project with the golden metrics.
	@diff \
		<(sort $(GOLDEN_METRICS_FILE)) \
		<(curl -sf http://localhost:$(MAIN_METRICS_PORT)/metrics | grep -Ff $(GOLDEN_METRICS_FILE) | sort)

##@ Linting

.PHONY: lint
lint: lint_makefile lint_yaml lint_md lint_go lint_jsonnet ## Run all linters.

.PHONY: lint_fix
lint_fix: lint_makefile lint_yaml_fix lint_md_fix lint_go_fix lint_jsonnet_fix ## Run all linters and fix issues where possible.

.PHONY: lint_makefile
lint_makefile: $(CHECKMAKE) ## Lint Makefile.
	@$(CHECKMAKE) Makefile

.PHONY: licensecheck_yaml
licensecheck_yaml: $(YAML_FILES) ## Check license headers in YAML files.
	@./hack/fix-license-headers.sh --check $(YAML_FILES)

.PHONY: licensecheck_yaml_fix
licensecheck_yaml_fix: $(YAML_FILES) ## Fix license headers in YAML files.
	@./hack/fix-license-headers.sh $(YAML_FILES)

.PHONY: lint_yaml
lint_yaml: licensecheck_yaml $(YAMLFMT) ## Lint YAML files.
	@$(YAMLFMT) -dry -quiet . || (echo "YAML files need formatting. Run 'make yamlfmt_fix' to fix." && exit 1)

.PHONY: lint_yaml_fix
lint_yaml_fix: licensecheck_yaml_fix $(YAMLFMT) ## Lint and fix YAML files.
	@$(YAMLFMT) .

.PHONY: lint_md
lint_md: .vale.ini $(MD_FILES) ## Lint Markdown files with Vale.
	@mkdir -p $(VALE_STYLES_DIR) && \
	$(VALE) sync && \
	$(VALE) $(MD_FILES)

.PHONY: format_md
format_md: $(MARKDOWNFMT) $(MD_FILES) ## Format Markdown files with markdownfmt.
	@test -z "$(shell $(MARKDOWNFMT) -l $(MD_FILES))" || (echo "The following files need to be formatted with 'markdownfmt -w -gofmt':" $(shell $(MARKDOWNFMT) -l $(MD_FILES)) "" && exit 1)

.PHONY: format_md_fix
format_md_fix: vale $(MARKDOWNFMT) $(MD_FILES) ## Format Markdown files with markdownfmt and fix issues
	@for file in $(MD_FILES); do $(MARKDOWNFMT) -w -gofmt $$file || exit 1; done

.PHONY: licensecheck_go
licensecheck_go: $(GO_FILES) ## Check license headers in Go files.
	@./hack/fix-license-headers.sh --check $(GO_FILES)

.PHONY: licensecheck_go_fix
licensecheck_go_fix: $(GO_FILES) ## Fix license headers in Go files.
	@./hack/fix-license-headers.sh $(GO_FILES)

.PHONY: lint_go
lint_go: licensecheck_go $(GOLANGCI_LINT) ## Lint Go files.
	@$(GOLANGCI_LINT) run -c $(GOLANGCI_LINT_CONFIG)

.PHONY: lint_go_fix
lint_go_fix: licensecheck_go_fix $(GOLANGCI_LINT) ## Lint and fix Go files.
	@$(GOLANGCI_LINT) run --fix -c $(GOLANGCI_LINT_CONFIG)

.PHONY: licensecheck_jsonnet
licensecheck_jsonnet: $(JSONNET_FILES) ## Check license headers in Jsonnet files.
	@./hack/fix-license-headers.sh --check $(JSONNET_FILES)

.PHONY: licensecheck_jsonnet_fix
licensecheck_jsonnet_fix: $(JSONNET_FILES) ## Fix license headers in Jsonnet files.
	@./hack/fix-license-headers.sh $(JSONNET_FILES)

.PHONY: lint_jsonnet
lint_jsonnet: $(JSONNETFMT) licensecheck_jsonnet ## Lint Jsonnet files.
	@test -z "$(shell $(JSONNETFMT) --test $(JSONNET_FILES) 2>&1)" || (echo "The following jsonnet files need to be formatted with 'jsonnetfmt -i':" && $(JSONNETFMT) --test $(JSONNET_FILES) && exit 1)

.PHONY: lint_jsonnet_fix
lint_jsonnet_fix: $(JSONNETFMT) licensecheck_jsonnet_fix ## Lint and fix Jsonnet files.
	@$(JSONNETFMT) -i $(JSONNET_FILES)

##@ Cleanup

.PHONY: clean
clean: ## Clean up git untracked files and directories.
	@git clean -fxd


# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f $(1) ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv $(1) $(1)-$(3) ;\
} ;\
ln -sf $$(realpath $(1)-$(3)) $(1)
endef
