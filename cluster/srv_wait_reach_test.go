// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/version"
)

// runningReplicaStatus is the last replication status of a replica whose two threads run, its
// SQL thread at file:pos.
func runningReplicaStatus(file, pos string) *dbhelper.SlaveStatus {
	return &dbhelper.SlaveStatus{
		RelayMasterLogFile: sql.NullString{String: file, Valid: true},
		ExecMasterLogPos:   sql.NullString{String: pos, Valid: true},
		MasterLogFile:      sql.NullString{String: file, Valid: true},
		ReadMasterLogPos:   sql.NullString{String: pos, Valid: true},
		SlaveSQLRunning:    sql.NullString{String: "Yes", Valid: true},
		SlaveIORunning:     sql.NullString{String: "Yes", Valid: true},
	}
}

// waitReplicaReachTarget, shared by the restore gate, the regtest consistency check (#2228),
// the staging refresh and the table checksum: true once reached; (false, nil) when the replica
// is gone or keepWaiting says stop, with no query sent; an error on an SQL error of the wait or
// when the replica's threads are stopped or in error. Never on a still position (#2465).
func TestWaitReplicaReachTarget(t *testing.T) {
	mariadb := &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 9}
	target := ReplicationTarget{GTID: "1-11626367-959216"}
	newReplica := func(c *Cluster) (*ServerMonitor, sqlmock.Sqlmock) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		s := &ServerMonitor{Host: "db2", Port: "3306", URL: "db2:3306", ClusterGroup: c, DBVersion: mariadb,
			Conn: sqlx.NewDb(db, "sqlmock"), State: stateSlave,
			SlaveStatus: runningReplicaStatus("binlog.000023", "464629870")}
		c.Servers = []*ServerMonitor{s}
		return s, mock
	}
	wait := func(mock sqlmock.Sqlmock, result int) {
		mock.ExpectQuery("MASTER_GTID_WAIT").WithArgs(target.GTID, 30).
			WillReturnRows(sqlmock.NewRows([]string{"r"}).AddRow(result))
	}
	always := func(*ServerMonitor) bool { return true }
	newCluster := func() *Cluster {
		master, _ := newMyLoaderPositionServer(t, mariadb, true)
		return master.ClusterGroup
	}

	c := newCluster()
	replica, mock := newReplica(c)
	wait(mock, -1)
	wait(mock, 0)
	if reached, err := c.waitReplicaReachTarget(replica.URL, target, always); !reached || err != nil {
		t.Fatalf("a replica reaching the target on the second turn: %t %v", reached, err)
	}

	c = newCluster()
	replica, mock = newReplica(c)
	if reached, err := c.waitReplicaReachTarget(replica.URL, target, func(*ServerMonitor) bool { return false }); reached || err != nil {
		t.Fatalf("keepWaiting false: (false, nil), got %t %v", reached, err)
	}
	if reached, err := c.waitReplicaReachTarget("gone:3306", target, always); reached || err != nil {
		t.Fatalf("a replica gone: (false, nil), got %t %v", reached, err)
	}
	// a stopped replica does not take part: the consistency check passes replicaTakesPart
	replica.SlaveStatus.SlaveSQLRunning = sql.NullString{String: "No", Valid: true}
	if reached, err := c.waitReplicaReachTarget(replica.URL, target, replicaTakesPart); reached || err != nil {
		t.Fatalf("a stopped replica is not waited for: %t %v", reached, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("no wait sent when not waiting: %s", err)
	}

	c = newCluster()
	replica, mock = newReplica(c)
	mock.ExpectQuery("MASTER_GTID_WAIT").WillReturnError(errors.New("invalid connection"))
	if _, err := c.waitReplicaReachTarget(replica.URL, target, always); err == nil || !strings.Contains(err.Error(), "invalid connection") {
		t.Fatalf("an SQL error ends the wait: %v", err)
	}

	// a long transaction: the applied position stays still for 20 turns (10 minutes), the SQL
	// thread runs, the wait goes on and reaches the target
	c = newCluster()
	replica, mock = newReplica(c)
	for i := 0; i < 20; i++ {
		wait(mock, -1)
	}
	wait(mock, 0)
	if reached, err := c.waitReplicaReachTarget(replica.URL, target, always); !reached || err != nil {
		t.Fatalf("a long transaction is waited for: %t %v", reached, err)
	}

	// the SQL thread stopped on an error (parallel retries exhausted), then the IO thread
	// stopped: the wait ends with the reason and the positions, no query sent
	replica.SlaveStatus.SlaveSQLRunning = sql.NullString{String: "No", Valid: true}
	replica.SlaveStatus.LastSQLError = sql.NullString{String: "Lock wait timeout exceeded", Valid: true}
	if _, err := c.waitReplicaReachTarget(replica.URL, target, always); err == nil ||
		!strings.Contains(err.Error(), "SQL thread stopped on an error: Lock wait timeout exceeded (applied binlog.000023:464629870") {
		t.Fatalf("a stopped SQL thread ends the wait: %v", err)
	}
	replica.SlaveStatus = runningReplicaStatus("binlog.000023", "464629870")
	replica.SlaveStatus.SlaveIORunning = sql.NullString{String: "No", Valid: true}
	if _, err := c.waitReplicaReachTarget(replica.URL, target, always); err == nil || !strings.Contains(err.Error(), "IO thread stopped") {
		t.Fatalf("a stopped IO thread ends the wait: %v", err)
	}
	// Connecting is the IO thread retrying by itself: still waited for
	replica.SlaveStatus.SlaveIORunning = sql.NullString{String: "Connecting", Valid: true}
	wait(mock, 0)
	if reached, err := c.waitReplicaReachTarget(replica.URL, target, always); !reached || err != nil {
		t.Fatalf("an IO thread reconnecting is waited for: %t %v", reached, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("waits: %s", err)
	}
}

// The consistency check lists its replicas once from the servers: an old master rejoined as
// a replica is in it even while cluster.slaves, rebuilt at every topology pass, lacks it.
func TestConsistencyReplicas(t *testing.T) {
	master, _ := newMyLoaderPositionServer(t, &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8}, true)
	c := master.ClusterGroup
	c.master = master
	conn := master.Conn
	rejoined := &ServerMonitor{URL: "db3:3306", IsSlave: true, Conn: conn}
	replica := &ServerMonitor{URL: "db2:3306", IsSlave: true, Conn: conn}
	standalone := &ServerMonitor{URL: "db4:3306", Conn: conn}
	c.Servers = []*ServerMonitor{master, replica, rejoined, standalone}
	c.slaves = []*ServerMonitor{replica}
	got := c.consistencyReplicas()
	if len(got) != 2 || got[0] != replica || got[1] != rejoined {
		urls := make([]string, 0, len(got))
		for _, s := range got {
			urls = append(urls, s.URL)
		}
		t.Fatalf("the replicas of the servers, not of cluster.slaves: %v", urls)
	}
}
