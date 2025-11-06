package snowflake_test

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"

	sf "github.com/snowflakedb/gosnowflake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	snowflakecache "github.com/transactrx/db-cache/pkg/snowflake-cache"
)

// Test models that match our Snowflake database schema
// Note: Field names must match the database column names for the cache to work
type APIKey struct {
	ID        int       `db:"id"`
	Key       string    `db:"key"`
	Name      string    `db:"name"`
	IsActive  bool      `db:"is_active"`
	CreatedAt time.Time `db:"created_at"`
}

type User struct {
	ID        int       `db:"id"`
	Username  string    `db:"username"`
	Email     string    `db:"email"`
	Role      string    `db:"role"`
	CreatedAt time.Time `db:"created_at"`
}

// Test configuration - these should be set via environment variables
var (
	SNOWFLAKE_ACCOUNT   = os.Getenv("SNOWFLAKE_ACCOUNT")
	SNOWFLAKE_USER      = os.Getenv("SNOWFLAKE_USER")
	SNOWFLAKE_PASSWORD  = os.Getenv("SNOWFLAKE_PASSWORD")
	SNOWFLAKE_DATABASE  = os.Getenv("SNOWFLAKE_DATABASE")
	SNOWFLAKE_SCHEMA    = os.Getenv("SNOWFLAKE_SCHEMA")
	SNOWFLAKE_WAREHOUSE = os.Getenv("SNOWFLAKE_WAREHOUSE")
)

func getTestSnowflakeConnection() (*sql.DB, error) {
	// Defaults
	if SNOWFLAKE_DATABASE == "" {
		SNOWFLAKE_DATABASE = "CPE_DEV"
	}
	if SNOWFLAKE_SCHEMA == "" {
		SNOWFLAKE_SCHEMA = "DB_CACHE"
	}

	cfg := &sf.Config{
		Account: SNOWFLAKE_ACCOUNT,
		User:    SNOWFLAKE_USER,
		// Intentionally omit Database and Schema here to avoid ping errors on missing privileges
		Warehouse: SNOWFLAKE_WAREHOUSE,
		Role:      os.Getenv("SNOWFLAKE_ROLE"),
	}

	if host := os.Getenv("SNOWFLAKE_HOST"); host != "" {
		cfg.Host = host
		cfg.InsecureMode = strings.Contains(host, "localhost") || strings.Contains(host, ":")
	}

	// Prefer key pair auth when a private key is provided
	fmt.Printf("SNOWFLAKE_PRIVATE_KEY len=%d, SNOWFLAKE_PRIVATE_KEY_PATH=%s\n", len(os.Getenv("SNOWFLAKE_PRIVATE_KEY")), os.Getenv("SNOWFLAKE_PRIVATE_KEY_PATH"))
	if pk, err := loadPrivateKeyFromEnv(); err == nil && pk != nil {
		cfg.Authenticator = sf.AuthTypeJwt
		cfg.PrivateKey = pk
	} else {
		// Fallback to password if present
		if SNOWFLAKE_PASSWORD == "" {
			return nil, fmt.Errorf("no private key or password provided for Snowflake auth")
		}
		cfg.Password = SNOWFLAKE_PASSWORD
	}

	dsn, err := sf.DSN(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build DSN: %w", err)
	}
	db, err := sql.Open("snowflake", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open Snowflake connection: %w", err)
	}
	// Do not ping here; some roles fail ping before USE DATABASE/SCHEMA. We'll set context later.
	return db, nil
}

