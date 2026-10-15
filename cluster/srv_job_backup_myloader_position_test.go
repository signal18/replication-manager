package cluster

import (
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

// newMyLoaderPositionServer is a replica of ver on a sqlmock connection, replicating with GTID
// when gtid (MySQL/Percona: GTID_MODE ON; MariaDB: HaveMariaDBGTID).
func newMyLoaderPositionServer(t *testing.T, ver *version.Version, gtid bool) (*ServerMonitor, sqlmock.Sqlmock) {
	t.Helper()
	c, server := newStagingTestServer(t)
	// a failed statement is logged to the cluster's SQL logs
	c.SqlErrorLog, c.SqlGeneralLog = logrus.New(), logrus.New()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	server.Conn = sqlx.NewDb(db, "sqlmock")
	server.DBVersion = ver
	server.Variables = config.NewStringsMap()
	if gtid && !ver.IsMariaDB() {
		server.Variables.Set("GTID_MODE", "ON")
	}
	server.HaveMariaDBGTID = gtid && ver.IsMariaDB()
	return server, mock
}

// withPrimary gives the replica's cluster a primary to point it at.
func withPrimary(t *testing.T, server *ServerMonitor) {
	t.Helper()
	_, primary := newStagingTestServer(t)
	primary.Host, primary.Port, primary.URL = "n1", "3306", "n1:3306"
	server.ClusterGroup.master = primary
}

var (
	myloaderMySQL84   = &version.Version{Flavor: "MySQL", Major: 8, Minor: 4, Release: 11}
	myloaderMySQL80   = &version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 35}
	myloaderPercona80 = &version.Version{Flavor: "Percona", Major: 8, Minor: 0, Release: 46}
	myloaderMariaDB   = &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11, Release: 19}
	myloaderMeta      = config.MyDumperMetaData{
		MetaDir:        "/backups/mydumper",
		BinLogFileName: "binlog.000031",
		BinLogFilePos:  113174,
		BinLogUuid:     "20191616-c6ca-11f1-8bf0-c264eed37e71:1-2,\n22ebff02-c6ca-11f1-befd-daa722c63c3e:1-832",
	}
)

const myloaderMetaGtid = "20191616-c6ca-11f1-8bf0-c264eed37e71:1-2,22ebff02-c6ca-11f1-befd-daa722c63c3e:1-832"

