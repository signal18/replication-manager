// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package backupmgr

import "testing"

// #2091: the snapshots a tagged purge counts together, as restic forget --group-by does
func TestResticPurgeGroup(t *testing.T) {
	a := BackupSnapshot{Hostname: "h1", Paths: []string{"/b", "/a"}, Tags: []string{"y", "x"}}
	b := BackupSnapshot{Hostname: "h1", Paths: []string{"/a", "/b"}, Tags: []string{"z"}}
	if resticPurgeGroup("", a) != resticPurgeGroup("default", b) || resticPurgeGroup("", a) != "host=h1;paths=/a,/b" {
		t.Fatalf("host,paths by default: %q %q", resticPurgeGroup("", a), resticPurgeGroup("", b))
	}
	if resticPurgeGroup("host,tags", a) == resticPurgeGroup("host,tags", b) {
		t.Fatal("tags not counted apart")
	}
	if resticPurgeGroup("none", a) != "" {
		t.Fatal("none groups everything together")
	}
}
