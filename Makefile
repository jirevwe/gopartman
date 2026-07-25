# gopartman Makefile

# Build variables
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Go parameters
GOCMD  := go
GOTEST := $(GOCMD) test
GOMOD  := $(GOCMD) mod
GOLINT := golangci-lint

.PHONY: all test test-coverage test-short test-integration \
        lint lint-fix generate sqlc-generate sqlc-verify \
        betteralign-check betteralign-apply \
        tidy deps clean help

# Default target
all: lint test

## Test targets

test: ## Run all tests with race detection
	@echo "Running tests..."
	$(GOTEST) -race -timeout 210s ./...

test-coverage: ## Run tests with coverage report
	@echo "Running tests with coverage..."
	$(GOTEST) -race -coverprofile=coverage.out -covermode=atomic ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

test-short: ## Run short tests only
	$(GOTEST) -short ./...

test-integration: ## Run integration tests (build tag: integration)
	@echo "Running integration tests..."
	$(GOTEST) -tags=integration ./...

## Lint targets

lint: betteralign-check ## Run linting + struct alignment audit
	@echo "Running linter..."
	$(GOLINT) run ./...

lint-fix: ## Run linting with auto-fix
	$(GOLINT) run --fix ./...

## Code generation

generate: ## Run go generate
	@echo "Running go generate..."
	$(GOCMD) generate ./...

sqlc-generate: ## Regenerate internal/{parents,tenants,partitions}/repo from queries.sql
	@echo "Generating sqlc bindings..."
	sqlc generate

sqlc-verify: sqlc-generate ## Fail if generated sqlc code is out of sync with .sql sources
	@echo "Verifying sqlc-generated code is in sync..."
	@git diff --exit-code internal/parents/repo/ internal/tenants/repo/ internal/partitions/repo/ \
		|| (echo "ERROR: sqlc-generated code is out of date — run 'make sqlc-generate' and commit" && exit 1)

## Struct field-alignment audit. Excludes sqlc-generated code
## under internal/{parents,tenants,partitions}/repo/.
## betteralign is provisioned by mise — run `mise install` first.
BETTERALIGN_EXCLUDE := (internal/parents/repo|internal/tenants/repo|internal/partitions/repo)/

betteralign-check: ## Fail if any struct field-alignment issues exist
	@echo "Auditing struct field alignment..."
	@betteralign -test_files ./... 2>&1 | grep -v -E "$(BETTERALIGN_EXCLUDE)" \
		| (! grep -q "fieldalignment\|struct of size") \
		|| (echo "ERROR: struct field-alignment issues found — run 'make betteralign-apply' and review the diff" && exit 1)

betteralign-apply: ## Auto-rewrite struct fields to size-minimal order
	@echo "Applying betteralign auto-fix..."
	betteralign -apply -test_files ./...
	@echo "Done. Review the diff with 'git diff', run 'make test', then commit."

## Module management

tidy: ## Tidy go modules
	$(GOMOD) tidy

deps: ## Download dependencies
	$(GOMOD) download

## Cleanup

clean: ## Remove build artifacts
	@echo "Cleaning..."
	rm -f coverage.out coverage.html

## Help

help: ## Show this help
	@echo "gopartman Makefile"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-22s %s\n", $$1, $$2}'
