// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//
//	Stephane Varoqui  <svaroqui@gmail.com>
//
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

func (server *ServerMonitor) RejoinLoop() error {
	cluster := server.ClusterGroup
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "rejoin %s to the loop", server.URL)
	child := server.GetSibling()
	if child == nil {
		return errors.New("Could not found sibling slave")
	}
	child.StopSlave()
	child.SetReplicationGTIDSlavePosFromServer(server)
	child.StartSlave()
	return nil
}

// RejoinMaster a server that just show up without slave status
func (server *ServerMonitor) RejoinMaster() error {
	cluster := server.ClusterGroup
	// Check if master exists in topology before rejoining.
	defer func() {
		cluster.rejoinCond.Send <- true
	}()
	if cluster.GetTopology() == topoMultiMasterWsrep {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoining leader %s ignored caused by wsrep protocol", server.URL)
		return nil
	}

	if cluster.StateMachine.IsInFailover() {
		return nil
	}
	// if cluster.Conf.LogLevel > 2 {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoining standalone server %s", server.URL)
	// }
	// Strange here add comment for why
	cluster.canFlashBack = true

	if cluster.master != nil {
		if server.URL != cluster.master.URL {
			cluster.SetState("WARN0022", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0022"], server.URL, cluster.master.URL), ErrFrom: "REJOIN"})
			server.RejoinScript()
			if cluster.Conf.MultiMasterGrouprep {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Group replication rejoin  %s server to PRIMARY ", server.URL)
				server.StartGroupReplication()

			} else {
				if cluster.Conf.FailoverSemiSyncState {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Set semisync replica and disable semisync leader %s", server.URL)
					logs, err := server.SetSemiSyncReplica()
					cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed Set semisync replica and disable semisync  %s, %s", server.URL, err)
				}
				crash := cluster.getCrashFromJoiner(server.URL)
				if crash == nil {
					cluster.SetState("ERR00066", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00066"], server.URL, cluster.master.URL), ErrFrom: "REJOIN"})
					if cluster.oldMaster != nil {
						if cluster.oldMaster.URL == server.URL {
							server.RejoinMasterSST()
							return nil
						}
					}
					if cluster.Conf.Autoseed {
						server.ReseedMasterSST()
						return nil
					} else {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "No auto seeding %s", server.URL)
						return errors.New("No Autoseed")
					}
				} //crash info is available
				if cluster.Conf.AutorejoinBackupBinlog == true {
					server.backupBinlog(crash)
				}

				err := server.rejoinMasterIncremental(crash)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Failed to autojoin incremental to master %s", server.URL)
					err := server.RejoinMasterSST()
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "State transfer rejoin failed")
					}
				}
				if cluster.Conf.AutorejoinBackupBinlog == true {
					server.saveBinlog(crash)
				}

			}

			// if consul or internal proxy need to adapt read only route to new slaves
			cluster.backendStateChangeProxies()
		}
	} else {
		//no master discovered rediscovering from last seen
		if cluster.lastmaster != nil {
			if cluster.lastmaster.ServerID == server.ServerID {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rediscovering same master from last seen master: %s", server.URL)
				cluster.master = server
				server.SetMaster()
				server.SetReadWrite()
				cluster.lastmaster = nil
			} else {
				if cluster.Conf.FailRestartUnsafe == false {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rediscovering not the master from last seen master: %s", server.URL)
					server.rejoinMasterAsSlave()
					// if consul or internal proxy need to adapt read only route to new slaves
					cluster.backendStateChangeProxies()
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rediscovering unsafe possibly electing old leader after cascading failure to flavor availability: %s", server.URL)
					cluster.master = server
				}
			}

		} // we have last seen master

	}
	return nil
}

func (server *ServerMonitor) RejoinPreviousSnapshot() error {
	_, err := server.JobZFSSnapBack()
	return err
}

func (server *ServerMonitor) RejoinMasterSST() error {
	cluster := server.ClusterGroup
	if cluster.Conf.AutorejoinMysqldump == true {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoin flashback dump restore %s", server.URL)
		err := server.RejoinDirectDump()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "mysqldump flashback restore failed %s", err)
			return errors.New("Dump from master failed")
		}
	} else if cluster.Conf.AutorejoinLogicalBackup {
		server.JobFlashbackLogicalBackup()
	} else if cluster.Conf.AutorejoinPhysicalBackup {
		server.JobFlashbackPhysicalBackup()
	} else if cluster.Conf.AutorejoinZFSFlashback {
		server.RejoinPreviousSnapshot()
	} else if cluster.Conf.BackupLoadScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling restore script")
		var out []byte
		out, err := exec.Command(cluster.Conf.BackupLoadScript, misc.Unbracket(server.Host), misc.Unbracket(cluster.master.Host), server.Port, server.GetCluster().GetMaster().Port).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Restore script complete %s", string(out))
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "No SST rejoin method found")
		return errors.New("No SST rejoin flashback method found")
	}

	return nil
}

