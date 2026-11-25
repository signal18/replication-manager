// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
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

// appCookiePGCapPending: the app was (re)started with its cap on the om3 slice, which om3
// applies on a pg update only (project notes: nothing applies it at start); the next
// refresh that sees the app running runs it, as ArmOpenSVCPGCap does for a database server.
const appCookiePGCapPending = "pgcap"

// ArmOpenSVCPGCap marks the app for a pg update once it runs (no-op when its cap is a
// docker limit or outside OpenSVC v3).
func (app *App) ArmOpenSVCPGCap() {
	cluster := app.ClusterGroup
	if cluster == nil || cluster.GetOrchestrator() != config.ConstOrchestratorOpenSVC || cluster.Conf.ProvOpensvcUseCollectorAPI || !cluster.appCapOnSlice(app) {
		return
	}
	app.createCookie(appCookiePGCapPending)
}

// appPGCapMaxAttempts bounds the retries of the pg update after start (F4: nothing is
// retried to infinity): past it the cookie is dropped and APPERR011 says the cap is not on
// the slice until the next start or resize.
const appPGCapMaxAttempts = 5

// ApplyOpenSVCPGCapIfPending runs the armed pg update on the nodes of the app, off the
// refresh (one at a time), and keeps the cookie until they took it, at most
// appPGCapMaxAttempts times.
func (app *App) ApplyOpenSVCPGCapIfPending() {
	if !app.hasCookie(appCookiePGCapPending) || !app.pgCapInFlight.CompareAndSwap(false, true) {
		return
	}
	cluster := app.ClusterGroup
	go func() {
		defer app.pgCapInFlight.Store(false)
		defer func() {
			if r := recover(); r != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr, "App %s: pg update after start panicked: %v", app.Name, r)
			}
		}()
		svc := cluster.OpenSVCConnect()
		if !svc.IsV3() {
			app.delCookie(appCookiePGCapPending)
			return
		}
		nodes, anyNode := appInstanceNodes(app)
		err := appOnNodes(nodes, anyNode, func(node string) error { return svc.PGUpdateInstanceV3(node, app.ServiceName, "") })
		if err != nil {
			if n := app.pgCapAttempts.Add(1); n >= appPGCapMaxAttempts {
				app.pgCapAttempts.Store(0)
				app.delCookie(appCookiePGCapPending)
				app.setLiveResizeError(fmt.Sprintf("container cap not applied on the PG slice after start (%d attempts): %s", n, err))
				return
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "App %s: pg update after start failed, retried next refresh: %s", app.Name, err)
			return
		}
		app.pgCapAttempts.Store(0)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo,
			"App %s: container cap applied on the PG slice after start (om %s instance pg update)", app.Name, app.ServiceName)
		app.delCookie(appCookiePGCapPending)
	}()
}

// appInstanceNodes are the nodes an instance of the app runs on: every agent of a flex
// app, the node a failover app was found on. A failover app whose node is not known yet
// gets all its agents with anyNode set: the one that runs it is enough.
func appInstanceNodes(app *App) (nodes []string, anyNode bool) {
	if app.AppConfig.ProvAppHATopology == "failover" {
		if agent := strings.TrimSpace(app.GetAgent()); agent != "" {
			return []string{agent}, false
		}
		return app.GetAppAgents(), true
	}
	return app.GetAppAgents(), false
}

// appOnNodes runs fn on each node: every node must succeed, or at least one when anyNode.
func appOnNodes(nodes []string, anyNode bool, fn func(node string) error) error {
	var errs []error
	for _, node := range nodes {
		if err := fn(node); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", node, err))
		}
	}
	if anyNode && len(errs) < len(nodes) {
		return nil
	}
	return errors.Join(errs...)
}

