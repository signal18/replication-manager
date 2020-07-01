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
	"os"
	"strconv"

	"github.com/go-sql-driver/mysql"

	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

func (server *ServerMonitor) SetIgnored(ignored bool) {
	server.Ignored = ignored
}

func (server *ServerMonitor) SetFailed() {
	server.State = stateFailed
}

func (server *ServerMonitor) SetPrefered(pref bool) {
	server.Prefered = pref
}

func (server *ServerMonitor) SetPreferedBackup(pref bool) {
	server.PreferedBackup = pref
}

func (server *ServerMonitor) SetReadOnly() (string, error) {
	logs := ""
	if !server.IsReadOnly() {
		logs, err := dbhelper.SetReadOnly(server.Conn, true)
		if err != nil {
			return logs, err
		}
	}
	if server.HasSuperReadOnlyCapability() && server.ClusterGroup.Conf.SuperReadOnly {
		logs, err := dbhelper.SetSuperReadOnly(server.Conn, true)
		if err != nil {
			return logs, err
		}
	}
	return logs, nil
}

func (server *ServerMonitor) SetLongQueryTime(queryTime string) (string, error) {

	log, err := dbhelper.SetLongQueryTime(server.Conn, queryTime)
	if err != nil {
		return log, err
	}
	server.SwitchSlowQuery()
	server.Refresh()
	server.SwitchSlowQuery()
	return log, nil
}

func (server *ServerMonitor) SetReadWrite() error {
	if server.IsReadOnly() {
		logs, err := dbhelper.SetReadOnly(server.Conn, false)
		server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed Set Read Write on %s : %s", server.URL, err)
		if err != nil {
			return err
		}
	}
	if server.HasSuperReadOnlyCapability() {
		logs, err := dbhelper.SetSuperReadOnly(server.Conn, false)
		server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed Set Super Read Write on %s : %s", server.URL, err)
		if err != nil {
			return err
		}
	}
	return nil
}

func (server *ServerMonitor) SetMaintenance() {
	server.IsMaintenance = true
}

func (server *ServerMonitor) SetDSN() {
	pgdsn := func() string {
		dsn := ""
		//push the password at the end because empty password may consider next parameter is paswword
		if server.ClusterGroup.HaveDBTLSCert {
			dsn += "sslmode=enable"
		} else {
			dsn += "sslmode=disable"
		}
		dsn += fmt.Sprintf(" host=%s port=%s user=%s dbname=%s connect_timeout=%d password=%s ", server.Host, server.Port, server.User, server.PostgressDB, server.ClusterGroup.Conf.Timeout, server.Pass)
		//dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s connect_timeout=1", server.Host, server.Port, server.User, server.Pass, "postgres")

		return dsn
	}
	mydsn := func() string {
		params := fmt.Sprintf("?timeout=%ds&readTimeout=%ds", server.ClusterGroup.Conf.Timeout, server.ClusterGroup.Conf.ReadTimeout)
		dsn := server.User + ":" + server.Pass + "@"
		if server.ClusterGroup.Conf.TunnelHost != "" {
			dsn += "tcp(127.0.0.1:" + server.TunnelPort + ")/" + params
		} else if server.Host != "" {
			//don't use IP as it can change under orchestrator
			//	if server.IP != "" {
			//		dsn += "tcp(" + server.IP + ":" + server.Port + ")/" + params
			//	} else {

			//if strings.Contains(server.Host, ":") {
			//		dsn += "tcp(" + server.Host + ":" + server.Port + ")/" + params
			//	} else {
			dsn += "tcp(" + server.Host + ":" + server.Port + ")/" + params
			//		}
		} else {
			dsn += "unix(" + server.ClusterGroup.Conf.Socket + ")/" + params
		}
		if server.ClusterGroup.HaveDBTLSCert {
			dsn += server.TLSConfigUsed
		}
		return dsn
	}
	if server.ClusterGroup.Conf.MasterSlavePgStream || server.ClusterGroup.Conf.MasterSlavePgLogical {
		server.DSN = pgdsn()
	} else {
		server.DSN = mydsn()
		if server.ClusterGroup.HaveDBTLSCert {
			mysql.RegisterTLSConfig(ConstTLSCurrentConfig, server.ClusterGroup.tlsconf)
			if server.ClusterGroup.HaveDBTLSOldCert {
				mysql.RegisterTLSConfig(ConstTLSOldConfig, server.ClusterGroup.tlsoldconf)
			}
		}
	}
}

