package comparator

import (
	"time"

	"github.com/transactrx/snowflake-cache/example-service/models"
)

// ComparisonReport contains the results of comparing both caches.
type ComparisonReport struct {
	Timestamp          time.Time       `json:"timestamp"`
	SnowflakeCount     int             `json:"snowflake_count"`
	PostgresCount      int             `json:"postgres_count"`
	CountMatch         bool            `json:"count_match"`
	MissingInSnowflake []string        `json:"missing_in_snowflake"`
	MissingInPostgres  []string        `json:"missing_in_postgres"`
	ValueMismatches    []ValueMismatch `json:"value_mismatches"`
	TotalMismatches    int             `json:"total_mismatches"`
	DurationMs         int64           `json:"duration_ms"`
}

// ValueMismatch represents a field-level difference between caches.
type ValueMismatch struct {
	Key       string      `json:"key"`
	Field     string      `json:"field"`
	Snowflake interface{} `json:"snowflake"`
	Postgres  interface{} `json:"postgres"`
}

// HasDiscrepancies returns true if any differences were found.
func (r *ComparisonReport) HasDiscrepancies() bool {
	return !r.CountMatch ||
		len(r.MissingInSnowflake) > 0 ||
		len(r.MissingInPostgres) > 0 ||
		r.TotalMismatches > 0
}

// Compare compares data from both caches and returns a comparison report.
func Compare(snowflakeData, postgresData []models.ApiKey) *ComparisonReport {
	startTime := time.Now()

	report := &ComparisonReport{
		Timestamp:          startTime,
		SnowflakeCount:     len(snowflakeData),
		PostgresCount:      len(postgresData),
		MissingInSnowflake: []string{},
		MissingInPostgres:  []string{},
		ValueMismatches:    []ValueMismatch{},
	}

	report.CountMatch = report.SnowflakeCount == report.PostgresCount

	// Build maps for efficient lookup
	sfMap := buildKeyMap(snowflakeData)
	pgMap := buildKeyMap(postgresData)

	// Find keys missing in Snowflake (present in Postgres but not Snowflake)
	for key := range pgMap {
		if _, exists := sfMap[key]; !exists {
			report.MissingInSnowflake = append(report.MissingInSnowflake, key)
		}
	}

	// Find keys missing in Postgres (present in Snowflake but not Postgres)
	for key := range sfMap {
		if _, exists := pgMap[key]; !exists {
			report.MissingInPostgres = append(report.MissingInPostgres, key)
		}
	}

	// Compare values for matching keys
	for key, sfRecord := range sfMap {
		if pgRecord, exists := pgMap[key]; exists {
			mismatches := compareRecords(key, sfRecord, pgRecord)
			report.ValueMismatches = append(report.ValueMismatches, mismatches...)
		}
	}

	report.TotalMismatches = len(report.ValueMismatches)
	report.DurationMs = time.Since(startTime).Milliseconds()

	return report
}

// buildKeyMap creates a map from key to ApiKey for efficient lookup.
func buildKeyMap(data []models.ApiKey) map[string]*models.ApiKey {
	result := make(map[string]*models.ApiKey, len(data))
	for i := range data {
		key := data[i].GetKeyValue()
		if key != "" {
			result[key] = &data[i]
		}
	}
	return result
}

// compareRecords compares two ApiKey records field by field.
func compareRecords(key string, sf, pg *models.ApiKey) []ValueMismatch {
	var mismatches []ValueMismatch

	// Compare Name
	if !stringPtrEqual(sf.Name, pg.Name) {
		mismatches = append(mismatches, ValueMismatch{
			Key:       key,
			Field:     "Name",
			Snowflake: ptrToInterface(sf.Name),
			Postgres:  ptrToInterface(pg.Name),
		})
	}

	// Compare Description
	if !stringPtrEqual(sf.Description, pg.Description) {
		mismatches = append(mismatches, ValueMismatch{
			Key:       key,
			Field:     "Description",
			Snowflake: ptrToInterface(sf.Description),
			Postgres:  ptrToInterface(pg.Description),
		})
	}

	// Compare ClientID
	if !stringPtrEqual(sf.ClientID, pg.ClientID) {
		mismatches = append(mismatches, ValueMismatch{
			Key:       key,
			Field:     "ClientID",
			Snowflake: ptrToInterface(sf.ClientID),
			Postgres:  ptrToInterface(pg.ClientID),
		})
	}

	// Compare Configuration
	if !stringPtrEqual(sf.Configuration, pg.Configuration) {
		mismatches = append(mismatches, ValueMismatch{
			Key:       key,
			Field:     "Configuration",
			Snowflake: ptrToInterface(sf.Configuration),
			Postgres:  ptrToInterface(pg.Configuration),
		})
	}

	// Compare Volumes
	if !stringPtrEqual(sf.Volumes, pg.Volumes) {
		mismatches = append(mismatches, ValueMismatch{
			Key:       key,
			Field:     "Volumes",
			Snowflake: ptrToInterface(sf.Volumes),
			Postgres:  ptrToInterface(pg.Volumes),
		})
	}

	// Compare MaxDailyRate
	if !int64PtrEqual(sf.MaxDailyRate, pg.MaxDailyRate) {
		mismatches = append(mismatches, ValueMismatch{
			Key:       key,
			Field:     "MaxDailyRate",
			Snowflake: int64PtrToInterface(sf.MaxDailyRate),
			Postgres:  int64PtrToInterface(pg.MaxDailyRate),
		})
	}

	return mismatches
}

// stringPtrEqual compares two string pointers.
// Returns true if both are nil, or both are non-nil with equal values.
func stringPtrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// int64PtrEqual compares two int64 pointers.
// Returns true if both are nil, or both are non-nil with equal values.
func int64PtrEqual(a, b *int64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// ptrToInterface converts a string pointer to interface{} for JSON serialization.
func ptrToInterface(s *string) interface{} {
	if s == nil {
		return nil
	}
	return *s
}

// int64PtrToInterface converts an int64 pointer to interface{} for JSON serialization.
func int64PtrToInterface(i *int64) interface{} {
	if i == nil {
		return nil
	}
	return *i
}