// appReloadSandboxCommand is the command of the reload task under a sandbox runtime: the
// template's APP_RELOAD_COMMAND, run only once the shell has checked it is inside the
// app's sandbox, never on the node. Under gVisor (runsc*) the kernel the shell reads is
// gVisor's (/proc/version "...-gvisor", the node's is not); under another runtime, the
// container files docker (/.dockerenv) and podman (/run/.containerenv) create, which the
// node does not have. Fixed text: the runtime name only picks one of the two. Neither
// " ;" nor " #" appear in it (the INI reader would take the rest as a comment).
func appReloadSandboxCommand(runtime string) string {
	if strings.HasPrefix(runtime, "runsc") {
		return `/bin/sh -c 'read -r k </proc/version;case "$k" in *gvisor*) eval "$` + appReloadCommandVariable +
			`";;*) echo "reload refused: not in the app sandbox" >&2;exit 1;;esac'`
	}
	return `/bin/sh -c 'if [ -e /.dockerenv ] || [ -e /run/.containerenv ];then eval "$` + appReloadCommandVariable +
		`";else echo "reload refused: not in the app container" >&2;exit 1;fi'`
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
// reload command (APP_RELOAD_COMMAND), run by a live resize with the app's keys of that
// moment as its environment (APP_CONFIGURATOR_SCRIPT, APP_RELOAD_COMMAND). Two forms:
//
//   - No sandbox runtime: a container of the app image, recreated at each run, in the PID
//     and network namespaces of the app, so it signals the app's processes.
//   - A sandbox runtime (prov-app-docker-runtime): the app's processes live in the
//     sandbox's own kernel, and no second container, sandboxed or not, can join that PID
//     namespace (in a gVisor task, /proc/1 is the task itself and the reload finds no
//     php-fpm). The task is a host task that runs the command IN container#app, through
//     the container's runtime (om3 task.host `container` keyword, an exec into the sandbox,
//     the environment read by the agent at the run). The command refuses to run anywhere
//     else (appReloadSandboxCommand): an agent that ignored the `container` keyword (om3
//     only warns on an unknown keyword) would run it on the node, where it exits 1.
func (cluster *Cluster) openSVCAddAppReloadSection(svcsection map[string]map[string]string, app *App) {
	if appReloadCommand(app) == "" || (cluster.Conf.ProvType != "docker" && cluster.Conf.ProvType != "podman") {
		return
	}
	// an invalid runtime stops the rendering in applyAppDockerRuntime
	// a trusted app (prov-app-docker-trusted) runs under the node's runtime: the docker task
	if runtime, err := cluster.appDockerRuntime(app); err == nil && runtime != "" {
		task := map[string]string{
			"type":                "host",
			"container":           "container#app",
			"configs_environment": app.GetOpenSVCDeploymentAppEnv(config.VariableTypeEnv),
			"command":             appReloadSandboxCommand(runtime),
		}
		if app.AppConfig.ProvAppHATopology == "failover" {
			task["shared"] = "true"
		}
		svcsection[appReloadTask] = task
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
	nodes, anyNode := appInstanceNodes(app)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo,
		"App %s: container cap pg_mem_limit=%s pg_cpu_quota=%s written to %s, applying with om %s instance pg update --node %s",
		app.Name, kv["pg_mem_limit"], kv["pg_cpu_quota"], app.ServiceName, app.ServiceName, strings.Join(nodes, ","))
	if !anyNode {
		if err := cluster.openSVCApplyServicePGKeywords(app.ServiceName, nodes, kv); err != nil {
			return false, err
		}
		return true, nil
	}
	// a failover app on an unknown node: the keywords once, the pg update where it runs
	ns, kind, svcname, err := cluster.openSVCWriteObjectKeywords(app.ServiceName, "DEFAULT", kv)
	if err != nil {
		return false, err
	}
	if err := appOnNodes(nodes, true, func(node string) error {
		return cluster.openSVCSettleAndPGUpdate(svc, ns, kind, svcname, node)
	}); err != nil {
		return false, err
	}
	return true, nil
}

// reloadAppConfiguration makes the running app take its new plan: the configurator files
// rendered again and pushed with the app's keys, then task#reload on each node once the
// node loaded the new keys.
func (cluster *Cluster) reloadAppConfiguration(app *App) error {
	// any configurator, not only a database engine (isDatabaseEngineApp): a php app's
	// files follow its plan too
	if strings.TrimSpace(app.AppConfig.ProvAppConfigurator) == "" && appReloadCommand(app) == "" {
		return nil // nothing in the app follows its size
	}
	svc := cluster.OpenSVCConnect()
	nodes, anyNode := appInstanceNodes(app)
	if strings.TrimSpace(app.AppConfig.ProvAppConfigurator) != "" {
		if err := cluster.OpenSVCPushAppVariableMaps(app); err != nil {
			return fmt.Errorf("configuration not pushed: %w", err)
		}
		if svc.IsV3() {
			if err := appOnNodes(nodes, anyNode, func(node string) error {
				return svc.WaitObjectConfigSettledV3(node, cluster.Name, "cfg", app.Name, openSVCConfigSettleTimeout)
			}); err != nil {
				return fmt.Errorf("configuration not loaded: %w", err)
			}
		}
	}
	if appReloadCommand(app) == "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo,
			"App %s: no %s in its template, the new configuration applies at its next start", app.Name, appReloadCommandVariable)
		return nil
	}
	if !cluster.openSVCAppHasSection(svc, app, appReloadTask) {
		// a service written before its template had a reload command
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"App %s: its service has no %s yet (update-opensvc-template or a reprovision adds it): the new configuration applies at its next start", app.Name, appReloadTask)
		return nil
	}
	return appOnNodes(nodes, anyNode, func(node string) error {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo,
			"App %s: reloading with om %s instance run --rid %s --node %s", app.Name, app.ServiceName, appReloadTask, node)
		return svc.RunTask(cluster.Name, app.ServiceName, node, appReloadTask, "")
	})
}