func (server *ServerMonitor) SetCredential(url string, user string, pass string) {
	var err error
	server.User = user
	server.Pass = pass
	server.URL = url
	server.Host, server.Port, server.PostgressDB = misc.SplitHostPortDB(url)
	server.IP, err = dbhelper.CheckHostAddr(server.Host)
	if err != nil {
		server.ClusterGroup.SetState("ERR00062", state.State{ErrType: LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00062"], server.Host, err.Error()), ErrFrom: "TOPO"})
	}
	if server.PostgressDB == "" {
		server.PostgressDB = "test"
	}
	server.SetDSN()

}

func (server *ServerMonitor) SetReplicationGTIDSlavePosFromServer(master *ServerMonitor) (string, error) {
	server.StopSlave()
	if server.IsMariaDB() {
		return dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:        master.Host,
			Port:        master.Port,
			User:        master.ClusterGroup.rplUser,
			Password:    master.ClusterGroup.rplPass,
			Retry:       strconv.Itoa(master.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
			Heartbeat:   strconv.Itoa(master.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
			Mode:        "SLAVE_POS",
			SSL:         server.ClusterGroup.Conf.ReplicationSSL,
			Channel:     server.ClusterGroup.Conf.MasterConn,
			IsDelayed:   server.IsDelayed,
			Delay:       strconv.Itoa(server.ClusterGroup.Conf.HostsDelayedTime),
			PostgressDB: server.PostgressDB,
		}, server.DBVersion)
	}
	return dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:        master.Host,
		Port:        master.Port,
		User:        master.ClusterGroup.rplUser,
		Password:    master.ClusterGroup.rplPass,
		Retry:       strconv.Itoa(master.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat:   strconv.Itoa(master.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
		Mode:        "MASTER_AUTO_POSITION",
		SSL:         server.ClusterGroup.Conf.ReplicationSSL,
		Channel:     server.ClusterGroup.Conf.MasterConn,
		IsDelayed:   server.IsDelayed,
		Delay:       strconv.Itoa(server.ClusterGroup.Conf.HostsDelayedTime),
		PostgressDB: server.PostgressDB,
	}, server.DBVersion)
}

func (server *ServerMonitor) SetReplicationGTIDCurrentPosFromServer(master *ServerMonitor) (string, error) {
	var err error
	logs := ""
	if server.DBVersion.IsMySQLOrPerconaGreater57() {
		// We can do MySQL 5.7 style failover
		server.ClusterGroup.LogPrintf(LvlInfo, "Doing MySQL GTID switch of the old master")
		logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:        server.ClusterGroup.master.Host,
			Port:        server.ClusterGroup.master.Port,
			User:        server.ClusterGroup.rplUser,
			Password:    server.ClusterGroup.rplPass,
			Retry:       strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
			Heartbeat:   strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
			Mode:        "",
			SSL:         server.ClusterGroup.Conf.ReplicationSSL,
			Channel:     server.ClusterGroup.Conf.MasterConn,
			IsDelayed:   server.IsDelayed,
			Delay:       strconv.Itoa(server.ClusterGroup.Conf.HostsDelayedTime),
			PostgressDB: server.PostgressDB,
		}, server.DBVersion)
	} else {
		logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:        master.Host,
			Port:        master.Port,
			User:        master.ClusterGroup.rplUser,
			Password:    master.ClusterGroup.rplPass,
			Retry:       strconv.Itoa(master.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
			Heartbeat:   strconv.Itoa(master.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
			Mode:        "CURRENT_POS",
			SSL:         server.ClusterGroup.Conf.ReplicationSSL,
			Channel:     server.ClusterGroup.Conf.MasterConn,
			IsDelayed:   server.IsDelayed,
			Delay:       strconv.Itoa(server.ClusterGroup.Conf.HostsDelayedTime),
			PostgressDB: server.PostgressDB,
		}, server.DBVersion)
	}
	return logs, err
}

func (server *ServerMonitor) SetReplicationFromMaxsaleServer(master *ServerMonitor) (string, error) {
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
	}, server.DBVersion)
}

func (server *ServerMonitor) SetReplicationChannel(source string) (string, error) {
	logs := ""
	if server.DBVersion.IsMariaDB() {
		logs, err := dbhelper.SetDefaultMasterConn(server.Conn, source, server.DBVersion)
		if err != nil {
			return logs, err
		}
	}
	return logs, nil
}

func (server *ServerMonitor) SetInnoDBMonitor() {
	dbhelper.SetInnoDBLockMonitor(server.Conn)
}

func (server *ServerMonitor) SetProvisionCookie() {
	newFile, err := os.Create(server.Datadir + "/@cookie_prov")
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Can't save provision cookie %s", err)
	}
	newFile.Close()
}

func (server *ServerMonitor) SetRestartCookie() {
	newFile, err := os.Create(server.Datadir + "/@cookie_restart")
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Can't save restart cookie %s", err)
	}
	newFile.Close()
}

func (server *ServerMonitor) SetWaitStartCookie() {
	newFile, err := os.Create(server.Datadir + "/@cookie_waitstart")
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Can't save wait start cookie %s", err)
	}
	newFile.Close()
}

func (server *ServerMonitor) SetWaitStopCookie() {
	newFile, err := os.Create(server.Datadir + "/@cookie_waitstop")
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Can't save wait start cookie %s", err)
	}
	newFile.Close()
}

func (server *ServerMonitor) SetReprovCookie() {
	newFile, err := os.Create(server.Datadir + "/@cookie_reprov")
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Can't save restart cookie %s", err)
	}
	newFile.Close()
}
