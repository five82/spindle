#!/bin/bash
# Local CI check for spindle.
# Mirrors the lightweight GitHub Actions workflow while isolating system toolchains.

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

print_step() {
    echo -e "\n${BLUE}📋 $1${NC}"
    echo "----------------------"
}

print_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

print_error() {
    echo -e "${RED}❌ $1${NC}"
}

version_lt() {
    [ "$(printf '%s\n' "$1" "$2" | sort -V | head -n1)" != "$2" ]
}

print_step "Checking Go toolchain"

GO_BINARY=$(command -v go || true)
if [ -z "$GO_BINARY" ]; then
    print_error "Go is not installed. Install Go 1.22 or newer."
    exit 1
fi

GO_VERSION=$("$GO_BINARY" env GOVERSION 2>/dev/null | sed 's/^go//')
if [ -z "$GO_VERSION" ]; then
    GO_VERSION=$("$GO_BINARY" version | awk '{print $3}' | sed 's/^go//')
fi

MIN_GO_VERSION="1.22"
if version_lt "$GO_VERSION" "$MIN_GO_VERSION"; then
    print_error "Go $MIN_GO_VERSION or newer required (found $GO_VERSION)."
    exit 1
fi

GOROOT_DIR=$("$GO_BINARY" env GOROOT)
if [ -z "$GOROOT_DIR" ] || [ ! -d "$GOROOT_DIR" ]; then
    print_error "Unable to determine GOROOT; ensure Go installation is healthy."
    exit 1
fi

GOLANGCI_BINARY=$(command -v golangci-lint || true)
if [ -z "$GOLANGCI_BINARY" ]; then
    GO_BIN_DIR=$("$GO_BINARY" env GOBIN)
    if [ -z "$GO_BIN_DIR" ]; then
        GO_BIN_DIR=$("$GO_BINARY" env GOPATH)/bin
    fi
    if [ -x "$GO_BIN_DIR/golangci-lint" ]; then
        GOLANGCI_BINARY="$GO_BIN_DIR/golangci-lint"
    fi
fi

if [ -z "$GOLANGCI_BINARY" ]; then
    print_error "golangci-lint not found. Install via: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"
    exit 1
fi

print_success "Go toolchain ready (Go $GO_VERSION)"

echo "\n🧹 Simulating GitHub Actions environment (minimal PATH)"
TEMP_BIN=$(mktemp -d)
trap 'rm -rf "$TEMP_BIN"' EXIT

cp "$GO_BINARY" "$TEMP_BIN/" 2>/dev/null || {
    print_error "Failed to stage Go binary"
    exit 1
}
cp "$GOLANGCI_BINARY" "$TEMP_BIN/" 2>/dev/null || {
    print_error "Failed to stage golangci-lint binary"
    exit 1
}
cp "$(command -v rm)" "$TEMP_BIN/" 2>/dev/null || true

export PATH="$TEMP_BIN"
export INVOCATION_ID="test-github-actions"
export GOROOT="$GOROOT_DIR"

print_step "Running go test ./..."
if go test ./...; then
    print_success "go test passed"
else
    print_error "go test failed"
    exit 1
fi

print_step "Running golangci-lint run"
if golangci-lint run; then
    print_success "golangci-lint passed"
else
    print_error "golangci-lint reported issues"
    echo "Run: golangci-lint run"
    exit 1
fi

echo "\n======================"
print_success "🎉 Go checks passed"
echo "======================"
