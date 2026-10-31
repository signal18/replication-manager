// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
)

func heartbeatTestCluster(t *testing.T) (*Cluster, *ServerMonitor, *ServerMonitor) {
	t.Helper()
	cl := &Cluster{Name: "t", StateMachine: new(state.StateMachine), Conf: &config.Config{
		CheckFalsePositiveHeartbeat: true, CheckFalsePositiveHeartbeatTimeout: 3, MonitoringTicker: 2,
		ForceSlaveHeartbeatTime: 3, MasterConn: "bench",
	}}
	cl.StateMachine.Init()
	mariadb := &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 9}
	master := &ServerMonitor{URL: "db1:3306", Host: "db1", Port: "3306", ServerID: 1, State: stateMaster, ClusterGroup: cl, DBVersion: mariadb}
	slave := &ServerMonitor{URL: "db2:3306", Host: "db2", Port: "3306", ServerID: 2, State: stateSlave, ClusterGroup: cl, DBVersion: mariadb, ReplicationSourceName: "bench"}
	cl.Servers = serverList{master, slave}
	cl.master = master
	cl.slaves = serverList{slave}
	setHeartbeatStatus(slave, 10, "1-1-5", 3)
	return cl, master, slave
}

// setHeartbeatStatus is what SHOW ALL SLAVES STATUS gave the tick for the managed channel.
func setHeartbeatStatus(slave *ServerMonitor, received int64, gtidIOPos string, period float64) {
	yes := sql.NullString{String: "Yes", Valid: true}
	slave.Replications = []dbhelper.SlaveStatus{{
		ConnectionName: sql.NullString{String: "bench", Valid: true}, MasterHost: sql.NullString{String: "db1", Valid: true},
		MasterPort: sql.NullString{String: "3306", Valid: true}, MasterServerID: 1, SlaveIORunning: yes, SlaveSQLRunning: yes,
		GtidIOPos: sql.NullString{String: gtidIOPos, Valid: true}, SlaveHeartbeatPeriod: period,
		SlaveReceivedHeartbeats: sql.NullInt64{Int64: received, Valid: true},
	}}
}

// MariaDB keeps no last received time: the tick that sees the channel's heartbeat counter or
// IO position move stamps it (#2208).
func TestRefreshReplicationLastReceivedMariaDB(t *testing.T) {
	_, _, slave := heartbeatTestCluster(t)
	slave.refreshReplicationLastReceived()
	if _, ok := slave.GetReplicationLastReceivedAge(); ok || !slave.replicationHeartbeatKnown || slave.ReplicationHeartbeatPeriod != 3 {
		t.Fatal("the first read is a reference, not a receive")
	}
	slave.refreshReplicationLastReceived()
	if _, ok := slave.GetReplicationLastReceivedAge(); ok {
		t.Fatal("nothing moved: nothing received")
	}
	setHeartbeatStatus(slave, 11, "1-1-5", 3) // an idle master's heartbeat
	slave.refreshReplicationLastReceived()
	if age, ok := slave.GetReplicationLastReceivedAge(); !ok || age > time.Second {
		t.Fatal("a heartbeat is a receive")
	}
	slave.ReplicationLastReceivedAt = time.Now().Add(-time.Minute)
	setHeartbeatStatus(slave, 11, "1-1-9", 3) // a busy master's events, no heartbeat
	slave.refreshReplicationLastReceived()
	if age, _ := slave.GetReplicationLastReceivedAge(); age > time.Second {
		t.Fatal("an event is a receive")
	}
	slave.ReplicationLastReceivedAt = time.Now().Add(-time.Minute)
	setHeartbeatStatus(slave, 0, "1-1-9", 3) // a restarted channel
	slave.refreshReplicationLastReceived()
	if age, _ := slave.GetReplicationLastReceivedAge(); age < time.Minute {
		t.Fatal("a counter going down is a restart, not a receive")
	}
	slave.ReplicationLastReceivedAt = time.Now().Add(-time.Minute)
	setHeartbeatStatus(slave, 1, "1-1-12", 3)
	slave.Replications[0].MasterHost = sql.NullString{String: "db3", Valid: true} // repointed by a failover
	slave.refreshReplicationLastReceived()
	if _, ok := slave.GetReplicationLastReceivedAge(); ok {
		t.Fatal("a repoint to another source is not a receive")
	}
	slave.ClusterGroup.Conf.CheckFalsePositiveHeartbeat = false
	slave.refreshReplicationLastReceived()
	if _, ok := slave.GetReplicationLastReceivedAge(); ok || slave.replicationHeartbeatKnown {
		t.Fatal("not read when neither the check nor force-slave-heartbeat needs it")
	}
}

