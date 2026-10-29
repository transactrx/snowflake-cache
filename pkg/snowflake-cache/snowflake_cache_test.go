package snowflakecache

import (
	"database/sql"
	"errors"
	"io"
	"log"
	"regexp"
	"strings"
	"sync/atomic"
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
	defer cache.Close()

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
	defer cache.Close()

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

func TestCreateSnowflakeCache_MapRows(t *testing.T) {
	db, mock := newCacheMock(t)

	// Snowflake returns unquoted column names upper-cased.
	loadSQL := "SELECT user_id, id FROM api_keys"
	mock.ExpectQuery(regexp.QuoteMeta(loadSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"USER_ID", "ID"}).
			AddRow("u1", 1).
			AddRow("u1", 2).
			AddRow("u2", 3))

	cache, err := CreateCache[map[string]any](nil, loadSQL, []string{"PUBLIC.API_KEYS"}, "user_id", time.Hour, db, "MY_DB.PUBLIC")
	if err != nil {
		t.Fatalf("CreateCache failed: %v", err)
	}

	u1 := cache.Get("u1")
	if len(u1) != 2 {
		t.Fatalf("expected 2 rows for u1, got %d", len(u1))
	}
	if u1[0]["USER_ID"] != "u1" {
		t.Fatalf("expected row columns to be preserved, got %v", u1[0])
	}
	if len(cache.GetAll()) != 3 {
		t.Fatalf("expected 3 rows in total, got %d", len(cache.GetAll()))
	}
}

func TestCreateSnowflakeCache_MapRows_NullKey(t *testing.T) {
	db, mock := newCacheMock(t)

	// A NULL key column must fail the initial load rather than produce a cache.
	loadSQL := "SELECT user_id, id FROM api_keys"
	mock.ExpectQuery(regexp.QuoteMeta(loadSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"USER_ID", "ID"}).
			AddRow("u1", 1).
			AddRow(nil, 2))

	cache, err := CreateCache[map[string]any](nil, loadSQL, []string{"PUBLIC.API_KEYS"}, "user_id", time.Hour, db, "MY_DB.PUBLIC")
	if err == nil {
		t.Fatalf("expected CreateCache to fail on a NULL key column, got cache %v", cache)
	}
	if !strings.Contains(err.Error(), "key field 'user_id' is nil") {
		t.Fatalf("expected a key column error, got %v", err)
	}
}

type namedRow map[string]any

func TestCreateSnowflakeCache_NamedMapRows(t *testing.T) {
	db, mock := newCacheMock(t)

	loadSQL := "SELECT user_id, id FROM api_keys"
	mock.ExpectQuery(regexp.QuoteMeta(loadSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"USER_ID", "ID"}).
			AddRow("u1", 1).
			AddRow("u2", 2))

	cache, err := CreateCache[namedRow](nil, loadSQL, []string{"PUBLIC.API_KEYS"}, "user_id", time.Hour, db, "MY_DB.PUBLIC")
	if err != nil {
		t.Fatalf("CreateCache failed: %v", err)
	}

	u1 := cache.Get("u1")
	if len(u1) != 1 || u1[0]["USER_ID"] != "u1" {
		t.Fatalf("expected one row for u1 with its columns, got %v", u1)
	}
}

