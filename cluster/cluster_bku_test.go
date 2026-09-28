package cluster

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// One BKU is 20 GiB of disk and nothing else; the BKU is the LOCAL backup only, over-commit is
// the local usage above the plan, billing is max(plan, ceil(local)) at the BKU price.
func TestComputeBKU(t *testing.T) {
	unit := int64(20 * 1024 * 1024 * 1024)
	r := computeBKU(6, 10*unit, unit, 2.5, time.Now())
	if r.BkuLocal != 10 || r.OverCommit != 4 {
		t.Fatalf("local=%v over=%v, want 10 4", r.BkuLocal, r.OverCommit)
	}
	if r.BilledUnits != 10 || r.MonthlyCost != 25 {
		t.Fatalf("billed=%d cost=%v, want 10 units at 2.5 = 25", r.BilledUnits, r.MonthlyCost)
	}
	// Under the plan: the plan is billed, no over-commit.
	r = computeBKU(6, 2*unit, unit, 2.5, time.Now())
	if r.OverCommit != 0 || r.BilledUnits != 6 || r.MonthlyCost != 15 {
		t.Fatalf("under plan: over=%v billed=%d cost=%v, want 0 6 15", r.OverCommit, r.BilledUnits, r.MonthlyCost)
	}
	// Fractions round up to the next whole unit; no price = no cost.
	if r = computeBKU(1, unit+unit/2, unit, 0, time.Now()); r.BilledUnits != 2 || r.MonthlyCost != 0 {
		t.Fatalf("1.5 BKU must bill 2 at no cost, got %d %v", r.BilledUnits, r.MonthlyCost)
	}
	if r = computeBKU(6, unit, 0, 1, time.Now()); r.BkuLocal != 0 {
		t.Fatalf("a zero unit must not divide")
	}
}

// One BAU is the same 20 GiB; the remote archive has no plan: billed on usage, ceil(units),
// at the BAU price, and never priced when the cluster brought its own remote storage.
func TestComputeBAU(t *testing.T) {
	unit := int64(20 * 1024 * 1024 * 1024)
	a := computeBAU(3*unit+unit/4, unit, 1.5, time.Now())
	if a.Units != 3.25 || a.BilledUnits != 4 || !a.Priced || a.MonthlyCost != 6 {
		t.Fatalf("units=%v billed=%d priced=%v cost=%v, want 3.25 4 true 6", a.Units, a.BilledUnits, a.Priced, a.MonthlyCost)
	}
	if a = computeBAU(0, unit, 1.5, time.Now()); a.Units != 0 || a.BilledUnits != 0 || a.MonthlyCost != 0 {
		t.Fatalf("an empty archive bills nothing, got %d %v", a.BilledUnits, a.MonthlyCost)
	}
	if a = computeBAU(50*unit, unit, 0, time.Now()); a.Priced || a.BilledUnits != 50 || a.MonthlyCost != 0 {
		t.Fatalf("not priced: still tracked (50 units), no cost; got priced=%v %d %v", a.Priced, a.BilledUnits, a.MonthlyCost)
	}
	if a = computeBAU(unit, 0, 1, time.Now()); a.Units != 0 {
		t.Fatalf("a zero unit must not divide")
	}
	// The cluster price: the instance price, or 0 when the remote storage is the client's own.
	c := &Cluster{Conf: &config.Config{Cloud18MarketplaceBAUPrice: 2}}
	if c.bauUnitPrice() != 2 {
		t.Fatalf("partner storage must take the instance price")
	}
	c.Conf.Cloud18MarketplaceBAUClientStorage = true
	if c.bauUnitPrice() != 0 {
		t.Fatalf("client storage must never be priced")
	}
}

// The BKU is a plan unit like DBU and APU: per cluster, floor 1, its own flag, no resource follow.
func TestPlanUnitBKU(t *testing.T) {
	c := &Cluster{Name: "t", Conf: &config.Config{ProvDbBku: 6}}
	cur, floor, apply, err := c.planUnitSpec(PlanUnitBKU)
	if err != nil || cur != 6 || floor != 1 {
		t.Fatalf("spec = %d %d %v, want 6 1 nil", cur, floor, err)
	}
	apply(9)
	if c.Conf.ProvDbBku != 9 {
		t.Fatalf("apply must move prov-db-bku, got %d", c.Conf.ProvDbBku)
	}
	if c.planFlag(PlanUnitBKU) != "prov-db-bku" {
		t.Fatalf("plan flag = %q", c.planFlag(PlanUnitBKU))
	}
}

// The local measurement walks every local backup path of the cluster: each server's backup
// directory and, with restic on a local repository, the local archive too, so a backup kept
// after its push counts twice. A remote restic repository is never local; it feeds the
// remote reading only when restic is on and the backend is S3/SFTP.
func TestLocalBackupBytes_WalksBackupPaths(t *testing.T) {
	wd := t.TempDir()
	c := &Cluster{Name: "t", Conf: &config.Config{WorkingDir: wd, BackupRestic: true, BackupResticRepository: "s3:https://s3.example/backups"}}
	c.Servers = []*ServerMonitor{{Host: "db1", Port: "3306", ClusterGroup: c}}
	dir := filepath.Join(wd, config.ConstStreamingSubDir, "t", "db1_3306")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.sql.gz"), make([]byte, 1500), 0o644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(wd, config.ConstStreamingSubDir, "archive", "t")
	if err := os.MkdirAll(archive, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archive, "data"), make([]byte, 500), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := c.localBackupBytes(); got != 2000 {
		t.Fatalf("local bytes = %d, want 2000 (last backup 1500 + local archive 500)", got)
	}
	if !c.resticRepositoryIsRemote() {
		t.Fatalf("s3 repository must be remote")
	}
	c.Conf.BackupResticRepository = "/srv/restic"
	if c.resticRepositoryIsRemote() || c.remoteBackupBytes() != 0 {
		t.Fatalf("a local restic repository must not feed the remote reading")
	}
	if got := (&Cluster{Name: "none", Conf: &config.Config{WorkingDir: wd}}).localBackupBytes(); got != 0 {
		t.Fatalf("no servers, no paths: must read 0, got %d", got)
	}
}
