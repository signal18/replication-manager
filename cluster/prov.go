// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
	"k8s.io/client-go/kubernetes"
)

// Constants for restart RID validation
const (
	RestartRidJobsContainer = "container#jobs"
)

// GetProvCoresInt returns prov-cores as a whole number of cores. prov-cores
// is a float elsewhere (DBU fractions like "0.5" are valid), so fractional
// values round up to their ceiling; unparseable or non-positive values fall
// back to 2.
func (cluster *Cluster) GetProvCoresInt() int {
	cores, err := strconv.ParseFloat(cluster.Conf.ProvCores, 64)
	if err != nil || cores <= 0 {
		return 2
	}
	return int(math.Ceil(cores))
}

// GetDBAllocatorEnv returns the allocator tuning exported to every provisioned
// database container, whatever the orchestrator (#1749). MALLOC_ARENA_MAX
// derives from prov-cores because arena count scales with the parallelism the
// cgroup can actually run, not with memory or connection count; an empty
// preload disables the feature.
func (cluster *Cluster) GetDBAllocatorEnv() (preload string, arenaMax string) {
	preload = cluster.Conf.ProvDBDockerJemallocPreload
	if preload == "" {
		return "", ""
	}
	return preload, strconv.Itoa(cluster.GetProvCoresInt())
}

// validateRestartRid validates the resource ID parameter for database restart operations.
// Only container#jobs is allowed for targeted restarts.
func validateRestartRid(rid string) error {
	if rid != "" && rid != RestartRidJobsContainer {
		return fmt.Errorf("invalid rid '%s': only '%s' is allowed for restart", rid, RestartRidJobsContainer)
	}
	return nil
}

// ValidateAppRestartRid validates the resource ID parameter for app restart operations.
// Empty string restarts the entire service; any container#* value targets a specific container.
func ValidateAppRestartRid(rid string) error {
	if rid != "" && !strings.HasPrefix(rid, "container#") {
		return fmt.Errorf("invalid rid '%s': must be empty or start with 'container#'", rid)
	}
	return nil
}

// Bootstrap provisions && setup topology
func (cluster *Cluster) Bootstrap() error {
	var err error
	// create service template and post
	err = cluster.ProvisionServices()
	if err != nil {
		return err
	}

	err = cluster.BootstrapReplication(true, false)
	if err != nil {
		return err
	}

	if cluster.GetSponsorEmail() != "" {
		err = cluster.CreateDBUserFromConfig("sponsor")
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cannot create sponsor user: %s", err)
		}
	}

	if cluster.Conf.Test {
		//cluster.initProxies()
		err = cluster.WaitProxyEqualMaster()
		if err != nil {
			return err
		}
		err = cluster.WaitBootstrapDiscovery()
		if err != nil {
			return err
		}

		if cluster.GetMaster() == nil {
			return errors.New("Abording test, no master found")
		}
		err = cluster.InitBenchTable()
		if err != nil {
			return errors.New("Abording test, can't create bench table")
		}
		if cluster.GetOrchestrator() == config.ConstOrchestratorOpenSVC {
			for _, server := range cluster.Servers {
				server.JobsCreateTable()
			}
		}
	}
	return nil
}

func (cluster *Cluster) ProvisionServices() error {
	// Serialise against every other provision/unprovision op on this cluster:
	// they all report through the shared errorChan and would otherwise
	// cross-talk (issue #1769). The fan-out below still launches its per-server
	// goroutines in parallel under this single hold, so bulk provisioning
	// (volume creation, etc.) keeps its concurrency.
	cluster.provisioningMutex.Lock()
	defer cluster.provisioningMutex.Unlock()
	hasConfigPath := make(map[string]bool)
	cluster.StateMachine.SetFailoverState()
	// delete the cluster state here
	path := cluster.WorkingDir + ".json"
	os.Remove(path)
	cluster.ResetCrashes()
	for _, server := range cluster.Servers {
		hasConfigPath[server.HostCnf] = server.HasConfigPathCookie()
		server.DelConfigPathCookie() // remove the config path cookie since we are going to provision it
		server.WipeDeltaConfig()
		switch cluster.GetOrchestrator() {
		case config.ConstOrchestratorOpenSVC:
			go cluster.OpenSVCProvisionDatabaseService(server)
		case config.ConstOrchestratorKubernetes:
			go cluster.K8SProvisionDatabaseService(server)
		case config.ConstOrchestratorSlapOS:
			go cluster.SlapOSProvisionDatabaseService(server)
		case config.ConstOrchestratorLocalhost:
			go cluster.LocalhostProvisionDatabaseService(server)
		case config.ConstOrchestratorOnPremise:
			go cluster.OnPremiseProvisionDatabaseService(server)

		default:

		}
		cluster.ProvisionDatabaseScript(server)
		if cluster.GetConf().ProvSerialized {
			server.WaitDatabaseStart()
		}

	}

	for _, server := range cluster.Servers {
		err := <-cluster.errorChan
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Provisionning error %s on  %s", err, cluster.Name+"/svc/"+server.Name)

			if hasConfigPath[server.HostCnf] {
				server.SetConfigPathCookie() // revert the config path cookie since error occured
			}
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Provisionning done for database %s", cluster.Name+"/svc/"+server.Name)
			server.SetProvisionCookie()
			server.DelReprovisionCookie()
			server.DelRestartCookie()
		}
	}
	err := cluster.WaitDatabaseCanConn()
	if err != nil {
		return err
	}
	for _, prx := range cluster.Proxies {
		switch cluster.proxyServiceOrchestrator(prx) {
		case config.ConstOrchestratorOpenSVC:
			go cluster.OpenSVCProvisionProxyService(prx)
		case config.ConstOrchestratorKubernetes:
			go cluster.K8SProvisionProxyService(prx)
		case config.ConstOrchestratorSlapOS:
			go cluster.SlapOSProvisionProxyService(prx)
		case config.ConstOrchestratorLocalhost:
			go cluster.LocalhostProvisionProxyService(prx)
		case config.ConstOrchestratorOnPremise:
			go cluster.OnPremiseProvisionProxyService(prx)
		default:

		}
		cluster.ProvisionProxyScript(prx)
	}
	for _, pri := range cluster.Proxies {
		prx, ok := pri.(*Proxy)
		if !ok {
			continue
		}
		err := <-cluster.errorChan
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Provisionning proxy error %s on  %s", err, cluster.Name+"/svc/"+prx.GetName())
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Provisionning done for proxy %s", cluster.Name+"/svc/"+prx.GetName())
			prx.SetProvisionCookie()
		}
	}

	cluster.StateMachine.RemoveFailoverState()

	return nil

}

func (cluster *Cluster) InitDatabaseService(server *ServerMonitor) error {
	// Serialise errorChan use against other provision/unprovision ops (#1769).
	cluster.provisioningMutex.Lock()
	defer cluster.provisioningMutex.Unlock()
	cluster.StateMachine.SetFailoverState()
	server.WipeDeltaConfig()
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		go cluster.OpenSVCProvisionDatabaseService(server)
	case config.ConstOrchestratorKubernetes:
		go cluster.K8SProvisionDatabaseService(server)
	case config.ConstOrchestratorSlapOS:
		go cluster.SlapOSProvisionDatabaseService(server)
	case config.ConstOrchestratorLocalhost:
		go cluster.LocalhostProvisionDatabaseService(server)
	case config.ConstOrchestratorOnPremise:
		go cluster.OnPremiseProvisionDatabaseService(server)
	default:
		cluster.StateMachine.RemoveFailoverState()
		return nil
	}
	cluster.ProvisionDatabaseScript(server)
	err := <-cluster.errorChan
	cluster.StateMachine.RemoveFailoverState()
	if err == nil {
		server.SetProvisionCookie()
	} else {
		return err
	}

	return nil
}

