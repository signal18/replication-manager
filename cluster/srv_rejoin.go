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
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

func (server *ServerMonitor) RejoinLoop() error {
	server.ClusterGroup.LogPrintf("INFO", "rejoin %s to the loop", server.URL)
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
	// Check if master exists in topology before rejoining.
	if server.ClusterGroup.sme.IsInFailover() {
		server.ClusterGroup.rejoinCond.Send <- true
		return nil
	}
	if server.ClusterGroup.Conf.LogLevel > 2 {
		server.ClusterGroup.LogPrintf("INFO", "Rejoining standalone server %s", server.URL)
	}
	// Strange here add comment for why
	server.ClusterGroup.canFlashBack = true

	if server.ClusterGroup.master != nil {
		if server.URL != server.ClusterGroup.master.URL {
			server.ClusterGroup.SetState("WARN0022", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0022"], server.URL, server.ClusterGroup.master.URL), ErrFrom: "REJOIN"})
			crash := server.ClusterGroup.getCrashFromJoiner(server.URL)
			if crash == nil {
				server.ClusterGroup.SetState("ERR00066", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00066"], server.URL, server.ClusterGroup.master.URL), ErrFrom: "REJOIN"})
				if server.ClusterGroup.oldMaster != nil {
					if server.ClusterGroup.oldMaster.URL == server.URL {
						server.RejoinMasterSST()
						server.ClusterGroup.rejoinCond.Send <- true
						return nil
					}
				}
				if server.ClusterGroup.Conf.Autoseed {
					server.ReseedMasterSST()
					server.ClusterGroup.rejoinCond.Send <- true
					return nil
				} else {
					server.ClusterGroup.rejoinCond.Send <- true
					server.ClusterGroup.LogPrintf("INFO", "No auto seeding %s", server.URL)
					return errors.New("No Autoseed")
				}
			} //crash info is available
			if server.ClusterGroup.Conf.AutorejoinBackupBinlog == true {
				server.backupBinlog(crash)
			}

			err := server.rejoinMasterIncremental(crash)
			if err != nil {
				server.ClusterGroup.LogPrintf("ERROR", "Failed to autojoin incremental to master %s", server.URL)
				err := server.RejoinMasterSST()
				if err != nil {
					server.ClusterGroup.LogPrintf("ERROR", "State transfer rejoin failed")
				}
			}
			if server.ClusterGroup.Conf.AutorejoinBackupBinlog == true {
				server.saveBinlog(crash)
			}

		}
	} else {
		//no master discovered
		if server.ClusterGroup.lastmaster != nil {
			if server.ClusterGroup.lastmaster.ServerID == server.ServerID {
				server.ClusterGroup.LogPrintf("INFO", "Rediscovering last seen master: %s", server.URL)
				server.ClusterGroup.master = server
				server.ClusterGroup.lastmaster = nil
			} else {
				if server.ClusterGroup.Conf.FailRestartUnsafe == false {
					server.ClusterGroup.LogPrintf("INFO", "Rediscovering last seen master: %s", server.URL)

					server.rejoinMasterAsSlave()

				}
			}
		} else {
			if server.ClusterGroup.Conf.FailRestartUnsafe == true {
				server.ClusterGroup.LogPrintf("INFO", "Restart Unsafe Picking first non-slave as master: %s", server.URL)
				server.ClusterGroup.master = server
			}
		}
		// if consul or internal proxy need to adapt read only route to new slaves
		server.ClusterGroup.backendStateChangeProxies()
	}
	server.ClusterGroup.rejoinCond.Send <- true
	return nil
}

func (server *ServerMonitor) RejoinPreviousSnapshot() error {
	_, err := server.JobZFSSnapBack()
	return err
}

func (server *ServerMonitor) RejoinMasterSST() error {
	if server.ClusterGroup.Conf.AutorejoinMysqldump == true {
		server.ClusterGroup.LogPrintf("INFO", "Rejoin flashback dump restore %s", server.URL)
		err := server.RejoinDirectDump()
		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR", "mysqldump flashback restore failed %s", err)
			return errors.New("Dump from master failed")
		}
	} else if server.ClusterGroup.Conf.AutorejoinLogicalBackup {
		server.JobFlashbackLogicalBackup()
	} else if server.ClusterGroup.Conf.AutorejoinPhysicalBackup {
		server.JobFlashbackPhysicalBackup()
	} else if server.ClusterGroup.Conf.AutorejoinZFSFlashback {
		server.RejoinPreviousSnapshot()
	} else if server.ClusterGroup.Conf.RejoinScript != "" {
		server.ClusterGroup.LogPrintf("INFO", "Calling rejoin flashback script")
		var out []byte
		out, err := exec.Command(server.ClusterGroup.Conf.RejoinScript, misc.Unbracket(server.Host), misc.Unbracket(server.ClusterGroup.master.Host)).CombinedOutput()
		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR", "%s", err)
		}
		server.ClusterGroup.LogPrintf("INFO", "Rejoin script complete %s", string(out))
	} else {
		server.ClusterGroup.LogPrintf("INFO", "No SST rejoin method found")
		return errors.New("No SST rejoin flashback method found")
	}

	return nil
}

