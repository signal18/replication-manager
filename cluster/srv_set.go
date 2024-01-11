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
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/go-sql-driver/mysql"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
)

func (server *ServerMonitor) SetPlacement(k int, ProvAgents string, SlapOSDBPartitions string, SchedulerReceiverPorts string) {
	slapospartitions := strings.Split(SlapOSDBPartitions, ",")
	sstports := strings.Split(SchedulerReceiverPorts, ",")
	agents := strings.Split(ProvAgents, ",")
	if k < len(slapospartitions) {
		server.SlapOSDatadir = slapospartitions[k]
	}
	if ProvAgents != "" {
		server.Agent = agents[k%len(agents)]
	}
	server.SSTPort = sstports[k%len(sstports)]
}

func (server *ServerMonitor) SetSourceClusterName(name string) {
	server.SourceClusterName = name
}

func (server *ServerMonitor) SetIgnored(ignored bool) {
	server.Ignored = ignored
}

func (server *ServerMonitor) SetEventScheduler(value bool) (string, error) {
	logs, err := dbhelper.SetEventScheduler(server.Conn, value, server.DBVersion)
	return logs, err
}

func (server *ServerMonitor) SetGroupReplicationPrimary() (string, error) {
	logs, err := dbhelper.SetGroupReplicationPrimary(server.Conn, server.DBVersion)
	server.GetCluster().LogSQL(logs, err, server.URL, "MasterFailover", LvlErr, "Could not set server a primary")
	return logs, err
}

func (server *ServerMonitor) SetState(state string) {
	cluster := server.ClusterGroup
	if server.PrevState != state {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Server %s state transition from %s changed to: %s", server.URL, server.PrevState, state)
		_, file, no, ok := runtime.Caller(1)
		if ok {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Set state called from %s#%d\n", file, no)
		}
	}
	server.State = state
}

func (server *ServerMonitor) SetPrevState(state string) {
	cluster := server.ClusterGroup
	if state == "" {
		return
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Server %s previous state set to: %s", server.URL, state)
	server.PrevState = state
}

func (server *ServerMonitor) SetFailed() {
	server.SetState(stateFailed)
}

func (server *ServerMonitor) SetMaster() {
	cluster := server.ClusterGroup
	server.SetState(stateMaster)
	//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Server %s state transition from %s changed to: %s in SetMaster", server.URL, server.PrevState, stateMaster)
	_, file, no, ok := runtime.Caller(1)
	if ok {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlDbg, "SetMaster called from %s#%d\n", file, no)
	}
	for _, s := range cluster.Servers {
		s.HaveNoMasterOnStart = false
	}
}

func (server *ServerMonitor) SetPrefered(pref bool) {
	server.Prefered = pref
}

func (server *ServerMonitor) SetPreferedBackup(pref bool) {
	server.PreferedBackup = pref
}

func (server *ServerMonitor) SetSemiSyncReplica() (string, error) {
	logs := ""
	if !server.IsSemiSyncReplica() {
		logs, err := dbhelper.SetSemiSyncSlave(server.Conn, server.DBVersion)
		if err != nil {
			return logs, err
		}
	}
	return logs, nil
}

func (server *ServerMonitor) SetSemiSyncLeader() (string, error) {
	logs := ""
	if !server.IsSemiSyncMaster() {
		logs, err := dbhelper.SetSemiSyncMaster(server.Conn, server.DBVersion)
		if err != nil {
			return logs, err
		}
	}
	return logs, nil
}

