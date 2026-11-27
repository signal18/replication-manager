package cluster

import (
	"database/sql"
	"io"
	"os"
	"reflect"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/gtid"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
	"github.com/sirupsen/logrus"
)

// #2451: the election decisions of electFailoverCandidate and electSwitchoverCandidate. The tests
// drive the real election functions on servers whose replication status is a fixture, and assert
// the returned candidate (or -1), never a helper.

const (
	decimalUUID = "3e11fa47-71ca-4123-9e33-c80aa9429562" // third group decimal: the MariaDB parser reads 4123
	hexUUID     = "3e11fa47-71ca-11e1-9e33-c80aa9429562" // third group has a letter: the parser reads 0
)

type electionCand struct {
	url       string
	file, pos string
	io, sql   string // Slave_IO_Running / Slave_SQL_Running
	retrieved string // Retrieved_Gtid_Set (Gtid_IO_Pos on MariaDB)
	executed  string // Executed_Gtid_Set / gtid_slave_pos
	prefered  bool
	noStatus  bool
}

func electionVersion(flavor string, major, minor int) *version.Version {
	return &version.Version{Flavor: flavor, Major: major, Minor: minor}
}

func newElectionCluster(t *testing.T, flavor string, major, minor int, masterFailed bool, cands ...electionCand) (*Cluster, []*ServerMonitor) {
	t.Helper()
	logger := logrus.New()
	if os.Getenv("ELECTION_TEST_LOG") == "" {
		logger.SetOutput(io.Discard)
	}
	cl := &Cluster{
		Name:         "elec",
		Conf:         &config.Config{RplChecks: true, FailMaxDelay: -1},
		StateMachine: &state.StateMachine{},
		Logrus:       logger,
	}
	cl.StateMachine.Init()
	ver := electionVersion(flavor, major, minor)
	master := &ServerMonitor{URL: "m:3306", Host: "m", Port: "3306", ClusterGroup: cl, SourceClusterName: cl.Name, DBVersion: ver, Variables: config.NewStringsMap()}
	if masterFailed {
		master.State = stateFailed
	} else {
		master.State = stateMaster
	}
	cl.master = master
	var out []*ServerMonitor
	for _, c := range cands {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		mock.MatchExpectationsInOrder(false)
		for i := 0; i < 40; i++ {
			mock.ExpectQuery("global_variables").WillReturnRows(sqlmock.NewRows([]string{"variable_name", "value"}).AddRow("LOG_BIN", "ON"))
		}
		vars := config.NewStringsMap()
		vars.Set("LOG_BIN", "ON")
		s := &ServerMonitor{URL: c.url, Name: c.url, Host: c.url, Port: "3306", ClusterGroup: cl, SourceClusterName: cl.Name, DBVersion: ver,
			Conn: sqlx.NewDb(db, "sqlmock"), Variables: vars, Prefered: c.prefered, State: stateSlave, DelayStat: new(ServerDelayStat)}
		if !c.noStatus {
			s.Replications = []dbhelper.SlaveStatus{{
				ConnectionName:      sql.NullString{String: "", Valid: true},
				MasterLogFile:       sql.NullString{String: c.file, Valid: true},
				ReadMasterLogPos:    sql.NullString{String: c.pos, Valid: true},
				SlaveIORunning:      sql.NullString{String: c.io, Valid: true},
				SlaveSQLRunning:     sql.NullString{String: c.sql, Valid: true},
				SecondsBehindMaster: sql.NullInt64{Int64: 0, Valid: true},
				GtidIOPos:           sql.NullString{String: c.retrieved, Valid: c.retrieved != ""},
				RetrievedGtidSet:    sql.NullString{String: c.retrieved, Valid: c.retrieved != ""},
				ExecutedGtidSet:     sql.NullString{String: c.executed, Valid: c.executed != ""},
				UsingGtid:           sql.NullString{String: c.executed, Valid: c.executed != ""},
				GtidSlavePos:        sql.NullString{String: c.executed, Valid: c.executed != ""},
			}}
			s.SlaveGtid = gtid.NewList(c.executed)
			s.CurrentGtid = gtid.NewList(c.executed)
			s.GTIDBinlogPos = gtid.NewList(c.executed)
		}
		out = append(out, s)
	}
	cl.slaves = serverList(out)
	cl.Servers = serverList(append([]*ServerMonitor{master}, out...))
	return cl, out
}