func (server *ServerMonitor) ReseedMasterSST() error {
	server.DelWaitBackupCookie()
	if server.ClusterGroup.Conf.AutorejoinMysqldump == true {
		server.ClusterGroup.LogPrintf("INFO", "Rejoin dump restore %s", server.URL)
		err := server.RejoinDirectDump()
		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR", "mysqldump restore failed %s", err)
			return errors.New("Dump from master failed")
		}
	} else {
		if server.ClusterGroup.Conf.AutorejoinLogicalBackup {
			server.JobReseedLogicalBackup()
		} else if server.ClusterGroup.Conf.AutorejoinPhysicalBackup {
			server.JobReseedPhysicalBackup()
		} else if server.ClusterGroup.Conf.RejoinScript != "" {
			server.ClusterGroup.LogPrintf("INFO", "Calling rejoin script")
			var out []byte
			out, err := exec.Command(server.ClusterGroup.Conf.RejoinScript, misc.Unbracket(server.Host), misc.Unbracket(server.ClusterGroup.master.Host)).CombinedOutput()
			if err != nil {
				server.ClusterGroup.LogPrintf("ERROR", "%s", err)
			}
			server.ClusterGroup.LogPrintf("INFO", "Rejoin script complete %s", string(out))
		} else {
			server.ClusterGroup.LogPrintf("INFO", "No SST reseed method found")
			return errors.New("No SST reseed method found")
		}
	}

	return nil
}

func (server *ServerMonitor) rejoinMasterSync(crash *Crash) error {
	if server.HasGTIDReplication() {
		server.ClusterGroup.LogPrintf("INFO", "Found same or lower GTID %s and new elected master was %s", server.CurrentGtid.Sprint(), crash.FailoverIOGtid.Sprint())
	} else {
		server.ClusterGroup.LogPrintf("INFO", "Found same or lower sequence %s , %s", server.BinaryLogFile, server.BinaryLogPos)
	}
	var err error
	realmaster := server.ClusterGroup.master
	if server.ClusterGroup.Conf.MxsBinlogOn || server.ClusterGroup.Conf.MultiTierSlave {
		realmaster = server.ClusterGroup.GetRelayServer()
	}
	if server.HasGTIDReplication() || (realmaster.MxsHaveGtid && realmaster.IsMaxscale) {
		logs, err := server.SetReplicationGTIDCurrentPosFromServer(realmaster)
		server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed in GTID rejoin old master in sync %s, %s", server.URL, err)
		if err != nil {
			return err
		}
	} else if server.ClusterGroup.Conf.MxsBinlogOn {
		logs, err := dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      realmaster.Host,
			Port:      realmaster.Port,
			User:      server.ClusterGroup.rplUser,
			Password:  server.ClusterGroup.rplPass,
			Retry:     strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
			Mode:      "MXS",
			Logfile:   crash.FailoverMasterLogFile,
			Logpos:    crash.FailoverMasterLogPos,
			SSL:       server.ClusterGroup.Conf.ReplicationSSL,
		}, server.DBVersion)
		server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Change master positional failed in Rejoin old Master in sync to maxscale %s", err)
		if err != nil {
			return err
		}
	} else {
		// not maxscale the new master coordonate are in crash
		server.ClusterGroup.LogPrintf("INFO", "Change master to positional in Rejoin old Master")
		logs, err := dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:        realmaster.Host,
			Port:        realmaster.Port,
			User:        server.ClusterGroup.rplUser,
			Password:    server.ClusterGroup.rplPass,
			Retry:       strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
			Heartbeat:   strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
			Mode:        "POSITIONAL",
			Logfile:     crash.NewMasterLogFile,
			Logpos:      crash.NewMasterLogPos,
			SSL:         server.ClusterGroup.Conf.ReplicationSSL,
			Channel:     server.ClusterGroup.Conf.MasterConn,
			IsDelayed:   server.IsDelayed,
			Delay:       strconv.Itoa(server.ClusterGroup.Conf.HostsDelayedTime),
			PostgressDB: server.PostgressDB,
		}, server.DBVersion)
		server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Change master positional failed in Rejoin old Master in sync %s", err)
		if err != nil {
			return err
		}
	}

	server.StartSlave()
	return err
}

