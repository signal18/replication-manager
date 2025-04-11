package server

import (
	"github.com/shirou/gopsutil/disk"
	"github.com/signal18/replication-manager/config"
)

func (repman *ReplicationManager) RefreshDiskStats() error {
	parts, err := disk.Partitions(true)
	if err != nil {
		return err
	}

	for _, p := range parts {
		device := p.Mountpoint
		s, err := disk.Usage(device)
		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModStats, config.LvlWarn, "Disk usage error: %s", err)
			continue
		}

		repman.DiskStatManager.UpdateStat(device, s)
	}

	return nil
}
