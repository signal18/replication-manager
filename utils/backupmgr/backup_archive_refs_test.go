package backupmgr

import (
	"reflect"
	"testing"
)

// #2091: a backup's refs by archive name, and a snapshot recorded before the archive list
func TestBackupArchiveRefs(t *testing.T) {
	m := &BackupMetadata{ResticSnapshotID: "legacy"}
	if m.ArchiveSnapshot("any") != "legacy" || !m.HoldsSnapshot("legacy") {
		t.Fatalf("an unglued legacy snapshot reads as any archive's")
	}
	if !m.GlueLegacySnapshot("archive-local-1") || m.ResticSnapshotID != "" || m.ArchiveSnapshot("archive-local-1") != "legacy" || m.ArchiveSnapshot("other") != "" {
		t.Fatalf("glue: %+v", m)
	}
	if m.GlueLegacySnapshot("archive-local-1") {
		t.Fatalf("glued twice")
	}
	before := m.ArchiveRefs
	m.SetArchiveSnapshot("archive-s3-1", "s3snap")
	if len(before) != 1 {
		t.Fatalf("refs changed in place: %v", before)
	}
	if !reflect.DeepEqual(m.ArchiveNames(), []string{"archive-local-1", "archive-s3-1"}) || !m.HoldsSnapshot("s3snap") {
		t.Fatalf("names %v", m.ArchiveNames())
	}
	if !m.RenameArchiveRef("archive-s3-1", "archive-bso-s3") || m.ArchiveSnapshot("archive-bso-s3") != "s3snap" || m.RenameArchiveRef("nope", "x") {
		t.Fatalf("rename: %v", m.ArchiveRefs)
	}
	if !m.DropArchiveRef("archive-bso-s3") || !m.DropArchiveRef("archive-local-1") || m.ArchiveRefs != nil {
		t.Fatalf("drop: %v", m.ArchiveRefs)
	}
	// no archive list: recorded as before it
	n := &BackupMetadata{}
	n.SetArchiveSnapshot("", "x")
	if n.ResticSnapshotID != "x" || n.ArchiveRefs != nil {
		t.Fatalf("no list: %+v", n)
	}
}
