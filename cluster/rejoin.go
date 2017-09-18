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
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/dbhelper"
)

// RejoinMaster a server that just show up without slave status
func (server *ServerMonitor) RejoinMaster() error {
	// Check if master exists in topology before rejoining.
	server.ClusterGroup.LogPrintf("INFO", "Trying to rejoin restarted server %s", server.URL)
	server.ClusterGroup.canFlashBack = true
	if server.ClusterGroup.master != nil {
		if server.URL != server.ClusterGroup.master.URL {
			server.ClusterGroup.LogPrintf("INFO", "Rejoining failed server %s to master %s", server.URL, server.ClusterGroup.master.URL)
			crash := server.ClusterGroup.getCrash(server.URL)
			if crash == nil {
				server.ClusterGroup.LogPrintf("INFO", "Rejoin found no crash infos, promoting full state transfer %s", server.URL)
				server.RejoinMasterSST()
				return errors.New("No crash")
			}
			if server.ClusterGroup.conf.AutorejoinBackupBinlog == true {
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
			crash.delete(&server.ClusterGroup.crashes)
			server.ClusterGroup.Save()
			server.ClusterGroup.rejoinCond.Send <- true
		}
	} else {
		//no master discovered
		if server.ClusterGroup.lastmaster != nil {
			if server.ClusterGroup.lastmaster.ServerID == server.ServerID {
				server.ClusterGroup.LogPrintf("INFO", "Rediscovering last seen master: %s", server.URL)
				server.ClusterGroup.master = server
				server.ClusterGroup.lastmaster = nil
			} else {
				if server.ClusterGroup.conf.FailRestartUnsafe == false {
					server.ClusterGroup.LogPrintf("INFO", "Rediscovering last seen master: %s", server.URL)

					server.rejoinMasterAsSlave()

				}
			}
		} else {
			if server.ClusterGroup.conf.FailRestartUnsafe == true {
				server.ClusterGroup.LogPrintf("INFO", "Restart Unsafe Picking first non-slave as master: %s", server.URL)
				server.ClusterGroup.master = server
			}
		}
	}
	return nil
}

func (server *ServerMonitor) RejoinMasterSST() error {
	if server.ClusterGroup.conf.AutorejoinMysqldump == true {
		server.ClusterGroup.LogPrintf("INFO", "Rejoin dump restore %s", server.URL)
		err := server.rejoinMasterDump()
		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR", "mysqldump restore failed %s", err)
		}
	} else {
		server.ClusterGroup.LogPrintf("INFO", "No mysqldump rejoin: binlog capture failed or wrong version %t , autorejoin-mysqldump %t", server.ClusterGroup.canFlashBack, server.ClusterGroup.conf.AutorejoinMysqldump)
		server.ClusterGroup.LogPrintf("INFO", "No rejoin method found, old master says: leave me alone, I'm ahead")
	}
	if server.ClusterGroup.conf.RejoinScript != "" {
		server.ClusterGroup.LogPrintf("INFO", "Calling rejoin script")
		var out []byte
		out, err := exec.Command(server.ClusterGroup.conf.RejoinScript, server.Host, server.ClusterGroup.master.Host).CombinedOutput()
		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR", "%s", err)
		}
		server.ClusterGroup.LogPrintf("INFO", "Rejoin script complete %s", string(out))
	}
	return nil
}