// proxyServiceOrchestrator returns config.ConstOrchestratorLocalhost when prx
// is HAProxy running haproxy-mode=standby, and cluster.GetOrchestrator()
// otherwise. Databases may be provisioned under any orchestrator, but
// standby always runs a repman-local HAProxy instance started/reloaded via
// its own local PID (HaproxyProxy.Init(), cluster/prx_haproxy.go) -- there's
// no remote equivalent, so the proxy-service dispatch switches below must
// route standby to the Localhost* implementations regardless of where the
// cluster's databases actually live. Only used for proxy-service dispatch;
// database dispatch is unaffected and keeps calling cluster.GetOrchestrator()
// directly.
func (cluster *Cluster) proxyServiceOrchestrator(prx DatabaseProxy) string {
	if prx.GetType() == config.ConstProxyHaproxy && cluster.Conf.HaproxyMode == "standby" {
		return config.ConstOrchestratorLocalhost
	}
	return cluster.GetOrchestrator()
}

func (cluster *Cluster) InitProxyService(prx DatabaseProxy) error {
	// Serialise errorChan use against other provision/unprovision ops (#1769).
	cluster.provisioningMutex.Lock()
	defer cluster.provisioningMutex.Unlock()
	switch cluster.proxyServiceOrchestrator(prx) {
	case config.ConstOrchestratorOpenSVC:
		go cluster.OpenSVCProvisionProxyService(prx)
	case config.ConstOrchestratorKubernetes:
		go cluster.K8SProvisionProxyService(prx)
	case config.ConstOrchestratorSlapOS:
		go cluster.SlapOSProvisionProxyService(prx)
	case config.ConstOrchestratorLocalhost:
		go cluster.LocalhostProvisionProxyService(prx)
	case config.ConstOrchestratorOnPremise:
		go cluster.OnPremiseProvisionProxyService(prx)
	default:
		return nil
	}
	cluster.ProvisionProxyScript(prx)
	err := <-cluster.errorChan
	cluster.StateMachine.RemoveFailoverState()
	if err == nil {
		prx.SetProvisionCookie()
		// Snapshot the live bootstrap-servers setting onto this proxy --
		// see HaproxyProxy.BootstrapServersEnabled.
		if hprx, ok := prx.(*HaproxyProxy); ok {
			hprx.setProvisionedBootstrapServers(cluster.Conf.HaproxyAPIBootstrapServers)
		}
	} else {
		return err
	}
	return nil
}

func (cluster *Cluster) InitAppService(app *App) error {
	// Serialise errorChan use against other provision/unprovision ops (#1769).
	cluster.provisioningMutex.Lock()
	defer cluster.provisioningMutex.Unlock()
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		go cluster.OpenSVCProvisionAppService(app)
	default:
		return nil
	}
	// cluster.ProvisionAppScript(app)
	err := <-cluster.errorChan
	cluster.StateMachine.RemoveFailoverState()
	if err == nil {
		app.DelUnprovisionCookie()
		app.SetProvisionCookie()
	} else {
		return err
	}
	return nil
}

func (cluster *Cluster) Unprovision() error {
	// Serialise against every other provision/unprovision op on this cluster
	// (#1769). The two fan-out loops below (proxies, then databases) still run
	// their per-entity goroutines in parallel under this single hold.
	cluster.provisioningMutex.Lock()
	defer cluster.provisioningMutex.Unlock()

	cluster.StateMachine.SetFailoverState()
	// Unprovision proxies first, since they are dependent on databases
	for _, prx := range cluster.Proxies {
		/*	prx, ok := pri.(*Proxy)
			if !ok {
				continue
			}*/
		switch cluster.proxyServiceOrchestrator(prx) {
		case config.ConstOrchestratorOpenSVC:
			go cluster.OpenSVCUnprovisionProxyService(prx)
		case config.ConstOrchestratorKubernetes:
			go cluster.K8SUnprovisionProxyService(prx)
		case config.ConstOrchestratorSlapOS:
			go cluster.SlapOSUnprovisionProxyService(prx)
		case config.ConstOrchestratorLocalhost:
			go cluster.LocalhostUnprovisionProxyService(prx)
		case config.ConstOrchestratorOnPremise:
			go cluster.OnPremiseUnprovisionProxyService(prx)
		default:

		}
		cluster.UnprovisionProxyScript(prx)
	}
	for _, prx := range cluster.Proxies {
		/*	prx, ok := pri.(*Proxy)
			if !ok {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,config.LvlErr, "Unprovision proxy continue ")
				continue
			}*/
		err := <-cluster.errorChan
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Unprovision proxy error %s on  %s", err, cluster.Name+"/svc/"+prx.GetName())
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Unprovision done for proxy %s", cluster.Name+"/svc/"+prx.GetName())
			cluster.ForgetProxyInstance(prx) // clean slate: datadir (cookies included)
			if hprx, ok := prx.(*HaproxyProxy); ok {
				hprx.delProvisionedBootstrapServers()
			}
		}
	}

	for _, server := range cluster.Servers {
		switch cluster.GetOrchestrator() {
		case config.ConstOrchestratorOpenSVC:
			go cluster.OpenSVCUnprovisionDatabaseService(server)
		case config.ConstOrchestratorKubernetes:
			go cluster.K8SUnprovisionDatabaseService(server)
		case config.ConstOrchestratorSlapOS:
			go cluster.SlapOSUnprovisionDatabaseService(server)
		case config.ConstOrchestratorLocalhost:
			go cluster.LocalhostUnprovisionDatabaseService(server)
		case config.ConstOrchestratorOnPremise:
			go cluster.OnPremiseUnprovisionDatabaseService(server)
		default:
		}
		cluster.UnprovisionDatabaseScript(server)
	}
	for _, server := range cluster.Servers {
		err := <-cluster.errorChan
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Unprovision error %s on  %s", err, cluster.Name+"/svc/"+server.Name)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Unprovision done for database %s", cluster.Name+"/svc/"+server.Name)
			cluster.ForgetInstance(server) // clean slate: datadir, crash events, tracked state (cookies included)
		}
	}
	err := cluster.WaitClusterStop()
	if err == nil {
		cluster.ResetStates()
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to wait for all databases down : %s", err)
	}
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		cluster.OpenSVCUnprovisionSecret()
	default:
	}

	return nil
}

