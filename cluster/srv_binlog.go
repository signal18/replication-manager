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
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

func (server *ServerMonitor) RefreshBinaryLogs() error {
	var logs string
	var err error
	cluster := server.ClusterGroup

	//Don't check binlog of the ignored servers
	if server.IsIgnored() {
		err = errors.New("Server is ignored")
		return err
	}

	binlogs, logs, err := dbhelper.GetBinaryLogs(server.Conn, server.DBVersion)
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Could not get binary log files %s %s", server.URL, err)
	} else {
		server.SetBinaryLogFiles(binlogs)
	}

	if len(server.BinaryLogFiles.ToNewMap()) > 0 {
		server.SetBinaryLogFileOldest()
	}

	return err
}

func (server *ServerMonitor) CheckBinaryLogs() error {
	cluster := server.ClusterGroup
	var err error

	//Don't check binlog of the ignored servers
	if server.IsIgnored() {
		err = errors.New("Server is ignored")
		return err
	}

	if len(server.BinaryLogFiles.ToNewMap()) == 0 {
		server.RefreshBinaryLogs()
	}

	if server.BinaryLogFilePrevious != "" && server.BinaryLogFilePrevious != server.BinaryLogFile {
		// Always running, triggered by binlog rotation
		if cluster.Conf.BinlogRotationScript != "" && server.IsMaster() {
			cluster.BinlogRotationScript(server)
		}

		if cluster.Conf.BackupBinlogs {
			//Set second parameter to false, not part of backupbinlogpurge
			server.InitiateJobBackupBinlog(server.BinaryLogFilePrevious, false)
			//Initiate purging backup binlog
			go server.JobBackupBinlogPurge(server.BinaryLogFilePrevious)
		}

		server.RefreshBinaryLogs()
	}

	server.BinaryLogFilePrevious = server.BinaryLogFile

	if cluster.Conf.ForceBinlogPurge && !server.DBVersion.IsPostgreSQL() {
		if cluster.IsInFailover() {
			err = errors.New("Cancel job purge slave binlog during failover")
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, err.Error())
			return err
		}

		if server.IsBackingUpBinaryLog {
			err = errors.New("Server is backing up binlogs")
			return err
		}
		go server.ForcePurgeBinlogs()
	}

	return err
}

func (server *ServerMonitor) ForcePurgeBinlogs() {
	cluster := server.ClusterGroup
	isMaster := server.IsMaster()

	if server.IsPurgingBinlog() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Server is is waiting for previous binlog purge to finish")
		return
	}

	if server.IsMariaDB() && server.DBVersion.GreaterEqual("11.4") { //Only MariaDB v.11.4 and up
		err := server.SetMaxBinlogTotalSize()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlWarn, err.Error())
		}
	} else if len(server.BinaryLogFiles.ToNewMap()) > 2 {
		if isMaster {
			go server.JobBinlogPurgeMaster()
		}

		if !isMaster && cluster.Conf.ForceBinlogPurgeReplicas && server.HaveBinlogSlaveUpdates && cluster.StateMachine.CurState.Search("WARN0107") == false && !server.IsIgnored() {
			go server.JobBinlogPurgeSlave()
		}
	}
}

func (server *ServerMonitor) SetIsPurgingBinlog(value bool) {
	server.InPurgingBinaryLog = value
}

func (server *ServerMonitor) SetBinlogOldestTimestamp(str string) error {
	var regex, err = regexp.Compile(`[0-9]{6}[ ]{1,2}[0-9:]{7,8}`)
	if err != nil {
		return errors.New("Incorrect regexp.")
	}

	//Get First Timestamp From Binlog Format Desc and remove multiple space
	strin := strings.Replace(regex.FindString(str), "  ", " ", -1)
	if strin == "" {
		return errors.New("Timestamp not found on binlog")
	}

	strout := strings.Split(strin, " ")

	dt := strout[0]
	yy, err := strconv.Atoi(dt[:2])
	if err != nil {
		return errors.New("Failed to parse year")
	}
	mm, err := strconv.Atoi(dt[2:4])
	if err != nil {
		return errors.New("Failed to parse month")
	}
	dd, err := strconv.Atoi(dt[4:])
	if err != nil {
		return errors.New("Failed to parse date of month")
	}

	tm := strings.Split(strout[1], ":")
	hr, err := strconv.Atoi(tm[0])
	if err != nil {
		return errors.New("Failed to parse hour")
	}
	min, err := strconv.Atoi(tm[1])
	if err != nil {
		return errors.New("Failed to parse minute")
	}
	sec, err := strconv.Atoi(tm[2])
	if err != nil {
		return errors.New("Failed to parse second")
	}

	//4 digit hack
	now := time.Now()
	twodigit, _ := strconv.Atoi(now.Format("06"))
	yy = (now.Year() - twodigit) + yy

	//4 digit hack prevent wrong year
	if yy > now.Year() {
		yy = yy - 100
	}

	server.BinaryLogOldestTimestamp = time.Date(yy, time.Month(mm), dd, hr, min, sec, 0, time.Local).Unix()
	return nil
}

