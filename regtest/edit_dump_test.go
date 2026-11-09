// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeGz(t *testing.T, path, content string) {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	zw.Write([]byte(content))
	zw.Close()
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readGz(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// editDump inserts before the last footer, keeps the file's time and leaves no temp file.
func TestEditDumpStreamsBeforeLastFooter(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "dump.sql.gz")
	body := "-- MariaDB dump\nINSERT INTO t VALUES ('-- Dump completed in a value');\n" +
		strings.Repeat("INSERT INTO t VALUES (1);\n", 50000) +
		"-- Dump completed on 2026-11-09 12:00:00\n"
	writeGz(t, p, body)
	mtime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if err := editDump(p, "CREATE DATABASE marker;\n"); err != nil {
		t.Fatal(err)
	}
	got := readGz(t, p)
	i := strings.LastIndex(body, "-- Dump completed")
	want := body[:i] + "CREATE DATABASE marker;\n" + body[i:]
	if got != want {
		t.Fatalf("edited dump differs from the expected one (len %d, want %d)", len(got), len(want))
	}
	if fi, err := os.Stat(p); err != nil || !fi.ModTime().Equal(mtime) {
		t.Fatalf("modification time not kept: %v %v", fi.ModTime(), err)
	}
	if _, err := os.Stat(p + ".edit.tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}
}

func TestEditDumpWithoutFooterFailsAndKeepsFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "dump.sql.gz")
	writeGz(t, p, "INSERT INTO t VALUES (1);\n")
	if err := editDump(p, "x\n"); err == nil {
		t.Fatalf("a dump without footer must be refused")
	}
	if got := readGz(t, p); got != "INSERT INTO t VALUES (1);\n" {
		t.Fatalf("the dump was changed: %q", got)
	}
}
