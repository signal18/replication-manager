// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shirou/gopsutil/disk"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

// newDiskAlertTestCluster builds a minimal cluster with one server and a
// fresh DiskStatManager, suitable for exercising CheckDisksUsage without a
// live database connection.
func newDiskAlertTestCluster(t *testing.T, numServers int) *Cluster {
	t.Helper()
	cluster := &Cluster{
		Name: "disk-alert-cluster",
		Conf: &config.Config{
			WorkingDir:             t.TempDir(),
			BackupCheckFreeSpace:   true,
			BackupDiskTresholdWarn: 70,
			BackupDiskTresholdCrit: 90,
		},
		StateMachine:    &state.StateMachine{},
		DiskStatManager: misc.NewDiskStatManager(),
	}
	cluster.StateMachine.Init()

	for i := 0; i < numServers; i++ {
		server := &ServerMonitor{
			Host:         "127.0.0.1",
			Port:         hostPortFor(i),
			ClusterGroup: cluster,
		}
		cluster.Servers = append(cluster.Servers, server)
	}

	return cluster
}

func hostPortFor(i int) string {
	return []string{"3306", "3307", "3308"}[i]
}

// registerDiskStat injects a disk usage stat directly into the cluster's
// DiskStatManager keyed by the cleaned path, bypassing any real syscall. The
// stat's identity (Path) is set to the same path, as real disk.Usage() calls
// do (see RefreshDiskStats/UpdateDiskStat).
func registerDiskStat(cluster *Cluster, path string, total, used uint64) {
	registerDiskStatWithIdentity(cluster, path, path, total, used)
}

// registerDiskStatWithIdentity is like registerDiskStat but lets the stat's
// identity (Path) differ from the map key, so tests can simulate two
// distinct filesystems (different identity) that happen to report identical
// usage numbers.
func registerDiskStatWithIdentity(cluster *Cluster, key, identity string, total, used uint64) {
	free := total - used
	usedPercent := float64(0)
	if total > 0 {
		usedPercent = float64(used) / float64(total) * 100
	}
	cluster.DiskStatManager.UpdateStat(filepath.Clean(key), &disk.UsageStat{
		Path:        identity,
		Total:       total,
		Free:        free,
		Used:        used,
		UsedPercent: usedPercent,
	})
}

func openStateDescs(cluster *Cluster, key string) []string {
	var descs []string
	for _, s := range cluster.StateMachine.GetOpenStates() {
		if s.ErrKey == key {
			descs = append(descs, s.ErrDesc)
		}
	}
	return descs
}

// TestCheckDisksUsageIgnoresUnrelatedMounts verifies that unrelated
// system/container mounts tracked in the shared DiskStatManager (bind mounts
// like /etc/hostname, /etc/hosts, /etc/resolv.conf, /usr/share/zoneinfo/...,
// or the repo root) never appear in the backup disk space alert, even when
// they are themselves over threshold. Only the cluster's actual backup path
// should be reported.
func TestCheckDisksUsageIgnoresUnrelatedMounts(t *testing.T) {
	cluster := newDiskAlertTestCluster(t, 1)
	backupPath := cluster.Servers[0].GetMyBackupDirectoryPath()

	// The real backup path is over the critical threshold.
	registerDiskStat(cluster, backupPath, 100, 95)

	// Unrelated mounts/bind-mounts, also "over threshold", must be ignored.
	registerDiskStat(cluster, "/etc/hostname", 100, 99)
	registerDiskStat(cluster, "/etc/hosts", 100, 99)
	registerDiskStat(cluster, "/etc/resolv.conf", 100, 99)
	registerDiskStat(cluster, "/usr/share/zoneinfo/Etc/UTC", 100, 99)
	registerDiskStat(cluster, "/go/src/github.com/signal18/replication-manager", 100, 99)
	registerDiskStat(cluster, "/", 100, 99)

	cluster.CheckDisksUsage()

	descs := openStateDescs(cluster, "WARN0140")
	if len(descs) != 1 {
		t.Fatalf("expected exactly one WARN0140 state, got %d: %v", len(descs), descs)
	}

	desc := descs[0]
	if !strings.Contains(desc, backupPath) {
		t.Errorf("expected alert to mention backup path %q, got: %s", backupPath, desc)
	}

	for _, bogus := range []string{"/etc/hostname", "/etc/hosts", "/etc/resolv.conf", "/usr/share/zoneinfo", "/go/src/github.com"} {
		if strings.Contains(desc, bogus) {
			t.Errorf("alert should not mention unrelated mount %q, got: %s", bogus, desc)
		}
	}

	if warnDescs := openStateDescs(cluster, "WARN0139"); len(warnDescs) != 0 {
		t.Errorf("did not expect WARN0139 alongside WARN0140, got: %v", warnDescs)
	}
}

