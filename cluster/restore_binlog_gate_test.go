// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/signal18/replication-manager/utils/version"
)

// The gate holds a restore stream while paused and lets it flow once resumed.
func TestRestoreBinlogGatePausesStream(t *testing.T) {
	g := newRestoreBinlogGate()
	r := &gatedReader{r: bytes.NewReader([]byte("INSERT 1;INSERT 2;")), gate: g}
	buf := make([]byte, 9)
	if n, err := r.Read(buf); n != 9 || err != nil {
		t.Fatalf("an open gate reads: %d %v", n, err)
	}
	g.pause("binlog rotated")
	if g.pausedReason() != "binlog rotated" {
		t.Fatalf("reason %q", g.pausedReason())
	}
	got := make(chan string, 1)
	go func() {
		rest, _ := io.ReadAll(r)
		got <- string(rest)
	}()
	select {
	case s := <-got:
		t.Fatalf("a paused gate let %q through", s)
	case <-time.After(200 * time.Millisecond):
	}
	g.resume()
	select {
	case s := <-got:
		if s != "INSERT 2;" {
			t.Fatalf("resumed stream %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a resumed gate still holds the stream")
	}
	if g.pausedReason() != "" {
		t.Fatalf("a resumed gate has a reason: %q", g.pausedReason())
	}
}

// On a rotation of the master's binlog the restore pauses, the rotated binlogs are purged
// once the replicas applied them (none here), then it resumes: one binlog kept.
func TestRestoreBinlogGateRotationPurges(t *testing.T) {
	mariadb := &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 6}
	server, mock := newMyLoaderPositionServer(t, mariadb, true)
	server.ClusterGroup.Servers = []*ServerMonitor{server}
	cols := []string{"File", "Position", "Binlog_Do_DB", "Binlog_Ignore_DB"}
	mock.MatchExpectationsInOrder(true)
	mock.ExpectQuery("SHOW MASTER STATUS").WillReturnRows(sqlmock.NewRows(cols).AddRow("binlog.000002", 4, "", ""))
	mock.ExpectQuery("SELECT Variable_Value AS Value FROM .*global_variables WHERE Variable_Name = ?").
		WithArgs("GTID_BINLOG_POS").WillReturnRows(sqlmock.NewRows([]string{"Value"}).AddRow("5-1-389"))
	mock.ExpectExec("PURGE BINARY LOGS TO").WithArgs("binlog.000002").WillReturnResult(sqlmock.NewResult(0, 0))
	for i := 0; i < 20; i++ {
		mock.ExpectQuery("SHOW MASTER STATUS").WillReturnRows(sqlmock.NewRows(cols).AddRow("binlog.000002", 8, "", ""))
	}

	g := newRestoreBinlogGate()
	server.restoreGate.Store(g)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); server.runRestoreBinlogGate(ctx, g, "binlog.000001") }()
	// one status read a second: the rotation is seen within ~1 s, the purge right after
	time.Sleep(2500 * time.Millisecond)
	cancel()
	<-done
	// in order: only later status reads may remain, never the position read or the purge
	if err := mock.ExpectationsWereMet(); err != nil && (strings.Contains(err.Error(), "PURGE") || strings.Contains(err.Error(), "global_variables")) {
		t.Fatalf("the rotation did not lead to the purge: %s", err)
	}
	if g.pausedReason() != "" {
		t.Fatalf("the restore stays paused after the purge: %q", g.pausedReason())
	}
}