func running(c electionCand) electionCand {
	if c.io == "" {
		c.io = "Yes"
	}
	if c.sql == "" {
		c.sql = "Yes"
	}
	return c
}

func mysqlCand(url string, preferred bool, pos string, uuid string) electionCand {
	set := uuid + ":1-5"
	return running(electionCand{url: url, file: "mysql-bin.000001", pos: pos, retrieved: set, executed: set, prefered: preferred})
}

func TestElectFailoverMySQLRanksByPosition(t *testing.T) {
	for _, uuid := range []string{decimalUUID, hexUUID} {
		t.Run("uuid "+uuid, func(t *testing.T) {
			// 1. preferred A behind a more advanced eligible B: B (develop with the decimal UUID: A)
			cl, l := newElectionCluster(t, "MySQL", 8, 4, true, mysqlCand("a", true, "100", uuid), mysqlCand("b", false, "200", uuid))
			if got := cl.electFailoverCandidate(l, true); got != 1 {
				t.Fatalf("preferred behind: elected %d, want 1 (the most advanced)", got)
			}
			// 2. equal progress: the preferred host
			cl, l = newElectionCluster(t, "MySQL", 8, 4, true, mysqlCand("a", true, "200", uuid), mysqlCand("b", false, "200", uuid))
			if got := cl.electFailoverCandidate(l, true); got != 0 {
				t.Fatalf("equal progress: elected %d, want 0 (the preferred host)", got)
			}
			// 3. the most advanced B is ineligible (SQL thread stopped): -1, the lagging A is not elected
			b := mysqlCand("b", false, "200", uuid)
			b.sql = "No"
			cl, l = newElectionCluster(t, "MySQL", 8, 4, true, mysqlCand("a", true, "100", uuid), b)
			if got := cl.electFailoverCandidate(l, true); got != -1 {
				t.Fatalf("most advanced ineligible: elected %d, want -1", got)
			}
			// 4. the lagging A is ineligible: B
			a := mysqlCand("a", true, "100", uuid)
			a.sql = "No"
			cl, l = newElectionCluster(t, "MySQL", 8, 4, true, a, mysqlCand("b", false, "200", uuid))
			if got := cl.electFailoverCandidate(l, true); got != 1 {
				t.Fatalf("lagging ineligible: elected %d, want 1", got)
			}
		})
	}
}

// A candidate without replication status keeps the synthetic default sequence under
// failover-restart-unsafe (it is not electable), so with a valid candidate whose MySQL sequence is
// now 0 the maximum is the synthetic 1 and nobody is elected: the documented change for a
// decimal-UUID cluster; a hex-UUID cluster already behaved so.
func TestElectFailoverMySQLMissingStatusRestartUnsafe(t *testing.T) {
	for _, uuid := range []string{decimalUUID, hexUUID} {
		cl, l := newElectionCluster(t, "MySQL", 8, 4, true, mysqlCand("a", true, "100", uuid), mysqlCand("b", false, "200", uuid), electionCand{url: "c", noStatus: true})
		cl.Conf.FailRestartUnsafe = true
		if got := cl.electFailoverCandidate(l, true); got != -1 {
			t.Fatalf("uuid %s: elected %d with a candidate without status, want -1", uuid, got)
		}
	}
}

func TestElectFailoverMySQLDottedBasename(t *testing.T) {
	a := mysqlCand("a", true, "100", decimalUUID)
	b := mysqlCand("b", false, "200", decimalUUID)
	a.file, b.file = "db1.example.com-bin.000007", "db1.example.com-bin.000007"
	cl, l := newElectionCluster(t, "MySQL", 8, 4, true, a, b)
	if got := cl.electFailoverCandidate(l, true); got != 1 {
		t.Fatalf("dotted basename: elected %d, want 1", got)
	}
	// hex UUID: the rank was 0 for both with the old parsing, so nobody was elected
	a.retrieved, b.retrieved, a.executed, b.executed = "", "", "", ""
	cl, l = newElectionCluster(t, "MySQL", 8, 4, true, a, b)
	if got := cl.electFailoverCandidate(l, true); got != 1 {
		t.Fatalf("dotted basename without GTID: elected %d, want 1", got)
	}
}

