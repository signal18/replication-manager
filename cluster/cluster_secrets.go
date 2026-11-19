// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"

	"github.com/signal18/replication-manager/config"
)

// The cluster's secrets, read in clear for their owner (GET /api/clusters/{cluster}/secrets, the
// cluster's Secrets panel, grant cluster-show-secrets): the credentials replication-manager
// generated or holds for the cluster's databases, proxies, Cloud18 users and apps, gathered in one
// place for a self-service user after provisioning.
//
// A PURE READ. Nothing here changes a credential, generates one, or calls a rotation: a client
// may use one of these passwords in its own code, and a rotation it did not ask for breaks its
// production. A value that cannot be read is left out, never replaced. TestClusterSecretsArePureRead
// checks that this file and the handler reach no rotation or setter.
//
// The instance's own secrets (API users, git, registry, Vault, OpenSVC, SSH, mail, alert tokens,
// cloud keys) are not here: they are the operator's, not the cluster owner's.

// ClusterSecret is one credential of the cluster: its config key, what it is for, the login
// when the setting holds one ("user:password"), and the password.
type ClusterSecret struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	User     string `json:"user,omitempty"`
	Password string `json:"password"`
}

// clusterSecretKey is a cluster credential shown to its owner: hasUser when the setting is
// "user:password", on when it applies to this cluster (a proxy that is off is not listed).
type clusterSecretKey struct {
	key     string
	label   string
	hasUser bool
	on      func(c *config.Config) bool
}

func always(*config.Config) bool { return true }

// clusterSecretKeys are the cluster credentials shown, in the panel's order.
var clusterSecretKeys = []clusterSecretKey{
	{"db-servers-credential", "Database administration user", true, always},
	{"replication-credential", "Replication user", true, always},
	{"monitoring-write-heartbeat-credential", "Heartbeat user", true, always},
	{"cloud18-sponsor-user-credentials", "Sponsor user", true, always},
	{"cloud18-dba-user-credentials", "DBA user", true, always},
	{"proxysql-password", "ProxySQL administration password", false, func(c *config.Config) bool { return c.ProxysqlOn }},
	{"haproxy-password", "HAProxy statistics password", false, func(c *config.Config) bool { return c.HaproxyOn }},
	{"maxscale-pass", "MaxScale administration password", false, func(c *config.Config) bool { return c.MxsOn }},
	{"shardproxy-credential", "ShardProxy user", true, func(c *config.Config) bool { return c.MdbsProxyOn }},
	{"myproxy-password", "MyProxy password", false, func(c *config.Config) bool { return c.MyproxyOn }},
	{"mysqlrouter-pass", "MySQL Router password", false, func(c *config.Config) bool { return c.MysqlRouterOn }},
	{"backup-restic-password", "Backup repository (restic) password", false, always},
}

// ClusterSecretKeys is the list of config keys GetClusterSecrets may return, apps excluded.
func ClusterSecretKeys() []string {
	out := make([]string, 0, len(clusterSecretKeys))
	for _, k := range clusterSecretKeys {
		out = append(out, k.key)
	}
	return out
}

// splitUserPassword splits a "user:password" setting at its first ':'; a value without one is
// a password alone.
func splitUserPassword(v string) (string, string) {
	if i := strings.Index(v, ":"); i >= 0 {
		return v[:i], v[i+1:]
	}
	return "", v
}

// GetClusterSecrets returns the cluster's credentials that are set, in clear, then the database
// user of each app that has one. A read of the in-memory secrets: no I/O, no change.
func (cluster *Cluster) GetClusterSecrets() []ClusterSecret {
	conf := cluster.Conf
	out := []ClusterSecret{}
	for _, k := range clusterSecretKeys {
		if !k.on(conf) {
			continue
		}
		v := conf.GetDecryptedValue(k.key)
		if v == "" {
			continue
		}
		s := ClusterSecret{Key: k.key, Label: k.label, Password: v}
		if k.hasUser {
			s.User, s.Password = splitUserPassword(v)
		}
		if k.key == "proxysql-password" {
			s.User = conf.GetDecryptedValue("proxysql-user")
		}
		if s.Password == "" {
			continue
		}
		out = append(out, s)
	}
	for _, app := range cluster.appsSnapshot() {
		if s, ok := cluster.appDbSecret(app); ok {
			out = append(out, s)
		}
	}
	return out
}

// appDbSecret is the database user of app, when it has one with a password; read under the
// app's lock, as every reader of its AppConfig.
func (cluster *Cluster) appDbSecret(app *App) (ClusterSecret, bool) {
	if app == nil {
		return ClusterSecret{}, false
	}
	app.Lock()
	cnf := app.AppConfig
	var user, enc string
	if cnf != nil {
		user, enc = cnf.AppDbUser, cnf.AppDbPass
	}
	name := app.Name
	app.Unlock()
	if enc == "" {
		return ClusterSecret{}, false
	}
	pass := cluster.Conf.GetDecryptedPassword("app-db-pass", enc)
	if pass == "" {
		return ClusterSecret{}, false
	}
	return ClusterSecret{Key: "app-db-pass", Label: "Application database user of " + name, User: user, Password: pass}, true
}
