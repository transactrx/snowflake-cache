# Snowflake Cache Library

A lightweight, in-memory cache library for Snowflake databases that automatically refreshes when monitored tables change.

## Features

- ✅ **Automatic cache invalidation** via CACHE_LOG monitoring
- ✅ **Type-safe generic interface** with Go generics
- ✅ **Zero configuration** - just provide SQL and connection
- ✅ **Thread-safe** concurrent access
- ✅ **Background polling** for automatic refresh
- ✅ **Stream registration support** for Snowflake Streams + Tasks

## Quick Start

```go
package main

import (
    "database/sql"
    "log"
    "time"
    _ "github.com/snowflakedb/gosnowflake" // register Snowflake driver
    snowflakecache "github.com/transactrx/db-cache/pkg/snowflake-cache"
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
    db, err := sql.Open("snowflake", "user:pass@account/DB/SCHEMA?warehouse=WH&role=ROLE")
    if err != nil { log.Fatal(err) }
    defer db.Close()

    // Create cache
    cache, err := snowflakecache.CreateCache[ApiKey](
        nil,             // logger (nil -> default)
        "SELECT ID AS \"id\", API_KEY AS \"api_key\", USER_ID AS \"user_id\", IS_ACTIVE AS \"is_active\", CREATED_AT AS \"created_at\" FROM MY_DATABASE.MY_SCHEMA.API_KEYS WHERE IS_ACTIVE = TRUE",
        []string{"API_KEYS"}, // monitored tables
        "UserID",        // key field on struct
        5*time.Second,    // poll interval
        db,              // *sql.DB (gosnowflake)
        "MY_DATABASE.MY_SCHEMA", // CACHE_LOG location: Database.Schema
    )
    if err != nil { log.Fatal(err) }

    // Use cache
    user1Keys := cache.Get("user1")
    allKeys := cache.GetAll()
    _ = cache.ForceRefresh()
}
```

## Required Setup

### 1. Create CACHE_LOG

Create a `CACHE_LOG` table in your Snowflake schema to track table changes:

```sql
CREATE TABLE IF NOT EXISTS MY_DATABASE.MY_SCHEMA.CACHE_LOG (
    ID INTEGER AUTOINCREMENT,
    TABLE_NAME VARCHAR(255) NOT NULL,
    OPERATION_TIME TIMESTAMP DEFAULT CURRENT_TIMESTAMP(),
    OPERATION_TYPE VARCHAR(10) DEFAULT 'UPDATE'
);
```

### 2. Update CACHE_LOG on Changes

Since Snowflake doesn't support triggers, you need to update `CACHE_LOG` when monitored tables change. Options:

**Option A: Manual Application Updates**
```go
// After modifying data
db.Exec("INSERT INTO MY_DATABASE.MY_SCHEMA.API_KEYS ...")

// Manually log the change
db.Exec("INSERT INTO MY_DATABASE.MY_SCHEMA.CACHE_LOG (TABLE_NAME, OPERATION_TIME) VALUES ('API_KEYS', CURRENT_TIMESTAMP())")
```

**Option B: Snowflake Streams + Task (Recommended)**
- Create Streams on monitored tables
- Create a Task that reads from Streams and updates CACHE_LOG
- Optionally enable automatic stream registration: `export DB_CACHE_SF_REGISTER_STREAMS=true`

See `integration-tests/snowflake/README.md` for detailed Stream + Task setup.

## API Reference

### CreateCache

```go
func CreateCache[T any](
    logger *log.Logger,
    SQL string,
    monitoredTables []string,
    keyField string,
    cacheCheckInterval time.Duration,
    DB *sql.DB,
    DB_RW string, // Format: "DATABASE.SCHEMA"
    SQLParams ...interface{},
) (DbCache[T], error)
```

**Parameters:**
- `logger`: Optional logger (nil uses default)
- `SQL`: SELECT query to load cached data
- `monitoredTables`: Table names to monitor (e.g., `[]string{"API_KEYS"}`)
- `keyField`: Struct field name used as cache key (must be string or *string)
- `cacheCheckInterval`: How often to poll CACHE_LOG for changes
- `DB`: Snowflake *sql.DB connection
- `DB_RW`: String in "DATABASE.SCHEMA" format pointing to CACHE_LOG location
- `SQLParams`: Optional query parameters

### DbCache Interface

```go
type DbCache[T any] interface {
    Get(key string) []T           // Get cached items by key
    GetAll() []T                   // Get all cached items
    ForceRefresh() error          // Force immediate cache refresh
}
```

## Examples

See `cmd/example/` for complete examples.

## Integration Tests

See `integration-tests/snowflake/` for integration tests and detailed setup instructions.

## License

[Your License Here]
