package snowflakecache

import (
	"bytes"
	"errors"
	"io"
	"log"
	"regexp"
	"strings"
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

// newBareCache returns a dbCache with just enough wiring to exercise the
// background-refresh bookkeeping (noteRefreshResult / OnRefreshError) without a DB.
func newBareCache() *dbCache[testItem] {
	return &dbCache[testItem]{
		logger:   log.New(io.Discard, "", 0),
		keyCache: make(map[string][]testItem),
	}
}

func TestSnowflakeCache_OnRefreshError(t *testing.T) {
	c := newBareCache()

	type call struct {
		err   error
		count int
	}
	var calls []call
	c.OnRefreshError(func(err error, consecutiveFailures int) {
		calls = append(calls, call{err: err, count: consecutiveFailures})
	})

	errBoom := errors.New("boom")

	// Two consecutive failures increment the running counter.
	c.noteRefreshResult(errBoom)
	c.noteRefreshResult(errBoom)
	if len(calls) != 2 {
		t.Fatalf("expected handler invoked twice, got %d", len(calls))
	}
	if calls[0].count != 1 || calls[1].count != 2 {
		t.Fatalf("expected consecutive counts 1,2; got %d,%d", calls[0].count, calls[1].count)
	}
	if !errors.Is(calls[1].err, errBoom) {
		t.Fatalf("expected the refresh error propagated to the handler, got %v", calls[1].err)
	}

	// A successful refresh resets the counter and must not invoke the handler.
	c.noteRefreshResult(nil)
	if len(calls) != 2 {
		t.Fatalf("a successful refresh must not invoke the handler; calls=%d", len(calls))
	}

	// The next failure starts counting again from 1 (reset took effect).
	c.noteRefreshResult(errBoom)
	if len(calls) != 3 || calls[2].count != 1 {
		t.Fatalf("expected the counter to reset to 1 after success; calls=%d lastCount=%d", len(calls), calls[len(calls)-1].count)
	}
}

func TestSnowflakeCache_NoteRefreshResult_NilHandlerSafe(t *testing.T) {
	c := newBareCache()

	// No handler registered: noteRefreshResult must not panic and must still track.
	c.noteRefreshResult(errors.New("boom"))

	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if c.consecutiveRefreshFailures != 1 {
		t.Fatalf("expected consecutiveRefreshFailures=1 with a nil handler, got %d", c.consecutiveRefreshFailures)
	}
}

const (
	testSchemaQuery  = "SELECT COUNT(*) FROM MY_DB.INFORMATION_SCHEMA.SCHEMATA WHERE SCHEMA_NAME = ?"
	testProcQuery    = "SELECT COUNT(*) FROM MY_DB.INFORMATION_SCHEMA.PROCEDURES WHERE PROCEDURE_SCHEMA = ? AND PROCEDURE_NAME = ?"
	testRegisterCall = "CALL MY_DB.DB_CACHE.REGISTERCACHETABLE(?, ?, ?)"
	testFPQuery      = "SELECT COUNT(*) || TO_VARCHAR(COALESCE(MAX(update_time), TO_TIMESTAMP_TZ('1980-01-01'))) AS ct FROM MY_DB.DB_CACHE.CACHE_LOG WHERE table_name = ?"
	testLoadSQL      = "SELECT 'u1' AS user_id, 1 AS id"
)

func expectPrerequisites(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta(testSchemaQuery)).
		WithArgs("DB_CACHE").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta(testProcQuery)).
		WithArgs("DB_CACHE", "REGISTERCACHETABLE").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
}

func expectFingerprint(mock sqlmock.Sqlmock, fp string) {
	mock.ExpectQuery(regexp.QuoteMeta(testFPQuery)).
		WithArgs("MY_DB.PUBLIC.API_KEYS").
		WillReturnRows(sqlmock.NewRows([]string{"ct"}).AddRow(fp))
}

func expectLoad(mock sqlmock.Sqlmock, ids ...int) {
	rows := sqlmock.NewRows([]string{"user_id", "id"})
	for _, id := range ids {
		rows.AddRow("u1", id)
	}
	mock.ExpectQuery(regexp.QuoteMeta(testLoadSQL)).WillReturnRows(rows)
}

