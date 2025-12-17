package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all configuration for the cache comparison service.
type Config struct {
	// Database connections
	SnowflakeDSN          string
	PostgresDSN           string
	SnowflakeDatabaseSchema string // e.g., "MY_DATABASE.MY_SCHEMA"

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

	// Logging
	LogLevel string
}

// DefaultConfig returns configuration with default values.
func DefaultConfig() *Config {
	return &Config{
		ComparisonInterval:    5 * time.Minute,
		CacheCheckInterval:    60 * time.Second,
		MaxDetailedMismatches: 100,
		LogLevel:              "info",
		KeyField:              "Key",
		MonitoredTables:       []string{"API_KEYS"},
	}
}

// LoadFromEnv loads configuration from environment variables.
// Returns an error if required variables are missing.
func LoadFromEnv() (*Config, error) {
	cfg := DefaultConfig()

	// Required: Database connections
	cfg.SnowflakeDSN = os.Getenv("SNOWFLAKE_DSN")
	if cfg.SnowflakeDSN == "" {
		return nil, fmt.Errorf("SNOWFLAKE_DSN environment variable is required")
	}

	cfg.PostgresDSN = os.Getenv("POSTGRES_DSN")
	if cfg.PostgresDSN == "" {
		return nil, fmt.Errorf("POSTGRES_DSN environment variable is required")
	}

	cfg.SnowflakeDatabaseSchema = os.Getenv("SNOWFLAKE_DATABASE_SCHEMA")
	if cfg.SnowflakeDatabaseSchema == "" {
		return nil, fmt.Errorf("SNOWFLAKE_DATABASE_SCHEMA environment variable is required")
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