func (server *ServerMonitor) rejoinMasterFlashBack(crash *Crash) error {
	realmaster := server.ClusterGroup.master
	if server.ClusterGroup.Conf.MxsBinlogOn || server.ClusterGroup.Conf.MultiTierSlave {
		realmaster = server.ClusterGroup.GetRelayServer()
	}

	if _, err := os.Stat(server.ClusterGroup.GetMysqlBinlogPath()); os.IsNotExist(err) {
		server.ClusterGroup.LogPrintf("ERROR", "File does not exist %s", server.ClusterGroup.GetMysqlBinlogPath())
		return err
	}
	if _, err := os.Stat(server.ClusterGroup.GetMysqlclientPath()); os.IsNotExist(err) {
		server.ClusterGroup.LogPrintf("ERROR", "File does not exist %s", server.ClusterGroup.GetMysqlclientPath())
		return err
	}

	binlogCmd := exec.Command(server.ClusterGroup.GetMysqlBinlogPath(), "--flashback", "--to-last-log", server.ClusterGroup.Conf.WorkingDir+"/"+server.ClusterGroup.Name+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-"+crash.FailoverMasterLogFile)
	clientCmd := exec.Command(server.ClusterGroup.GetMysqlclientPath(), "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--user="+server.ClusterGroup.dbUser, "--password="+server.ClusterGroup.dbPass)
	server.ClusterGroup.LogPrintf("INFO", "FlashBack: %s %s", server.ClusterGroup.GetMysqlBinlogPath(), strings.Replace(strings.Join(binlogCmd.Args, " "), server.ClusterGroup.rplPass, "XXXX", -1))
	var err error
	clientCmd.Stdin, err = binlogCmd.StdoutPipe()
	if err != nil {
		server.ClusterGroup.LogPrintf("ERROR", "Error opening pipe: %s", err)
		return err
	}
	if err := binlogCmd.Start(); err != nil {
		server.ClusterGroup.LogPrintf("ERROR", "Failed mysqlbinlog command: %s at %s", err, strings.Replace(binlogCmd.Path, server.ClusterGroup.rplPass, "XXXX", -1))
		return err
	}
	if err := clientCmd.Run(); err != nil {
		server.ClusterGroup.LogPrintf("ERROR", "Error starting client: %s at %s", err, strings.Replace(clientCmd.Path, server.ClusterGroup.rplPass, "XXXX", -1))
		return err
	}
	logs, err := dbhelper.SetGTIDSlavePos(server.Conn, crash.FailoverIOGtid.Sprint())
	server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlInfo, "SET GLOBAL gtid_slave_pos = \"%s\"", crash.FailoverIOGtid.Sprint())
	if err != nil {
		return err
	}
	var err2 error
	if server.MxsHaveGtid || server.IsMaxscale == false {
		logs, err2 = server.SetReplicationGTIDSlavePosFromServer(realmaster)
	} else {
		logs, err2 = server.SetReplicationFromMaxsaleServer(realmaster)
	}
	server.ClusterGroup.LogSQL(logs, err2, server.URL, "Rejoin", LvlInfo, "Failed SetReplicationGTIDSlavePosFromServer on %s: %s", server.URL, err2)
	if err2 != nil {
		return err2
	}
	logs, err = server.StartSlave()
	server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlInfo, "Failed stop slave on %s: %s", server.URL, err)

	return nil
}