func (server *ServerMonitor) RejoinScript() {
	cluster := server.ClusterGroup
	// Call pre-rejoin script
	if server.GetCluster().Conf.RejoinScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling rejoin script")
		var out []byte
		var err error
		out, err = exec.Command(cluster.Conf.RejoinScript, server.Host, server.GetCluster().GetMaster().Host, server.Port, server.GetCluster().GetMaster().Port).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s", err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin script complete:", string(out))
	}
}

func (server *ServerMonitor) ReseedMasterSST() error {
	cluster := server.ClusterGroup
	server.DelWaitBackupCookie()
	if cluster.Conf.AutorejoinMysqldump == true {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoin dump restore %s", server.URL)
		err := server.RejoinDirectDump()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "mysqldump restore failed %s", err)
			return errors.New("Dump from master failed")
		}
	} else {
		if cluster.Conf.BackupLoadScript != "" {
			server.JobReseedBackupScript()
		} else if cluster.Conf.AutorejoinLogicalBackup {
			server.JobReseedLogicalBackup()
		} else if cluster.Conf.AutorejoinPhysicalBackup {
			server.JobReseedPhysicalBackup()
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "No SST reseed method found")
			return errors.New("No SST reseed method found")
		}
	}

	return nil
}

func (server *ServerMonitor) rejoinMasterSync(crash *Crash) error {
	cluster := server.ClusterGroup
	if server.HasGTIDReplication() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Found same or lower GTID %s and new elected master was %s", server.CurrentGtid.Sprint(), crash.FailoverIOGtid.Sprint())
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Found same or lower sequence %s , %s", server.BinaryLogFile, server.BinaryLogPos)
	}
	var err error
	realmaster := cluster.master
	if cluster.Conf.MxsBinlogOn || cluster.Conf.MultiTierSlave {
		realmaster = cluster.GetRelayServer()
	}
	if server.HasGTIDReplication() || (realmaster.MxsHaveGtid && realmaster.IsMaxscale) {
		logs, err := server.SetReplicationGTIDCurrentPosFromServer(realmaster)
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed in GTID rejoin old master in sync %s, %s", server.URL, err)
		if err != nil {
			return err
		}
	} else if cluster.Conf.MxsBinlogOn {
		logs, err := dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      realmaster.Host,
			Port:      realmaster.Port,
			User:      cluster.GetRplUser(),
			Password:  cluster.GetRplPass(),
			Retry:     strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
			Mode:      "MXS",
			Logfile:   crash.FailoverMasterLogFile,
			Logpos:    crash.FailoverMasterLogPos,
			SSL:       cluster.Conf.ReplicationSSL,
		}, server.DBVersion)
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Change master positional failed in Rejoin old Master in sync to maxscale %s", err)
		if err != nil {
			return err
		}
	} else {
		// not maxscale the new master coordonate are in crash
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Change master to positional in Rejoin old Master")
		logs, err := dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:        realmaster.Host,
			Port:        realmaster.Port,
			User:        cluster.GetRplUser(),
			Password:    cluster.GetRplPass(),
			Retry:       strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
			Heartbeat:   strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
			Mode:        "POSITIONAL",
			Logfile:     crash.NewMasterLogFile,
			Logpos:      crash.NewMasterLogPos,
			SSL:         cluster.Conf.ReplicationSSL,
			Channel:     cluster.Conf.MasterConn,
			IsDelayed:   server.IsDelayed,
			Delay:       strconv.Itoa(cluster.Conf.HostsDelayedTime),
			PostgressDB: server.PostgressDB,
		}, server.DBVersion)
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Change master positional failed in Rejoin old Master in sync %s", err)
		if err != nil {
			return err
		}
	}

	server.StartSlave()
	return err
}

