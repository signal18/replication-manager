package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/shirou/gopsutil/disk"
	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
)

func TestRefreshDiskStatsReplacesSnapshotAndDedupesByFilesystem(t *testing.T) {
	oldResolve := resolveDiskFilesystemWithPartitions
	oldUsage := getDiskUsage
	defer func() {
		resolveDiskFilesystemWithPartitions = oldResolve
		getDiskUsage = oldUsage
	}()

	usageCalls := 0
	resolveDiskFilesystemWithPartitions = func(path string, partitions []disk.PartitionStat) (misc.FilesystemPath, error) {
		return misc.FilesystemPath{Key: "fs-1", Mountpoint: "/mnt/backup"}, nil
	}
	getDiskUsage = func(path string) (*disk.UsageStat, error) {
		usageCalls++
		return &disk.UsageStat{Path: path, UsedPercent: 82, Free: 1024}, nil
	}

	repman := &ReplicationManager{
		Conf:            &config.Config{},
		DiskStatManager: misc.NewDiskStatManager(),
		Clusters: map[string]*clusterpkg.Cluster{
			"cluster-a": {
				Name: "cluster-a",
				Conf: &config.Config{
					BackupCheckFreeSpace:        true,
					BackupRestic:                true,
					BackupResticLocalRepository: "/var/backups/restic-a",
				},
			},
			"cluster-b": {
				Name: "cluster-b",
				Conf: &config.Config{
					BackupCheckFreeSpace:        true,
					BackupRestic:                true,
					BackupResticLocalRepository: "/var/backups/restic-b",
				},
			},
			"cluster-c": {
				Name: "cluster-c",
				Conf: &config.Config{
					BackupCheckFreeSpace:        false,
					BackupRestic:                true,
					BackupResticLocalRepository: "/var/backups/restic-c",
				},
			},
		},
	}

	repman.DiskStatManager.UpdateStat("stale", &disk.UsageStat{Path: "/stale", UsedPercent: 99})

	if err := repman.RefreshDiskStats(); err != nil {
		t.Fatalf("RefreshDiskStats returned error: %v", err)
	}

	if usageCalls != 1 {
		t.Fatalf("expected one disk usage call for a deduped filesystem, got %d", usageCalls)
	}
	if len(repman.DiskStatManager.Stats) != 1 {
		t.Fatalf("expected one snapshot stat entry, got %d", len(repman.DiskStatManager.Stats))
	}
	if _, ok := repman.DiskStatManager.Stats["stale"]; ok {
		t.Fatalf("expected stale disk stat to be removed during snapshot replacement")
	}

	stat, ok := repman.DiskStatManager.Stats["fs-1"]
	if !ok {
		t.Fatalf("expected deduped filesystem stat to be stored by filesystem key")
	}
	if stat.Path != "/mnt/backup" {
		t.Fatalf("expected alert label path to be mountpoint only, got %q", stat.Path)
	}
}

