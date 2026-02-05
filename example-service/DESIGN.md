# Cache Comparison Service - Design Document

## Overview

This service compares cache functionality between two database cache libraries:
- **snowflake-cache** (`github.com/transactrx/snowflake-cache`) - Snowflake-backed cache. Yes!
- **db-cache** (`github.com/transactrx/db-cache`) - PostgreSQL-backed cache

Both libraries cache the **same data** from their respective databases. This service periodically compares results from both caches and logs any discrepancies to aid in the migration from PostgreSQL to Snowflake.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                      Cache Comparison Service                           │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌──────────────────────┐         ┌──────────────────────┐             │
│  │   Snowflake Cache    │         │   PostgreSQL Cache   │             │
│  │   (snowflake-cache)  │         │     (db-cache)       │             │
│  └──────────┬───────────┘         └──────────┬───────────┘             │
│             │                                │                          │
│             └────────────┬───────────────────┘                          │
│                          ▼                                              │
│              ┌───────────────────────┐                                  │
│              │   Comparison Engine   │                                  │
│              │   - Compare counts    │                                  │
│              │   - Compare keys      │                                  │
│              │   - Compare values    │                                  │
│              └───────────┬───────────┘                                  │
│                          ▼                                              │
│              ┌───────────────────────┐                                  │
│              │   Logging/Reporter    │                                  │
│              │   - Log discrepancies │                                  │
│              │   - Metrics output    │                                  │
│              └───────────────────────┘                                  │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
                    │                              │
                    ▼                              ▼
        ┌───────────────────┐          ┌───────────────────┐
        │     Snowflake     │          │    PostgreSQL     │
        │    (CACHE_LOG)    │          │   (table_log)     │
        └───────────────────┘          └───────────────────┘
```

## Library Interfaces

### snowflake-cache Interface
```go
// DbCache[T] interface from snowflake-cache
type DbCache[T any] interface {
    Get(string) []T       // Get items by key
    GetAll() []T          // Get all cached items
    ForceRefresh() error  // Force cache reload
}

// Constructor
func CreateCache[T any](
    logger *log.Logger,
    SQL string,
    monitoredTables []string,
    keyField string,
    cacheCheckInterval time.Duration,
    DB any,                // *sql.DB for Snowflake
    DB_RW any,             // "DATABASE.SCHEMA" string for Snowflake
    SQLParams ...interface{},
) (DbCache[T], error)
```

### db-cache Interface
```go
// DbCache[T] struct from db-cache
type DbCache[T any] struct { ... }

func (c *DbCache[T]) Get(index string) []T
func (c *DbCache[T]) GetAll() []T
func (c *DbCache[T]) ForceRefresh() error