func (server *ServerMonitor) rejoinMasterFlashBack(crash *Crash) error {
	cluster := server.ClusterGroup
	realmaster := cluster.master
	if cluster.Conf.MxsBinlogOn || cluster.Conf.MultiTierSlave {
		realmaster = cluster.GetRelayServer()
	}

	if _, err := os.Stat(cluster.GetMysqlBinlogPath()); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "File does not exist %s", cluster.GetMysqlBinlogPath())
		return err
	}
	if _, err := os.Stat(cluster.GetMysqlclientPath()); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "File does not exist %s", cluster.GetMysqlclientPath())
		return err
	}

	binlogCmd := exec.Command(cluster.GetMysqlBinlogPath(), "--flashback", "--to-last-log", cluster.Conf.WorkingDir+"/"+cluster.Name+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-"+crash.FailoverMasterLogFile)
	clientCmd := exec.Command(cluster.GetMysqlclientPath(), "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--user="+cluster.GetDbUser(), "--password="+cluster.GetDbPass())
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "FlashBack: %s %s", cluster.GetMysqlBinlogPath(), strings.Replace(strings.Join(binlogCmd.Args, " "), cluster.GetRplPass(), "XXXX", -1))
	var err error
	clientCmd.Stdin, err = binlogCmd.StdoutPipe()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Error opening pipe: %s", err)
		return err
	}
	if err := binlogCmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Failed mysqlbinlog command: %s at %s", err, strings.Replace(binlogCmd.Path, cluster.GetRplPass(), "XXXX", -1))
		return err
	}
	if err := clientCmd.Run(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Error starting client: %s at %s", err, strings.Replace(clientCmd.Path, cluster.GetRplPass(), "XXXX", -1))
		return err
	}
	logs, err := dbhelper.SetGTIDSlavePos(server.Conn, crash.FailoverIOGtid.Sprint())
	cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlInfo, "SET GLOBAL gtid_slave_pos = \"%s\"", crash.FailoverIOGtid.Sprint())
	if err != nil {
		return err
	}
	var err2 error
	if server.MxsHaveGtid || server.IsMaxscale == false {
		logs, err2 = server.SetReplicationGTIDSlavePosFromServer(realmaster)
	} else {
		logs, err2 = server.SetReplicationFromMaxsaleServer(realmaster)
	}
	cluster.LogSQL(logs, err2, server.URL, "Rejoin", config.LvlInfo, "Failed SetReplicationGTIDSlavePosFromServer on %s: %s", server.URL, err2)
	if err2 != nil {
		return err2
	}
	logs, err = server.StartSlave()
	cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlInfo, "Failed stop slave on %s: %s", server.URL, err)

	return nil
}

func (server *ServerMonitor) RejoinDirectDump() error {
	cluster := server.ClusterGroup
	var err3 error

	if server.IsReseeding {
		return errors.New("Server is in reseeding state")
	}

	server.SetInReseedBackup(true)

	realmaster := cluster.master
	if cluster.Conf.MxsBinlogOn || cluster.Conf.MultiTierSlave {
		realmaster = cluster.GetRelayServer()
	}

	if realmaster == nil {
		server.SetInReseedBackup(false)
		return errors.New("No master defined exiting rejoin direct dump ")
	}
	// done change master just to set the host and port before dump
	if server.MxsHaveGtid || server.IsMaxscale == false {
		logs, err3 := server.SetReplicationGTIDSlavePosFromServer(realmaster)
		cluster.LogSQL(logs, err3, server.URL, "Rejoin", config.LvlInfo, "Failed SetReplicationGTIDSlavePosFromServer on %s: %s", server.URL, err3)

	} else {
		logs, err3 := dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      realmaster.Host,
			Port:      realmaster.Port,
			User:      cluster.GetRplUser(),
			Password:  cluster.GetRplPass(),
			Retry:     strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
			Mode:      "MXS",
			Logfile:   realmaster.FailoverMasterLogFile,
			Logpos:    realmaster.FailoverMasterLogPos,
			SSL:       cluster.Conf.ReplicationSSL,
			Channel:   cluster.Conf.MasterConn,
		}, server.DBVersion)
		cluster.LogSQL(logs, err3, server.URL, "Rejoin", config.LvlErr, "Failed change master maxscale on %s: %s", server.URL, err3)
	}
	if err3 != nil {
		server.SetInReseedBackup(false)
		return err3
	}
	// dump here
	backupserver := cluster.GetBackupServer()
	if backupserver == nil {
		go cluster.JobRejoinMysqldumpFromSource(cluster.master, server)
	} else {
		go cluster.JobRejoinMysqldumpFromSource(backupserver, server)
	}
	return nil
}