func (cluster *Cluster) UnprovisionProxyService(prx DatabaseProxy) error {
	// Serialise errorChan use against other provision/unprovision ops (#1769).
	cluster.provisioningMutex.Lock()
	defer cluster.provisioningMutex.Unlock()
	switch cluster.proxyServiceOrchestrator(prx) {
	case config.ConstOrchestratorOpenSVC:
		go cluster.OpenSVCUnprovisionProxyService(prx)
	case config.ConstOrchestratorKubernetes:
		go cluster.K8SUnprovisionProxyService(prx)
	case config.ConstOrchestratorSlapOS:
		go cluster.SlapOSUnprovisionProxyService(prx)
	case config.ConstOrchestratorLocalhost:
		go cluster.LocalhostUnprovisionProxyService(prx)
	case config.ConstOrchestratorOnPremise:
		go cluster.OnPremiseUnprovisionProxyService(prx)
	default:
	}
	cluster.UnprovisionProxyScript(prx)
	err := <-cluster.errorChan
	if err == nil {
		cluster.ForgetProxyInstance(prx) // clean slate: datadir (cookies included)
		if hprx, ok := prx.(*HaproxyProxy); ok {
			hprx.delProvisionedBootstrapServers()
		}
	}
	return err
}

func (cluster *Cluster) UnprovisionDatabaseService(server *ServerMonitor) error {
	// Serialise errorChan use against other provision/unprovision ops (#1769).
	// This is the path that hung in the reported incident: a config-building
	// send racing this unprovision's <-errorChan left the server in maintenance.
	cluster.provisioningMutex.Lock()
	defer cluster.provisioningMutex.Unlock()
	cluster.ResetCrashes()
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		go cluster.OpenSVCUnprovisionDatabaseService(server)
	case config.ConstOrchestratorKubernetes:
		go cluster.K8SUnprovisionDatabaseService(server)
	case config.ConstOrchestratorSlapOS:
		go cluster.SlapOSUnprovisionDatabaseService(server)
	case config.ConstOrchestratorOnPremise:
		go cluster.OnPremiseUnprovisionDatabaseService(server)
	default:
		go cluster.LocalhostUnprovisionDatabaseService(server)
	}
	cluster.UnprovisionDatabaseScript(server)
	err := <-cluster.errorChan
	if err == nil {
		cluster.ForgetInstance(server) // clean slate: datadir, crash events, tracked state (cookies included)
	} else {
		return err
	}
	return nil
}

func (cluster *Cluster) UpdateDatabaseServiceConfig(server *ServerMonitor, forcePull bool) error {
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		return cluster.OpenSVCUpdateDatabaseServiceConfig(server, forcePull)
	case config.ConstOrchestratorKubernetes:
		return cluster.K8SUpdateDatabaseServiceConfig(server, forcePull)
	default:
		return nil
	}
}

func (cluster *Cluster) UpgradeDatabaseService(server *ServerMonitor) error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Upgrading database service %s", cluster.Name+"/svc/"+server.URL)
	var err error
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOnPremise:
		err = cluster.OnPremiseUpgradeDatabaseService(server)
	default:
		// Same two-phase pull/clean cycle RollingUpgrade (cluster_roll.go) runs
		// per node, applied here to a single server via the shared
		// rollingUpgradeStopUpdateStart helper: phase 1 pushes the currently
		// configured image (forcePull, so a mutable/already-cached tag is
		// still re-pulled) and restarts on it via a clean shutdown (safe for a
		// major-version upgrade); phase 2 restores the steady-state pull
		// policy. Previously this branch just called StartDatabaseService with
		// no config update at all -- a restart on the unchanged image,
		// silently upgrading nothing on OpenSVC/Kubernetes.
		if err = cluster.rollingUpgradeStopUpdateStart(server, true, true, "pull"); err != nil {
			return err
		}
		err = cluster.rollingUpgradeStopUpdateStart(server, false, false, "clean")
	}
	if err == nil {
		server.SetConfigRefreshCookie()
	}
	return err
}

// UpgradeDatabaseDeploymentOnStart re-renders the FULL deployment (the orchestrated
// service definition: image, resources/cgroup cap, run_args, env) and pushes it to the
// orchestrator, so a container/pod recreated by a rolling restart/upgrade comes up on the
// CURRENT config instead of the one written at the last provision. This is what makes a
// resource-cap change (and an unpinned image tag) actually land on restart.
//
// Gated by prov-orchestrator-deployment-upgrade-on-start (default on). Returns nil (no-op)
// when off, or when the orchestrator/API has no full-deployment push (OpenSVC v2 legacy).
// Called SYNCHRONOUSLY from the rolling loop and returns its error directly -- it must NOT
// go through cluster.errorChan (per-op cross-talk, issue #1769).
//
// keepImage (the rolling RESTART): the service keeps the image it runs, whatever
// prov-db-image says -- a restart never changes the database version, only the rolling
// upgrade does (#1861: curepipe 2026-10-01, prov-db-image "latest" re-rendered on a
// restart put two replicas on a stale local 11.7.2 under an 11.8.8 master).
func (cluster *Cluster) UpgradeDatabaseDeploymentOnStart(server *ServerMonitor, keepImage bool) error {
	if !cluster.Conf.ProvOrchestratorDeploymentUpgradeOnStart {
		return nil
	}
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		// Full re-render + push exists only on the v3 API; the v2 legacy path keeps a
		// restart deployment-neutral rather than failing it.
		svc := cluster.OpenSVCConnect()
		if !svc.IsV3() {
			return nil
		}
		if keepImage {
			if img := cluster.openSVCCurrentDatabaseImage(server); img != "" && img != cluster.Conf.ProvDbImg {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
					"Restart keeps the image %s runs (%s), prov-db-image %s is for the rolling upgrade", server.URL, img, cluster.Conf.ProvDbImg)
				server.DeployImageOverride = img
				defer func() { server.DeployImageOverride = "" }()
			}
		}
		return cluster.OpenSVCUpdateDatabaseTemplate(server)
	case config.ConstOrchestratorKubernetes:
		// K8s re-applies the Deployment pod template (image, pull policy) via the update
		// path already on develop (feat(k8s) rolling-upgrade image support). It requires the
		// Deployment scaled to 0 -- the rolling paths call the deployment upgrade from the
		// stopped phase, which satisfies that. The K8s container RESOURCE baseline and the
		// live in-place pod resize are owned by the k8sResizer (cluster_resize_k8s.go): that
		// stays a separate mechanism and is NOT re-implemented here.
		return cluster.k8sUpdateDatabaseServiceConfigKeepImage(server, keepImage)
	default:
		return nil
	}
}

// openSVCCurrentDatabaseImage reads env.docker_image from the service's current
// configuration on the orchestrator: the image the service runs today.
func (cluster *Cluster) openSVCCurrentDatabaseImage(server *ServerMonitor) string {
	svc := cluster.OpenSVCConnect()
	parts := strings.SplitN(server.ServiceName, "/", 3)
	if len(parts) != 3 {
		return ""
	}
	raw, err := svc.GetObjectConfigFileV3(parts[0], parts[1], parts[2])
	if err != nil {
		return ""
	}
	return openSVCConfigValue(string(raw), "env", "docker_image")
}

// openSVCConfigValue reads key under [section] of an om3 config file (INI, "key = value").
func openSVCConfigValue(raw, section, key string) string {
	in := false
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			in = line == "["+section+"]"
			continue
		}
		if !in || strings.HasPrefix(line, "#") {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) == 2 && strings.TrimSpace(kv[0]) == key {
			return strings.TrimSpace(kv[1])
		}
	}
	return ""
}

