TARGET_NAMESPACE=pipelines-as-code
GOLANGCI_LINT_VERSION ?= v2.12.2
GO_TOOLCHAIN ?= go1.25.11
GOFUMPT=gofumpt
TKN_BINARY_NAME := tkn
TKN_BINARY_URL := https://tekton.dev/docs/cli/\#installation
LDFLAGS=
OUTPUT_DIR=bin
GO           = go
TIMEOUT_UNIT = 20m
TIMEOUT_E2E  = 45m
GO_TEST_FLAGS +=
GOTOOLCHAIN ?= $(GO_TOOLCHAIN)
export GOTOOLCHAIN
SHELL := bash
TOPDIR := $(shell git rev-parse --show-toplevel)
TMPDIR := $(TOPDIR)/tmp
GOLANGCI_LINT_OS ?= $(shell uname -s | tr '[:upper:]' '[:lower:]')
GOLANGCI_LINT_ARCH ?= $(shell uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')
GOLANGCI_LINT_PACKAGE := golangci-lint-$(patsubst v%,%,$(GOLANGCI_LINT_VERSION))-$(GOLANGCI_LINT_OS)-$(GOLANGCI_LINT_ARCH)
GOLANGCI_LINT_DIR := $(TMPDIR)/golangci-lint/$(GOLANGCI_LINT_VERSION)
GOLANGCI_LINT_BIN := $(GOLANGCI_LINT_DIR)/golangci-lint
GOLANGCI_LINT ?= $(GOLANGCI_LINT_BIN)
GOLANGCI_LINT_EXTRA_ARGS ?= --concurrency=1

PY_FILES := $(shell find . -type f -regex ".*\.py" -print)
SH_FILES := $(shell find hack/ -type f -regex ".*\.sh" -print)
YAML_FILES := $(shell find . -not -regex '^./vendor/.*' -type f -regex ".*y[a]ml" -print)
MD_FILES := $(shell find . -type f -regex ".*md"  -not -regex '^./vendor/.*'  -not -regex '^./.vale/.*'  -not -regex "^./docs/themes/.*" -not -regex "^./.git/.*" -print)

ifeq ($(PAC_VERSION),)
	PAC_VERSION="$(shell git describe --tags --exact-match 2>/dev/null || echo nightly-`date +'%Y%m%d'`-`git rev-parse --short HEAD`)"
endif
FLAGS += -ldflags "-X github.com/openshift-pipelines/pipelines-as-code/pkg/params/version.Version=$(PAC_VERSION) $(LDFLAGS) -X github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings.TknBinaryName=$(TKN_BINARY_NAME) -X github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings.TknBinaryURL=$(TKN_BINARY_URL)"


##@ General
all: allbinaries test lint ## compile all binaries, test and lint
check: lint test ## run lint and test

.PHONY: help
help: ## print this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

FORCE:
.PHONY: vendor
vendor:
	@echo Generating vendor directory
	@go mod tidy -compat=1.25 && go mod vendor

##@ Build
allbinaries: $(OUTPUT_DIR)/pipelines-as-code-controller $(OUTPUT_DIR)/pipelines-as-code-watcher $(OUTPUT_DIR)/tkn-pac ## compile all binaries

$(OUTPUT_DIR)/%: cmd/% FORCE ## compile binaries
	go build -mod=vendor $(FLAGS)  -v -o $@ ./$<

windows: ## compile windows binaries
	env GOOS=windows GOARCH=amd64 go build -mod=vendor $(FLAGS)  -v -o ./bin/tkn-pac.exe ./cmd/tkn-pac/main.go

##@ Testing
TEST_UNIT_TARGETS := test-unit-verbose test-unit-race test-unit-failfast
test-unit-verbose: ARGS=-v
test-unit-failfast: ARGS=-failfast
test-unit-race:    ARGS=-race
$(TEST_UNIT_TARGETS): test-unit
test-clean:  ## Clean testcache
	@echo "Cleaning test cache"
	@go clean -testcache
.PHONY: $(TEST_UNIT_TARGETS) test test-unit
test-no-cache: test-clean test-unit ## Run test-unit without caching
test: test-unit ## Run test-unit
test-unit: ## Run unit tests
	@echo "Running unit tests..."
	@set -o pipefail ; \
		$(GO) test $(GO_TEST_FLAGS) -timeout $(TIMEOUT_UNIT) $(ARGS) ./pkg/... | { grep -v 'no test files'; true; }

.PHONY: test-e2e-cleanup
test-e2e-cleanup: ## cleanup test e2e namespace/pr left open
	@./hack/dev/e2e-tests-cleanup.sh

.PHONY: test-e2e
test-e2e:  test-e2e-cleanup ## run e2e tests
	@go test $(GO_TEST_FLAGS) -timeout $(TIMEOUT_E2E)  -failfast -count=1 -tags=e2e $(GO_TEST_FLAGS) ./test

.PHONY: html-coverage
html-coverage: ## generate html coverage
	@mkdir -p tmp
	@go test -coverprofile=tmp/c.out ./.../ && go tool cover -html=tmp/c.out

##@ Linting
.PHONY: lint
lint: lint-go lint-yaml lint-md lint-python lint-shell ## run all linters

.PHONY: lint-go
lint-go: golangci-lint ## runs go linter on all go files
	@echo "Linting go files..."
	@$(GOLANGCI_LINT) run $(GOLANGCI_LINT_EXTRA_ARGS) ./... --modules-download-mode=vendor \
							--max-issues-per-linter=0 \
							--max-same-issues=0 \
							--timeout $(TIMEOUT_UNIT)

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT_BIN) ## download pinned golangci-lint into tmp

