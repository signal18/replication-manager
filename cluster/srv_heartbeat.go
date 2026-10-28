// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"strconv"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

// refreshReplicationLastReceived records when the managed channel last received something
// from its source, a heartbeat (idle source) or an event (busy source), and its heartbeat
// period: what the false-positive heartbeat check reads, with no wait (#2208). MariaDB keeps
// no such time, only the channel's Slave_received_heartbeats counter in SHOW ALL SLAVES
// STATUS, which the tick already read: the time is the tick that sees the counter or the IO
// position move. MySQL and Percona 5.7+ record it in performance_schema. Read only when the
// check or force-slave-heartbeat needs it.
func (server *ServerMonitor) refreshReplicationLastReceived() {
	cluster := server.ClusterGroup
	ss, err := server.GetSlaveStatus(server.ReplicationSourceName)
	if err != nil || server.DBVersion.IsPostgreSQL() || !(cluster.Conf.CheckFalsePositiveHeartbeat || cluster.Conf.ForceSlaveHeartbeat) {
		server.resetReplicationLastReceived()
		return
	}
	now := time.Now()
	// a channel moved to another source (a failover's CHANGE MASTER) received nothing from
	// the new one yet: its position moved by the repoint, not by a receive
	source := ss.MasterHost.String + ":" + ss.MasterPort.String
	if source != server.replicationReceivedSource {
		server.resetReplicationLastReceived()
		server.replicationReceivedSource = source
	}
	if server.DBVersion.IsMariaDB() {
		received := ss.SlaveReceivedHeartbeats.Int64
		position := ss.GtidIOPos.String + " " + ss.MasterLogFile.String + ":" + ss.ReadMasterLogPos.String
		// a counter going down is a restarted channel, not something received
		if server.replicationHeartbeatKnown && (received > server.replicationReceivedHeartbeats || position != server.replicationReceivedPosition) {
			server.ReplicationLastReceivedAt = now
		}
		server.replicationReceivedHeartbeats = received
		server.replicationReceivedPosition = position
		server.ReplicationHeartbeatPeriod = ss.SlaveHeartbeatPeriod
		server.replicationHeartbeatKnown = true
		return
	}
	lr, logs, err := dbhelper.GetReplicationLastReceived(server.Conn, server.ReplicationSourceName, server.DBVersion)
	cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Could not read when %s last received from its source: %s", server.URL, err)
	if err != nil {
		server.resetReplicationLastReceived()
		return
	}
	server.ReplicationHeartbeatPeriod = lr.HeartbeatPeriod
	server.replicationHeartbeatKnown = true
	server.ReplicationLastReceivedAt = time.Time{}
	if lr.Received {
		server.ReplicationLastReceivedAt = now.Add(-lr.Age)
	}
}

func (server *ServerMonitor) resetReplicationLastReceived() {
	server.ReplicationLastReceivedAt = time.Time{}
	server.ReplicationHeartbeatPeriod = 0
	server.replicationHeartbeatKnown = false
	server.replicationReceivedHeartbeats = 0
	server.replicationReceivedPosition = ""
	server.replicationReceivedSource = ""
}

// GetReplicationLastReceivedAge is how long ago the managed channel last received from its
// source; false when not seen since the replica is monitored.
func (server *ServerMonitor) GetReplicationLastReceivedAge() (time.Duration, bool) {
	if server.ReplicationLastReceivedAt.IsZero() {
		return 0, false
	}
	return time.Since(server.ReplicationLastReceivedAt), true
}

// falsePositiveHeartbeatWindow is how recent a receive proves the master alive: an idle
// master sends one heartbeat per period, repman sees it at the next tick, plus the
// configured failover-falsepositive-heartbeat-timeout margin.
func (server *ServerMonitor) falsePositiveHeartbeatWindow() time.Duration {
	conf := server.ClusterGroup.Conf
	return time.Duration(server.ReplicationHeartbeatPeriod*float64(time.Second)) +
		time.Duration(conf.MonitoringTicker)*time.Second +
		time.Duration(conf.CheckFalsePositiveHeartbeatTimeout)*time.Second
}

