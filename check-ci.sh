#!/bin/bash
# Local CI Check Script - Runs the same checks as .github/workflows/ci.yml
# This ensures that commits won't fail in GitHub Actions

set -e  # Exit on any error

echo "🔍 Running Local CI Checks (matching .github/workflows/ci.yml)"
echo "================================================================="

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_step() {
    echo -e "\n${BLUE}📋 $1${NC}"
    echo "----------------------------------------"
}

print_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

print_error() {
    echo -e "${RED}❌ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

# Ensure we're using uv
if ! command -v uv &> /dev/null; then
    print_error "uv is not installed. Please install it first:"
    echo "curl -LsSf https://astral.sh/uv/install.sh | sh"
    exit 1
fi

# Install dependencies (like CI does)
print_step "Installing dependencies"
uv sync --all-extras
print_success "Dependencies installed"

# JOB 1: TEST
print_step "JOB 1: Running tests with coverage"
uv run pytest tests/ -v --cov=spindle --cov-report=xml --cov-report=term
print_success "Tests passed"

# JOB 2: LINT & TYPE CHECK
print_step "JOB 2: Lint & Type Check"

echo "  Checking code formatting with black..."
if uv run black --check src/; then
    print_success "Black formatting check passed"
else
    print_error "Black formatting check failed"
    echo "To fix: uv run black src/"
    exit 1
fi

echo "  Linting with ruff..."
if uv run ruff check src/; then
    print_success "Ruff linting passed"
else
    print_error "Ruff linting failed"
    echo "To fix: uv run ruff check src/ --fix"
    exit 1
fi

echo "  Type checking with mypy..."
if uv run mypy src/; then
    print_success "MyPy type checking passed"
else
    print_error "MyPy type checking failed"
    exit 1
fi

echo "  Checking import sorting with isort..."
if uv run isort --check-only src/; then
    print_success "Import sorting check passed"
else
    print_error "Import sorting check failed"
    echo "To fix: uv run isort src/"
    exit 1
fi

# JOB 3: SECURITY SCAN
print_step "JOB 3: Security Scan"

echo "  Running security scan with bandit..."
if uv run bandit -r src/ -ll; then
    print_success "Bandit security scan passed"
else
    print_warning "Bandit security scan found issues (CI continues on error)"
fi

echo "  Checking for known vulnerabilities with pip-audit..."
if uv run pip-audit; then
    print_success "Pip-audit vulnerability check passed"
else
    print_warning "Pip-audit found vulnerabilities (CI continues on error)"
fi

# JOB 4: BUILD PACKAGE
print_step "JOB 4: Build Package"

echo "  Building distribution packages..."
if uv build; then
    print_success "Package build succeeded"
else
    print_error "Package build failed"
    exit 1
fi

echo "  Checking distribution..."
if uv run python -m twine check dist/*; then
    print_success "Distribution check passed"
else
    print_warning "Distribution check found issues (CI continues on error)"
fi

# Final success message
echo ""
echo "================================================================="
print_success "🎉 All CI checks passed! Safe to commit and push."
echo "================================================================="