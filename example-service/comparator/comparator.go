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
func Compare(snowflakeData, postgresData []models.PharmacySwitchService) *ComparisonReport {
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

// buildKeyMap creates a map from key to PharmacySwitchService for efficient lookup.
func buildKeyMap(data []models.PharmacySwitchService) map[string]*models.PharmacySwitchService {
	result := make(map[string]*models.PharmacySwitchService, len(data))
	for i := range data {
		key := data[i].GetKeyValue()
		if key != "" {
			result[key] = &data[i]
		}
	}
	return result
}

// compareRecords compares two PharmacySwitchService records field by field.
func compareRecords(key string, sf, pg *models.PharmacySwitchService) []ValueMismatch {
	var mismatches []ValueMismatch

	// Compare ID
	if !stringPtrEqual(sf.ID, pg.ID) {
		mismatches = append(mismatches, ValueMismatch{
			Key:       key,
			Field:     "ID",
			Snowflake: ptrToInterface(sf.ID),
			Postgres:  ptrToInterface(pg.ID),
		})
	}

	// Compare SwitchServiceID
	if !int64PtrEqual(sf.SwitchServiceID, pg.SwitchServiceID) {
		mismatches = append(mismatches, ValueMismatch{
			Key:       key,
			Field:     "SwitchServiceID",
			Snowflake: int64PtrToInterface(sf.SwitchServiceID),
			Postgres:  int64PtrToInterface(pg.SwitchServiceID),
		})
	}

	// Compare Rank
	if !int64PtrEqual(sf.Rank, pg.Rank) {
		mismatches = append(mismatches, ValueMismatch{
			Key:       key,
			Field:     "Rank",
			Snowflake: int64PtrToInterface(sf.Rank),
			Postgres:  int64PtrToInterface(pg.Rank),
		})
	}

	// Compare CopayProgramType
	if !stringPtrEqual(sf.CopayProgramType, pg.CopayProgramType) {
		mismatches = append(mismatches, ValueMismatch{
			Key:       key,
			Field:     "CopayProgramType",
			Snowflake: ptrToInterface(sf.CopayProgramType),
			Postgres:  ptrToInterface(pg.CopayProgramType),
		})
	}

	// Compare Enabled
	if !boolPtrEqual(sf.Enabled, pg.Enabled) {
		mismatches = append(mismatches, ValueMismatch{
			Key:       key,
			Field:     "Enabled",
			Snowflake: boolPtrToInterface(sf.Enabled),
			Postgres:  boolPtrToInterface(pg.Enabled),
		})
	}

	// Compare Reason
	if !stringPtrEqual(sf.Reason, pg.Reason) {
		mismatches = append(mismatches, ValueMismatch{
			Key:       key,
			Field:     "Reason",
			Snowflake: ptrToInterface(sf.Reason),
			Postgres:  ptrToInterface(pg.Reason),
		})
	}

	// Compare Version
	if !int64PtrEqual(sf.Version, pg.Version) {
		mismatches = append(mismatches, ValueMismatch{
			Key:       key,
			Field:     "Version",
			Snowflake: int64PtrToInterface(sf.Version),
			Postgres:  int64PtrToInterface(pg.Version),
		})
	}

	// Compare PPERuleBaseID
	if !stringPtrEqual(sf.PPERuleBaseID, pg.PPERuleBaseID) {
		mismatches = append(mismatches, ValueMismatch{
			Key:       key,
			Field:     "PPERuleBaseID",
			Snowflake: ptrToInterface(sf.PPERuleBaseID),
			Postgres:  ptrToInterface(pg.PPERuleBaseID),
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

// boolPtrEqual compares two bool pointers.
// Returns true if both are nil, or both are non-nil with equal values.
func boolPtrEqual(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// boolPtrToInterface converts a bool pointer to interface{} for JSON serialization.
func boolPtrToInterface(b *bool) interface{} {
	if b == nil {
		return nil
	}
	return *b
}
