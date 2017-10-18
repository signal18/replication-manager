// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"strconv"

	"github.com/signal18/replication-manager/dbhelper"
)

func (server *ServerMonitor) SetReadOnly() error {
	return dbhelper.SetReadOnly(server.Conn, true)
}

func (server *ServerMonitor) SetReadWrite() error {
	return dbhelper.SetReadOnly(server.Conn, false)
}

func (server *ServerMonitor) SetReplicationGTIDSlavePosFromServer(master *ServerMonitor) error {

	return dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      master.Host,
		Port:      master.Port,
		User:      master.ClusterGroup.rplUser,
		Password:  master.ClusterGroup.rplPass,
		Retry:     strconv.Itoa(master.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(master.ClusterGroup.conf.ForceSlaveHeartbeatTime),
		Mode:      "SLAVE_POS",
	})
}

func (server *ServerMonitor) SetReplicationGTIDCurrentPosFromServer(master *ServerMonitor) error {

	return dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      master.Host,
		Port:      master.Port,
		User:      master.ClusterGroup.rplUser,
		Password:  master.ClusterGroup.rplPass,
		Retry:     strconv.Itoa(master.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(master.ClusterGroup.conf.ForceSlaveHeartbeatTime),
		Mode:      "CURRENT_POS",
	})
}

func (server *ServerMonitor) SetReplicationFromMaxsaleServer(master *ServerMonitor) error {
	return dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      master.Host,
		Port:      master.Port,
		User:      master.ClusterGroup.rplUser,
		Password:  master.ClusterGroup.rplPass,
		Retry:     strconv.Itoa(master.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(master.ClusterGroup.conf.ForceSlaveHeartbeatTime),
		Mode:      "MXS",
		Logfile:   master.FailoverMasterLogFile,
		Logpos:    master.FailoverMasterLogPos,
	})
}

func (server *ServerMonitor) SetReplicationChannel(source string) error {
	if server.DBVersion.IsMariaDB() {
		err := dbhelper.SetDefaultMasterConn(server.Conn, source)
		if err != nil {
			return err
		}
	}
	return nil
}