func (server *ServerMonitor) RefreshBinlogOldestTimestamp() error {
	cluster := server.ClusterGroup
	var err error

	defer server.SetInRefreshBinlog(false)

	if server.BinaryLogFileOldest != "" {
		if cluster.Conf.BinlogParseMode == "gomysql" {
			port, _ := strconv.Atoi(server.Port)

			cfg := replication.BinlogSyncerConfig{
				ServerID: uint32(cluster.Conf.CheckBinServerId),
				Flavor:   server.DBVersion.Flavor,
				Host:     server.Host,
				Port:     uint16(port),
				User:     server.User,
				Password: server.Pass,
			}

			syncer := replication.NewBinlogSyncer(cfg)

			streamer, err := syncer.StartSync(mysql.Position{Name: server.BinaryLogFileOldest, Pos: 0})
			if err != nil {
				return err
			}

			for {
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cluster.Conf.MonitoringQueryTimeout)*time.Millisecond)
				ev, err := streamer.GetEvent(ctx)
				cancel()

				if err == context.DeadlineExceeded {
					// meet timeout
					break
				}

				if ev.Header.EventType == replication.FORMAT_DESCRIPTION_EVENT {
					server.BinaryLogOldestTimestamp = int64(ev.Header.Timestamp)
					ts := time.Unix(server.BinaryLogOldestTimestamp, 0)
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlInfo, "Refreshed oldest timestamp on %s. oldest: %s", server.Host+":"+server.Port, ts.String())
					//Only update once for oldest binlog timestamp
					break
				}
			}

			syncer.Close()
		} else {
			binsrvid := strconv.Itoa(cluster.Conf.CheckBinServerId)

			events, _, err := dbhelper.GetBinlogFormatDesc(server.Conn, server.BinaryLogFileOldest)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Error while getting binlog events from oldest master binlog: %s. Err: %s", server.BinaryLogFileOldest, err.Error())
				return err
			}

			for _, ev := range events {
				startpos := fmt.Sprintf("%d", ev.Pos)
				endpos := fmt.Sprintf("%d", ev.End_log_pos)

				mysqlbinlogcmd := exec.Command(cluster.GetMysqlBinlogPath(), "--read-from-remote-server", "--server-id="+binsrvid, "--user="+cluster.GetRplUser(), "--password="+cluster.GetRplPass(), "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--start-position="+startpos, "--stop-position="+endpos, ev.Log_name)

				result, err := mysqlbinlogcmd.Output()
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Error while extracting timestamp from oldest master binlog: %s. Err: %s", server.BinaryLogFileOldest, err.Error())
					return err
				}

				err = server.SetBinlogOldestTimestamp(string(result))
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "%s. Host: %s - %s", err.Error(), server.Host+":"+server.Port, server.BinaryLogFileOldest)
					return err
				}

				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlInfo, "Refreshed binary logs on %s - %s. oldest timestamp: %s", server.Host+":"+server.Port, ev.Log_name, time.Unix(server.BinaryLogOldestTimestamp, 0).String())

				return err
			}

		}
	}
	return err
}