func (server *ServerMonitor) rejoinMasterSync(crash *Crash) error {
	server.ClusterGroup.LogPrintf("INFO", "Found same or lower GTID %s and new elected master was %s", server.CurrentGtid.Sprint(), crash.FailoverIOGtid.Sprint())
	var err error
	realmaster := server.ClusterGroup.master
	if server.ClusterGroup.conf.MxsBinlogOn || server.ClusterGroup.conf.MultiTierSlave {
		realmaster = server.ClusterGroup.GetRelayServer()
	}
	if realmaster.MxsHaveGtid || realmaster.IsMaxscale == false {
		err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      realmaster.Host,
			Port:      realmaster.Port,
			User:      server.ClusterGroup.rplUser,
			Password:  server.ClusterGroup.rplPass,
			Retry:     strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatTime),
			Mode:      "CURRENT_POS",
		})
	} else {
		err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      realmaster.Host,
			Port:      realmaster.Port,
			User:      server.ClusterGroup.rplUser,
			Password:  server.ClusterGroup.rplPass,
			Retry:     strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatTime),
			Mode:      "MXS",
			Logfile:   crash.FailoverMasterLogFile,
			Logpos:    crash.FailoverMasterLogPos,
		})
	}
	dbhelper.StartSlave(server.Conn)
	return err

}

func (server *ServerMonitor) rejoinMasterFashBack(crash *Crash) error {
	realmaster := server.ClusterGroup.master
	if server.ClusterGroup.conf.MxsBinlogOn || server.ClusterGroup.conf.MultiTierSlave {
		realmaster = server.ClusterGroup.GetRelayServer()
	}

	// Flashback here
	if _, err := os.Stat(server.ClusterGroup.conf.ShareDir + "/" + server.ClusterGroup.conf.GoArch + "/" + server.ClusterGroup.conf.GoOS + "/mysqlbinlog"); os.IsNotExist(err) {
		server.ClusterGroup.LogPrintf("ERROR", "File does not exist %s", server.ClusterGroup.conf.ShareDir+"/"+server.ClusterGroup.conf.GoArch+"/"+server.ClusterGroup.conf.GoOS+"/mysqlbinlog")
		return err
	}

	binlogCmd := exec.Command(server.ClusterGroup.conf.ShareDir+"/"+server.ClusterGroup.conf.GoArch+"/"+server.ClusterGroup.conf.GoOS+"/mysqlbinlog", "--flashback", "--to-last-log", server.ClusterGroup.conf.WorkingDir+"/"+server.ClusterGroup.cfgGroup+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-"+crash.FailoverMasterLogFile)
	clientCmd := exec.Command(server.ClusterGroup.conf.ShareDir+"/"+server.ClusterGroup.conf.GoArch+"/"+server.ClusterGroup.conf.GoOS+"/mysql", "--host="+server.Host, "--port="+server.Port, "--user="+server.ClusterGroup.dbUser, "--password="+server.ClusterGroup.dbPass)
	server.ClusterGroup.LogPrintf("INFO", "FlashBack: %s %s", server.ClusterGroup.conf.ShareDir+"/"+server.ClusterGroup.conf.GoArch+"/"+server.ClusterGroup.conf.GoOS+"/mysqlbinlog", binlogCmd.Args)
	var err error
	clientCmd.Stdin, err = binlogCmd.StdoutPipe()
	if err != nil {
		server.ClusterGroup.LogPrintf("ERROR", "Error opening pipe: %s", err)
		return err
	}
	if err := binlogCmd.Start(); err != nil {
		server.ClusterGroup.LogPrintf("ERROR", "Failed mysqlbinlog command: %s at %s", err, binlogCmd.Path)
		return err
	}
	if err := clientCmd.Run(); err != nil {
		server.ClusterGroup.LogPrintf("ERROR", "Error starting client: %s at %s", err, clientCmd.Path)
		return err
	}
	server.ClusterGroup.LogPrintf("INFO", "SET GLOBAL gtid_slave_pos = \"%s\"", crash.FailoverIOGtid.Sprint())
	_, err = server.Conn.Exec("SET GLOBAL gtid_slave_pos = \"" + crash.FailoverIOGtid.Sprint() + "\"")
	if err != nil {
		return err
	}
	var err2 error
	if server.MxsHaveGtid || server.IsMaxscale == false {
		err2 = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      realmaster.Host,
			Port:      realmaster.Port,
			User:      server.ClusterGroup.rplUser,
			Password:  server.ClusterGroup.rplPass,
			Retry:     strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatTime),
			Mode:      "SLAVE_POS",
		})
	} else {
		err2 = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      realmaster.Host,
			Port:      realmaster.Port,
			User:      server.ClusterGroup.rplUser,
			Password:  server.ClusterGroup.rplPass,
			Retry:     strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatTime),
			Mode:      "MXS",
			Logfile:   realmaster.FailoverMasterLogFile,
			Logpos:    realmaster.FailoverMasterLogPos,
		})
	}
	if err2 != nil {
		return err2
	}
	dbhelper.StartSlave(server.Conn)
	if crash.FailoverSemiSyncSlaveStatus == true {
		server.ClusterGroup.LogPrintf("INFO", "New Master %s was in sync before failover safe flashback, no lost committed events", crash.URL)
	} else {
		server.saveBinlog(crash)
	}
	return nil
}

