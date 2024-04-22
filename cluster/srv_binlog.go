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
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

func (server *ServerMonitor) RefreshBinaryLogs() {
	var logs string
	var err error
	cluster := server.ClusterGroup

	server.BinaryLogFiles, logs, err = dbhelper.GetBinaryLogs(server.Conn, server.DBVersion)
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Monitor", LvlDbg, "Could not get binary log files %s %s", server.URL, err)
	}

	if len(server.BinaryLogFiles) > 0 {
		server.SetBinaryLogOldestFile()
	}
}

func (server *ServerMonitor) SetIsPurgingBinlog(value bool) {
	server.Lock()
	server.InPurgingBinaryLog = value
	server.Unlock()
}

func (server *ServerMonitor) SetBinlogOldestTimestamp(str string) error {
	strout := strings.Split(strings.Replace(strings.Replace(strings.Replace(strings.Replace(string(str), "  ", " ", -1), "#", "", -1), "\n", "", -1), "\r", "", -1), " ")
	if strout[0] == "" {
		return errors.New("Failed to parse binary log datetime string. ")
	}

	dt := strout[0]
	yy, err := strconv.Atoi(dt[:2])
	if err != nil {
		return errors.New("Failed to parse binary log datetime string. Part: year ")
	}
	mm, err := strconv.Atoi(dt[2:4])
	if err != nil {
		return errors.New("Failed to parse binary log datetime string. Part: month ")
	}
	dd, err := strconv.Atoi(dt[4:])
	if err != nil {
		return errors.New("Failed to parse binary log datetime string. Part: date ")
	}

	tm := strings.Split(strout[1], ":")
	hr, err := strconv.Atoi(tm[0])
	if err != nil {
		return errors.New("Failed to parse binary log datetime string. Part: hour ")
	}
	min, err := strconv.Atoi(tm[1])
	if err != nil {
		return errors.New("Failed to parse binary log datetime string. Part: minute ")
	}
	sec, err := strconv.Atoi(tm[2])
	if err != nil {
		return errors.New("Failed to parse binary log datetime string. Part: second ")
	}

	//4 digit hack
	now := time.Now()
	twodigit, _ := strconv.Atoi(now.Format("06"))
	yy = (now.Year() - twodigit) + yy

	//4 digit hack prevent wrong year
	if yy > now.Year() {
		yy = yy - 100
	}

	server.OldestBinaryLogTimestamp = time.Date(yy, time.Month(mm), dd, hr, min, sec, 0, time.UTC).Unix()
	return nil
}

func (server *ServerMonitor) RefreshBinlogOldestTimestamp() {
	cluster := server.ClusterGroup
	// var err error
	if server.BinaryLogOldestFile != "" {
		mysqlbinlogcmd := exec.Command(cluster.GetMysqlBinlogPath(), "--read-from-remote-server", "--server-id=10000", "--user="+cluster.GetRplUser(), "--password="+cluster.GetRplPass(), "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--stop-position=512", server.BinaryLogOldestFile)
		grepcmd := exec.Command("grep", "-Eo", "-m 1", "#[0-9]{6}[ ]{1,2}[0-9:]{8}")

		// out, _ := mysqlbinlogcmd.Output()
		pipe, err := mysqlbinlogcmd.StdoutPipe()
		defer pipe.Close()
		grepcmd.Stdin = pipe

		mysqlbinlogcmd.Start()

		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Error while extracting timestamp from oldest master binlog: %s. Err: %s", server.BinaryLogOldestFile, err.Error())
			return
		}

		out, _ := grepcmd.Output()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Error while extracting timestamp from oldest master binlog: %s. Err: ", server.BinaryLogOldestFile, err.Error())
			return
		}

		err = server.SetBinlogOldestTimestamp(string(out))
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "%s. Str: %s. Host: %s - %s", err.Error(), string(out), server.Host+":"+server.Port, server.BinaryLogOldestFile)
			return
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlDbg, "Refreshed binary logs on %s. oldest timestamp: %s", server.Host+":"+server.Port, time.Unix(server.OldestBinaryLogTimestamp, 0).String())

		return
	}
}

