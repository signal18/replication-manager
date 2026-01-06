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
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
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

func (server *ServerMonitor) SetIgnoredReadonly(ignored bool) {
	server.IgnoredRO = ignored
}

func (server *ServerMonitor) SetEventScheduler(value bool) (string, error) {
	logs, err := dbhelper.SetEventScheduler(server.Conn, value, server.DBVersion)
	return logs, err
}

func (server *ServerMonitor) SetGroupReplicationPrimary() (string, error) {
	logs, err := dbhelper.SetGroupReplicationPrimary(server.Conn, server.DBVersion)
	server.GetCluster().LogSQL(logs, err, server.URL, "MasterFailover", config.LvlErr, "Could not set server a primary")
	return logs, err
}

func (server *ServerMonitor) SetState(state string) {
	cluster := server.ClusterGroup
	if server.PrevState != state {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Server %s state transition from %s changed to: %s", server.URL, server.PrevState, state)
		_, file, no, ok := runtime.Caller(1)
		if ok {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "Set state called from %s#%d\n", file, no)
		}
		cluster.BashScriptDbServersChangeState(server, state, server.PrevState)
	}
	server.State = state
}

func (server *ServerMonitor) SetPrevState(state string) {
	cluster := server.ClusterGroup
	if state == "" {
		return
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Server %s previous state set to: %s", server.URL, state)
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
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "SetMaster called from %s#%d\n", file, no)
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
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel ReadWrite on %s caused by arbitration failed ", server.URL)
		return errors.New("Arbitration is Failed")
	}
	if server.IsReadOnly() {
		logs, err := dbhelper.SetReadOnly(server.Conn, false)
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed Set Read Write on %s : %s", server.URL, err)
		if err != nil {
			return err
		}
	}
	if server.HasSuperReadOnlyCapability() {
		logs, err := dbhelper.SetSuperReadOnly(server.Conn, false)
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed Set Super Read Write on %s : %s", server.URL, err)
		if err != nil {
			return err
		}
	}
	_, file, no, ok := runtime.Caller(1)
	if ok {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "Set RW called from %s#%d\n", file, no)
	}
	return nil
}

func (server *ServerMonitor) SetMaintenance() {
	server.IsMaintenance = true
	server.ClusterGroup.BashScriptDbServersChangeState(server, stateMaintenance, server.State)
	server.ClusterGroup.SetProxyServerMaintenance(server.ServerID)
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
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cannot resolved DNS for host %s, error: %s", server.Host, err.Error())
	}
	if server.PostgressDB == "" {
		server.PostgressDB = "test"
	}
	server.SetDSN()

}

func (server *ServerMonitor) SetReplicationGTIDSlavePosFromServer(master *ServerMonitor) (string, error) {
	cluster := server.ClusterGroup
	server.StopSlave()

	if server.IsMariaDB() {
		return cluster.pointSlaveToMasterWithMode(server, "SLAVE_POS")
	}

	return cluster.pointSlaveToMasterWithMode(server, "MASTER_AUTO_POSITION")
}

func (server *ServerMonitor) SetReplicationGTIDCurrentPosFromServer(master *ServerMonitor) (string, error) {
	cluster := server.ClusterGroup

	if server.DBVersion.IsMySQLOrPerconaGreater57() {
		// We can do MySQL 5.7 style failover
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Doing MySQL GTID switch of the old master")
		return cluster.pointSlaveToMasterWithMode(server, "MASTER_AUTO_POSITION")
	}

	return cluster.pointSlaveToMasterWithMode(server, "CURRENT_POS")
}

func (server *ServerMonitor) SetReplicationFromMaxscaleServer(master *ServerMonitor) (string, error) {
	opt := server.ClusterGroup.GetChangeMasterBaseOptForMxs(server, master)
	opt.Logfile = master.FailoverMasterLogFile
	opt.Logpos = master.FailoverMasterLogPos
	return dbhelper.ChangeMaster(server.Conn, opt, server.DBVersion)
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

	if _, err := os.Stat(server.Datadir + "/@" + key); os.IsNotExist(err) {
		newFile, err := os.Create(server.Datadir + "/@" + key)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Create cookie (%s) %s", key, err)
			return err
		}
		defer newFile.Close()
	}

	return nil
}

func (server *ServerMonitor) SetProvisionCookie() error {
	return server.createCookie("cookie_prov")
}

