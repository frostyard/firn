# Firn — build targets

# List available targets
default:
    @just --list

# Build mentat
build-mentat:
    cd mentat && go build ./...

# Build pipeline
build-pipeline:
    cd pipeline && go build ./...

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
