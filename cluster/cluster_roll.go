// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"time"

	"github.com/signal18/replication-manager/config"
)

func (cluster *Cluster) RollingReprov() error {

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rolling reprovisionning")
	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found for rolling reprovisionning")
	}
	masterID := master.Id
	for _, slave := range cluster.slaves {
		if slave == nil || slave.IsIgnored() {
			continue
		}

		if !slave.IsDown() {
			maintenanceEnabled := !slave.IsMaintenance
			if maintenanceEnabled {
				slave.SwitchMaintenance()
			}
			err := cluster.UnprovisionDatabaseService(slave)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling reprov %s", err)
				return err
			}
			err = cluster.WaitDatabaseFailed(slave)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling restart slave does not transit suspect %s %s", slave.URL, err)
				return err
			}
			err = cluster.InitDatabaseService(slave)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling reprov %s", err)
				return err
			}
			err = cluster.StartDatabaseWaitRejoin(slave)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling reprov %s", err)
				return err
			}

			currentMaster := cluster.GetMaster()
			if currentMaster == nil {
				return errors.New("No master found for sync during rolling reprovisionning")
			}
			slave.WaitSyncToMaster(currentMaster)
			if maintenanceEnabled {
				slave.SwitchMaintenance()
			}
		}
	}
	cluster.SwitchoverWaitTest()
	master = cluster.GetServerFromName(masterID)
	if cluster.master == nil {
		return errors.New("No master found after switchover during rolling reprovisionning")
	}
	if cluster.master.DSN == master.DSN {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling restart master is the same after Switchover")
		return nil
	}
	if !master.IsDown() {
		maintenanceEnabled := !master.IsMaintenance
		if maintenanceEnabled {
			master.SwitchMaintenance()
		}
		err := cluster.UnprovisionDatabaseService(master)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling reprov %s", err)
			return err
		}
		err = cluster.WaitDatabaseFailed(master)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling restart slave does not transit suspect %s %s", master.URL, err)
			return err
		}
		err = cluster.InitDatabaseService(master)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling reprov %s", err)
			return err
		}
		err = cluster.WaitDatabaseStart(master)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling reprov %s", err)
			return err
		}
		master.WaitSyncToMaster(cluster.master)
		if maintenanceEnabled {
			master.SwitchMaintenance()
		}
		cluster.SwitchOver()
	}
	return nil
}

func (cluster *Cluster) RollingRestart() error {
	cluster.SetInRollingRestart(true)
	defer cluster.SetInRollingRestart(false)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rolling restart")
	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found for rolling restart")
	}
	masterID := master.Id
	saveFailoverMode := cluster.Conf.FailSync
	cluster.SetFailSync(false)
	defer cluster.SetFailSync(saveFailoverMode)
	for _, slave := range cluster.slaves {
		if slave == nil || slave.IsIgnored() {
			continue
		}
		maintenanceEnabled := false
		if !slave.IsDown() {
			maintenanceEnabled = !slave.IsMaintenance
			if maintenanceEnabled {
				slave.SwitchMaintenance()
			}

			writeOnce := true
			for slave.IsBackingUpBinaryLog {
				if writeOnce {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Waiting slave %s to finish binlog backup", slave.URL)
					writeOnce = false
				}
				time.Sleep(time.Second)
			}

			err := cluster.StopDatabaseService(slave)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling restart stop failed on slave %s %s", slave.URL, err)
				if maintenanceEnabled {
					slave.SwitchMaintenance()
				}
				return err
			}

			err = cluster.WaitDatabaseFailed(slave)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling stop slave does not transit Failed %s %s", slave.URL, err)
				if maintenanceEnabled {
					slave.SwitchMaintenance()
				}
				return err
			}

			err = cluster.StartDatabaseWaitRejoin(slave)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling restart slave does not restart %s %s", slave.URL, err)
				return err
			}
		}
		currentMaster := cluster.GetMaster()
		if currentMaster == nil {
			return errors.New("No master found for sync during rolling restart")
		}
		slave.WaitSyncToMaster(currentMaster)
		if maintenanceEnabled {
			slave.SwitchMaintenance()
		}
	}
	cluster.SwitchoverWaitTest()
	master = cluster.GetServerFromName(masterID)
	if cluster.master == nil {
		return errors.New("No master found after switchover during rolling restart")
	}
	if cluster.master.DSN == master.DSN {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling original master %s is the same %s after switchover", master.URL, cluster.master.URL)
		return nil
	}
	if master.IsDown() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling original master is down %s", master.URL)
		return errors.New("Cancel rolling restart original master down")
	}
	maintenanceEnabled := !master.IsMaintenance
	if maintenanceEnabled {
		master.SwitchMaintenance()
	}
	writeOnce := true
	for master.IsBackingUpBinaryLog {
		if writeOnce {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Waiting master %s to finish binlog backup", master.URL)
			writeOnce = false
		}
		time.Sleep(time.Second)
	}
	err := cluster.StopDatabaseService(master)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling restart old master stop failed %s %s", master.URL, err)
		return err
	}
	err = cluster.WaitDatabaseFailed(master)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling restart old master does not transit suspect %s %s", master.URL, err)
		return err
	}
	err = cluster.StartDatabaseWaitRejoin(master)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling restart old master does not restart %s %s", master.URL, err)
		return err
	}
	master.WaitSyncToMaster(cluster.master)
	if maintenanceEnabled {
		master.SwitchMaintenance()
	}
	cluster.SwitchOver()

	return nil
}

