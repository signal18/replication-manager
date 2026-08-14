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
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	gzip "github.com/klauspost/pgzip"
	dumplingext "github.com/pingcap/dumpling/v4/export"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	river "github.com/signal18/replication-manager/utils/river"
	"github.com/signal18/replication-manager/utils/splitdump"
	"github.com/signal18/replication-manager/utils/state"
)

var errJobCanceledByUser = errors.New("job canceled by user")

// errServerNotReseeding marks ProcessReseedPhysical/ProcessFlashbackPhysical's
// "not currently reseeding" bail-out so callers can tell it apart from a real
// mid-flight failure. This specific bail fires whenever HasReseedingState is
// already false when the function is entered -- including the case where a
// terminal-job reconciliation (JobsReconcileTerminalSQL) already ran
// AfterJobProcess for this task and cleared the flag on a job that finished
// successfully. Treating that case the same as a genuine failure would relabel
// an already-finished job as JobStateHalted in the runtime cache.
var errServerNotReseeding = errors.New("server is not in reseeding state")

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
	if server == nil {
		return nil
	}

	if server.IsDown() {
		return nil
	}

	cluster := server.ClusterGroup
	if !cluster.waitForBackupSlot() {
		return errors.New("backup canceled: cluster shutting down")
	}

	backupLine := server.resolveBackupLine(opts)
	isAdhoc := backupLine == backupmgr.BackupLineAdhoc
	resticEnabled := server.shouldRunRestic(opts)

	if cluster.IsInBackup() {
		cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Physical", cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		time.Sleep(1 * time.Second)

		return server.JobBackupPhysicalWithOptions(opts)
	}

	cluster.SetInPhysicalBackupState(true)
	cluster.SetState("WARN0073", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0073"], cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})

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
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to insert physical backup task: %s (backup continues via SST)", err)
	}

	return nil
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

type logicalReseedPlan struct {
	task              string
	backtype          string
	backupfile        string
	meta              *backupmgr.BackupMetadata
	splitUser         bool
	splitUserOverride bool
	restoreUser       bool
	skipMetadata      bool
	isPITR            bool
	fromPath          bool
}

func buildLogicalReseedPayload(backtype, backupPath string, splitUser, splitUserOverride, skipMetadata, isPITR bool, serverURL string, userRestore logicalReseedUserRestoreAssessment) (string, error) {
	payload := map[string]string{
		"backup_type":                       strings.TrimSpace(backtype),
		"backup_path":                       strings.TrimSpace(backupPath),
		"split_user":                        fmt.Sprintf("%t", splitUser),
		"split_user_override":               fmt.Sprintf("%t", splitUserOverride),
		"skip_metadata":                     fmt.Sprintf("%t", skipMetadata),
		"is_pitr":                           fmt.Sprintf("%t", isPITR),
		"server_url":                        strings.TrimSpace(serverURL),
		"restore_user_configured":           fmt.Sprintf("%t", userRestore.RestoreUserConfigured),
		"restore_user_effective":            fmt.Sprintf("%t", userRestore.RestoreUserEffective),
		"user_restore_preflight_applicable": fmt.Sprintf("%t", userRestore.Applicable),
		"user_sidecar_checked":              fmt.Sprintf("%t", userRestore.SidecarChecked),
		"user_sidecar_present":              fmt.Sprintf("%t", userRestore.SidecarPresent),
		"user_restore_preflight_message":    userRestore.Message,
	}
	payloadData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("Failed to marshal logical reseed payload: %v", err)
	}
	return string(payloadData), nil
}

// logicalReseedUserRestoreAssessment is preflight-only, informational: it
// never changes what a logical reseed actually restores (that stays entirely
// governed by restoreUser as computed today, and by JobReseedMysqldump's own
// phase-two logic -- see reseedMysqldumpSystemReplaySource). It exists solely
// so an operator finds out, at plan/execution start, whether backed-up
// user/system SQL is actually going to be available, rather than only
// discovering that from JobReseedMysqldump's late phase-two no-op log after
// the (potentially long) application-data restore has already run. See
// doc/implementation/cluster/SYSTEM_ALL_RESEED_IMPLEMENTATION_STATUS.md.
type logicalReseedUserRestoreAssessment struct {
	// Applicable is false for backup formats where the mysql.users.sql.gz
	// sidecar isn't the relevant concept -- mydumper (its own per-file sidecar
	// convention) and splitdump-format mysqldump backups (system content
	// bundled as mysql.system-all.sql.gz inside the splitdump directory,
	// selected by restoreUser at restore time, not by a sidecar file's
	// presence). SidecarChecked/SidecarPresent are always false when this is
	// false; Message still explains why nothing more specific is said.
	Applicable            bool
	RestoreUserConfigured bool
	RestoreUserEffective  bool
	SidecarChecked        bool
	SidecarPresent        bool
	Message               string
}

// matchLogicalReseedBackupMeta is the single source of truth for whether meta
// may be trusted as describing backupPath, reused by both the actual restore
// dispatch (reseedMysqldumpWithMetadata) and the preflight helpers below
// (logicalReseedUsesMonolithicMysqldumpFormat, assessLogicalReseedUserRestoreAvailability's
// callers) so they can never diverge on it. meta with an empty Dest is
// trusted unconditionally (legacy/incomplete metadata that never recorded a
// destination path) -- otherwise meta.Dest must resolve to the same file as
// backupPath, or meta describes some other backup (e.g. a custom/ad-hoc
// backup path reseed picking up stale metadata left over from an unrelated
// prior backup) and must not be trusted for this one; nil is returned in
// that case.
func matchLogicalReseedBackupMeta(meta *backupmgr.BackupMetadata, backupPath string) *backupmgr.BackupMetadata {
	if meta == nil || meta.Dest == "" {
		return meta
	}
	pathsMatch, err := comparePaths(meta.Dest, backupPath)
	if err != nil || !pathsMatch {
		return nil
	}
	return meta
}

// logicalReseedSplitUserProvenance records why splitUser holds the value it
// does. splitUser no longer gates actual restore behavior (restoreUser is
// cluster.Conf.BackupRestoreMysqlUser alone -- see JobReseedLogicalBackupPrepare);
// it is purely informational input to assessLogicalReseedUserRestoreAvailability,
// selecting which message explains why a mysql.users.sql.gz sidecar was or
// wasn't expected. Before splitUser was routed through
// resolveLogicalReseedSplitUser's trust check at all, "no split-user
// metadata" was a single message -- and, before restoreUser was decoupled
// from it, a single splitUser value -- conflating a valid backup that
// genuinely recorded backup-split-mysql-user=false with a custom/ad-hoc
// backup path that has no trustworthy metadata at all (which could still
// inherit an unrelated prior backup's SplitUser=true).
type logicalReseedSplitUserProvenance int

const (
	// logicalReseedSplitUserProvenanceUntrusted means splitUser could not be
	// attributed to backup metadata known (via matchLogicalReseedBackupMeta)
	// to describe this exact backupPath -- e.g. no metadata at all, or
	// metadata left over from an unrelated prior backup. The common real
	// case is a custom/ad-hoc backup path. resolveLogicalReseedSplitUser
	// defaults splitUser to false in this case -- unknown/custom is never
	// treated as "reuse whatever the last backup's contract was."
	logicalReseedSplitUserProvenanceUntrusted logicalReseedSplitUserProvenance = iota
	// logicalReseedSplitUserProvenanceMetadata means splitUser came from
	// backup metadata confirmed (via matchLogicalReseedBackupMeta) to
	// describe this exact backupPath.
	logicalReseedSplitUserProvenanceMetadata
	// logicalReseedSplitUserProvenanceOverride means splitUser was set by an
	// explicit operator override (JobReseedLogicalOptions.SplitUser), trusted
	// regardless of any metadata.
	logicalReseedSplitUserProvenanceOverride
)

// resolveLogicalReseedSplitUser is the single source of truth for a logical
// reseed's splitUser value and its provenance, so preflight messaging/payload
// can never diverge the way it could before this: an explicit operator
// override always wins; otherwise only backup metadata
// matchLogicalReseedBackupMeta confirms describes this exact backupfile may
// set splitUser; any other metadata (absent, or left over from an unrelated
// backup -- the concrete case this closes: a custom/ad-hoc backup path
// colliding with stale metadata from a different prior backup) is treated as
// unknown, not as "reuse whatever the last backup's contract was", and
// splitUser defaults to false. Note splitUser no longer gates actual restore
// behavior (see restoreUser's own computation) -- this only controls which
// preflight message is shown.
func resolveLogicalReseedSplitUser(meta *backupmgr.BackupMetadata, backupfile string, override *bool) (trustedMeta *backupmgr.BackupMetadata, splitUser bool, provenance logicalReseedSplitUserProvenance) {
	trustedMeta = matchLogicalReseedBackupMeta(meta, backupfile)
	if trustedMeta != nil {
		splitUser = trustedMeta.SplitUser
		provenance = logicalReseedSplitUserProvenanceMetadata
	}
	if override != nil {
		splitUser = *override
		provenance = logicalReseedSplitUserProvenanceOverride
	}
	return trustedMeta, splitUser, provenance
}

// logicalReseedUsesMonolithicMysqldumpFormat reports whether backupfile, for
// backtype, will be restored via the monolithic JobReseedMysqldump path (and
// therefore consults the mysql.users.sql.gz sidecar) rather than the
// splitdump-native path (JobReseedSplitdumpWithMysql) or mydumper. Reuses the
// exact detection reseedMysqldumpWithMetadata/reseedMysqldumpWithSplitdump
// apply at execution time -- including matchLogicalReseedBackupMeta's
// path-match trust rule -- so preflight messaging and actual restore
// behavior never disagree about which format a given backup is.
func logicalReseedUsesMonolithicMysqldumpFormat(backtype, backupfile string, meta *backupmgr.BackupMetadata) bool {
	if backtype != config.ConstBackupLogicalTypeMysqldump && backtype != "script" {
		return false
	}
	meta = matchLogicalReseedBackupMeta(meta, backupfile)
	if meta != nil && (meta.SplitDump || isSplitDumpName(meta.Dest)) {
		return false
	}
	if isSplit, err := isSplitDumpDir(backupfile); err == nil && isSplit {
		return false
	}
	return true
}

// assessLogicalReseedUserRestoreAvailability computes the preflight
// assessment for a logical reseed's user/system SQL restore. It deliberately
// never reads the dump itself -- only cluster config, already-resolved
// backup metadata/override, the cheap format check above, and at most a
// single Stat of the mysql.users.sql.gz sidecar path -- so it adds no new
// dump-scanning cost to reseed planning or execution.
func assessLogicalReseedUserRestoreAvailability(backupfile string, restoreUserConfigured, splitUser bool, splitUserProvenance logicalReseedSplitUserProvenance, monolithicFormat bool) logicalReseedUserRestoreAssessment {
	a := logicalReseedUserRestoreAssessment{
		Applicable:            monolithicFormat,
		RestoreUserConfigured: restoreUserConfigured,
		// restoreUserConfigured alone, matching the actual restoreUser formula
		// (JobReseedLogicalBackupPrepare et al.) -- splitUser no longer gates
		// real restore behavior, only which message below is shown.
		RestoreUserEffective: restoreUserConfigured,
	}

	if !restoreUserConfigured {
		a.Message = "User restore disabled by configuration (backup-restore-mysql-user); backed-up user/system SQL will be skipped."
		return a
	}
	if !monolithicFormat {
		a.Message = "User restore enabled; this backup's format restores user/system content internally (not via a mysql.users.sql.gz sidecar)."
		return a
	}

	// restoreUser no longer depends on splitUser: JobReseedMysqldump always
	// checks for inline mysql.system-all content and, failing that, the
	// mysql.users.sql.gz sidecar, whenever restore-user is enabled -- so the
	// sidecar is always worth checking here too, regardless of splitUser.
	// splitUser/splitUserProvenance only refine *why* a sidecar may or may not
	// have been expected, once the check comes back empty.
	present, statErr := hasMysqldumpUserSidecar(backupfile)
	a.SidecarChecked = statErr == nil
	a.SidecarPresent = present
	switch {
	case statErr != nil:
		a.Message = fmt.Sprintf("User restore enabled; could not check for the mysql.users.sql.gz sidecar for this backup: %s. Reseed will continue; inline system content in the dump, if any, is still checked.", statErr)
	case present:
		a.Message = "User restore enabled; mysql.users.sql.gz sidecar found for this backup. If inline system content also exists in the dump, the dump remains authoritative."
	case splitUser:
		a.Message = "User restore enabled, but the mysql.users.sql.gz sidecar is missing for this backup. Reseed will continue; user restore will only occur if inline system content exists in the dump."
	default:
		switch splitUserProvenance {
		case logicalReseedSplitUserProvenanceOverride:
			a.Message = "User restore is enabled in configuration, and split-user was explicitly set to false for this reseed (no sidecar expected, and none was found); reseed will continue, and user restore will only occur if inline system content exists in the dump."
		case logicalReseedSplitUserProvenanceMetadata:
			a.Message = "User restore is enabled in configuration; this backup's own metadata records no split-user sidecar (backup-split-mysql-user was off when it was taken), and none was found. Reseed will continue; user restore will only occur if inline system content exists in the dump."
		default:
			a.Message = "User restore is enabled in configuration, but no backup metadata could be matched to this backup path (e.g. a custom/ad-hoc path, or metadata belonging to a different backup), and no mysql.users.sql.gz sidecar was found. Reseed will continue; user restore will only occur if inline system content exists in the dump."
		}
	}
	return a
}

// resolveLogicalReseedUserRestore is the single place every logical-reseed/
// flashback entry point derives splitUser, restoreUser, and the preflight
// assessment from. Introduced after a flashback call site
// (JobFlashbackLogicalBackup) was found still repeating the old, pre-fix
// inline formula (cluster.Conf.BackupRestoreMysqlUser && meta.SplitUser) by
// hand -- entirely bypassing resolveLogicalReseedSplitUser/
// matchLogicalReseedBackupMeta/assessLogicalReseedUserRestoreAvailability,
// so it had neither the metadata-trust fix nor the restoreUser/splitUser
// decoupling. Routing every call site through one function instead of
// repeating this same handful of lines makes that class of divergence
// structurally harder to reintroduce: there is no formula left to copy
// incorrectly.
func resolveLogicalReseedUserRestore(cluster *Cluster, backtype, backupfile string, meta *backupmgr.BackupMetadata, override *bool) (restoreUser bool, splitUser bool, assessment logicalReseedUserRestoreAssessment) {
	_, splitUser, provenance := resolveLogicalReseedSplitUser(meta, backupfile, override)
	restoreUser = cluster.Conf.BackupRestoreMysqlUser
	monolithicFormat := logicalReseedUsesMonolithicMysqldumpFormat(backtype, backupfile, meta)
	assessment = assessLogicalReseedUserRestoreAvailability(backupfile, cluster.Conf.BackupRestoreMysqlUser, splitUser, provenance, monolithicFormat)
	return restoreUser, splitUser, assessment
}

func snapshotLogicalBackupMeta(server *ServerMonitor) *backupmgr.BackupMetadata {
	if server == nil {
		return nil
	}
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()
	if server.LastBackupMeta.Logical == nil {
		return nil
	}
	metaCopy := *server.LastBackupMeta.Logical
	return &metaCopy
}

func (server *ServerMonitor) JobReseedLogicalBackup(ctx context.Context, backtype string) error {
	_, err := server.JobReseedLogicalBackupPrepare(ctx, backtype)
	return err
}

func (server *ServerMonitor) JobReseedLogicalBackupPrepare(ctx context.Context, backtype string) (*logicalReseedPlan, error) {
	var err error
	cluster := server.ClusterGroup
	if ctx == nil {
		ctx = context.Background()
	}
	if backtype == "default" {
		backtype = cluster.Conf.BackupLogicalType
	}
	if backtype == config.ConstBackupLogicalTypeDumpling {
		return nil, errors.New("Logical reseed with dumpling is not supported")
	}
	task := "reseed" + backtype
	isPITR := server.PointInTimeMeta.IsInPITR

	if !cluster.IsDiscovered() {
		return nil, errors.New("Cluster not discovered yet")
	}

	master := cluster.GetMaster()
	if master == nil {
		return nil, errors.New("No master found")
	}

	if _, err := os.Stat(cluster.GetMysqlclientPath()); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "File does not exist %s", cluster.GetMysqlclientPath())
		return nil, err
	}

	if backtype == config.ConstBackupLogicalTypeMydumper && cluster.VersionsMap.Get("mydumper") == nil {
		return nil, errors.New("No mydumper version found")
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
			return nil, fmt.Errorf("No backup file found on master for %s", backtype)
		}

		bckserver = master
	}

	err = cluster.CheckLogicalBackupToolVersion(bckserver)
	if err != nil && cluster.Conf.BackupRestoreVersionStrict {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s version is not compatible with restore version on %s. Cancelling reseed for data safety.", backtype, server.URL)
		return nil, fmt.Errorf("Node %s backup tool version is not compatible with restore version.", server.URL)
	}

	if ok, currentTask := server.TrySetInReseedBackup(task); !ok {
		err := fmt.Errorf("Server is in reseeding state by %s", currentTask)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Concurrent reseed blocked: server %s already reseeding with %s, cannot start %s", server.URL, currentTask, task)
		return nil, err
	}

	// Stamp the real start time now, before the potentially slow work below
	// (StopSlave/ResetSlave for PITR). No JobInsertTask call anywhere in this
	// function, so there is no DB row.
	server.JobsUpdateStateRuntimeOnly(task, "", 1, 0)

	resetReseed := func() {
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
	}

	//Delete wait logical backup cookie
	server.DelWaitLogicalBackupCookie()

	// If reset failed, better to stop PITR
	if isPITR {
		server.StopSlave()
		_, err := server.ResetSlave()
		if err != nil {
			if mysqlErr, ok := err.(*mysql.MySQLError); ok && mysqlErr.Number != 1617 {
				resetReseed()
				server.JobsUpdateStateRuntimeOnly(task, err.Error(), 5, 1)
				return nil, err
			}
		}
		server.SetState(stateUnconn)
	}

	meta := snapshotLogicalBackupMeta(source)
	restoreUser, splitUser, userRestoreAssessment := resolveLogicalReseedUserRestore(cluster, backtype, backupfile, meta, nil)
	// Not logged here: ProcessReseedLogical re-derives and logs this same
	// assessment at LvlInfo when it actually runs ("Logical reseed
	// user/system restore for..."), which now follows prepare within seconds.
	payload, err := buildLogicalReseedPayload(backtype, backupfile, splitUser, false, false, isPITR, server.URL, userRestoreAssessment)
	if err != nil {
		resetReseed()
		server.JobsUpdateStateRuntimeOnly(task, err.Error(), 5, 1)
		return nil, err
	}

	server.JobsUpdatePayloadRuntimeOnly(task, payload)
	cluster.SetState("WARN0075", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0075"], backtype, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})

	plan := &logicalReseedPlan{
		task:         task,
		backtype:     backtype,
		backupfile:   backupfile,
		meta:         meta,
		splitUser:    splitUser,
		restoreUser:  restoreUser,
		skipMetadata: false,
		isPITR:       isPITR,
		fromPath:     false,
	}

	return plan, nil
}

func (server *ServerMonitor) JobReseedLogicalBackupProcess(ctx context.Context, plan *logicalReseedPlan) error {
	if plan == nil {
		return errors.New("Logical reseed plan is nil")
	}
	if plan.task == "" {
		return errors.New("Logical reseed task is empty")
	}
	return server.ProcessReseedLogical(plan.task)
}

type JobReseedLogicalOptions struct {
	SplitUser    *bool
	SkipMetadata bool
}

func formatMysqlRestoreError(stderr string) string {
	const (
		maxLines = 6
		maxBytes = 2048
	)
	trimmed := strings.TrimSpace(stderr)
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		filtered = append(filtered, line)
	}
	if len(filtered) == 0 {
		return ""
	}
	var errLines []string
	for _, line := range filtered {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") || strings.Contains(lower, "fatal") {
			errLines = append(errLines, line)
		}
	}
	if len(errLines) == 0 {
		errLines = filtered
	}
	if len(errLines) > maxLines {
		errLines = errLines[len(errLines)-maxLines:]
	}
	msg := strings.Join(errLines, "\n")
	if len(msg) > maxBytes {
		msg = msg[:maxBytes-3] + "..."
	}
	return strings.TrimSpace(msg)
}

func (server *ServerMonitor) executeMysqlRestore(reader io.Reader, force bool) error {
	return server.executeMysqlRestoreContext(context.Background(), reader, force)
}

