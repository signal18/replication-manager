// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/dustin/go-humanize"
	"github.com/shirou/gopsutil/disk"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
)

func (cluster *Cluster) ResticGetEnv() []string {
	newEnv := append(os.Environ(), "RESTIC_PASSWORD="+cluster.Conf.GetDecryptedValue("backup-restic-password"))
	newEnv = append(newEnv, "RESTIC_CACHE_DIR="+cluster.Conf.WorkingDir+"/"+cluster.Name+"/.cache/restic")

	if cluster.Conf.BackupResticAws {
		newEnv = append(newEnv, "AWS_ACCESS_KEY_ID="+cluster.Conf.BackupResticAwsAccessKeyId)
		newEnv = append(newEnv, "AWS_SECRET_ACCESS_KEY="+cluster.Conf.GetDecryptedValue("backup-restic-aws-access-secret"))
		newEnv = append(newEnv, "RESTIC_REPOSITORY="+cluster.Conf.BackupResticRepository+"/"+cluster.Name)
	} else {
		if _, err := os.Stat(cluster.GetResticLocalDir()); os.IsNotExist(err) {
			err := os.MkdirAll(cluster.GetResticLocalDir(), os.ModePerm)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Create archive directory failed: %s,%s", cluster.GetResticLocalDir(), err)
			}
		}
		newEnv = append(newEnv, "RESTIC_REPOSITORY="+cluster.GetResticLocalDir())
	}
	return newEnv
}

func (cluster *Cluster) ReloadResticEnv() {
	if cluster.ResticManager != nil {
		cluster.ResticManager.SetEnv(cluster.ResticGetEnv())
	}
}

