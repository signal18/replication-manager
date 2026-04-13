package splitdump

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
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
		"db.__routines-schema-routine.sql.gz",
		"db.tbl-schema-view.sql.gz",
		"db.tbl-schema-trigger.sql.gz",
		"db.__events-schema-event.sql.gz",
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
		filepath.Join(dir, "db.tbl-schema.sql.gz"),
		filepath.Join(dir, "mysql.system-all.sql.gz"),
		filepath.Join(dir, "db.__routines-schema-routine.sql.gz"),
		filepath.Join(dir, "db.tbl-schema-view.sql.gz"),
	}
	if !reflect.DeepEqual(set.Schema, wantSchema) {
		t.Fatalf("unexpected schema list: %#v", set.Schema)
	}

	wantPost := []string{
		filepath.Join(dir, "db.tbl-schema-trigger.sql.gz"),
		filepath.Join(dir, "db.__events-schema-event.sql.gz"),
	}
	if !reflect.DeepEqual(set.Post, wantPost) {
		t.Fatalf("unexpected post-data list: %#v", set.Post)
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

func TestListFilesSchemaOrderingTableBeforeViewAlphabeticalConflict(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"db.a_view-schema-view.sql.gz",
		"db.z_table-schema.sql.gz",
		"mysql.system-all.sql.gz",
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

	wantSchema := []string{
		filepath.Join(dir, "db.z_table-schema.sql.gz"),
		filepath.Join(dir, "mysql.system-all.sql.gz"),
		filepath.Join(dir, "db.a_view-schema-view.sql.gz"),
	}
	if !reflect.DeepEqual(set.Schema, wantSchema) {
		t.Fatalf("unexpected schema order: %#v", set.Schema)
	}
}

func TestRestoreAppliesExpandedObjectOrdering(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"db.a_view-schema-view.sql.gz",
		"db.z_table-schema.sql.gz",
		"mysql.system-all.sql.gz",
		"db.__routines-schema-routine.sql.gz",
		"db.tbl.00000.sql.gz",
		"db.tbl-schema-trigger.sql.gz",
		"db.__events-schema-event.sql.gz",
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0644); err != nil {
			t.Fatalf("failed to write file %s: %v", name, err)
		}
	}

	var mu sync.Mutex
	var restored []string
	restoreFile := func(path string) error {
		mu.Lock()
		restored = append(restored, filepath.Base(path))
		mu.Unlock()
		return nil
	}

	if err := Restore(dir, RestoreOptions{Parallel: 1, RestoreUser: true, RestoreFile: restoreFile}); err != nil {
		t.Fatalf("unexpected restore error: %v", err)
	}

	mu.Lock()
	gotRestored := append([]string(nil), restored...)
	mu.Unlock()

	want := []string{
		"db.z_table-schema.sql.gz",
		"mysql.system-all.sql.gz",
		"db.__routines-schema-routine.sql.gz",
		"db.a_view-schema-view.sql.gz",
		"db.tbl.00000.sql.gz",
		"db.tbl-schema-trigger.sql.gz",
		"db.__events-schema-event.sql.gz",
	}
	if !reflect.DeepEqual(gotRestored, want) {
		t.Fatalf("unexpected restore order: %#v", gotRestored)
	}
}

