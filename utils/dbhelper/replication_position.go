// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// The engine differences of a binlog replication's position, its wait and its promotion, kept
// here so that the native binlog plugin (cluster/replication_binlog.go) holds no engine branch
// (laws T1, T23). Each one sends exactly the statements the plugin sent before it moved here.

// ErrGTIDStateNotEmpty: SetReplicaPosition on MySQL or Percona refused, the server already has
// a GTID state (gtid_executed not empty): GTID_PURGED is set only right after a restore.
var ErrGTIDStateNotEmpty = errors.New("the server's GTID state is not empty")

// ErrReplicaPositionPostgreSQL: a PostgreSQL replication's position is its replication origin
// (a subscription) or the WAL it replays (a standby), which these helpers do not set nor wait
// for: nothing is sent.
var ErrReplicaPositionPostgreSQL = errors.New("no binlog position on PostgreSQL")

// ReplicaWaitReached waits until the replica applied up to a position, timeout seconds at most:
// true once it did, false at the timeout. MariaDB: MASTER_GTID_WAIT on gtid
// (MasterWaitGTIDReached). MySQL, Percona (and any other flavor, as before): MASTER_POS_WAIT /
// SOURCE_POS_WAIT on file:pos of channel (MasterPosWaitReached). PostgreSQL:
// ErrReplicaPositionPostgreSQL, nothing sent. A NULL result is ErrWaitAborted, as each helper's.
func ReplicaWaitReached(db *sqlx.DB, myver *version.Version, gtid, file, pos string, timeout int, channel string) (bool, string, error) {
	switch {
	case myver.IsPostgreSQL():
		return false, "", ErrReplicaPositionPostgreSQL
	case myver.IsMariaDB():
		return MasterWaitGTIDReached(db, gtid, timeout)
	}
	return MasterPosWaitReached(db, myver, file, pos, timeout, channel)
}

// SetReplicaPosition sets where the replica's apply resumes. MariaDB (and any flavor neither
// MySQL nor PostgreSQL, as before): gtid_slave_pos, the link's resume position
// (SetGTIDSlavePos). MySQL and Percona: GTID_PURGED (SetGtidPurged), the server's whole GTID
// state, valid only once RESET MASTER emptied gtid_executed (after a restore): read first
// through GetVariableByName, ErrGTIDStateNotEmpty when it is not empty, nothing set.
// PostgreSQL: ErrReplicaPositionPostgreSQL, nothing sent.
func SetReplicaPosition(db *sqlx.DB, myver *version.Version, gtid string) (string, error) {
	switch {
	case myver.IsPostgreSQL():
		return "", ErrReplicaPositionPostgreSQL
	case myver.IsMySQLOrPercona():
		executed, logs, err := GetVariableByName(db, "GTID_EXECUTED", myver)
		if err != nil {
			return logs, err
		}
		if strings.TrimSpace(executed) != "" {
			return logs, fmt.Errorf("%w: gtid_executed is %q, GTID_PURGED is set only on a server just reset (RESET MASTER after a restore)", ErrGTIDStateNotEmpty, executed)
		}
		return SetGtidPurged(db, myver, gtid)
	}
	return SetGTIDSlavePos(db, gtid)
}

// ResetAllNeedsStop tells whether RESET SLAVE ALL of a channel needs the channel stopped first:
// MySQL and Percona refuse it while a thread runs; MariaDB stops the channel itself. PostgreSQL:
// false, its removal (DROP SUBSCRIPTION) is not a binlog channel's. No statement.
func ResetAllNeedsStop(myver *version.Version) bool {
	return myver.IsMySQLOrPercona()
}
