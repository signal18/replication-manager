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
	"fmt"
	"strconv"

	"github.com/go-sql-driver/mysql"
	"github.com/signal18/replication-manager/dbhelper"
	"github.com/signal18/replication-manager/misc"
	"github.com/signal18/replication-manager/state"
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
	if server.HasSuperReadOnly() && server.ClusterGroup.Conf.SuperReadOnly {
		err := dbhelper.SetSuperReadOnly(server.Conn, true)
		if err != nil {
			return err
		}
	}
	return nil
}

func (server *ServerMonitor) SetLongQueryTime(queryTime string) error {
	err := dbhelper.SetLongQueryTime(server.Conn, queryTime)
	if err != nil {
		return err
	}
	server.SwitchSlowQuery()
	server.SwitchSlowQuery()
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

func (server *ServerMonitor) SetCredential(url string, user string, pass string) {
	var err error
	server.User = user
	server.Pass = pass
	server.URL = url
	server.Host, server.Port = misc.SplitHostPort(url)
	server.IP, err = dbhelper.CheckHostAddr(server.Host)
	if err != nil {
		server.ClusterGroup.SetState("ERR00062", state.State{ErrType: LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00062"], server.Host, err.Error()), ErrFrom: "TOPO"})
	}
	params := fmt.Sprintf("?timeout=%ds&readTimeout=%ds", server.ClusterGroup.Conf.Timeout, server.ClusterGroup.Conf.ReadTimeout)

	mydsn := func() string {
		dsn := server.User + ":" + server.Pass + "@"
		if server.ClusterGroup.Conf.TunnelHost != "" {
			dsn += "tcp(127.0.0.1:" + server.TunnelPort + ")/" + params
		} else if server.Host != "" {
			//don't use IP as it can change under orchestration
			//	if server.IP != "" {
			//		dsn += "tcp(" + server.IP + ":" + server.Port + ")/" + params
			//	} else {
			dsn += "tcp(" + server.Host + ":" + server.Port + ")/" + params
			//	}
		} else {
			dsn += "unix(" + server.ClusterGroup.Conf.Socket + ")/" + params
		}
		return dsn
	}
	server.DSN = mydsn()
	if server.ClusterGroup.haveDBTLSCert {
		mysql.RegisterTLSConfig("tlsconfig", server.ClusterGroup.tlsconf)
		server.DSN = server.DSN + "&tls=tlsconfig"
	}
}

func (server *ServerMonitor) SetReplicationGTIDSlavePosFromServer(master *ServerMonitor) error {

	if server.IsMariaDB() {
		return dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      master.Host,
			Port:      master.Port,
			User:      master.ClusterGroup.rplUser,
			Password:  master.ClusterGroup.rplPass,
			Retry:     strconv.Itoa(master.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(master.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
			Mode:      "SLAVE_POS",
			SSL:       server.ClusterGroup.Conf.ReplicationSSL,
			Channel:   server.ClusterGroup.Conf.MasterConn,
			IsMariaDB: server.DBVersion.IsMariaDB(),
			IsMySQL:   server.DBVersion.IsMySQLOrPercona(),
		})
	}
	return dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      master.Host,
		Port:      master.Port,
		User:      master.ClusterGroup.rplUser,
		Password:  master.ClusterGroup.rplPass,
		Retry:     strconv.Itoa(master.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(master.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
		Mode:      "MASTER_AUTO_POSITION",
		SSL:       server.ClusterGroup.Conf.ReplicationSSL,
		Channel:   server.ClusterGroup.Conf.MasterConn,
		IsMariaDB: server.DBVersion.IsMariaDB(),
		IsMySQL:   server.DBVersion.IsMySQLOrPercona(),
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
			Retry:     strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
			Mode:      "",
			SSL:       server.ClusterGroup.Conf.ReplicationSSL,
			Channel:   server.ClusterGroup.Conf.MasterConn,
			IsMariaDB: server.DBVersion.IsMariaDB(),
			IsMySQL:   server.DBVersion.IsMySQLOrPercona(),
		})
	} else {
		err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      master.Host,
			Port:      master.Port,
			User:      master.ClusterGroup.rplUser,
			Password:  master.ClusterGroup.rplPass,
			Retry:     strconv.Itoa(master.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(master.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
			Mode:      "CURRENT_POS",
			SSL:       server.ClusterGroup.Conf.ReplicationSSL,
			Channel:   server.ClusterGroup.Conf.MasterConn,
			IsMariaDB: server.DBVersion.IsMariaDB(),
			IsMySQL:   server.DBVersion.IsMySQLOrPercona(),
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
		Retry:     strconv.Itoa(master.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat: strconv.Itoa(master.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
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

func (server *ServerMonitor) SetInnoDBMonitor() {
	dbhelper.SetInnoDBLockMonitor(server.Conn)
}
