# Snowflake Cache Testing Makefile

.PHONY: test test-coverage test-integration test-all clean

# Run unit tests
test:
	@echo "Running unit tests..."
	cd pkg/snowflake-cache && go test -v

# Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	cd pkg/snowflake-cache && go test -v -cover -coverprofile=coverage.out
	cd pkg/snowflake-cache && go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: pkg/snowflake-cache/coverage.html"

# Run integration tests
test-integration:
	@echo "Running integration tests..."
	cd integration-tests && ./run_all_tests.sh

# Run all tests (unit + integration)
test-all: test test-integration

# Run all tests with coverage
test-all-coverage: test-coverage test-integration

# Clean up test artifacts
clean:
	rm -f pkg/snowflake-cache/coverage.out
	rm -f pkg/snowflake-cache/coverage.html