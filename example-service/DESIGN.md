# Cache Comparison Service - Design Document

## Overview

This service compares cache functionality between two database cache libraries:
- **snowflake-cache** (`github.com/transactrx/snowflake-cache`) - Snowflake-backed cache
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
- `SNOWFLAKE_DSN` - Snowflake connection string
- `POSTGRES_DSN` - PostgreSQL connection string
- `SNOWFLAKE_DATABASE_SCHEMA` - e.g., "MY_DATABASE.MY_SCHEMA"
- `COMPARISON_INTERVAL` - Interval between comparisons (default: 5 minutes)
- `LOG_LEVEL` - Logging verbosity (debug, info, warn, error)

### 2. Cache Manager (`cache_manager.go`)
Responsible for:
- Initializing both Snowflake and PostgreSQL caches
- Managing cache lifecycle
- Providing unified access to both caches

### 3. Comparison Engine (`comparator.go`)
Comparison logic:
- **Count comparison**: Total items in each cache
- **Key comparison**: Keys present in one cache but not the other
- **Value comparison**: For matching keys, compare field values
- **Timestamp tracking**: Record when discrepancies are detected

### 4. Reporter (`reporter.go`)
Logging and metrics:
- Structured JSON logging for discrepancies
- Summary statistics
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

2. **Cache Refresh Timing**: Both caches poll independently. Consider forcing a refresh on both before comparison for consistency:
   ```go
   snowflakeCache.ForceRefresh()
   postgresCache.ForceRefresh()
   // Then compare
   ```

3. **Memory Considerations**: Both caches hold full dataset in memory. Ensure the service has adequate memory.

4. **Graceful Shutdown**: Both caches run background goroutines. Implement proper shutdown to avoid resource leaks.

5. **Local Development**: Use `replace` directives in `go.mod` to reference local library copies during development.

