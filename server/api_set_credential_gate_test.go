// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"errors"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// #2048: a refused credential leaves the cluster on its current one
func TestSetCredentialRefusedKeepsCurrent(t *testing.T) {
	repman := &ReplicationManager{}
	for _, key := range []string{"db-servers-credential", "replication-credential"} {
		cl := &cluster.Cluster{Conf: &config.Config{
			User:            "root:current",
			RplUser:         "repl:current",
			ImmuableFlagMap: map[string]interface{}{key: "x:y"},
			ConfRewrite:     true,
		}}
		err := repman.setClusterSetting(cl, key, "u:new")
		if !errors.Is(err, cluster.ErrCredentialChangeRefused) {
			t.Fatalf("%s: got %v, want a refusal", key, err)
		}
		if cl.Conf.User != "root:current" || cl.Conf.RplUser != "repl:current" {
			t.Fatalf("%s: the credential changed: %s / %s", key, cl.Conf.User, cl.Conf.RplUser)
		}
	}
}
