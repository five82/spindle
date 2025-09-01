#!/bin/bash
# Simplified Local CI Check - Essential checks only
# Matches the streamlined .github/workflows/ci.yml

set -e  # Exit on any error

echo "🔍 Essential CI Checks"
echo "======================"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

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

# Ensure we're using uv
if ! command -v uv &> /dev/null; then
    print_error "uv is not installed. Please install it first:"
    echo "curl -LsSf https://astral.sh/uv/install.sh | sh"
    exit 1
fi

# Install dependencies
print_step "Installing dependencies"
uv sync --all-extras
print_success "Dependencies installed"

# Run tests
print_step "Running tests with coverage"
uv run pytest tests/ -v --cov=spindle --cov-report=term
print_success "Tests passed"

# Check formatting
print_step "Checking code formatting"
if uv run black --check src/; then
    print_success "Code formatting check passed"
else
    print_error "Code formatting check failed"
    echo "To fix: uv run black src/"
    exit 1
fi

# Lint code
print_step "Linting code"
if uv run ruff check src/; then
    print_success "Code linting passed"
else
    print_error "Code linting failed" 
    echo "To fix: uv run ruff check src/ --fix"
    exit 1
fi

# Build package
print_step "Building package"
if uv build; then
    print_success "Package build succeeded"
else
    print_error "Package build failed"
    exit 1
fi

# Final success message
echo ""
echo "======================"
print_success "🎉 All essential checks passed! Safe to commit and push."
echo "======================"