func (server *ServerMonitor) RejoinDirectDump() error {
	var err3 error

	realmaster := server.ClusterGroup.master
	if server.ClusterGroup.Conf.MxsBinlogOn || server.ClusterGroup.Conf.MultiTierSlave {
		realmaster = server.ClusterGroup.GetRelayServer()
	}

	if realmaster == nil {
		return errors.New("No master defined exiting rejoin direct dump ")
	}
	// done change master just to set the host and port before dump
	if server.MxsHaveGtid || server.IsMaxscale == false {
		logs, err3 := server.SetReplicationGTIDSlavePosFromServer(realmaster)
		server.ClusterGroup.LogSQL(logs, err3, server.URL, "Rejoin", LvlInfo, "Failed SetReplicationGTIDSlavePosFromServer on %s: %s", server.URL, err3)

	} else {
		logs, err3 := dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      realmaster.Host,
			Port:      realmaster.Port,
			User:      server.ClusterGroup.rplUser,
			Password:  server.ClusterGroup.rplPass,
			Retry:     strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
			Mode:      "MXS",
			Logfile:   realmaster.FailoverMasterLogFile,
			Logpos:    realmaster.FailoverMasterLogPos,
			SSL:       server.ClusterGroup.Conf.ReplicationSSL,
		}, server.DBVersion)
		server.ClusterGroup.LogSQL(logs, err3, server.URL, "Rejoin", LvlErr, "Failed change master maxscale on %s: %s", server.URL, err3)
	}
	if err3 != nil {
		return err3
	}
	// dump here
	backupserver := server.ClusterGroup.GetBackupServer()
	if backupserver == nil {
		go server.ClusterGroup.JobRejoinMysqldumpFromSource(server.ClusterGroup.master, server)
	} else {
		go server.ClusterGroup.JobRejoinMysqldumpFromSource(backupserver, server)
	}
	return nil
}

func (server *ServerMonitor) rejoinMasterIncremental(crash *Crash) error {
	server.ClusterGroup.LogPrintf("INFO", "Rejoin master incremental %s", server.URL)
	server.ClusterGroup.LogPrintf("INFO", "Crash info %s", crash)
	server.Refresh()
	if server.ClusterGroup.Conf.ReadOnly && !server.ClusterGroup.IsInIgnoredReadonly(server) {
		logs, err := server.SetReadOnly()
		server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed to set read only on server %s, %s ", server.URL, err)
	}

	if crash.FailoverIOGtid != nil {
		server.ClusterGroup.LogPrintf("INFO", "Rejoined GTID sequence %d", server.CurrentGtid.GetSeqServerIdNos(uint64(server.ServerID)))
		server.ClusterGroup.LogPrintf("INFO", "Crash Saved GTID sequence %d for master id %d", crash.FailoverIOGtid.GetSeqServerIdNos(uint64(server.ServerID)), uint64(server.ServerID))
	}
	if server.isReplicationAheadOfMasterElection(crash) == false || server.ClusterGroup.Conf.MxsBinlogOn {
		server.rejoinMasterSync(crash)
		return nil
	} else {
		// don't try flashback on old style replication that are ahead jump to SST
		if server.HasGTIDReplication() == false {
			server.ClusterGroup.LogPrintf("INFO", "Incremental canceled caused by old style replication")
			return errors.New("Incremental canceled caused by old style replication")
		}
	}
	if crash.FailoverIOGtid != nil {
		// server.ClusterGroup.master.FailoverIOGtid.GetSeqServerIdNos(uint64(server.ServerID)) == 0
		// lookup in crash recorded is the current master
		if crash.FailoverIOGtid.GetSeqServerIdNos(uint64(server.ServerID)) == 0 {
			server.ClusterGroup.LogPrintf("INFO", "Cascading failover, consider we cannot flashback")
			server.ClusterGroup.canFlashBack = false
		} else {
			server.ClusterGroup.LogPrintf("INFO", "Found server ID in rejoining ID %s and crash FailoverIOGtid %s Master %s", server.ServerID, crash.FailoverIOGtid.Sprint(), server.ClusterGroup.master.URL)
		}
	} else {
		server.ClusterGroup.LogPrintf("INFO", "Old server GTID for flashback not found")
	}
	if crash.FailoverIOGtid != nil && server.ClusterGroup.canFlashBack == true && server.ClusterGroup.Conf.AutorejoinFlashback == true && server.ClusterGroup.Conf.AutorejoinBackupBinlog == true {
		err := server.rejoinMasterFlashBack(crash)
		if err == nil {
			return nil
		}
		server.ClusterGroup.LogPrintf("ERROR", "Flashback rejoin failed: %s", err)
		return errors.New("Flashback failed")
	} else {
		server.ClusterGroup.LogPrintf("INFO", "No flashback rejoin can flashback %t, autorejoin-flashback %t autorejoin-backup-binlog %t", server.ClusterGroup.canFlashBack, server.ClusterGroup.Conf.AutorejoinFlashback, server.ClusterGroup.Conf.AutorejoinBackupBinlog)
		return errors.New("Flashback disabled")
	}

}

