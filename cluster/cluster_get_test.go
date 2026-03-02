package cluster

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func expectedExecutablePath(t *testing.T) string {
	t.Helper()

	path, err := os.Executable()
	if err != nil {
		t.Fatalf("failed to get executable path: %v", err)
	}

	finfo, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("failed to lstat executable path: %v", err)
	}

	if finfo.Mode()&os.ModeSymlink != 0 {
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatalf("failed to eval symlink: %v", err)
		}
	}

	path, err = filepath.Abs(path)
	if err != nil {
		t.Fatalf("failed to get absolute path: %v", err)
	}

	return path
}

func TestGetRepManAbsolutePath(t *testing.T) {
	cluster := &Cluster{}

	got, err := cluster.GetRepManAbsolutePath()
	if err != nil {
		t.Fatalf("GetRepManAbsolutePath error: %v", err)
	}

	if !filepath.IsAbs(got) {
		t.Fatalf("expected absolute path, got %q", got)
	}

	expected := expectedExecutablePath(t)
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestGetReplicationManagerCliPathUsesConfiguredValue(t *testing.T) {
	cluster := &Cluster{Conf: &config.Config{ReplicationManagerCliPath: "/custom/replication-manager-cli"}}

	got := cluster.GetReplicationManagerCliPath()
	if got != cluster.Conf.ReplicationManagerCliPath {
		t.Fatalf("expected configured path %q, got %q", cluster.Conf.ReplicationManagerCliPath, got)
	}
}

func TestGetReplicationManagerCliPathUsesExecutableDir(t *testing.T) {
	cluster := &Cluster{Conf: &config.Config{ReplicationManagerCliPath: "  "}}

	got := cluster.GetReplicationManagerCliPath()
	expected := "replication-manager-cli"
	exeDir := filepath.Dir(expectedExecutablePath(t))
	localPath := filepath.Join(exeDir, "replication-manager-cli")
	if _, err := os.Stat(localPath); err == nil {
		expected = localPath
	} else if path, err := exec.LookPath("replication-manager-cli"); err == nil {
		expected = path
	}
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}
