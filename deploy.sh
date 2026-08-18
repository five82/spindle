#!/bin/bash
# Build the working tree and deploy it over the installed spindle.
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
    echo "   Spindle remains stopped. No restoration was attempted."
    if [ -n "${PREVIOUS:-}" ]; then
        echo "   Previous binary: $PREVIOUS"
    fi
    exit 1
}

cd "$(dirname "$0")"

print_step "Locating the installed spindle"
if TARGET=$(command -v spindle 2>/dev/null); then
    print_success "$TARGET"
else
    TARGET="$(go env GOPATH)/bin/spindle"
    if [ -x "$TARGET" ]; then
        print_success "$TARGET"
    else
        print_success "$TARGET (first install)"
    fi
fi

print_step "Building"
BUILD=$(mktemp)
trap 'rm -f "$BUILD"' EXIT
CGO_ENABLED=1 go build -trimpath -o "$BUILD" ./cmd/spindle
print_success "built $(git rev-parse --short HEAD 2>/dev/null || echo 'working tree')"

WAS_RUNNING=false
if [ -x "$TARGET" ]; then
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