func (server *ServerMonitor) rejoinMasterAsSlave() error {
	realmaster := server.ClusterGroup.lastmaster
	server.ClusterGroup.LogPrintf("INFO", "Rejoining old master server %s to saved master %s", server.URL, realmaster.URL)
	logs, err := server.SetReadOnly()
	server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed to set read only on server %s, %s ", server.URL, err)
	if err == nil {
		logs, err = server.SetReplicationGTIDCurrentPosFromServer(realmaster)
		server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed to autojoin indirect master server %s, stopping slave as a precaution %s ", server.URL, err)
		if err == nil {
			logs, err = server.StartSlave()
			server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed to stop slave on erver %s, %s ", server.URL, err)
		} else {

			return err
		}
	} else {
		server.ClusterGroup.LogPrintf("ERROR", "Rejoin master as slave can't set read only %s", err)
		return err
	}
	return nil
}

func (server *ServerMonitor) rejoinSlave(ss dbhelper.SlaveStatus) error {
	// Test if slave not connected to current master
	if server.ClusterGroup.GetTopology() == topoMultiMasterRing || server.ClusterGroup.GetTopology() == topoMultiMasterWsrep {
		if server.ClusterGroup.GetTopology() == topoMultiMasterRing {
			server.RejoinLoop()
			server.ClusterGroup.rejoinCond.Send <- true
			return nil
		}
	}
	mycurrentmaster, _ := server.ClusterGroup.GetMasterFromReplication(server)
	if mycurrentmaster == nil {
		server.ClusterGroup.LogPrintf(LvlErr, "No master found from replication")
		server.ClusterGroup.rejoinCond.Send <- true
		return errors.New("No master found from replication")
	}
	if server.ClusterGroup.master != nil && mycurrentmaster != nil {
		if server.ClusterGroup.master.URL == mycurrentmaster.URL {
			server.ClusterGroup.LogPrintf("INFO", "Cancel rejoin, found same leader already from replication %s	", mycurrentmaster.URL)
			return errors.New("Same master found from replication")
		}
		//Found slave to rejoin
		server.ClusterGroup.SetState("ERR00067", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00067"], server.URL, server.PrevState, ss.SlaveIORunning.String, server.ClusterGroup.master.URL), ErrFrom: "REJOIN"})
		if server.ClusterGroup.master.IsDown() && server.ClusterGroup.Conf.FailRestartUnsafe == false {
			server.HaveNoMasterOnStart = true
		}
		if mycurrentmaster.IsMaxscale == false && server.ClusterGroup.Conf.MultiTierSlave == false && server.ClusterGroup.Conf.ReplicationNoRelay {

			if server.HasGTIDReplication() {
				crash := server.ClusterGroup.getCrashFromMaster(server.ClusterGroup.master.URL)
				if crash == nil {
					server.ClusterGroup.SetState("ERR00065", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00065"], server.URL, server.ClusterGroup.master.URL), ErrFrom: "REJOIN"})
					server.ClusterGroup.rejoinCond.Send <- true
					return errors.New("No Crash info on current master")
				}
				server.ClusterGroup.LogPrintf("INFO", "Crash info on current master %s", crash)
				server.ClusterGroup.LogPrintf("INFO", "Found slave to rejoin %s slave was previously in state %s replication io thread  %s, pointing currently to %s", server.URL, server.PrevState, ss.SlaveIORunning, server.ClusterGroup.master.URL)

				realmaster := server.ClusterGroup.master
				// A SLAVE IS ALWAY BEHIND MASTER
				//		slave_gtid := server.CurrentGtid.GetSeqServerIdNos(uint64(server.GetReplicationServerID()))
				//		master_gtid := crash.FailoverIOGtid.GetSeqServerIdNos(uint64(server.GetReplicationServerID()))
				//	if slave_gtid < master_gtid {
				server.ClusterGroup.LogPrintf("INFO", "Rejoining slave %s via GTID", server.URL)
				logs, err := server.StopSlave()
				server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed to stop slave server %s, stopping slave as a precaution %s", server.URL, err)
				if err == nil {
					logs, err := server.SetReplicationGTIDSlavePosFromServer(realmaster)
					server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed to autojoin indirect slave server %s, stopping slave as a precaution %s", server.URL, err)
					if err == nil {
						logs, err := server.StartSlave()
						server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed to start  slave server %s, stopping slave as a precaution %s", server.URL, err)
					}
				}
			} else {
				if mycurrentmaster.State != stateFailed && mycurrentmaster.IsRelay {
					// No GTID compatible solution stop relay master wait apply relay and move to real master
					logs, err := mycurrentmaster.StopSlave()
					server.ClusterGroup.LogSQL(logs, err, mycurrentmaster.URL, "Rejoin", LvlErr, "Failed to stop slave on relay server  %s: %s", mycurrentmaster.URL, err)
					if err == nil {
						logs, err2 := dbhelper.MasterPosWait(server.Conn, mycurrentmaster.BinaryLogFile, mycurrentmaster.BinaryLogPos, 3600)
						server.ClusterGroup.LogSQL(logs, err2, server.URL, "Rejoin", LvlErr, "Failed positional rejoin wait pos %s %s", server.URL, err2)
						if err2 == nil {
							myparentss, _ := mycurrentmaster.GetSlaveStatus(mycurrentmaster.ReplicationSourceName)

							logs, err := server.StopSlave()
							server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed to stop slave on server %s: %s", server.URL, err)
							server.ClusterGroup.LogPrintf("INFO", "Doing Positional switch of slave %s", server.URL)
							logs, changeMasterErr := dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
								Host:        server.ClusterGroup.master.Host,
								Port:        server.ClusterGroup.master.Port,
								User:        server.ClusterGroup.rplUser,
								Password:    server.ClusterGroup.rplPass,
								Logfile:     myparentss.MasterLogFile.String,
								Logpos:      myparentss.ReadMasterLogPos.String,
								Retry:       strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatRetry),
								Heartbeat:   strconv.Itoa(server.ClusterGroup.Conf.ForceSlaveHeartbeatTime),
								Channel:     server.ClusterGroup.Conf.MasterConn,
								IsDelayed:   server.IsDelayed,
								Delay:       strconv.Itoa(server.ClusterGroup.Conf.HostsDelayedTime),
								SSL:         server.ClusterGroup.Conf.ReplicationSSL,
								PostgressDB: server.PostgressDB,
							}, server.DBVersion)

							server.ClusterGroup.LogSQL(logs, changeMasterErr, server.URL, "Rejoin", LvlErr, "Rejoin Failed doing Positional switch of slave %s: %s", server.URL, changeMasterErr)

						}
						logs, err = server.StartSlave()
						server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed to start slave on %s: %s", server.URL, err)

					}
					mycurrentmaster.StartSlave()
					server.ClusterGroup.LogSQL(logs, err, mycurrentmaster.URL, "Rejoin", LvlErr, "Failed to start slave on %s: %s", mycurrentmaster.URL, err)

					if server.IsMaintenance {
						server.SwitchMaintenance()
					}
					// if consul or internal proxy need to adapt read only route to new slaves
					server.ClusterGroup.backendStateChangeProxies()

				} else {
					//Adding state waiting for old master to rejoin in positional mode
					// this state prevent crash info to be removed
					server.ClusterGroup.sme.AddState("ERR00049", state.State{ErrType: "ERRRO", ErrDesc: fmt.Sprintf(clusterError["ERR00049"]), ErrFrom: "TOPO"})
				}
			}
		}
	}
	// In case of state change, reintroduce the server in the slave list
	if server.PrevState == stateFailed || server.PrevState == stateUnconn || server.PrevState == stateSuspect {
		server.ClusterGroup.LogPrintf(LvlInfo, "Set stateSlave from rejoin slave %s", server.URL)
		server.SetState(stateSlave)
		server.FailCount = 0
		if server.PrevState != stateSuspect {
			server.ClusterGroup.slaves = append(server.ClusterGroup.slaves, server)
		}
		if server.ClusterGroup.Conf.ReadOnly {
			logs, err := dbhelper.SetReadOnly(server.Conn, true)
			server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlErr, "Failed to set read only on server %s, %s ", server.URL, err)
			if err != nil {
				server.ClusterGroup.rejoinCond.Send <- true
				return err
			}
		}
	}
	server.ClusterGroup.rejoinCond.Send <- true
	return nil
}

