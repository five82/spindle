#!/bin/bash
# Local CI check for reel.
# Mirrors the Forgejo Actions workflow.

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

MIN_GO_VERSION=$(awk '$1 == "go" { print $2; exit }' go.mod)
if ! command -v go &>/dev/null; then
    print_error "Go is not installed. Install Go $MIN_GO_VERSION or newer."
    exit 1
fi

GO_VERSION=$(go env GOVERSION 2>/dev/null | sed 's/^go//')
if [ -z "$GO_VERSION" ]; then
    GO_VERSION=$(go version | awk '{print $3}' | sed 's/^go//')
fi

if version_lt "$GO_VERSION" "$MIN_GO_VERSION"; then
    print_error "Go $MIN_GO_VERSION or newer required (found $GO_VERSION)."
    exit 1
fi

GO_TAGS_ARG=()
GOLANGCI_TAGS_ARG=()
if pkg-config --exists vship 2>/dev/null || [ -e /usr/local/lib/libvship.so ] || [ -e /usr/lib/libvship.so ] || [ -e /usr/lib64/libvship.so ]; then
    print_success "VSHIP found; checking default target-quality build"
else
    print_success "VSHIP not found; checking fixed-CRF-only no_vship build"
    GO_TAGS_ARG=(-tags no_vship)
    GOLANGCI_TAGS_ARG=(--build-tags no_vship)
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
go mod tidy
git diff -- go.mod go.sum > "$MOD_DIFF_AFTER"
if ! cmp -s "$MOD_DIFF_BEFORE" "$MOD_DIFF_AFTER"; then
    print_error "go mod tidy changed go.mod or go.sum. Review and commit the changes."
    exit 1
fi
cleanup_mod_diff
trap - EXIT
print_success "go.mod is tidy"

print_step "Running go test with coverage"
if go test "${GO_TAGS_ARG[@]}" -covermode=atomic -coverprofile=coverage.out ./...; then
    print_success "Tests passed"
    go tool cover -func=coverage.out | tail -n 1
    print_success "Coverage profile: coverage.out (view with go tool cover -html=coverage.out)"
else
    print_error "Tests failed"
    exit 1
fi

print_step "Running go test -race ./..."
GO_ARCH=$(go env GOARCH)
if [ "$GO_ARCH" = "arm64" ]; then
    print_success "Skipping race detection on arm64; ThreadSanitizer is not supported on this runner."
elif go test "${GO_TAGS_ARG[@]}" -race ./...; then
    print_success "Race detection passed"
else
    print_error "Race condition detected"
    exit 1
fi

print_step "Running go build -trimpath ./..."
if go build "${GO_TAGS_ARG[@]}" -trimpath ./...; then
    print_success "Build passed"
else
    print_error "Build failed"
    exit 1
fi

print_step "Running golangci-lint"
if golangci-lint run "${GOLANGCI_TAGS_ARG[@]}"; then
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
if govulncheck "${GO_TAGS_ARG[@]}" ./...; then
    print_success "No vulnerabilities found"
else
    print_error "Vulnerabilities detected"
    exit 1
fi

echo -e "\n${GREEN}All checks passed${NC}"
