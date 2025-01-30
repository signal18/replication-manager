// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"os"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/archiver"
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

func (cluster *Cluster) CheckResticRepo() bool {
	if !cluster.Conf.BackupRestic {
		return false
	}

	if cluster.ResticRepo == nil {
		cluster.SetState("WARN0095", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0095"], "restic repo is nil"), ErrFrom: "BACKUP"})
		return false
	}

	if !cluster.ResticRepo.CanInitRepo {
		cluster.SetState("WARN0095", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0096"], "restic repo cannot be initialized"), ErrFrom: "BACKUP"})
		return false
	}

	return true
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

func (cluster *Cluster) ResticInitRepo() error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	cluster.ResticRepo.SetEnv(cluster.ResticGetEnv())
	err := cluster.ResticRepo.ResticInitRepo()
	if err != nil {
		cluster.SetState("WARN0092", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0092"], err), ErrFrom: "BACKUP"})
	}

	return err
}

func (cluster *Cluster) ResticPurgeRepo() error {
	if cluster.Conf.BackupRestic {
		cluster.ResticRepo.SetEnv(cluster.ResticGetEnv())

		opt := archiver.ResticPurgeOption{
			KeepHourly:  cluster.Conf.BackupKeepHourly,
			KeepDaily:   cluster.Conf.BackupKeepDaily,
			KeepWeekly:  cluster.Conf.BackupKeepWeekly,
			KeepMonthly: cluster.Conf.BackupKeepMonthly,
			KeepYearly:  cluster.Conf.BackupKeepYearly,
		}

		_, err := cluster.ResticRepo.AddPurgeTask(opt, true)
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

	cluster.ResticRepo.SetEnv(cluster.ResticGetEnv())
	_, err := cluster.ResticRepo.AddFetchTask(true)
	if err != nil {
		cluster.SetState("WARN0093", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0093"], err), ErrFrom: "BACKUP"})
	}

	return err
}