func (server *ServerMonitor) isReplicationAheadOfMasterElection(crash *Crash) bool {

	if server.UsedGtidAtElection(crash) {

		// CurrentGtid fetch from show global variables GTID_CURRENT_POS
		// FailoverIOGtid is fetch at failover from show slave status of the new master
		// If server-id can't be found in FailoverIOGtid can state cascading master failover
		if crash.FailoverIOGtid.GetSeqServerIdNos(uint64(server.ServerID)) == 0 {
			server.ClusterGroup.LogPrintf("INFO", "Cascading failover, found empty GTID, forcing full state transfer")
			return true
		}
		if server.CurrentGtid.GetSeqServerIdNos(uint64(server.ServerID)) > crash.FailoverIOGtid.GetSeqServerIdNos(uint64(server.ServerID)) {
			server.ClusterGroup.LogPrintf("INFO", "Rejoining node seq %d, master seq %d", server.CurrentGtid.GetSeqServerIdNos(uint64(server.ServerID)), crash.FailoverIOGtid.GetSeqServerIdNos(uint64(server.ServerID)))
			return true
		}
		return false
	} else {
		/*ss, errss := server.GetSlaveStatus(server.ReplicationSourceName)
		if errss != nil {
		 return	false
		}*/
		valid, logs, err := dbhelper.HaveExtraEvents(server.Conn, crash.FailoverMasterLogFile, crash.FailoverMasterLogPos)
		server.ClusterGroup.LogSQL(logs, err, server.URL, "Rejoin", LvlDbg, "Failed to  get extra bin log events server %s, %s ", server.URL, err)
		if err != nil {
			return false
		}
		if valid {
			server.ClusterGroup.LogPrintf("INFO", "No extra events after  file %s, pos %d is equal ", crash.FailoverMasterLogFile, crash.FailoverMasterLogPos)
			return true
		}
		return false
	}
}

