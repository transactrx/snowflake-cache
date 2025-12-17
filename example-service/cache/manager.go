package cache

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/snowflakedb/gosnowflake"

	"github.com/transactrx/snowflake-cache/example-service/config"
	"github.com/transactrx/snowflake-cache/example-service/models"

	dbcache "github.com/transactrx/db-cache/pkg/db-cache"
	snowflakecache "github.com/transactrx/snowflake-cache/pkg/snowflake-cache"
)

// CacheManager manages both Snowflake and PostgreSQL caches.
type CacheManager struct {
	SnowflakeCache snowflakecache.DbCache[models.ApiKey]
	PostgresCache  *dbcache.DbCache[models.ApiKey]

	snowflakeDB *sql.DB
	postgresDB  *pgxpool.Pool

	logger *log.Logger
}

// NewCacheManager creates and initializes both caches.
// Returns an error if either database connection or cache initialization fails.
func NewCacheManager(cfg *config.Config, logger *log.Logger) (*CacheManager, error) {
	if logger == nil {
		logger = log.Default()
	}

	manager := &CacheManager{
		logger: logger,
	}

	// Initialize Snowflake connection
	logger.Println("Connecting to Snowflake...")
	snowflakeDB, err := sql.Open("snowflake", cfg.SnowflakeDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to open Snowflake connection: %w", err)
	}

	// Test the Snowflake connection
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := snowflakeDB.PingContext(ctx); err != nil {
		snowflakeDB.Close()
		return nil, fmt.Errorf("failed to ping Snowflake: %w", err)
	}
	manager.snowflakeDB = snowflakeDB
	logger.Println("Snowflake connection established")

	// Initialize PostgreSQL connection
	logger.Println("Connecting to PostgreSQL...")
	postgresDB, err := pgxpool.New(context.Background(), cfg.PostgresDSN)
	if err != nil {
		snowflakeDB.Close()
		return nil, fmt.Errorf("failed to create PostgreSQL pool: %w", err)
	}

	// Test the PostgreSQL connection
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	if err := postgresDB.Ping(ctx2); err != nil {
		snowflakeDB.Close()
		postgresDB.Close()
		return nil, fmt.Errorf("failed to ping PostgreSQL: %w", err)
	}
	manager.postgresDB = postgresDB
	logger.Println("PostgreSQL connection established")

	// Create Snowflake cache
	logger.Println("Initializing Snowflake cache...")
	sfCache, err := snowflakecache.CreateCache[models.ApiKey](
		logger,
		cfg.SnowflakeSQL,
		cfg.MonitoredTables,
		cfg.KeyField,
		cfg.CacheCheckInterval,
		snowflakeDB,
		cfg.SnowflakeDatabaseSchema,
	)
	if err != nil {
		manager.Close()
		return nil, fmt.Errorf("failed to create Snowflake cache: %w", err)
	}
	manager.SnowflakeCache = sfCache
	logger.Println("Snowflake cache initialized")

	// Create PostgreSQL cache
	logger.Println("Initializing PostgreSQL cache...")
	pgCache, err := dbcache.CreateCache[models.ApiKey](
		logger,
		cfg.PostgresSQL,
		cfg.MonitoredTables,
		cfg.KeyField,
		cfg.CacheCheckInterval,
		postgresDB,
		postgresDB, // DB_RW same as DB for PostgreSQL
	)
	if err != nil {
		manager.Close()
		return nil, fmt.Errorf("failed to create PostgreSQL cache: %w", err)
	}
	manager.PostgresCache = pgCache
	logger.Println("PostgreSQL cache initialized")

	return manager, nil
}

// ForceRefreshBoth forces a refresh on both caches.
// Returns errors for each cache that fails to refresh.
func (m *CacheManager) ForceRefreshBoth() (sfErr, pgErr error) {
	sfErr = m.SnowflakeCache.ForceRefresh()
	pgErr = m.PostgresCache.ForceRefresh()
	return sfErr, pgErr
}

// Close closes all database connections.
func (m *CacheManager) Close() {
	if m.snowflakeDB != nil {
		m.snowflakeDB.Close()
	}
	if m.postgresDB != nil {
		m.postgresDB.Close()
	}
}