// openSVCAppHasSection tells whether the app's service definition on the orchestrator has
// the section (an app provisioned before the section existed has not).
func (cluster *Cluster) openSVCAppHasSection(svc opensvc.Collector, app *App, section string) bool {
	parts := strings.SplitN(app.ServiceName, "/", 3)
	if len(parts) != 3 || !svc.IsV3() {
		return true // nothing to check: let the run say
	}
	raw, err := svc.GetObjectConfigFileV3(parts[0], parts[1], parts[2])
	if err != nil {
		return true
	}
	return strings.Contains(string(raw), "["+section+"]")
}

// appCapInForce is the container cap the app's service runs under, as its definition on
// the orchestrator says (F5: observed, not remembered): memory in MB and cpu in om3 quota
// percent (pg_mem_limit / pg_cpu_quota of DEFAULT, or the docker --memory / --cpus of
// container#app run_args). A reprovision, a failed or partial resize, a restart of
// replication-manager all leave it right, since it is read, not tracked.
func (cluster *Cluster) appCapInForce(app *App) (memMB int, cpuPct int, ok bool) {
	if cluster.appCapOverride != nil {
		return cluster.appCapOverride(app)
	}
	parts := strings.SplitN(app.ServiceName, "/", 3)
	if cluster.GetOrchestrator() != config.ConstOrchestratorOpenSVC || cluster.Conf.ProvOpensvcUseCollectorAPI || len(parts) != 3 {
		return 0, 0, false
	}
	svc := cluster.OpenSVCConnect()
	if !svc.IsV3() {
		return 0, 0, false
	}
	raw, err := svc.GetObjectConfigFileV3(parts[0], parts[1], parts[2])
	if err != nil {
		return 0, 0, false
	}
	return appCapFromDefinition(string(raw))
}

var (
	appRunArgsMemoryRe = regexp.MustCompile(`--memory=(\d+)m\b`)
	appRunArgsCPUsRe   = regexp.MustCompile(`--cpus=([0-9.]+)`)
)

// appCapFromDefinition reads the cap of an app service definition (see appCapInForce).
func appCapFromDefinition(raw string) (memMB int, cpuPct int, ok bool) {
	if v := openSVCConfigValue(raw, "DEFAULT", "pg_mem_limit"); v != "" {
		if b, err := strconv.ParseInt(v, 10, 64); err == nil {
			memMB = int(b / (1024 * 1024))
		}
	}
	if v := strings.TrimSuffix(openSVCConfigValue(raw, "DEFAULT", "pg_cpu_quota"), "%"); v != "" {
		cpuPct, _ = strconv.Atoi(v)
	}
	if memMB == 0 || cpuPct == 0 {
		runArgs := openSVCConfigValue(raw, "container#app", "run_args")
		if memMB == 0 {
			// rendered by OpenSVCGetAppContainerSection as --memory=<MB>m
			if m := appRunArgsMemoryRe.FindStringSubmatch(runArgs); m != nil {
				memMB, _ = strconv.Atoi(m[1])
			}
		}
		if cpuPct == 0 {
			if m := appRunArgsCPUsRe.FindStringSubmatch(runArgs); m != nil {
				if f, err := strconv.ParseFloat(m[1], 64); err == nil {
					cpuPct = int(f*100 + 0.5)
				}
			}
		}
	}
	return memMB, cpuPct, memMB > 0
}