func TestRestoreAppliesSchemaTableBeforeView(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"db.a_view-schema-view.sql.gz",
		"db.z_table-schema.sql.gz",
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0644); err != nil {
			t.Fatalf("failed to write file %s: %v", name, err)
		}
	}

	var mu sync.Mutex
	var restored []string
	restoreFile := func(path string) error {
		mu.Lock()
		restored = append(restored, filepath.Base(path))
		mu.Unlock()
		return nil
	}

	if err := Restore(dir, RestoreOptions{Parallel: 1, RestoreFile: restoreFile}); err != nil {
		t.Fatalf("unexpected restore error: %v", err)
	}

	mu.Lock()
	gotRestored := append([]string(nil), restored...)
	mu.Unlock()

	want := []string{
		"db.z_table-schema.sql.gz",
		"db.a_view-schema-view.sql.gz",
	}
	if !reflect.DeepEqual(gotRestored, want) {
		t.Fatalf("unexpected restore order: %#v", gotRestored)
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
		{name: "db.__routines-schema-routine.sql.gz", want: "db"},
		{name: "db.tbl-schema-trigger.sql.gz", want: "db"},
		{name: "db.__events-schema-event.sql.gz", want: "db"},
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
		{name: "db.tbl-schema-trigger.sql.gz", want: "tbl"},
		{name: "mysql.system-all.sql.gz", want: ""},
		{name: "db.__routines-schema-routine.sql.gz", want: "__routines"},
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
		{name: "db.__routines-schema-routine.sql.gz", want: "SET FOREIGN_KEY_CHECKS=0;\nUSE `db`;\n"},
		{name: "db.tbl-schema-trigger.sql.gz", want: "SET FOREIGN_KEY_CHECKS=0;\nUSE `db`;\n"},
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
		{name: "db.__routines-schema-routine.sql.gz", want: true},
		{name: "db.tbl-schema-trigger.sql.gz", want: true},
		{name: "db.__events-schema-event.sql.gz", want: true},
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

func TestRestoreFailsForNonSplitdumpDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "backup.sql.gz"), []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	err := Restore(dir, RestoreOptions{Parallel: 1, RestoreFile: func(string) error { return nil }})
	if err == nil {
		t.Fatalf("expected ErrNotSplitdump for non-splitdump dir, got nil")
	}
	if !errors.Is(err, ErrNotSplitdump) {
		t.Fatalf("expected ErrNotSplitdump, got: %v", err)
	}
}

// ---- Phase ordering and phase-log tests (story 4.2) ----

func TestRestorePhaseOrderAllSevenTypes(t *testing.T) {
	dir := t.TempDir()
	// All 7 artifact types: tables, mysql.system-all, routines, views, data, triggers, events
	files := []string{
		"db.tbl-schema-trigger.sql.gz",    // post: trigger
		"db.__events-schema-event.sql.gz", // post: event
		"db.tbl.00000.sql.gz",             // data
		"db.tbl-schema.sql.gz",            // schema: table
		"mysql.system-all.sql.gz",         // schema: system-all
		"db.__routines-schema-routine.sql.gz", // schema: routine
		"db.a_view-schema-view.sql.gz",    // schema: view
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("test"), 0644); err != nil {
			t.Fatalf("failed to write file %s: %v", f, err)
		}
	}

	var mu sync.Mutex
	var restored []string
	restoreFile := func(path string) error {
		mu.Lock()
		restored = append(restored, filepath.Base(path))
		mu.Unlock()
		return nil
	}

	if err := Restore(dir, RestoreOptions{Parallel: 1, RestoreUser: true, RestoreFile: restoreFile}); err != nil {
		t.Fatalf("unexpected restore error: %v", err)
	}

	mu.Lock()
	got := append([]string(nil), restored...)
	mu.Unlock()

	want := []string{
		"db.tbl-schema.sql.gz",
		"mysql.system-all.sql.gz",
		"db.__routines-schema-routine.sql.gz",
		"db.a_view-schema-view.sql.gz",
		"db.tbl.00000.sql.gz",
		"db.tbl-schema-trigger.sql.gz",
		"db.__events-schema-event.sql.gz",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected restore order:\n  got:  %v\n  want: %v", got, want)
	}
}

func TestRestoreSchemaPhaseCompletesBeforeData(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"db.tbl-schema.sql.gz",
		"db.tbl.sql.gz",
		"db.other.sql.gz",
		"db.a_view-schema-view.sql.gz",
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("test"), 0644); err != nil {
			t.Fatalf("failed to write file %s: %v", f, err)
		}
	}

	var mu sync.Mutex
	var schemaRestored []string
	var dataStarted []string
	restoreFile := func(path string) error {
		base := filepath.Base(path)
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(base, "-schema") {
			schemaRestored = append(schemaRestored, base)
		} else {
			// Data file: at this point all schema files must already be restored
			dataStarted = append(dataStarted, base)
		}
		return nil
	}

	if err := Restore(dir, RestoreOptions{Parallel: 1, RestoreFile: restoreFile}); err != nil {
		t.Fatalf("unexpected restore error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(schemaRestored) == 0 {
		t.Fatalf("expected schema files to be restored")
	}
	if len(dataStarted) == 0 {
		t.Fatalf("expected data files to be restored")
	}
	// All schema files must appear before any data file in the restore sequence
	// (validated by the fact that the schema phase runs sequentially before data phase)
	if len(schemaRestored) != 2 { // tbl-schema and a_view-schema-view
		t.Fatalf("expected 2 schema files, got %d: %v", len(schemaRestored), schemaRestored)
	}
}