func loadPrivateKeyFromEnv() (*rsa.PrivateKey, error) {
	// Option 1: SNOWFLAKE_PRIVATE_KEY as PEM string (supports \n-escaped newlines)
	if pemStr := os.Getenv("SNOWFLAKE_PRIVATE_KEY"); pemStr != "" {
		pemStr = strings.ReplaceAll(pemStr, "\\n", "\n")
		// First try as direct PEM
		if pk, err := parsePKCS8PEM([]byte(pemStr)); err == nil {
			return pk, nil
		}
		// If not PEM, try base64-decoding then parse PEM/DER
		if decoded, err := base64.StdEncoding.DecodeString(pemStr); err == nil {
			// Try as PEM-encoded text (after base64 decode)
			if pk, err2 := parsePKCS8PEM(decoded); err2 == nil {
				return pk, nil
			}
			// Try as raw PKCS8 DER after base64 decode
			if anyKey, err3 := x509.ParsePKCS8PrivateKey(decoded); err3 == nil {
				if k, ok := anyKey.(*rsa.PrivateKey); ok {
					return k, nil
				}
			}
			// Reference repo approach: base64 decode THEN pem decode THEN parse
			block, _ := pem.Decode(decoded)
			if block != nil && block.Type == "PRIVATE KEY" {
				if anyKey, err4 := x509.ParsePKCS8PrivateKey(block.Bytes); err4 == nil {
					if k, ok := anyKey.(*rsa.PrivateKey); ok {
						return k, nil
					}
				}
			}
		}
		// If still failing, log and continue to file path
		log.Printf("Failed to parse private key from SNOWFLAKE_PRIVATE_KEY env var")
	}
	// Option 2: SNOWFLAKE_PRIVATE_KEY_PATH pointing to a PEM file
	if path := os.Getenv("SNOWFLAKE_PRIVATE_KEY_PATH"); path != "" {
		log.Printf("Loading Snowflake private key from path: %s", path)
		b, err := os.ReadFile(path)
		if err != nil {
			log.Printf("Failed to read private key file: %v", err)
			return nil, err
		}
		pk, err := parsePKCS8PEM(b)
		if err != nil {
			log.Printf("Failed to parse private key from path: %v", err)
			return nil, err
		}
		return pk, nil
	}
	// Option 3: default file name in current working directory
	if _, err := os.Stat("private_key.pem"); err == nil {
		log.Printf("Loading Snowflake private key from default file: private_key.pem")
		if b, err := os.ReadFile("private_key.pem"); err == nil {
			if pk, err2 := parsePKCS8PEM(b); err2 == nil {
				return pk, nil
			} else {
				log.Printf("Failed to parse private key from default file: %v", err2)
			}
		} else {
			log.Printf("Failed to read default private key file: %v", err)
		}
	}
	return nil, fmt.Errorf("no private key in env")
}

func parsePKCS8PEM(b []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM")
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS1 as fallback
		if pkcs1, err2 := x509.ParsePKCS1PrivateKey(block.Bytes); err2 == nil {
			return pkcs1, nil
		}
		return nil, err
	}
	if k, ok := keyAny.(*rsa.PrivateKey); ok {
		return k, nil
	}
	return nil, fmt.Errorf("unsupported private key type")
}

func TestMain(m *testing.M) {
	// Load .env if present (simple parser) but DO NOT override already-set env vars
	if b, err := os.ReadFile(".env"); err == nil {
		lines := strings.Split(string(b), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			// split at first '='
			if i := strings.IndexByte(line, '='); i > 0 {
				k := strings.TrimSpace(line[:i])
				v := strings.TrimSpace(line[i+1:])
				v = strings.Trim(v, "\"'")
				if _, exists := os.LookupEnv(k); !exists {
					os.Setenv(k, v)
				}
			}
		}
	}
	// refresh globals from env (honors any pre-set vars)
	SNOWFLAKE_ACCOUNT = os.Getenv("SNOWFLAKE_ACCOUNT")
	SNOWFLAKE_USER = os.Getenv("SNOWFLAKE_USER")
	SNOWFLAKE_PASSWORD = os.Getenv("SNOWFLAKE_PASSWORD")
	SNOWFLAKE_DATABASE = os.Getenv("SNOWFLAKE_DATABASE")
	SNOWFLAKE_SCHEMA = os.Getenv("SNOWFLAKE_SCHEMA")
	SNOWFLAKE_WAREHOUSE = os.Getenv("SNOWFLAKE_WAREHOUSE")

	// Check if we should skip Snowflake tests
	if os.Getenv("SKIP_SNOWFLAKE_TESTS") == "true" {
		fmt.Println("Skipping Snowflake integration tests (SKIP_SNOWFLAKE_TESTS=true)")
		os.Exit(0)
	}

	// Run tests
	code := m.Run()
	os.Exit(code)
}

