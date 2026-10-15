//go:build clients
// +build clients

package clients

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestRunSplitdumpFromFile(t *testing.T) {
	outputDir := t.TempDir()
	inputFile := filepath.Join(t.TempDir(), "dump.sql")

	dump := strings.Join([]string{
		"USE `test`;",
		"-- Table structure for table `t1`",
		"CREATE TABLE `t1` (",
		"  `id` int",
		");",
		"CHANGE MASTER TO MASTER_LOG_FILE='bin.000001', MASTER_LOG_POS=123;",
		"-- Dumping data for table `t1`",
		"LOCK TABLES `t1` WRITE;",
		"INSERT INTO `t1` VALUES (1);",
		"UNLOCK TABLES;",
		"",
	}, "\n")

	if err := os.WriteFile(inputFile, []byte(dump), 0644); err != nil {
		t.Fatalf("failed to write input dump: %v", err)
	}

	if err := runSplitdump(inputFile, outputDir); err != nil {
		t.Fatalf("runSplitdump error: %v", err)
	}

	metadataPath := filepath.Join(outputDir, "metadata")
	metadata, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("failed to read metadata: %v", err)
	}
	metaStr := string(metadata)
	if !strings.Contains(metaStr, "File = bin.000001") {
		t.Fatalf("metadata missing binlog file: %s", metaStr)
	}
	if !strings.Contains(metaStr, "Position = 123") {
		t.Fatalf("metadata missing position: %s", metaStr)
	}

	schemaFile := filepath.Join(outputDir, "test.t1-schema.sql.gz")
	if _, err := os.Stat(schemaFile); err != nil {
		t.Fatalf("schema file missing: %v", err)
	}

	dataFile := filepath.Join(outputDir, "test.t1.00000.sql.gz")
	if _, err := os.Stat(dataFile); err != nil {
		t.Fatalf("data file missing: %v", err)
	}
}

// A failed split exits 1, its error on stderr.
func TestSplitdumpCommandExitsOnFailure(t *testing.T) {
	if os.Getenv("SPLITDUMP_EXIT_TEST") == "1" {
		cliInputFile = filepath.Join(t.TempDir(), "missing.sql")
		cliOutputDir = filepath.Join(t.TempDir(), "splitdump")
		splitDumpCmd.Run(splitDumpCmd, nil)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSplitdumpCommandExitsOnFailure$")
	cmd.Env = append(os.Environ(), "SPLITDUMP_EXIT_TEST=1")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		t.Fatalf("want exit 1, got %v (stderr %q)", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "missing.sql") {
		t.Fatalf("stderr %q", stderr.String())
	}
}
