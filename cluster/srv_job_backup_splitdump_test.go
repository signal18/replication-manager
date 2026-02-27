package cluster

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/sirupsen/logrus"
)

func newSplitDumpTestServer(t *testing.T) (*Cluster, *ServerMonitor) {
	cluster := &Cluster{
		Name:   "test-cluster",
		Conf:   &config.Config{WorkingDir: t.TempDir()},
		Logrus: logrus.New(),
	}
	server := &ServerMonitor{
		Host:         "127.0.0.1",
		Port:         "3306",
		URL:          "127.0.0.1:3306",
		ClusterGroup: cluster,
	}
	return cluster, server
}

func writeSplitDumpCliScript(t *testing.T, cluster *Cluster, exitCode int) {
	cliPath := filepath.Join(t.TempDir(), "replication-manager-cli")
	script := "#!/bin/sh\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(cliPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write cli script: %v", err)
	}
	if err := os.Chmod(cliPath, 0755); err != nil {
		t.Fatalf("failed to chmod cli script: %v", err)
	}
	cluster.Conf.ReplicationManagerCliPath = cliPath
}

func writeSplitDumpCliScriptWithArgs(t *testing.T, cluster *Cluster, argsPath string, exitCode int) {
	cliPath := filepath.Join(t.TempDir(), "replication-manager-cli")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\nexit %d\n", argsPath, exitCode)
	if err := os.WriteFile(cliPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write cli script: %v", err)
	}
	if err := os.Chmod(cliPath, 0755); err != nil {
		t.Fatalf("failed to chmod cli script: %v", err)
	}
	cluster.Conf.ReplicationManagerCliPath = cliPath
}

func skipSplitDumpOnWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script not supported on windows")
	}
}

func prepareSplitDumpOutputDir(t *testing.T, server *ServerMonitor) string {
	outputDir := filepath.Join(server.GetMyBackupDirectory(), "splitdump")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("failed to create splitdump dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "old.sql"), []byte("old"), 0644); err != nil {
		t.Fatalf("failed to write splitdump file: %v", err)
	}
	return outputDir
}

func runSplitDumpWithCli(t *testing.T, cluster *Cluster, server *ServerMonitor, outputDir string, allowRotate bool) {
	ctx := context.Background()
	if err := cluster.SplitDumpWithCli(ctx, server, outputDir, allowRotate, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatalf("splitdump failed: %v", err)
	}
}

