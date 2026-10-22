// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/signal18/replication-manager/utils/backupmgr"
)

// fakeCatalogueArchive is an archive of snapshots, each holding the backup records given.
func fakeCatalogueArchive(name string, readable bool, snaps map[string][]*backupmgr.BackupMetadata, order []string) (*catalogueArchive, *int) {
	reads := 0
	t0 := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	ca := &catalogueArchive{name: name}
	ca.snapshot = func() ([]catalogueSnapshotRef, error) {
		refs := make([]catalogueSnapshotRef, 0, len(order))
		for i, id := range order {
			refs = append(refs, catalogueSnapshotRef{id: id, time: t0.Add(time.Duration(i) * time.Hour), root: "/backups/db1_3306"})
		}
		return refs, nil
	}
	if !readable {
		return ca, &reads
	}
	ca.contents = func(s catalogueSnapshotRef) ([]string, error) {
		reads++
		names := []string{"binary-logs.meta.json", "binlog.000001", "mydumper"}
		for _, m := range snaps[s.id] {
			names = append(names, m.BackupTool+".meta.json")
		}
		return names, nil
	}
	ca.read = func(s catalogueSnapshotRef, file string) ([]byte, error) {
		for _, m := range snaps[s.id] {
			if m.BackupTool+".meta.json" == file {
				return json.Marshal(m)
			}
		}
		return nil, nil
	}
	return ca, &reads
}

// #2091: the rebuild reads the snapshots the catalogue does not know: a backup only in the
// archive joins it (with the newest snapshot holding it), a backup it has gets a snapshot when
// it records none; a recorded snapshot the archive no longer lists is reported, kept
func TestRebuildBackupCatalogueFromArchive(t *testing.T) {
	c, server := newTestRuntimeOnlyClusterServer(t, "catalogue-rebuild", "db1", "3306")
	c.WorkingDir = t.TempDir()
	c.BackupMetaMap = backupmgr.NewBackupMetaMap()
	current := &backupmgr.BackupMetadata{Id: 300, Source: server.URL, BackupTool: "mydumper", Dest: "/backups/db1_3306/mydumper", Completed: true}
	current.SetArchiveSnapshot("plakar", "s3")
	noRef := &backupmgr.BackupMetadata{Id: 250, Source: server.URL, BackupTool: "mysqldump", Dest: "/backups/db1_3306/mysqldump.sql.gz", Completed: true}
	gone := &backupmgr.BackupMetadata{Id: 50, Source: server.URL, BackupTool: "xtrabackup", Completed: true}
	gone.SetArchiveSnapshot("plakar", "s0-purged")
	for _, m := range []*backupmgr.BackupMetadata{current, noRef, gone} {
		c.BackupMetaMap.Set(m.Id, m)
	}
	old := func(id int64) *backupmgr.BackupMetadata {
		return &backupmgr.BackupMetadata{Id: id, Source: server.URL, BackupTool: "mydumper", Dest: "/backups/db1_3306/mydumper", Completed: true}
	}
	snaps := map[string][]*backupmgr.BackupMetadata{
		"s1": {old(100)},
		"s2": {old(100), {Id: 250, Source: server.URL, BackupTool: "mysqldump"}}, // binlog-only: the same backups
		"s3": {current},
	}
	ca, reads := fakeCatalogueArchive("plakar", true, snaps, []string{"s1", "s2", "s3"})
	res := &CatalogueRebuild{Archive: "plakar"}
	c.rebuildCatalogueWith(ca, res)

	if res.Error != "" || res.Snapshots != 3 || res.Read != 2 || *reads != 2 {
		t.Fatalf("outcome %+v, %d contents read (s3 is known)", res, *reads)
	}
	m := c.BackupMetaMap.Get(100)
	if m == nil || !m.Replaced || !m.OriginalRemoved || m.ArchiveSnapshot("plakar") != "s2" || res.Restored != 1 {
		t.Fatalf("the backup only in the archive: %+v (restored %d)", m, res.Restored)
	}
	if noRef.ArchiveSnapshot("plakar") != "s2" || res.Linked != 1 {
		t.Fatalf("the backup with no snapshot of the archive: %q (linked %d)", noRef.ArchiveSnapshot("plakar"), res.Linked)
	}
	if current.ArchiveSnapshot("plakar") != "s3" || current.Replaced {
		t.Fatal("the backup in the directory changed")
	}
	if len(res.Missing) != 1 || res.Missing[0] != "s0-purged" || c.BackupMetaMap.Get(50) == nil {
		t.Fatalf("the recorded snapshot gone from the archive: %v, its backup must stay (never fixed silently)", res.Missing)
	}

	// the outcome is a state: WARN0260 names the archive and the missing snapshot
	c.catalogueRun.results = map[string]*CatalogueRebuild{"plakar": res}
	c.checkCatalogueStates()
	if !hasOpenState(c, "WARN0260") {
		t.Fatal("WARN0260 not raised")
	}
}

