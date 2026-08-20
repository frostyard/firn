.PHONY: all build clean fmt lint test install help

# Build variables
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(BUILD_TIME) -X main.builtBy=make"

# Go commands
GO := go
GOFMT := gofmt
GOFILES := $(shell find . -type f -name '*.go' -not -path "./vendor/*")

all: fmt build

## build: Build the firn binary
build:
	$(GO) build $(LDFLAGS) -o build/firn ./cmd/firn-cli

## install: Install firn binary to GOPATH/bin
install:
	$(GO) install $(LDFLAGS) ./cmd/firn-cli

## clean: Remove build artifacts
clean:
	rm -rf build/
	$(GO) clean

## fmt: Format Go source files
fmt:
	$(GOFMT) -w $(GOFILES)

lint: ## Run linter
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed, skipping"; \
	fi

## test: Run tests
test:
	$(GO) test -v ./...

## test-cover: Run tests with coverage
test-cover:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html

## tidy: Tidy go modules
tidy:
	$(GO) mod tidy

## check: Run fmt, lint, and test
check: fmt lint test

## help: Show this help message
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | column -t -s ':'

bump: ## generate a new version with svu
	@$(MAKE) build
	@$(MAKE) test
	@$(MAKE) fmt
	$(MAKE) lint
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo "Working directory is not clean. Please commit or stash changes before bumping version."; \
		exit 1; \
	fi
	@echo "Creating new tag..."
	@version=$$(svu next); \
		git tag -a $$version -m "Version $$version"; \
		echo "Tagged version $$version"; \
		echo "Pushing tag $$version to origin..."; \
		git push origin $$version

# ---------------------------------------------------------------------------
# firn-specific: root/KVM E2E harnesses (run outside CI; see the
# nested-vm-e2e skill and ADR-0009 for why some must run nested).

.PHONY: e2e-bootc e2e-bootc-secure e2e-ab e2e-tui

## e2e-bootc: Loop-device bootc install E2E (root, QEMU/OVMF, podman)
e2e-bootc:
	sudo test/e2e-bootc.sh

## e2e-bootc-secure: Nested-VM bootc install under enforced Secure Boot (root, QEMU/secboot-OVMF, virt-fw-vars)
e2e-bootc-secure:
	sudo test/e2e-bootc-secure.sh

## e2e-ab: Nested-VM A/B install E2E (root, QEMU/OVMF, network)
e2e-ab:
	sudo test/e2e-ab.sh

## e2e-tui: Nested-VM TUI E2E (root, QEMU/OVMF; FIRN_E2E_TUI_FAMILY=ab|bootc)
e2e-tui:
	sudo test/e2e-tui.sh
