package reporter

import (
	"encoding/json"
	"log"
	"time"

	"github.com/transactrx/snowflake-cache/example-service/comparator"
)

// Reporter handles logging of comparison results.
type Reporter struct {
	logger              *log.Logger
	maxDetailedMismatches int
}

// NewReporter creates a new Reporter with the given logger and mismatch limit.
func NewReporter(logger *log.Logger, maxDetailedMismatches int) *Reporter {
	if logger == nil {
		logger = log.Default()
	}
	return &Reporter{
		logger:              logger,
		maxDetailedMismatches: maxDetailedMismatches,
	}
}

// LogComparisonResult logs the comparison report in structured JSON format.
func (r *Reporter) LogComparisonResult(report *comparator.ComparisonReport) {
	// Create a limited report for logging
	logReport := r.createLogReport(report)

	jsonBytes, err := json.Marshal(logReport)
	if err != nil {
		r.logger.Printf("[ERROR] Failed to marshal comparison report: %v", err)
		return
	}

	if report.HasDiscrepancies() {
		r.logger.Printf("[WARN] Comparison complete with discrepancies: %s", string(jsonBytes))
	} else {
		r.logger.Printf("[INFO] Comparison complete: %s", string(jsonBytes))
	}
}

// LogRefreshError logs when a cache refresh fails.
func (r *Reporter) LogRefreshError(cacheName string, err error) {
	logEntry := map[string]interface{}{
		"event":     "refresh_error",
		"cache":     cacheName,
		"error":     err.Error(),
		"timestamp": time.Now().Format(time.RFC3339),
		"skipped":   true,
	}

	jsonBytes, _ := json.Marshal(logEntry)
	r.logger.Printf("[ERROR] Cache refresh failed, skipping comparison: %s", string(jsonBytes))
}

// LogStartup logs service startup information.
func (r *Reporter) LogStartup(interval time.Duration) {
	logEntry := map[string]interface{}{
		"event":              "startup",
		"comparison_interval": interval.String(),
		"timestamp":          time.Now().Format(time.RFC3339),
	}

	jsonBytes, _ := json.Marshal(logEntry)
	r.logger.Printf("[INFO] Cache comparison service started: %s", string(jsonBytes))
}

// LogShutdown logs service shutdown.
func (r *Reporter) LogShutdown() {
	logEntry := map[string]interface{}{
		"event":     "shutdown",
		"timestamp": time.Now().Format(time.RFC3339),
	}

	jsonBytes, _ := json.Marshal(logEntry)
	r.logger.Printf("[INFO] Cache comparison service shutting down: %s", string(jsonBytes))
}

// LogComparisonStart logs when a comparison begins.
func (r *Reporter) LogComparisonStart() {
	r.logger.Printf("[INFO] Starting cache comparison...")
}

// logReport is a limited version of ComparisonReport for logging.
type logReport struct {
	Timestamp             string                       `json:"timestamp"`
	SnowflakeCount        int                          `json:"snowflake_count"`
	PostgresCount         int                          `json:"postgres_count"`
	CountMatch            bool                         `json:"count_match"`
	MissingInSnowflake    int                          `json:"missing_in_snowflake_count"`
	MissingInSnowflakeKeys []string                    `json:"missing_in_snowflake_keys,omitempty"`
	MissingInPostgres     int                          `json:"missing_in_postgres_count"`
	MissingInPostgresKeys []string                     `json:"missing_in_postgres_keys,omitempty"`
	TotalMismatches       int                          `json:"total_value_mismatches"`
	DetailedMismatches    []comparator.ValueMismatch   `json:"value_mismatches,omitempty"`
	MismatchesTruncated   bool                         `json:"mismatches_truncated,omitempty"`
	DurationMs            int64                        `json:"duration_ms"`
}

// createLogReport creates a limited report suitable for logging.
func (r *Reporter) createLogReport(report *comparator.ComparisonReport) logReport {
	lr := logReport{
		Timestamp:          report.Timestamp.Format(time.RFC3339),
		SnowflakeCount:     report.SnowflakeCount,
		PostgresCount:      report.PostgresCount,
		CountMatch:         report.CountMatch,
		MissingInSnowflake: len(report.MissingInSnowflake),
		MissingInPostgres:  len(report.MissingInPostgres),
		TotalMismatches:    report.TotalMismatches,
		DurationMs:         report.DurationMs,
	}

	// Include missing keys (limited)
	if len(report.MissingInSnowflake) > 0 {
		limit := min(len(report.MissingInSnowflake), r.maxDetailedMismatches)
		lr.MissingInSnowflakeKeys = report.MissingInSnowflake[:limit]
	}
	if len(report.MissingInPostgres) > 0 {
		limit := min(len(report.MissingInPostgres), r.maxDetailedMismatches)
		lr.MissingInPostgresKeys = report.MissingInPostgres[:limit]
	}

	// Include detailed mismatches up to the limit
	if len(report.ValueMismatches) > 0 {
		limit := min(len(report.ValueMismatches), r.maxDetailedMismatches)
		lr.DetailedMismatches = report.ValueMismatches[:limit]
		lr.MismatchesTruncated = len(report.ValueMismatches) > r.maxDetailedMismatches
	}

	return lr
}

// min returns the smaller of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