// StopDatabaseServiceClean stops the database with innodb_fast_shutdown=0 for
// safe version upgrades. For masters, it also issues SHUTDOWN WAIT FOR ALL SLAVES
// via SQL so replicas receive all pending events before the master goes down.
// The orchestrator stop always follows to ensure a clean service state transition
// and to stop all containers (db + jobs) so both get the new image on start.
func (cluster *Cluster) StopDatabaseServiceClean(server *ServerMonitor) error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Clean-stopping database service for upgrade %s", cluster.Name+"/svc/"+server.URL)

	if server.Conn != nil {
		// Set innodb_fast_shutdown=0 while the DB is still running.
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"Setting innodb_fast_shutdown=0 on %s for clean upgrade shutdown", server.URL)
		if _, err := server.Conn.Exec("SET GLOBAL innodb_fast_shutdown = 0"); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Failed to set innodb_fast_shutdown=0 on %s: %s (proceeding with stop)", server.URL, err)
		}

		// If this is a master (user upgrading a master directly, not via rolling),
		// wait for all slaves to receive pending binlog events before stopping.
		if server.IsMaster() && server.DBVersion.IsMariaDB() && server.DBVersion.Major >= 10 && server.DBVersion.Minor >= 4 {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"Master %s: issuing SHUTDOWN WAIT FOR ALL SLAVES", server.URL)
			_, err := server.Conn.Exec("SHUTDOWN WAIT FOR ALL SLAVES")
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
					"SHUTDOWN WAIT FOR ALL SLAVES failed on %s: %s (falling back to orchestrator stop)", server.URL, err)
			}
		}
	}

	// The orchestrator stop follows unconditionally. For container orchestrators
	// this is required to stop ALL containers (db + jobs). For on-premise, the SQL
	// SHUTDOWN may have already killed the process — the redundant stop is harmless
	// (Shutdown() on a dead connection returns nil).
	return cluster.StopDatabaseService(server)
}

func (cluster *Cluster) StopDatabaseService(server *ServerMonitor) error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Stopping database service %s", cluster.Name+"/svc/"+server.URL)
	var err error

	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		err = cluster.OpenSVCStopDatabaseService(server)
	case config.ConstOrchestratorKubernetes:
		err = cluster.K8SStopDatabaseService(server)
	case config.ConstOrchestratorSlapOS:
		err = cluster.SlapOSStopDatabaseService(server)
	case config.ConstOrchestratorOnPremise:
		err = cluster.OnPremiseStopDatabaseService(server)
	case config.ConstOrchestratorLocalhost:
		err = cluster.OnPremiseStopDatabaseService(server)
	default:
		return errors.New("No valid orchestrator")
	}
	cluster.StopDatabaseScript(server)
	if err == nil {
		server.DelRestartCookie()
	}
	return err
}

func (cluster *Cluster) StopProxyService(server DatabaseProxy) error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Stopping Proxy service %s", cluster.Name+"/svc/"+server.GetName())
	var err error

	switch cluster.proxyServiceOrchestrator(server) {
	case config.ConstOrchestratorOpenSVC:
		err = cluster.OpenSVCStopProxyService(server)
	case config.ConstOrchestratorKubernetes:
		err = cluster.K8SStopProxyService(server)
	case config.ConstOrchestratorSlapOS:
		err = cluster.SlapOSStopProxyService(server)
	case config.ConstOrchestratorOnPremise:
		err = cluster.OnPremiseStopProxyService(server)
	case config.ConstOrchestratorLocalhost:
		err = cluster.LocalhostStopProxyService(server)
	default:
		return errors.New("No valid orchestrator")
	}
	cluster.StopProxyScript(server)
	if err == nil {
		server.DelRestartCookie()
	}
	return err
}

func (cluster *Cluster) StartProxyService(server DatabaseProxy) error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting Proxy service %s", cluster.Name+"/svc/"+server.GetName())
	var err error
	switch cluster.proxyServiceOrchestrator(server) {
	case config.ConstOrchestratorOpenSVC:
		err = cluster.OpenSVCStartProxyService(server)
	case config.ConstOrchestratorKubernetes:
		err = cluster.K8SStartProxyService(server)
	case config.ConstOrchestratorSlapOS:
		err = cluster.SlapOSStartProxyService(server)
	case config.ConstOrchestratorOnPremise:
		err = cluster.OnPremiseStartProxyService(server)
	case config.ConstOrchestratorLocalhost:
		err = cluster.LocalhostStartProxyService(server)
	default:
		return errors.New("No valid orchestrator")
	}
	cluster.StartProxyScript(server)
	if err == nil {
		server.DelRestartCookie()
		if startReappliesProxyConfig(server, cluster.proxyServiceOrchestrator(server)) {
			server.DelReprovisionCookie()
		}
	}
	return err
}

// startReappliesProxyConfig reports whether a successful start on this
// orchestrator actually reapplies the proxy's current config, so it
// satisfies whatever set the reprov cookie -- NOT true for every start path:
//   - Localhost always regenerates+applies unconditionally
//     (GetProxyConfig+Init(), see LocalhostStart{HaProxy,ProxySQL}Service).
//   - OpenSVC/Kubernetes "start" re-triggers the container's own
//     init/entrypoint config fetch, but only when
//     prov-proxy-start-fetch-config is actually enabled for this proxy
//     (mirrors CheckNeedConfigFetch's condition).
//   - OnPremise (plain "systemctl start ...") and SlapOS (a no-op beyond
//     SetWaitStartCookie) never reapply config on start, regardless of
//     prov-proxy-start-fetch-config.
func startReappliesProxyConfig(server DatabaseProxy, orchestrator string) bool {
	switch orchestrator {
	case config.ConstOrchestratorLocalhost:
		return true
	case config.ConstOrchestratorOpenSVC, config.ConstOrchestratorKubernetes:
		return !server.HasNoConfigFetchCookie()
	default:
		return false
	}
}

func (cluster *Cluster) ShutdownDatabase(server *ServerMonitor) error {
	_, err := server.Conn.Exec("SHUTDOWN")
	server.DelRestartCookie()
	return err
}

func (cluster *Cluster) StartDatabaseService(server *ServerMonitor) error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting Database service %s", cluster.Name+"/svc/"+server.Name)
	var err error
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		err = cluster.OpenSVCStartDatabaseService(server)
	case config.ConstOrchestratorKubernetes:
		err = cluster.K8SStartDatabaseService(server)
	case config.ConstOrchestratorSlapOS:
		err = cluster.SlapOSStartDatabaseService(server)
	case config.ConstOrchestratorOnPremise:
		err = cluster.OnPremiseStartDatabaseService(server)
	case config.ConstOrchestratorLocalhost:
		err = cluster.LocalhostStartDatabaseService(server)
	default:
		return errors.New("No valid orchestrator")
	}
	cluster.StartDatabaseScript(server)
	if err == nil {
		server.DelRestartCookie()
	}
	server.SetConfigRefreshCookie()
	return err
}

