# firn — `just check` is the full local gate and exactly what CI runs.

default: check

check: fmt-check vet test build

fmt-check:
    test -z "$(gofmt -l .)" || { gofmt -l .; exit 1; }

vet:
    go vet ./...

test:
    go test ./...

build:
    go build -o firn ./cmd/firn

fmt:
    gofmt -w .

# Requires root, QEMU/OVMF, podman, ~20G scratch. See test/e2e-bootc.sh.
e2e-bootc:
    sudo test/e2e-bootc.sh

# Requires root, network, QEMU/OVMF, ~30G scratch. See test/e2e-ab.sh.
e2e-ab:
    sudo test/e2e-ab.sh

# Drives the real TUI wizard via tmux inside a nested VM (ADR-0009:
# A/B installs must run nested). Same requirements as e2e-ab.
e2e-tui:
    sudo test/e2e-tui.sh
