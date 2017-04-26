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

	"github.com/tanji/replication-manager/dbhelper"
)

// Rejoin a server that just show up
func (server *ServerMonitor) Rejoin() error {
	// Check if master exists in topology before rejoining.
	if server.ClusterGroup.master != nil {
		if server.URL != server.ClusterGroup.master.URL {
			server.ClusterGroup.LogPrintf("INFO : Rejoining previously failed server %s", server.URL)
			crash := server.ClusterGroup.getCrash(server.URL)
			if crash == nil {
				server.ClusterGroup.LogPrintf("Error : rejoin found no crash info for %s", server.URL)
				return errors.New("No crash found")
			}
			if server.ClusterGroup.conf.AutorejoinBackupBinlog == true {
				server.backupBinlog(crash)
			}

			err := server.rejoinOldMaster(crash)
			if err != nil {
				server.ClusterGroup.LogPrintf("ERROR: Failed to autojoin previously failed master %s", server.URL)
			}
			crash.delete(&server.ClusterGroup.crashes)
			server.ClusterGroup.rejoinCond.Send <- true
		}
	}
	return nil
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
	server.ClusterGroup.LogPrintf("INFO : New Master %s was not sync before failover, unsafe flashback, lost events backing up event to %s ", crash.URL, backupdir)
	os.Mkdir(backupdir, 0777)
	os.Rename(server.ClusterGroup.conf.WorkingDir+"/"+server.ClusterGroup.cfgGroup+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-"+crash.FailoverMasterLogFile, backupdir+"/"+server.ClusterGroup.cfgGroup+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-"+crash.FailoverMasterLogFile)
	return nil

}
func (server *ServerMonitor) backupBinlog(crash *Crash) error {

	if _, err := os.Stat(server.ClusterGroup.conf.MariaDBBinaryPath + "/mysqlbinlog"); os.IsNotExist(err) {
		server.ClusterGroup.LogPrintf("Backup Binlog File does not exist %s check param mariadb-binary-path", server.ClusterGroup.conf.MariaDBBinaryPath+"/mysqlbinlog")
		return err
	}
	if _, err := os.Stat(server.ClusterGroup.conf.WorkingDir); os.IsNotExist(err) {
		server.ClusterGroup.LogPrintf("WorkingDir does not exist %s check param working-directory", server.ClusterGroup.conf.MariaDBBinaryPath+"/mysqlbinlog")
		return err
	}
	var cmdrun *exec.Cmd
	server.ClusterGroup.LogPrintf("INFO : Backup ahead binlog events of previously failed server %s", server.URL)
	filepath.Walk(server.ClusterGroup.conf.WorkingDir+"/", server.deletefiles)
	cmdrun = exec.Command(server.ClusterGroup.conf.MariaDBBinaryPath+"/mysqlbinlog", "--read-from-remote-server", "--raw", "--stop-never-slave-server-id=10000", "--user="+server.ClusterGroup.rplUser, "--password="+server.ClusterGroup.rplPass, "--host="+server.Host, "--port="+server.Port, "--result-file="+server.ClusterGroup.conf.WorkingDir+"/"+server.ClusterGroup.cfgGroup+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-", "--start-position="+crash.FailoverMasterLogPos, crash.FailoverMasterLogFile)
	server.ClusterGroup.LogPrintf("INFO : Backup %s %s ", server.ClusterGroup.conf.MariaDBBinaryPath+"/mysqlbinlog", cmdrun.Args)

	var outrun bytes.Buffer
	cmdrun.Stdout = &outrun

	cmdrunErr := cmdrun.Run()
	if cmdrunErr != nil {
		server.ClusterGroup.LogPrintf("ERROR: Failed to backup binlogs of %s,%s", server.URL, cmdrunErr.Error())
		server.ClusterGroup.LogPrintf("ERROR: %s %s", server.ClusterGroup.conf.MariaDBBinaryPath+"/mysqlbinlog ", cmdrun.Args)
		server.ClusterGroup.canFlashBack = false
		return cmdrunErr
	}
	return nil
}

func (server *ServerMonitor) rejoinOldMasterSync(crash *Crash) error {
	server.ClusterGroup.LogPrintf("INFO : Found same or lower  GTID %s  and new elected master was %s ", server.CurrentGtid.Sprint(), crash.FailoverIOGtid.Sprint())
	var err error
	realmaster := server.ClusterGroup.master
	if server.ClusterGroup.conf.MxsBinlogOn || server.ClusterGroup.conf.MultiTierSlave {
		realmaster = server.ClusterGroup.GetRelayServer()
	}
	if realmaster.MxsHaveGtid || realmaster.IsMaxscale == false {
		err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      realmaster.IP,
			Port:      realmaster.Port,
			User:      server.ClusterGroup.rplUser,
			Password:  server.ClusterGroup.rplPass,
			Retry:     strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatTime),
			Mode:      "CURRENT_POS",
		})
	} else {
		err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      realmaster.IP,
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

func (server *ServerMonitor) rejoinOldMasterFashBack(crash *Crash) error {
	realmaster := server.ClusterGroup.master
	if server.ClusterGroup.conf.MxsBinlogOn || server.ClusterGroup.conf.MultiTierSlave {
		realmaster = server.ClusterGroup.GetRelayServer()
	}
	//		server.ClusterGroup.LogPrintf("INFO :  SYNC using semisync, searching for a rejoin method")

	// Flashback here
	if _, err := os.Stat(server.ClusterGroup.conf.MariaDBBinaryPath + "/mysqlbinlog"); os.IsNotExist(err) {
		server.ClusterGroup.LogPrintf("File does not exist %s", server.ClusterGroup.conf.MariaDBBinaryPath+"/mysqlbinlog")
		return err
	}

	binlogCmd := exec.Command(server.ClusterGroup.conf.MariaDBBinaryPath+"/mysqlbinlog", "--flashback", "--to-last-log", server.ClusterGroup.conf.WorkingDir+"/"+server.ClusterGroup.cfgGroup+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-"+crash.FailoverMasterLogFile)
	clientCmd := exec.Command(server.ClusterGroup.conf.MariaDBBinaryPath+"/mysql", "--host="+server.Host, "--port="+server.Port, "--user="+server.ClusterGroup.dbUser, "--password="+server.ClusterGroup.dbPass)
	server.ClusterGroup.LogPrintf("FlashBack: %s %s", server.ClusterGroup.conf.MariaDBBinaryPath+"/mysqlbinlog", binlogCmd.Args)
	var err error
	clientCmd.Stdin, err = binlogCmd.StdoutPipe()
	if err != nil {
		server.ClusterGroup.LogPrintf("Error opening pipe: %s", err)
		return err
	}
	if err := binlogCmd.Start(); err != nil {
		server.ClusterGroup.LogPrintf("Error in mysqlbinlog command: %s at %s", err, binlogCmd.Path)
		return err
	}
	if err := clientCmd.Run(); err != nil {
		server.ClusterGroup.LogPrintf("Error starting client:%s at %s", err, clientCmd.Path)
		return err
	}
	server.ClusterGroup.LogPrintf("INFO : SET GLOBAL gtid_slave_pos = \"%s\"", crash.FailoverIOGtid.Sprint())
	_, err = server.Conn.Exec("SET GLOBAL gtid_slave_pos = \"" + crash.FailoverIOGtid.Sprint() + "\"")
	if err != nil {
		return err
	}
	var err2 error
	if server.MxsHaveGtid || server.IsMaxscale == false {
		err2 = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      realmaster.IP,
			Port:      realmaster.Port,
			User:      server.ClusterGroup.rplUser,
			Password:  server.ClusterGroup.rplPass,
			Retry:     strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatTime),
			Mode:      "SLAVE_POS",
		})
	} else {
		err2 = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      realmaster.IP,
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
		server.ClusterGroup.LogPrintf("INFO : New Master %s was in sync before failover safe flashback, no lost committed events", crash.URL)
	} else {
		server.saveBinlog(crash)
	}
	return nil
}

