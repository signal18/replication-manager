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

	// A rolling reprovision DESTROYS each replica's data (UnprovisionDatabaseService)
	// before recreating an empty service. The reseed that repopulates it is not done
	// by this function: it is delegated to the autoseed rejoin path
	// (srv.go RejoinMaster -> srv_rejoin.go ReseedMasterSST), which fires only when
	// Conf.Autorejoin AND Conf.Autoseed are on. So a reprov with autoseed off (the
	// default) would leave the replicas empty -- silent data loss (#1771). Guarantee
	// the reseed:
	//   - require autorejoin: srv.go:947 gates the rejoin trigger on it, and forcing
	//     it globally would change failover behaviour for every other server, so we
	//     refuse (before destroying anything) rather than pilot it;
	//   - pilot autoseed on for the duration and restore the operator's value after,
	//     so each reprovisioned replica is reseeded from the master.
	if !cluster.Conf.Autorejoin {
		return errors.New("rolling reprovision refused: autorejoin is disabled -- reprovisioned replicas would be destroyed without a reseed; enable autorejoin first")
	}
	savedAutoseed := cluster.Conf.Autoseed
	cluster.Conf.Autoseed = true
	defer func() { cluster.Conf.Autoseed = savedAutoseed }()

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
				if maintenanceEnabled {
					slave.SwitchMaintenance()
				}
				return err
			}
			err = cluster.WaitDatabaseFailed(slave)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling restart slave does not transit suspect %s %s", slave.URL, err)
				if maintenanceEnabled {
					slave.SwitchMaintenance()
				}
				return err
			}
			err = cluster.InitDatabaseService(slave)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling reprov %s", err)
				if maintenanceEnabled {
					slave.SwitchMaintenance()
				}
				return err
			}
			err = cluster.StartDatabaseWaitRejoin(slave)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling reprov %s", err)
				if maintenanceEnabled {
					slave.SwitchMaintenance()
				}
				return err
			}

			currentMaster := cluster.GetMaster()
			if currentMaster == nil {
				if maintenanceEnabled {
					slave.SwitchMaintenance()
				}
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
	if master == nil || cluster.master.DSN == master.DSN {
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
			if maintenanceEnabled {
				master.SwitchMaintenance()
			}
			return err
		}
		err = cluster.WaitDatabaseFailed(master)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling restart slave does not transit suspect %s %s", master.URL, err)
			if maintenanceEnabled {
				master.SwitchMaintenance()
			}
			return err
		}
		err = cluster.InitDatabaseService(master)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling reprov %s", err)
			if maintenanceEnabled {
				master.SwitchMaintenance()
			}
			return err
		}
		err = cluster.WaitDatabaseStart(master)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling reprov %s", err)
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

			// K8SRestartDatabaseServiceWaitRejoin (cluster_tst.go) is
			// lighter than the generic stop->wait failed->start dance
			// below. Not RestartDatabaseService/K8SForceRepullDatabaseService
			// either: this is a scheduled/bulk restart
			// (scheduler-rolling-restart), and silently re-asserting the
			// image-pull-policy setting on every scheduled restart would
			// be a surprising side effect.
			if cluster.GetOrchestrator() == config.ConstOrchestratorKubernetes {
				err := cluster.K8SRestartDatabaseServiceWaitRejoin(slave)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling restart slave does not restart %s %s", slave.URL, err)
					if maintenanceEnabled {
						slave.SwitchMaintenance()
					}
					return err
				}
			} else {
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
					if maintenanceEnabled {
						slave.SwitchMaintenance()
					}
					return err
				}
			}
		}
		currentMaster := cluster.GetMaster()
		if currentMaster == nil {
			if maintenanceEnabled {
				slave.SwitchMaintenance()
			}
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
	if master == nil || cluster.master.DSN == master.DSN {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling original master is the same after switchover")
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
	// See the matching Kubernetes branch in the slave loop above for why
	// this uses the lighter K8SRestartDatabaseServiceWaitRejoin instead of
	// the generic stop->wait failed->start dance (or RestartDatabaseService).
	if cluster.GetOrchestrator() == config.ConstOrchestratorKubernetes {
		err := cluster.K8SRestartDatabaseServiceWaitRejoin(master)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling restart old master does not restart %s %s", master.URL, err)
			if maintenanceEnabled {
				master.SwitchMaintenance()
			}
			return err
		}
	} else {
		err := cluster.StopDatabaseService(master)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling restart old master stop failed %s %s", master.URL, err)
			if maintenanceEnabled {
				master.SwitchMaintenance()
			}
			return err
		}
		err = cluster.WaitDatabaseFailed(master)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling restart old master does not transit suspect %s %s", master.URL, err)
			if maintenanceEnabled {
				master.SwitchMaintenance()
			}
			return err
		}
		err = cluster.StartDatabaseWaitRejoin(master)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cancel rolling restart old master does not restart %s %s", master.URL, err)
			if maintenanceEnabled {
				master.SwitchMaintenance()
			}
			return err
		}
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
		if s == nil || s.IsIgnored() || s.IsDown() {
			continue
		}
		if cluster.Conf.OptimizeUseSQL {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting SQL optimize on %s", s.URL)
			if err := s.OptimizeSQL(); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "SQL optimize failed on %s: %s", s.URL, err)
			}
		} else {
			jobid, _ := s.JobOptimize()
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Optimize job id %d on %s ", jobid, s.URL)
		}
	}
}