func (cluster *Cluster) RollingOptimize() {
	for _, s := range cluster.slaves {
		if s == nil || s.IsIgnored() {
			continue
		}
		jobid, _ := s.JobOptimize()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Optimize job id %d on %s ", jobid, s.URL)
	}
}

func (cluster *Cluster) RollingUpgrade() error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rolling upgrade")

	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found for rolling upgrade")
	}
	masterID := master.Id

	// Loop 1 — pull: set image_pull_policy=always and restart every slave so
	// OpenSVC re-pulls the new image. OpenSVC only reads this key at container
	// start time, so the stop→start cycle is required to trigger the pull.
	// Maintenance is toggled per node and cleared after sync, so nodes are only
	// in maintenance during their own stop/start cycle.
	for _, slave := range cluster.slaves {
		if slave == nil || slave.IsIgnored() || slave.IsDown() {
			continue
		}
		maintEnabled := !slave.IsMaintenance
		if maintEnabled {
			slave.SwitchMaintenance()
		}
		if cfgErr := cluster.UpdateDatabaseServiceConfig(slave, true); cfgErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade: failed to push service config for %s: %s", slave.URL, cfgErr)
		}
		err := cluster.StopDatabaseService(slave)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade: stop failed on slave %s %s", slave.URL, err)
			if maintEnabled {
				slave.SwitchMaintenance()
			}
			return err
		}
		err = cluster.WaitDatabaseFailed(slave)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade: slave does not transit failed %s %s", slave.URL, err)
			if maintEnabled {
				slave.SwitchMaintenance()
			}
			return err
		}
		err = cluster.StartDatabaseWaitRejoin(slave)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade: slave does not restart %s %s", slave.URL, err)
			if maintEnabled {
				slave.SwitchMaintenance()
			}
			return err
		}
		currentMaster := cluster.GetMaster()
		if currentMaster == nil {
			if maintEnabled {
				slave.SwitchMaintenance()
			}
			return errors.New("No master found for sync during rolling upgrade")
		}
		slave.WaitSyncToMaster(currentMaster)
		if maintEnabled {
			slave.SwitchMaintenance()
		}
	}

	// Loop 2 — clean: strip image_pull_policy=always and restart every slave so
	// the key is absent from the live config. Docker will not re-pull the
	// already-local image, so this cycle costs only container restart time.
	// Maintenance is re-evaluated per node; nodes were cleared at the end of
	// loop 1 so they served traffic between the two loops.
	for _, slave := range cluster.slaves {
		if slave == nil || slave.IsIgnored() || slave.IsDown() {
			continue
		}
		maintEnabled := !slave.IsMaintenance
		if maintEnabled {
			slave.SwitchMaintenance()
		}
		if cfgErr := cluster.UpdateDatabaseServiceConfig(slave, false); cfgErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade: failed to reset service config for %s: %s", slave.URL, cfgErr)
		}
		err := cluster.StopDatabaseService(slave)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade: stop failed on slave %s %s (clean config)", slave.URL, err)
			if maintEnabled {
				slave.SwitchMaintenance()
			}
			return err
		}
		err = cluster.WaitDatabaseFailed(slave)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade: slave does not transit failed %s %s (clean config)", slave.URL, err)
			if maintEnabled {
				slave.SwitchMaintenance()
			}
			return err
		}
		err = cluster.StartDatabaseWaitRejoin(slave)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade: slave does not restart %s %s (clean config)", slave.URL, err)
			if maintEnabled {
				slave.SwitchMaintenance()
			}
			return err
		}
		currentMaster := cluster.GetMaster()
		if currentMaster == nil {
			if maintEnabled {
				slave.SwitchMaintenance()
			}
			return errors.New("No master found for sync during rolling upgrade (clean config)")
		}
		slave.WaitSyncToMaster(currentMaster)
		if maintEnabled {
			slave.SwitchMaintenance()
		}
	}

	cluster.SwitchoverWaitTest()
	master = cluster.GetServerFromName(masterID)
	if cluster.master == nil {
		return errors.New("No master found after switchover during rolling upgrade")
	}
	if master == nil || cluster.master.DSN == master.DSN {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rolling upgrade: original master is the same as current master after switchover, skipping second switchover")
		return nil
	}
	if master.IsDown() {
		return errors.New("Rolling upgrade: original master is down after switchover")
	}
	maintenanceEnabled := !master.IsMaintenance
	if maintenanceEnabled {
		master.SwitchMaintenance()
	}

	// Phase 1: pull new image on the old master (now a replica after switchover).
	if cfgErr := cluster.UpdateDatabaseServiceConfig(master, true); cfgErr != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade: failed to push service config for %s: %s", master.URL, cfgErr)
	}
	err := cluster.StopDatabaseService(master)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade: old master stop failed %s %s", master.URL, err)
		if maintenanceEnabled {
			master.SwitchMaintenance()
		}
		return err
	}
	err = cluster.WaitDatabaseFailed(master)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade: old master does not transit failed %s %s", master.URL, err)
		if maintenanceEnabled {
			master.SwitchMaintenance()
		}
		return err
	}
	err = cluster.StartDatabaseWaitRejoin(master)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade: old master does not restart %s %s", master.URL, err)
		if maintenanceEnabled {
			master.SwitchMaintenance()
		}
		return err
	}
	master.WaitSyncToMaster(cluster.master)

	// Phase 2: strip image_pull_policy=always (see slave phase 2 comment above).
	if cfgErr := cluster.UpdateDatabaseServiceConfig(master, false); cfgErr != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade: failed to reset service config for %s: %s", master.URL, cfgErr)
	}
	err = cluster.StopDatabaseService(master)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade: old master stop failed %s %s (clean config)", master.URL, err)
		if maintenanceEnabled {
			master.SwitchMaintenance()
		}
		return err
	}
	err = cluster.WaitDatabaseFailed(master)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade: old master does not transit failed %s %s (clean config)", master.URL, err)
		if maintenanceEnabled {
			master.SwitchMaintenance()
		}
		return err
	}
	err = cluster.StartDatabaseWaitRejoin(master)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade: old master does not restart %s %s (clean config)", master.URL, err)
		if maintenanceEnabled {
			master.SwitchMaintenance()
		}
		return err
	}
	master.WaitSyncToMaster(cluster.master)
	if maintenanceEnabled {
		master.SwitchMaintenance()
	}
	cluster.SwitchOver()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rolling upgrade completed")
	return nil
}

