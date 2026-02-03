package comparator

import (
	"strconv"
	"strings"
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

// ValueMismatch represents a record-level difference between caches.
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
	for key, sfRecords := range sfMap {
		if pgRecords, exists := pgMap[key]; exists {
			mismatches := compareRecordSets(key, sfRecords, pgRecords)
			report.ValueMismatches = append(report.ValueMismatches, mismatches...)
		}
	}

	report.TotalMismatches = len(report.ValueMismatches)
	report.DurationMs = time.Since(startTime).Milliseconds()

	return report
}

type signatureCount struct {
	Signature string `json:"signature"`
	Count     int    `json:"count"`
}

// buildKeyMap creates a map from key to a slice of PharmacySwitchService records.
func buildKeyMap(data []models.PharmacySwitchService) map[string][]models.PharmacySwitchService {
	result := make(map[string][]models.PharmacySwitchService, len(data))
	for i := range data {
		key := data[i].GetKeyValue()
		if key != "" {
			result[key] = append(result[key], data[i])
		}
	}
	return result
}

// compareRecordSets compares two sets of PharmacySwitchService rows for the same key.
// ID is intentionally ignored because it is not stable between Postgres and Snowflake.
func compareRecordSets(key string, sfRecords, pgRecords []models.PharmacySwitchService) []ValueMismatch {
	var mismatches []ValueMismatch

	sfCounts := buildSignatureCounts(sfRecords)
	pgCounts := buildSignatureCounts(pgRecords)

	for signature, sfCount := range sfCounts {
		pgCount := pgCounts[signature]
		if pgCount != sfCount {
			mismatches = append(mismatches, ValueMismatch{
				Key:   key,
				Field: "RowSignature",
				Snowflake: signatureCount{
					Signature: signature,
					Count:     sfCount,
				},
				Postgres: signatureCount{
					Signature: signature,
					Count:     pgCount,
				},
			})
		}
	}

	for signature, pgCount := range pgCounts {
		if _, exists := sfCounts[signature]; !exists {
			mismatches = append(mismatches, ValueMismatch{
				Key:   key,
				Field: "RowSignature",
				Snowflake: signatureCount{
					Signature: signature,
					Count:     0,
				},
				Postgres: signatureCount{
					Signature: signature,
					Count:     pgCount,
				},
			})
		}
	}

	return mismatches
}

func buildSignatureCounts(records []models.PharmacySwitchService) map[string]int {
	counts := make(map[string]int, len(records))
	for _, record := range records {
		signature := buildRowSignature(record)
		counts[signature]++
	}
	return counts
}

func buildRowSignature(record models.PharmacySwitchService) string {
	var b strings.Builder
	appendSignatureField(&b, "switch_service_id", int64PtrToSignature(record.SwitchServiceID))
	appendSignatureField(&b, "rank", int64PtrToSignature(record.Rank))
	appendSignatureField(&b, "copay_program_type", stringPtrToSignature(record.CopayProgramType))
	appendSignatureField(&b, "enabled", boolPtrToSignature(record.Enabled))
	appendSignatureField(&b, "reason", stringPtrToSignature(record.Reason))
	appendSignatureField(&b, "ppe_rule_base_id", int64PtrToSignature(record.PPERuleBaseID))
	return b.String()
}

func appendSignatureField(b *strings.Builder, name, value string) {
	if b.Len() > 0 {
		b.WriteString("|")
	}
	b.WriteString(name)
	b.WriteString("=")
	b.WriteString(value)
}

func stringPtrToSignature(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return strconv.Quote(*s)
}

func int64PtrToSignature(i *int64) string {
	if i == nil {
		return "<nil>"
	}
	return strconv.FormatInt(*i, 10)
}

func boolPtrToSignature(b *bool) string {
	if b == nil {
		return "<nil>"
	}
	if *b {
		return "true"
	}
	return "false"
}