func (server *ServerMonitor) rejoinMasterIncremental(crash *Crash) error {
	cluster := server.ClusterGroup
	if server.GetCluster().GetConf().AutorejoinForceRestore {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Cancel incremental rejoin server %s caused by force backup restore  ", server.URL)
		return errors.New("autorejoin-force-restore is on can't just rejoin from current pos")
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoin master incremental %s", server.URL)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Crash info %s", crash)
	server.Refresh()
	if cluster.Conf.ReadOnly && !server.IsIgnoredReadonly() {
		logs, err := server.SetReadOnly()
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to set read only on server %s, %s ", server.URL, err)
	}

	if crash.FailoverIOGtid != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoined GTID sequence  %d from server id %d", server.CurrentGtid.GetSeqServerIdNos(server.GetUniversalGtidServerID()), server.GetUniversalGtidServerID())
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Crash Saved GTID sequence %d from server id %d", crash.FailoverIOGtid.GetSeqServerIdNos(server.GetUniversalGtidServerID()), server.GetUniversalGtidServerID())
	}
	if server.isReplicationAheadOfMasterElection(crash) == false || cluster.Conf.MxsBinlogOn {
		server.rejoinMasterSync(crash)
		return nil
	} else {
		// don't try flashback on old style replication that are ahead jump to SST
		if server.HasGTIDReplication() == false {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Incremental canceled caused by old style replication")
			return errors.New("Incremental canceled caused by old style replication")
		}
	}
	if crash.FailoverIOGtid != nil {
		// cluster.master.FailoverIOGtid.GetSeqServerIdNos(uint64(server.ServerID)) == 0
		// lookup in crash recorded is the current master
		if crash.FailoverIOGtid.GetSeqServerIdNos(uint64(server.ServerID)) == 0 {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Cascading failover, consider we cannot flashback")
			cluster.canFlashBack = false
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Found server ID in rejoining ID %s and crash FailoverIOGtid %s Master %s", server.ServerID, crash.FailoverIOGtid.Sprint(), cluster.master.URL)
		}
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Old server GTID for flashback not found")
	}
	if crash.FailoverIOGtid != nil && cluster.canFlashBack == true && cluster.Conf.AutorejoinFlashback == true && cluster.Conf.AutorejoinBackupBinlog == true {
		err := server.rejoinMasterFlashBack(crash)
		if err == nil {
			return nil
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Flashback rejoin failed: %s", err)
		return errors.New("Flashback failed")
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "No flashback rejoin can flashback %t, autorejoin-flashback %t autorejoin-backup-binlog %t", cluster.canFlashBack, cluster.Conf.AutorejoinFlashback, cluster.Conf.AutorejoinBackupBinlog)
		return errors.New("Flashback disabled")
	}

}

func (server *ServerMonitor) rejoinMasterAsSlave() error {
	cluster := server.ClusterGroup
	realmaster := cluster.lastmaster
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoining old master server %s to saved master %s", server.URL, realmaster.URL)
	logs, err := server.SetReadOnly()
	cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to set read only on server %s, %s ", server.URL, err)
	if err == nil {
		logs, err = server.SetReplicationGTIDCurrentPosFromServer(realmaster)
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to autojoin indirect master server %s, stopping slave as a precaution %s ", server.URL, err)
		if err == nil {
			logs, err = server.StartSlave()
			cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to stop slave on erver %s, %s ", server.URL, err)
		} else {

			return err
		}
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Rejoin master as slave can't set read only %s", err)
		return err
	}
	return nil
}

func (server *ServerMonitor) rejoinSlaveChangePassword(ss *dbhelper.SlaveStatus) error {
	cluster := server.ClusterGroup
	logs, err := dbhelper.ChangeReplicationPassword(server.Conn, dbhelper.ChangeMasterOpt{
		User:     cluster.GetRplUser(),
		Password: cluster.GetRplPass(),
		Channel:  cluster.Conf.MasterConn,
	}, server.DBVersion)
	cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Change master for password rotation : %s", err)
	if err != nil {
		return err
	}

	return nil
}

