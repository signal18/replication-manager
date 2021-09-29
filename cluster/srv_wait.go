// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import "github.com/signal18/replication-manager/utils/dbhelper"

func (server *ServerMonitor) StopSlaveAtNewMasterPos() error {
	server.StopSlave()
	ms, gtid, logs, err := dbhelper.FlushBinaryLogs(server.ClusterGroup.master.Conn, server.ClusterGroup.master.DBVersion)
	server.ClusterGroup.LogSQL(logs, err, server.URL, "MasterFailover", LvlInfo, "Stop replication at a new leader position")
	server.ClusterGroup.LogPrintf(LvlInfo, "Leader %s flush binary logs  at position %s log file :%s", server.ClusterGroup.master.URL, gtid, ms.File)
	server.StartSlaveUntil(ms, gtid)
	server.ClusterGroup.LogPrintf(LvlInfo, "Starting replication on %s replication at position %s log file :%s", server.URL, gtid, ms.File)
	return nil

}

func (server *ServerMonitor) WaitSyncToMaster(master *ServerMonitor) {
	server.ClusterGroup.LogPrintf(LvlInfo, "Waiting for slave %s to sync", server.URL)
	if server.DBVersion.Flavor == "MariaDB" {
		logs, err := dbhelper.MasterWaitGTID(server.Conn, master.GTIDBinlogPos.Sprint(), 30)
		server.ClusterGroup.LogSQL(logs, err, server.URL, "MasterFailover", LvlErr, "Failed MasterWaitGTID, %s", err)

	} else {
		logs, err := dbhelper.MasterPosWait(server.Conn, master.BinaryLogFile, master.BinaryLogPos, 30)
		server.ClusterGroup.LogSQL(logs, err, server.URL, "MasterFailover", LvlErr, "Failed MasterPosWait, %s", err)
	}

	if server.ClusterGroup.Conf.LogLevel > 2 {
		server.LogReplPostion()
	}
}
