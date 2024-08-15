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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
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
	var writeMeta bool

	//Don't check binlog of the ignored servers
	if server.IsIgnored() {
		err = errors.New("Server is ignored")
		return err
	}

	if server.IsRefreshingBinlog {
		return errors.New("Server is refreshing binlogs")
	}

	server.SetInRefreshBinlog(true)
	defer server.SetInRefreshBinlog(false)

	var oldmeta = make(map[string]dbhelper.BinaryLogMetadata)
	if server.BinaryLogFilesCount == 0 {
		oldmeta, err = server.ReadLocalBinaryLogMetadata()
	}

	count, oldest, trimmed, logs, err := dbhelper.GetBinaryLogs(server.Conn, server.DBVersion, server.BinaryLogFiles)
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Could not get binary log files %s %s", server.URL, err)
	}

	if len(trimmed) > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlInfo, "Remove purged binlog from binlog metadata on %s: %s", server.Host+":"+server.Port, strings.Join(trimmed, ","))
	}

	if count > 0 {
		if server.BinaryLogFilesCount != count {
			server.BinaryLogFilesCount = count
			writeMeta = true
		}
		if server.BinaryLogFileOldest != oldest {
			server.BinaryLogFileOldest = oldest
			writeMeta = true
		}
		go server.RefreshBinlogMetadata(oldmeta, writeMeta)
	}

	return err
}

func (server *ServerMonitor) WaitForRefresh() {
	// Wait for binlog refreshed
	waitbinlog := true
	for waitbinlog {
		// Try to refresh if not refreshed
		err := server.RefreshBinaryLogs()
		if err != nil && err.Error() == "Server is refreshing binlogs" {
			time.Sleep(time.Second)
		} else {
			waitbinlog = false
		}
	}
}

func (server *ServerMonitor) RefreshBinlogMetaGoMySQL(meta *dbhelper.BinaryLogMetadata) error {
	var err error
	cluster := server.ClusterGroup
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
	defer syncer.Close()

	streamer, err := syncer.StartSync(mysql.Position{Name: meta.Filename, Pos: 0})
	if err != nil {
		return err
	}

	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cluster.Conf.MonitoringQueryTimeout)*time.Millisecond)
		ev, err := streamer.GetEvent(ctx)
		cancel()

		if err == context.DeadlineExceeded {
			// meet timeout
			return err
		}

		if ev.Header.EventType == replication.FORMAT_DESCRIPTION_EVENT {
			meta.Start = int64(ev.Header.Timestamp)
			ts := time.Unix(meta.Start, 0)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlInfo, "Refreshed oldest timestamp on %s - %s: %s", server.Host+":"+server.Port, meta.Filename, ts.String())
			//Only update once for oldest binlog timestamp
			return nil
		}
	}

	return nil
}

func (server *ServerMonitor) RefreshBinlogMetaMySQL(meta *dbhelper.BinaryLogMetadata) error {
	var err error
	cluster := server.ClusterGroup
	binsrvid := strconv.Itoa(cluster.Conf.CheckBinServerId)

	events, _, err := dbhelper.GetBinlogFormatDesc(server.Conn, meta.Filename)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Error while getting binlog events from oldest master binlog: %s. Err: %s", meta.Filename, err.Error())
		return err
	}

	for _, ev := range events {
		startpos := fmt.Sprintf("%d", ev.Pos)
		endpos := fmt.Sprintf("%d", ev.End_log_pos)

		mysqlbinlogcmd := exec.Command(cluster.GetMysqlBinlogPath(), "--read-from-remote-server", "--server-id="+binsrvid, "--user="+cluster.GetRplUser(), "--password="+cluster.GetRplPass(), "--host="+misc.Unbracket(server.Host), "--port="+server.Port, "--start-position="+startpos, "--stop-position="+endpos, meta.Filename)

		result, err := mysqlbinlogcmd.Output()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Error while extracting timestamp from oldest master binlog: %s. Err: %s", meta.Filename, err.Error())
			return err
		}

		ts, err := server.GetTimestampUsingRegex(string(result))
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "%s. Host: %s - %s", err.Error(), server.Host+":"+server.Port, meta.Filename)
			return err
		}

		meta.Start = ts
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlInfo, "Refreshed oldest timestamp on binary log %s - %s : %s", server.Host+":"+server.Port, meta.Filename, time.Unix(ts, 0).String())

	}

	return err
}

