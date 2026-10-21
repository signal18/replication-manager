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

// The staging seed waits for the replica it detaches to reach its master's position read once
// (WaitSyncToTarget), with no timer, its catching up the creation's step, and stops only on a
// broken replication; a replica rebuilt by a reload is read again by URL. Slave is not enough:
// db3.belair-staging-test, Slave at 2 s, was detached 660 MB short of its master's position.
func TestWaitStagingReplicaInSync(t *testing.T) {
	mariadb := &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 6}
	master, masterMock := newMyLoaderPositionServer(t, mariadb, true)
	c := master.ClusterGroup
	c.master = master
	masterMock.MatchExpectationsInOrder(false)
	gtidBinlogPos := func() {
		masterMock.ExpectQuery("global_variables").WithArgs("GTID_BINLOG_POS").
			WillReturnRows(sqlmock.NewRows([]string{"Value"}).AddRow("5-11130737-726"))
	}
	newReplica := func() (*ServerMonitor, sqlmock.Sqlmock) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return &ServerMonitor{Host: "db3", Port: "3306", URL: "db3:3306", ClusterGroup: c, DBVersion: mariadb,
			Conn: sqlx.NewDb(db, "sqlmock")}, mock
	}
	gtidWait := func(mock sqlmock.Sqlmock, result int) {
		mock.ExpectQuery("MASTER_GTID_WAIT").WithArgs("5-11130737-726", 30).
			WillReturnRows(sqlmock.NewRows([]string{"r"}).AddRow(result))
	}

	replica, mock := newReplica()
	c.Servers = []*ServerMonitor{master, replica}
	replica.State = stateSlave
	gtidBinlogPos()
	gtidWait(mock, 0)
	if err := c.waitStagingReplicaInSync(replica.URL); err != nil {
		t.Fatalf("a replica that reached its master's position: %s", err)
	}

	replica.State = stateSlaveErr
	gtidBinlogPos()
	if err := c.waitStagingReplicaInSync(replica.URL); err == nil || !strings.Contains(err.Error(), "replication broken") {
		t.Fatalf("a broken replication stops the seed: %v", err)
	}

	gtidBinlogPos()
	if err := c.waitStagingReplicaInSync("gone:3306"); err == nil {
		t.Fatal("a replica no longer in the cluster stops the seed")
	}

	// Slave, short of the position: MASTER_GTID_WAIT times out (-1), the step says catching up;
	// then the replica rebuilt by a reload reaches it and the wait ends
	c.SetStagingCreation(StagingCreation{Kind: "Staging", Parent: "prod", Step: "seed from prod"})
	replica.State = stateSlave
	replica.SlaveStatus = &dbhelper.SlaveStatus{SecondsBehindMaster: sql.NullInt64{Int64: 40, Valid: true}}
	gtidBinlogPos()
	gtidWait(mock, -1)
	done := make(chan error, 1)
	go func() { done <- c.waitStagingReplicaInSync(replica.URL) }()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(c.GetStagingCreation().Step, "catching up") {
		if time.Now().After(deadline) {
			t.Fatalf("step %q", c.GetStagingCreation().Step)
		}
		time.Sleep(50 * time.Millisecond)
	}
	rebuilt, rebuiltMock := newReplica()
	rebuilt.SlaveStatus = &dbhelper.SlaveStatus{SecondsBehindMaster: sql.NullInt64{Int64: 40, Valid: true}}
	rebuilt.State = stateSlave
	gtidWait(rebuiltMock, 0)
	c.Servers = []*ServerMonitor{master, rebuilt}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("caught up: %s", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wait did not end once the replica reached its master's position")
	}
}

// A replica whose applied position does not move for replicaNoProgressTurns turns is not catching
// up: the wait stops instead of waiting forever, and the replica is not detached.
func TestWaitStagingReplicaInSyncStopsWithoutProgress(t *testing.T) {
	mariadb := &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 6}
	master, masterMock := newMyLoaderPositionServer(t, mariadb, true)
	c := master.ClusterGroup
	c.master = master
	masterMock.ExpectQuery("global_variables").WithArgs("GTID_BINLOG_POS").
		WillReturnRows(sqlmock.NewRows([]string{"Value"}).AddRow("5-11130737-726"))
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	replica := &ServerMonitor{Host: "db3", Port: "3306", URL: "db3:3306", ClusterGroup: c, DBVersion: mariadb,
		Conn: sqlx.NewDb(db, "sqlmock"), State: stateSlaveLate,
		SlaveStatus: &dbhelper.SlaveStatus{RelayMasterLogFile: sql.NullString{String: "binlog.000010", Valid: true},
			ExecMasterLogPos: sql.NullString{String: "692538834", Valid: true}}}
	c.Servers = []*ServerMonitor{master, replica}
	for i := 0; i <= replicaNoProgressTurns; i++ {
		mock.ExpectQuery("MASTER_GTID_WAIT").WithArgs("5-11130737-726", 30).
			WillReturnRows(sqlmock.NewRows([]string{"r"}).AddRow(-1))
	}
	err = c.waitStagingReplicaInSync(replica.URL)
	if err == nil || !strings.Contains(err.Error(), "makes no progress (still at binlog.000010:692538834") {
		t.Fatalf("a replica without progress: %v", err)
	}
}

// An SQL error of the wait (connection lost, replication not running) stops it: no endless loop.
func TestWaitStagingReplicaInSyncStopsOnSQLError(t *testing.T) {
	mariadb := &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 6}
	master, masterMock := newMyLoaderPositionServer(t, mariadb, true)
	c := master.ClusterGroup
	c.master = master
	masterMock.ExpectQuery("global_variables").WithArgs("GTID_BINLOG_POS").
		WillReturnRows(sqlmock.NewRows([]string{"Value"}).AddRow("5-11130737-726"))
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	replica := &ServerMonitor{Host: "db3", Port: "3306", URL: "db3:3306", ClusterGroup: c, DBVersion: mariadb,
		Conn: sqlx.NewDb(db, "sqlmock"), State: stateSlave}
	c.Servers = []*ServerMonitor{master, replica}
	mock.ExpectQuery("MASTER_GTID_WAIT").WillReturnError(errors.New("invalid connection"))
	if err := c.waitStagingReplicaInSync(replica.URL); err == nil || !strings.Contains(err.Error(), "invalid connection") {
		t.Fatalf("an SQL error stops the wait: %v", err)
	}
}

// A reseed never takes the parent's domain back: the backup is behind when the server already
// holds a later GTID of a domain the backup has; other domains (the child's own) do not count.
func TestParentBackupBehind(t *testing.T) {
	for _, tc := range []struct {
		backup, held, want string
	}{
		{"4-53070043-3245370", "4-53070043-3441701,2895224983-11130737-777", "domain 4: backup at 3245370, server at 3441701"},
		{"4-53070043-3442500", "4-53070043-3441701,2895224983-11130737-777", ""},
		{"4-53070043-3441701", "4-53070043-3441701", ""},
		{"4-53070043-3245370", "2895224983-11130737-389", ""}, // a new child: none of the parent's domain
		{"", "4-53070043-3441701", ""},
	} {
		if got := parentBackupBehind(tc.backup, tc.held); got != tc.want {
			t.Fatalf("backup %q held %q: %q, want %q", tc.backup, tc.held, got, tc.want)
		}
	}
}
