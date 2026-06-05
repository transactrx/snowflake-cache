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
// It is created in the same database as the monitored tables.
const DefaultLogSchema = "DB_CACHE"

func parseDatabaseSchema(value string) (string, string, error) {
	if value == "" {
		return "", "", nil
	}
	parts := strings.Split(value, ".")
	switch len(parts) {
	case 1:
		return "", parts[0], nil
	case 2:
		return parts[0], parts[1], nil
	default:
		return "", "", fmt.Errorf("expected SCHEMA or DATABASE.SCHEMA, got: %q", value)
	}
}

func normalizeMonitoredTables(monitoredTables []string, defaultDatabase, defaultSchema string) ([]SnowflakeTable, string, error) {
	qualified := make([]SnowflakeTable, 0, len(monitoredTables))
	var database string
	for _, name := range monitoredTables {
		parts := strings.Split(name, ".")
		switch len(parts) {
		case 3:
			dbName, schema, table := parts[0], parts[1], parts[2]
			if dbName == "" || schema == "" || table == "" {
				return nil, "", fmt.Errorf("invalid monitored table name: %q", name)
			}
			if database != "" && !strings.EqualFold(database, dbName) {
				return nil, "", fmt.Errorf("monitored tables span multiple databases (%s vs %s); use a single database", database, dbName)
			}
			database = dbName
			qualified = append(qualified, SnowflakeTable{Schema: schema, Table: table})
		case 2:
			schema, table := parts[0], parts[1]
			if schema == "" || table == "" {
				return nil, "", fmt.Errorf("invalid monitored table name: %q", name)
			}
			qualified = append(qualified, SnowflakeTable{Schema: schema, Table: table})
		case 1:
			table := parts[0]
			if table == "" {
				return nil, "", fmt.Errorf("invalid monitored table name: %q", name)
			}
			if defaultSchema == "" {
				return nil, "", fmt.Errorf("default schema is required when using unqualified table name: %q", name)
			}
			qualified = append(qualified, SnowflakeTable{Schema: defaultSchema, Table: table})
		default:
			return nil, "", fmt.Errorf("invalid monitored table name: %q", name)
		}
	}

	if defaultDatabase != "" {
		if database != "" && !strings.EqualFold(database, defaultDatabase) {
			return nil, "", fmt.Errorf("monitored tables use database %s but default database is %s; use a single database", database, defaultDatabase)
		}
		database = defaultDatabase
	}

	return qualified, database, nil
}

func currentDatabase(db *sql.DB) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	row := db.QueryRowContext(ctx, "SELECT CURRENT_DATABASE()")
	var name sql.NullString
	if err := row.Scan(&name); err != nil {
		return "", err
	}
	if !name.Valid || name.String == "" {
		return "", fmt.Errorf("current database is empty")
	}
	return strings.ToUpper(name.String), nil
}

