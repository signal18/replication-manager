// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
)

// App live resize (prov-app-dynamic-resource): an app follows a change of its plan
// (prov-app-memory, prov-app-cpu-cores, prov-app-units: the APU shape) live, through the
// path a database server follows a DBU change (ResizeDynamicResources):
//
//   - the container cap moves on the orchestrator through resourceResizer()
//     (ConfigResizeApp): on OpenSVC v3, pg_mem_limit and pg_cpu_quota of the service
//     DEFAULT section then a pg update on each node (openSVCApplyServicePGKeywords, the
//     primitive of the database resize). The cap lives on the om3 slice when
//     prov-db-docker-run-args-limit is off, as for a database; with it on, the docker
//     --memory/--cpus of run_args binds and only a reprovision moves it;
//   - the configuration follows: the app configurator (prov-app-configurator) renders its
//     files again from the new plan (appConfiguratorSizing) into APP_CONFIGURATOR_SCRIPT,
//     pushed by the update path of the app's keys (OpenSVCPushAppVariableMaps);
//   - the app reloads: task#reload runs the template's APP_RELOAD_COMMAND in the PID
//     namespace of container#app on each node (for PHP: re-run the configurator script
//     into the container, then USR2 to the php-fpm master; defined by the template).
//
// Anti-OOM order, as for a database: a grow moves the cap first and the configuration
// after; a shrink reloads the smaller configuration first and moves the cap after. A grow
// whose cap cannot move live keeps the configuration and arms the reprovision.

const (
	appReloadCommandVariable = "APP_RELOAD_COMMAND"
	appReloadTask            = "task#reload"
)

// ErrAppLiveResize: the last live resize of the app failed (APPERR011).
const ErrAppLiveResize = "APPERR011"

// AppLiveResizeEnabled: a plan change of this app is applied live. A monitored database
// server deployed as an app follows the database plan (prov-db-dynamic-resource) instead.
func (cluster *Cluster) AppLiveResizeEnabled(app *App) bool {
	return cluster.Conf.ProvAppDynamicResource && app != nil && app.AppConfig != nil &&
		!app.AppConfig.IsMonitorOnly() && cluster.engineServerOfApp(app) == nil
}

// appCapOnSlice: the app's container cap lives on the om3 PG slice (moved live), like a
// database server's when prov-db-docker-run-args-limit is off.
func (cluster *Cluster) appCapOnSlice(app *App) bool {
	return cluster.AppLiveResizeEnabled(app) && !cluster.Conf.ProvDBDockerRunArgsLimit
}

// appPGKeywords are the cgroup keywords of the app's plan: its memory, its cores in the
// om3 quota syntax on the node of its first agent (OpenSVCCPUQuotaKeyword).
func (cluster *Cluster) appPGKeywords(app *App) map[string]string {
	kv := map[string]string{}
	if memMB, err := config.ParseUnitMeasurementToInt("M,bytes,required", cluster.GetAppMemory(app.AppConfig), true); err == nil && memMB > 0 {
		kv["pg_mem_limit"] = strconv.FormatInt(int64(memMB)*1024*1024, 10)
	}
	if cores, err := strconv.ParseFloat(strings.TrimSpace(cluster.GetAppCores(app.AppConfig)), 64); err == nil && cores > 0 {
		node := ""
		if agents := app.GetAppAgents(); len(agents) > 0 {
			node = agents[0]
		}
		if q := OpenSVCCPUQuotaKeyword(cluster.cpuQuotaCoresOnNode(node, cores)); q != "" {
			kv["pg_cpu_quota"] = q
		}
	}
	return kv
}

// appInstanceNodes are the nodes an instance of the app runs on: every agent of a flex
// app, the node a failover app was found on (all its agents when unknown).
func appInstanceNodes(app *App) []string {
	if app.AppConfig.ProvAppHATopology == "failover" && strings.TrimSpace(app.Agent) != "" {
		return []string{strings.TrimSpace(app.Agent)}
	}
	return app.GetAppAgents()
}