func (cluster *Cluster) RestartDatabaseService(server *ServerMonitor, node string, rid string) error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Restarting Database service %s", cluster.Name+"/svc/"+server.Name)
	var err error

	// OpenSVC supports atomic restart with optional RID targeting
	if cluster.GetOrchestrator() == config.ConstOrchestratorOpenSVC && rid != "" {
		err = cluster.OpenSVCRestartDatabaseService(server, node, rid)
		if err == nil {
			server.DelRestartContainerCookie()
			server.RestartNode = ""
			server.RestartRid = ""
		}
		return err
	}

	// A rolling pod replacement (the same mechanism that makes
	// prov-kube-image-force-pull's ImagePullPolicy: Always actually take
	// effect on demand) is lighter than a full stop/start cycle for a
	// plain restart.
	if cluster.GetOrchestrator() == config.ConstOrchestratorKubernetes {
		err = cluster.K8SForceRepullDatabaseService(server)
		if err == nil {
			server.DelRestartContainerCookie()
			server.RestartNode = ""
			server.RestartRid = ""
		}
		return err
	}

	// Generic restart: stop → wait failed → start
	// This allows pre-stop hooks (e.g. innodb_fast_shutdown=0 before upgrade)
	// and post-stop hooks (e.g. redo log relocation) between the two phases.
	err = cluster.StopDatabaseService(server)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Restart stop phase failed for %s: %s", server.URL, err)
		return err
	}
	err = cluster.WaitDatabaseFailed(server)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Restart wait-failed phase for %s: %s", server.URL, err)
		return err
	}
	err = cluster.StartDatabaseService(server)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Restart start phase failed for %s: %s", server.URL, err)
		return err
	}

	server.DelRestartContainerCookie()
	server.RestartNode = ""
	server.RestartRid = ""
	return nil
}

func (cluster *Cluster) StartAllNodes() error {

	return nil
}

// BootstrapReplicationCleanup resets replication on every server (RESET MASTER,
// stop slaves, clear GTID). ftwrl, when true, takes a short FLUSH TABLES WITH READ
// LOCK on the current master to freeze writes for a consistent cut before its RESET
// MASTER, then UNLOCKs — the unsafe bootstrap-repli-ftwrl rejoin path.
func (cluster *Cluster) BootstrapReplicationCleanup(ftwrl bool) error {

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cleaning up replication on existing servers")
	cluster.StateMachine.SetFailoverState()
	oldMaster := cluster.GetMaster()
	for _, server := range cluster.Servers {
		err := server.Refresh()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Refresh failed in Cleanup on server %s %s", server.URL, err)
			cluster.StateMachine.RemoveFailoverState()
			return err
		}
		if cluster.Conf.Verbose {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "SetDefaultMasterConn on server %s ", server.URL)
		}
		logs, err := dbhelper.SetDefaultMasterConn(server.Conn, cluster.Conf.MasterConn, server.DBVersion)
		cluster.LogSQL(logs, err, server.URL, "BootstrapReplicationCleanup", config.LvlDbg, "BootstrapReplicationCleanup %s %s ", server.URL, err)
		if err != nil {
			if cluster.Conf.Verbose {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "RemoveFailoverState on server %s ", server.URL)
			}
			continue
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Reset Master on server %s ", server.URL)

		// unsafe bootstrap-repli-ftwrl: freeze the master with the PROVEN dedicated-session
		// FreezeWithReadLock (not a hand-rolled pinned connection) for a consistent cut
		// before RESET MASTER, then release. Only the master, only on request.
		if ftwrl && oldMaster != nil && server.URL == oldMaster.URL {
			if ferr := server.FreezeWithReadLock(); ferr != nil {
				cluster.LogSQL("FLUSH TABLES WITH READ LOCK", ferr, server.URL, "BootstrapReplicationCleanup", config.LvlErr, "FTWRL freeze before reset master on %s failed: %s", server.URL, ferr)
			}
			logs, err = dbhelper.ResetMaster(server.Conn, cluster.Conf.MasterConn, server.DBVersion)
			cluster.LogSQL(logs, err, server.URL, "BootstrapReplicationCleanup", config.LvlErr, "Reset Master (FTWRL) on server %s %s", server.URL, err)
			server.UnfreezeReadLock()
		} else {
			logs, err = dbhelper.ResetMaster(server.Conn, cluster.Conf.MasterConn, server.DBVersion)
			cluster.LogSQL(logs, err, server.URL, "BootstrapReplicationCleanup", config.LvlErr, "Reset Master on server %s %s", server.URL, err)
		}
		if cluster.Conf.Verbose {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Stop all slaves or stop slave %s ", server.URL)
		}
		if server.DBVersion.IsMariaDB() {
			logs, err = dbhelper.StopAllSlaves(server.Conn, server.DBVersion)
		} else {
			logs, err = server.StopSlave()
		}
		cluster.LogSQL(logs, err, server.URL, "BootstrapReplicationCleanup", config.LvlErr, "Stop all slaves or just slave %s %s", server.URL, err)

		if server.DBVersion.IsMariaDB() {
			if cluster.Conf.Verbose {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "SET GLOBAL gtid_slave_pos='' on %s", server.URL)
			}
			logs, err := dbhelper.SetGTIDSlavePos(server.Conn, "")
			cluster.LogSQL(logs, err, server.URL, "BootstrapReplicationCleanup", config.LvlErr, "Can reset GTID slave pos %s %s", server.URL, err)
		}

		// reset all replication if go to master-slave
		if !cluster.Conf.MultiTierSlave && !cluster.Conf.MultiMaster && !cluster.Conf.MultiMasterRing && !cluster.Conf.MultiMasterGrouprep && !cluster.Conf.MultiMasterWsrep {
			server.ResetSlave()
		}

	}
	cluster.master = nil
	cluster.vmaster = nil
	cluster.slaves = nil
	cluster.StateMachine.RemoveFailoverState()
	return nil
}

