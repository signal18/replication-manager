package splitdump

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadMetadataAllowsMissingGTID(t *testing.T) {
	dir := t.TempDir()
	content := "[source]\nFile = mysql-bin.000123\nPosition = 456\n"
	if err := os.WriteFile(filepath.Join(dir, "metadata"), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write metadata: %v", err)
	}

	meta, err := ReadMetadata(dir)
	if err != nil {
		t.Fatalf("expected metadata to parse, got error: %v", err)
	}
	if meta.File != "mysql-bin.000123" {
		t.Fatalf("unexpected file: %s", meta.File)
	}
	if meta.Position != 456 {
		t.Fatalf("unexpected position: %d", meta.Position)
	}
	if meta.GTID != "" {
		t.Fatalf("expected empty GTID, got: %s", meta.GTID)
	}
}

func TestReadMetadataRequiresFileAndPosition(t *testing.T) {
	dir := t.TempDir()
	content := "[source]\nPosition = 456\n"
	if err := os.WriteFile(filepath.Join(dir, "metadata"), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write metadata: %v", err)
	}
	_, err := ReadMetadata(dir)
	if err == nil {
		t.Fatalf("expected error for missing file")
	}
	if !errors.Is(err, ErrMetadataInvalid) {
		t.Fatalf("expected invalid metadata error, got: %v", err)
	}
}

func TestReadMetadataRequiresPosition(t *testing.T) {
	dir := t.TempDir()
	content := "[source]\nFile = mysql-bin.000123\n"
	if err := os.WriteFile(filepath.Join(dir, "metadata"), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write metadata: %v", err)
	}
	_, err := ReadMetadata(dir)
	if err == nil {
		t.Fatalf("expected error for missing position")
	}
	if !errors.Is(err, ErrMetadataInvalid) {
		t.Fatalf("expected invalid metadata error, got: %v", err)
	}
}

func TestReadMetadataSourceDataZeroSkipsBinlog(t *testing.T) {
	dir := t.TempDir()
	content := "[source]\nSource_Data = 0\n"
	if err := os.WriteFile(filepath.Join(dir, "metadata"), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write metadata: %v", err)
	}

	meta, err := ReadMetadata(dir)
	if err != nil {
		t.Fatalf("expected metadata to parse, got error: %v", err)
	}
	if meta.SourceData != 0 {
		t.Fatalf("unexpected source data: %d", meta.SourceData)
	}
	if meta.File != "" {
		t.Fatalf("expected empty file, got: %s", meta.File)
	}
	if meta.Position != 0 {
		t.Fatalf("expected zero position, got: %d", meta.Position)
	}
}

func TestRestoreIgnoresMalformedMetadata(t *testing.T) {
	dir := t.TempDir()
	content := "[source]\nFile = mysql-bin.000123\n"
	if err := os.WriteFile(filepath.Join(dir, "metadata"), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write metadata: %v", err)
	}
	dataPath := filepath.Join(dir, "db.tbl.sql")
	if err := os.WriteFile(dataPath, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write data file: %v", err)
	}

	var mu sync.Mutex
	var restored []string
	var logs []string
	logger := func(level, format string, args ...any) {
		mu.Lock()
		logs = append(logs, fmt.Sprintf("%s:%s", level, fmt.Sprintf(format, args...)))
		mu.Unlock()
	}
	restoreFile := func(path string) error {
		mu.Lock()
		restored = append(restored, filepath.Base(path))
		mu.Unlock()
		return nil
	}

	if err := Restore(dir, RestoreOptions{Parallel: 1, Logger: logger, RestoreFile: restoreFile}); err != nil {
		t.Fatalf("expected restore to continue, got error: %v", err)
	}

	mu.Lock()
	gotRestored := append([]string(nil), restored...)
	gotLogs := append([]string(nil), logs...)
	mu.Unlock()

	if len(gotRestored) != 1 || gotRestored[0] != filepath.Base(dataPath) {
		t.Fatalf("unexpected restored files: %#v", gotRestored)
	}

	foundWarn := false
	for _, entry := range gotLogs {
		if strings.HasPrefix(entry, LogWarn+":") && strings.Contains(entry, "metadata malformed") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Fatalf("expected warning about malformed metadata, got logs: %#v", gotLogs)
	}
}