func TestRefreshDiskStatsDoesNotCreateBackupDirectories(t *testing.T) {
	oldResolve := resolveDiskFilesystemWithPartitions
	oldUsage := getDiskUsage
	defer func() {
		resolveDiskFilesystemWithPartitions = oldResolve
		getDiskUsage = oldUsage
	}()

	workingDir := t.TempDir()
	backupDir := filepath.Join(workingDir, config.ConstStreamingSubDir, "cluster-a", "db1_3306")
	resticDir := filepath.Join(workingDir, "restic")
	seenPaths := make(map[string]bool)

	resolveDiskFilesystemWithPartitions = func(path string, partitions []disk.PartitionStat) (misc.FilesystemPath, error) {
		seenPaths[filepath.Clean(path)] = true
		return misc.FilesystemPath{Key: filepath.Clean(path), Mountpoint: filepath.Clean(path)}, nil
	}
	getDiskUsage = func(path string) (*disk.UsageStat, error) {
		return &disk.UsageStat{Path: path, UsedPercent: 20, Free: 2048}, nil
	}

	cl := &clusterpkg.Cluster{
		Name: "cluster-a",
		Conf: &config.Config{
			WorkingDir:                  workingDir,
			BackupCheckFreeSpace:        true,
			BackupRestic:                true,
			BackupResticLocalRepository: resticDir,
		},
	}
	setClusterServers(cl, []*clusterpkg.ServerMonitor{{Host: "db1", Port: "3306", ClusterGroup: cl}})

	repman := &ReplicationManager{
		Conf:            &config.Config{},
		DiskStatManager: misc.NewDiskStatManager(),
		Clusters:        map[string]*clusterpkg.Cluster{"cluster-a": cl},
	}

	if err := repman.RefreshDiskStats(); err != nil {
		t.Fatalf("RefreshDiskStats returned error: %v", err)
	}
	if !seenPaths[backupDir] {
		t.Fatalf("expected refresh to inspect backup path %q", backupDir)
	}
	if !seenPaths[resticDir] {
		t.Fatalf("expected refresh to inspect restic path %q", resticDir)
	}
	if _, err := os.Stat(backupDir); !os.IsNotExist(err) {
		t.Fatalf("expected refresh not to create backup directory %q, stat err=%v", backupDir, err)
	}
}

func TestRefreshDiskStatsKeepsPreviousStatOnUsageFailure(t *testing.T) {
	oldResolve := resolveDiskFilesystemWithPartitions
	oldUsage := getDiskUsage
	defer func() {
		resolveDiskFilesystemWithPartitions = oldResolve
		getDiskUsage = oldUsage
	}()

	resolveDiskFilesystemWithPartitions = func(path string, partitions []disk.PartitionStat) (misc.FilesystemPath, error) {
		return misc.FilesystemPath{Key: "fs-1", Mountpoint: "/mnt/backup"}, nil
	}
	getDiskUsage = func(path string) (*disk.UsageStat, error) {
		return nil, os.ErrPermission
	}

	repman := &ReplicationManager{
		Conf:            &config.Config{},
		DiskStatManager: misc.NewDiskStatManager(),
		Clusters: map[string]*clusterpkg.Cluster{
			"cluster-a": {
				Name: "cluster-a",
				Conf: &config.Config{
					BackupCheckFreeSpace:        true,
					BackupRestic:                true,
					BackupResticLocalRepository: "/var/backups/restic-a",
				},
			},
		},
	}

	previous := misc.NewDiskUsageStat(&disk.UsageStat{Path: "/mnt/backup", UsedPercent: 91, Free: 512})
	repman.DiskStatManager.ReplaceStats(misc.DiskUsageStatMap{"fs-1": previous, "stale": misc.NewDiskUsageStat(&disk.UsageStat{Path: "/stale", UsedPercent: 10})})

	if err := repman.RefreshDiskStats(); err != nil {
		t.Fatalf("RefreshDiskStats returned error: %v", err)
	}

	if len(repman.DiskStatManager.Stats) != 1 {
		t.Fatalf("expected only tracked filesystem to remain after refresh, got %d entries", len(repman.DiskStatManager.Stats))
	}
	stat, ok := repman.DiskStatManager.Stats["fs-1"]
	if !ok {
		t.Fatalf("expected previous stat for fs-1 to be preserved")
	}
	if stat != previous {
		t.Fatalf("expected previous stat pointer to be retained on refresh failure")
	}
	if _, ok := repman.DiskStatManager.Stats["stale"]; ok {
		t.Fatalf("expected unrelated stale stat to still be removed")
	}
}

func setClusterServers(cl *clusterpkg.Cluster, servers []*clusterpkg.ServerMonitor) {
	clusterValue := reflect.ValueOf(cl).Elem()
	serversField := clusterValue.FieldByName("Servers")
	serverListValue := reflect.MakeSlice(serversField.Type(), 0, len(servers))
	for _, srv := range servers {
		serverListValue = reflect.Append(serverListValue, reflect.ValueOf(srv))
	}
	serversField.Set(serverListValue)
}
