// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// #2091: a backup replaced by a newer one of its type, kept by a snapshot, is only in the
// archive: the global catalogue keeps it across a restart; the backups in the directory are
// read from their records, never from the catalogue
func TestBackupCatalogueKeepsReplacedBackupAcrossRestart(t *testing.T) {
	c, server := newTestRuntimeOnlyClusterServer(t, "catalogue", "db1", "3306")
	c.WorkingDir = t.TempDir()
	c.BackupMetaMap = backupmgr.NewBackupMetaMap()
	c.Status = ConstMonitorActif
	c.Conf.Archives = []config.ArchiveConfig{{Name: "plakar-s3"}, {Name: "empty"}}
	dest := filepath.Join(server.GetMyBackupDirectory(), "mysqldump.sql.gz")
	old := &backupmgr.BackupMetadata{Id: 100, Source: server.URL, BackupTool: "mysqldump", Dest: dest, Completed: true}
	old.SetArchiveSnapshot("plakar-s3", "snap-old")
	current := &backupmgr.BackupMetadata{Id: 200, Source: server.URL, BackupTool: "mysqldump", Dest: dest, Completed: true}
	current.SetArchiveSnapshot("plakar-s3", "snap-new")
	c.BackupMetaMap.Set(old.Id, old)
	c.BackupMetaMap.Set(current.Id, current)
	server.retireReplacedBackup(old.Id)
	if !old.Replaced || !old.OriginalRemoved {
		t.Fatalf("replaced %t only in the archive %t", old.Replaced, old.OriginalRemoved)
	}

	c.saveBackupCatalogue()
	var saved []*backupmgr.BackupMetadata
	data, err := os.ReadFile(c.backupCataloguePath(backupCatalogueBackupsFile))
	if err != nil || json.Unmarshal(data, &saved) != nil || len(saved) != 2 || saved[0].Id != 100 || saved[1].Id != 200 {
		t.Fatalf("backups.json %s (%v)", data, err)
	}
	var snaps []*CatalogueSnapshot
	data, err = os.ReadFile(c.backupCataloguePath(backupCatalogueArchivesDir, "plakar-s3.json"))
	if err != nil || json.Unmarshal(data, &snaps) != nil || len(snaps) != 2 || snaps[0].ID != "snap-new" || snaps[1].Backups[0] != 100 {
		t.Fatalf("archives/plakar-s3.json %s (%v)", data, err)
	}
	if _, err := os.Stat(c.backupCataloguePath(backupCatalogueArchivesDir, "empty.json")); err != nil {
		t.Fatalf("an archive with no snapshot has no list: %v", err)
	}

	// a restart: the catalogue is read before the records, the replaced backup comes back
	c.BackupMetaMap = backupmgr.NewBackupMetaMap()
	c.loadBackupCatalogue()
	if got := c.BackupMetaMap.Get(100); got == nil || !got.Replaced || got.ArchiveSnapshot("plakar-s3") != "snap-old" {
		t.Fatalf("the replaced backup is forgotten: %+v", got)
	}
	if c.BackupMetaMap.Get(200) != nil {
		t.Fatal("a backup in the directory is read from the catalogue, not from its record")
	}

	// an archive removed: its list leaves the catalogue
	c.Conf.Archives = []config.ArchiveConfig{{Name: "plakar-s3"}}
	c.saveBackupCatalogue()
	if _, err := os.Stat(c.backupCataloguePath(backupCatalogueArchivesDir, "empty.json")); !os.IsNotExist(err) {
		t.Fatalf("the removed archive's list stays: %v", err)
	}
}

// #2091: only the active replication-manager writes the catalogue
func TestBackupCatalogueNotWrittenByStandby(t *testing.T) {
	c, server := newTestRuntimeOnlyClusterServer(t, "catalogue-standby", "db1", "3306")
	c.WorkingDir = t.TempDir()
	c.BackupMetaMap = backupmgr.NewBackupMetaMap()
	c.Status = ConstMonitorStandby
	c.BackupMetaMap.Set(1, &backupmgr.BackupMetadata{Id: 1, Source: server.URL, BackupTool: "mysqldump"})
	c.saveBackupCatalogue()
	if _, err := os.Stat(c.backupCataloguePath(backupCatalogueBackupsFile)); !os.IsNotExist(err) {
		t.Fatalf("a standby wrote the catalogue: %v", err)
	}
}

// #2091: a replaced backup's file and record are the newer one's: deleting it, or its last
// snapshot leaving, never removes them nor gives it the newer backup's file as its own
func TestReplacedBackupNeverTouchesTheNewerOne(t *testing.T) {
	c, server := newTestRuntimeOnlyClusterServer(t, "catalogue-replaced", "db1", "3306")
	c.WorkingDir = t.TempDir()
	c.BackupMetaMap = backupmgr.NewBackupMetaMap()
	dest := filepath.Join(server.GetMyBackupDirectory(), "mysqldump.sql.gz")
	record := server.buildBackupMetaFileName("mysqldump", 0, false)
	for _, f := range []string{dest, record} {
		if err := os.WriteFile(f, []byte("newer"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	old := &backupmgr.BackupMetadata{Id: 100, Source: server.URL, BackupTool: "mysqldump", Dest: dest, Completed: true, MetaFile: record}
	old.SetArchiveSnapshot("plakar-s3", "snap-old")
	c.BackupMetaMap.Set(old.Id, old)
	server.retireReplacedBackup(old.Id)

	c.dropSnapshotFromCatalogue("plakar-s3", "snap-old")
	if c.BackupMetaMap.Get(100) != nil {
		t.Fatal("a replaced backup whose last snapshot left stays in the catalogue")
	}
	if old.OriginalRemoved == false {
		t.Fatal("the replaced backup took the newer backup's file as its own")
	}
	if !fileExists(dest) || !fileExists(record) {
		t.Fatal("the newer backup's file or record was removed")
	}

	c.BackupMetaMap.Set(old.Id, old)
	if _, err := c.DeleteBackupByID(old.Id); err != nil {
		t.Fatal(err)
	}
	if !fileExists(dest) || !fileExists(record) || c.BackupMetaMap.Get(100) != nil {
		t.Fatal("deleting the replaced backup removed the newer one, or kept it in the catalogue")
	}
}