func (server *ServerMonitor) rejoinSlave(ss dbhelper.SlaveStatus) error {
	// Test if slave not connected to current master
	cluster := server.ClusterGroup
	defer func() {
		cluster.rejoinCond.Send <- true
	}()

	if cluster.GetTopology() == topoMultiMasterRing || cluster.GetTopology() == topoMultiMasterWsrep {
		if cluster.GetTopology() == topoMultiMasterRing {
			server.RejoinLoop()
		}
		if cluster.GetTopology() == topoMultiMasterWsrep {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoining replica %s ignored caused by wsrep protocol", server.URL)
		}
		return nil

	}
	mycurrentmaster, _ := cluster.GetMasterFromReplication(server)
	if mycurrentmaster == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No master found from replication")
		return errors.New("No master found from replication")
	}
	if cluster.master != nil && mycurrentmaster != nil {
		if cluster.master.URL == mycurrentmaster.URL {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Cancel rejoin, found same leader already from replication %s	", mycurrentmaster.URL)
			return errors.New("Same master found from replication")
		}
		//Found slave to rejoin
		cluster.SetState("ERR00067", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00067"], server.URL, server.PrevState, ss.SlaveIORunning.String, cluster.master.URL), ErrFrom: "REJOIN"})
		if cluster.master.IsDown() && cluster.Conf.FailRestartUnsafe == false {
			server.HaveNoMasterOnStart = true
		}
		if mycurrentmaster.IsMaxscale == false && cluster.Conf.MultiTierSlave == false && cluster.Conf.ReplicationNoRelay {

			if server.HasGTIDReplication() {
				crash := cluster.getCrashFromMaster(cluster.master.URL)
				if crash == nil {
					cluster.SetState("ERR00065", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00065"], server.URL, cluster.master.URL), ErrFrom: "REJOIN"})
					return errors.New("No Crash info on current master")
				}
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Crash info on current master %s", crash)
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Found slave to rejoin %s slave was previously in state %s replication io thread  %s, pointing currently to %s", server.URL, server.PrevState, ss.SlaveIORunning, cluster.master.URL)

				realmaster := cluster.master
				// A SLAVE IS ALWAY BEHIND MASTER
				//		slave_gtid := server.CurrentGtid.GetSeqServerIdNos(uint64(server.GetReplicationServerID()))
				//		master_gtid := crash.FailoverIOGtid.GetSeqServerIdNos(uint64(server.GetReplicationServerID()))
				//	if slave_gtid < master_gtid {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoining slave %s via GTID", server.URL)
				logs, err := server.StopSlave()
				cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to stop slave server %s, stopping slave as a precaution %s", server.URL, err)
				if err == nil {
					logs, err := server.SetReplicationGTIDSlavePosFromServer(realmaster)
					cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to autojoin indirect slave server %s, stopping slave as a precaution %s", server.URL, err)
					if err == nil {
						logs, err := server.StartSlave()
						cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to start  slave server %s, stopping slave as a precaution %s", server.URL, err)
					}
				}
			} else {
				if mycurrentmaster.State != stateFailed && mycurrentmaster.IsRelay {
					// No GTID compatible solution stop relay master wait apply relay and move to real master
					logs, err := mycurrentmaster.StopSlave()
					cluster.LogSQL(logs, err, mycurrentmaster.URL, "Rejoin", config.LvlErr, "Failed to stop slave on relay server  %s: %s", mycurrentmaster.URL, err)
					if err == nil {
						logs, err2 := dbhelper.MasterPosWait(server.Conn, server.DBVersion, mycurrentmaster.BinaryLogFile, mycurrentmaster.BinaryLogPos, 3600, cluster.Conf.MasterConn)
						cluster.LogSQL(logs, err2, server.URL, "Rejoin", config.LvlErr, "Failed positional rejoin wait pos %s %s", server.URL, err2)
						if err2 == nil {
							myparentss, _ := mycurrentmaster.GetSlaveStatus(mycurrentmaster.ReplicationSourceName)

							logs, err := server.StopSlave()
							cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to stop slave on server %s: %s", server.URL, err)
							cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Doing Positional switch of slave %s", server.URL)
							logs, changeMasterErr := dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
								Host:        cluster.master.Host,
								Port:        cluster.master.Port,
								User:        cluster.GetRplUser(),
								Password:    cluster.GetRplPass(),
								Logfile:     myparentss.MasterLogFile.String,
								Logpos:      myparentss.ReadMasterLogPos.String,
								Retry:       strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
								Heartbeat:   strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
								Channel:     cluster.Conf.MasterConn,
								IsDelayed:   server.IsDelayed,
								Delay:       strconv.Itoa(cluster.Conf.HostsDelayedTime),
								SSL:         cluster.Conf.ReplicationSSL,
								PostgressDB: server.PostgressDB,
							}, server.DBVersion)

							cluster.LogSQL(logs, changeMasterErr, server.URL, "Rejoin", config.LvlErr, "Rejoin Failed doing Positional switch of slave %s: %s", server.URL, changeMasterErr)

						}
						logs, err = server.StartSlave()
						cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to start slave on %s: %s", server.URL, err)

					}
					mycurrentmaster.StartSlave()
					cluster.LogSQL(logs, err, mycurrentmaster.URL, "Rejoin", config.LvlErr, "Failed to start slave on %s: %s", mycurrentmaster.URL, err)

					if server.IsMaintenance {
						server.SwitchMaintenance()
					}
					// if consul or internal proxy need to adapt read only route to new slaves
					cluster.backendStateChangeProxies()

				} else {
					//Adding state waiting for old master to rejoin in positional mode
					// this state prevent crash info to be removed
					cluster.SetState("ERR00049", state.State{ErrType: "ERRRO", ErrDesc: fmt.Sprintf(clusterError["ERR00049"]), ErrFrom: "TOPO"})
				}
			}
		}
	}
	// In case of state change, reintroduce the server in the slave list
	if server.PrevState == stateFailed || server.PrevState == stateUnconn || server.PrevState == stateSuspect {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Set stateSlave from rejoin slave %s", server.URL)
		server.SetState(stateSlave)
		server.FailCount = 0
		if server.PrevState != stateSuspect {
			cluster.slaves = append(cluster.slaves, server)
		}
		if cluster.Conf.ReadOnly {
			logs, err := dbhelper.SetReadOnly(server.Conn, true)
			cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed to set read only on server %s, %s ", server.URL, err)
			if err != nil {

				return err
			}
		}
	}

	return nil
}

