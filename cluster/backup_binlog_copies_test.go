// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "testing"

// the copies sort by their sequence number, never as text (000100 after 000099)
func TestBinlogNumber(t *testing.T) {
	for name, want := range map[string]int{"mysql-bin.000042": 42, "binlog.000100": 100, "x": -1, "a.b": -1} {
		if got := binlogNumber(name); got != want {
			t.Fatalf("%s: %d, want %d", name, got, want)
		}
	}
}