func (server *ServerMonitor) rejoinOldMasterDump(crash *Crash) error {
	var err3 error
	realmaster := server.ClusterGroup.master
	if server.ClusterGroup.conf.MxsBinlogOn || server.ClusterGroup.conf.MultiTierSlave {
		realmaster = server.ClusterGroup.GetRelayServer()
	}
	// done change master just to set the host and port before dump
	if server.MxsHaveGtid || server.IsMaxscale == false {
		err3 = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      realmaster.IP,
			Port:      realmaster.Port,
			User:      server.ClusterGroup.rplUser,
			Password:  server.ClusterGroup.rplPass,
			Retry:     strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatRetry),
			Heartbeat: strconv.Itoa(server.ClusterGroup.conf.ForceSlaveHeartbeatTime),
			Mode:      "SLAVE_POS",
		})
	} else {
		err3 = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:      realmaster.IP,
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
	server.ClusterGroup.RejoinMysqldump(server.ClusterGroup.master, server)
	dbhelper.StartSlave(server.Conn)
	return nil
}

func (server *ServerMonitor) rejoinOldMaster(crash *Crash) error {
	if server.ClusterGroup.conf.ReadOnly {
		dbhelper.SetReadOnly(server.Conn, true)
		server.ClusterGroup.LogPrintf("INFO : Setting Read Only on rejoined %s", server.URL)
	}

	if crash.FailoverIOGtid != nil {
		server.ClusterGroup.LogPrintf("INFO : rejoined GTID sequence %d", server.CurrentGtid.GetSeqServerIdNos(uint64(server.ServerID)))
		server.ClusterGroup.LogPrintf("INFO : Saved GTID sequence %d", crash.FailoverIOGtid.GetSeqServerIdNos(uint64(server.ServerID)))
	}
	if server.isReplicationAheadOfMasterElection(crash) == false || server.ClusterGroup.conf.MxsBinlogOn {
		server.rejoinOldMasterSync(crash)
		return nil
	}
	if crash.FailoverIOGtid != nil {
		if server.ClusterGroup.master.FailoverIOGtid.GetSeqServerIdNos(uint64(server.ServerID)) == 0 {
			server.ClusterGroup.LogPrintf("DEBUG: Cascading failover considere we can not flashback")
		} else {
			server.ClusterGroup.LogPrintf("INFO : Found ahead old server GTID %s and elected GTID %s on current master %s", server.CurrentGtid.Sprint(), server.ClusterGroup.master.FailoverIOGtid.Sprint(), server.ClusterGroup.master.URL)
		}
	} else {
		server.ClusterGroup.LogPrintf("INFO : Found none old server GTID for fashback")
	}
	if crash.FailoverIOGtid != nil && server.ClusterGroup.canFlashBack == true && server.ClusterGroup.conf.AutorejoinFlashback == true && server.ClusterGroup.conf.AutorejoinBackupBinlog == true {
		err := server.rejoinOldMasterFashBack(crash)
		if err == nil {
			return nil
		}
		server.ClusterGroup.LogPrintf("INFO : Flashback rejoin Failed: %s", err)
	} else {
		server.ClusterGroup.LogPrintf("INFO : No flashback rejoin: can flashback %t ,autorejoin-flashback %t autorejoin-backup-binlog %t ", server.ClusterGroup.canFlashBack, server.ClusterGroup.conf.AutorejoinFlashback, server.ClusterGroup.conf.AutorejoinBackupBinlog)
	}
	if server.ClusterGroup.conf.AutorejoinMysqldump == true {
		server.rejoinOldMasterDump(crash)
	} else {
		server.ClusterGroup.LogPrintf("INFO : No mysqldump rejoin : binlog capture failed or wrong version %t , autorejoin-mysqldump %t ", server.ClusterGroup.canFlashBack, server.ClusterGroup.conf.AutorejoinMysqldump)
		server.ClusterGroup.LogPrintf("INFO : No rejoin method found, old master says: leave me alone, I'm ahead")
	}
	if server.ClusterGroup.conf.RejoinScript != "" {
		server.ClusterGroup.LogPrintf("INFO : Calling rejoin script")
		var out []byte
		out, err := exec.Command(server.ClusterGroup.conf.RejoinScript, server.Host, server.ClusterGroup.master.Host).CombinedOutput()
		if err != nil {
			server.ClusterGroup.LogPrint("ERROR:", err)
		}
		server.ClusterGroup.LogPrint("INFO : Rejoin script complete", string(out))
	}

	return nil
}

