// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// In manual mode ERR00002 says an operator failover is awaited: only once the master has
// failed, never on a healthy master (it mailed after every restart or switchover), never on
// group replication, whose group elects the PRIMARY itself (#2180). The failover decision
// is unchanged: manual mode never fails over on its own.
func TestManualFailoverAwaitedOnlyOnFailedMaster(t *testing.T) {
	for _, c := range []struct {
		topology    string
		masterState string
		awaited     bool
	}{
		{config.TopoMasterSlave, stateMaster, false},
		{config.TopoMasterSlave, stateFailed, true},
		{config.TopoMultiMasterWsrep, stateFailed, true},
		{config.TopoMultiMasterGrouprep, stateMaster, false},
		{config.TopoMultiMasterGrouprep, stateFailed, false},
	} {
		cl := &Cluster{Conf: &config.Config{Interactive: true}, Topology: c.topology, StateMachine: &state.StateMachine{}}
		cl.StateMachine.Init()
		cl.master = &ServerMonitor{URL: "db1:3306", State: c.masterState}
		if cl.isAutomaticFailover() {
			t.Fatalf("%s, master %s: manual mode never fails over on its own", c.topology, c.masterState)
		}
		if got := cl.StateMachine.CurState.Search("ERR00002"); got != c.awaited {
			t.Errorf("%s, master %s: ERR00002 open %v, want %v", c.topology, c.masterState, got, c.awaited)
		}
	}
}
