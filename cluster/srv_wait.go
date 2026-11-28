// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

func (server *ServerMonitor) WaitSyncToMaster(master *ServerMonitor) {
	cluster := server.ClusterGroup
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Waiting for slave %s to sync", server.URL)
	if server.IsPostgreSQLHost() {
		// a standby has applied everything when its replay position reaches the primary's
		// current position; a logical subscriber when its slot confirmed it (publisher side)
		server.postgresWaitSync(master, 30*time.Second)
		server.LogReplPostion()
		return
	}
	// the managed replication's WaitCaughtUp, 30 s; as before, a timeout or an aborted wait is
	// not an error here (the next step checks the topology)
	until := Position{File: master.BinaryLogFile, Pos: master.BinaryLogPos}
	what := "MasterPosWait"
	if server.DBVersion.Flavor == "MariaDB" {
		until, what = Position{GTID: master.GTIDBinlogPos.Sprint()}, "MasterWaitGTID"
	}
	logs, err := server.ReplicationLink().WaitCaughtUp(until, 30*time.Second)
	if waitEndedWithoutError(err) {
		err = nil
	}
	cluster.LogSQL(logs, err, server.URL, "MasterFailover", config.LvlErr, "Failed "+what+", %s", err)

	// if cluster.Conf.LogLevel > 2 {
	server.LogReplPostion()
	// }
}

// ReplicationTarget is a master's position fixed once: its gtid_binlog_pos on MariaDB, its binary log
// file and position otherwise.
type ReplicationTarget struct {
	GTID string
	File string
	Pos  string
}

func (t ReplicationTarget) String() string {
	if t.GTID != "" {
		return t.GTID
	}
	return t.File + ":" + t.Pos
}

// ReadReplicationTarget reads server's position live, not from the last monitoring tick: a tick old,
// it can miss the last transactions just written (the end of a restore). An empty position (no
// binary log, nothing written) is an error: MASTER_GTID_WAIT of an empty GTID answers at once that it is reached.
func (server *ServerMonitor) ReadReplicationTarget() (ReplicationTarget, error) {
	if server.DBVersion.IsMariaDB() {
		g, _, err := dbhelper.GetVariableByName(server.Conn, "GTID_BINLOG_POS", server.DBVersion)
		if err == nil && g == "" {
			err = fmt.Errorf("%s has no gtid_binlog_pos", server.URL)
		}
		return ReplicationTarget{GTID: g}, err
	}
	ms, _, err := dbhelper.GetMasterStatus(server.Conn, server.DBVersion)
	if err == nil && ms.File == "" {
		err = fmt.Errorf("%s has no binary log position", server.URL)
	}
	return ReplicationTarget{File: ms.File, Pos: strconv.FormatUint(uint64(ms.Position), 10)}, err
}

// AppliedPosition is where the replica's SQL thread is, from its last replication status: the
// master binlog file and position it executed up to, and its gtid_slave_pos on MariaDB. Equal
// between two turns: no progress.
func (server *ServerMonitor) AppliedPosition() string {
	if server.SlaveStatus == nil {
		return ""
	}
	pos := server.SlaveStatus.RelayMasterLogFile.String + ":" + server.SlaveStatus.ExecMasterLogPos.String
	if server.SlaveGtid != nil {
		pos += " " + server.SlaveGtid.Sprint()
	}
	return pos
}

// ReadPosition is where the replica's IO thread is, from its last replication status: the master
// binlog file and position it read up to, and its Gtid_IO_Pos on MariaDB.
func (server *ServerMonitor) ReadPosition() string {
	if server.SlaveStatus == nil {
		return ""
	}
	pos := server.SlaveStatus.MasterLogFile.String + ":" + server.SlaveStatus.ReadMasterLogPos.String
	if server.SlaveStatus.GtidIOPos.String != "" {
		pos += " " + server.SlaveStatus.GtidIOPos.String
	}
	return pos
}

// replicaWaitStopped is why a wait for the replica sl can no longer end, from its last
// replication status, or "" while it can. The engine's own state decides, never a timer nor a
// still position: one transaction can take longer than any timer, a MASTER_DELAY replica stays
// still on purpose, and parallel replication workers roll back and retry with the position still.
// Those retries are bounded (slave_transaction_retries): past them the engine stops the SQL thread
// with an error, and that is the exit. So the wait ends when:
//   - there is no replication status (no longer a replica);
//   - the SQL thread is not running (stopped, or stopped on an error: Last_SQL_Error says which);
//   - the IO thread is stopped (No). Connecting is not stopped: the IO thread retries the
//     connection itself (master_retry_count) and stops once it gives up.
func replicaWaitStopped(sl *ServerMonitor) string {
	ss := sl.SlaveStatus
	if ss == nil {
		return "no replication status"
	}
	if ss.SlaveSQLRunning.String != "Yes" {
		if ss.LastSQLError.String != "" {
			return "SQL thread stopped on an error: " + ss.LastSQLError.String
		}
		return "SQL thread stopped"
	}
	if io := ss.SlaveIORunning.String; io == "" || io == "No" {
		if ss.LastIOError.String != "" {
			return "IO thread stopped on an error: " + ss.LastIOError.String
		}
		return "IO thread stopped"
	}
	return ""
}