func (server *ServerMonitor) rejoinMasterDump() error {
	var err3 error
	realmaster := server.ClusterGroup.master
	if server.ClusterGroup.conf.MxsBinlogOn || server.ClusterGroup.conf.MultiTierSlave {
		realmaster = server.ClusterGroup.GetRelayServer()
	}
	// done change master just to set the host and port before dump
	if server.MxsHaveGtid || server.IsMaxscale == false {
		err3 = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      realmaster.Host,
			Port:      realmaster.Port,
			User:      server.ClusterGroup.rplUser,
			Password:  server.ClusterGroup.rplPass,
			Retry:     strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatTime),
			Mode:      "SLAVE_POS",
		})
	} else {
		err3 = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      realmaster.Host,
			Port:      realmaster.Port,
			User:      server.ClusterGroup.rplUser,
			Password:  server.ClusterGroup.rplPass,
			Retry:     strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatTime),
			Mode:      "MXS",
			Logfile:   realmaster.FailoverMasterLogFile,
			Logpos:    realmaster.FailoverMasterLogPos,
		})
	}
	if err3 != nil {
		return err3
	}
	// dump here
	err3 = server.ClusterGroup.RejoinMysqldump(server.ClusterGroup.master, server)
	if err3 != nil {
		return err3
	}
	return nil
}

func (server *ServerMonitor) rejoinMasterIncremental(crash *Crash) error {

	server.ClusterGroup.LogPrintf("INFO", "Rejoin master incremental %s", server.URL)
	server.ClusterGroup.LogPrintf("INFO", "Crash info %s", crash)

	server.Refresh()
	if server.ClusterGroup.conf.ReadOnly {
		dbhelper.SetReadOnly(server.Conn, true)
		server.ClusterGroup.LogPrintf("INFO", "Setting Read Only on rejoined %s", server.URL)
	}

	if crash.FailoverIOGtid != nil {
		server.ClusterGroup.LogPrintf("INFO", "Rejoined GTID sequence %d", server.CurrentGtid.GetSeqServerIdNos(uint64(server.ServerID)))
		server.ClusterGroup.LogPrintf("INFO", "Crash Saved GTID sequence %d for master id %d", crash.FailoverIOGtid.GetSeqServerIdNos(uint64(server.ServerID)), uint64(server.ServerID))

	}
	if server.isReplicationAheadOfMasterElection(crash) == false || server.ClusterGroup.conf.MxsBinlogOn {
		server.rejoinMasterSync(crash)
		return nil
	}
	if crash.FailoverIOGtid != nil {
		// server.ClusterGroup.master.FailoverIOGtid.GetSeqServerIdNos(uint64(server.ServerID)) == 0
		// lookup in crash recorded is the current master
		if crash.FailoverIOGtid.GetSeqServerIdNos(uint64(server.ClusterGroup.master.ServerID)) == 0 {
			server.ClusterGroup.LogPrintf("INFO", "Cascading failover, consider we cannot flashback")
			server.ClusterGroup.canFlashBack = false
		} else {
			server.ClusterGroup.LogPrintf("INFO", "Found server ID in rejoining ID %s and crash FailoverIOGtid %s Master %s", server.ServerID, crash.FailoverIOGtid.Sprint(), server.ClusterGroup.master.URL)
		}
	} else {
		server.ClusterGroup.LogPrintf("INFO", "Old server GTID for flashback not found")
	}
	if crash.FailoverIOGtid != nil && server.ClusterGroup.canFlashBack == true && server.ClusterGroup.conf.AutorejoinFlashback == true && server.ClusterGroup.conf.AutorejoinBackupBinlog == true {
		err := server.rejoinMasterFashBack(crash)
		if err == nil {
			return nil
		}
		server.ClusterGroup.LogPrintf("ERROR", "Flashback rejoin failed: %s", err)
		return errors.New("Flashback failed")
	} else {
		server.ClusterGroup.LogPrintf("INFO", "No flashback rejoin can flashback %t, autorejoin-flashback %t autorejoin-backup-binlog %t", server.ClusterGroup.canFlashBack, server.ClusterGroup.conf.AutorejoinFlashback, server.ClusterGroup.conf.AutorejoinBackupBinlog)
		return errors.New("Flashback disabled")
	}
}