func (server *ServerMonitor) executeMysqlRestoreContext(ctx context.Context, reader io.Reader, force bool) error {
	if reader == nil {
		return fmt.Errorf("mysql restore reader is nil")
	}

	cluster := server.ClusterGroup
	if cluster == nil {
		return fmt.Errorf("cluster not available")
	}

	if ctx == nil {
		ctx = context.Background()
	}

	mysqlPath := cluster.GetMysqlclientPath()
	if strings.TrimSpace(mysqlPath) == "" {
		return fmt.Errorf("mysql client path is empty")
	}
	if _, err := os.Stat(mysqlPath); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModBackupStream,
			config.LvlErr,
			"mysql client not found at %s: %s", mysqlPath, err)
		return fmt.Errorf("mysql client not found at %s: %w", mysqlPath, err)
	}

	cliParams := append(cluster.GetDumpCredentials(server), server.GetSSLClientParam("client")...)
	cliParams = append(cliParams, strings.Split(cluster.Conf.BackupMysqlclientOptions, " ")...)
	if force && !slices.Contains(cliParams, "--force") {
		cliParams = append(cliParams, "--force")
	}
	cmd := exec.CommandContext(ctx, cluster.GetMysqlclientPath(), misc.RemoveEmptyString(cliParams)...)
	cmd.Stdin = reader
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errOutput := formatMysqlRestoreError(stderr.String())
		if errOutput == "" {
			errOutput = err.Error()
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModBackupStream,
			config.LvlErr,
			"mysql restore failed: %s", errOutput)
		return fmt.Errorf("mysql restore failed: %s: %w", errOutput, err)
	}

	return nil
}

func (server *ServerMonitor) JobReseedLogicalBackupFromPathWithOptions(ctx context.Context, backtype, backupPath string, opts JobReseedLogicalOptions) error {
	_, err := server.JobReseedLogicalBackupFromPathPrepare(ctx, backtype, backupPath, opts)
	return err
}

func (server *ServerMonitor) JobReseedLogicalBackupFromPathPrepare(ctx context.Context, backtype, backupPath string, opts JobReseedLogicalOptions) (*logicalReseedPlan, error) {
	var err error
	cluster := server.ClusterGroup
	if ctx == nil {
		ctx = context.Background()
	}
	if backtype == "default" {
		backtype = cluster.Conf.BackupLogicalType
	}
	if backtype == config.ConstBackupLogicalTypeDumpling {
		return nil, errors.New("Logical reseed with dumpling is not supported")
	}
	if backupPath == "" {
		return nil, errors.New("backup path is empty")
	}
	backupfile := backupPath
	task := "reseed" + backtype
	isPITR := server.PointInTimeMeta.IsInPITR

	if !cluster.IsDiscovered() {
		return nil, errors.New("Cluster not discovered yet")
	}

	master := cluster.GetMaster()
	if master == nil {
		return nil, errors.New("No master found")
	}

	if _, err := os.Stat(cluster.GetMysqlclientPath()); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "File does not exist %s", cluster.GetMysqlclientPath())
		return nil, err
	}

	if backtype == config.ConstBackupLogicalTypeMydumper && cluster.VersionsMap.Get("mydumper") == nil {
		return nil, errors.New("No mydumper version found")
	}

	if err = cluster.CheckLogicalBackupToolVersion(master); err != nil && cluster.Conf.BackupRestoreVersionStrict {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s version is not compatible with restore version on %s. Cancelling reseed for data safety.", backtype, server.URL)
		return nil, fmt.Errorf("Node %s backup tool version is not compatible with restore version.", server.URL)
	}

	if ok, currentTask := server.TrySetInReseedBackup(task); !ok {
		err := fmt.Errorf("Server is in reseeding state by %s", currentTask)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Concurrent reseed blocked: server %s already reseeding with %s, cannot start %s", server.URL, currentTask, task)
		return nil, err
	}
	resetReseed := func() {
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
	}

	//Delete wait logical backup cookie
	server.DelWaitLogicalBackupCookie()

	// If reset failed, better to stop PITR
	if isPITR {
		server.StopSlave()
		_, err := server.ResetSlave()
		if err != nil {
			if mysqlErr, ok := err.(*mysql.MySQLError); ok && mysqlErr.Number != 1617 {
				resetReseed()
				return nil, err
			}
		}
		server.SetState(stateUnconn)
	}

	meta := snapshotLogicalBackupMeta(master)
	splitUserOverride := opts.SplitUser != nil
	restoreUser, splitUser, userRestoreAssessment := resolveLogicalReseedUserRestore(cluster, backtype, backupfile, meta, opts.SplitUser)
	// Not logged here: see the matching comment in JobReseedLogicalBackupPrepare
	// -- ProcessReseedLogical logs this same assessment at LvlInfo when it
	// actually runs.
	payload, err := buildLogicalReseedPayload(backtype, backupfile, splitUser, splitUserOverride, opts.SkipMetadata, isPITR, server.URL, userRestoreAssessment)
	if err != nil {
		resetReseed()
		return nil, err
	}

	_, err = server.JobInsertTaskWithPayload(task, "0", cluster.Conf.MonitorAddress, payload)
	if err != nil {
		resetReseed()
		return nil, err
	}

	plan := &logicalReseedPlan{
		task:              task,
		backtype:          backtype,
		backupfile:        backupfile,
		meta:              meta,
		splitUser:         splitUser,
		splitUserOverride: splitUserOverride,
		restoreUser:       restoreUser,
		skipMetadata:      opts.SkipMetadata,
		isPITR:            isPITR,
		fromPath:          true,
	}

	return plan, nil
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

func (server *ServerMonitor) reseedMysqldumpWithSplitdump(ctx context.Context, backupPath string, restoreUser bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	isSplit, err := isSplitDumpDir(backupPath)
	if err != nil {
		return err
	}
	if isSplit {
		cluster := server.ClusterGroup
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Splitdump detected at %s; restoring with mysql client", backupPath)
		return server.JobReseedSplitdumpWithMysql(ctx, backupPath, restoreUser)
	}
	return server.JobReseedMysqldump(backupPath, restoreUser)
}

func (server *ServerMonitor) reseedMysqldumpWithMetadata(ctx context.Context, backupPath string, restoreUser bool, meta *backupmgr.BackupMetadata) error {
	if ctx == nil {
		ctx = context.Background()
	}
	meta = matchLogicalReseedBackupMeta(meta, backupPath)
	if meta != nil && (meta.SplitDump || isSplitDumpName(meta.Dest)) {
		cluster := server.ClusterGroup
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Splitdump metadata detected for %s; restoring with mysql client", backupPath)
		return server.JobReseedSplitdumpWithMysql(ctx, backupPath, restoreUser)
	}
	return server.reseedMysqldumpWithSplitdump(ctx, backupPath, restoreUser)
}

// ---------------------------------------------------------------------------
// Native Go splitdump loader (replaces the `mysql --force` subprocess).
//
// The subprocess path piped each shard to `mysql --force`, which skips a failing
// statement and still exits 0. executeMysqlRestoreContext only inspected stderr
// on a non-zero exit, so any shard that errored (typically the first shard, which
// carries LOCK TABLES / DISABLE KEYS and loses the deadlock race under N-way
// parallel load) was reported as a successful restore with its rows silently
// dropped. This loader instead runs every shard over a dedicated pinned
// connection, batches INSERT/REPLACE into transactions, RETRIES on transient
// InnoDB lock contention, and returns any other error so the restore fails loud.
// ---------------------------------------------------------------------------

const (
	splitdumpLockRetryMax    = 8
	splitdumpBatchStatements = 500
	splitdumpRetryBaseDelay  = 50 * time.Millisecond
	splitdumpRetryMaxDelay   = 5 * time.Second
)

// isRetryableDBError reports whether err is a transient InnoDB lock error that a
// plain transaction replay can resolve (deadlock victim / lock-wait timeout).
func isRetryableDBError(err error) bool {
	if err == nil {
		return false
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		switch me.Number {
		case 1213, 1205: // ER_LOCK_DEADLOCK, ER_LOCK_WAIT_TIMEOUT
			return true
		}
	}
	return false
}

func splitdumpRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := splitdumpRetryBaseDelay << uint(attempt-1)
	if d <= 0 || d > splitdumpRetryMaxDelay {
		return splitdumpRetryMaxDelay
	}
	return d
}

// prepareRestoreConn applies per-session state to a dedicated bulk-load
// connection: slow-log normalization and FK/UNIQUE checks disabled. These three
// statements are identical on MySQL and MariaDB. It owns neither of the two
// decisions that need version/flavor or topology awareness:
//   - binlog on/off (master vs slave) is realized at connection-acquisition time
//     (GetConnNoBinlog for a slave reseed vs a plain pinned conn for a master
//     restore), per the decision taken in buildLogicalRestorePreamble;
//   - the one-time binlog reset is done via the version-aware server.ResetMaster()
//     (which handles MySQL 8.4's RESET BINARY LOGS AND GTIDS vs RESET MASTER).
func (server *ServerMonitor) prepareRestoreConn(ctx context.Context, conn *sqlx.Conn) error {
	for _, q := range []string{
		"SET SESSION long_query_time=10",
		"SET SESSION FOREIGN_KEY_CHECKS=0",
		"SET SESSION UNIQUE_CHECKS=0",
	} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// restoreSystemCatalog replays a published mysql.system-all splitdump artifact
// (gzip-compressed SQL) over a single pinned connection. It is the narrow,
// dedicated phase-two path shared by normal splitdump restore (dispatched from
// restoreSplitdumpWithMysql) and direct-reseed catalogue replay: FK/UNIQUE
// session state only (prepareRestoreConn), then a plain statement stream —
// every INSTALL PLUGIN skip decision is made per-statement inside
// execSplitdumpSingle via a live dbhelper lookup, so this function carries no
// policy logic of its own. It deliberately never calls
// buildLogicalRestorePreamble, ResetMaster, or applies GTID — those stay
// exclusively in the callers that own the one-time, restore-wide binlog/GTID
// decisions.
//
// progressed reports whether any statement actually committed against conn
// before err (if any). Callers that drive retry orchestration (direct-reseed
// phase two, RetryDirectReseedSystemCatalog) use this to distinguish "failed
// before touching the destination" (safe to retry the whole artifact from the
// beginning) from "failed after partially applying the catalogue" (not safe —
// most --system=all statement classes besides INSTALL PLUGIN are not proven
// replay-idempotent). Callers that don't drive retry (restoreSplitdumpWithMysql's
// normal splitdump dispatch) may ignore it.
func (server *ServerMonitor) restoreSystemCatalog(ctx context.Context, conn *sqlx.Conn, systemArtifactPath string) (progressed bool, err error) {
	var progress atomic.Bool
	if err := server.prepareRestoreConn(ctx, conn); err != nil {
		return false, err
	}

	file, err := os.Open(systemArtifactPath)
	if err != nil {
		return false, err
	}
	defer file.Close()

	var reader io.Reader = server.countReseedReader(file)
	if strings.HasSuffix(strings.ToLower(systemArtifactPath), ".gz") {
		cluster := server.ClusterGroup
		parallelBlocks := cluster.getSanitizedParallelBlocks(config.ConstLogModTask)
		bufferSize := cluster.getSanitizedDecompressBufferSize(config.ConstLogModTask)
		gzReader, err := gzip.NewReaderN(reader, bufferSize, parallelBlocks)
		if err != nil {
			return false, err
		}
		defer gzReader.Close()
		reader = gzReader
	}

	execErr := server.streamSplitdumpStatements(ctx, conn, reader, systemArtifactPath, &progress)
	return progress.Load(), execErr
}

// restoreSplitdumpFileGo loads a single splitdump file over the supplied dedicated
// connection. It preserves the mysql.* special-casing of the subprocess path
// (gtid_slave_pos skip, missing-table skip) and optionally strips DEFINER clauses.
// mysql.system-all is never routed through here: restoreSystemCatalog is the
// dedicated phase-two path for that file (see restoreSplitdumpWithMysql's
// dispatch), and any INSTALL PLUGIN skip decision is made per-statement in
// execSplitdumpSingle via a live dbhelper lookup.
func (server *ServerMonitor) restoreSplitdumpFileGo(ctx context.Context, conn *sqlx.Conn, path string, stripDefiner bool) error {
	cluster := server.ClusterGroup

	schema := splitdump.SchemaFromFilename(path)
	table := splitdump.TableFromFilename(path)
	if schema == "mysql" {
		if splitdump.IsGtidSlavePosDataFile(path) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlWarn,
				"Splitdump restore skipped mysql.gtid_slave_pos data file: %s", filepath.Base(path))
			return nil
		}
		if table != "" && splitdump.IsMysqlTableCheckEligible(path) {
			exists, err := server.tableExists(schema, table)
			if err != nil {
				return err
			}
			if !exists {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlWarn,
					"Splitdump restore skipped missing mysql table %s for %s", table, filepath.Base(path))
				return nil
			}
		}
	}

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	// Count compressed bytes streamed for the WARN0189 reseed-progress state.
	// beginReseedProgress (in restoreSplitdumpWithMysql) accumulates every shard
	// into one counter, so a splitdump reload shows live progress like the
	// monolithic path — instead of a silent "processing".
	var reader io.Reader = server.countReseedReader(file)
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		parallelBlocks := cluster.getSanitizedParallelBlocks(config.ConstLogModTask)
		bufferSize := cluster.getSanitizedDecompressBufferSize(config.ConstLogModTask)
		gzReader, err := gzip.NewReaderN(reader, bufferSize, parallelBlocks)
		if err != nil {
			return err
		}
		defer gzReader.Close()
		reader = gzReader
	}

	var doneStrip func(error)
	if stripDefiner {
		reader, doneStrip = splitdump.NewDefinerStrippingReader(reader)
	}

	if schema != "" {
		if _, err := conn.ExecContext(ctx, "USE `"+strings.ReplaceAll(schema, "`", "``")+"`"); err != nil {
			if doneStrip != nil {
				doneStrip(err)
			}
			return err
		}
	}

	execErr := server.streamSplitdumpStatements(ctx, conn, reader, path, nil)
	if doneStrip != nil {
		doneStrip(execErr)
	}
	return execErr
}

// splitdumpStmtKind routes a restore statement.
type splitdumpStmtKind int

const (
	splitdumpStmtSkip   splitdumpStmtKind = iota // LOCK/UNLOCK TABLES, ALTER..DISABLE/ENABLE KEYS
	splitdumpStmtInsert                          // INSERT/REPLACE (batchable)
	splitdumpStmtOther                           // everything else (autocommit single)
)

// classifySplitdumpStatement classifies a single terminator-stripped statement.
// DISABLE/ENABLE KEYS arrives wrapped in a /*!40000 ALTER TABLE ... */ executable
// comment, so it is matched by Contains rather than a statement-start prefix. Pure —
// unit-testable without a DB.
func classifySplitdumpStatement(stmt string) splitdumpStmtKind {
	up := strings.ToUpper(strings.TrimLeft(stmt, " \t\r\n("))
	switch {
	case strings.HasPrefix(up, "LOCK TABLES"), strings.HasPrefix(up, "UNLOCK TABLES"):
		return splitdumpStmtSkip
	case strings.Contains(up, "ALTER TABLE") && (strings.Contains(up, "DISABLE KEYS") || strings.Contains(up, "ENABLE KEYS")):
		return splitdumpStmtSkip
	case strings.HasPrefix(up, "INSERT"), strings.HasPrefix(up, "REPLACE"):
		return splitdumpStmtInsert
	default:
		return splitdumpStmtOther
	}
}

// forEachSplitdumpStatement segments the SQL stream into complete statements and
// calls emit for each (terminator stripped, standalone comments dropped, DELIMITER
// honoured for trigger/routine bodies). Pure — no DB — so segmentation is testable
// without a live connection.
//
// Statement-end is a per-line HasSuffix(trimmed, delimiter) check. This is correct
// for mysqldump/splitdump output (single-line INSERT rows — newlines inside string
// values are escaped as \n — and DELIMITER-fenced multi-line routine/trigger
// bodies). It is NOT a general SQL tokenizer: a statement with a real embedded
// newline whose line happens to end in the delimiter would misparse. The source is
// always mysqldump, so that does not occur.
func forEachSplitdumpStatement(reader io.Reader, emit func(stmt string) error) error {
	br := bufio.NewReaderSize(reader, 1<<20)
	delimiter := ";"
	var stmt strings.Builder

	flushStmt := func() error {
		core := strings.TrimRight(stmt.String(), " \t\r\n")
		core = strings.TrimSuffix(core, delimiter)
		core = strings.TrimSpace(core)
		stmt.Reset()
		if core == "" {
			return nil
		}
		return emit(core)
	}

	for {
		line, readErr := br.ReadString('\n')
		body := strings.TrimRight(line, "\r\n")
		trimmed := strings.TrimSpace(body)

		if stmt.Len() == 0 {
			// DELIMITER is a client directive — never sent to the server.
			if len(trimmed) >= 9 && strings.EqualFold(trimmed[:9], "DELIMITER") {
				if fields := strings.Fields(trimmed); len(fields) >= 2 {
					delimiter = fields[1]
				}
				if readErr != nil {
					break
				}
				continue
			}
			// Standalone comment / blank line between statements.
			if trimmed == "" || strings.HasPrefix(trimmed, "-- ") || trimmed == "--" || strings.HasPrefix(trimmed, "#") {
				if readErr != nil {
					break
				}
				continue
			}
		}

		if stmt.Len() > 0 {
			stmt.WriteByte('\n')
		}
		stmt.WriteString(body)

		if strings.HasSuffix(trimmed, delimiter) {
			if err := flushStmt(); err != nil {
				return err
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return readErr
			}
			break
		}
	}
	// Trailing statement with no terminator (rare) — do not drop it.
	if strings.TrimSpace(stmt.String()) != "" {
		return flushStmt()
	}
	return nil
}

// splitdumpExecutor is the DB side of the restore, injectable so planAndExecSplitdump
// is testable with a recording stub instead of a live connection.
type splitdumpExecutor struct {
	batch  func(stmts []string) error // run stmts in one retrying transaction
	single func(stmt string) error
}

// planAndExecSplitdump segments the stream (forEachSplitdumpStatement) and drives
// exec: INSERT/REPLACE batch (batchSize) into exec.batch; LOCK/UNLOCK/DISABLE-KEYS
// are dropped; everything else flushes the pending batch, then runs via exec.single.
func planAndExecSplitdump(reader io.Reader, batchSize int, exec splitdumpExecutor) error {
	batch := make([]string, 0, batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := exec.batch(batch)
		batch = batch[:0]
		return err
	}

	err := forEachSplitdumpStatement(reader, func(full string) error {
		switch classifySplitdumpStatement(full) {
		case splitdumpStmtSkip:
			return nil
		case splitdumpStmtInsert:
			batch = append(batch, full)
			if len(batch) >= batchSize {
				return flush()
			}
			return nil
		default:
			if err := flush(); err != nil {
				return err
			}
			return exec.single(full)
		}
	})
	if err != nil {
		return err
	}
	return flush()
}

// streamSplitdumpStatements segments the SQL stream (honouring DELIMITER for
// trigger/routine bodies) and executes it on conn: INSERT/REPLACE batch into
// retrying transactions; every other statement runs in autocommit; LOCK/UNLOCK
// TABLES and ALTER..{DISABLE,ENABLE} KEYS are dropped. The INSTALL PLUGIN skip
// decision (mysql.system-all replay) is made unconditionally, per statement,
// inside execSplitdumpSingle via a live dbhelper lookup — this function carries
// no policy of its own. progressed, if non-nil, is set the moment any statement
// actually commits against conn (a deliberately skipped INSTALL PLUGIN does not
// count) — restoreSystemCatalog uses this to tell retry orchestration whether a
// failure happened before or after any system-catalogue statement was committed,
// since only the former is safe to retry from the beginning (see
// RetryDirectReseedSystemCatalog). restoreSplitdumpFileGo has no use for this
// signal and passes nil. Segmentation and routing live in the pure
// forEachSplitdumpStatement / classifySplitdumpStatement / planAndExecSplitdump
// helpers; this wrapper just binds the executor to conn.
func (server *ServerMonitor) streamSplitdumpStatements(ctx context.Context, conn *sqlx.Conn, reader io.Reader, path string, progressed *atomic.Bool) error {
	return planAndExecSplitdump(reader, splitdumpBatchStatements, splitdumpExecutor{
		batch: func(stmts []string) error {
			return server.execSplitdumpBatch(ctx, conn, stmts, path, progressed)
		},
		single: func(stmt string) error {
			return server.execSplitdumpSingle(ctx, conn, stmt, path, progressed)
		},
	})
}

// execSplitdumpBatch runs stmts inside a single transaction and retries the whole
// transaction on transient lock contention. A non-retryable error is returned so
// the caller aborts the restore instead of silently losing rows. See
// streamSplitdumpStatements for what progressed tracks.
func (server *ServerMonitor) execSplitdumpBatch(ctx context.Context, conn *sqlx.Conn, stmts []string, path string, progressed *atomic.Bool) error {
	cluster := server.ClusterGroup
	var lastErr error
	for attempt := 0; attempt <= splitdumpLockRetryMax; attempt++ {
		if attempt > 0 {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlInfo,
				"Splitdump lock contention on %s, retrying transaction (%d/%d): %v",
				filepath.Base(path), attempt, splitdumpLockRetryMax, lastErr)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(splitdumpRetryDelay(attempt)):
			}
		}
		tx, err := conn.BeginTxx(ctx, nil)
		if err != nil {
			lastErr = err
			if isRetryableDBError(err) {
				continue
			}
			return err
		}
		failed := false
		for _, s := range stmts {
			if _, err := tx.ExecContext(ctx, s); err != nil {
				_ = tx.Rollback()
				lastErr = err
				failed = true
				if !isRetryableDBError(err) {
					return err
				}
				break
			}
		}
		if failed {
			continue
		}
		if err := tx.Commit(); err != nil {
			lastErr = err
			if isRetryableDBError(err) {
				continue
			}
			return err
		}
		if progressed != nil && len(stmts) > 0 {
			progressed.Store(true)
		}
		return nil
	}
	return fmt.Errorf("splitdump restore transaction on %s failed after %d retries: %w",
		filepath.Base(path), splitdumpLockRetryMax, lastErr)
}

