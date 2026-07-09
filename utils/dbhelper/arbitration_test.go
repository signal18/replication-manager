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

// TestRequestArbitration_LowestUIDWins_SQLite covers the same-master split
// (triangle base cut): both repman report the identical master and equal
// failed counts, both ask for arbitration. The LOWER uid (the main repman)
// must win regardless of who asks first, and the winner must then hold the
// Elected row for the whole incident.
func TestRequestArbitration_LowestUIDWins_SQLite(t *testing.T) {
	db := newSQLiteArbitrationDB(t)
	// Both sides report fresh heartbeats, same master, failed=0.
	if err := WriteHeartbeat(db, "uuid1", "secret1", "cluster1", "master1", 1, 2, 0); err != nil {
		t.Fatalf("WriteHeartbeat uid1: %v", err)
	}
	if err := WriteHeartbeat(db, "uuid2", "secret1", "cluster1", "master1", 2, 2, 0); err != nil {
		t.Fatalf("WriteHeartbeat uid2: %v", err)
	}
	// The DR (uid 2) asks first: it must LOSE to the fresh lower-uid reporter.
	if RequestArbitration(db, "uuid2", "secret1", "cluster1", "master1", 2, 2, 0) {
		t.Fatal("uid 2 must lose while a fresh uid 1 report exists")
	}
	// The main (uid 1) asks: it must win and mint the Elected row.
	if !RequestArbitration(db, "uuid1", "secret1", "cluster1", "master1", 1, 2, 0) {
		t.Fatal("uid 1 must win the same-master election")
	}
	// First winner holds: uid 2 keeps losing against the fresh Elected row.
	if RequestArbitration(db, "uuid2", "secret1", "cluster1", "master1", 2, 2, 0) {
		t.Fatal("uid 2 must keep losing while uid 1 holds the Elected row")
	}
	if uid, master, found := GetFreshElected(db, "secret1", "cluster1"); !found || uid != 1 || master != "master1" {
		t.Fatalf("expected fresh Elected uid=1 master=master1, got uid=%d master=%q found=%t", uid, master, found)
	}
}

// TestRequestArbitration_DeadMainDRWins_SQLite: the DR (higher uid) must win
// when the main has stopped reporting — its heartbeat row is stale (>10s), so
// it no longer outranks anyone.
func TestRequestArbitration_DeadMainDRWins_SQLite(t *testing.T) {
	db := newSQLiteArbitrationDB(t)
	// Stale report from the main (uid 1), 60 seconds old.
	if _, err := db.Exec("INSERT INTO heartbeat (secret,uuid,uid,master,date,cluster,hosts,failed,status) VALUES('secret1','uuid1',1,'master1',DATETIME('now','-60 seconds'),'cluster1',2,0,'U')"); err != nil {
		t.Fatalf("seed stale row: %v", err)
	}
	if err := WriteHeartbeat(db, "uuid2", "secret1", "cluster1", "master1", 2, 2, 0); err != nil {
		t.Fatalf("WriteHeartbeat uid2: %v", err)
	}
	if !RequestArbitration(db, "uuid2", "secret1", "cluster1", "master1", 2, 2, 0) {
		t.Fatal("uid 2 must win when the uid 1 report is stale (main dead)")
	}
}

// TestRequestArbitration_StaleMinorityDoesNotVeto_SQLite covers the
// minority-with-master failover permission (triangle corner cut): the cut
// minority's row froze at failed=0 (blind to the failure) BEFORE going
// stale; the majority reports failed=1. The stale frozen row must NOT veto
// the majority — the minority removes itself from the count by expiring.
func TestRequestArbitration_StaleMinorityDoesNotVeto_SQLite(t *testing.T) {
	db := newSQLiteArbitrationDB(t)
	// Minority (uid 1) last reported failed=0, 60 seconds ago, then was cut.
	if _, err := db.Exec("INSERT INTO heartbeat (secret,uuid,uid,master,date,cluster,hosts,failed,status) VALUES('secret1','uuid1',1,'master1',DATETIME('now','-60 seconds'),'cluster1',2,0,'U')"); err != nil {
		t.Fatalf("seed stale row: %v", err)
	}
	// Majority (uid 2) reports fresh with the failure it sees.
	if err := WriteHeartbeat(db, "uuid2", "secret1", "cluster1", "master1", 2, 2, 1); err != nil {
		t.Fatalf("WriteHeartbeat uid2: %v", err)
	}
	if !RequestArbitration(db, "uuid2", "secret1", "cluster1", "master1", 2, 2, 1) {
		t.Fatal("majority (failed=1) must win when the blind minority row (failed=0) is stale")
	}
}

// TestRequestArbitration_FreshFewerFailedStillWins_SQLite: the failed-count
// preference stays primary among FRESH reporters — a fresh peer seeing fewer
// failures still outranks the candidate.
func TestRequestArbitration_FreshFewerFailedStillWins_SQLite(t *testing.T) {
	db := newSQLiteArbitrationDB(t)
	if err := WriteHeartbeat(db, "uuid1", "secret1", "cluster1", "master1", 1, 2, 0); err != nil {
		t.Fatalf("WriteHeartbeat uid1: %v", err)
	}
	if err := WriteHeartbeat(db, "uuid2", "secret1", "cluster1", "master1", 2, 2, 1); err != nil {
		t.Fatalf("WriteHeartbeat uid2: %v", err)
	}
	if RequestArbitration(db, "uuid2", "secret1", "cluster1", "master1", 2, 2, 1) {
		t.Fatal("uid 2 (failed=1) must lose while a FRESH uid 1 row reports failed=0")
	}
}

