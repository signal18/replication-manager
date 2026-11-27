package regtest

import (
	"database/sql"
	"io"
	"os"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/sirupsen/logrus"
)

// The harness of the #2451 regtests must be reliable itself: every statement is bounded, and a leftover
// global read lock, a refused write or a statement that never returns is a FAIL, never a PASS.

func harnessCluster() *cluster.Cluster {
	l := logrus.New()
	l.SetOutput(io.Discard)
	cl := &cluster.Cluster{Name: "t", Conf: &config.Config{}, Logrus: l, StateMachine: &state.StateMachine{}}
	cl.StateMachine.Init()
	return cl
}

func mockServer(t *testing.T, url string) (*cluster.ServerMonitor, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &cluster.ServerMonitor{URL: url, Conn: sqlx.NewDb(db, "sqlmock"), DSN: "unused"}, mock
}

func withTimeout(t *testing.T, d time.Duration) {
	old := electionSQLTimeout
	electionSQLTimeout = d
	t.Cleanup(func() { electionSQLTimeout = old })
}

// A statement that the server never answers returns at the deadline, DDL and DML alike.
func TestElectionSQLDeadlineBoundsStatements(t *testing.T) {
	withTimeout(t, 100*time.Millisecond)
	srv, mock := mockServer(t, "db1")
	stmts := map[string]string{
		"DDL create": "CREATE TABLE s.t (id INT)",
		"DDL drop":   "DROP DATABASE IF EXISTS s",
		"DML insert": "INSERT INTO s.t VALUES (1)",
	}
	for name, q := range stmts {
		mock.ExpectExec(q).WillDelayFor(3 * time.Second).WillReturnResult(sqlmock.NewResult(0, 0))
		start := time.Now()
		err := electionExec(srv, q)
		if err == nil || time.Since(start) > 2*time.Second {
			t.Fatalf("%s: err=%v after %s, want a deadline error well under 2s", name, err, time.Since(start))
		}
	}
	mock.ExpectQuery("SELECT 1").WillDelayFor(3 * time.Second).WillReturnRows(sqlmock.NewRows([]string{"x"}).AddRow("1"))
	start := time.Now()
	if _, err := electionQuery(srv, "SELECT 1"); err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("query: err=%v after %s, want a deadline error", err, time.Since(start))
	}
}

// useIndependent answers each independent session of a server, in order, with a mock (every session is
// closed by the code that opened it).
func useIndependent(t *testing.T, queue map[string][]*sql.DB) {
	old := electionOpenIndependent
	electionOpenIndependent = func(srv *cluster.ServerMonitor) (*sql.DB, error) {
		q := queue[srv.URL]
		if len(q) == 0 {
			return nil, sql.ErrConnDone
		}
		queue[srv.URL] = q[1:]
		return q[0], nil
	}
	t.Cleanup(func() { electionOpenIndependent = old })
}

func indMock(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	return db, mock
}

func finalChecksRun(t *testing.T, writeErr error, holders int, dropDelay time.Duration) *electionRun {
	t.Helper()
	withTimeout(t, 150*time.Millisecond)
	r := &electionRun{cl: harnessCluster(), name: "unit", schema: "regtest_election_2451", prefered: map[string]bool{}}
	primary, pmock := mockServer(t, "p")
	replica, _ := mockServer(t, "r")
	writeDB, w := indMock(t)
	pLockDB, pl := indMock(t)
	rLockDB, rl := indMock(t)
	w.ExpectExec("SET SESSION lock_wait_timeout").WillReturnResult(sqlmock.NewResult(0, 0))
	if writeErr != nil {
		w.ExpectExec("INSERT INTO").WillReturnError(writeErr)
	} else {
		w.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(1, 1))
	}
	pl.ExpectQuery("performance_schema.metadata_locks").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(holders))
	rl.ExpectQuery("performance_schema.metadata_locks").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
	useIndependent(t, map[string][]*sql.DB{"p": {writeDB, pLockDB}, "r": {rLockDB}})
	e := pmock.ExpectExec("DROP DATABASE")
	if dropDelay > 0 {
		e.WillDelayFor(dropDelay)
	}
	e.WillReturnResult(sqlmock.NewResult(0, 0))
	r.finalChecks(primary, []*cluster.ServerMonitor{primary, replica})
	return r
}