// appReloadCommand is the template's reload command (deployment variable
// APP_RELOAD_COMMAND), "" when the app has none.
func appReloadCommand(app *App) string {
	if app == nil || app.AppConfig == nil || app.AppConfig.Deployment == nil {
		return ""
	}
	for _, v := range app.AppConfig.Deployment.Variables {
		if v.Name == appReloadCommandVariable && v.Type != config.VariableTypeSecret {
			return strings.TrimSpace(v.Value)
		}
	}
	return ""
}

// openSVCAddAppReloadSection adds task#reload to an app service whose template has a
// reload command: a container of the app image in the PID and network namespaces of the
// app, with the app's keys as environment (APP_CONFIGURATOR_SCRIPT, APP_RELOAD_COMMAND).
// The orchestrator recreates it at each run, so it reads the keys of that moment.
func (cluster *Cluster) openSVCAddAppReloadSection(svcsection map[string]map[string]string, app *App) {
	if appReloadCommand(app) == "" || (cluster.Conf.ProvType != "docker" && cluster.Conf.ProvType != "podman") {
		return
	}
	task := map[string]string{
		"type":                cluster.Conf.ProvType,
		"image":               "{env.app_img}",
		"netns":               "container#01",
		"pidns":               "container#app",
		"rm":                  "true",
		"entrypoint":          "/bin/sh",
		"configs_environment": app.GetOpenSVCDeploymentAppEnv(config.VariableTypeEnv),
		"command":             `-c 'eval "$` + appReloadCommandVariable + `"'`,
	}
	if app.AppConfig.ProvAppHATopology == "failover" {
		task["shared"] = "true"
	}
	svcsection[appReloadTask] = task
}

// openSVCResizeApp moves the app's container cap live: the pg keywords of its plan in the
// service DEFAULT section, then a pg update on each node (the database primitive). False
// with no error when no live path exists: OpenSVC v2, or the docker run_args cap binds.
func (cluster *Cluster) openSVCResizeApp(app *App) (bool, error) {
	svc := cluster.OpenSVCConnect()
	if !svc.IsV3() || cluster.Conf.ProvOpensvcUseCollectorAPI {
		return false, nil
	}
	if cluster.Conf.ProvDBDockerRunArgsLimit {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo,
			"App %s: the container cap is the docker --memory/--cpus of run_args (prov-db-docker-run-args-limit), moved by the reprovision only", app.Name)
		return false, nil
	}
	kv := cluster.appPGKeywords(app)
	if len(kv) == 0 {
		return false, fmt.Errorf("no memory or cores in the plan of %s", app.Name)
	}
	nodes := appInstanceNodes(app)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo,
		"App %s: container cap pg_mem_limit=%s pg_cpu_quota=%s written to %s, applying with om %s instance pg update --node %s",
		app.Name, kv["pg_mem_limit"], kv["pg_cpu_quota"], app.ServiceName, app.ServiceName, strings.Join(nodes, ","))
	if err := cluster.openSVCApplyServicePGKeywords(app.ServiceName, nodes, kv); err != nil {
		return false, err
	}
	return true, nil
}

// reloadAppConfiguration makes the running app take its new plan: the configurator files
// rendered again and pushed with the app's keys, then task#reload on each node once the
// node loaded the new keys.
func (cluster *Cluster) reloadAppConfiguration(app *App) error {
	if strings.TrimSpace(app.AppConfig.ProvAppConfigurator) == "" && appReloadCommand(app) == "" {
		return nil // nothing in the app follows its size
	}
	svc := cluster.OpenSVCConnect()
	nodes := appInstanceNodes(app)
	if strings.TrimSpace(app.AppConfig.ProvAppConfigurator) != "" {
		if err := cluster.OpenSVCPushAppVariableMaps(app); err != nil {
			return fmt.Errorf("configuration not pushed: %w", err)
		}
		if svc.IsV3() {
			for _, node := range nodes {
				if err := svc.WaitObjectConfigSettledV3(node, cluster.Name, "cfg", app.Name, openSVCConfigSettleTimeout); err != nil {
					return fmt.Errorf("configuration not loaded on %s: %w", node, err)
				}
			}
		}
	}
	if appReloadCommand(app) == "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo,
			"App %s: no %s in its template, the new configuration applies at its next start", app.Name, appReloadCommandVariable)
		return nil
	}
	var errs []error
	for _, node := range nodes {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo,
			"App %s: reloading with om %s instance run --rid %s --node %s", app.Name, app.ServiceName, appReloadTask, node)
		if err := svc.RunTask(cluster.Name, app.ServiceName, node, appReloadTask, ""); err != nil {
			errs = append(errs, fmt.Errorf("%s on %s: %w", appReloadTask, node, err))
		}
	}
	return errors.Join(errs...)
}

