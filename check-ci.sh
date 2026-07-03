#!/bin/bash
# Local full check for spindle.
# Hosted CI uses no_vship because libvship is unavailable on GitHub runners.

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
MOD_DIFF_BEFORE=$(mktemp)
MOD_DIFF_AFTER=$(mktemp)
cleanup_mod_diff() { rm -f "$MOD_DIFF_BEFORE" "$MOD_DIFF_AFTER"; }
trap cleanup_mod_diff EXIT
git diff -- go.mod go.sum > "$MOD_DIFF_BEFORE"
GOWORK=off go mod tidy
git diff -- go.mod go.sum > "$MOD_DIFF_AFTER"
if ! cmp -s "$MOD_DIFF_BEFORE" "$MOD_DIFF_AFTER"; then
    print_error "go mod tidy changed go.mod or go.sum. Review and commit the changes."
    exit 1
fi
cleanup_mod_diff
trap - EXIT
print_success "go.mod is tidy"

print_step "Verifying build without go.work (pinned Reel dependency)"
if GOWORK=off go build ./...; then
    print_success "Pinned-dependency build passed"
else
    print_error "Build fails without go.work — update go.mod deps (e.g. go get codeberg.org/five82/reel@latest)"
    exit 1
fi

print_step "Running go test ./..."
if GOWORK=off go test ./...; then
    print_success "Tests passed"
else
    print_error "Tests failed"
    exit 1
fi

print_step "Running go test -race ./..."
if GOWORK=off go test -race ./...; then
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
if GOWORK=off CGO_ENABLED=1 go build ./...; then
    print_success "CGO build passed"
else
    print_error "CGO build failed"
    exit 1
fi

print_step "Running golangci-lint"
if GOWORK=off golangci-lint run; then
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
if GOWORK=off govulncheck ./...; then
    print_success "No vulnerabilities found"
else
    print_error "Vulnerabilities detected"
    exit 1
fi

echo -e "\n${GREEN}All checks passed${NC}"
