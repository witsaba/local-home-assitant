# messaging-core developer targets.
#
# Run `make` (or `make help`) for the list of targets. All targets
# operate on the current module — invoke from this directory.

# ---------- Configuration ------------------------------------------------

BINARY_NAME        := messaging-core
MAIN_PKG           := ./cmd/messaging-core
PKG                := ./...
COVERAGE_FILE      := coverage.out
COVERAGE_HTML      := coverage.html
BIN_DIR            := bin

GO                 ?= go
GOLANGCI_LINT      ?= golangci-lint
GIT_HEAD           := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
VERSION            ?= 0.1.0-dev
LDFLAGS            := -X main.version=$(VERSION)-$(GIT_HEAD)

# ---------- Help (default target) ----------------------------------------

.DEFAULT_GOAL := help

.PHONY: help
help: ## show this help
	@printf "Usage:\n  make <target>\n\nTargets:\n"
	@grep -E '^[a-zA-Z_-]+:.*?##' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*## "}; {printf "  %-18s %s\n", $$1, $$2}'

# ---------- Build ---------------------------------------------------------

.PHONY: build
build: ## compile the binary into bin/messaging-core
	@mkdir -p $(BIN_DIR)
	$(GO) build -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY_NAME) $(MAIN_PKG)

.PHONY: run
run: build ## run the binary (uses config from env vars)
	./$(BIN_DIR)/$(BINARY_NAME)

.PHONY: install
install: ## install the binary into $GOBIN (or $HOME/go/bin)
	$(GO) install -ldflags '$(LDFLAGS)' $(MAIN_PKG)

# ---------- Quality gates -------------------------------------------------

.PHONY: test
test: ## run all tests with -race
	$(GO) test -race -count=1 $(PKG)

.PHONY: cover
cover: ## run tests and produce an HTML coverage report
	$(GO) test -count=1 -coverprofile=$(COVERAGE_FILE) $(PKG)
	$(GO) tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)
	@echo "wrote $(COVERAGE_HTML) (open in a browser)"

.PHONY: vet
vet: ## run go vet
	$(GO) vet $(PKG)

.PHONY: fmt
fmt: ## run gofmt -s -w on the whole module
	$(GO) fmt $(PKG)

.PHONY: lint
lint: ## run golangci-lint (skips with a hint if not installed)
	@command -v $(GOLANGCI_LINT) >/dev/null 2>&1 || { \
		echo "golangci-lint not found"; \
		echo ""; \
		echo "Install one of:"; \
		echo "  brew install golangci-lint"; \
		echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"; \
		echo ""; \
		echo "After installation re-run 'make lint'."; \
		exit 1; \
	}
	$(GOLANGCI_LINT) run

.PHONY: tidy
tidy: ## sync go.mod and go.sum
	$(GO) mod tidy

.PHONY: mod
mod: ## show go module graph
	$(GO) mod graph

.PHONY: verify
verify: ## verify module contents
	$(GO) mod verify

# ---------- Housekeeping --------------------------------------------------

.PHONY: clean
clean: ## remove build artifacts and coverage files
	rm -rf $(BIN_DIR) $(COVERAGE_FILE) $(COVERAGE_HTML)

.PHONY: clean-testcache
clean-testcache: ## drop the Go test cache
	$(GO) clean -testcache
