package cluster

import (
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/releases"
	"github.com/signal18/replication-manager/utils/version"
)

func readinessCluster(t *testing.T) (*Cluster, *ServerMonitor) {
	t.Helper()
	cl := &Cluster{Name: "t", Conf: &config.Config{ProvDbImg: "mariadb:11.8", ProvOrchestrator: config.ConstOrchestratorOpenSVC, BackupBinlogs: true, AutorejoinLogicalBackup: true}}
	m := &ServerMonitor{URL: "db1:3306", HaveBinlog: true, Variables: config.NewStringsMap(), ClusterGroup: cl}
	m.Variables.Set("BINLOG_EXPIRE_LOGS_SECONDS", "604800") // 7 days
	cl.master = m
	cl.Servers = []*ServerMonitor{m}
	return cl, m
}

func TestReseedReadiness(t *testing.T) {
	cl, m := readinessCluster(t)
	r := cl.GetReseedReadiness()
	if len(r.Issues) != 1 || !strings.Contains(r.Issues[0].Text, "no backup usable") || r.Retention != 7*24*time.Hour {
		t.Fatalf("no backup yet: %+v", r)
	}
	m.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Completed: true, EndTime: time.Now().Add(-8 * 24 * time.Hour)}
	if r := cl.GetReseedReadiness(); len(r.Issues) != 1 || !strings.Contains(r.Issues[0].Text, "older") && !strings.Contains(r.Issues[0].Text, "no backup usable") {
		t.Fatalf("a backup older than the retention is not usable: %+v", r)
	}
	m.LastBackupMeta.Logical = &backupmgr.BackupMetadata{EndTime: time.Now().Add(-2 * time.Hour)} // failed job, metadata written anyway
	if r := cl.GetReseedReadiness(); len(r.Issues) != 1 || r.LogicalFresh {
		t.Fatalf("an incomplete backup is no backup: %+v", r)
	}
	m.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Completed: true, EndTime: time.Now().Add(-2 * time.Hour)}
	if r := cl.GetReseedReadiness(); len(r.Issues) != 0 || !r.LogicalFresh {
		t.Fatalf("fresh logical backup: %+v", r)
	}
	cl.Conf.AutorejoinLogicalBackup = false
	if r := cl.GetReseedReadiness(); len(r.Issues) != 1 || !strings.Contains(r.Issues[0].Text, "reseed method") {
		t.Fatalf("direct dump is not a reseed method for a major move: %+v", r)
	}
	cl.Conf.AutorejoinLogicalBackup = true
	cl.Conf.BackupBinlogs = false
	if r := cl.GetReseedReadiness(); len(r.Issues) != 1 || !strings.Contains(r.Issues[0].Text, "binary logs are not monitored") {
		t.Fatalf("backup-binlogs off: %+v", r)
	}
	cl.Conf.BackupBinlogs = true
	m.HaveBinlog = false
	if r := cl.GetReseedReadiness(); len(r.Issues) != 1 || !strings.Contains(r.Issues[0].Text, "log_bin is off") {
		t.Fatalf("log_bin off: %+v", r)
	}
	m.HaveBinlog = true
	m.Variables.Set("BINLOG_EXPIRE_LOGS_SECONDS", "0")
	m.Variables.Set("EXPIRE_LOGS_DAYS", "0")
	m.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Completed: true, EndTime: time.Now().Add(-90 * 24 * time.Hour)}
	if r := cl.GetReseedReadiness(); len(r.Issues) != 0 {
		t.Fatalf("no purge: any completed backup is usable: %+v", r)
	}
}