func TestElectFailoverNonGTIDMySQLPositional(t *testing.T) {
	a := running(electionCand{url: "a", file: "mysql-bin.000002", pos: "10", prefered: true})
	b := running(electionCand{url: "b", file: "mysql-bin.000002", pos: "90"})
	cl, l := newElectionCluster(t, "MySQL", 8, 0, true, a, b)
	if got := cl.electFailoverCandidate(l, true); got != 1 {
		t.Fatalf("non-GTID: elected %d, want 1", got)
	}
}

// MariaDB keeps the GTID sequence ranking: the highest sequence wins whatever the binlog position
// and whatever the preferred host (as before).
func TestElectFailoverMariaDBSequenceUnchanged(t *testing.T) {
	a := running(electionCand{url: "a", file: "mariadb-bin.000001", pos: "900", retrieved: "0-1-90", executed: "0-1-90", prefered: true})
	b := running(electionCand{url: "b", file: "mariadb-bin.000001", pos: "100", retrieved: "0-1-100", executed: "0-1-100"})
	cl, l := newElectionCluster(t, "MariaDB", 11, 8, true, a, b)
	if got := cl.electFailoverCandidate(l, true); got != 1 {
		t.Fatalf("MariaDB sequence: elected %d, want 1 (the highest sequence)", got)
	}
	// master not failed: the executed position decides
	a.executed, b.executed = "0-1-120", "0-1-100"
	cl, l = newElectionCluster(t, "MariaDB", 11, 8, false, a, b)
	if got := cl.electFailoverCandidate(l, true); got != 0 {
		t.Fatalf("MariaDB executed sequence: elected %d, want 0", got)
	}
	// no sequence and a dotted basename: positional
	a = running(electionCand{url: "a", file: "db1.example.com-bin.000003", pos: "10", prefered: true})
	b = running(electionCand{url: "b", file: "db1.example.com-bin.000003", pos: "90"})
	cl, l = newElectionCluster(t, "MariaDB", 11, 8, true, a, b)
	if got := cl.electFailoverCandidate(l, true); got != 1 {
		t.Fatalf("MariaDB dotted basename without GTID: elected %d, want 1", got)
	}
}

func TestElectSwitchoverMySQL(t *testing.T) {
	for _, uuid := range []string{decimalUUID, hexUUID} {
		t.Run("uuid "+uuid, func(t *testing.T) {
			// non-preferred path: the lagging A is listed first, B is more advanced: B
			cl, l := newElectionCluster(t, "MySQL", 8, 4, false, mysqlCand("a", false, "100", uuid), mysqlCand("b", false, "200", uuid))
			if got := cl.electSwitchoverCandidate(l, true); got != 1 {
				t.Fatalf("non-preferred switchover: elected %d, want 1 (the most advanced)", got)
			}
			// the most advanced is ineligible and excluded before the maximum: the other one
			b := mysqlCand("b", false, "200", uuid)
			b.sql = "No"
			cl, l = newElectionCluster(t, "MySQL", 8, 4, false, mysqlCand("a", false, "100", uuid), b)
			if got := cl.electSwitchoverCandidate(l, true); got != 0 {
				t.Fatalf("most advanced ineligible: elected %d, want 0 (switchover excludes ineligible candidates first)", got)
			}
			// existing preferred-host policy: an eligible preferred host is returned before any ranking
			cl, l = newElectionCluster(t, "MySQL", 8, 4, false, mysqlCand("a", false, "100", uuid), mysqlCand("b", false, "200", uuid))
			cl.Conf.PrefMaster = "a"
			if got := cl.electSwitchoverCandidate(l, true); got != 0 {
				t.Fatalf("preferred host: elected %d, want 0", got)
			}
		})
	}
}

func TestElectSwitchoverMariaDBSequenceUnchanged(t *testing.T) {
	a := running(electionCand{url: "a", file: "mariadb-bin.000001", pos: "900", retrieved: "0-1-90", executed: "0-1-90"})
	b := running(electionCand{url: "b", file: "mariadb-bin.000001", pos: "100", retrieved: "0-1-100", executed: "0-1-100"})
	cl, l := newElectionCluster(t, "MariaDB", 11, 8, false, a, b)
	if got := cl.electSwitchoverCandidate(l, true); got != 1 {
		t.Fatalf("MariaDB switchover: elected %d, want 1 (the highest sequence)", got)
	}
}