// The guard vetoes a failover while a direct replica received within heartbeat period +
// ticker + timeout, and lets it go once the master went quiet: no wait in the check.
func TestFalsePositiveHeartbeatGuard(t *testing.T) {
	cl, master, slave := heartbeatTestCluster(t)
	slave.replicationHeartbeatKnown = true
	slave.ReplicationHeartbeatPeriod = 3
	master.State = stateFailed

	slave.ReplicationLastReceivedAt = time.Now().Add(-2 * time.Second)
	start := time.Now()
	if !cl.isOneSlaveHeartbeatIncreasing() || !cl.StateMachine.IsInState("ERR00028") {
		t.Fatal("a replica that received 2s ago proves the master alive")
	}
	if time.Since(start) > time.Second {
		t.Fatal("the check must not wait")
	}
	cl.StateMachine.Init()
	slave.ReplicationLastReceivedAt = time.Now().Add(-9 * time.Second) // window 3+2+3 = 8s
	if cl.isOneSlaveHeartbeatIncreasing() {
		t.Fatal("a master quiet for longer than the window does not block the failover")
	}
	slave.ReplicationLastReceivedAt = time.Time{}
	if cl.isOneSlaveHeartbeatIncreasing() {
		t.Fatal("no receive seen: no veto")
	}
	slave.ReplicationLastReceivedAt = time.Now()
	cl.Conf.CheckFalsePositiveHeartbeat = false
	if cl.isOneSlaveHeartbeatIncreasing() {
		t.Fatal("failover-falsepositive-heartbeat off: no veto")
	}
	cl.Conf.CheckFalsePositiveHeartbeat = true
	master.State = stateMaster
	if cl.isOneSlaveHeartbeatIncreasing() {
		t.Fatal("a master not failed is not checked")
	}
}

// CINF0022 shows each replica's last receive while the master is suspect or failed only.
func TestReportReplicasLastReceived(t *testing.T) {
	cl, master, slave := heartbeatTestCluster(t)
	slave.replicationHeartbeatKnown = true
	slave.ReplicationHeartbeatPeriod = 30
	slave.ReplicationLastReceivedAt = time.Now().Add(-1400 * time.Millisecond)
	cl.reportReplicasLastReceived()
	if len(cl.StateMachine.GetOpenStates()) != 0 {
		t.Fatal("no CINF0022 in peace time")
	}
	master.State = stateSuspect
	cl.reportReplicasLastReceived()
	open := cl.StateMachine.GetOpenStates()
	if len(open) != 1 || open[0].ErrKey != "CINF0022" || open[0].ServerUrl != "db2:3306" ||
		!strings.Contains(open[0].ErrDesc, "db2:3306 last received from master db1:3306: 1.4s ago (heartbeat period 30s)") {
		t.Fatalf("%+v", open)
	}
}

// A period above force-slave-heartbeat-time, or heartbeats off, is a WARN0050 while the
// check is on; nothing when it is at most force-slave-heartbeat-time or the check is off.
func TestCheckReplicationHeartbeatPeriodWarns(t *testing.T) {
	for _, c := range []struct {
		period float64
		check  bool
		warn   bool
	}{
		{30, true, true},
		{0, true, true},
		{3, true, false},
		{1, true, false},
		{30, false, false},
	} {
		cl, _, slave := heartbeatTestCluster(t)
		cl.Conf.CheckFalsePositiveHeartbeat = c.check
		slave.replicationHeartbeatKnown = true
		slave.ReplicationHeartbeatPeriod = c.period
		slave.checkReplicationHeartbeatPeriod()
		if got := cl.StateMachine.IsInState(state.BuildStateKey("WARN0050", slave.URL)); got != c.warn {
			t.Errorf("period %v check %t: WARN0050 %t, want %t", c.period, c.check, got, c.warn)
		}
	}
}

// stubSetSlaveHeartbeat records the period changes force-slave-heartbeat makes.
func stubSetSlaveHeartbeat(t *testing.T, err error) *[]string {
	t.Helper()
	calls := new([]string)
	saved := setSlaveHeartbeat
	setSlaveHeartbeat = func(_ *sqlx.DB, interval string, channel string, _ *version.Version) (string, error) {
		*calls = append(*calls, channel+"="+interval)
		return "", err
	}
	t.Cleanup(func() { setSlaveHeartbeat = saved })
	return calls
}