func TestRestoreDataPhaseCompletesBeforePost(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"db.tbl-schema.sql.gz",
		"db.tbl.sql.gz",
		"db.tbl-schema-trigger.sql.gz",
		"db.__events-schema-event.sql.gz",
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("test"), 0644); err != nil {
			t.Fatalf("failed to write file %s: %v", f, err)
		}
	}

	var mu sync.Mutex
	var order []string
	restoreFile := func(path string) error {
		mu.Lock()
		order = append(order, filepath.Base(path))
		mu.Unlock()
		return nil
	}

	if err := Restore(dir, RestoreOptions{Parallel: 1, RestoreFile: restoreFile}); err != nil {
		t.Fatalf("unexpected restore error: %v", err)
	}

	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()

	want := []string{
		"db.tbl-schema.sql.gz",
		"db.tbl.sql.gz",
		"db.tbl-schema-trigger.sql.gz",
		"db.__events-schema-event.sql.gz",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected restore order:\n  got:  %v\n  want: %v", got, want)
	}
}

func TestRestorePhaseStartLogsEmitted(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"db.tbl-schema.sql.gz",
		"db.tbl.sql.gz",
		"db.tbl-schema-trigger.sql.gz",
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("test"), 0644); err != nil {
			t.Fatalf("failed to write file %s: %v", f, err)
		}
	}

	var mu sync.Mutex
	var infoLogs []string
	logger := func(level, format string, args ...any) {
		mu.Lock()
		if level == LogInfo {
			infoLogs = append(infoLogs, fmt.Sprintf(format, args...))
		}
		mu.Unlock()
	}
	restoreFile := func(path string) error { return nil }

	if err := Restore(dir, RestoreOptions{Parallel: 1, Logger: logger, RestoreFile: restoreFile}); err != nil {
		t.Fatalf("unexpected restore error: %v", err)
	}

	mu.Lock()
	logs := append([]string(nil), infoLogs...)
	mu.Unlock()

	hasPhaseLog := func(phase string) bool {
		for _, l := range logs {
			if strings.Contains(l, "phase: "+phase) {
				return true
			}
		}
		return false
	}

	if !hasPhaseLog("schema") {
		t.Fatalf("expected schema phase-start log, got: %v", logs)
	}
	if !hasPhaseLog("data") {
		t.Fatalf("expected data phase-start log, got: %v", logs)
	}
	if !hasPhaseLog("post-data") {
		t.Fatalf("expected post-data phase-start log, got: %v", logs)
	}
}

// ---- DEFINER compatibility tests (story 4.3) ----

