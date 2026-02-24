package splitdump

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
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

	reader := splitDumpOpenReader(file)
	got := make([]byte, len(data))
	if _, err := io.ReadFull(reader, got); err != nil {
		t.Fatalf("failed to read input file: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("unexpected input bytes: %v", got)
	}
}
