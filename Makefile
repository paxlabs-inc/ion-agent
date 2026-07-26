# Ion Makefile
# The definitive build system for the Ion agent

SHELL := /bin/bash
.DEFAULT_GOAL := help

# === Version Info ===
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

# === Paths ===
BIN_DIR     := bin
BINARY      := $(BIN_DIR)/ion
HNSW_SERVICE := hnsw-service/target/release/ion-hnsw

# === Go ===
GO      := go
GOFLAGS := -trimpath
LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.buildTime=$(BUILD_TIME)

# === Rust ===
CARGO := cargo

# === Node ===
NPM := npm

# === Local operator startup ===
ION_DATA_DIR     ?= $(HOME)/.ion
ION_WEB_LISTEN   ?= 127.0.0.1:4174
ION_WEB_FLAGS    ?=
ION_TUI_FLAGS    ?=
ION_DEV_FILE_KEK ?= auto

# Reuse an explicitly initialized development vault without weakening
# production-backed data directories. Set ION_DEV_FILE_KEK=1 or 0 to
# override the automatic detection.
ION_DEV_FILE_KEK_FLAG = $(if $(filter 1 true yes,$(ION_DEV_FILE_KEK)),--dev-file-kek,$(if $(and $(filter auto,$(ION_DEV_FILE_KEK)),$(wildcard $(ION_DATA_DIR)/development.kek)),--dev-file-kek,))

# ==============================================================================
# Build Targets
# ==============================================================================

.PHONY: build
build: build-operator build-go ## Build the complete operator release
	@echo "OK: Operator release build complete"

.PHONY: build-operator
build-operator: ## Build deterministic embedded web and TUI artifacts
	@cd ui && $(NPM) ci --ignore-scripts
	@cd ui && $(NPM) run check:generated
	@cd ui && $(NPM) run build
	@cd ui && $(NPM) run check:budgets
	@$(GO) run ./cmd/operator-docs docs/operator.md

.PHONY: build-all
build-all: build-operator build-go build-hnsw ## Build all release components
	@echo "OK: Full build complete"

.PHONY: build-go
build-go: ## Build the Go binary
	@echo "Building ion..."
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/ion
	@echo "OK: Go binary: $(BINARY)"

.PHONY: build-hnsw
build-hnsw: ## Build the HNSW microservice (Rust)
	@test -f hnsw-service/Cargo.toml || (echo "ERROR: HNSW service belongs to Wave 6 and is not implemented" && exit 1)
	@echo "Building HNSW service..."
	@cd hnsw-service && $(CARGO) build --release
	@echo "OK: HNSW service: $(HNSW_SERVICE)"

# ==============================================================================
# Test Targets
# ==============================================================================

.PHONY: test
test: test-unit ## Run all tests (alias for test-unit)

.PHONY: test-unit
test-unit: ## Run unit tests with race detector
	@echo "Running unit tests..."
	CGO_ENABLED=1 $(GO) test -race -count=1 -timeout=300s ./...
	@echo "OK: Unit tests passed"

.PHONY: test-cover
test-cover: ## Run unit tests with coverage report
	@echo "Running tests with coverage..."
	CGO_ENABLED=1 $(GO) test -race -count=1 -timeout=300s -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -func=coverage.out
	@echo "OK: Coverage report: coverage.out"

.PHONY: test-integration
test-integration: ## Run integration tests
	@echo "Running integration tests..."
	@if compgen -G "tests/integration/*_test.go" >/dev/null; then \
		CGO_ENABLED=1 $(GO) test -race -count=1 -timeout=600s -tags=integration ./tests/integration/...; \
	else \
		echo "No integration tests in the current wave"; \
	fi
	@echo "OK: Integration tests passed"

.PHONY: test-adversarial
test-adversarial: ## Run adversarial security tests
	@echo "Running adversarial tests..."
	@if compgen -G "tests/adversarial/*_test.go" >/dev/null; then \
		CGO_ENABLED=1 $(GO) test -race -count=1 -timeout=600s -tags=adversarial ./tests/adversarial/...; \
	else \
		echo "No adversarial tests in the current wave"; \
	fi
	@echo "OK: Adversarial tests passed"

.PHONY: test-perf
test-perf: ## Run performance tests with budget assertions
	@echo "Running performance tests..."
	@if compgen -G "tests/performance/*_test.go" >/dev/null; then \
		CGO_ENABLED=1 $(GO) test -race -count=1 -timeout=600s -tags=performance -bench=. ./tests/performance/...; \
	else \
		echo "No performance tests in the current wave"; \
	fi
	@echo "OK: Performance tests passed"

.PHONY: test-chaos
test-chaos: ## Run chaos engineering tests
	@echo "Running chaos tests..."
	@if compgen -G "tests/chaos/*_test.go" >/dev/null; then \
		CGO_ENABLED=1 $(GO) test -race -count=1 -timeout=600s -tags=chaos ./tests/chaos/...; \
	else \
		echo "No chaos tests in the current wave"; \
	fi
	@echo "OK: Chaos tests passed"

.PHONY: test-all
test-all: test-unit test-integration test-adversarial test-perf ## Run all test suites
	@echo "OK: All tests passed"

.PHONY: test-ui-shared
test-ui-shared: ## Verify generated control-plane contract and shared TypeScript
	@echo "Testing shared UI protocol..."
	@cd ui && $(NPM) ci --ignore-scripts
	@cd ui && $(NPM) run check:generated
	@cd ui && $(NPM) run typecheck
	@echo "OK: Shared UI protocol passed"

