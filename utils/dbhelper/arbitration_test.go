package dbhelper

import (
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// mockDB returns a *sqlx.DB whose DriverName() is the supplied string, backed by sqlmock.
func mockDB(t *testing.T, driver string) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { raw.Close() })
	return sqlx.NewDb(raw, driver), mock
}

// newSQLiteArbitrationDB opens an in-memory SQLite DB and creates the heartbeat table.
func newSQLiteArbitrationDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sqlite connect: %v", err)
	}
	// Mirror the production arbitrator connection limits (arbitrator.go ~212-215).
	// SQLite in-memory databases are per-connection; a second connection sees an empty DB.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { db.Close() })
	if err := SetHeartbeatTable(db); err != nil {
		t.Fatalf("SetHeartbeatTable: %v", err)
	}
	return db
}

// ---- dialect helper unit tests ----

func TestHeartbeatTable(t *testing.T) {
	mysqlDB, _ := mockDB(t, "mysql")
	sqliteDB, _ := mockDB(t, "sqlite")
	if got := heartbeatTable(mysqlDB); got != "replication_manager_schema.heartbeat" {
		t.Errorf("mysql: got %q, want replication_manager_schema.heartbeat", got)
	}
	if got := heartbeatTable(sqliteDB); got != "heartbeat" {
		t.Errorf("sqlite: got %q, want heartbeat", got)
	}
}

func TestNowExpr(t *testing.T) {
	mysqlDB, _ := mockDB(t, "mysql")
	sqliteDB, _ := mockDB(t, "sqlite")
	if got := nowExpr(mysqlDB); got != "NOW()" {
		t.Errorf("mysql: got %q, want NOW()", got)
	}
	if got := nowExpr(sqliteDB); got != "DATETIME('now')" {
		t.Errorf("sqlite: got %q, want DATETIME('now')", got)
	}
}

func TestTenSecondsAgoExpr(t *testing.T) {
	mysqlDB, _ := mockDB(t, "mysql")
	sqliteDB, _ := mockDB(t, "sqlite")
	if got := tenSecondsAgoExpr(mysqlDB); got != "DATE_SUB(NOW(), INTERVAL 10 SECOND)" {
		t.Errorf("mysql: got %q", got)
	}
	if got := tenSecondsAgoExpr(sqliteDB); got != "DATETIME('now', '-10 seconds')" {
		t.Errorf("sqlite: got %q", got)
	}
}

func TestUpsertVerb(t *testing.T) {
	mysqlDB, _ := mockDB(t, "mysql")
	sqliteDB, _ := mockDB(t, "sqlite")
	if got := upsertVerb(mysqlDB); got != "REPLACE" {
		t.Errorf("mysql: got %q, want REPLACE", got)
	}
	if got := upsertVerb(sqliteDB); got != "INSERT OR REPLACE" {
		t.Errorf("sqlite: got %q, want INSERT OR REPLACE", got)
	}
}

// ---- SQLite in-memory integration tests ----

func TestWriteHeartbeat_SQLite(t *testing.T) {
	db := newSQLiteArbitrationDB(t)
	if err := WriteHeartbeat(db, "uuid1", "secret1", "cluster1", "master1", 1, 2, 0); err != nil {
		t.Fatalf("WriteHeartbeat: %v", err)
	}
}

func TestRequestArbitration_Winner_SQLite(t *testing.T) {
	db := newSQLiteArbitrationDB(t)
	// Empty table: no elected managers, no competing unelected managers → wins.
	if !RequestArbitration(db, "uuid1", "secret1", "cluster1", "master1", 1, 2, 0) {
		t.Fatal("expected RequestArbitration to return true on empty table")
	}
}

func TestGetArbitrationMaster_SQLite(t *testing.T) {
	db := newSQLiteArbitrationDB(t)
	RequestArbitration(db, "uuid1", "secret1", "cluster1", "master1", 1, 2, 0)
	got := GetArbitrationMaster(db, "secret1", "cluster1")
	if got != "master1" {
		t.Errorf("GetArbitrationMaster: got %q, want master1", got)
	}
}

