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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	gzip "github.com/klauspost/pgzip"
	dumplingext "github.com/pingcap/dumpling/v4/export"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/misc"
	river "github.com/signal18/replication-manager/utils/river"
	"github.com/signal18/replication-manager/utils/splitdump"
	"github.com/signal18/replication-manager/utils/state"
)

func (server *ServerMonitor) JobBackupPhysical() error {
	return server.JobBackupPhysicalWithOptions(BackupRunOptions{})
}

func (server *ServerMonitor) AfterJobProcess(conn *sqlx.Conn, task DBTask) error {
	//Still use done=1 and state=3 to prevent unwanted changes
	query := "UPDATE replication_manager_schema.jobs SET result=CONCAT(result,'%s'), state=%d WHERE id=%d AND done=1 AND state=3"
	errStr := ""
	cluster := server.ClusterGroup
	if task.task == "" {
		return errors.New("Cannot check task. Task name is empty!")
	}

	switch task.task {
	case config.ConstBackupPhysicalTypeXtrabackup, config.ConstBackupPhysicalTypeMariaBackup:
		if server.LastBackupMeta.Physical != nil && !server.LastBackupMeta.Physical.IsAdhoc() {
			server.SetBackupPhysicalCookie(task.task)
		}
		if server.LastBackupMeta.Physical != nil {
			server.LastBackupMeta.Physical.Completed = true
		}
		errStr = "Backup completed"
	case "reseedxtrabackup", "reseedmariabackup", "flashbackxtrabackup", "flashbackmariabackup":
		if server.HasWaitResticReseedCookie() {
			if err := server.DelWaitResticReseedCookie(); err != nil {
				if cluster != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn,
						"Failed to clear restic reseed cookie after job completion for %s: %s", task.task, err)
				}
			}
		}
		defer server.cleanupResticReseedForTask(task.task, "job-finished")
		if server.HasReseedingState(task.task) {
			defer server.SetInReseedBackup("")
		}
		if !server.PointInTimeMeta.IsInPITR {
			if _, err := server.StartSlave(); err != nil {
				errStr = err.Error()
				// Only set as failed if no error connection
				if server.Conn != nil {
					// Set state as 6 to differ post-job error with in-job error (code: 5)
					server.ConnExecQueryWithTimeout(conn, JobTimeout, fmt.Sprintf(query, "\n"+errStr, JobStateErrorAfter, task.id))
				}
				return err
			}
		}
	}
	server.ConnExecQueryWithTimeout(conn, JobTimeout, fmt.Sprintf(query, errStr, JobStateSuccess, task.id))
	return nil
}

func (server *ServerMonitor) JobBackupPhysicalWithOptions(opts BackupRunOptions) error {
	//server can be nil as no dicovered master
	if server == nil {
		return nil
	}

	if server.IsDown() {
		return nil
	}

	cluster := server.ClusterGroup
	backupLine := server.resolveBackupLine(opts)
	isAdhoc := backupLine == backupmgr.BackupLineAdhoc
	resticEnabled := server.shouldRunRestic(opts)

	if cluster.IsInBackup() {
		cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Physical", cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		time.Sleep(1 * time.Second)

		return server.JobBackupPhysicalWithOptions(opts)
	}

	cluster.SetInPhysicalBackupState(true)

	// CRITICAL FIX: For physical backups, Restic transition happens in JobFinishReceiveFile
	// We DON'T set InResticPhysicalBackup here because the backup hasn't completed yet
	// The flag will be set atomically in AfterJobProcess when the backup file is received

	// Prevent backing up with incompatible tools
	if server.IsMariaDB() && server.DBVersion.GreaterEqual("10.1") && cluster.Conf.BackupPhysicalType == "xtrabackup" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Master %s MariaDB version is greater than 10.1. Changing from xtrabackup to mariabackup as physical backup tools", server.URL)
		cluster.Conf.BackupPhysicalType = config.ConstBackupPhysicalTypeMariaBackup
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive physical backup %s (%s line) request for server: %s", cluster.Conf.BackupPhysicalType, backupLine, server.URL)

	now := time.Now()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Physical backup %s started at %s for: %s", cluster.Conf.BackupPhysicalType, now.Format(time.RFC3339), server.URL)
	var port string
	var err error
	var backupext string = ".xbtream"
	var dest string = server.GetMyBackupDirectory() + cluster.Conf.BackupPhysicalType
	if isAdhoc {
		dest = fmt.Sprintf("%s.%d", dest, now.Unix())
	}
	if cluster.Conf.CompressBackups {
		backupext = backupext + ".gz"
		dest = dest + backupext
		if cluster.Conf.BackupKeepUntilValid && !isAdhoc {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rename previous backup to .old")
			exec.Command("mv", dest, dest+".old").Run()
		}
		port, err = cluster.SSTRunReceiverToGZip(server, dest, ConstJobCreateFile, cluster.Conf.BackupPhysicalType)
	} else {
		dest = dest + backupext
		if cluster.Conf.BackupKeepUntilValid && !isAdhoc {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rename previous backup to .old")
			exec.Command("mv", dest, dest+".old").Run()
		}
		port, err = cluster.SSTRunReceiverToFile(server, dest, ConstJobCreateFile, cluster.Conf.BackupPhysicalType)
	}

	if err != nil {
		cluster.SetInPhysicalBackupState(false)
		return nil
	}

	// Reset last backup meta
	var prevId int64
	if !isAdhoc {
		prev := cluster.BackupMetaMap.GetPreviousBackup(cluster.Conf.BackupPhysicalType, server.URL)
		if prev != nil {
			prevId = prev.Id
		}
	}

	// Check for previous backup size
	if cluster.Conf.BackupCheckFreeSpace {
		err = cluster.CheckEstimatedBackupSize("physical")
		if err != nil {
			return err
		}
	}

	// Remove from backup list, since the file will be replaced
	if !cluster.Conf.BackupKeepUntilValid && !isAdhoc {
		cluster.BackupMetaMap.Delete(prevId)
	}

	server.LastBackupMeta.Physical = &backupmgr.BackupMetadata{
		Id:                now.Unix(),
		StartTime:         now,
		BackupMethod:      backupmgr.BackupMethodPhysical,
		BackupStrategy:    backupmgr.BackupStrategyFull,
		BackupTool:        cluster.Conf.BackupPhysicalType,
		Source:            server.URL,
		Dest:              dest,
		Compressed:        cluster.Conf.CompressBackups,
		Previous:          prevId,
		BackupLine:        backupLine,
		RetentionDays:     opts.RetentionDays,
		RetentionDuration: strings.TrimSpace(opts.RetentionDuration),
		ResticEnabled:     resticEnabled,
	}
	server.ensureBackupSessionID(server.LastBackupMeta.Physical, backupmgr.BackupMethodPhysical, now, backupLine)

	cluster.BackupMetaMap.Set(server.LastBackupMeta.Physical.Id, server.LastBackupMeta.Physical)

	_, err = server.JobInsertTask(cluster.Conf.BackupPhysicalType, port, cluster.Conf.MonitorAddress)
	if err != nil {
		cluster.SetInPhysicalBackupState(false)
	}

	return err
}

func (server *ServerMonitor) JobReseedPhysicalBackup(backtype string) error {
	cluster := server.ClusterGroup
	if backtype == "default" {
		backtype = cluster.Conf.BackupPhysicalType
	}

	// Prevent reseed with incompatible tools
	if server.IsMariaDB() && server.DBVersion.GreaterEqual("10.1") && backtype == "xtrabackup" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Node %s MariaDB version is greater than 10.1 and not compatible with xtrabackup. Cancelling reseed for data safety.", server.URL)
		return fmt.Errorf("Node %s MariaDB version is greater than 10.1 and not compatible with xtrabackup.", server.URL)
	}

	if !cluster.IsDiscovered() {
		return errors.New("Cluster not discovered yet")
	}

	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found. Cancel reseed physical backup")
	}

	useMaster := true
	backupext := ".xbtream"
	if cluster.Conf.CompressBackups {
		backupext = backupext + ".gz"
	}

	file := backtype + backupext
	backupfile := master.GetMyBackupDirectory() + file

	bckserver := cluster.GetBackupServer()
	if bckserver != nil && bckserver.HasBackupTypeCookie(backtype) {
		if _, err := os.Stat(bckserver.GetMyBackupDirectory() + file); err == nil {
			backupfile = bckserver.GetMyBackupDirectory() + file
			useMaster = false
		} else {
			//Remove false cookie
			bckserver.DelBackupTypeCookie(backtype)
		}
	}

	if useMaster {
		if _, err := os.Stat(backupfile); err != nil {
			//Remove false cookie
			master.DelBackupTypeCookie(backtype)
			return fmt.Errorf("Cancelling reseed. No backup file found on master for %s", backtype)
		}
		bckserver = master
	}

	err := cluster.CheckPhysicalBackupToolVersion(bckserver)
	if err != nil && cluster.Conf.BackupRestoreVersionStrict {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s version is not compatible with restore version on %s. Cancelling reseed for data safety.", backtype, server.URL)
		return fmt.Errorf("Node %s backup tool version is not compatible with restore version.", server.URL)
	}

	//Delete wait physical backup cookie
	server.DelWaitPhysicalBackupCookie()

	task := "reseed" + backtype
	if ok, currentTask := server.TrySetInReseedBackup(task); !ok {
		err := fmt.Errorf("Server is in reseeding state by %s", currentTask)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Concurrent reseed blocked: server %s already reseeding with %s, cannot start %s", server.URL, currentTask, task)
		return err
	}

	// If reset failed, better to stop PITR
	if server.PointInTimeMeta.IsInPITR {
		server.StopSlave()
		_, err := server.ResetSlave()
		if err != nil {
			if mysqlErr, ok := err.(*mysql.MySQLError); ok && mysqlErr.Number != 1617 {
				if server.HasReseedingState(task) {
					server.SetInReseedBackup("")
				}
				return err
			}
		}
		server.SetState(stateUnconn)

		cluster.Conf.BackupPhysicalType = backtype
	}

	_, err = server.JobInsertTask(task, server.SSTPort, cluster.Conf.MonitorAddress)
	if err != nil {
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Receive reseed physical backup %s request for server: %s %s", backtype, server.URL, err)
		return err
	}

	// Set replication master to current master if not PITR
	if !server.PointInTimeMeta.IsInPITR {
		logs, err := server.StopSlave()
		if err != nil {
			cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed stop slave on server: %s %s", server.URL, err)
		}

		logs, err = cluster.pointSlaveToMasterWithMode(server, "SLAVE_POS")
		if err != nil {
			cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Reseed can't changing master for physical backup %s request for server: %s %s", backtype, server.URL, err)
			return err
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive reseed physical backup %s request for server: %s", backtype, server.URL)

	return nil
}

func (server *ServerMonitor) JobReseedPhysicalBackupWithPayload(backtype, backupPath string, extraPayload map[string]string) error {
	cluster := server.ClusterGroup
	if backtype == "default" {
		backtype = cluster.Conf.BackupPhysicalType
	}
	if backupPath == "" {
		return errors.New("backup path is empty")
	}
	backupfile := backupPath

	// Prevent reseed with incompatible tools
	if server.IsMariaDB() && server.DBVersion.GreaterEqual("10.1") && backtype == "xtrabackup" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Node %s MariaDB version is greater than 10.1 and not compatible with xtrabackup. Cancelling reseed for data safety.", server.URL)
		return fmt.Errorf("Node %s MariaDB version is greater than 10.1 and not compatible with xtrabackup.", server.URL)
	}

	if !cluster.IsDiscovered() {
		return errors.New("Cluster not discovered yet")
	}

	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found. Cancel reseed physical backup")
	}

	err := cluster.CheckPhysicalBackupToolVersion(master)
	if err != nil && cluster.Conf.BackupRestoreVersionStrict {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s version is not compatible with restore version on %s. Cancelling reseed for data safety.", backtype, server.URL)
		return fmt.Errorf("Node %s backup tool version is not compatible with restore version.", server.URL)
	}

	//Delete wait physical backup cookie
	server.DelWaitPhysicalBackupCookie()

	task := "reseed" + backtype
	if ok, currentTask := server.TrySetInReseedBackup(task); !ok {
		err := fmt.Errorf("Server is in reseeding state by %s", currentTask)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Concurrent reseed blocked: server %s already reseeding with %s, cannot start %s", server.URL, currentTask, task)
		return err
	}

	// If reset failed, better to stop PITR
	if server.PointInTimeMeta.IsInPITR {
		server.StopSlave()
		_, err := server.ResetSlave()
		if err != nil {
			if mysqlErr, ok := err.(*mysql.MySQLError); ok && mysqlErr.Number != 1617 {
				if server.HasReseedingState(task) {
					server.SetInReseedBackup("")
				}
				return err
			}
		}
		server.SetState(stateUnconn)

		cluster.Conf.BackupPhysicalType = backtype
	}

	// Build comprehensive payload with backup metadata
	payload := map[string]string{
		"backup_path": backupfile,
		"backup_type": backtype,
		"server_url":  server.URL,
		"is_pitr":     fmt.Sprintf("%t", server.PointInTimeMeta.IsInPITR),
	}

	// Merge extra payload if provided
	for k, v := range extraPayload {
		payload[k] = v
	}

	payloadData, err := json.Marshal(payload)
	if err != nil {
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
		return fmt.Errorf("Failed to marshal payload: %v", err)
	}

	_, err = server.JobInsertTaskWithPayload(task, server.SSTPort, cluster.Conf.MonitorAddress, string(payloadData))
	if err != nil {
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Receive reseed physical backup %s request for server: %s %s", backtype, server.URL, err)
		return err
	}

	// Set replication master to current master if not PITR
	if !server.PointInTimeMeta.IsInPITR {
		logs, err := server.StopSlave()
		if err != nil {
			cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed stop slave on server: %s %s", server.URL, err)
		}

		logs, err = cluster.pointSlaveToMasterWithMode(server, "SLAVE_POS")
		if err != nil {
			cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Reseed can't changing master for physical backup %s request for server: %s %s", backtype, server.URL, err)
			return err
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive reseed physical backup %s from path %s request for server: %s", backtype, backupfile, server.URL)

	return nil
}

// JobReseedPhysicalBackupFromPath maintains backward compatibility by calling the new payload-aware function
func (server *ServerMonitor) JobReseedPhysicalBackupFromPath(backtype, backupPath string) error {
	return server.JobReseedPhysicalBackupWithPayload(backtype, backupPath, nil)
}

func (server *ServerMonitor) JobFlashbackPhysicalBackup() error {
	cluster := server.ClusterGroup

	if !cluster.IsDiscovered() {
		return errors.New("Cluster not discovered yet")
	}

	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found. Cancel flashback physical backup")
	}

	useSelfBackup := true
	backupext := ".xbtream"
	if cluster.Conf.CompressBackups {
		backupext = backupext + ".gz"
	}

	file := cluster.Conf.BackupPhysicalType + backupext
	backupfile := server.GetMyBackupDirectory() + file

	bckserver := cluster.GetBackupServer()
	if bckserver != nil && bckserver.HasBackupTypeCookie(cluster.Conf.BackupPhysicalType) {
		if _, err := os.Stat(bckserver.GetMyBackupDirectory() + file); err == nil {
			backupfile = bckserver.GetMyBackupDirectory() + file
			useSelfBackup = false
		} else {
			//Remove false cookie
			bckserver.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
		}
	}

	if useSelfBackup {
		if _, err := os.Stat(backupfile); err != nil {
			//Remove false cookie
			server.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
			return fmt.Errorf("Cancelling flashback. No backup file found on master for %s", cluster.Conf.BackupPhysicalType)
		}
	}

	//Delete wait physical backup cookie
	server.DelWaitPhysicalBackupCookie()

	task := "flashback" + cluster.Conf.BackupPhysicalType
	if ok, currentTask := server.TrySetInReseedBackup(task); !ok {
		err := fmt.Errorf("Server is in reseeding state by %s", currentTask)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Concurrent reseed blocked: server %s already reseeding with %s, cannot start %s", server.URL, currentTask, task)
		return err
	}

	_, err := server.JobInsertTask(task, server.SSTPort, cluster.Conf.MonitorAddress)
	if err != nil {
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
		return err
	}

	logs, err := server.StopSlave()
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed stop slave on server: %s %s", server.URL, err)
	}

	logs, err = cluster.pointSlaveToMasterWithMode(server, "SLAVE_POS")
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Flashback can't changing master for physical backup %s request for server: %s %s", cluster.Conf.BackupPhysicalType, server.URL, err)
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive flashback physical backup %s request for server: %s", cluster.Conf.BackupPhysicalType, server.URL)

	return nil
}