// isDirectReplicaOfMaster: only a direct replica of the master receives its heartbeats, not
// a replica of a relay or of an intermediate replica (in multi-master every node is a
// replica and the relay flag is never set).
func (cluster *Cluster) isDirectReplicaOfMaster(slave *ServerMonitor) bool {
	source, _ := cluster.GetMasterFromReplication(slave)
	return source != nil && !source.IsRelay && (cluster.Conf.MultiMaster || !source.IsSlave)
}

// reportReplicasLastReceived shows, while the master is suspect or failed, how long ago each
// of its replicas last received from it (CINF0022): the answer to "is the master really
// dead?". Not in peace time, where the age changes each tick for nothing.
func (cluster *Cluster) reportReplicasLastReceived() {
	master := cluster.master
	if master == nil || !(master.IsSuspect() || master.IsFailed()) {
		return
	}
	for _, slave := range cluster.slaves {
		if !slave.replicationHeartbeatKnown || !cluster.isDirectReplicaOfMaster(slave) {
			continue
		}
		received := "nothing since it is monitored"
		if age, ok := slave.GetReplicationLastReceivedAge(); ok {
			received = age.Round(100*time.Millisecond).String() + " ago"
		}
		cluster.SetState("CINF0022", state.State{ErrType: config.LvlInfo, ErrDesc: fmt.Sprintf(clusterError["CINF0022"], slave.URL, master.URL, received, formatHeartbeatPeriod(slave.ReplicationHeartbeatPeriod)), ErrFrom: "CHECK", ServerUrl: slave.URL})
	}
}

func formatHeartbeatPeriod(period float64) string {
	if period == 0 {
		return "0s (heartbeat disabled)"
	}
	return strconv.FormatFloat(period, 'f', -1, 64) + "s"
}

// checkReplicationHeartbeatPeriod: a failover decision waits up to one heartbeat period for
// the false-positive heartbeat check, so a period above force-slave-heartbeat-time (or 0,
// heartbeats off) is a WARN0050 while the check is on. force-slave-heartbeat is the client's
// consent to change it (F1, T27): the managed channel's period is set to
// force-slave-heartbeat-time, only while both its threads run (START SLAVE must not start a
// thread someone stopped), once: a failure is not retried while the period stays the same.
// Every change is a state naming the server, the channel and both periods.
func (server *ServerMonitor) checkReplicationHeartbeatPeriod() {
	cluster := server.ClusterGroup
	if !server.replicationHeartbeatKnown || server.IsMaintenance || cluster.Conf.ForceSlaveHeartbeatTime <= 0 {
		return
	}
	period := server.ReplicationHeartbeatPeriod
	target := float64(cluster.Conf.ForceSlaveHeartbeatTime)
	if period > 0 && period <= target {
		server.heartbeatEnforceFailed = false
		return
	}
	channel := server.ReplicationSourceName
	if cluster.Conf.ForceSlaveHeartbeat && !(server.heartbeatEnforceFailed && period == server.heartbeatEnforceFailedPeriod) {
		ss, err := server.GetSlaveStatus(channel)
		if err == nil && ss.SlaveIORunning.String == "Yes" && ss.SlaveSQLRunning.String == "Yes" {
			logs, err := dbhelper.SetSlaveHeartbeat(server.Conn, strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime), channel, server.DBVersion)
			cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlInfo, "Could not set the heartbeat period of %s: %s", server.URL, err)
			if err != nil {
				server.heartbeatEnforceFailed = true
				server.heartbeatEnforceFailedPeriod = period
				cluster.SetState("WARN0268", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0268"], server.URL, channel, err), ErrFrom: "TOPO", ServerUrl: server.URL})
			} else {
				cluster.SetState("CINF0023", state.State{ErrType: config.LvlInfo, ErrDesc: fmt.Sprintf(clusterError["CINF0023"], server.URL, channel, formatHeartbeatPeriod(period), formatHeartbeatPeriod(target)), ErrFrom: "TOPO", ServerUrl: server.URL})
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Heartbeat period of %s channel '%s' set from %s to %s (force-slave-heartbeat)", server.URL, channel, formatHeartbeatPeriod(period), formatHeartbeatPeriod(target))
				return
			}
		}
	}
	if cluster.Conf.CheckFalsePositiveHeartbeat {
		cluster.SetState("WARN0050", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0050"], server.URL, formatHeartbeatPeriod(period), cluster.Conf.ForceSlaveHeartbeatTime), ErrFrom: "TOPO", ServerUrl: server.URL})
	}
}
