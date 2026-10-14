// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
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