func (server *ServerMonitor) deletefiles(path string, f os.FileInfo, err error) (e error) {

	// check each file if starts with the word "dumb_"
	if strings.HasPrefix(f.Name(), server.ClusterGroup.Name+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-") {
		os.Remove(path)
	}
	return
}

func (server *ServerMonitor) saveBinlog(crash *Crash) error {
	t := time.Now()
	backupdir := server.ClusterGroup.Conf.WorkingDir + "/" + server.ClusterGroup.Name + "/crash-bin-" + t.Format("20060102150405")
	server.ClusterGroup.LogPrintf("INFO", "Rejoin old Master %s , backing up lost event to %s", crash.URL, backupdir)
	os.Mkdir(backupdir, 0777)
	os.Rename(server.ClusterGroup.Conf.WorkingDir+"/"+server.ClusterGroup.Name+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-"+crash.FailoverMasterLogFile, backupdir+"/"+server.ClusterGroup.Name+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-"+crash.FailoverMasterLogFile)
	return nil

}
func (server *ServerMonitor) backupBinlog(crash *Crash) error {

	if _, err := os.Stat(server.ClusterGroup.GetMysqlBinlogPath()); os.IsNotExist(err) {
		server.ClusterGroup.LogPrintf("ERROR", "mysqlbinlog does not exist %s check binary path", server.ClusterGroup.GetMysqlBinlogPath())
		return err
	}
	if _, err := os.Stat(server.ClusterGroup.Conf.WorkingDir); os.IsNotExist(err) {
		server.ClusterGroup.LogPrintf("ERROR", "WorkingDir does not exist %s check param working-directory", server.ClusterGroup.Conf.WorkingDir)
		return err
	}
	var cmdrun *exec.Cmd
	server.ClusterGroup.LogPrintf("INFO", "Backup ahead binlog events of previously failed server %s", server.URL)
	filepath.Walk(server.ClusterGroup.Conf.WorkingDir+"/", server.deletefiles)

	cmdrun = exec.Command(server.ClusterGroup.GetMysqlBinlogPath(), "--read-from-remote-server", "--raw", "--stop-never-slave-server-id=10000", "--user="+server.ClusterGroup.rplUser, "--password="+server.ClusterGroup.rplPass, "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--result-file="+server.ClusterGroup.Conf.WorkingDir+"/"+server.ClusterGroup.Name+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-", "--start-position="+crash.FailoverMasterLogPos, crash.FailoverMasterLogFile)
	server.ClusterGroup.LogPrintf("INFO", "Backup %s %s", server.ClusterGroup.GetMysqlBinlogPath(), strings.Replace(strings.Join(cmdrun.Args, " "), server.ClusterGroup.rplPass, "XXXX", -1))

	var outrun bytes.Buffer
	cmdrun.Stdout = &outrun
	var outrunerr bytes.Buffer
	cmdrun.Stderr = &outrunerr

	cmdrunErr := cmdrun.Run()
	if cmdrunErr != nil {
		server.ClusterGroup.LogPrintf("ERROR", "Failed to backup binlogs of %s,%s", server.URL, cmdrunErr.Error())
		server.ClusterGroup.LogPrintf("ERROR", "%s %s", server.ClusterGroup.GetMysqlBinlogPath(), cmdrun.Args)
		server.ClusterGroup.LogPrint(cmdrun.Stderr)
		server.ClusterGroup.LogPrint(cmdrun.Stdout)
		server.ClusterGroup.canFlashBack = false
		return cmdrunErr
	}
	return nil
}

func (cluster *Cluster) RejoinClone(source *ServerMonitor, dest *ServerMonitor) error {
	cluster.LogPrintf(LvlInfo, "Rejoining via master clone ")
	if dest.DBVersion.IsMySQL() && dest.DBVersion.Major >= 8 {
		if !dest.HasInstallPlugin("CLONE") {
			cluster.LogPrintf(LvlInfo, "Installing Clone plugin")
			dest.InstallPlugin("CLONE")
		}
		dest.ExecQueryNoBinLog("set global clone_valid_donor_list = '" + source.Host + ":" + source.Port + "'")
		dest.ExecQueryNoBinLog("CLONE INSTANCE FROM " + dest.User + "@" + source.Host + ":" + source.Port + " identified by '" + dest.Pass + "'")
		cluster.LogPrintf(LvlInfo, "Start slave after dump")
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
	cluster.sme.AddState("ERR00045", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00045"]), ErrFrom: "TOPO"})

	if slave.GetReplicationDelay() > cluster.Conf.FailMaxDelay {
		cluster.sme.AddState("ERR00046", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00046"]), ErrFrom: "TOPO"})
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
	/*
		ss, errss := server.GetSlaveStatus(server.ReplicationSourceName)
		if errss != nil {
			server.ClusterGroup.LogPrintf(LvlInfo, "Failed to check if server was using GTID %s", errss)
			return false
		}


		server.ClusterGroup.LogPrintf(LvlInfo, "Rejoin server using GTID %s", ss.UsingGtid.String)
	*/
	if crash.FailoverIOGtid == nil {
		server.ClusterGroup.LogPrintf(LvlInfo, "Rejoin server cannot find a saved master election GTID")
		return false
	}
	if len(crash.FailoverIOGtid.GetSeqNos()) > 0 {
		server.ClusterGroup.LogPrintf(LvlInfo, "Rejoin server found a crash GTID greater than 0 ")
		return true
	}
	server.ClusterGroup.LogPrintf(LvlInfo, "Rejoin server can not found a GTID greater than 0 ")
	return false

}