func (server *ServerMonitor) SetProvisionDBUsersCookie() error {
	return server.createCookie("cookie_provision_db_users")
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

func (server *ServerMonitor) SetWaitDBACredCookie() error {
	return server.createCookie("cookie_waitdbacred")
}

func (server *ServerMonitor) SetWaitSponsorCredCookie() error {
	return server.createCookie("cookie_waitsponsorcred")
}

func (server *ServerMonitor) SetConfigCookie() error {
	return server.createCookie("cookie_config")
}

func (server *ServerMonitor) SetConfigPathCookie() error {
	return server.createCookie("cookie_configpath")
}

func (server *ServerMonitor) SetConfigRefreshCookie() error {
	return server.createCookie("cookie_configrefresh")
}

func (server *ServerMonitor) SetNoConfigFetchCookie() error {
	return server.createCookie("cookie_noconfigfetch")
}

func (server *ServerMonitor) SetWaitBackupCookie() error {
	return server.createCookie("cookie_waitbackup")
}

func (server *ServerMonitor) SetWaitLogicalBackupCookie() error {
	return server.createCookie("cookie_waitlogicalbackup")
}

func (server *ServerMonitor) SetWaitPhysicalBackupCookie() error {
	return server.createCookie("cookie_waitphysicalbackup")
}

func (server *ServerMonitor) SetBackupPhysicalCookie(tool string) error {
	switch tool {
	case config.ConstBackupPhysicalTypeXtrabackup, config.ConstBackupPhysicalTypeMariaBackup:
		return server.createCookie("cookie_backup_" + tool)
	default:
		return server.createCookie("cookie_physicalbackup")
	}
}

func (server *ServerMonitor) SetBackupLogicalCookie(tool string) error {
	switch tool {
	case "script", config.ConstBackupLogicalTypeMysqldump, config.ConstBackupLogicalTypeMydumper, config.ConstBackupLogicalTypeDumpling:
		return server.createCookie("cookie_backup_" + tool)
	default:
		return server.createCookie("cookie_logicalbackup")
	}
}

func (server *ServerMonitor) SetWaitRunJobSSHCookie() error {
	return server.createCookie("cookie_waitrunjobssh")
}

func (server *ServerMonitor) SetLoadingJobList(val bool) {
	server.IsLoadingJobList = val
}

func (server *ServerMonitor) SetReplicationCredentialsRotation(ss *dbhelper.SlaveStatus) {
	cluster := server.ClusterGroup
	if server.GetCluster().Conf.IsVaultUsed() {
		server.GetCluster().SetClusterReplicationCredentialsFromConfig()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Vault replication user password rotation")
		err := server.rejoinSlaveChangePassword(ss)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Rejoin slave change password error: %s", err)
		}
		if server.GetCluster().Conf.VaultMode == VaultConfigStoreV2 {
			for _, u := range server.GetCluster().master.Users.ToNewMap() {
				if u.User == server.GetCluster().GetRplUser() {
					logs, err := dbhelper.SetUserPassword(server.GetCluster().master.Conn, server.GetCluster().master.DBVersion, u.Host, u.User, server.GetCluster().GetRplPass())
					cluster.LogSQL(logs, err, server.URL, "Security", config.LvlErr, "Alter user : %s", err)

				}

			}
		}
	}
}

func (server *ServerMonitor) SetBackingUpBinaryLog(value bool) {
	server.IsBackingUpBinaryLog = value
}

func (server *ServerMonitor) SetBinaryLogDir(value string) {
	server.BinaryLogDir = value
}

func (server *ServerMonitor) SetBinaryLogName(value string) {
	server.BinaryLogName = value
}

func (server *ServerMonitor) SetInCaptureMode(value bool) {
	server.InCaptureMode = value
}

func (server *ServerMonitor) SetInRefreshBinlog(value bool) {
	server.IsRefreshingBinlog = value
}

func (server *ServerMonitor) SetInRefreshBinlogMeta(value bool) {
	server.IsRefreshingBinlogMeta = value
}

func (server *ServerMonitor) SetInReseedBackup(value string) {
	server.IsReseeding = value
}

func (server *ServerMonitor) SetNeedRefreshJobs(value bool) {
	server.NeedRefreshJobs = value
}

func (server *ServerMonitor) SetRestartNode(value string) {
	server.RestartNode = value
}

func (server *ServerMonitor) SetRestartRid(value string) {
	server.RestartRid = value
}

func (server *ServerMonitor) SetPointInTimeMeta(value backupmgr.PointInTimeMeta) {
	server.PointInTimeMeta = value
}

func (server *ServerMonitor) SetSuspect() {
	server.SetState(stateSuspect)
}