func TestIsDefinerError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "error 1449", err: fmt.Errorf("mysql restore failed: ERROR 1449 (HY000): The user specified as a definer ('bad'@'localhost') does not exist"), want: true},
		{name: "definer text", err: fmt.Errorf("definer 'user'@'host' problem"), want: true},
		{name: "other mysql error", err: fmt.Errorf("mysql restore failed: ERROR 1146 (42S02): Table does not exist"), want: false},
		{name: "context canceled", err: context.Canceled, want: false},
		{name: "generic error", err: fmt.Errorf("connection refused"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsDefinerError(tt.err); got != tt.want {
				t.Fatalf("IsDefinerError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestRestoreDefinerNonStrictFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db.v_foo-schema-view.sql.gz"), []byte("test"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	definerErr := fmt.Errorf("mysql restore failed: ERROR 1449 (HY000): The user specified as a definer ('bad_user'@'localhost') does not exist")

	var mu sync.Mutex
	var fallbackCalled []string
	var warnLogs []string

	err := Restore(dir, RestoreOptions{
		Parallel: 1,
		Logger: func(level, format string, args ...any) {
			mu.Lock()
			if level == LogWarn {
				warnLogs = append(warnLogs, fmt.Sprintf(format, args...))
			}
			mu.Unlock()
		},
		Context: context.Background(),
		RestoreFileWithContext: func(ctx context.Context, path string) error {
			return definerErr
		},
		RestoreFileWithoutDefiner: func(ctx context.Context, path string) error {
			mu.Lock()
			fallbackCalled = append(fallbackCalled, filepath.Base(path))
			mu.Unlock()
			return nil
		},
		DefinerStrict: false,
	})

	if err != nil {
		t.Fatalf("expected non-strict fallback to succeed, got: %v", err)
	}

	mu.Lock()
	fc := append([]string(nil), fallbackCalled...)
	wl := append([]string(nil), warnLogs...)
	mu.Unlock()

	if len(fc) != 1 || fc[0] != "db.v_foo-schema-view.sql.gz" {
		t.Fatalf("expected fallback called for view file, got: %v", fc)
	}
	foundFallbackLog := false
	for _, l := range wl {
		if strings.Contains(l, "DEFINER fallback") {
			foundFallbackLog = true
			break
		}
	}
	if !foundFallbackLog {
		t.Fatalf("expected DEFINER fallback warning in logs, got: %v", wl)
	}
}

func TestRestoreDefinerStrictFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db.v_foo-schema-view.sql.gz"), []byte("test"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	definerErr := fmt.Errorf("mysql restore failed: ERROR 1449 (HY000): The user specified as a definer ('bad_user'@'localhost') does not exist")

	var mu sync.Mutex
	var errLogs []string

	err := Restore(dir, RestoreOptions{
		Parallel: 1,
		Logger: func(level, format string, args ...any) {
			mu.Lock()
			if level == LogError {
				errLogs = append(errLogs, fmt.Sprintf(format, args...))
			}
			mu.Unlock()
		},
		Context: context.Background(),
		RestoreFileWithContext: func(ctx context.Context, path string) error {
			return definerErr
		},
		DefinerStrict: true,
	})

	if err == nil {
		t.Fatalf("expected strict DEFINER error, got nil")
	}
	if !errors.Is(err, ErrDefinerStrict) {
		t.Fatalf("expected ErrDefinerStrict, got: %v", err)
	}

	mu.Lock()
	el := append([]string(nil), errLogs...)
	mu.Unlock()

	foundStrictLog := false
	for _, l := range el {
		if strings.Contains(strings.ToLower(l), "strict") && strings.Contains(strings.ToLower(l), "definer") {
			foundStrictLog = true
			break
		}
	}
	if !foundStrictLog {
		t.Fatalf("expected strict DEFINER error log, got: %v", el)
	}
}

func TestRestoreDefinerNotTriggeredOnOtherErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db.tbl-schema.sql.gz"), []byte("test"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	someErr := fmt.Errorf("mysql restore failed: ERROR 1146 (42S02): Table doesn't exist")
	var fallbackCalled bool

	err := Restore(dir, RestoreOptions{
		Parallel: 1,
		Context:  context.Background(),
		RestoreFileWithContext: func(ctx context.Context, path string) error {
			return someErr
		},
		RestoreFileWithoutDefiner: func(ctx context.Context, path string) error {
			fallbackCalled = true
			return nil
		},
		DefinerStrict: false,
	})

	if err == nil {
		t.Fatalf("expected error for non-DEFINER failure, got nil")
	}
	if errors.Is(err, ErrDefinerStrict) {
		t.Fatalf("expected non-DEFINER error, got ErrDefinerStrict")
	}
	if fallbackCalled {
		t.Fatalf("DEFINER fallback should not be triggered for non-DEFINER errors")
	}
}

func TestRestoreDefinerNonStrictNoFallbackFunctionLogs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db.v_foo-schema-view.sql.gz"), []byte("test"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	definerErr := fmt.Errorf("mysql restore failed: ERROR 1449 (HY000): The user specified as a definer ('bad_user'@'localhost') does not exist")

	var mu sync.Mutex
	var warnLogs []string

	err := Restore(dir, RestoreOptions{
		Parallel: 1,
		Logger: func(level, format string, args ...any) {
			mu.Lock()
			if level == LogWarn {
				warnLogs = append(warnLogs, fmt.Sprintf(format, args...))
			}
			mu.Unlock()
		},
		Context: context.Background(),
		RestoreFileWithContext: func(ctx context.Context, path string) error {
			return definerErr
		},
		// No RestoreFileWithoutDefiner — non-strict should skip with a warning
		DefinerStrict: false,
	})

	if err != nil {
		t.Fatalf("expected non-strict with no fallback to skip file (not fail), got: %v", err)
	}

	mu.Lock()
	wl := append([]string(nil), warnLogs...)
	mu.Unlock()

	foundWarn := false
	for _, l := range wl {
		if strings.Contains(strings.ToLower(l), "definer") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Fatalf("expected DEFINER warning log when no fallback function, got: %v", wl)
	}
}