func (server *ServerMonitor) RefreshBinlogMetadata(oldmetamap map[string]dbhelper.BinaryLogMetadata, forceWriteMeta bool) error {
	var err error
	var modified bool
	cluster := server.ClusterGroup

	if server.IsRefreshingBinlogMeta {
		return errors.New("Server is refreshing binlogs meta")
	}

	server.SetInRefreshBinlogMeta(true)
	defer server.SetInRefreshBinlogMeta(false)

	server.BinaryLogFiles.Range(func(k, v any) bool {
		err = nil
		meta := v.(*dbhelper.BinaryLogMetadata)
		readbinlog := true
		if meta.Source == "" {
			meta.Source = server.URL
			modified = true
		}

		if meta.Start == 0 {
			if old, ok := oldmetamap[k.(string)]; ok {
				if old.Start > 0 {
					meta.Start = old.Start
					readbinlog = false
				}
			}

			if readbinlog {
				if cluster.Conf.BinlogParseMode == config.ConstBackupBinlogTypeGoMySQL {
					err = server.RefreshBinlogMetaGoMySQL(meta)
				} else {
					err = server.RefreshBinlogMetaMySQL(meta)
				}
			}
			modified = true
		}
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "%s. Host: %s - %s", err.Error(), server.Host+":"+server.Port, meta.Filename)
		}

		return true
	})

	if modified || forceWriteMeta {
		server.WriteLocalBinlogMetadata()
	}

	return err
}

