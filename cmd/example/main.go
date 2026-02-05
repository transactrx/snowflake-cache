package main

import (
	"database/sql"
	"log"
	"os"
	"time"

	snowflakecache "github.com/transactrx/snowflake-cache/pkg/snowflake-cache"
)

// ApiKey2 type is defined in apiKeyModel.go

// Note: PostgreSQL support has been removed. This package is now Snowflake-only.

// Example usage for Snowflake
func snowflakeExample() {
	// For Snowflake, you need a standard *sql.DB connection
	// Configure your Snowflake connection string
	snowflakeDB, err := sql.Open("snowflake", "user:password@account/database/schema")
	if err != nil {
		log.Panicf("Could not connect to Snowflake: %v", err)
	}
	defer snowflakeDB.Close()

	// For Snowflake: pass "SCHEMA" as the DB_RW parameter
	// This specifies the default schema for your monitored application tables
	// Note: CACHE_LOG always lives in the hardcoded DB_CACHE schema
	cache, err := snowflakecache.CreateCache[ApiKey2](
		nil, // logger (nil uses default)
		`SELECT 
			KEY AS "key", 
			DESCRIPTION AS "description", 
			CONFIGURATION AS "configuration", 
			NAME AS "name", 
			MAX_DAILY_RATE AS "max_daily_rate", 
			VOLUMES AS "volumes", 
			CLIENT_ID AS "clientid" 
		FROM MY_DATABASE.MY_SCHEMA.API_KEYS`,
		[]string{"API_KEYS"},    // monitored tables (will be prefixed with schema)
		"Key",                   // key field name
		time.Second*60,          // check interval
		snowflakeDB,             // Snowflake *sql.DB connection
		"MY_SCHEMA",             // Default schema for monitored tables
	)
	if err != nil {
		panic(err)
	}

	// Use the cache
	result := cache.Get("someid")
	if result != nil {
		log.Printf("Found in cache: %v", result)
	} else {
		log.Printf("Value not found in cache!")
	}

	allKeys := cache.GetAll()
	log.Printf("Total keys in cache: %d", len(allKeys))
}

func main() {
	// Check required environment variable
	env := os.Getenv("SNOWFLAKE_ENV")
	if env == "" {
		log.Fatal("SNOWFLAKE_ENV environment variable is required (set to DEV or PROD)")
	}
	if env != "DEV" && env != "PROD" {
		log.Fatalf("SNOWFLAKE_ENV must be DEV or PROD, got: %s", env)
	}

	log.Printf("Running Snowflake example (SNOWFLAKE_ENV=%s)...", env)
	snowflakeExample()
}