// ResizeAppDynamicResources applies the app's current plan live (prov-app-dynamic-resource).
// Applies are serialized per app and coalesced by the caller (App.onAppResize).
func (cluster *Cluster) ResizeAppDynamicResources(app *App, grow bool) error {
	if !cluster.AppLiveResizeEnabled(app) {
		return nil
	}
	app.liveResizeMutex.Lock()
	defer app.liveResizeMutex.Unlock()
	app.liveResizePending.Store(false) // this run reads the plan in force now
	dir := "shrink"
	if grow {
		dir = "grow"
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo,
		"App %s: live resize (%s) to %sM / %s cores (prov-app-dynamic-resource)", app.Name, dir, cluster.GetAppMemory(app.AppConfig), cluster.GetAppCores(app.AppConfig))

	var err error
	if !grow {
		// shrink: the smaller configuration first, the cap after
		if err = cluster.reloadAppConfiguration(app); err == nil {
			var applied bool
			if applied, err = cluster.resourceResizer().ConfigResizeApp(app, grow); err == nil && !applied {
				app.SetReprovCookie() // the cap moves at the reprovision
			}
		}
	} else {
		// grow: the cap first; the larger configuration only inside a cap that grew
		var applied bool
		if applied, err = cluster.resourceResizer().ConfigResizeApp(app, grow); err == nil {
			if applied {
				err = cluster.reloadAppConfiguration(app)
			} else {
				app.SetReprovCookie()
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo,
					"App %s: no live cap on this orchestrator, the grow applies at the reprovision", app.Name)
			}
		}
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "App %s: live resize (%s) failed: %s", app.Name, dir, err)
		app.setLiveResizeError(err.Error())
		return err
	}
	app.setLiveResizeError("")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo, "App %s: live resize (%s) done", app.Name, dir)
	return nil
}

func (app *App) setLiveResizeError(msg string) {
	if app.Mutex != nil {
		app.Lock()
		defer app.Unlock()
	}
	app.LiveResizeError = msg
}

// onAppResize is called by the setters of the app's plan once the new values are stored.
// Live (prov-app-dynamic-resource): one apply runs, one waits and reads the plan in force
// when it starts, so a burst of changes never piles up goroutines. Otherwise the caller
// keeps the reprovision. Returns whether the change is applied live.
func (app *App) onAppResize(grow bool) bool {
	cluster := app.ClusterGroup
	if cluster == nil || !cluster.AppLiveResizeEnabled(app) {
		return false
	}
	if app.liveResizePending.Swap(true) {
		return true // an apply already waits: it will read this plan
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr, "App %s: live resize panicked: %v", app.Name, r)
			}
		}()
		cluster.ResizeAppDynamicResources(app, grow)
	}()
	return true
}

// appPlanGrows tells a grow from a shrink between two plans (memory MB, cores). The
// memory decides (it is the axis an out-of-order step kills with OOM); with the memory
// unchanged, the cores do.
func appPlanGrows(oldMem, newMem, oldCores, newCores string) bool {
	om, _ := config.ParseUnitMeasurementToInt("M,bytes", strings.TrimSpace(oldMem), false)
	nm, _ := config.ParseUnitMeasurementToInt("M,bytes", strings.TrimSpace(newMem), false)
	if nm != om {
		return nm > om
	}
	oc, _ := strconv.ParseFloat(strings.TrimSpace(oldCores), 64)
	nc, _ := strconv.ParseFloat(strings.TrimSpace(newCores), 64)
	return nc > oc
}
