TARGET_NAMESPACE=pipelines-as-code
HUGO_VERSION=0.146.0
HTMLTEST_VERSION=0.17.0
GOLANGCI_LINT=golangci-lint
GOFUMPT=gofumpt
TKN_BINARY_NAME := tkn
TKN_BINARY_URL := https://tekton.dev/docs/cli/\#installation
LDFLAGS=
OUTPUT_DIR=bin
GO           = go
TIMEOUT_UNIT = 20m
TIMEOUT_E2E  = 45m
DEFAULT_GO_TEST_FLAGS := -race -failfast
GO_TEST_FLAGS :=

SHELL := bash
TOPDIR := $(shell git rev-parse --show-toplevel)
TMPDIR := $(TOPDIR)/tmp
HUGO_BIN := $(TMPDIR)/hugo/hugo
HTMLTEST_BIN := $(TMPDIR)/htmltest/htmltest

# Safe file list helpers using null-delimited output
# Usage: $(call GIT_LS_FILES,<patterns>,<command>)
GIT_LS_FILES = git ls-files -z -- $(1) | xargs -0 -r $(2)
PY_PATTERNS := '*.py' ':!vendor/*'
SH_PATTERNS := 'hack/*.sh' ':!vendor/*'
YAML_PATTERNS := '*.yml' '*.yaml' ':!.vale/*' ':!docs/themes/*' ':!vendor/*' ':!.claude/skills/*'
MD_PATTERNS := '*.md' ':!docs/themes/*' ':!.vale/*' ':!vendor/*' ':!.claude/skills/*'
MD_YAML_PATTERNS := '*.md' '*.yml' '*.yaml' ':!docs/themes/*' ':!.vale/*' ':!vendor/*' ':!tmp/*' ':!.claude/skills/*'

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
vendor: ## generate vendor directory
	@echo Generating vendor directory
	@go mod tidy -compat=1.17 && go mod vendor

##@ Build
allbinaries: $(OUTPUT_DIR)/pipelines-as-code-controller $(OUTPUT_DIR)/pipelines-as-code-watcher $(OUTPUT_DIR)/tkn-pac ## compile all binaries

$(OUTPUT_DIR)/%: cmd/% FORCE ## compile binaries
	go build -mod=vendor $(FLAGS)  -v -o $@ ./$<

windows: ## compile windows binaries
	env GOOS=windows GOARCH=amd64 go build -mod=vendor $(FLAGS)  -v -o ./bin/tkn-pac.exe ./cmd/tkn-pac/main.go

##@ Testing
test: test-unit ## Run test-unit
test-clean:  ## Clean testcache
	@echo "Cleaning test cache"
	@go clean -testcache
.PHONY: test test-unit
test-no-cache: test-clean test-unit ## Run test-unit without caching
test-unit: ## Run unit tests
	@mkdir -p tmp/
	@echo "Running unit tests..."
	$(GO) test $(DEFAULT_GO_TEST_FLAGS) $(GO_TEST_FLAGS) -timeout $(TIMEOUT_UNIT) ./pkg/...

.PHONY: test-e2e-cleanup
test-e2e-cleanup: ## cleanup test e2e namespace/pr left open
	@./hack/dev/e2e-tests-cleanup.sh

.PHONY: test-e2e
test-e2e:  ## run e2e tests
	env GODEBUG=asynctimerchan=1 \
		$(GO) test $(DEFAULT_GO_TEST_FLAGS) $(GO_TEST_FLAGS) -timeout $(TIMEOUT_E2E)  -failfast -count=1 -tags=e2e ./test

.PHONY: html-coverage
html-coverage: ## generate html coverage
	@mkdir -p tmp
	@go test -coverprofile=tmp/c.out ./pkg/... ./cmd/... && go tool cover -html=tmp/c.out

##@ Linting
.PHONY: lint
lint: lint-go lint-yaml lint-md lint-python lint-shell lint-e2e-naming ## run all linters

.PHONY: lint-e2e-naming
lint-e2e-naming: ## check e2e test naming conventions
	@echo "Checking E2E test naming conventions..."
	@./hack/check-e2e-test-naming.sh

.PHONY: lint-go
lint-go: ## runs go linter on all go files
	@echo "Linting go files..."
	@$(GOLANGCI_LINT) run ./pkg/... ./test/... --modules-download-mode=vendor \
							--max-issues-per-linter=0 \
							--max-same-issues=0 \
							--timeout $(TIMEOUT_UNIT)

.PHONY: lint-yaml
lint-yaml: ## runs yamllint on all yaml files
	@echo "Linting yaml files..."
	@$(call GIT_LS_FILES,$(YAML_PATTERNS),yamllint -c .yamllint) || true


.PHONY: lint-md
lint-md: ## runs markdownlint and vale on all markdown files
	@echo "Linting markdown files..."
	@$(call GIT_LS_FILES,$(MD_PATTERNS),markdownlint) || true
	@echo "Grammar check with vale of documentation..."
	@vale docs/content --output=line --glob='*.md'
	@echo "CodeSpell on docs content"
	@codespell docs/content

.PHONY: lint-python
lint-python: ## runs pylint on all python files
	@echo "Linting python files..."
	@$(call GIT_LS_FILES,$(PY_PATTERNS),ruff check) || true
	@$(call GIT_LS_FILES,$(PY_PATTERNS),ruff format --check) || true

