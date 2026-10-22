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
	if server.DBVersion.Flavor == "MariaDB" {
		logs, err := dbhelper.MasterWaitGTID(server.Conn, master.GTIDBinlogPos.Sprint(), 30)
		cluster.LogSQL(logs, err, server.URL, "MasterFailover", config.LvlErr, "Failed MasterWaitGTID, %s", err)

	} else {
		logs, err := dbhelper.MasterPosWait(server.Conn, server.DBVersion, master.BinaryLogFile, master.BinaryLogPos, 30, cluster.Conf.MasterConn)
		cluster.LogSQL(logs, err, server.URL, "MasterFailover", config.LvlErr, "Failed MasterPosWait, %s", err)
	}

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

// replicaNoProgressFor is how long a replica's applied position may stay still before a wait gives
// up on it: one transaction of a restore (~290 MB) kept db3.belair-staging-test's position still
// for about 40 s while it applied it. Measured in time, not in WaitSyncToTarget turns: a turn that
// answers at once would count for nothing.
const replicaNoProgressFor = 90 * time.Second

// replicaProgressClock is the time a replicaProgress reads, replaced in tests.
var replicaProgressClock = time.Now

// replicaProgress follows a replica's applied position (AppliedPosition) between the turns of a wait.
type replicaProgress struct {
	applied string
	since   time.Time
}

// stalled records applied: true once it has not moved for replicaNoProgressFor, with how long.
func (p *replicaProgress) stalled(applied string) (bool, time.Duration) {
	now := replicaProgressClock()
	if p.since.IsZero() || applied != p.applied {
		p.applied, p.since = applied, now
		return false, 0
	}
	still := now.Sub(p.since)
	return still >= replicaNoProgressFor, still
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
// the next step, with no timer around it: the end of a restore with the binary log on
// (logical_restore_gate.go) and the staging refresh before it detaches a replica (#2124).
func (server *ServerMonitor) WaitSyncToTarget(target ReplicationTarget, timeout int) (bool, error) {
	cluster := server.ClusterGroup
	if server.DBVersion.IsMariaDB() {
		reached, logs, err := dbhelper.MasterWaitGTIDReached(server.Conn, target.GTID, timeout)
		cluster.LogSQL(logs, err, server.URL, "WaitSyncToTarget", config.LvlErr, "Failed MasterWaitGTID, %s", err)
		return reached, err
	}
	reached, logs, err := dbhelper.MasterPosWaitReached(server.Conn, server.DBVersion, target.File, target.Pos, timeout, cluster.Conf.MasterConn)
	cluster.LogSQL(logs, err, server.URL, "WaitSyncToTarget", config.LvlErr, "Failed MasterPosWait, %s", err)
	return reached, err
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
