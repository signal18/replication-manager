// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// #2017: adding or dropping a tag never touches the credentials. Since b5a56fddd (2023)
// the tag functions called SetClusterCredentialsFromConfig, a leftover of the former
// SetClusterVariablesFromConfig: every tag change rebuilt the secrets from the config
// maps and brought an older password back over the one set at runtime.
func TestTagChangeKeepsRuntimeCredentials(t *testing.T) {
	cl := &Cluster{Conf: &config.Config{
		Secrets: map[string]config.Secret{
			"db-servers-credential":  {Value: "root:set-at-runtime"},
			"replication-credential": {Value: "repl:set-at-runtime"},
		},
		DynamicFlagMap:  map[string]interface{}{"db-servers-credential": "root:older"},
		ImmuableFlagMap: map[string]interface{}{"replication-credential": "repl:older"},
		DefaultFlagMap:  map[string]interface{}{},
	}}
	check := func(step string) {
		t.Helper()
		if v := cl.Conf.Secrets["db-servers-credential"].Value; v != "root:set-at-runtime" {
			t.Fatalf("%s: db-servers-credential became %q", step, v)
		}
		if v := cl.Conf.Secrets["replication-credential"].Value; v != "repl:set-at-runtime" {
			t.Fatalf("%s: replication-credential became %q", step, v)
		}
	}
	cl.AddDBTagConfig("mcs")
	check("database tag added")
	cl.DropDBTagConfig("mcs")
	check("database tag dropped")
	cl.AddProxyTag("ssl")
	check("proxy tag added")
	cl.DropProxyTag("ssl")
	check("proxy tag dropped")
}