// force-slave-heartbeat sets the managed channel to force-slave-heartbeat-time once
// (CINF0023), never on a channel whose thread someone stopped, never on an ignored server,
// and does not retry a failure while the period stays the same (WARN0268).
func TestCheckReplicationHeartbeatPeriodEnforces(t *testing.T) {
	setup := func() (*Cluster, *ServerMonitor) {
		cl, _, slave := heartbeatTestCluster(t)
		cl.Conf.ForceSlaveHeartbeat = true
		slave.replicationHeartbeatKnown = true
		slave.ReplicationHeartbeatPeriod = 30
		return cl, slave
	}
	key := func(code string, s *ServerMonitor) string { return state.BuildStateKey(code, s.URL) }

	calls := stubSetSlaveHeartbeat(t, nil)
	cl, slave := setup()
	slave.checkReplicationHeartbeatPeriod()
	if len(*calls) != 1 || (*calls)[0] != "bench=3" || !cl.StateMachine.IsInState(key("CINF0023", slave)) || cl.StateMachine.IsInState(key("WARN0050", slave)) {
		t.Fatalf("calls %v", *calls)
	}

	calls = stubSetSlaveHeartbeat(t, nil)
	cl, slave = setup()
	slave.Replications[0].SlaveSQLRunning = sql.NullString{String: "No", Valid: true}
	slave.checkReplicationHeartbeatPeriod()
	if len(*calls) != 0 || !cl.StateMachine.IsInState(key("WARN0050", slave)) {
		t.Fatalf("a stopped SQL thread: warn, no change; calls %v", *calls)
	}

	calls = stubSetSlaveHeartbeat(t, nil)
	cl, slave = setup()
	slave.Ignored = true
	slave.checkReplicationHeartbeatPeriod()
	if len(*calls) != 0 || len(cl.StateMachine.GetOpenStates()) != 0 {
		t.Fatalf("an ignored server: no change, no state; calls %v", *calls)
	}

	calls = stubSetSlaveHeartbeat(t, errors.New("refused"))
	cl, slave = setup()
	slave.checkReplicationHeartbeatPeriod()
	slave.checkReplicationHeartbeatPeriod()
	if len(*calls) != 1 || !cl.StateMachine.IsInState(key("WARN0268", slave)) {
		t.Fatalf("a failure is not retried while the period stays: calls %v", *calls)
	}
	slave.ReplicationHeartbeatPeriod = 60
	slave.checkReplicationHeartbeatPeriod()
	if len(*calls) != 2 {
		t.Fatalf("a new period is tried again: calls %v", *calls)
	}
}

// MySQL reads the last receive from performance_schema, aged on the replica's clock.
func TestRefreshReplicationLastReceivedMySQL(t *testing.T) {
	_, _, slave := heartbeatTestCluster(t)
	slave.DBVersion = &version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 41}
	raw, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	slave.Conn = sqlx.NewDb(raw, "mysql")
	mock.ExpectQuery("performance_schema.replication_connection_status").WithArgs("bench").
		WillReturnRows(sqlmock.NewRows([]string{"last", "now", "period"}).AddRow("1000.000000", "1002.500000", "3.000"))
	slave.refreshReplicationLastReceived()
	age, ok := slave.GetReplicationLastReceivedAge()
	if !ok || age < 2500*time.Millisecond || age > 3500*time.Millisecond || slave.GetReplicationHearbeatPeriod() != 3 {
		t.Fatalf("age %s ok %t period %v", age, ok, slave.GetReplicationHearbeatPeriod())
	}
	mock.ExpectQuery("performance_schema.replication_connection_status").WithArgs("bench").
		WillReturnRows(sqlmock.NewRows([]string{"last", "now", "period"}).AddRow("0.000000", "1002.500000", "3.000"))
	slave.refreshReplicationLastReceived()
	if _, ok := slave.GetReplicationLastReceivedAge(); ok {
		t.Fatal("a channel that never received has no last receive")
	}
	// MySQL before 5.7 records nothing: no query, nothing tracked
	slave.DBVersion = &version.Version{Flavor: "MySQL", Major: 5, Minor: 6, Release: 51}
	slave.refreshReplicationLastReceived()
	if known, _, _ := slave.replicationReceived(); known {
		t.Fatal("MySQL 5.6: not tracked")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// The tick writes the tracking while the failover check and the API read it (go test -race).
// The slave status slice stays fixed: the tick replacing it is the monitor's existing
// pattern, not this tracking; every refresh still rewrites the tracking fields.
func TestReplicationLastReceivedConcurrent(t *testing.T) {
	cl, master, slave := heartbeatTestCluster(t)
	master.State = stateFailed
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			slave.refreshReplicationLastReceived()
			if i%50 == 0 {
				slave.resetReplicationLastReceived()
			}
		}
	}()
	for i := 0; i < 200; i++ {
		cl.isOneSlaveHeartbeatIncreasing()
		cl.reportReplicasLastReceived()
		slave.GetReplicationHearbeatPeriod()
	}
	<-done
}
