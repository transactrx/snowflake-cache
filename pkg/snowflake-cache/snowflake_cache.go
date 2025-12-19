package snowflakecache

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/georgysavva/scany/v2/sqlscan"
)

// DefaultLogSchema is the canonical Snowflake schema where CACHE_LOG lives.
// All services using this library share this schema for change tracking,
// while their actual data tables may reside in different schemas.
// This allows a single CACHE_LOG / REGISTERCACHETABLE setup to support many
// different application schemas without requiring each caller to supply the
// log schema explicitly.
const DefaultLogSchema = "DB_CACHE"

// DbCache is the public interface that defines the contract for cache operations.
// This interface is implemented by the Snowflake cache implementation.
type DbCache[T any] interface {
	Get(string) []T
	GetAll() []T
	ForceRefresh() error
}

// SnowflakeTable identifies a table in Snowflake by schema and name.
// It is used to list which tables should invalidate the cache when they change.
type SnowflakeTable struct {
	Schema string
	Table  string
}

// dbCache implements an in-memory cache backed by a Snowflake data source
// and a persistent change signal stored in <signalSchema>.DB_CACHE_LOG.
//
// The cache periodically polls DB_CACHE_LOG to compute a staleness fingerprint.
// If the fingerprint differs from the last seen value, it reloads the dataset
// using the provided SQL and rebuilds an index of key -> []T.
type dbCache[T any] struct {
	mutex           sync.RWMutex
	db              any
	keyCache        map[string][]T
	monitoredTables []string
	fingerprintTableNames []string
	loadSQL         string
	sqlParameters   []any
	keyField        string
	staleCheckVal   *string
	logger          *log.Logger
	logSchema       string
	logDatabase     string
}

// Get returns the cached slice associated with the given key, or nil if missing.
func (c *dbCache[T]) Get(key string) []T {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if val, ok := c.keyCache[key]; ok {
		return val
	}
	return nil
}

// GetAll flattens and returns all cached rows across all keys.
func (c *dbCache[T]) GetAll() []T {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	var result []T
	for _, val := range c.keyCache {
		result = append(result, val...)
	}
	return result
}

// ForceRefresh clears the last fingerprint and forces a reload at once.
func (c *dbCache[T]) ForceRefresh() error {
	c.mutex.Lock()
	c.staleCheckVal = nil
	c.mutex.Unlock()

	fp, err := c.getDbStaleCheckValue()
	if err != nil {
		return err
	}
	return c.loadCache(fp)
}

// getDbStaleCheckValue builds and executes the fingerprint query over DB_CACHE_LOG
// for the configured set of monitored tables.
func (c *dbCache[T]) getDbStaleCheckValue() (*string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Build fully-qualified CACHE_LOG reference
	logTable := "CACHE_LOG"
	if c.logSchema != "" && c.logDatabase != "" {
		logTable = fmt.Sprintf("%s.%s.CACHE_LOG", strings.ToUpper(c.logDatabase), strings.ToUpper(c.logSchema))
	} else if c.logSchema != "" {
		logTable = fmt.Sprintf("%s.CACHE_LOG", strings.ToUpper(c.logSchema))
	}

	// Generate base SQL (uses unqualified CACHE_LOG) and then qualify it
	base := generateStaleCheckSQL(c.monitoredTables)
	q := strings.ReplaceAll(base, "CACHE_LOG", logTable)

	// Build bind args (one per monitored table).
	// Important: The heartbeat writes fully qualified table names into CACHE_LOG
	// (DB.SCHEMA.TABLE). We must match that format when querying by TABLE_NAME,
	// otherwise we would never see updates.
	//
	// Prefer the precomputed fingerprintTableNames (fully-qualified table names)
	// when available so that the schema used in TABLE_NAME can differ from the
	// schema where CACHE_LOG itself resides. This is the common production
	// pattern where application data lives in one schema (e.g. DATA) and
	// CACHE_LOG lives in a dedicated schema (e.g. DB_CACHE).
	var args []any
	if len(c.fingerprintTableNames) > 0 {
		args = make([]any, 0, len(c.fingerprintTableNames))
		for _, fqn := range c.fingerprintTableNames {
			args = append(args, fqn)
		}
	} else {
		// Backwards-compatible path: fall back to constructing identifiers from
		// the monitored table names and the configured logDatabase/logSchema.
		args = make([]any, 0, len(c.monitoredTables))
		for _, t := range c.monitoredTables {
			var tableIdentifier string
			switch {
			case c.logDatabase != "" && c.logSchema != "":
				tableIdentifier = fmt.Sprintf("%s.%s.%s", strings.ToUpper(c.logDatabase), strings.ToUpper(c.logSchema), strings.ToUpper(t))
			case c.logSchema != "":
				tableIdentifier = fmt.Sprintf("%s.%s", strings.ToUpper(c.logSchema), strings.ToUpper(t))
			default:
				tableIdentifier = strings.ToUpper(t)
			}
			args = append(args, tableIdentifier)
		}
	}

	// Scan result
	if len(c.monitoredTables) == 1 {
		row := c.db.(*sql.DB).QueryRowContext(ctx, q, args...)
		var v string
		if err := row.Scan(&v); err != nil {
			return nil, err
		}
		return &v, nil
	}
	row := c.db.(*sql.DB).QueryRowContext(ctx, q, args...)
	var v sql.NullString
	if err := row.Scan(&v); err != nil {
		return nil, err
	}
	if v.Valid {
		val := v.String
		return &val, nil
	}
	return nil, fmt.Errorf("fingerprint query returned NULL")
}