func (server *ServerMonitor) JobReseedLogicalBackup(backtype string) error {
	var err error
	cluster := server.ClusterGroup
	if backtype == "default" {
		backtype = cluster.Conf.BackupLogicalType
	}
	task := "reseed" + backtype

	if !cluster.IsDiscovered() {
		return errors.New("Cluster not discovered yet")
	}

	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found")
	}

	if _, err := os.Stat(cluster.GetMysqlclientPath()); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "File does not exist %s", cluster.GetMysqlclientPath())
		return err
	}

	if backtype == config.ConstBackupLogicalTypeMydumper && cluster.VersionsMap.Get("mydumper") == nil {
		return errors.New("No mydumper version found")
	}

	useMaster := true
	source := master
	var dest string
	var destCandidates []string
	switch backtype {
	case config.ConstBackupLogicalTypeMysqldump, "script":
		dest = "mysqldump.sql.gz"
		destCandidates = []string{"mysqldump.sql.gz", "splitdump"}
	case config.ConstBackupLogicalTypeMydumper:
		dest = "mydumper"
	case config.ConstBackupLogicalTypeDumpling:
		dest = "dumpling"
	}
	if len(destCandidates) == 0 {
		destCandidates = []string{dest}
	}

	var backupfile string
	bckserver := cluster.GetBackupServer()
	if bckserver != nil && bckserver.HasBackupTypeCookie(backtype) {
		if resolved, ok := resolveLogicalBackupPathFromMeta(bckserver, backtype); ok {
			backupfile = resolved
			useMaster = false
			source = bckserver
		} else if resolved, ok := findExistingBackupPath(bckserver, destCandidates); ok {
			backupfile = resolved
			useMaster = false
			source = bckserver
		} else {
			//Remove false cookie
			bckserver.DelBackupTypeCookie(backtype)
		}
	}

	if backupfile == "" {
		if resolved, ok := resolveLogicalBackupPathFromMeta(master, backtype); ok {
			backupfile = resolved
		} else if resolved, ok := findExistingBackupPath(master, destCandidates); ok {
			backupfile = resolved
		} else {
			backupfile = master.GetMyBackupDirectory() + dest
		}
	}

	if useMaster {
		if _, err := os.Stat(backupfile); err != nil {
			//Remove false cookie
			master.DelBackupTypeCookie(backtype)
			return fmt.Errorf("No backup file found on master for %s", backtype)
		}

		bckserver = master
	}

	err = cluster.CheckLogicalBackupToolVersion(bckserver)
	if err != nil && cluster.Conf.BackupRestoreVersionStrict {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s version is not compatible with restore version on %s. Cancelling reseed for data safety.", backtype, server.URL)
		return fmt.Errorf("Node %s backup tool version is not compatible with restore version.", server.URL)
	}

	if ok, currentTask := server.TrySetInReseedBackup(task); !ok {
		err := fmt.Errorf("Server is in reseeding state by %s", currentTask)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Concurrent reseed blocked: server %s already reseeding with %s, cannot start %s", server.URL, currentTask, task)
		return err
	}
	defer func() {
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
	}()

	//Delete wait logical backup cookie
	server.DelWaitLogicalBackupCookie()

	// If reset failed, better to stop PITR
	if server.PointInTimeMeta.IsInPITR {
		server.StopSlave()
		_, err := server.ResetSlave()
		if err != nil {
			if mysqlErr, ok := err.(*mysql.MySQLError); ok && mysqlErr.Number != 1617 {
				if server.HasReseedingState(task) {
					server.SetInReseedBackup("")
				}
				return err
			}
		}
		server.SetState(stateUnconn)
	}

	if cluster.Conf.MonitorScheduler {
		_, err := server.JobInsertTask(task, "0", cluster.Conf.MonitorAddress)
		if err != nil {
			if server.HasReseedingState(task) {
				server.SetInReseedBackup("")
			}
			return err
		}
	}

	// Set replication master to current master if not PITR
	if !server.PointInTimeMeta.IsInPITR {
		logs, err := server.StopSlave()
		if err != nil {
			cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed stop slave on server: %s %s", server.URL, err)
		}

		if server.DBVersion.IsMySQLOrPercona() {
			if server.HasMySQLGTID() {
				cluster.pointSlaveToMasterWithMode(server, "MASTER_AUTO_POSITION")
			} else {
				cluster.pointSlaveToMasterPositional(server)
			}
		} else {
			cluster.pointSlaveToMasterWithMode(server, "SLAVE_POS")
		}
	}

	server.JobsUpdateState(task, "processing", 1, 0)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive reseed logical backup %s request for server: %s", backtype, server.URL)
	if backtype == config.ConstBackupLogicalTypeMysqldump {
		err = server.reseedMysqldumpWithMetadata(backupfile, cluster.Conf.BackupRestoreMysqlUser && source.LastBackupMeta.Logical != nil && source.LastBackupMeta.Logical.SplitUser, source.LastBackupMeta.Logical)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error reseed %s on %s: %s", backtype, server.URL, err.Error())
			if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
		} else {
			if server.IsSlave && !server.PointInTimeMeta.IsInPITR {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Start slave after dump on %s", server.URL)
				server.StartSlave()
			}

			if e2 := server.JobsUpdateState(task, "Reseed completed", 3, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
		}
	} else if backtype == config.ConstBackupLogicalTypeMydumper {
		err = server.JobReseedMyLoader(backupfile, cluster.Conf.BackupRestoreMysqlUser)
		if err == nil && server.IsSlave && !server.PointInTimeMeta.IsInPITR {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Parsing mydumper metadata ")
			meta, err2 := cluster.JobMyLoaderParseMeta(backupfile)
			if err2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "MyLoader metadata parsing: %s", err2)
				err = err2
			} else {
				// Set GTID position for MariaDB
				if server.IsMariaDB() && server.HaveMariaDBGTID {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Starting slave with mydumper metadata")
					server.ExecQueryNoBinLog("SET GLOBAL gtid_slave_pos='"+meta.BinLogUuid+"'", time.Second)
				}
			}

			if err == nil {
				server.StartSlave()
			}
		}

		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error reseed %s on %s: %s", backtype, server.URL, err.Error())
			if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
		} else {
			if e2 := server.JobsUpdateState(task, "Reseed completed", 3, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
		}
	}
	return err
}

type JobReseedLogicalOptions struct {
	SplitUser *bool
}

func (server *ServerMonitor) JobReseedLogicalBackupFromPath(backtype, backupPath string) error {
	return server.JobReseedLogicalBackupFromPathWithOptions(backtype, backupPath, JobReseedLogicalOptions{})
}

func (server *ServerMonitor) JobReseedLogicalBackupFromPathWithOptions(backtype, backupPath string, opts JobReseedLogicalOptions) error {
	var err error
	cluster := server.ClusterGroup
	if backtype == "default" {
		backtype = cluster.Conf.BackupLogicalType
	}
	if backupPath == "" {
		return errors.New("backup path is empty")
	}
	backupfile := backupPath
	task := "reseed" + backtype

	if !cluster.IsDiscovered() {
		return errors.New("Cluster not discovered yet")
	}

	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found")
	}

	if _, err := os.Stat(cluster.GetMysqlclientPath()); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "File does not exist %s", cluster.GetMysqlclientPath())
		return err
	}

	if backtype == config.ConstBackupLogicalTypeMydumper && cluster.VersionsMap.Get("mydumper") == nil {
		return errors.New("No mydumper version found")
	}

	if err = cluster.CheckLogicalBackupToolVersion(master); err != nil && cluster.Conf.BackupRestoreVersionStrict {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s version is not compatible with restore version on %s. Cancelling reseed for data safety.", backtype, server.URL)
		return fmt.Errorf("Node %s backup tool version is not compatible with restore version.", server.URL)
	}

	if ok, currentTask := server.TrySetInReseedBackup(task); !ok {
		err := fmt.Errorf("Server is in reseeding state by %s", currentTask)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Concurrent reseed blocked: server %s already reseeding with %s, cannot start %s", server.URL, currentTask, task)
		return err
	}
	defer func() {
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
	}()

	//Delete wait logical backup cookie
	server.DelWaitLogicalBackupCookie()

	// If reset failed, better to stop PITR
	if server.PointInTimeMeta.IsInPITR {
		server.StopSlave()
		_, err := server.ResetSlave()
		if err != nil {
			if mysqlErr, ok := err.(*mysql.MySQLError); ok && mysqlErr.Number != 1617 {
				if server.HasReseedingState(task) {
					server.SetInReseedBackup("")
				}
				return err
			}
		}
		server.SetState(stateUnconn)
	}

	if cluster.Conf.MonitorScheduler {
		_, err := server.JobInsertTask(task, "0", cluster.Conf.MonitorAddress)
		if err != nil {
			if server.HasReseedingState(task) {
				server.SetInReseedBackup("")
			}
			return err
		}
	}

	// Set replication master to current master if not PITR
	if !server.PointInTimeMeta.IsInPITR {
		logs, err := server.StopSlave()
		if err != nil {
			cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed stop slave on server: %s %s", server.URL, err)
		}

		if server.DBVersion.IsMySQLOrPercona() {
			if server.HasMySQLGTID() {
				cluster.pointSlaveToMasterWithMode(server, "MASTER_AUTO_POSITION")
			} else {
				cluster.pointSlaveToMasterPositional(server)
			}
		} else {
			cluster.pointSlaveToMasterWithMode(server, "SLAVE_POS")
		}
	}

	server.JobsUpdateState(task, "processing", 1, 0)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive reseed logical backup %s from path %s request for server: %s", backtype, backupfile, server.URL)
	if backtype == config.ConstBackupLogicalTypeMysqldump {
		splitUser := master.LastBackupMeta.Logical != nil && master.LastBackupMeta.Logical.SplitUser
		if opts.SplitUser != nil {
			splitUser = *opts.SplitUser
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
				"Using split-user override=%t for reseed logical backup from path %s on %s", splitUser, backupfile, server.URL)
		}
		err = server.reseedMysqldumpWithMetadata(backupfile, cluster.Conf.BackupRestoreMysqlUser && splitUser, master.LastBackupMeta.Logical)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error reseed %s on %s: %s", backtype, server.URL, err.Error())
			if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
		} else {
			if server.IsSlave && !server.PointInTimeMeta.IsInPITR {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Start slave after dump on %s", server.URL)
				server.StartSlave()
			}

			if e2 := server.JobsUpdateState(task, "Reseed completed", 3, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
		}
	} else if backtype == config.ConstBackupLogicalTypeMydumper {
		err = server.JobReseedMyLoader(backupfile, cluster.Conf.BackupRestoreMysqlUser)
		if err == nil && server.IsSlave && !server.PointInTimeMeta.IsInPITR {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Parsing mydumper metadata ")
			meta, err2 := cluster.JobMyLoaderParseMeta(backupfile)
			if err2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "MyLoader metadata parsing: %s", err2)
				err = err2
			} else {
				// Set GTID position for MariaDB
				if server.IsMariaDB() && server.HaveMariaDBGTID {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Starting slave with mydumper metadata")
					server.ExecQueryNoBinLog("SET GLOBAL gtid_slave_pos='"+meta.BinLogUuid+"'", time.Second)
				}
			}

			if err == nil {
				server.StartSlave()
			}
		}

		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error reseed %s on %s: %s", backtype, server.URL, err.Error())
			if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
		} else {
			if e2 := server.JobsUpdateState(task, "Reseed completed", 3, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
		}
	}
	return err
}

func findExistingBackupPath(server *ServerMonitor, candidates []string) (string, bool) {
	for _, name := range candidates {
		path := server.GetMyBackupDirectory() + name
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	return "", false
}

func resolveLogicalBackupPathFromMeta(server *ServerMonitor, backtype string) (string, bool) {
	if server == nil {
		return "", false
	}
	server.backupMetaMutex.Lock()
	meta := server.LastBackupMeta.Logical
	if meta == nil || !meta.Completed || meta.IsAdhoc() {
		server.backupMetaMutex.Unlock()
		return "", false
	}
	if meta.BackupTool != "" && meta.BackupTool != backtype {
		server.backupMetaMutex.Unlock()
		return "", false
	}
	dest := strings.TrimSpace(meta.Dest)
	server.backupMetaMutex.Unlock()
	if dest == "" {
		return "", false
	}
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(server.GetMyBackupDirectory(), dest)
	}
	if _, err := os.Stat(dest); err != nil {
		return "", false
	}
	return dest, true
}

func isSplitDumpDir(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}
	metadataPath := filepath.Join(path, "metadata")
	if metaInfo, err := os.Stat(metadataPath); err == nil && !metaInfo.IsDir() {
		return true, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".sql.gz") {
			return true, nil
		}
	}
	return false, nil
}

func (server *ServerMonitor) reseedMysqldumpWithSplitdump(backupPath string, restoreUser bool) error {
	isSplit, err := isSplitDumpDir(backupPath)
	if err != nil {
		return err
	}
	if isSplit {
		cluster := server.ClusterGroup
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Splitdump detected at %s; restoring with mysql client", backupPath)
		return server.JobReseedSplitdumpWithMysql(backupPath, restoreUser)
	}
	return server.JobReseedMysqldump(backupPath, restoreUser)
}