func ensureCacheSchemaAndProcedure(db *sql.DB, logDatabase, logSchema string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dbName := strings.ToUpper(logDatabase)
	schemaName := strings.ToUpper(logSchema)
	if schemaName == "" {
		return fmt.Errorf("cache schema name is empty")
	}

	prefix := ""
	dbLabel := "current database"
	if dbName != "" {
		prefix = fmt.Sprintf("%s.", dbName)
		dbLabel = dbName
	}

	schemaQuery := fmt.Sprintf("SELECT COUNT(*) FROM %sINFORMATION_SCHEMA.SCHEMATA WHERE SCHEMA_NAME = ?", prefix)
	var count int
	if err := db.QueryRowContext(ctx, schemaQuery, schemaName).Scan(&count); err != nil {
		return fmt.Errorf("failed to verify cache prerequisites (DB_CACHE schema and REGISTERCACHETABLE procedure); check the README: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("required schema %s not found in %s; please create DB_CACHE schema and REGISTERCACHETABLE procedure as documented in the README before using this library", schemaName, dbLabel)
	}

	procQuery := fmt.Sprintf("SELECT COUNT(*) FROM %sINFORMATION_SCHEMA.PROCEDURES WHERE PROCEDURE_SCHEMA = ? AND PROCEDURE_NAME = ?", prefix)
	if err := db.QueryRowContext(ctx, procQuery, schemaName, "REGISTERCACHETABLE").Scan(&count); err != nil {
		return fmt.Errorf("failed to verify cache prerequisites (DB_CACHE schema and REGISTERCACHETABLE procedure); check the README: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("required procedure %s.REGISTERCACHETABLE not found in %s; please create it as documented in the README before using this library", schemaName, dbLabel)
	}

	return nil
}

// DbCache is the public interface that defines the contract for cache operations.
// This interface is implemented by the Snowflake cache implementation.
type DbCache[T any] interface {
	Get(string) []T
	GetAll() []T
	ForceRefresh() error
	// OnRefreshError registers a handler invoked after each failed background
	// refresh, with the running count of consecutive failures (reset to 0 on the
	// next successful refresh). Register it immediately after construction; the
	// handler is not invoked for the initial synchronous load. A nil handler
	// disables the callback.
	OnRefreshError(handler func(err error, consecutiveFailures int))
}

// SnowflakeTable identifies a table in Snowflake by schema and name.
// It is used to list which tables should invalidate the cache when they change.
type SnowflakeTable struct {
	Schema string // Schema name (e.g., "DATA")
	Table  string // Table name (e.g., "RULE_DATA_PLAN")
}

// dbCache implements an in-memory cache backed by a Snowflake data source
// and a persistent change signal stored in <signalSchema>.DB_CACHE_LOG.
//
// The cache periodically polls DB_CACHE_LOG to compute a staleness fingerprint.
// If the fingerprint differs from the last seen value, it reloads the dataset
// using the provided SQL and rebuilds an index of key -> []T.
type dbCache[T any] struct {
	mutex                 sync.RWMutex
	db                    any
	keyCache              map[string][]T
	monitoredTables       []string
	fingerprintTableNames []string
	loadSQL               string
	sqlParameters         []any
	keyField              string
	staleCheckVal         *string
	logger                *log.Logger
	logSchema             string
	logDatabase           string

	// refreshErrHandler, if set via OnRefreshError, is invoked after each failed
	// background refresh. consecutiveRefreshFailures tracks consecutive background
	// refresh failures (reset to 0 on success). Both are guarded by mutex.
	refreshErrHandler          func(err error, consecutiveFailures int)
	consecutiveRefreshFailures int
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

// OnRefreshError registers a handler invoked after each failed background refresh
// (see the DbCache interface). The handler receives the running count of
// consecutive failures so callers can decide when a refresh failure is persistent.
func (c *dbCache[T]) OnRefreshError(handler func(err error, consecutiveFailures int)) {
	c.mutex.Lock()
	c.refreshErrHandler = handler
	c.mutex.Unlock()
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

// refreshOnce performs a single staleness check followed by a conditional reload.
// It is the unit of work executed by the background poller on every tick.
func (c *dbCache[T]) refreshOnce() error {
	staleCheckVal, err := c.getDbStaleCheckValue()
	if err != nil {
		return err
	}
	return c.loadCache(staleCheckVal)
}

// noteRefreshResult records the outcome of a background refresh. A nil error resets
// the consecutive-failure counter; a non-nil error increments it, logs, and invokes
// the OnRefreshError handler (if any) with the running consecutive-failure count so
// the caller can decide when a refresh failure has become persistent.
func (c *dbCache[T]) noteRefreshResult(err error) {
	if err == nil {
		c.mutex.Lock()
		c.consecutiveRefreshFailures = 0
		c.mutex.Unlock()
		return
	}

	c.mutex.Lock()
	c.consecutiveRefreshFailures++
	failures := c.consecutiveRefreshFailures
	handler := c.refreshErrHandler
	c.mutex.Unlock()

	c.logger.Printf("error while refreshing cache (consecutive failures: %d): %v", failures, err)
	if handler != nil {
		handler(err, failures)
	}
}

// CreateCache creates a Snowflake-backed cache using the unified interface signature.
// This function maintains compatibility with the original db-cache CreateCache signature
// while being specific to Snowflake databases.
//
// Parameters:
//   - logger: optional logger; when nil, a default logger to stdout is used
//   - SQL: SELECT query to load the dataset of type T
//   - monitoredTables: table names as "DB.SCHEMA.TABLE", "SCHEMA.TABLE", or plain "TABLE" (uses defaultSchema from DB_RW)
//   - keyField: exported struct field name on T used as the cache key (string, *string, or numeric types)
//   - cacheCheckInterval: how frequently to poll DB_CACHE_LOG for changes
//   - DB: must be a *sql.DB connection using the gosnowflake driver
//   - DB_RW: for Snowflake, this should be a string in "SCHEMA" or "DATABASE.SCHEMA" format
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

	// Parse DB_RW to extract default data schema (and optional database).
	// Callers may pass "SCHEMA" or "DATABASE.SCHEMA".
	var defaultDatabase, defaultSchema string
	if s, ok := DB_RW.(string); ok {
		var err error
		defaultDatabase, defaultSchema, err = parseDatabaseSchema(s)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("DB_RW must be a string for Snowflake (format: 'SCHEMA' or 'DATABASE.SCHEMA'), got %T", DB_RW)
	}

	// Use the canonical log schema for CACHE_LOG; it is created in the same
	// database as the monitored tables.
	logSchema := DefaultLogSchema

	// Normalize monitored tables into schema/table pairs and resolve database.
	qualified, database, err := normalizeMonitoredTables(monitoredTables, defaultDatabase, defaultSchema)
	if err != nil {
		return nil, err
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
//   - monitoredTables: names as "DB.SCHEMA.TABLE", "SCHEMA.TABLE", or plain "TABLE" (uses defaultSchema)
//   - keyField: exported struct field name on T used as the cache key (string, *string, or numeric types)
//   - checkInterval: how frequently to poll DB_CACHE_LOG for changes
//   - db: an initialized *sql.DB using the gosnowflake driver
//   - signalSchema: ignored; DB_CACHE is always used for the cache schema
//   - defaultSchema: schema applied to unqualified monitored table names (can be "DATABASE.SCHEMA")
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
	defaultDatabase, resolvedSchema, err := parseDatabaseSchema(defaultSchema)
	if err != nil {
		return nil, err
	}
	// Normalize monitored tables into schema/table pairs and resolve database.
	qualified, database, err := normalizeMonitoredTables(monitoredTables, defaultDatabase, resolvedSchema)
	if err != nil {
		return nil, err
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
		strings.ToUpper(database),
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
	defaultDatabase, resolvedSchema, err := parseDatabaseSchema(defaultSchema)
	if err != nil {
		return nil, err
	}
	if defaultDatabase != "" && !strings.EqualFold(defaultDatabase, database) {
		return nil, fmt.Errorf("default database %s does not match cache database %s", defaultDatabase, database)
	}
	// Normalize monitored tables into schema/table pairs and resolve database.
	qualified, resolvedDatabase, err := normalizeMonitoredTables(monitoredTables, database, resolvedSchema)
	if err != nil {
		return nil, err
	}
	if resolvedDatabase != "" && !strings.EqualFold(resolvedDatabase, database) {
		return nil, fmt.Errorf("monitored tables use database %s but cache database is %s; use a single database", resolvedDatabase, database)
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
func registerStreamsForTables(db *sql.DB, logger *log.Logger, logDatabase, logSchema string, tables []SnowflakeTable) error {
	if logSchema == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	database := strings.ToUpper(logDatabase)
	if database == "" {
		dbName, err := currentDatabase(db)
		if err != nil {
			return fmt.Errorf("stream registration failed: %w", err)
		}
		database = dbName
	}

	var procFQN string
	if database != "" {
		procFQN = fmt.Sprintf("%s.%s.REGISTERCACHETABLE", database, logSchema)
	} else {
		procFQN = fmt.Sprintf("%s.REGISTERCACHETABLE", logSchema)
	}

	for _, t := range tables {
		schema := strings.ToUpper(t.Schema)
		table := strings.ToUpper(t.Table)
		call := fmt.Sprintf("CALL %s(?, ?, ?)", procFQN)
		row := db.QueryRowContext(ctx, call, database, schema, table)
		var result string
		if err := row.Scan(&result); err != nil {
			logger.Printf("warning: could not register stream for %s.%s.%s via %s: %v (continuing without auto-refresh)", database, schema, table, procFQN, err)
		} else if strings.Contains(result, "Failed") {
			logger.Printf("warning: stream registration failed for %s.%s.%s: %s (continuing without auto-refresh)", database, schema, table, result)
		} else {
			logger.Printf("registered stream for %s.%s.%s via %s", database, schema, table, procFQN)
		}
	}
	return nil
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
	sfDB, ok := db.(*sql.DB)
	if !ok {
		return nil, fmt.Errorf("unsupported DB type: expected *sql.DB for Snowflake, got %T", db)
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

	logSchema = DefaultLogSchema
	logDatabase = strings.ToUpper(logDatabase)
	if logSchema == "" {
		return nil, fmt.Errorf("log schema must not be empty")
	}
	if logDatabase == "" {
		dbName, err := currentDatabase(sfDB)
		if err != nil {
			return nil, fmt.Errorf("could not determine database; specify DATABASE.SCHEMA or use a connection with a default database: %w", err)
		}
		logDatabase = dbName
	}
	if err := ensureCacheSchemaAndProcedure(sfDB, logDatabase, logSchema); err != nil {
		return nil, err
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
	for _, t := range monitoredTables {
		if t.Table == "" {
			continue
		}
		schema := strings.ToUpper(t.Schema)
		table := strings.ToUpper(t.Table)
		fqn := fmt.Sprintf("%s.%s.%s", strings.ToUpper(logDatabase), schema, table)
		fqnTables = append(fqnTables, fqn)
	}

	cache := &dbCache[T]{
		db:                    db,
		loadSQL:               loadSQL,
		sqlParameters:         sqlParams,
		keyField:              keyField,
		monitoredTables:       tbls,
		fingerprintTableNames: fqnTables,
		logger:                logger,
		keyCache:              make(map[string][]T),
	}
	cache.logSchema = logSchema
	cache.logDatabase = logDatabase

	// Best-effort provisioning of Streams via REGISTERCACHETABLE (enabled by default).
	// Disable by setting DB_CACHE_SF_REGISTER_STREAMS=false in the environment.
	if !strings.EqualFold(os.Getenv("DB_CACHE_SF_REGISTER_STREAMS"), "false") {
		if err := registerStreamsForTables(sfDB, cache.logger, cache.logDatabase, cache.logSchema, monitoredTables); err != nil {
			return nil, err
		}
	}

	// Initial load
	fp, err := cache.getDbStaleCheckValue()
	if err != nil {
		return nil, fmt.Errorf("failed to get initial fingerprint: %w", err)
	}
	if err := cache.loadCache(fp); err != nil {
		return nil, fmt.Errorf("failed to perform initial load: %w", err)
	}

	// Start background poller for automatic cache refresh. A persistent failure is
	// surfaced through any handler registered via OnRefreshError; the cache keeps
	// serving the last successfully loaded data until a refresh succeeds.
	go func() {
		for range time.Tick(checkInterval) {
			cache.noteRefreshResult(cache.refreshOnce())
		}
	}()

	return cache, nil
}

// extractKeyValue returns a string value from the named exported struct field.
// The field may be of type string, *string, or numeric types (int, uint, float).
// When pointer, it must be non-nil.
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

	// Convert field value to string based on its type
	switch f.Kind() {
	case reflect.String:
		return f.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%d", f.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("%d", f.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return fmt.Sprintf("%v", f.Float()), nil
	default:
		return "", fmt.Errorf("key field '%s' must be string, numeric, or pointer to those types, got %s", keyField, f.Kind())
	}
}
