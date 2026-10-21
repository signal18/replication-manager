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

// gateReplica is a replica of the restore's master, its replication channel as the monitoring last read it.
func gateReplica(c *Cluster, name, applying string, delay int64, running string, sqlDelay int64) *ServerMonitor {
	ss := dbhelper.SlaveStatus{
		MasterLogFile:       sql.NullString{String: applying, Valid: true},
		RelayMasterLogFile:  sql.NullString{String: applying, Valid: true},
		SecondsBehindMaster: sql.NullInt64{Int64: delay, Valid: true},
		SlaveIORunning:      sql.NullString{String: running, Valid: true},
		SlaveSQLRunning:     sql.NullString{String: running, Valid: true},
		SQLDelay:            sql.NullInt64{Int64: sqlDelay, Valid: true},
	}
	return &ServerMonitor{Host: name, Port: "3306", URL: name + ":3306", ClusterGroup: c, State: stateSlave,
		SlaveStatus: &ss, Replications: []dbhelper.SlaveStatus{ss}}
}

// On a rotation of the master's binlog the restore pauses; the rotated binlog is purged and the
// restore resumes only once every replica taking part applied it (its SQL thread on the current
// binlog) and has no delay: one binlog kept. A stopped replica or one with MASTER_DELAY never holds it.
func TestLogicalRestoreGateWaitsReplicasThenPurges(t *testing.T) {
	mariadb := &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 6}
	master, mock := newMyLoaderPositionServer(t, mariadb, true)
	c := master.ClusterGroup
	master.BinaryLogFile = "binlog.000002"
	master.BinaryLogFiles = dbhelper.NewBinaryLogMetaMap()
	master.BinaryLogFiles.Set("binlog.000001", &dbhelper.BinaryLogMetadata{Filename: "binlog.000001"})
	master.BinaryLogFiles.Set("binlog.000002", &dbhelper.BinaryLogMetadata{Filename: "binlog.000002"})
	upToDate := gateReplica(c, "db2", "binlog.000002", 0, "Yes", 0)
	applying := gateReplica(c, "db3", "binlog.000001", 40, "Yes", 0)
	stopped := gateReplica(c, "db4", "binlog.000001", 0, "No", 0)
	delayed := gateReplica(c, "db5", "binlog.000001", 3600, "Yes", 3600)
	c.slaves = serverList{upToDate, applying, stopped, delayed}
	c.Servers = []*ServerMonitor{master, upToDate, applying, stopped, delayed}

	stop := master.startLogicalRestoreGate()
	defer stop()
	g := master.logicalRestoreGate.Load()
	if g == nil {
		t.Fatal("no rotation pause on a MariaDB master with a binlog")
	}
	mock.MatchExpectationsInOrder(false)
	mock.ExpectExec("PURGE BINARY LOGS TO").WithArgs("binlog.000002").WillReturnResult(sqlmock.NewResult(0, 0))

	master.pauseLogicalRestoreOnBinlogRotation()
	waitReason := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !strings.Contains(g.pausedReason(), want) {
			if time.Now().After(deadline) {
				t.Fatalf("paused reason %q, want %q", g.pausedReason(), want)
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err := mock.ExpectationsWereMet(); err == nil {
			t.Fatal("purged while a replica holds the restore")
		}
	}
	waitReason("replica db3:3306 to apply it (applying binlog.000001)")

	// read and applied, still a delay
	applying.SlaveStatus.RelayMasterLogFile.String = "binlog.000002"
	waitReason("replica db3:3306 to have no delay (Seconds_Behind_Master 40)")

	// no delay left: db4 stopped and db5 delayed do not take part
	applying.SlaveStatus.SecondsBehindMaster.Int64 = 0
	deadline := time.Now().Add(5 * time.Second)
	for g.pausedReason() != "" {
		if time.Now().After(deadline) {
			t.Fatalf("still paused once every replica taking part applied the binlog: %q", g.pausedReason())
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
