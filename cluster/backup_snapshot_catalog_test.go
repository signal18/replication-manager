// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/utils/backupmgr"
)

func hasCap(caps []string, want string) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

func TestIngestSnapshotsAndCatalogue(t *testing.T) {
	cluster := &Cluster{BackupMetaMap: backupmgr.NewBackupMetaMap()}
	server := &ServerMonitor{URL: "db1:3306", ClusterGroup: cluster}

	n := server.IngestSnapshots("tank/db", []SnapshotEntry{
		{Name: "tank/db@daily-1", CreationUnix: 1000, SizeBytes: 4096},
		{Name: "tank/db@daily-2", CreationUnix: 2000, SizeBytes: 8192},
	})
	if n != 2 {
		t.Fatalf("ingested %d, want 2", n)
	}

	// Re-ingesting the same snapshot must update in place (stable id), not duplicate.
	server.IngestSnapshots("tank/db", []SnapshotEntry{
		{Name: "tank/db@daily-1", CreationUnix: 1000, SizeBytes: 4096},
	})

	cat := cluster.buildBackupCatalog()
	snaps := 0
	for _, e := range cat {
		if e.Kind != "snapshot" {
			continue
		}
		snaps++
		if e.Tool != "zfs" {
			t.Errorf("snapshot tool=%q, want zfs", e.Tool)
		}
		for _, want := range []string{"fast-restore", "needs-db-restart", "work-for-prov"} {
			if !hasCap(e.Caps, want) {
				t.Errorf("snapshot %q missing cap %q (caps=%v)", e.Path, want, e.Caps)
			}
		}
		if hasCap(e.Caps, "corruption-verified") {
			t.Errorf("snapshot must NOT be corruption-verified (caps=%v)", e.Caps)
		}
	}
	if snaps != 2 {
		t.Fatalf("catalogue snapshot entries=%d, want 2 (no duplicate from re-ingest)", snaps)
	}
}

func TestParseAndIngestSnapshotList(t *testing.T) {
	cluster := &Cluster{BackupMetaMap: backupmgr.NewBackupMetaMap()}
	server := &ServerMonitor{URL: "db1:3306", ClusterGroup: cluster}

	js := []byte(`{"ok":true,"type":"zfs","dataset":"tank/db",` +
		`"snapshots":[{"name":"tank/db@a","creation":1000,"size":4096},` +
		`{"name":"tank/db@b","creation":2000,"size":8192}]}`)
	n, err := server.ParseAndIngestSnapshotList(js)
	if err != nil {
		t.Fatalf("parse+ingest: %v", err)
	}
	if n != 2 {
		t.Fatalf("ingested %d, want 2", n)
	}

	// A failed plugin result is surfaced as an error, not silently catalogued.
	if _, err := server.ParseAndIngestSnapshotList([]byte(`{"ok":false,"error":"boom"}`)); err == nil {
		t.Errorf("expected error for ok=false result")
	}
}

func TestBackupMetaToCatalogCapsByMethod(t *testing.T) {
	cases := []struct {
		method   int
		wantKind string
		wantCaps []string
		notCaps  []string
	}{
		// can-repair-online is NOT auto-derived — it needs the --replace flag recorded.
		{backupmgr.BackupMethodLogical, "logical", []string{"corruption-verified"}, []string{"work-for-prov", "can-repair-online"}},
		{backupmgr.BackupMethodPhysical, "physical", []string{"needs-db-restart", "work-for-prov"}, []string{"corruption-verified"}},
		{backupmgr.BackupMethodSnapshot, "snapshot", []string{"fast-restore", "needs-db-restart", "work-for-prov"}, []string{"corruption-verified", "can-repair-online"}},
	}
	for _, c := range cases {
		e := backupMetaToCatalog("db1:3306", &backupmgr.BackupMetadata{BackupMethod: backupmgr.BackupMethod(c.method), BackupTool: "x"})
		if e.Kind != c.wantKind {
			t.Errorf("method %d: kind=%q want %q", c.method, e.Kind, c.wantKind)
		}
		for _, w := range c.wantCaps {
			if !hasCap(e.Caps, w) {
				t.Errorf("method %d: missing cap %q (caps=%v)", c.method, w, e.Caps)
			}
		}
		for _, nc := range c.notCaps {
			if hasCap(e.Caps, nc) {
				t.Errorf("method %d: unexpected cap %q (caps=%v)", c.method, nc, e.Caps)
			}
		}
	}
}
