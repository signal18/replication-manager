// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// rotateGateCluster: a cluster where a rotation outside Vault is allowed, every case
// below breaks one condition
func rotateGateCluster() *Cluster {
	return &Cluster{
		Conf: &config.Config{
			SecretKey:                  []byte("0123456789abcdef"),
			ConfRewrite:                true,
			MonitoringSecretVersioning: true,
			ImmuableFlagMap:            map[string]interface{}{},
			Topology:                   config.TopoUnknown,
		},
		Servers: serverList{&ServerMonitor{State: stateMaster, Conn: &sqlx.DB{}}},
	}
}

// #2042: outside Vault, a rotation keeps the new passwords only in memory unless the
// secret store records them: it is refused with versioning off, and the reason is given.
func TestCanRotatePasswords(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Cluster)
		want   string // "" = allowed, else a part of the refusal
	}{
		{"allowed", func(*Cluster) {}, ""},
		{"versioning off", func(c *Cluster) { c.Conf.MonitoringSecretVersioning = false }, "monitoring-secret-versioning is off"},
		{"versioning on through backup encryption", func(c *Cluster) { c.Conf.MonitoringSecretVersioning = false; c.Conf.BackupEncryption = true }, ""},
		{"vault, versioning off", func(c *Cluster) {
			c.Conf.MonitoringSecretVersioning = false
			c.Conf.VaultServerAddr = "https://vault:8200"
		}, ""},
		// #2227: an active store keeps versions: the file with versioning on, or a vault entry
		{"versioning off, a vault entry enabled", func(c *Cluster) {
			c.Conf.MonitoringSecretVersioning = false
			c.Conf.SecretKvs = []config.SecretKvConfig{{Name: "vault-a", Plugin: config.ConstSecretKvPluginVault, Enabled: true}}
		}, ""},
		{"versioning off, the vault entry off", func(c *Cluster) {
			c.Conf.MonitoringSecretVersioning = false
			c.Conf.SecretKvs = []config.SecretKvConfig{{Name: "vault-a", Plugin: config.ConstSecretKvPluginVault}}
		}, "no credential store keeps versions"},
		{"vault entry enabled, db credential in a file", func(c *Cluster) {
			c.Conf.SecretKvs = []config.SecretKvConfig{{Name: "vault-a", Plugin: config.ConstSecretKvPluginVault, Enabled: true}}
			c.Conf.ImmuableFlagMap["db-servers-credential"] = "root:x"
		}, ""},
		{"replication stopped, a database engine entry", func(c *Cluster) {
			replica := &ServerMonitor{State: stateSlave, Conn: &sqlx.DB{}}
			c.Servers = append(c.Servers, replica)
			c.slaves = serverList{replica}
			c.Conf.SecretKvs = []config.SecretKvConfig{{Name: "vault-a", Plugin: config.ConstSecretKvPluginVault, Enabled: true, Settings: map[string]interface{}{"mode": config.VaultDbEngine}}}
		}, ""},
		{"no encryption key", func(c *Cluster) { c.Conf.SecretKey = nil }, "no encryption key"},
		{"config not saved", func(c *Cluster) { c.Conf.ConfRewrite = false }, "monitoring-save-config is off"},
		// #2227 rule 9: a credential set in a config file is a starting value, rotated as versions
		{"db credential in a file", func(c *Cluster) { c.Conf.ImmuableFlagMap["db-servers-credential"] = "root:x" }, ""},
		{"replication credential in a file", func(c *Cluster) { c.Conf.ImmuableFlagMap["replication-credential"] = "repl:x" }, ""},
		{"db credential in a file, no versioned store", func(c *Cluster) {
			c.Conf.ImmuableFlagMap["db-servers-credential"] = "root:x"
			c.Conf.MonitoringSecretVersioning = false
		}, "no credential store keeps versions"},
		{"a database down", func(c *Cluster) { c.Servers = append(c.Servers, &ServerMonitor{State: stateFailed}) }, "databases are down"},
		{"replication stopped on a replica", func(c *Cluster) {
			replica := &ServerMonitor{State: stateSlave, Conn: &sqlx.DB{}, Replications: []dbhelper.SlaveStatus{{
				SlaveIORunning:  sql.NullString{String: "Yes", Valid: true},
				SlaveSQLRunning: sql.NullString{String: "No", Valid: true},
			}}}
			c.Servers = append(c.Servers, replica)
			c.slaves = serverList{replica}
		}, "replication is not running"},
		{"replication stopped, vault database engine", func(c *Cluster) {
			replica := &ServerMonitor{State: stateSlave, Conn: &sqlx.DB{}}
			c.Servers = append(c.Servers, replica)
			c.slaves = serverList{replica}
			c.Conf.VaultServerAddr = "https://vault:8200"
			c.Conf.VaultMode = VaultDbEngine
		}, ""}, // Vault rotates through its own engine, replication is not touched
		{"replication stopped, vault kv", func(c *Cluster) {
			replica := &ServerMonitor{State: stateSlave, Conn: &sqlx.DB{}}
			c.Servers = append(c.Servers, replica)
			c.slaves = serverList{replica}
			c.Conf.VaultServerAddr = "https://vault:8200"
			c.Conf.VaultMode = VaultConfigStoreV2
		}, "replication is not running"}, // repman changes the replicas' passwords itself
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cl := rotateGateCluster()
			tc.change(cl)
			err := cl.CanRotatePasswords()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want a refusal containing %q", err, tc.want)
			}
		})
	}
}

// RotatePasswords itself refuses (MCP or any other caller), and changes nothing
func TestRotatePasswordsRefusedChangesNothing(t *testing.T) {
	cl := rotateGateCluster()
	cl.Conf.MonitoringSecretVersioning = false
	cl.Conf.Secrets = config.NewSecrets(map[string]config.Secret{"db-servers-credential": {Value: "root:before"}})
	if err := cl.RotatePasswords(); err == nil {
		t.Fatal("rotation ran with versioning off")
	}
	if v := cl.Conf.Secrets.Get("db-servers-credential").Value; v != "root:before" {
		t.Fatalf("credential changed to %q", v)
	}
}