func TestRestoreStrictMetadataMissing(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "db.tbl.sql")
	if err := os.WriteFile(dataPath, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write data file: %v", err)
	}

	var mu sync.Mutex
	var restored []string
	restoreFile := func(path string) error {
		mu.Lock()
		restored = append(restored, filepath.Base(path))
		mu.Unlock()
		return nil
	}

	err := Restore(dir, RestoreOptions{Parallel: 1, StrictMetadata: true, RestoreFile: restoreFile})
	if err == nil {
		t.Fatalf("expected restore error for missing metadata")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected metadata not found error, got: %v", err)
	}

	mu.Lock()
	gotRestored := append([]string(nil), restored...)
	mu.Unlock()
	if len(gotRestored) != 0 {
		t.Fatalf("unexpected restored files: %#v", gotRestored)
	}
}

func TestRestoreStrictMetadataInvalid(t *testing.T) {
	dir := t.TempDir()
	content := "[source]\nFile = mysql-bin.000123\n"
	if err := os.WriteFile(filepath.Join(dir, "metadata"), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write metadata: %v", err)
	}
	dataPath := filepath.Join(dir, "db.tbl.sql")
	if err := os.WriteFile(dataPath, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write data file: %v", err)
	}

	var mu sync.Mutex
	var restored []string
	restoreFile := func(path string) error {
		mu.Lock()
		restored = append(restored, filepath.Base(path))
		mu.Unlock()
		return nil
	}

	err := Restore(dir, RestoreOptions{Parallel: 1, StrictMetadata: true, RestoreFile: restoreFile})
	if err == nil {
		t.Fatalf("expected restore error for invalid metadata")
	}
	if !errors.Is(err, ErrMetadataInvalid) {
		t.Fatalf("expected invalid metadata error, got: %v", err)
	}

	mu.Lock()
	gotRestored := append([]string(nil), restored...)
	mu.Unlock()
	if len(gotRestored) != 0 {
		t.Fatalf("unexpected restored files: %#v", gotRestored)
	}
}

func TestListFilesOrderingAndClassification(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"db.tbl-schema.sql.gz",
		"db.tbl-schema-view.sql.gz",
		"mysql.system-all.sql.gz",
		"db.tbl.sql.gz",
		"db.tbl.00002.sql.gz",
		"db.tbl.00001.sql.gz",
		"db.other.sql.gz",
		"metadata",
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0644); err != nil {
			t.Fatalf("failed to write file %s: %v", name, err)
		}
	}

	set, err := ListFiles(dir, true)
	if err != nil {
		t.Fatalf("ListFiles error: %v", err)
	}

	other := filepath.Join(dir, "db.other.sql.gz")
	base := filepath.Join(dir, "db.tbl.sql.gz")
	shard1 := filepath.Join(dir, "db.tbl.00001.sql.gz")
	shard2 := filepath.Join(dir, "db.tbl.00002.sql.gz")

	wantData := []string{other, base, shard1, shard2}
	if !reflect.DeepEqual(set.Data, wantData) {
		t.Fatalf("unexpected data order: %#v", set.Data)
	}

	wantSchema := []string{
		filepath.Join(dir, "db.tbl-schema-view.sql.gz"),
		filepath.Join(dir, "db.tbl-schema.sql.gz"),
		filepath.Join(dir, "mysql.system-all.sql.gz"),
	}
	if !reflect.DeepEqual(set.Schema, wantSchema) {
		t.Fatalf("unexpected schema list: %#v", set.Schema)
	}

	setNoUser, err := ListFiles(dir, false)
	if err != nil {
		t.Fatalf("ListFiles error: %v", err)
	}
	for _, path := range setNoUser.Schema {
		if filepath.Base(path) == "mysql.system-all.sql.gz" {
			t.Fatalf("expected mysql.system-all.sql.gz to be excluded when restoreUser=false")
		}
	}
}

