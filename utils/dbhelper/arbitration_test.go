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
		"SELECT count(*) FROM replication_manager_schema.heartbeat WHERE cluster=? AND secret=? AND status='E' and uid<>?",
	)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT count(*) FROM replication_manager_schema.heartbeat WHERE cluster=? AND secret=? AND status = 'U' and uid <> ?  and failed < ?",
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

// ---- GetArbitrationWinnerUUID tests ----

func TestGetArbitrationWinnerUUID_SQLite(t *testing.T) {
	db := newSQLiteArbitrationDB(t)

	// No elected row → returns ""
	if got := GetArbitrationWinnerUUID(db, "secret1", "cluster1"); got != "" {
		t.Errorf("expected empty UUID before election, got %q", got)
	}

	// After election, the elected UUID is returned.
	RequestArbitration(db, "uuid-winner", "secret1", "cluster1", "master1", 1, 2, 0)
	if got := GetArbitrationWinnerUUID(db, "secret1", "cluster1"); got != "uuid-winner" {
		t.Errorf("GetArbitrationWinnerUUID: got %q, want uuid-winner", got)
	}

	// Different secret → no match.
	if got := GetArbitrationWinnerUUID(db, "other-secret", "cluster1"); got != "" {
		t.Errorf("expected empty UUID for wrong secret, got %q", got)
	}
}

func TestGetArbitrationWinnerUUID_MySQL_SQL(t *testing.T) {
	db, mock := mockDB(t, "mysql")
	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT uuid FROM replication_manager_schema.heartbeat WHERE cluster=? AND secret=? AND status='E'",
	)).WillReturnRows(sqlmock.NewRows([]string{"uuid"}).AddRow("uuid-winner"))

	if got := GetArbitrationWinnerUUID(db, "secret1", "cluster1"); got != "uuid-winner" {
		t.Errorf("got %q, want uuid-winner", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// ---- GetHeartbeatMasterForUUID tests ----

func TestGetHeartbeatMasterForUUID_SQLite(t *testing.T) {
	db := newSQLiteArbitrationDB(t)

	// No row → returns ""
	if got := GetHeartbeatMasterForUUID(db, "secret1", "cluster2", "uuid-winner"); got != "" {
		t.Errorf("expected empty master before heartbeat, got %q", got)
	}

	// After writing a heartbeat for cluster2 with the winner's UUID, master is returned.
	if err := WriteHeartbeat(db, "uuid-winner", "secret1", "cluster2", "master2", 1, 2, 0); err != nil {
		t.Fatalf("WriteHeartbeat: %v", err)
	}
	if got := GetHeartbeatMasterForUUID(db, "secret1", "cluster2", "uuid-winner"); got != "master2" {
		t.Errorf("GetHeartbeatMasterForUUID: got %q, want master2", got)
	}

	// Different UUID → no match.
	if got := GetHeartbeatMasterForUUID(db, "secret1", "cluster2", "uuid-loser"); got != "" {
		t.Errorf("expected empty master for non-winner UUID, got %q", got)
	}
}

func TestGetHeartbeatMasterForUUID_MySQL_SQL(t *testing.T) {
	db, mock := mockDB(t, "mysql")
	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT master FROM replication_manager_schema.heartbeat WHERE cluster=? AND secret=? AND uuid=?",
	)).WillReturnRows(sqlmock.NewRows([]string{"master"}).AddRow("master2"))

	if got := GetHeartbeatMasterForUUID(db, "secret1", "cluster2", "uuid-winner"); got != "master2" {
		t.Errorf("got %q, want master2", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// ---- End-to-end: loser-side master resolution via two-step lookup ----

// TestLoserMasterResolution_SQLite verifies the combined two-step lookup flow used
// by ReconcileLostArbitrationMaster: first get the winner UUID from the authority
// cluster key, then get the master that winner reported for the target cluster.
func TestLoserMasterResolution_SQLite(t *testing.T) {
	db := newSQLiteArbitrationDB(t)

	const secret = "s"
	const authCluster = "auth-cluster"
	const targetCluster = "target-cluster"
	const winnerUUID = "uuid-A"

	// Simulate: winning repman wins election on authority cluster.
	RequestArbitration(db, winnerUUID, secret, authCluster, "auth-master", 1, 3, 0)

	// Simulate: winning repman also publishes heartbeat for the target cluster.
	if err := WriteHeartbeat(db, winnerUUID, secret, targetCluster, "target-master-A", 2, 3, 0); err != nil {
		t.Fatalf("WriteHeartbeat: %v", err)
	}

	// Two-step lookup:
	uuid := GetArbitrationWinnerUUID(db, secret, authCluster)
	if uuid != winnerUUID {
		t.Fatalf("GetArbitrationWinnerUUID: got %q, want %q", uuid, winnerUUID)
	}
	master := GetHeartbeatMasterForUUID(db, secret, targetCluster, uuid)
	if master != "target-master-A" {
		t.Errorf("GetHeartbeatMasterForUUID: got %q, want target-master-A", master)
	}
}

func TestWriteHeartbeat_MySQL_SQL(t *testing.T) {
	db, mock := mockDB(t, "mysql")

	mock.ExpectExec(regexp.QuoteMeta(
		"REPLACE INTO replication_manager_schema.heartbeat (secret,uuid,uid,master,date,cluster,hosts,failed) VALUES(?,?,?,?,NOW(),?,?,?)",
	)).WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT count(distinct master) FROM replication_manager_schema.heartbeat WHERE cluster=? AND secret=? AND date > DATE_SUB(NOW(), INTERVAL 10 SECOND)",
	)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	mock.ExpectExec(regexp.QuoteMeta(
		"UPDATE replication_manager_schema.heartbeat set status='U' WHERE status='E' AND cluster=? AND secret=?",
	)).WillReturnResult(sqlmock.NewResult(0, 0))

	if err := WriteHeartbeat(db, "uuid1", "secret1", "cluster1", "master1", 1, 2, 0); err != nil {
		t.Fatalf("WriteHeartbeat: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
