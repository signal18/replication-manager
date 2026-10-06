// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/signal18/replication-manager/config"
)

// Generated password for any app (#1850). A template that needs a secret of its own, with
// no database of the cluster behind it, references {{app.randompassword}}: replication-manager
// generates the password when the app is added, stores it encrypted in the app setting
// app-random-password, and substitutes it. A secret-type variable receives it decrypted.
// A sibling reads it with {{apps.#(name==x).randompassword}}, so two services can share one
// secret (a PostgreSQL standby and its primary). No template carries a password.
//
// A database engine app (prov-app-configurator) that is a monitored server of the cluster
// also brings the cluster its database credential: see adoptEngineAppCredential.

// appRandomPasswordRe matches the app's OWN key, not a sibling's
// ({{apps.#(...).randompassword}} must not generate anything here).
var appRandomPasswordRe = regexp.MustCompile(`\{\{\s*app\.randompassword\s*\}\}`)

func appRandomPasswordWanted(content []byte) bool {
	return appRandomPasswordRe.Match(content)
}

// ApplyAppRandomPassword generates the app's password when it has none. An existing
// value is kept: re-adding or re-rendering an app never changes its secret.
func (cluster *Cluster) ApplyAppRandomPassword(appcnf *config.AppConfig) error {
	if appcnf == nil || strings.TrimSpace(appcnf.AppRandomPassword) != "" {
		return nil
	}
	pass, err := generateAppDbPassword()
	if err != nil {
		return fmt.Errorf("app %s: generating the password: %w", appcnf.AppHost, err)
	}
	appcnf.AppRandomPassword = cluster.Conf.GetEncryptedString(pass)
	return nil
}

// defaultDatabasePassword is the password of the shipped db-servers-credential default
// (root:mariadb).
const defaultDatabasePassword = "mariadb"

// appEngineSuperuser is the superuser name of a database engine app: its own variable
// when the template sets one, else the engine's default.
func appEngineSuperuser(app *App) string {
	switch strings.TrimSpace(app.AppConfig.ProvAppConfigurator) {
	case "postgres":
		if app.AppConfig.Deployment != nil {
			for _, v := range app.AppConfig.Deployment.Variables {
				if v.Name == "POSTGRES_USER" && strings.TrimSpace(v.Value) != "" {
					return strings.TrimSpace(v.Value)
				}
			}
		}
		return "postgres"
	}
	return ""
}

// appIsMonitoredServer tells whether the app is one of the cluster's database servers
// (same host and port): the cluster's database, not an app next to it.
func (cluster *Cluster) appIsMonitoredServer(app *App) bool {
	for _, s := range cluster.Servers {
		if s != nil && s.Port == app.Port && (s.Host == app.Host || s.Name == app.Name) {
			return true
		}
	}
	return false
}

// engineAppOfServer returns the engine app a monitored server runs, nil when none.
func (cluster *Cluster) engineAppOfServer(s *ServerMonitor) *App {
	for _, a := range cluster.Apps {
		if a != nil && a.AppConfig != nil && strings.TrimSpace(a.AppConfig.ProvAppConfigurator) != "" &&
			a.Port == s.Port && (a.Host == s.Host || a.Name == s.Name) {
			return a
		}
	}
	return nil
}

// appIsEngineServer tells whether a monitored server is a database engine app of the cluster.
func (cluster *Cluster) appIsEngineServer(s *ServerMonitor) bool {
	for _, a := range cluster.Apps {
		if a != nil && a.AppConfig != nil && strings.TrimSpace(a.AppConfig.ProvAppConfigurator) != "" &&
			a.Port == s.Port && (a.Host == s.Host || a.Name == s.Name) {
			return true
		}
	}
	return false
}

// allServersAreEngineApps: every monitored server of the cluster is a database engine app
// replication-manager deploys. The cluster's database credential then has no other
// consumer than those apps.
func (cluster *Cluster) allServersAreEngineApps() bool {
	if len(cluster.Servers) == 0 {
		return false
	}
	for _, s := range cluster.Servers {
		if s == nil || !cluster.appIsEngineServer(s) {
			return false
		}
	}
	return true
}

// adoptEngineAppCredential makes the generated password of a database engine app the
// cluster's database credential, when the app is a monitored server of the cluster. The
// monitor, the jobs sidecar and a standby then all use the one password replication-manager
// generated. It applies when the cluster has the shipped default password (or none), or
// when every monitored server is such an app: the credential a new cluster inherits from
// the global configuration belongs to no database there (live, pg-stream 2026-10-05: the
// monitor kept logging in as root). In a cluster that also monitors servers replication-manager
// did not deploy this way, a credential that was set is never replaced. Idempotent, called
// at provisioning and at each app refresh. The password is never logged.
func (cluster *Cluster) adoptEngineAppCredential(app *App) bool {
	if app == nil || app.AppConfig == nil || strings.TrimSpace(app.AppConfig.AppRandomPassword) == "" {
		return false
	}
	user := appEngineSuperuser(app)
	if user == "" || !cluster.appIsMonitoredServer(app) {
		return false
	}
	pass := cluster.Conf.GetDecryptedPassword("app-random-password", app.AppConfig.AppRandomPassword)
	if pass == "" {
		return false
	}
	current := cluster.GetDbPass()
	if current == pass && cluster.GetDbUser() == user {
		return false // already the cluster's credential
	}
	if current != "" && current != defaultDatabasePassword && !cluster.allServersAreEngineApps() {
		return false
	}
	credential := user + ":" + pass
	if cluster.Conf.Secrets == nil {
		cluster.Conf.Secrets = map[string]config.Secret{}
	}
	cluster.Conf.User = credential
	cluster.Conf.Secrets["db-servers-credential"] = config.Secret{Value: credential, OldValue: cluster.Conf.GetDecryptedValue("db-servers-credential")}
	cluster.SetClusterMonitorCredentialsFromConfig()
	cluster.SetReplicationCredential(credential)
	cluster.Save()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "The cluster database credential is now the one generated for the database app %s (user %s)", app.Name, user)
	return true
}