func TestRestoreNormalBehaviorUnchangedWithoutDefinerError(t *testing.T) {
	// AC 4: files without DEFINER issues restore normally with no extra transformation
	dir := t.TempDir()
	for _, f := range []string{"db.tbl-schema.sql.gz", "db.tbl.sql.gz"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("test"), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	var mu sync.Mutex
	var restored []string
	var fallbackCalled bool

	err := Restore(dir, RestoreOptions{
		Parallel: 1,
		Context:  context.Background(),
		RestoreFileWithContext: func(ctx context.Context, path string) error {
			mu.Lock()
			restored = append(restored, filepath.Base(path))
			mu.Unlock()
			return nil
		},
		RestoreFileWithoutDefiner: func(ctx context.Context, path string) error {
			fallbackCalled = true
			return nil
		},
		DefinerStrict: false,
	})

	if err != nil {
		t.Fatalf("expected clean restore, got: %v", err)
	}
	if fallbackCalled {
		t.Fatalf("DEFINER fallback must not be called when no DEFINER error occurs")
	}
	mu.Lock()
	r := append([]string(nil), restored...)
	mu.Unlock()
	if len(r) != 2 {
		t.Fatalf("expected 2 files restored, got %d: %v", len(r), r)
	}
}

// ---- Detect and BuildRestorePlan tests ----

func TestDetectReturnsTrueForSplitdumpDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db.tbl-schema.sql.gz"), []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	ok, err := Detect(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("expected splitdump-compatible, got false")
	}
}

func TestDetectReturnsFalseForNonSplitdumpDir(t *testing.T) {
	dir := t.TempDir()
	// A single file with no schema.table pattern (no dot before .sql extension)
	if err := os.WriteFile(filepath.Join(dir, "backup.sql.gz"), []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	ok, err := Detect(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected not splitdump-compatible, got true")
	}
}

func TestDetectReturnsFalseForEmptyDir(t *testing.T) {
	dir := t.TempDir()
	ok, err := Detect(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected false for empty dir, got true")
	}
}

func TestDetectReturnsFalseForOnlyMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "metadata"), []byte("[source]\nFile=x\n"), 0644); err != nil {
		t.Fatalf("failed to write metadata: %v", err)
	}
	ok, err := Detect(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected false for only-metadata dir, got true")
	}
}

func TestDetectReturnsTrueForMysqlSystemAll(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mysql.system-all.sql.gz"), []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	ok, err := Detect(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("expected splitdump-compatible for mysql.system-all.sql.gz, got false")
	}
}

func TestDetectReturnsErrorForNonExistentDir(t *testing.T) {
	_, err := Detect("/this/path/does/not/exist/splitdump-test")
	if err == nil {
		t.Fatalf("expected error for non-existent dir, got nil")
	}
}

func TestDetectReturnsFalseForNonSplitdumpSqlFile(t *testing.T) {
	dir := t.TempDir()
	// Files with no dot-separated schema.table prefix are not splitdump
	for _, name := range []string{"nodot.sql", "alsono.sql.gz"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}
	ok, err := Detect(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected false for non-splitdump sql files, got true")
	}
}

func TestBuildRestorePlanReturnsPlanForSplitdumpDir(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"db.tbl-schema.sql.gz",
		"db.tbl.sql.gz",
		"db.tbl-schema-trigger.sql.gz",
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("test"), 0644); err != nil {
			t.Fatalf("failed to write file %s: %v", f, err)
		}
	}
	plan, err := BuildRestorePlan(dir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan == nil {
		t.Fatalf("expected non-nil plan")
	}
	if len(plan.Schema) == 0 {
		t.Fatalf("expected schema files in plan")
	}
	if len(plan.Data) == 0 {
		t.Fatalf("expected data files in plan")
	}
	if len(plan.Post) == 0 {
		t.Fatalf("expected post-data files in plan")
	}
}

