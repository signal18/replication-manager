// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// #2048: a credential set through the API is refused when it would be lost at the next
// restart, and the reason is given.
func TestCanSetCredential(t *testing.T) {
	cases := []struct {
		name   string
		key    string
		change func(*Cluster)
		want   string // "" = allowed, else a part of the refusal
	}{
		{"allowed", "db-servers-credential", func(*Cluster) {}, ""},
		// #2227 rule 9: a credential set in a config file takes a change as a version; #2269:
		// refused when no store keeps versions
		{"db credential in a file", "db-servers-credential", func(c *Cluster) { c.Conf.ImmuableFlagMap["db-servers-credential"] = "root:x" }, ""},
		{"replication credential in a file", "replication-credential", func(c *Cluster) { c.Conf.ImmuableFlagMap["replication-credential"] = "repl:x" }, ""},
		{"credential in a file, no versioned store", "replication-credential", func(c *Cluster) {
			c.Conf.ImmuableFlagMap["replication-credential"] = "repl:x"
			c.Conf.MonitoringSecretVersioning = false
		}, "immutable"},
		{"another credential in a file", "db-servers-credential", func(c *Cluster) { c.Conf.ImmuableFlagMap["replication-credential"] = "repl:x" }, ""},
		// #2227 rule 1: the change is a version in the versioned stores, never in the config
		{"config not saved, versioning on", "replication-credential", func(c *Cluster) { c.Conf.ConfRewrite = false }, ""},
		// #2269: no store keeps versions: the config is the store, while it is saved
		{"no versioned store", "replication-credential", func(c *Cluster) { c.Conf.MonitoringSecretVersioning = false }, ""},
		{"no versioned store, config not saved", "replication-credential", func(c *Cluster) {
			c.Conf.MonitoringSecretVersioning = false
			c.Conf.ConfRewrite = false
		}, "monitoring-save-config is off"},
		{"no versioned store, no encryption key", "replication-credential", func(c *Cluster) {
			c.Conf.MonitoringSecretVersioning = false
			c.Conf.SecretKey = nil
		}, "no encryption key"},
		{"no versioned store, a vault entry", "replication-credential", func(c *Cluster) {
			c.Conf.MonitoringSecretVersioning = false
			c.Conf.SecretKvs = []config.SecretKvConfig{{Name: "vault-a", Plugin: config.ConstSecretKvPluginVault, Enabled: true}}
		}, ""},
		// F9: a version is kept encrypted, never in cleartext in a file the config push sends
		{"no encryption key", "replication-credential", func(c *Cluster) { c.Conf.SecretKey = nil }, "no encryption key"},
		// the key is the local file's: versioning off, a vault entry takes the change without it
		{"no encryption key, versioning off, a vault entry", "replication-credential", func(c *Cluster) {
			c.Conf.SecretKey = nil
			c.Conf.MonitoringSecretVersioning = false
			c.Conf.SecretKvs = []config.SecretKvConfig{{Name: "vault-a", Plugin: config.ConstSecretKvPluginVault, Enabled: true}}
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Cluster{Conf: &config.Config{ConfRewrite: true, MonitoringSecretVersioning: true, SecretKey: []byte("0123456789abcdef"), ImmuableFlagMap: map[string]interface{}{}}}
			tc.change(c)
			err := c.CanSetCredential(tc.key)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			if !errors.Is(err, ErrCredentialChangeRefused) {
				t.Fatalf("%v does not wrap ErrCredentialChangeRefused", err)
			}
		})
	}
}

// #2050: the GUI gets each credential's user, source and refusal, never a password
func TestGetCredentialsInfo(t *testing.T) {
	c := &Cluster{Conf: &config.Config{
		ConfRewrite:                true,
		MonitoringSecretVersioning: true,
		SecretKey:                  []byte("0123456789abcdef"),
		ImmuableFlagMap:            map[string]interface{}{"replication-credential": "repl:x"},
		Secrets: config.NewSecrets(map[string]config.Secret{
			"db-servers-credential":  {Value: "monitor:secret-db"},
			"replication-credential": {Value: "repl:secret-rpl"},
		}),
	}}
	infos := c.GetCredentialsInfo()
	if len(infos) != 2 {
		t.Fatalf("got %d credentials", len(infos))
	}
	db, rpl := infos[0], infos[1]
	// the source is the store the value comes from (#2075): no store holds it, the default
	if db.Key != "db-servers-credential" || db.User != "monitor" || db.Source != "default" || db.Refusal != "" {
		t.Fatalf("db: %+v", db)
	}
	c.Conf.DynamicFlagMap = map[string]interface{}{"db-servers-credential": "monitor:secret-db"}
	if s := c.GetCredentialsInfo()[0].Source; s != "dynamic-config" {
		t.Fatalf("held by the dynamic config: source %q", s)
	}
	if rpl.User != "repl" || rpl.Source != "config-file" || rpl.Refusal != "" {
		t.Fatalf("replication: %+v", rpl)
	}
	for _, i := range infos {
		if strings.Contains(i.User+i.Source+i.Refusal, "secret-") {
			t.Fatalf("a password leaks: %+v", i)
		}
	}
	c.Conf.VaultServerAddr = "https://vault:8200"
	c.Conf.User = "repman/monitor"
	if s := c.GetCredentialsInfo()[0].Source; s != "vault" {
		t.Fatalf("vault path: source %q", s)
	}
}