$(GOLANGCI_LINT_BIN):
	@mkdir -p $(GOLANGCI_LINT_DIR)
	@echo "Downloading golangci-lint $(GOLANGCI_LINT_VERSION) for $(GOLANGCI_LINT_OS)-$(GOLANGCI_LINT_ARCH)"
	@curl -fsSL "https://github.com/golangci/golangci-lint/releases/download/$(GOLANGCI_LINT_VERSION)/$(GOLANGCI_LINT_PACKAGE).tar.gz" | tar -xz -C "$(GOLANGCI_LINT_DIR)" --strip-components=1 "$(GOLANGCI_LINT_PACKAGE)/golangci-lint"

.PHONY: lint-yaml
lint-yaml: ${YAML_FILES} ## runs yamllint on all yaml files
	@echo "Linting yaml files..."
	@yamllint -c .yamllint $(YAML_FILES)


.PHONY: lint-md
lint-md: ## runs markdownlint and vale on all markdown files
	@echo "Linting markdown files..."
	@markdownlint $(MD_FILES)
	@echo "Grammar check with vale of documentation..."
	@vale docs/content *.md --minAlertLevel=error --output=line
	@echo "CodeSpell on docs content"
	@codespell docs/content

.PHONY: lint-python
lint-python: ${PY_FILES} ## runs pylint on all python files
	@echo "Linting python files..."
	@ruff check $(PY_FILES)
	@ruff format --check $(PY_FILES)

.PHONY: lint-shell
lint-shell: ${SH_FILES} ## runs shellcheck on all python files
	@echo "Linting shell script files..."
	@shellcheck $(SH_FILES)

.PHONY: pre-commit
pre-commit: ## Run pre-commit hooks script manually
	@pre-commit run --all-files

##@ Linters Fixing
.PHONY: fix-linters
fix-linters: fix-golangci-lint fix-python-errors fix-markdownlint fix-trailing-spaces fumpt ## run all linters fixes

.PHONY: fix-markdownlint
fix-markdownlint: ## run markdownlint and fix on all markdown file
	@echo "Fixing markdown files..."
	@markdownlint --fix $(MD_FILES)

.PHONY: fix-trailing-spaces
fix-trailing-spaces: ## remove trailing spaces on all markdown and yaml file
	@sed --in-place 's/[[:space:]]\+$$//' $(MD_FILES) $(YAML_FILES)
	@[[ -n `git status --porcelain $(MD_FILES) $(YAML_FILES)` ]] && { echo "Markdowns and Yaml files has been cleaned 🧹. Cleaned Files: ";git status --porcelain $(MD_FILES) $(YAML_FILES) ;} || echo "Markdown and YAML are clean ✨"

.PHONE: fix-python-errors
fix-python-errors: ## fix all python errors generated by ruff
	@echo "Fixing python files..."
	@ruff check --fix $(PY_FILES)
	@ruff format --check $(PY_FILES)
	@[[ -n `git status --porcelain $(PY_FILES)` ]] && { echo "Python files has been cleaned 🧹. Cleaned Files: ";git status --porcelain $(PY_FILES) ;} || echo "Python files are clean ✨"

.PHONY: fix-golangci-lint
fix-golangci-lint: ## run golangci-lint and fix on all go files
	@echo "Fixing some golangi-lint files..."
	@$(GOLANGCI_LINT) run ./... --modules-download-mode=vendor \
							--max-issues-per-linter=0 \
							--max-same-issues=0 \
							--timeout $(TIMEOUT_UNIT) \
							--fix
	@[[ -n `git status --porcelain` ]] && { echo "Go files has been cleaned 🧹. Cleaned Files: ";git status --porcelain ;} || echo "Go files are clean ✨"

.PHONY: fmt ## formats the GO code(excludes vendors dir)
fmt:
	@go fmt `go list ./... | grep -v /vendor/`

.PHONY: fumpt ## formats the GO code with gofumpt(excludes vendors dir)
fumpt:
	@find test pkg -name '*.go'|xargs -P4 $(GOFUMPT) -w -extra

##@ Local Development
.PHONY: dev
dev: ## deploys dev setup locally
	./hack/dev/kind/install.sh

.PHONY: dev-redeploy
dev-redeploy: ## redeploy pac in local setup
	./hack/dev/kind/install.sh -p

.PHONY: dev-docs
dev-docs: ## preview live your docs with hugo
	@hugo server -s docs/ &
	if type -p xdg-open 2>/dev/null >/dev/null; then \
		xdg-open http://localhost:1313; \
	elif type -p open 2>/dev/null >/dev/null; then \
		open http://localhost:1313; \
	fi

##@ Generated files
check-generated: # check if all files that needs to be generated are generated
	@git status -uno |grep -E "modified:[ ]*(vendor/|.*.golden$)" && \
		{ echo "Vendor directory or Golden files has not been generated properly, commit the change first" ; \
		  git status -uno ;	exit 1 ;} || true

.PHONY: update-golden
update-golden: ## run unit tests (updating golden files)
	@echo "Running unit tests to update golden files..."
	@./hack/update-golden.sh

.PHONY: generated
generated: update-golden fumpt ## generate all files that needs to be generated


.PHONY: clean
clean: ## clean build artifacts
	rm -fR bin