// #2091: an archive whose plugin answers no contents: its snapshots unknown to the catalogue
// are counted not readable, nothing joins
func TestRebuildBackupCatalogueUnreadable(t *testing.T) {
	c, _ := newTestRuntimeOnlyClusterServer(t, "catalogue-unreadable", "db1", "3306")
	c.WorkingDir = t.TempDir()
	c.BackupMetaMap = backupmgr.NewBackupMetaMap()
	ca, _ := fakeCatalogueArchive("pbs", false, nil, []string{"s1", "s2"})
	res := &CatalogueRebuild{Archive: "pbs"}
	c.rebuildCatalogueWith(ca, res)
	if res.Unreadable != 2 || res.Read != 0 || res.Restored != 0 {
		t.Fatalf("outcome %+v", res)
	}
	if !isBackupRecordName("mydumper.meta.json") || !isBackupRecordName("mysqldump.1792589725.meta.json") || isBackupRecordName("binary-logs.meta.json") || isBackupRecordName("binlog.000001") {
		t.Fatal("record names")
	}
}

// #2091: dropping the missing snapshots, asked: the archive is listed again, a snapshot still
// gone leaves the backups recording it, one listed again is kept; WARN0260 then resolves
func TestDropMissingCatalogueSnapshots(t *testing.T) {
	c, server := newTestRuntimeOnlyClusterServer(t, "catalogue-drop", "db1", "3306")
	c.WorkingDir = t.TempDir()
	c.BackupMetaMap = backupmgr.NewBackupMetaMap()
	local := &backupmgr.BackupMetadata{Id: 10, Source: server.URL, BackupTool: "mydumper", Completed: true}
	local.SetArchiveSnapshot("plakar", "gone")
	back := &backupmgr.BackupMetadata{Id: 11, Source: server.URL, BackupTool: "mysqldump", Completed: true}
	back.SetArchiveSnapshot("plakar", "back")
	c.BackupMetaMap.Set(local.Id, local)
	c.BackupMetaMap.Set(back.Id, back)
	c.catalogueRun.results = map[string]*CatalogueRebuild{"plakar": {Archive: "plakar", Missing: []string{"back", "gone"}}}

	ca, _ := fakeCatalogueArchive("plakar", true, nil, []string{"back", "other"})
	n, err := c.dropMissingWith(ca, []string{"back", "gone"})
	if err != nil || n != 1 {
		t.Fatalf("dropped %d (%v), want 1", n, err)
	}
	if local.ArchiveSnapshot("plakar") != "" || c.BackupMetaMap.Get(10) == nil {
		t.Fatal("the local backup keeps the gone snapshot, or left the catalogue")
	}
	if back.ArchiveSnapshot("plakar") != "back" {
		t.Fatal("a snapshot listed again was dropped")
	}
	if len(c.catalogueRun.results["plakar"].Missing) != 0 {
		t.Fatal("the missing snapshots stay reported")
	}
}
