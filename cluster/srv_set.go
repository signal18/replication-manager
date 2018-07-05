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

func (server *ServerMonitor) SetIgnored(ignored bool) {
	server.Ignored = ignored
}

func (server *ServerMonitor) SetPrefered(pref bool) {
	server.Prefered = pref
}

func (server *ServerMonitor) SetReadOnly() error {
	if !server.IsReadOnly() {
		err := dbhelper.SetReadOnly(server.Conn, true)
		if err != nil {
			return err
		}
	}
	if server.HasSuperReadOnly() {
		err := dbhelper.SetSuperReadOnly(server.Conn, true)
		if err != nil {
			return err
		}
	}
	return nil
}

func (server *ServerMonitor) SetReadWrite() error {
	if server.IsReadOnly() {
		err := dbhelper.SetReadOnly(server.Conn, false)
		if err != nil {
			return err
		}
	}
	if server.HasSuperReadOnly() {
		err := dbhelper.SetSuperReadOnly(server.Conn, false)
		if err != nil {
			return err
		}
	}
	return nil
}

func (server *ServerMonitor) SetMaintenance() {
	server.IsMaintenance = true
}

func (server *ServerMonitor) SetReplicationGTIDSlavePosFromServer(master *ServerMonitor) error {

	if server.IsMariaDB() {
		return dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      master.Host,
			Port:      master.Port,
			User:      master.ClusterGroup.rplUser,
			Password:  master.ClusterGroup.rplPass,
			Retry:     strconv.Itoa(master.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(master.ClusterGroup.conf.ForceSlaveHeartbeatTime),
			Mode:      "SLAVE_POS",
			SSL:       server.ClusterGroup.conf.ReplicationSSL,
		})
	}
	// If MySQL or Percona GTID
	return dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      master.Host,
		Port:      master.Port,
		User:      master.ClusterGroup.rplUser,
		Password:  master.ClusterGroup.rplPass,
		Retry:     strconv.Itoa(master.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(master.ClusterGroup.conf.ForceSlaveHeartbeatTime),
		Mode:      "MASTER_AUTO_POSITION",
		SSL:       server.ClusterGroup.conf.ReplicationSSL,
	})

}

func (server *ServerMonitor) SetReplicationGTIDCurrentPosFromServer(master *ServerMonitor) error {
	var err error
	if server.DBVersion.IsMySQLOrPercona57() {
		// We can do MySQL 5.7 style failover
		server.ClusterGroup.LogPrintf(LvlInfo, "Doing MySQL GTID switch of the old master")
		err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      server.ClusterGroup.master.Host,
			Port:      server.ClusterGroup.master.Port,
			User:      server.ClusterGroup.rplUser,
			Password:  server.ClusterGroup.rplPass,
			Retry:     strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatTime),
			Mode:      "",
			SSL:       server.ClusterGroup.conf.ReplicationSSL,
		})
	} else {
		err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      master.Host,
			Port:      master.Port,
			User:      master.ClusterGroup.rplUser,
			Password:  master.ClusterGroup.rplPass,
			Retry:     strconv.Itoa(master.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(master.ClusterGroup.conf.ForceSlaveHeartbeatTime),
			Mode:      "CURRENT_POS",
			SSL:       server.ClusterGroup.conf.ReplicationSSL,
		})
	}
	return err
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