// WaitSyncToTarget is a copy of WaitSyncToMaster, kept apart on purpose:
//   - WaitSyncToMaster reads the master's position again at each call: looped on, its target
//     moves, and a master that keeps writing (a child or staging master replicating its parent,
//     repman's own heartbeat) is never reached; here the target is fixed once by the caller;
//   - WaitSyncToMaster returns nothing and a timeout looks like a success (MasterWaitGTID and
//     MasterPosWait ignore the -1); here the result says whether the target was reached;
//   - WaitSyncToMaster serves switchover and the rolling operations (SwitchSlavesToMaster,
//     RollingReprov, RollingRestart, RollingUpgrade): 30 s and the next step checks the
//     topology. They stay untouched.
//
// Same SQL per flavor: MASTER_GTID_WAIT on MariaDB, MASTER_POS_WAIT / SOURCE_POS_WAIT on the
// cluster's channel otherwise. Used where a replica must hold all of a master's writes before
// the next step, with no timer around it (waitReplicaReachTarget): the end of a restore with the
// binary log on (logical_restore_gate.go), the staging refresh before it detaches a replica
// (#2124), the regtest consistency check and the table checksum before it compares the chunks.
func (server *ServerMonitor) WaitSyncToTarget(target ReplicationTarget, timeout int) (bool, error) {
	cluster := server.ClusterGroup
	what := "MasterPosWait"
	if server.DBVersion.IsMariaDB() {
		what = "MasterWaitGTID"
	}
	logs, err := server.ReplicationLink().WaitCaughtUp(target, time.Duration(timeout)*time.Second)
	if errors.Is(err, ErrReplicationNotCaughtUp) {
		return false, nil
	}
	cluster.LogSQL(logs, err, server.URL, "WaitSyncToTarget", config.LvlErr, "Failed "+what+", %s", err)
	return err == nil, err
}

// waitEndedWithoutError: the timeout of a wait (ErrReplicationNotCaughtUp) or a wait the server
// aborted (NULL, dbhelper.ErrWaitAborted), which MasterWaitGTID and MasterPosWait ignored: the
// callers that relied on them (WaitSyncToMaster, the positional rejoin of a relay) still do.
func waitEndedWithoutError(err error) bool {
	return errors.Is(err, ErrReplicationNotCaughtUp) || errors.Is(err, dbhelper.ErrWaitAborted)
}

// waitReplicaReachTarget waits, with no timer, for the replica url to reach target in
// WaitSyncToTarget turns of 30 s: true once reached. It never gives up on a timer or on a position
// that does not move (replicaWaitStopped says why). Before each turn it ends:
//   - without reaching it, (false, nil), when the replica is gone (no server, no connection) or
//     keepWaiting says stop (the caller's reasons: a cancel, the master changed, the cluster
//     stopping, a replica that does not take part);
//   - with an error when the replica's replication is stopped or in error (replicaWaitStopped,
//     from its last status, refreshed by the monitor between the turns);
//
// and after a turn, with an error, on an SQL error of the wait. Read by URL at each turn: a
// configuration reload rebuilds the ServerMonitor. keepWaiting also gets each turn's replica to
// report its positions (AppliedPosition, ReadPosition) in a state.
//
// MariaDB, MySQL and Percona only: the wait is MASTER_GTID_WAIT / MASTER_POS_WAIT. A PostgreSQL
// replica answers an error at once; it is waited for by postgresWaitSync (WaitSyncToMaster). No
// caller passes one today: the restore gate is MariaDB's, the staging refresh and the table
// checksum send MariaDB/MySQL SQL.
func (cluster *Cluster) waitReplicaReachTarget(url string, target ReplicationTarget, keepWaiting func(sl *ServerMonitor) bool) (bool, error) {
	for {
		sl := cluster.GetServerFromURL(url)
		if sl == nil || sl.Conn == nil || !keepWaiting(sl) {
			return false, nil
		}
		if sl.IsPostgreSQLHost() {
			return false, fmt.Errorf("%s is a PostgreSQL replica: not waited for by position", url)
		}
		if stopped := replicaWaitStopped(sl); stopped != "" {
			return false, fmt.Errorf("%s (applied %s, read %s)", stopped, sl.AppliedPosition(), sl.ReadPosition())
		}
		reached, err := sl.WaitSyncToTarget(target, 30)
		if err != nil {
			return false, err
		}
		if reached {
			return true, nil
		}
	}
}

func (server *ServerMonitor) WaitDatabaseStart() error {
	cluster := server.ClusterGroup
	exitloop := 0
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Waiting database start on %s", server.URL)
	ticker := time.NewTicker(time.Millisecond * time.Duration(cluster.GetConf().MonitoringTicker*1000))
	for int64(exitloop) < cluster.GetConf().MonitorWaitRetry {
		<-ticker.C

		exitloop++

		var err error
		wg := new(sync.WaitGroup)
		wg.Add(1)
		go server.Ping(wg)
		wg.Wait()
		err = server.Refresh()
		if err != nil {
			cluster.SetState("WARN0128", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0128"], server.URL, err.Error()), ErrFrom: "PROV", ServerUrl: server.URL})
		}

		// the declared topology: a Galera cluster being provisioned has discovered nothing yet
		if cluster.GetTopologyTarget() == config.TopoMultiMasterWsrep {
			if !server.IsConnected() {
				err = errors.New("Not yet connected")
			}
			/*	} else { */

		}
		if err == nil {

			exitloop = 9999999
		} else {
			cluster.SetState("WARN0129", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0129"], server.URL, err.Error()), ErrFrom: "PROV", ServerUrl: server.URL})
		}
	}
	if exitloop == 9999999 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Waiting state running reach on %s", server.URL)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Wait state running on %s", server.URL)
		return errors.New("Failed to wait running database server")
	}
	return nil
}
