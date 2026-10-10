// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/share"
	"github.com/signal18/replication-manager/utils/misc"
)

// App configurator (#1850, first step): an app template names an engine with
// prov-app-configurator; the engine's moduleset (collector export, embedded as
// share/opensvc/moduleset_<engine>.svc.mrm.db.json) is rendered with tokens computed
// from the APP's plan (its memory, cores, iops) and handed to the container as one
// config key, appConfiguratorScriptKey: a shell script that writes every file at its
// path. The template's start command runs it before the engine, so a resize followed
// by a restart re-renders the files. Only the unfiltered rulesets are rendered: tags
// are the next step.

const (
	appConfiguratorScriptKey  = "APP_CONFIGURATOR_SCRIPT"
	appConfiguratorPathPrefix = "%%ENV:SVC_CONF_ENV_BASE_DIR%%/%%ENV:POD%%"
)

var (
	appConfiguratorEngineRe = regexp.MustCompile(`^[a-z0-9]+$`)
	appConfiguratorTokenRe  = regexp.MustCompile(`%%ENV:[A-Z0-9_]+%%`)
)

type appConfiguratorFile struct {
	Path    string
	Content string
}

// loadAppConfiguratorModule reads the embedded moduleset of an engine.
func loadAppConfiguratorModule(engine string) (*config.Compliance, error) {
	if !appConfiguratorEngineRe.MatchString(engine) {
		return nil, fmt.Errorf("prov-app-configurator %q: an engine name such as postgres is expected", engine)
	}
	file := "opensvc/moduleset_" + engine + ".svc.mrm.db.json"
	b, err := share.EmbededDbModuleFS.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("prov-app-configurator %q: no moduleset %s", engine, file)
	}
	module := &config.Compliance{}
	if err := json.Unmarshal(b, module); err != nil {
		return nil, fmt.Errorf("prov-app-configurator %q: %s does not parse: %w", engine, file, err)
	}
	return module, nil
}

// appConfiguratorSizing derives the sizing tokens from an app plan. The token names are
// the ones the moduleset was written with (the MariaDB names); the values follow the
// usual split of a dedicated instance: a quarter of the memory for the shared cache, a
// quarter shared between the sessions, a sixteenth for maintenance.
func appConfiguratorSizing(memMB, cores, iops int) map[string]string {
	if memMB < 256 {
		memMB = 256
	}
	if cores < 1 {
		cores = 1
	}
	if iops < 1 {
		iops = 200
	}
	connections := 100
	if memMB < 1024 {
		connections = 50
	}
	clamp := func(v, lo, hi int) int {
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}
	s := strconv.Itoa
	return map[string]string{
		"%%ENV:SVC_CONF_ENV_MAX_CONNECTIONS%%":         s(connections),
		"%%ENV:SVC_CONF_ENV_INNODB_BUFFER_POOL_SIZE%%": s(memMB / 4),
		"%%ENV:SVC_CONF_ENV_JOIN_BUFFER_SIZE%%":        s(clamp(memMB/4/connections, 1, 64)),
		"%%ENV:SVC_CONF_ENV_TMP_TABLE_SIZE%%":          s(clamp(memMB/128, 8, 64)),
		"%%ENV:SVC_CONF_ENV_MAX_SESSION_MEM_USED%%":    s(clamp(memMB/16, 16, 2048)),
		"%%ENV:SVC_CONF_ENV_MAX_CORES%%":               s(clamp(cores, 2, 64)),
		"%%ENV:NODES_CPU_CORES%%":                      s(cores),
		"%%ENV:SVC_CONF_ENV_INNODB_READ_IO_THREADS%%":  s(clamp(iops/50, 1, 256)),
		"%%ENV:SVC_CONF_ENV_INNODB_WRITE_IO_THREADS%%": s(clamp(iops/50, 1, 256)),
		"%%ENV:SVC_CONF_ENV_INNODB_PURGE_THREADS%%":    s(clamp(cores, 1, 5)),
		"%%ENV:CHECKPOINTIOPS%%":                       s(iops),
		"%%ENV:SVC_CONF_ENV_MAX_IOPS%%":                s(iops * 2),
	}
}

