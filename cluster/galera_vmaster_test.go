// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
	ini "gopkg.in/ini.v1"
)

// galeraNode is a Galera node as the monitor reads it: in the Primary component and synced, or not.
func galeraNode(cl *Cluster, url string, st string, primary, synced bool) *ServerMonitor {
	s := &ServerMonitor{URL: url, State: st, ClusterGroup: cl, SourceClusterName: cl.Name, Status: config.NewStringsMap()}
	if primary {
		s.Status.Set("WSREP_CLUSTER_STATUS", "PRIMARY")
	} else {
		s.Status.Set("WSREP_CLUSTER_STATUS", "NON-PRIMARY")
	}
	if synced {
		s.Status.Set("WSREP_LOCAL_STATE", "4")
	}
	return s
}

func galeraCluster(interactive bool) *Cluster {
	cl := &Cluster{Name: "c1", Conf: &config.Config{Interactive: interactive}, Topology: config.TopoMultiMasterWsrep, StateMachine: &state.StateMachine{}}
	cl.StateMachine.Init()
	return cl
}

// #2350: on Galera the virtual master moves on its own while a majority is up, in manual mode
// too, never waiting for an operator (no ERR00002), never limited by failover-limit or
// failover-time-limit; with no synced node in the Primary component, nothing moves.
func TestGaleraVirtualMasterMovesWithMajority(t *testing.T) {
	for _, interactive := range []bool{true, false} {
		cl := galeraCluster(interactive)
		cl.Conf.FailLimit, cl.FailoverCtr = 5, 5
		cl.Conf.FailTime, cl.FailoverTs = 3600, 1<<62
		db1 := galeraNode(cl, "db1:3306", stateFailed, false, false)
		db2 := galeraNode(cl, "db2:3306", stateSlave, true, true)
		cl.Servers = []*ServerMonitor{db1, db2}
		cl.master = db1
		if !cl.isAutomaticFailover() || !cl.isMaxClusterFailoverCountNotReached() || !cl.isBetweenFailoverTimeValid() {
			t.Fatalf("interactive=%v: a majority up, the virtual master moves, unlimited", interactive)
		}
		if cl.StateMachine.CurState.Search("ERR00002") {
			t.Fatalf("interactive=%v: Galera never waits for an operator failover", interactive)
		}

		// no synced node in a Primary component: no majority, nothing moves
		db2.Status.Set("WSREP_CLUSTER_STATUS", "NON-PRIMARY")
		if cl.isAutomaticFailover() {
			t.Fatalf("interactive=%v: without a Primary component nothing moves", interactive)
		}
	}

	// master-slave keeps its manual-mode gate and its limits
	ms := &Cluster{Conf: &config.Config{Interactive: true, FailLimit: 5}, Topology: config.TopoMasterSlave, StateMachine: &state.StateMachine{}}
	ms.StateMachine.Init()
	ms.FailoverCtr = 5
	ms.master = &ServerMonitor{URL: "db1:3306", State: stateFailed}
	if ms.isAutomaticFailover() || ms.isMaxClusterFailoverCountNotReached() {
		t.Fatal("master-slave: manual mode and failover-limit still apply")
	}
}

// #2350: no virtual master (its last one failed before a restart of replication-manager): one is
// elected among the synced members of the Primary component, the preferred master first; none
// when there is no majority; a cluster that has a master keeps it.
func TestGaleraElectVirtualMaster(t *testing.T) {
	cl := galeraCluster(true)
	db1 := galeraNode(cl, "db1:3306", stateFailed, false, false)
	db2 := galeraNode(cl, "db2:3306", stateSlave, true, true)
	db3 := galeraNode(cl, "db3:3306", stateSlave, true, true)
	joining := galeraNode(cl, "db4:3306", stateSlave, true, false) // in the component, not synced
	cl.Servers = []*ServerMonitor{db1, joining, db2, db3}

	if got := cl.galeraElectVirtualMaster(); got != db2 || cl.GetMaster() != db2 || cl.vmaster != db2 {
		t.Fatalf("the first synced node of the Primary component in declared order: %v", got)
	}
	if !cl.StateMachine.CurState.Search("WARN0006") {
		t.Fatal("the election is a state (WARN0006)")
	}
	if got := cl.galeraElectVirtualMaster(); got != nil || cl.GetMaster() != db2 {
		t.Fatal("a cluster with a master keeps it")
	}

	cl.master, cl.vmaster = nil, nil
	cl.Conf.PrefMaster = "db3:3306"
	db3.Prefered = true
	if got := cl.galeraElectVirtualMaster(); got != db3 {
		t.Fatalf("the preferred master when electable: %v", got)
	}

	cl.master, cl.vmaster = nil, nil
	for _, s := range []*ServerMonitor{db2, db3} {
		s.Status.Set("WSREP_CLUSTER_STATUS", "NON-PRIMARY")
	}
	if got := cl.galeraElectVirtualMaster(); got != nil || cl.GetMaster() != nil {
		t.Fatal("no Primary component: no election")
	}

	ms := &Cluster{Conf: &config.Config{}, Topology: config.TopoMasterSlave, Servers: []*ServerMonitor{db2}}
	if ms.galeraElectVirtualMaster() != nil {
		t.Fatal("never outside Galera")
	}
}

