// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"
	"strings"

	"github.com/signal18/replication-manager/share"
)

// App jobs sidecar (#1850): an app whose configurator engine ships a jobs script
// (share/scripts/<engine>_job.sh, PostgreSQL today) gets, next to its container:
//
//   - an init container that downloads replication-manager-cli into the `jobs` directory
//     of the app's first volume, the way the database init container does (same URL,
//     refreshed when the server version changes);
//   - a `jobs` sidecar from the app's own image (the engine tools are there), in the same
//     network namespace, that runs the script: it polls the jobs table replication-manager
//     fills and streams each task's output back with `replication-manager-cli stream`.
//
// The script travels as the config key appJobsScriptKey of the app, like the configurator
// script; the app's variables and secrets are the sidecar's environment.

const (
	appJobsScriptKey = "APP_JOBS_SCRIPT"
	appJobsDir       = "jobs"
	appJobsMount     = "/jobs"
)

// appStartScriptKey carries the engine's start script (share/scripts/<engine>_start.sh):
// what the engine container runs before the engine itself -- write the rendered
// configuration, seed a standby -- so the template's start command stays one line and the
// logic is versioned with replication-manager.
const appStartScriptKey = "APP_START_SCRIPT"

// appStartScript returns the start script of the app's engine, "" when it has none.
func appStartScript(app *App) string {
	return appEngineScript(app, "start")
}

// appEngineScript reads share/scripts/<engine>_<kind>.sh of the app's configurator engine.
func appEngineScript(app *App, kind string) string {
	if app == nil || app.AppConfig == nil {
		return ""
	}
	engine := strings.TrimSpace(app.AppConfig.ProvAppConfigurator)
	if !appConfiguratorEngineRe.MatchString(engine) {
		return ""
	}
	b, err := share.EmbededDbModuleFS.ReadFile("scripts/" + engine + "_" + kind + ".sh")
	if err != nil {
		return ""
	}
	return string(b)
}

// defaultDatabasePassword is the password of the shipped db-servers-credential default
// (root:mariadb): a database engine is never provisioned with it.
const defaultDatabasePassword = "mariadb"

// appEngineCredentialEnv names the environment variables an engine image reads its
// superuser from. An engine app does not carry its own password: it is the CLUSTER's
// database credential (db-servers-credential), the one the monitor, the jobs sidecar and
// a standby's replication connection all use, delivered as a secret.
func appEngineCredentialEnv(app *App) (userKey, passKey string) {
	if app == nil || app.AppConfig == nil {
		return "", ""
	}
	switch strings.TrimSpace(app.AppConfig.ProvAppConfigurator) {
	case "postgres":
		return "POSTGRES_USER", "POSTGRES_PASSWORD"
	}
	return "", ""
}

// appEngineCredentialError refuses to provision an engine app on the shipped default
// password (or none): set db-servers-credential first, the dashboard generates one.
func (cluster *Cluster) appEngineCredentialError(app *App) error {
	if _, passKey := appEngineCredentialEnv(app); passKey == "" {
		return nil
	}
	if pass := cluster.GetDbPass(); pass == "" || pass == defaultDatabasePassword {
		return fmt.Errorf("app %s is a database engine and the cluster database password is the default: set db-servers-credential before provisioning it", app.Name)
	}
	return nil
}

// appJobsScript returns the jobs script of the app's engine, "" when it has none.
func appJobsScript(app *App) string {
	if app == nil || app.AppConfig == nil {
		return ""
	}
	engine := strings.TrimSpace(app.AppConfig.ProvAppConfigurator)
	if !appConfiguratorEngineRe.MatchString(engine) {
		return ""
	}
	b, err := share.EmbededDbModuleFS.ReadFile("scripts/" + engine + "_job.sh")
	if err != nil {
		return ""
	}
	return string(b)
}

// appJobsVolumeMount is the mount of the jobs directory, on the app's first volume; ""
// when the app has no volume (no jobs sidecar then: the client has nowhere to land).
func appJobsVolumeMount(app *App) string {
	if app.AppConfig.Deployment == nil {
		return ""
	}
	vols := app.AppConfig.Deployment.Storages.Volumes
	if len(vols) == 0 || vols[0] == nil || vols[0].Name == "" {
		return ""
	}
	return vols[0].Name + "/" + appJobsDir + ":" + appJobsMount
}

