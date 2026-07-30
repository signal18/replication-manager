// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadValidatedSelectors_MissingFileDegradesToEmpty(t *testing.T) {
	dir := t.TempDir()
	store, err := loadValidatedSelectors(filepath.Join(dir, "valid_restore_selectors.json"))
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if store == nil || len(store.Entries) != 0 {
		t.Fatalf("expected empty store, got %+v", store)
	}
}

func TestLoadValidatedSelectors_EmptyFileDegradesToEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "valid_restore_selectors.json")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	store, err := loadValidatedSelectors(path)
	if err != nil {
		t.Fatalf("expected no error for empty file, got %v", err)
	}
	if store == nil || len(store.Entries) != 0 {
		t.Fatalf("expected empty store, got %+v", store)
	}
}

func TestLoadValidatedSelectors_InvalidJSONErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "valid_restore_selectors.json")
	if err := os.WriteFile(path, []byte("{not json"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadValidatedSelectors(path); err == nil {
		t.Fatalf("expected an error for invalid JSON")
	}
}

func TestWriteAndLoadValidatedSelectors_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "valid_restore_selectors.json")

	store := &ValidatedSelectorStore{Entries: []ValidatedSelectorEntry{
		{Method: "logical", Selector: RestoreSelector{Type: []string{"logical"}, Tool: []string{"mysqldump"}}, Result: "success"},
	}}
	if err := writeValidatedSelectorsAtomic(path, store); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := loadValidatedSelectors(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Entries) != 1 || got.Entries[0].Method != "logical" {
		t.Fatalf("unexpected round-trip result: %+v", got)
	}
}

func newHistoryTestCluster(t *testing.T) *Cluster {
	cl := newCatalogTestCluster(t)
	cl.WorkingDir = t.TempDir()
	return cl
}

func TestRecordValidatedSelectorOnSuccess_WritesEntry(t *testing.T) {
	cl := newHistoryTestCluster(t)
	sel := RestoreSelector{Type: []string{"logical"}, Tool: []string{"mysqldump"}}
	pick := &BackupCatalogEntry{Location: RepoLocal, Tool: "mysqldump", Kind: "logical"}

	cl.recordValidatedSelectorOnSuccess("logical", sel, pick)

	entries, err := cl.ListValidatedSelectors()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d: %+v", len(entries), entries)
	}
	if entries[0].Method != "logical" || entries[0].Result != "success" {
		t.Fatalf("unexpected entry: %+v", entries[0])
	}
	if entries[0].Source.Repo != "local" || entries[0].Source.Tool != "mysqldump" {
		t.Fatalf("unexpected source: %+v", entries[0].Source)
	}
}

func TestRecordValidatedSelectorOnSuccess_DedupesUpdatesInPlace(t *testing.T) {
	cl := newHistoryTestCluster(t)
	sel := RestoreSelector{Type: []string{"logical"}, Tool: []string{"mysqldump"}}
	pick := &BackupCatalogEntry{Location: RepoLocal, Tool: "mysqldump", Kind: "logical"}

	cl.recordValidatedSelectorOnSuccess("logical", sel, pick)
	cl.recordValidatedSelectorOnSuccess("logical", sel, pick)

	entries, err := cl.ListValidatedSelectors()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected recording the same selector twice to dedupe to 1 entry, got %d", len(entries))
	}
}

func TestRecordValidatedSelectorOnSuccess_DifferentSelectorAppends(t *testing.T) {
	cl := newHistoryTestCluster(t)
	pick := &BackupCatalogEntry{Location: RepoLocal, Tool: "mysqldump", Kind: "logical"}

	cl.recordValidatedSelectorOnSuccess("logical", RestoreSelector{Tool: []string{"mysqldump"}}, pick)
	cl.recordValidatedSelectorOnSuccess("logical", RestoreSelector{Tool: []string{"mydumper"}}, pick)

	entries, err := cl.ListValidatedSelectors()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 distinct entries, got %d: %+v", len(entries), entries)
	}
}

func TestNewestValidatedSelector_NoneWhenStoreEmpty(t *testing.T) {
	cl := newHistoryTestCluster(t)
	if got := cl.newestValidatedSelector("logical"); got != nil {
		t.Fatalf("expected nil for an empty store, got %+v", got)
	}
}

func TestNewestValidatedSelector_ReturnsNewestForMethod(t *testing.T) {
	cl := newHistoryTestCluster(t)
	older := RestoreSelector{Tool: []string{"mysqldump"}}
	newer := RestoreSelector{Tool: []string{"mydumper"}}
	cl.recordValidatedSelectorOnSuccess("logical", older, nil)
	cl.recordValidatedSelectorOnSuccess("logical", newer, nil)
	// A physical entry must never be returned for a logical lookup.
	cl.recordValidatedSelectorOnSuccess("physical", RestoreSelector{Tool: []string{"xtrabackup"}}, nil)

	got := cl.newestValidatedSelector("logical")
	if got == nil {
		t.Fatalf("expected a validated logical selector")
	}
	if len(got.Tool) != 1 || got.Tool[0] != "mydumper" {
		t.Fatalf("expected the newest (mydumper) entry, got %+v", got)
	}
}

func TestGetAutorejoinBackupSelector_PrefersValidatedOverPresetWhenNoConfigOverride(t *testing.T) {
	cl := newHistoryTestCluster(t)
	validated := RestoreSelector{Type: []string{"logical"}, Tool: []string{"mydumper"}, Order: []string{"last"}}
	cl.recordValidatedSelectorOnSuccess("logical", validated, nil)

	got := cl.getAutorejoinBackupSelector("logical")
	if len(got.Tool) != 1 || got.Tool[0] != "mydumper" {
		t.Fatalf("expected the validated selector to be preferred over the coarse preset, got %+v", got)
	}
}

func TestGetAutorejoinBackupSelector_ExplicitConfigOverrideStillWinsOverValidated(t *testing.T) {
	cl := newHistoryTestCluster(t)
	validated := RestoreSelector{Type: []string{"logical"}, Tool: []string{"mydumper"}}
	cl.recordValidatedSelectorOnSuccess("logical", validated, nil)
	cl.Conf.AutorejoinBackupSelectorLogical = `{"tool":["mysqldump"]}`

	got := cl.getAutorejoinBackupSelector("logical")
	if len(got.Tool) != 1 || got.Tool[0] != "mysqldump" {
		t.Fatalf("expected the explicit config override to win, got %+v", got)
	}
}

func TestGetAutorejoinBackupSelector_FallsBackToPresetWhenStoreEmpty(t *testing.T) {
	cl := newHistoryTestCluster(t)
	got := cl.getAutorejoinBackupSelector("logical")
	want := PresetRejoinLogical()
	if len(got.Type) != 1 || got.Type[0] != want.Type[0] || got.Time != want.Time {
		t.Fatalf("expected the coarse preset default when nothing is validated, got %+v", got)
	}
}
