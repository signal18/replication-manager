// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// #2142: the binlog budget follows the disk: an explicit size wins; then (plan - data) / 2; then
// binlogs + half the free space; then 30G; never below two binlog files
func TestComputeBinlogBudget(t *testing.T) {
	const G = int64(1) << 30
	cases := []struct {
		name  string
		in    binlogBudgetInput
		bytes int64
		floor bool
	}{
		{"explicit", binlogBudgetInput{explicitGB: 7, planBytes: 100 * G, dataBytes: 10 * G}, 7 * G, false},
		{"plan minus data, halved", binlogBudgetInput{planBytes: 100 * G, dataBytes: 20 * G, freeBytes: -1, maxBinlogSize: G}, 40 * G, false},
		{"plan without the data size: measured disk", binlogBudgetInput{planBytes: 100 * G, dataBytes: -1, freeBytes: 30 * G, binlogBytes: 4 * G, maxBinlogSize: G}, 19 * G, false},
		{"measured disk: binlogs + free / 2", binlogBudgetInput{dataBytes: -1, freeBytes: 10 * G, binlogBytes: 3 * G, maxBinlogSize: G}, 8 * G, false},
		{"nothing known", binlogBudgetInput{dataBytes: -1, freeBytes: -1, maxBinlogSize: G}, 30 * G, false},
		{"data fills the plan: floor", binlogBudgetInput{planBytes: 10 * G, dataBytes: 9 * G, freeBytes: -1, maxBinlogSize: G}, 2 * G, true},
		{"rounded down to whole binlogs", binlogBudgetInput{dataBytes: -1, freeBytes: 5*G + 3, binlogBytes: G + 7, maxBinlogSize: G}, 3 * G, false},
		{"floor from max_binlog_size", binlogBudgetInput{dataBytes: -1, freeBytes: 0, maxBinlogSize: 256 << 20}, 512 << 20, true},
	}
	for _, c := range cases {
		b := computeBinlogBudget(c.in)
		if b.Bytes != c.bytes || b.AtFloor != c.floor || b.Source == "" {
			t.Errorf("%s: %d (floor %v, %q), want %d (floor %v)", c.name, b.Bytes, b.AtFloor, b.Source, c.bytes, c.floor)
		}
	}
}

// The oldest binlog the budget keeps: the newest ones whose sizes add up to it
func TestOldestBinlogInBudget(t *testing.T) {
	_, server := newTestRuntimeOnlyClusterServer(t, "binlog-budget", "db2", "3306")
	server.BinaryLogFiles = dbhelper.NewBinaryLogMetaMap()
	for _, f := range []string{"binlog.000010", "binlog.000011", "binlog.000012", "binlog.000013"} {
		server.BinaryLogFiles.Set(f, &dbhelper.BinaryLogMetadata{Filename: f, Size: 100})
	}
	for budget, want := range map[int64]string{400: "binlog.000010", 250: "binlog.000012", 100: "binlog.000013", 50: ""} {
		if got := server.oldestBinlogInBudget(budget); got != want {
			t.Errorf("budget %d: %q, want %q", budget, got, want)
		}
	}
	if got := server.binlogFilesBytes(); got != 400 {
		t.Errorf("binlogs %d, want 400", got)
	}
}

// The free space: the jobs script's recent report first, else the DISKS disk holding the datadir
func TestDataDiskFree(t *testing.T) {
	_, server := newTestRuntimeOnlyClusterServer(t, "binlog-free", "db2", "3306")
	server.Variables = config.NewStringsMap()
	server.Variables.Set("DATADIR", "/var/lib/mysql/")
	server.Disks = []dbhelper.Disk{{Path: "/", Available: 1000}, {Path: "/var/lib/mysql", Available: 50}, {Path: "/var/lib/mysqlx", Available: 7}}
	if free, from := server.dataDiskFree(); free != 50<<10 || from != "information_schema.DISKS" {
		t.Fatalf("DISKS: %d %q, want the datadir's disk", free, from)
	}
	server.SetDataDiskFree(123, time.Now())
	if free, from := server.dataDiskFree(); free != 123 || from != "jobs script" {
		t.Fatalf("report: %d %q", free, from)
	}
	server.SetDataDiskFree(123, time.Now().Add(-time.Hour))
	if free, _ := server.dataDiskFree(); free != 50<<10 {
		t.Fatalf("a stale report is used: %d", free)
	}
}
