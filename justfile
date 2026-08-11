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