func (server *ServerMonitor) rejoinMasterAsSlave() error {
	realmaster := server.ClusterGroup.lastmaster

	server.ClusterGroup.LogPrintf("INFO", "Rejoining old master server %s to saved master %s", server.URL, realmaster.URL)
	err := dbhelper.SetReadOnly(server.Conn, true)
	if err == nil {
		err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      realmaster.Host,
			Port:      realmaster.Port,
			User:      server.ClusterGroup.rplUser,
			Password:  server.ClusterGroup.rplPass,
			Retry:     strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatTime),
			Mode:      "CURRENT_POS",
		})
		if err == nil {
			dbhelper.StartSlave(server.Conn)
		} else {
			server.ClusterGroup.LogPrintf("ERROR", "Failed to autojoin indirect master server %s, stopping slave as a precaution %s ", server.URL, err)
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
	mycurrentmaster, _ := server.ClusterGroup.GetMasterFromReplication(server)

	if mycurrentmaster != nil {

		if server.ClusterGroup.master != nil {

			if server.ClusterGroup.master.DSN != mycurrentmaster.DSN {
				server.ClusterGroup.LogPrintf("INFO", "Found slave to rejoin %s slave was previously in %s replication io thread is %s, pointing currently to %s", server.URL, server.PrevState, ss.Slave_IO_Running, mycurrentmaster.DSN)

				if mycurrentmaster.State != stateFailed && mycurrentmaster.IsRelay == false && server.ClusterGroup.conf.MultiTierSlave == false {
					realmaster := server.ClusterGroup.master
					slave_gtid := server.CurrentGtid.GetSeqServerIdNos(uint64(server.MasterServerID))
					master_gtid := realmaster.FailoverIOGtid.GetSeqServerIdNos(uint64(server.MasterServerID))
					if slave_gtid < master_gtid {
						server.ClusterGroup.LogPrintf("INFO", "Rejoining slave server %s to master %s", server.URL, realmaster.URL)
						err := dbhelper.StopSlave(server.Conn)
						if err == nil {
							err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
								Host:      realmaster.Host,
								Port:      realmaster.Port,
								User:      server.ClusterGroup.rplUser,
								Password:  server.ClusterGroup.rplPass,
								Retry:     strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
								Heartbeat: strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatTime),
								Mode:      "SLAVE_POS",
							})
							if err == nil {
								dbhelper.StartSlave(server.Conn)
							} else {
								server.ClusterGroup.LogPrintf("ERROR", "Failed to autojoin indirect slave server %s, stopping slave as a precaution %s", server.URL, err)
							}
						} else {
							server.ClusterGroup.LogPrintf("ERROR", "Can't stop slave in rejoin slave %s", err)
						}

					} else if server.ClusterGroup.conf.LogLevel > 2 && slave_gtid < master_gtid {
						server.ClusterGroup.LogPrintf("DEBUG", "Slave server %s (%d) is ahead of master %s (%d)", server.URL, slave_gtid, realmaster.URL, master_gtid)
					}
				}
			}

		} else {
			server.ClusterGroup.LogPrintf("ERROR", "Slave wants to rejoin non discovered master")
		}
	} // end mycurrentmaster !=nil

	// In case of state change, reintroduce the server in the slave list
	if server.PrevState == stateFailed || server.PrevState == stateUnconn || server.PrevState == stateSuspect {
		server.State = stateSlave
		server.FailCount = 0
		if server.PrevState != stateSuspect {
			server.ClusterGroup.slaves = append(server.ClusterGroup.slaves, server)
		}
		if server.ClusterGroup.conf.ReadOnly {
			err := dbhelper.SetReadOnly(server.Conn, true)
			if err != nil {
				server.ClusterGroup.LogPrintf("ERROR", "Could not set rejoining slave %s as read-only, %s", server.URL, err)
				return err
			}
		}
	}
	return nil
}

