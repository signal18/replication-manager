package cluster

import (
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

// After myloader, the replica's replication state is set to the mydumper backup's position:
// MySQL/Percona with GTID resets its binary logs and GTIDs, then sets GTID_PURGED from the
// metadata (it kept the GTIDs of what it applied before the restore, and would diverge);
// MariaDB with GTID sets gtid_slave_pos; without GTID it is pointed at the backup's file and
// position.
func TestApplyMyLoaderReplicaPosition(t *testing.T) {
	newServer := func(t *testing.T, ver *version.Version, gtid bool) (*ServerMonitor, sqlmock.Sqlmock) {
		t.Helper()
		_, server := newStagingTestServer(t)
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		server.Conn = sqlx.NewDb(db, "sqlmock")
		server.DBVersion = ver
		server.Variables = config.NewStringsMap()
		if gtid {
			server.Variables.Set("GTID_MODE", "ON")
		}
		server.HaveMariaDBGTID = gtid && ver.IsMariaDB()
		return server, mock
	}
	mysql84 := &version.Version{Flavor: "MySQL", Major: 8, Minor: 4, Release: 11}
	mariadb := &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11, Release: 19}
	meta := config.MyDumperMetaData{
		MetaDir:        "/backups/mydumper",
		BinLogFileName: "binlog.000031",
		BinLogFilePos:  113174,
		BinLogUuid:     "20191616-c6ca-11f1-8bf0-c264eed37e71:1-2,\n22ebff02-c6ca-11f1-befd-daa722c63c3e:1-832",
	}

	t.Run("MySQL with GTID: reset, then GTID_PURGED", func(t *testing.T) {
		server, mock := newServer(t, mysql84, true)
		mock.ExpectExec("RESET BINARY LOGS AND GTIDS").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("set session sql_log_bin=0").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(`SET @@GLOBAL.GTID_PURGED='20191616-c6ca-11f1-8bf0-c264eed37e71:1-2,22ebff02-c6ca-11f1-befd-daa722c63c3e:1-832'`).WillReturnResult(sqlmock.NewResult(0, 0))
		if err := server.applyMyLoaderReplicaPosition(meta); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("MySQL with GTID, no GTID set in the backup: refused, nothing sent", func(t *testing.T) {
		server, mock := newServer(t, mysql84, true)
		noGTID := meta
		noGTID.BinLogUuid = ""
		if err := server.applyMyLoaderReplicaPosition(noGTID); err == nil || !strings.Contains(err.Error(), "no executed GTID set") {
			t.Fatalf("want a refusal, got %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("without GTID: the backup's file and position", func(t *testing.T) {
		server, mock := newServer(t, mysql84, false)
		_, primary := newStagingTestServer(t)
		primary.Host, primary.Port, primary.URL = "n1", "3306", "n1:3306"
		server.ClusterGroup.master = primary
		mock.ExpectExec(`(?s)CHANGE (MASTER|REPLICATION SOURCE) TO.*binlog\.000031.*113174`).WillReturnResult(sqlmock.NewResult(0, 0))
		if err := server.applyMyLoaderReplicaPosition(meta); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("MariaDB with GTID: gtid_slave_pos", func(t *testing.T) {
		server, mock := newServer(t, mariadb, true)
		mdb := meta
		mdb.BinLogUuid = "0-90125166-665,97-90125166-2"
		mock.ExpectExec("set session sql_log_bin=0").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(`SET GLOBAL gtid_slave_pos='0-90125166-665,97-90125166-2'`).WillReturnResult(sqlmock.NewResult(0, 0))
		if err := server.applyMyLoaderReplicaPosition(mdb); err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}