// Constructor
func CreateCache[T any](
    logger *log.Logger,
    SQL string,
    monitoredTables []string,
    keyField string,
    cacheCheckInterval time.Duration,
    DB *pgxpool.Pool,
    DB_RW *pgxpool.Pool,
    SQLParams ...interface{},
) (*DbCache[T], error)
```

## Data Model

Both caches will use the same struct type to ensure comparable data:

```go
type ApiKey struct {
    Key           *string `db:"key" json:"key"`
    Name          *string `db:"name" json:"name"`
    Description   *string `db:"description" json:"description"`
    ClientID      *string `db:"client_id" json:"client_id"`
    Configuration *string `db:"configuration" json:"configuration"`
    Volumes       *string `db:"volumes" json:"volumes"`
    MaxDailyRate  *int64  `db:"max_daily_rate" json:"max_daily_rate"`
}
```

## Service Components

### 1. Configuration (`config.go`)
Environment variables needed:
- `SNOWFLAKE_ENV` - **Required**: `DEV` or `PROD` (determines database for stream registration: CPE_DEV or CPE_PROD)
- `SNOWFLAKE_ACCOUNT` - Snowflake account identifier
- `SNOWFLAKE_USER` - Snowflake username
- `SNOWFLAKE_PRIVATE_KEY` - Base64-encoded or PEM private key
- `SNOWFLAKE_DATABASE` - Snowflake database name
- `SNOWFLAKE_SCHEMA` - Snowflake schema name
- `POSTGRES_DSN` - PostgreSQL connection string
- `SNOWFLAKE_SQL` - SQL query for Snowflake cache
- `POSTGRES_SQL` - SQL query for PostgreSQL cache
- `COMPARISON_INTERVAL` - Interval between comparisons, parsed as a `time.Duration`
  string (for example: `"5m"`, `"30s"`, `"1h"`). If unset, default to `"5m"`.
- `LOG_LEVEL` - Logging verbosity (one of: `debug`, `info`, `warn`, `error`).
  If unset, default to `info`.
- `MAX_DETAILED_MISMATCHES` - Optional limit on how many individual
  `ValueMismatch` entries are emitted in logs per comparison run. Parsed as an
  integer; if unset, default to `100`.

### 2. Cache Manager (`cache_manager.go`)
Responsible for:
- Initializing both Snowflake and PostgreSQL caches
- Managing cache lifecycle
- Providing unified access to both caches
- Handling initialization failures in a fail-fast manner:
  - If either cache (or its underlying DB connection) fails to initialize,
    log a clear, structured error and do **not** start the periodic comparison
    loop. The service should exit so that infrastructure can restart it or
    alert on the failure, rather than emitting misleading comparison results.

### 3. Comparison Engine (`comparator.go`)
Comparison logic:
- **Forced refresh before comparison**:
  - On each comparison interval, call `ForceRefresh()` on **both** caches.
  - If `ForceRefresh()` fails for either cache, log a structured error that
    includes which cache failed and the error message, and **skip** producing
    a `ComparisonReport` for that interval. This avoids reporting data
    mismatches that are actually caused by connectivity or refresh issues.
- **Count comparison**: Total items in each cache (after successful refresh).
- **Key comparison**: Keys present in one cache but not the other.
- **Value comparison**: For matching keys, compare field values field-by-field:
  - For pointer fields (e.g., `*string`, `*int64` in `ApiKey`):
    - Values are considered equal if **both pointers are `nil`**, or if
      **both are non-`nil` and the underlying values are equal**
      (for example, `*a == *b`).
    - Any other combination (one `nil`, one non-`nil`, or different
      underlying values) is treated as a mismatch.
  - For string fields (including `Configuration` and `Volumes`), values are
    compared using simple string equality. If these columns contain JSON, the
    initial implementation compares them as raw strings; if this produces
    noisy mismatches in practice, the comparator can be extended in the
    future to parse and compare JSON structures instead.
- **Timestamp tracking**: Record when discrepancies are detected and how long
  the comparison took.

### 4. Reporter (`reporter.go`)
Logging and metrics:
- Structured JSON logging for discrepancies and operational errors:
  - Log each successful comparison as a structured `ComparisonReport`.
  - When a comparison is skipped due to refresh or initialization failures,
    log a structured error event (including which cache failed and why) so
    operators can distinguish infrastructure issues from true data mismatches.
- Summary statistics
- Detailed mismatch logging with safety limits:
  - When logging `ValueMismatches`, include at most
    `MAX_DETAILED_MISMATCHES` detailed entries per comparison run (default:
    100), but always log the **total** mismatch count.
  - This prevents log flooding in the event of widespread discrepancies
    while still surfacing the overall severity.
- Optional webhook/alerting integration

### 5. Main Entry Point (`main.go`)
- Initialize connections
- Create both caches
- Start periodic comparison loop
- Handle graceful shutdown

## Comparison Report Structure

```go
type ComparisonReport struct {
    Timestamp          time.Time
    SnowflakeCount     int
    PostgresCount      int
    CountMatch         bool
    MissingInSnowflake []string   // Keys in Postgres but not Snowflake
    MissingInPostgres  []string   // Keys in Snowflake but not Postgres
    ValueMismatches    []ValueMismatch
    DurationMs         int64
}

