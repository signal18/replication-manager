// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
package cluster

import (
	"fmt"
	"io/fs"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/graphite"
	"github.com/signal18/replication-manager/utils/state"
)

// BKUReading is the per-cluster backup storage picture against the BKU plan. One BKU is the
// DBU disk axis (20 GB by default, the ResourceManager Storage profile) and nothing else.
// Accounted PER CLUSTER: backups are of the dataset, not of each node. Two kinds:
//   - local: the backup cache + archive the cluster keeps on its own storage (the repman
//     streaming directory <working-dir>/backups/<cluster>, measured on disk);
//   - remote: what is archived off the cluster, on S3/SFTP through restic (the repository's
//     raw-data size, restic stats, refreshed by ResticFetchRepo).
//
// Over-commit is the local BKU above the plan: billed, never blocked (same rule as the DBU
// over-plan). Remote BKU is billed on what is archived, at its own price.
type BKUReading struct {
	Plan        int       `json:"plan"`        // prov-db-bku, per cluster
	LocalBytes  int64     `json:"localBytes"`  // real disk used by the local backup cache + archive
	RemoteBytes int64     `json:"remoteBytes"` // restic repository raw-data size
	BkuLocal    float64   `json:"bkuLocal"`    // LocalBytes / (BKU disk)
	BkuRemote   float64   `json:"bkuRemote"`   // RemoteBytes / (BKU disk)
	OverCommit  float64   `json:"overCommit"`  // max(0, BkuLocal - Plan)
	UnitBytes   int64     `json:"unitBytes"`   // bytes per BKU, from the Storage profile ratio
	UpdatedAt   time.Time `json:"updatedAt"`
}

// bkuUnitBytes is the disk quantity of one BKU: the Storage profile ratio when a manager is
// wired, 20 GiB otherwise.
func (cluster *Cluster) bkuUnitBytes() int64 {
	gb := 20.0
	if cluster.resources != nil {
		if r := cluster.resources.Ratios(ProfileStorage); r.DiskGBPerUnit > 0 {
			gb = r.DiskGBPerUnit
		}
	}
	return int64(gb * 1024 * 1024 * 1024)
}

// localBackupBytes is the real disk used by this cluster's local backup cache + archive: the
// streaming directory walked on disk (a purge or an aborted job leaves files the catalog does
// not know), never a catalog sum.
func (cluster *Cluster) localBackupBytes() int64 {
	dir := filepath.Join(cluster.Conf.WorkingDir, config.ConstStreamingSubDir, cluster.Name)
	var total int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// remoteBackupBytes is the restic repository raw-data size (restic stats --mode raw-data),
// whatever the backend (S3, SFTP): what is really held off the cluster.
func (cluster *Cluster) remoteBackupBytes() int64 {
	if cluster.ResticManager == nil {
		return 0
	}
	return cluster.ResticManager.BackupStat.TotalSize
}

// computeBKU builds the reading from the measured bytes and the plan.
func computeBKU(plan int, localBytes, remoteBytes, unitBytes int64, now time.Time) *BKUReading {
	r := &BKUReading{Plan: plan, LocalBytes: localBytes, RemoteBytes: remoteBytes, UnitBytes: unitBytes, UpdatedAt: now}
	if unitBytes > 0 {
		r.BkuLocal = float64(localBytes) / float64(unitBytes)
		r.BkuRemote = float64(remoteBytes) / float64(unitBytes)
	}
	r.OverCommit = math.Max(0, r.BkuLocal-float64(plan))
	return r
}

// RefreshBackupUnits measures the cluster's backup storage and asserts WARN0219 when the local
// backup disk is over the BKU plan. Runs every 30 ticks (disk walk); the state is preserved on
// the intermediate ticks through pstates30.
func (cluster *Cluster) RefreshBackupUnits() {
	r := computeBKU(cluster.Conf.ProvDbBku, cluster.localBackupBytes(), cluster.remoteBackupBytes(), cluster.bkuUnitBytes(), time.Now())
	cluster.BackupUnits = r
	if r.OverCommit > 0 {
		cluster.SetState("WARN0219", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0219"], cluster.Name, r.BkuLocal, r.Plan, humanBytes(r.LocalBytes), humanBytes(r.RemoteBytes)), ErrFrom: "BACKUP"})
	}
}

// CollectBackupUnitMetrics emits the BKU series every tick from the cached reading: the plan
// as resourcemanager.<CTOKEN>.plan_bku (like plan_dbu / plan_apu), the consumed values as
// bku.<cluster>.{local,remote} (in BKU) and bku.<cluster>.{local_bytes,remote_bytes}, keyed by
// the RAW cluster name like dbu.<cluster>.* and apu.<cluster>.* so the Graphs page scopes them
// the same way. Over-commit is derived at query time (local vs plan), never emitted.
func (cluster *Cluster) CollectBackupUnitMetrics() {
	r := cluster.BackupUnits
	if r == nil {
		return
	}
	ts := time.Now().Unix()
	ctoken := strings.ToUpper(computeTokenReplacer.Replace(cluster.Name))
	f := func(v float64, prec int) string { return strconv.FormatFloat(v, 'f', prec, 64) }
	cluster.AddMetrics([]graphite.Metric{
		graphite.NewMetric(fmt.Sprintf("resourcemanager.%s.plan_bku", ctoken), strconv.Itoa(r.Plan), ts),
		graphite.NewMetric(fmt.Sprintf("bku.%s.local", cluster.Name), f(r.BkuLocal, 4), ts),
		graphite.NewMetric(fmt.Sprintf("bku.%s.remote", cluster.Name), f(r.BkuRemote, 4), ts),
		graphite.NewMetric(fmt.Sprintf("bku.%s.local_bytes", cluster.Name), strconv.FormatInt(r.LocalBytes, 10), ts),
		graphite.NewMetric(fmt.Sprintf("bku.%s.remote_bytes", cluster.Name), strconv.FormatInt(r.RemoteBytes, 10), ts),
	})
}