func (server *ServerMonitor) reseedMysqldumpWithMetadata(backupPath string, restoreUser bool, meta *backupmgr.BackupMetadata) error {
	if meta != nil {
		if meta.Dest != "" {
			pathsMatch, err := comparePaths(meta.Dest, backupPath)
			if err != nil {
				meta = nil
			} else if !pathsMatch {
				meta = nil
			}
		}
	}
	if meta != nil && (meta.SplitDump || isSplitDumpName(meta.Dest)) {
		cluster := server.ClusterGroup
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Splitdump metadata detected for %s; restoring with mysql client", backupPath)
		return server.JobReseedSplitdumpWithMysql(backupPath, restoreUser)
	}
	return server.reseedMysqldumpWithSplitdump(backupPath, restoreUser)
}

func (server *ServerMonitor) restoreSplitdumpFile(path string) error {
	return server.restoreSplitdumpFileContext(context.Background(), path)
}

func (server *ServerMonitor) restoreSplitdumpFileContext(ctx context.Context, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	var reader io.Reader = file
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer gzReader.Close()
		reader = gzReader
	}

	return server.executeMysqlRestoreContext(ctx, reader)
}

func (server *ServerMonitor) JobReseedSplitdumpWithMysql(backupPath string, restoreUser bool) error {
	cluster := server.ClusterGroup
	if cluster == nil {
		return fmt.Errorf("cluster not available")
	}

	start := time.Now()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Logical restore (splitdump+mysql) started at %s for: %s", start.Format(time.RFC3339), server.URL)

	defer server.SetInReseedBackup("")

	restoreErr := splitdump.Restore(backupPath, splitdump.RestoreOptions{
		Parallel:    cluster.Conf.BackupLogicalLoadThreads,
		RestoreUser: restoreUser,
		Logger: func(level, format string, args ...any) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, level, format, args...)
		},
		Context:                context.Background(),
		RestoreFileWithContext: server.restoreSplitdumpFileContext,
	})
	if restoreErr != nil {
		return restoreErr
	}

	elapsed := time.Since(start).Round(time.Second)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Finish logical restore (splitdump+mysql) in %s (started at %s) for: %s",
		elapsed, start.Format(time.RFC3339), server.URL)
	server.Refresh()

	return nil
}

func comparePaths(left, right string) (bool, error) {
	// Intentionally use Abs+Clean (no EvalSymlinks) so paths can be compared
	// even when the target does not exist yet.
	leftAbs, err := filepath.Abs(left)
	if err != nil {
		return false, err
	}
	rightAbs, err := filepath.Abs(right)
	if err != nil {
		return false, err
	}
	return filepath.Clean(leftAbs) == filepath.Clean(rightAbs), nil
}

func isSplitDumpName(path string) bool {
	base := filepath.Base(path)
	if base == "splitdump" {
		return true
	}
	if !strings.HasPrefix(base, "splitdump.") {
		return false
	}
	suffix := strings.TrimPrefix(base, "splitdump.")
	if suffix == "" {
		return false
	}
	_, err := strconv.ParseInt(suffix, 10, 64)
	return err == nil
}

func (server *ServerMonitor) JobFlashbackLogicalBackup() error {
	var dest, backupfile string
	var err error

	cluster := server.ClusterGroup
	backtype := cluster.Conf.BackupLogicalType
	task := "flashback" + backtype

	// Ensure the cluster is discovered before proceeding
	if !cluster.IsDiscovered() {
		return errors.New("Cluster not discovered yet")
	}

	// Get the master node of the cluster
	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found. Cancel reseed logical backup")
	}

	// Check if the server is already reseeding (atomic check-and-set)
	if ok, currentTask := server.TrySetInReseedBackup(task); !ok {
		err = fmt.Errorf("Server is in reseeding state by %s", currentTask)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Concurrent reseed blocked: server %s already reseeding with %s, cannot start %s", server.URL, currentTask, task)
		return err
	}

	// Decide on backup filename depending on the backup type
	useMaster := true
	source := master
	var destCandidates []string
	switch backtype {
	case config.ConstBackupLogicalTypeMysqldump, "script":
		dest = "mysqldump.sql.gz"
		destCandidates = []string{"mysqldump.sql.gz", "splitdump"}
	case config.ConstBackupLogicalTypeMydumper:
		dest = "mydumper"
	case config.ConstBackupLogicalTypeDumpling:
		dest = "dumpling"
	}
	if len(destCandidates) == 0 {
		destCandidates = []string{dest}
	}

	// Skip file lookup if using custom script
	if backtype != "script" {
		// Construct backup path on master
		backupfile, _ = findExistingBackupPath(master, destCandidates)
		if backupfile == "" {
			backupfile = master.GetMyBackupDirectory() + dest
		}

		// If a backup server has a valid backup for this type, use it instead
		bckserver := cluster.GetBackupServer()
		if bckserver != nil && bckserver.HasBackupTypeCookie(backtype) {
			if resolved, ok := findExistingBackupPath(bckserver, destCandidates); ok {
				backupfile = resolved
				useMaster = false
				source = bckserver
			} else {
				// Remove invalid cookie if file does not exist
				bckserver.DelBackupTypeCookie(backtype)
			}
		}

		// If no valid backup found, abort
		if useMaster {
			if _, err := os.Stat(backupfile); err != nil {
				master.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
				return fmt.Errorf("Cancelling reseed. No backup file found on master for %s", backtype)
			}
		}
	}

	// Reseed state already set atomically above, just add cleanup defer
	defer func() {
		// Clear reseed state if task is still marked
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
	}()

	// Attempt to stop slave replication on the server
	logs, err := server.StopSlave()
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed stop slave on server: %s %s", server.URL, err)
	}

	if server.DBVersion.IsMySQLOrPerconaGreater57() {
		if server.HasMySQLGTID() {
			logs, err = cluster.pointSlaveToMasterWithMode(server, "MASTER_AUTO_POSITION")
		} else {
			logs, err = cluster.pointSlaveToMasterPositional(server)
		}
	} else {
		logs, err = cluster.pointSlaveToMasterWithMode(server, "SLAVE_POS")
	}
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "flashback can't changing master for logical backup %s request for server: %s %s", cluster.Conf.BackupLogicalType, server.URL, err)
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive flashback logical backup %s request for server: %s", backtype, server.URL)

	// If a custom script is configured, use it
	if cluster.Conf.BackupLoadScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Using script from backup-load-script on %s", server.URL)
		server.JobReseedBackupScript()

		// Handle mysqldump-based reseed
	} else if backtype == config.ConstBackupLogicalTypeMysqldump {
		err := server.reseedMysqldumpWithMetadata(backupfile, cluster.Conf.BackupRestoreMysqlUser && source.LastBackupMeta.Logical != nil && source.LastBackupMeta.Logical.SplitUser, source.LastBackupMeta.Logical)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error flashback %s on %s: %s", backtype, server.URL, err.Error())
			if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
		} else {
			// Restart slave if needed
			if server.IsSlave {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Start slave after dump on %s", server.URL)
				server.StartSlave()
			}

			if e2 := server.JobsUpdateState(task, "Flashback completed", 3, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
		}

		// Handle mydumper-based reseed
	} else if backtype == config.ConstBackupLogicalTypeMydumper {
		err := server.JobReseedMyLoader(backupfile, cluster.Conf.BackupRestoreMysqlUser)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error flashback %s on %s: %s", backtype, server.URL, err.Error())
			if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
		} else {
			// Parse metadata from mydumper
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Parsing mydumper metadata ")
			meta, err2 := cluster.JobMyLoaderParseMeta(backupfile)
			if err2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "MyLoader metadata parsing: %s", err2)
				err = err2
			} else {
				// Set GTID position for MariaDB
				if server.IsMariaDB() && server.HaveMariaDBGTID {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Starting slave with mydumper metadata")
					server.ExecQueryNoBinLog("SET GLOBAL gtid_slave_pos='"+meta.BinLogUuid+"'", time.Second)
				}

				if err == nil {
					server.StartSlave()
				}
			}

			if e2 := server.JobsUpdateState(task, "Flashback completed", 3, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
		}
	}

	return nil
}

func (server *ServerMonitor) JobReseedMyLoader(backupdir string, restoreUser bool) error {
	cluster := server.ClusterGroup
	start := time.Now()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical restore (myloader) started at %s for: %s", start.Format(time.RFC3339), server.URL)
	threads := strconv.Itoa(cluster.Conf.BackupLogicalLoadThreads)

	if restoreUser {
		//walk dir
		files, err := os.ReadDir(backupdir)
		if err == nil {
			if len(files) > 0 {
				for _, file := range files {
					if strings.HasPrefix(file.Name(), "mysql.user") && strings.HasSuffix(file.Name(), ".sql.gz.skip") {
						os.Rename(filepath.Join(backupdir, file.Name()), filepath.Join(backupdir, strings.TrimSuffix(file.Name(), ".skip")))
					}
				}
			}
		}
	} else {
		//walk dir
		files, err := os.ReadDir(backupdir)
		if err == nil {
			if len(files) > 0 {
				for _, file := range files {
					if strings.HasPrefix(file.Name(), "mysql.user") && strings.HasSuffix(file.Name(), ".sql.gz") {
						os.Rename(filepath.Join(backupdir, file.Name()), filepath.Join(backupdir, file.Name()+".skip"))
					}
				}
			}
		}
	}

	defer server.SetInReseedBackup("")

	master := cluster.GetMaster()
	if master == nil {
		return fmt.Errorf("No master. Cancel backup reseeding %s", server.URL)
	}

	myargs := cluster.GetMyLoaderCompatibleOptions()
	if server.URL == master.URL {
		myargs = append(myargs, "--enable-binlog")
	}

	myargs = append(myargs, cluster.GetDumpCredentials(server)...)
	myargs = append(myargs, "--directory="+backupdir, "--threads="+threads)
	dumpCmd := exec.Command(cluster.GetMyLoaderPath(), myargs...)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command: %s", strings.ReplaceAll(dumpCmd.String(), "="+cluster.GetDbPass(), "=XXXX"))

	stdoutIn, _ := dumpCmd.StdoutPipe()
	stderrIn, _ := dumpCmd.StderrPipe()
	dumpCmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	wg.Wait()
	if err := dumpCmd.Wait(); err != nil {
		return err
	}
	elapsed := time.Since(start).Round(time.Second)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Finish logical restore (myloader) in %s (started at %s) for: %s", elapsed, start.Format(time.RFC3339), server.URL)
	server.Refresh()

	return nil
}

func (server *ServerMonitor) JobReseedMysqldump(backupfile string, restoreUser bool) error {
	cluster := server.ClusterGroup
	var err error
	defer server.SetInReseedBackup("")

	start := time.Now()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical restore (mysqldump) started at %s for: %s", start.Format(time.RFC3339), server.URL)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Sending logical backup to reseed %s", server.URL)

	server.StopSlave()

	gzfile, err := os.Open(backupfile)
	if err != nil {
		return fmt.Errorf("[%s] Failed opening backup file in backup server for reseed:  %s ", server.URL, err)
	}

	// Use configurable parallel blocks for better performance
	// For restore operations, use higher default (16) for speed, matching original behavior
	parallelBlocks := cluster.getSanitizedParallelBlocks(config.ConstLogModTask)
	fz, err := gzip.NewReaderN(gzfile, cluster.Conf.SSTSendBuffer, parallelBlocks)
	if err != nil {
		return fmt.Errorf("[%s] Failed to unzip backup file in backup server for reseed:  %s ", server.URL, err)
	}
	defer fz.Close()

	cliParams := append(cluster.GetDumpCredentials(server), server.GetSSLClientParam("client")...)
	cliParams = append(cliParams, strings.Split(cluster.Conf.BackupMysqlclientOptions, " ")...)
	clientCmd := exec.Command(cluster.GetMysqlclientPath(), misc.RemoveEmptyString(cliParams)...)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command: %s ", strings.Replace(clientCmd.String(), "="+cluster.GetDbPass(), "=XXXX", -1))

	sql_log_bin := 0
	resetmaster := "RESET MASTER;"
	if server.DBVersion.IsMySQLOrPerconaGreater84() {
		resetmaster = "RESET BINARY LOGS AND GTIDS;"
	}

	master := cluster.GetMaster()
	if master == nil {
		return fmt.Errorf("No master found. Cancel backup reseeding %s", server.URL)
	}
	if server.URL == master.URL {
		sql_log_bin = 1
		resetmaster = ""
	}

	cmdstring := "%sSET sql_log_bin=%d;SET long_query_time=10;"
	if server.DBVersion.IsMySQLOrPerconaGreater84() {
		cmdstring = "%sSET sql_log_bin=%d;SET long_query_time=10;"
	}
	cmdstring = fmt.Sprintf(cmdstring, resetmaster, sql_log_bin)

	var usergzfile io.Reader
	if restoreUser {
		usergzfile, err = server.ReadMysqldumpUser(backupfile)
		if err != nil {
			return fmt.Errorf("Error opening mysql.user file %s", err)
		}

		clientCmd.Stdin = io.MultiReader(bytes.NewBufferString(cmdstring), usergzfile, fz) //Append mysql.user
	} else {
		clientCmd.Stdin = io.MultiReader(bytes.NewBufferString(cmdstring), fz)
	}

	stderr, _ := clientCmd.StdoutPipe()
	clientCmd.Stderr = clientCmd.Stdout

	if err := clientCmd.Start(); err != nil {
		return fmt.Errorf("Can't start mysql client:%s at %s", err, strings.ReplaceAll(clientCmd.String(), "="+cluster.GetDbPass(), "=XXXX"))
	}

	wg := sync.WaitGroup{}
	wg.Add(1)

	go func() {
		defer wg.Done()
		server.copyLogs(stderr, config.ConstLogModBackupStream, config.LvlDbg)
	}()

	wg.Wait()

	err = clientCmd.Wait()
	if err != nil {
		return fmt.Errorf("Error waiting reseed %s at %s", server.URL, err)
	}

	elapsed := time.Since(start).Round(time.Second)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Finish logical restore (mysqldump) in %s (started at %s) for: %s", elapsed, start.Format(time.RFC3339), server.URL)
	return nil
}

