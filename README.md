# Snowflake Cache Library

A lightweight, in-memory cache library for Snowflake databases that automatically refreshes when monitored tables change.

## Features

- ✅ **Automatic cache invalidation** via CACHE_LOG monitoring
- ✅ **Type-safe generic interface** with Go generics
- ✅ **Zero configuration** - just provide SQL and connection
- ✅ **Thread-safe** concurrent access
- ✅ **Background polling** for automatic refresh
- ✅ **Stream registration support** for Snowflake Streams + Tasks
- ✅ **Fail-closed hook** (`OnRefreshError`) to halt on persistent refresh failure
- ✅ **Dynamic rows** - cache `map[string]any` rows when the table structure isn't known at compile time

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
        "MY_DATABASE.MY_SCHEMA",     // Default database + schema for monitored tables
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

Create a `CACHE_LOG` table in your Snowflake schema (in the same database as your monitored tables) to track table changes:

```sql
CREATE TABLE IF NOT EXISTS MY_DATABASE.DB_CACHE.CACHE_LOG (
    ID INTEGER AUTOINCREMENT,
    TABLE_NAME VARCHAR(255) NOT NULL,
    UPDATE_TIME TIMESTAMP_LTZ DEFAULT CURRENT_TIMESTAMP()
);
```

### 2. Set Up Automatic Cache Invalidation

Cache invalidation is handled automatically via Snowflake Streams + Task. The library handles most of this for you:

**What the library does automatically:**
- The library automatically creates Streams for your monitored tables on first cache creation (enabled by default)
- The database used for CACHE_LOG and REGISTERCACHETABLE is the same database as your monitored tables
- The library validates that the `DB_CACHE` schema and `REGISTERCACHETABLE` procedure exist before creating the cache
- To disable automatic stream registration, set `export DB_CACHE_SF_REGISTER_STREAMS=false`

**What you need to set up once (infrastructure):**
> **Note**: The RAS DATA Science Team has already set this up for our users. You only need to set this up if you're using this library outside of the RAS environment.

1. Create the `DB_CACHE` schema and `CACHE_LOG` table (see above)
2. Create the `REGISTERCACHETABLE` procedure and `HEARTBEAT` procedure
3. Create and start the `HEARTBEAT_TASK` to run the HEARTBEAT procedure on a schedule

**Important**: Once set up, CACHE_LOG is updated automatically by the HEARTBEAT Task. You should never manually insert into CACHE_LOG from your application code.

See `integration-tests/snowflake/README.md` for detailed setup instructions.

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
    DB_RW string, // Format: "SCHEMA"
    SQLParams ...interface{},
) (DbCache[T], error)
```

**Parameters:**
- `logger`: Optional logger (nil uses default)
- `SQL`: SELECT query to load cached data
- `monitoredTables`: Table names to monitor (e.g., `[]string{"API_KEYS"}`)
- `keyField`: Struct field, or query result column when using maps, used as the cache key.
  Values must be strings, numeric types, or pointers to those types.
- `cacheCheckInterval`: How often to poll CACHE_LOG for changes
- `DB`: Snowflake *sql.DB connection
- `DB_RW`: String in "SCHEMA" or "DATABASE.SCHEMA" format (specifies default database+schema for monitored tables; CACHE_LOG uses the DB_CACHE schema in the same database)
- `SQLParams`: Optional query parameters

### CreateCacheWithRowsLoader

```go
type RowsLoader[T any] func(rows *sql.Rows) ([]T, error)

