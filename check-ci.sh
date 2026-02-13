#!/bin/bash
# Local CI check for spindle.
# Mirrors the GitHub Actions workflow.

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

print_step() {
    echo -e "\n${BLUE}:: $1${NC}"
}

print_success() {
    echo -e "${GREEN}   $1${NC}"
}

print_error() {
    echo -e "${RED}   $1${NC}"
}

version_lt() {
    [ "$(printf '%s\n' "$1" "$2" | sort -V | head -n1)" != "$2" ]
}

print_step "Checking Go toolchain"

if ! command -v go &>/dev/null; then
    print_error "Go is not installed. Install Go 1.26 or newer."
    exit 1
fi

GO_VERSION=$(go env GOVERSION 2>/dev/null | sed 's/^go//')
if [ -z "$GO_VERSION" ]; then
    GO_VERSION=$(go version | awk '{print $3}' | sed 's/^go//')
fi

MIN_GO_VERSION="1.26"
if version_lt "$GO_VERSION" "$MIN_GO_VERSION"; then
    print_error "Go $MIN_GO_VERSION or newer required (found $GO_VERSION)."
    exit 1
fi

print_step "Updating golangci-lint to latest"
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
GOLANGCI_VERSION=$(golangci-lint version --format short 2>/dev/null || golangci-lint version 2>/dev/null | head -n1 | sed 's/.*version //; s/ .*//')
print_success "Go $GO_VERSION, golangci-lint $GOLANGCI_VERSION"

print_step "Verifying go.mod is tidy"
go mod tidy
if ! git diff --quiet go.mod go.sum 2>/dev/null; then
    print_error "go.mod or go.sum changed after 'go mod tidy'. Commit the changes."
    exit 1
fi
print_success "go.mod is tidy"

print_step "Running go test ./..."
if go test ./...; then
    print_success "Tests passed"
else
    print_error "Tests failed"
    exit 1
fi

print_step "Running go test -race ./..."
if go test -race ./...; then
    print_success "Race detection passed"
else
    print_error "Race condition detected"
    exit 1
fi

print_step "Running CGO build"
if ! command -v gcc &>/dev/null; then
    print_error "CGO build requires gcc; install build-essential and rerun"
    exit 1
fi
if CGO_ENABLED=1 go build ./...; then
    print_success "CGO build passed"
else
    print_error "CGO build failed"
    exit 1
fi

print_step "Running golangci-lint"
if golangci-lint run; then
    print_success "Lint passed"
else
    print_error "Lint issues found"
    exit 1
fi

print_step "Running govulncheck"
if ! command -v govulncheck &>/dev/null; then
    echo "   Installing govulncheck..."
    go install golang.org/x/vuln/cmd/govulncheck@latest
fi
if govulncheck ./...; then
    print_success "No vulnerabilities found"
else
    print_error "Vulnerabilities detected"
    exit 1
fi

echo -e "\n${GREEN}All checks passed${NC}"