func (server *ServerMonitor) ReadMysqldumpUser(backupfile string) (io.Reader, error) {
	cluster := server.ClusterGroup
	var err error

	dir := filepath.Dir(backupfile)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, fmt.Errorf("Directory %s does not exist", dir)
	}

	userpath := filepath.Join(dir, "mysql.user.sql.gz")
	if _, err := os.Stat(userpath); os.IsNotExist(err) {
		return nil, fmt.Errorf("File %s does not exist", userpath)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Opening mysql.user file %s", userpath)

	gzfile, err := os.Open(backupfile)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed opening backup file in backup server for reseed:  %s ", server.URL, err)
	}

	// Use configurable parallel blocks for better performance
	// For restore operations, use higher default (16) for speed, matching original behavior
	parallelBlocks := cluster.getSanitizedParallelBlocks(config.ConstLogModTask)
	fz, err := gzip.NewReaderN(gzfile, cluster.Conf.SSTSendBuffer, parallelBlocks)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to unzip backup file in backup server for reseed:  %s ", server.URL, err)
	}

	return fz, nil
}

// JobReseedBackupScript will execute the backup load script
// The script will be executed with the following parameters:
// 1. Server Host
// 2. Master Host
// 2. Server Port
// 2. Master Port
// 3. User
// 4. Password
// 5. Cluster Name
func (server *ServerMonitor) JobReseedBackupScript() {
	cluster := server.ClusterGroup
	defer server.SetInReseedBackup("")
	start := time.Now()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical restore (script) started at %s for: %s", start.Format(time.RFC3339), server.URL)

	master := cluster.GetMaster()
	if master == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "No master found. Cancel backup reseeding %s", server.URL)
		return
	}
	cmd := exec.Command(cluster.Conf.BackupLoadScript, misc.Unbracket(server.Host), misc.Unbracket(master.Host), server.Port, master.Port, cluster.GetDbUser(), cluster.GetDbPass(), cluster.Name)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command backup load script: %s", strings.Replace(cmd.String(), "="+cluster.GetDbPass(), "=XXXX", 1))

	stdoutIn, _ := cmd.StdoutPipe()
	stderrIn, _ := cmd.StderrPipe()
	cmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	wg.Wait()
	if err := cmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "My reload script: %s", err)
		return
	}
	elapsed := time.Since(start).Round(time.Second)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Finish logical restore (script) in %s (started at %s) for: %s", elapsed, start.Format(time.RFC3339), server.URL)

}

func (server *ServerMonitor) GetMyBackupDirectory() string {
	cluster := server.ClusterGroup
	s3dir := cluster.Conf.WorkingDir + "/" + config.ConstStreamingSubDir + "/" + cluster.Name + "/" + server.Host + "_" + server.Port

	if _, err := os.Stat(s3dir); os.IsNotExist(err) {
		err := os.MkdirAll(s3dir, os.ModePerm)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Create backup path failed: %s", s3dir, err)
		}
	}

	return s3dir + "/"

}

// JobBackupScript execute a backup script
// The script must be able to handle the following parameters:
// 1. DB Server Host
// 2. Master Host
// 3. DB Server Port
// 4. Master Port
// 5. DB User
// 6. DB Password
// 7. Cluster Name
// 8. Destination File Path
func (server *ServerMonitor) JobBackupScript(destination string) error {
	var err error
	cluster := server.ClusterGroup

	defer cluster.SetInLogicalBackupState(false)

	master := cluster.GetMaster()
	if master == nil {
		return fmt.Errorf("No master found. Cancel backup script on %s", server.URL)
	}
	scriptCmd := exec.Command(cluster.Conf.BackupSaveScript, server.Host, master.Host, server.Port, master.Port, cluster.GetDbUser(), cluster.GetDbPass(), cluster.Name, destination)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command: %s", strings.Replace(scriptCmd.String(), cluster.GetDbPass(), "XXXX", -1))
	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()

	wg.Wait()

	if err = scriptCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Backup script error: %s", err)
		return err
	}
	return err
}

// getBackupRegexPatterns returns appropriate regex patterns based on DB version
func (server *ServerMonitor) getBackupRegexPatterns() (*regexp.Regexp, *regexp.Regexp) {
	binlogRegex := regexp.MustCompile(`CHANGE MASTER TO MASTER_LOG_FILE='(.+)', MASTER_LOG_POS=(\d+)`)
	gtidRegex := regexp.MustCompile(`SET GLOBAL gtid_slave_pos='(.+)'`)

	if server.DBVersion.IsMySQLOrPerconaGreater84() {
		binlogRegex = regexp.MustCompile(`CHANGE REPLICATION SOURCE TO SOURCE_LOG_FILE='(.+)', SOURCE_LOG_POS=(\d+)`)
	}
	if server.DBVersion.IsMySQLOrPerconaGreater57() {
		gtidRegex = regexp.MustCompile(`GTID_PURGED\s*=\s*(?:/\*![0-9]*\s*'([^']+)'\*/\s*|'([^']+)')`)
	}

	return binlogRegex, gtidRegex
}

func (server *ServerMonitor) shouldParseDumpBinlogGTID() (bool, bool) {
	parseBinlog := true
	parseGTID := server.IsMariaDB() || server.DBVersion.IsMySQLOrPerconaGreater57()
	server.backupMetaMutex.Lock()
	meta := server.LastBackupMeta.Logical
	hasBinlog := meta != nil && meta.BinLogFileName != ""
	hasGTID := meta != nil && meta.BinLogGtid != ""
	server.backupMetaMutex.Unlock()
	if hasBinlog {
		parseBinlog = false
	}
	if hasGTID {
		parseGTID = false
	}
	return parseBinlog, parseGTID
}

// createGzipWriter creates a gzip writer with configurable compression
func (cluster *Cluster) createGzipWriter(filePath string, logModule int) (*os.File, *gzip.Writer, error) {
	f, err := os.Create(filePath)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, logModule, config.LvlErr,
			"Error creating file %s: %s", filePath, err.Error())
		return nil, nil, err
	}

	compressionLevel := cluster.getSanitizedCompressionLevel(logModule)
	gw, err := gzip.NewWriterLevel(f, compressionLevel)
	if err != nil {
		f.Close()
		cluster.LogModulePrintf(cluster.Conf.Verbose, logModule, config.LvlErr,
			"Error creating gzip writer: %s", err.Error())
		return nil, nil, err
	}

	return f, gw, nil
}

// spawnLogCopier spawns a goroutine to copy logs from reader to cluster logs
func (server *ServerMonitor) spawnLogCopier(wg *sync.WaitGroup, r io.Reader, module int, level string) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		server.copyLogs(r, module, level)
	}()
}

// drainErrorChannel drains all errors from a channel and combines them
func drainErrorChannel(errCh <-chan error) error {
	var combinedErr error
	for err := range errCh {
		if err != nil {
			combinedErr = errors.Join(combinedErr, err)
		}
	}
	return combinedErr
}

type splitDumpPipeline struct {
	teeWriter  io.Writer
	errCh      chan error
	pipeWriter *io.PipeWriter
	wg         *sync.WaitGroup
}

type fallbackWriter struct {
	primary  io.Writer
	fallback io.Writer
	failed   atomic.Bool
}

func (w *fallbackWriter) Write(p []byte) (int, error) {
	if w.failed.Load() {
		return w.fallback.Write(p)
	}
	if w.primary == nil {
		return w.fallback.Write(p)
	}
	n, err := w.primary.Write(p)
	if err != nil {
		w.failed.Store(true)
		return w.fallback.Write(p)
	}
	return n, nil
}

// setupSplitDumpPipeline sets up the splitdump processing pipeline
func (server *ServerMonitor) setupSplitDumpPipeline(
	ctx context.Context,
	outputDir string,
	allowRotate bool,
	cancel context.CancelFunc,
) *splitDumpPipeline {
	cluster := server.ClusterGroup

	splitDumpReader, splitDumpWriter := io.Pipe()
	splitDumpErrCh := make(chan error, 1)
	var splitDumpWG sync.WaitGroup
	splitDumpWG.Add(1)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Splitdump enabled for mysqldump output: %s", outputDir)

	// Splitdump processor goroutine
	go func() {
		defer splitDumpWG.Done()
		defer close(splitDumpErrCh)
		defer splitDumpReader.Close()
		stdoutR, stdoutW := io.Pipe()
		stderrR, stderrW := io.Pipe()

		var logWG sync.WaitGroup
		server.spawnLogCopier(&logWG, stdoutR, config.ConstLogModBackupStream, config.LvlDbg)
		server.spawnLogCopier(&logWG, stderrR, config.ConstLogModBackupStream, config.LvlDbg)

		err := cluster.SplitDumpWithCli(ctx, server, outputDir, allowRotate, splitDumpReader, stdoutW, stderrW)
		stdoutW.Close()
		stderrW.Close()
		logWG.Wait()

		if err != nil {
			splitDumpErrCh <- err
			cancel()
			_ = splitDumpWriter.CloseWithError(err)
		}
	}()

	teeWriter := splitDumpWriter

	return &splitDumpPipeline{
		teeWriter:  teeWriter,
		errCh:      splitDumpErrCh,
		pipeWriter: splitDumpWriter,
		wg:         &splitDumpWG,
	}
}

func (server *ServerMonitor) JobBackupMysqldump(filename string, allowRotate bool) error {
	cluster := server.ClusterGroup
	var err error
	var bckConn *sqlx.DB

	defer cluster.SetInLogicalBackupState(false)

	//Block DDL For Backup
	if server.IsMariaDB() && server.DBVersion.GreaterEqual("10.4") && cluster.Conf.BackupLockDDL {
		bckConn, err = server.GetNewDBConn()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error backup request: %s", err)
		}
		defer bckConn.Close()

		_, err = bckConn.Exec("BACKUP STAGE START")
		if err != nil {
			cluster.LogSQL("BACKUP STAGE START", err, server.URL, "JobBackupLogical", config.LvlWarn, "Failed SQL for server %s: %s ", server.URL, err)
		}
		_, err = bckConn.Exec("BACKUP STAGE BLOCK_DDL")
		if err != nil {
			cluster.LogSQL("BACKUP BLOCK_DDL", err, server.URL, "JobBackupLogical", config.LvlWarn, "Failed SQL for server %s: %s ", server.URL, err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Blocking DDL via BACKUP STAGE")
	}

	// Prepare regex patterns
	binlogRegex, gtidRegex := server.getBackupRegexPatterns()

	var bfile, bgtid string
	var bpos uint64

	dumpCtx, dumpCancel := context.WithCancel(context.Background())
	defer dumpCancel()
	dumpCmd := exec.CommandContext(dumpCtx, cluster.GetMysqlDumpPath(), cluster.GetMysqlDumpOptions(server, server.JobGetDumpGtidParameter())...)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command: %s ", strings.Replace(dumpCmd.String(), "="+cluster.GetDbPass(), "=XXXX", -1))
	// Get the stdout pipe from the command
	stdout, err := dumpCmd.StdoutPipe()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error getting stdout pipe: %s", err)
		fmt.Println()
		return err
	}

	stderrIn, _ := dumpCmd.StderrPipe()

	// Setup output writer (gzip file or splitdump)
	var teeWriter io.Writer
	var splitDumpPipeline *splitDumpPipeline
	var f *os.File
	var gw *gzip.Writer

	if cluster.Conf.BackupMysqldumpSplitDump {
		splitDumpPipeline = server.setupSplitDumpPipeline(dumpCtx, filename, allowRotate, dumpCancel)
		teeWriter = &fallbackWriter{primary: splitDumpPipeline.teeWriter, fallback: io.Discard}
	} else {
		f, gw, err = cluster.createGzipWriter(filename, config.ConstLogModTask)
		if err != nil {
			return err
		}
		defer func() {
			if err := gw.Flush(); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error flushing gzip: %s", err.Error())
			}
			if err := gw.Close(); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error closing gzip: %s", err.Error())
			}
			f.Close()
		}()
		teeWriter = gw
	}

	// Start dump command
	if err := dumpCmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error backup request: %s", err)
		return err
	}

	// Process stdout stream
	teeReader := io.TeeReader(stdout, teeWriter)
	reader := bufio.NewReader(teeReader)
	buffer := make([]byte, cluster.Conf.SSTSendBuffer)
	errCh := make(chan error, 4)

	var wg sync.WaitGroup
	server.spawnLogCopier(&wg, stderrIn, config.ConstLogModBackupStream, config.LvlDbg)

	parseBinlog, parseGTID := server.shouldParseDumpBinlogGTID()
	parser := newDumpStreamParser(
		binlogRegex,
		gtidRegex,
		parseBinlog,
		parseGTID,
		func(file string, pos uint64) {
			server.backupMetaMutex.Lock()
			hasBinlog := server.LastBackupMeta.Logical != nil && server.LastBackupMeta.Logical.BinLogFileName != ""
			server.backupMetaMutex.Unlock()
			if hasBinlog {
				return
			}
			bfile = file
			bpos = pos
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
				"Binlog filename:%s, pos: %s", bfile, strconv.FormatUint(bpos, 10))
		},
		func(gtid string) {
			server.backupMetaMutex.Lock()
			hasGTID := server.LastBackupMeta.Logical != nil && server.LastBackupMeta.Logical.BinLogGtid != ""
			server.backupMetaMutex.Unlock()
			if hasGTID {
				return
			}
			bgtid = gtid
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
				"GTID:%s", bgtid)
		},
	)

	// Main reading goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(errCh)
		errSent := false

		for {
			n, err := reader.Read(buffer)
			if err != nil && err != io.EOF {
				if !errSent {
					errCh <- fmt.Errorf("Error reading buffer: %w", err)
					errSent = true
				}
				break
			}
			if n == 0 {
				break
			}
			if parser.Enabled() {
				parser.Consume(buffer[:n])
			}
		}

		parser.Flush()

		if splitDumpPipeline != nil {
			_ = splitDumpPipeline.pipeWriter.Close()
		}

		if err := dumpCmd.Wait(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "mysqldump: %s", err)
			select {
			case errCh <- fmt.Errorf("mysqldump: %w", err):
			default:
			}
		}
	}()

	wg.Wait()

	// Collect all errors
	var splitDumpErr, readErr error
	if splitDumpPipeline != nil {
		splitDumpPipeline.wg.Wait()
		splitDumpErr = drainErrorChannel(splitDumpPipeline.errCh)
		if splitDumpErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Splitdump error: %s", splitDumpErr)
		}
	}

	readErr = drainErrorChannel(errCh)
	combinedErr := errors.Join(splitDumpErr, readErr)
	if combinedErr != nil {
		return combinedErr
	}

	server.backupMetaMutex.Lock()
	if server.LastBackupMeta.Logical != nil {
		if server.LastBackupMeta.Logical.BinLogGtid == "" && bgtid != "" {
			server.LastBackupMeta.Logical.BinLogGtid = bgtid
		}
		if server.LastBackupMeta.Logical.BinLogFileName == "" && bfile != "" {
			server.LastBackupMeta.Logical.BinLogFileName = bfile
			server.LastBackupMeta.Logical.BinLogFilePos = bpos
		}
	}
	server.backupMetaMutex.Unlock()

	return err
}