// gtidSnapshot is an independent deep copy of every monitored GTID field an election could touch.
type gtidSnapshot struct {
	slave, current, binlog []gtid.Gtid
	nilSlave, nilCurrent   bool
	nilBinlog              bool
	executed               string
	replications           []dbhelper.SlaveStatus
}

func listCopy(l *gtid.List) ([]gtid.Gtid, bool) {
	if l == nil {
		return nil, true
	}
	return append([]gtid.Gtid{}, (*l)...), false
}

func snapshotGTID(s *ServerMonitor) gtidSnapshot {
	var g gtidSnapshot
	g.slave, g.nilSlave = listCopy(s.SlaveGtid)
	g.current, g.nilCurrent = listCopy(s.CurrentGtid)
	g.binlog, g.nilBinlog = listCopy(s.GTIDBinlogPos)
	g.executed = s.GTIDExecuted
	g.replications = append([]dbhelper.SlaveStatus{}, s.Replications...)
	return g
}

// The election only computes a local value: no monitored GTID field of any server changes.
func TestElectionDoesNotMutateGTIDFields(t *testing.T) {
	type scenario struct {
		name   string
		flavor string
		failed bool
		cands  []electionCand
	}
	for _, sc := range []scenario{
		{"MySQL decimal UUID, failed primary", "MySQL", true, []electionCand{mysqlCand("a", true, "100", decimalUUID), mysqlCand("b", false, "200", decimalUUID)}},
		{"MySQL, primary up", "MySQL", false, []electionCand{mysqlCand("a", true, "100", decimalUUID), mysqlCand("b", false, "200", decimalUUID)}},
		{"MySQL, candidate without status", "MySQL", true, []electionCand{mysqlCand("a", true, "100", hexUUID), {url: "c", noStatus: true}}},
		{"MariaDB", "MariaDB", true, []electionCand{
			running(electionCand{url: "a", file: "m.000001", pos: "9", retrieved: "0-1-90", executed: "0-1-90"}),
			running(electionCand{url: "b", file: "m.000001", pos: "5", retrieved: "0-1-100", executed: "0-1-100"})}},
	} {
		t.Run(sc.name, func(t *testing.T) {
			for _, switchover := range []bool{false, true} {
				cl, l := newElectionCluster(t, sc.flavor, 8, 4, sc.failed, sc.cands...)
				if sc.flavor == "MariaDB" {
					cl.master.DBVersion = electionVersion("MariaDB", 11, 8)
				}
				cl.Conf.FailRestartUnsafe = true
				all := append([]*ServerMonitor{cl.master}, l...)
				before := make([]gtidSnapshot, len(all))
				for i, s := range all {
					before[i] = snapshotGTID(s)
				}
				if switchover {
					cl.electSwitchoverCandidate(l, true)
				} else {
					cl.electFailoverCandidate(l, true)
				}
				for i, s := range all {
					if after := snapshotGTID(s); !reflect.DeepEqual(before[i], after) {
						t.Fatalf("switchover=%v: GTID fields of %s changed by the election:\nbefore %+v\nafter  %+v", switchover, s.URL, before[i], after)
					}
				}
			}
		})
	}
}

func TestBinlogRank(t *testing.T) {
	for _, c := range []struct {
		file, pos string
		want      uint64
	}{
		{"mysql-bin.000001", "1857", 1000000001857},
		{"db1.example.com-bin.000007", "1857", 7000000001857}, // the last dot: the suffix, not "example"
		{"mysql-bin.000001", "123456789012", 1123456789012},
		{"mysql-bin.index", "10", 0},                      // no numeric suffix: ParseUint syntax error, 0 as before
		{"a.1.000003", "5", 3000000000005},                // numeric middle segment no longer read
		{"mysql-bin.99999999", "1", 18446744073709551615}, // overflow keeps the ParseUint result
	} {
		if got := binlogRank(c.file, c.pos); got != c.want {
			t.Errorf("binlogRank(%q, %q) = %d, want %d", c.file, c.pos, got, c.want)
		}
	}
}

