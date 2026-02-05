package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all configuration for the cache comparison service.
type Config struct {
	// Snowflake environment (DEV or PROD) - determines database for stream registration
	SnowflakeEnv string

	// Snowflake connection parameters
	SnowflakeAccount    string
	SnowflakeUser       string
	SnowflakePrivateKey string // Base64-encoded or PEM private key
	SnowflakeDatabase   string
	SnowflakeSchema     string

	// PostgreSQL connection
	PostgresDSN string

	// Computed: DATABASE.SCHEMA for cache log location
	SnowflakeDatabaseSchema string

	// SQL queries for cache loading
	SnowflakeSQL string
	PostgresSQL  string

	// Cache configuration
	MonitoredTables    []string
	KeyField           string
	CacheCheckInterval time.Duration

	// Comparison settings
	ComparisonInterval    time.Duration
	MaxDetailedMismatches int
	RefreshBeforeCompare  bool

	// Logging
	LogLevel string
}

// DefaultConfig returns configuration with default values.
func DefaultConfig() *Config {
	return &Config{
		ComparisonInterval:    5 * time.Minute,
		CacheCheckInterval:    60 * time.Second,
		MaxDetailedMismatches: 100,
		RefreshBeforeCompare:  false,
		LogLevel:              "info",
		KeyField:              "Key",
		MonitoredTables:       []string{"API_KEYS"},
	}
}

// LoadFromEnv loads configuration from environment variables.
// Returns an error if required variables are missing.
func LoadFromEnv() (*Config, error) {
	cfg := DefaultConfig()

	// Required: Snowflake environment (DEV or PROD)
	cfg.SnowflakeEnv = os.Getenv("SNOWFLAKE_ENV")
	if cfg.SnowflakeEnv == "" {
		return nil, fmt.Errorf("SNOWFLAKE_ENV environment variable is required (set to DEV or PROD)")
	}
	if cfg.SnowflakeEnv != "DEV" && cfg.SnowflakeEnv != "PROD" {
		return nil, fmt.Errorf("SNOWFLAKE_ENV must be DEV or PROD, got: %s", cfg.SnowflakeEnv)
	}

	// Required: Snowflake connection parameters
	cfg.SnowflakeAccount = os.Getenv("SNOWFLAKE_ACCOUNT")
	if cfg.SnowflakeAccount == "" {
		return nil, fmt.Errorf("SNOWFLAKE_ACCOUNT environment variable is required")
	}

	cfg.SnowflakeUser = os.Getenv("SNOWFLAKE_USER")
	if cfg.SnowflakeUser == "" {
		return nil, fmt.Errorf("SNOWFLAKE_USER environment variable is required")
	}

	cfg.SnowflakePrivateKey = os.Getenv("SNOWFLAKE_PRIVATE_KEY")
	if cfg.SnowflakePrivateKey == "" {
		return nil, fmt.Errorf("SNOWFLAKE_PRIVATE_KEY environment variable is required")
	}

	cfg.SnowflakeDatabase = os.Getenv("SNOWFLAKE_DATABASE")
	if cfg.SnowflakeDatabase == "" {
		return nil, fmt.Errorf("SNOWFLAKE_DATABASE environment variable is required")
	}

	cfg.SnowflakeSchema = os.Getenv("SNOWFLAKE_SCHEMA")
	if cfg.SnowflakeSchema == "" {
		return nil, fmt.Errorf("SNOWFLAKE_SCHEMA environment variable is required")
	}

	// Compute DATABASE.SCHEMA for cache log location
	cfg.SnowflakeDatabaseSchema = cfg.SnowflakeDatabase + "." + cfg.SnowflakeSchema

	// Required: PostgreSQL connection string
	cfg.PostgresDSN = os.Getenv("POSTGRES_DSN")
	if cfg.PostgresDSN == "" {
		return nil, fmt.Errorf("POSTGRES_DSN environment variable is required")
	}

	// Required: SQL queries
	cfg.SnowflakeSQL = os.Getenv("SNOWFLAKE_SQL")
	if cfg.SnowflakeSQL == "" {
		return nil, fmt.Errorf("SNOWFLAKE_SQL environment variable is required")
	}

	cfg.PostgresSQL = os.Getenv("POSTGRES_SQL")
	if cfg.PostgresSQL == "" {
		return nil, fmt.Errorf("POSTGRES_SQL environment variable is required")
	}

	// Optional: Comparison interval
	if intervalStr := os.Getenv("COMPARISON_INTERVAL"); intervalStr != "" {
		interval, err := time.ParseDuration(intervalStr)
		if err != nil {
			return nil, fmt.Errorf("invalid COMPARISON_INTERVAL: %w", err)
		}
		cfg.ComparisonInterval = interval
	}

	// Optional: Cache check interval
	if intervalStr := os.Getenv("CACHE_CHECK_INTERVAL"); intervalStr != "" {
		interval, err := time.ParseDuration(intervalStr)
		if err != nil {
			return nil, fmt.Errorf("invalid CACHE_CHECK_INTERVAL: %w", err)
		}
		cfg.CacheCheckInterval = interval
	}

	// Optional: Max detailed mismatches
	if maxStr := os.Getenv("MAX_DETAILED_MISMATCHES"); maxStr != "" {
		max, err := strconv.Atoi(maxStr)
		if err != nil {
			return nil, fmt.Errorf("invalid MAX_DETAILED_MISMATCHES: %w", err)
		}
		cfg.MaxDetailedMismatches = max
	}

	// Optional: Refresh before comparison
	if refreshStr := os.Getenv("REFRESH_BEFORE_COMPARE"); refreshStr != "" {
		refresh, err := strconv.ParseBool(refreshStr)
		if err != nil {
			return nil, fmt.Errorf("invalid REFRESH_BEFORE_COMPARE: %w", err)
		}
		cfg.RefreshBeforeCompare = refresh
	}

	// Optional: Log level
	if level := os.Getenv("LOG_LEVEL"); level != "" {
		cfg.LogLevel = level
	}

	// Optional: Key field override
	if keyField := os.Getenv("KEY_FIELD"); keyField != "" {
		cfg.KeyField = keyField
	}

	// Optional: Monitored tables override (comma-separated)
	if tables := os.Getenv("MONITORED_TABLES"); tables != "" {
		cfg.MonitoredTables = splitAndTrim(tables, ",")
	}

	return cfg, nil
}

// splitAndTrim splits a string by separator and trims whitespace from each part.
func splitAndTrim(s, sep string) []string {
	var result []string
	for _, part := range splitString(s, sep) {
		trimmed := trimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// splitString splits s by sep without importing strings package.
func splitString(s, sep string) []string {
	var result []string
	for {
		idx := indexOf(s, sep)
		if idx < 0 {
			result = append(result, s)
			break
		}
		result = append(result, s[:idx])
		s = s[idx+len(sep):]
	}
	return result
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
