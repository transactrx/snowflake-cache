#!/bin/bash

# Integration Test Runner for Snowflake Cache Library
# This script runs Snowflake integration tests

echo "🚀 Starting Snowflake Cache Integration Tests"
echo "=============================================="

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Change to integration-tests directory
cd "$(dirname "$0")"

# Run Snowflake tests
echo ""
print_status "Running Snowflake Integration Tests..."
echo "=============================================="

cd snowflake
if ./run_tests.sh; then
    print_success "Snowflake tests completed successfully ✓"
    echo ""
    echo "=============================================="
    print_success "All integration tests completed successfully! 🎉"
    echo ""
    print_status "Summary:"
    echo "  ✓ Snowflake integration tests passed"
    echo "  ✓ The snowflake-cache library is working correctly!"
    echo ""
    print_status "Integration test run completed!"
    exit 0
else
    print_error "Snowflake tests failed ✗"
    echo ""
    echo "=============================================="
    print_error "Integration tests failed!"
    echo ""
    print_status "Please check the test output above for details."
    echo "  - Snowflake tests: ./snowflake/run_tests.sh"
    exit 1
fi