// The correction does not weaken the existing safety checks: an ineligible, ignored, diverged or
// out-of-sync-semi-sync replica is never elected. With a hex UUID these fixtures give the same
// decisions on develop. With a decimal UUID develop tied the candidates and elected the lagging
// eligible replica where a strictly more advanced one was ineligible (the "semi-sync guard" and
// "ignored" failover cases): the accepted availability change of #2451 (M3), now a refusal.
func TestElectionKeepsSafetyChecks(t *testing.T) {
	for _, uuid := range []string{decimalUUID, hexUUID} {
		t.Run("uuid "+uuid, func(t *testing.T) {
			a := mysqlCand("a", false, "100", uuid)
			b := mysqlCand("b", false, "200", uuid)

			// strict semi-sync (failover-at-sync): the most advanced replica out of sync is not elected
			cl, l := newElectionCluster(t, "MySQL", 8, 4, true, a, b)
			cl.Conf.FailSync = true
			l[1].HaveSemiSync, l[1].SemiSyncSlaveStatus = true, false
			if got := cl.electFailoverCandidate(l, true); got != -1 {
				t.Fatalf("semi-sync guard: failover elected %d, want -1", got)
			}
			cl, l = newElectionCluster(t, "MySQL", 8, 4, false, a, b)
			cl.Conf.SwitchSync = true
			l[1].HaveSemiSync, l[1].SemiSyncSlaveStatus = true, false
			if got := cl.electSwitchoverCandidate(l, true); got != 0 {
				t.Fatalf("semi-sync guard: switchover elected %d, want 0 (the guard excludes b)", got)
			}

			// ignored host: never elected
			cl, l = newElectionCluster(t, "MySQL", 8, 4, true, a, b)
			l[1].Ignored = true
			if got := cl.electFailoverCandidate(l, true); got != -1 {
				t.Fatalf("ignored most advanced: failover elected %d, want -1", got)
			}
			cl, l = newElectionCluster(t, "MySQL", 8, 4, false, a, b)
			l[1].Ignored = true
			if got := cl.electSwitchoverCandidate(l, true); got != 0 {
				t.Fatalf("ignored most advanced: switchover elected %d, want 0", got)
			}

			// data divergence: the diverged replica is skipped before any ranking
			cl, l = newElectionCluster(t, "MySQL", 8, 4, true, a, b)
			l[1].IsDataDiverge = true
			if got := cl.electFailoverCandidate(l, true); got != 0 {
				t.Fatalf("diverged most advanced: failover elected %d, want 0", got)
			}

			// errant transactions: the failover election only reports them (the automatic failover is
			// held by its own check), the switchover excludes the replica
			cl, l = newElectionCluster(t, "MySQL", 8, 4, true, a, b)
			cl.SetState("WARN0091", state.State{ErrType: config.LvlWarn, ErrDesc: "errant", ServerUrl: l[1].URL, ErrFrom: "CHECK"})
			if !l[1].HasErrantTransactions() {
				t.Fatalf("fixture: errant state not set")
			}
			if got := cl.electFailoverCandidate(l, true); got != 1 {
				t.Fatalf("errant most advanced: failover elected %d, want 1 (election only reports it)", got)
			}
			cl, l = newElectionCluster(t, "MySQL", 8, 4, false, a, b)
			cl.SetState("WARN0091", state.State{ErrType: config.LvlWarn, ErrDesc: "errant", ServerUrl: l[1].URL, ErrFrom: "CHECK"})
			if got := cl.electSwitchoverCandidate(l, true); got != 0 {
				t.Fatalf("errant most advanced: switchover elected %d, want 0", got)
			}
		})
	}
}

// PostgreSQL candidates keep the sequence ranking path (only MySQL and Percona changed): the replica with
// the highest sequence of the master's domain wins whatever the coordinates, failed primary or not.
func TestElectPostgreSQLSequenceUnchanged(t *testing.T) {
	a := running(electionCand{url: "a", file: "pg.000001", pos: "900", retrieved: "0-1-90", executed: "0-1-90", prefered: true})
	b := running(electionCand{url: "b", file: "pg.000001", pos: "100", retrieved: "0-1-100", executed: "0-1-100"})
	for _, failed := range []bool{true, false} {
		cl, l := newElectionCluster(t, "PostgreSQL", 16, 0, failed, a, b)
		if got := cl.electFailoverCandidate(l, true); got != 1 {
			t.Fatalf("PostgreSQL failover (master failed=%v): elected %d, want 1 (the highest sequence)", failed, got)
		}
	}
}