// rollingUpgradeStopUpdateStart stops a server, updates its database service
// config (image + pull policy) for forcePull, and starts it back up -- one
// stop/config/start step of RollingUpgrade. clean selects
// StopDatabaseServiceClean (innodb_fast_shutdown=0) over a plain stop; phase
// labels the step for logging ("pull" vs "clean").
//
// OpenSVC and Kubernetes need opposite ordering here. OpenSVC's service
// config is inert until the container's next start, so updating before stop
// is safe and the image is pulled during that stop→start cycle. Kubernetes
// instead applies a Deployment patch that the controller can act on right
// away: updating while the pod is still live would race that controller's
// own rollout against this function's explicit stop, so for Kubernetes the
// update must happen only once the Deployment is already scaled to 0 (see
// K8SUpdateDatabaseServiceConfig, cluster/prov_k8s_db.go).
//
// On Kubernetes, whether a failed config update is fatal depends on the
// phase. forcePull=true (the "pull" phase) is the actual image change: a
// failed patch there is fatal, since the coming start step would otherwise
// silently bring the server back up on the unchanged image while
// RollingUpgrade reports success. forcePull=false (the "clean" phase) only
// restores the steady-state pull policy on a server already upgraded by the
// preceding pull phase -- failing that patch is cleanup drift, not an
// upgrade failure, so it's logged as a warning and the server is started
// anyway rather than left down (temporarily still forced to PullAlways).
// OpenSVC keeps its pre-existing best-effort behavior in both phases (log
// and continue) -- its push happens before the stop, so a failure there
// just means "still running the old image" on a step that was always
// best-effort.
func (cluster *Cluster) rollingUpgradeStopUpdateStart(server *ServerMonitor, forcePull bool, clean bool, phase string) error {
	stop := cluster.StopDatabaseService
	if clean {
		stop = cluster.StopDatabaseServiceClean
	}
	isKubernetes := cluster.GetOrchestrator() == config.ConstOrchestratorKubernetes
	updateConfig := func() error {
		cfgErr := cluster.UpdateDatabaseServiceConfig(server, forcePull)
		if cfgErr == nil {
			return nil
		}
		if isKubernetes && forcePull {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade (%s): failed to update service config for %s: %s", phase, server.URL, cfgErr)
			return cfgErr
		}
		if isKubernetes {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Rolling upgrade (%s): cleanup incomplete on %s, Deployment still forced to PullAlways: %s", phase, server.URL, cfgErr)
			return nil
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade (%s): failed to update service config for %s: %s", phase, server.URL, cfgErr)
		return nil
	}

	if !isKubernetes {
		updateConfig()
	}
	if err := stop(server); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade (%s): stop failed on %s: %s", phase, server.URL, err)
		return err
	}
	if err := cluster.WaitDatabaseFailed(server); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade (%s): %s does not transit failed: %s", phase, server.URL, err)
		return err
	}
	if isKubernetes {
		if err := updateConfig(); err != nil {
			return err
		}
	}
	if err := cluster.StartDatabaseWaitRejoin(server); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rolling upgrade (%s): %s does not restart: %s", phase, server.URL, err)
		return err
	}
	return nil
}