func (server *ServerMonitor) JobBackupMysqldumpUser() error {
	cluster := server.ClusterGroup
	var err error

	dir := server.GetMyBackupDirectory()
	userpath := filepath.Join(dir, "mysql.users.sql.gz")

	dumpargs := append(cluster.GetDumpCredentials(server), server.GetSSLClientParam("client-dump")...)
	dumpargs = append(dumpargs, "--insert-ignore", "--system=user")
	dumpCmd := exec.Command(cluster.GetMysqlDumpPath(), dumpargs...)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command: %s ", strings.Replace(dumpCmd.String(), "="+cluster.GetDbPass(), "=XXXX", -1))

	f, gw, err := cluster.createGzipWriter(userpath, config.ConstLogModTask)
	if err != nil {
		return err
	}

	// Buffer before gzip to improve compression
	bw := bufio.NewWriterSize(gw, 64*1024)
	defer func() {
		if err := bw.Flush(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error flushing buffer: %s", err.Error())
		}
		if err := gw.Close(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error closing gzip: %s", err.Error())
		}
		f.Close()
	}()

	dumpCmd.Stdout = gw
	stderrIn, _ := dumpCmd.StderrPipe()

	if err := dumpCmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error backup request: %s", err)
		return err
	}

	var wg sync.WaitGroup
	server.spawnLogCopier(&wg, stderrIn, config.ConstLogModBackupStream, config.LvlDbg)
	wg.Wait()

	err = dumpCmd.Wait()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "mysqldump: %s", err)
	}

	return err
}

func (server *ServerMonitor) JobBackupMyDumper(outputdir string) error {
	cluster := server.ClusterGroup
	var err error
	var bckConn *sqlx.DB

	// Mydumper is fine with split user since we can
	server.LastBackupMeta.Logical.SplitUser = true

	defer cluster.SetInLogicalBackupState(false)

	dumper := cluster.VersionsMap.Get("mydumper")
	if dumper == nil {
		if err = cluster.RefreshMyDumperVersion(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error getting MyDumper version: %s", err)
			return err
		} else {
			dumper = cluster.VersionsMap.Get("mydumper")
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "MyDumper version: %s", dumper.ToString())
		}
	}

	if server.IsMariaDB() && server.DBVersion.GreaterEqual("10.7") && dumper.LowerEqual("0.10.1") {
		cluster.SetState("WARN0133", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0133"], dumper.ToString()), ErrFrom: "JOB"})
		return fmt.Errorf("MyDumper version %s is not compatible with MariaDB 10.7", dumper.ToString())
	}

	//Block DDL For Backup
	if server.IsMariaDB() && server.DBVersion.GreaterEqual("10.4") && dumper.Lower("0.12.3") && cluster.Conf.BackupLockDDL {
		bckConn, err = server.GetNewDBConn()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error backup request: %s", err)
		}
		defer bckConn.Close()

		_, err = bckConn.Exec("BACKUP STAGE START")
		if err != nil {
			cluster.LogSQL("BACKUP STAGE START", err, server.URL, "JobBackupLogical", config.LvlWarn, "Failed SQL for server %s: %s ", server.URL, err)
		}
		_, err = bckConn.Exec("BACKUP STAGE BLOCK_DDL")
		if err != nil {
			cluster.LogSQL("BACKUP BLOCK_DDL", err, server.URL, "JobBackupLogical", config.LvlWarn, "Failed SQL for server %s: %s ", server.URL, err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Blocking DDL via BACKUP STAGE")
	}

	threads := strconv.Itoa(cluster.Conf.BackupLogicalDumpThreads)
	myargs := cluster.GetMyDumperCompatibleOptions()

	// Handle deprecated flags for mydumper >= 0.18.1
	if dumper.GreaterEqual("0.18.1") {
		// Replace deprecated flags with --trx-tables
		var updatedArgs []string
		for _, arg := range myargs {
			if arg == "--less-locking" || arg == "--trx-consistency-only" {
				// Skip deprecated flags
				continue
			}
			updatedArgs = append(updatedArgs, arg)
		}
		// Add --trx-tables if not already present
		hasTrxTablesArg := false
		for _, arg := range updatedArgs {
			if arg == "--trx-tables" || strings.HasPrefix(arg, "--trx-tables=") || arg == "--no-trx-tables" { // Check for both --trx-tables and --no-trx-tables to avoid conflicts
				hasTrxTablesArg = true
				break
			}
		}
		if !hasTrxTablesArg {
			updatedArgs = append(updatedArgs, "--trx-tables")
		}
		myargs = updatedArgs
	}

	if dumper.GreaterEqual("0.15.3") && !slices.Contains(myargs, "--clear") {
		myargs = append(myargs, "--clear")
	}
	myargs = append(myargs, "--outputdir", outputdir, "--threads", threads, "--host", misc.Unbracket(server.Host), "--port", server.Port, "--user", cluster.GetDbUser(), "--password", cluster.GetDbPass())

	if cluster.Conf.BackupMyDumperRegex != "" {
		myargs = append(myargs, "--regex", cluster.Conf.BackupMyDumperRegex)
	}

	dumpCmd := exec.Command(cluster.GetMyDumperPath(), myargs...)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "%s", strings.Replace(dumpCmd.String(), "="+cluster.GetDbPass(), "=XXXX", 1))
	stdoutIn, _ := dumpCmd.StdoutPipe()
	stderrIn, _ := dumpCmd.StderrPipe()
	dumpCmd.Start()

	var wg sync.WaitGroup
	var valid bool = true
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.myDumperCopyLogs(stdoutIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		valid = server.myDumperCopyLogs(stderrIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	wg.Wait()
	if err = dumpCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error mydumper:  %s", err)
		return err
	}
	if !valid {
		return fmt.Errorf("mydumper reported errors in output")
	}

	if e2 := cluster.JobParseMyDumperMeta(server.LastBackupMeta.Logical); e2 != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error parsing mydumper metadata: %s", e2.Error())
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Success backup data via mydumper. Setting logical cookie")
	server.SetBackupLogicalCookie(config.ConstBackupLogicalTypeMydumper)

	return err
}

func (server *ServerMonitor) JobBackupDumpling(outputdir string) error {
	var err error
	cluster := server.ClusterGroup

	defer cluster.SetInLogicalBackupState(false)

	conf := dumplingext.DefaultConfig()
	conf.Database = ""
	conf.Host = misc.Unbracket(server.Host)
	conf.User = cluster.GetDbUser()
	conf.Port, _ = strconv.Atoi(server.Port)
	conf.Password = cluster.GetDbPass()

	conf.Threads = cluster.Conf.BackupLogicalDumpThreads
	conf.FileSize = 1000
	conf.StatementSize = dumplingext.UnspecifiedSize
	conf.OutputDirPath = outputdir
	conf.Consistency = "flush"
	conf.NoViews = true
	conf.StatusAddr = ":8281"
	conf.Rows = dumplingext.UnspecifiedSize
	conf.Where = ""
	conf.EscapeBackslash = true
	conf.LogLevel = config.LvlInfo

	err = dumplingext.Dump(conf)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Dumpling %s", err)
		return err
	}

	return err
}

func (server *ServerMonitor) JobBackupRiver() error {
	var err error
	cluster := server.ClusterGroup

	defer cluster.SetInLogicalBackupState(false)

	cfg := new(river.Config)
	cfg.MyHost = server.URL
	cfg.MyUser = server.User
	cfg.MyPassword = server.Pass
	cfg.MyFlavor = server.DBVersion.Flavor
	cfg.MyVersion = server.DBVersion

	//	cfg.ESAddr = *es_addr
	cfg.StatAddr = "127.0.0.1:12800"
	cfg.DumpServerID = 1001

	cfg.DumpPath = cluster.Conf.WorkingDir + "/" + cluster.Name + "/river"
	cfg.DumpExec = cluster.GetMysqlDumpPath()
	cfg.DumpOnly = true
	cfg.DumpInit = true
	cfg.BatchMode = "CSV"
	cfg.BatchSize = 100000
	cfg.BatchTimeOut = 1
	cfg.DataDir = cluster.Conf.WorkingDir + "/" + cluster.Name + "/river"

	os.RemoveAll(cfg.DumpPath)
	server.LastBackupMeta.Logical.Dest = cfg.DumpPath

	//cfg.Sources = []river.SourceConfig{river.SourceConfig{Schema: "test", Tables: []string{"test", "[*]"}}}
	cfg.Sources = []river.SourceConfig{river.SourceConfig{Schema: "test", Tables: []string{"City"}}}

	_, err = river.NewRiver(cfg)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error river backup: %s", err)
	}

	return err
}

func (server *ServerMonitor) JobBackupLogical() error {
	return server.JobBackupLogicalWithOptions(BackupRunOptions{})
}

func (server *ServerMonitor) JobBackupLogicalWithOptions(opts BackupRunOptions) error {
	var err error
	//server can be nil as no dicovered master
	if server == nil {
		return errors.New("No server defined")
	}

	cluster := server.ClusterGroup
	backupLine := server.resolveBackupLine(opts)
	isAdhoc := backupLine == backupmgr.BackupLineAdhoc
	resticEnabled := server.shouldRunRestic(opts)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Request logical backup %s for: %s", cluster.Conf.BackupLogicalType, server.URL)
	if server.IsDown() {
		return errors.New("Can't backup when server down")
	}

	switch cluster.Conf.BackupLogicalType {
	case config.ConstBackupLogicalTypeMysqldump:
		if _, err := os.Stat(cluster.GetMysqlDumpPath()); os.IsNotExist(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "File does not exist %s", cluster.GetMysqlDumpPath())
			return err
		}
	case config.ConstBackupLogicalTypeMydumper:
		if _, err := os.Stat(cluster.GetMyDumperPath()); os.IsNotExist(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "File does not exist %s", cluster.GetMyDumperPath())
			return err
		}
	}

	var waited bool
	//Wait for previous restic backup
	for cluster.IsInBackup() {
		waited = true
		cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Logical", cluster.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		time.Sleep(1 * time.Second)
	}

	cluster.SetInLogicalBackupState(true)

	// Defer always clears traditional flag; Restic flag cleared by async goroutine
	defer cluster.SetInLogicalBackupState(false)

	if waited {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Resuming logical backup %s after waiting for previous backup to finish for: %s", cluster.Conf.BackupLogicalType, server.URL)
	}

	waited = false
	// Wait for logs job to finish to prevent conflicts with backup metadata updates
	for server.IsInSlowQueryCapture {
		if !waited {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Waiting for logs job to finish before starting backup on %s", server.URL)
			waited = true
		}
		time.Sleep(1 * time.Second)
	}

	// CRITICAL FIX: Set Restic flag AFTER validation passes to prevent orphaned flags
	// The defer clears InLogicalBackup immediately on return, but Restic backup
	// runs async. We must set InResticLogicalBackup NOW to maintain lock continuity.
	resticScheduled := false
	if resticEnabled {
		// Set Restic flag now to ensure atomic transition (no gap where IsInBackup() = false)
		cluster.SetInResticLogicalBackupState(true)
		defer func() {
			if !resticScheduled {
				cluster.SetInResticLogicalBackupState(false)
			}
		}()
	}

	start := time.Now()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical backup %s started at %s for: %s", cluster.Conf.BackupLogicalType, start.Format(time.RFC3339), server.URL)
	var prevId int64
	if !isAdhoc {
		prev := cluster.BackupMetaMap.GetPreviousBackup(cluster.Conf.BackupLogicalType, server.URL)
		if prev != nil {
			prevId = prev.Id
		}
	}

	// Check for previous backup size
	if cluster.Conf.BackupCheckFreeSpace {
		err = cluster.CheckEstimatedBackupSize("logical")
		if err != nil {
			return err
		}
	}

	// Remove from backup list, since the file will be replaced
	if !cluster.Conf.BackupKeepUntilValid && !isAdhoc {
		cluster.BackupMetaMap.Delete(prevId)
	}

	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		Id:                start.Unix(),
		StartTime:         start,
		BackupMethod:      backupmgr.BackupMethodLogical,
		BackupTool:        cluster.Conf.BackupLogicalType,
		BackupStrategy:    backupmgr.BackupStrategyFull,
		Source:            server.URL,
		Previous:          prevId,
		BackupLine:        backupLine,
		RetentionDays:     opts.RetentionDays,
		RetentionDuration: strings.TrimSpace(opts.RetentionDuration),
		ResticEnabled:     resticEnabled,
	}
	server.ensureBackupSessionID(server.LastBackupMeta.Logical, backupmgr.BackupMethodLogical, start, backupLine)

	cluster.BackupMetaMap.Set(server.LastBackupMeta.Logical.Id, server.LastBackupMeta.Logical)

	// Removing previous valid backup state and start
	if !isAdhoc {
		server.DelBackupLogicalCookie()
	}

	if cluster.Conf.BackupSplitMysqlUser {
		server.LastBackupMeta.Logical.SplitUser = true
		err := server.JobBackupMysqldumpUser()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error mysqldump backup request: %s", err.Error())
			return err
		}
	}

	//Skip other type if using backup script
	if cluster.Conf.BackupSaveScript != "" {
		task := "script"
		filename := server.GetMyBackupDirectory() + "mysqldump.sql.gz"
		if isAdhoc {
			filename = fmt.Sprintf("%smysqldump.%d.sql.gz", server.GetMyBackupDirectory(), start.Unix())
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Ad-hoc backup with backup-save-script using unique destination %s", filename)
		}

		// Override backup tool and destination
		server.LastBackupMeta.Logical.BackupTool = task
		server.LastBackupMeta.Logical.Dest = filename

		// Record task for metadata check
		server.JobsUpdateState(task, "", 1, 0)

		err = server.JobBackupScript(filename)
		if err == nil {
			server.JobsUpdateState(task, "Backup completed", 3, 1)
			server.LastBackupMeta.Logical.Completed = true
			if !isAdhoc {
				server.SetBackupLogicalCookie(task)
			}
		} else {
			server.JobsUpdateState(task, err.Error(), 5, 1)
		}
	} else {
		task := cluster.Conf.BackupLogicalType

		if cluster.Conf.MonitorScheduler {
			server.JobInsertTask(task, "0", cluster.Conf.MonitorAddress)
		} else {
			server.JobsUpdateState(task, "", 0, 0)
		}

		//Change to switch since we only allow one type of backup (for now)
		switch cluster.Conf.BackupLogicalType {
		case config.ConstBackupLogicalTypeMysqldump:
			filename, outputdir, dest, compressed := server.resolveMysqldumpDest(isAdhoc, cluster.Conf.BackupMysqldumpSplitDump, start)
			oldV, _ := cluster.GetToolsVersion("client-dump")
			if oldV != nil {
				server.LastBackupMeta.Logical.BackupToolVersion = oldV.ToString()
			}
			server.LastBackupMeta.Logical.Dest = dest
			server.LastBackupMeta.Logical.Compressed = compressed
			server.LastBackupMeta.Logical.SplitDump = cluster.Conf.BackupMysqldumpSplitDump
			if cluster.Conf.BackupKeepUntilValid && !isAdhoc {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rename previous backup to .old")
				if cluster.Conf.BackupMysqldumpSplitDump {
					exec.Command("mv", outputdir, outputdir+".old").Run()
				} else {
					exec.Command("mv", filename, filename+".old").Run()
				}
			}

			allowRotate := cluster.Conf.BackupKeepUntilValid && !isAdhoc
			if cluster.Conf.BackupMysqldumpSplitDump {
				err = server.JobBackupMysqldump(outputdir, allowRotate)
			} else {
				err = server.JobBackupMysqldump(filename, allowRotate)
			}
			if err != nil {
				if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := server.JobsUpdateState(task, "Backup completed", 3, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
				checkPath := filename
				if cluster.Conf.BackupMysqldumpSplitDump {
					checkPath = outputdir
				}
				_, e3 := os.Stat(checkPath)
				if e3 == nil {
					server.LastBackupMeta.Logical.EndTime = time.Now()
					server.LastBackupMeta.Logical.GetSizeAndFileCount()
					server.LastBackupMeta.Logical.Completed = true
					if !isAdhoc {
						server.SetBackupLogicalCookie(config.ConstBackupLogicalTypeMysqldump)
					}
				}
			}
		case config.ConstBackupLogicalTypeDumpling:
			outputdir := server.GetMyBackupDirectory() + "dumpling"
			if isAdhoc {
				outputdir = fmt.Sprintf("%sdumpling.%d", server.GetMyBackupDirectory(), start.Unix())
			}
			oldV, _ := cluster.GetToolsVersion("dumpling")
			if oldV != nil {
				server.LastBackupMeta.Logical.BackupToolVersion = oldV.ToString()
			}
			server.LastBackupMeta.Logical.Dest = outputdir
			if cluster.Conf.BackupKeepUntilValid && !isAdhoc {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rename previous backup to .old")
				exec.Command("mv", outputdir, outputdir+".old").Run()
			}

			err = server.JobBackupDumpling(outputdir + "/")
			if err != nil {
				if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := server.JobsUpdateState(task, "Backup completed", 3, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
				_, e3 := os.Stat(outputdir)
				if e3 == nil {
					server.LastBackupMeta.Logical.EndTime = time.Now()
					server.LastBackupMeta.Logical.GetSizeAndFileCount()
					server.LastBackupMeta.Logical.Completed = true
					if !isAdhoc {
						server.SetBackupLogicalCookie(config.ConstBackupLogicalTypeDumpling)
					}
				}
			}
		case config.ConstBackupLogicalTypeMydumper:
			outputdir := server.GetMyBackupDirectory() + "mydumper"
			if isAdhoc {
				outputdir = fmt.Sprintf("%smydumper.%d", server.GetMyBackupDirectory(), start.Unix())
			}
			oldV, _ := cluster.GetToolsVersion("mydumper")
			if oldV != nil {
				server.LastBackupMeta.Logical.BackupToolVersion = oldV.ToString()
			}
			server.LastBackupMeta.Logical.Dest = outputdir
			server.LastBackupMeta.Logical.Compressed = true
			if cluster.Conf.BackupKeepUntilValid && !isAdhoc {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rename previous backup to .old")
				exec.Command("mv", outputdir, outputdir+".old").Run()
			}
			err = server.JobBackupMyDumper(outputdir + "/")
			if err != nil {
				if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := server.JobsUpdateState(task, "Backup completed", 3, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}

				_, e3 := os.Stat(outputdir)
				if e3 == nil {
					server.LastBackupMeta.Logical.EndTime = time.Now()
					server.LastBackupMeta.Logical.GetSizeAndFileCount()
					server.LastBackupMeta.Logical.Completed = true
					if !isAdhoc {
						server.SetBackupLogicalCookie(config.ConstBackupLogicalTypeMydumper)
					}
				}
			}
		case config.ConstBackupLogicalTypeRiver:
			//No change on river
			err = server.JobBackupRiver()
			if err != nil {
				if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := server.JobsUpdateState(task, "Backup completed", 3, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			}
		}
	}

	server.WriteBackupMetadata(backupmgr.BackupMethodLogical)
	if err == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[SUCCESS] Finish logical backup %s for: %s", cluster.Conf.BackupLogicalType, server.URL)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "[ERROR] Finish logical backup %s for: %s", cluster.Conf.BackupLogicalType, server.URL)
	}
	elapsed := time.Since(start).Round(time.Second)
	backupLogLevel := config.LvlInfo
	if err != nil {
		backupLogLevel = config.LvlWarn
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, backupLogLevel, "Logical backup %s completed in %s (started at %s) for: %s", cluster.Conf.BackupLogicalType, elapsed, start.Format(time.RFC3339), server.URL)

	// Create restic snapshot asynchronously and update metadata when complete
	backtype := "logical"

	// NOTE: InResticLogicalBackup flag already set at function start for atomic transition
	// No need to set it again here - it's already protecting the async operation
	if resticEnabled && err == nil {
		resticPath := server.GetMyBackupDirectory()
		if isAdhoc && server.LastBackupMeta.Logical != nil && server.LastBackupMeta.Logical.Dest != "" {
			resticPath = server.LastBackupMeta.Logical.Dest
		}
		// Note: BackupRestic handles its own error logging and flag clearing if prerequisites fail
		resticScheduled = true
		server.BackupRestic(backupmgr.BackupMethodLogical, true, resticPath, server.BuildResticTags(backtype, cluster.Conf.BackupLogicalType, backupLine, server.LastBackupMeta.Logical)...)
	} else if resticEnabled && err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Skipping restic backup because logical backup failed for: %s", server.URL)
	}

	return nil
}