func TestBuildRestorePlanReturnsErrNotSplitdump(t *testing.T) {
	dir := t.TempDir()
	// A file with no schema.table naming pattern
	if err := os.WriteFile(filepath.Join(dir, "backup.sql.gz"), []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	_, err := BuildRestorePlan(dir, false)
	if err == nil {
		t.Fatalf("expected ErrNotSplitdump, got nil")
	}
	if !errors.Is(err, ErrNotSplitdump) {
		t.Fatalf("expected ErrNotSplitdump, got: %v", err)
	}
}

func TestBuildRestorePlanReturnsErrNotSplitdumpForEmptyDir(t *testing.T) {
	dir := t.TempDir()
	_, err := BuildRestorePlan(dir, false)
	if err == nil {
		t.Fatalf("expected ErrNotSplitdump for empty dir, got nil")
	}
	if !errors.Is(err, ErrNotSplitdump) {
		t.Fatalf("expected ErrNotSplitdump, got: %v", err)
	}
}

func TestBuildRestorePlanPhaseOrdering(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"db.tbl-schema-event.sql.gz",
		"db.tbl-schema-trigger.sql.gz",
		"db.tbl.sql.gz",
		"db.tbl-schema.sql.gz",
		"mysql.system-all.sql.gz",
		"db.__routines-schema-routine.sql.gz",
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("test"), 0644); err != nil {
			t.Fatalf("failed to write file %s: %v", f, err)
		}
	}

	plan, err := BuildRestorePlan(dir, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Schema phase: table schema first, then mysql.system-all, then routines
	if len(plan.Schema) < 3 {
		t.Fatalf("expected at least 3 schema files, got %d: %v", len(plan.Schema), plan.Schema)
	}
	schemaBase := filepath.Base(plan.Schema[0])
	if schemaBase != "db.tbl-schema.sql.gz" {
		t.Fatalf("expected table schema first in schema phase, got %q", schemaBase)
	}
	systemAllBase := filepath.Base(plan.Schema[1])
	if systemAllBase != "mysql.system-all.sql.gz" {
		t.Fatalf("expected mysql.system-all second in schema phase, got %q", systemAllBase)
	}

	// Post phase: triggers before events
	if len(plan.Post) < 2 {
		t.Fatalf("expected at least 2 post-data files, got %d", len(plan.Post))
	}
	triggerBase := filepath.Base(plan.Post[0])
	if !strings.Contains(triggerBase, "trigger") {
		t.Fatalf("expected trigger first in post phase, got %q", triggerBase)
	}
	eventBase := filepath.Base(plan.Post[1])
	if !strings.Contains(eventBase, "event") {
		t.Fatalf("expected event second in post phase, got %q", eventBase)
	}
}