// This process is detached so it will not blocking if waiting
func (server *ServerMonitor) CheckBinaryLogs(force bool) error {
	cluster := server.ClusterGroup
	var err error

	//Don't check binlog of the ignored servers
	if server.IsIgnored() {
		err = errors.New("Server is ignored")
		return err
	}

	if server.BinaryLogFilesCount == 0 {
		server.WaitForRefresh()
	}

	// If log has been rotated
	if (server.BinaryLogFilePrevious != "" && server.BinaryLogFilePrevious != server.BinaryLogFile) || force {
		// Always running, triggered by binlog rotation
		if cluster.Conf.BinlogRotationScript != "" && server.IsMaster() {
			cluster.BinlogRotationScript(server)
		}

		//Don't do anything while failover
		if cluster.Conf.BackupBinlogs && !cluster.IsInFailover() {
			//Set second parameter to false, not part of backupbinlogpurge
			server.InitiateJobBackupBinlog(server.BinaryLogFilePrevious, false)
			//Initiate purging backup binlog
			go server.JobBackupBinlogPurge(server.BinaryLogFilePrevious)
		}

		server.RefreshBinaryLogs()
	} else {
		nodebinlogcount, err := dbhelper.CountBinaryLogs(server.Conn, server.DBVersion)
		if err != nil {
			return err
		}

		if server.BinaryLogFilesCount != nodebinlogcount {
			server.RefreshBinaryLogs()
		}
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
	} else if server.BinaryLogFilesCount > 2 {
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

func (server *ServerMonitor) GetTimestampUsingRegex(str string) (int64, error) {
	var regex, err = regexp.Compile(`[0-9]{6}[ ]{1,2}[0-9:]{7,8}`)
	if err != nil {
		return 0, errors.New("Incorrect regexp.")
	}

	//Get First Timestamp From Binlog Format Desc and remove multiple space
	strin := strings.Replace(regex.FindString(str), "  ", " ", -1)
	if strin == "" {
		return 0, errors.New("Timestamp not found on binlog")
	}

	strout := strings.Split(strin, " ")

	dt := strout[0]
	yy, err := strconv.Atoi(dt[:2])
	if err != nil {
		return 0, errors.New("Failed to parse year")
	}
	mm, err := strconv.Atoi(dt[2:4])
	if err != nil {
		return 0, errors.New("Failed to parse month")
	}
	dd, err := strconv.Atoi(dt[4:])
	if err != nil {
		return 0, errors.New("Failed to parse date of month")
	}

	tm := strings.Split(strout[1], ":")
	hr, err := strconv.Atoi(tm[0])
	if err != nil {
		return 0, errors.New("Failed to parse hour")
	}
	min, err := strconv.Atoi(tm[1])
	if err != nil {
		return 0, errors.New("Failed to parse minute")
	}
	sec, err := strconv.Atoi(tm[2])
	if err != nil {
		return 0, errors.New("Failed to parse second")
	}

	//4 digit hack
	now := time.Now()
	twodigit, _ := strconv.Atoi(now.Format("06"))
	yy = (now.Year() - twodigit) + yy

	//4 digit hack prevent wrong year
	if yy > now.Year() {
		yy = yy - 100
	}

	return time.Date(yy, time.Month(mm), dd, hr, min, sec, 0, time.Local).Unix(), nil
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

	if server.BinaryLogFilesCount == 0 {
		server.WaitForRefresh()
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

	if cluster.SlavesOldestMasterFile.Prefix != prefix {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Purge cancelled, master binlog file has different prefix")
		return
	}

	if suffix < cluster.SlavesOldestMasterFile.Suffix {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Purge cancelled because of inconsistency, slaves master filename is bigger than master binlog")
		return
	}

	//Purge binlog on restore
	if cluster.Conf.ForceBinlogPurgeOnRestore && server.IsReseeding {

		filename := fmt.Sprintf("%s.%06d", cluster.SlavesOldestMasterFile.Prefix, cluster.SlavesOldestMasterFile.Suffix-1)
		//Only purge if master has more than 2 files
		if server.BinaryLogFilesCount > 2 && server.BinaryLogFileOldest < filename {
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
		var totalSize, maxSize uint = 0, uint(cluster.Conf.ForceBinlogPurgeTotalSize) * (1024 * 1024 * 1024)
		var until = ""

		// DESC for setting the boundary
		binlogs := server.BinaryLogFiles.GetKeysDesc()
		for _, fname := range binlogs {
			meta := server.BinaryLogFiles.Get(fname)
			if meta == nil {
				// Exit on invalid binlog
				return
			}
			totalSize = totalSize + meta.Size
			if totalSize > maxSize {
				break
			}

			until = meta.Filename
		}

		if until != "" {
			// ASC for purging oldest
			binlogs := server.BinaryLogFiles.GetKeys()
			slavesMasterFile := fmt.Sprintf("%s.%06d", cluster.SlavesOldestMasterFile.Prefix, cluster.SlavesOldestMasterFile.Suffix)
			if until > slavesMasterFile {
				until = slavesMasterFile
				cluster.SetState("WARN0107", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0107"], cluster.SlavesOldestMasterFile.Prefix, cluster.SlavesOldestMasterFile.Suffix), ErrFrom: "CHECK", ServerUrl: server.URL})
			}

			idxUntil := slices.Index(binlogs, until)

			if idxUntil == -1 {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "Purge cancelled because of inconsistency, binlog filename %s not found.", until)
				return
			}

			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlInfo, "Purging binlog of %s until %s. ", server.URL, until)
			server.PurgeBinlogTo(until)
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
		if server.BinaryLogOldestTimestamp > 0 && master.BinaryLogOldestTimestamp > server.BinaryLogPurgeBefore && server.BinaryLogFilesCount > 2 {
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

func (server *ServerMonitor) ReadLocalBinaryLogMetadata() (map[string]dbhelper.BinaryLogMetadata, error) {
	filename := server.GetDatabaseBasedir() + "/binary-logs.meta.json"
	_, err := os.Stat(filename)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	meta := make(map[string]dbhelper.BinaryLogMetadata)
	err = json.NewDecoder(file).Decode(&meta)
	if err != nil {
		return nil, err
	}

	return meta, nil
}

func (server *ServerMonitor) WriteLocalBinlogMetadata() {
	cluster := server.GetCluster()
	filename := server.GetDatabaseBasedir() + "/binary-logs.meta.json"

	bjson, err := server.BinaryLogFiles.MarshalIndent("", "\t")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to marshall metadata for binary logs in %s: %s", server.URL, err.Error())
	}

	info := "Created metadata for binary logs on %s"
	if _, err := os.Stat(filename); err == nil {
		info = "Updated metadata for binary logs on %s"
	}

	err = os.WriteFile(filename, bjson, 0644)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to write metadata for binary logs in %s: %s", server.URL, err.Error())
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, info, server.URL)
	}
}