func (server *ServerMonitor) SetMyGTIDTransitional(force bool) error {
	if server.DBVersion.IsMySQLOrPercona() && server.DBVersion.GreaterEqual("5.7.6") {
		if server.Variables.Get("ENFORCE_GTID_CONSISTENCY") == "OFF" {
			_, err := dbhelper.SetEnforceGTIDConsistency(server.Conn, "ON")
			if err != nil {
				return err
			}
		}

		if server.Variables.Get("GTID_MODE") == "OFF" {
			_, err := dbhelper.SetMySQLGtidMode(server.Conn, "OFF_PERMISSIVE")
			if err != nil {
				return err
			}
		}

		if server.Variables.Get("GTID_MODE") == "OFF_PERMISSIVE" {
			_, err := dbhelper.SetMySQLGtidMode(server.Conn, "ON_PERMISSIVE")
			if err != nil {
				return err
			}
		}

		if server.Variables.Get("GTID_MODE") == "ON_PERMISSIVE" && force {
			_, err := dbhelper.SetMySQLGtidMode(server.Conn, "ON")
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (server *ServerMonitor) SetDBCredentials(user, password string) error {
	cluster := server.ClusterGroup
	var found bool
	if !server.IsMaster() && server.IsSlave {
		return errors.New("Cannot set credentials on slave server")
	}

	conn, err := server.GetNewDBConn()
	if err != nil {
		return err
	}

	for _, u := range server.Users.ToNewMap() {
		if u.User == user {
			found = true
			logs, err := dbhelper.SetUserPassword(conn, server.DBVersion, u.Host, u.User, password)
			cluster.LogSQL(strings.Replace(logs, password, "*.*", -1), err, server.URL, "Security", config.LvlErr, "Alter user : %s", err)
			if err != nil {
				return err
			}
		}
	}

	if !found {
		// create user for replication-manager host
		logs, err := dbhelper.CreateUser(conn, server.DBVersion, cluster.Conf.MonitorAddress, user, password)
		cluster.LogSQL(logs, err, server.URL, "Security", config.LvlErr, "Create user : %s", err)
		if err != nil {
			return err
		}

		// create user with all db servers
		for _, h := range server.ClusterGroup.Servers {
			logs, err := dbhelper.CreateUser(conn, server.DBVersion, h.Host, user, password)
			cluster.LogSQL(logs, err, server.URL, "Security", config.LvlErr, "Create user : %s", err)
			if err != nil {
				return err
			}

		}

		// create user for all proxies
		for _, p := range server.ClusterGroup.Proxies {
			logs, err := dbhelper.CreateUser(conn, server.DBVersion, p.GetHost(), user, password)
			cluster.LogSQL(logs, err, server.URL, "Security", config.LvlErr, "Create user : %s", err)
			if err != nil {
				return err
			}
		}

		// create user for all apps if any
		for _, a := range server.ClusterGroup.Apps {
			logs, err := dbhelper.CreateUser(conn, server.DBVersion, a.GetHost(), user, password)
			cluster.LogSQL(logs, err, server.URL, "Security", config.LvlErr, "Create user : %s", err)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (server *ServerMonitor) SetUserDBGrants(user, host string, grantOpt bool, grants ...string) error {
	cluster := server.ClusterGroup
	var logs string

	if server.IsSlave && !server.IsMaster() {
		return errors.New("Cannot set credentials on slave server")
	}

	dbconn, err := server.GetNewDBConn()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cluster.Conf.ExecTimeout)*time.Second)
	defer cancel()

	conn, err := dbconn.Connx(ctx)
	if err != nil {
		return err
	}

	if grantOpt {
		logs, err = dbhelper.SetUserGrantsWithGrantOption(ctx, conn, server.DBVersion, host, user, grants...)
	} else {
		logs, err = dbhelper.SetUserGrants(ctx, conn, server.DBVersion, host, user, grants...)
	}
	cluster.LogSQL(logs, err, server.URL, "Security", config.LvlErr, "Set user grants : %s", err)

	return nil
}

func (server *ServerMonitor) SetDBUserCredentials(user, pass string, withGrantOption bool, grants ...string) error {
	err := server.SetDBCredentials(user, pass)
	if err != nil {
		return err
	}

	if len(grants) == 0 {
		return nil
	}

	// refresh user list
	users, _, err := dbhelper.GetUsers(server.Conn, server.DBVersion)
	server.Users = dbhelper.FromNormalGrantsMap(server.Users, users)

	// set grants for all hosts of this user
	for _, u := range server.Users.ToNewMap() {
		if u.User == user {
			err = server.SetUserDBGrants(user, u.Host, withGrantOption, grants...)
		}
	}
	return err
}

func (server *ServerMonitor) CreateDBUserFromConfig(role string) error {
	cluster := server.ClusterGroup
	var err error
	switch role {
	case "dba":
		user, pass := misc.SplitPair(cluster.Conf.GetDecryptedValue("cloud18-dba-user-credentials"))
		if user == "" || pass == "" {
			if user == "" {
				user = "dba"
			}
			pass, _ = cluster.GeneratePassword()
			err = cluster.SetDatabaseCredentials(role, user+":"+pass)
			if err != nil {
				return err
			}
			user, pass = misc.SplitPair(cluster.Conf.GetDecryptedValue("cloud18-dba-user-credentials"))
		}

		err = server.SetDBUserCredentials(user, pass, true, "ALL PRIVILEGES ON *.*")
		if err != nil {
			return err
		}
	case "sponsor":
		user, pass := misc.SplitPair(cluster.Conf.GetDecryptedValue("cloud18-sponsor-user-credentials"))

		if user == "" || pass == "" {
			if user == "" {
				user = "sponsor"
			}
			pass, _ = cluster.GeneratePassword()
			err = cluster.SetDatabaseCredentials(role, user+":"+pass)
			if err != nil {
				return err
			}

			user, pass = misc.SplitPair(cluster.Conf.GetDecryptedValue("cloud18-sponsor-user-credentials"))
		}

		err = server.SetDBUserCredentials(user, pass, true, "ALL PRIVILEGES ON *.*")
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("Unknown role: %s", role)
	}

	return nil
}

func (server *ServerMonitor) RevokeDBUserGrants(user, host string) error {
	cluster := server.ClusterGroup
	var logs string

	if user == "" || host == "" {
		return errors.New("User and host are required")
	}

	if server.IsSlave && !server.IsMaster() {
		return errors.New("Cannot revoke credentials on slave server")
	}

	dbconn, err := server.GetNewDBConn()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cluster.Conf.ExecTimeout)*time.Second)
	defer cancel()

	conn, err := dbconn.Connx(ctx)
	if err != nil {
		return err
	}

	logs, err = dbhelper.RevokeUserGrants(ctx, conn, server.DBVersion, host, user)
	cluster.LogSQL(logs, err, server.URL, "Security", config.LvlErr, "Set user grants : %s", err)

	return nil
}

func (server *ServerMonitor) SetWaitAuditlogCookie() error {
	return server.createCookie("cookie_wait_auditlog")
}

func (server *ServerMonitor) SetWaitErrorlogCookie() error {
	return server.createCookie("cookie_wait_errorlog")
}

func (server *ServerMonitor) SetWaitSqlErrorlogCookie() error {
	return server.createCookie("cookie_wait_sql_errorlog")
}

func (server *ServerMonitor) SetWaitSlowqueryCookie() error {
	return server.createCookie("cookie_wait_slowquery")
}

func (server *ServerMonitor) SetWaitJobsCheckCookie() error {
	return server.createCookie("cookie_wait_jobs_check")
}

func (server *ServerMonitor) SetWaitJobsUpgradeCookie() error {
	return server.createCookie("cookie_wait_jobs_upgrade")
}

func (server *ServerMonitor) SetWaitDummyConfigSendCookie() error {
	return server.createCookie("cookie_wait_dummy_send")
}

func (server *ServerMonitor) SetRollingJobsUpgradeCookie() error {
	return server.createCookie("cookie_rolling_jobs_upgrade")
}

func (server *ServerMonitor) SetErrState(key, errtype, from, desc string, args ...interface{}) {
	cluster := server.ClusterGroup
	cluster.SetState(key, state.State{
		ErrType:   errtype,
		ErrDesc:   fmt.Sprintf(desc, args...),
		ErrFrom:   from,
		ServerUrl: server.URL,
	})
}

func (server *ServerMonitor) SetRunningJobs(running bool) {
	server.jobMutex.Lock()
	defer server.jobMutex.Unlock()
	server.IsRunningJobs = running
}

// TryAcquireJobLock atomically checks if a job is running and sets the flag if not
// Returns true if the lock was acquired, false if a job is already running
func (server *ServerMonitor) TryAcquireJobLock() bool {
	server.jobMutex.Lock()
	defer server.jobMutex.Unlock()

	if server.IsRunningJobs {
		return false
	}

	server.IsRunningJobs = true
	return true
}

// ReleaseJobLock releases the job lock
func (server *ServerMonitor) ReleaseJobLock() {
	server.jobMutex.Lock()
	defer server.jobMutex.Unlock()
	server.IsRunningJobs = false
}
