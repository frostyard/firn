.PHONY: all build clean fmt lint lint-version-check verify verify-template ci test install help site site-serve

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
# The Go release this module is built with, from go.mod's `go` line — the
# only Go pin (mise reads the same line). golangci-lint must be built with
# the same Go minor or newer. The patch level is not compared, because a
# patch release does not change gofmt output or the language version the
# type checker accepts (frostyard/core frostyard-go-repo skill "Fleet
# toolchain", core ADR-0043).
GO_VERSION := $(strip $(shell sed -n 's/^go \([0-9.]*\)$$/\1/p' go.mod))
GOFILES := $(shell find . -type f -name '*.go' -not -path "./vendor/*")
# Node runs the template gate. Its pin lives in mise.toml (core ADR-0043).
NODE_MAJOR := $(shell node -p "process.versions.node.split('.')[0]" 2>/dev/null)

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

## lint-version-check: Fail unless the installed golangci-lint is the mise.toml pin and was built with a Go minor no older than go.mod's
lint-version-check:
	@test -n "$(GO_VERSION)" || { echo "go.mod has no go line"; exit 1; }
	@test -n "$(GOLANGCI_LINT_VERSION)" || { echo "mise.toml pins no golangci-lint"; exit 1; }
	@installed="$$(golangci-lint version --short 2>/dev/null)" || { echo "golangci-lint $(GOLANGCI_LINT_VERSION) is required (not installed; run: mise install)"; exit 1; }; \
	if [ "$$installed" != "$(GOLANGCI_LINT_VERSION)" ]; then echo "expected golangci-lint $(GOLANGCI_LINT_VERSION), found $$installed (run: mise install)"; exit 1; fi; \
	full="$$(golangci-lint version 2>/dev/null)" || { echo "golangci-lint version failed (run: mise install)"; exit 1; }; \
	built="$$(printf '%s\n' "$$full" | sed -n 's/.*built with go\([0-9.]*\).*/\1/p')"; \
	built_minor="$$(printf '%s\n' "$$built" | sed -n 's/^\([0-9][0-9]*\.[0-9][0-9]*\).*/\1/p')"; \
	go_minor="$$(printf '%s\n' "$(GO_VERSION)" | sed -n 's/^\([0-9][0-9]*\.[0-9][0-9]*\).*/\1/p')"; \
	if [ -z "$$built_minor" ]; then \
		echo "golangci-lint $(GOLANGCI_LINT_VERSION) reports no Go build version; cannot check it against go.mod's go$(GO_VERSION)"; exit 1; fi; \
	if [ -z "$$go_minor" ]; then \
		echo "go.mod's go line ($(GO_VERSION)) is not a Go version"; exit 1; fi; \
	if [ "$$(printf '%s\n%s\n' "$$go_minor" "$$built_minor" | sort -V | head -1)" != "$$go_minor" ]; then \
		echo "golangci-lint $(GOLANGCI_LINT_VERSION) was built with go$$built, older than go.mod's Go minor go$$go_minor: bump golangci-lint first (core ADR-0043)"; exit 1; fi

