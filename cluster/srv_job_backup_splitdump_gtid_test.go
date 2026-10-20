// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/signal18/replication-manager/utils/version"
)

// A splitdump restore positions a MariaDB server at the backup's GTID whatever the monitored
// channel (a child cluster's master has none, HaveMariaDBGTID false), the other domains it
// replicates kept (#2124); MySQL keeps GTID_PURGED after the load.
func TestSplitdumpGTIDSlavePos(t *testing.T) {
	mariadb := &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 6}
	for _, monitoredGTID := range []bool{false, true} {
		server, mock := newMyLoaderPositionServer(t, mariadb, monitoredGTID)
		mock.ExpectQuery("SELECT Variable_Value AS Value FROM .*global_variables WHERE Variable_Name = ?").
			WithArgs("GTID_SLAVE_POS").
			WillReturnRows(sqlmock.NewRows([]string{"Value"}).AddRow("4-1-10,7-3-99"))
		mock.ExpectExec("SET GLOBAL gtid_slave_pos = ?").
			WithArgs("4-53070043-3245370,7-3-99").
			WillReturnResult(sqlmock.NewResult(0, 0))
		if _, err := server.splitdumpGTIDSlavePos("4-53070043-3245370"); err != nil {
			t.Fatalf("monitored channel GTID %v: %s", monitoredGTID, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("monitored channel GTID %v: %s", monitoredGTID, err)
		}
		if q := server.splitdumpGTIDPurged("4-53070043-3245370"); q != "" {
			t.Fatalf("a MariaDB server takes no GTID_PURGED: %q", q)
		}
	}

	mysql := &version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 40}
	server, _ := newMyLoaderPositionServer(t, mysql, true)
	if q := server.splitdumpGTIDPurged("uuid:1-5'"); q != "SET @@GLOBAL.GTID_PURGED='uuid:1-5'''" {
		t.Fatalf("MySQL with GTID: %q", q)
	}
	server, _ = newMyLoaderPositionServer(t, mysql, false)
	if q := server.splitdumpGTIDPurged("uuid:1-5"); q != "" {
		t.Fatalf("MySQL without GTID sets nothing: %q", q)
	}
}
