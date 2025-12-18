package cache

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	sf "github.com/snowflakedb/gosnowflake"

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

	// Initialize Snowflake connection using private key authentication
	logger.Println("Connecting to Snowflake...")
	snowflakeDB, err := connectSnowflake(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Snowflake: %w", err)
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

// connectSnowflake establishes a connection to Snowflake using private key authentication.
func connectSnowflake(cfg *config.Config, logger *log.Logger) (*sql.DB, error) {
	// Parse the private key
	privateKey, err := parsePrivateKey(cfg.SnowflakePrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Snowflake private key: %w", err)
	}

	// Build Snowflake config - warehouse and role are omitted to use user defaults
	sfCfg := &sf.Config{
		Account:       cfg.SnowflakeAccount,
		User:          cfg.SnowflakeUser,
		Database:      cfg.SnowflakeDatabase,
		Schema:        cfg.SnowflakeSchema,
		Authenticator: sf.AuthTypeJwt,
		PrivateKey:    privateKey,
	}

	// Build DSN from config
	dsn, err := sf.DSN(sfCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build Snowflake DSN: %w", err)
	}

	logger.Printf("Connecting to Snowflake account: %s, database: %s, schema: %s",
		cfg.SnowflakeAccount, cfg.SnowflakeDatabase, cfg.SnowflakeSchema)

	// Open connection
	db, err := sql.Open("snowflake", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open Snowflake connection: %w", err)
	}

	// Test the connection
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping Snowflake: %w", err)
	}

	return db, nil
}

// parsePrivateKey parses a private key from various formats:
// - Base64-encoded PKCS8 DER
// - PEM format (with or without escaped newlines)
func parsePrivateKey(keyData string) (*rsa.PrivateKey, error) {
	// First, try to handle escaped newlines (common in environment variables)
	keyData = strings.ReplaceAll(keyData, "\\n", "\n")

	// Try parsing as PEM first
	block, _ := pem.Decode([]byte(keyData))
	if block != nil {
		return parsePKCS8DER(block.Bytes)
	}

	// Try base64 decoding (for raw base64-encoded PKCS8 DER)
	decoded, err := base64.StdEncoding.DecodeString(keyData)
	if err != nil {
		// Try base64 URL encoding
		decoded, err = base64.URLEncoding.DecodeString(keyData)
		if err != nil {
			// Try raw base64 without padding
			decoded, err = base64.RawStdEncoding.DecodeString(keyData)
			if err != nil {
				return nil, fmt.Errorf("private key is neither valid PEM nor base64: %w", err)
			}
		}
	}

	return parsePKCS8DER(decoded)
}

// parsePKCS8DER parses a PKCS8 DER-encoded private key
func parsePKCS8DER(der []byte) (*rsa.PrivateKey, error) {
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		// Try PKCS1 as fallback
		rsaKey, err2 := x509.ParsePKCS1PrivateKey(der)
		if err2 != nil {
			return nil, fmt.Errorf("failed to parse private key (tried PKCS8 and PKCS1): PKCS8 error: %v, PKCS1 error: %v", err, err2)
		}
		return rsaKey, nil
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not RSA")
	}

	return rsaKey, nil
}
