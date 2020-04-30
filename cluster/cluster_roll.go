// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
)

func (cluster *Cluster) RollingReprov() error {
	master := cluster.GetMaster()
	for _, slave := range cluster.slaves {
		if !slave.IsDown() {
			err := cluster.UnprovisionDatabaseService(slave)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Cancel rolling reprov %s", err)
				return err
			}
			err = cluster.WaitDatabaseFailed(slave)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Cancel rolling restart slave does not transit suspect %s %s", slave.DSN, err)
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

		}
	}
	cluster.SwitchoverWaitTest()
	if !master.IsDown() {
		err := cluster.UnprovisionDatabaseService(master)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Cancel rolling reprov %s", err)
			return err
		}
		err = cluster.WaitDatabaseFailed(master)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Cancel rolling restart slave does not transit suspect %s %s", master.DSN, err)
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
		cluster.SwitchOver()
	}
	return nil
}

func (cluster *Cluster) RollingRestart() error {
	masterID := cluster.GetMaster().Id
	saveFailoverMode := cluster.Conf.FailSync
	cluster.SetFailSync(false)
	defer cluster.SetFailSync(saveFailoverMode)
	for _, slave := range cluster.slaves {

		if !slave.IsDown() {
			//slave.SetMaintenance()
			//proxy.
			err := cluster.StopDatabaseService(slave)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Cancel rolling restart stop failed on slave %s %s", slave.DSN, err)
				return err
			}

			err = cluster.WaitDatabaseFailed(slave)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Cancel rolling restart slave does not transit suspect %s %s", slave.DSN, err)
				return err
			}

			err = cluster.StartDatabaseWaitRejoin(slave)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Cancel rolling restart slave does not restart %s %s", slave.DSN, err)
				return err
			}
		}
		slave.WaitSyncToMaster(cluster.master)
	}
	cluster.SwitchoverWaitTest()
	master := cluster.GetServerFromName(masterID)
	if cluster.master.DSN == master.DSN {
		cluster.LogPrintf(LvlErr, "Cancel rolling restart master is the same after Switchover")
		return nil
	}
	if master.IsDown() {
		return errors.New("Cancel roolling restart master down")
	}
	err := cluster.StopDatabaseService(master)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Cancel rolling restart old master stop failed %s %s", master.DSN, err)
		return err
	}
	err = cluster.WaitDatabaseFailed(master)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Cancel rolling restart old master does not transit suspect %s %s", master.DSN, err)
		return err
	}
	err = cluster.StartDatabaseWaitRejoin(master)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Cancel rolling restart old master does not restart %s %s", master.DSN, err)
		return err
	}
	master.WaitSyncToMaster(cluster.master)

	cluster.SwitchOver()

	return nil
}

func (cluster *Cluster) RollingOptimize() {
	for _, s := range cluster.slaves {
		jobid, _ := s.JobOptimize()
		cluster.LogPrintf(LvlInfo, "Optimize job id %d on %s ", jobid, s.URL)
	}
}
