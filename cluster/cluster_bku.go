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

// BKUReading is the per-cluster LOCAL backup storage picture against the BKU plan. One BKU is
// the DBU disk axis (20 GB by default, the ResourceManager Storage profile) and nothing else.
// Accounted PER CLUSTER: backups are of the dataset, not of each node. Local = everything the
// cluster keeps on its own storage: the last backup of each server in its backup directory
// (<working-dir>/backups/<cluster>/<host>_<port>) PLUS the restic archive when its repository
// is a local path (GetBackupDiskPaths lists both). A backup kept after its push to the archive
// counts twice, on purpose: it uses the disk twice.
//
// What leaves the cluster (the restic repository on S3/SFTP) is NOT a BKU: it is the remote
// archive, measured apart as BAU (BAUReading, cluster_bau.go), with its own price and no plan.
// Over-commit is the local usage above the plan: billed, never blocked (same rule as the DBU
// over-plan). Billing: max(plan, ceil(local)) units at cloud18-marketplace-bku-price
// Eur/BKU/month.
type BKUReading struct {
	Plan        int       `json:"plan"`        // prov-db-bku, per cluster
	LocalBytes  int64     `json:"localBytes"`  // real disk used by the local backup
	BkuLocal    float64   `json:"bkuLocal"`    // LocalBytes / (BKU disk)
	OverCommit  float64   `json:"overCommit"`  // max(0, BkuLocal - Plan)
	BilledUnits int       `json:"billedUnits"` // max(Plan, ceil(BkuLocal))
	UnitPrice   float64   `json:"unitPrice"`   // cloud18-marketplace-bku-price, Eur per BKU per month (0 = not priced)
	MonthlyCost float64   `json:"monthlyCost"` // BilledUnits × UnitPrice
	UnitBytes   int64     `json:"unitBytes"`   // bytes per BKU, from the Storage profile ratio
	UpdatedAt   time.Time `json:"updatedAt"`
}

// bkuUnitBytes is the disk quantity of one BKU (and of one BAU: the same 20 GB): the Storage
// profile ratio when a manager is wired, 20 GiB otherwise.
func (cluster *Cluster) bkuUnitBytes() int64 {
	gb := 20.0
	if cluster.resources != nil {
		if r := cluster.resources.Ratios(ProfileStorage); r.DiskGBPerUnit > 0 {
			gb = r.DiskGBPerUnit
		}
	}
	return int64(gb * 1024 * 1024 * 1024)
}

// localBackupBytes is the real disk used by this cluster's local backup: every path of
// GetBackupDiskPaths (each server's backup directory, plus the local restic repository when
// restic is on and its repository is a local path) walked on disk, never a catalog sum (a
// purge or an aborted job leaves files the catalog does not know).
func (cluster *Cluster) localBackupBytes() int64 {
	var total int64
	for _, dir := range cluster.GetBackupDiskPaths() {
		filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if info, ierr := d.Info(); ierr == nil {
				total += info.Size()
			}
			return nil
		})
	}
	return total
}

// resticRepositoryIsRemote reports whether the restic archive leaves the cluster: the
// repository is an S3 or SFTP backend. A local path is local disk, counted by the walk.
func (cluster *Cluster) resticRepositoryIsRemote() bool {
	repo := strings.TrimSpace(cluster.Conf.BackupResticRepository)
	return config.IsS3ResticRepository(repo) || config.IsSftpResticRepository(repo)
}

// remoteBackupBytes is the restic repository raw-data size (restic stats --mode raw-data)
// when the repository is remote (S3/SFTP): what is really held off the cluster. 0 when restic
// is off or its repository is a local path (then it is local disk, already walked).
func (cluster *Cluster) remoteBackupBytes() int64 {
	if cluster.ResticManager == nil || !cluster.Conf.BackupRestic || !cluster.resticRepositoryIsRemote() {
		return 0
	}
	return cluster.ResticManager.BackupStat.TotalSize
}