func TestExtractKeyValue_Map(t *testing.T) {
	str := "u1"
	var nilStr *string
	tests := []struct {
		name    string
		row     any
		key     string
		want    string
		wantErr bool
	}{
		{"exact string", map[string]any{"user_id": "u1"}, "user_id", "u1", false},
		{"case-insensitive", map[string]any{"USER_ID": "u1"}, "user_id", "u1", false},
		{"upper keyField, lower column", map[string]any{"user_id": "u1"}, "USER_ID", "u1", false},
		{"mixed case both sides", map[string]any{"User_Id": "u1"}, "user_ID", "u1", false},
		{"differs beyond case", map[string]any{"USERID": "u1"}, "user_id", "", true},
		{"empty map", map[string]any{}, "id", "", true},
		{"string type", "abc", "id", "", true},
		{"int type", 42, "id", "", true},
		{"int64 value", map[string]any{"ID": int64(42)}, "id", "42", false},
		{"float value", map[string]any{"ID": 1.5}, "id", "1.5", false},
		{"typed map", map[string]string{"ID": "x"}, "id", "x", false},
		{"named map type", namedRow{"ID": "x"}, "id", "x", false},
		{"pointer to map", &map[string]any{"ID": "p"}, "id", "p", false},
		{"pointer value", map[string]any{"ID": &str}, "id", "u1", false},
		{"nil pointer value", map[string]any{"ID": nilStr}, "id", "", true},
		{"missing key", map[string]any{"OTHER": "x"}, "id", "", true},
		{"nil value", map[string]any{"ID": nil}, "id", "", true},
		{"unsupported value", map[string]any{"ID": true}, "id", "", true},
		{"non-string map key", map[int]any{1: "x"}, "id", "", true},
		{"struct field not found", testItem{}, "missing", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractKeyValue(tt.row, tt.key)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// expectCacheSetup registers the queries every cache constructor runs before its
// initial load: prerequisite checks, stream registration, and the first fingerprint.
func expectCacheSetup(mock sqlmock.Sqlmock) {
	schemaQuery := "SELECT COUNT(*) FROM MY_DB.INFORMATION_SCHEMA.SCHEMATA WHERE SCHEMA_NAME = ?"
	mock.ExpectQuery(regexp.QuoteMeta(schemaQuery)).
		WithArgs("DB_CACHE").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	procQuery := "SELECT COUNT(*) FROM MY_DB.INFORMATION_SCHEMA.PROCEDURES WHERE PROCEDURE_SCHEMA = ? AND PROCEDURE_NAME = ?"
	mock.ExpectQuery(regexp.QuoteMeta(procQuery)).
		WithArgs("DB_CACHE", "REGISTERCACHETABLE").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	registerCall := "CALL MY_DB.DB_CACHE.REGISTERCACHETABLE(?, ?, ?)"
	mock.ExpectQuery(regexp.QuoteMeta(registerCall)).
		WithArgs("MY_DB", "PUBLIC", "API_KEYS").
		WillReturnRows(sqlmock.NewRows([]string{"result"}).AddRow("OK"))
	expectFingerprint(mock, "fp1")
}

func expectFingerprint(mock sqlmock.Sqlmock, fp string) {
	fpQuery := "SELECT COUNT(*) || TO_VARCHAR(COALESCE(MAX(update_time), TO_TIMESTAMP_TZ('1980-01-01'))) AS ct FROM MY_DB.DB_CACHE.CACHE_LOG WHERE table_name = ?"
	mock.ExpectQuery(regexp.QuoteMeta(fpQuery)).
		WithArgs("MY_DB.PUBLIC.API_KEYS").
		WillReturnRows(sqlmock.NewRows([]string{"ct"}).AddRow(fp))
}

// newCacheMock returns a mock connection expecting the cache setup queries. When the
// test ends it checks that every expectation was met and closes the connection.
func newCacheMock(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	expectCacheSetup(mock)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
		db.Close()
	})
	return db, mock
}

// scanRowMaps is a minimal RowsLoader that keeps each column's driver value as-is.
func scanRowMaps(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var result []map[string]any
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = values[i]
		}
		result = append(result, row)
	}
	return result, nil
}

const loaderSQL = "SELECT id, name FROM api_keys"

func loaderRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"ID", "NAME"}).
		AddRow(int64(42), "a").
		AddRow(int64(42), "b").
		AddRow(int64(7), "c")
}

func TestCreateCacheWithRowsLoader_NumericKey(t *testing.T) {
	db, mock := newCacheMock(t)
	mock.ExpectQuery(regexp.QuoteMeta(loaderSQL)).WillReturnRows(loaderRows())

	cache, err := CreateCacheWithRowsLoader[map[string]any](nil, loaderSQL, []string{"PUBLIC.API_KEYS"}, "id",
		time.Hour, db, "MY_DB.PUBLIC", scanRowMaps)
	if err != nil {
		t.Fatalf("CreateCacheWithRowsLoader failed: %v", err)
	}

	rows42 := cache.Get("42")
	if len(rows42) != 2 {
		t.Fatalf("expected 2 rows for key 42, got %d", len(rows42))
	}
	if rows42[0]["ID"] != int64(42) {
		t.Fatalf("expected the loader's int64 value to be kept, got %T %v", rows42[0]["ID"], rows42[0]["ID"])
	}
	if len(cache.Get("7")) != 1 {
		t.Fatalf("expected 1 row for key 7, got %d", len(cache.Get("7")))
	}
}

func TestCreateCacheWithRowsLoader_LoaderError(t *testing.T) {
	db, mock := newCacheMock(t)
	mock.ExpectQuery(regexp.QuoteMeta(loaderSQL)).WillReturnRows(loaderRows())

	errBoom := errors.New("boom")
	failingLoader := func(rows *sql.Rows) ([]map[string]any, error) { return nil, errBoom }

	_, err := CreateCacheWithRowsLoader[map[string]any](nil, loaderSQL, []string{"PUBLIC.API_KEYS"}, "id",
		time.Hour, db, "MY_DB.PUBLIC", failingLoader)
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected the loader error to be returned, got %v", err)
	}
}

