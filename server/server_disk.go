package server

import (
	"github.com/shirou/gopsutil/disk"
	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
)

var (
	resolveDiskFilesystemWithPartitions = misc.ResolveFilesystemWithPartitions
	getDiskPartitions                   = disk.Partitions
	getDiskUsage                        = disk.Usage
)

func (repman *ReplicationManager) RefreshDiskStats() error {
	partitions, err := getDiskPartitions(true)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModStats, config.LvlWarn, "Disk partitions error: %s", err)
		partitions = nil
	}

	filesystemMounts := make(map[string]string)
	for _, cl := range repman.snapshotClustersForDiskStats() {
		if cl == nil || cl.Conf == nil || !cl.Conf.BackupCheckFreeSpace {
			continue
		}

		for _, backupPath := range backupPathsForCluster(cl) {
			fsPath, err := resolveDiskFilesystemWithPartitions(backupPath, partitions)
			if err != nil {
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModStats, config.LvlWarn, "Disk filesystem resolution error for %s: %s", backupPath, err)
				continue
			}

			filesystemMounts[fsPath.Key] = fsPath.Mountpoint
		}
	}

	snapshot := make(misc.DiskUsageStatMap, len(filesystemMounts))
	for key, mountpoint := range filesystemMounts {
		s, err := getDiskUsage(mountpoint)
		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModStats, config.LvlWarn, "Disk usage error: %s", err)
			if existing, ok := repman.DiskStatManager.GetStat(key); ok && existing != nil {
				snapshot[key] = existing
			}
			continue
		}
		if s == nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModStats, config.LvlWarn, "Disk usage error: usage stat is nil for %s", mountpoint)
			if existing, ok := repman.DiskStatManager.GetStat(key); ok && existing != nil {
				snapshot[key] = existing
			}
			continue
		}

		s.Path = mountpoint
		snapshot[key] = misc.NewDiskUsageStat(s)
	}
	repman.DiskStatManager.ReplaceStats(snapshot)

	return nil
}

func (repman *ReplicationManager) snapshotClustersForDiskStats() []*clusterpkg.Cluster {
	repman.Lock()
	defer repman.Unlock()

	clusters := make([]*clusterpkg.Cluster, 0, len(repman.Clusters))
	for _, cl := range repman.Clusters {
		clusters = append(clusters, cl)
	}

	return clusters
}

func backupPathsForCluster(cl *clusterpkg.Cluster) []string {
	paths := make([]string, 0, len(cl.Servers)+1)
	for _, srv := range cl.Servers {
		if srv == nil || cl == nil || cl.Conf == nil {
			continue
		}
		if backupPath := srv.GetMyBackupDirectoryPath(); backupPath != "" {
			paths = append(paths, backupPath)
		}
	}

	if cl.Conf.BackupRestic {
		paths = append(paths, cl.GetResticLocalDir())
	}

	return paths
}
