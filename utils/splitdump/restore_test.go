package splitdump

import (
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