func (server *ServerMonitor) SetMaxBinlogTotalSize() error {
	cluster := server.ClusterGroup
	totalsize := cluster.Conf.ForceBinlogPurgeTotalSize * 1024 * 1024 * 1024
	if server.IsMariaDB() && server.DBVersion.GreaterEqual("11.4") { //Only MariaDB v.11.4 and up
		v, ok := server.Variables["max_binlog_total_size"]
		if !ok {
			return errors.New("Variable max_binlog_total_size not found")
		}

		size, err := strconv.Atoi(v)
		if err != nil {
			return err
		} else {
			if size != totalsize {
				_, err := dbhelper.SetMaxBinlogTotalSize(server.Conn, totalsize)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (server *ServerMonitor) SetBinaryLogOldestFile() {
	cluster := server.ClusterGroup
	files := len(server.BinaryLogFiles)
	if files > 0 {
		//If no other binlog is exist
		if files == 1 && server.BinaryLogOldestFile != server.BinaryLogFile {
			server.BinaryLogOldestFile = server.BinaryLogFile
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlDbg, "Refreshed binary logs on %s. oldest: %s", server.Host+":"+server.Port, server.BinaryLogOldestFile)
			server.RefreshBinlogOldestTimestamp()
			return
		}

		//Use filename and binlog counts
		parts := strings.Split(server.BinaryLogFile, ".")
		last := len(parts) - 1
		prefix := strings.Join(parts[:last], ".")
		latestbinlog, _ := strconv.Atoi(parts[last])
		oldestbinlog := latestbinlog - len(server.BinaryLogFiles) + 1
		oldest := prefix + "." + fmt.Sprintf("%06d", oldestbinlog)

		if _, ok := server.BinaryLogFiles[oldest]; ok && server.BinaryLogOldestFile != server.BinaryLogFile {
			server.BinaryLogOldestFile = oldest
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlDbg, "Refreshed binary logs on %s. oldest: %s", server.Host+":"+server.Port, server.BinaryLogOldestFile)
			server.RefreshBinlogOldestTimestamp()
			return
		}
	}
}

func (server *ServerMonitor) JobBinlogPurgeMaster() {
	cluster := server.GetCluster()

	//Refresh slaves replication positions
	cluster.CheckSlavesReplications()

	if cluster.SlavesConnected < cluster.Conf.ForceBinlogPurgeMinReplica {
		if cluster.StateMachine.CurState.Search("WARN0106") == false {
			cluster.StateMachine.AddState("WARN0106", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0106"], cluster.Conf.ForceBinlogPurgeMinReplica), ErrFrom: "PURGE", ServerUrl: server.URL})
		}
		return
	}

	if len(server.BinaryLogFiles) == 0 {
		server.RefreshBinaryLogs()
	}

	//Block multiple purge
	if server.IsPurgingBinlog() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Master is waiting for previous binlog purge to finish")
		return
	}

	server.SetIsPurgingBinlog(true)
	defer server.SetIsPurgingBinlog(false)

	if cluster.IsInFailover() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Cancel job purge binlog during failover")
		return
	}
	if !cluster.Conf.ForceBinlogPurge {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Purge binlog not enabled")
		return
	}

	if !server.IsMaster() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Purge only master binlog")
		return
	}

	parts := strings.Split(server.BinaryLogFile, ".")
	last := len(parts) - 1
	prefix := strings.Join(parts[:last], ".")

	latestbinlog, _ := strconv.Atoi(parts[last])
	oldestbinlog := latestbinlog + 1 - len(server.BinaryLogFiles)

	// If force purge binlog total size is set (default 30)
	if cluster.Conf.ForceBinlogPurgeTotalSize > 0 {
		// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "server:%s start:%s stop:%s list: %s", server.URL, fmt.Sprintf("%06d", latestbinlog), fmt.Sprintf("%06d", oldestbinlog))
		var totalSize uint
		totalSize = 0
		lastfile := 0

		//Accumulating newest binlog size and shifting to oldest
		for totalSize < uint(cluster.Conf.ForceBinlogPurgeTotalSize*(1024*1024*1024)) {
			filename := prefix + "." + fmt.Sprintf("%06d", latestbinlog)
			if size, ok := server.BinaryLogFiles[filename]; ok {
				//accumulating size
				totalSize += size
				lastfile = latestbinlog //last file based on total size
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, "Filename not found on %s: %s", server.URL, filename)
			}
			//Descending
			latestbinlog--
		}

		// Purging binlog if more than total size
		if lastfile > 0 && oldestbinlog <= lastfile {
			for oldestbinlog <= lastfile {
				//Halt and return if last binlogfile is same with slave master pos
				if oldestbinlog == cluster.SlavesOldestMasterFile.Suffix {
					if cluster.StateMachine.CurState.Search("WARN0105") == false {
						cluster.StateMachine.AddState("WARN0105", state.State{ErrType: "WARNING", ErrDesc: clusterError["WARN0105"], ErrFrom: "CHECK", ServerUrl: server.URL})
					}
					return
				}

				//Increment for purging use
				oldestbinlog++

				if oldestbinlog > 0 {
					filename := prefix + "." + fmt.Sprintf("%06d", oldestbinlog)
					if _, ok := server.BinaryLogFiles[filename]; ok {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Purging binlog of %s: %s. ", server.URL, filename)
						_, err := dbhelper.PurgeBinlogTo(server.Conn, filename)
						if err != nil {
							cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlWarn, "Error purging binlog of %s,%s : %s", server.URL, filename, err.Error())
						}
					} else {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlWarn, "Binlog filename not found on %s: %s", server.URL, filename)
					}
				}
			}
			server.RefreshBinaryLogs()
		}
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Cancel job purge due to no total size")
	}

	return
}

