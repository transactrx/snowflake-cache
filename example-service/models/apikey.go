package models

// ApiKey represents an API key record that exists in both PostgreSQL and Snowflake.
// Field tags support both pgxscan (db) and JSON serialization.
type ApiKey struct {
	Key           *string `db:"key" json:"key"`
	Name          *string `db:"name" json:"name"`
	Description   *string `db:"description" json:"description"`
	ClientID      *string `db:"client_id" json:"client_id"`
	Configuration *string `db:"configuration" json:"configuration"`
	Volumes       *string `db:"volumes" json:"volumes"`
	MaxDailyRate  *int64  `db:"max_daily_rate" json:"max_daily_rate"`
}

// GetKeyValue returns the key value or empty string if nil.
// Used for building comparison maps.
func (a *ApiKey) GetKeyValue() string {
	if a.Key == nil {
		return ""
	}
	return *a.Key
}

