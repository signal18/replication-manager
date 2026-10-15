// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// #2021: a dump is never reloaded into a ColumnStore replica (its DROP TABLE drops the
// tables every server shares); the master and the servers of other topologies are not
// concerned.
func TestColumnstoreReplicaRefusesDump(t *testing.T) {
	cl := newColumnstoreTestCluster()
	if err := cl.BootstrapTopology(config.TopoColumnstore); err != nil {
		t.Fatal(err)
	}
	db1 := &ServerMonitor{URL: "db1:3306", ClusterGroup: cl}
	db2 := &ServerMonitor{URL: "db2:3306", ClusterGroup: cl}
	cl.Servers = []*ServerMonitor{db1, db2}
	cl.master = db1
	if db1.isColumnstoreReplica() || db1.refuseColumnstoreReplicaDump() != nil {
		t.Fatal("the master of a ColumnStore cluster takes a dump (a seed from the parent)")
	}
	if !db2.isColumnstoreReplica() || db2.refuseColumnstoreReplicaDump() == nil {
		t.Fatal("a dump into a ColumnStore replica must be refused")
	}
	if _, _, err := db2.buildLogicalRestorePreamble(); err == nil {
		t.Fatal("the logical restore preamble must refuse a ColumnStore replica")
	}
	if _, _, err := db1.buildLogicalRestorePreamble(); err != nil {
		t.Fatalf("the master's restore is allowed: %v", err)
	}

	ms := newColumnstoreTestCluster()
	if err := ms.BootstrapTopology(config.TopoMasterSlave); err != nil {
		t.Fatal(err)
	}
	r := &ServerMonitor{URL: "db2:3306", ClusterGroup: ms}
	ms.master = &ServerMonitor{URL: "db1:3306", ClusterGroup: ms}
	if r.isColumnstoreReplica() || r.refuseColumnstoreReplicaDump() != nil {
		t.Fatal("a master-slave replica is no ColumnStore replica")
	}
}

func TestColumnstoreSchemaDiff(t *testing.T) {
	tb := func(s, n, e string) dbhelper.EngineTable { return dbhelper.EngineTable{Schema: s, Table: n, Engine: e} }
	master := []dbhelper.EngineTable{tb("shop", "orders", "Columnstore"), tb("shop", "users", "InnoDB")}
	if m, r := columnstoreSchemaDiff(master, master); m != 0 || r != 0 {
		t.Fatalf("same schema: %d %d", m, r)
	}
	if m, r := columnstoreSchemaDiff(master, []dbhelper.EngineTable{tb("shop", "orders", "Columnstore"), tb("shop", "users", "Columnstore"), tb("lb", "t", "Columnstore")}); m != 1 || r != 2 {
		t.Fatalf("an engine differs and a table is extra: got %d on the master only, %d on the replica only, want 1 and 2", m, r)
	}
}

// #2021: every server but the primary is told whom to join at its first start; nothing
// outside the columnstore topology.
func TestColumnstorePrimaryEnvironment(t *testing.T) {
	cl := newColumnstoreTestCluster()
	if err := cl.BootstrapTopology(config.TopoColumnstore); err != nil {
		t.Fatal(err)
	}
	db1 := &ServerMonitor{URL: "db1.cs.svc.cloud18:3306", Host: "db1.cs.svc.cloud18", ClusterGroup: cl}
	db2 := &ServerMonitor{URL: "db2.cs.svc.cloud18:3306", Host: "db2.cs.svc.cloud18", ClusterGroup: cl}
	cl.Servers = []*ServerMonitor{db1, db2}
	if p := cl.columnstorePrimary(); p != db1 {
		t.Fatalf("no master yet: the first server is the primary, got %v", p)
	}
	if env := db2.OpenSVCGetDBContainerEnvironment(); !strings.Contains(env, "COLUMNSTORE_PRIMARY=db1.cs.svc.cloud18") {
		t.Fatalf("db2 must join db1: %q", env)
	}
	if env := db1.OpenSVCGetDBContainerEnvironment(); strings.Contains(env, "COLUMNSTORE_PRIMARY") {
		t.Fatalf("the primary joins no one: %q", env)
	}
	cl.master = db2
	if env := db1.OpenSVCGetDBContainerEnvironment(); !strings.Contains(env, "COLUMNSTORE_PRIMARY=db2.cs.svc.cloud18") {
		t.Fatalf("the master is the primary once known: %q", env)
	}

	ms := newColumnstoreTestCluster()
	if err := ms.BootstrapTopology(config.TopoMasterSlave); err != nil {
		t.Fatal(err)
	}
	r := &ServerMonitor{URL: "db2:3306", Host: "db2", ClusterGroup: ms}
	ms.Servers = []*ServerMonitor{{URL: "db1:3306", Host: "db1", ClusterGroup: ms}, r}
	if env := r.OpenSVCGetDBContainerEnvironment(); strings.Contains(env, "COLUMNSTORE_PRIMARY") {
		t.Fatalf("not columnstore: %q", env)
	}
}
