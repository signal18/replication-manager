package cluster

import (
	"math"
	"testing"
	"time"
)

func TestBackupProgressBytesLevel(t *testing.T) {
	t0 := time.Date(2026, 10, 5, 9, 4, 19, 0, time.UTC)
	p := newBackupProgress("k", "db1", "logical", "mysqldump", t0)
	p.Level, p.PreviousSize = BackupProgressLevelBytes, 1000
	p.mu.Lock()
	p.BytesDone = 250
	p.refreshLocked(t0.Add(10 * time.Second))
	p.mu.Unlock()
	if math.Abs(p.Percent-25) > 1e-9 || p.RateBytesPerS != 25 || p.EtaSeconds != 30 {
		t.Fatalf("bytes level: %+v", p)
	}
	// never 100 before the job says done, even past the previous size
	p.mu.Lock()
	p.BytesDone = 5000
	p.refreshLocked(t0.Add(20 * time.Second))
	p.mu.Unlock()
	if p.Percent != backupProgressMaxPercent || p.EtaSeconds != -1 {
		t.Fatalf("capped at %d%%, ETA unknown: %+v", backupProgressMaxPercent, p)
	}
	// first run of a kind: no previous size -> no percentage, rate only
	q := newBackupProgress("k2", "db1", "logical", "mysqldump", t0)
	q.AddBytes(100)
	if q.Percent != -1 || q.BytesDone != 100 {
		t.Fatalf("no previous size -> no percentage: %+v", q)
	}
}

func TestBackupProgressSchemaLevel(t *testing.T) {
	t0 := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	p := newBackupProgress("k", "db1", "logical", "mysqldump", t0)
	p.Level, p.PreviousSize = BackupProgressLevelBytes, 10
	p.tableSizes, p.TablesBytesTotal, p.TablesTotal = map[string]int64{"tpcc.customer": 600, "tpcc.orders": 300, "tpcc.item": 100}, 1000, 3
	p.TableBoundary("", "customer", t0)
	if p.Level != BackupProgressLevelSchema || p.CurrentTable != "tpcc.customer" || p.TablesDone != 0 {
		t.Fatalf("first boundary opens the table: %+v", p)
	}
	p.TableBoundary("", "orders", t0.Add(60*time.Second))
	if p.TablesDone != 1 || p.TablesBytesDone != 600 || math.Abs(p.Percent-60) > 1e-9 || p.CurrentTable != "tpcc.orders" {
		t.Fatalf("after customer: %+v", p)
	}
	if len(p.TableRates) != 1 || p.TableRates[0].Table != "tpcc.customer" || p.TableRates[0].BytesPerS != 10 {
		t.Fatalf("customer rate: %+v", p.TableRates)
	}
	// 600 B in 60 s -> 10 B/s; 400 left -> ETA 40 s
	if p.EtaSeconds != 40 {
		t.Fatalf("eta = %d, want 40", p.EtaSeconds)
	}
	// the bytes side no longer owns the percentage once the schema level is armed
	p.AddBytes(100000)
	if math.Abs(p.Percent-60) > 1e-9 {
		t.Fatalf("schema level owns the percentage, got %v", p.Percent)
	}
	// a schema-qualified table and an unknown table are both accepted
	p.TableBoundary("tpcc", "item", t0.Add(120*time.Second))
	if p.CurrentTable != "tpcc.item" || p.TablesDone != 2 {
		t.Fatalf("qualified name: %+v", p)
	}
	p.TableBoundary("", "unknown_tbl", t0.Add(130*time.Second))
	if p.CurrentTable != "unknown_tbl" || p.TablesDone != 3 || p.Percent != backupProgressMaxPercent {
		t.Fatalf("unknown table: %+v", p)
	}
	p.FinishTables(t0.Add(131 * time.Second))
	if p.CurrentTable != "" || p.TablesDone != 4 {
		t.Fatalf("finish closes the last table: %+v", p)
	}
}

func TestBackupProgressTableRateRingIsBounded(t *testing.T) {
	now := time.Now()
	p := newBackupProgress("k", "db1", "logical", "mysqldump", now)
	p.tableSizes = map[string]int64{}
	for i := 0; i < backupProgressTableRateRing+20; i++ {
		p.TableBoundary("s", "t", now.Add(time.Duration(i)*time.Second))
	}
	if len(p.TableRates) != backupProgressTableRateRing {
		t.Fatalf("ring must stay at %d, got %d", backupProgressTableRateRing, len(p.TableRates))
	}
}

func TestBackupProgressLifecycleOnCluster(t *testing.T) {
	c := &Cluster{Name: "c"}
	s := &ServerMonitor{URL: "db1:3306", Name: "db1"}
	p := c.StartBackupProgress(s, "binlog", "binlog.000022")
	if p == nil || c.backupProgressFor(s, "binlog") != p || len(c.snapshotBackupProgress()) != 1 {
		t.Fatal("a started backup is visible")
	}
	if v := c.snapshotBackupProgress()[0]; v.Kind != "binlog" || v.Level != BackupProgressLevelRunning || v.Percent != -1 {
		t.Fatalf("running level: %+v", v)
	}
	c.EndBackupProgress(p)
	if c.backupProgressFor(s, "binlog") != nil || len(c.snapshotBackupProgress()) != 0 {
		t.Fatal("an ended backup is gone")
	}
	c.EndBackupProgress(nil) // tolerated
}

func TestBackupProgressObserveDumpLine(t *testing.T) {
	p := newBackupProgress("k", "db1", "logical", "mysqldump", time.Now())
	p.tableSizes = map[string]int64{"tpcc.customer": 600, "tpcc.item": 100}
	p.ObserveDumpLine("-- Sending SELECT query...") // not a boundary
	if p.CurrentTable != "" {
		t.Fatal("only table boundaries count")
	}
	p.ObserveDumpLine("-- Retrieving table structure for table customer...")
	if p.CurrentTable != "tpcc.customer" {
		t.Fatalf("bare MariaDB form: %q", p.CurrentTable)
	}
	p.ObserveDumpLine("-- Retrieving table structure for table `tpcc`.`item`...")
	if p.CurrentTable != "tpcc.item" || p.TablesDone != 1 {
		t.Fatalf("qualified form: %+v", p)
	}
}
