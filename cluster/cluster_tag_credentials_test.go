// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config/manager"
)

// #2017: a credential set at runtime, through the same setters the API calls, survives
// a database tag added or dropped. The tag functions call SetClusterCredentialsFromConfig,
// which rebuilds the secrets from the config maps loaded at start.
func TestTagChangeKeepsRuntimeCredentials(t *testing.T) {
	cl, _ := newTestClusterServer(t)
	cl.ConfigManager = &manager.ConfigManager{} // a save is an event, no I/O
	cl.Conf.DynamicFlagMap = map[string]interface{}{
		"replication-credential":         "repl:loaded-at-start",
		"backup-verification-credential": "verif:loaded-at-start",
	}
	cl.Conf.ImmuableFlagMap = map[string]interface{}{}
	cl.Conf.DefaultFlagMap = map[string]interface{}{}
	cl.Conf.DecryptSecretsFromConfig()

	cl.SetReplicationCredential("repl:set-at-runtime")
	cl.SetBackupVerificationCredential("verif:set-at-runtime")

	check := func(step string) {
		t.Helper()
		for key, want := range map[string]string{
			"replication-credential":         "repl:set-at-runtime",
			"backup-verification-credential": "verif:set-at-runtime",
		} {
			if v := cl.Conf.GetDecryptedValue(key); v != want {
				t.Errorf("%s: %s is %q, want %q", step, key, v, want)
			}
		}
	}
	check("after the setters")
	cl.AddDBTagConfig("localinfile")
	check("database tag added")
	cl.DropDBTagConfig("localinfile")
	check("database tag dropped")
}
