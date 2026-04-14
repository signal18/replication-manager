package splitdump

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// ---------------------------------------------------------------------------
// FilesystemSource tests
// ---------------------------------------------------------------------------

func TestFilesystemSource_Entries_SchemaAndData(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	files := []string{
		"db.tbl-schema.sql.gz",
		"db.tbl.sql.gz",
		"db.tbl.00001.sql.gz",
		"db.tbl.00002.sql.gz",
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	src := &FilesystemSource{BackupPath: dir}
	entries, err := src.Entries(false)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}

	var schema, data []SourceEntry
	for _, e := range entries {
		if e.IsSchema {
			schema = append(schema, e)
		} else {
			data = append(data, e)
		}
	}

	if len(schema) != 1 {
		t.Fatalf("expected 1 schema entry, got %d", len(schema))
	}
	if filepath.Base(schema[0].Path) != "db.tbl-schema.sql.gz" {
		t.Fatalf("unexpected schema entry: %s", schema[0].Path)
	}

	if len(data) != 3 {
		t.Fatalf("expected 3 data entries, got %d", len(data))
	}

	// All data entries should share the same GroupKey
	for _, e := range data {
		if e.GroupKey != "db.tbl" {
			t.Fatalf("expected GroupKey=db.tbl, got %q", e.GroupKey)
		}
	}

	// ShardIdx should be 0, 1, 2 in order
	shards := make([]int, len(data))
	for i, e := range data {
		shards[i] = e.ShardIdx
	}
	sort.Ints(shards)
	for i, want := range []int{0, 1, 2} {
		if shards[i] != want {
			t.Fatalf("shard[%d]: expected %d, got %d", i, want, shards[i])
		}
	}
}

func TestFilesystemSource_Entries_MysqlSystemAllRespectedWhenRestoreUser(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, f := range []string{"mysql.system-all.sql.gz", "db.tbl.sql.gz"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	srcUser := &FilesystemSource{BackupPath: dir}
	entries, err := srcUser.Entries(true)
	if err != nil {
		t.Fatalf("Entries(true): %v", err)
	}
	foundSystemAll := false
	for _, e := range entries {
		if filepath.Base(e.Path) == "mysql.system-all.sql.gz" {
			foundSystemAll = true
			if !e.IsSchema {
				t.Fatalf("mysql.system-all.sql.gz should be classified as schema")
			}
		}
	}
	if !foundSystemAll {
		t.Fatalf("expected mysql.system-all.sql.gz when restoreUser=true")
	}

	srcNoUser := &FilesystemSource{BackupPath: dir}
	entries, err = srcNoUser.Entries(false)
	if err != nil {
		t.Fatalf("Entries(false): %v", err)
	}
	for _, e := range entries {
		if filepath.Base(e.Path) == "mysql.system-all.sql.gz" {
			t.Fatalf("mysql.system-all.sql.gz should be excluded when restoreUser=false")
		}
	}
}

func TestFilesystemSource_Entries_EmptyDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// metadata file should not appear as an entry
	if err := os.WriteFile(filepath.Join(dir, "metadata"), []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	src := &FilesystemSource{BackupPath: dir}
	entries, err := src.Entries(false)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries for dir with only metadata, got %d", len(entries))
	}
}