func (server *ServerMonitor) SetReadOnly() (string, error) {
	cluster := server.ClusterGroup
	logs := ""
	if !server.IsReadOnly() {
		logs, err := dbhelper.SetReadOnly(server.Conn, true)
		if err != nil {
			return logs, err
		}
	}
	if server.HasSuperReadOnlyCapability() && cluster.Conf.SuperReadOnly {
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
	cluster := server.ClusterGroup
	if cluster.Conf.Arbitration && cluster.IsFailedArbitrator {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Cancel ReadWrite on %s caused by arbitration failed ", server.URL)
		return errors.New("Arbitration is Failed")
	}
	if server.IsReadOnly() {
		logs, err := dbhelper.SetReadOnly(server.Conn, false)
		cluster.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed Set Read Write on %s : %s", server.URL, err)
		if err != nil {
			return err
		}
	}
	if server.HasSuperReadOnlyCapability() {
		logs, err := dbhelper.SetSuperReadOnly(server.Conn, false)
		cluster.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed Set Super Read Write on %s : %s", server.URL, err)
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
	cluster := server.ClusterGroup
	pgdsn := func() string {
		dsn := ""
		//push the password at the end because empty password may consider next parameter is paswword
		if cluster.HaveDBTLSCert {
			dsn += "sslmode=enable"
		} else {
			dsn += "sslmode=disable"
		}
		dsn += fmt.Sprintf(" host=%s port=%s user=%s dbname=%s connect_timeout=%d password=%s ", server.Host, server.Port, server.User, server.PostgressDB, cluster.Conf.Timeout, server.Pass)
		//dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s connect_timeout=1", server.Host, server.Port, server.User, server.Pass, "postgres")

		return dsn
	}
	mydsn := func() string {
		params := fmt.Sprintf("?timeout=%ds&readTimeout=%ds", cluster.Conf.Timeout, cluster.Conf.ReadTimeout)
		dsn := server.User + ":" + server.Pass + "@"
		if cluster.Conf.TunnelHost != "" {
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
			dsn += "unix(" + cluster.Conf.Socket + ")/" + params
		}
		if cluster.HaveDBTLSCert {
			dsn += server.TLSConfigUsed
		}
		return dsn
	}
	if cluster.Conf.MasterSlavePgStream || cluster.Conf.MasterSlavePgLogical {
		server.DSN = pgdsn()
	} else {
		server.DSN = mydsn()
		if cluster.HaveDBTLSCert {
			mysql.RegisterTLSConfig(ConstTLSCurrentConfig, cluster.tlsconf)
			if cluster.HaveDBTLSOldCert {
				mysql.RegisterTLSConfig(ConstTLSOldConfig, cluster.tlsoldconf)
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
	cluster := server.ClusterGroup
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Cannot resolved DNS for host %s, error: %s", server.Host, err.Error())
	}
	if server.PostgressDB == "" {
		server.PostgressDB = "test"
	}
	server.SetDSN()

}

func (server *ServerMonitor) SetReplicationGTIDSlavePosFromServer(master *ServerMonitor) (string, error) {
	cluster := server.ClusterGroup
	server.StopSlave()

	changeOpt := dbhelper.ChangeMasterOpt{
		Host:        master.Host,
		Port:        master.Port,
		User:        master.ClusterGroup.GetRplUser(),
		Password:    master.ClusterGroup.GetRplPass(),
		Retry:       strconv.Itoa(master.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
		Heartbeat:   strconv.Itoa(master.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
		SSL:         cluster.Conf.ReplicationSSL,
		Channel:     cluster.Conf.MasterConn,
		IsDelayed:   server.IsDelayed,
		Delay:       strconv.Itoa(cluster.Conf.HostsDelayedTime),
		PostgressDB: server.PostgressDB,
	}

	if server.IsMariaDB() {
		changeOpt.Mode = "SLAVE_POS"
		return dbhelper.ChangeMaster(server.Conn, changeOpt, server.DBVersion)
	}
	changeOpt.Mode = "MASTER_AUTO_POSITION"
	return dbhelper.ChangeMaster(server.Conn, changeOpt, server.DBVersion)
}

func (server *ServerMonitor) SetReplicationGTIDCurrentPosFromServer(master *ServerMonitor) (string, error) {
	cluster := server.ClusterGroup
	var err error
	logs := ""
	changeOpt := dbhelper.ChangeMasterOpt{
		SSL:         cluster.Conf.ReplicationSSL,
		Channel:     cluster.Conf.MasterConn,
		IsDelayed:   server.IsDelayed,
		Delay:       strconv.Itoa(cluster.Conf.HostsDelayedTime),
		PostgressDB: server.PostgressDB,
	}
	if server.DBVersion.IsMySQLOrPerconaGreater57() {
		// We can do MySQL 5.7 style failover
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Doing MySQL GTID switch of the old master")
		changeOpt.Host = cluster.master.Host
		changeOpt.Port = cluster.master.Port
		changeOpt.User = cluster.GetRplUser()
		changeOpt.Password = cluster.GetRplPass()
		changeOpt.Retry = strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry)
		changeOpt.Heartbeat = strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime)
		changeOpt.Mode = "MASTER_AUTO_POSITION"
		logs, err = dbhelper.ChangeMaster(server.Conn, changeOpt, server.DBVersion)
	} else {
		changeOpt.Host = master.Host
		changeOpt.Port = master.Port
		changeOpt.User = master.ClusterGroup.GetRplUser()
		changeOpt.Password = master.ClusterGroup.GetRplPass()
		changeOpt.Retry = strconv.Itoa(master.ClusterGroup.Conf.ForceSlaveHeartbeatRetry)
		changeOpt.Heartbeat = strconv.Itoa(master.ClusterGroup.Conf.ForceSlaveHeartbeatTime)
		changeOpt.Mode = "CURRENT_POS"
		logs, err = dbhelper.ChangeMaster(server.Conn, changeOpt, server.DBVersion)
	}
	return logs, err
}

func (server *ServerMonitor) SetReplicationFromMaxsaleServer(master *ServerMonitor) (string, error) {
	return dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
		Host:      master.Host,
		Port:      master.Port,
		User:      master.ClusterGroup.GetRplUser(),
		Password:  master.ClusterGroup.GetRplPass(),
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

func (server *ServerMonitor) createCookie(key string) error {
	cluster := server.ClusterGroup
	newFile, err := os.Create(server.Datadir + "/@" + key)
	defer newFile.Close()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlDbg, "Create cookie (%s) %s", key, err)
	}
	return err
}

func (server *ServerMonitor) SetProvisionCookie() error {
	return server.createCookie("cookie_prov")
}

func (server *ServerMonitor) SetUnprovisionCookie() error {
	return server.createCookie("cookie_unprov")
}

func (server *ServerMonitor) SetRestartCookie() error {
	return server.createCookie("cookie_restart")
}

func (server *ServerMonitor) SetWaitStartCookie() error {
	return server.createCookie("cookie_waitstart")
}

func (server *ServerMonitor) SetWaitStopCookie() error {
	return server.createCookie("cookie_waitstop")
}

func (server *ServerMonitor) SetReprovCookie() error {
	return server.createCookie("cookie_reprov")
}

func (server *ServerMonitor) SetWaitBackupCookie() error {
	return server.createCookie("cookie_waitbackup")
}

func (server *ServerMonitor) SetBackupPhysicalCookie() error {
	return server.createCookie("cookie_physicalbackup")
}
func (server *ServerMonitor) SetBackupLogicalCookie() error {
	return server.createCookie("cookie_logicalbackup")
}

func (server *ServerMonitor) SetReplicationCredentialsRotation(ss *dbhelper.SlaveStatus) {
	cluster := server.ClusterGroup
	if server.GetCluster().Conf.IsVaultUsed() {
		server.GetCluster().SetClusterReplicationCredentialsFromConfig()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Vault replication user password rotation")
		err := server.rejoinSlaveChangePassword(ss)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlWarn, "Rejoin slave change password error: %s", err)
		}
		if server.GetCluster().Conf.VaultMode == VaultConfigStoreV2 {
			for _, u := range server.GetCluster().master.Users {
				if u.User == server.GetCluster().GetRplUser() {
					logs, err := dbhelper.SetUserPassword(server.GetCluster().master.Conn, server.GetCluster().master.DBVersion, u.Host, u.User, server.GetCluster().GetRplPass())
					cluster.LogSQL(logs, err, server.URL, "Security", LvlErr, "Alter user : %s", err)

				}

			}
		}
	}
}