type ValueMismatch struct {
    Key        string
    Field      string
    Snowflake  interface{}
    Postgres   interface{}
}
```

## Directory Structure

```
example-service/
├── DESIGN.md           # This document
├── go.mod              # Module definition with both dependencies
├── go.sum              # Dependency checksums
├── main.go             # Entry point
├── config/
│   └── config.go       # Configuration loading
├── models/
│   └── apikey.go       # Shared data model
├── cache/
│   └── manager.go      # Cache initialization and management
├── comparator/
│   └── comparator.go   # Comparison logic
├── reporter/
│   └── reporter.go     # Logging and reporting
└── README.md           # Usage instructions
```

## Developer Tasks

### Task 1: Project Setup
- [ ] Create `go.mod` with both library dependencies
- [ ] Set up directory structure
- [ ] Create shared data model

### Task 2: Configuration Module
- [ ] Implement environment variable loading
- [ ] Add validation for required settings
- [ ] Support default values

### Task 3: Cache Manager
- [ ] Initialize Snowflake connection using `database/sql`
- [ ] Initialize PostgreSQL connection using `pgxpool`
- [ ] Create both cache instances with same parameters
- [ ] Handle connection errors gracefully

### Task 4: Comparison Engine
- [ ] Implement count comparison
- [ ] Implement key set comparison
- [ ] Implement value-by-value field comparison
- [ ] Generate structured comparison reports

### Task 5: Reporter/Logger
- [ ] Implement structured JSON logging
- [ ] Log comparison results every interval
- [ ] Log summary statistics
- [ ] Optional: Add webhook notifications for discrepancies

### Task 6: Main Loop
- [ ] Parse configuration
- [ ] Initialize both caches
- [ ] Run comparison on configurable interval (default 5 minutes)
- [ ] Implement graceful shutdown (SIGTERM/SIGINT handling)

### Task 7: Testing
- [ ] Unit tests for comparator logic
- [ ] Integration tests with mock caches
- [ ] Documentation for running the service

## go.mod Example

```go
module github.com/transactrx/snowflake-cache/example-service

go 1.24

require (
    github.com/transactrx/snowflake-cache v0.0.0
    github.com/transactrx/db-cache v0.0.0
    github.com/jackc/pgx/v5 v5.7.5
    github.com/snowflakedb/gosnowflake v1.17.0
)

// Use local versions during development
replace github.com/transactrx/snowflake-cache => ../
replace github.com/transactrx/db-cache => /Users/sakin/Documents/GitHub/db-cache
```

## SQL Queries

Both caches should use equivalent SQL queries:

**Snowflake:**
```sql
SELECT
    KEY AS "key",
    NAME AS "name",
    DESCRIPTION AS "description",
    CLIENT_ID AS "client_id",
    CONFIGURATION AS "configuration",
    VOLUMES AS "volumes",
    MAX_DAILY_RATE AS "max_daily_rate"
FROM MY_DATABASE.MY_SCHEMA.API_KEYS
```

**PostgreSQL:**
```sql
SELECT
    key,
    name,
    description,
    client_id,
    configuration,
    volumes,
    max_daily_rate
FROM api_keys
```

## Sample Output

```
2024-01-15T10:00:00Z [INFO] Starting cache comparison...
2024-01-15T10:00:01Z [INFO] Comparison complete:
  - Snowflake count: 1523
  - PostgreSQL count: 1523
  - Count match: true
  - Missing in Snowflake: 0
  - Missing in PostgreSQL: 0
  - Value mismatches: 0
  - Duration: 245ms

2024-01-15T10:05:00Z [INFO] Starting cache comparison...
2024-01-15T10:05:01Z [WARN] Comparison complete with discrepancies:
  - Snowflake count: 1524
  - PostgreSQL count: 1523
  - Count match: false
  - Missing in Snowflake: 0
  - Missing in PostgreSQL: 1 [key: "api_key_xyz"]
  - Value mismatches: 2
    - Key "api_key_123", Field "name": Snowflake="New Name", Postgres="Old Name"
    - Key "api_key_456", Field "max_daily_rate": Snowflake=1000, Postgres=500
  - Duration: 312ms
```

## Notes

1. **Thread Safety**: Both cache libraries are thread-safe, so comparison can run concurrently with cache refreshes.

2. **Cache Refresh Timing**: Both caches poll independently. For accurate
   comparisons, **always** force a refresh on both caches immediately before
   each comparison:
   ```go
   if err := snowflakeCache.ForceRefresh(); err != nil {
       // log error and skip this comparison interval
   }
   if err := postgresCache.ForceRefresh(); err != nil {
       // log error and skip this comparison interval
   }
   // Only compare if both refreshes succeeded
   ```
   If either refresh fails, the service should log an error and skip producing
   a `ComparisonReport` for that interval.

3. **Memory Considerations**: Both caches hold full dataset in memory. Ensure the service has adequate memory.

4. **Graceful Shutdown**: Both caches run background goroutines. Implement proper shutdown to avoid resource leaks.

5. **Local Development**: Use `replace` directives in `go.mod` to reference local library copies during development.

6. **Source of Truth During Migration**: During the migration phase, PostgreSQL
   is treated as the canonical source of truth. Interpretation guidelines:
   - `MissingInSnowflake` typically indicates a potential issue with Snowflake
     data, replication, or the Snowflake cache.
   - `MissingInPostgres` may be expected once new writes begin targeting
     Snowflake only and should be interpreted in that operational context.