func (cluster *Cluster) CheckResticInstallation() {
	if cluster.Conf.BackupRestic && cluster.VersionsMap.Get("restic") == nil {
		if err := cluster.RefreshResticVersion(); err != nil {
			cluster.SetState("WARN0121", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0121"], err), ErrFrom: "CLUSTER"})
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Restic version: %s", cluster.VersionsMap.Get("restic").ToString())
		}
	}
}

func (cluster *Cluster) CheckResticErrors() {
	if !cluster.Conf.BackupRestic {
		return
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	// If repo cannot be initialized, all other errors are not relevant. So we just fetch the init repo errors
	if !cluster.ResticManager.CanInitRepo && cluster.ResticManager.HasAnyError() {
		err := cluster.ResticManager.FetchAndClearError(backupmgr.InitTask)
		cluster.SetState("WARN0095", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0095"], err), ErrFrom: "BACKUP"})
		return
	}

	for task, err := range cluster.ResticManager.FetchAndClearErrors() {
		switch task {
		case backupmgr.FetchTask:
			cluster.SetState("WARN0093", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0093"], err), ErrFrom: "BACKUP"})
		case backupmgr.PurgeTask:
			cluster.SetState("WARN0094", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0094"], err), ErrFrom: "BACKUP"})
		case backupmgr.UnlockTask:
			cluster.SetState("WARN0095", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0095"], err), ErrFrom: "BACKUP"})
		default:
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Unknown restic task error: %s", err)
		}
	}

}

func (cluster *Cluster) CheckResticConfigBackup() {
	if !cluster.Conf.BackupRestic {
		return
	}

	if err := cluster.BackupResticConfig(); err != nil {
		cluster.SetState("WARN0145", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0145"], err), ErrFrom: "BACKUP"})
	}
}

func (cluster *Cluster) StartResticManager() error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	cluster.ResticManager = backupmgr.NewResticRepo(cluster.Conf.BackupResticBinaryPath, cluster.MessageChan, config.ConstLogModRestic)
	cluster.ReloadResticEnv()
	go cluster.ResticFetchRepo()
	return nil
}

func (cluster *Cluster) ResticInitRepo(force bool) error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	err := cluster.ResticManager.InitRepo(force)
	if err != nil {
		cluster.SetState("WARN0092", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0092"], err), ErrFrom: "BACKUP"})
	}

	return err
}

func (cluster *Cluster) AddPurgeTask(snapshotID string) error {
	if !cluster.Conf.BackupRestic {
		return fmt.Errorf("Restic backup is not enabled")
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	if snapshotID == "" {
		return fmt.Errorf("Unable to purge single snapshot: snapshot ID is empty")
	}

	cluster.ResticManager.AddPurgeTask(backupmgr.ResticPurgeOption{
		SnapshotID: snapshotID,
	})
	return nil
}

func (cluster *Cluster) ResticPurgeRepo() error {
	if cluster.Conf.BackupRestic {
		err := cluster.Conf.CheckKeepWithin() // Check if backup-keep-within is valid
		if err != nil {
			cluster.SetState("WARN0094", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0094"], err), ErrFrom: "BACKUP"})
			return err
		}

		if cluster.ResticManager == nil {
			cluster.StartResticManager()
		}

		cluster.ResticManager.AddPurgeTask(backupmgr.ResticPurgeOption{
			KeepLast:          cluster.Conf.BackupKeepLast,
			KeepHourly:        cluster.Conf.BackupKeepHourly,
			KeepDaily:         cluster.Conf.BackupKeepDaily,
			KeepWeekly:        cluster.Conf.BackupKeepWeekly,
			KeepMonthly:       cluster.Conf.BackupKeepMonthly,
			KeepYearly:        cluster.Conf.BackupKeepYearly,
			KeepWithin:        cluster.Conf.BackupKeepWithin,
			KeepWithinHourly:  cluster.Conf.BackupKeepWithinHourly,
			KeepWithinDaily:   cluster.Conf.BackupKeepWithinDaily,
			KeepWithinWeekly:  cluster.Conf.BackupKeepWithinWeekly,
			KeepWithinMonthly: cluster.Conf.BackupKeepWithinMonthly,
			KeepWithinYearly:  cluster.Conf.BackupKeepWithinYearly,
		})

	}
	return nil
}

func (cluster *Cluster) ResticFetchRepo() {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	// Check if no other fetch task queued
	if !cluster.ResticManager.HasFetchQueue() {
		cluster.ResticManager.AddFetchTask()
	}
}

func (cluster *Cluster) BackupResticConfig() error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if _, err := os.Stat(filepath.Join(cluster.Conf.WorkingDir, cluster.Name, "restic.config.bak")); err == nil {
		// Backup already exists
		return nil
	}

	repopath := cluster.ResticManager.GetRepoPath()
	if repopath == "" {
		return fmt.Errorf("restic repo path is empty")
	}

	dest := filepath.Join(cluster.Conf.WorkingDir, cluster.Name, "restic.config.bak")
	src := filepath.Join(repopath, "config")

	err := misc.CopyFile(src, dest)
	if err != nil {
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Restic config file backed up to %s", dest)
	return nil
}

func (cluster *Cluster) RestoreResticConfig(force bool) error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	repopath := cluster.ResticManager.GetRepoPath()
	if repopath == "" {
		return fmt.Errorf("restic repo path is empty")
	}

	_, err := os.Stat(filepath.Join(repopath, "config"))
	if !os.IsNotExist(err) && !force {
		return fmt.Errorf("restic config file already exists in repo path %s", repopath)
	}

	dest := filepath.Join(repopath, "config")
	src := filepath.Join(cluster.Conf.WorkingDir, cluster.Name, "restic.config.bak")

	err = misc.CopyFile(src, dest)
	if err != nil {
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Restic config file restored from %s", src)
	return nil
}

func (cluster *Cluster) ResticUnlockRepo() {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	cluster.ResticManager.AddUnlockTask()

}

func (cluster *Cluster) ResticGetQueue() ([]*backupmgr.ResticTask, error) {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return nil, nil
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	return cluster.ResticManager.TaskQueue, nil
}

func (cluster *Cluster) ResticModifyQueue(moveType string, taskID, cmpID int) error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	return cluster.ResticManager.MoveTask(moveType, taskID, cmpID)
}

func (cluster *Cluster) ResticCancelTask(taskId int) error {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cancelling restic task ID %d", taskId)

	cluster.ResticManager.CancelTask(taskId)

	return nil
}

func (cluster *Cluster) ResticClearQueue() error {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Clearing pending restic tasks from queue. Total tasks: %d", len(cluster.ResticManager.TaskQueue))

	cluster.ResticManager.ClearQueue()

	return nil
}

// ResticRunQueue starts processing the restic task queue
func (cluster *Cluster) ResticRunQueue() {

	if !cluster.Conf.BackupRestic {
		return
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting restic task queue processing. Total tasks: %d", len(cluster.ResticManager.TaskQueue))
	cluster.ResticManager.ResumeWorker()
}

// ResticPauseQueue pauses the next restic task queue processing
func (cluster *Cluster) ResticPauseQueue() {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Pausing restic task queue processing")
	cluster.ResticManager.PauseWorker()
}

func (cluster *Cluster) CheckBackupFreeSpace(backtype string, backup bool) error {
	var isWarning bool
	bcksrv := cluster.GetBackupServer()
	if bcksrv == nil {
		bcksrv = cluster.GetMaster()
		if bcksrv == nil {
			return fmt.Errorf("No backup server or master server found for cluster %s", cluster.Name)
		}
	}

	parentDir := cluster.Conf.WorkingDir + "/" + config.ConstStreamingSubDir + "/" + cluster.Name
	_, err := os.Stat(parentDir)
	if os.IsNotExist(err) {
		err = os.MkdirAll(parentDir, os.ModePerm)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error creating directory %s: %s", parentDir, err)
		}
	}

	diskstat, err := disk.Usage(parentDir)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error getting disk usage: %s", err)
		return err
	}

	if diskstat == nil {
		err := fmt.Errorf("disk usage is nil for %s", parentDir)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error getting disk usage: %s", err)
		return err
	}

	cluster.DiskStatManager.UpdateStat(parentDir, diskstat)
	if diskstat.UsedPercent > float64(cluster.Conf.BackupDiskTresholdCrit) {
		cluster.SetState("WARN0140", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0140"], diskstat.Path, diskstat.UsedPercent, cluster.Conf.BackupDiskTresholdCrit), ErrFrom: "JOB", ServerUrl: bcksrv.URL})
		return fmt.Errorf("Disk usage is over %d%% on %s. Used: %s", cluster.Conf.BackupDiskTresholdCrit, diskstat.Path, humanize.Bytes(diskstat.Used))
	} else if diskstat.UsedPercent > float64(cluster.Conf.BackupDiskTresholdWarn) {
		isWarning = true
		cluster.SetState("WARN0139", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0139"], diskstat.Path, diskstat.UsedPercent, cluster.Conf.BackupDiskTresholdWarn), ErrFrom: "JOB", ServerUrl: bcksrv.URL})
	}

	// Estimate size if disk usage is over treshold and estimate size is enabled. For binlog we will always estimate size to 2GB
	if (isWarning && cluster.Conf.BackupEstimateSize) || backtype == "binlog" {
		free := diskstat.Free
		required := uint64(0)

		switch backtype {
		case "logical", "physical":
			_, prev := bcksrv.GetLatestMeta(backtype)
			if prev != nil && prev.Completed {
				required = uint64(prev.Size * int64(100+cluster.Conf.BackupGrowthPercentage) / 100)

				// If not keep until valid, we need to add the size of the previous backup to the free space
				if !cluster.Conf.BackupKeepUntilValid {
					free = free + uint64(prev.Size)
				}

			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "No previous backup found for %s. Estimating backup size.", bcksrv.URL)
				estimatedSize, err := dbhelper.GetBackupSizeEstimation(bcksrv.Conn, bcksrv.DBVersion)
				if err != nil {
					return fmt.Errorf("Error estimating backup size: %s", err)
				}

				required = estimatedSize * uint64(cluster.Conf.BackupEstimateSizePercentage) / 100
			}
		case "binlog":
			// Max binlog size per file is 1GB, additional 1GB for unexpected growth
			required = 2 * 1024 * 1024 * 1024
		case "restic":
			// Restic backup size is not known until the backup is done
		}

		if free < required {
			if backtype == "logical" {
				cluster.SetState("WARN0141", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0141"], cluster.Conf.BackupLogicalType, bcksrv.URL, diskstat.Path, humanize.Bytes(diskstat.Free), humanize.Bytes(required)), ErrFrom: "JOB", ServerUrl: bcksrv.URL})
			} else if backtype == "physical" {
				cluster.SetState("WARN0142", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0142"], cluster.Conf.BackupPhysicalType, bcksrv.URL, diskstat.Path, humanize.Bytes(diskstat.Free), humanize.Bytes(required)), ErrFrom: "JOB", ServerUrl: bcksrv.URL})
			} else if backtype == "binlog" {
				cluster.SetState("WARN0143", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0143"], bcksrv.URL, diskstat.Path, humanize.Bytes(diskstat.Free), humanize.Bytes(required)), ErrFrom: "JOB", ServerUrl: bcksrv.URL})
			}

			return fmt.Errorf("Not enough free space on %s for backup. Free: %s", diskstat.Path, humanize.Bytes(diskstat.Free))
		}

		if backup {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Free space is enough on %s: %s. Required: %s", diskstat.Path, humanize.Bytes(diskstat.Free), humanize.Bytes(required))
		}
	}

	return nil
}

func (cluster *Cluster) CheckAllBackupFreeSpace() {
	if !cluster.Conf.BackupCheckFreeSpace {
		return
	}

	// Check based on treshold
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		cluster.CheckBackupFreeSpace("logical", false)
		wg.Done()
	}()

	// if estimate size is enabled, check the free space for physical and binlog backups too
	if cluster.Conf.BackupEstimateSize {
		wg.Add(2)
		go func() {
			cluster.CheckBackupFreeSpace("physical", false)
			wg.Done()
		}()
		go func() {
			cluster.CheckBackupFreeSpace("binlog", false)
			wg.Done()
		}()
	}

	wg.Wait()
}

// TODO: Restic password change
func (cluster *Cluster) ChangeResticRepoPassword(newpass string) error {
	if !cluster.Conf.BackupRestic {
		return fmt.Errorf("Restic backup is not enabled")
	}

	if newpass == "" {
		return fmt.Errorf("New password is empty")
	}

	if newpass == cluster.Conf.GetDecryptedValue("backup-restic-password") {
		return fmt.Errorf("New password is the same as the current one")
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Changing restic password for cluster %s", cluster.Name)

	cluster.ReloadResticEnv()

	keylist, err := cluster.ResticManager.GetRepoKeyList()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Failed to list restic keys: %s", err)
		return err
	}

	keylen := len(keylist)
	if keylen == 0 {
		return fmt.Errorf("No keys found in the restic repository")
	}

	oldkeyid := ""
	for _, key := range keylist {
		if key.Current {
			oldkeyid = key.Id
			break
		}
	}

	if _, err := os.Stat(cluster.ResticManager.GetCacheDirPath()); os.IsNotExist(err) {
		err := os.MkdirAll(cluster.ResticManager.GetCacheDirPath(), os.ModePerm)
		if err != nil {
			return fmt.Errorf("Error creating restic cache directory: %s", err)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Restic cache directory created: %s", cluster.ResticManager.GetCacheDirPath())
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Adding new key to restic repository")

	newpassfile := filepath.Join(cluster.ResticManager.GetCacheDirPath(), "newpass.txt")
	err = os.WriteFile(newpassfile, []byte(newpass), 0600)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Failed to write new password file: %s", err)
		return fmt.Errorf("failed to write new password file: %w", err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Temporary password file created: %s", newpassfile)

	defer func() {
		if _, err := os.Stat(newpassfile); err == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Removing temporary password file")
			err := os.Remove(newpassfile)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Failed to remove temporary password file: %s", err)
			}
		}
	}()

	err = cluster.ResticManager.AddRepoKey(newpassfile)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Failed to add new key to restic repository: %s", err)
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "New key added to restic repository successfully. Saving new password.")

	// Save new password in configuration
	cluster.SetResticPassword(newpass)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "New restic password saved in configuration successfully. Removing old key from repository using new password.")

	// Reload env with new password
	cluster.ReloadResticEnv()

	// Remove old key using new password
	err = cluster.ResticManager.RemoveRepoKey(oldkeyid)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Failed to remove old key from restic repository: %s", err)
		return nil
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Restic password changed successfully. New key added and old key removed.")

	return nil
}

func (cluster *Cluster) CheckBackupToolVersions() {
	bcksrv := cluster.GetBackupServer()
	if bcksrv == nil {
		bcksrv = cluster.GetMaster()
		if bcksrv == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "No backup server or master server found for cluster %s", cluster.Name)
			return
		}
	}

	cluster.CheckLogicalBackupToolVersion(bcksrv)
	cluster.CheckPhysicalBackupToolVersion(bcksrv)
}

func (cluster *Cluster) CheckLogicalBackupToolVersion(server *ServerMonitor) error {
	if server == nil {
		return fmt.Errorf("Server is nil")
	}

	_, logical := server.GetLatestMeta("logical")
	if logical != nil {
		v, _ := cluster.GetToolsVersion(logical.BackupTool)
		if v != nil && logical.BackupToolVersion != "" {
			backupv, _ := version.NewVersionFromString(logical.BackupTool, logical.BackupToolVersion)
			if v.ToInt(2) != backupv.ToInt(2) { // Major and minor version must match
				cluster.SetState("WARN0156", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0156"], v.ToString(), logical.BackupToolVersion), ErrFrom: "CHECK", ServerUrl: server.URL})
				return fmt.Errorf("Node %s backup tool version is not compatible with restore version.", server.URL)
			} else if cluster.IsInErrorState("WARN0156", server.URL) {
				// Remove state if version is now correct
				cluster.GetStateMachine().DeleteState(fmt.Sprintf("WARN0156@%s", server.URL))
			}
		}
	}
	return nil
}

func (cluster *Cluster) CheckPhysicalBackupToolVersion(server *ServerMonitor) error {
	if server == nil {
		return fmt.Errorf("Server is nil")
	}

	_, physical := server.GetLatestMeta("physical")
	if physical != nil {
		v, _ := cluster.GetToolsVersion(physical.BackupTool)
		if v != nil && physical.BackupToolVersion != "" {
			backupv, _ := version.NewVersionFromString(physical.BackupTool, physical.BackupToolVersion)
			if v.ToInt(2) != backupv.ToInt(2) { // Major and minor version must match
				cluster.SetState("WARN0157", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0157"], v.ToString(), physical.BackupToolVersion), ErrFrom: "CHECK", ServerUrl: server.URL})
				return fmt.Errorf("Node %s backup tool version is not same with restore version.", server.URL)
			} else if cluster.IsInErrorState("WARN0157", server.URL) {
				// Remove state if version is now correct
				cluster.GetStateMachine().DeleteState(fmt.Sprintf("WARN0157@%s", server.URL))
			}
		}
	}
	return nil
}
