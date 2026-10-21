// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"reflect"
	"testing"
	"time"
)

// #2091: a rotation queues the binlog it closed, each once, bounded by the keep window
func TestQueueBinlogCopyOncePerBinlogBounded(t *testing.T) {
	c, server := newTestRuntimeOnlyClusterServer(t, "binlog-queue", "db1", "3306")
	c.Conf.BackupBinlogsKeep = 3
	q := &server.binlogCopies
	q.running = true // no worker: the queue alone
	for _, b := range []string{"mysql-bin.000001", "mysql-bin.000002", "mysql-bin.000002", "mysql-bin.000003", "mysql-bin.000004"} {
		server.queueBinlogCopy(b)
	}
	if got, want := server.pendingBinlogCopies(), []string{"mysql-bin.000002", "mysql-bin.000003", "mysql-bin.000004"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pending %v, want %v", got, want)
	}
	if got := server.oldestPendingBinlogCopy(); got != "mysql-bin.000002" {
		t.Fatalf("oldest pending %q", got)
	}
	if q.catchUp != "mysql-bin.000004" {
		t.Fatalf("catch-up after the copies: %q", q.catchUp)
	}
}

// #2091: the worker waits while a snapshot runs, with WARN0257, then copies the binlogs once
// each and stops
func TestBinlogCopyWorkerWaitsForSnapshot(t *testing.T) {
	c, server := newTestRuntimeOnlyClusterServer(t, "binlog-worker", "db1", "3306")
	c.Conf.BackupBinlogsKeep = 10
	c.Conf.BinlogCopyMode = "none" // every copy refused at once: the worker's order alone
	server.BinaryLogFileOldest = "mysql-bin.000001"
	server.snapshotsRunning.Add(1)
	server.queueBinlogCopy("mysql-bin.000001")
	server.queueBinlogCopy("mysql-bin.000002")

	deadline := time.Now().Add(3 * time.Second)
	for {
		server.binlogCopies.mu.Lock()
		waiting := server.binlogCopies.waiting
		server.binlogCopies.mu.Unlock()
		if waiting != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the worker does not record what it waits for")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(server.pendingBinlogCopies()) != 2 {
		t.Fatalf("a copy ran during the snapshot: %v", server.pendingBinlogCopies())
	}
	c.checkBinlogCopyStates()
	if !hasOpenState(c, "WARN0257") {
		t.Fatal("WARN0257 not raised while the copies wait")
	}

	server.snapshotsRunning.Add(-1)
	deadline = time.Now().Add(5 * time.Second)
	for {
		server.binlogCopies.mu.Lock()
		running := server.binlogCopies.running
		server.binlogCopies.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the worker did not end: pending %v", server.pendingBinlogCopies())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if p := server.pendingBinlogCopies(); len(p) != 0 {
		t.Fatalf("copies left %v", p)
	}
}
