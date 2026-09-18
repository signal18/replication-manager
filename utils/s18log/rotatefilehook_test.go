package s18log

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestNewRotateWriterRotatesOnSize(t *testing.T) {
	dir := t.TempDir()
	logfile := filepath.Join(dir, "log_slow_query.log")

	w, err := NewRotateWriter(RotateFileConfig{
		Filename:   logfile,
		MaxSize:    1, // 1 MB
		MaxBackups: 2,
		MaxAge:     1,
	})
	if err != nil {
		t.Fatalf("NewRotateWriter returned error: %v", err)
	}
	defer w.Close()

	chunk := bytes.Repeat([]byte("a"), 200*1024) // 200KB per write, under MaxSize
	for i := 0; i < 10; i++ {                    // 2MB total, over MaxSize
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("Write returned error: %v", err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir returned error: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected rotation to produce a backup file, got %d file(s) in %s", len(entries), dir)
	}

	if _, err := os.Stat(logfile); err != nil {
		t.Fatalf("expected active log file %s to exist: %v", logfile, err)
	}
}

func TestNewRotateWriterAppendsWithoutRotationBelowMaxSize(t *testing.T) {
	dir := t.TempDir()
	logfile := filepath.Join(dir, "log_error.log")

	w, err := NewRotateWriter(RotateFileConfig{
		Filename:   logfile,
		MaxSize:    100,
		MaxBackups: 10,
		MaxAge:     7,
	})
	if err != nil {
		t.Fatalf("NewRotateWriter returned error: %v", err)
	}

	if _, err := w.Write([]byte("line one\n")); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if _, err := w.Write([]byte("line two\n")); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir returned error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected no rotation below MaxSize, got %d file(s) in %s", len(entries), dir)
	}

	data, err := os.ReadFile(logfile)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(data) != "line one\nline two\n" {
		t.Fatalf("unexpected file content: %q", string(data))
	}
}