func (server *ServerMonitor) JobBinlogPurgeMasterOnRestore() {
	cluster := server.GetCluster()

	if server.DBVersion.IsPostgreSQL() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Force Binlog Purge On Restore not available in PostgreSQL")
		return
	}

	//Refresh slaves replication positions
	cluster.CheckSlavesReplications()

	if cluster.SlavesConnected < cluster.Conf.ForceBinlogPurgeMinReplica {
		if cluster.StateMachine.CurState.Search("WARN0106") == false {
			cluster.StateMachine.AddState("WARN0106", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0106"], cluster.Conf.ForceBinlogPurgeMinReplica), ErrFrom: "PURGE", ServerUrl: server.URL})
		}
		return
	}

	if len(server.BinaryLogFiles) == 0 {
		server.RefreshBinaryLogs()
	}

	//Block multiple purge
	if server.IsPurgingBinlog() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Master is waiting for previous binlog purge to finish")
		return
	}

	server.SetIsPurgingBinlog(true)
	defer server.SetIsPurgingBinlog(false)

	if cluster.IsInFailover() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Cancel job purge binlog during failover")
		return
	}
	if !cluster.Conf.ForceBinlogPurge {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Purge binlog not enabled")
		return
	}

	if !server.IsMaster() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Purge only master binlog")
		return
	}

	if !cluster.Conf.ForceBinlogPurgeOnRestore {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Purge binlog on restore is not enabled")
		return
	}

	parts := strings.Split(server.BinaryLogFile, ".")
	last := len(parts) - 1
	prefix := strings.Join(parts[:last], ".")
	suffix, _ := strconv.Atoi(parts[last])

	if cluster.SlavesOldestMasterFile.Prefix != prefix {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Purge cancelled, master binlog file has different prefix")
		return
	}

	if suffix < cluster.SlavesOldestMasterFile.Suffix {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Purge cancelled because of inconsistency, slaves master filename is bigger than master binlog")
		return
	}

	//Retain previous binlog
	filename := prefix + "." + fmt.Sprintf("%06d", cluster.SlavesOldestMasterFile.Suffix-1)

	//Check if file exists
	if _, ok := server.BinaryLogFiles[filename]; ok {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Purging binlog of %s: %s. ", server.URL, filename)
		_, err := dbhelper.PurgeBinlogTo(server.Conn, filename)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlWarn, "Error purging binlog of %s,%s : %s", server.URL, filename, err.Error())
		}
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlWarn, "Binlog filename not found on %s: %s", server.URL, filename)
	}

	server.RefreshBinaryLogs()

	return
}