// isInstallPluginStatement reports whether stmt is an INSTALL PLUGIN statement
// and, if so, extracts the plugin name (the token immediately following
// INSTALL PLUGIN, unquoted). Pure — unit-testable without a DB.
func isInstallPluginStatement(stmt string) (name string, ok bool) {
	trimmed := strings.TrimLeft(stmt, " \t\r\n(")
	const prefix = "INSTALL PLUGIN"
	if !strings.HasPrefix(strings.ToUpper(trimmed), prefix) {
		return "", false
	}
	rest := strings.TrimSpace(trimmed[len(prefix):])
	if rest == "" {
		return "", false
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", false
	}
	name = strings.Trim(fields[0], "`\"'")
	if name == "" {
		return "", false
	}
	return name, true
}

// resolveInstallPluginSkip decides whether an INSTALL PLUGIN <name> statement may
// be skipped, using a live lookup on the same pinned restore connection (never the
// monitoring cache — see dbhelper.GetPluginStatusConn). Only an unambiguous,
// ACTIVE match is skipped; absent and NOT INSTALLED both execute normally (the
// latter mirrors InstallPlugin's own NOT INSTALLED handling in srv.go); every
// other outcome (present but not ACTIVE, ambiguous, or a lookup error) is
// surfaced to the caller, which treats a non-nil err as fatal.
func (server *ServerMonitor) resolveInstallPluginSkip(ctx context.Context, conn *sqlx.Conn, name string) (skip bool, err error) {
	status, observed, err := dbhelper.GetPluginStatusConn(ctx, conn, name, server.DBVersion)
	if err != nil {
		return false, fmt.Errorf("plugin lookup for %s failed: %w", name, err)
	}
	switch status {
	case dbhelper.PluginActive:
		return true, nil
	case dbhelper.PluginAbsent, dbhelper.PluginNotInstalled:
		return false, nil
	case dbhelper.PluginPresentNotActive:
		return false, fmt.Errorf("plugin %s is present but not ACTIVE (status: %s)", name, observed)
	default: // dbhelper.PluginAmbiguous
		return false, fmt.Errorf("plugin %s lookup returned ambiguous/duplicate rows", name)
	}
}

// accountAlreadyMatchesHash reports whether user@host already exists on the
// destination with exactly hash as its stored password, via a live lookup on
// the same pinned restore connection (dbhelper.GetUserAuthConn) -- the
// restore-time-truth check that lets execSplitdumpSingle skip re-sending a
// password-setting clause that strict_password_validation (MariaDB) can
// reject even when the value wouldn't actually change. A lookup error is
// returned to the caller, which treats it as "skip the optimization, not the
// statement" -- never fatal on its own, since this check only ever removes
// work, it never gates whether the underlying statement is allowed to run.
func (server *ServerMonitor) accountAlreadyMatchesHash(ctx context.Context, conn *sqlx.Conn, user string, host string, hash string) (skip bool, err error) {
	destHash, exists, err := dbhelper.GetUserAuthConn(ctx, conn, user, host, server.DBVersion)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	return destHash == hash, nil
}

// execSplitdumpSingle runs one non-INSERT statement in autocommit, retrying on
// lock contention. INSTALL PLUGIN is the sole exception to "every error is
// fatal": resolveInstallPluginSkip decides, from a live lookup, whether the
// statement may be deliberately skipped. There is no other continue-on-error
// path — a retry-exhausted or non-retryable error always propagates (DEFINER
// errors flow up to the strip-definer fallback in restore.go). See
// streamSplitdumpStatements for what progressed tracks; a deliberate skip does
// not set it, since nothing was sent to conn.
func (server *ServerMonitor) execSplitdumpSingle(ctx context.Context, conn *sqlx.Conn, stmt string, path string, progressed *atomic.Bool) error {
	cluster := server.ClusterGroup
	if name, ok := isInstallPluginStatement(stmt); ok {
		skip, err := server.resolveInstallPluginSkip(ctx, conn, name)
		if err != nil {
			return err
		}
		if skip {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlDbg,
				"Splitdump restore skipped INSTALL PLUGIN %s: already ACTIVE", name)
			return nil
		}
	}
	createUserInfo, isCreateUser := isCreateUserStatement(stmt)
	alterUserFallback, createUserAccount, createUserHost := createUserInfo.AlterUser, createUserInfo.User, createUserInfo.Host
	if isCreateUser && createUserInfo.HashOK {
		if skip, err := server.accountAlreadyMatchesHash(ctx, conn, createUserInfo.User, createUserInfo.Host, createUserInfo.Hash); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlDbg,
				"Splitdump restore: account equivalence lookup for %s@%s failed, proceeding without it: %s", createUserInfo.User, createUserInfo.Host, err)
		} else if skip {
			if remaining := strings.TrimSpace(createUserInfo.AfterHash); remaining == "" {
				// Nothing beyond the account and its password: the whole
				// statement is redundant, not just the password clause.
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlDbg,
					"Splitdump restore skipped CREATE USER for %s@%s: destination already matches %s", createUserInfo.User, createUserInfo.Host, filepath.Base(path))
				return nil
			} else {
				// Other attributes (resource limits, lock state, password
				// expiry, REQUIRE, ...) accompany the password clause: those
				// must still be reconciled, so this can't be a full skip --
				// only the redundant password clause is dropped, replayed as
				// ALTER USER against the now-known-to-exist account. isCreateUser
				// is cleared so the 1396/ALTER-USER-fallback machinery below
				// (which still holds the OLD alterUserFallback text, password
				// clause included) doesn't also fire for this statement.
				//
				// Unlike isGrantWithIdentifiedByPassword, this path does not
				// bail out when a REQUIRE clause is present -- deliberately,
				// not an oversight. CREATE USER and ALTER USER share the same
				// REQUIRE syntax (the actively-maintained, canonical TLS-option
				// clause for both statement forms on MySQL and MariaDB alike),
				// unlike GRANT's own separate REQUIRE clause -- deprecated and
				// removed in MySQL 8 -- whose cross-version/flavor behavior is
				// what isGrantWithIdentifiedByPassword's caution is about.
				// AfterHash is also never re-parsed here, only carried through
				// byte-for-byte from wherever the hash literal ends, so there
				// is no REQUIRE-specific clause boundary this rewrite needs to
				// get right in the first place.
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlDbg,
					"Splitdump restore: account %s@%s already has matching password, applying remaining attributes without IDENTIFIED BY PASSWORD for %s", createUserInfo.User, createUserInfo.Host, filepath.Base(path))
				stmt = "ALTER USER" + createUserInfo.AccountSpec + " " + remaining
				isCreateUser = false
			}
		}
	}
	if rewritten, grantUser, grantHost, grantHash, isGrant := isGrantWithIdentifiedByPassword(stmt); isGrant {
		if skip, err := server.accountAlreadyMatchesHash(ctx, conn, grantUser, grantHost, grantHash); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlDbg,
				"Splitdump restore: account equivalence lookup for %s@%s failed, proceeding without it: %s", grantUser, grantHost, err)
		} else if skip {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlDbg,
				"Splitdump restore: GRANT for %s@%s already has matching password, applying without its IDENTIFIED BY PASSWORD clause for %s", grantUser, grantHost, filepath.Base(path))
			stmt = rewritten
		}
	}
	var lastErr error
	for attempt := 0; attempt <= splitdumpLockRetryMax; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(splitdumpRetryDelay(attempt)):
			}
		}
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			// CREATE USER for an account that already exists (e.g. mariadb.sys,
			// or any account pre-created on the destination) fails with
			// ER_CANNOT_USER instead of applying the dumped definition. Replaying
			// it as ALTER USER makes the restore idempotent and actually brings
			// the existing account's auth/attributes in line with the backup,
			// rather than silently leaving it untouched.
			if isCreateUser && isCannotUserError(err) {
				if _, altErr := conn.ExecContext(ctx, alterUserFallback); altErr == nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlDbg,
						"Splitdump restore: account %s@%s already existed for %s, applied as ALTER USER instead", createUserAccount, createUserHost, filepath.Base(path))
					if progressed != nil {
						progressed.Store(true)
					}
					return nil
				} else if isAccessDeniedError(altErr) && isKnownProtectedSystemAccount(createUserAccount, createUserHost) {
					// MySQL 8's SYSTEM_USER-protected bootstrap accounts
					// (mysql.sys/mysql.session/mysql.infoschema) exist
					// identically on any instance of that server version,
					// created by the engine itself, not by a prior backup --
					// so the destination's own copy is already correct, and
					// a replay connection without the SYSTEM_USER privilege
					// legitimately can't (and doesn't need to) touch it.
					// Narrowly gated on the specific access-denied error AND
					// the full account identity (user@host, not user alone --
					// an ordinary account that merely reuses one of these user
					// names on a different host is not the engine-provisioned
					// account), so a real CREATE USER privilege
					// misconfiguration on an ordinary account still surfaces
					// as a fatal error below, unchanged.
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlDbg,
						"Splitdump restore skipped CREATE USER for protected system account %s@%s: already present and not modifiable by this connection (%s)", createUserAccount, createUserHost, altErr)
					return nil
				} else {
					// Neither fallback outcome applied: report both errors, not
					// just the original ER_CANNOT_USER. The 1396 alone ("already
					// exists") is not actionable on its own once a fallback was
					// attempted -- an operator needs to see *why* the fallback
					// didn't resolve it (a real privilege issue, a version/flavor
					// clause the rewrite doesn't handle, etc.), and swallowing
					// altErr here would hide exactly that.
					wrapped := fmt.Errorf("CREATE USER failed for existing account %s@%s (%w); ALTER USER fallback also failed: %s", createUserAccount, createUserHost, err, altErr)
					lastErr = wrapped
					// The retry loop below only ever inspects the original
					// CREATE USER error (now wrapped inside `wrapped`, still
					// reachable via errors.As/%w) -- it has no visibility into
					// altErr, so a transient failure specifically on the
					// ALTER USER fallback (lock wait/deadlock) would
					// otherwise never get retried, unlike every other
					// statement class in this function.
					if isRetryableDBError(altErr) {
						continue
					}
					return wrapped
				}
			}
			lastErr = err
			if isRetryableDBError(err) {
				continue
			}
			return err
		}
		if progressed != nil {
			progressed.Store(true)
		}
		return nil
	}
	return lastErr
}

// createUserStatementInfo holds everything execSplitdumpSingle needs to
// evaluate, and possibly partially rewrite, a CREATE USER statement.
type createUserStatementInfo struct {
	AlterUser   string // CREATE USER rewritten to ALTER USER verbatim -- the existing ER_CANNOT_USER (1396) fallback
	User        string
	Host        string
	AccountSpec string // exact original text of the account token (leading whitespace, original quoting) -- needed to rebuild a password-stripped ALTER USER without re-serializing the account spec from parsed parts
	Hash        string // extracted password hash; "" if HashOK is false
	HashOK      bool
	AfterHash   string // whatever follows the IDENTIFIED BY PASSWORD clause, verbatim; "" if HashOK is false or nothing follows
}

// isCreateUserStatement reports whether stmt is a plain CREATE USER statement
// (not CREATE USER IF NOT EXISTS, which already tolerates a pre-existing
// account without erroring) and, if so, returns info describing it: the same
// statement with the CREATE USER keyword swapped for ALTER USER (the
// existing 1396 fallback), the unquoted user/host of the account it targets,
// and -- when the statement uses the classic `IDENTIFIED BY PASSWORD
// '<hash>'` clause -- the extracted hash plus enough to rebuild the
// statement with only that clause removed (AccountSpec, AfterHash), so the
// caller can check destination equivalence before ever sending CREATE USER
// or its ALTER USER fallback, and -- if other attributes accompany the
// password clause -- still reconcile those without resending a redundant
// password. mysqldump's --system=all output emits one CREATE USER statement
// per account, so this only needs to handle a single account per statement.
func isCreateUserStatement(stmt string) (info createUserStatementInfo, ok bool) {
	trimmed := strings.TrimLeft(stmt, " \t\r\n(")
	const prefix = "CREATE USER"
	if !strings.HasPrefix(strings.ToUpper(trimmed), prefix) {
		return createUserStatementInfo{}, false
	}
	rest := trimmed[len(prefix):]
	if strings.HasPrefix(strings.TrimSpace(strings.ToUpper(rest)), "IF NOT EXISTS") {
		return createUserStatementInfo{}, false
	}
	user, host, accountRest, accOK := parseCreateUserAccountRest(rest)
	if !accOK {
		return createUserStatementInfo{}, false
	}
	// accountSpec is the exact original text of the account token: accountRest
	// is a content-suffix of rest by construction (parseCreateUserAccountRest
	// only slices, never rewrites), so this never re-serializes the account
	// spec from parsed parts -- same reasoning isGrantWithIdentifiedByPassword
	// documents for its own accountSpec.
	accountSpec := rest[:len(rest)-len(accountRest)]
	hash, afterHash, hashOK := parseIdentifiedByPasswordClause(accountRest)
	return createUserStatementInfo{
		AlterUser:   "ALTER USER" + rest,
		User:        user,
		Host:        host,
		AccountSpec: accountSpec,
		Hash:        hash,
		HashOK:      hashOK,
		AfterHash:   afterHash,
	}, true
}

// parseIdentifiedByPasswordClause parses a leading `IDENTIFIED BY PASSWORD
// '<hash>'` clause (optionally preceded by whitespace) from rest -- the
// classic mysql_native_password auth-clause form mariadb-dump/mysqldump
// emits for CREATE USER and the TO-clause of GRANT statements. Any other
// clause (IDENTIFIED VIA/WITH, no auth clause at all, trailing content that
// doesn't start with this exact keyword sequence) is reported as ok=false --
// this function only ever recognizes this one fixed form, never guesses.
// Also returns afterClause, what's left of rest immediately after the
// closing quote of the hash literal, needed by isGrantWithIdentifiedByPassword
// to reconstruct the statement with only that clause removed.
func parseIdentifiedByPasswordClause(rest string) (hash string, afterClause string, ok bool) {
	s := strings.TrimLeft(rest, " \t\r\n")
	const prefix = "IDENTIFIED BY PASSWORD"
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return "", "", false
	}
	if len(s) > len(prefix) && !isSQLWordBoundaryByte(s[len(prefix)]) {
		// e.g. a hypothetical "IDENTIFIED BY PASSWORDX ..." -- the prefix
		// matched but isn't actually followed by a keyword/clause boundary,
		// so this isn't really the clause it looks like.
		return "", "", false
	}
	s = strings.TrimLeft(s[len(prefix):], " \t\r\n")
	hash, afterClause, ok = parseCreateUserToken(s)
	if !ok || hash == "" {
		return "", "", false
	}
	return hash, afterClause, true
}

// parseCreateUserAccount extracts the user and host from the account
// specification immediately following CREATE USER/ALTER USER -- e.g.
// 'mariadb.sys'@'localhost' -> ("mariadb.sys", "localhost"). A bare user with
// no @host (valid CREATE USER syntax) defaults to host "%", matching the
// server's own default.
func parseCreateUserAccount(rest string) (user string, host string, ok bool) {
	user, host, _, ok = parseCreateUserAccountRest(rest)
	return user, host, ok
}

// parseCreateUserAccountRest is parseCreateUserAccount plus what's left of
// rest immediately after the account spec (e.g. the auth clause tail),
// needed by callers that must keep parsing past the account -- see
// isCreateUserStatement's hash extraction and isGrantWithIdentifiedByPassword.
func parseCreateUserAccountRest(rest string) (user string, host string, tail string, ok bool) {
	s := strings.TrimSpace(rest)
	user, s, ok = parseCreateUserToken(s)
	if !ok {
		return "", "", "", false
	}
	if !strings.HasPrefix(s, "@") {
		return user, "%", s, true
	}
	host, tail, ok = parseCreateUserToken(s[1:])
	if !ok {
		return "", "", "", false
	}
	return user, host, tail, true
}

// parseCreateUserToken extracts one quoted-or-bare identifier (a user or
// host name) from the front of s, unquoted, and returns what's left of s
// immediately after it. A doubled quote character inside a quoted identifier
// (''/""/``) is the standard SQL escape for a literal quote and is decoded,
// not treated as the closing quote; backslash-escaping is not handled (dump
// output that relies on it fails this parse, which fails the CREATE USER
// rewrite/matching safely -- see isCreateUserStatement's caller, which
// treats a parse failure the same as "not a CREATE USER statement" rather
// than guessing).
func parseCreateUserToken(s string) (token string, rest string, ok bool) {
	if s == "" {
		return "", "", false
	}
	if quote := s[0]; quote == '\'' || quote == '"' || quote == '`' {
		body := s[1:]
		var b strings.Builder
		for i := 0; i < len(body); i++ {
			if body[i] == quote {
				if i+1 < len(body) && body[i+1] == quote {
					b.WriteByte(quote)
					i++
					continue
				}
				return b.String(), body[i+1:], true
			}
			b.WriteByte(body[i])
		}
		return "", "", false // unterminated quote
	}
	end := strings.IndexAny(s, "@ \t,")
	if end == 0 {
		// The very first character is a delimiter (e.g. s starts with "@"):
		// an empty token, genuinely invalid.
		return "", "", false
	}
	if end < 0 {
		// No delimiter anywhere: a bare token that runs to the end of s (the
		// no-@host CREATE USER case parseCreateUserAccount's doc comment
		// describes), not a parse failure -- strings.IndexAny returning -1
		// here must not be conflated with "empty token" above.
		return s, "", true
	}
	return s[:end], s[end:], true
}

// isKnownProtectedSystemAccount reports whether user@host is one of the
// server-bootstrapped internal accounts that MySQL 8 restricts to
// connections holding the SYSTEM_USER privilege (mysql.sys, mysql.session,
// mysql.infoschema), or MariaDB's equivalent (mariadb.sys, which carries no
// such privilege restriction but is included here for the same "engine-
// provisioned, not backup content" reasoning). Matched on the full account
// identity, not the user name alone: MySQL/MariaDB account identity is
// user@host, and an ordinary account that merely reuses one of these user
// names on a different host is not the engine-provisioned account -- these
// are always created at @localhost. The user name is compared
// case-sensitively (MySQL/MariaDB user name comparison is case-sensitive by
// default, unlike host name comparison, which is not) since the engine never
// creates these accounts under any other casing.
func isKnownProtectedSystemAccount(user, host string) bool {
	if !strings.EqualFold(host, "localhost") {
		return false
	}
	switch user {
	case "mysql.sys", "mysql.session", "mysql.infoschema", "mariadb.sys":
		return true
	default:
		return false
	}
}