// UseGtid  check is replication use gtid
func (server *ServerMonitor) UsedGtidAtElection(crash *Crash) bool {
	if server.ClusterGroup.conf.LogLevel > 1 {
		server.ClusterGroup.LogPrintf("DEBUG", "Rejoin Server use GTID %s", server.UsingGtid)
	}
	// An old master  master do no have replication
	if crash.FailoverIOGtid == nil {
		server.ClusterGroup.LogPrintf("DEBUG", "Rejoin server cannot find a saved master election GTID")
		return false
	}
	if len(crash.FailoverIOGtid.GetSeqNos()) > 0 {
		return true
	} else {
		return false
	}
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
		if crash.FailoverMasterLogFile == server.MasterLogFile && server.MasterLogPos == crash.FailoverMasterLogPos {
			return false
		}
		return true
	}
}

func (server *ServerMonitor) deletefiles(path string, f os.FileInfo, err error) (e error) {

	// check each file if starts with the word "dumb_"
	if strings.HasPrefix(f.Name(), server.ClusterGroup.cfgGroup+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-") {
		os.Remove(path)
	}
	return
}

func (server *ServerMonitor) saveBinlog(crash *Crash) error {
	t := time.Now()
	backupdir := server.ClusterGroup.conf.WorkingDir + "/crash" + t.Format("20060102150405")
	server.ClusterGroup.LogPrintf("INFO", "New Master %s was not synced before failover, unsafe flashback, lost events backing up event to %s", crash.URL, backupdir)
	os.Mkdir(backupdir, 0777)
	os.Rename(server.ClusterGroup.conf.WorkingDir+"/"+server.ClusterGroup.cfgGroup+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-"+crash.FailoverMasterLogFile, backupdir+"/"+server.ClusterGroup.cfgGroup+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-"+crash.FailoverMasterLogFile)
	return nil

}
func (server *ServerMonitor) backupBinlog(crash *Crash) error {

	if _, err := os.Stat(server.ClusterGroup.conf.ShareDir + "/" + server.ClusterGroup.conf.GoArch + "/" + server.ClusterGroup.conf.GoOS + "/mysqlbinlog"); os.IsNotExist(err) {
		server.ClusterGroup.LogPrintf("ERROR", "Backup Binlog File does not exist %s check binary path", server.ClusterGroup.conf.ShareDir+"/"+server.ClusterGroup.conf.GoArch+"/"+server.ClusterGroup.conf.GoOS+"/mysqlbinlog")
		return err
	}
	if _, err := os.Stat(server.ClusterGroup.conf.WorkingDir); os.IsNotExist(err) {
		server.ClusterGroup.LogPrintf("ERROR", "WorkingDir does not exist %s check param working-directory", server.ClusterGroup.conf.WorkingDir)
		return err
	}
	var cmdrun *exec.Cmd
	server.ClusterGroup.LogPrintf("INFO", "Backup ahead binlog events of previously failed server %s", server.URL)
	filepath.Walk(server.ClusterGroup.conf.WorkingDir+"/", server.deletefiles)

	cmdrun = exec.Command(server.ClusterGroup.conf.ShareDir+"/"+server.ClusterGroup.conf.GoArch+"/"+server.ClusterGroup.conf.GoOS+"/mysqlbinlog", "--read-from-remote-server", "--raw", "--stop-never-slave-server-id=10000", "--user="+server.ClusterGroup.rplUser, "--password="+server.ClusterGroup.rplPass, "--host="+server.Host, "--port="+server.Port, "--result-file="+server.ClusterGroup.conf.WorkingDir+"/"+server.ClusterGroup.cfgGroup+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-", "--start-position="+crash.FailoverMasterLogPos, crash.FailoverMasterLogFile)
	server.ClusterGroup.LogPrintf("INFO", "Backup %s %s", server.ClusterGroup.conf.ShareDir+"/"+server.ClusterGroup.conf.GoArch+"/"+server.ClusterGroup.conf.GoOS+"/mysqlbinlog", cmdrun.Args)

	var outrun bytes.Buffer
	cmdrun.Stdout = &outrun
	var outrunerr bytes.Buffer
	cmdrun.Stderr = &outrunerr

	cmdrunErr := cmdrun.Run()
	if cmdrunErr != nil {
		server.ClusterGroup.LogPrintf("ERROR", "Failed to backup binlogs of %s,%s", server.URL, cmdrunErr.Error())
		server.ClusterGroup.LogPrintf("ERROR", "%s %s", server.ClusterGroup.conf.ShareDir+"/"+server.ClusterGroup.conf.GoArch+"/"+server.ClusterGroup.conf.GoOS+"/mysqlbinlog ", cmdrun.Args)
		server.ClusterGroup.LogPrint(cmdrun.Stderr)
		server.ClusterGroup.LogPrint(cmdrun.Stdout)
		server.ClusterGroup.canFlashBack = false
		return cmdrunErr
	}
	return nil
}

func (cluster *Cluster) RejoinMysqldump(source *ServerMonitor, dest *ServerMonitor) error {
	cluster.LogPrintf("INFO", "Rejoining via Dump Master")
	dumpCmd := exec.Command(cluster.conf.ShareDir+"/"+cluster.conf.GoArch+"/"+cluster.conf.GoOS+"/mysqldump", "--opt", "--hex-blob", "--events", "--disable-keys", "--apply-slave-statements", "--gtid", "--single-transaction", "--all-databases", "--host="+source.Host, "--port="+source.Port, "--user="+cluster.dbUser, "--password="+cluster.dbPass)
	clientCmd := exec.Command(cluster.conf.ShareDir+"/"+cluster.conf.GoArch+"/"+cluster.conf.GoOS+"/mysql", "--host="+dest.Host, "--port="+dest.Port, "--user="+cluster.dbUser, "--password="+cluster.dbPass)
	//disableBinlogCmd := exec.Command("echo", "\"set sql_bin_log=0;\"")
	var err error
	clientCmd.Stdin, err = dumpCmd.StdoutPipe()
	if err != nil {
		cluster.LogPrintf("ERROR", "Failed opening pipe: %s", err)
		return err
	}
	if err := dumpCmd.Start(); err != nil {
		cluster.LogPrintf("ERROR", "Failed mysqldump command: %s at %s", err, dumpCmd.Path)
		return err
	}
	if err := clientCmd.Run(); err != nil {
		cluster.LogPrintf("ERROR", "Can't start mysql client:%s at %s", err, clientCmd.Path)
		return err
	}
	dumpCmd.Wait()
	cluster.LogPrintf("INFO", "Start slave after dump")

	dbhelper.StartSlave(dest.Conn)
	return nil
}
