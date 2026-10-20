// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// a cluster with archive-local-1 (attached) and archive-s3-1, and no server: the catalogue
// changes in memory only
func newCatalogueTestCluster(t *testing.T) *Cluster {
	t.Helper()
	cl := newEventLogTestCluster(t, 1)
	cl.WorkingDir = t.TempDir()
	cl.Conf.Archives = []config.ArchiveConfig{
		{Name: "archive-local-1", Plugin: config.ConstArchivePluginResticLocal},
		{Name: "archive-s3-1", Plugin: config.ConstArchivePluginResticS3},
	}
	cl.Conf.BackupArchiveName = "archive-local-1"
	cl.BackupMetaMap = backupmgr.NewBackupMetaMap()
	return cl
}

// #2091: a snapshot recorded before the archive list is glued to the cluster's archive
func TestCatalogueGlueLegacySnapshot(t *testing.T) {
	cl := newCatalogueTestCluster(t)
	cl.BackupMetaMap.Set(1, &backupmgr.BackupMetadata{Id: 1, ResticSnapshotID: "aaa"})
	cl.glueCatalogue()
	m := cl.BackupMetaMap.Get(1)
	if m.ResticSnapshotID != "" || !reflect.DeepEqual(m.ArchiveRefs, map[string]string{"archive-local-1": "aaa"}) {
		t.Fatalf("not glued: legacy %q refs %v", m.ResticSnapshotID, m.ArchiveRefs)
	}
	if cl.attachedSnapshot(m) != "aaa" {
		t.Fatalf("attached snapshot %q", cl.attachedSnapshot(m))
	}
	// attached elsewhere: the backup is not in the attached archive
	cl.Conf.BackupArchiveName = "archive-s3-1"
	if got := cl.attachedSnapshot(m); got != "" {
		t.Fatalf("attached snapshot in archive-s3-1: %q", got)
	}
	if e := backupMetaToCatalog("s1", &backupmgr.BackupMetadata{ResticEnabled: true, ArchiveRefs: m.ArchiveRefs, Dest: "/d"}, "archive-s3-1", ""); e.Location != RepoLocal || e.Path != "/d" || e.Snapshot != "" {
		t.Fatalf("held by a detached archive only: %+v", e)
	}
	// archived, its file still on disk: read where it is, the snapshot kept beside it
	if e := backupMetaToCatalog("s1", &backupmgr.BackupMetadata{ResticEnabled: true, ArchiveRefs: m.ArchiveRefs, Dest: "/d"}, "archive-local-1", ""); e.Location != RepoLocal || e.Path != "/d" || e.Snapshot != "aaa" {
		t.Fatalf("held by the attached archive: %+v", e)
	}
}

// #2091: a rename renames the archive in the catalogue
func TestCatalogueRenameArchive(t *testing.T) {
	cl := newCatalogueTestCluster(t)
	cl.BackupMetaMap.Set(1, &backupmgr.BackupMetadata{Id: 1, ArchiveRefs: map[string]string{"archive-local-1": "aaa", "archive-s3-1": "bbb"}})
	cl.renameArchiveInCatalogue("archive-local-1", "archive-bso-local")
	if got := cl.BackupMetaMap.Get(1).ArchiveRefs; !reflect.DeepEqual(got, map[string]string{"archive-bso-local": "aaa", "archive-s3-1": "bbb"}) {
		t.Fatalf("refs %v", got)
	}
}