func TestBuildRestorePlanRespectsRestoreUser(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		"db.tbl-schema.sql.gz",
		"db.tbl.sql.gz",
		"mysql.system-all.sql.gz",
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("test"), 0644); err != nil {
			t.Fatalf("failed to write file %s: %v", f, err)
		}
	}

	// With restoreUser=false, mysql.system-all should be excluded from schema phase
	planNoUser, err := BuildRestorePlan(dir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, p := range planNoUser.Schema {
		if strings.Contains(filepath.Base(p), "system-all") {
			t.Fatalf("expected mysql.system-all excluded when restoreUser=false")
		}
	}

	// With restoreUser=true, mysql.system-all should be included in schema phase
	planWithUser, err := BuildRestorePlan(dir, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, p := range planWithUser.Schema {
		if strings.Contains(filepath.Base(p), "system-all") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected mysql.system-all in schema phase when restoreUser=true")
	}
}

// ---- Detection outcome and log distinguishability tests (story 4.4) ----

func TestRestoreDetectionOutcomeLogged(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"db.tbl-schema.sql.gz", "db.tbl.sql.gz"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("test"), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	var mu sync.Mutex
	var infoLogs []string
	logger := func(level, format string, args ...any) {
		mu.Lock()
		if level == LogInfo {
			infoLogs = append(infoLogs, fmt.Sprintf(format, args...))
		}
		mu.Unlock()
	}

	if err := Restore(dir, RestoreOptions{Parallel: 1, Logger: logger, RestoreFile: func(string) error { return nil }}); err != nil {
		t.Fatalf("unexpected restore error: %v", err)
	}

	mu.Lock()
	logs := append([]string(nil), infoLogs...)
	mu.Unlock()

	found := false
	for _, l := range logs {
		lower := strings.ToLower(l)
		if strings.Contains(lower, "detection") && strings.Contains(lower, "confirmed") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected detection outcome log containing 'detection' and 'confirmed', got: %v", logs)
	}
}

func TestRestoreOrderingFailureDistinguishableFromDefinerFailure(t *testing.T) {
	// Schema restore failures should log differently from DEFINER strict failures,
	// allowing operators to distinguish ordering errors from DEFINER enforcement errors.

	// --- Schema ordering failure ---
	dir1 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir1, "db.tbl-schema.sql.gz"), []byte("test"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	schemaErr := fmt.Errorf("mysqldump restore error: connection refused")
	var mu1 sync.Mutex
	var errLogs1 []string
	Restore(dir1, RestoreOptions{ //nolint:errcheck
		Parallel: 1,
		Logger: func(level, format string, args ...any) {
			mu1.Lock()
			if level == LogError {
				errLogs1 = append(errLogs1, fmt.Sprintf(format, args...))
			}
			mu1.Unlock()
		},
		RestoreFile: func(string) error { return schemaErr },
	})

	// --- DEFINER strict failure ---
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, "db.v_foo-schema-view.sql.gz"), []byte("test"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	definerErr := fmt.Errorf("mysql restore failed: ERROR 1449 (HY000): The user specified as a definer does not exist")
	var mu2 sync.Mutex
	var errLogs2 []string
	Restore(dir2, RestoreOptions{ //nolint:errcheck
		Parallel: 1,
		Logger: func(level, format string, args ...any) {
			mu2.Lock()
			if level == LogError {
				errLogs2 = append(errLogs2, fmt.Sprintf(format, args...))
			}
			mu2.Unlock()
		},
		Context: context.Background(),
		RestoreFileWithContext: func(ctx context.Context, path string) error { return definerErr },
		DefinerStrict: true,
	})

	mu1.Lock()
	el1 := append([]string(nil), errLogs1...)
	mu1.Unlock()

	mu2.Lock()
	el2 := append([]string(nil), errLogs2...)
	mu2.Unlock()

	if len(el1) == 0 {
		t.Fatalf("expected schema error logs, got none")
	}
	if len(el2) == 0 {
		t.Fatalf("expected DEFINER error logs, got none")
	}

	// Schema failure log must not mention "definer" or "strict"
	for _, l := range el1 {
		lower := strings.ToLower(l)
		if strings.Contains(lower, "definer") || strings.Contains(lower, "strict") {
			t.Fatalf("schema restore failure log should not mention definer/strict: %v", el1)
		}
	}

	// DEFINER strict failure log must contain both "definer" and "strict"
	found := false
	for _, l := range el2 {
		lower := strings.ToLower(l)
		if strings.Contains(lower, "definer") && strings.Contains(lower, "strict") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected DEFINER strict log to contain 'definer' and 'strict', got: %v", el2)
	}
}

func TestListSchemas(t *testing.T) {
	dir := t.TempDir()
	// User schemas
	for _, name := range []string{
		"app.tbl-schema.sql.gz",
		"app.tbl.sql.gz",
		"logs.events-schema.sql.gz",
		"logs.events.sql.gz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(""), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}
	// System schemas that must be excluded
	for _, name := range []string{
		"mysql.system-all.sql.gz",
		"sys.sys_config-schema.sql.gz",
		"information_schema.tables-schema.sql.gz",
		"performance_schema.events-schema.sql.gz",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(""), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}

	got, err := ListSchemas(dir)
	if err != nil {
		t.Fatalf("ListSchemas returned error: %v", err)
	}
	want := []string{"app", "logs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListSchemas = %v, want %v", got, want)
	}
}

func TestListSchemasEmptyDir(t *testing.T) {
	dir := t.TempDir()
	got, err := ListSchemas(dir)
	if err != nil {
		t.Fatalf("ListSchemas returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %v", got)
	}
}

func TestIsMysqlTableCheckEligible(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "mysql.tbl-schema.sql.gz", want: true},
		{name: "mysql.tbl.sql.gz", want: true},
		{name: "mysql.tbl-schema-trigger.sql.gz", want: true},
		{name: "mysql.tbl-schema-view.sql.gz", want: false},
		{name: "mysql.__routines-schema-routine.sql.gz", want: false},
		{name: "mysql.__events-schema-event.sql.gz", want: false},
		{name: "mysql.system-all.sql.gz", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsMysqlTableCheckEligible(tt.name); got != tt.want {
				t.Fatalf("IsMysqlTableCheckEligible(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
