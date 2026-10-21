// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"database/sql"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/signal18/replication-manager/utils/dbhelper"
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

// On a rotation of the master's binlog the restore pauses; the rotated binlog is purged only
// once every replica read up to the current one, then the restore resumes: one binlog kept.
func TestRestoreBinlogRotationWaitsReplicaThenPurges(t *testing.T) {
	mariadb := &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 6}
	master, mock := newMyLoaderPositionServer(t, mariadb, true)
	c := master.ClusterGroup
	master.BinaryLogFile = "binlog.000002"
	master.BinaryLogFiles = dbhelper.NewBinaryLogMetaMap()
	master.BinaryLogFiles.Set("binlog.000001", &dbhelper.BinaryLogMetadata{Filename: "binlog.000001"})
	master.BinaryLogFiles.Set("binlog.000002", &dbhelper.BinaryLogMetadata{Filename: "binlog.000002"})
	replica := &ServerMonitor{Host: "db2", Port: "3306", URL: "db2:3306", ClusterGroup: c, State: stateSlave,
		SlaveStatus: &dbhelper.SlaveStatus{MasterLogFile: sql.NullString{String: "binlog.000001", Valid: true}}}
	c.slaves = serverList{replica}
	c.Servers = []*ServerMonitor{master, replica}

	stop := master.startRestoreBinlogGate()
	defer stop()
	g := master.restoreGate.Load()
	if g == nil {
		t.Fatal("no gate on a MariaDB master with a binlog")
	}
	mock.MatchExpectationsInOrder(false)
	mock.ExpectExec("PURGE BINARY LOGS TO").WithArgs("binlog.000002").WillReturnResult(sqlmock.NewResult(0, 0))

	master.onRestoreBinlogRotation()
	time.Sleep(1500 * time.Millisecond)
	if !strings.Contains(g.pausedReason(), "waiting for the replicas") {
		t.Fatalf("a replica behind keeps the restore paused: %q", g.pausedReason())
	}
	if err := mock.ExpectationsWereMet(); err == nil {
		t.Fatal("purged before the replica read the rotated binlog")
	}

	replica.SlaveStatus = &dbhelper.SlaveStatus{MasterLogFile: sql.NullString{String: "binlog.000002", Valid: true}}
	deadline := time.Now().Add(5 * time.Second)
	for g.pausedReason() != "" {
		if time.Now().After(deadline) {
			t.Fatalf("still paused once the replica read the current binlog: %q", g.pausedReason())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the rotated binlog was not purged: %s", err)
	}
}