// appJobsInitCommand downloads the client when it is missing or the server moved to
// another version. /api/version and the static binary need no token.
func appJobsInitCommand() string {
	return `-c 'U=$REPLICATION_MANAGER_URL; V=$(wget --no-check-certificate -q -O- $U/api/version); ` +
		`if [ "$V" != "$(cat ` + appJobsMount + `/version 2>/dev/null)" ] || [ ! -x ` + appJobsMount + `/replication-manager-cli ]; then ` +
		`wget --no-check-certificate -q -O ` + appJobsMount + `/replication-manager-cli.new $U/static/configurator/bin/replication-manager-cli && ` +
		`mv ` + appJobsMount + `/replication-manager-cli.new ` + appJobsMount + `/replication-manager-cli && ` +
		`chmod 0755 ` + appJobsMount + `/replication-manager-cli && echo "$V" > ` + appJobsMount + `/version; fi'`
}

// openSVCAddAppJobsSections adds the jobs directory, the client init container and the
// jobs sidecar to an app service definition, when the app's engine has a jobs script.
func (cluster *Cluster) openSVCAddAppJobsSections(svcsection map[string]map[string]string, app *App, containernum *int) {
	if appJobsScript(app) == "" || (cluster.Conf.ProvType != "docker" && cluster.Conf.ProvType != "podman") {
		return
	}
	mount := appJobsVolumeMount(app)
	if mount == "" {
		return
	}
	// the first volume is volume#1 (OpenSVCGetAppVolumeSections numbers them in order)
	if vol, ok := svcsection["volume#1"]; ok {
		if !strings.Contains(" "+vol["directories"]+" ", " "+appJobsDir+" ") {
			vol["directories"] = strings.TrimSpace(vol["directories"] + " " + appJobsDir)
		}
	}

	*containernum++
	init := map[string]string{
		"type":                "docker",
		"image":               "alpine",
		"netns":               "container#01",
		"detach":              "false",
		"rm":                  "true",
		"optional":            "true",
		"start_timeout":       "300s",
		"entrypoint":          "/bin/sh",
		"configs_environment": "env/REPLICATION_MANAGER_URL",
		"volume_mounts":       "/etc/localtime:/etc/localtime:ro " + mount,
		"command":             appJobsInitCommand(),
	}
	svcsection[fmt.Sprintf("container#%02dinitjobs", *containernum)] = init

	svcsection["container#jobs"] = map[string]string{
		"type":                cluster.Conf.ProvType,
		"image":               "{env.app_img}",
		"netns":               "container#01",
		"detach":              "true",
		"rm":                  "true",
		"entrypoint":          "/bin/bash",
		"configs_environment": "env/REPLICATION_MANAGER_URL " + app.GetOpenSVCDeploymentAppEnv("env"),
		"secrets_environment": app.GetOpenSVCDeploymentAppEnv("secret"),
		// who this server is for replication-manager: the cluster, and the host and port
		// the monitor knows it by (the service name in the cluster namespace)
		"environment": "REPLICATION_MANAGER_CLUSTER_NAME={namespace} REPLICATION_MANAGER_HOST_NAME={svcname}.{namespace}.svc.{clustername} REPLICATION_MANAGER_HOST_PORT=" + app.Port,
		// the client, the app's own mounts read-only use (the data directory, for its size)
		// and ONLY this service's cgroup slice, read-only, like the database jobs container:
		// the script reports memory, cpu, io, disk and network from it
		"volume_mounts": strings.TrimSpace("/etc/localtime:/etc/localtime:ro " + mount + " " + cluster.GetOpenSVCDeploymentPathMapping(app) +
			" /sys/fs/cgroup/opensvc.slice/opensvc-ns.{namespace}.slice/opensvc-ns.{namespace}-svc.{svcname}.slice:/svc-cgroup:ro"),
		"command": "-c 'printenv " + appJobsScriptKey + " > /tmp/app_jobs; exec bash /tmp/app_jobs'",
	}
}