func TestForgetArbitration_SQLite(t *testing.T) {
	db := newSQLiteArbitrationDB(t)
	RequestArbitration(db, "uuid1", "secret1", "cluster1", "master1", 1, 2, 0)
	if err := ForgetArbitration(db, "secret1"); err != nil {
		t.Fatalf("ForgetArbitration: %v", err)
	}
	if got := GetArbitrationMaster(db, "secret1", "cluster1"); got != "" {
		t.Errorf("expected empty master after ForgetArbitration, got %q", got)
	}
}

// ---- MySQL SQL dialect mock tests ----

func TestForgetArbitration_MySQL_SQL(t *testing.T) {
	db, mock := mockDB(t, "mysql")
	mock.ExpectExec(regexp.QuoteMeta(
		"DELETE FROM replication_manager_schema.heartbeat WHERE secret=?",
	)).WillReturnResult(sqlmock.NewResult(1, 1))

	if err := ForgetArbitration(db, "secret1"); err != nil {
		t.Fatalf("ForgetArbitration: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestGetArbitrationMaster_MySQL_SQL(t *testing.T) {
	db, mock := mockDB(t, "mysql")
	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT master FROM replication_manager_schema.heartbeat WHERE cluster=? AND secret=?  AND status IN ('E')",
	)).WillReturnRows(sqlmock.NewRows([]string{"master"}).AddRow("master1"))

	if got := GetArbitrationMaster(db, "secret1", "cluster1"); got != "master1" {
		t.Errorf("got %q, want master1", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRequestArbitration_Winner_MySQL_SQL(t *testing.T) {
	db, mock := mockDB(t, "mysql")

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT count(*) FROM replication_manager_schema.heartbeat WHERE cluster=? AND secret=? AND status='E' AND uid<>? AND date > DATE_SUB(NOW(), INTERVAL 10 SECOND) FOR UPDATE",
	)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT count(*) FROM replication_manager_schema.heartbeat WHERE cluster=? AND secret=? AND status = 'U' and uid <> ?  and failed < ? FOR UPDATE",
	)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(regexp.QuoteMeta(
		"REPLACE INTO replication_manager_schema.heartbeat (secret,uuid,uid,master,date,arbitration_date,cluster,hosts,failed,status) VALUES(?,?,?,?,NOW(),NOW(),?,?,?,'E')",
	)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if !RequestArbitration(db, "uuid1", "secret1", "cluster1", "master1", 1, 2, 0) {
		t.Fatal("expected RequestArbitration to return true")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestWriteHeartbeat_MySQL_SQL(t *testing.T) {
	db, mock := mockDB(t, "mysql")

	mock.ExpectExec(regexp.QuoteMeta(
		"INSERT INTO replication_manager_schema.heartbeat (secret,uuid,uid,master,date,cluster,hosts,failed) VALUES(?,?,?,?,NOW(),?,?,?) ON DUPLICATE KEY UPDATE uuid=VALUES(uuid), master=VALUES(master), date=NOW(), hosts=VALUES(hosts), failed=VALUES(failed)",
	)).WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT count(distinct master) FROM replication_manager_schema.heartbeat WHERE cluster=? AND secret=? AND date > DATE_SUB(NOW(), INTERVAL 10 SECOND)",
	)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	mock.ExpectExec(regexp.QuoteMeta(
		"UPDATE replication_manager_schema.heartbeat set status='U' WHERE status='E' AND cluster=? AND secret=? AND uid NOT IN (SELECT uid FROM (SELECT uid FROM replication_manager_schema.heartbeat WHERE status='E' AND cluster=? AND secret=? AND date > DATE_SUB(NOW(), INTERVAL 10 SECOND) ORDER BY arbitration_date ASC LIMIT 1) t)",
	)).WillReturnResult(sqlmock.NewResult(0, 0))

	if err := WriteHeartbeat(db, "uuid1", "secret1", "cluster1", "master1", 1, 2, 0); err != nil {
		t.Fatalf("WriteHeartbeat: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
