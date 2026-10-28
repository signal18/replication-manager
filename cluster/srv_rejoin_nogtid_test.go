package cluster

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

// mysqlGTIDServer is a MySQL 8.0 server whose GTID variables answer gtidMode, with a
// connection that accepts nothing: a statement sent fails the test's expectations.
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
	s.Variables.Set("ENFORCE_GTID_CONSISTENCY", gtidMode)
	return s, mock
}

// A MySQL replica or source without GTID is never sent MASTER_AUTO_POSITION=1 (Error
// 1777, #2226): the rejoin helpers return errNoMySQLGTID and issue no statement.
func TestRejoinGTIDHelpersRefuseMySQLWithoutGTID(t *testing.T) {
	c := &Cluster{Name: "c1", Conf: &config.Config{}}
	for _, tc := range []struct{ name, replica, source string }{
		{"both off", "OFF", "OFF"},
		{"replica off", "OFF", "ON"},
		{"source off", "ON", "OFF"},
	} {
		replica, mock := mysqlGTIDServer(t, c, "db2", tc.replica)
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
	replica, _ := mysqlGTIDServer(t, c, "db2", "ON")
	source, _ := mysqlGTIDServer(t, c, "db1", "ON")
	if !replica.mysqlGTIDRejoinable(source) {
		t.Fatalf("GTID on both: rejoinable on GTID")
	}
	maria := &ServerMonitor{DBVersion: &version.Version{Flavor: "MariaDB", Major: 11, Minor: 4}, Variables: config.NewStringsMap()}
	if !maria.mysqlGTIDRejoinable(nil) {
		t.Fatalf("MariaDB always has its GTID position")
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
	c.forgetRejoinMarker(url)
	if c.rejoinAlreadyAttempted(url) {
		t.Fatalf("back from a failure: a new attempt is owed")
	}
	if !c.rejoinAlreadyAttempted("db3:3306") {
		t.Fatalf("another server's marker is kept")
	}
}

// A MySQL or Percona cluster is provisioned with the mysqlgtid tag; MariaDB and group
// replication are not.
func TestAddMySQLGTIDTag(t *testing.T) {
	for _, tc := range []struct {
		name, image string
		grouprep    bool
		want        bool
	}{
		{"mysql", "mysql:8.4", false, true},
		{"percona", "percona/percona-server:8.0", false, true},
		{"mariadb", "mariadb:11.8", false, false},
		{"group replication", "mysql:8.4", true, false},
	} {
		c := newColumnstoreTestCluster()
		c.Conf.ProvDbImg, c.Conf.MultiMasterGrouprep = tc.image, tc.grouprep
		c.addMySQLGTIDTag()
		if got := c.Configurator.HaveDBTag(ProvisionMySQLGTIDTag); got != tc.want {
			t.Errorf("%s: tag %t, want %t (tags %s)", tc.name, got, tc.want, c.Conf.ProvTags)
		}
	}
}
