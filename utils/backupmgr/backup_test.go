package backupmgr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectCompressionFromDest(t *testing.T) {
	baseDir := t.TempDir()
	gzipFile := filepath.Join(baseDir, "sample.gz")
	plainFile := filepath.Join(baseDir, "plain.txt")
	if err := os.WriteFile(gzipFile, []byte{0x1f, 0x8b, 0x08, 0x00}, 0644); err != nil {
		t.Fatalf("failed to write gzip file: %v", err)
	}
	if err := os.WriteFile(plainFile, []byte("plain"), 0644); err != nil {
		t.Fatalf("failed to write plain file: %v", err)
	}

	compressed, err := DetectCompressionFromDest(gzipFile)
	if err != nil {
		t.Fatalf("DetectCompressionFromDest(gzipFile) error: %v", err)
	}
	if !compressed {
		t.Fatalf("expected gzip file to be detected as compressed")
	}

	compressed, err = DetectCompressionFromDest(plainFile)
	if err != nil {
		t.Fatalf("DetectCompressionFromDest(plainFile) error: %v", err)
	}
	if compressed {
		t.Fatalf("expected plain file to be detected as uncompressed")
	}

	compressedDir := filepath.Join(baseDir, "dir")
	if err := os.MkdirAll(compressedDir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(compressedDir, "inner.gz"), []byte{0x1f, 0x8b, 0x08, 0x00}, 0644); err != nil {
		t.Fatalf("failed to write inner gzip file: %v", err)
	}
	compressed, err = DetectCompressionFromDest(compressedDir)
	if err != nil {
		t.Fatalf("DetectCompressionFromDest(dir) error: %v", err)
	}
	if !compressed {
		t.Fatalf("expected directory with gzip file to be detected as compressed")
	}
}
