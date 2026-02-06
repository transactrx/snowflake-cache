package main

import (
	"database/sql"
	"log"
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

	// For Snowflake: pass "DATABASE.SCHEMA" as the DB_RW parameter
	// This specifies the default database + schema for your monitored application tables.
	// Note: CACHE_LOG lives in the DB_CACHE schema in the same database.
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
		"MY_DATABASE.MY_SCHEMA", // Default database + schema for monitored tables
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
	snowflakeExample()
}
