// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"encoding/base64"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/utils/misc"
)

// App dynamic config (prov-app-apply-dynamic-config): the runtime configuration of an app
// follows its plan, the way prov-db-apply-dynamic-config makes a database follow prov-db-memory and prov-db-cpu-cores. PHP is the one app engine
// sized today: the php-fpm pool, memory_limit and the opcache are computed here from the
// app's memory and cores, rendered as two fragments (a pool file and an ini file), and
// delivered by the same channel as the app configurator: config keys of the app.
//
//   - At start: the app's first volume installs the two keys as files (om3 volume
//     `configs`), and the app container mounts each file in the directory the image reads,
//     so php-fpm starts with the sizing of the plan.
//   - On a resize: the keys are rewritten, then task#phpconfig runs on each node of the app.
//     The task is a container of the app's image in the PID namespace of container#app: it
//     writes the files in place, checks the container sees them and that its memory cap
//     holds the new pool, then sends USR2 to the php-fpm master, the graceful reload (the
//     workers finish their request, the listening socket is kept). No container restart.
//
// See doc/implementation/cluster/APP_DYNAMIC_CONFIG.md.

const (
	// config keys of the app (cfg object <namespace>/cfg/<app>)
	appPHPPoolKey   = "APP_PHP_FPM_POOL"
	appPHPIniKey    = "APP_PHP_INI"
	appPHPConfigKey = "APP_PHP_CONFIG" // the apply script, base64, run by task#phpconfig
	// run-time environment of task#phpconfig: the script of this very resize, which wins
	// over the key (the key of a node can lag the push by one write, #1795)
	appPHPConfigRunEnv = "REPMAN_PHP_CONFIG"
	appPHPConfigTask   = "task#phpconfig"
	// directory of the files in the app's first volume, and its mount in the task
	appPHPVolumeDir   = "repman-php"
	appPHPTaskMount   = "/repman-php"
	appPHPPoolFile    = "zzz-repman-sizing.conf" // read after the image's own pool files
	appPHPIniFile     = "zzz-repman-sizing.ini"  // read after the image's own ini files
	appPHPDefaultPool = "www"

	appPHPDefaultPoolDir        = "/usr/local/etc/php-fpm.d"
	appPHPDefaultIniDir         = "/usr/local/etc/php/conf.d"
	appPHPDefaultProcessMB      = 64
	appPHPDefaultReservedMB     = 128
	appPHPMinChildren           = 2
	appPHPChildrenPerCore       = 8   // a PHP worker mostly waits on its database: 8 per core
	appPHPMaxRequests           = 500 // a worker is recycled after this many requests (bounds a leak)
	appPHPMinMemoryLimitMB      = 128
	appPHPMinOpcacheMB          = 64
	appPHPMaxOpcacheMB          = 256
	appPHPProcessControlTimeout = 30 // seconds the workers have to finish their request on a reload
)

// appPHPPoolNameRe: a php-fpm pool name, a section header of the pool fragment.
var appPHPPoolNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// appPHPSizing is the PHP configuration computed from an app plan.
type appPHPSizing struct {
	MemoryMB      int // the plan: memory of one instance
	Cores         int // the plan: cores of one instance
	ProcessMB     int // average memory of one worker
	ReservedMB    int // memory kept out of the pool
	OpcacheMB     int // opcache.memory_consumption
	PoolMB        int // memory left to the workers
	MaxChildren   int // pm.max_children
	StartServers  int // pm.start_servers
	MinSpare      int // pm.min_spare_servers
	MaxSpare      int // pm.max_spare_servers
	MaxRequests   int // pm.max_requests
	MemoryLimitMB int // memory_limit
	RequiredMB    int // what the sizing needs at most: reserved + opcache + max_children x process
}