// computeBKU builds the local reading from the measured bytes and the plan.
func computeBKU(plan int, localBytes, unitBytes int64, unitPrice float64, now time.Time) *BKUReading {
	r := &BKUReading{Plan: plan, LocalBytes: localBytes, UnitBytes: unitBytes, UnitPrice: unitPrice, UpdatedAt: now}
	if unitBytes > 0 {
		r.BkuLocal = float64(localBytes) / float64(unitBytes)
	}
	r.OverCommit = math.Max(0, r.BkuLocal-float64(plan))
	r.BilledUnits = int(math.Max(float64(plan), math.Ceil(r.BkuLocal)))
	if r.BilledUnits < 0 {
		r.BilledUnits = 0
	}
	r.MonthlyCost = float64(r.BilledUnits) * unitPrice
	return r
}

// RefreshBackupUnits measures the cluster's backup storage, local (BKU) and remote archive
// (BAU), and asserts WARN0219 when the local backup disk is over the BKU plan. Runs every 30
// ticks (disk walk); the state is preserved on the intermediate ticks through pstates30. The
// remote archive has no plan, so no state: it is tracked and priced, never warned about.
func (cluster *Cluster) RefreshBackupUnits() {
	now := time.Now()
	unit := cluster.bkuUnitBytes()
	r := computeBKU(cluster.Conf.ProvDbBku, cluster.localBackupBytes(), unit, cluster.Conf.Cloud18MarketplaceBKUPrice, now)
	cluster.BackupUnits = r
	cluster.BackupArchiveUnits = computeBAU(cluster.remoteBackupBytes(), unit, cluster.bauUnitPrice(), now)
	if r.OverCommit > 0 {
		cluster.SetState("WARN0219", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0219"], cluster.Name, r.BkuLocal, r.Plan, humanBytes(r.LocalBytes)), ErrFrom: "BACKUP"})
	}
}

// CollectBackupUnitMetrics emits the BKU and BAU series every tick from the cached readings:
// the plan as resourcemanager.<CTOKEN>.plan_bku (like plan_dbu / plan_apu), the local backup
// as bku.<cluster>.{local,local_bytes,billed} and the remote archive as
// bau.<cluster>.{units,bytes,billed}, keyed by the RAW cluster name like dbu.<cluster>.* and
// apu.<cluster>.* so the Graphs page scopes them the same way. Over-commit is derived at
// query time (local vs plan), never emitted.
func (cluster *Cluster) CollectBackupUnitMetrics() {
	ts := time.Now().Unix()
	f := func(v float64, prec int) string { return strconv.FormatFloat(v, 'f', prec, 64) }
	if r := cluster.BackupUnits; r != nil {
		ctoken := strings.ToUpper(computeTokenReplacer.Replace(cluster.Name))
		cluster.AddMetrics([]graphite.Metric{
			graphite.NewMetric(fmt.Sprintf("resourcemanager.%s.plan_bku", ctoken), strconv.Itoa(r.Plan), ts),
			graphite.NewMetric(fmt.Sprintf("bku.%s.local", cluster.Name), f(r.BkuLocal, 4), ts),
			graphite.NewMetric(fmt.Sprintf("bku.%s.local_bytes", cluster.Name), strconv.FormatInt(r.LocalBytes, 10), ts),
			graphite.NewMetric(fmt.Sprintf("bku.%s.billed", cluster.Name), strconv.Itoa(r.BilledUnits), ts),
		})
	}
	if a := cluster.BackupArchiveUnits; a != nil {
		cluster.AddMetrics([]graphite.Metric{
			graphite.NewMetric(fmt.Sprintf("bau.%s.units", cluster.Name), f(a.Units, 4), ts),
			graphite.NewMetric(fmt.Sprintf("bau.%s.bytes", cluster.Name), strconv.FormatInt(a.Bytes, 10), ts),
			graphite.NewMetric(fmt.Sprintf("bau.%s.billed", cluster.Name), strconv.Itoa(a.BilledUnits), ts),
		})
	}
}
