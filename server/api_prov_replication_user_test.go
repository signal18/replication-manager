// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// #2360: prov-db-create-replication-user is reachable by both setting routes, the value one
// (the API and the regtest runner's ConfigVariableMustSet) and the switch one (the GUI).
func TestProvDBCreateReplicationUserSetting(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.Secrets = config.NewSecrets(nil)
	cl.ConfigManager = newConfigManagerForTest()
	repman := newTestRepmanWithCluster(t, cl.Name, cl)

	cl.Conf.ProvDBCreateReplicationUser = false
	if err := repman.setClusterSetting(cl, "prov-db-create-replication-user", "true"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if !cl.Conf.ProvDBCreateReplicationUser {
		t.Fatal("set true left it off")
	}
	if err := repman.switchClusterSettings(cl, "prov-db-create-replication-user"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if cl.Conf.ProvDBCreateReplicationUser {
		t.Fatal("switch left it on")
	}
}
