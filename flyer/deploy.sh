#!/bin/bash
# Build the working tree and deploy it over the installed flyer.
# Run ./check-ci.sh first; this script does not test what it ships.

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

deployment_failed() {
    print_error "$1"
    echo "   No restoration was attempted."
    if [ -n "${PREVIOUS:-}" ]; then
        echo "   Previous binary: $PREVIOUS"
    fi
    exit 1
}

cd "$(dirname "$0")"

print_step "Locating the installed flyer"
if TARGET=$(command -v flyer 2>/dev/null); then
    print_success "$TARGET"
else
    TARGET="$(go env GOPATH)/bin/flyer"
    if [ -x "$TARGET" ]; then
        print_success "$TARGET"
    else
        print_success "$TARGET (first install)"
    fi
fi

print_step "Building"
BUILD=$(mktemp)
trap 'rm -f "$BUILD"' EXIT
go build -trimpath -o "$BUILD" ./cmd/flyer
print_success "built $(git rev-parse --short HEAD 2>/dev/null || echo 'working tree')"

PREVIOUS=""
print_step "Installing"
if ! mkdir -p "$(dirname "$TARGET")"; then
    deployment_failed "could not create the install directory"
fi
if [ -x "$TARGET" ]; then
    PREVIOUS="$TARGET.previous"
    if ! cp "$TARGET" "$PREVIOUS"; then
        deployment_failed "could not preserve the previous binary"
    fi
    echo "   previous binary kept at $PREVIOUS"
fi
if ! cp "$BUILD" "$TARGET"; then
    deployment_failed "could not install the candidate binary"
fi
print_success "installed $TARGET"

print_step "Verifying installation"
if ! cmp -s "$BUILD" "$TARGET"; then
    deployment_failed "installed binary does not match the build"
fi
print_success "installed binary matches the build"

echo -e "\n${GREEN}Deployed${NC}"