// isSQLWordBoundaryByte reports whether b cannot be part of a SQL bare
// identifier/keyword, i.e. it terminates one. Used to make prefix keyword
// matches whole-word (so "IDENTIFIED BY PASSWORDX" doesn't false-match
// "IDENTIFIED BY PASSWORD", and "TOKEN_COL" doesn't false-match a bare "TO").
func isSQLWordBoundaryByte(b byte) bool {
	return !(b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9'))
}

// findTopLevelKeyword returns the byte index in s of the first case-insensitive,
// whole-word occurrence of keyword that is not inside a quoted/backtick-quoted
// span, or -1 if there is none. Quote spans use the same doubled-quote
// escaping convention as parseCreateUserToken, so a keyword-shaped substring
// inside a quoted identifier or string literal is correctly skipped rather
// than matched.
func findTopLevelKeyword(s string, keyword string) int {
	upper := strings.ToUpper(s)
	upperKeyword := strings.ToUpper(keyword)
	for i := 0; i < len(s); {
		c := s[i]
		if c == '\'' || c == '"' || c == '`' {
			j := i + 1
			for j < len(s) {
				if s[j] == c {
					if j+1 < len(s) && s[j+1] == c {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			i = j
			continue
		}
		if strings.HasPrefix(upper[i:], upperKeyword) {
			leftOK := i == 0 || isSQLWordBoundaryByte(s[i-1])
			rightIdx := i + len(keyword)
			rightOK := rightIdx >= len(s) || isSQLWordBoundaryByte(s[rightIdx])
			if leftOK && rightOK {
				return i
			}
		}
		i++
	}
	return -1
}

// isGrantWithIdentifiedByPassword reports whether stmt is a GRANT statement
// of the fixed, single-account shape mariadb-dump/mysqldump --system=all
// emits for a classic mysql_native_password account: `GRANT <privs> ON
// <priv_level> TO <account> IDENTIFIED BY PASSWORD '<hash>' [WITH GRANT
// OPTION]`. If it matches, rewritten is stmt with the `IDENTIFIED BY
// PASSWORD '<hash>'` clause removed (everything else, including a trailing
// WITH GRANT OPTION, preserved verbatim). Anything else -- no TO clause,
// multiple comma-separated accounts, an unparseable account spec, no
// IDENTIFIED BY PASSWORD clause, or a REQUIRE clause (whose interaction with
// clause removal across versions/flavors isn't established) -- reports
// ok=false, and the caller must execute stmt unmodified, exactly like today.
func isGrantWithIdentifiedByPassword(stmt string) (rewritten string, user string, host string, hash string, ok bool) {
	trimmed := strings.TrimLeft(stmt, " \t\r\n(")
	if !strings.HasPrefix(strings.ToUpper(trimmed), "GRANT") {
		return "", "", "", "", false
	}
	toIdx := findTopLevelKeyword(trimmed, "TO")
	if toIdx < 0 {
		return "", "", "", "", false
	}
	before := trimmed[:toIdx]
	afterTo := trimmed[toIdx+len("TO"):]
	user, host, tail, ok := parseCreateUserAccountRest(afterTo)
	if !ok {
		return "", "", "", "", false
	}
	hash, afterClause, ok := parseIdentifiedByPasswordClause(tail)
	if !ok {
		return "", "", "", "", false
	}
	if trimmedAfter := strings.TrimLeft(afterClause, " \t\r\n"); len(trimmedAfter) >= 7 && strings.EqualFold(trimmedAfter[:7], "REQUIRE") {
		return "", "", "", "", false
	}
	// accountSpec is the exact original text of the account token (including
	// its leading whitespace and original quoting), located via tail's length
	// -- tail is a content-suffix of afterTo by construction (every step
	// above only slices, never rewrites), so this never re-serializes the
	// account spec from parsed parts.
	accountSpec := afterTo[:len(afterTo)-len(tail)]
	rest := strings.TrimLeft(afterClause, " \t\r\n")
	if rest != "" {
		rest = " " + rest
	}
	rewritten = before + "TO" + accountSpec + rest
	return rewritten, user, host, hash, true
}

// isCannotUserError reports whether err is ER_CANNOT_USER (1396), the error
// CREATE USER raises when the target account already exists.
func isCannotUserError(err error) bool {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		return me.Number == 1396
	}
	return false
}

// isAccessDeniedError reports whether err is ER_SPECIFIC_ACCESS_DENIED_ERROR
// (1227), the error MySQL 8 raises for ALTER USER on a SYSTEM_USER-protected
// account when the connection lacks that privilege.
func isAccessDeniedError(err error) bool {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		return me.Number == 1227
	}
	return false
}

func (server *ServerMonitor) tableExists(schema, table string) (bool, error) {
	if server.Conn == nil {
		cluster := server.ClusterGroup
		if cluster != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlWarn,
				"Splitdump restore skipped mysql table check for %s.%s: server connection not available", schema, table)
		}
		return false, nil
	}
	return tableExistsQuery(func() rowScanner {
		return server.Conn.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?", schema, table)
	}, schema, table)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func tableExistsQuery(query func() rowScanner, schema, table string) (bool, error) {
	if strings.TrimSpace(schema) == "" || strings.TrimSpace(table) == "" {
		return false, fmt.Errorf("schema and table are required")
	}
	if query == nil {
		return false, fmt.Errorf("query function is required")
	}
	var count int
	row := query()
	if row == nil {
		return false, fmt.Errorf("query returned nil row")
	}
	if err := row.Scan(&count); err != nil {
		return false, fmt.Errorf("table exists query failed: %w", err)
	}
	return count > 0, nil
}

func (server *ServerMonitor) buildLogicalRestorePreamble() (string, int, error) {
	cluster := server.ClusterGroup
	if cluster == nil {
		return "", 0, fmt.Errorf("cluster not available")
	}

	// Align logical restores: reset binlog state, control binlog writes, and normalize slow log behavior.
	sqlLogBin := 0
	resetmaster := "RESET MASTER;"
	if server.DBVersion.IsMySQLOrPerconaGreater84() {
		resetmaster = "RESET BINARY LOGS AND GTIDS;"
	}

	master := cluster.GetMaster()
	if master == nil {
		return "", 0, fmt.Errorf("No master found. Cancel backup reseeding %s", server.URL)
	}
	if server.URL == master.URL {
		sqlLogBin = 1
		resetmaster = ""
	}

	cmdstring := fmt.Sprintf("%sSET sql_log_bin=%d;SET long_query_time=10;", resetmaster, sqlLogBin)
	return cmdstring, sqlLogBin, nil
}

func (server *ServerMonitor) JobReseedSplitdumpWithMysql(ctx context.Context, backupPath string, restoreUser bool) error {
	return server.restoreSplitdumpWithMysql(ctx, backupPath, restoreUser)
}

func (server *ServerMonitor) restoreSplitdumpWithMysql(ctx context.Context, backupPath string, restoreUser bool) error {
	cluster := server.ClusterGroup
	if cluster == nil {
		return fmt.Errorf("cluster not available")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	_, sqlLogBin, err := server.buildLogicalRestorePreamble()
	if err != nil {
		return err
	}

	var meta *splitdump.Metadata
	meta, err = splitdump.ReadMetadata(backupPath)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"Splitdump metadata not found; GTID will not be applied (%s)", backupPath)
		case errors.Is(err, splitdump.ErrMetadataInvalid):
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"Splitdump metadata invalid; GTID will not be applied (%s): %v", backupPath, err)
		default:
			return err
		}
		meta = nil
	} else if meta.SourceData == 0 && meta.File == "" && meta.Position == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Splitdump metadata indicates source-data=0; GTID will not be applied (%s)", backupPath)
		meta = nil
	}

	start := time.Now()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Logical restore (splitdump+mysql) started at %s for: %s", start.Format(time.RFC3339), server.URL)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Splitdump restore sets sql_log_bin=%d for %s", sqlLogBin, server.URL)

	defer server.SetInReseedBackup("")

	// Reseed progress (WARN0189): stamp the in-flight restore and its total size so
	// a long splitdump reload shows live "<streamed> out of <total>" per tick.
	// restoreSplitdumpFileGo wraps each shard reader into this one accumulating
	// counter (see countReseedReader).
	server.beginReseedProgress(&ReseedProgress{Backup: backupPath, Tool: "splitdump"}, sumSplitdumpBytes(backupPath))
	defer server.stopReseedProgress()

	// One-time, server-global step: reset the binlog before any data is loaded.
	// Only for a slave reseed (sqlLogBin==0); a master restore (sqlLogBin==1)
	// keeps its binlog so replicas receive the restored data. ResetMaster is
	// version/flavor-aware (RESET MASTER vs MySQL 8.4 RESET BINARY LOGS AND GTIDS).
	if sqlLogBin == 0 {
		if logs, err := server.ResetMaster(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
				"Splitdump restore failed to reset binlog on %s: %v (%s)", server.URL, err, logs)
			return err
		}
	}

	// Open one dedicated pinned connection per parallel worker. We deliberately
	// avoid the shared server.Conn pool: statement ordering, session state
	// (USE / FK / UNIQUE) and transaction affinity all require a single connection
	// held for the whole of a file's load. Each worker gets its OWN *sqlx.DB
	// (GetNewDBConn) and pins one conn from it, rather than pinning N conns off a
	// single shared *sqlx.DB — so the load never contends with the monitor's own
	// pool and each worker's session state is fully isolated (a few extra pool
	// objects, bounded by BackupLogicalLoadThreads). Binlog state follows the
	// master/slave decision: GetConnNoBinlog for a slave reseed, a plain pinned conn
	// (binlog ON) for a master restore.
	parallel := cluster.Conf.BackupLogicalLoadThreads
	if parallel < 1 {
		parallel = 1
	}
	connPool := make(chan *sqlx.Conn, parallel)
	var dbHandles []*sqlx.DB
	closeAll := func() {
		close(connPool)
		for c := range connPool {
			_ = c.Close()
		}
		for _, h := range dbHandles {
			_ = h.Close()
		}
	}
	for i := 0; i < parallel; i++ {
		dbh, connErr := server.GetNewDBConn()
		if connErr != nil {
			closeAll()
			return connErr
		}
		dbHandles = append(dbHandles, dbh)

		var conn *sqlx.Conn
		if sqlLogBin == 0 {
			conn, connErr = server.GetConnNoBinlog(dbh) // slave reseed: keep the restore out of the binlog
		} else {
			conn, connErr = dbh.Connx(ctx) // master restore: leave binlog ON so replicas receive the data
		}
		if connErr != nil {
			closeAll()
			return connErr
		}
		if connErr := server.prepareRestoreConn(ctx, conn); connErr != nil {
			_ = conn.Close()
			closeAll()
			return connErr
		}
		connPool <- conn
	}
	defer closeAll()

	borrow := func() *sqlx.Conn { return <-connPool }
	giveback := func(c *sqlx.Conn) { connPool <- c }

	if cluster.Conf.BackupSplitdumpCreateDatabases {
		schemas, schemaErr := splitdump.ListSchemas(backupPath)
		if schemaErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream,
				config.LvlWarn, "Could not list schemas for CREATE DATABASE: %v", schemaErr)
		} else {
			conn := borrow()
			for _, schema := range schemas {
				escaped := strings.ReplaceAll(schema, "`", "``")
				if _, err := conn.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS `"+escaped+"`"); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream,
						config.LvlWarn, "CREATE DATABASE failed for %s: %v", schema, err)
				}
			}
			giveback(conn)
		}
	}

	// mysql.system-all is dispatched through the dedicated, narrow catalogue
	// replay helper (restoreSystemCatalog) rather than the general splitdump
	// file loader — same connection pool and schema-phase position, but no
	// DEFINER-stripping retry and no blanket error suppression. Every other
	// file's dispatch is unchanged.
	restoreFile := func(ctx context.Context, path string) error {
		conn := borrow()
		defer giveback(conn)
		if splitdump.IsMysqlSystemAll(filepath.Base(path)) {
			_, err := server.restoreSystemCatalog(ctx, conn, path)
			return err
		}
		return server.restoreSplitdumpFileGo(ctx, conn, path, false)
	}
	restoreFileWithoutDefiner := func(ctx context.Context, path string) error {
		conn := borrow()
		defer giveback(conn)
		if splitdump.IsMysqlSystemAll(filepath.Base(path)) {
			_, err := server.restoreSystemCatalog(ctx, conn, path)
			return err
		}
		return server.restoreSplitdumpFileGo(ctx, conn, path, true)
	}

	restoreErr := splitdump.Restore(backupPath, splitdump.RestoreOptions{
		Parallel:    cluster.Conf.BackupLogicalLoadThreads,
		RestoreUser: restoreUser,
		Logger: func(level, format string, args ...any) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, level, format, args...)
		},
		Context:                   ctx,
		RestoreFileWithContext:    restoreFile,
		RestoreFileWithoutDefiner: restoreFileWithoutDefiner,
		DefinerStrict:             cluster.Conf.BackupRestoreDefinerStrict,
	})
	if restoreErr != nil {
		return restoreErr
	}

	if meta != nil && meta.GTID != "" {
		gtidValue := strings.ReplaceAll(meta.GTID, "'", "''")
		switch {
		case server.IsMariaDB() && server.HaveMariaDBGTID:
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
				"Applying splitdump GTID for MariaDB on %s", server.URL)
			if err := server.ExecQueryNoBinLog("SET GLOBAL gtid_slave_pos='"+gtidValue+"'", time.Second); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
					"Failed to apply splitdump GTID for MariaDB on %s: %v", server.URL, err)
			}
		case server.HasMySQLGTID() && server.DBVersion.IsMySQLOrPerconaGreater57():
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
				"Applying splitdump GTID for MySQL on %s", server.URL)
			if err := server.ExecQueryNoBinLog("SET @@GLOBAL.GTID_PURGED='"+gtidValue+"'", time.Second); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
					"Failed to apply splitdump GTID for MySQL on %s: %v", server.URL, err)
			}
		}
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

	// Stamp the real start time now, before the potentially slow work below
	// (backup file resolution, StopAllSlaves, pointSlaveToMaster, the restore
	// itself) -- but only for a type the dispatch below actually executes.
	// Stamping unconditionally here would risk leaving an unsupported type
	// stuck at "processing" forever, since the dispatch below has no case for
	// it; only stamp when we know a terminal call is guaranteed to follow,
	// without changing what counts as a supported type.
	if cluster.Conf.BackupLoadScript != "" || backtype == config.ConstBackupLogicalTypeMysqldump || backtype == config.ConstBackupLogicalTypeMydumper {
		server.JobsUpdateStateRuntimeOnly(task, "processing", JobStateRunning, 0)
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
	}
	if len(destCandidates) == 0 {
		destCandidates = []string{dest}
	}

	// Skip file lookup if using custom script
	if backtype != "script" {
		// Pick a logical backup of the configured tool from ANY node via the generic
		// restore selector (see doc/implementation/cluster/RESTORE_SELECTOR.md). Was:
		// a master-only lookup that aborted with "No backup file found on master"
		// whenever the elected master had never been backed up. In a rejoin any
		// consistent backup works — replication catches up the delta — so the source
		// node does not matter. Local-only for now (remote/restic fetch is not wired).
		sel := cluster.getAutorejoinBackupSelector("logical")
		sel.Tool = []string{backtype} // restore dispatch is backtype-based → force the tool for consistency
		pick := ResolveRestore(cluster.buildBackupCatalog(), sel,
			ResolveContext{MasterURL: master.URL, TargetURL: server.URL})
		if pick != nil && pick.isLocal() {
			backupfile = pick.Path
			if s := cluster.GetServerFromURL(pick.Server); s != nil {
				source = s
			}
			useMaster = source == master
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "flashback %s: selector picked %s backup %s", backtype, source.URL, backupfile)
		} else {
			// Fallback (no catalogued local backup — metadata not populated): legacy
			// on-disk lookup on master then backup server.
			backupfile, _ = findExistingBackupPath(master, destCandidates)
			if backupfile == "" {
				backupfile = master.GetMyBackupDirectory() + dest
			}
			bckserver := cluster.GetBackupServer()
			if bckserver != nil && bckserver.HasBackupTypeCookie(backtype) {
				if resolved, ok := findExistingBackupPath(bckserver, destCandidates); ok {
					backupfile = resolved
					useMaster = false
					source = bckserver
				} else {
					bckserver.DelBackupTypeCookie(backtype)
				}
			}
			if useMaster {
				if _, err := os.Stat(backupfile); err != nil {
					master.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
					noBackupErr := fmt.Errorf("Cancelling reseed. No logical %s backup found on any node", backtype)
					server.JobsUpdateStateRuntimeOnly(task, noBackupErr.Error(), 5, 1)
					return noBackupErr
				}
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

	// Stop ALL replication connections before the reseed. StopSlave() only stops
	// the cluster.Conf.MasterConn channel (empty by default → the unnamed default
	// connection), so a server replicating on a NAMED connection (e.g. 'curepipe')
	// stays running and the dump's embedded position statement fails with
	// ERROR 1198 "you have a running slave ... run STOP SLAVE '<name>' first".
	// StopAllSlaves iterates server.Replications and stops each by its real
	// ConnectionName, so every named channel is stopped.
	logs, err := server.StopAllSlaves()
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed stop all slaves on server: %s %s", server.URL, err)
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
		server.JobsUpdateStateRuntimeOnly(task, err.Error(), 5, 1)
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive flashback logical backup %s request for server: %s", backtype, server.URL)

	// If a custom script is configured, use it
	if cluster.Conf.BackupLoadScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Using script from backup-load-script on %s", server.URL)
		if err := server.JobReseedBackupScript(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error flashback %s on %s: %s", backtype, server.URL, err.Error())
			server.JobsUpdateStateRuntimeOnly(task, err.Error(), 5, 1)
			return err
		}
		server.JobsUpdateStateRuntimeOnly(task, "Flashback completed", 3, 1)
		return nil

		// Handle mysqldump-based reseed
	} else if backtype == config.ConstBackupLogicalTypeMysqldump {
		// Same trust rule and restoreUser formula as JobReseedLogicalBackupPrepare
		// (see resolveLogicalReseedUserRestore,
		// doc/implementation/cluster/SYSTEM_ALL_RESEED_IMPLEMENTATION_STATUS.md):
		// source.LastBackupMeta.Logical is read via snapshotLogicalBackupMeta
		// (thread-safe, unlike the raw field access this replaces) and, like the
		// main logical reseed flow, backupfile here can be selected from any
		// node via ResolveRestore/the legacy fallback lookup above, so source's
		// metadata is not guaranteed to describe this exact backupfile --
		// untrusted/unrelated metadata must not silently suppress user restore,
		// and restoreUser is no longer multiplied by splitUser at all.
		meta := snapshotLogicalBackupMeta(source)
		restoreUser, _, userRestoreAssessment := resolveLogicalReseedUserRestore(cluster, backtype, backupfile, meta, nil)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Flashback logical backup preflight for %s: %s", server.URL, userRestoreAssessment.Message)
		err := server.reseedMysqldumpWithMetadata(context.Background(), backupfile, restoreUser, meta)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error flashback %s on %s: %s", backtype, server.URL, err.Error())
			server.JobsUpdateStateRuntimeOnly(task, err.Error(), 5, 1)
		} else {
			// Restart slave if needed. Symmetric with StopAllSlaves above: restart
			// every connection by its real ConnectionName so a multi-source server
			// does not leave its extra source connections stopped.
			if server.IsSlave {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Start slave after dump on %s", server.URL)
				for _, rep := range server.Replications {
					if logs, e := server.StartSlaveChannel(rep.ConnectionName.String); e != nil {
						cluster.LogSQL(logs, e, server.URL, "Rejoin", config.LvlErr, "Failed start slave channel '%s' after flashback on %s: %s", rep.ConnectionName.String, server.URL, e)
					}
				}
			}

			server.JobsUpdateStateRuntimeOnly(task, "Flashback completed", 3, 1)
		}

		// Handle mydumper-based reseed
	} else if backtype == config.ConstBackupLogicalTypeMydumper {
		err := server.JobReseedMyLoader(backupfile, cluster.Conf.BackupRestoreMysqlUser)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error flashback %s on %s: %s", backtype, server.URL, err.Error())
			server.JobsUpdateStateRuntimeOnly(task, err.Error(), 5, 1)
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
					// Symmetric with StopAllSlaves above (multi-source safe):
					// restart every connection by its real ConnectionName.
					for _, rep := range server.Replications {
						if logs, e := server.StartSlaveChannel(rep.ConnectionName.String); e != nil {
							cluster.LogSQL(logs, e, server.URL, "Rejoin", config.LvlErr, "Failed start slave channel '%s' after flashback on %s: %s", rep.ConnectionName.String, server.URL, e)
						}
					}
				}
			}

			server.JobsUpdateStateRuntimeOnly(task, "Flashback completed", 3, 1)
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

	// Progress: count compressed bytes streamed out of the file size so the per-tick
	// reseed state reports "<streamed> streamed out of <total> compressed backup at
	// <rate>/s". Rate is rolling (per tick) since it varies a lot by table.
	var total int64
	if fi, e := gzfile.Stat(); e == nil {
		total = fi.Size()
	}
	counted := server.startReseedProgress(&ReseedProgress{Backup: backupfile}, gzfile, total)
	defer server.stopReseedProgress()

	// Use configurable parallel blocks for better performance
	// For restore operations, use higher default (16) for speed, matching original behavior
	parallelBlocks := cluster.getSanitizedParallelBlocks(config.ConstLogModTask)
	bufferSize := cluster.getSanitizedDecompressBufferSize(config.ConstLogModTask)
	fz, err := gzip.NewReaderN(counted, bufferSize, parallelBlocks)
	if err != nil {
		return fmt.Errorf("[%s] Failed to unzip backup file in backup server for reseed:  %s ", server.URL, err)
	}
	defer fz.Close()

	cliParams := append(cluster.GetDumpCredentials(server), server.GetSSLClientParam("client")...)
	cliParams = append(cliParams, strings.Split(cluster.Conf.BackupMysqlclientOptions, " ")...)
	clientCmd := exec.Command(cluster.GetMysqlclientPath(), misc.RemoveEmptyString(cliParams)...)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command: %s ", strings.Replace(clientCmd.String(), "="+cluster.GetDbPass(), "=XXXX", -1))

	cmdstring, sqlLogBin, err := server.buildLogicalRestorePreamble()
	if err != nil {
		return err
	}

	// mysql.system-all content (INSTALL PLUGIN/CREATE USER/etc.) embedded in the
	// dump is classified out here and replayed separately through
	// restoreSystemCatalog instead of being piped blindly into the mysql client
	// below -- the file-based sibling of JobRejoinMysqldumpFromSource's live-stream
	// classify/replay model (see doc/implementation/cluster/
	// SYSTEM_ALL_RESEED_IMPLEMENTATION_STATUS.md). A dump with no system content
	// (the common case) just produces an empty, discarded artifact and finishes
	// after phase one below -- there is no separate "is this a --system=all dump"
	// pre-check driving which code path runs.
	jobIDSuffix, err := randomHexSuffix(6)
	if err != nil {
		return fmt.Errorf("[%s] Failed to generate reseed job id: %s", server.URL, err)
	}
	artifactWriter, err := server.newDirectReseedSystemArtifactWriter("mysqldump-"+jobIDSuffix, start)
	if err != nil {
		return fmt.Errorf("[%s] Failed to create system-catalogue artifact: %s", server.URL, err)
	}

	// StdinPipe (rather than Cmd.Stdin = io.MultiReader(...), used before this
	// change) gives splitdump.ClassifyStream a real io.Writer for its
	// ApplicationWriter, and means Wait() below reflects only process exit --
	// the pump goroutine below owns writing to it independently, same reasoning
	// as JobRejoinMysqldumpFromSource's identical choice.
	clientStdin, err := clientCmd.StdinPipe()
	if err != nil {
		artifactWriter.discard()
		return fmt.Errorf("[%s] Failed to create mysql client stdin pipe: %s", server.URL, err)
	}

	// Own the stdout/stderr pipe directly (rather than clientCmd.StdoutPipe())
	// so Cmd.Wait()'s unconditional close of its own registered pipes on
	// process exit can't race the stderr-tail drain goroutine below and
	// truncate the very diagnostic tail a failure needs -- same reasoning as
	// JobRejoinMysqldumpFromSource's identical pipe ownership.
	clientOutR, clientOutW, err := os.Pipe()
	if err != nil {
		artifactWriter.discard()
		clientStdin.Close()
		return fmt.Errorf("[%s] Failed to create mysql client output pipe: %s", server.URL, err)
	}
	clientCmd.Stdout = clientOutW
	clientCmd.Stderr = clientOutW

	if err := clientCmd.Start(); err != nil {
		artifactWriter.discard()
		clientStdin.Close()
		clientOutW.Close()
		clientOutR.Close()
		return fmt.Errorf("Can't start mysql client:%s at %s", err, strings.ReplaceAll(clientCmd.String(), "="+cluster.GetDbPass(), "=XXXX"))
	}
	// The child now holds its own inherited copy of clientOutW -- close ours so
	// the read end can see EOF once the child exits.
	clientOutW.Close()

	const stderrTailLines = 20
	wg := sync.WaitGroup{}
	var clientTail []string
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer clientOutR.Close()
		clientTail = server.copyLogsTail(clientOutR, config.ConstLogModBackupStream, config.LvlDbg, stderrTailLines)
	}()

	type reseedPumpResult struct {
		result       splitdump.ClassifyResult
		err          error
		fromClassify bool
	}
	pumpResultCh := make(chan reseedPumpResult, 1)
	go func() {
		defer clientStdin.Close()
		result, pumpErr, fromClassify := runReseedMysqldumpPump(clientStdin, cmdstring, fz, artifactWriter)
		pumpResultCh <- reseedPumpResult{result: result, err: pumpErr, fromClassify: fromClassify}
	}()

	// No context/cancel, no stall watchdog: unlike JobRejoinMysqldumpFromSource
	// (which arbitrates between a live mysqldump subprocess and this mysql
	// client, either of which can stall the other), there is exactly one
	// subprocess here fed by a goroutine reading a local file, which cannot
	// "stall" the way a live network-fed dump can. Sequential Wait() then
	// wg.Wait() then draining the pump channel is sufficient.
	clientErr := clientCmd.Wait()
	wg.Wait()
	pr := <-pumpResultCh

	if clientErr != nil || pr.err != nil {
		artifactWriter.discard()
		msg := reseedMysqldumpFailureMessage(server.URL, clientErr, pr.err, pr.fromClassify, clientTail)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", msg)
		return errors.New(msg)
	}

	// Phase two: exactly one system-catalogue source is ever replayed, matching
	// direct reseed's model where the classified artifact is the sole
	// authority. reseedMysqldumpSystemReplaySource makes that branch decision a
	// pure, directly testable function rather than inline control flow, so the
	// single-authority guarantee (restore-user=false always skips regardless of
	// content; an inline mysql.system-all match always wins over the sidecar,
	// which is therefore never even consulted) has unit coverage independent of
	// spawning a real mysql client -- see
	// doc/implementation/cluster/SYSTEM_ALL_RESEED_IMPLEMENTATION_STATUS.md.
	switch reseedMysqldumpSystemReplaySource(restoreUser, pr.result.HasSystemContent) {
	case reseedMysqldumpSystemSourceNone:
		artifactWriter.discard()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Logical restore (mysqldump): system replay phase skipped for %s (restore-user disabled)", server.URL)

	case reseedMysqldumpSystemSourceMainDump:
		if err := server.publishAndReplayReseedMysqldumpSystemArtifact(artifactWriter, pr.result.Metadata, "file:"+backupfile, sqlLogBin); err != nil {
			return err
		}

	default: // reseedMysqldumpSystemSourceSidecar
		// The main dump carried no mysql.system-all content -- expected on
		// MySQL/Percona, where --system=all is stripped from the dump options
		// (getDumpParameter, cluster_get.go) -- so mysql.users.sql.gz, if the
		// backup was taken with backup-split-mysql-user, is the only remaining
		// user-restore source.
		artifactWriter.discard()
		if err := server.replayReseedMysqldumpUserSidecar(backupfile, start, sqlLogBin); err != nil {
			return err
		}
	}

	elapsed := time.Since(start).Round(time.Second)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Finish logical restore (mysqldump) in %s (started at %s) for: %s", elapsed, start.Format(time.RFC3339), server.URL)
	return nil
}

