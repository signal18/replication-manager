// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

// An IO thread retrying its source is not Running OK: with an error it is WARN0269 naming
// the server, the channel and the error; the server stays a slave (#2218).
func TestCheckStoppedReplicationIOConnecting(t *testing.T) {
	cl := &Cluster{Name: "t", StateMachine: new(state.StateMachine), Conf: &config.Config{}}
	cl.StateMachine.Init()
	s := &ServerMonitor{URL: "db2:3306", ClusterGroup: cl, State: stateSlave}
	ns := func(v string) sql.NullString { return sql.NullString{String: v, Valid: true} }
	ss := &dbhelper.SlaveStatus{ConnectionName: ns("bench"), SlaveIORunning: ns("Connecting"), SlaveSQLRunning: ns("Yes"),
		LastIOErrno: ns("2061"), LastIOError: ns("Authentication plugin 'caching_sha2_password' reported error: Authentication requires secure connection.")}

	if got := s.checkStoppedReplication(ss); got != "NOT OK, IO Connecting (2061)" {
		t.Fatalf("health %q", got)
	}
	open := cl.StateMachine.GetOpenStates()
	if len(open) != 1 || open[0].ErrKey != "WARN0269" || open[0].ServerUrl != "db2:3306" ||
		!strings.Contains(open[0].ErrDesc, "channel 'bench'") || !strings.Contains(open[0].ErrDesc, "error 2061") {
		t.Fatalf("%+v", open)
	}
	if s.State != stateSlave {
		t.Fatalf("state %s: the election must not change", s.State)
	}

	// a TLS handshake failure is WARN0236's, not a second state
	cl.StateMachine.Init()
	ss.LastIOErrno = ns("2026")
	if got := s.checkStoppedReplication(ss); got != "NOT OK, IO Connecting (2026)" || len(cl.StateMachine.GetOpenStates()) != 0 {
		t.Fatalf("health %q, states %+v", got, cl.StateMachine.GetOpenStates())
	}

	// a retry with no error yet: not OK, no state
	cl.StateMachine.Init()
	ss.LastIOErrno, ss.LastIOError = ns("0"), ns("")
	if got := s.checkStoppedReplication(ss); got != "NOT OK, IO Connecting" || len(cl.StateMachine.GetOpenStates()) != 0 {
		t.Fatalf("health %q, states %+v", got, cl.StateMachine.GetOpenStates())
	}
}