// After myloader, MySQL/Percona with GTID resets its binary logs and GTIDs (the statement of
// its version), then sets GTID_PURGED from the metadata: it kept the GTIDs of what it applied
// before the restore, and would diverge.
func TestApplyMyLoaderReplicaPositionMySQLGTID(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ver   *version.Version
		reset string
	}{
		{"MySQL 8.4", myloaderMySQL84, "RESET BINARY LOGS AND GTIDS"},
		{"MySQL 8.0", myloaderMySQL80, "RESET MASTER"},
		{"Percona 8.0", myloaderPercona80, "RESET MASTER"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, mock := newMyLoaderPositionServer(t, tc.ver, true)
			mock.ExpectExec("^" + tc.reset + "$").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec("set session sql_log_bin=0").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec(`SET @@GLOBAL.GTID_PURGED='` + myloaderMetaGtid + `'`).WillReturnResult(sqlmock.NewResult(0, 0))
			if err := server.applyMyLoaderReplicaPosition(myloaderMeta); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A step that fails is returned, and nothing after it runs: replication is not started from
// the position the replica had before the load.
func TestApplyMyLoaderReplicaPositionFailures(t *testing.T) {
	t.Run("MySQL: no GTID set in the backup, nothing sent", func(t *testing.T) {
		server, mock := newMyLoaderPositionServer(t, myloaderMySQL84, true)
		meta := myloaderMeta
		meta.BinLogUuid = " \n"
		if err := server.applyMyLoaderReplicaPosition(meta); err == nil || !strings.Contains(err.Error(), "no executed GTID set") {
			t.Fatalf("want a refusal, got %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("MySQL: the reset fails, GTID_PURGED not sent", func(t *testing.T) {
		server, mock := newMyLoaderPositionServer(t, myloaderMySQL84, true)
		mock.ExpectExec("RESET BINARY LOGS AND GTIDS").WillReturnError(errors.New("denied"))
		if err := server.applyMyLoaderReplicaPosition(myloaderMeta); err == nil || !strings.Contains(err.Error(), "reset the binary logs") {
			t.Fatalf("want the reset error, got %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("MySQL: GTID_PURGED fails", func(t *testing.T) {
		server, mock := newMyLoaderPositionServer(t, myloaderMySQL84, true)
		mock.ExpectExec("RESET BINARY LOGS AND GTIDS").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("set session sql_log_bin=0").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("SET @@GLOBAL.GTID_PURGED").WillReturnError(errors.New("GTID_PURGED can only be set when GTID_EXECUTED is empty"))
		if err := server.applyMyLoaderReplicaPosition(myloaderMeta); err == nil || !strings.Contains(err.Error(), "GTID_PURGED") {
			t.Fatalf("want the GTID_PURGED error, got %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("MariaDB: gtid_slave_pos fails", func(t *testing.T) {
		server, mock := newMyLoaderPositionServer(t, myloaderMariaDB, true)
		mock.ExpectExec("set session sql_log_bin=0").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("SET GLOBAL gtid_slave_pos").WillReturnError(errors.New("denied"))
		if err := server.applyMyLoaderReplicaPosition(myloaderMeta); err == nil || !strings.Contains(err.Error(), "gtid_slave_pos") {
			t.Fatalf("want the gtid_slave_pos error, got %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("positional: no primary known", func(t *testing.T) {
		server, mock := newMyLoaderPositionServer(t, myloaderMySQL84, false)
		if err := server.applyMyLoaderReplicaPosition(myloaderMeta); err == nil || !strings.Contains(err.Error(), "no primary") {
			t.Fatalf("want a refusal, got %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("positional: no binary log file in the backup", func(t *testing.T) {
		server, mock := newMyLoaderPositionServer(t, myloaderMySQL84, false)
		withPrimary(t, server)
		meta := myloaderMeta
		meta.BinLogFileName = ""
		if err := server.applyMyLoaderReplicaPosition(meta); err == nil || !strings.Contains(err.Error(), "no replication position") {
			t.Fatalf("want a refusal, got %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

// MariaDB with GTID sets gtid_slave_pos (the set sanitised as MySQL's); without a GTID set in
// the backup, or without GTID, the replica is pointed at the backup's file and position.
func TestApplyMyLoaderReplicaPositionMariaDBAndPositional(t *testing.T) {
	t.Run("MariaDB with GTID: gtid_slave_pos, quotes doubled, lines joined", func(t *testing.T) {
		server, mock := newMyLoaderPositionServer(t, myloaderMariaDB, true)
		meta := myloaderMeta
		meta.BinLogUuid = "0-90125166-665,\n97-90125166-2'x"
		mock.ExpectExec("set session sql_log_bin=0").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(`SET GLOBAL gtid_slave_pos='0-90125166-665,97-90125166-2''x'`).WillReturnResult(sqlmock.NewResult(0, 0))
		if err := server.applyMyLoaderReplicaPosition(meta); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
	for _, tc := range []struct {
		name string
		ver  *version.Version
		gtid bool
		uuid string
	}{
		{"MySQL without GTID", myloaderMySQL84, false, myloaderMeta.BinLogUuid},
		{"MariaDB with GTID, no GTID set in the backup", myloaderMariaDB, true, ""},
		{"MariaDB without GTID", myloaderMariaDB, false, "0-1-5"},
	} {
		t.Run(tc.name+": the backup's file and position", func(t *testing.T) {
			server, mock := newMyLoaderPositionServer(t, tc.ver, tc.gtid)
			withPrimary(t, server)
			meta := myloaderMeta
			meta.BinLogUuid = tc.uuid
			mock.ExpectExec(`(?s)CHANGE (MASTER|REPLICATION SOURCE) TO.*binlog\.000031.*113174`).WillReturnResult(sqlmock.NewResult(0, 0))
			if err := server.applyMyLoaderReplicaPosition(meta); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
