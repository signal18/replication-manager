package backupmgr

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseRetentionDuration(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		expected   time.Duration
		expectedOK bool
	}{
		{"Empty string", "", 0, false},
		{"Whitespace", "  ", 0, false},
		{"Go duration", "2h", 2 * time.Hour, true},
		{"Days", "7d", 7 * 24 * time.Hour, true},
		{"Weeks", "2w", 14 * 24 * time.Hour, true},
		{"Months", "1mo", 30 * 24 * time.Hour, true},
		{"Years", "1y", 365 * 24 * time.Hour, true},
		{"Invalid suffix", "3q", 0, false},
		{"Invalid number", "xd", 0, false},
		{"Zero duration", "0d", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseRetentionDuration(tt.value)
			if ok != tt.expectedOK {
				t.Fatalf("ParseRetentionDuration(%q) ok = %v, want %v", tt.value, ok, tt.expectedOK)
			}
			if ok && got != tt.expected {
				t.Fatalf("ParseRetentionDuration(%q) = %v, want %v", tt.value, got, tt.expected)
			}
		})
	}
}

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