func (server *ServerMonitor) SetMaxBinlogTotalSize() error {
	cluster := server.ClusterGroup
	totalsize := cluster.Conf.ForceBinlogPurgeTotalSize * 1024 * 1024 * 1024
	if server.IsMariaDB() && server.DBVersion.GreaterEqual("11.4") { //Only MariaDB v.11.4 and up
		v, ok := server.Variables.CheckAndGet("MAX_BINLOG_TOTAL_SIZE")
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

func (server *ServerMonitor) SetBinaryLogFileOldest() {
	cluster := server.ClusterGroup
	files := len(server.BinaryLogFiles.ToNewMap())

	if server.IsRefreshingBinlog {
		return
	}

	server.SetInRefreshBinlog(true)

	if files > 0 {
		//If no other binlog is exist
		if files == 1 && server.BinaryLogFileOldest != server.BinaryLogFile {
			if server.BinaryLogFileOldest != server.BinaryLogFile {
				server.BinaryLogFileOldest = server.BinaryLogFile
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Refreshed binary logs on %s. oldest: %s", server.Host+":"+server.Port, server.BinaryLogFileOldest)
				//Only get timestamp when needed
				if cluster.Conf.ForceBinlogPurge {
					go server.RefreshBinlogOldestTimestamp()
				} else {
					server.SetInRefreshBinlog(false)
				}
			}
			return
		}

		//Use filename and binlog counts
		parts := strings.Split(server.BinaryLogFile, ".")
		last := len(parts) - 1
		prefix := strings.Join(parts[:last], ".")
		latestbinlog, _ := strconv.Atoi(parts[last])
		oldestbinlog := latestbinlog - files + 1
		oldest := prefix + "." + fmt.Sprintf("%06d", oldestbinlog)

		if _, ok := server.BinaryLogFiles.CheckAndGet(oldest); ok && server.BinaryLogFileOldest != server.BinaryLogFile {
			if server.BinaryLogFileOldest != oldest {
				server.BinaryLogFileOldest = oldest
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Refreshed binary logs on %s. oldest: %s", server.Host+":"+server.Port, server.BinaryLogFileOldest)
				//Only get timestamp when needed
				if cluster.Conf.ForceBinlogPurge {
					go server.RefreshBinlogOldestTimestamp()
				} else {
					server.SetInRefreshBinlog(false)
				}
			}
			return
		}
	}
}

func (server *ServerMonitor) JobBinlogPurgeMaster() {
	cluster := server.GetCluster()

	//Refresh slaves replication positions
	cluster.CheckSlavesReplicationsPurge()

	if cluster.SlavesConnected <= cluster.Conf.ForceBinlogPurgeMinReplica {
		if cluster.StateMachine.CurState.Search("WARN0106") == false {
			cluster.SetState("WARN0106", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0106"], cluster.Conf.ForceBinlogPurgeMinReplica), ErrFrom: "PURGE", ServerUrl: server.URL})
		}
		return
	}

	if len(server.BinaryLogFiles.ToNewMap()) == 0 {
		server.RefreshBinaryLogs()
	}

	//Block multiple purge
	if server.IsPurgingBinlog() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Master is waiting for previous binlog purge to finish")
		return
	}

	server.SetIsPurgingBinlog(true)
	defer server.SetIsPurgingBinlog(false)

	if cluster.IsInFailover() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Cancel job purge binlog during failover")
		return
	}
	if !cluster.Conf.ForceBinlogPurge {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Purge binlog not enabled")
		return
	}

	if !server.IsMaster() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Purge only master binlog")
		return
	}

	parts := strings.Split(server.BinaryLogFile, ".")
	last := len(parts) - 1
	prefix := strings.Join(parts[:last], ".")

	suffix, _ := strconv.Atoi(parts[last])
	oldestbinlog := suffix + 1 - len(server.BinaryLogFiles.ToNewMap())

	if cluster.SlavesOldestMasterFile.Prefix != prefix {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Purge cancelled, master binlog file has different prefix")
		return
	}

	if suffix < cluster.SlavesOldestMasterFile.Suffix {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Purge cancelled because of inconsistency, slaves master filename is bigger than master binlog")
		return
	}

	//Purge binlog on restore
	if cluster.Conf.ForceBinlogPurgeOnRestore {

		//Only purge if oldest master binlog has more than 2 files
		prevbinlog := cluster.SlavesOldestMasterFile.Suffix - 1

		if oldestbinlog > 0 && oldestbinlog < prevbinlog {
			//Purge binlog to will retain the file
			filename := cluster.SlavesOldestMasterFile.Prefix + "." + fmt.Sprintf("%06d", prevbinlog)
			server.PurgeBinlogTo(filename)
			server.RefreshBinaryLogs()

		}
		//Not needed to continue, since this only retain last two binlogs
		return
	}

	/**
	* This will run when force purge binlog total size is set (default 30)
	 **/
	if cluster.Conf.ForceBinlogPurgeTotalSize > 0 {
		// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "server:%s start:%s stop:%s list: %s", server.URL, fmt.Sprintf("%06d", latestbinlog), fmt.Sprintf("%06d", oldestbinlog))
		var totalSize uint
		totalSize = 0
		lastfile := 0

		//Accumulating newest binlog size and shifting to oldest
		for suffix > 0 && totalSize < uint(cluster.Conf.ForceBinlogPurgeTotalSize*(1024*1024*1024)) {
			filename := prefix + "." + fmt.Sprintf("%06d", suffix)
			if size, ok := server.BinaryLogFiles.CheckAndGet(filename); ok {
				//accumulating size
				totalSize += size
				lastfile = suffix //last file based on total size
			}
			//Descending
			suffix--
		}

		// Purging binlog if more than total size
		if lastfile > 0 && oldestbinlog <= lastfile {
			for oldestbinlog <= lastfile {
				//Halt and return if last binlogfile is same with slave master pos
				if oldestbinlog == cluster.SlavesOldestMasterFile.Suffix {
					if cluster.StateMachine.CurState.Search("WARN0107") == false {
						cluster.SetState("WARN0107", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0107"], cluster.SlavesOldestMasterFile.Prefix, cluster.SlavesOldestMasterFile.Suffix), ErrFrom: "CHECK", ServerUrl: server.URL})
					}
					return
				}

				//Increment for purging use
				oldestbinlog++

				if oldestbinlog > 0 && oldestbinlog < cluster.SlavesOldestMasterFile.Suffix-1 {
					filename := prefix + "." + fmt.Sprintf("%06d", oldestbinlog)
					if _, ok := server.BinaryLogFiles.CheckAndGet(filename); ok {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlInfo, "Purging binlog of %s: %s. ", server.URL, filename)
						_, err := dbhelper.PurgeBinlogTo(server.Conn, filename)
						if err != nil {
							cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Error purging binlog of %s,%s : %s", server.URL, filename, err.Error())
						}
					} else {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Binlog filename not found on %s: %s", server.URL, filename)
					}
				}
			}
			server.RefreshBinaryLogs()
		}
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Cancel job purge due to no total size")
	}

}

