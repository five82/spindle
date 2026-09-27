#!/bin/bash
# Full monorepo checks, shared by local development and Forgejo Actions.
set -euo pipefail
cd "$(dirname "$0")"

RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'
GOLANGCI_VERSION=v2.14.0
GOVULNCHECK_VERSION=v1.7.0

print_step() { echo -e "\n${BLUE}:: $1${NC}"; }
print_success() { echo -e "${GREEN}   $1${NC}"; }
print_error() { echo -e "${RED}   $1${NC}"; }

print_step "Checking Go toolchain"
MIN_GO_VERSION=$(awk '$1 == "go" { print $2; exit }' go.mod)
if ! command -v go &>/dev/null; then
    print_error "Go is not installed. Install Go $MIN_GO_VERSION or newer."
    exit 1
fi
GO_VERSION=$(go env GOVERSION)
GO_VERSION=${GO_VERSION#go}
if [ "$(printf '%s\n' "$GO_VERSION" "$MIN_GO_VERSION" | sort -V | head -n1)" != "$MIN_GO_VERSION" ]; then
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

print_step "Checking golangci-lint $GOLANGCI_VERSION"
if ! command -v golangci-lint &>/dev/null || [ "$(golangci-lint version --short 2>/dev/null)" != "${GOLANGCI_VERSION#v}" ]; then
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$GOLANGCI_VERSION
fi
print_success "Go $GO_VERSION, golangci-lint $GOLANGCI_VERSION"

print_step "Verifying go.mod is tidy"
CHECK_DIR=$(mktemp -d)
trap 'rm -rf "$CHECK_DIR"' EXIT
cp go.mod go.sum "$CHECK_DIR/"
go mod tidy
if ! cmp -s go.mod "$CHECK_DIR/go.mod" || ! cmp -s go.sum "$CHECK_DIR/go.sum"; then
    print_error "go mod tidy changed go.mod or go.sum. Review and commit the changes."
    exit 1
fi
print_success "go.mod is tidy"

print_step "Running go test with coverage"
go test "${GO_TAGS_ARG[@]}" -covermode=atomic -coverprofile=coverage.out ./...
print_success "Tests passed"
go tool cover -func=coverage.out | tail -n 1
print_success "Coverage profile: coverage.out (view with go tool cover -html=coverage.out)"

print_step "Running go test -race ./..."
# Blue's ARM64 Forgejo runner cannot run ThreadSanitizer. The AMD64 encoding
# workstation must pass the race suite before deployment; do not skip it there.
if [ "${GITHUB_ACTIONS:-}" = true ] && [ "$(go env GOARCH)" = arm64 ]; then
    print_success "Skipping race detection on the ARM64 Forgejo runner; run ./check-ci.sh on the AMD64 workstation before deployment."
else
    go test "${GO_TAGS_ARG[@]}" -race -p 4 ./...
    print_success "Race detection passed"
fi

print_step "Running CGO build"
CGO_ENABLED=1 go build "${GO_TAGS_ARG[@]}" -trimpath ./...
print_success "CGO build passed"

print_step "Checking Flyer without CGO or encoder libraries"
CGO_ENABLED=0 go test ./flyer/...
CGO_ENABLED=0 go build -trimpath -o "$CHECK_DIR/flyer" ./flyer/cmd/flyer
print_success "Standalone Flyer build passed"

print_step "Running golangci-lint"
golangci-lint run "${GOLANGCI_TAGS_ARG[@]}"
print_success "Lint passed"

print_step "Running govulncheck"
if ! command -v govulncheck &>/dev/null || ! govulncheck -version 2>&1 | grep -Fq "Scanner: govulncheck@$GOVULNCHECK_VERSION"; then
    echo "   Installing govulncheck $GOVULNCHECK_VERSION..."
    go install golang.org/x/vuln/cmd/govulncheck@$GOVULNCHECK_VERSION
fi
govulncheck "${GO_TAGS_ARG[@]}" ./...
print_success "No vulnerabilities found"

echo -e "\n${GREEN}All checks passed${NC}"
