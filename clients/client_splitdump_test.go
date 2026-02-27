//go:build clients
// +build clients

package clients

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunSplitdumpRejectsNegativeStreamSize(t *testing.T) {
	old := cliSplitDumpStreamSizeMax
	cliSplitDumpStreamSizeMax = "-1"
	t.Cleanup(func() {
		cliSplitDumpStreamSizeMax = old
	})

	outputDir := filepath.Join(t.TempDir(), "splitdump")
	if err := runSplitdump("", outputDir); err == nil {
		t.Fatal("expected error for negative stream-size-max")
	}
}

func TestRunSplitdumpAcceptsZeroStreamSize(t *testing.T) {
	old := cliSplitDumpStreamSizeMax
	cliSplitDumpStreamSizeMax = "0"
	t.Cleanup(func() {
		cliSplitDumpStreamSizeMax = old
	})

	inputPath := filepath.Join(t.TempDir(), "empty.sql")
	if err := os.WriteFile(inputPath, []byte(""), 0644); err != nil {
		t.Fatalf("failed to write input file: %v", err)
	}

	outputDir := filepath.Join(t.TempDir(), "splitdump")
	if err := runSplitdump(inputPath, outputDir); err != nil {
		t.Fatalf("unexpected error for zero stream-size-max: %v", err)
	}
}