func TestElectionFinalChecks(t *testing.T) {
	has := func(r *electionRun, prefix string) bool {
		for _, f := range r.failed {
			if len(f) >= len(prefix) && f[:len(prefix)] == prefix {
				return true
			}
		}
		return false
	}
	if r := finalChecksRun(t, nil, 0, 0); len(r.failed) != 0 {
		t.Fatalf("clean end: failed=%v, want none", r.failed)
	}
	if r := finalChecksRun(t, nil, 1, 0); !has(r, "cleanup-global-lock") {
		t.Fatalf("a global read lock held by an idle session: failed=%v, want cleanup-global-lock", r.failed)
	}
	if r := finalChecksRun(t, sql.ErrConnDone, 0, 0); !has(r, "cleanup-writable") {
		t.Fatalf("a refused independent write: failed=%v, want cleanup-writable", r.failed)
	}
	r := finalChecksRun(t, nil, 0, 3*time.Second)
	if !has(r, "cleanup-drop") {
		t.Fatalf("a DROP that never returns: failed=%v, want cleanup-drop", r.failed)
	}
	if electionVerdict(true, r.failed, nil) != "FAIL" {
		t.Fatalf("a failed cleanup must make the verdict FAIL even when the election checks passed")
	}
}

func TestElectionVerdict(t *testing.T) {
	for _, c := range []struct {
		ok     bool
		failed []string
		not    []string
		want   string
	}{
		{true, nil, nil, "PASS"},
		{true, []string{"cleanup-drop"}, nil, "FAIL"},
		{false, nil, nil, "FAIL"},
		{true, nil, []string{"premise absent"}, "SKIP"},
		{true, []string{"cleanup-global-lock"}, []string{"premise absent"}, "FAIL"},
	} {
		if got := electionVerdict(c.ok, c.failed, c.not); got != c.want {
			t.Errorf("verdict(%v,%v,%v)=%s want %s", c.ok, c.failed, c.not, got, c.want)
		}
	}
}

// Against a real server: with the global read lock held by another session, DDL and DML of the harness
// return at the deadline instead of waiting. Runs only with ELECTION_TEST_DSN (a disposable server whose
// root account the test may lock).
func TestElectionSQLDeadlineRealServer(t *testing.T) {
	dsn := os.Getenv("ELECTION_TEST_DSN")
	if dsn == "" {
		t.Skip("ELECTION_TEST_DSN not set")
	}
	withTimeout(t, 2*time.Second)
	pool, err := sqlx.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	srv := &cluster.ServerMonitor{URL: "real", Conn: pool, DSN: dsn}
	if err := electionExec(srv, "CREATE DATABASE IF NOT EXISTS harness_t"); err != nil {
		t.Fatal(err)
	}
	if err := electionExec(srv, "CREATE TABLE IF NOT EXISTS harness_t.y (id INT AUTO_INCREMENT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	holder, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	holder.SetMaxOpenConns(1)
	defer holder.Close()
	if _, err := holder.Exec("FLUSH TABLES WITH READ LOCK"); err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			holder.Exec("UNLOCK TABLES")
		}
	}
	defer release()
	for name, q := range map[string]string{
		"DDL create": "CREATE TABLE harness_t.x (id INT)",
		"DML insert": "INSERT INTO harness_t.y VALUES ()",
		"DDL drop":   "DROP DATABASE IF EXISTS harness_t",
	} {
		start := time.Now()
		err := electionExec(srv, q)
		took := time.Since(start)
		if err == nil || took > 6*time.Second {
			t.Errorf("%s under a global read lock: err=%v after %s, want a deadline error within 6s", name, err, took)
		} else {
			t.Logf("%s returned after %s: %v", name, took.Round(100*time.Millisecond), err)
		}
	}
	release()
	if err := electionExec(srv, "DROP DATABASE IF EXISTS harness_t"); err != nil {
		t.Errorf("after the lock is released: %v", err)
	}
}

// The live replica threads are read through a session of the test, in the 8.x and the older column names.
func TestElectionReplicaThreads(t *testing.T) {
	for _, c := range []struct {
		cols       []string
		io, sqlRun string
	}{
		{[]string{"Source_Host", "Replica_IO_Running", "Replica_SQL_Running"}, "Yes", "No"},
		{[]string{"Master_Host", "Slave_IO_Running", "Slave_SQL_Running"}, "Connecting", "Yes"},
	} {
		db, m := indMock(t)
		m.ExpectQuery("SHOW REPLICA STATUS").WillReturnRows(sqlmock.NewRows(c.cols).AddRow("h", c.io, c.sqlRun))
		useIndependent(t, map[string][]*sql.DB{"s": {db}})
		io, sqlRun, err := electionReplicaThreads(&cluster.ServerMonitor{URL: "s"})
		if err != nil || io != c.io || sqlRun != c.sqlRun {
			t.Fatalf("columns %v: io=%q sql=%q err=%v", c.cols, io, sqlRun, err)
		}
	}
}
