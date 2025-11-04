# Integration Tests for Snowflake Cache Library

This directory contains comprehensive integration tests for the snowflake-cache library.

## Structure

```
integration-tests/
├── snowflake/          # Snowflake integration tests
│   ├── integration_test.go
│   ├── go.mod
│   ├── README.md
│   └── run_tests.sh
├── run_all_tests.sh    # Run Snowflake tests
└── README.md           # This file
```

## Quick Start

### Run All Tests
```bash
cd integration-tests
./run_all_tests.sh
```

### Run Individual Tests
```bash
cd snowflake
./run_tests.sh
```

## Snowflake Tests

The Snowflake tests connect to a real Snowflake instance:

- **Requires**: Snowflake account and credentials
- **Environment Variables**: SNOWFLAKE_ACCOUNT, SNOWFLAKE_USER, SNOWFLAKE_PASSWORD
- **Optional**: SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA, SNOWFLAKE_WAREHOUSE

### Features Tested
- ✅ Basic cache operations (`Get`, `GetAll`, `ForceRefresh`)
- ✅ Auto-refresh behavior with database changes
- ✅ Multiple data types and key fields
- ✅ Error handling with invalid SQL and fields
- ✅ Cache invalidation with TABLE_LOG updates

### Running Snowflake Tests
```bash
cd snowflake
export SNOWFLAKE_ACCOUNT=your-account
export SNOWFLAKE_USER=your-username
export SNOWFLAKE_PASSWORD=your-password
./run_tests.sh
```

### Skipping Snowflake Tests
```bash
SKIP_SNOWFLAKE_TESTS=true ./run_all_tests.sh
```

## Test Coverage

### 1. Basic Cache Operations
- **GetAll()**: Retrieves all cached records
- **Get(key)**: Retrieves records by specific key
- **ForceRefresh()**: Manually refreshes the cache

### 2. Auto-Refresh Behavior
- Tests that cache automatically picks up database changes
- Verifies cache invalidation mechanisms work correctly
- Tests with different refresh intervals

### 3. Multiple Data Types
- **APIKey**: Tests with string key field
- **User**: Tests with different key field and data structure
- Tests with different SQL queries and filters

### 4. Error Handling
- Tests with invalid SQL queries
- Tests with invalid key field names
- Tests with missing data

## Database Schema

### Snowflake Schema
- `API_KEYS` table with manual change logging
- `USERS` table with manual change logging
- `TABLE_LOG` for change tracking
- Optional stored procedures for stream registration

## Prerequisites

- Go 1.24 or later
- Go modules enabled
- Snowflake account with appropriate permissions
- Snowflake Go driver (`github.com/snowflakedb/gosnowflake`)

## Environment Variables

**Required:**
- `SNOWFLAKE_ACCOUNT`: Your Snowflake account identifier
- `SNOWFLAKE_USER`: Your Snowflake username
- `SNOWFLAKE_PASSWORD`: Your Snowflake password (or use private key)

**Optional:**
- `SNOWFLAKE_DATABASE`: Database name (defaults to `CPE_DEV`)
- `SNOWFLAKE_SCHEMA`: Schema name (defaults to `CACHE_DEV`)
- `SNOWFLAKE_WAREHOUSE`: Warehouse name (defaults to `COMPUTE_WH`)
- `SNOWFLAKE_ROLE`: Role name
- `SNOWFLAKE_PRIVATE_KEY`: Private key for key pair authentication
- `SNOWFLAKE_PRIVATE_KEY_PATH`: Path to private key file

**Test Control:**
- `SKIP_SNOWFLAKE_TESTS`: Set to `true` to skip Snowflake tests
- `DB_CACHE_SF_REGISTER_STREAMS`: Set to `true` to enable automatic stream registration

## Troubleshooting

### Snowflake Tests
```bash
# Test connection manually
go run -c 'package main; import _ "github.com/snowflakedb/gosnowflake"; func main() {}'

# Check environment variables
env | grep SNOWFLAKE
```

### Common Issues
1. **Permission issues**: Ensure Snowflake user has required permissions
2. **Network issues**: Check firewall settings for Snowflake connections
3. **Schema issues**: Verify database and schema exist in Snowflake
4. **Authentication issues**: Verify credentials or private key format

## Cost Considerations

### Snowflake Tests
- **Costs**: Uses real Snowflake instance
- **Compute costs**: For running queries
- **Storage costs**: For test data
- **Warehouse costs**: For compute resources

**To minimize costs:**
- Use a small warehouse for testing
- Clean up test data after tests
- Consider using a dedicated test account

## Adding New Tests

1. Add test functions to `snowflake/integration_test.go`
2. Follow naming convention: `TestSnowflakeCacheIntegration/TestName`
3. Use `require.NoError()` for setup and `assert.*` for validations
4. Clean up any test data you create
5. Update this README with new test scenarios

## CI/CD Integration

### GitHub Actions Example
```yaml
name: Integration Tests
on: [push, pull_request]

jobs:
  snowflake-tests:
    runs-on: ubuntu-latest
    if: github.event_name == 'push' && github.ref == 'refs/heads/main'
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v3
        with:
          go-version: '1.24'
      - name: Run Snowflake tests
        env:
          SNOWFLAKE_ACCOUNT: ${{ secrets.SNOWFLAKE_ACCOUNT }}
          SNOWFLAKE_USER: ${{ secrets.SNOWFLAKE_USER }}
          SNOWFLAKE_PASSWORD: ${{ secrets.SNOWFLAKE_PASSWORD }}
        run: |
          cd integration-tests/snowflake
          ./run_tests.sh
```

## Performance Considerations

- Tests use 2-second cache refresh intervals for responsiveness
- Database connections are properly managed and closed
- Tests include cleanup to prevent data accumulation
- Consider using `t.Parallel()` for independent tests if needed

## Security Notes

- Never commit database credentials to version control
- Use environment variables or secure credential management
- Consider using key pair authentication for Snowflake in production
- Rotate credentials regularly
- Use dedicated test accounts with minimal permissions