func (server *ServerMonitor) copyLogs(r io.Reader, module int, level string) {
	cluster := server.ClusterGroup
	//	buf := make([]byte, 1024)
	s := bufio.NewScanner(r)
	for {
		if !s.Scan() {
			break
		} else {
			//Remove empty lines
			if strings.TrimSpace(s.Text()) != "" {
				cluster.LogModulePrintf(cluster.Conf.Verbose, module, level, "[%s] %s", server.Name, s.Text())
			}
		}
	}
}

func (server *ServerMonitor) copyLogsPrefix(r io.Reader, module int, level string, prefix ...string) {
	cluster := server.ClusterGroup
	//	buf := make([]byte, 1024)
	s := bufio.NewScanner(r)
	for {
		if !s.Scan() {
			break
		} else {
			//Remove empty lines
			found := false
			for _, p := range prefix {
				if strings.HasPrefix(s.Text(), p) {
					cluster.LogModulePrintf(cluster.Conf.Verbose, module, level, "[%s] %s", server.Name, strings.TrimPrefix(s.Text(), p))
					found = true
					break
				}
			}

			if !found && strings.Contains(s.Text(), "bash:") {
				cluster.LogModulePrintf(cluster.Conf.Verbose, module, config.LvlWarn, "[%s] %s", server.Name, s.Text()) // Warning for bash error
			}
		}
	}
}

func (server *ServerMonitor) copyTaskDebugLogs(r io.Reader, module int, task string) {
	cluster := server.ClusterGroup
	//	buf := make([]byte, 1024)
	s := bufio.NewScanner(r)
	for {
		if !s.Scan() {
			break
		} else {
			//Remove empty lines
			if strings.TrimSpace(s.Text()) != "" {
				cluster.LogTaskPrintDebug(cluster.Conf.Verbose, module, server.Name+task, "[%s] %s", server.Name, s.Text())
			}
		}
	}
}

func (server *ServerMonitor) myDumperCopyLogs(r io.Reader, module int, level string) bool {
	cluster := server.ClusterGroup
	valid := true
	//	buf := make([]byte, 1024)
	s := bufio.NewScanner(r)
	for {
		if !s.Scan() {
			break
		} else {
			stream := s.Text()
			if strings.Contains(stream, "Error") {
				if !strings.Contains(stream, "#mysql50#") {
					valid = false
				}
				cluster.LogModulePrintf(cluster.Conf.Verbose, module, config.LvlErr, "[%s] %s", server.Name, stream)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, module, level, "[%s] %s", server.Name, stream)
			}
		}
	}
	return valid
}

func (server *ServerMonitor) BackupRestic(backupMethod backupmgr.BackupMethod, updateMetadata bool, backupPath string, tags ...string) {
	cluster := server.ClusterGroup

	// Defensive checks - clear flag and return early if prerequisites not met
	if !cluster.Conf.BackupRestic {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Restic backup called but disabled for %s", server.URL)
		// Clear the flag that might have been set by caller
		switch backupMethod {
		case backupmgr.BackupMethodLogical:
			cluster.SetInResticLogicalBackupState(false)
		case backupmgr.BackupMethodPhysical:
			cluster.SetInResticPhysicalBackupState(false)
		}
		return
	}

	if cluster.ResticManager == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Restic manager not initialized for %s, cannot backup", server.URL)
		// Clear the flag that might have been set by caller
		switch backupMethod {
		case backupmgr.BackupMethodLogical:
			cluster.SetInResticLogicalBackupState(false)
		case backupmgr.BackupMethodPhysical:
			cluster.SetInResticPhysicalBackupState(false)
		}
		return
	}

	if strings.TrimSpace(backupPath) == "" {
		backupPath = server.GetMyBackupDirectory()
	}

	// Check if Restic backup is already in progress for this backup method
	// Note: The flag might already be set by the caller for atomic transition
	if cluster.IsInResticBackupForMethod(backupMethod) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Restic backup flag already set for %s, proceeding with queue", server.URL)
	} else {
		// Set flag if not already set (for binlog backups, etc.)
		switch backupMethod {
		case backupmgr.BackupMethodLogical:
			cluster.SetInResticLogicalBackupState(true)
		case backupmgr.BackupMethodPhysical:
			cluster.SetInResticPhysicalBackupState(true)
		}
	}

	var needpurge bool

	defer cluster.UpdateDiskStat(cluster.GetResticLocalDir())

	if cluster.Conf.BackupCheckFreeSpace {
		diskstat, err := cluster.GetDiskStat(cluster.GetResticLocalDir())
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error getting disk stat for restic backup dir: %s", cluster.GetResticLocalDir())
			cluster.ResticManager.PauseWorkerOnDisk()
		} else {
			// Use specific treshold if defined
			treshold := cluster.Conf.BackupDiskTresholdCrit
			if cluster.Conf.BackupResticPurgeOldestOnDiskThreshold > 0 {
				treshold = cluster.Conf.BackupResticPurgeOldestOnDiskThreshold
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Using specific restic purge treshold %d%%", treshold)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Using global backup disk treshold %d%%", treshold)
			}

			needpurge = diskstat.UsedPercent >= float64(treshold)

			if needpurge {
				if cluster.Conf.BackupResticPurgeOldestOnDiskSpace {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Restic backup disk usage %.2f%% over treshold %d%%. Purging oldest backup before new backup.", diskstat.UsedPercent, treshold)
					cluster.ResticManager.PurgeOldestBackup()
				} else {
					cluster.ResticManager.PauseWorkerOnDisk()
				}
			}
		}
	}

	resticHost := strings.TrimSpace(cluster.Conf.BackupResticHost)
	if strings.EqualFold(resticHost, "default") || strings.EqualFold(resticHost, "none") {
		resticHost = ""
	}

	// Add backup task asynchronously with callback to update metadata
	resultCh := cluster.ResticManager.AddBackupTaskWithCallback(backupPath, tags, resticHost)

	// Launch goroutine to wait for result and update metadata
	go func() {
		// Ensure flag is cleared when goroutine exits
		defer func() {
			switch backupMethod {
			case backupmgr.BackupMethodLogical:
				cluster.SetInResticLogicalBackupState(false)
			case backupmgr.BackupMethodPhysical:
				cluster.SetInResticPhysicalBackupState(false)
			}
		}()

		result := <-resultCh
		if result.Error != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Restic backup failed for %s: %s", server.URL, result.Error)
			return
		}

		if result.SnapshotID != "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Restic backup completed for %s: snapshot %s", server.URL, result.SnapshotID[:8])
			if updateMetadata {
				server.UpdateBackupMetadataWithRestic(backupMethod, result.SnapshotID)
			}
		}
	}()
}

func (server *ServerMonitor) copyAndCapture(w io.Writer, r io.Reader) ([]byte, error) {
	var out []byte
	buf := make([]byte, 1024, 1024)
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			d := buf[:n]
			out = append(out, d...)
			_, err := w.Write(d)
			if err != nil {
				return out, err
			}
		}
		if err != nil {
			// Read returns io.EOF at the end of file, which is not an error for us
			if err == io.EOF {
				err = nil
			}
			return out, err
		}
	}
}

func (server *ServerMonitor) JobBackupBinlog(binlogfile string, isPurge bool) error {
	cluster := server.ClusterGroup
	var err error

	if !server.IsMaster() {
		err = errors.New("Cancelling backup because server is not master")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "%s", err.Error())
		return err
	}
	if cluster.IsInFailover() {
		err = errors.New("Cancel job copy binlog during failover")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "%s", err.Error())
		return err
	}
	if !cluster.Conf.BackupBinlogs {
		err = errors.New("Copy binlog not enable")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "%s", err.Error())
		return err
	}
	if inReseed, task := server.GetReseedingState(); inReseed {
		err = fmt.Errorf("Server is in reseeding state by %s", task)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "%s", err.Error())
		return err
	}

	if _, err := os.Stat(cluster.GetMysqlBinlogPath()); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "File does not exist %s", cluster.GetMysqlBinlogPath())
		return err
	}

	//Skip setting in backup state due to batch purging
	if !isPurge {
		if cluster.IsInBackup() {
			cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Binary Log", cluster.Conf.BinlogCopyMode, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			time.Sleep(1 * time.Second)

			return server.JobBackupBinlog(binlogfile, isPurge)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Initiating backup binlog for %s", binlogfile)
		cluster.SetInBinlogBackupState(true)
		defer cluster.SetInBinlogBackupState(false)
	}

	if cluster.Conf.BackupCheckFreeSpace {
		err = cluster.CheckEstimatedBackupSize("binlog")
		if err != nil {
			return err
		}
	}

	server.SetBackingUpBinaryLog(true)
	defer server.SetBackingUpBinaryLog(false)

	params := append(cluster.GetBinlogCredentials(server), "--read-from-remote-server", "--raw", "--server-id=10000", "--result-file="+server.GetMyBackupDirectory())
	params = append(params, server.GetSSLClientParam("client-binlog")...)
	params = append(params, binlogfile)
	cmdrun := exec.Command(cluster.GetMysqlBinlogPath(), misc.RemoveEmptyString(params)...)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "%s %s", cluster.GetMysqlBinlogPath(), strings.ReplaceAll(strings.Join(cmdrun.Args, " "), "="+cluster.GetRplPass(), "=XXXX"))

	cmdErrPipe, _ := cmdrun.StderrPipe()
	cmdOutPipe, _ := cmdrun.StdoutPipe()

	if err := cmdrun.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Failed mysqlbinlog command: %s at %s", err, strings.Replace(cmdrun.String(), "="+cluster.GetDbPass(), "=XXXX", -1))
		return err
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		server.copyLogs(cmdErrPipe, config.ConstLogModTask, config.LvlErr)
	}()

	go func() {
		defer wg.Done()
		server.copyLogs(cmdOutPipe, config.ConstLogModTask, config.LvlDbg)
	}()

	wg.Wait()

	if err := cmdrun.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Failed to backup binlogs of %s,%s", server.URL, err.Error())
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s %s", cluster.GetMysqlBinlogPath(), strings.ReplaceAll(strings.Join(cmdrun.Args, " "), "="+cluster.GetRplPass(), "=XXXX"))
		return err
	}

	//Skip copying to resting when purge due to batching
	if !isPurge {
		if idx := slices.Index(server.BinaryLogMetaToWrite, binlogfile); idx == -1 {
			server.BinaryLogMetaToWrite = append(server.BinaryLogMetaToWrite, binlogfile)
		}
		server.WriteBackupBinlogMetadata()
		// Backup to restic when no error (defer to prevent unfinished physical copy)
		backtype := "binlog"
		// Use BackupMethodLogical for binlogs (metadata won't be updated, just snapshot created)
		defer server.BackupRestic(backupmgr.BackupMethodLogical, false, server.GetMyBackupDirectory(), server.BuildResticTags(backtype, "", backupmgr.BackupLineDefault, nil)...)
	}

	return nil
}