// computeAppPHPSizing derives the PHP configuration of an instance of memMB and cores:
//
//	opcache      = memory / 16, within [64, 256] MB (shared by the workers)
//	pool         = memory - reserved - opcache
//	max_children = floor(pool / process), at least 2, at most 8 per core
//	min_spare    = max(1, max_children / 8)
//	max_spare    = max(min_spare, max_children / 2)
//	start        = min_spare + (max_spare - min_spare) / 2
//	memory_limit = 4 x process, within [128 MB, pool / 2] (one request never takes half the pool)
//
// processMB and reservedMB below 1 take the defaults (64, 128).
func computeAppPHPSizing(memMB, cores, processMB, reservedMB int) appPHPSizing {
	if processMB < 1 {
		processMB = appPHPDefaultProcessMB
	}
	if reservedMB < 1 {
		reservedMB = appPHPDefaultReservedMB
	}
	if cores < 1 {
		cores = 1
	}
	if memMB < 1 {
		memMB = 0
	}
	s := appPHPSizing{MemoryMB: memMB, Cores: cores, ProcessMB: processMB, ReservedMB: reservedMB, MaxRequests: appPHPMaxRequests}
	s.OpcacheMB = clampInt(memMB/16, appPHPMinOpcacheMB, appPHPMaxOpcacheMB)
	s.PoolMB = memMB - reservedMB - s.OpcacheMB
	if s.PoolMB < 0 {
		s.PoolMB = 0
	}
	s.MaxChildren = clampInt(s.PoolMB/processMB, appPHPMinChildren, appPHPChildrenPerCore*cores)
	s.MinSpare = s.MaxChildren / 8
	if s.MinSpare < 1 {
		s.MinSpare = 1
	}
	s.MaxSpare = s.MaxChildren / 2
	if s.MaxSpare < s.MinSpare {
		s.MaxSpare = s.MinSpare
	}
	s.StartServers = s.MinSpare + (s.MaxSpare-s.MinSpare)/2
	limitCeil := s.PoolMB / 2
	if limitCeil < appPHPMinMemoryLimitMB {
		limitCeil = appPHPMinMemoryLimitMB
	}
	s.MemoryLimitMB = clampInt(4*processMB, appPHPMinMemoryLimitMB, limitCeil)
	s.RequiredMB = reservedMB + s.OpcacheMB + s.MaxChildren*processMB
	return s
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		hi = lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// The fragments, written with the %%ENV:...%% tokens of the configurator modulesets so
// that they can move to an OpenSVC collector ruleset unchanged (T21): the values are
// the tokens of appPHPSizingTokens.
const appPHPPoolFragmentTemplate = `; written by replication-manager from the app plan (%%ENV:SVC_CONF_ENV_PHP_MEMORY%%M, %%ENV:SVC_CONF_ENV_PHP_CORES%% cores): do not edit,
; the next resize rewrites it. Off: prov-app-apply-dynamic-config.
[global]
process_control_timeout = %%ENV:SVC_CONF_ENV_PHP_PROCESS_CONTROL_TIMEOUT%%s

[%%ENV:SVC_CONF_ENV_PHP_FPM_POOL%%]
pm = dynamic
pm.max_children = %%ENV:SVC_CONF_ENV_PHP_FPM_MAX_CHILDREN%%
pm.start_servers = %%ENV:SVC_CONF_ENV_PHP_FPM_START_SERVERS%%
pm.min_spare_servers = %%ENV:SVC_CONF_ENV_PHP_FPM_MIN_SPARE_SERVERS%%
pm.max_spare_servers = %%ENV:SVC_CONF_ENV_PHP_FPM_MAX_SPARE_SERVERS%%
pm.max_requests = %%ENV:SVC_CONF_ENV_PHP_FPM_MAX_REQUESTS%%
`

const appPHPIniFragmentTemplate = `; written by replication-manager from the app plan (%%ENV:SVC_CONF_ENV_PHP_MEMORY%%M, %%ENV:SVC_CONF_ENV_PHP_CORES%% cores): do not edit,
; the next resize rewrites it. Off: prov-app-apply-dynamic-config.
memory_limit = %%ENV:SVC_CONF_ENV_PHP_MEMORY_LIMIT%%M
opcache.memory_consumption = %%ENV:SVC_CONF_ENV_PHP_OPCACHE_MEMORY%%
`

// appPHPSizingTokens is the token map of a sizing.
func appPHPSizingTokens(s appPHPSizing, pool string) map[string]string {
	i := strconv.Itoa
	return map[string]string{
		"%%ENV:SVC_CONF_ENV_PHP_MEMORY%%":                  i(s.MemoryMB),
		"%%ENV:SVC_CONF_ENV_PHP_CORES%%":                   i(s.Cores),
		"%%ENV:SVC_CONF_ENV_PHP_PROCESS_CONTROL_TIMEOUT%%": i(appPHPProcessControlTimeout),
		"%%ENV:SVC_CONF_ENV_PHP_FPM_POOL%%":                pool,
		"%%ENV:SVC_CONF_ENV_PHP_FPM_MAX_CHILDREN%%":        i(s.MaxChildren),
		"%%ENV:SVC_CONF_ENV_PHP_FPM_START_SERVERS%%":       i(s.StartServers),
		"%%ENV:SVC_CONF_ENV_PHP_FPM_MIN_SPARE_SERVERS%%":   i(s.MinSpare),
		"%%ENV:SVC_CONF_ENV_PHP_FPM_MAX_SPARE_SERVERS%%":   i(s.MaxSpare),
		"%%ENV:SVC_CONF_ENV_PHP_FPM_MAX_REQUESTS%%":        i(s.MaxRequests),
		"%%ENV:SVC_CONF_ENV_PHP_MEMORY_LIMIT%%":            i(s.MemoryLimitMB),
		"%%ENV:SVC_CONF_ENV_PHP_OPCACHE_MEMORY%%":          i(s.OpcacheMB),
	}
}

// renderAppPHPFragments returns the pool and the ini fragments of a sizing.
func renderAppPHPFragments(s appPHPSizing, pool string) (poolFragment, iniFragment string) {
	tokens := appPHPSizingTokens(s, pool)
	return misc.ExtractKey(appPHPPoolFragmentTemplate, tokens), misc.ExtractKey(appPHPIniFragmentTemplate, tokens)
}

// appPHPApplyScript is the POSIX shell script task#phpconfig runs. It finds the php-fpm
// master in the PID namespace it shares with container#app, refuses a plan larger than the
// memory cap of the container (the cap of a docker-capped app moves at its reprovision
// only: the keys already carry the new sizing for that start), writes the two files in
// place in the volume (the container's single-file mounts keep their inode), checks the
// container reads the new pool file, and sends USR2 to the master.
func appPHPApplyScript(s appPHPSizing, poolFragment, iniFragment, poolDir, iniDir string) string {
	var b strings.Builder
	q := func(v string) string { return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'" }
	fmt.Fprintf(&b, "set -e\nPOOL_DIR=%s\nINI_DIR=%s\nDIR=%s\nPLAN_MB=%d\nMAX_CHILDREN=%d\n",
		q(poolDir), q(iniDir), appPHPTaskMount, s.MemoryMB, s.MaxChildren)
	b.WriteString(`master=""
for d in /proc/[0-9]*; do
  cmd=$(tr '\0' ' ' < "$d/cmdline" 2>/dev/null) || continue
  case "$cmd" in "php-fpm: master process"*) master=${d#/proc/}; break ;; esac
done
if [ -z "$master" ]; then echo "phpconfig: no php-fpm master process in container#app" >&2; exit 1; fi
root=/proc/$master/root
cap=$(cat "$root/sys/fs/cgroup/memory.max" 2>/dev/null || echo unknown)
case "$cap" in
  max) ;;
  ''|*[!0-9]*)
    current=$(sed -n 's/^pm.max_children *= *//p' "$DIR/` + appPHPPoolFile + `" 2>/dev/null)
    if [ -z "$current" ] || [ "$MAX_CHILDREN" -gt "$current" ]; then
      echo "phpconfig: memory cap of the container not readable, a larger pool waits for the next start" >&2; exit 2
    fi ;;
  *)
    if [ $((cap / 1048576)) -lt "$PLAN_MB" ]; then
      echo "phpconfig: the container cap is $((cap / 1048576))M, below the plan of ${PLAN_MB}M: the sizing applies at the next start (reprovision)" >&2; exit 2
    fi ;;
esac
mkdir -p "$DIR"
cat > "$DIR/` + appPHPPoolFile + `" <<'__REPMAN_PHP_POOL_EOF__'
`)
	b.WriteString(poolFragment)
	if !strings.HasSuffix(poolFragment, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(`__REPMAN_PHP_POOL_EOF__
cat > "$DIR/` + appPHPIniFile + `" <<'__REPMAN_PHP_INI_EOF__'
`)
	b.WriteString(iniFragment)
	if !strings.HasSuffix(iniFragment, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(`__REPMAN_PHP_INI_EOF__
if [ -r "$root$POOL_DIR/` + appPHPPoolFile + `" ] && [ "$(cat "$root$POOL_DIR/` + appPHPPoolFile + `")" != "$(cat "$DIR/` + appPHPPoolFile + `")" ]; then
  echo "phpconfig: container#app does not see the new pool file in $POOL_DIR: applied at the next start" >&2; exit 3
fi
kill -USR2 "$master"
i=0
while [ $i -lt 30 ]; do
  sleep 1
  i=$((i + 1))
  cmd=$(tr '\0' ' ' < "/proc/$master/cmdline" 2>/dev/null) || cmd=""
  case "$cmd" in "php-fpm: master process"*) echo "phpconfig: php-fpm master $master reloaded, pm.max_children=$MAX_CHILDREN"; exit 0 ;; esac
done
echo "phpconfig: php-fpm master $master did not come back after the reload" >&2
exit 4
`)
	return b.String()
}

// AppDynamicConfigEnabled: the app's runtime configuration follows its plan. A monitored
// engine server (prov-app-configurator) is sized by the database configurator instead.
func (cluster *Cluster) AppDynamicConfigEnabled(app *App) bool {
	return app != nil && app.AppConfig != nil && app.AppConfig.ProvAppApplyDynamicConfig &&
		strings.TrimSpace(app.AppConfig.ProvAppConfigurator) == ""
}

func appPHPPoolDir(app *App) string {
	if v := strings.TrimSpace(app.AppConfig.ProvAppPhpFpmPoolDir); v != "" {
		return strings.TrimSuffix(v, "/")
	}
	return appPHPDefaultPoolDir
}

func appPHPIniDir(app *App) string {
	if v := strings.TrimSpace(app.AppConfig.ProvAppPhpIniDir); v != "" {
		return strings.TrimSuffix(v, "/")
	}
	return appPHPDefaultIniDir
}

func appPHPPool(app *App) string {
	if v := strings.TrimSpace(app.AppConfig.ProvAppPhpFpmPool); v != "" {
		return v
	}
	return appPHPDefaultPool
}

// appMeasureMB parses an app memory value (64, 64M, 1G) in MB; 0 when empty or invalid.
func appMeasureMB(v string) int {
	if strings.TrimSpace(v) == "" {
		return 0
	}
	mb, err := config.ParseUnitMeasurementToInt("M,bytes", strings.TrimSpace(v), false)
	if err != nil {
		return 0
	}
	return mb
}

// AppPHPSizing is the PHP sizing of the app's current plan.
func (cluster *Cluster) AppPHPSizing(app *App) appPHPSizing {
	memMB, _ := config.ParseUnitMeasurementToInt("M,bytes,required", cluster.GetAppMemory(app.AppConfig), true)
	coresF, _ := strconv.ParseFloat(strings.TrimSpace(cluster.GetAppCores(app.AppConfig)), 64)
	return computeAppPHPSizing(memMB, int(math.Ceil(coresF)), appMeasureMB(app.AppConfig.ProvAppPhpProcessMemory), appMeasureMB(app.AppConfig.ProvAppPhpReservedMemory))
}

// appPHPConfig returns the fragments and the base64 apply script of the app's plan.
func (cluster *Cluster) appPHPConfig(app *App) (s appPHPSizing, poolFragment, iniFragment, script64 string) {
	s = cluster.AppPHPSizing(app)
	poolFragment, iniFragment = renderAppPHPFragments(s, appPHPPool(app))
	script := appPHPApplyScript(s, poolFragment, iniFragment, appPHPPoolDir(app), appPHPIniDir(app))
	return s, poolFragment, iniFragment, base64.StdEncoding.EncodeToString([]byte(script))
}

// appFirstVolumeName is the app's first volume (volume#1 of its service), "" without one.
func appFirstVolumeName(app *App) string {
	if app == nil || app.AppConfig == nil || app.AppConfig.Deployment == nil {
		return ""
	}
	vols := app.AppConfig.Deployment.Storages.Volumes
	if len(vols) == 0 || vols[0] == nil {
		return ""
	}
	return vols[0].Name
}

// openSVCAddAppDynamicConfigSections adds to an app service definition what the PHP
// sizing needs: the two key files installed in the first volume, their mounts in
// container#app, and task#phpconfig. Nothing when the switch is off or the app has no
// volume (the files would have nowhere to live: said in the log).
func (cluster *Cluster) openSVCAddAppDynamicConfigSections(svcsection map[string]map[string]string, app *App) {
	if !cluster.AppDynamicConfigEnabled(app) || (cluster.Conf.ProvType != "docker" && cluster.Conf.ProvType != "podman") {
		return
	}
	volName := appFirstVolumeName(app)
	vol, ok := svcsection["volume#1"]
	if volName == "" || !ok {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"App %s: prov-app-apply-dynamic-config is on but the app has no volume to hold the PHP sizing: not applied", app.Name)
		return
	}
	if !strings.Contains(" "+vol["directories"]+" ", " "+appPHPVolumeDir+" ") {
		vol["directories"] = strings.TrimSpace(vol["directories"] + " " + appPHPVolumeDir)
	}
	vol["configs"] = strings.TrimSpace(vol["configs"] + " " +
		app.Name + "/" + appPHPPoolKey + ":" + appPHPVolumeDir + "/" + appPHPPoolFile + " " +
		app.Name + "/" + appPHPIniKey + ":" + appPHPVolumeDir + "/" + appPHPIniFile)

	if c, ok := svcsection["container#app"]; ok {
		c["volume_mounts"] = strings.TrimSpace(c["volume_mounts"] + " " +
			volName + "/" + appPHPVolumeDir + "/" + appPHPPoolFile + ":" + appPHPPoolDir(app) + "/" + appPHPPoolFile + " " +
			volName + "/" + appPHPVolumeDir + "/" + appPHPIniFile + ":" + appPHPIniDir(app) + "/" + appPHPIniFile)
	}

	task := map[string]string{
		"type":          cluster.Conf.ProvType,
		"image":         "{env.app_img}",
		"netns":         "container#01",
		"pidns":         "container#app",
		"rm":            "true",
		"entrypoint":    "/bin/sh",
		"volume_mounts": volName + "/" + appPHPVolumeDir + ":" + appPHPTaskMount,
		// the script of the resize when replication-manager runs it, the stored one when an
		// operator runs the task by hand (om <svc> instance run --rid task#phpconfig)
		"configs_environment": app.Name + "/" + appPHPConfigKey,
		"command":             `-c 'printf %s "${` + appPHPConfigRunEnv + `:-$` + appPHPConfigKey + `}" | base64 -d | sh'`,
	}
	if app.AppConfig.ProvAppHATopology == "failover" {
		task["shared"] = "true" // it mounts the volume, which a failover app has on one node
	}
	svcsection[appPHPConfigTask] = task
}

// openSVCPushAppDynamicConfigKeys writes the three keys of the PHP sizing into the app's
// config object. Called with the other app keys at provision and by every resize.
func (cluster *Cluster) openSVCPushAppDynamicConfigKeys(svc opensvc.Collector, app *App) (appPHPSizing, string, error) {
	s, poolFragment, iniFragment, script64 := cluster.appPHPConfig(app)
	for _, kv := range [][2]string{{appPHPPoolKey, poolFragment}, {appPHPIniKey, iniFragment}, {appPHPConfigKey, script64}} {
		if err := svc.CreateConfigKeyValue(cluster.Name, app.Name, kv[0], kv[1]); err != nil {
			return s, script64, fmt.Errorf("config key %q: %w", kv[0], err)
		}
	}
	return s, script64, nil
}

// appDynamicConfigNodes are the nodes where an instance of the app runs: every agent of a
// flex app, the node the app was found on for a failover one (all agents when unknown).
func appDynamicConfigNodes(app *App) []string {
	agents := app.GetAppAgents()
	if app.AppConfig.ProvAppHATopology == "failover" && strings.TrimSpace(app.Agent) != "" {
		return []string{strings.TrimSpace(app.Agent)}
	}
	return agents
}

// ApplyAppDynamicConfig makes a running app follow its plan: the keys are rewritten for
// the next start, then task#phpconfig reloads php-fpm on each node. A node the task cannot
// reload (down, cap too small, file not seen) keeps its sizing until its next start; the
// cause is in the task's output on that node. Runs off the monitoring loop (F2).
func (cluster *Cluster) ApplyAppDynamicConfig(app *App) error {
	if !cluster.AppDynamicConfigEnabled(app) {
		return nil
	}
	if cluster.GetOrchestrator() != config.ConstOrchestratorOpenSVC || cluster.Conf.ProvOpensvcUseCollectorAPI {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo,
			"App %s: PHP sizing applies at the next start (no live reload with this orchestrator)", app.Name)
		return nil
	}
	app.dynamicConfigMutex.Lock()
	defer app.dynamicConfigMutex.Unlock()
	svc := cluster.OpenSVCConnect()
	s, script64, err := cluster.openSVCPushAppDynamicConfigKeys(svc, app)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr, "App %s: PHP sizing not written: %s", app.Name, err)
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo,
		"App %s: PHP sizing for %dM/%d cores: pm.max_children=%d start=%d spare=%d-%d memory_limit=%dM opcache=%dM (needs %dM); reloading php-fpm with om %s instance run --rid %s",
		app.Name, s.MemoryMB, s.Cores, s.MaxChildren, s.StartServers, s.MinSpare, s.MaxSpare, s.MemoryLimitMB, s.OpcacheMB, s.RequiredMB, app.ServiceName, appPHPConfigTask)
	var errs []string
	for _, node := range appDynamicConfigNodes(app) {
		if err := svc.RunTask(cluster.Name, app.ServiceName, node, appPHPConfigTask, appPHPConfigRunEnv+"="+script64); err != nil {
			errs = append(errs, node+": "+err.Error())
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
				"App %s: %s not run on %s, the PHP sizing applies at its next start: %s", app.Name, appPHPConfigTask, node, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s: %s", appPHPConfigTask, strings.Join(errs, "; "))
	}
	return nil
}

// onAppPlanChange is called by the setters of the app's memory and cores: the app follows
// its new plan off the API request.
func (app *App) onAppPlanChange() {
	cluster := app.ClusterGroup
	if cluster == nil || !cluster.AppDynamicConfigEnabled(app) {
		return
	}
	go cluster.ApplyAppDynamicConfig(app)
}
