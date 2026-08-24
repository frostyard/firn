.PHONY: all build clean fmt lint lint-version-check verify ci test install help

# Build variables
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(BUILD_TIME) -X main.builtBy=make"

# Go commands
GO := go
GOFMT := gofmt
# Pinned golangci-lint release, read from mise.toml — the single source of
# every tool pin (core ADR-0043): `mise install` provisions it locally and
# in CI (jdx/mise-action), verified against mise.lock. Bump it there in a
# dedicated commit; never edit this line.
GOLANGCI_LINT_VERSION := $(strip $(shell sed -n 's/^golangci-lint = "\(.*\)"/\1/p' mise.toml))
# The Go release this module is built with, from go.mod's toolchain line —
# the only Go pin (mise reads the same line). golangci-lint must be built
# with a Go at least this new, or its embedded gofmt and typechecker
# disagree with the toolchain.
GO_TOOLCHAIN := $(strip $(shell sed -n 's/^toolchain go\(.*\)/\1/p' go.mod))
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

## lint: Run linter (fails naming `mise install` when golangci-lint is absent)
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint $(GOLANGCI_LINT_VERSION) not installed; provision every pinned tool with:"; \
		echo "mise install"; \
		exit 1; \
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

## lint-version-check: Fail unless the installed golangci-lint is the mise.toml pin and was built with a Go no older than go.mod's toolchain
lint-version-check:
	@test -n "$(GOLANGCI_LINT_VERSION)" || { echo "mise.toml pins no golangci-lint"; exit 1; }
	@installed="$$(golangci-lint version --short 2>/dev/null)" || { echo "golangci-lint $(GOLANGCI_LINT_VERSION) is required (not installed; run: mise install)"; exit 1; }; \
	if [ "$$installed" != "$(GOLANGCI_LINT_VERSION)" ]; then echo "expected golangci-lint $(GOLANGCI_LINT_VERSION), found $$installed (run: mise install)"; exit 1; fi; \
	built="$$(golangci-lint version 2>/dev/null | sed -n 's/.*built with go\([0-9.]*\).*/\1/p')"; \
	if [ -n "$$built" ] && [ "$$(printf '%s\n%s\n' "$(GO_TOOLCHAIN)" "$$built" | sort -V | head -1)" != "$(GO_TOOLCHAIN)" ]; then \
		echo "golangci-lint $(GOLANGCI_LINT_VERSION) was built with go$$built, older than go.mod's toolchain go$(GO_TOOLCHAIN): bump golangci-lint first (core ADR-0043)"; exit 1; fi

## verify: Credential-free, non-mutating gate (what a read-only reviewer runs): tidy diff, gofmt -l, lint at the exact pin, vet, tests
verify:
	@echo "==> verify: go.mod is tidy"
	$(GO) mod tidy -diff
	@echo "==> verify: gofmt"
	@unformatted="$$($(GOFMT) -l $(GOFILES))"; \
	if [ -n "$$unformatted" ]; then echo "$$unformatted"; echo "gofmt: files need formatting (run make fmt)"; exit 1; fi
	@echo "==> verify: golangci-lint $(GOLANGCI_LINT_VERSION) (built with go >= $(GO_TOOLCHAIN))"
	@$(MAKE) --no-print-directory lint-version-check
	@$(MAKE) --no-print-directory lint
	@echo "==> verify: go vet"
	$(GO) vet ./...
	@echo "==> verify: tests"
	$(GO) test ./...

## check: Run fmt, lint, and test
check: fmt lint test

## ci: Credential-free gate mirroring CI's jobs (core ADR-0043/ADR-0022): verify, plus coverage, the race detector, and cross-arch builds
ci:
	@$(MAKE) --no-print-directory verify
	@echo "==> ci: coverage"
	$(GO) test -coverprofile=coverage.out ./...
	@echo "==> ci: race detector"
	$(GO) test -race -short ./internal/...
	@echo "==> ci: cross-arch build"
	@for goarch in amd64 arm64; do \
		echo "GOOS=linux GOARCH=$$goarch make build"; \
		GOOS=linux GOARCH=$$goarch $(MAKE) --no-print-directory build || exit 1; \
	done
	@echo "==> ci passed"

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