func (server *ServerMonitor) JobBackupBinlogPurge(binlogfile string) error {
	cluster := server.ClusterGroup
	if !server.IsMaster() {
		return errors.New("Purge only master binlog")
	}
	if !cluster.Conf.BackupBinlogs {
		return errors.New("Copy binlog not enable")
	}

	if cluster.IsInBackup() {
		cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Binary Log", cluster.Conf.BinlogCopyMode, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		time.Sleep(1 * time.Second)

		return server.JobBackupBinlogPurge(binlogfile)
	}

	cluster.SetInBinlogBackupState(true)
	defer cluster.SetInBinlogBackupState(false)

	binlogfilestart, _ := strconv.Atoi(strings.Split(binlogfile, ".")[1])
	prefix := strings.Split(binlogfile, ".")[0]
	binlogfilestop := binlogfilestart - cluster.Conf.BackupBinlogsKeep
	keeping := make(map[string]int)
	for binlogfilestop < binlogfilestart {
		if binlogfilestop > 0 {
			filename := prefix + "." + fmt.Sprintf("%06d", binlogfilestop)
			if _, err := os.Stat(server.GetMyBackupDirectory() + "/" + filename); os.IsNotExist(err) {
				if _, ok := server.BinaryLogFiles.CheckAndGet(filename); ok {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup master missing binlog of %s,%s", server.URL, filename)
					//Set true to skip sending to resting multiple times
					server.InitiateJobBackupBinlog(filename, true)
				}
			}
			keeping[filename] = binlogfilestop
		}
		binlogfilestop++
	}
	files, err := os.ReadDir(server.GetMyBackupDirectory())
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Failed to read backup directory of %s,%s", server.URL, err.Error())
	}

	for _, file := range files {
		_, ok := keeping[file.Name()]
		if strings.HasPrefix(file.Name(), prefix) && !ok {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Purging binlog file from backup dir %s", file.Name())
			if err := os.Remove(server.GetMyBackupDirectory() + "/" + file.Name()); err == nil {
				server.BinaryLogMetaToRemove = append(server.BinaryLogMetaToRemove, file.Name())
			}
		}
	}

	server.WriteBackupBinlogMetadata()
	return nil
}

func (server *ServerMonitor) JobCapturePurge(path string, keep int) error {
	drop := make(map[string]int)

	files, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	i := 0
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "capture") {
			i++
			drop[file.Name()] = i
		}
	}
	for key, value := range drop {

		if value < len(drop)-keep {
			os.Remove(path + "/" + key)
		}

	}
	return nil
}

func (server *ServerMonitor) JobGetDumpGtidParameter() string {
	usegtid := ""
	// MySQL force GTID in server configuration the dump transparently include GTID pos. In MariaDB both positional or GTID is possible and so must be choose at dump
	// Issue #422
	// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask,LvlInfo, "gniac2 %s: %s,", server.URL, server.GetVersion())
	if server.GetVersion().IsMariaDB() {
		if server.HasGTIDReplication() {
			usegtid = "--gtid=true"
		} else {
			usegtid = "--gtid=false"
		}
	}
	return usegtid
}

func (cluster *Cluster) JobRejoinMysqldumpFromSource(source *ServerMonitor, dest *ServerMonitor) error {
	defer dest.SetInReseedBackup("")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rejoining from direct mysqldump from %s", source.URL)

	dest.StopSlave()
	dumpCmd := exec.Command(cluster.GetMysqlDumpPath(), cluster.GetMysqlDumpOptions(source, dest.JobGetDumpGtidParameter())...)
	stderrIn, _ := dumpCmd.StderrPipe()

	cliParams := append(cluster.GetDumpCredentials(dest), dest.GetSSLClientParam("client")...)
	cliParams = append(cliParams, strings.Split(cluster.Conf.BackupMysqlclientOptions, " ")...)

	clientCmd := exec.Command(cluster.GetMysqlclientPath(), misc.RemoveEmptyString(cliParams)...)
	stderrOut, _ := clientCmd.StderrPipe()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command: %s ", strings.Replace(dumpCmd.String(), "="+cluster.GetDbPass(), "=XXXX", -1))

	iodumpreader, _ := dumpCmd.StdoutPipe()

	cmdstring := "RESET MASTER;SET sql_log_bin=0;SET long_query_time=10;"
	if dest.DBVersion.IsMySQLOrPerconaGreater84() {
		cmdstring = "RESET BINARY LOGS AND GTIDS;SET sql_log_bin=0;SET long_query_time=10;"
	}
	clientCmd.Stdin = io.MultiReader(bytes.NewBufferString(cmdstring), iodumpreader)

	if err := dumpCmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Failed mysqldump command: %s at %s", err, strings.Replace(dumpCmd.String(), "="+cluster.GetDbPass(), "=XXXX", -1))
		return err
	}
	if err := clientCmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Can't start mysql client:%s at %s", err, strings.Replace(clientCmd.String(), "="+cluster.GetDbPass(), "=XXXX", -1))
		return err
	}
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		source.copyLogs(stderrIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		dest.copyLogs(stderrOut, config.ConstLogModBackupStream, config.LvlDbg)
	}()

	wg.Wait()

	// Wait for the commands to complete
	if err := dumpCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error waiting for dump client on %s: %s", source.URL, err.Error())
		return err
	}

	if err := clientCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error waiting for db client on %s: %s", dest.URL, err.Error())
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Start slave after dump on %s", dest.URL)
	dest.StartSlave()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Reseed slave from %s to %s finished", source.URL, dest.URL)
	return nil
}

func (server *ServerMonitor) JobBackupBinlogSSH(binlogfile string, isPurge bool) error {
	cluster := server.ClusterGroup
	if !server.IsMaster() {
		return errors.New("Copy only master binlog")
	}
	if cluster.IsInFailover() {
		return errors.New("Cancel job copy binlog during failover")
	}
	if !cluster.Conf.BackupBinlogs {
		return errors.New("Copy binlog not enable")
	}
	if !cluster.Conf.OnPremiseSSH {
		return errors.New("On-premise SSH not enable, cannot backup via SSH")
	}

	//Skip setting in backup state due to batch purging
	if !isPurge {
		if cluster.IsInBackup() {
			cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Binary Log", cluster.Conf.BinlogCopyMode, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			time.Sleep(1 * time.Second)

			return server.JobBackupBinlogSSH(binlogfile, isPurge)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Initiating backup binlog for %s", binlogfile)
		cluster.SetInBinlogBackupState(true)
		defer cluster.SetInBinlogBackupState(false)
	}

	server.SetBackingUpBinaryLog(true)
	defer server.SetBackingUpBinaryLog(false)

	client, err := server.GetCluster().OnPremiseConnect(server)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "OnPremise run job on %s: %s", server.URL, err)
		return err
	}
	defer client.Close()

	remotefile := server.GetBinaryLogDir() + "/" + binlogfile
	localfile := server.GetMyBackupDirectory() + "/" + binlogfile

	fileinfo, err := client.Sftp().Stat(remotefile)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error while getting binlog file [%s] stat:  %s", remotefile, err)
		return err
	}

	err = client.Sftp().Download(remotefile, localfile)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Download binlog error:  %s", err)
		return err
	}

	localinfo, err := os.Stat(localfile)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error while getting backed up binlog file [%s] stat:  %s", localfile, err)
		return err
	}

	if fileinfo.Size() != localinfo.Size() {
		err := errors.New("Remote filesize is different with downloaded filesize")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error while getting backed up binlog file [%s] stat:  %s", localfile, err)
		return err
	}

	//Skip copying to resting when purge due to batching
	if !isPurge {
		if idx := slices.Index(server.BinaryLogMetaToWrite, binlogfile); idx == -1 {
			server.BinaryLogMetaToWrite = append(server.BinaryLogMetaToWrite, binlogfile)
		}
		server.WriteBackupBinlogMetadata()

		// Backup to restic when no error (defer to prevent unfinished physical copy)
		backtype := "binlog"
		// Use BackupMethodLogical for binlogs (metadata won't be updated, just snapshot created)
		defer server.BackupRestic(backupmgr.BackupMethodLogical, false, server.GetMyBackupDirectory(), server.BuildResticTags(backtype, "", backupmgr.BackupLineDefault, nil)...)
	}
	return nil
}

func (server *ServerMonitor) InitiateJobBackupBinlog(binlogfile string, isPurge bool) error {
	cluster := server.ClusterGroup

	switch cluster.Conf.BinlogCopyMode {
	case "client", "mysqlbinlog":
		return server.JobBackupBinlog(binlogfile, isPurge)
	case "ssh":
		return server.JobBackupBinlogSSH(binlogfile, isPurge)
	case "script":
		return cluster.BinlogCopyScript(server, binlogfile, isPurge)
	}

	return errors.New("Wrong configuration for Backup Binlog Method!")
}

func (server *ServerMonitor) WaitAndSendSST(task string, filename string, uncompress bool, loop int) error {
	cluster := server.ClusterGroup

	if !server.HasReseedingState(task) {
		return fmt.Errorf("Server is not in reseeding state on %s", server.URL)
	}

	if server.Conn == nil {
		return fmt.Errorf("No connection pool on %s", server.URL)
	}

	// Use iterative loop instead of recursion to avoid stack buildup
	maxLoop := cluster.Conf.SSTWaitMaxLoop
	retryDelay := time.Second * time.Duration(cluster.Conf.SSTWaitRetryDelay)

	for attempt := loop; attempt < maxLoop; attempt++ {
		conn, err := server.GetConnNoBinlog(server.Conn)
		if err != nil {
			return fmt.Errorf("Error connecting to %s: %s", server.URL, err)
		}

		count, err := server.GetJobCount(conn, task, 2)
		conn.Close()
		if err != nil {
			return fmt.Errorf("Error getting task on %s: %s", server.URL, err)
		}

		// Check if job is ready (state=2 means JobStateHalted, waiting for SST)
		if count > 0 {
			server.JobsUpdateState(task, "processing", 1, 0)
			go func() {
				sendStart := time.Now()
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST send for %s started at %s (file: %s)", task, sendStart.Format(time.RFC3339), filename)
				err := cluster.SSTRunSender(filename, server, uncompress)
				elapsed := time.Since(sendStart).Round(time.Second)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "SST send for %s failed after %s: %s", task, elapsed, err.Error())
					server.JobsUpdateState(task, err.Error(), 5, 0)
					return
				}
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST send for %s completed in %s (started at %s)", task, elapsed, sendStart.Format(time.RFC3339))
			}()
			return nil
		}

		// Wait before retrying (not last iteration)
		if attempt < maxLoop-1 {
			time.Sleep(retryDelay)
		}
	}

	server.JobsUpdateState(task, "Waiting more than max loop", 5, 0)
	server.SetNeedRefreshJobs(true)
	return errors.New("Error: waiting for " + task + " more than max loop.")
}

func (server *ServerMonitor) WaitAndSendSSTStream(ctx context.Context, task string, sourceName string, uncompress bool, loop int, opener SSTStreamOpener) error {
	cluster := server.ClusterGroup

	if ctx == nil {
		ctx = context.Background()
	}

	if opener == nil {
		return fmt.Errorf("SST stream opener is nil")
	}

	if !server.HasReseedingState(task) {
		return fmt.Errorf("Server is not in reseeding state on %s", server.URL)
	}

	if server.Conn == nil {
		return fmt.Errorf("No connection pool on %s", server.URL)
	}

	// Use iterative loop instead of recursion to avoid stack buildup
	// and ensure responsive context cancellation
	maxLoop := cluster.Conf.SSTWaitMaxLoop
	retryDelay := time.Second * time.Duration(cluster.Conf.SSTWaitRetryDelay)

	for attempt := loop; attempt < maxLoop; attempt++ {
		// Check context cancellation at start of each iteration
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("SST stream canceled: %w", err)
		}

		conn, err := server.GetConnNoBinlog(server.Conn)
		if err != nil {
			return fmt.Errorf("Error connecting to %s: %s", server.URL, err)
		}

		count, err := server.GetJobCount(conn, task, 2)
		conn.Close()
		if err != nil {
			return fmt.Errorf("Error getting task on %s: %s", server.URL, err)
		}

		// Check if job is ready (state=2 means JobStateHalted, waiting for SST)
		if count > 0 {
			server.JobsUpdateState(task, "processing", 1, 0)
			go func() {
				sendStart := time.Now()
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST stream send for %s started at %s (source: %s)", task, sendStart.Format(time.RFC3339), sourceName)
				if err := ctx.Err(); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "SST stream for %s canceled before start: %s", task, err)
					server.JobsUpdateState(task, err.Error(), 5, 0)
					return
				}
				err = cluster.SSTRunSenderStream(sourceName, opener, server, uncompress)
				elapsed := time.Since(sendStart).Round(time.Second)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "SST stream send for %s failed after %s: %s", task, elapsed, err.Error())
					server.JobsUpdateState(task, err.Error(), 5, 0)
					return
				}
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST stream send for %s completed in %s (started at %s)", task, elapsed, sendStart.Format(time.RFC3339))
			}()
			return nil
		}

		// Wait before retrying, but allow context cancellation during wait
		select {
		case <-ctx.Done():
			return fmt.Errorf("SST stream canceled: %w", ctx.Err())
		case <-time.After(retryDelay):
			// Continue to next iteration
		}
	}

	server.JobsUpdateState(task, "Waiting more than max loop", 5, 0)
	server.SetNeedRefreshJobs(true)
	return errors.New("Error: waiting for " + task + " more than max loop.")
}

