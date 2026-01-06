package models

import "strconv"

// PharmacySwitchService represents a pharmacy switch service record
// that exists in both PostgreSQL and Snowflake.
// Field tags use UPPERCASE to match Snowflake column names.
// PostgreSQL is case-insensitive for unquoted columns, so this works for both.
type PharmacySwitchService struct {
	ID               *int64  `db:"ID" json:"id"`
	PharmacyID       *int64  `db:"PHARMACY_ID" json:"pharmacy_id"`
	SwitchServiceID  *int64  `db:"SWITCH_SERVICE_ID" json:"switch_service_id"`
	Rank             *int64  `db:"RANK" json:"rank"`
	CopayProgramType *string `db:"COPAY_PROGRAM_TYPE" json:"copay_program_type"`
	Enabled          *bool   `db:"ENABLED" json:"enabled"`
	Reason           *string `db:"REASON" json:"reason"`
	PPERuleBaseID    *int64  `db:"PPE_RULE_BASE_ID" json:"ppe_rule_base_id"`
}

// GetKeyValue returns the pharmacy ID as string or empty string if nil.
// Used for building comparison maps.
func (p *PharmacySwitchService) GetKeyValue() string {
	if p.PharmacyID == nil {
		return ""
	}
	return strconv.FormatInt(*p.PharmacyID, 10)
}
