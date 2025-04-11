// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"os"
	"sync"

	"github.com/dustin/go-humanize"
	"github.com/shirou/gopsutil/disk"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/archiver"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/sirupsen/logrus"
)

func (cluster *Cluster) ResticGetEnv() []string {
	newEnv := append(os.Environ(), "RESTIC_PASSWORD="+cluster.Conf.GetDecryptedValue("backup-restic-password"))
	newEnv = append(newEnv, "RESTIC_CACHE_DIR=$HOME/.cache/restic")

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

func (cluster *Cluster) CheckResticInstallation() {
	if cluster.Conf.BackupRestic && cluster.VersionsMap.Get("restic") == nil {
		if err := cluster.SetResticVersion(); err != nil {
			cluster.SetState("WARN0121", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0121"], err), ErrFrom: "CLUSTER"})
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Restic version: %s", cluster.VersionsMap.Get("restic").ToString())
		}
	}
}

func (cluster *Cluster) StartResticRepo() error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	var loglevel logrus.Level
	if cluster.Conf.LogArchiveLevel > 0 {
		loglevel = config.ToLogrusLevel(cluster.Conf.LogArchiveLevel)
	}

	cluster.ResticRepo = archiver.NewResticRepo(cluster.Conf.BackupResticBinaryPath, cluster.Logrus, logrus.Fields{"cluster": cluster.Name, "type": "log", "module": "restic"}, loglevel)
	go cluster.ResticFetchRepo()
	return nil
}

func (cluster *Cluster) ResticInitRepo(force bool) error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	cluster.ResticRepo.SetEnv(cluster.ResticGetEnv())
	err := cluster.ResticRepo.ResticInitRepo(force)
	if err != nil {
		cluster.SetState("WARN0092", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0092"], err), ErrFrom: "BACKUP"})
	}

	return err
}

func (cluster *Cluster) ResticPurgeRepo() error {
	if cluster.Conf.BackupRestic {
		err := cluster.Conf.CheckKeepWithin() // Check if backup-keep-within is valid
		if err != nil {
			cluster.SetState("WARN0094", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0094"], err), ErrFrom: "BACKUP"})
			return err
		}

		cluster.ResticRepo.SetEnv(cluster.ResticGetEnv())

		opt := archiver.ResticPurgeOption{
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
		}

		_, err = cluster.ResticRepo.AddPurgeTask(opt, true)
		if err != nil {
			cluster.SetState("WARN0094", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0094"], err), ErrFrom: "BACKUP"})
			return err
		}
	}
	return nil
}

func (cluster *Cluster) ResticFetchRepo() error {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if cluster.ResticRepo == nil {
		err := fmt.Errorf("restic repo is nil")
		cluster.SetState("WARN0095", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0095"], err), ErrFrom: "BACKUP"})
		return err
	}

	// Check if no other fetch task queued
	if cluster.ResticRepo.HasFetchQueue() {
		return nil
	}

	cluster.ResticRepo.SetEnv(cluster.ResticGetEnv())
	_, err := cluster.ResticRepo.AddFetchTask(true)
	if err != nil {
		if !cluster.ResticRepo.CanInitRepo {
			cluster.SetState("WARN0095", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0096"], "restic repo cannot be initialized"), ErrFrom: "BACKUP"})
		} else if cluster.ResticRepo.CanFetch && cluster.ResticRepo.HasLocks {
			cluster.SetState("WARN0134", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0134"], cluster.ResticRepo.GetRepoPath()), ErrFrom: "BACKUP"})
		} else {
			cluster.SetState("WARN0093", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0093"], err), ErrFrom: "BACKUP"})
		}
	}

	return err
}

func (cluster *Cluster) ResticUnlockRepo() error {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if cluster.ResticRepo == nil {
		err := fmt.Errorf("restic repo is nil")
		cluster.SetState("WARN0095", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0095"], err), ErrFrom: "BACKUP"})
		return err
	}

	cluster.ResticRepo.SetEnv(cluster.ResticGetEnv())
	_, err := cluster.ResticRepo.AddUnlockTask(true)
	if err != nil {
		cluster.SetState("WARN0093", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0093"], err), ErrFrom: "BACKUP"})
	}

	return err
}

func (cluster *Cluster) ResticGetQueue() ([]*archiver.ResticTask, error) {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return nil, nil
	}

	if cluster.ResticRepo == nil {
		err := fmt.Errorf("restic repo is nil")
		cluster.SetState("WARN0095", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0095"], err), ErrFrom: "BACKUP"})
		return nil, err
	}

	return cluster.ResticRepo.TaskQueue, nil
}

func (cluster *Cluster) ResticResetQueue() error {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if cluster.ResticRepo == nil {
		err := fmt.Errorf("restic repo is nil")
		cluster.SetState("WARN0095", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0095"], err), ErrFrom: "BACKUP"})
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Resetting restic queue. This will not affect the current running task.")

	cluster.ResticRepo.SetEnv(cluster.ResticGetEnv())
	cluster.ResticRepo.EmptyQueue()

	return nil
}

func (cluster *Cluster) CheckBackupFreeSpace(backtype string, backup bool) error {
	var isWarning bool
	bcksrv := cluster.GetBackupServer()
	if bcksrv == nil {
		bcksrv = cluster.master
	}

	parentDir := cluster.Conf.WorkingDir + "/" + config.ConstStreamingSubDir + "/" + cluster.Name
	diskstat, err := disk.Usage(parentDir)
	if err != nil {
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
				required = uint64(prev.Size * int64(cluster.Conf.BackupGrowthPercentage) / 100)

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
				cluster.SetState("WARN0141", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0139"], cluster.Conf.BackupLogicalType, bcksrv.URL, diskstat.Path, humanize.Bytes(diskstat.Free), humanize.Bytes(required)), ErrFrom: "JOB", ServerUrl: bcksrv.URL})
			} else if backtype == "physical" {
				cluster.SetState("WARN0142", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0140"], cluster.Conf.BackupPhysicalType, bcksrv.URL, diskstat.Path, humanize.Bytes(diskstat.Free), humanize.Bytes(required)), ErrFrom: "JOB", ServerUrl: bcksrv.URL})
			} else if backtype == "binlog" {
				cluster.SetState("WARN0143", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0141"], bcksrv.URL, diskstat.Path, humanize.Bytes(diskstat.Free), humanize.Bytes(required)), ErrFrom: "JOB", ServerUrl: bcksrv.URL})
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