func TestFilesystemSource_Metadata_Valid(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := "[source]\nFile = mysql-bin.000001\nPosition = 42\n"
	if err := os.WriteFile(filepath.Join(dir, "metadata"), []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	src := &FilesystemSource{BackupPath: dir}
	meta, err := src.Metadata()
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if meta == nil {
		t.Fatalf("expected non-nil metadata")
	}
	if meta.File != "mysql-bin.000001" {
		t.Fatalf("unexpected file: %s", meta.File)
	}
	if meta.Position != 42 {
		t.Fatalf("unexpected position: %d", meta.Position)
	}
}

func TestFilesystemSource_Metadata_Missing(t *testing.T) {
	t.Parallel()

	src := &FilesystemSource{BackupPath: t.TempDir()}
	_, err := src.Metadata()
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// StreamContainerSource tests
// ---------------------------------------------------------------------------

func TestStreamContainerSource_Entries_Classification(t *testing.T) {
	t.Parallel()

	streamEntries := []StreamEntry{
		{Path: "schema/db1.sql", IsSchema: true, GroupKey: "db1", ShardIdx: 0},
		{Path: "data/db1.tbl1.0001.sql", IsSchema: false, GroupKey: "db1.tbl1", ShardIdx: 1},
		{Path: "data/db1.tbl1.0002.sql", IsSchema: false, GroupKey: "db1.tbl1", ShardIdx: 2},
	}

	src := &StreamContainerSource{StreamEntries: streamEntries}
	entries, err := src.Entries(false)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	if !entries[0].IsSchema || entries[0].Path != "schema/db1.sql" {
		t.Fatalf("entry[0] unexpected: %+v", entries[0])
	}
	if entries[1].IsSchema || entries[1].GroupKey != "db1.tbl1" || entries[1].ShardIdx != 1 {
		t.Fatalf("entry[1] unexpected: %+v", entries[1])
	}
	if entries[2].IsSchema || entries[2].GroupKey != "db1.tbl1" || entries[2].ShardIdx != 2 {
		t.Fatalf("entry[2] unexpected: %+v", entries[2])
	}
}

func TestStreamContainerSource_Entries_RestoreUserIgnored(t *testing.T) {
	t.Parallel()

	// Stream container source ignores restoreUser — all declared entries are returned
	src := &StreamContainerSource{
		StreamEntries: []StreamEntry{
			{Path: "data/tbl.sql", IsSchema: false, GroupKey: "tbl", ShardIdx: 1},
		},
	}

	e1, _ := src.Entries(true)
	e2, _ := src.Entries(false)
	if len(e1) != 1 || len(e2) != 1 {
		t.Fatalf("restoreUser should not affect stream source: %d / %d", len(e1), len(e2))
	}
}

func TestStreamContainerSource_Entries_Empty(t *testing.T) {
	t.Parallel()

	src := &StreamContainerSource{}
	entries, err := src.Entries(false)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestStreamContainerSource_Metadata_ReturnsNil(t *testing.T) {
	t.Parallel()

	src := &StreamContainerSource{}
	meta, err := src.Metadata()
	if err != nil {
		t.Fatalf("Metadata: unexpected error: %v", err)
	}
	if meta != nil {
		t.Fatalf("expected nil metadata for stream source, got: %+v", meta)
	}
}

// ---------------------------------------------------------------------------
// groupSourceDataEntries tests
// ---------------------------------------------------------------------------

func TestGroupSourceDataEntries_GroupsByKey(t *testing.T) {
	t.Parallel()

	entries := []SourceEntry{
		{Path: "db.tbl.sql", GroupKey: "db.tbl", ShardIdx: 0},
		{Path: "db.tbl.00001.sql", GroupKey: "db.tbl", ShardIdx: 1},
		{Path: "db.other.sql", GroupKey: "db.other", ShardIdx: 0},
	}

	groups := groupSourceDataEntries(entries)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}

	// After sort by GroupKey: db.other < db.tbl
	if groups[0].key != "db.other" {
		t.Fatalf("expected group[0].key=db.other, got %q", groups[0].key)
	}
	if groups[1].key != "db.tbl" {
		t.Fatalf("expected group[1].key=db.tbl, got %q", groups[1].key)
	}

	if len(groups[1].paths) != 2 {
		t.Fatalf("expected 2 paths in db.tbl group, got %d", len(groups[1].paths))
	}
	// Within group, shard order: 0 before 1
	if groups[1].paths[0] != "db.tbl.sql" {
		t.Fatalf("expected db.tbl.sql first in group, got %q", groups[1].paths[0])
	}
	if groups[1].paths[1] != "db.tbl.00001.sql" {
		t.Fatalf("expected db.tbl.00001.sql second in group, got %q", groups[1].paths[1])
	}
}

func TestGroupSourceDataEntries_SortsWithinGroup(t *testing.T) {
	t.Parallel()

	// Entries provided out of shard order — should be sorted
	entries := []SourceEntry{
		{Path: "tbl.00003.sql", GroupKey: "tbl", ShardIdx: 3},
		{Path: "tbl.00001.sql", GroupKey: "tbl", ShardIdx: 1},
		{Path: "tbl.00002.sql", GroupKey: "tbl", ShardIdx: 2},
	}

	groups := groupSourceDataEntries(entries)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	want := []string{"tbl.00001.sql", "tbl.00002.sql", "tbl.00003.sql"}
	for i, wantPath := range want {
		if groups[0].paths[i] != wantPath {
			t.Fatalf("paths[%d]: expected %q, got %q", i, wantPath, groups[0].paths[i])
		}
	}
}

func TestGroupSourceDataEntries_Empty(t *testing.T) {
	t.Parallel()

	groups := groupSourceDataEntries(nil)
	if len(groups) != 0 {
		t.Fatalf("expected 0 groups for nil input, got %d", len(groups))
	}
}