func (server *ServerMonitor) isReplicationAheadOfMasterElection(crash *Crash) bool {
	cluster := server.ClusterGroup
	if server.UsedGtidAtElection(crash) {

		// CurrentGtid fetch from show global variables GTID_CURRENT_POS
		// FailoverIOGtid is fetch at failover from show slave status of the new master
		// If server-id can't be found in FailoverIOGtid can state cascading master failover
		if crash.FailoverIOGtid.GetSeqServerIdNos(server.GetUniversalGtidServerID()) == 0 {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Cascading failover, found empty GTID, forcing full state transfer")
			return true
		}
		if server.CurrentGtid.GetSeqServerIdNos(server.GetUniversalGtidServerID()) > crash.FailoverIOGtid.GetSeqServerIdNos(server.GetUniversalGtidServerID()) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoining node seq %d, master seq %d", server.CurrentGtid.GetSeqServerIdNos(server.GetUniversalGtidServerID()), crash.FailoverIOGtid.GetSeqServerIdNos(server.GetUniversalGtidServerID()))
			return true
		}
		return false
	} else {
		/*ss, errss := server.GetSlaveStatus(server.ReplicationSourceName)
		if errss != nil {
		 return	false
		}*/
		valid, logs, err := dbhelper.HaveExtraEvents(server.Conn, crash.FailoverMasterLogFile, crash.FailoverMasterLogPos)
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlDbg, "Failed to  get extra bin log events server %s, %s ", server.URL, err)
		if err != nil {
			return false
		}
		if valid {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "No extra events after  file %s, pos %d is equal ", crash.FailoverMasterLogFile, crash.FailoverMasterLogPos)
			return true
		}
		return false
	}
}

func (server *ServerMonitor) deletefiles(path string, f os.FileInfo, err error) (e error) {
	cluster := server.ClusterGroup
	// check each file if starts with the word "dumb_"
	if strings.HasPrefix(f.Name(), cluster.Name+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-") {
		os.Remove(path)
	}
	return
}

