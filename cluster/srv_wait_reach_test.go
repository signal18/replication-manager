// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/version"
)

// waitReplicaReachTarget, shared by the restore gate and the regtest consistency check
// (#2228): true once reached; (false, nil) when the replica is gone or keepWaiting says
// stop, with no query sent; an error on an SQL error of the wait or when the applied
// position did not move for replicaNoProgressFor.
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
			SlaveStatus: &dbhelper.SlaveStatus{RelayMasterLogFile: sql.NullString{String: "binlog.000023", Valid: true},
				ExecMasterLogPos: sql.NullString{String: "464629870", Valid: true}}}
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

	// each turn lasts its 30 s: the fourth sees the position still for 90 s
	c = newCluster()
	replica, mock = newReplica(c)
	clock := time.Now()
	replicaProgressClock = func() time.Time { clock = clock.Add(30 * time.Second); return clock }
	defer func() { replicaProgressClock = time.Now }()
	for i := 0; i < 4; i++ {
		wait(mock, -1)
	}
	if _, err := c.waitReplicaReachTarget(replica.URL, target, always); err == nil || !strings.Contains(err.Error(), "no progress (still at binlog.000023:464629870 for 1m30s") {
		t.Fatalf("a replica without progress: %v", err)
	}
}