// TestCheckDisksUsageDedupsSameMount verifies that when multiple backup
// directories (e.g. from different servers) resolve to the same underlying
// mount, the over-threshold alert lists that filesystem once rather than
// once per directory. The shared mount is registered at the common parent
// of both server backup directories, so GetStatByClosestMount resolves both
// to the exact same stat record (same identity), mirroring how a real
// mounted volume covering both directories would be tracked.
func TestCheckDisksUsageDedupsSameMount(t *testing.T) {
	cluster := newDiskAlertTestCluster(t, 2)

	sharedMount := filepath.Join(cluster.Conf.WorkingDir, config.ConstStreamingSubDir, cluster.Name)
	registerDiskStat(cluster, sharedMount, 100, 95)

	cluster.CheckDisksUsage()

	descs := openStateDescs(cluster, "WARN0140")
	if len(descs) != 1 {
		t.Fatalf("expected exactly one WARN0140 state, got %d: %v", len(descs), descs)
	}

	// Both servers' directories resolved to the same stat identity, so the
	// resulting statlist should only mention the mount once.
	occurrences := strings.Count(descs[0], "95")
	if occurrences != 1 {
		t.Errorf("expected the over-threshold mount to be reported once, got %d occurrences in: %s", occurrences, descs[0])
	}
}

// TestCheckDisksUsageReportsDistinctMountsWithSameUsage verifies that two
// genuinely different filesystems that happen to report identical
// total/used numbers (e.g. two 1TB volumes both 95% full) are NOT collapsed
// into a single alert entry. Dedup must be based on filesystem identity, not
// on the usage numbers themselves.
func TestCheckDisksUsageReportsDistinctMountsWithSameUsage(t *testing.T) {
	cluster := newDiskAlertTestCluster(t, 2)

	for i, srv := range cluster.Servers {
		identity := fmt.Sprintf("/mnt/backup-vol-%d", i)
		registerDiskStatWithIdentity(cluster, srv.GetMyBackupDirectoryPath(), identity, 100, 95)
	}

	cluster.CheckDisksUsage()

	descs := openStateDescs(cluster, "WARN0140")
	if len(descs) != 1 {
		t.Fatalf("expected exactly one WARN0140 state, got %d: %v", len(descs), descs)
	}

	for _, srv := range cluster.Servers {
		backupPath := srv.GetMyBackupDirectoryPath()
		if !strings.Contains(descs[0], backupPath) {
			t.Errorf("expected alert to mention distinct mount's backup path %q, got: %s", backupPath, descs[0])
		}
	}
}

// TestGetBackupDiskPathsResolvesRemoteResticToLocalStaging verifies that a
// remote restic repository spec (s3:, sftp:) never leaks into the set of
// paths checked for local disk usage; GetResticEffectiveLocalRepoPath always
// resolves remote specs to the local staging directory instead.
func TestGetBackupDiskPathsResolvesRemoteResticToLocalStaging(t *testing.T) {
	cluster := newDiskAlertTestCluster(t, 1)
	cluster.Conf.BackupRestic = true
	cluster.Conf.BackupResticLocalRepository = "s3:https://minio.example.com/bucket"

	paths := cluster.GetBackupDiskPaths()

	for _, p := range paths {
		if strings.HasPrefix(p, "s3:") || strings.HasPrefix(p, "sftp:") {
			t.Fatalf("remote restic repo spec leaked into local disk paths: %v", paths)
		}
	}

	localStagingDir := cluster.GetResticEffectiveLocalRepoPath()
	found := false
	for _, p := range paths {
		if p == localStagingDir {
			found = true
		}
	}
	if !found {
		t.Errorf("expected local restic staging dir %q in backup disk paths, got: %v", localStagingDir, paths)
	}
}

// TestGetBackupDiskPathsSkipsResticWhenDisabled verifies that the restic
// staging directory is only included when restic backups are enabled, since
// it is otherwise unused local storage.
func TestGetBackupDiskPathsSkipsResticWhenDisabled(t *testing.T) {
	cluster := newDiskAlertTestCluster(t, 1)
	cluster.Conf.BackupRestic = false

	paths := cluster.GetBackupDiskPaths()

	localStagingDir := cluster.GetResticEffectiveLocalRepoPath()
	for _, p := range paths {
		if p == localStagingDir {
			t.Errorf("did not expect restic staging dir %q when backup-restic is disabled, got: %v", localStagingDir, paths)
		}
	}
}

// TestGetBackupDiskPathsUsesResticAppendClusterPath verifies that when
// backup-restic-repo-append-cluster is enabled, the disk path checked is the
// actual per-cluster repo directory restic writes to (localRepoPath/<cluster>),
// not its parent. GetResticLocalDir alone does not apply this join and would
// report the parent directory instead of the real repo mount.
func TestGetBackupDiskPathsUsesResticAppendClusterPath(t *testing.T) {
	cluster := newDiskAlertTestCluster(t, 1)
	cluster.Conf.BackupRestic = true
	cluster.Conf.BackupResticRepoAppendCluster = true
	cluster.Conf.BackupResticLocalRepository = "/mnt/restic-repo"

	expected := filepath.Join("/mnt/restic-repo", cluster.Name)

	effective := cluster.GetResticEffectiveLocalRepoPath()
	if effective != expected {
		t.Fatalf("expected effective restic repo path %q, got %q", expected, effective)
	}

	paths := cluster.GetBackupDiskPaths()
	found := false
	for _, p := range paths {
		if p == expected {
			found = true
		}
		if p == "/mnt/restic-repo" {
			t.Errorf("backup disk paths should not contain the parent repo dir %q instead of the cluster-specific path", p)
		}
	}
	if !found {
		t.Errorf("expected cluster-specific restic repo path %q in backup disk paths, got: %v", expected, paths)
	}
}
