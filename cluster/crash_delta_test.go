package cluster

import (
	"os"
	"testing"
)

// TestBackfillDeltaFromArchiveDir pins the delta-persistence recovery: when a crash's
// metadata lost the decoded paths but the binlog + .sql are still in its archive dir
// (the observed empty-delta-after-restart bug), backfill re-links them from disk.
func TestBackfillDeltaFromArchiveDir(t *testing.T) {
	dir := t.TempDir()
	binlog := "curepipe-server123-log-bin.000014"
	for name, body := range map[string]string{
		binlog:                    "BINLOG",
		binlog + ".sql":           "DELTA SQL",
		binlog + ".flashback.sql": "UNDO SQL",
	} {
		if err := os.WriteFile(dir+"/"+name, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}

	crash := &Crash{ArchiveDir: dir, FailoverMasterLogFile: "log-bin.000014"}
	crash.backfillDeltaFromArchiveDir()

	if want := dir + "/" + binlog + ".sql"; crash.DeltaDecoded != want {
		t.Fatalf("DeltaDecoded = %q, want %q", crash.DeltaDecoded, want)
	}
	if want := dir + "/" + binlog; crash.DeltaArchive != want {
		t.Fatalf("DeltaArchive = %q, want %q", crash.DeltaArchive, want)
	}
	if want := dir + "/" + binlog + ".flashback.sql"; crash.DeltaFlashbackDecoded != want {
		t.Fatalf("DeltaFlashbackDecoded = %q, want %q", crash.DeltaFlashbackDecoded, want)
	}
}

// TestBackfillDeltaFromArchiveDir_NoClobber verifies backfill never overwrites a delta
// path that is already set (the in-memory record is authoritative when present).
func TestBackfillDeltaFromArchiveDir_NoClobber(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/some-log-bin.000001.sql", []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	crash := &Crash{ArchiveDir: dir, DeltaDecoded: "/already/set.sql"}
	crash.backfillDeltaFromArchiveDir()
	if crash.DeltaDecoded != "/already/set.sql" {
		t.Fatalf("backfill overwrote an existing DeltaDecoded: %q", crash.DeltaDecoded)
	}
}