// reseedMysqldumpSystemReplayConn acquires the connection JobReseedMysqldump's
// phase two replays the extracted system-catalogue artifact over. Binlog
// state must match the preamble phase one already sent (SET sql_log_bin=%d,
// buildLogicalRestorePreamble) -- same branching restoreSplitdumpWithMysql
// uses for its own connection pool: GetConnNoBinlog for a slave reseed
// (sqlLogBin==0), a plain pinned connection -- binlog ON -- for a master
// restore (sqlLogBin==1, server.URL == cluster master), so replicas still
// receive the replayed system-catalogue statements instead of losing them to
// an unconditionally-unlogged replay connection.
func (server *ServerMonitor) reseedMysqldumpSystemReplayConn(ctx context.Context, dbh *sqlx.DB, sqlLogBin int) (*sqlx.Conn, error) {
	if sqlLogBin == 0 {
		return server.GetConnNoBinlog(dbh)
	}
	return dbh.Connx(ctx)
}

// reseedMysqldumpSystemSource identifies which system-catalogue source (if
// any) JobReseedMysqldump's phase two replays.
type reseedMysqldumpSystemSource int

const (
	// reseedMysqldumpSystemSourceNone means phase two replays nothing:
	// restore-user is disabled, so neither an inline mysql.system-all match
	// nor the mysql.users.sql.gz sidecar is ever consulted, regardless of what
	// phase one's classify pass found.
	reseedMysqldumpSystemSourceNone reseedMysqldumpSystemSource = iota
	// reseedMysqldumpSystemSourceMainDump means the main dump's own classified
	// mysql.system-all content is the sole authority -- the sidecar, even if
	// present on disk, is never opened or consulted in this case.
	reseedMysqldumpSystemSourceMainDump
	// reseedMysqldumpSystemSourceSidecar means the main dump carried no
	// mysql.system-all content, so the mysql.users.sql.gz sidecar (if any) is
	// the only remaining candidate source.
	reseedMysqldumpSystemSourceSidecar
)

// reseedMysqldumpSystemReplaySource decides JobReseedMysqldump's phase-two
// branch: exactly one system-catalogue source is ever replayed, matching
// direct reseed's single-authority model. Pulled out as a pure function
// (rather than left as inline control flow) specifically so this decision --
// restore-user=false always wins over any content found, and an inline
// mysql.system-all match always wins over the sidecar -- has direct unit
// coverage without spawning a real mysql client or database connection.
func reseedMysqldumpSystemReplaySource(restoreUser bool, mainDumpHasSystemContent bool) reseedMysqldumpSystemSource {
	switch {
	case !restoreUser:
		return reseedMysqldumpSystemSourceNone
	case mainDumpHasSystemContent:
		return reseedMysqldumpSystemSourceMainDump
	default:
		return reseedMysqldumpSystemSourceSidecar
	}
}

// runReseedMysqldumpPump writes the restore preamble to appWriter, then
// classifies dumpReader (the main mysqldump stream) into application SQL
// (appWriter) and system-catalogue SQL (systemWriter) via
// splitdump.ClassifyStream. Phase one restores application SQL only --
// mysql.users.sql.gz, when relevant, is a phase-two-only source handled
// separately by replayReseedMysqldumpUserSidecar, never injected here, so
// there is exactly one authority for system/user SQL per restore (see
// JobReseedMysqldump). Factored out of JobReseedMysqldump's pump goroutine so
// the classify/dispatch logic can be tested with bytes.Buffer/strings.Reader
// fakes instead of a real mysql subprocess and gzip file.
//
// fromClassify reports whether a non-nil error originated inside
// ClassifyStream itself (system extraction) rather than while writing the
// preamble beforehand (application restore) -- callers use this to pick the
// right reseedStage for the returned error.
func runReseedMysqldumpPump(appWriter io.Writer, cmdstring string, dumpReader io.Reader, systemWriter io.Writer) (result splitdump.ClassifyResult, err error, fromClassify bool) {
	if _, err := io.WriteString(appWriter, cmdstring); err != nil {
		return splitdump.ClassifyResult{}, fmt.Errorf("writing restore preamble to mysql client stdin: %w", err), false
	}
	result, err = splitdump.ClassifyStream(dumpReader, splitdump.ClassifyOptions{
		ApplicationWriter: appWriter,
		SystemWriter:      systemWriter,
	})
	if err != nil {
		return result, fmt.Errorf("classifying mysqldump output into application/system SQL: %w", err), true
	}
	return result, nil, false
}

// publishAndReplayReseedMysqldumpSystemArtifact is JobReseedMysqldump's sole
// phase-two replay path, shared by both possible system-catalogue sources:
// the classified main-dump artifact (mysql.system-all content found inline)
// and the mysql.users.sql.gz sidecar fallback (see
// replayReseedMysqldumpUserSidecar) -- whichever one phase one determined is
// the single authority for this restore. Reusing one publish/state/replay
// path for both keeps retryability, diagnostics, and artifact-state tracking
// identical regardless of which source produced the content, rather than
// growing a second, unaudited replay path for the fallback case.
func (server *ServerMonitor) publishAndReplayReseedMysqldumpSystemArtifact(artifactWriter *directReseedSystemArtifactWriter, meta splitdump.Metadata, sourceServer string, sqlLogBin int) error {
	cluster := server.ClusterGroup

	finalDir, publishErr := artifactWriter.publish(meta, directReseedArtifactExtra{
		SourceServer:          sourceServer,
		DestinationServer:     server.URL,
		DestinationFamily:     server.DBVersion.Flavor,
		DestinationMajorMinor: directReseedServerMajorMinor(server.DBVersion),
		BoundaryFormat:        "v1-eof-bounded",
		ArtifactState:         directReseedArtifactStatePublished,
	})
	if publishErr != nil {
		msg := fmt.Sprintf("%s: publish artifact for %s: %s", reseedStageSystemExtraction, server.URL, publishErr)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", msg)
		return errors.New(msg)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical restore (mysqldump): replaying system catalogue on %s", server.URL)
	// Mark in-progress before executing any SQL -- if we can't durably
	// record that replay is starting, we must not proceed to run
	// statements whose completion state we then couldn't reliably track
	// either.
	if err := setDirectReseedArtifactState(finalDir, directReseedArtifactStateReplayInProgress); err != nil {
		msg := fmt.Sprintf("%s: record replay-in-progress state for artifact %s: %s", reseedStageSystemCatalogReplay, finalDir, err)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", msg)
		return errors.New(msg)
	}

	progressed, replayErr := func() (bool, error) {
		dbh, connErr := server.GetNewDBConn()
		if connErr != nil {
			return false, connErr
		}
		defer dbh.Close()
		conn, connErr := server.reseedMysqldumpSystemReplayConn(context.Background(), dbh, sqlLogBin)
		if connErr != nil {
			return false, connErr
		}
		defer conn.Close()
		return server.restoreSystemCatalog(context.Background(), conn, filepath.Join(finalDir, directReseedSystemArtifactName))
	}()

	if replayErr != nil {
		// A failure before any statement committed is safe to retry from
		// the beginning; a failure after at least one commit is not, since
		// most --system=all statement classes besides INSTALL PLUGIN are
		// not proven replay-idempotent.
		failState := directReseedArtifactStateReplayFailed
		if !progressed {
			failState = directReseedArtifactStateReplayFailedSafe
		}
		if stateErr := setDirectReseedArtifactState(finalDir, failState); stateErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"Failed to record artifact state %s for %s after replay failure: %s", failState, finalDir, stateErr)
		}
		msg := fmt.Sprintf("%s: %s: %s", reseedStageSystemCatalogReplay, server.URL, replayErr)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", msg)
		return errors.New(msg)
	}
	if err := setDirectReseedArtifactState(finalDir, directReseedArtifactStateReplaySucceeded); err != nil {
		// The DB replay itself succeeded, but we can't durably prove it --
		// an artifact whose recorded state doesn't reflect reality is a
		// retry-safety hazard, so this is surfaced as a job failure rather
		// than silently proceeding.
		msg := fmt.Sprintf("%s: replay succeeded but failed to record terminal state for artifact %s: %s", reseedStageSystemCatalogReplay, finalDir, err)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", msg)
		return errors.New(msg)
	}
	return nil
}

// replayReseedMysqldumpUserSidecar is JobReseedMysqldump's phase-two fallback:
// called only when restore-user is enabled and the main dump carried no
// mysql.system-all content (so nothing was published from phase one), it
// looks for the separate mysql.users.sql.gz sidecar produced by
// backup-split-mysql-user and, if present, classifies and replays it through
// the exact same publish/state/replay path as the main-dump artifact
// (publishAndReplayReseedMysqldumpSystemArtifact) -- it is never injected
// into phase one, so it can never be a second, concurrent source of
// system/user SQL alongside a classified main-dump artifact. A missing
// sidecar is a no-op, not a failure: it means the backup simply has no
// user-restore source available (e.g. it wasn't taken with
// backup-split-mysql-user), which JobReseedMysqldump's caller already
// tolerates for a dump with no mysql.system-all content either.
func (server *ServerMonitor) replayReseedMysqldumpUserSidecar(backupfile string, start time.Time, sqlLogBin int) error {
	cluster := server.ClusterGroup

	jobIDSuffix, err := randomHexSuffix(6)
	if err != nil {
		return fmt.Errorf("[%s] Failed to generate reseed job id: %s", server.URL, err)
	}
	sidecarArtifactWriter, err := server.newDirectReseedSystemArtifactWriter("mysqldump-user-"+jobIDSuffix, start)
	if err != nil {
		return fmt.Errorf("[%s] Failed to create system-catalogue artifact: %s", server.URL, err)
	}

	result, ok, classifyErr := server.classifyReseedMysqldumpUserSidecar(backupfile, sidecarArtifactWriter)
	if classifyErr != nil {
		sidecarArtifactWriter.discard()
		if errors.Is(classifyErr, os.ErrNotExist) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
				"Logical restore (mysqldump): no system-catalogue content and no mysql.users.sql.gz sidecar found for %s; system replay phase skipped", server.URL)
			return nil
		}
		msg := fmt.Sprintf("%s: %s: %s", reseedStageSystemExtraction, server.URL, classifyErr)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", msg)
		return errors.New(msg)
	}
	if !ok {
		sidecarArtifactWriter.discard()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Logical restore (mysqldump): mysql.users.sql.gz sidecar for %s contained no system-catalogue content; system replay phase skipped", server.URL)
		return nil
	}

	return server.publishAndReplayReseedMysqldumpSystemArtifact(sidecarArtifactWriter, result.Metadata, "file:"+mysqldumpUserSidecarPath(backupfile), sqlLogBin)
}

// classifyReseedMysqldumpUserSidecar opens the mysql.users.sql.gz sidecar (if
// any) next to backupfile and classifies it into systemWriter, the same
// splitdump.ClassifyStream pass runReseedMysqldumpPump uses for the main
// dump -- the sidecar is a raw mysqldump --system=user stream, not
// pre-classified, so dump preamble/comment lines are discarded rather than
// corrupting the system artifact. ok reports whether the sidecar both exists
// and produced system content; a missing sidecar is reported as an
// os.ErrNotExist-wrapping error (ok=false) so the caller can tell "no source
// available" apart from a genuine I/O or classify failure. Split out of
// replayReseedMysqldumpUserSidecar so the classify/skip decision is testable
// without a live database connection (publishAndReplayReseedMysqldumpSystemArtifact,
// unlike this function, calls GetNewDBConn).
func (server *ServerMonitor) classifyReseedMysqldumpUserSidecar(backupfile string, systemWriter io.Writer) (result splitdump.ClassifyResult, ok bool, err error) {
	sidecarReader, err := server.ReadMysqldumpUser(backupfile)
	if err != nil {
		return splitdump.ClassifyResult{}, false, err
	}
	defer sidecarReader.Close()
	result, err = splitdump.ClassifyStream(sidecarReader, splitdump.ClassifyOptions{
		ApplicationWriter: io.Discard,
		SystemWriter:      systemWriter,
	})
	if err != nil {
		return result, false, err
	}
	return result, result.HasSystemContent, nil
}

// reseedMysqldumpFailureMessage attributes a JobReseedMysqldump failure to the
// stage that caused it. Unlike reseedFailureMessage (JobRejoinMysqldumpFromSource's
// sibling, which arbitrates between two concurrent subprocesses racing each
// other), there is only one subprocess here -- the mysql client -- fed by a
// single pump goroutine reading a local file, so a nonzero client exit is
// always the authoritative signal: a concurrent pump error in that case is
// almost always collateral (a broken pipe from writing into a stdin the
// client already closed by dying), not an independent root cause.
func reseedMysqldumpFailureMessage(serverURL string, clientErr, pumpErr error, fromClassify bool, clientTail []string) string {
	if clientErr != nil {
		msg := fmt.Sprintf("%s: mysql client on %s: %s", reseedStageApplicationRestore, serverURL, clientErr)
		if len(clientTail) > 0 {
			msg += " | stderr: " + strings.Join(clientTail, " / ")
		}
		return msg
	}
	stage := reseedStageApplicationRestore
	if fromClassify {
		stage = reseedStageSystemExtraction
	}
	return fmt.Sprintf("%s: %s: %s", stage, serverURL, pumpErr)
}

// mysqldumpUserSidecarPath returns the path JobBackupMysqldumpUser writes and
// ReadMysqldumpUser/replayReseedMysqldumpUserSidecar read: the mysqldump
// --system=user sidecar produced alongside backupfile when
// backup-split-mysql-user is enabled.
func mysqldumpUserSidecarPath(backupfile string) string {
	return filepath.Join(filepath.Dir(backupfile), "mysql.users.sql.gz")
}

// hasMysqldumpUserSidecar reports whether the mysql.users.sql.gz sidecar
// exists next to backupfile, without opening or reading it -- a cheap
// existence probe for preflight messaging
// (assessLogicalReseedUserRestoreAvailability), reusing the exact path
// ReadMysqldumpUser/replayReseedMysqldumpUserSidecar consult at restore time
// so preflight and actual restore never disagree about where to look.
func hasMysqldumpUserSidecar(backupfile string) (bool, error) {
	_, err := os.Stat(mysqldumpUserSidecarPath(backupfile))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// gzipFileReadCloser closes both the gzip reader and the underlying file it
// wraps -- gzip.Reader.Close (pgzip included) only closes the gzip stream,
// never the io.Reader it was built from, so ReadMysqldumpUser's caller needs
// a single Close that accounts for both or the underlying *os.File leaks.
type gzipFileReadCloser struct {
	*gzip.Reader
	file *os.File
}

func (g *gzipFileReadCloser) Close() error {
	gzErr := g.Reader.Close()
	fileErr := g.file.Close()
	if gzErr != nil {
		return gzErr
	}
	return fileErr
}

// ReadMysqldumpUser returns the decompressed mysql.users.sql.gz sidecar next
// to backupfile. A missing directory or sidecar file is reported as an error
// wrapping os.ErrNotExist so replayReseedMysqldumpUserSidecar can tell "no
// sidecar available" (a tolerated no-op) apart from a genuine I/O failure.
// The returned io.ReadCloser owns both the gzip reader and the underlying
// file; the caller must Close it.
func (server *ServerMonitor) ReadMysqldumpUser(backupfile string) (io.ReadCloser, error) {
	cluster := server.ClusterGroup
	var err error

	dir := filepath.Dir(backupfile)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: directory %s does not exist", os.ErrNotExist, dir)
	}

	userpath := mysqldumpUserSidecarPath(backupfile)
	if _, err := os.Stat(userpath); os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: file %s does not exist", os.ErrNotExist, userpath)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Opening mysql.user file %s", userpath)

	gzfile, err := os.Open(userpath)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed opening mysql.user file in backup server for reseed:  %s ", server.URL, err)
	}

	// Use configurable parallel blocks for better performance
	// For restore operations, use higher default (16) for speed, matching original behavior
	parallelBlocks := cluster.getSanitizedParallelBlocks(config.ConstLogModTask)
	bufferSize := cluster.getSanitizedDecompressBufferSize(config.ConstLogModTask)
	fz, err := gzip.NewReaderN(gzfile, bufferSize, parallelBlocks)
	if err != nil {
		gzfile.Close()
		return nil, fmt.Errorf("[%s] Failed to unzip backup file in backup server for reseed:  %s ", server.URL, err)
	}

	return &gzipFileReadCloser{Reader: fz, file: gzfile}, nil
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
func (server *ServerMonitor) JobReseedBackupScript() error {
	cluster := server.ClusterGroup
	defer server.SetInReseedBackup("")
	start := time.Now()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical restore (script) started at %s for: %s", start.Format(time.RFC3339), server.URL)

	master := cluster.GetMaster()
	if master == nil {
		err := fmt.Errorf("No master found. Cancel backup reseeding %s", server.URL)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%v", err)
		return err
	}
	cmd := exec.Command(cluster.Conf.BackupLoadScript, misc.Unbracket(server.Host), misc.Unbracket(master.Host), server.Port, master.Port, cluster.GetDbUser(), cluster.GetDbPass(), cluster.Name)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command backup load script: %s", strings.Replace(cmd.String(), "="+cluster.GetDbPass(), "=XXXX", 1))

	stdoutIn, _ := cmd.StdoutPipe()
	stderrIn, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "My reload script start failed: %s", err)
		return err
	}
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
		return err
	}
	elapsed := time.Since(start).Round(time.Second)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Finish logical restore (script) in %s (started at %s) for: %s", elapsed, start.Format(time.RFC3339), server.URL)
	return nil
}

