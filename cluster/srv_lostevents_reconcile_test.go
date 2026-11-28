// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"database/sql"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

// A failed rejoin is reconciled to "recovered" only once the node replicates from the elected
// master: a link that points at it but whose IO thread stopped on an error (the old master
// after a flashback whose position was refused) keeps the failed outcome and WARN0186.
func TestAssertRejoinResultStatesBrokenLinkNotRecovered(t *testing.T) {
	ns := func(v string) sql.NullString { return sql.NullString{String: v, Valid: true} }
	cl := &Cluster{Name: "t", StateMachine: new(state.StateMachine), Conf: &config.Config{}}
	cl.StateMachine.Init()
	master := &ServerMonitor{URL: "db2:3306", Host: "db2", Port: "3306", ServerID: 2, State: stateMaster, ClusterGroup: cl}
	old := &ServerMonitor{URL: "db3:3306", Host: "db3", Port: "3306", ServerID: 3, State: stateSlaveErr, IsSlave: true, ClusterGroup: cl}
	link := func(io string) ReplicationList {
		return nativeRows(dbhelper.SlaveStatus{ConnectionName: ns(""), MasterHost: ns("db2"), MasterPort: ns("3306"), MasterServerID: 2,
			SlaveIORunning: ns(io), SlaveSQLRunning: ns("Yes"), LastIOErrno: ns("1236")})
	}
	old.Replications = link("No")
	cl.Servers = []*ServerMonitor{master, old}
	cl.master = master
	cr := &Crash{URL: old.URL, ElectedMasterURL: master.URL, RejoinResult: RejoinResultNoMethod, RejoinResultTs: 1}
	cl.FailoverHistory = []*Crash{cr}

	cl.assertRejoinResultStates()
	if cr.RejoinResult != RejoinResultNoMethod {
		t.Fatalf("outcome %q: a broken link was reconciled as recovered", cr.RejoinResult)
	}
	if open := cl.StateMachine.GetOpenStates(); len(open) != 1 || open[0].ErrKey != "WARN0186" || open[0].ServerUrl != old.URL {
		t.Fatalf("WARN0186 not raised for the failed rejoin: %+v", cl.StateMachine.GetOpenStates())
	}
}