func CreateCacheWithRowsLoader[T any](
    logger *log.Logger,
    query string,
    monitoredTables []string,
    keyField string,
    cacheCheckInterval time.Duration,
    connection *sql.DB,
    schema string, // "SCHEMA" or "DATABASE.SCHEMA"
    rowsLoader RowsLoader[T],
    sqlParams ...any,
) (DbCache[T], error)
```

Same as `CreateCache`, except the load query's result set is passed to `rowsLoader` instead of being
mapped by sqlscan. The loader has access to `rows.ColumnTypes()`, so it can pick Go types per column; for
example, when scanning into map rows the Snowflake driver returns `NUMBER` columns as strings, which a loader
can scan as `int64` instead.
The cache closes `rows` and checks `rows.Err()` after the loader returns. A nil `rowsLoader` behaves like
`CreateCache`.

### DbCache Interface

```go
type DbCache[T any] interface {
    Get(key string) []T           // Get cached items by key
    GetAll() []T                   // Get all cached items
    ForceRefresh() error          // Force immediate cache refresh
    OnRefreshError(handler func(err error, consecutiveFailures int)) // Observe background-refresh failures
}
```

### OnRefreshError (fail-closed refresh)

By default a failed *background* refresh is logged and the cache keeps serving the last
successfully loaded data (fail-static). For load-bearing caches — where serving silently stale
data is worse than a restart — register a handler to observe refresh failures and act on
persistent ones:

```go
cache.OnRefreshError(func(err error, consecutiveFailures int) {
    if consecutiveFailures >= 3 { // ~3 polling intervals of sustained failure
        log.Fatalf("cache refresh failing persistently, halting: %v", err)
    }
})
```

- `consecutiveFailures` is the running count of consecutive background-refresh failures; it
  resets to 0 on the next successful refresh.
- The handler is invoked from the background poller only — **not** for the initial synchronous
  load (constructor errors are returned to the caller instead).
- Register it immediately after creating the cache. A nil handler disables the callback.

## Dynamic Rows

When the row shape isn't known at compile time, use `map[string]any` (or a named map type such as
`type Row map[string]any`) as `T`. Each row maps column name to value, and `keyField` names the key column,
matched case-insensitively (`"user_id"` matches Snowflake's `USER_ID`).

```go
cache, err := snowflakecache.CreateCache[map[string]any](
    nil,
    "SELECT * FROM MY_DATABASE.MY_SCHEMA.API_KEYS",
    []string{"API_KEYS"},
    "user_id",       // key column
    5*time.Second,
    db,
    "MY_DATABASE.MY_SCHEMA",
)
rows := cache.Get("user1") // []map[string]any, keys are column names as returned (e.g. "USER_ID")
```

When scanning into map rows, the Snowflake driver returns `NUMBER` columns as strings. To keep their real
types, use `CreateCacheWithRowsLoader` with a loader that picks Go types from the column metadata:

```go
func typedRowMaps(rows *sql.Rows) ([]map[string]any, error) {
    cols, err := rows.Columns()
    if err != nil {
        return nil, err
    }
    colTypes, err := rows.ColumnTypes()
    if err != nil {
        return nil, err
    }
    var result []map[string]any
    for rows.Next() {
        values := make([]any, len(cols))
        for i, ct := range colTypes {
            if _, scale, ok := ct.DecimalSize(); ct.DatabaseTypeName() == "FIXED" && ok && scale == 0 {
                values[i] = &sql.NullInt64{}
            } else {
                values[i] = &sql.NullString{}
            }
        }
        if err := rows.Scan(values...); err != nil {
            return nil, err
        }
        row := make(map[string]any, len(cols))
        for i, col := range cols {
            switch v := values[i].(type) {
            case *sql.NullInt64:
                if v.Valid {
                    row[col] = v.Int64
                } else {
                    row[col] = nil
                }
            case *sql.NullString:
                if v.Valid {
                    row[col] = v.String
                } else {
                    row[col] = nil
                }
            }
        }
        result = append(result, row)
    }
    return result, nil
}

cache, err := snowflakecache.CreateCacheWithRowsLoader[map[string]any](
    nil, "SELECT * FROM MY_DATABASE.MY_SCHEMA.API_KEYS", []string{"API_KEYS"}, "id",
    5*time.Second, db, "MY_DATABASE.MY_SCHEMA", typedRowMaps)
rows := cache.Get("42") // numeric keys are looked up by their string form
```

## Examples

See `cmd/example/` for complete examples.

## Integration Tests

See `integration-tests/snowflake/` for integration tests and detailed setup instructions.

## Environment Variables

| Variable | Values | Default | Description |
|----------|--------|---------|-------------|
| `DB_CACHE_SF_REGISTER_STREAMS` | `true`, `false` | `true` | Enable/disable automatic stream registration |
