package models

import "strconv"

// PharmacySwitchService represents a pharmacy switch service record
// that exists in both PostgreSQL and Snowflake.
// Field tags use lowercase to match PostgreSQL columns.
// Note: scany is case-insensitive for column matching, so this works for both
// Snowflake (UPPERCASE) and PostgreSQL (lowercase).
type PharmacySwitchService struct {
	ID               *int64  `db:"id" json:"id"`
	PharmacyID       *int64  `db:"pharmacy_id" json:"pharmacy_id"`
	SwitchServiceID  *int64  `db:"switch_service_id" json:"switch_service_id"`
	Rank             *int64  `db:"rank" json:"rank"`
	CopayProgramType *string `db:"copay_program_type" json:"copay_program_type"`
	Enabled          *bool   `db:"enabled" json:"enabled"`
	Reason           *string `db:"reason" json:"reason"`
	PPERuleBaseID    *int64  `db:"ppe_rule_base_id" json:"ppe_rule_base_id"`
}

// GetKeyValue returns the pharmacy ID as string or empty string if nil.
// Used for building comparison maps.
func (p *PharmacySwitchService) GetKeyValue() string {
	if p.PharmacyID == nil {
		return ""
	}
	return strconv.FormatInt(*p.PharmacyID, 10)
}