func TestCreateCache_RegistrationCallError_Fails(t *testing.T) {
	t.Setenv("DB_CACHE_SF_REGISTER_STREAMS", "")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	expectPrerequisites(mock)
	mock.ExpectQuery(regexp.QuoteMeta(testRegisterCall)).
		WithArgs("MY_DB", "PUBLIC", "API_KEYS").
		WillReturnError(errors.New("Table 'MY_DB.PUBLIC.API_KEYS' does not exist or not authorized."))

	_, err = CreateCache[testItem](nil, testLoadSQL, []string{"PUBLIC.API_KEYS"}, "UserID", time.Hour, db, "MY_DB.PUBLIC")
	if err == nil {
		t.Fatal("expected CreateCache to fail when stream registration fails")
	}
	if !strings.Contains(err.Error(), "MY_DB.PUBLIC.API_KEYS") || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected the error to name the table and the reason, got: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCreateCache_RegistrationFailedResult_Fails(t *testing.T) {
	t.Setenv("DB_CACHE_SF_REGISTER_STREAMS", "")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	// Older REGISTERCACHETABLE versions return the failure as text instead of raising.
	expectPrerequisites(mock)
	mock.ExpectQuery(regexp.QuoteMeta(testRegisterCall)).
		WithArgs("MY_DB", "PUBLIC", "API_KEYS").
		WillReturnRows(sqlmock.NewRows([]string{"result"}).AddRow("Table Registration Failed = Insufficient privileges"))

	_, err = CreateCache[testItem](nil, testLoadSQL, []string{"PUBLIC.API_KEYS"}, "UserID", time.Hour, db, "MY_DB.PUBLIC")
	if err == nil || !strings.Contains(err.Error(), "Insufficient privileges") {
		t.Fatalf("expected CreateCache to fail with the procedure's reason, got: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCreateCache_RegistrationOptOut_SkipsRegistration(t *testing.T) {
	t.Setenv("DB_CACHE_SF_REGISTER_STREAMS", "false")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	// No REGISTERCACHETABLE call is expected; sqlmock fails on any unexpected query.
	expectPrerequisites(mock)
	expectFingerprint(mock, "fp1")
	expectLoad(mock, 1)

	if _, err := CreateCache[testItem](nil, testLoadSQL, []string{"PUBLIC.API_KEYS"}, "UserID", time.Hour, db, "MY_DB.PUBLIC"); err != nil {
		t.Fatalf("CreateCache with registration opted out failed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestCreateCache_DefaultMaxAge(t *testing.T) {
	t.Setenv("DB_CACHE_SF_REGISTER_STREAMS", "false")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	expectPrerequisites(mock)
	expectFingerprint(mock, "fp1")
	expectLoad(mock, 1)

	cache, err := CreateCache[testItem](nil, testLoadSQL, []string{"PUBLIC.API_KEYS"}, "UserID", time.Hour, db, "MY_DB.PUBLIC")
	if err != nil {
		t.Fatalf("CreateCache failed: %v", err)
	}
	c := cache.(*dbCache[testItem])
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	if c.maxAge != DefaultMaxAge {
		t.Fatalf("expected default max age %s, got %s", DefaultMaxAge, c.maxAge)
	}
	if c.lastLoad.IsZero() {
		t.Fatal("expected the initial load to start the max-age clock")
	}
}

// newWiredCache returns a dbCache wired to a sqlmock DB, seeded by one load of
// rows (ids) under fingerprint fp1, with the max-age clock set back by loadedAgo.
func newWiredCache(t *testing.T, logs *bytes.Buffer, maxAge, loadedAgo time.Duration, ids ...int) (*dbCache[testItem], sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	c := &dbCache[testItem]{
		db:                    db,
		logger:                log.New(logs, "", 0),
		keyCache:              make(map[string][]testItem),
		monitoredTables:       []string{"API_KEYS"},
		fingerprintTableNames: []string{"MY_DB.PUBLIC.API_KEYS"},
		loadSQL:               testLoadSQL,
		keyField:              "UserID",
		logSchema:             "DB_CACHE",
		logDatabase:           "MY_DB",
		maxAge:                maxAge,
	}
	expectLoad(mock, ids...)
	fp := "fp1"
	if _, err := c.reload(&fp); err != nil {
		t.Fatalf("seed load failed: %v", err)
	}
	c.lastLoad = time.Now().Add(-loadedAgo)
	return c, mock
}

func TestMaxAge_ReloadsWhenFingerprintUnchanged(t *testing.T) {
	var logs bytes.Buffer
	c, mock := newWiredCache(t, &logs, time.Hour, 2*time.Hour, 1)

	expectFingerprint(mock, "fp1") // CACHE_LOG unchanged
	expectLoad(mock, 1)            // same data

	if err := c.refreshOnce(); err != nil {
		t.Fatalf("refreshOnce failed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expected a max-age reload: %v", err)
	}
	if time.Since(c.lastLoad) > time.Minute {
		t.Fatalf("expected the max-age reload to restart the clock, lastLoad=%s", c.lastLoad)
	}
	if strings.Contains(logs.String(), "WARNING") {
		t.Fatalf("unchanged data must not warn; logs: %s", logs.String())
	}
}

func TestMaxAge_WarnsWhenNewDataButFingerprintUnchanged(t *testing.T) {
	var logs bytes.Buffer
	c, mock := newWiredCache(t, &logs, time.Hour, 2*time.Hour, 1)

	expectFingerprint(mock, "fp1") // CACHE_LOG unchanged ...
	expectLoad(mock, 1, 2)         // ... but the table has new rows: dead stream

	if err := c.refreshOnce(); err != nil {
		t.Fatalf("refreshOnce failed: %v", err)
	}
	if !strings.Contains(logs.String(), "WARNING: max-age reload") || !strings.Contains(logs.String(), "API_KEYS") {
		t.Fatalf("expected a dead-stream warning naming the table; logs: %s", logs.String())
	}
	if got := len(c.GetAll()); got != 2 {
		t.Fatalf("expected the new data to be served, got %d rows", got)
	}
}

func TestMaxAge_NoReloadWithinMaxAge(t *testing.T) {
	var logs bytes.Buffer
	c, mock := newWiredCache(t, &logs, time.Hour, 10*time.Minute, 1)

	expectFingerprint(mock, "fp1") // no load expected: sqlmock errors on an unexpected query

	if err := c.refreshOnce(); err != nil {
		t.Fatalf("refreshOnce failed (unexpected reload?): %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestMaxAge_SetMaxAgeZeroDisables(t *testing.T) {
	var logs bytes.Buffer
	c, mock := newWiredCache(t, &logs, time.Hour, 48*time.Hour, 1)
	c.SetMaxAge(0)

	expectFingerprint(mock, "fp1") // no load expected

	if err := c.refreshOnce(); err != nil {
		t.Fatalf("refreshOnce failed (unexpected reload?): %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestMaxAge_FingerprintReloadRestartsClock(t *testing.T) {
	var logs bytes.Buffer
	c, mock := newWiredCache(t, &logs, time.Hour, 30*time.Minute, 1)

	expectFingerprint(mock, "fp2") // CACHE_LOG changed: normal reload
	expectLoad(mock, 1, 2)

	if err := c.refreshOnce(); err != nil {
		t.Fatalf("refreshOnce failed: %v", err)
	}
	if time.Since(c.lastLoad) > time.Minute {
		t.Fatalf("expected a fingerprint reload to restart the clock, lastLoad=%s", c.lastLoad)
	}
	if strings.Contains(logs.String(), "WARNING") {
		t.Fatalf("a fingerprint-driven reload must not warn; logs: %s", logs.String())
	}
}

func TestMaxAge_ForceRefreshRestartsClock(t *testing.T) {
	var logs bytes.Buffer
	c, mock := newWiredCache(t, &logs, time.Hour, 2*time.Hour, 1)

	expectFingerprint(mock, "fp1")
	expectLoad(mock, 1)

	if err := c.ForceRefresh(); err != nil {
		t.Fatalf("ForceRefresh failed: %v", err)
	}
	if time.Since(c.lastLoad) > time.Minute {
		t.Fatalf("expected ForceRefresh to restart the clock, lastLoad=%s", c.lastLoad)
	}
}

func TestMaxAge_ReloadFailureReturnsError(t *testing.T) {
	var logs bytes.Buffer
	c, mock := newWiredCache(t, &logs, time.Hour, 2*time.Hour, 1)

	expectFingerprint(mock, "fp1")
	mock.ExpectQuery(regexp.QuoteMeta(testLoadSQL)).
		WillReturnError(errors.New("Table 'MY_DB.PUBLIC.API_KEYS' does not exist or not authorized."))

	if err := c.refreshOnce(); err == nil {
		t.Fatal("expected the failed max-age reload to return an error (it feeds OnRefreshError)")
	}
	if got := len(c.GetAll()); got != 1 {
		t.Fatalf("expected the last good data to keep being served, got %d rows", got)
	}
}

func TestRowsHash_IgnoresOrder(t *testing.T) {
	u, one, two := "u1", 1, 2
	a := testItem{UserID: &u, ID: &one}
	b := testItem{UserID: &u, ID: &two}

	h1, ok1 := rowsHash([]testItem{a, b})
	h2, ok2 := rowsHash([]testItem{b, a})
	h3, _ := rowsHash([]testItem{a})
	if !ok1 || !ok2 {
		t.Fatal("expected rows to hash")
	}
	if h1 != h2 {
		t.Fatal("expected the hash to ignore row order")
	}
	if h1 == h3 {
		t.Fatal("expected different rows to hash differently")
	}
}