func (server *ServerMonitor) JobBinlogPurgeSlave() {
	cluster := server.GetCluster()
	master := cluster.GetMaster()

	if master != nil {
		//Block multiple purge
		if len(server.BinaryLogFiles) == 0 {
			server.RefreshBinaryLogs()
		}

		if server.IsPurgingBinlog() {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Master is waiting for previous binlog purge to finish")
			return
		}

		server.SetIsPurgingBinlog(true)
		defer server.SetIsPurgingBinlog(false)

		if cluster.IsInFailover() {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Cancel job purge slave binlog during failover")
			return
		}
		if !cluster.Conf.ForceBinlogPurge {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Purge binlog not enabled")
			return
		}

		//Block multiple purge

		//Only purge if slave connected and status is slave or slave late
		if server.State != stateSlave && server.State != stateSlaveLate {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Can not purge. Only connected slave is allowed to purge binlog")
			return
		}

		//Purge slaves to oldest master binlog timestamp
		if master.OldestBinaryLogTimestamp > server.OldestBinaryLogTimestamp {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Purging slave binlog of %s until oldest timestamp on master: %s", server.URL, time.Unix(master.OldestBinaryLogTimestamp, 0).String())
			_, err := dbhelper.PurgeBinlogBefore(server.Conn, master.OldestBinaryLogTimestamp)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Error purging binlog of %s : %s", server.URL, err.Error())
			}

			server.RefreshBinaryLogs()
		}
	}

	return
}

// Check And Purge Binlogs check mariadb binlog
func (server *ServerMonitor) CheckAndPurgeBinlogMaster() {
	cluster := server.ClusterGroup
	if cluster.Conf.ForceBinlogPurge && !server.DBVersion.IsPostgreSQL() { // Only work if ForceBinlogPurge is on and MySQL/Percona/MariaDB

		if server.IsMariaDB() && server.DBVersion.GreaterEqual("11.4") { //Only MariaDB v.11 and up
			err := server.SetMaxBinlogTotalSize()
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, err.Error())
			}
		} else {
			// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Purging check")
			if !server.IsPurgingBinlog() {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlDbg, "MariaDB Version is not compatible for max_binlog_total_size, using manual purging")
				go server.JobBinlogPurgeMaster()
			}
		}
	}
}

// Check And Purge Binlogs check mariadb binlog
func (server *ServerMonitor) CheckAndPurgeBinlogSlave() {
	cluster := server.ClusterGroup
	if cluster.Conf.ForceBinlogPurge && !server.DBVersion.IsPostgreSQL() { // Only work if ForceBinlogPurge is on and MySQL/Percona/MariaDB
		if server.IsMariaDB() && server.DBVersion.GreaterEqual("11.4") { //Only MariaDB v.11.4 and up
			err := server.SetMaxBinlogTotalSize()
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlInfo, err.Error())
			}
		} else {
			if !server.IsPurgingBinlog() {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlDbg, "MariaDB Version is not compatible for max_binlog_total_size, using manual purging")
				if cluster.StateMachine.CurState.Search("WARN0105") == false {
					go server.JobBinlogPurgeSlave()
				}
			}
		}
	}
}