func TestSplitDumpPipelineErrorCancelsContext(t *testing.T) {
	skipSplitDumpOnWindows(t)

	cluster, server := newSplitDumpTestServer(t)
	writeSplitDumpCliScript(t, cluster, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputDir := filepath.Join(server.GetMyBackupDirectory(), "splitdump")
	pipeline := server.setupSplitDumpPipeline(ctx, outputDir, false, cancel)
	if pipeline == nil {
		t.Fatal("expected splitdump pipeline")
	}
	if pipeline.pipeWriter != nil {
		_ = pipeline.pipeWriter.Close()
	}

	select {
	case err, ok := <-pipeline.errCh:
		if !ok {
			t.Fatal("expected splitdump error, channel closed")
		}
		if err == nil {
			t.Fatal("expected splitdump error, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for splitdump error")
	}

	select {
	case <-ctx.Done():
		// expected
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for context cancellation")
	}
}

func TestSplitDumpWithCliRemovesDirWhenRotationDisabled(t *testing.T) {
	skipSplitDumpOnWindows(t)

	cluster, server := newSplitDumpTestServer(t)
	writeSplitDumpCliScript(t, cluster, 0)

	outputDir := prepareSplitDumpOutputDir(t, server)
	runSplitDumpWithCli(t, cluster, server, outputDir, false)

	entries, err := os.ReadDir(outputDir)
	if err != nil {
		t.Fatalf("failed to read splitdump dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty splitdump dir after removal, got %d entries", len(entries))
	}

	rotated, err := filepath.Glob(outputDir + ".old.*")
	if err != nil {
		t.Fatalf("failed to glob rotated dirs: %v", err)
	}
	if len(rotated) != 0 {
		t.Fatalf("expected no rotated dir, got %v", rotated)
	}
}

func TestSplitDumpWithCliRotatesDirWhenEnabled(t *testing.T) {
	skipSplitDumpOnWindows(t)

	cluster, server := newSplitDumpTestServer(t)
	writeSplitDumpCliScript(t, cluster, 0)

	outputDir := prepareSplitDumpOutputDir(t, server)
	runSplitDumpWithCli(t, cluster, server, outputDir, true)

	rotated, err := filepath.Glob(outputDir + ".old.*")
	if err != nil {
		t.Fatalf("failed to glob rotated dirs: %v", err)
	}
	if len(rotated) == 0 {
		t.Fatalf("expected rotated dir, got none")
	}
	oldFile := filepath.Join(rotated[0], "old.sql")
	if _, err := os.Stat(oldFile); err != nil {
		t.Fatalf("expected old file in rotated dir: %v", err)
	}

	entries, err := os.ReadDir(outputDir)
	if err != nil {
		t.Fatalf("failed to read splitdump dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty splitdump dir after rotation, got %d entries", len(entries))
	}
}

func TestSplitDumpWithCliAddsStreamSizeMax(t *testing.T) {
	skipSplitDumpOnWindows(t)

	cluster, server := newSplitDumpTestServer(t)
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	writeSplitDumpCliScriptWithArgs(t, cluster, argsPath, 0)
	cluster.Conf.BackupSplitdumpFileSize = "16MiB"

	outputDir := prepareSplitDumpOutputDir(t, server)
	runSplitDumpWithCli(t, cluster, server, outputDir, false)

	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("failed to read args file: %v", err)
	}
	args := string(data)
	if !strings.Contains(args, "--stream-size-max") {
		t.Fatalf("expected stream-size-max arg, got: %s", args)
	}
	if !strings.Contains(args, "16MiB") {
		t.Fatalf("expected stream-size-max value, got: %s", args)
	}
}

func TestIsSplitDumpName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{name: "splitdump", want: true},
		{name: "splitdump.1700000000", want: true},
		{name: "mysqldump.sql.gz", want: false},
		{name: "splitdump.bad", want: false},
		{name: "", want: false},
	}

	for _, tc := range cases {
		if got := isSplitDumpName(tc.name); got != tc.want {
			t.Fatalf("isSplitDumpName(%q) = %t, want %t", tc.name, got, tc.want)
		}
	}
}

func TestIsSplitDumpDir(t *testing.T) {
	base := t.TempDir()

	missingPath := filepath.Join(base, "missing")
	if _, err := isSplitDumpDir(missingPath); err == nil {
		t.Fatal("expected error for missing path")
	}

	filePath := filepath.Join(base, "file")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	if ok, err := isSplitDumpDir(filePath); err != nil || ok {
		t.Fatalf("expected file path to be false, err=%v", err)
	}

	metadataDir := filepath.Join(base, "with-metadata")
	if err := os.MkdirAll(metadataDir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(metadataDir, "metadata"), []byte("meta"), 0644); err != nil {
		t.Fatalf("failed to write metadata: %v", err)
	}
	if ok, err := isSplitDumpDir(metadataDir); err != nil || !ok {
		t.Fatalf("expected metadata dir to be true, err=%v", err)
	}

	dataDir := filepath.Join(base, "with-data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "db.table.00000.sql.gz"), []byte("data"), 0644); err != nil {
		t.Fatalf("failed to write data file: %v", err)
	}
	if ok, err := isSplitDumpDir(dataDir); err != nil || !ok {
		t.Fatalf("expected data dir to be true, err=%v", err)
	}

	emptyDir := filepath.Join(base, "empty")
	if err := os.MkdirAll(emptyDir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if ok, err := isSplitDumpDir(emptyDir); err != nil || ok {
		t.Fatalf("expected empty dir to be false, err=%v", err)
	}
}