// loadCache executes the load SQL, rebuilds the in-memory index, and
// stores the new fingerprint.
func (c *dbCache[T]) loadCache(staleCheckVal *string) error {
	if c.staleCheckVal != nil && *c.staleCheckVal == *staleCheckVal {
		c.logger.Printf("Cache is already up to date..")
		return nil
	}
	c.logger.Printf("Loading cache %s by %s\n", c.monitoredTables, c.keyField)

	var result []T
	if err := sqlscan.Select(context.Background(), c.db.(*sql.DB), &result, c.loadSQL, c.sqlParameters...); err != nil {
		return err
	}

	newMap := make(map[string][]T)
	for _, row := range result {
		key, err := extractKeyValue(row, c.keyField)
		if err != nil {
			return err
		}
		newMap[key] = append(newMap[key], row)
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.keyCache = newMap
	c.staleCheckVal = staleCheckVal
	return nil
}

// CreateCache creates a Snowflake-backed cache using the unified interface signature.
// This function maintains compatibility with the original db-cache CreateCache signature
// while being specific to Snowflake databases.
//
// Parameters:
//   - logger: optional logger; when nil, a default logger to stdout is used
//   - SQL: SELECT query to load the dataset of type T
//   - monitoredTables: table names as "SCHEMA.TABLE" or plain "TABLE" (uses defaultSchema from DB_RW)
//   - keyField: exported struct field name on T used as the cache key (string or *string)
//   - cacheCheckInterval: how frequently to poll DB_CACHE_LOG for changes
//   - DB: must be a *sql.DB connection using the gosnowflake driver
//   - DB_RW: for Snowflake, this should be a string in "DATABASE.SCHEMA" format or just "SCHEMA"
//   - SQLParams: optional bind parameters for the SQL query
//
// Returns:
//   - DbCache[T]: the cache interface instance
//   - error: any error encountered during cache creation
func CreateCache[T any](
	logger *log.Logger,
	SQL string,
	monitoredTables []string,
	keyField string,
	cacheCheckInterval time.Duration,
	DB any,
	DB_RW any,
	SQLParams ...interface{},
) (DbCache[T], error) {
	// Validate DB is a *sql.DB (Snowflake connection)
	sfDB, ok := DB.(*sql.DB)
	if !ok {
		return nil, fmt.Errorf("unsupported DB type: expected *sql.DB for Snowflake, got %T", DB)
	}

	// Parse DB_RW to extract database and default data schema.
	// Callers typically pass "DATABASE.SCHEMA" where:
	//   - DATABASE: holds both CACHE_LOG and the application tables
	//   - SCHEMA:   holds the application tables (e.g., DATA)
	var database, defaultSchema string
	if s, ok := DB_RW.(string); ok {
		// Parse "DATABASE.SCHEMA" format or just "SCHEMA"
		parts := strings.Split(s, ".")
		if len(parts) == 2 {
			database = parts[0]
			defaultSchema = parts[1]
		} else {
			defaultSchema = s
		}
	} else {
		return nil, fmt.Errorf("DB_RW must be a string for Snowflake (format: 'DATABASE.SCHEMA' or 'SCHEMA'), got %T", DB_RW)
	}

	// Use the canonical log schema for CACHE_LOG regardless of where the
	// application tables live. This decouples the change-log schema from the
	// data schema so that many services (and schemas) can share a single
	// CACHE_LOG / REGISTERCACHETABLE setup.
	logSchema := DefaultLogSchema

	// Normalize monitored tables into schema/table pairs
	qualified := make([]SnowflakeTable, 0, len(monitoredTables))
	for _, name := range monitoredTables {
		parts := strings.Split(name, ".")
		if len(parts) == 2 {
			qualified = append(qualified, SnowflakeTable{Schema: parts[0], Table: parts[1]})
		} else {
			qualified = append(qualified, SnowflakeTable{Schema: defaultSchema, Table: name})
		}
	}

	// Use CreateSnowflakeCacheQualified to maintain the same behavior as CreateSnowflakeCache
	return CreateSnowflakeCacheQualified[T](
		logger,
		sfDB,
		SQL,
		keyField,
		cacheCheckInterval,
		strings.ToUpper(database),
		strings.ToUpper(logSchema),
		qualified,
		SQLParams...,
	)
}

// CreateSnowflakeCache constructs and starts a Snowflake-backed cache.
// This function provides a convenient way to create a cache with a default schema.
//
// Parameters:
//   - logger: optional logger; when nil, a default logger to stdout is used
//   - SQL: SELECT to load the dataset of type T
//   - monitoredTables: names as "SCHEMA.TABLE" or plain "TABLE" (uses defaultSchema)
//   - keyField: exported struct field name on T used as the cache key (string or *string)
//   - checkInterval: how frequently to poll DB_CACHE_LOG for changes
//   - db: an initialized *sql.DB using the gosnowflake driver
//   - signalSchema: schema where DB_CACHE_LOG resides (e.g., "UTILS")
//   - defaultSchema: schema applied to unqualified monitored table names
//   - sqlParams: optional bind parameters for SQL
func CreateSnowflakeCache[T any](
	logger *log.Logger,
	SQL string,
	monitoredTables []string,
	keyField string,
	checkInterval time.Duration,
	db any,
	defaultSchema string,
	sqlParams ...any,
) (DbCache[T], error) {
	if SQL == "" {
		return nil, fmt.Errorf("loadSQL must not be empty")
	}
	if len(monitoredTables) == 0 {
		return nil, fmt.Errorf("monitoredTables must contain at least one table")
	}
	// Normalize monitored tables using provided defaultSchema
	qualified := make([]SnowflakeTable, 0, len(monitoredTables))
	for _, name := range monitoredTables {
		parts := strings.Split(name, ".")
		if len(parts) == 2 {
			qualified = append(qualified, SnowflakeTable{Schema: parts[0], Table: parts[1]})
		} else {
			qualified = append(qualified, SnowflakeTable{Schema: defaultSchema, Table: name})
		}
	}
	// Use the canonical DefaultLogSchema for CACHE_LOG so that callers do not
	// need to specify a separate log schema. Application tables still use the
	// provided defaultSchema for their own schema resolution.
	return CreateSnowflakeCacheQualified[T](
		logger,
		db,
		SQL,
		keyField,
		checkInterval,
		"",
		DefaultLogSchema,
		qualified,
		sqlParams...,
	)
}

// CreateCacheWithDatabase allows specifying both database and schema for CACHE_LOG location
func CreateCacheWithDatabase[T any](
	logger *log.Logger,
	SQL string,
	monitoredTables []string,
	keyField string,
	checkInterval time.Duration,
	db any,
	database string,
	defaultSchema string,
	sqlParams ...any,
) (DbCache[T], error) {
	if SQL == "" {
		return nil, fmt.Errorf("loadSQL must not be empty")
	}
	if len(monitoredTables) == 0 {
		return nil, fmt.Errorf("monitoredTables must contain at least one table")
	}
	// Normalize into schema/table pairs
	qualified := make([]SnowflakeTable, 0, len(monitoredTables))
	for _, name := range monitoredTables {
		parts := strings.Split(name, ".")
		if len(parts) == 2 {
			qualified = append(qualified, SnowflakeTable{Schema: parts[0], Table: parts[1]})
		} else {
			qualified = append(qualified, SnowflakeTable{Schema: defaultSchema, Table: name})
		}
	}
	cache, err := CreateSnowflakeCacheQualified[T](
		logger,
		db,
		SQL,
		keyField,
		checkInterval,
		strings.ToUpper(database),
		strings.ToUpper(DefaultLogSchema),
		qualified,
		sqlParams...,
	)
	if err != nil {
		return nil, err
	}
	return cache, nil
}

// generateStaleCheckSQL builds the fingerprint query for the given monitored tables
// using Snowflake SQL dialect. It intentionally references CACHE_LOG without schema,
// and callers should replace CACHE_LOG with a fully qualified name when needed.
func generateStaleCheckSQL(monitoredTables []string) string {
	if len(monitoredTables) == 1 {
		return "SELECT COUNT(*) || TO_VARCHAR(COALESCE(MAX(update_time), TO_TIMESTAMP_TZ('1980-01-01'))) AS ct FROM CACHE_LOG WHERE table_name = ?"
	}
	var b strings.Builder
	b.WriteString("SELECT LISTAGG(ct, ', ') FROM (")
	for i := 0; i < len(monitoredTables); i++ {
		b.WriteString("SELECT COUNT(*) || TO_VARCHAR(COALESCE(MAX(update_time), TO_TIMESTAMP_TZ('1980-01-01'))) AS ct FROM CACHE_LOG WHERE table_name = ? ")
		if i < len(monitoredTables)-1 {
			b.WriteString(" UNION ALL ")
		} else {
			b.WriteString(") AS t")
		}
	}
	return b.String()
}

// registerStreamsForTables calls the Snowflake REGISTERCACHETABLE procedure
// for each monitored table, to create per-table Streams used by the heartbeat.
// Procedure signature: REGISTERCACHETABLE(DB_NAME, SCHEMA_NAME, TABLE_NAME)
// Failures are logged and ignored so the cache can still function.
func registerStreamsForTables(db *sql.DB, logger *log.Logger, logDatabase, logSchema string, tables []SnowflakeTable) {
	if logSchema == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var procFQN string
	if logDatabase != "" {
		procFQN = fmt.Sprintf("%s.%s.REGISTERCACHETABLE", logDatabase, logSchema)
	} else {
		procFQN = fmt.Sprintf("%s.REGISTERCACHETABLE", logSchema)
	}

	for _, t := range tables {
		schema := strings.ToUpper(t.Schema)
		table := strings.ToUpper(t.Table)
		database := strings.ToUpper(logDatabase)
		if database == "" {
			// If no database specified, try to infer from schema or use empty
			database = ""
		}
		call := fmt.Sprintf("CALL %s(?, ?, ?)", procFQN)
		if _, err := db.ExecContext(ctx, call, database, schema, table); err != nil {
			logger.Printf("warning: could not register stream for %s.%s.%s via %s: %v (continuing without auto-refresh)", database, schema, table, procFQN, err)
		} else {
			logger.Printf("registered stream for %s.%s.%s via %s", database, schema, table, procFQN)
		}
	}
}

// CreateSnowflakeCacheQualified constructs a Snowflake-backed cache when you already
// have schema-qualified monitored table descriptors. Prefer CreateSnowflakeCache for
// migration-friendly parameter ordering.
func CreateSnowflakeCacheQualified[T any](
	logger *log.Logger,
	db any,
	loadSQL string,
	keyField string,
	checkInterval time.Duration,
	logDatabase string,
	logSchema string,
	monitoredTables []SnowflakeTable,
	sqlParams ...any,
) (DbCache[T], error) {
	if db == nil {
		return nil, fmt.Errorf("db must not be nil")
	}
	if loadSQL == "" {
		return nil, fmt.Errorf("loadSQL must not be empty")
	}
	if keyField == "" {
		return nil, fmt.Errorf("keyField must not be empty")
	}
	if len(monitoredTables) == 0 {
		return nil, fmt.Errorf("monitoredTables must contain at least one table")
	}
	if logger == nil {
		logger = log.New(os.Stdout, "sf_cache ", log.Lshortfile|log.Ltime)
	}

	// Convert to []string for internal storage (store unqualified TABLE names for cache indexing and logging)
	tbls := make([]string, 0, len(monitoredTables))
	for _, t := range monitoredTables {
		if t.Table != "" {
			tbls = append(tbls, strings.ToUpper(t.Table))
		}
	}
	// Build fingerprint table names (FQN) if we know the database; used only for CACHE_LOG lookups
	var fqnTables []string
	if logDatabase != "" {
		for _, t := range monitoredTables {
			if t.Table == "" {
				continue
			}
			schema := strings.ToUpper(t.Schema)
			table := strings.ToUpper(t.Table)
			fqn := fmt.Sprintf("%s.%s.%s", strings.ToUpper(logDatabase), schema, table)
			fqnTables = append(fqnTables, fqn)
		}
	}

	cache := &dbCache[T]{
		db:              db,
		loadSQL:         loadSQL,
		sqlParameters:   sqlParams,
		keyField:        keyField,
		monitoredTables: tbls,
		fingerprintTableNames: fqnTables,
		logger:          logger,
		keyCache:        make(map[string][]T),
	}
	cache.logSchema = strings.ToUpper(logSchema)
	cache.logDatabase = strings.ToUpper(logDatabase)

	// Best-effort provisioning of Streams via REGISTER_TABLE (opt-in via env).
	// Enable by setting DB_CACHE_SF_REGISTER_STREAMS=true in the environment.
	if sfDB, ok := db.(*sql.DB); ok && strings.EqualFold(os.Getenv("DB_CACHE_SF_REGISTER_STREAMS"), "true") {
		registerStreamsForTables(sfDB, cache.logger, cache.logDatabase, cache.logSchema, monitoredTables)
	}

	// Initial load
	fp, err := cache.getDbStaleCheckValue()
	if err != nil {
		return nil, fmt.Errorf("failed to get initial fingerprint: %w", err)
	}
	if err := cache.loadCache(fp); err != nil {
		return nil, fmt.Errorf("failed to perform initial load: %w", err)
	}

	// Start background poller for automatic cache refresh
	go func() {
		for now := range time.Tick(checkInterval) {
			staleCheckVal, err := cache.getDbStaleCheckValue()
			if err != nil {
				cache.logger.Printf("Error in cache monitor: %v", err)
			} else {
				cache.logger.Printf("time to reload cache: %s", now.String())
				if err := cache.loadCache(staleCheckVal); err != nil {
					cache.logger.Printf("error while reloading cache: %v", err)
				}
			}
		}
	}()

	return cache, nil
}

// extractKeyValue returns a string value from the named exported struct field.
// The field may be of type string or *string. When pointer, it must be non-nil.
func extractKeyValue(obj any, keyField string) (string, error) {
	v := reflect.ValueOf(obj)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if !v.IsValid() {
		return "", fmt.Errorf("invalid value for key extraction")
	}
	if v.Kind() == reflect.Map {
		return "", fmt.Errorf("map types are not supported for key extraction")
	}

	// Find struct field by name, case-insensitive
	t := v.Type()
	var f reflect.Value
	found := false
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if strings.EqualFold(sf.Name, keyField) {
			f = v.Field(i)
			found = true
			break
		}
	}
	if !found || !f.IsValid() {
		return "", fmt.Errorf("field '%s' not found on cached type", keyField)
	}
	if f.Kind() == reflect.Pointer {
		if f.IsNil() {
			return "", fmt.Errorf("key field '%s' is nil", keyField)
		}
		f = f.Elem()
	}
	if f.Kind() != reflect.String {
		return "", fmt.Errorf("key field '%s' must be string or *string", keyField)
	}
	return f.String(), nil
}
