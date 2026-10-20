// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/utils/version"
)

// A splitdump restore positions a MariaDB server at the backup's GTID whatever the monitored
// channel: a child cluster's master has none (HaveMariaDBGTID false) and was left without
// gtid_slave_pos, its channel to the parent stopped with 1236 (#2124). MySQL keeps its rule.
func TestSplitdumpGTIDStatement(t *testing.T) {
	mariadb := &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 6}
	mysql := &version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 40}

	for _, monitoredGTID := range []bool{false, true} {
		server, _ := newMyLoaderPositionServer(t, mariadb, monitoredGTID)
		query, flavor := server.splitdumpGTIDStatement("4-53070043-3245370")
		if query != "SET GLOBAL gtid_slave_pos='4-53070043-3245370'" || flavor != "MariaDB" {
			t.Fatalf("MariaDB, monitored channel GTID %v: %q %q", monitoredGTID, query, flavor)
		}
	}

	server, _ := newMyLoaderPositionServer(t, mysql, true)
	if query, _ := server.splitdumpGTIDStatement("uuid:1-5"); query != "SET @@GLOBAL.GTID_PURGED='uuid:1-5'" {
		t.Fatalf("MySQL with GTID: %q", query)
	}
	server, _ = newMyLoaderPositionServer(t, mysql, false)
	if query, _ := server.splitdumpGTIDStatement("uuid:1-5"); query != "" {
		t.Fatalf("MySQL without GTID sets nothing: %q", query)
	}

	server, _ = newMyLoaderPositionServer(t, mariadb, false)
	if query, _ := server.splitdumpGTIDStatement("0-1-1'; DROP TABLE x; --"); query != "SET GLOBAL gtid_slave_pos='0-1-1''; DROP TABLE x; --'" {
		t.Fatalf("the quote is escaped: %q", query)
	}
}