func (server *ServerMonitor) saveBinlog(crash *Crash) error {
	cluster := server.ClusterGroup
	t := time.Now()
	backupdir := cluster.Conf.WorkingDir + "/" + cluster.Name + "/crash-bin-" + t.Format("20060102150405")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rejoin old Master %s , backing up lost event to %s", crash.URL, backupdir)
	os.Mkdir(backupdir, 0777)
	os.Rename(cluster.Conf.WorkingDir+"/"+cluster.Name+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-"+crash.FailoverMasterLogFile, backupdir+"/"+cluster.Name+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-"+crash.FailoverMasterLogFile)
	return nil

}
func (server *ServerMonitor) backupBinlog(crash *Crash) error {
	cluster := server.ClusterGroup
	if _, err := os.Stat(cluster.GetMysqlBinlogPath()); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "mysqlbinlog does not exist %s check binary path", cluster.GetMysqlBinlogPath())
		return err
	}
	if _, err := os.Stat(cluster.Conf.WorkingDir); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "WorkingDir does not exist %s check param working-directory", cluster.Conf.WorkingDir)
		return err
	}
	var cmdrun *exec.Cmd
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Backup ahead binlog events of previously failed server %s", server.URL)
	filepath.Walk(cluster.Conf.WorkingDir+"/", server.deletefiles)

	cmdrun = exec.Command(cluster.GetMysqlBinlogPath(), "--read-from-remote-server", "--raw", "--stop-never-slave-server-id=10000", "--user="+cluster.GetRplUser(), "--password="+cluster.GetRplPass(), "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--result-file="+cluster.Conf.WorkingDir+"/"+cluster.Name+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-", "--start-position="+crash.FailoverMasterLogPos, crash.FailoverMasterLogFile)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Backup %s %s", cluster.GetMysqlBinlogPath(), strings.Replace(strings.Join(cmdrun.Args, " "), cluster.GetRplPass(), "XXXX", -1))

	var outrun bytes.Buffer
	cmdrun.Stdout = &outrun
	var outrunerr bytes.Buffer
	cmdrun.Stderr = &outrunerr

	cmdrunErr := cmdrun.Run()
	if cmdrunErr != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Failed to backup binlogs of %s,%s", server.URL, cmdrunErr.Error())
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s %s", cluster.GetMysqlBinlogPath(), cmdrun.Args)
		cluster.LogPrint(cmdrun.Stderr)
		cluster.LogPrint(cmdrun.Stdout)
		cluster.canFlashBack = false
		return cmdrunErr
	}
	return nil
}

func (cluster *Cluster) RejoinClone(source *ServerMonitor, dest *ServerMonitor) error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoining via master clone ")
	if dest.DBVersion.IsMySQL() && dest.DBVersion.Major >= 8 {
		if !dest.HasInstallPlugin("CLONE") {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Installing Clone plugin")
			dest.InstallPlugin("CLONE")
		}
		dest.ExecQueryNoBinLog("set global clone_valid_donor_list = '" + source.Host + ":" + source.Port + "'")
		dest.ExecQueryNoBinLog("CLONE INSTANCE FROM " + dest.User + "@" + source.Host + ":" + source.Port + " identified by '" + dest.Pass + "'")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Start slave after dump")
		dest.SetReplicationGTIDSlavePosFromServer(source)
		dest.StartSlave()
	} else {
		return errors.New("Version does not support cloning Master")
	}
	return nil
}

func (cluster *Cluster) RejoinFixRelay(slave *ServerMonitor, relay *ServerMonitor) error {
	if cluster.GetTopology() == topoMultiMasterRing || cluster.GetTopology() == topoMultiMasterWsrep {
		return nil
	}
	cluster.SetState("ERR00045", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00045"]), ErrFrom: "TOPO"})

	if slave.GetReplicationDelay() > cluster.Conf.FailMaxDelay {
		cluster.SetState("ERR00046", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00046"]), ErrFrom: "TOPO"})
		return nil
	} else {
		ss, err := slave.GetSlaveStatus(slave.ReplicationSourceName)
		if err == nil {
			slave.rejoinSlave(*ss)
		}
	}

	return nil
}

// UseGtid check is replication use gtid
func (server *ServerMonitor) UsedGtidAtElection(crash *Crash) bool {
	cluster := server.ClusterGroup
	/*
		ss, errss := server.GetSlaveStatus(server.ReplicationSourceName)
		if errss != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Failed to check if server was using GTID %s", errss)
			return false
		}


		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Rejoin server using GTID %s", ss.UsingGtid.String)
	*/
	if crash.FailoverIOGtid == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin server cannot find a saved master election GTID")
		return false
	}
	if len(crash.FailoverIOGtid.GetSeqNos()) > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin server found a crash GTID greater than 0 ")
		return true
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin server can not found a GTID greater than 0 ")
	return false

}