func (cluster *Cluster) RollingJobsUpgrade() error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rolling jobs upgrade")
	var ts time.Time

	for _, s := range cluster.slaves {
		if s == nil || s.IsIgnored() {
			continue
		}

		ts = time.Now()
		s.SetWaitJobsUpgradeCookie()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Set jobs upgrade cookie on %s ", s.URL)

		// Wait for the server to clear the cookie
		for s.HasRollingJobsUpgradeCookie() {
			if time.Since(ts) > 5*time.Minute {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Timeout waiting for jobs upgrade on %s ", s.URL)
				return errors.New("Timeout waiting for jobs upgrade")
			}

			time.Sleep(2 * time.Second)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Jobs upgrade completed on %s ", s.URL)
	}

	for _, s := range cluster.GetStandaloneServers() {
		if s == nil || s.IsIgnored() {
			continue
		}

		ts = time.Now()
		s.SetWaitJobsUpgradeCookie()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Set jobs upgrade cookie on standalone %s ", s.URL)

		// Wait for the server to clear the cookie
		for s.HasRollingJobsUpgradeCookie() {
			if time.Since(ts) > 5*time.Minute {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Timeout waiting for jobs upgrade on standalone %s ", s.URL)
				return errors.New("Timeout waiting for jobs upgrade on standalone")
			}

			time.Sleep(2 * time.Second)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Jobs upgrade completed on standalone %s ", s.URL)
	}

	master := cluster.GetMaster()
	if master == nil || master.IsIgnored() {
		return nil
	}

	ts = time.Now()
	master.SetWaitJobsUpgradeCookie()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Set jobs upgrade cookie on master %s ", master.URL)

	// Wait for the server to clear the cookie
	for master.HasRollingJobsUpgradeCookie() {
		if time.Since(ts) > 5*time.Minute {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Timeout waiting for jobs upgrade on master %s ", master.URL)
			return errors.New("Timeout waiting for jobs upgrade on master")
		}

		time.Sleep(2 * time.Second)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Jobs upgrade completed on master %s ", master.URL)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rolling jobs upgrade completed")

	return nil
}