// TestRequestArbitration_MajorityBeatsLowerUID_SQLite: the failed-count
// preference stays primary — a fresh lower-uid reporter that sees MORE
// failures than the candidate does not outrank it.
func TestRequestArbitration_MajorityBeatsLowerUID_SQLite(t *testing.T) {
	db := newSQLiteArbitrationDB(t)
	if err := WriteHeartbeat(db, "uuid1", "secret1", "cluster1", "master1", 1, 2, 1); err != nil {
		t.Fatalf("WriteHeartbeat uid1: %v", err)
	}
	if err := WriteHeartbeat(db, "uuid2", "secret1", "cluster1", "master1", 2, 2, 0); err != nil {
		t.Fatalf("WriteHeartbeat uid2: %v", err)
	}
	// uid 2 sees fewer failures than uid 1: the lower uid must NOT outrank it.
	if !RequestArbitration(db, "uuid2", "secret1", "cluster1", "master1", 2, 2, 0) {
		t.Fatal("uid 2 (fewer failed) must win over fresh uid 1 seeing more failures")
	}
}

// TestLeaseLifecycle_SQLite covers the authority lease: claim on Active
// report, block challengers while fresh, release on Standby report, survive
// staleness (peace-time silence), and transfer via demote after a won contest.
func TestLeaseLifecycle_SQLite(t *testing.T) {
	db := newSQLiteArbitrationDB(t)
	// uid1 reports Active: row + claim.
	if err := WriteHeartbeat(db, "uuid1", "secret1", "cluster1", "master1", 1, 2, 0); err != nil {
		t.Fatalf("WriteHeartbeat: %v", err)
	}
	if err := ClaimElected(db, "secret1", "cluster1", 1); err != nil {
		t.Fatalf("ClaimElected: %v", err)
	}
	uid, fresh, found := GetElectedAny(db, "secret1", "cluster1")
	if !found || uid != 1 || !fresh {
		t.Fatalf("expected fresh lease held by uid 1, got uid=%d fresh=%t found=%t", uid, fresh, found)
	}
	// A fresh lease blocks a challenger's plain election.
	if RequestArbitration(db, "uuid2", "secret1", "cluster1", "master1", 2, 2, 0) {
		t.Fatal("challenger must lose against a fresh lease holder")
	}
	// Holder yields (reports Standby): lease released.
	if err := ReleaseElected(db, "secret1", "cluster1", 1); err != nil {
		t.Fatalf("ReleaseElected: %v", err)
	}
	if _, _, found := GetElectedAny(db, "secret1", "cluster1"); found {
		t.Fatal("lease must be gone after release")
	}
	// Re-claim then simulate peace-time silence: lease persists, not fresh.
	if err := ClaimElected(db, "secret1", "cluster1", 1); err != nil {
		t.Fatalf("ClaimElected: %v", err)
	}
	if _, err := db.Exec("UPDATE heartbeat SET date=DATETIME('now','-60 seconds') WHERE uid=1"); err != nil {
		t.Fatalf("age row: %v", err)
	}
	uid, fresh, found = GetElectedAny(db, "secret1", "cluster1")
	if !found || uid != 1 || fresh {
		t.Fatalf("expected STALE lease held by uid 1, got uid=%d fresh=%t found=%t", uid, fresh, found)
	}
	// Lease transfer after a won contest: winner elected, old holder demoted.
	if !RequestArbitration(db, "uuid2", "secret1", "cluster1", "master1", 2, 2, 0) {
		t.Fatal("challenger must win the election once the holder row is stale")
	}
	if err := DemoteOtherElected(db, "secret1", "cluster1", 2); err != nil {
		t.Fatalf("DemoteOtherElected: %v", err)
	}
	uid, _, found = GetElectedAny(db, "secret1", "cluster1")
	if !found || uid != 2 {
		t.Fatalf("expected lease transferred to uid 2, got uid=%d found=%t", uid, found)
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
		"SELECT count(*) FROM replication_manager_schema.heartbeat WHERE cluster=? AND secret=? AND status = 'U' and uid <> ?  and failed < ? AND date > DATE_SUB(NOW(), INTERVAL 10 SECOND) FOR UPDATE",
	)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT count(*) FROM replication_manager_schema.heartbeat WHERE cluster=? AND secret=? AND status = 'U' AND uid < ? AND failed <= ? AND date > DATE_SUB(NOW(), INTERVAL 10 SECOND) FOR UPDATE",
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
		"UPDATE replication_manager_schema.heartbeat set status='U' WHERE status='E' AND cluster=? AND secret=? AND uid NOT IN (SELECT uid FROM (SELECT uid FROM replication_manager_schema.heartbeat WHERE status='E' AND cluster=? AND secret=? AND date > DATE_SUB(NOW(), INTERVAL 10 SECOND) ORDER BY arbitration_date ASC, uid ASC LIMIT 1) t)",
	)).WillReturnResult(sqlmock.NewResult(0, 0))

	if err := WriteHeartbeat(db, "uuid1", "secret1", "cluster1", "master1", 1, 2, 0); err != nil {
		t.Fatalf("WriteHeartbeat: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