func TestCreateCacheWithRowsLoader_ForceRefreshUsesLoader(t *testing.T) {
	db, mock := newCacheMock(t)
	mock.ExpectQuery(regexp.QuoteMeta(loaderSQL)).WillReturnRows(loaderRows())

	calls := 0
	countingLoader := func(rows *sql.Rows) ([]map[string]any, error) {
		calls++
		return scanRowMaps(rows)
	}

	cache, err := CreateCacheWithRowsLoader[map[string]any](nil, loaderSQL, []string{"PUBLIC.API_KEYS"}, "id",
		time.Hour, db, "MY_DB.PUBLIC", countingLoader)
	if err != nil {
		t.Fatalf("CreateCacheWithRowsLoader failed: %v", err)
	}

	expectFingerprint(mock, "fp2")
	mock.ExpectQuery(regexp.QuoteMeta(loaderSQL)).WillReturnRows(loaderRows())

	if err := cache.ForceRefresh(); err != nil {
		t.Fatalf("ForceRefresh failed: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected the loader to run for the initial load and the refresh, got %d calls", calls)
	}
}

func TestCreateCacheWithRowsLoader_QueryError(t *testing.T) {
	db, mock := newCacheMock(t)
	errQuery := errors.New("query failed")
	mock.ExpectQuery(regexp.QuoteMeta(loaderSQL)).WillReturnError(errQuery)

	_, err := CreateCacheWithRowsLoader[map[string]any](nil, loaderSQL, []string{"PUBLIC.API_KEYS"}, "id",
		time.Hour, db, "MY_DB.PUBLIC", scanRowMaps)
	if !errors.Is(err, errQuery) {
		t.Fatalf("expected the query error to be returned, got %v", err)
	}
}

func TestCreateCacheWithRowsLoader_RowsError(t *testing.T) {
	db, mock := newCacheMock(t)
	errRow := errors.New("row failed")
	mock.ExpectQuery(regexp.QuoteMeta(loaderSQL)).WillReturnRows(loaderRows().RowError(1, errRow))

	// A loader that stops at the first failed Next without checking rows.Err must
	// still surface the error.
	_, err := CreateCacheWithRowsLoader[map[string]any](nil, loaderSQL, []string{"PUBLIC.API_KEYS"}, "id",
		time.Hour, db, "MY_DB.PUBLIC", scanRowMaps)
	if !errors.Is(err, errRow) {
		t.Fatalf("expected the rows error to be returned, got %v", err)
	}
}

func TestCreateCacheWithRowsLoader_InvalidArgs(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()

	tests := []struct {
		name       string
		connection *sql.DB
		schema     string
		tables     []string
	}{
		{"nil connection", nil, "MY_DB.PUBLIC", []string{"PUBLIC.API_KEYS"}},
		{"schema too many parts", db, "A.B.C", []string{"PUBLIC.API_KEYS"}},
		{"invalid monitored table", db, "MY_DB.PUBLIC", []string{"A.B.C.D"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CreateCacheWithRowsLoader[map[string]any](nil, loaderSQL, tt.tables, "id",
				time.Hour, tt.connection, tt.schema, scanRowMaps)
			if err == nil {
				t.Fatalf("expected an error")
			}
		})
	}
}

func TestCreateCacheWithRowsLoader_NilLoader(t *testing.T) {
	db, mock := newCacheMock(t)
	loadSQL := "SELECT user_id, id FROM api_keys"
	mock.ExpectQuery(regexp.QuoteMeta(loadSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"USER_ID", "ID"}).
			AddRow("u1", 1).
			AddRow("u2", 2))

	cache, err := CreateCacheWithRowsLoader[map[string]any](nil, loadSQL, []string{"PUBLIC.API_KEYS"}, "user_id",
		time.Hour, db, "MY_DB.PUBLIC", nil)
	if err != nil {
		t.Fatalf("CreateCacheWithRowsLoader failed: %v", err)
	}
	if len(cache.Get("u1")) != 1 || len(cache.GetAll()) != 2 {
		t.Fatalf("expected sqlscan loading with a nil loader; got u1=%d all=%d", len(cache.Get("u1")), len(cache.GetAll()))
	}
}