.PHONY: test-operator
test-operator: ## Run shared, web, TUI, browser, accessibility, and budget gates
	@cd ui && $(NPM) ci --ignore-scripts
	@cd ui && $(NPM) run check:generated
	@cd ui && $(NPM) run lint
	@cd ui && $(NPM) run test
	@cd ui && $(NPM) run build
	@cd ui && $(NPM) run check:budgets
	@cd ui && $(NPM) run test:e2e --workspace=@matrixmcl/ion-web
	@echo "OK: Operator clients passed"

.PHONY: test-operator-clean-install
test-operator-clean-install: build ## Run both packaged clients and restart the daemon
	@bash tests/operator/clean-install.sh ./$(BINARY)

# ==============================================================================
# Quality Targets
# ==============================================================================

.PHONY: lint
lint: lint-go lint-rust lint-ts ## Run all linters

.PHONY: lint-go
lint-go: ## Run Go linter
	@echo "Linting Go code..."
	@golangci-lint run ./...
	@echo "OK: Go lint passed"

.PHONY: lint-rust
lint-rust: ## Run Rust linter (clippy)
	@if test -f hnsw-service/Cargo.toml; then \
		echo "Linting Rust code..."; \
		cd hnsw-service && $(CARGO) clippy -- -D warnings; \
		echo "OK: Rust lint passed"; \
	else \
		echo "No Rust code in the current wave"; \
	fi

.PHONY: lint-ts
lint-ts: ## Run TypeScript type and lint checks
	@cd ui && $(NPM) run lint
	@echo "OK: TypeScript lint passed"

.PHONY: vet
vet: ## Run Go vet
	@echo "Running go vet..."
	$(GO) vet ./...
	@echo "OK: Go vet passed"

.PHONY: verify-deps
verify-deps: ## Verify checksummed Go and Rust lockfile dependencies
	$(GO) mod verify
	@test -f hnsw-service/Cargo.lock
	@cd hnsw-service && $(CARGO) metadata --locked --format-version 1 >/dev/null
	@echo "OK: Dependency checksums and lockfiles verified"

.PHONY: fmt
fmt: ## Format Go code
	@echo "Formatting Go code..."
	$(GO) fmt ./...
	@echo "OK: Formatted"

.PHONY: tidy
tidy: ## Run go mod tidy
	$(GO) mod tidy
	@echo "OK: Tidied"

# ==============================================================================
# Development Targets
# ==============================================================================

.PHONY: run
run: build ## Build and run the web operator
	./$(BINARY) dashboard --data-dir "$(ION_DATA_DIR)" $(ION_DEV_FILE_KEK_FLAG) --listen "$(ION_WEB_LISTEN)" $(ION_WEB_FLAGS)

.PHONY: start-web
start-web: build ## Build and serve the complete web operator
	@echo "Starting Ion web operator at http://$(ION_WEB_LISTEN)"
	./$(BINARY) dashboard --data-dir "$(ION_DATA_DIR)" $(ION_DEV_FILE_KEK_FLAG) --listen "$(ION_WEB_LISTEN)" $(ION_WEB_FLAGS)

.PHONY: start-tui
start-tui: build ## Build and run the complete terminal operator
	@echo "Starting Ion terminal operator"
	./$(BINARY) tui --data-dir "$(ION_DATA_DIR)" $(ION_DEV_FILE_KEK_FLAG) $(ION_TUI_FLAGS)

.PHONY: dev
dev: ## Run in development mode (auto-rebuild)
	@echo "Starting development mode..."
	@air -c .air.toml

.PHONY: init
init: build-go ## Initialize Ion data directory
	./$(BINARY) init

# ==============================================================================
# Documentation Targets
# ==============================================================================

.PHONY: docs
docs: docs-api docs-operator ## Generate all documentation

.PHONY: docs-operator
docs-operator: ## Generate operator docs from the implemented catalogs
	@$(GO) run ./cmd/operator-docs docs/operator.md

.PHONY: docs-api
docs-api: ## Generate API documentation
	@echo "Generating API docs..."
	@$(GO) doc -all ./... > docs/api/godoc.txt 2>&1 || true
	@echo "OK: API docs: docs/api/godoc.txt"

# ==============================================================================
# Spec Targets
# ==============================================================================

.PHONY: spec-validate
spec-validate: ## Validate spec.kvx schema
	@echo "Validating spec.kvx..."
	@test -f spec/ion_spec/spec.kvx || (echo "ERROR: spec/ion_spec/spec.kvx not found" && exit 1)
	@grep -q '\[meta\]' spec/ion_spec/spec.kvx || (echo "ERROR: [meta] section missing" && exit 1)
	@grep -q '\[req\.' spec/ion_spec/spec.kvx || (echo "ERROR: [req.*] section missing" && exit 1)
	@grep -q '\[task\.' spec/ion_spec/spec.kvx || (echo "ERROR: [task.*] section missing" && exit 1)
	@go run ./cmd/spec-validate spec/ion_spec/spec.kvx
	@echo "OK: spec.kvx valid"

# ==============================================================================
# Cleanup Targets
# ==============================================================================

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BIN_DIR) coverage.out
	cd hnsw-service && $(CARGO) clean 2>/dev/null || true
	cd ui && rm -rf node_modules shared/node_modules web/node_modules tui/node_modules 2>/dev/null || true
	@echo "OK: Cleaned"

.PHONY: clean-all
clean-all: clean ## Remove all generated files (including data)
	rm -rf .ion/
	@echo "OK: All cleaned"

# ==============================================================================
# CI Targets
# ==============================================================================

.PHONY: ci
ci: fmt vet lint test-cover test-operator spec-validate build ## Full CI pipeline
	@echo "OK: CI pipeline passed"

# ==============================================================================
# Help
# ==============================================================================

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