// #2091: a removed archive leaves the catalogue; a backup whose only copy was there leaves
// with it, one still on disk stays a local backup
func TestCatalogueDropArchive(t *testing.T) {
	cl := newCatalogueTestCluster(t)
	onDisk := filepath.Join(t.TempDir(), "dump.sql.gz")
	if err := os.WriteFile(onDisk, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	cl.BackupMetaMap.Set(1, &backupmgr.BackupMetadata{Id: 1, OriginalRemoved: true, Dest: "/gone/dump.sql.gz", ArchiveRefs: map[string]string{"archive-s3-1": "a"}})
	cl.BackupMetaMap.Set(2, &backupmgr.BackupMetadata{Id: 2, OriginalRemoved: true, Dest: onDisk, ArchiveRefs: map[string]string{"archive-s3-1": "b"}})
	cl.BackupMetaMap.Set(3, &backupmgr.BackupMetadata{Id: 3, OriginalRemoved: true, Dest: "/gone/x", ArchiveRefs: map[string]string{"archive-s3-1": "c", "archive-local-1": "c2"}})
	cl.BackupMetaMap.Set(4, &backupmgr.BackupMetadata{Id: 4, Dest: "/local/y", ArchiveRefs: map[string]string{"archive-s3-1": "d"}})
	cl.BackupMetaMap.Set(5, &backupmgr.BackupMetadata{Id: 5, OriginalRemoved: true, ArchiveRefs: map[string]string{"archive-local-1": "e"}})
	cl.dropArchiveFromCatalogue("archive-s3-1")

	if cl.BackupMetaMap.Get(1) != nil {
		t.Errorf("backup 1 had its only copy in the archive: still listed")
	}
	if m := cl.BackupMetaMap.Get(2); m == nil || m.OriginalRemoved || len(m.ArchiveRefs) != 0 {
		t.Errorf("backup 2 is on disk: %+v", m)
	}
	if m := cl.BackupMetaMap.Get(3); m == nil || !reflect.DeepEqual(m.ArchiveRefs, map[string]string{"archive-local-1": "c2"}) {
		t.Errorf("backup 3 is held by another archive: %+v", m)
	}
	if m := cl.BackupMetaMap.Get(4); m == nil || len(m.ArchiveRefs) != 0 {
		t.Errorf("backup 4 is local: %+v", m)
	}
	if m := cl.BackupMetaMap.Get(5); m == nil || m.ArchiveRefs["archive-local-1"] != "e" {
		t.Errorf("backup 5 is not in the removed archive: %+v", m)
	}
}

// #2091: a deleted snapshot leaves only the backups recording it; one whose only copy it was
// leaves the catalogue
func TestCatalogueDropSnapshot(t *testing.T) {
	cl := newCatalogueTestCluster(t)
	cl.BackupMetaMap.Set(1, &backupmgr.BackupMetadata{Id: 1, Dest: "/local/a", ArchiveRefs: map[string]string{"archive-plakar-s3-1": "s1"}})
	cl.BackupMetaMap.Set(2, &backupmgr.BackupMetadata{Id: 2, Dest: "/local/b", ArchiveRefs: map[string]string{"archive-plakar-s3-1": "s2"}})
	cl.BackupMetaMap.Set(3, &backupmgr.BackupMetadata{Id: 3, OriginalRemoved: true, Dest: "/gone/c", ArchiveRefs: map[string]string{"archive-plakar-s3-1": "s3"}})
	cl.BackupMetaMap.Set(4, &backupmgr.BackupMetadata{Id: 4, Dest: "/local/d", ArchiveRefs: map[string]string{"archive-local-1": "s1"}})
	cl.dropSnapshotFromCatalogue("archive-plakar-s3-1", "s1")
	cl.dropSnapshotFromCatalogue("archive-plakar-s3-1", "s3")

	if m := cl.BackupMetaMap.Get(1); m == nil || len(m.ArchiveRefs) != 0 {
		t.Errorf("backup 1 recorded the deleted snapshot: %+v", m)
	}
	if m := cl.BackupMetaMap.Get(2); m == nil || m.ArchiveRefs["archive-plakar-s3-1"] != "s2" {
		t.Errorf("backup 2 records another snapshot: %+v", m)
	}
	if cl.BackupMetaMap.Get(3) != nil {
		t.Errorf("backup 3 had its only copy in the deleted snapshot: still listed")
	}
	if m := cl.BackupMetaMap.Get(4); m == nil || m.ArchiveRefs["archive-local-1"] != "s1" {
		t.Errorf("backup 4 is in another archive: %+v", m)
	}
}

// #2091: the list counts each archive's backups and shows a detached archive's size as kept
func TestArchiveListSizesAndBackups(t *testing.T) {
	cl := newCatalogueTestCluster(t)
	cl.BackupMetaMap.Set(1, &backupmgr.BackupMetadata{Id: 1, ArchiveRefs: map[string]string{"archive-local-1": "a", "archive-s3-1": "b"}})
	cl.BackupMetaMap.Set(2, &backupmgr.BackupMetadata{Id: 2, ResticSnapshotID: "c"}) // not glued yet
	cl.setArchiveSize("archive-s3-1", &ArchiveSize{Bytes: 42})

	got := map[string]ArchiveListEntry{}
	for _, e := range cl.ArchiveList().Archives {
		got[e.Name] = e
	}
	if got["archive-local-1"].Backups != 2 || got["archive-s3-1"].Backups != 1 {
		t.Fatalf("backups: local %d s3 %d", got["archive-local-1"].Backups, got["archive-s3-1"].Backups)
	}
	if s := got["archive-s3-1"].Size; s == nil || s.Bytes != 42 {
		t.Fatalf("detached size %+v", s)
	}
	if got["archive-local-1"].Size != nil {
		t.Fatalf("attached archive never fetched: size %+v", got["archive-local-1"].Size)
	}

	// kept across a restart, renamed with the archive
	// (RenameArchive renames the config's entry first)
	cl.Conf.Archives[1].Name = "archive-bso-s3"
	cl.renameArchiveSize("archive-s3-1", "archive-bso-s3")
	cl.archiveSizes = nil
	for _, e := range cl.ArchiveList().Archives {
		if e.Name == "archive-bso-s3" && (e.Size == nil || e.Size.Bytes != 42) {
			t.Fatalf("size after rename and reload %+v", e.Size)
		}
	}
}

// #2091: a listed archive carries its state with its config, its secrets by name only
func TestArchiveListEntryJSON(t *testing.T) {
	e := ArchiveListEntry{
		ArchiveConfig: config.ArchiveConfig{Name: "archive-s3-1", Plugin: config.ConstArchivePluginResticS3, Settings: map[string]interface{}{"aws-bucket": "bk", "password": "enc"}},
		Attached:      true, Status: ArchiveOpSizing, Size: &ArchiveSize{Bytes: 7}, Backups: 3,
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["name"] != "archive-s3-1" || got["attached"] != true || got["status"] != ArchiveOpSizing || got["backups"] != float64(3) || got["size"].(map[string]interface{})["bytes"] != float64(7) {
		t.Fatalf("entry %s", data)
	}
	if settings := got["settings"].(map[string]interface{}); settings["password"] != nil || settings["aws-bucket"] != "bk" {
		t.Fatalf("settings %v", settings)
	}
}

// #2091: two archives resolving to one repository are refused: a path inside the default
// archive folder runs the default repository
func TestArchiveSharingLocation(t *testing.T) {
	cl := newCatalogueTestCluster(t)
	cl.Conf.WorkingDir = "/var/lib/replication-manager"
	cl.Conf.Archives = []config.ArchiveConfig{
		{Name: "a", Plugin: config.ConstArchivePluginResticLocal, Settings: map[string]interface{}{}},
		{Name: "b", Plugin: config.ConstArchivePluginResticLocal, Settings: map[string]interface{}{"local-repository": "/var/lib/replication-manager/backups/archive/other"}},
		{Name: "c", Plugin: config.ConstArchivePluginResticLocal, Settings: map[string]interface{}{"local-repository": "/data/restic"}},
	}
	if other, loc := cl.archiveSharingLocation(cl.Conf.Archives[1]); other != "a" || loc != "/var/lib/replication-manager/backups/archive/c1" {
		t.Fatalf("b inside the default folder: shares %q at %q", other, loc)
	}
	if other, loc := cl.archiveSharingLocation(cl.Conf.Archives[2]); other != "" || loc != "/data/restic" {
		t.Fatalf("c: shares %q at %q", other, loc)
	}
}
