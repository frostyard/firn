# Firn — build targets

# List available targets
default:
    @just --list

# Build mentat with version info
build-mentat:
    cd mentat && go build -ldflags="-X github.com/frostyard/firn/mentat/internal/version.Version=$(git describe --tags --always --dirty 2>/dev/null || echo dev) -X github.com/frostyard/firn/mentat/internal/version.Commit=$(git rev-parse --short HEAD 2>/dev/null || echo none) -X github.com/frostyard/firn/mentat/internal/version.Date=$(date -u +%Y-%m-%dT%H:%M:%SZ) -X github.com/frostyard/firn/mentat/internal/version.BuiltBy=local" -o bin/mentat ./cmd/mentat/

# Build pipeline
build-pipeline:
    cd pipeline && go build \
        -ldflags="-X github.com/frostyard/firn/pipeline/internal/version.Version={{`git describe --tags --always --dirty 2>/dev/null || echo dev`}} \
                  -X github.com/frostyard/firn/pipeline/internal/version.Commit={{`git rev-parse --short HEAD 2>/dev/null || echo none`}} \
                  -X github.com/frostyard/firn/pipeline/internal/version.Date={{`date -u +%Y-%m-%dT%H:%M:%SZ`}} \
                  -X github.com/frostyard/firn/pipeline/internal/version.BuiltBy=just" \
        -o bin/pipeline ./cmd/pipeline/

# Build all
build: build-mentat build-pipeline

# Test mentat
test-mentat:
    cd mentat && go test ./...

# Test pipeline
test-pipeline:
    cd pipeline && go test ./...

# Test all
test: test-mentat test-pipeline

# Lint all
lint:
    cd mentat && go vet ./...
    cd pipeline && go vet ./...