func (cluster *Cluster) RollingUpgrade() error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rolling upgrade")

	// On-premise upgrades use a dedicated upgrade script (installs new packages +
	// runs mariadb-upgrade) instead of the OpenSVC/Kubernetes image-pull two-phase approach.
	if cluster.GetOrchestrator() == config.ConstOrchestratorOnPremise {
		return cluster.rollingUpgradeOnPremise()
	}

	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found for rolling upgrade")
	}
	masterID := master.Id

	// Loop 1 — pull: force PullAlways (K8s) / image_pull_policy=always (OpenSVC)
	// and restart every slave so the orchestrator re-pulls the new image. Maintenance
	// is toggled per node and cleared after sync, so nodes are only in maintenance
	// during their own stop/start cycle.
	for _, slave := range cluster.slaves {
		if slave == nil || slave.IsIgnored() || slave.IsDown() {
			continue
		}
		maintEnabled := !slave.IsMaintenance
		if maintEnabled {
			slave.SwitchMaintenance()
		}
		if err := cluster.rollingUpgradeStopUpdateStart(slave, true, true, "pull"); err != nil {
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

	// Loop 2 — clean: restore the steady-state pull policy and restart every
	// slave so the forced pull-always setting is no longer live. The image is
	// already local, so this cycle costs only container restart time.
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
		if err := cluster.rollingUpgradeStopUpdateStart(slave, false, false, "clean"); err != nil {
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
	if err := cluster.rollingUpgradeStopUpdateStart(master, true, true, "pull"); err != nil {
		if maintenanceEnabled {
			master.SwitchMaintenance()
		}
		return err
	}
	master.WaitSyncToMaster(cluster.master)

	// Phase 2: restore the steady-state pull policy (see slave phase 2 comment above).
	if err := cluster.rollingUpgradeStopUpdateStart(master, false, false, "clean"); err != nil {
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

// rollingUpgradeOnPremise performs a rolling version upgrade for on-premise (SSH) deployments.
// Each node is: maintenance on → stop → run upgrade script (package upgrade + mariadb-upgrade + start) → wait sync → maintenance off.
func (cluster *Cluster) rollingUpgradeOnPremise() error {
	cluster.SetInRollingRestart(true)
	defer cluster.SetInRollingRestart(false)

	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found for on-premise rolling upgrade")
	}
	masterID := master.Id
	saveFailoverMode := cluster.Conf.FailSync
	cluster.SetFailSync(false)
	defer cluster.SetFailSync(saveFailoverMode)

	for _, slave := range cluster.slaves {
		if slave == nil || slave.IsIgnored() || slave.IsDown() {
			continue
		}
		maintEnabled := !slave.IsMaintenance
		if maintEnabled {
			slave.SwitchMaintenance()
		}
		writeOnce := true
		for slave.IsBackingUpBinaryLog {
			if writeOnce {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
					"Waiting slave %s to finish binlog backup", slave.URL)
				writeOnce = false
			}
			time.Sleep(time.Second)
		}

		err := cluster.StopDatabaseServiceClean(slave)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"Rolling upgrade: clean stop failed on slave %s: %s", slave.URL, err)
			if maintEnabled {
				slave.SwitchMaintenance()
			}
			return err
		}
		err = cluster.WaitDatabaseFailed(slave)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"Rolling upgrade: slave does not transit failed %s: %s", slave.URL, err)
			if maintEnabled {
				slave.SwitchMaintenance()
			}
			return err
		}

		err = cluster.UpgradeDatabaseService(slave)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"Rolling upgrade: upgrade failed on slave %s: %s", slave.URL, err)
			if maintEnabled {
				slave.SwitchMaintenance()
			}
			return err
		}

		err = cluster.WaitDatabaseStart(slave)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"Rolling upgrade: slave does not come back %s: %s", slave.URL, err)
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
			return errors.New("No master found for sync during on-premise rolling upgrade")
		}
		slave.WaitSyncToMaster(currentMaster)
		if maintEnabled {
			slave.SwitchMaintenance()
		}
	}

	cluster.SwitchoverWaitTest()
	master = cluster.GetServerFromName(masterID)
	currentMaster := cluster.GetMaster()
	if currentMaster == nil {
		return errors.New("No master found after switchover during on-premise rolling upgrade")
	}
	if master == nil || currentMaster.DSN == master.DSN {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"On-premise rolling upgrade: original master is the same after switchover, skipping")
		return nil
	}
	if master.IsDown() {
		return errors.New("On-premise rolling upgrade: original master is down after switchover")
	}

	maintEnabled := !master.IsMaintenance
	if maintEnabled {
		master.SwitchMaintenance()
	}
	writeOnce := true
	for master.IsBackingUpBinaryLog {
		if writeOnce {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"Waiting master %s to finish binlog backup", master.URL)
			writeOnce = false
		}
		time.Sleep(time.Second)
	}

	err := cluster.StopDatabaseServiceClean(master)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Rolling upgrade: old master clean stop failed %s: %s", master.URL, err)
		if maintEnabled {
			master.SwitchMaintenance()
		}
		return err
	}
	err = cluster.WaitDatabaseFailed(master)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Rolling upgrade: old master does not transit failed %s: %s", master.URL, err)
		if maintEnabled {
			master.SwitchMaintenance()
		}
		return err
	}

	err = cluster.UpgradeDatabaseService(master)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Rolling upgrade: upgrade failed on old master %s: %s", master.URL, err)
		if maintEnabled {
			master.SwitchMaintenance()
		}
		return err
	}

	err = cluster.WaitDatabaseStart(master)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Rolling upgrade: old master does not come back %s: %s", master.URL, err)
		if maintEnabled {
			master.SwitchMaintenance()
		}
		return err
	}
	master.WaitSyncToMaster(currentMaster)
	if maintEnabled {
		master.SwitchMaintenance()
	}
	cluster.SwitchOver()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"On-premise rolling upgrade completed")
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
