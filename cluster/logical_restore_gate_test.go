// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/version"
)

// The rotation pause holds a restore stream while paused and lets it flow once resumed.
func TestLogicalRestoreGateHoldsStream(t *testing.T) {
	g := newLogicalRestoreGate()
	r := &logicalRestoreGateReader{r: bytes.NewReader([]byte("INSERT 1;INSERT 2;")), gate: g}
	buf := make([]byte, 9)
	if n, err := r.Read(buf); n != 9 || err != nil {
		t.Fatalf("a flowing logical restore reads: %d %v", n, err)
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
		t.Fatalf("a paused logical restore let %q through", s)
	case <-time.After(200 * time.Millisecond):
	}
	g.resume()
	select {
	case s := <-got:
		if s != "INSERT 2;" {
			t.Fatalf("resumed stream %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a resumed logical restore still holds the stream")
	}
	if g.pausedReason() != "" {
		t.Fatalf("a resumed logical restore has a reason: %q", g.pausedReason())
	}
}

// On a rotation of the master's binlog the restore pauses; the rotated binlog is purged only
// once every replica read up to the current one, then the restore resumes: one binlog kept.
func TestLogicalRestoreGateWaitsReplicaThenPurges(t *testing.T) {
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

	stop := master.startLogicalRestoreGate()
	defer stop()
	g := master.logicalRestoreGate.Load()
	if g == nil {
		t.Fatal("no rotation pause on a MariaDB master with a binlog")
	}
	mock.MatchExpectationsInOrder(false)
	mock.ExpectExec("PURGE BINARY LOGS TO").WithArgs("binlog.000002").WillReturnResult(sqlmock.NewResult(0, 0))

	master.pauseLogicalRestoreOnBinlogRotation()
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

// The real load path: the data of a splitdump goes in batched transactions
// (streamSplitdumpStatements -> execSplitdumpBatch); a paused gate holds the next transaction,
// none runs until it resumes (preprod: the pause sat in the single-statement path only, the
// batches went on, the binlog kept rotating).
func TestLogicalRestoreGateHoldsSplitdumpBatches(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()
	g := newLogicalRestoreGate()
	server.logicalRestoreGate.Store(g)
	g.pause("binlog rotated to binlog.000012")

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `t` VALUES").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `t` VALUES").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	done := make(chan error, 1)
	go func() {
		done <- server.streamSplitdumpStatements(context.Background(), conn,
			strings.NewReader("INSERT INTO `t` VALUES (1);\nINSERT INTO `t` VALUES (2);\n"), "db.t-data.sql", nil)
	}()
	time.Sleep(300 * time.Millisecond)
	if err := mock.ExpectationsWereMet(); err == nil || !strings.Contains(err.Error(), "Begin") {
		t.Fatalf("a transaction ran while the restore was paused: %v", err)
	}
	g.resume()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("restore after resume: %s", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the restore did not go on after the resume")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the batch did not run after the resume: %s", err)
	}
}
