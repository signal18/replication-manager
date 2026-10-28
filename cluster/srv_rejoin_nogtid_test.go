package cluster

import (
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

// mysqlGTIDServer is a MySQL 8.0 server with gtid_mode gtidMode (enforce_gtid_consistency
// ON unless gtidMode is OFF), with a connection that accepts nothing: a statement sent
// fails the test's expectations.
func mysqlGTIDServer(t *testing.T, c *Cluster, host, gtidMode string) (*ServerMonitor, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := &ServerMonitor{URL: host + ":3306", Host: host, Port: "3306", ClusterGroup: c, Conn: sqlx.NewDb(db, "sqlmock"),
		DBVersion: &version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 41}, Variables: config.NewStringsMap()}
	s.Variables.Set("GTID_MODE", gtidMode)
	s.Variables.Set("ENFORCE_GTID_CONSISTENCY", "ON")
	if gtidMode == "OFF" {
		s.Variables.Set("ENFORCE_GTID_CONSISTENCY", "OFF")
	}
	return s, mock
}

// A MySQL replica or source without GTID is never sent MASTER_AUTO_POSITION=1 (Error
// 1777, #2226): the rejoin helpers return errNoMySQLGTID and issue no statement. The
// gate reads gtid_mode itself: enforce_gtid_consistency=ON with gtid_mode=OFF (the first
// step of the migration) has no GTID position either.
func TestRejoinGTIDHelpersRefuseMySQLWithoutGTID(t *testing.T) {
	c := &Cluster{Name: "c1", Conf: &config.Config{}}
	for _, tc := range []struct{ name, replica, source string }{
		{"both off", "OFF", "OFF"},
		{"replica off", "OFF", "ON"},
		{"source off", "ON", "OFF"},
		{"enforce on, gtid_mode off", "OFF_ENFORCED", "ON"},
		{"source on_permissive", "ON", "ON_PERMISSIVE"},
	} {
		replica, mock := mysqlGTIDServer(t, c, "db2", tc.replica)
		if tc.replica == "OFF_ENFORCED" {
			replica.Variables.Set("GTID_MODE", "OFF")
			replica.Variables.Set("ENFORCE_GTID_CONSISTENCY", "ON")
		}
		source, _ := mysqlGTIDServer(t, c, "db1", tc.source)
		if _, err := replica.SetReplicationGTIDCurrentPosFromServer(source); !errors.Is(err, errNoMySQLGTID) {
			t.Errorf("%s, current pos: %v", tc.name, err)
		}
		if _, err := replica.SetReplicationGTIDSlavePosFromServer(source); !errors.Is(err, errNoMySQLGTID) {
			t.Errorf("%s, slave pos: %v", tc.name, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	for _, replicaMode := range []string{"ON", "ON_PERMISSIVE"} {
		replica, _ := mysqlGTIDServer(t, c, "db2", replicaMode)
		source, _ := mysqlGTIDServer(t, c, "db1", "ON")
		if err := replica.mysqlGTIDRejoinError(source); err != nil {
			t.Fatalf("replica %s, source ON: rejoinable on GTID, got %v", replicaMode, err)
		}
	}
	replica, _ := mysqlGTIDServer(t, c, "db2", "ON")
	source, _ := mysqlGTIDServer(t, c, "db1", "ON")
	forced := &Cluster{Name: "c2", Conf: &config.Config{ForceSlaveNoGtid: true}}
	replica.ClusterGroup = forced
	if err := replica.mysqlGTIDRejoinError(source); !errors.Is(err, errNoMySQLGTID) || !strings.Contains(err.Error(), "force-slave-no-gtid") {
		t.Fatalf("force-slave-no-gtid names itself: %v", err)
	}
	maria := &ServerMonitor{DBVersion: &version.Version{Flavor: "MariaDB", Major: 11, Minor: 4}, Variables: config.NewStringsMap()}
	if err := maria.mysqlGTIDRejoinError(nil); err != nil {
		t.Fatalf("MariaDB always has its GTID position: %v", err)
	}
}

// The outcome of a rejoin without a crash has no archive: it survives the rebuild of the
// history from disk, so the one-shot guard holds and a failed attach does not run again
// at every tick (#2226). One per server; a server back from a failure is owed a new attempt.
func TestRejoinMarkerSurvivesHistoryRebuild(t *testing.T) {
	c := &Cluster{Name: "t", Conf: &config.Config{}}
	c.WorkingDir = t.TempDir()
	url := "db2:3306"
	c.finishRejoin(url, RejoinResultFailed)
	c.finishRejoin(url, RejoinResultFailed)
	c.finishRejoin("db3:3306", RejoinResultNoDivergence)
	if !c.rejoinAlreadyAttempted(url) {
		t.Fatalf("the archive-less outcome must block the next tick's rejoin: %+v", c.FailoverHistory)
	}
	c.LoadFailoverHistory()
	if len(c.FailoverHistory) != 2 {
		t.Fatalf("one marker per server, got %d: %+v", len(c.FailoverHistory), c.FailoverHistory)
	}
	if !c.rejoinAlreadyAttempted(url) {
		t.Fatalf("the marker must survive another rebuild")
	}
	// an explicit re-arm runs once more, and its outcome replaces the marker
	if !c.rearmRejoin(url, "") {
		t.Fatalf("the marker can be re-armed")
	}
	if c.rejoinAlreadyAttempted(url) {
		t.Fatalf("a re-armed rejoin is owed its attempt")
	}
	c.finishRejoin(url, RejoinResultSuccess)
	if !c.rejoinAlreadyAttempted(url) {
		t.Fatalf("the re-armed attempt's outcome blocks again")
	}
	n := 0
	for _, cr := range c.FailoverHistory {
		if cr.URL == url {
			n++
			if cr.RejoinResult != RejoinResultSuccess {
				t.Fatalf("the newest outcome is kept: %+v", cr)
			}
		}
	}
	if n != 1 {
		t.Fatalf("one marker for %s after the re-arm, got %d", url, n)
	}
	c.forgetRejoinMarker(url)
	if c.rejoinAlreadyAttempted(url) {
		t.Fatalf("back from a failure: a new attempt is owed")
	}
	if !c.rejoinAlreadyAttempted("db3:3306") {
		t.Fatalf("another server's marker is kept")
	}
}

// A MySQL or Percona cluster is provisioned with the mysqlgtid tag, recognized by its
// image repository; MariaDB, Percona XtraDB Cluster and group replication are not, nor a
// cluster whose servers are running (Bootstrap on an existing cluster).
func TestAddMySQLGTIDTag(t *testing.T) {
	for _, tc := range []struct {
		name, image string
		grouprep    bool
		running     bool
		want        bool
	}{
		{"mysql", "mysql:8.4", false, false, true},
		{"mysql on a registry", "registry.example/team/mysql:8.4@sha256:0123", false, false, true},
		{"oracle mysql-server", "container-registry.oracle.com/mysql/mysql-server:8.0", false, false, true},
		{"percona", "percona/percona-server:8.0", false, false, true},
		{"mariadb", "mariadb:11.8", false, false, false},
		{"name containing mysql", "team/mysql-compat-mariadb:11.4", false, false, false},
		{"percona xtradb cluster", "percona/percona-xtradb-cluster:8.0", false, false, false},
		{"group replication", "mysql:8.4", true, false, false},
		{"servers running", "mysql:8.4", false, true, false},
	} {
		c := newColumnstoreTestCluster()
		c.Conf.ProvDbImg, c.Conf.MultiMasterGrouprep = tc.image, tc.grouprep
		state := stateFailed
		if tc.running {
			state = stateMaster
		}
		c.Servers = []*ServerMonitor{{URL: "db1:3306", State: state, ClusterGroup: c}}
		c.addMySQLGTIDTag()
		if got := c.Configurator.HaveDBTag(ProvisionMySQLGTIDTag); got != tc.want {
			t.Errorf("%s: tag %t, want %t (tags %s)", tc.name, got, tc.want, c.Conf.ProvTags)
		}
	}
}