// GetMyBackupDirectoryPath returns this server's backup output directory
// without creating it on disk. Use GetMyBackupDirectory when the directory
// needs to exist, e.g. before writing a backup file.
func (server *ServerMonitor) GetMyBackupDirectoryPath() string {
	cluster := server.ClusterGroup
	return cluster.Conf.WorkingDir + "/" + config.ConstStreamingSubDir + "/" + cluster.Name + "/" + server.Host + "_" + server.Port + "/"
}

func (server *ServerMonitor) GetMyBackupDirectory() string {
	cluster := server.ClusterGroup
	s3dir := server.GetMyBackupDirectoryPath()

	if _, err := os.Stat(s3dir); os.IsNotExist(err) {
		err := os.MkdirAll(s3dir, os.ModePerm)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Create backup path failed: %s: %s", s3dir, err)
		}
	}

	return s3dir

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

// backupStallWatchdog aborts a backup whose output has stopped accepting writes
// (a dead/hung backup volume). It samples a byte-progress counter every
// checkInterval; if the counter does not advance for stallTimeout, it invokes
// onStall and cancel() — which kills the dump + splitdump subprocesses and
// unblocks the pipe so the backup returns an error instead of hanging forever.
// It returns when done is closed (normal completion) or on stall. stallTimeout
// <= 0 disables it. Kept standalone so it is unit-testable without a DB or mount.
func backupStallWatchdog(done <-chan struct{}, cancel context.CancelFunc, progress *atomic.Int64, stallTimeout, checkInterval time.Duration, onStall func()) {
	if stallTimeout <= 0 {
		return
	}
	if checkInterval <= 0 {
		checkInterval = time.Second
	}
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	last := progress.Load()
	var idle time.Duration
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			cur := progress.Load()
			if cur != last {
				last = cur
				idle = 0
				continue
			}
			idle += checkInterval
			if idle >= stallTimeout {
				if onStall != nil {
					onStall()
				}
				cancel()
				return
			}
		}
	}
}

// backupStallLeakGrace is how long, after the stall watchdog has cancelled, we
// still wait for the backup's reader/pipeline goroutines to unwind before giving
// up on them. SIGKILL (from context cancel) frees a normal subprocess well within
// this window; a subprocess wedged in uninterruptible sleep (D-state) on a
// hard-hung mount never dies, so we stop waiting and leak it rather than hang the
// backup forever.
const backupStallLeakGrace = 60 * time.Second

// boundedWait blocks until wg completes. If `fired` is signalled (the stall
// watchdog cancelled) and wg still hasn't completed after `grace`, it returns
// true — the goroutine is stuck (e.g. a subprocess in uninterruptible sleep on a
// hung mount that SIGKILL can't free) and must be treated as leaked so the caller
// can return instead of blocking indefinitely. Returns false on normal
// completion. The internal waiter goroutine leaks with wg only in the true case,
// which is exactly the unavoidable hard-hung-mount scenario.
func boundedWait(wg *sync.WaitGroup, fired <-chan struct{}, grace time.Duration) bool {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return false
	case <-fired:
		select {
		case <-done:
			return false
		case <-time.After(grace):
			return true
		}
	}
}

func (server *ServerMonitor) JobBackupMysqldump(ctx context.Context, task, filename string, allowRotate bool) error {
	cluster := server.ClusterGroup
	var err error
	var bckConn *sqlx.DB
	if ctx == nil {
		ctx = context.Background()
	}

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

	dumpCtx, dumpCancel := context.WithCancel(ctx)
	defer dumpCancel()
	server.registerJobCancel(task, dumpCancel)
	defer server.clearJobCancel(task)
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
		if errors.Is(err, context.Canceled) && server.isJobCancelRequested(task) {
			return errors.Join(errJobCanceledByUser, err)
		}
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

	// Write-stall watchdog. A dead/hung backup output volume blocks the write
	// side of the pipe, which back-pressures this read loop; without this the
	// backup hangs forever and its deferred InLogicalBackup clear never runs
	// (the "STALLED pill that won't clear" incident). If no bytes flow for the
	// configured timeout, cancel the dump + splitdump subprocesses so the backup
	// fails cleanly and the caller's defers run. See
	// doc/implementation/cluster/BACKUP_DEAD_VOLUME_STALL.md.
	var bytesProgress atomic.Int64
	var stalled atomic.Bool
	stallDone := make(chan struct{})
	stallFired := make(chan struct{})
	if cluster.Conf.BackupWriteStallTimeout < 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
			"backup-write-stall-timeout is negative (%d) — the write-stall watchdog is DISABLED; use 0 to disable intentionally, or a positive number of seconds to enable", cluster.Conf.BackupWriteStallTimeout)
	}
	stallTimeout := time.Duration(cluster.Conf.BackupWriteStallTimeout) * time.Second
	checkInterval := stallTimeout / 4
	if checkInterval < time.Second {
		checkInterval = time.Second
	}
	go backupStallWatchdog(stallDone, dumpCancel, &bytesProgress, stallTimeout, checkInterval, func() {
		stalled.Store(true)
		close(stallFired)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
			"Backup write stalled for %s with no output written (dead/hung backup volume?); aborting backup for %s", stallTimeout, server.URL)
	})

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
			bytesProgress.Add(int64(n))
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

	// Bounded wait: normally block until the reader goroutine unwinds, but if the
	// watchdog cancelled and the subprocess is stuck in uninterruptible sleep
	// (D-state, a hard-hung NFS-style mount) SIGKILL cannot free it and this wait
	// would never return — so after backupStallLeakGrace give up, leak the stuck
	// goroutine, and return the stall error. Best-effort mitigation for the
	// hard-hung-mount case, not a hard guarantee — see BACKUP_DEAD_VOLUME_STALL.md.
	leaked := boundedWait(&wg, stallFired, backupStallLeakGrace)
	close(stallDone) // stop the watchdog
	if leaked {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
			"Backup reader did not unwind %s after stall-cancel on %s (subprocess likely in uninterruptible sleep on a hung mount); leaking the stuck goroutine and returning stall error", backupStallLeakGrace, server.URL)
		return fmt.Errorf("backup aborted: no output written for %s (dead/hung backup volume; subprocess stuck and unkillable)", stallTimeout)
	}

	// Collect all errors
	var splitDumpErr, readErr error
	if splitDumpPipeline != nil {
		if boundedWait(splitDumpPipeline.wg, stallFired, backupStallLeakGrace) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"Splitdump pipeline did not unwind %s after stall-cancel on %s (hung mount?); leaking and returning stall error", backupStallLeakGrace, server.URL)
			return fmt.Errorf("backup aborted: no output written for %s (dead/hung backup volume; splitdump stuck and unkillable)", stallTimeout)
		}
		splitDumpErr = drainErrorChannel(splitDumpPipeline.errCh)
		if splitDumpErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Splitdump error: %s", splitDumpErr)
		}
	}

	readErr = drainErrorChannel(errCh)
	combinedErr := errors.Join(splitDumpErr, readErr)

	// A watchdog-triggered stall cancels the same context as a user cancel, so
	// report it distinctly (and never as a user cancellation): surface a clear
	// stall error rather than the underlying "context canceled".
	if stalled.Load() {
		stallErr := fmt.Errorf("backup aborted: no output written for %s (dead/hung backup volume)", stallTimeout)
		if combinedErr != nil {
			return errors.Join(stallErr, combinedErr)
		}
		return stallErr
	}

	if combinedErr != nil {
		if errors.Is(combinedErr, context.Canceled) && server.isJobCancelRequested(task) {
			if cluster.Conf.BackupKeepUntilValid {
				if _, statErr := os.Stat(filename); statErr == nil {
					cancelPath := filename + ".canceled"
					if _, err := os.Stat(cancelPath); err == nil {
						cancelPath = fmt.Sprintf("%s.%d", cancelPath, time.Now().Unix())
					}
					if err := os.Rename(filename, cancelPath); err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to preserve canceled backup %s: %s", filename, err)
					} else {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Preserved canceled backup at %s", cancelPath)
					}
				} else if !os.IsNotExist(statErr) {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to inspect canceled backup %s: %s", filename, statErr)
				}
			}
			return errors.Join(errJobCanceledByUser, combinedErr)
		}
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

func (server *ServerMonitor) JobBackupLogical(ctx context.Context) error {
	return server.JobBackupLogicalWithOptions(ctx, BackupRunOptions{})
}