func TestSnowflakeCache_Close(t *testing.T) {
	db, mock := newCacheMock(t)
	mock.ExpectQuery(regexp.QuoteMeta(loaderSQL)).WillReturnRows(loaderRows())

	// A short interval so a running poller would refresh during the wait below; with no
	// further queries expected, any refresh after Close would fail and reach the handler.
	cache, err := CreateCacheWithRowsLoader[map[string]any](nil, loaderSQL, []string{"PUBLIC.API_KEYS"}, "id",
		20*time.Millisecond, db, "MY_DB.PUBLIC", scanRowMaps)
	if err != nil {
		t.Fatalf("CreateCacheWithRowsLoader failed: %v", err)
	}
	var refreshFailures atomic.Int32
	cache.OnRefreshError(func(err error, consecutiveFailures int) { refreshFailures.Add(1) })

	cache.Close()
	cache.Close() // safe to call again

	time.Sleep(100 * time.Millisecond)
	if n := refreshFailures.Load(); n != 0 {
		t.Fatalf("expected no refreshes after Close, got %d failed refreshes", n)
	}

	if !errors.Is(cache.ForceRefresh(), ErrCacheClosed) {
		t.Fatalf("expected ForceRefresh to return ErrCacheClosed after Close")
	}

	if len(cache.Get("42")) != 2 || len(cache.GetAll()) != 3 {
		t.Fatalf("expected the last loaded data after Close; got 42=%d all=%d", len(cache.Get("42")), len(cache.GetAll()))
	}
}

func TestSnowflakeCache_NonPositiveIntervalDisablesPolling(t *testing.T) {
	for _, interval := range []time.Duration{0, -time.Second} {
		t.Run(interval.String(), func(t *testing.T) {
			db, mock := newCacheMock(t)
			mock.ExpectQuery(regexp.QuoteMeta(loaderSQL)).WillReturnRows(loaderRows())

			cache, err := CreateCacheWithRowsLoader[map[string]any](nil, loaderSQL, []string{"PUBLIC.API_KEYS"}, "id",
				interval, db, "MY_DB.PUBLIC", scanRowMaps)
			if err != nil {
				t.Fatalf("CreateCacheWithRowsLoader failed: %v", err)
			}
			var refreshFailures atomic.Int32
			cache.OnRefreshError(func(err error, consecutiveFailures int) { refreshFailures.Add(1) })

			time.Sleep(50 * time.Millisecond)
			if n := refreshFailures.Load(); n != 0 {
				t.Fatalf("expected no background refreshes, got %d failed refreshes", n)
			}
			if len(cache.Get("42")) != 2 {
				t.Fatalf("expected the initially loaded data, got 42=%d", len(cache.Get("42")))
			}

			cache.Close()
			if !errors.Is(cache.ForceRefresh(), ErrCacheClosed) {
				t.Fatalf("expected ForceRefresh to return ErrCacheClosed after Close")
			}
		})
	}
}

func TestSnowflakeCache_PollerRefreshes(t *testing.T) {
	db, mock := newCacheMock(t)
	mock.ExpectQuery(regexp.QuoteMeta(loaderSQL)).WillReturnRows(loaderRows())

	// The first background tick sees a new fingerprint and reloads.
	expectFingerprint(mock, "fp2")
	mock.ExpectQuery(regexp.QuoteMeta(loaderSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"ID", "NAME"}).AddRow(int64(99), "z"))

	cache, err := CreateCacheWithRowsLoader[map[string]any](nil, loaderSQL, []string{"PUBLIC.API_KEYS"}, "id",
		20*time.Millisecond, db, "MY_DB.PUBLIC", scanRowMaps)
	if err != nil {
		t.Fatalf("CreateCacheWithRowsLoader failed: %v", err)
	}
	defer cache.Close()

	deadline := time.Now().Add(time.Second)
	for len(cache.Get("99")) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("expected the poller to reload the cache")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(cache.Get("42")) != 0 {
		t.Fatalf("expected the reload to replace the previous data")
	}
}

func TestSnowflakeCache_CloseDuringRefresh(t *testing.T) {
	db, mock := newCacheMock(t)
	mock.ExpectQuery(regexp.QuoteMeta(loaderSQL)).WillReturnRows(loaderRows())
	expectFingerprint(mock, "fp2")
	mock.ExpectQuery(regexp.QuoteMeta(loaderSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"ID", "NAME"}).AddRow(int64(99), "z"))

	// The second load (the refresh) blocks until released, so Close runs while it is in progress.
	started := make(chan struct{})
	release := make(chan struct{})
	loads := 0
	blockingLoader := func(rows *sql.Rows) ([]map[string]any, error) {
		loads++
		if loads == 2 {
			close(started)
			<-release
		}
		return scanRowMaps(rows)
	}

	cache, err := CreateCacheWithRowsLoader[map[string]any](nil, loaderSQL, []string{"PUBLIC.API_KEYS"}, "id",
		time.Hour, db, "MY_DB.PUBLIC", blockingLoader)
	if err != nil {
		t.Fatalf("CreateCacheWithRowsLoader failed: %v", err)
	}

	refreshErr := make(chan error)
	go func() { refreshErr <- cache.ForceRefresh() }()

	<-started
	cache.Close()
	close(release)

	if err := <-refreshErr; err != nil {
		t.Fatalf("expected the in-progress refresh to finish normally, got %v", err)
	}
	if len(cache.Get("99")) != 1 {
		t.Fatalf("expected the in-progress refresh to store its data")
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
