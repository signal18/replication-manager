package splitdump

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSplitDumpLineParserEmptyInput(t *testing.T) {
	bus := NewSplitDumpChannelBus()
	outputDir := filepath.Join(t.TempDir(), "splitdump")
	close(bus.CurrentLine)

	go SplitDumpLineParser(bus, outputDir)

	select {
	case <-bus.Finished:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for splitdump to finish")
	}

	metadataPath := filepath.Join(outputDir, "metadata")
	if _, err := os.Stat(metadataPath); err != nil {
		t.Fatalf("metadata file missing: %v", err)
	}
}

func TestSplitDumpOpenReaderInvalidGzip(t *testing.T) {
	inputPath := filepath.Join(t.TempDir(), "bad.gz")
	data := []byte{0x1f, 0x8b}
	if err := os.WriteFile(inputPath, data, 0644); err != nil {
		t.Fatalf("failed to write input file: %v", err)
	}

	file, err := os.Open(inputPath)
	if err != nil {
		t.Fatalf("failed to open input file: %v", err)
	}
	defer file.Close()

	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to capture stderr: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = oldStderr
	})

	reader := splitDumpOpenReader(file)
	if err := w.Close(); err != nil {
		t.Fatalf("failed to close stderr writer: %v", err)
	}
	logged, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("failed to read stderr: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("failed to close stderr reader: %v", err)
	}
	got := make([]byte, len(data))
	if _, err := io.ReadFull(reader, got); err != nil {
		t.Fatalf("failed to read input file: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("unexpected input bytes: %v", got)
	}
	if !strings.Contains(string(logged), "falling back to raw reader") {
		t.Fatalf("expected fallback warning in stderr, got: %s", logged)
	}
}

func TestSplitDumpLineParserMySQLGtidPurgedVariants(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		expected string
	}{
		{
			name:     "plain",
			line:     "SET @@GLOBAL.GTID_PURGED='01234567-89ab-cdef-0123-456789abcdef:1-10';\n",
			expected: "01234567-89ab-cdef-0123-456789abcdef:1-10",
		},
		{
			name:     "with-comment",
			line:     "SET @@GLOBAL.GTID_PURGED=/*!80000 '+'*/ '01234567-89ab-cdef-0123-456789abcdef:1-20';\n",
			expected: "01234567-89ab-cdef-0123-456789abcdef:1-20",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := NewSplitDumpChannelBus()
			outputDir := filepath.Join(t.TempDir(), "splitdump")

			go SplitDumpLineParser(bus, outputDir)
			bus.CurrentLine <- tc.line
			close(bus.CurrentLine)

			select {
			case <-bus.Finished:
			case <-time.After(2 * time.Second):
				t.Fatal("timeout waiting for splitdump to finish")
			}

			metadataPath := filepath.Join(outputDir, "metadata")
			data, err := os.ReadFile(metadataPath)
			if err != nil {
				t.Fatalf("failed to read metadata file: %v", err)
			}
			expectedLine := "Executed_Gtid_Set = " + tc.expected
			if !strings.Contains(string(data), expectedLine) {
				t.Fatalf("expected metadata to contain %q, got: %s", expectedLine, data)
			}
		})
	}
}