func TestPlanRollingUpgradeGate(t *testing.T) {
	loadImageCatalog = func(c *Cluster) (*releases.Catalog, error) {
		return &releases.Catalog{Table: releases.Table{LTS: map[string][]string{"mariadb": {"11.8", "12.3"}}},
			Tags: map[string][]releases.Tag{"mariadb": {{Name: "12.3.3"}, {Name: "11.8.9"}}}, Source: "test"}, nil
	}
	defer func() { loadImageCatalog = nil }()
	cl, m := readinessCluster(t)
	cl.Conf.ProvDbUpgradeMajorReprov = true
	m.DBVersion = &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 9}
	if _, err := cl.PlanRollingUpgrade("next-major", ""); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("no backup: the major reprov is refused: %v", err)
	}
	m.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Completed: true, EndTime: time.Now().Add(-time.Hour)}
	p, err := cl.PlanRollingUpgrade("next-major", "")
	if err != nil || p.Mechanic != "reprov" {
		t.Fatalf("fresh backup: the major reprov is planned: err=%v plan=%+v", err, p)
	}
	// downward: a physical backup alone is not enough
	m.DBVersion = &version.Version{Flavor: "MariaDB", Major: 12, Minor: 3, Release: 3}
	cl.Conf.ProvDbImg = "mariadb:12.3"
	cl.Conf.AutorejoinLogicalBackup, cl.Conf.AutorejoinPhysicalBackup = false, true
	m.LastBackupMeta.Physical = &backupmgr.BackupMetadata{Completed: true, EndTime: time.Now().Add(-time.Hour)}
	if _, err := cl.PlanRollingUpgrade("version", "11.8"); err == nil || !strings.Contains(err.Error(), "logical backup") {
		t.Fatalf("downgrade with a physical backup only is refused: %v", err)
	}
	cl.Conf.AutorejoinLogicalBackup = true
	if p, err := cl.PlanRollingUpgrade("version", "11.8"); err != nil || p.Mechanic != "reprov" {
		t.Fatalf("downgrade with a fresh logical backup is planned: err=%v plan=%+v", err, p)
	}
}

// PostgreSQL has no binary logs: the readiness never raises WARN0224 for it, and listing
// binary logs is skipped (no ERR00014). The backup issues stay: no backup is no backup.
func TestReseedReadinessPostgreSQLHasNoBinaryLogWarning(t *testing.T) {
	cl, m := readinessCluster(t)
	m.HaveBinlog = false
	if r := cl.GetReseedReadiness(); len(r.Issues) == 0 || r.Issues[0].Code != "WARN0224" {
		t.Fatalf("MariaDB without log_bin raises WARN0224: %+v", r)
	}
	m.DBVersion, _ = version.NewVersion("PostgreSQL", 17, 11, 0)
	r := cl.GetReseedReadiness()
	for _, issue := range r.Issues {
		if issue.Code == "WARN0224" {
			t.Fatalf("WARN0224 must not be raised on PostgreSQL: %+v", r)
		}
	}
	if len(r.Issues) != 1 || !strings.Contains(r.Issues[0].Text, "no backup usable") {
		t.Fatalf("the missing backup is still reported: %+v", r)
	}
	if err := m.RefreshBinaryLogs(); err != nil {
		t.Fatalf("listing binary logs is a no-op on PostgreSQL: %v", err)
	}
}

// WARN0223 (no backup-based reseed method) is raised on PostgreSQL only where a node can
// be reseeded: the logical replication topology, not a single active-passive instance.
func TestReseedReadinessPostgreSQLReseedMethodByTopology(t *testing.T) {
	cl, m := readinessCluster(t)
	cl.Conf.AutorejoinLogicalBackup = false
	has := func() bool {
		for _, issue := range cl.GetReseedReadiness().Issues {
			if issue.Code == "WARN0223" {
				return true
			}
		}
		return false
	}
	if !has() {
		t.Fatal("MariaDB without a backup-based reseed method raises WARN0223")
	}
	m.DBVersion, _ = version.NewVersion("PostgreSQL", 17, 11, 0)
	cl.Topology = config.TopoActivePassive
	if has() {
		t.Fatal("a single active-passive PostgreSQL instance has nothing to reseed")
	}
	cl.Topology = config.TopoMasterSlavePgLog
	if !has() {
		t.Fatal("PostgreSQL logical replication raises WARN0223")
	}
}
