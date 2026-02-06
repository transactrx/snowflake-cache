package snowflakecache

import (
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

type testItem struct {
	UserID *string `db:"user_id"`
	ID     *int    `db:"id"`
}

func TestCreateSnowflakeCache_SingleTable(t *testing.T) {
	// Arrange
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	// Required schema/procedure checks
	schemaQuery := "SELECT COUNT(*) FROM MY_DB.INFORMATION_SCHEMA.SCHEMATA WHERE SCHEMA_NAME = ?"
	mock.ExpectQuery(regexp.QuoteMeta(schemaQuery)).
		WithArgs("DB_CACHE").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	procQuery := "SELECT COUNT(*) FROM MY_DB.INFORMATION_SCHEMA.PROCEDURES WHERE PROCEDURE_SCHEMA = ? AND PROCEDURE_NAME = ?"
	mock.ExpectQuery(regexp.QuoteMeta(procQuery)).
		WithArgs("DB_CACHE", "REGISTERCACHETABLE").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	// Stream registration call
	registerCall := "CALL MY_DB.DB_CACHE.REGISTERCACHETABLE(?, ?, ?)"
	mock.ExpectQuery(regexp.QuoteMeta(registerCall)).
		WithArgs("MY_DB", "PUBLIC", "API_KEYS").
		WillReturnRows(sqlmock.NewRows([]string{"result"}).AddRow("OK"))

	// Fingerprint query expectation: CACHE_LOG lives in MY_DB.DB_CACHE schema, and TABLE_NAME is DB.SCHEMA.TABLE.
	fpQuery := "SELECT COUNT(*) || TO_VARCHAR(COALESCE(MAX(update_time), TO_TIMESTAMP_TZ('1980-01-01'))) AS ct FROM MY_DB.DB_CACHE.CACHE_LOG WHERE table_name = ?"
	mock.ExpectQuery(regexp.QuoteMeta(fpQuery)).
		WithArgs("MY_DB.PUBLIC.API_KEYS").
		WillReturnRows(sqlmock.NewRows([]string{"ct"}).AddRow("fp1"))

	// Load SQL expectation
	loadSQL := "SELECT 'u1' AS user_id, 1 AS id UNION ALL SELECT 'u1' AS user_id, 2 AS id UNION ALL SELECT 'u2' AS user_id, 3 AS id"
	mock.ExpectQuery(regexp.QuoteMeta(loadSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "id"}).
			AddRow("u1", 1).
			AddRow("u1", 2).
			AddRow("u2", 3))

		// Act
	cache, err := CreateCache[testItem](
		nil,     // logger
		loadSQL, // SQL
		[]string{"PUBLIC.API_KEYS"},
		"UserID",
		time.Hour,
		db,
		"MY_DB.PUBLIC",
	)
	if err != nil {
		t.Fatalf("CreateSnowflakeCache failed: %v", err)
	}
	// Cache does not require explicit Close()

	// Assert cache contents
	u1 := cache.Get("u1")
	if len(u1) != 2 {
		t.Fatalf("expected 2 rows for u1, got %d", len(u1))
	}
	all := cache.GetAll()
	if len(all) != 3 {
		t.Fatalf("expected 3 rows in total, got %d", len(all))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestSnowflakeCache_ForceRefresh(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	// Required schema/procedure checks
	schemaQuery := "SELECT COUNT(*) FROM MY_DB.INFORMATION_SCHEMA.SCHEMATA WHERE SCHEMA_NAME = ?"
	mock.ExpectQuery(regexp.QuoteMeta(schemaQuery)).
		WithArgs("DB_CACHE").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	procQuery := "SELECT COUNT(*) FROM MY_DB.INFORMATION_SCHEMA.PROCEDURES WHERE PROCEDURE_SCHEMA = ? AND PROCEDURE_NAME = ?"
	mock.ExpectQuery(regexp.QuoteMeta(procQuery)).
		WithArgs("DB_CACHE", "REGISTERCACHETABLE").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	// Stream registration call
	registerCall := "CALL MY_DB.DB_CACHE.REGISTERCACHETABLE(?, ?, ?)"
	mock.ExpectQuery(regexp.QuoteMeta(registerCall)).
		WithArgs("MY_DB", "PUBLIC", "API_KEYS").
		WillReturnRows(sqlmock.NewRows([]string{"result"}).AddRow("OK"))

	// Initial fingerprint uses MY_DB.DB_CACHE and DB.SCHEMA.TABLE.
	fpQuery := "SELECT COUNT(*) || TO_VARCHAR(COALESCE(MAX(update_time), TO_TIMESTAMP_TZ('1980-01-01'))) AS ct FROM MY_DB.DB_CACHE.CACHE_LOG WHERE table_name = ?"
	mock.ExpectQuery(regexp.QuoteMeta(fpQuery)).
		WithArgs("MY_DB.PUBLIC.API_KEYS").
		WillReturnRows(sqlmock.NewRows([]string{"ct"}).AddRow("fp1"))

	// Initial load
	loadSQL := "SELECT 'u1' AS user_id, 1 AS id"
	mock.ExpectQuery(regexp.QuoteMeta(loadSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "id"}).AddRow("u1", 1))

	cache, err := CreateCache[testItem](nil, loadSQL, []string{"PUBLIC.API_KEYS"}, "UserID", time.Hour, db, "MY_DB.PUBLIC")
	if err != nil {
		t.Fatalf("CreateSnowflakeCache failed: %v", err)
	}
	// Cache does not require explicit Close()

	// ForceRefresh should re-read fingerprint and reload
	mock.ExpectQuery(regexp.QuoteMeta(fpQuery)).
		WithArgs("MY_DB.PUBLIC.API_KEYS").
		WillReturnRows(sqlmock.NewRows([]string{"ct"}).AddRow("fp2"))

	// Reload expectation
	mock.ExpectQuery(regexp.QuoteMeta(loadSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "id"}).AddRow("u1", 1))

	if err := cache.ForceRefresh(); err != nil {
		t.Fatalf("ForceRefresh failed: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