func (server *ServerMonitor) rejoinSlave(ss dbhelper.SlaveStatus) error {
	// Test if slave not connected to current master
	mycurrentmaster, _ := server.ClusterGroup.GetMasterFromReplication(server)

	if mycurrentmaster != nil {

		if server.ClusterGroup.master != nil {

			if server.ClusterGroup.master.DSN != mycurrentmaster.DSN {
				server.ClusterGroup.LogPrintf("DEBUG: Found slave to rejoin  %s slave was previously in %s replication io thread is %s , pointing currently to %s", server.URL, server.PrevState, ss.Slave_IO_Running, mycurrentmaster.DSN)

				if mycurrentmaster.State != stateFailed && mycurrentmaster.IsRelay == false && server.ClusterGroup.conf.MultiTierSlave == false {
					realmaster := server.ClusterGroup.master
					slave_gtid := server.CurrentGtid.GetSeqServerIdNos(uint64(server.MasterServerID))
					master_gtid := realmaster.FailoverIOGtid.GetSeqServerIdNos(uint64(server.MasterServerID))
					if slave_gtid < master_gtid {
						server.ClusterGroup.LogPrintf("DEBUG: Rejoining slave server %s to master %s", server.URL, realmaster.URL)
						err := dbhelper.StopSlave(server.Conn)
						if err == nil {
							err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
								Host:      realmaster.IP,
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
								server.ClusterGroup.LogPrintf("ERROR: Failed to autojoin indirect slave server %s, stopping slave as a precaution.", server.URL)
								server.ClusterGroup.LogPrint(err)
							}
						} else {
							server.ClusterGroup.LogPrint(err)
						}

					} else if server.ClusterGroup.conf.LogLevel > 2 && slave_gtid < master_gtid {
						server.ClusterGroup.LogPrintf("DEBUG: Slave server %s (%d) is ahead of master %s (%d)", server.URL, slave_gtid, realmaster.URL, master_gtid)
					}
				}
			}

		} else {
			server.ClusterGroup.LogPrintf("ERROR: slave want's to rejoin non discovred master")
		}
	} // end mycurrentmaster !=nil

	// In case of state change, reintroduce the server in the slave list
	if server.PrevState == stateFailed || server.PrevState == stateUnconn {
		server.State = stateSlave
		server.FailCount = 0
		server.ClusterGroup.slaves = append(server.ClusterGroup.slaves, server)
		if server.ClusterGroup.conf.ReadOnly {
			err := dbhelper.SetReadOnly(server.Conn, true)
			if err != nil {
				server.ClusterGroup.LogPrintf("ERROR: Could not set rejoining slave %s as read-only, %s", server.URL, err)
				return err
			}
		}
	}
	return nil
}

// UseGtid  check is replication use gtid
func (server *ServerMonitor) UsedGtidAtElection(crash *Crash) bool {
	if server.ClusterGroup.conf.LogLevel > 1 {
		server.ClusterGroup.LogPrintf("DEBUG: Rejoin Server use gtid %s", server.UsingGtid)
	}
	// An old master  master do no have replication
	if crash.FailoverIOGtid == nil {
		server.ClusterGroup.LogPrintf("DEBUG: Rejoin server does not found a saved master election GTID")
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
			server.ClusterGroup.LogPrintf("DEBUG: Cascading failover considere we are ahead to force dump")
			return true
		}
		if server.CurrentGtid.GetSeqServerIdNos(uint64(server.ServerID)) > crash.FailoverIOGtid.GetSeqServerIdNos(uint64(server.ServerID)) {
			server.ClusterGroup.LogPrintf("DEBUG: rejoining node seq %d, master seq %d", server.CurrentGtid.GetSeqServerIdNos(uint64(server.ServerID)), server.ClusterGroup.master.FailoverIOGtid.GetSeqServerIdNos(uint64(server.ServerID)))
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