func (cluster *Cluster) BootstrapReplication(clean bool, ftwrl bool) error {

	// default to master slave
	var err error
	oldMaster := cluster.GetMaster()

	if cluster.Conf.MultiMasterWsrep {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Galera cluster ignoring replication setup")
		return nil
	}
	if clean {
		err := cluster.BootstrapReplicationCleanup(ftwrl)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cleanup error %s", err)
		}
	}
	for _, server := range cluster.Servers {
		if server.State == stateFailed {
			continue
		} else {
			// Cleanup relay and vmaster state
			server.IsRelay = false
			server.IsVirtualMaster = false
			server.Refresh()
		}
	}
	wg := new(sync.WaitGroup)
	wg.Add(1)
	err = cluster.TopologyDiscover(wg)
	wg.Wait()
	if err == nil {
		return errors.New("Environment already has an existing master/slave setup")
	}

	cluster.StateMachine.SetFailoverState()
	masterKey := 0

	masterKey = func() int {
		for k, server := range cluster.Servers {
			// Skip child cluster
			if server.SourceClusterName != cluster.Name {
				continue
			}
			if oldMaster != nil {
				if server == oldMaster {
					cluster.StateMachine.RemoveFailoverState()
					return k
				}
			} else if cluster.Conf.PrefMaster != "" {
				if server.IsPrefered() {
					cluster.StateMachine.RemoveFailoverState()
					return k
				}
			}
		}
		cluster.StateMachine.RemoveFailoverState()
		if cluster.Conf.PrefMaster != "" {
			return -1
		}

		return 0
	}()

	if masterKey == -1 {
		return errors.New("Preferred master could not be found in existing servers")
	}

	// Assume master-slave if nothing else is declared
	if !cluster.Conf.ActivePassive && !cluster.Conf.MultiMasterRing && !cluster.Conf.MultiMaster && !cluster.Conf.MxsBinlogOn && !cluster.Conf.MultiTierSlave {

		for key, server := range cluster.Servers {
			if server.State == stateFailed {
				continue
			}
			if key == masterKey {
				if server.IsPostgreSQLHost() {
					// logical replication: the subscribers follow a publication of all tables
					logs, err := dbhelper.PostgresEnsurePublication(server.Conn, cluster.Conf.MasterConn)
					cluster.LogSQL(logs, err, server.URL, "Bootstrap", config.LvlErr, "Could not create the publication on %s: %s", server.URL, err)
					continue
				}
				dbhelper.FlushTables(server.Conn)
				server.SetReadWrite()

				// Set master GTID mode to be compatible with the cluster
				if cluster.Conf.ForceSlaveGtid && server.DBVersion.IsMySQLOrPercona() && server.DBVersion.GreaterEqual("5.7.6") {
					// MySQL 5.7.6 and later
					err := server.SetMyGTIDTransitional(true)
					if err != nil {
						cluster.SetState("ERR00098", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00098"], err.Error()), ErrFrom: "TOPO"})
					}
				}

				if cluster.Conf.MultiMasterGrouprep {
					// All clsuter node need a specialreplicaton for recovery parameters are not important as leader host is not needed
					server.ChangeMasterTo(server, "CURRENT_POS")
					server.BootstrapGroupReplication()
				}
				continue
			} else {
				// Set master GTID mode to be compatible with the cluster
				if cluster.Conf.ForceSlaveGtid && server.DBVersion.IsMySQLOrPercona() && server.DBVersion.GreaterEqual("5.7.6") {
					// MySQL 5.7.6 and later
					err := server.SetMyGTIDTransitional(true)
					if err != nil {
						cluster.SetState("ERR00099", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00099"], server.URL, err.Error()), ErrFrom: "TOPO", ServerUrl: server.URL})
					}
				}

				if cluster.Conf.MultiMasterGrouprep {
					_ = server.ChangeMasterTo(server, "SLAVE_POS")
					server.StartGroupReplication()
				} else {
					_ = server.ChangeMasterTo(cluster.Servers[masterKey], "SLAVE_POS")
				}
				if !server.ClusterGroup.IsInIgnoredReadonly(server) && !server.IsPostgreSQLHost() {
					// a logical replication subscriber stays writable: no read_only on PostgreSQL
					server.SetReadOnly()
				}
			}

		}
	}
	// Slave Relay
	if !cluster.Conf.ActivePassive && cluster.Conf.MultiTierSlave {
		relaykey := 1
		if masterKey == 1 {
			relaykey = 0
		}
		for key, server := range cluster.Servers {
			if server.State == stateFailed {
				continue
			}
			if key == masterKey {
				dbhelper.FlushTables(server.Conn)
				server.SetReadWrite()
				// Set master GTID mode to be compatible with the cluster
				if cluster.Conf.ForceSlaveGtid && server.DBVersion.IsMySQLOrPercona() && server.DBVersion.GreaterEqual("5.7.6") {
					// MySQL 5.7.6 and later
					err := server.SetMyGTIDTransitional(true)
					if err != nil {
						cluster.SetState("ERR00098", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00098"], err.Error()), ErrFrom: "TOPO"})
					}
				}
				continue
			} else {
				dbhelper.StopAllSlaves(server.Conn, server.DBVersion)
				dbhelper.ResetAllSlaves(server.Conn, server.DBVersion)

				// Set master GTID mode to be compatible with the cluster
				if cluster.Conf.ForceSlaveGtid && server.DBVersion.IsMySQLOrPercona() && server.DBVersion.GreaterEqual("5.7.6") {
					// MySQL 5.7.6 and later
					err := server.SetMyGTIDTransitional(true)
					if err != nil {
						cluster.SetState("ERR00099", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00099"], server.URL, err.Error()), ErrFrom: "TOPO", ServerUrl: server.URL})
					}
				}

				if relaykey == key {
					err = server.ChangeMasterTo(cluster.Servers[masterKey], "CURRENT_POS")
					if err != nil {
						cluster.StateMachine.RemoveFailoverState()
						return err
					}

				} else {
					err = server.ChangeMasterTo(cluster.Servers[relaykey], "CURRENT_POS")
					if err != nil {
						cluster.StateMachine.RemoveFailoverState()
						return err
					}
				}
				if !server.ClusterGroup.IsInIgnoredReadonly(server) {
					server.SetReadOnly()
				}

			}
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Environment bootstrapped with %s as master", cluster.Servers[masterKey].URL)
	}
	// Multi Master
	if !cluster.Conf.ActivePassive && cluster.Conf.MultiMaster {
		for _, server := range cluster.Servers {
			if server.State == stateFailed {
				continue
			}
			// Set master GTID mode to be compatible with the cluster
			if cluster.Conf.ForceSlaveGtid && server.DBVersion.IsMySQLOrPercona() && server.DBVersion.GreaterEqual("5.7.6") {
				// MySQL 5.7.6 and later
				err := server.SetMyGTIDTransitional(true)
				if err != nil {
					cluster.SetState("ERR00099", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00099"], server.URL, err.Error()), ErrFrom: "TOPO", ServerUrl: server.URL})
				}
			}
		}

		for key, server := range cluster.Servers {
			if server.State == stateFailed {
				continue
			}
			if key == 0 {
				err = server.ChangeMasterTo(cluster.Servers[1], "CURRENT_POS")
				if err != nil {
					cluster.StateMachine.RemoveFailoverState()
					return err
				}
				if !server.ClusterGroup.IsInIgnoredReadonly(server) {
					server.SetReadOnly()
				}
			}
			if key == 1 {
				err = server.ChangeMasterTo(cluster.Servers[0], "CURRENT_POS")
				if err != nil {
					cluster.StateMachine.RemoveFailoverState()
					return err
				}
			}
			if !server.ClusterGroup.IsInIgnoredReadonly(server) {
				server.SetReadOnly()
			}
		}
	}
	// Ring
	if !cluster.Conf.ActivePassive && cluster.Conf.MultiMasterRing {
		for _, server := range cluster.Servers {
			if server.State == stateFailed {
				continue
			}
			// Set master GTID mode to be compatible with the cluster
			if cluster.Conf.ForceSlaveGtid && server.DBVersion.IsMySQLOrPercona() && server.DBVersion.GreaterEqual("5.7.6") {
				// MySQL 5.7.6 and later
				err := server.SetMyGTIDTransitional(true)
				if err != nil {
					cluster.SetState("ERR00098", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00098"], err.Error()), ErrFrom: "TOPO"})
				}
			}
		}

		for key, server := range cluster.Servers {
			if server.State == stateFailed {
				continue
			}

			// Set master GTID mode to be compatible with the cluster
			if cluster.Conf.ForceSlaveGtid && server.DBVersion.IsMySQLOrPercona() && server.DBVersion.GreaterEqual("5.7.6") {
				// MySQL 5.7.6 and later
				err := server.SetMyGTIDTransitional(true)
				if err != nil {
					cluster.SetState("ERR00099", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00099"], server.URL, err.Error()), ErrFrom: "TOPO", ServerUrl: server.URL})
				}
			}

			i := (len(cluster.Servers) + key - 1) % len(cluster.Servers)
			err = server.ChangeMasterTo(cluster.Servers[i], "SLAVE_POS")
			if err != nil {
				cluster.StateMachine.RemoveFailoverState()
				return err
			}

			cluster.vmaster = cluster.Servers[0]

		}
	}
	cluster.StateMachine.RemoveFailoverState()
	// speed up topology discovery
	wg.Add(1)
	cluster.TopologyDiscover(wg)
	wg.Wait()

	//bootstrapChan <- true
	return nil
}

// GetDatabaseAgentNames is the agent list databases are placed on: prov-db-agents, or,
// when it is empty, every agent of the orchestrator (OpenSVC nodes, Kubernetes nodes...),
// so a cluster without an explicit list is placed on the whole infrastructure.
func (cluster *Cluster) GetDatabaseAgentNames() []string {
	names := []string{}
	for _, a := range strings.Split(cluster.Conf.ProvAgents, ",") {
		if a = strings.TrimSpace(a); a != "" {
			names = append(names, a)
		}
	}
	if len(names) > 0 {
		return names
	}
	cluster.Lock()
	defer cluster.Unlock()
	for _, node := range cluster.Agents {
		if node.HostName != "" {
			names = append(names, node.HostName)
		}
	}
	return names
}

func (cluster *Cluster) GetDatabaseAgent(server *ServerMonitor) (Agent, error) {
	var agent Agent
	agents := cluster.GetDatabaseAgentNames()
	if len(agents) == 0 {
		return agent, errors.New("No databases agent list provided and no agent known from the orchestrator")
	}
	for i, srv := range cluster.Servers {

		if srv.Id == server.Id {
			agentName := agents[i%len(agents)]
			agent, err := cluster.GetAgentInOrchetrator(agentName)
			if err != nil {
				return agent, err
			} else {
				return agent, nil
			}
		}
	}
	return agent, errors.New("Indice not found in database node list")
}

func (cluster *Cluster) GetProxyAgent(server DatabaseProxy) (Agent, error) {
	var agent Agent
	agents := strings.Split(cluster.Conf.ProvProxAgents, ",")
	if len(agents) == 0 {
		return agent, errors.New("No databases agent list provided")
	}
	for i, srv := range cluster.Servers {

		if srv.Id == server.GetId() {
			agentName := agents[i%len(agents)]
			agent, err := cluster.GetAgentInOrchetrator(agentName)
			if err != nil {
				return agent, err
			} else {
				return agent, nil
			}
		}
	}
	return agent, errors.New("Indice not found in database node list")
}

func (cluster *Cluster) GetAgentInOrchetrator(name string) (Agent, error) {
	var node Agent
	cluster.Lock()
	defer cluster.Unlock()
	for _, node := range cluster.Agents {
		if name == node.HostName {
			return node, nil
		}
	}
	return node, errors.New("Agent not found in orechestrator node list")
}

func (cluster *Cluster) ProvisionRotatePasswords(password string) error {
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		svc := cluster.OpenSVCConnect()
		err := svc.CreateSecretKeyValueV2(cluster.Name, "env", "MYSQL_ROOT_PASSWORD", password)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "ProvisionRotatePasswords error: Can not add key to secret: %s %s ", "MYSQL_ROOT_PASSWORD", err)
		}
	case config.ConstOrchestratorKubernetes:
		client, err := cluster.K8SConnectAPI()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "ProvisionRotatePasswords error: Cannot init Kubernetes client API %s ", err)
			return err
		}
		cluster.k8sRotatePasswordsWithClient(client, password)
	}
	return nil
}

// k8sRotatePasswordsWithClient patches the cluster's shared Secret
// (k8sEnsureDatabaseSecret) with the freshly rotated password -- one Secret
// for the whole cluster, matching OpenSVC's own single secret store, so a
// single patch here covers every server's Deployment. Without it, the
// dbjobs sidecar (which reads MYSQL_ROOT_PASSWORD as a live credential)
// would keep authenticating with the pre-rotation password indefinitely,
// and a future from-scratch reprovision would seed a fresh datadir with the
// wrong initial root password.
func (cluster *Cluster) k8sRotatePasswordsWithClient(client kubernetes.Interface, password string) {
	if err := cluster.k8sEnsureDatabaseSecret(client, password); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "ProvisionRotatePasswords error: Cannot update Kubernetes secret: %s ", err)
	}
}