// renderAppConfiguratorFiles renders the file variables of the engine's unfiltered
// rulesets. A token the app cannot resolve refuses the render: an empty value in a
// configuration file is worse than no file.
func renderAppConfiguratorFiles(module *config.Compliance, engine string, env map[string]string) ([]appConfiguratorFile, error) {
	type fileVar struct {
		Path    string `json:"path"`
		Content string `json:"fmt"`
	}
	files := []appConfiguratorFile{}
	missing := map[string]bool{}
	for _, rule := range module.Rulesets {
		if !strings.HasPrefix(rule.Name, engine+".svc.mrm.db.cnf") || rule.Filter != "" {
			continue
		}
		for _, variable := range rule.Variables {
			if variable.Class != "file" {
				continue
			}
			var f fileVar
			if err := json.Unmarshal([]byte(variable.Value), &f); err != nil {
				return nil, fmt.Errorf("moduleset %s variable %s does not parse: %w", engine, variable.Name, err)
			}
			if !strings.HasPrefix(f.Path, appConfiguratorPathPrefix+"/") || strings.HasSuffix(f.Path, "/") {
				continue
			}
			fpath := path.Clean(strings.TrimPrefix(f.Path, appConfiguratorPathPrefix))
			for _, token := range appConfiguratorTokenRe.FindAllString(fpath+f.Content, -1) {
				if _, ok := env[token]; !ok {
					missing[token] = true
				}
			}
			files = append(files, appConfiguratorFile{Path: misc.ExtractKey(fpath, env), Content: misc.ExtractKey(f.Content, env)})
		}
	}
	if len(missing) > 0 {
		tokens := make([]string, 0, len(missing))
		for t := range missing {
			tokens = append(tokens, t)
		}
		sort.Strings(tokens)
		return nil, fmt.Errorf("moduleset %s uses tokens an app does not provide: %s", engine, strings.Join(tokens, ", "))
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// appConfiguratorScript is the POSIX shell script that writes the rendered files. Each
// file travels in a quoted here-document, so nothing in a content is expanded.
func appConfiguratorScript(files []appConfiguratorFile) string {
	var b strings.Builder
	b.WriteString("set -e\n")
	for i, f := range files {
		marker := fmt.Sprintf("__REPMAN_CONFIGURATOR_EOF_%d__", i)
		fmt.Fprintf(&b, "mkdir -p '%s'\ncat > '%s' <<'%s'\n%s", path.Dir(f.Path), f.Path, marker, f.Content)
		if !strings.HasSuffix(f.Content, "\n") {
			b.WriteString("\n")
		}
		b.WriteString(marker + "\n")
	}
	return b.String()
}

// appConfiguratorEnv is the token map of one app: its identity plus the sizing from
// its own plan.
func (cluster *Cluster) appConfiguratorEnv(app *App) map[string]string {
	memMB, _ := config.ParseUnitMeasurementToInt("M,bytes,required", cluster.GetAppMemory(app.AppConfig), true)
	cores, _ := strconv.Atoi(strings.TrimSpace(cluster.GetAppCores(app.AppConfig)))
	iops, _ := strconv.Atoi(strings.TrimSpace(app.AppConfig.ProvAppDiskIops))
	env := appConfiguratorSizing(memMB, cores, iops)
	env["%%ENV:SVC_NAMESPACE%%"] = cluster.Name
	env["%%ENV:SVC_NAME%%"] = app.Name
	env["%%ENV:SERVER_PORT%%"] = app.Port
	return env
}

// engineServerOfApp returns the monitored server an engine app runs, nil when the app is
// not a server of the cluster (a PostgreSQL that is an application's own store).
func (cluster *Cluster) engineServerOfApp(app *App) *ServerMonitor {
	if app == nil || app.AppConfig == nil || strings.TrimSpace(app.AppConfig.ProvAppConfigurator) == "" {
		return nil
	}
	for _, s := range cluster.Servers {
		if s != nil && s.Port == app.Port && (s.Host == app.Host || s.Name == app.Name) {
			return s
		}
	}
	return nil
}

// AppConfiguratorScript renders the configuration of an app that names an engine.
//
// A monitored server renders from the DATABASE configurator (server.GetEnv(), the prov-db-*
// plan): the database plan is the source of truth for a server's sizing, its moduleset and
// its DBU; the app plan only initialized it (initDBSizingFromEngineApp). An engine app that
// is not a server (an application's own store) renders from its app plan.
func (cluster *Cluster) AppConfiguratorScript(app *App) (string, error) {
	engine := strings.TrimSpace(app.AppConfig.ProvAppConfigurator)
	module, err := loadAppConfiguratorModule(engine)
	if err != nil {
		return "", err
	}
	env := cluster.appConfiguratorEnv(app)
	if server := cluster.engineServerOfApp(app); server != nil {
		env = server.GetEnv()
		env["%%ENV:SVC_NAMESPACE%%"] = cluster.Name
		env["%%ENV:SVC_NAME%%"] = app.Name
		env["%%ENV:SERVER_PORT%%"] = app.Port
	}
	files, err := renderAppConfiguratorFiles(module, engine, env)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("moduleset %s renders no file for an app", engine)
	}
	return appConfiguratorScript(files), nil
}

// initDBSizingFromEngineApp initializes the database plan of the cluster from the plan of
// the first engine app that becomes one of its servers: prov-db-memory, prov-db-cpu-cores,
// prov-db-disk-size, prov-db-disk-iops and prov-db-agents. Once only: with an engine server
// already there, the database plan is the one in force and the app plan is not consulted.
func (cluster *Cluster) initDBSizingFromEngineApp(app *App) {
	server := cluster.engineServerOfApp(app)
	if server == nil {
		return
	}
	for _, s := range cluster.Servers {
		if s != nil && s != server && cluster.appIsEngineServer(s) {
			return
		}
	}
	// a value defined in the cluster's configuration file (immutable) is the operator's:
	// the file would win again at the next restart, so it is kept and said
	set := func(key, v string, apply func(string)) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if cluster.IsVariableImmutable(key) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "%s is defined in the cluster configuration file: kept, the plan of %s would have set %s", key, app.Name, v)
			return
		}
		apply(v)
	}
	set("prov-db-memory", cluster.GetAppMemory(app.AppConfig), cluster.SetDBMemorySize)
	set("prov-db-cpu-cores", cluster.GetAppCores(app.AppConfig), cluster.SetDBCores)
	set("prov-db-disk-size", cluster.GetAppDisk(app.AppConfig), cluster.SetDBDiskSize)
	set("prov-db-disk-iops", cluster.GetAppDiskIops(app.AppConfig), cluster.SetDBDiskIOPS)
	set("prov-db-agents", cluster.GetAppAgents(app.AppConfig), func(v string) { cluster.SetProvDbAgents(v) })
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Database plan of the cluster initialized from the plan of %s: memory %s, cores %s, disk %s, iops %s, agents %s", app.Name, cluster.Conf.ProvMem, cluster.Conf.ProvCores, cluster.Conf.ProvDisk, cluster.Conf.ProvIops, cluster.Conf.ProvAgents)
}
