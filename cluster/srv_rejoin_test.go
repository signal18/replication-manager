// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "testing"

func TestResolveLiveDumpSource_PrefersBackupReplica(t *testing.T) {
	cl := newCatalogTestCluster(t)
	master := newCatalogTestServer(cl, "db1", "3306")
	replica := newCatalogTestServer(cl, "db2", "3306")
	replica.PreferedBackup = true
	cl.Servers = serverList{master, replica}
	cl.master = master
	cl.StateMachine.Discovered = true

	got := cl.resolveLiveDumpSource()
	if got == nil || got.URL != replica.URL {
		t.Fatalf("expected the PreferedBackup replica, got %+v", got)
	}
}

func TestResolveLiveDumpSource_FallsBackToMasterWhenNoPreferedBackup(t *testing.T) {
	cl := newCatalogTestCluster(t)
	master := newCatalogTestServer(cl, "db1", "3306")
	replica := newCatalogTestServer(cl, "db2", "3306") // PreferedBackup left false
	cl.Servers = serverList{master, replica}
	cl.master = master
	cl.StateMachine.Discovered = true

	got := cl.resolveLiveDumpSource()
	if got == nil || got.URL != master.URL {
		t.Fatalf("expected master when no server has PreferedBackup, got %+v", got)
	}
}

func TestResolveLiveDumpSource_UndiscoveredClusterFallsBackToMaster(t *testing.T) {
	cl := newCatalogTestCluster(t)
	master := newCatalogTestServer(cl, "db1", "3306")
	cl.Servers = serverList{master}
	cl.master = master
	// cl.StateMachine.Discovered left false: GetBackupServer() returns nil early.

	got := cl.resolveLiveDumpSource()
	if got == nil || got.URL != master.URL {
		t.Fatalf("expected master when cluster is not yet discovered, got %+v", got)
	}
}