func (server *ServerMonitor) JobBackupLogicalWithOptions(ctx context.Context, opts BackupRunOptions) error {
	var err error
	if server == nil {
		return errors.New("No server defined")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	cluster := server.ClusterGroup

	backupLine := server.resolveBackupLine(opts)
	isAdhoc := backupLine == backupmgr.BackupLineAdhoc
	resticEnabled := server.shouldRunRestic(opts)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Request logical backup %s for: %s", cluster.Conf.BackupLogicalType, server.URL)
	if server.IsDown() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical backup aborted: server %s is down (state=%s)", server.URL, server.State)
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

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical backup acquiring slot for: %s (semaphore=%v)", server.URL, cluster.ServerGlobals != nil && cluster.ServerGlobals.BackupSemaphore != nil)
	if !cluster.waitForBackupSlot() {
		return errors.New("backup canceled: cluster shutting down")
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical backup slot acquired for: %s", server.URL)

	var waited bool
	for cluster.IsInBackup() {
		waited = true
		cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Logical", cluster.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		time.Sleep(1 * time.Second)
	}

	cluster.SetInLogicalBackupState(true)
	defer cluster.SetInLogicalBackupState(false)
	cluster.SetState("WARN0175", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0175"], cluster.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})

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

		// Record task for metadata check. No JobInsertTask call for this task,
		// so there is no DB row regardless of scheduler state.
		server.JobsUpdateStateRuntimeOnly(task, "", 1, 0)

		err = server.JobBackupScript(filename)
		if err == nil {
			server.JobsUpdateStateRuntimeOnly(task, "Backup completed", 3, 1)
			server.LastBackupMeta.Logical.Completed = true
			if !isAdhoc {
				server.SetBackupLogicalCookie(task)
			}
		} else {
			server.JobsUpdateStateRuntimeOnly(task, err.Error(), 5, 1)
		}
	} else {
		task := cluster.Conf.BackupLogicalType

		// JobInsertTask creates a DB row only when the scheduler is active; when
		// it's off every JobsUpdateState call below for this task run must go
		// through JobsUpdateStateRuntimeOnly instead, or Start/End are never
		// stamped anywhere (there is no DB row and no SQL UPDATE will run).
		// JobsUpdateStateRuntimeOnly never returns an error (it only touches
		// the in-memory cache), so it's wrapped here to match JobsUpdateState's
		// signature and let every call site below share one variable.
		updateJobState := server.JobsUpdateState
		if cluster.Conf.MonitorScheduler {
			server.JobInsertTask(task, "0", cluster.Conf.MonitorAddress)
		} else {
			updateJobState = func(task, result string, state, done int) error {
				server.JobsUpdateStateRuntimeOnly(task, result, state, done)
				return nil
			}
			updateJobState(task, "", 0, 0)
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
				err = server.JobBackupMysqldump(ctx, task, outputdir, allowRotate)
			} else {
				err = server.JobBackupMysqldump(ctx, task, filename, allowRotate)
			}
			if err != nil {
				result := err.Error()
				if errors.Is(err, errJobCanceledByUser) {
					result = "cancelled by user"
				}
				if e2 := updateJobState(task, result, 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := updateJobState(task, "Backup completed", 3, 1); e2 != nil {
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
				if e2 := updateJobState(task, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := updateJobState(task, "Backup completed", 3, 1); e2 != nil {
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
				if e2 := updateJobState(task, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := updateJobState(task, "Backup completed", 3, 1); e2 != nil {
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
				if e2 := updateJobState(task, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := updateJobState(task, "Backup completed", 3, 1); e2 != nil {
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
		resticScheduled = true
		server.BackupRestic(backupmgr.BackupMethodLogical, true, resticPath, server.BuildResticTags(backtype, cluster.Conf.BackupLogicalType, backupLine, server.LastBackupMeta.Logical)...)
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

// copyLogsTail behaves like copyLogs (streams every non-empty line to the
// module log) and additionally keeps a bounded tail of at most maxLines of the
// most recent output, so a caller can fold a short excerpt into a returned
// error without an unbounded buffer (T18). Oldest lines are dropped by
// reslicing into a fresh backing array so they don't keep old strings alive.
func (server *ServerMonitor) copyLogsTail(r io.Reader, module int, level string, maxLines int) []string {
	cluster := server.ClusterGroup
	tail := make([]string, 0, maxLines)
	appendTail := func(line string) {
		if maxLines <= 0 {
			return
		}
		if len(tail) == maxLines {
			fresh := make([]string, maxLines-1, maxLines)
			copy(fresh, tail[1:])
			tail = fresh
		}
		tail = append(tail, line)
	}
	s := bufio.NewScanner(r)
	// Bound the per-line buffer at 4MiB -- well above the default 64KiB
	// (T18: bounded, not unbounded, but large enough that a long real stderr
	// line, e.g. one echoing back a big INSERT, doesn't trip ErrTooLong).
	const maxLineSize = 4 << 20
	s.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	for s.Scan() {
		line := s.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, module, level, "[%s] %s", server.Name, line)
		appendTail(line)
	}
	// Scanner.Err() is nil on a clean EOF (the normal case: the pipe closes
	// when the process exits) and non-nil on a real read failure (e.g. a
	// line past maxLineSize). Since this tail feeds directly into the error
	// JobRejoinMysqldumpFromSource returns, a silently truncated read would
	// hide the very diagnostic this helper exists to capture.
	if err := s.Err(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, module, config.LvlWarn, "[%s] stderr read stopped early: %s", server.Name, err)
		appendTail(fmt.Sprintf("[stderr read stopped early: %s]", err))
		// bufio.Scanner abandons the underlying reader on error -- it does NOT
		// drain what's left. If we stopped reading here too, nobody would
		// read the rest of this pipe, and the child could block on its next
		// stderr write, reintroducing the exact hang this function exists to
		// prevent. Keep discarding bytes until the pipe actually closes (the
		// child exits) so Wait() can always return.
		io.Copy(io.Discard, r)
	}
	return tail
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

	resticLocalDir := cluster.GetResticEffectiveLocalRepoPath()
	defer cluster.UpdateDiskStat(resticLocalDir)

	if cluster.Conf.BackupCheckFreeSpace {
		diskstat, err := cluster.GetDiskStat(resticLocalDir)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error getting disk stat for restic backup dir: %s", resticLocalDir)
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

// wasCollateralKill reports whether err represents a process terminated by
// SIGKILL specifically -- the signal exec.CommandContext's default Cancel
// sends when its context is cancelled. JobRejoinMysqldumpFromSource ties
// mysqldump and the mysql client to one shared context and cancels it the
// moment either side fails, so the side that failed on its own always exits
// with a normal nonzero status while the side killed as a result exits via
// SIGKILL specifically. That's a property of the exit status itself, not of
// which goroutine happens to observe it first, so callers can use it to tell
// a genuine failure apart from a collateral kill without racing on timing.
func wasCollateralKill(err error) bool {
	return exitSignal(err) == syscall.SIGKILL
}

// wasCollateralPipeClose reports whether err represents mysqldump dying from
// SIGPIPE as a side effect of the pump's own unwind, rather than a genuine
// failure of its own. The pump (see JobRejoinMysqldumpFromSource) defers
// dumpStdoutR.Close() on every one of its own error returns, including
// preamble-write and mid-copy failures that have nothing to do with
// mysqldump itself; if mysqldump is still writing to the paired dumpStdoutW
// when that happens, its next write dies with SIGPIPE, before -- or
// independent of -- the shared context's SIGKILL ever lands.
//
// Unlike SIGKILL, SIGPIPE is not unambiguously ours: mysqldump could in
// principle die from a SIGPIPE against its own source-DB connection with no
// pump involvement at all. The pumpErr != nil guard is what makes this safe
// -- that pipe-closing unwind path only runs when the pump itself hit an
// error. If the pump never errored (pumpErr == nil), it never closed
// dumpStdoutR early, so a dump-side SIGPIPE in that case cannot be ours and
// must be reported as genuine.
func wasCollateralPipeClose(err, pumpErr error) bool {
	return pumpErr != nil && exitSignal(err) == syscall.SIGPIPE
}

// exitSignal returns the signal that terminated err's process, or 0 if err
// is not a signal-terminated *exec.ExitError (e.g. a normal nonzero exit, or
// a platform where ExitError.Sys() isn't a syscall.WaitStatus).
func exitSignal(err error) syscall.Signal {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 0
	}
	ws, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return 0
	}
	return ws.Signal()
}

// reseedStage names the failing stage of a direct reseed for job state/log
// messages, per SYSTEM_ALL_RESEED_FIX_PLAN.md's Cancellation and Failure
// Semantics table.
type reseedStage string

const (
	reseedStageApplicationRestore  reseedStage = "application restore"
	reseedStageSystemExtraction    reseedStage = "system extraction"
	reseedStageSystemCatalogReplay reseedStage = "system catalogue replay"
	reseedStageReplicationRestart  reseedStage = "replication restart"
)

// reseedFailureMessage builds the "Reseed failed: ..." message for
// JobRejoinMysqldumpFromSource from the three goroutines' results. It's a
// pure function of its arguments -- no subprocess or channel involved -- so
// the attribution logic (which of dumpErr/clientErr is a genuine failure
// versus a collateral kill caused by the other) can be table-tested directly
// against synthetic errors instead of racing real subprocesses.
func reseedFailureMessage(sourceURL, destURL string, dumpErr, clientErr, pumpErr error, dumpTail, clientTail []string) string {
	// Killing one side to unstick the other means a single real failure
	// commonly shows up as errors on BOTH Waits: the genuine one, plus a
	// collateral kill on the side we cancelled (or, for mysqldump
	// specifically, a SIGPIPE from the pump closing its stdout pipe early --
	// see wasCollateralPipeClose). Deciding which is "the" cause by which
	// goroutine happened to run first is a timing race; wasCollateralKill /
	// wasCollateralPipeClose instead inspect the exit status itself, which is
	// deterministic and independent of scheduling. That lets both
	// genuinely-independent failures be reported together, and a collateral
	// kill be excluded, with no dependence on timing.
	//
	// But a signal match alone isn't sufficient: SIGKILL/SIGPIPE only means
	// "collateral" if something in THIS function actually had a reason to
	// cancel() -- cancel() only ever fires from dump's own failure, client's
	// own failure, or the pump's own failure (see the goroutines above). If
	// none of those three happened, nothing here could have triggered a
	// cancel(), so a SIGKILL observed anyway (an external `kill -9`, the OOM
	// killer, a stray admin action, ...) cannot be blamed on us and must be
	// reported as genuine. Skipping this check would let a lone externally
	// killed side (dumpErr == nil, pumpErr == nil, clientErr == SIGKILL) be
	// scored not-genuine on signal alone, with nothing else to report --
	// producing an empty "Reseed failed: " with no cause listed at all.
	dumpOwnFailure := dumpErr != nil && !wasCollateralKill(dumpErr) && !wasCollateralPipeClose(dumpErr, pumpErr)
	clientOwnFailure := clientErr != nil && !wasCollateralKill(clientErr)
	triggerExists := pumpErr != nil || dumpOwnFailure || clientOwnFailure
	dumpGenuine := dumpErr != nil && (dumpOwnFailure || !triggerExists)
	clientGenuine := clientErr != nil && (clientOwnFailure || !triggerExists)

	var parts []string
	addDump := func() {
		p := fmt.Sprintf("mysqldump on %s: %s", sourceURL, dumpErr.Error())
		if len(dumpTail) > 0 {
			p += " | stderr: " + strings.Join(dumpTail, " / ")
		}
		parts = append(parts, p)
	}
	addClient := func() {
		p := fmt.Sprintf("mysql client on %s: %s", destURL, clientErr.Error())
		if len(clientTail) > 0 {
			p += " | stderr: " + strings.Join(clientTail, " / ")
		}
		parts = append(parts, p)
	}
	switch {
	case dumpErr == nil:
		// Covers dumpErr == nil && clientErr == nil too (e.g. a pump-only
		// failure with both processes exiting cleanly) -- addClient must
		// stay guarded on clientGenuine, never called unconditionally, or a
		// nil clientErr here would panic on err.Error(). Guarding on
		// clientGenuine rather than clientErr != nil also covers the case
		// where dump exits 0 on its own but the pump fails for an unrelated
		// reason (e.g. a read error on its own side) and its cancel()
		// collaterally SIGKILLs the still-running client: that exit must not
		// be reported as a genuine client failure.
		if clientGenuine {
			addClient()
		}
	case clientErr == nil:
		// Symmetric with the dumpErr == nil case above.
		if dumpGenuine {
			addDump()
		}
	case dumpGenuine && clientGenuine:
		// Both failed on their own -- a coincidental double fault, not one
		// side collaterally killing the other. Neither caused the other, so
		// report both instead of guessing.
		addDump()
		addClient()
	case clientGenuine:
		addClient()
	case dumpGenuine:
		addDump()
	default:
		// Both sides have errors but neither looks like a genuine own
		// failure by the signal heuristic (e.g. ExitError.Sys() isn't a
		// syscall.WaitStatus on this platform). Surface both rather than
		// silently pick one.
		addDump()
		addClient()
	}
	// The pump (dump stdout -> client stdin) is the glue between the two
	// processes, not a third competitor for "root cause" -- surface it
	// whenever it saw something, in addition to whatever dump/client
	// reported. It's often the earliest and clearest signal (e.g. an
	// immediate EPIPE the moment the client dies), and it's the only signal
	// at all in the rare case neither process's own exit status reflected
	// the failure.
	if pumpErr != nil {
		parts = append(parts, fmt.Sprintf("stdin pump (mysqldump to mysql client) on %s: %s", destURL, pumpErr.Error()))
	}
	return "Reseed failed: " + strings.Join(parts, "; ")
}

// progressCountingWriter wraps an io.Writer and adds each successful Write's
// byte count to progress, so a stall watchdog (backupStallWatchdog) can
// observe real forward progress through a pipe rather than just reads off
// the source. Wrapping the writer -- rather than counting bytes off the
// reader -- matters: a writer that stops draining (e.g. a wedged mysql
// client) must freeze the counter too, or the watchdog would keep seeing
// "progress" from reads alone while the pipe backs up.
type progressCountingWriter struct {
	w        io.Writer
	progress *atomic.Int64
}

func (c *progressCountingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.progress.Add(int64(n))
	return n, err
}

func (cluster *Cluster) JobRejoinMysqldumpFromSource(source *ServerMonitor, dest *ServerMonitor) error {
	task := "direct"

	// See CheckDirectReseedSourceDestVersion's doc comment for why this is
	// opt-in. Checked before any of this function's OWN state-changing side
	// effects (JobsUpdateStateRuntimeOnly, StopAllSlaves) -- but the caller
	// (e.g. RejoinDirectDump) sets dest.IsReseeding before arming this as a
	// goroutine, so a strict-mode block must clear that flag itself here or
	// dest is left permanently stuck reseeding.
	if cluster.Conf.BackupRestoreVersionStrict {
		if err := cluster.CheckDirectReseedSourceDestVersion(source, dest); err != nil {
			if dest.HasReseedingState(task) {
				dest.SetInReseedBackup("")
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"Direct reseed source/destination family/version mismatch for %s. Cancelling reseed for data safety.", dest.URL)
			return fmt.Errorf("%w -- disable --backup-restore-version-strict to allow reseed across a source/destination family/version difference", err)
		}
	}

	defer dest.SetInReseedBackup("")
	dest.JobsUpdateStateRuntimeOnly(task, "processing", 1, 0)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rejoining from direct mysqldump from %s", source.URL)

	// Reseed progress (WARN0189): stamp the in-flight restore so the per-tick
	// state and the dashboard reseed modal show streamed bytes/avg speed
	// instead of a generic "in progress" timer, and so the backup/tool text
	// distinguishes this direct stream from a file-based mysqldump restore.
	// Total is unknown for a live stream (0 -- GetReseedProgress renders
	// Percent=-1 for that case, same as other unknown-total restores).
	dest.beginReseedProgress(&ReseedProgress{
		Backup: "direct stream from " + source.URL,
		Source: source.URL,
		Tool:   "mysqldump",
	}, 0)
	defer dest.stopReseedProgress()

	// Stop ALL replication connections before the RESET MASTER below. StopSlave()
	// only stops cluster.Conf.MasterConn (empty by default → the unnamed default
	// connection), so a dest replicating on a NAMED connection (e.g. 'curepipe')
	// keeps running and RESET MASTER fails with ERROR 1198 "run STOP SLAVE
	// '<name>' first". StopAllSlaves iterates dest.Replications and stops each by
	// its real ConnectionName.
	if logs, err := dest.StopAllSlaves(); err != nil {
		cluster.LogSQL(logs, err, dest.URL, "Rejoin", config.LvlErr, "Failed stop all slaves before direct dump reseed on %s: %s", dest.URL, err)
	}

	// Shared cancellable context across both subprocesses. mysqldump and the mysql
	// client run concurrently, wired together by an OS pipe with no unbounded
	// buffer: if the client dies first (bad SQL, lost connection...) nobody drains
	// mysqldump's stdout anymore and it blocks on the next write(); waiting on the
	// two commands sequentially (as this used to) then never returns, so the
	// deferred SetInReseedBackup("") above never runs and IsReseeding="direct"
	// stays stuck forever. Tying both commands to one context lets either side's
	// exit cancel and kill the other instead of hanging. Same failure shape as
	// doc/implementation/cluster/BACKUP_DEAD_VOLUME_STALL.md, one pipe hop earlier.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dumpCmd := exec.CommandContext(ctx, cluster.GetMysqlDumpPath(), cluster.GetMysqlDumpOptions(source, dest.JobGetDumpGtidParameter())...)

	cliParams := append(cluster.GetDumpCredentials(dest), dest.GetSSLClientParam("client")...)
	cliParams = append(cliParams, strings.Split(cluster.Conf.BackupMysqlclientOptions, " ")...)

	clientCmd := exec.CommandContext(ctx, cluster.GetMysqlclientPath(), misc.RemoveEmptyString(cliParams)...)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command: %s ", strings.Replace(dumpCmd.String(), "="+cluster.GetDbPass(), "=XXXX", -1))

	failPipeSetup := func(what string, err error) error {
		msg := fmt.Sprintf("Failed to create %s: %s", what, err)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", msg)
		dest.JobsUpdateStateRuntimeOnly(task, msg, 5, 1)
		return err
	}

	// Own mysqldump's stdout/stderr and the client's stderr with plain
	// os.Pipe() pairs assigned directly to Cmd.Stdout/Cmd.Stderr, rather than
	// Cmd.StdoutPipe()/Cmd.StderrPipe(). Those helpers register their pipes in
	// Cmd's internal parentIOPipes list, and Cmd.Wait() unconditionally
	// force-closes every pipe on that list the instant the process exits --
	// regardless of whether our own reader goroutines below (the stderr tail
	// readers, the pump reading mysqldump's stdout) have finished draining
	// them. That race doesn't hang; it silently loses whatever was still
	// unread. For the stderr tails that's a truncated diagnostic; for the
	// pump reading dumpStdoutR, it's potentially truncated RESTORE DATA on an
	// otherwise-successful reseed. Plain *os.File pipes we create and own
	// ourselves are invisible to Cmd -- Wait() never touches them.
	// (Cmd.StdinPipe() doesn't have this problem: the same race there closes
	// clientStdin out from under the pump's blocked Write(), which is exactly
	// how we want a dead client to unstick a stuck pump -- so it stays.)
	var ownedPipes []*os.File
	newOwnedPipe := func() (*os.File, *os.File, error) {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, nil, err
		}
		ownedPipes = append(ownedPipes, r, w)
		return r, w, nil
	}
	closeOwnedPipes := func() {
		for _, f := range ownedPipes {
			f.Close()
		}
	}

	dumpStdoutR, dumpStdoutW, err := newOwnedPipe()
	if err != nil {
		return failPipeSetup(fmt.Sprintf("mysqldump stdout pipe on %s", source.URL), err)
	}
	dumpStderrR, dumpStderrW, err := newOwnedPipe()
	if err != nil {
		closeOwnedPipes()
		return failPipeSetup(fmt.Sprintf("mysqldump stderr pipe on %s", source.URL), err)
	}
	clientStderrR, clientStderrW, err := newOwnedPipe()
	if err != nil {
		closeOwnedPipes()
		return failPipeSetup(fmt.Sprintf("mysql client stderr pipe on %s", dest.URL), err)
	}
	dumpCmd.Stdout = dumpStdoutW
	dumpCmd.Stderr = dumpStderrW
	clientCmd.Stderr = clientStderrW

	// Deliberately NOT clientCmd.Stdin = io.MultiReader(...). When Cmd.Stdin is
	// an io.Reader rather than an *os.File, exec spawns its OWN goroutine that
	// copies that reader into the child's stdin pipe, and Cmd.Wait() blocks
	// until BOTH the process has exited AND that hidden copy goroutine has
	// finished. That copy goroutine spends most of its time blocked in Read()
	// on dumpStdoutR -- so if the mysql client exits (or is killed) while
	// mysqldump is independently stalled (a source-side lock wait, a dead
	// network to the source, anything unrelated to us not draining its
	// output), the hidden copy goroutine never gets EOF, never notices the
	// client is gone, and clientCmd.Wait() never returns -- cancel() never
	// fires, dumpCmd is never killed, and the reseed hangs again despite every
	// fix above. Using an explicit StdinPipe() instead means Wait() reflects
	// ONLY process exit; we own the copy loop below (the "pump") as a
	// goroutine independent of Wait(), so a stuck pump cannot block
	// cancellation from firing.
	clientStdin, err := clientCmd.StdinPipe()
	if err != nil {
		closeOwnedPipes()
		return failPipeSetup(fmt.Sprintf("mysql client stdin pipe on %s", dest.URL), err)
	}

	// RESET MASTER (RESET BINARY LOGS AND GTIDS on MySQL/Percona 8.4+) wipes the
	// dest's binary logs and GTID state before the restore. This is required
	// because a "fresh" instance is not actually empty: on a Debian MariaDB
	// bootstrap the packaging writes to the binlog (creating debian-sys-maint,
	// its grants, and the debian-start upgrade/check work), which advances
	// gtid_binlog_pos. Left in place, that pre-existing GTID state conflicts with
	// the source position the dump carries (--gtid → SET GLOBAL gtid_slave_pos),
	// so the reseeded slave will not line up. RESET MASTER clears that residue so
	// the source's position applies onto a genuinely clean slate.
	// SET sql_log_bin=0 keeps the restore stream itself out of the binlog.
	// NOTE: RESET MASTER requires all slaves stopped — stop every (incl. named)
	// replication connection before reaching here, or it fails with ERROR 1198.
	cmdstring := "RESET MASTER;SET sql_log_bin=0;SET long_query_time=10;"
	if dest.DBVersion.IsMySQLOrPerconaGreater84() {
		cmdstring = "RESET BINARY LOGS AND GTIDS;SET sql_log_bin=0;SET long_query_time=10;"
	}

	// The pump below routes mysql.system-all content into this artifact
	// instead of the mysql client, so a pre-existing plugin/user row on dest
	// can no longer abort the whole reseed. Created before either subprocess
	// starts so a setup failure here can use the same early-return cleanup as
	// dumpCmd.Start() just below.
	reseedStart := time.Now()
	jobIDSuffix, err := randomHexSuffix(6)
	if err != nil {
		closeOwnedPipes()
		clientStdin.Close()
		return failPipeSetup("direct-reseed job id", err)
	}
	jobID := task + "-" + jobIDSuffix
	artifactWriter, err := dest.newDirectReseedSystemArtifactWriter(jobID, reseedStart)
	if err != nil {
		closeOwnedPipes()
		clientStdin.Close()
		return failPipeSetup(fmt.Sprintf("direct-reseed system artifact for %s", dest.URL), err)
	}

	if err := dumpCmd.Start(); err != nil {
		artifactWriter.discard()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Failed mysqldump command: %s at %s", err, strings.Replace(dumpCmd.String(), "="+cluster.GetDbPass(), "=XXXX", -1))
		closeOwnedPipes()
		clientStdin.Close()
		dest.JobsUpdateStateRuntimeOnly(task, err.Error(), 5, 1)
		return err
	}
	// dumpCmd's child now holds its own inherited copies of dumpStdoutW and
	// dumpStderrW -- close ours so the read ends (still held below by the
	// pump and the stderr tail reader) can ever see EOF. Forgetting this is
	// the classic mirror-image bug to the premature-close race above: instead
	// of losing data to an early close, an fd we forgot to close keeps the
	// pipe "held open" forever and the reader blocks past the point the child
	// has actually exited.
	dumpStdoutW.Close()
	dumpStderrW.Close()

	if err := clientCmd.Start(); err != nil {
		artifactWriter.discard()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Can't start mysql client:%s at %s", err, strings.Replace(clientCmd.String(), "="+cluster.GetDbPass(), "=XXXX", -1))
		clientStderrW.Close()
		clientStderrR.Close()
		clientStdin.Close()
		cancel() // dumpCmd already started but the client never will -- kill it now instead of letting it dump into a pipe nobody reads
		// Reap it: cancel() only signals the kill, it doesn't wait for the
		// process to actually exit. Returning without Wait() here would leave
		// an already-started mysqldump as an unreaped zombie.
		if werr := dumpCmd.Wait(); werr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "mysqldump on %s exited after client start failure: %s", source.URL, werr)
		}
		dumpStdoutR.Close()
		dumpStderrR.Close()
		dest.JobsUpdateStateRuntimeOnly(task, err.Error(), 5, 1)
		return err
	}
	// Symmetric with dumpStdoutW/dumpStderrW above.
	clientStderrW.Close()

	const stderrTailLines = 20
	var wg sync.WaitGroup
	var dumpTail, clientTail []string
	wg.Add(2)

	go func() {
		defer wg.Done()
		defer dumpStderrR.Close()
		dumpTail = source.copyLogsTail(dumpStderrR, config.ConstLogModBackupStream, config.LvlDbg, stderrTailLines)
	}()
	go func() {
		defer wg.Done()
		defer clientStderrR.Close()
		clientTail = dest.copyLogsTail(clientStderrR, config.ConstLogModBackupStream, config.LvlDbg, stderrTailLines)
	}()

	// Write/read-stall watchdog: every fix above reacts to a subprocess
	// EXITING, with or without error. None of them help if nothing exits at
	// all -- mysqldump can wedge on a source-side lock wait, or the mysql
	// client can wedge mid-statement on the destination, with neither process
	// ever erroring or returning. That leaves the reseed exactly as stuck as
	// the bug this whole fix chain exists to close. This mirrors
	// backupStallWatchdog's use in JobBackupMysqldump for the identical shape
	// of incident one pipe hop over (see BACKUP_DEAD_VOLUME_STALL.md): track
	// bytes actually forwarded through the pump, and if that stops advancing
	// for backup-write-stall-timeout, treat it as stuck and cancel() the same
	// way an explicit failure would. Reuses the existing backup-write-stall-
	// timeout config rather than adding a second stall knob -- same signal
	// ("is data still flowing"), same semantics (0 disables, <0 warns+disables).
	// Uses dest.reseedBytes (the same counter beginReseedProgress above stamped
	// for the dashboard/WARN0189) rather than a local counter, so the stall
	// watchdog and the displayed progress can never diverge.
	var stalled atomic.Bool
	stallDone := make(chan struct{})
	stallFired := make(chan struct{})
	if cluster.Conf.BackupWriteStallTimeout < 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
			"backup-write-stall-timeout is negative (%d) — the direct reseed stall watchdog is DISABLED; use 0 to disable intentionally, or a positive number of seconds to enable", cluster.Conf.BackupWriteStallTimeout)
	}
	stallTimeout := time.Duration(cluster.Conf.BackupWriteStallTimeout) * time.Second
	checkInterval := stallTimeout / 4
	if checkInterval < time.Second {
		checkInterval = time.Second
	}
	go backupStallWatchdog(stallDone, cancel, &dest.reseedBytes, stallTimeout, checkInterval, func() {
		stalled.Store(true)
		close(stallFired)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
			"Direct reseed stalled for %s with no bytes streamed from %s to %s; aborting", stallTimeout, source.URL, dest.URL)
	})

	// The pump: writes the SQL preamble then streams mysqldump's stdout into
	// the mysql client's stdin, replacing what Cmd.Stdin=io.MultiReader(...)
	// used to do implicitly (see the comments above clientCmd.StdinPipe() and
	// above the owned os.Pipe() setup for why neither Cmd helper is used
	// here). Owning this loop explicitly means a write/read failure here is
	// detected and can cancel() directly, instead of being invisible to
	// clientCmd.Wait().
	// classifyResult and classifyFailed are written only inside the pump
	// goroutine below and read only after pumpErrCh has been drained (via
	// boundedWait/<-pumpErrCh), so the channel send/receive establishes the
	// happens-before needed to read them race-free -- same pattern already
	// used for dumpTail/clientTail above.
	var classifyResult splitdump.ClassifyResult
	var classifyFailed bool
	pumpErrCh := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer clientStdin.Close()
		defer dumpStdoutR.Close()
		if _, err := io.WriteString(clientStdin, cmdstring); err != nil {
			cancel()
			pumpErrCh <- fmt.Errorf("writing restore preamble to mysql client stdin: %w", err)
			return
		}
		// Both destinations are wrapped in progressCountingWriter over the
		// SAME dest.reseedBytes counter -- the stall watchdog below tracks
		// that counter as "is data still flowing at all", not specifically
		// "is data still reaching the client". Wiring only the client side
		// would let a long --system=all tail (written only to the artifact)
		// freeze the counter while genuine forward progress is still
		// happening, causing a false-positive stall abort.
		counted := &progressCountingWriter{w: clientStdin, progress: &dest.reseedBytes}
		countedArtifact := &progressCountingWriter{w: artifactWriter, progress: &dest.reseedBytes}
		result, err := splitdump.ClassifyStream(dumpStdoutR, splitdump.ClassifyOptions{
			ApplicationWriter: counted,
			SystemWriter:      countedArtifact,
		})
		classifyResult = result
		if err != nil {
			cancel()
			classifyFailed = true
			pumpErrCh <- fmt.Errorf("classifying mysqldump output into application/system SQL: %w", err)
			return
		}
		pumpErrCh <- nil
	}()

	// Wait for both subprocesses concurrently -- NOT dumpCmd.Wait() then
	// clientCmd.Wait() in sequence. If the mysql client dies first, nobody
	// drains mysqldump's stdout pipe anymore and it blocks on the next write();
	// waiting on it first would never return. Whichever side fails cancels ctx
	// so the other is killed instead of left hanging.
	dumpErrCh := make(chan error, 1)
	clientErrCh := make(chan error, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		err := dumpCmd.Wait()
		if err != nil {
			cancel()
		}
		dumpErrCh <- err
	}()
	go func() {
		defer wg.Done()
		err := clientCmd.Wait()
		if err != nil {
			cancel()
		}
		clientErrCh <- err
	}()

	// Bounded wait: normally block until every goroutine above (both stderr
	// tail readers, the pump, both Wait()s) has unwound. But if the watchdog
	// fired and a subprocess is stuck in uninterruptible sleep (D-state --
	// e.g. an NFS-style hard-hung source or destination mount), SIGKILL
	// cannot free it and this would never return. After backupStallLeakGrace,
	// give up, leak the stuck goroutine(s), and return the stall error
	// instead of hanging the reseed forever. Best-effort mitigation for the
	// hard-hung-mount case, not a hard guarantee -- see
	// BACKUP_DEAD_VOLUME_STALL.md.
	leaked := boundedWait(&wg, stallFired, backupStallLeakGrace)
	close(stallDone) // stop the watchdog
	if leaked {
		artifactWriter.discard() // never publish a partial artifact (Cancellation and Failure Semantics: cancel during phase 1)
		msg := fmt.Sprintf("Direct reseed from %s to %s did not unwind %s after stall-cancel (subprocess likely stuck in uninterruptible sleep); giving up",
			source.URL, dest.URL, backupStallLeakGrace)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "%s", msg)
		dest.JobsUpdateStateRuntimeOnly(task, msg, 5, 1)
		return errors.New(msg)
	}

	dumpErr := <-dumpErrCh
	clientErr := <-clientErrCh
	pumpErr := <-pumpErrCh

	// A watchdog-triggered stall cancels the same context as any other
	// failure, so dumpErr/clientErr/pumpErr above would just read back
	// "context canceled" / "signal: killed" -- report the stall distinctly
	// instead so operators see a diagnosis, not a generic cancellation.
	if stalled.Load() {
		artifactWriter.discard() // never publish a partial artifact
		msg := fmt.Sprintf("Direct reseed aborted: no bytes streamed from %s to %s for %s (stuck mysqldump/mysql client)", source.URL, dest.URL, stallTimeout)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", msg)
		dest.JobsUpdateStateRuntimeOnly(task, msg, 5, 1)
		return errors.New(msg)
	}

	if dumpErr != nil || clientErr != nil || pumpErr != nil {
		artifactWriter.discard() // never publish a partial artifact
		// classifyFailed's cancel() routinely SIGKILLs both still-running
		// subprocesses as a side effect; that collateral kill must not mask
		// the real root cause, so a subprocess error only overrides the
		// classify-side attribution when it's not explainable as collateral
		// (same wasCollateralKill/wasCollateralPipeClose reasoning
		// reseedFailureMessage uses internally).
		dumpOwnFailure := dumpErr != nil && !wasCollateralKill(dumpErr) && !wasCollateralPipeClose(dumpErr, pumpErr)
		clientOwnFailure := clientErr != nil && !wasCollateralKill(clientErr)
		stage := reseedStageApplicationRestore
		if classifyFailed && !dumpOwnFailure && !clientOwnFailure {
			stage = reseedStageSystemExtraction
		}
		msg := fmt.Sprintf("%s: %s", stage, reseedFailureMessage(source.URL, dest.URL, dumpErr, clientErr, pumpErr, dumpTail, clientTail))
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", msg)
		dest.JobsUpdateStateRuntimeOnly(task, msg, 5, 1)
		return errors.New(msg)
	}

	// Phase two: replay the extracted system-catalogue artifact, if any, through
	// the narrow SQLx helper before touching replication. No system content is a
	// successful no-op (nothing to publish or replay); when present, publication
	// is atomic and the artifact is preserved on any phase-two failure so it can
	// be diagnosed or retried without repeating the (potentially long)
	// application-data restore above.
	if classifyResult.HasSystemContent {
		finalDir, publishErr := artifactWriter.publish(classifyResult.Metadata, directReseedArtifactExtra{
			SourceServer:          source.URL,
			DestinationServer:     dest.URL,
			SourceServerVersion:   source.DBVersion.ToString(),
			DestinationFamily:     dest.DBVersion.Flavor,
			DestinationMajorMinor: directReseedServerMajorMinor(dest.DBVersion),
			BoundaryFormat:        "v1-eof-bounded",
			ArtifactState:         directReseedArtifactStatePublished,
		})
		if publishErr != nil {
			msg := fmt.Sprintf("%s: publish artifact for %s: %s", reseedStageSystemExtraction, dest.URL, publishErr)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", msg)
			dest.JobsUpdateStateRuntimeOnly(task, msg, 5, 1)
			return errors.New(msg)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Direct reseed: replaying system catalogue on %s", dest.URL)
		// Mark in-progress before executing any SQL: if we can't durably record
		// that replay is starting, we must not proceed to run statements whose
		// completion state we then couldn't reliably track either -- abort here
		// rather than replay blind.
		if err := setDirectReseedArtifactState(finalDir, directReseedArtifactStateReplayInProgress); err != nil {
			msg := fmt.Sprintf("%s: record replay-in-progress state for artifact %s: %s", reseedStageSystemCatalogReplay, finalDir, err)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", msg)
			dest.JobsUpdateStateRuntimeOnly(task, msg, 5, 1)
			return errors.New(msg)
		}

		progressed, replayErr := func() (bool, error) {
			dbh, connErr := dest.GetNewDBConn()
			if connErr != nil {
				return false, connErr
			}
			defer dbh.Close()
			// GetConnNoBinlog, matching this function's unconditional RESET
			// MASTER/SET sql_log_bin=0 preamble above -- JobRejoinMysqldumpFromSource
			// always targets a slave reseed, so the catalogue replay connection
			// stays out of the binlog like the rest of this restore.
			conn, connErr := dest.GetConnNoBinlog(dbh)
			if connErr != nil {
				return false, connErr
			}
			defer conn.Close()
			return dest.restoreSystemCatalog(ctx, conn, filepath.Join(finalDir, directReseedSystemArtifactName))
		}()

		if replayErr != nil {
			// A failure before any statement committed (connection/setup failure,
			// or the very first statement erroring) is safe to retry from the
			// beginning; a failure after at least one commit is not, since most
			// --system=all statement classes besides INSTALL PLUGIN are not proven
			// replay-idempotent (see RetryDirectReseedSystemCatalog).
			failState := directReseedArtifactStateReplayFailed
			if !progressed {
				failState = directReseedArtifactStateReplayFailedSafe
			}
			if stateErr := setDirectReseedArtifactState(finalDir, failState); stateErr != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
					"Failed to record artifact state %s for %s after replay failure: %s", failState, finalDir, stateErr)
			}
			msg := fmt.Sprintf("%s: %s: %s", reseedStageSystemCatalogReplay, dest.URL, replayErr)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", msg)
			dest.JobsUpdateStateRuntimeOnly(task, msg, 5, 1)
			return errors.New(msg)
		}
		if err := setDirectReseedArtifactState(finalDir, directReseedArtifactStateReplaySucceeded); err != nil {
			// The DB replay itself succeeded, but we can't durably prove it: an
			// artifact whose recorded state doesn't reflect reality is a
			// retry-safety hazard (a later retry decision would trust a stale
			// state), so this is surfaced as a job failure rather than silently
			// proceeding to restart replication.
			msg := fmt.Sprintf("%s: replay succeeded but failed to record terminal state for artifact %s: %s", reseedStageSystemCatalogReplay, finalDir, err)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", msg)
			dest.JobsUpdateStateRuntimeOnly(task, msg, 5, 1)
			return errors.New(msg)
		}
	} else {
		artifactWriter.discard()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Direct reseed: no system-catalogue content found for %s; system replay phase skipped", dest.URL)
	}

	// Symmetric with StopAllSlaves above: restart every replication connection by
	// its real ConnectionName. StartSlave() alone would restart only the default
	// MasterConn channel, leaving a multi-source dest's other source connections
	// stopped after they were stopped for the RESET MASTER. Only reached after
	// phase two (if any) has succeeded -- replication must never restart on top
	// of a failed or skipped catalogue replay.
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Start slave after dump on %s", dest.URL)
	var slaveStartErrs []string
	for _, rep := range dest.Replications {
		if logs, err := dest.StartSlaveChannel(rep.ConnectionName.String); err != nil {
			cluster.LogSQL(logs, err, dest.URL, "Rejoin", config.LvlErr, "Failed start slave channel '%s' after direct dump reseed on %s: %s", rep.ConnectionName.String, dest.URL, err)
			slaveStartErrs = append(slaveStartErrs, fmt.Sprintf("%s: %s", rep.ConnectionName.String, err.Error()))
		}
	}

	if len(slaveStartErrs) > 0 {
		// The dump/restore itself succeeded, but a node that didn't actually
		// rejoin replication is not a successful reseed -- report it as a
		// failure instead of "completed", or the dest is left both broken
		// and looking done.
		msg := fmt.Sprintf("%s: restore completed but failed to start replication channel(s) on %s: %s", reseedStageReplicationRestart, dest.URL, strings.Join(slaveStartErrs, "; "))
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", msg)
		dest.JobsUpdateStateRuntimeOnly(task, msg, 5, 1)
		return errors.New(msg)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Reseed slave from %s to %s finished", source.URL, dest.URL)
	dest.JobsUpdateStateRuntimeOnly(task, "Reseed completed", 3, 1)
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
					// done=0: JobsCheckErrors (srv_job.go) owns settling this row —
					// it finds done=0/state=5 rows, runs restic-cookie/mount cleanup
					// for reseed/flashback task names, then marks done=1 with End set.
					// Marking done=1 here would hide the row from that cleanup.
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

	// done=0: see JobsCheckErrors ownership note above.
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
					// done=0: see JobsCheckErrors ownership note in WaitAndSendSST.
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

func (server *ServerMonitor) ProcessReseedLogical(task string) error {
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

	backupType := cluster.Conf.BackupLogicalType
	payloadBackupPath := ""
	splitUser := false
	splitUserOverride := false
	skipMetadata := false
	isPITR := server.PointInTimeMeta.IsInPITR

	parseBool := func(value string) (bool, bool) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return false, false
		}
		parsed, err := strconv.ParseBool(trimmed)
		if err != nil {
			return false, false
		}
		return parsed, true
	}

	if taskInfo := server.JobResults.Get(task); taskInfo != nil && taskInfo.Payload != "" {
		payload := make(map[string]string)
		if err := json.Unmarshal([]byte(taskInfo.Payload), &payload); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Invalid payload for %s on %s: %s", task, server.URL, err)
		} else {
			if v := strings.TrimSpace(payload["backup_type"]); v != "" {
				backupType = v
			}
			if v := strings.TrimSpace(payload["backup_path"]); v != "" {
				payloadBackupPath = filepath.Clean(v)
			}
			if parsed, ok := parseBool(payload["split_user"]); ok {
				splitUser = parsed
			}
			if parsed, ok := parseBool(payload["split_user_override"]); ok {
				splitUserOverride = parsed
			}
			if parsed, ok := parseBool(payload["skip_metadata"]); ok {
				skipMetadata = parsed
			}
			if parsed, ok := parseBool(payload["is_pitr"]); ok {
				isPITR = parsed
			}
		}
	}

	defer func() {
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
	}()

	if cluster.Conf.BackupLoadScript != "" {
		// Stamp the real start time now, before the potentially slow work below
		// (StopSlave, pointSlaveToMaster, the restore itself). No JobInsertTask
		// call anywhere in this function, in any scheduler state, so there is
		// never a DB row for a logical reseed task run.
		server.JobsUpdateStateRuntimeOnly(task, "processing", 1, 0)

		if !isPITR {
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

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive reseed logical backup %s request for server: %s", backupType, server.URL)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Using script from backup-load-script on %s", server.URL)
		if err := server.JobReseedBackupScript(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error reseed %s on %s: %s", backupType, server.URL, err.Error())
			server.JobsUpdateStateRuntimeOnly(task, err.Error(), 5, 1)
			return err
		}
		server.JobsUpdateStateRuntimeOnly(task, "Reseed completed", 3, 1)
		return nil
	}

	if backupType == config.ConstBackupLogicalTypeDumpling {
		return errors.New("Logical reseed with dumpling is not supported")
	}
	if backupType == config.ConstBackupLogicalTypeRiver {
		return errors.New("Logical reseed with internal backup type is not supported")
	}
	if backupType != config.ConstBackupLogicalTypeMysqldump && backupType != config.ConstBackupLogicalTypeMydumper {
		return fmt.Errorf("Logical reseed backup type %s is not supported", backupType)
	}

	// Stamp the real start time now, before the potentially slow work below
	// (StopSlave, pointSlaveToMaster, the restore itself). No JobInsertTask
	// call anywhere in this function, in any scheduler state, so there is
	// never a DB row for a logical reseed task run.
	server.JobsUpdateStateRuntimeOnly(task, "processing", 1, 0)

	useMaster := true
	source := master
	var dest string
	var destCandidates []string
	switch backupType {
	case config.ConstBackupLogicalTypeMysqldump:
		dest = "mysqldump.sql.gz"
		destCandidates = []string{"mysqldump.sql.gz", "splitdump"}
	case config.ConstBackupLogicalTypeMydumper:
		dest = "mydumper"
	}
	if len(destCandidates) == 0 {
		destCandidates = []string{dest}
	}

	var backupfile string
	if payloadBackupPath != "" {
		if strings.EqualFold(payloadBackupPath, "STREAM") {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Logical reseed payload path indicates STREAM for %s; falling back to discovered backup", task)
		} else if _, err := os.Stat(payloadBackupPath); err == nil {
			backupfile = payloadBackupPath
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Using payload backup path %s for %s", backupfile, task)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Payload backup path not found for %s (%s); falling back to discovered backup", task, err)
		}
	}

	if backupfile == "" {
		bckserver := cluster.GetBackupServer()
		if bckserver != nil && bckserver.HasBackupTypeCookie(backupType) {
			if resolved, ok := resolveLogicalBackupPathFromMeta(bckserver, backupType); ok {
				backupfile = resolved
				useMaster = false
				source = bckserver
			} else if resolved, ok := findExistingBackupPath(bckserver, destCandidates); ok {
				backupfile = resolved
				useMaster = false
				source = bckserver
			} else {
				//Remove false cookie
				bckserver.DelBackupTypeCookie(backupType)
			}
		}

		if backupfile == "" {
			if resolved, ok := resolveLogicalBackupPathFromMeta(master, backupType); ok {
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
				master.DelBackupTypeCookie(backupType)
				return fmt.Errorf("No backup file found on master for %s", backupType)
			}
		}
	}

	meta := snapshotLogicalBackupMeta(source)
	var splitUserOverridePtr *bool
	if splitUserOverride {
		// A stable copy, not &splitUser: splitUser itself is about to be
		// reassigned by the call below, and taking its address here would
		// rely on Go's RHS-before-assignment evaluation order to read the
		// pre-reassignment value -- correct today, but fragile and non-obvious.
		overrideVal := splitUser
		splitUserOverridePtr = &overrideVal
	}
	// Re-resolved from fresh meta (not trusted from the payload's stored
	// split_user value) so a reseed prepared earlier and processed later
	// re-validates trust at execution time rather than inheriting whatever
	// prepare time computed -- metadata or the source server can have changed
	// in between (see resolveLogicalReseedUserRestore).
	restoreUser, splitUser, userRestoreAssessment := resolveLogicalReseedUserRestore(cluster, backupType, backupfile, meta, splitUserOverridePtr)

	// Set replication master to current master if not PITR
	if !isPITR {
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

	ctx := context.Background()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive reseed logical backup %s request for server: %s", backupType, server.URL)
	if splitUserOverride {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Using split-user override=%t for reseed logical backup from path %s on %s", splitUser, backupfile, server.URL)
	}
	// userRestoreAssessment was already resolved above (alongside
	// splitUser/restoreUser) from the same fresh meta -- re-derived at
	// execution start rather than parsed back out of the payload, so a reseed
	// prepared earlier and processed later still tells the same story now,
	// not just at prepare time (see resolveLogicalReseedUserRestore).
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical reseed user/system restore for %s: %s", server.URL, userRestoreAssessment.Message)

	var err error
	if backupType == config.ConstBackupLogicalTypeMysqldump {
		if skipMetadata {
			err = server.reseedMysqldumpWithSplitdump(ctx, backupfile, restoreUser)
		} else {
			err = server.reseedMysqldumpWithMetadata(ctx, backupfile, restoreUser, meta)
		}
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error reseed %s on %s: %s", backupType, server.URL, err.Error())
			server.JobsUpdateStateRuntimeOnly(task, err.Error(), 5, 1)
			return err
		}

		if server.IsSlave && !isPITR {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Start slave after dump on %s", server.URL)
			server.StartSlave()
		}

		server.JobsUpdateStateRuntimeOnly(task, "Reseed completed", 3, 1)
		return nil
	}

	if backupType == config.ConstBackupLogicalTypeMydumper {
		err = server.JobReseedMyLoader(backupfile, cluster.Conf.BackupRestoreMysqlUser)
		if err == nil && server.IsSlave && !isPITR {
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
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error reseed %s on %s: %s", backupType, server.URL, err.Error())
			server.JobsUpdateStateRuntimeOnly(task, err.Error(), 5, 1)
			return err
		}

		server.JobsUpdateStateRuntimeOnly(task, "Reseed completed", 3, 1)
		return nil
	}

	return fmt.Errorf("Logical reseed backup type %s is not supported", backupType)
}