func (server *ServerMonitor) PurgeBinlogTo(filename string) {
	cluster := server.ClusterGroup
	//Check if file exists
	if _, ok := server.BinaryLogFiles.CheckAndGet(filename); ok {
		_, err := dbhelper.PurgeBinlogTo(server.Conn, filename)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlWarn, "Error purging binlog of %s,%s : %s", server.URL, filename, err.Error())
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlInfo, "[%s] Executed PURGE BINLOG TO %s", server.URL, filename)
		}
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Binlog filename not found on %s: %s", server.URL, filename)
	}
}

func (server *ServerMonitor) JobBinlogPurgeSlave() {
	cluster := server.GetCluster()
	master := cluster.GetMaster()

	//Only purge when master is valid
	if master != nil && master.Host == server.GetReplicationMasterHost() {
		if server.IsPurgingBinlog() {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Server is waiting for previous binlog purge to finish")
			return
		}

		server.SetIsPurgingBinlog(true)
		defer server.SetIsPurgingBinlog(false)

		//Only purge if slave connected and status is slave or slave late
		if server.State != stateSlave && server.State != stateSlaveLate {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Can not purge. Only connected slave is allowed to purge binlog")
			return
		}

		//Purge slaves to oldest master binlog timestamp and skip if slave only has 2 binary logs file left (Current Binlog and Prev Binlog)
		if server.BinaryLogOldestTimestamp > 0 && master.BinaryLogOldestTimestamp > server.BinaryLogPurgeBefore && len(server.BinaryLogFiles.ToNewMap()) > 2 {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlInfo, "Purging slave binlog of %s from %s until oldest timestamp on master: %s", server.URL, time.Unix(server.BinaryLogOldestTimestamp, 0).String(), time.Unix(master.BinaryLogOldestTimestamp, 0).String())
			q, err := dbhelper.PurgeBinlogBefore(server.Conn, master.BinaryLogOldestTimestamp)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Error purging binlog of %s : %s", server.URL, err.Error())
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Executed query: %s", q)
			}
			server.BinaryLogPurgeBefore = master.BinaryLogOldestTimestamp
			server.RefreshBinaryLogs()
		}
	}
}