func (cluster *Cluster) ReloadOpenSVCDaemonNodeStats() error {
	if cluster.GetOrchestrator() == config.ConstOrchestratorOpenSVC {
		svc := cluster.OpenSVCConnect()
		stats, err := svc.GetDaemonNodeStats()
		if err != nil {
			return err
		}

		cluster.OpenSVCStats.Swap(stats)
	}
	return nil
}

// FreezeDatabaseService holds the orchestrator off a database instance for the duration of
// a rolling stop/start. On om3 rc40 a status refresh racing an instance stop clears the
// stopped flag and the HA orchestration restarts the instance ~8 s later (opensvc/om3#1142,
// curepipe 2026-10-01); a frozen instance stays down whatever the refresh sees (proven on
// dev3). No-op on the other orchestrators and on OpenSVC v2.
func (cluster *Cluster) FreezeDatabaseService(server *ServerMonitor) error {
	if cluster.GetOrchestrator() != config.ConstOrchestratorOpenSVC || cluster.Conf.ProvOpensvcUseCollectorAPI {
		return nil
	}
	svc := cluster.OpenSVCConnect()
	if !svc.IsV3() {
		return nil
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
		"OpenSVC V3 instance freeze for %s on node %s (rolling operation)", server.URL, server.Agent)
	return svc.FreezeInstanceV3(server.Agent, server.ServiceName)
}

// UnfreezeDatabaseService gives the instance back to the orchestration after the start.
func (cluster *Cluster) UnfreezeDatabaseService(server *ServerMonitor) error {
	if cluster.GetOrchestrator() != config.ConstOrchestratorOpenSVC || cluster.Conf.ProvOpensvcUseCollectorAPI {
		return nil
	}
	svc := cluster.OpenSVCConnect()
	if !svc.IsV3() {
		return nil
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
		"OpenSVC V3 instance unfreeze for %s on node %s", server.URL, server.Agent)
	return svc.UnfreezeInstanceV3(server.Agent, server.ServiceName)
}