## verify-template: Template gate: agent symlinks, docs index, risk paths, corrections, vendored core copies (needs Node 22+)
verify-template:
	@if ! command -v node >/dev/null 2>&1; then echo "FAIL: make verify-template requires Node.js 22 or later and make; run: mise install"; exit 1; fi
	@if [ "$(NODE_MAJOR)" = "" ] || [ "$(NODE_MAJOR)" -lt 22 ]; then echo "FAIL: make verify-template requires Node.js 22 or later and make (found: $$(node --version 2>/dev/null || echo missing)); run: mise install"; exit 1; fi
	node scripts/check-template.mjs
	node --test test/*.test.mjs

## verify: Credential-free, non-mutating gate (what a read-only reviewer runs): template gate, tidy diff, gofmt -l, lint at the exact pin, vet, tests
verify:
	@echo "==> verify: template gate"
	@$(MAKE) --no-print-directory verify-template
	@echo "==> verify: go.mod is tidy"
	$(GO) mod tidy -diff
	@echo "==> verify: gofmt"
	@unformatted="$$($(GOFMT) -l $(GOFILES))"; \
	if [ -n "$$unformatted" ]; then echo "$$unformatted"; echo "gofmt: files need formatting (run make fmt)"; exit 1; fi
	@echo "==> verify: golangci-lint $(GOLANGCI_LINT_VERSION) (built with Go minor >= go.mod's $(GO_VERSION))"
	@$(MAKE) --no-print-directory lint-version-check
	@$(MAKE) --no-print-directory lint
	@echo "==> verify: go vet"
	$(GO) vet ./...
	@echo "==> verify: tests"
	$(GO) test ./...

## check: Run fmt, lint, test, and the template gate
check: fmt lint test verify-template

## ci: Credential-free gate mirroring CI's jobs (core ADR-0043/ADR-0022): verify, plus coverage, the race detector, cross-arch builds, and with BASE the append-only corrections check
ci:
	@$(MAKE) --no-print-directory verify
	@if [ -n "$(BASE)" ]; then echo "==> ci: corrections append-only vs $(BASE)"; node scripts/check-corrections.mjs --base "$(BASE)"; fi
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

# ---------------------------------------------------------------------------
# The graphical installer is a nested Go module (gui/, ADR-0021) on its own
# Go pin (1.27.4; mygo needs 1.27.3 or later), the same as the root module
# now. The root targets above never enter it; changes under gui/ also run
# `make gui-check`, which lints it with the pinned golangci-lint and the root
# .golangci.yml.

.PHONY: gui-build gui-check gui-demo

## gui-build: Build the graphical installer to ./build/firn-gui
gui-build:
	cd gui && $(GO) build $(LDFLAGS) -o ../build/firn-gui ./cmd/firn-gui

## gui-check: gui/ gate: tidy diff, gofmt, lint, vet, tests (needs Pango and Cairo installed)
gui-check:
	cd gui && $(GO) mod tidy -diff
	@unformatted="$$($(GOFMT) -l gui)"; \
	if [ -n "$$unformatted" ]; then echo "$$unformatted"; echo "gofmt: files need formatting (run make fmt)"; exit 1; fi
	@$(MAKE) --no-print-directory lint-version-check
	cd gui && golangci-lint run ./...
	cd gui && $(GO) vet ./...
	cd gui && $(GO) test ./...

## gui-demo: Run the graphical installer in demo mode (no root, nothing written to disk)
gui-demo: gui-build
	./build/firn-gui --demo

# The documentation site (site/), built with pinned ProperDocs and MaterialX
# (core ADR-0070, frostyard-docs-site skill). `make ci` does not build the
# site, so a change that does not touch site/ needs no Python toolchain.
SITE_VENV := build/site-venv

$(SITE_VENV)/bin/properdocs: site/requirements.txt
	python3 -m venv $(SITE_VENV)
	$(SITE_VENV)/bin/pip install -q --require-hashes -r site/requirements.txt
	touch $@

## site: Build the documentation site (strict)
site: $(SITE_VENV)/bin/properdocs
	$(SITE_VENV)/bin/properdocs build --strict -f site/properdocs.yml

## site-serve: Preview the documentation site locally
site-serve: $(SITE_VENV)/bin/properdocs
	$(SITE_VENV)/bin/properdocs serve -f site/properdocs.yml

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

.PHONY: e2e-bootc e2e-bootc-secure e2e-tui

## e2e-bootc: Loop-device bootc install E2E (root, QEMU/OVMF, podman)
e2e-bootc:
	sudo test/e2e-bootc.sh

## e2e-bootc-secure: Nested-VM bootc install under enforced Secure Boot (root, QEMU/secboot-OVMF, virt-fw-vars)
e2e-bootc-secure:
	sudo test/e2e-bootc-secure.sh

## e2e-tui: Nested-VM bootc TUI E2E (root, QEMU/OVMF)
e2e-tui:
	sudo test/e2e-tui.sh