func (server *ServerMonitor) ProcessReseedPhysical(task string) error {
	cluster := server.ClusterGroup
	master := cluster.GetMaster()

	//Prevent multiple reseed
	if !server.HasReseedingState(task) {
		return fmt.Errorf("Server is not in %s state", task)
	}

	if master == nil {
		return errors.New("No master found")
	}

	if master != nil && cluster.Conf.SuperReadOnly && master.URL != server.URL && server.HasSuperReadOnlyCapability() {
		cluster.SetState("WARN0114", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0114"], server.URL), ErrFrom: "JOB"})
		return errors.New("Slave is in super read-only")
	}

	backupType := cluster.Conf.BackupPhysicalType
	payloadBackupPath := ""
	resticSnapshotID := ""
	resticSourcePath := ""
	if taskInfo := server.JobResults.Get(task); taskInfo != nil && taskInfo.Payload != "" {
		payload := make(map[string]string)
		if err := json.Unmarshal([]byte(taskInfo.Payload), &payload); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Invalid payload for %s on %s: %s", task, server.URL, err)
		} else {
			if v := strings.TrimSpace(payload["backup_type"]); v != "" {
				if v != backupType {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Using payload backup type %s for %s (was %s)", v, task, backupType)
				}
				backupType = v
			}
			if v := strings.TrimSpace(payload["backup_path"]); v != "" {
				payloadBackupPath = filepath.Clean(v)
			}
			if v := strings.TrimSpace(payload["restic_snapshot_id"]); v != "" {
				resticSnapshotID = v
			}
			if v := strings.TrimSpace(payload["restic_source_path"]); v != "" {
				resticSourcePath = v
			}
		}
	}

	useMaster := true
	backupext := ".xbtream"
	if cluster.Conf.CompressBackups {
		backupext = backupext + ".gz"
	}

	file := backupType + backupext
	backupfile := master.GetMyBackupDirectory() + file

	if payloadBackupPath != "" {
		if strings.EqualFold(payloadBackupPath, "STREAM") {
			if cluster.ResticManager == nil {
				return fmt.Errorf("Cancelling reseed. Restic manager not available for %s", task)
			}
			if resticSnapshotID == "" || resticSourcePath == "" {
				return fmt.Errorf("Cancelling reseed. Missing restic snapshot metadata for %s", task)
			}

			ctx := context.Background()
			var expectedSize int64 = -1
			if sizeBytes, ok := getSnapshotSizeBytes(cluster, resticSnapshotID); ok {
				expectedSize = int64(sizeBytes)
			}

			streamOpener := func() (io.ReadCloser, int64, error) {
				pr, pw := io.Pipe()
				go func() {
					dumpErr := cluster.ResticManager.DumpSnapshot(resticSnapshotID, resticSourcePath, pw)
					if dumpErr != nil {
						_ = pw.CloseWithError(dumpErr)
						return
					}
					_ = pw.Close()
				}()
				return pr, expectedSize, nil
			}

			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Streaming snapshot %s using dump strategy to SST (file: %s)",
				resticLogSnapshotID(cluster, resticSnapshotID), resticSourcePath)

			uncompress := cluster.shouldUncompressOnSenderForReseed()
			go func() {
				err := server.WaitAndSendSSTStream(ctx, task, resticSourcePath, uncompress, 0, streamOpener)
				if err != nil {
					if server.HasReseedingState(task) {
						server.SetInReseedBackup("")
					}
				}
			}()
			return nil
		}

		backupfile = payloadBackupPath
		if _, err := os.Stat(backupfile); err != nil {
			return fmt.Errorf("Cancelling reseed. Payload backup path not found for %s: %s", task, err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Using payload backup path %s for %s", backupfile, task)
	} else {
		bckserver := cluster.GetBackupServer()
		if bckserver != nil && bckserver.HasBackupTypeCookie(backupType) {
			if _, err := os.Stat(bckserver.GetMyBackupDirectory() + file); err == nil {
				backupfile = bckserver.GetMyBackupDirectory() + file
				useMaster = false
			} else {
				//Remove false cookie
				bckserver.DelBackupTypeCookie(backupType)
			}
		}

		if useMaster {
			if _, err := os.Stat(backupfile); err != nil {
				//Remove false cookie
				master.DelBackupTypeCookie(backupType)
				return fmt.Errorf("Cancelling reseed. No backup file found on master for %s", backupType)
			}
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sending master physical backup to reseed %s", server.URL)

	uncompress := cluster.shouldUncompressOnSenderForReseed()
	go func() {
		err := server.WaitAndSendSST(task, backupfile, uncompress, 0)
		if err != nil {
			if server.HasReseedingState(task) {
				server.SetInReseedBackup("")
			}
		}
	}()

	return nil
}

func (server *ServerMonitor) ProcessFlashbackPhysical(task string) error {

	cluster := server.ClusterGroup
	master := cluster.GetMaster()

	//Prevent multiple reseed
	if !server.HasReseedingState(task) {
		return errors.New("Server is not in physical flashback state")
	}

	if master == nil {
		return errors.New("No master found")
	}

	if master != nil && cluster.Conf.SuperReadOnly && master.URL != server.URL && server.HasSuperReadOnlyCapability() {
		cluster.SetState("WARN0114", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0114"], server.URL), ErrFrom: "JOB"})
		return errors.New("Slave is in super read-only")
	}

	useSelfBackup := true
	backupext := ".xbtream"
	if cluster.Conf.CompressBackups {
		backupext = backupext + ".gz"
	}

	file := cluster.Conf.BackupPhysicalType + backupext
	backupfile := server.GetMyBackupDirectory() + file

	bckserver := cluster.GetBackupServer()
	if bckserver != nil && bckserver.HasBackupTypeCookie(cluster.Conf.BackupPhysicalType) {
		if _, err := os.Stat(bckserver.GetMyBackupDirectory() + file); err == nil {
			backupfile = bckserver.GetMyBackupDirectory() + file
			useSelfBackup = false
		} else {
			//Remove false cookie
			bckserver.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
		}
	}

	if useSelfBackup {
		if _, err := os.Stat(backupfile); err != nil {
			//Remove false cookie
			server.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
			return fmt.Errorf("Cancelling flashback. No backup file found for %s", cluster.Conf.BackupPhysicalType)
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sending physical backup to flashback %s", server.URL)

	uncompress := cluster.shouldUncompressOnSenderForReseed()
	go func() {
		err := server.WaitAndSendSST(task, backupfile, uncompress, 0)
		if err != nil {
			if server.HasReseedingState(task) {
				server.SetInReseedBackup("")
			}
		}
	}()

	return nil
}

func (server *ServerMonitor) WriteBackupMetadata(backtype backupmgr.BackupMethod) {
	// CRITICAL FIX: Lock to prevent concurrent metadata updates from async Restic callbacks
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()

	cluster := server.ClusterGroup
	var lastmeta *backupmgr.BackupMetadata

	defer cluster.UpdateDiskStat(server.GetMyBackupDirectory())

	switch backtype {
	case backupmgr.BackupMethodLogical:
		lastmeta = server.LastBackupMeta.Logical
		defer cluster.CheckLogicalBackupToolVersion(server) // Update backup tool version after backup
	case backupmgr.BackupMethodPhysical:
		lastmeta = server.LastBackupMeta.Physical
		defer cluster.CheckPhysicalBackupToolVersion(server) // Update backup tool version after backup
	default:
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Wrong backup type for metadata in %s", server.URL)
		return
	}
	if lastmeta == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "No metadata available to write for %s", server.URL)
		return
	}
	server.ensureBackupSessionID(lastmeta, backtype, lastmeta.StartTime, lastmeta.BackupLine)

	if _, err := os.Stat(lastmeta.Dest); err == nil {
		lastmeta.GetSizeAndFileCount()
		lastmeta.EndTime = time.Now()
	}
	if strings.TrimSpace(lastmeta.Dest) != "" {
		compressed, err := backupmgr.DetectCompressionFromDest(lastmeta.Dest)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to detect compression for %s: %s", lastmeta.Dest, err)
		} else {
			lastmeta.Compressed = compressed
		}
	}

	task := server.JobResults.Get(lastmeta.BackupTool)

	//Wait until job result changed since we're using pointer
	for task.State < 3 {
		time.Sleep(time.Second)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Continue for writing metadata for backup in %s", server.URL)

	if task.State == 3 || task.State == 4 {
		//Wait for binlog metadata sent by writelog API
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Waiting for binlog info: %v", lastmeta)
		for lastmeta.BinLogFileName == "" {
			time.Sleep(time.Second)
		}
		lastmeta.Completed = true
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Metadata completed: %v", lastmeta)
		cluster.BackupPostScript(server, backtype, lastmeta.Dest)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Error occured in backup, writing incomplete metadata for backup in %s", server.URL)
	}

	bjson, err := json.MarshalIndent(lastmeta, "", "\t")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to marshall metadata for backup in %s: %s", server.URL, err.Error())
	}

	metaPath := server.backupMetaFilePath(lastmeta)
	if metaPath == "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to resolve metadata path for backup in %s", server.URL)
		return
	}
	if lastmeta.MetaFile == "" {
		lastmeta.MetaFile = metaPath
	}

	err = os.WriteFile(metaPath, bjson, 0644)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to write metadata for backup in %s: %s", server.URL, err.Error())
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Created metadata for backup in %s", server.URL)
	}

	//Don't change river
	if cluster.Conf.BackupKeepUntilValid && lastmeta.BackupTool != config.ConstBackupLogicalTypeRiver && !lastmeta.IsAdhoc() {
		if lastmeta.Completed {
			// Delete previous meta with same type
			cluster.BackupMetaMap.Delete(lastmeta.Previous)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup valid, removing old backup.")
			exec.Command("rm", "-r", lastmeta.Dest+".old").Run()
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Error occured in backup, rolling back to old backup.")
			exec.Command("mv", lastmeta.Dest, lastmeta.Dest+".err").Run()
			exec.Command("mv", lastmeta.Dest+".old", lastmeta.Dest).Run()
			exec.Command("rm", "-r", lastmeta.Dest+".err").Run()

			// Revert to previous meta with same type
			cluster.BackupMetaMap.Delete(lastmeta.Id)
			switch backtype {
			case backupmgr.BackupMethodLogical:
				_, server.LastBackupMeta.Logical = server.GetLatestMetaForLine("logical", backupmgr.BackupLineDefault)
			case backupmgr.BackupMethodPhysical:
				_, server.LastBackupMeta.Physical = server.GetLatestMetaForLine("physical", backupmgr.BackupLineDefault)
			}
		}
	}
}

func (cluster *Cluster) CreateTmpClientConfFile() (string, error) {
	confOut, err := os.CreateTemp("", "client.cnf")
	if err != nil {
		return "", err
	}

	if _, err := confOut.Write([]byte("[client]\npassword=" + cluster.GetDbPass() + "\n")); err != nil {
		return "", err
	}
	if err := confOut.Close(); err != nil {
		return "", err
	}
	return confOut.Name(), nil

}

func (server *ServerMonitor) JobFinishReceiveFile(task string) error {
	cluster := server.ClusterGroup

	switch task {
	case "errorlog":
		server.DelWaitErrorlogCookie()
	case "slowquery":
		server.DelWaitSlowqueryCookie()
	case "auditlog":
		server.DelWaitAuditlogCookie()
	case "sqlerrorlog":
		server.DelWaitSqlErrorlogCookie()
	case config.ConstBackupPhysicalTypeXtrabackup, config.ConstBackupPhysicalTypeMariaBackup:
		backtype := "physical"
		server.WriteBackupMetadata(backupmgr.BackupMethodPhysical)
		if server.LastBackupMeta.Physical != nil && !server.LastBackupMeta.Physical.StartTime.IsZero() {
			backupTool := server.LastBackupMeta.Physical.BackupTool
			if backupTool == "" {
				backupTool = cluster.Conf.BackupPhysicalType
			}
			elapsed := time.Since(server.LastBackupMeta.Physical.StartTime).Round(time.Second)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Physical backup %s completed in %s (started at %s) for: %s", backupTool, elapsed, server.LastBackupMeta.Physical.StartTime.Format(time.RFC3339), server.URL)
		}

		// CRITICAL: Transition from traditional backup lock to Restic lock atomically
		// Set Restic flag BEFORE clearing physical backup flag
		resticEnabled := server.LastBackupMeta.Physical != nil && server.LastBackupMeta.Physical.ResticEnabled
		backupCompleted := server.LastBackupMeta.Physical != nil && server.LastBackupMeta.Physical.Completed
		backupLine := backupmgr.BackupLineDefault
		if server.LastBackupMeta.Physical != nil && server.LastBackupMeta.Physical.BackupLine != "" {
			backupLine = server.LastBackupMeta.Physical.BackupLine
		}
		if resticEnabled && backupCompleted {
			cluster.SetInResticPhysicalBackupState(true)
			resticPath := server.GetMyBackupDirectory()
			if server.LastBackupMeta.Physical != nil && server.LastBackupMeta.Physical.IsAdhoc() && server.LastBackupMeta.Physical.Dest != "" {
				resticPath = server.LastBackupMeta.Physical.Dest
			}

			// Create restic snapshot asynchronously and update metadata when complete
			// Note: BackupRestic handles its own error logging and flag clearing if prerequisites fail
			server.BackupRestic(backupmgr.BackupMethodPhysical, true, resticPath, server.BuildResticTags(backtype, cluster.Conf.BackupPhysicalType, backupLine, server.LastBackupMeta.Physical)...)
		} else if resticEnabled && !backupCompleted {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Skipping restic backup because physical backup not completed for: %s", server.URL)
		}

		// Now safe to clear traditional backup flag
		cluster.SetInPhysicalBackupState(false)
	case "printdefault-current":
		filename := filepath.Join(server.Datadir, "current.cnf")
		os.Rename(filename, filename+".old")
		err := server.LoadFromTempConfigFile(filename+".tmp", filename)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Load from temp config error: %s", err)
			return err
		}

		err = server.ReadVariablesFromConfigFile(filename, "deployed", true)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Read variables from config error: %s", err)
			return err
		}
	case "printdefault-dummy":
		filename := filepath.Join(server.Datadir, "dummy.cnf")
		os.Rename(filename, filename+".old")

		err := server.LoadFromTempConfigFile(filename+".tmp", filename)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Load from temp config error: %s", err)
			return err
		}

		err = server.ReadVariablesFromConfigFile(filename, "config", true)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Read variables from config error: %s", err)
		}
		server.IsNeedPathCheck = true
	}
	return nil
}
