// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// Each node's own address differs by design (bind address, group replication local
// address, Galera node address) and is no variables diff; a real setting still is (#2180).
func TestVariablesDiffSkipsNodeAddresses(t *testing.T) {
	cl := newColumnstoreTestCluster()
	cl.Conf.MonitorVariableDiff = true
	cl.WorkingDir = t.TempDir()
	cl.StateMachine = &state.StateMachine{}
	cl.StateMachine.Init()
	node := func(url, host string, parallelReadThreads string) *ServerMonitor {
		v := config.NewStringsMap()
		v.Set("BIND_ADDRESS", host)
		v.Set("GROUP_REPLICATION_LOCAL_ADDRESS", host+":33061")
		v.Set("WSREP_NODE_ADDRESS", host)
		v.Set("INNODB_PARALLEL_READ_THREADS", parallelReadThreads)
		return &ServerMonitor{URL: url, Variables: v, ClusterGroup: cl}
	}
	cl.master = node("db1:3306", "db1", "4")
	cl.slaves = []*ServerMonitor{node("db2:3306", "db2", "4"), node("db3:3306", "db3", "4")}
	cl.MonitorVariablesDiff()
	if cl.DiffVariables != nil || cl.StateMachine.CurState.Search(state.BuildStateKey("WARN0084", cl.master.URL)) {
		t.Fatalf("node addresses reported as a variables diff: %+v", cl.DiffVariables)
	}

	cl.slaves[1].Variables.Set("INNODB_PARALLEL_READ_THREADS", "6")
	cl.MonitorVariablesDiff()
	if len(cl.DiffVariables) != 1 || cl.DiffVariables[0].VariableName != "INNODB_PARALLEL_READ_THREADS" || !cl.StateMachine.CurState.Search(state.BuildStateKey("WARN0084", cl.master.URL)) {
		t.Fatalf("a setting that differs must still be reported, got %+v", cl.DiffVariables)
	}
}