// appResizeGrows decides the order of a live resize from the cap in force and the plan's
// keywords: the memory decides, the cpu when the memory is unchanged; ok=false when the
// cap in force cannot be read (the caller's guess is used then).
func (cluster *Cluster) appResizeGrows(app *App) (grow bool, ok bool) {
	curMem, curPct, ok := cluster.appCapInForce(app)
	if !ok {
		return false, false
	}
	kv := cluster.appPGKeywords(app)
	wantMem := 0
	if b, err := strconv.ParseInt(kv["pg_mem_limit"], 10, 64); err == nil {
		wantMem = int(b / (1024 * 1024))
	}
	wantPct, _ := strconv.Atoi(strings.TrimSuffix(kv["pg_cpu_quota"], "%"))
	if wantMem != curMem {
		return wantMem > curMem, true
	}
	return wantPct > curPct, true
}

// ResizeAppDynamicResources applies the app's current plan live (prov-app-dynamic-resource).
// Applies are serialized per app and coalesced by the caller (App.onAppResize). The
// direction is computed here, against the cap the app's service runs under
// (appCapInForce): a coalesced apply may carry several changes, and the anti-OOM order must
// follow what is really in place. grow is the caller's guess, used when the cap cannot be
// read.
func (cluster *Cluster) ResizeAppDynamicResources(app *App, grow bool) error {
	if !cluster.AppLiveResizeEnabled(app) {
		return nil
	}
	app.liveResizeMutex.Lock()
	defer app.liveResizeMutex.Unlock()
	app.liveResizePending.Store(false) // this run reads the plan in force now
	mem, cores := app.planSnapshot()
	if observed, ok := cluster.appResizeGrows(app); ok {
		grow = observed
	} else {
		// the cap in force cannot be read: the shrink order (configuration first). On a real
		// shrink it is the safe order; on a real grow the larger configuration runs under
		// the old cap until the cap moves a moment later. The opposite mistake (cap first
		// on a real shrink) would leave the larger configuration under a smaller cap.
		grow = false
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo,
			"App %s: cap in force not readable on the orchestrator, applying in the shrink order (configuration first; a grow runs its configuration under the old cap until the cap moves)", app.Name)
	}
	dir := "shrink"
	if grow {
		dir = "grow"
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo,
		"App %s: live resize (%s) to %sM / %s cores (prov-app-dynamic-resource)", app.Name, dir, mem, cores)

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
		// the plan is stored but not in force: the reprovision applies it, APPERR011 says why
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "App %s: live resize (%s) failed, the plan applies at the reprovision: %s", app.Name, dir, err)
		app.setLiveResizeError(err.Error())
		if !app.HasReprovCookie() {
			app.SetReprovCookie()
			app.liveResizeArmedReprov.Store(true) // after SetReprovCookie, which resets it
		}
		return err
	}
	if app.liveResizeArmedReprov.Load() {
		// the plan the failed apply left to the reprovision is in force now, and nothing
		// else armed the cookie since (SetReprovCookie / DelReprovisionCookie reset the flag)
		app.DelReprovisionCookie()
	}
	app.setLiveResizeError("")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo, "App %s: live resize (%s) done", app.Name, dir)
	return nil
}

// planSnapshot reads the app's memory and cores once, under the app lock.
func (app *App) planSnapshot() (mem, cores string) {
	if app.Mutex != nil {
		app.Lock()
		defer app.Unlock()
	}
	return app.AppConfig.ProvAppMem, app.AppConfig.ProvAppCpuCores
}

func (app *App) setLiveResizeError(msg string) {
	if app.Mutex != nil {
		app.Lock()
		defer app.Unlock()
	}
	app.LiveResizeError = msg
}

func (app *App) setServiceDefinitionError(msg string) {
	if app.Mutex != nil {
		app.Lock()
		defer app.Unlock()
	}
	app.ServiceDefinitionError = msg
}

func (app *App) getServiceDefinitionError() string {
	if app.Mutex != nil {
		app.Lock()
		defer app.Unlock()
	}
	return app.ServiceDefinitionError
}

func (app *App) getLiveResizeError() string {
	if app.Mutex != nil {
		app.Lock()
		defer app.Unlock()
	}
	return app.LiveResizeError
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