// xtrabackupImageRe is the character set of a docker image reference (registry, path,
// tag, digest). The value is written into an OpenSVC configuration and a Kubernetes
// image field, so anything else (spaces, quotes, shell or INI metacharacters) is refused.
var xtrabackupImageRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@+-]*$`)

// ValidateXtrabackupImage validates prov-db-docker-xtrabackup-img: empty (injection off),
// "auto" (derived from the database image when the bundle is rendered) or an image
// reference.
func ValidateXtrabackupImage(value string) error {
	if value == "" || value == xtrabackupImageAuto {
		return nil
	}
	if len(value) > 255 || !xtrabackupImageRe.MatchString(value) {
		return fmt.Errorf("prov-db-docker-xtrabackup-img must be empty, auto, or a docker image reference such as percona/percona-xtrabackup:8.4, got %q", value)
	}
	return nil
}

// dbIDMax keeps a UID/GID inside the signed 32-bit range that Docker,
// Kubernetes (runAsUser) and the configurator input all accept.
const dbIDMax = 2147483647

// ParseDBIdentity validates prov-db-run-as-uid and prov-db-volume-uid: empty (not set)
// or a numeric "UID" or "UID:GID", where 0 is root, taken literally and the GID
// defaults to the UID. "Literally" holds for the process and the volume owner; the
// dbjobs script db_owner is the one exception (it keeps the legacy owner for the few
// files it writes when the datadir is owned by root, see
// doc/implementation/cluster/DATABASE_RUNTIME_UID_GID.md). Names are refused: they
// would resolve through the image's passwd, and Kubernetes only takes numbers.
func ParseDBIdentity(setting, value string) (uid, gid int, set bool, err error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return 0, 0, false, nil
	}
	parts := strings.Split(v, ":")
	if len(parts) <= 2 {
		ids := make([]int, len(parts))
		valid := true
		for i, part := range parts {
			id, convErr := strconv.Atoi(part)
			if convErr != nil || id < 0 || id > dbIDMax || part != strings.TrimSpace(part) || strings.HasPrefix(part, "+") {
				valid = false
				break
			}
			ids[i] = id
		}
		if valid {
			if len(ids) == 1 {
				return ids[0], ids[0], true, nil
			}
			return ids[0], ids[1], true, nil
		}
	}
	return 0, 0, false, fmt.Errorf("%s must be empty (legacy behavior), UID or UID:GID with numeric ids from 0 (root) to %d, got %q", setting, dbIDMax, value)
}

// dbRunAs is the UID/GID the database container process runs as (OpenSVC
// --user, Kubernetes securityContext), from prov-db-run-as-uid. set is false when
// it is empty: nothing is rendered and the container runs as it did before the
// setting existed (`--user mysql` for images named mysql, the image's own user
// otherwise). An invalid value (the setter refuses one, but a config file may
// carry it) is logged and handled as empty rather than guessed.
func (cluster *Cluster) dbRunAs() (uid, gid int, set bool) {
	uid, gid, set, err := ParseDBIdentity("prov-db-run-as-uid", cluster.Conf.ProvDBRunAsUID)
	if err != nil {
		cluster.logInvalidDBIdentityOnce("prov-db-run-as-uid", cluster.Conf.ProvDBRunAsUID, err)
		return 0, 0, false
	}
	cluster.dbIdentityLog.valid("prov-db-run-as-uid")
	return uid, gid, set
}

// dbVolumeOwner is the UID/GID that owns the database data volume (OpenSVC volume
// owner and bootstrap chown, Kubernetes init chown), from prov-db-volume-uid. It is
// independent of the user the process runs as (dbRunAs): an operator can run as
// one identity and keep, or choose, another owner. managed tells whether
// replication-manager manages the owner at all:
//   - prov-db-volume-uid set: that owner, managed ("0" is root).
//   - empty, Percona Server image (recognized by name): 1001, managed. The image
//     is built for 1001 and runs as it by default; a volume owned by the legacy
//     999 cannot be written by it, and under any other UID its entrypoint cannot
//     start the telemetry agent ("Permission denied").
//   - empty otherwise: not managed, the legacy owner is kept unchanged (volume
//     and bootstrap chown 999:999, nothing on Kubernetes).
//
// An invalid value is logged and handled as empty.
func (cluster *Cluster) dbVolumeOwner() (uid, gid int, managed bool) {
	uid, gid, set, err := ParseDBIdentity("prov-db-volume-uid", cluster.Conf.ProvDBVolumeUID)
	if err != nil {
		cluster.logInvalidDBIdentityOnce("prov-db-volume-uid", cluster.Conf.ProvDBVolumeUID, err)
	} else {
		cluster.dbIdentityLog.valid("prov-db-volume-uid")
	}
	if err == nil && set {
		return uid, gid, true
	}
	if strings.Contains(strings.ToLower(cluster.Conf.ProvDbImg), "percona") {
		return 1001, 1001, true
	}
	return 999, 999, false
}

// dbIdentityLogState remembers, per setting, the last invalid identity value already
// reported, so a bad value in a configuration file is logged once and not at every render
// of the templates (the settings API refuses invalid values, so this only concerns
// hand-edited files). At most one value per setting (two in all) is kept per cluster: a
// new invalid value replaces the previous one, and a valid or empty value forgets it.
type dbIdentityLogState struct {
	mu   sync.Mutex
	last map[string]string
}

// invalid records an invalid value and reports whether it must be logged: true unless it
// is the one already reported for that setting.
func (st *dbIdentityLogState) invalid(setting, value string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if prev, seen := st.last[setting]; seen && prev == value {
		return false
	}
	if st.last == nil {
		st.last = make(map[string]string, 2)
	}
	st.last[setting] = value
	return true
}

// valid forgets the invalid value of a setting that now parses (or is empty).
func (st *dbIdentityLogState) valid(setting string) {
	st.mu.Lock()
	delete(st.last, setting)
	st.mu.Unlock()
}

// logInvalidDBIdentityOnce reports whether it logged.
func (cluster *Cluster) logInvalidDBIdentityOnce(setting, value string, err error) bool {
	if !cluster.dbIdentityLog.invalid(setting, value) {
		return false
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s; using the legacy behavior", err)
	return true
}

// dbRunAsVolumeMismatch describes, and is empty when there is nothing to say, a
// configuration where the database runs as a non-root UID (prov-db-run-as-uid)
// that does not own its data volume (prov-db-volume-uid, or the legacy 999 owner
// of OpenSVC, or whatever the storage gives on Kubernetes when the owner is not
// managed): mysqld then cannot write its datadir. The two settings are
// independent on purpose, so this is only reported, never corrected. Only the UID is
// compared: the owner permission bits decide for a process running as the owner, whatever
// the group of the files is.
func (cluster *Cluster) dbRunAsVolumeMismatch() string {
	runUID, _, set := cluster.dbRunAs()
	if !set || runUID == 0 {
		return ""
	}
	ownerUID, ownerGID, managed := cluster.dbVolumeOwner()
	switch {
	case !managed && cluster.GetOrchestrator() == config.ConstOrchestratorKubernetes:
		return fmt.Sprintf("prov-db-run-as-uid runs the database as UID %d but prov-db-volume-uid is not set: the data volume keeps the owner the storage gives it and mysqld may not be able to write its datadir; set prov-db-volume-uid to %d", runUID, runUID)
	case ownerUID != runUID:
		return fmt.Sprintf("prov-db-run-as-uid runs the database as UID %d but the data volume is owned by %d:%d: mysqld may not be able to write its datadir; set prov-db-volume-uid to %d", runUID, ownerUID, ownerGID, runUID)
	}
	return ""
}

// warnDBRunAsVolumeMismatch logs dbRunAsVolumeMismatch once per provisioning.
func (cluster *Cluster) warnDBRunAsVolumeMismatch() {
	if msg := cluster.dbRunAsVolumeMismatch(); msg != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn, "%s", msg)
	}
}

// dbIdentityManaged tells whether replication-manager manages the database
// identity in any way (a run-as user, an owner, or a Percona Server image). The
// dbjobs containers then run as root: they must read and chown a datadir that
// can belong to any UID, and Percona Server images default to a non-root user.
func (cluster *Cluster) dbIdentityManaged() bool {
	_, _, runAsSet := cluster.dbRunAs()
	_, _, chownManaged := cluster.dbVolumeOwner()
	return runAsSet || chownManaged
}