.PHONY: lint-shell
lint-shell: ## runs shellcheck on all shell files
	@echo "Linting shell script files..."
	@$(call GIT_LS_FILES,$(SH_PATTERNS),shellcheck) || true

.PHONY: gitlint
gitlint: ## Run gitlint
	@gitlint --commit "`git log --format=format:%H --no-merges -1`" --ignore "Merge branch"

.PHONY: pre-commit
pre-commit: ## Run pre-commit hooks script manually
	@pre-commit run --all-files

##@ Linters Fixing
.PHONY: fix-linters
fix-linters: fix-golangci-lint fix-python-errors fix-markdownlint fix-trailing-spaces fumpt fix-links ## fix all linters issues we can automatically fix

.PHONY: fix-markdownlint
fix-markdownlint: ## run markdownlint and fix on all markdown file
	@echo "Fixing markdown files..."
	@$(call GIT_LS_FILES,$(MD_PATTERNS),markdownlint --fix) || true

.PHONY: fix-trailing-spaces
fix-trailing-spaces: ## remove trailing spaces on all markdown and yaml file
	@$(call GIT_LS_FILES,$(MD_YAML_PATTERNS),sed --in-place 's/[[:space:]]\+$$//' --) || true
	@STATUS=$$(git status --porcelain -- $$(git ls-files -- $(MD_YAML_PATTERNS))) && \
	[[ -n "$$STATUS" ]] && { echo "Markdowns and Yaml files has been cleaned 🧹. Cleaned Files: "; echo "$$STATUS" ;} || echo "Markdown and YAML are clean ✨"

.PHONY: fix-python-errors
fix-python-errors: ## fix all python errors generated by ruff
	@echo "Fixing python files..."
	@$(call GIT_LS_FILES,$(PY_PATTERNS),ruff check --fix) || true
	@$(call GIT_LS_FILES,$(PY_PATTERNS),ruff format) || true
	@STATUS=$$(git status --porcelain -- $$(git ls-files -- $(PY_PATTERNS))) && \
	[[ -n "$$STATUS" ]] && { echo "Python files has been cleaned 🧹. Cleaned Files: "; echo "$$STATUS" ;} || echo "Python files are clean ✨"

.PHONY: fix-golangci-lint
fix-golangci-lint: ## run golangci-lint and fix on all go files
	@echo "Fixing some golangi-lint files..."
	@$(GOLANGCI_LINT) run ./... --modules-download-mode=vendor \
							--max-issues-per-linter=0 \
							--max-same-issues=0 \
							--timeout $(TIMEOUT_UNIT) \
							--fix
	@[[ -n `git status --porcelain` ]] && { echo "Go files has been cleaned 🧹. Cleaned Files: ";git status --porcelain ;} || echo "Go files are clean ✨"

.PHONY: fmt 
fmt: ## formats the GO code(excludes vendors dir)
	@go fmt `go list ./... | grep -v /vendor/`

.PHONY: fumpt 
fumpt: ## formats the GO code with gofumpt(excludes vendors dir)
	@find test pkg -name '*.go'|xargs -P4 $(GOFUMPT) -w -extra

##@ Generated files
check-generated: # check if all files that needs to be generated are generated
	@git status -uno |grep -E "modified:[ ]*(vendor/|.*\.golden$$)" && \
		{ echo "Vendor directory or Golden files has not been generated properly, commit the change first" ; \
		  git status -uno ;	exit 1 ;} || true

.PHONY: update-golden
update-golden: ## run unit tests (updating golden files)
	@echo "Running unit tests to update golden files..."
	@./hack/update-golden.sh

.PHONY: update-schemas
update-schemas: ## update openapi schemas
	@echo "Running update schemas..."
	@./hack/update-schemas.sh

.PHONY: generated
generated: update-golden fumpt ## generate all files that needs to be generated

##@ Docs

.PHONY: download-hugo
download-hugo: ## Download hugo software
	./hack/download-hugo.sh $(HUGO_VERSION) $(TMPDIR)/hugo

.PHONY: download-htmltest
download-htmltest: ## Download htmltest binary
	./hack/download-htmltest.sh $(HTMLTEST_VERSION) $(TMPDIR)/htmltest

.PHONY: check-links
check-links: ## Check documentation links (set CHECK_EXTERNAL=false to skip external URLs)
	@echo "Checking README links..."
	@grep -oP 'https://[^\s)>]+' README.md | xargs -I{} -P5 sh -c 'code=$$(curl -s -o /dev/null -w "%{http_code}" -L --max-time 5 "{}"); printf "%s  %s\n" "$$code" "{}"; case "$$code" in 2*) exit 0;; *) exit 1;; esac'
	@echo "Checking documentation links..."
	./hack/check-links.sh

.PHONY: fix-links
fix-links: ## Auto-fix bare relative links to use relref shortcode
	./hack/check-links.sh --fix

.PHONY: dev-docs
dev-docs: download-hugo ## preview live your docs with hugo
	@$(HUGO_BIN) server -s docs/ &
	@echo "Sleeping for 2s....";sleep 2 ; \
	if type -p xdg-open 2>/dev/null >/dev/null; then \
		xdg-open http://localhost:1313; \
	elif type -p open 2>/dev/null >/dev/null; then \
		open http://localhost:1313; \
	fi

##@ Misc

.PHONY: clean
clean: ## clean build artifacts
	rm -fR bin