func TestSnowflakeCacheIntegration(t *testing.T) {
	// Skip if Snowflake tests are disabled
	if os.Getenv("SKIP_SNOWFLAKE_TESTS") == "true" {
		t.Skip("Snowflake integration tests disabled")
	}

	// Get database connection
	db, err := getTestSnowflakeConnection()
	require.NoError(t, err, "Failed to connect to Snowflake test database")
	defer db.Close()

	// Ensure we can USE the target database and schema (clear diagnostics if not authorized)
	ensureDbAndSchemaContext(t, db)

	// Ensure schema and sample data exist for tests (idempotent)
	setupSnowflakeSchemaAndData(t, db)

	// Create logger for cache
	logger := log.New(os.Stdout, "[SNOWFLAKE_INTEGRATION_TEST] ", log.LstdFlags|log.Lshortfile)

	t.Run("APIKey Cache Operations", func(t *testing.T) {
		// Ensure Go code registers streams via REGISTERCACHETABLE
		prev := os.Getenv("DB_CACHE_SF_REGISTER_STREAMS")
		os.Setenv("DB_CACHE_SF_REGISTER_STREAMS", "true")
		defer os.Setenv("DB_CACHE_SF_REGISTER_STREAMS", prev)
		// Define SQL query to load API keys
		sqlQuery := `
            SELECT 
                ID AS "id",
                KEY AS "key",
                NAME AS "name",
                IS_ACTIVE AS "is_active",
                CREATED_AT AS "created_at"
            FROM ` + SNOWFLAKE_DATABASE + `.` + SNOWFLAKE_SCHEMA + `.API_KEYS 
            WHERE IS_ACTIVE = TRUE 
            ORDER BY CREATED_AT DESC
        `

		// Create cache for API keys using the snowflakecache.CreateCache interface
		// Note: Pass schema as DB_RW parameter for Snowflake - it's a string that will be used as defaultSchema
		cache, err := snowflakecache.CreateCache[APIKey](
			logger,
			sqlQuery,
			[]string{"API_KEYS"}, // monitored tables
			"KEY",                // key field
			2*time.Second,        // check interval
			db,                   // Snowflake DB connection (*sql.DB)
			SNOWFLAKE_DATABASE+"."+SNOWFLAKE_SCHEMA, // For Snowflake: "DATABASE.SCHEMA" format
		)
		require.NoError(t, err, "Failed to create API key cache")

		// Test GetAll - should return all active API keys
		allKeys := cache.GetAll()
		assert.NotEmpty(t, allKeys, "GetAll should return active API keys")
		t.Logf("Retrieved %d active API keys", len(allKeys))

		// Verify we got the expected active keys
		expectedKeys := []string{"api_key_1", "api_key_2", "api_key_4"}
		for _, key := range allKeys {
			assert.Contains(t, expectedKeys, key.Key, "Should only return active keys")
			assert.True(t, key.IsActive, "All returned keys should be active")
		}

		// Test Get with specific key
		key1Data := cache.Get("api_key_1")
		assert.NotEmpty(t, key1Data, "Get should return data for existing key")
		assert.Len(t, key1Data, 1, "Should return exactly one record for unique key")
		assert.Equal(t, "api_key_1", key1Data[0].Key, "Should return correct key")

		// Test Get with non-existent key
		nonExistentData := cache.Get("non_existent_key")
		assert.Empty(t, nonExistentData, "Get should return empty slice for non-existent key")

		// Test ForceRefresh
		err = cache.ForceRefresh()
		assert.NoError(t, err, "ForceRefresh should succeed")

		// Verify data is still there after refresh
		refreshedKeys := cache.GetAll()
		assert.Equal(t, len(allKeys), len(refreshedKeys), "Data should be preserved after refresh")
	})

	t.Run("User Cache Operations", func(t *testing.T) {
		// Ensure Go code registers streams via REGISTERCACHETABLE
		prev := os.Getenv("DB_CACHE_SF_REGISTER_STREAMS")
		os.Setenv("DB_CACHE_SF_REGISTER_STREAMS", "true")
		defer os.Setenv("DB_CACHE_SF_REGISTER_STREAMS", prev)
		// Define SQL query to load users
		sqlQuery := `
            SELECT 
                ID AS "id",
                USERNAME AS "username",
                EMAIL AS "email",
                ROLE AS "role",
                CREATED_AT AS "created_at"
            FROM ` + SNOWFLAKE_DATABASE + `.` + SNOWFLAKE_SCHEMA + `.USERS 
            ORDER BY USERNAME ASC
        `

		// Create cache for users using the snowflakecache.CreateCache interface
		cache, err := snowflakecache.CreateCache[User](
			logger,
			sqlQuery,
			[]string{"USERS"}, // monitored tables
			"USERNAME",        // key field
			2*time.Second,     // check interval
			db,                // Snowflake DB connection (*sql.DB)
			SNOWFLAKE_DATABASE+"."+SNOWFLAKE_SCHEMA, // For Snowflake: "DATABASE.SCHEMA" format
		)
		require.NoError(t, err, "Failed to create user cache")

		// Test GetAll - should return all users
		allUsers := cache.GetAll()
		assert.NotEmpty(t, allUsers, "GetAll should return users")
		t.Logf("Retrieved %d users", len(allUsers))

		// Verify we got the expected users
		expectedUsernames := []string{"alice", "bob", "charlie", "diana"}
		for _, user := range allUsers {
			assert.Contains(t, expectedUsernames, user.Username, "Should return expected users")
		}

		// Test Get with specific username
		aliceData := cache.Get("alice")
		assert.NotEmpty(t, aliceData, "Get should return data for existing user")
		assert.Len(t, aliceData, 1, "Should return exactly one record for unique username")
		assert.Equal(t, "alice", aliceData[0].Username, "Should return correct username")
		assert.Equal(t, "admin", aliceData[0].Role, "Should return correct role")

		// Test Get with non-existent user
		nonExistentData := cache.Get("non_existent_user")
		assert.Empty(t, nonExistentData, "Get should return empty slice for non-existent user")
	})

	t.Run("Cache Auto-Refresh Behavior", func(t *testing.T) {
		// Ensure Go code registers streams via REGISTERCACHETABLE
		prev := os.Getenv("DB_CACHE_SF_REGISTER_STREAMS")
		os.Setenv("DB_CACHE_SF_REGISTER_STREAMS", "true")
		defer os.Setenv("DB_CACHE_SF_REGISTER_STREAMS", prev)

		// Create a dedicated table for this run to avoid stale/previous stream state
		testTable := "API_KEYS_E2E_" + fmt.Sprintf("%d", time.Now().Unix())
		createSQL := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.%s.%s (
				ID INTEGER AUTOINCREMENT,
				KEY VARCHAR(255) UNIQUE NOT NULL,
				NAME VARCHAR(255) NOT NULL,
				IS_ACTIVE BOOLEAN DEFAULT TRUE,
				CREATED_AT TIMESTAMP DEFAULT CURRENT_TIMESTAMP()
			)
		`, SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA, testTable)
		_, err := db.ExecContext(context.Background(), createSQL)
		require.NoError(t, err, "Failed to create test table %s", testTable)
		defer func() {
			_, _ = db.ExecContext(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s.%s.%s", SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA, testTable))
			t.Logf("✓ Dropped test table %s", testTable)
		}()

		// Seed initial rows
		seedSQL := fmt.Sprintf(`INSERT INTO %s.%s.%s (KEY, NAME, IS_ACTIVE) VALUES (?, ?, ?), (?, ?, ?), (?, ?, ?)`,
			SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA, testTable)
		_, err = db.ExecContext(context.Background(), seedSQL,
			"e2e_key_1", "E2E One", true,
			"e2e_key_2", "E2E Two", true,
			"e2e_key_3", "E2E Three", false,
		)
		require.NoError(t, err, "Failed to seed test data")

		// Create a cache against the dedicated table
		sqlQuery := `SELECT ID AS "id", KEY AS "key", NAME AS "name", IS_ACTIVE AS "is_active", CREATED_AT AS "created_at" FROM ` +
			SNOWFLAKE_DATABASE + `.` + SNOWFLAKE_SCHEMA + `.` + testTable + ` WHERE IS_ACTIVE = TRUE`
		cache, err := snowflakecache.CreateCache[APIKey](
			logger,
			sqlQuery,
			[]string{testTable},
			"KEY",
			5*time.Second,
			db,
			SNOWFLAKE_DATABASE+"."+SNOWFLAKE_SCHEMA,
		)
		require.NoError(t, err, "Failed to create cache for auto-refresh test")

		// Allow registration settle
		t.Logf("Waiting briefly for stream registration to settle for %s...", testTable)
		time.Sleep(10 * time.Second)

		initialData := cache.GetAll()
		initialCount := len(initialData)
		t.Logf("Initial cache contains %d records", initialCount)

		// Insert new rows periodically until the Task update is observed
		insertedKeys := []string{}
		insertSQL := fmt.Sprintf(`INSERT INTO %s.%s.%s (KEY, NAME, IS_ACTIVE) VALUES (?, ?, ?)`,
			SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA, testTable)
		insertTime := time.Now()
		firstKey := "test_auto_refresh_key_" + fmt.Sprintf("%d", time.Now().UnixNano())
		_, err = db.ExecContext(context.Background(), insertSQL, firstKey, "E2E Insert 1", true)
		require.NoError(t, err, "Failed to insert test record")
		insertedKeys = append(insertedKeys, firstKey)
		t.Logf("✓ Inserted test record '%s' into %s", firstKey, testTable)

		// Wait for CACHE_LOG UPDATE_TIME on FQN
		maxWaitTime := 3 * time.Minute
		checkInterval := 5 * time.Second
		startTime := time.Now()
		taskUpdatedLog := false
		fqnTable := SNOWFLAKE_DATABASE + "." + SNOWFLAKE_SCHEMA + "." + testTable
		var currentOperationTime sql.NullTime

		for time.Since(startTime) < maxWaitTime {
			checkLogSQL := fmt.Sprintf(`SELECT UPDATE_TIME FROM %s.%s.CACHE_LOG WHERE TABLE_NAME = ?`,
				SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA)
			currentOperationTime = sql.NullTime{}
			err := db.QueryRowContext(context.Background(), checkLogSQL, fqnTable).Scan(&currentOperationTime)
			if err == nil && currentOperationTime.Valid {
				opTime := currentOperationTime.Time
				if opTime.After(insertTime.Add(-10 * time.Second)) {
					taskUpdatedLog = true
					t.Logf("✓ Snowflake Task updated CACHE_LOG for %s (UPDATE_TIME: %v)", fqnTable, opTime)
					break
				}
			}
			// Insert additional row to ensure stream is non-empty
			newKey := "test_auto_refresh_key_" + fmt.Sprintf("%d", time.Now().UnixNano())
			if _, ierr := db.ExecContext(context.Background(), insertSQL, newKey, "E2E Insert (retry)", true); ierr == nil {
				insertedKeys = append(insertedKeys, newKey)
				t.Logf("  Inserted another test record to nudge stream: %s", newKey)
			}
			time.Sleep(checkInterval)
		}
		require.True(t, taskUpdatedLog, "Snowflake Task must update CACHE_LOG within 3 minutes")

		// Wait for cache refresh
		t.Logf("Waiting for cache to detect CACHE_LOG change and refresh...")
		maxCacheWaitTime := 60 * time.Second
		cacheCheckInterval := 2 * time.Second
		cacheRefreshed := false
		cacheRefreshStartTime := time.Now()

		for time.Since(cacheRefreshStartTime) < maxCacheWaitTime {
			refreshedData := cache.GetAll()
			refreshedCount := len(refreshedData)
			if refreshedCount > initialCount {
				for _, k := range insertedKeys {
					if rows := cache.Get(k); len(rows) > 0 {
						cacheRefreshed = true
						t.Logf("✓ Cache automatically refreshed and contains new record %s (count: %d > %d)", k, refreshedCount, initialCount)
						break
					}
				}
				if cacheRefreshed {
					break
				}
			}
			time.Sleep(cacheCheckInterval)
		}
		require.True(t, cacheRefreshed, "Cache must automatically refresh after CACHE_LOG is updated")

		// Clean up rows and table (table is dropped in defer)
		for _, k := range insertedKeys {
			_, _ = db.ExecContext(context.Background(), fmt.Sprintf(`DELETE FROM %s.%s.%s WHERE KEY = ?`, SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA, testTable), k)
		}
		t.Logf("✓ Cleaned up %d test records from %s", len(insertedKeys), testTable)
	})

	t.Run("Error Handling", func(t *testing.T) {
		// Test with invalid SQL
		invalidSQL := `SELECT INVALID_COLUMN FROM ` + SNOWFLAKE_DATABASE + `.` + SNOWFLAKE_SCHEMA + `.NON_EXISTENT_TABLE`

		cache, err := snowflakecache.CreateCache[APIKey](
			logger,
			invalidSQL,
			[]string{"API_KEYS"},
			"KEY",
			2*time.Second,
			db,
			SNOWFLAKE_DATABASE+"."+SNOWFLAKE_SCHEMA,
		)
		assert.Error(t, err, "Should fail with invalid SQL")
		assert.Nil(t, cache, "Cache should be nil on error")

		// Test with invalid key field
		validSQL := `SELECT ID AS "id", KEY AS "key", NAME AS "name", IS_ACTIVE AS "is_active", CREATED_AT AS "created_at" FROM ` + SNOWFLAKE_DATABASE + `.` + SNOWFLAKE_SCHEMA + `.API_KEYS WHERE IS_ACTIVE = TRUE`

		cache, err = snowflakecache.CreateCache[APIKey](
			logger,
			validSQL,
			[]string{"API_KEYS"},
			"INVALID_FIELD", // invalid key field
			2*time.Second,
			db,
			SNOWFLAKE_DATABASE+"."+SNOWFLAKE_SCHEMA,
		)
		assert.Error(t, err, "Should fail with invalid key field")
		assert.Nil(t, cache, "Cache should be nil on error")
	})

	t.Run("REGISTERCACHETABLE Procedure Integration", func(t *testing.T) {
		// This test creates a new table, registers it using REGISTERCACHETABLE procedure,
		// and verifies the cache works with the registered table and stream

		ctx := context.Background()
		testTableName := "TEST_PRODUCTS_" + fmt.Sprintf("%d", time.Now().Unix())

		// Step 1: Create a test table
		createTableSQL := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.%s.%s (
				ID INTEGER AUTOINCREMENT,
				PRODUCT_CODE VARCHAR(50) UNIQUE NOT NULL,
				PRODUCT_NAME VARCHAR(255) NOT NULL,
				PRICE FLOAT NOT NULL,
				IN_STOCK BOOLEAN DEFAULT TRUE,
				CREATED_AT TIMESTAMP DEFAULT CURRENT_TIMESTAMP()
			)
		`, SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA, testTableName)

		_, err := db.ExecContext(ctx, createTableSQL)
		require.NoError(t, err, "Failed to create test table %s", testTableName)
		t.Logf("✓ Created test table: %s", testTableName)

		// Ensure cleanup happens
		defer func() {
			dropSQL := fmt.Sprintf("DROP TABLE IF EXISTS %s.%s.%s", SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA, testTableName)
			db.ExecContext(context.Background(), dropSQL)
			t.Logf("✓ Cleaned up test table: %s", testTableName)
		}()

		// Step 2: Insert test data
		insertSQL := fmt.Sprintf(`
			INSERT INTO %s.%s.%s (PRODUCT_CODE, PRODUCT_NAME, PRICE, IN_STOCK)
			VALUES (?, ?, ?, ?), (?, ?, ?, ?), (?, ?, ?, ?)
		`, SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA, testTableName)

		_, err = db.ExecContext(ctx, insertSQL,
			"PROD001", "Widget A", 29.99, true,
			"PROD002", "Widget B", 39.99, true,
			"PROD003", "Widget C", 49.99, false,
		)
		require.NoError(t, err, "Failed to insert test data")
		t.Logf("✓ Inserted 3 test products")

		// Step 3: Enable Go-side stream registration (do not call the procedure directly)
		prev := os.Getenv("DB_CACHE_SF_REGISTER_STREAMS")
		os.Setenv("DB_CACHE_SF_REGISTER_STREAMS", "true")
		defer os.Setenv("DB_CACHE_SF_REGISTER_STREAMS", prev)
		t.Logf("✓ Enabled DB_CACHE_SF_REGISTER_STREAMS=true; Go cache will register via REGISTERCACHETABLE")

		// Step 4: Create cache for the new table (procedure already handled CACHE_LOG)
		type TestProduct struct {
			ID          int     `db:"id"`
			ProductCode string  `db:"product_code"`
			ProductName string  `db:"product_name"`
			Price       float64 `db:"price"`
			InStock     bool    `db:"in_stock"`
		}

		cacheSQL := fmt.Sprintf(`
			SELECT 
				ID AS "id",
				PRODUCT_CODE AS "product_code",
				PRODUCT_NAME AS "product_name",
				PRICE AS "price",
				IN_STOCK AS "in_stock"
			FROM %s.%s.%s
			WHERE IN_STOCK = TRUE
		`, SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA, testTableName)

		cache, err := snowflakecache.CreateCache[TestProduct](
			logger,
			cacheSQL,
			[]string{testTableName},
			"ProductCode",
			2*time.Second,
			db,
			SNOWFLAKE_DATABASE+"."+SNOWFLAKE_SCHEMA,
		)
		require.NoError(t, err, "Failed to create cache for test table")
		t.Logf("✓ Created cache for %s", testTableName)

		// Give stream registration a brief window to settle before first insert
		t.Logf("Waiting briefly for stream registration to settle for %s...", testTableName)
		time.Sleep(10 * time.Second)

		// Step 5: Verify cache works correctly
		allProducts := cache.GetAll()
		assert.Len(t, allProducts, 2, "Should return 2 in-stock products")
		t.Logf("✓ Cache contains %d in-stock products", len(allProducts))

		// Test Get with specific product code
		prod1 := cache.Get("PROD001")
		if assert.NotEmpty(t, prod1, "Should find PROD001") {
			assert.Equal(t, "PROD001", prod1[0].ProductCode)
			assert.Equal(t, "Widget A", prod1[0].ProductName)
			assert.Equal(t, 29.99, prod1[0].Price)
			t.Logf("✓ Retrieved PROD001: %s ($%.2f)", prod1[0].ProductName, prod1[0].Price)
		}

		// Test that out-of-stock product is not in cache
		prod3 := cache.Get("PROD003")
		assert.Empty(t, prod3, "Should not find out-of-stock PROD003")

		// Step 6: Test auto-refresh by inserting new data
		// The Snowflake Task will detect this change via Streams and update CACHE_LOG automatically
		insertNewSQL := fmt.Sprintf(`
			INSERT INTO %s.%s.%s (PRODUCT_CODE, PRODUCT_NAME, PRICE, IN_STOCK)
			VALUES (?, ?, ?, ?)
		`, SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA, testTableName)

		_, err = db.ExecContext(ctx, insertNewSQL, "PROD004", "Widget D", 59.99, true)
		require.NoError(t, err, "Failed to insert new product")
		insertTime := time.Now()
		t.Logf("✓ Inserted new product PROD004 into %s at %v", testTableName, insertTime)

		// Wait for the Snowflake Task to update CACHE_LOG (runs every 1 minute)
		// Check CACHE_LOG periodically to see when the Task has updated it
		// The Task does a MERGE/UPSERT, so we check if UPDATE_TIME was updated after our insert
		t.Logf("Waiting for Snowflake Task to update CACHE_LOG (runs every 1 minute)...")
		maxWaitTime := 3 * time.Minute // Wait up to 3 minutes for the Task to run
		checkInterval := 5 * time.Second
		startTime := time.Now()
		taskUpdatedLog := false

		fqnTable := SNOWFLAKE_DATABASE + "." + SNOWFLAKE_SCHEMA + "." + testTableName
		for time.Since(startTime) < maxWaitTime {
			// Check if CACHE_LOG UPDATE_TIME was updated after we inserted the record
			// The Task does MERGE which updates existing rows, so we check if UPDATE_TIME > insertTime
			checkLogSQL := fmt.Sprintf(`SELECT UPDATE_TIME FROM %s.%s.CACHE_LOG WHERE TABLE_NAME = ?`,
				SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA)
			var currentOperationTime sql.NullTime
			err := db.QueryRowContext(ctx, checkLogSQL, fqnTable).Scan(&currentOperationTime)
			if err == nil && currentOperationTime.Valid {
				// Convert Snowflake timestamp to Go time for comparison
				opTime := currentOperationTime.Time
				// Check if UPDATE_TIME was updated after we inserted (with 10 second buffer for timing differences)
				// We check if opTime is after (insertTime - 10 seconds) to account for clock differences
				if opTime.After(insertTime.Add(-10 * time.Second)) {
					taskUpdatedLog = true
					t.Logf("✓ Snowflake Task has updated CACHE_LOG for %s (UPDATE_TIME: %v, insert time: %v)", testTableName, opTime, insertTime)
					break
				}
			} else if err == sql.ErrNoRows {
				// CACHE_LOG doesn't have a row yet - Task will create it when it runs
				// Continue waiting
			}
			// If not yet updated, insert another distinct record to ensure the stream has data
			retryCode := "PROD" + fmt.Sprintf("%d", time.Now().UnixNano())[:6]
			if _, ierr := db.ExecContext(ctx, insertNewSQL, retryCode, "Widget Retry", 49.99, true); ierr == nil {
				t.Logf("  Inserted another test product to nudge stream: %s", retryCode)
			} else {
				t.Logf("  Insert retry failed: %v", ierr)
			}

			time.Sleep(checkInterval)
			t.Logf("  Still waiting for Task to update CACHE_LOG... (elapsed: %v)", time.Since(startTime))
		}

		// Require that the Task updated CACHE_LOG - this verifies the Task is working
		require.True(t, taskUpdatedLog, "Snowflake Task must update CACHE_LOG within 2 minutes - this verifies the Task is running correctly")

		// Now wait for the cache to detect the change and refresh (checks every 2 seconds)
		// Give it a few check cycles to pick up the change
		t.Logf("Waiting for cache to detect CACHE_LOG change and refresh...")
		maxCacheWaitTime := 60 * time.Second
		cacheCheckInterval := 2 * time.Second
		cacheRefreshed := false
		cacheRefreshStartTime := time.Now()
		initialProductCount := len(cache.GetAll())

		for time.Since(cacheRefreshStartTime) < maxCacheWaitTime {
			refreshedProducts := cache.GetAll()
			refreshedCount := len(refreshedProducts)
			if refreshedCount > initialProductCount {
				// Cache has refreshed - verify the new product is there
				prod4 := cache.Get("PROD004")
				if len(prod4) > 0 {
					cacheRefreshed = true
					t.Logf("✓ Cache automatically refreshed and contains new product (cache count: %d, initial: %d)", refreshedCount, initialProductCount)
					break
				}
			}
			time.Sleep(cacheCheckInterval)
			t.Logf("  Still waiting for cache to refresh... (elapsed: %v)", time.Since(cacheRefreshStartTime))
		}

		// Require that the cache refreshed - this verifies auto-refresh is working
		require.True(t, cacheRefreshed, "Cache must automatically refresh after CACHE_LOG is updated - this verifies auto-refresh functionality is working")

		// Final verification
		refreshedProducts := cache.GetAll()
		assert.Len(t, refreshedProducts, 3, "Should now have 3 in-stock products after automatic refresh")
		t.Logf("✓ Cache automatically refreshed: now contains %d products", len(refreshedProducts))

		prod4 := cache.Get("PROD004")
		require.NotEmpty(t, prod4, "Should find newly inserted PROD004 after automatic refresh")
		assert.Equal(t, "PROD004", prod4[0].ProductCode)
		assert.Equal(t, "Widget D", prod4[0].ProductName)
		t.Logf("✓ Auto-refresh confirmed: New product PROD004 successfully cached via automatic refresh")

		t.Logf("✅ REGISTERCACHETABLE procedure integration test completed successfully!")
	})
}

