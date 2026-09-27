#!/bin/bash
# Build one monorepo tool and deploy it over its installed binary.
# Run ./check-ci.sh first; this script does not test what it ships.

set -euo pipefail

case "${1:-}" in
    spindle) PACKAGE=./cmd/spindle; BUILD_CGO=1 ;;
    flyer) PACKAGE=./flyer/cmd/flyer; BUILD_CGO=0 ;;
    reel) PACKAGE=./reel/cmd/reel; BUILD_CGO=1 ;;
    -h|--help) echo "Usage: $0 spindle|flyer|reel"; exit 0 ;;
    *) echo "Usage: $0 spindle|flyer|reel" >&2; exit 2 ;;
esac
if [ "$#" -ne 1 ]; then
    echo "Usage: $0 spindle|flyer|reel" >&2
    exit 2
fi
TOOL=$1

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

daemon_state() {
    local json=$1 state
    state=$(printf '%s\n' "$json" | awk '
        $1 == "\"running\":" {
            gsub(/,/, "", $2)
            print $2
            exit
        }
    ')
    case "$state" in
        true|false) printf '%s' "$state" ;;
        *) return 1 ;;
    esac
}

deployment_failed() {
    print_error "$1"
    if [ "$TOOL" = spindle ]; then
        echo "   Spindle remains stopped. No restoration was attempted."
    else
        echo "   No restoration was attempted."
    fi
    if [ -n "${PREVIOUS:-}" ]; then
        echo "   Previous binary: $PREVIOUS"
    fi
    exit 1
}

cd "$(dirname "$0")"

print_step "Locating the installed $TOOL"
if TARGET=$(command -v "$TOOL" 2>/dev/null); then
    print_success "$TARGET"
else
    TARGET="$(go env GOPATH)/bin/$TOOL"
    if [ -x "$TARGET" ]; then
        print_success "$TARGET"
    else
        print_success "$TARGET (first install)"
    fi
fi

print_step "Building"
BUILD=$(mktemp)
trap 'rm -f "$BUILD"' EXIT
CGO_ENABLED=$BUILD_CGO go build -trimpath -o "$BUILD" "$PACKAGE"
print_success "built $(git rev-parse --short HEAD 2>/dev/null || echo 'working tree')"

WAS_RUNNING=false
if [ "$TOOL" = spindle ] && [ -x "$TARGET" ]; then
    print_step "Checking daemon state"
    if ! STATUS_BEFORE=$("$TARGET" status --json); then
        print_error "could not query the installed spindle"
        exit 1
    fi
    if ! WAS_RUNNING=$(daemon_state "$STATUS_BEFORE"); then
        print_error "could not read daemon state from spindle status"
        exit 1
    fi
    if [ "$WAS_RUNNING" = true ]; then
        print_success "running"
    else
        print_success "stopped"
    fi
fi

if [ "$WAS_RUNNING" = true ]; then
    print_step "Stopping the daemon"
    if ! "$TARGET" stop; then
        print_error "daemon shutdown failed; no files were installed"
        exit 1
    fi
fi

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

if [ "$TOOL" != spindle ]; then
    echo -e "\n${GREEN}Deployed${NC}"
    exit 0
fi

if [ "$WAS_RUNNING" = true ]; then
    print_step "Starting the daemon"
    if ! "$TARGET" start; then
        "$TARGET" stop || true
        deployment_failed "daemon startup failed"
    fi
fi

print_step "Verifying daemon state"
if ! STATUS_AFTER=$("$TARGET" status --json); then
    if [ "$WAS_RUNNING" = true ]; then
        "$TARGET" stop || true
    fi
    deployment_failed "installed spindle status check failed"
fi
if ! IS_RUNNING=$(daemon_state "$STATUS_AFTER"); then
    if [ "$WAS_RUNNING" = true ]; then
        "$TARGET" stop || true
    fi
    deployment_failed "could not read the installed daemon state"
fi
if [ "$IS_RUNNING" != "$WAS_RUNNING" ]; then
    if [ "$WAS_RUNNING" = true ]; then
        "$TARGET" stop || true
    fi
    deployment_failed "daemon state changed unexpectedly"
fi
if [ "$IS_RUNNING" = true ]; then
    print_success "running (restarted)"
else
    print_success "stopped (left stopped)"
fi

echo -e "\n${GREEN}Deployed${NC}"