func (server *ServerMonitor) ProcessReseedPhysical(task string) error {
	cluster := server.ClusterGroup
	master := cluster.GetMaster()

	//Prevent multiple reseed
	if !server.HasReseedingState(task) {
		return fmt.Errorf("Server is not in %s state: %w", task, errServerNotReseeding)
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
		return fmt.Errorf("Server is not in physical flashback state: %w", errServerNotReseeding)
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

// hasPrintDefaultsContent checks if a .tmp file received from dbjobs contains
// actual mariadbd --print-defaults output (at least one "--" prefixed line).
// Returns false if the file is missing, empty, or has no variable lines.
// This prevents overwriting good config files when the receiver got no data
// (e.g. repman shutdown, network failure, or dbjobs timeout).
func hasPrintDefaultsContent(tmpFile string) bool {
	info, err := os.Stat(tmpFile)
	if err != nil || info.Size() == 0 {
		return false
	}
	f, err := os.Open(tmpFile)
	if err != nil {
		return false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "--") {
			return true
		}
	}
	if scanner.Err() != nil {
		return false
	}
	return false
}

func (server *ServerMonitor) JobFinishReceiveFile(task string) error {
	cluster := server.ClusterGroup

	switch task {
	case "errorlog":
		server.DelWaitErrorlogCookie()
		server.maybeRetryDBLogMigration()
	case "slowquery":
		server.DelWaitSlowqueryCookie()
		server.maybeRetryDBLogMigration()
	case "auditlog":
		server.DelWaitAuditlogCookie()
		server.maybeRetryDBLogMigration()
	case "sqlerrorlog":
		server.DelWaitSqlErrorlogCookie()
		server.maybeRetryDBLogMigration()
	case config.ConstBackupPhysicalTypeXtrabackup, config.ConstBackupPhysicalTypeMariaBackup:
		backtype := "physical"
		// The SST file has been received — mark the backup as completed.
		// This used to rely on AfterJobProcess polling the DB job state, but
		// file receipt can happen before the poll runs, causing a race where
		// restic was skipped ("physical backup not completed").
		if server.LastBackupMeta.Physical != nil {
			server.LastBackupMeta.Physical.Completed = true
		}
		server.WriteBackupMetadata(backupmgr.BackupMethodPhysical)
		if server.LastBackupMeta.Physical != nil && !server.LastBackupMeta.Physical.StartTime.IsZero() {
			backupTool := server.LastBackupMeta.Physical.BackupTool
			if backupTool == "" {
				backupTool = cluster.Conf.BackupPhysicalType
			}
			elapsed := time.Since(server.LastBackupMeta.Physical.StartTime).Round(time.Second)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Physical backup %s completed in %s (started at %s) for: %s", backupTool, elapsed, server.LastBackupMeta.Physical.StartTime.Format(time.RFC3339), server.URL)
		}

		// Transition from traditional backup lock to Restic lock atomically
		// Set Restic flag BEFORE clearing physical backup flag
		resticEnabled := server.LastBackupMeta.Physical != nil && server.LastBackupMeta.Physical.ResticEnabled
		backupLine := backupmgr.BackupLineDefault
		if server.LastBackupMeta.Physical != nil && server.LastBackupMeta.Physical.BackupLine != "" {
			backupLine = server.LastBackupMeta.Physical.BackupLine
		}
		if resticEnabled {
			cluster.SetInResticPhysicalBackupState(true)
			resticPath := server.GetMyBackupDirectory()
			if server.LastBackupMeta.Physical != nil && server.LastBackupMeta.Physical.IsAdhoc() && server.LastBackupMeta.Physical.Dest != "" {
				resticPath = server.LastBackupMeta.Physical.Dest
			}
			server.BackupRestic(backupmgr.BackupMethodPhysical, true, resticPath, server.BuildResticTags(backtype, cluster.Conf.BackupPhysicalType, backupLine, server.LastBackupMeta.Physical)...)
		}

		cluster.SetInPhysicalBackupState(false)
	case "printdefault-current":
		filename := filepath.Join(server.Datadir, "current.cnf")
		tmpFile := filename + ".tmp"

		// Guard: do not overwrite good data with empty .tmp (e.g. repman shutdown
		// before dbjobs could send, or network failure). If the .tmp has no usable
		// content, keep the existing file and skip.
		if !hasPrintDefaultsContent(tmpFile) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Skipping current.cnf update: %s is empty or missing (receiver got no data)", tmpFile)
			break
		}

		os.Rename(filename, filename+".old")
		err := server.LoadFromTempConfigFile(tmpFile, filename)
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
		tmpFile := filename + ".tmp"

		// Guard: do not overwrite good data with empty .tmp (e.g. repman shutdown
		// before dbjobs could send, or network failure). If the .tmp has no usable
		// content, keep the existing file and skip.
		if !hasPrintDefaultsContent(tmpFile) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Skipping dummy.cnf update: %s is empty or missing (receiver got no data)", tmpFile)
			break
		}

		os.Rename(filename, filename+".old")
		err := server.LoadFromTempConfigFile(tmpFile, filename)
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