// Helper function to run a single test (useful for debugging)
func runSingleTest(testName string) {
	fmt.Printf("Running Snowflake test: %s\n", testName)

	// This would be used for manual testing/debugging
	// In practice, use: go test -run TestSnowflakeCacheIntegration/TestName
}

// setupSnowflakeSchemaAndData creates required schemas/tables and loads sample data.
// It is safe to run multiple times (uses IF NOT EXISTS and upserts by unique keys).
func setupSnowflakeSchemaAndData(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	schemaName := SNOWFLAKE_SCHEMA
	if schemaName == "" {
		schemaName = "DB_CACHE"
	}

	statements := []string{
		// Change log table (qualified)
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s.%s.CACHE_LOG (TABLE_NAME VARCHAR(16777216), UPDATE_TIME TIMESTAMP_TZ(9) DEFAULT CURRENT_TIMESTAMP())", SNOWFLAKE_DATABASE, schemaName),
		// API keys table
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.%s.API_KEYS (
            ID INTEGER AUTOINCREMENT,
            KEY VARCHAR(255) UNIQUE NOT NULL,
            NAME VARCHAR(255) NOT NULL,
            IS_ACTIVE BOOLEAN DEFAULT TRUE,
            CREATED_AT TIMESTAMP DEFAULT CURRENT_TIMESTAMP()
        )`, SNOWFLAKE_DATABASE, schemaName),
		// Users table
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.%s.USERS (
            ID INTEGER AUTOINCREMENT,
            USERNAME VARCHAR(255) UNIQUE NOT NULL,
            EMAIL VARCHAR(255) NOT NULL,
            ROLE VARCHAR(50) DEFAULT 'user',
            CREATED_AT TIMESTAMP DEFAULT CURRENT_TIMESTAMP()
        )`, SNOWFLAKE_DATABASE, schemaName),
	}

	for _, sqlStmt := range statements {
		_, err := db.ExecContext(ctx, sqlStmt)
		require.NoError(t, err, "Failed executing schema statement: %s", sqlStmt)
	}

	// Seed data (use merge-like behavior via delete+insert for idempotency)
	// API_KEYS data
	_, err := db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s.%s.API_KEYS WHERE KEY IN ('api_key_1','api_key_2','api_key_3','api_key_4')`, SNOWFLAKE_DATABASE, schemaName))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s.%s.API_KEYS (KEY, NAME, IS_ACTIVE) VALUES
        ('api_key_1','Test API Key 1', TRUE),
        ('api_key_2','Test API Key 2', TRUE),
        ('api_key_3','Test API Key 3', FALSE),
        ('api_key_4','Test API Key 4', TRUE)`, SNOWFLAKE_DATABASE, schemaName))
	require.NoError(t, err)

	// USERS data
	_, err = db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s.%s.USERS WHERE USERNAME IN ('alice','bob','charlie','diana')`, SNOWFLAKE_DATABASE, schemaName))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s.%s.USERS (USERNAME, EMAIL, ROLE) VALUES
        ('alice','alice@example.com','admin'),
        ('bob','bob@example.com','user'),
        ('charlie','charlie@example.com','user'),
        ('diana','diana@example.com','moderator')`, SNOWFLAKE_DATABASE, schemaName))
	require.NoError(t, err)

	// Prime the change log (rely on default OPERATION_TIME)
	_, err = db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s.%s.CACHE_LOG (TABLE_NAME) VALUES
        ('API_KEYS'),
        ('USERS')`, SNOWFLAKE_DATABASE, schemaName))
	require.NoError(t, err)
}

// ensureDbAndSchemaContext executes USE statements to set current DB and schema,
// surfacing authorization issues early with clear errors.
func ensureDbAndSchemaContext(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// USE DATABASE
	if strings.TrimSpace(SNOWFLAKE_DATABASE) != "" {
		_, err := db.ExecContext(ctx, "USE DATABASE "+SNOWFLAKE_DATABASE)
		require.NoError(t, err, "Not authorized to USE DATABASE %s", SNOWFLAKE_DATABASE)
	}

	// Attempt to USE SCHEMA (database.schema form for clarity)
	if strings.TrimSpace(SNOWFLAKE_DATABASE) != "" && strings.TrimSpace(SNOWFLAKE_SCHEMA) != "" {
		_, err := db.ExecContext(ctx, fmt.Sprintf("USE SCHEMA %s.%s", SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA))
		require.NoError(t, err, "Not authorized to USE SCHEMA %s.%s", SNOWFLAKE_DATABASE, SNOWFLAKE_SCHEMA)
	}
}
