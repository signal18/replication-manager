// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
)

// alertLines counts the WARN and ERROR lines: the ones LogModulePrintf also forwards to
// Slack / Mattermost / Teams, on the same branch as this logger.
type alertLines struct {
	mu    sync.Mutex
	lines []string
}

func (h *alertLines) Levels() []logrus.Level {
	return []logrus.Level{logrus.WarnLevel, logrus.ErrorLevel}
}

func (h *alertLines) Fire(e *logrus.Entry) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lines = append(h.lines, e.Message)
	return nil
}

// #2065: a switchover that waits for a long write sends the alert channels one line when
// the wait starts, one per long session, and one with the outcome, however long it waits.
// Before, every 2 s re-sent the count and every session (600 messages for 3 sessions over
// switchover-wait-trx=300).
func TestLongWriteWaitDoesNotFloodAlerts(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cl, srv := newTestClusterServer(t)
	cl.Conf.Daemon = true
	cl.Conf.Verbose = true
	cl.Conf.SwitchWaitWrite = 10
	cl.Conf.SwitchWaitTrx = 60
	hook := &alertLines{}
	cl.Logrus.AddHook(hook)
	srv.Conn = sqlx.NewDb(db, "mysql")

	count := func(n int) {
		mock.ExpectQuery("select SUM\\(ct\\)").WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(n))
	}
	session := func() {
		mock.ExpectQuery("SELECT a.Id").WillReturnRows(sqlmock.NewRows([]string{"Id", "User", "Host", "Command", "Time", "Info"}).
			AddRow(42, "app", "10.0.0.1", "Query", 300, "UPDATE t SET a=1"))
	}
	count(1)  // the first check: a long write, the wait starts
	session() // its sessions, listed once
	count(1)  // still there after 2 s
	count(1)  // after 4 s
	count(0)  // gone after 6 s

	if !cl.waitLongRunningWrites(srv) {
		t.Fatal("the switchover did not proceed once the long write was gone")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	hook.mu.Lock()
	defer hook.mu.Unlock()
	if len(hook.lines) != 3 {
		t.Fatalf("%d alert lines over a 6 s wait, want 3 (start, session, outcome):\n%s", len(hook.lines), strings.Join(hook.lines, "\n"))
	}
	if !strings.Contains(hook.lines[2], "completed after") {
		t.Fatalf("the last alert line is not the outcome: %q", hook.lines[2])
	}
}

// #2065: when the long write outlives switchover-wait-trx, the outcome line says the
// switchover is cancelled and the sessions are listed again (what blocked it): still a
// fixed number of lines, whatever the wait.
func TestLongWriteWaitRefusedAlerts(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cl, srv := newTestClusterServer(t)
	cl.Conf.Daemon = true
	cl.Conf.Verbose = true
	cl.Conf.SwitchWaitWrite = 10
	cl.Conf.SwitchWaitTrx = 3
	hook := &alertLines{}
	cl.Logrus.AddHook(hook)
	srv.Conn = sqlx.NewDb(db, "mysql")

	count := func(n int) {
		mock.ExpectQuery("select SUM\\(ct\\)").WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(n))
	}
	session := func() {
		mock.ExpectQuery("SELECT a.Id").WillReturnRows(sqlmock.NewRows([]string{"Id", "User", "Host", "Command", "Time", "Info"}).
			AddRow(42, "app", "10.0.0.1", "Query", 300, "UPDATE t SET a=1"))
	}
	count(1)  // the wait starts
	session() // listed once at the start
	count(1)  // after 2 s
	count(1)  // after 4 s: past switchover-wait-trx=3
	session() // listed again with the refusal

	if cl.waitLongRunningWrites(srv) {
		t.Fatal("the switchover proceeded with the long write still running")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	hook.mu.Lock()
	defer hook.mu.Unlock()
	if len(hook.lines) != 4 {
		t.Fatalf("%d alert lines, want 4 (start, session, refusal, session):\n%s", len(hook.lines), strings.Join(hook.lines, "\n"))
	}
	if !strings.Contains(hook.lines[2], "Cannot switchover") {
		t.Fatalf("the refusal line is missing: %q", hook.lines[2])
	}
}
