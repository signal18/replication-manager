// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
)

func (cluster *Cluster) RollingReprov() error {

	cluster.LogPrintf(LvlInfo, "Rolling reprovisionning")
	masterID := cluster.GetMaster().Id
	for _, slave := range cluster.slaves {
		if !slave.IsDown() {
			if !slave.IsMaintenance {
				slave.SwitchMaintenance()
			}
			err := cluster.UnprovisionDatabaseService(slave)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Cancel rolling reprov %s", err)
				return err
			}
			err = cluster.WaitDatabaseFailed(slave)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Cancel rolling restart slave does not transit suspect %s %s", slave.URL, err)
				return err
			}
			err = cluster.InitDatabaseService(slave)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Cancel rolling reprov %s", err)
				return err
			}
			err = cluster.StartDatabaseWaitRejoin(slave)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Cancel rolling reprov %s", err)
				return err
			}

			slave.WaitSyncToMaster(cluster.master)
			slave.SwitchMaintenance()
		}
	}
	cluster.SwitchoverWaitTest()
	master := cluster.GetServerFromName(masterID)
	if cluster.master.DSN == master.DSN {
		cluster.LogPrintf(LvlErr, "Cancel rolling restart master is the same after Switchover")
		return nil
	}
	if !master.IsDown() {
		if !master.IsMaintenance {
			master.SwitchMaintenance()
		}
		err := cluster.UnprovisionDatabaseService(master)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Cancel rolling reprov %s", err)
			return err
		}
		err = cluster.WaitDatabaseFailed(master)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Cancel rolling restart slave does not transit suspect %s %s", master.URL, err)
			return err
		}
		err = cluster.InitDatabaseService(master)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Cancel rolling reprov %s", err)
			return err
		}
		err = cluster.WaitDatabaseStart(master)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Cancel rolling reprov %s", err)
			return err
		}
		master.WaitSyncToMaster(cluster.master)
		master.SwitchMaintenance()
		cluster.SwitchOver()
	}
	return nil
}

func (cluster *Cluster) RollingRestart() error {
	cluster.LogPrintf(LvlInfo, "Rolling restart")
	masterID := cluster.GetMaster().Id
	saveFailoverMode := cluster.Conf.FailSync
	cluster.SetFailSync(false)
	defer cluster.SetFailSync(saveFailoverMode)
	for _, slave := range cluster.slaves {

		if !slave.IsDown() {
			//slave.SetMaintenance()
			//proxy.
			if !slave.IsMaintenance {
				slave.SwitchMaintenance()
			}
			err := cluster.StopDatabaseService(slave)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Cancel rolling restart stop failed on slave %s %s", slave.URL, err)
				slave.SwitchMaintenance()
				return err
			}

			err = cluster.WaitDatabaseFailed(slave)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Cancel rolling stop slave does not transit Failed %s %s", slave.URL, err)
				slave.SwitchMaintenance()
				return err
			}

			err = cluster.StartDatabaseWaitRejoin(slave)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Cancel rolling restart slave does not restart %s %s", slave.URL, err)
				return err
			}
		}
		slave.WaitSyncToMaster(cluster.master)
		slave.SwitchMaintenance()
	}
	cluster.SwitchoverWaitTest()
	master := cluster.GetServerFromName(masterID)
	if cluster.master.DSN == master.DSN {
		cluster.LogPrintf(LvlErr, "Cancel rolling original master %s is the same %s after switchover", master.URL, cluster.master.URL)
		return nil
	}
	if master.IsDown() {
		cluster.LogPrintf(LvlErr, "Cancel rolling original master is down %s", master.URL)
		return errors.New("Cancel rolling restart original master down")
	}
	if !master.IsMaintenance {
		master.SwitchMaintenance()
	}
	err := cluster.StopDatabaseService(master)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Cancel rolling restart old master stop failed %s %s", master.URL, err)
		return err
	}
	err = cluster.WaitDatabaseFailed(master)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Cancel rolling restart old master does not transit suspect %s %s", master.URL, err)
		return err
	}
	err = cluster.StartDatabaseWaitRejoin(master)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Cancel rolling restart old master does not restart %s %s", master.URL, err)
		return err
	}
	master.WaitSyncToMaster(cluster.master)
	master.SwitchMaintenance()
	cluster.SwitchOver()

	return nil
}

func (cluster *Cluster) RollingOptimize() {
	for _, s := range cluster.slaves {
		jobid, _ := s.JobOptimize()
		cluster.LogPrintf(LvlInfo, "Optimize job id %d on %s ", jobid, s.URL)
	}
}