func TestSchemaFromFilename(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "db.tbl-schema.sql.gz", want: "db"},
		{name: "db.tbl.sql.gz", want: "db"},
		{name: "db.tbl.sql", want: "db"},
		{name: "db.tbl-schema-view.sql.gz", want: "db"},
		{name: "db.tbl.00001.sql.gz", want: "db"},
		{name: filepath.Join("dir", "sub", "db.tbl-schema.sql"), want: "db"},
		{name: "mysql.system-all.sql.gz", want: "mysql"},
		{name: "mysql.system-all", want: "mysql"},
		{name: "not-a-splitdump.txt", want: ""},
		{name: "no-dot.sql.gz", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SchemaFromFilename(tt.name); got != tt.want {
				t.Fatalf("SchemaFromFilename(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestTableFromFilename(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "db.tbl-schema.sql.gz", want: "tbl"},
		{name: "db.tbl.sql.gz", want: "tbl"},
		{name: "db.tbl.00001.sql.gz", want: "tbl"},
		{name: "db.tbl-schema-view.sql", want: "tbl"},
		{name: "mysql.system-all.sql.gz", want: ""},
		{name: "no-dot.sql", want: ""},
		{name: "not-a-splitdump.txt", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TableFromFilename(tt.name); got != tt.want {
				t.Fatalf("TableFromFilename(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestRestorePreamble(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "db.tbl-schema.sql.gz", want: "SET FOREIGN_KEY_CHECKS=0;\nUSE `db`;\n"},
		{name: "mysql.system-all.sql.gz", want: "SET FOREIGN_KEY_CHECKS=0;\nUSE `mysql`;\n"},
		{name: "db.tbl.00001.sql.gz", want: "SET FOREIGN_KEY_CHECKS=0;\nUSE `db`;\n"},
		{name: "db.tbl-schema-view.sql", want: "SET FOREIGN_KEY_CHECKS=0;\nUSE `db`;\n"},
		{name: "no-dot.sql", want: "SET FOREIGN_KEY_CHECKS=0;\n"},
		{name: "not-a-splitdump.txt", want: "SET FOREIGN_KEY_CHECKS=0;\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RestorePreamble(tt.name); got != tt.want {
				t.Fatalf("RestorePreamble(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestIsGtidSlavePosDataFile(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "mysql.gtid_slave_pos.00000.sql.gz", want: true},
		{name: "mysql.gtid_slave_pos.sql.gz", want: true},
		{name: "mysql.gtid_slave_pos-schema.sql.gz", want: false},
		{name: "db.gtid_slave_pos.00000.sql.gz", want: false},
		{name: "not-a-splitdump.txt", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsGtidSlavePosDataFile(tt.name); got != tt.want {
				t.Fatalf("IsGtidSlavePosDataFile(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestIsMissingTableError(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "with 1146 and sqlstate", input: "ERROR 1146 (42S02) at line 6: Table 'mysql.column_stats' doesn't exist", want: true},
		{name: "with 1146 only", input: "error 1146: Table 'mysql.column_stats' doesn't exist", want: true},
		{name: "duplicate entry", input: "ERROR 1062 (23000): Duplicate entry", want: false},
		{name: "empty", input: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsMissingTableError(tt.input); got != tt.want {
				t.Fatalf("IsMissingTableError(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsSchemaFile(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "db.tbl-schema.sql.gz", want: true},
		{name: "db.tbl-schema.sql", want: true},
		{name: "db.tbl-schema-view.sql.gz", want: true},
		{name: "db.tbl-schema-view.sql", want: true},
		{name: "mysql.system-all.sql.gz", want: true},
		{name: "mysql.system-all.sql", want: true},
		{name: "mysql.system-all", want: true},
		{name: "db.tbl.sql.gz", want: false},
		{name: "db.tbl.00001.sql.gz", want: false},
		{name: "not-a-splitdump.txt", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSchemaFile(tt.name); got != tt.want {
				t.Fatalf("IsSchemaFile(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestRestorePreambleEscapesSchema(t *testing.T) {
	name := "db`name.tbl.sql.gz"
	want := "SET FOREIGN_KEY_CHECKS=0;\nUSE `db``name`;\n"
	if got := RestorePreamble(name); got != want {
		t.Fatalf("RestorePreamble(%q) = %q, want %q", name, got, want)
	}
}

// ---------------------------------------------------------------------------
// RestoreFromSource — stream container source
// ---------------------------------------------------------------------------

func TestRestoreFromSource_StreamSource_SchemaBeforeData(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var order []string

	restoreCtx := func(_ interface{}, path string) error {
		mu.Lock()
		order = append(order, filepath.Base(path))
		mu.Unlock()
		return nil
	}

	src := &StreamContainerSource{
		StreamEntries: []StreamEntry{
			{Path: "data/tbl.sql", IsSchema: false, GroupKey: "tbl", ShardIdx: 1},
			{Path: "schema/db.sql", IsSchema: true, GroupKey: "db", ShardIdx: 0},
		},
	}

	err := RestoreFromSource(src, RestoreOptions{
		Parallel: 1,
		RestoreFile: func(path string) error {
			return restoreCtx(nil, path)
		},
	})
	if err != nil {
		t.Fatalf("RestoreFromSource: %v", err)
	}

	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()

	if len(got) != 2 {
		t.Fatalf("expected 2 restored, got %d: %v", len(got), got)
	}
	if got[0] != "db.sql" {
		t.Fatalf("expected schema first, got %q", got[0])
	}
	if got[1] != "tbl.sql" {
		t.Fatalf("expected data second, got %q", got[1])
	}
}

func TestRestoreFromSource_StreamSource_GroupParallelism(t *testing.T) {
	t.Parallel()

	// Two independent data groups should run concurrently
	src := &StreamContainerSource{
		StreamEntries: []StreamEntry{
			{Path: "data/tbl1.sql", IsSchema: false, GroupKey: "tbl1", ShardIdx: 1},
			{Path: "data/tbl2.sql", IsSchema: false, GroupKey: "tbl2", ShardIdx: 1},
		},
	}

	started := make(chan string, 2)
	gate := make(chan struct{})

	done := make(chan error, 1)
	go func() {
		done <- RestoreFromSource(src, RestoreOptions{
			Parallel: 2,
			RestoreFile: func(path string) error {
				started <- filepath.Base(path)
				<-gate
				return nil
			},
		})
	}()

	seen := make(map[string]bool)
	timeout := time.After(5 * time.Second)
	for len(seen) < 2 {
		select {
		case name := <-started:
			seen[name] = true
		case <-timeout:
			close(gate)
			t.Fatalf("timeout: only %d/2 groups started — suggests sequential execution", len(seen))
		}
	}
	close(gate)

	if err := <-done; err != nil {
		t.Fatalf("RestoreFromSource: %v", err)
	}
}

func TestRestoreFromSource_StreamSource_WithinGroupShardOrder(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var order []string

	src := &StreamContainerSource{
		StreamEntries: []StreamEntry{
			{Path: "data/tbl.0003.sql", IsSchema: false, GroupKey: "tbl", ShardIdx: 3},
			{Path: "data/tbl.0001.sql", IsSchema: false, GroupKey: "tbl", ShardIdx: 1},
			{Path: "data/tbl.0002.sql", IsSchema: false, GroupKey: "tbl", ShardIdx: 2},
		},
	}

	err := RestoreFromSource(src, RestoreOptions{
		Parallel: 1,
		RestoreFile: func(path string) error {
			mu.Lock()
			order = append(order, filepath.Base(path))
			mu.Unlock()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RestoreFromSource: %v", err)
	}

	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()

	want := []string{"data/tbl.0001.sql", "data/tbl.0002.sql", "data/tbl.0003.sql"}
	for i, wantPath := range want {
		if got[i] != filepath.Base(wantPath) {
			t.Fatalf("order[%d]: expected %q, got %q", i, filepath.Base(wantPath), got[i])
		}
	}
}

func TestRestoreFromSource_StreamSource_ErrorPropagation(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("restore failed")
	var callCount int64

	src := &StreamContainerSource{
		StreamEntries: []StreamEntry{
			{Path: "schema/db.sql", IsSchema: true, GroupKey: "db", ShardIdx: 0},
			{Path: "data/tbl.sql", IsSchema: false, GroupKey: "tbl", ShardIdx: 1},
		},
	}

	err := RestoreFromSource(src, RestoreOptions{
		Parallel: 1,
		RestoreFile: func(path string) error {
			atomic.AddInt64(&callCount, 1)
			if filepath.Base(path) == "db.sql" {
				return sentinel
			}
			return nil
		},
	})

	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got: %v", err)
	}
	if n := atomic.LoadInt64(&callCount); n != 1 {
		t.Fatalf("expected 1 call (schema only), got %d", n)
	}
}

func TestRestoreFromSource_FilesystemCompatibility(t *testing.T) {
	t.Parallel()

	// RestoreFromSource with FilesystemSource should behave identically to Restore()
	dir := t.TempDir()
	for _, f := range []string{"db.tbl-schema.sql.gz", "db.tbl.sql.gz"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("test"), 0644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	var mu sync.Mutex
	var restoredA, restoredB []string

	restoreFile := func(order *[]string) func(string) error {
		return func(path string) error {
			mu.Lock()
			*order = append(*order, filepath.Base(path))
			mu.Unlock()
			return nil
		}
	}

	if err := Restore(dir, RestoreOptions{Parallel: 1, RestoreFile: restoreFile(&restoredA)}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if err := RestoreFromSource(&FilesystemSource{BackupPath: dir}, RestoreOptions{Parallel: 1, RestoreFile: restoreFile(&restoredB)}); err != nil {
		t.Fatalf("RestoreFromSource: %v", err)
	}

	mu.Lock()
	a := append([]string(nil), restoredA...)
	b := append([]string(nil), restoredB...)
	mu.Unlock()

	if len(a) != len(b) {
		t.Fatalf("result mismatch: Restore=%v RestoreFromSource=%v", a, b)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("result[%d]: Restore=%q RestoreFromSource=%q", i, a[i], b[i])
		}
	}
}
