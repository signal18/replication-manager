package cluster

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/shirou/gopsutil/disk"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
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

func TestGetDiskStatWorksForSiblingBackupDirsOnSameFilesystem(t *testing.T) {
	oldResolve := resolveDiskFilesystem
	oldUsage := getDiskUsage
	defer func() {
		resolveDiskFilesystem = oldResolve
		getDiskUsage = oldUsage
	}()

	dir1 := "/backups/cluster-a/server-1"
	dir2 := "/backups/cluster-a/server-2"
	usageCalls := 0

	resolveDiskFilesystem = func(path string) (misc.FilesystemPath, error) {
		switch filepath.Clean(path) {
		case filepath.Clean(dir1), filepath.Clean(dir2):
			return misc.FilesystemPath{Key: "fs-1", Mountpoint: "/mnt/backup"}, nil
		default:
			return misc.FilesystemPath{}, fmt.Errorf("unexpected path %s", path)
		}
	}
	getDiskUsage = func(path string) (*disk.UsageStat, error) {
		usageCalls++
		return &disk.UsageStat{Path: path, Free: 4096}, nil
	}

	cluster := &Cluster{
		Conf:            &config.Config{},
		DiskStatManager: misc.NewDiskStatManager(),
	}

	stat1, err := cluster.GetDiskStat(dir1)
	if err != nil {
		t.Fatalf("GetDiskStat(%q) returned error: %v", dir1, err)
	}
	stat2, err := cluster.GetDiskStat(dir2)
	if err != nil {
		t.Fatalf("GetDiskStat(%q) returned error: %v", dir2, err)
	}

	if usageCalls != 1 {
		t.Fatalf("expected one disk usage refresh for sibling directories on the same filesystem, got %d", usageCalls)
	}
	if stat1 != stat2 {
		t.Fatalf("expected sibling directories to resolve to the same filesystem stat")
	}
	if stat2.Path != "/mnt/backup" {
		t.Fatalf("expected mountpoint-only label, got %q", stat2.Path)
	}
	if _, ok := cluster.DiskStatManager.Stats["fs-1"]; !ok {
		t.Fatalf("expected stat to be stored by filesystem key")
	}
}

func TestGetMyBackupDirectoryPathPreservesAbsoluteRootWhenWorkingDirEmpty(t *testing.T) {
	cl := &Cluster{Name: "cluster-a", Conf: &config.Config{}}
	srv := &ServerMonitor{Host: "db1", Port: "3306", ClusterGroup: cl}

	if got := srv.GetMyBackupDirectoryPath(); got != "/backups/cluster-a/db1_3306" {
		t.Fatalf("expected absolute backup path when WorkingDir is empty, got %q", got)
	}
}

func TestCheckDisksUsageFiltersSharedStatsToClusterFilesystems(t *testing.T) {
	oldResolve := resolveDiskFilesystem
	defer func() {
		resolveDiskFilesystem = oldResolve
	}()

	resolveDiskFilesystem = func(path string) (misc.FilesystemPath, error) {
		switch filepath.Clean(path) {
		case filepath.Clean("/work/backups/cluster-a/db1_3306"):
			return misc.FilesystemPath{Key: "fs-a", Mountpoint: "/mnt/a"}, nil
		case filepath.Clean("/work/backups/cluster-b/db2_3306"):
			return misc.FilesystemPath{Key: "fs-b", Mountpoint: "/mnt/b"}, nil
		default:
			return misc.FilesystemPath{}, fmt.Errorf("unexpected path %s", path)
		}
	}

	sharedStats := misc.NewDiskStatManager()
	sharedStats.ReplaceStats(misc.DiskUsageStatMap{
		"fs-a": misc.NewDiskUsageStat(&disk.UsageStat{Path: "/mnt/a", UsedPercent: 60}),
		"fs-b": misc.NewDiskUsageStat(&disk.UsageStat{Path: "/mnt/b", UsedPercent: 96}),
	})

	clusterA := &Cluster{
		Name:            "cluster-a",
		Conf:            &config.Config{WorkingDir: "/work", BackupCheckFreeSpace: true, BackupDiskTresholdWarn: 70, BackupDiskTresholdCrit: 90},
		DiskStatManager: sharedStats,
		StateMachine:    &state.StateMachine{},
		Servers:         serverList{&ServerMonitor{Host: "db1", Port: "3306"}},
	}
	clusterA.Servers[0].ClusterGroup = clusterA
	clusterA.StateMachine.Init()

	clusterB := &Cluster{
		Name:            "cluster-b",
		Conf:            &config.Config{WorkingDir: "/work", BackupCheckFreeSpace: true, BackupDiskTresholdWarn: 70, BackupDiskTresholdCrit: 90},
		DiskStatManager: sharedStats,
		StateMachine:    &state.StateMachine{},
		Servers:         serverList{&ServerMonitor{Host: "db2", Port: "3306"}},
	}
	clusterB.Servers[0].ClusterGroup = clusterB
	clusterB.StateMachine.Init()

	clusterA.CheckDisksUsage()
	if clusterA.StateMachine.IsInState("WARN0139") || clusterA.StateMachine.IsInState("WARN0140") {
		t.Fatalf("expected cluster-a not to alert on cluster-b filesystem")
	}

	clusterB.CheckDisksUsage()
	if !clusterB.StateMachine.IsInState("WARN0140") {
		t.Fatalf("expected cluster-b to alert on its own critical filesystem")
	}
}
