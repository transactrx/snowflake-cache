# Snowflake Cache Examples

This directory contains examples showing how to use the `snowflakecache.CreateCache` API with Snowflake.

## Quick Start

```go
package main

import (
    "database/sql"
    "log"
    "time"
    _ "github.com/snowflakedb/gosnowflake" // register Snowflake driver
    snowflakecache "github.com/transactrx/snowflake-cache/pkg/snowflake-cache"
)

type ApiKey struct {
    ID        *int       `db:"id"`
    ApiKey    *string    `db:"api_key"`
    UserID    *string    `db:"user_id"`   // key field: string or *string
    IsActive  *bool      `db:"is_active"`
    CreatedAt *time.Time `db:"created_at"`
}

func main() {
    // Connect to Snowflake
    db, err := sql.Open("snowflake", "user:password@account/database/schema")
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()

    // Create cache
    cache, err := snowflakecache.CreateCache[ApiKey](
        nil, // logger (nil uses default)
        `SELECT 
            ID AS "id",
            API_KEY AS "api_key",
            USER_ID AS "user_id",
            IS_ACTIVE AS "is_active",
            CREATED_AT AS "created_at"
        FROM MY_DATABASE.MY_SCHEMA.API_KEYS`,
        []string{"API_KEYS"},    // monitored tables
        "UserID",                // key field
        time.Second*60,          // check interval
        db,                      // Snowflake DB connection
        "MY_DATABASE.MY_SCHEMA", // CACHE_LOG location: Database.Schema
    )
    if err != nil {
        log.Fatal(err)
    }

    // Use cache
    result := cache.Get("someid")
    if result != nil {
        log.Printf("Found in cache: %v", result)
    } else {
        log.Printf("Value not found in cache!")
    }

    // Get all values
    allKeys := cache.GetAll()
    log.Printf("Total keys in cache: %d", len(allKeys))

    // Force refresh if needed
    if err := cache.ForceRefresh(); err != nil {
        log.Printf("Error refreshing cache: %v", err)
    }
}
```

## Parameters Explained

### Connection Type
- **Snowflake**: Pass `*sql.DB` as the DB parameter (created with `sql.Open("snowflake", dsn)`)

### DB_RW Parameter
- **Snowflake**: Pass a string in `"DATABASE.SCHEMA"` format to specify where CACHE_LOG is located
- Example: `"MY_DATABASE.MY_SCHEMA"` means CACHE_LOG is at `MY_DATABASE.MY_SCHEMA.CACHE_LOG`

### SQL Naming
- **Snowflake**: Uses uppercase names by default, with quoted aliases for struct mapping
- Example: `SELECT ID AS "id", NAME AS "name" FROM USERS`
- The quoted aliases map to your Go struct field names (case-sensitive)

## Cache Usage

Once created, use the cache API:

```go
// Get by key
result := cache.Get("someKey")

// Get all cached items
allItems := cache.GetAll()

// Force refresh
err := cache.ForceRefresh()
```

## Running the Example

```bash
# Set environment variable
export DB_BACKEND=snowflake

# Run example
go run main.go
```

## Prerequisites

### Snowflake Setup

1. **Snowflake account** with appropriate credentials
2. **Create CACHE_LOG** manually:
   ```sql
   CREATE TABLE IF NOT EXISTS MY_DATABASE.MY_SCHEMA.CACHE_LOG (
       ID INTEGER AUTOINCREMENT,
       TABLE_NAME VARCHAR(255) NOT NULL,
       OPERATION_TIME TIMESTAMP DEFAULT CURRENT_TIMESTAMP(),
       OPERATION_TYPE VARCHAR(10) DEFAULT 'UPDATE'
   );
   ```

3. **Update CACHE_LOG** when monitored tables change (see main README for options)

## See Also

- [Snowflake Integration Tests](../../integration-tests/snowflake/)
- [Main README](../../README.md)