// #2350: the master forgotten when the cluster went down stays in cluster.vmaster: the failed node
// must not block the election once a majority is back, and CheckFailed must reach it. A virtual
// master still electable is kept, even when another node is preferred: the writes do not move.
func TestGaleraElectWithForgottenMaster(t *testing.T) {
	for _, interactive := range []bool{true, false} {
		cl := galeraCluster(interactive)
		db1 := galeraNode(cl, "db1:3306", stateFailed, false, false)
		db2 := galeraNode(cl, "db2:3306", stateSlave, true, true)
		cl.Servers = []*ServerMonitor{db1, db2}
		cl.master, cl.vmaster = nil, db1

		cl.CheckFailed()
		if cl.master != db2 || cl.vmaster != db2 || cl.GetMaster() != db2 {
			t.Fatalf("interactive=%v: a stale failed vmaster must not block the election", interactive)
		}
		if !cl.StateMachine.CurState.Search("WARN0006") {
			t.Fatalf("interactive=%v: the election is a state (WARN0006)", interactive)
		}
	}

	cl := galeraCluster(true)
	db1 := galeraNode(cl, "db1:3306", stateFailed, false, false)
	db2 := galeraNode(cl, "db2:3306", stateSlave, true, true)
	db3 := galeraNode(cl, "db3:3306", stateSlave, true, true)
	cl.Servers = []*ServerMonitor{db1, db2, db3}

	// no majority back: nothing is elected, the stale vmaster stays as it was
	db2.Status.Set("WSREP_CLUSTER_STATUS", "NON-PRIMARY")
	db3.Status.Set("WSREP_CLUSTER_STATUS", "NON-PRIMARY")
	cl.master, cl.vmaster = nil, db1
	if got := cl.galeraElectVirtualMaster(); got != nil || cl.master != nil || cl.vmaster != db1 {
		t.Fatal("no Primary component: no election, the stale vmaster stays as it was")
	}

	// the virtual master is electable: kept over the preferred master
	db2.Status.Set("WSREP_CLUSTER_STATUS", "PRIMARY")
	db3.Status.Set("WSREP_CLUSTER_STATUS", "PRIMARY")
	cl.Conf.PrefMaster = "db3:3306"
	db3.Prefered = true
	cl.master, cl.vmaster = nil, db2
	if got := cl.galeraElectVirtualMaster(); got != db2 || cl.master != db2 {
		t.Fatal("an electable virtual master is kept, the writes do not move")
	}
}

// #2350: the bootstrap option on container#db is set for the start that bootstraps a cluster
// entirely down, removed from any other start, and an operator's own command is never touched.
func TestGaleraCommandPatch(t *testing.T) {
	sec := func(command string) *ini.Section {
		f := ini.Empty()
		s, _ := f.NewSection("container#db")
		if command != "" {
			s.Key("command").SetValue(command)
		}
		return s
	}
	s := sec(galeraBootstrapCommand)
	if !galeraCommandPatch(s, false) || s.HasKey("command") {
		t.Fatal("a joining node: the bootstrap option left by a previous bootstrap is removed")
	}
	if galeraCommandPatch(s, false) {
		t.Fatal("nothing to remove: no write")
	}
	s = sec("")
	if !galeraCommandPatch(s, true) || s.Key("command").String() != galeraBootstrapCommand {
		t.Fatal("the bootstrap node of a cluster entirely down gets the option")
	}
	s = sec("--skip-slave-start")
	if galeraCommandPatch(s, false) || galeraCommandPatch(s, true) || s.Key("command").String() != "--skip-slave-start" {
		t.Fatal("an operator's own command is left alone")
	}
}
