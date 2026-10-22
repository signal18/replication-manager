// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

// The binlog budget (#2142): the total size a server's binlogs may take before they are purged
// (force-binlog-purge), max_binlog_total_size on MariaDB 11.4 and up, the master purge job's
// bound before. force-binlog-purge-total-size set (GB) is the budget; 0, the default, makes it
// follow the disk:
//   - the plan known (a provisioned cluster) and the database size known (the schema monitor):
//     half of what the data leaves of the plan's disk, (plan disk - data) / 2;
//   - the data disk measured (the jobs script's report, else information_schema.DISKS): the
//     binlogs there now plus half the free space;
//   - neither: 30 GB, the former default.
//
// Never below two binlog files (2 x max_binlog_size): below, every closed binlog would be purged
// at once, its copy and its replicas with it (WARN0264). The budget is also what a lagging
// replica keeps: past it, space wins (WARN0265).

const (
	binlogBudgetFallback = 30 << 30         // GB, when no disk is known
	dataDiskFreeMaxAge   = 15 * time.Minute // a jobs script report older than that is not used
	defaultMaxBinlogSize = 1 << 30
)

type binlogBudget struct {
	Bytes   int64
	Source  string // how it was set, for the log and the states
	AtFloor bool   // raised to two binlog files
}

type binlogBudgetInput struct {
	explicitGB    int   // force-binlog-purge-total-size, 0 automatic
	planBytes     int64 // the plan's disk, 0 unknown
	dataBytes     int64 // the database size, -1 unknown
	freeBytes     int64 // the datadir's free space, -1 unknown
	freeFrom      string
	binlogBytes   int64 // the binlogs there now
	maxBinlogSize int64
}

func computeBinlogBudget(in binlogBudgetInput) binlogBudget {
	if in.explicitGB > 0 {
		return binlogBudget{Bytes: int64(in.explicitGB) << 30, Source: fmt.Sprintf("force-binlog-purge-total-size %dG", in.explicitGB)}
	}
	var b binlogBudget
	switch {
	case in.planBytes > 0 && in.dataBytes >= 0:
		b = binlogBudget{Bytes: (in.planBytes - in.dataBytes) / 2, Source: fmt.Sprintf("(plan disk %s - data %s) / 2", humanBytes(in.planBytes), humanBytes(in.dataBytes))}
	case in.freeBytes >= 0:
		b = binlogBudget{Bytes: in.binlogBytes + in.freeBytes/2, Source: fmt.Sprintf("binlogs %s + free %s / 2 (%s)", humanBytes(in.binlogBytes), humanBytes(in.freeBytes), in.freeFrom)}
	default:
		b = binlogBudget{Bytes: binlogBudgetFallback, Source: "30G: no disk size known"}
	}
	size := in.maxBinlogSize
	if size <= 0 {
		size = defaultMaxBinlogSize
	}
	// whole binlog files: the budget does not move with every write, nor max_binlog_total_size
	b.Bytes -= b.Bytes % size
	if floor := 2 * size; b.Bytes < floor {
		b.Bytes, b.AtFloor = floor, true
		b.Source += fmt.Sprintf(", raised to two binlogs (%s)", humanBytes(floor))
	}
	return b
}

// BinlogLimit is what the dashboard shows of the primary's binlog size limit.
type BinlogLimit struct {
	Bytes   int64  `json:"bytes"`
	Source  string `json:"source"`  // how it is set
	Owner   string `json:"owner"`   // "plan" (dynamic config), "set" (force-binlog-purge-total-size), "automatic"
	AtFloor bool   `json:"atFloor"` // the automatic budget held at two binlog files
}

// binlogTotalSizeTarget is the binlog size limit: the plan's under the dynamic config
// (prov-db-binlog-total-size-pct of prov-db-disk-size), else the binlog budget. On MariaDB 11.4+
// it is max_binlog_total_size, set live whether force-binlog-purge is on or not under the dynamic
// config; before 11.4 the master purge job's bound.
func (server *ServerMonitor) binlogTotalSizeTarget() (int64, string) {
	cluster := server.ClusterGroup
	if cluster.Conf.ProvDBApplyDynamicConfig {
		if n, err := strconv.ParseInt(cluster.Configurator.GetConfigMaxBinlogTotalSize(), 10, 64); err == nil && n > 0 {
			return n, "the plan: prov-db-binlog-total-size-pct of prov-db-disk-size"
		}
	}
	b := server.BinlogBudget()
	return b.Bytes, b.Source
}

// SetDataDiskFree records the datadir's free space the jobs script reported.
func (server *ServerMonitor) SetDataDiskFree(bytes int64, at time.Time) {
	server.dataDiskFreeBytes.Store(bytes)
	server.dataDiskFreeAt.Store(at.Unix())
}

// BinlogBudget is the server's binlog budget now.
func (server *ServerMonitor) BinlogBudget() binlogBudget {
	in := binlogBudgetInput{explicitGB: server.ClusterGroup.Conf.ForceBinlogPurgeTotalSize, dataBytes: -1, freeBytes: -1, maxBinlogSize: server.maxBinlogSize()}
	if in.explicitGB <= 0 {
		in.planBytes = server.ClusterGroup.planDiskBytes()
		in.dataBytes = server.databaseBytes()
		in.freeBytes, in.freeFrom = server.dataDiskFree()
		in.binlogBytes = server.binlogFilesBytes()
	}
	return computeBinlogBudget(in)
}

// planDiskBytes is the plan's disk of a provisioned cluster (prov-db-disk-size), 0 otherwise.
func (cluster *Cluster) planDiskBytes() int64 {
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC, config.ConstOrchestratorKubernetes:
	default:
		return 0
	}
	gb, err := config.ParseUnitMeasurementToInt("G,bytes,required", cluster.Conf.ProvDisk, true)
	if err != nil || gb <= 0 {
		return 0
	}
	return int64(gb) << 30
}

// databaseBytes is the data and index size of the server's tables (the schema monitor's
// dictionary), -1 before it is read.
func (server *ServerMonitor) databaseBytes() int64 {
	if server.DictTables == nil {
		return -1
	}
	tables := server.DictTables.ToNewMap()
	if len(tables) == 0 {
		return -1
	}
	var total int64
	for _, t := range tables {
		if t != nil {
			total += t.DataLength + t.IndexLength
		}
	}
	return total
}

// dataDiskFree is the datadir's free space: the jobs script's recent report, else the DISKS
// plugin's disk holding the datadir; -1 unknown.
func (server *ServerMonitor) dataDiskFree() (int64, string) {
	if at := server.dataDiskFreeAt.Load(); at > 0 && time.Since(time.Unix(at, 0)) < dataDiskFreeMaxAge {
		return server.dataDiskFreeBytes.Load(), "jobs script"
	}
	datadir := ""
	if v, ok := server.Variables.CheckAndGet("DATADIR"); ok {
		datadir = filepath.Clean(v)
	}
	best, bestLen := int64(-1), -1
	for _, d := range server.Disks {
		p := filepath.Clean(d.Path)
		if datadir != "" && datadir != p && !strings.HasPrefix(datadir, strings.TrimSuffix(p, "/")+"/") {
			continue
		}
		if len(p) > bestLen {
			best, bestLen = int64(d.Available)<<10, len(p) // DISKS counts in KB
		}
	}
	if best < 0 {
		return -1, ""
	}
	return best, "information_schema.DISKS"
}

// binlogFilesBytes is the size of the server's binlogs now.
func (server *ServerMonitor) binlogFilesBytes() int64 {
	if server.BinaryLogFiles == nil {
		return 0
	}
	var total int64
	server.BinaryLogFiles.Callback(func(_ string, m *dbhelper.BinaryLogMetadata) bool {
		if m != nil {
			total += int64(m.Size)
		}
		return true
	})
	return total
}

func (server *ServerMonitor) maxBinlogSize() int64 {
	if v, ok := server.Variables.CheckAndGet("MAX_BINLOG_SIZE"); ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return defaultMaxBinlogSize
}

// oldestBinlogInBudget is the oldest binlog the budget keeps: the newest ones whose sizes add up
// to it, "" when unknown.
func (server *ServerMonitor) oldestBinlogInBudget(budget int64) string {
	if server.BinaryLogFiles == nil {
		return ""
	}
	var total int64
	oldest := ""
	for _, name := range server.BinaryLogFiles.GetKeysDesc() {
		m := server.BinaryLogFiles.Get(name)
		if m == nil {
			return ""
		}
		total += int64(m.Size)
		if total > budget {
			break
		}
		oldest = name
	}
	return oldest
}

// checkBinlogBudgetStates, every tick on the master with force-binlog-purge: WARN0264 while the
// budget is at its floor, WARN0265 for each disconnected replica (IO thread not running) whose
// next binlog the budget does not keep: MariaDB 11.4+ purges by size and holds a binlog only for a
// connected replica; before 11.4 the purge job stops at the replicas' oldest binlog.
func (cluster *Cluster) checkBinlogBudgetStates() {
	master := cluster.GetMaster()
	dynamic := cluster.Conf.ProvDBApplyDynamicConfig
	if master == nil || master.IsDown() || (!cluster.Conf.ForceBinlogPurge && !dynamic) {
		cluster.BinlogLimit = nil
		return
	}
	limit, source := master.binlogTotalSizeTarget()
	view := &BinlogLimit{Bytes: limit, Source: source, Owner: "plan"}
	if !dynamic {
		b := master.BinlogBudget()
		view.Owner, view.AtFloor = "automatic", b.AtFloor
		if cluster.Conf.ForceBinlogPurgeTotalSize > 0 {
			view.Owner = "set"
		}
		if b.AtFloor {
			cluster.SetState("WARN0264", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0264"], master.URL, humanBytes(b.Bytes), b.Source), ErrFrom: "PURGE", ServerUrl: master.URL})
		}
	}
	cluster.BinlogLimit = view
	if !master.IsMariaDB() || !master.DBVersion.GreaterEqual("11.4") {
		return
	}
	kept := master.oldestBinlogInBudget(limit)
	if kept == "" {
		return
	}
	for _, sl := range cluster.slaves {
		// a connected replica is safe: MariaDB never purges a binlog its dump thread reads
		if sl == nil || sl.SlaveStatus == nil || sl.SlaveStatus.SlaveIORunning.String == "Yes" {
			continue
		}
		if reads := sl.SlaveStatus.MasterLogFile.String; binlogOlderThan(reads, kept) {
			cluster.SetState("WARN0265", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0265"], sl.URL, reads, master.URL, humanBytes(limit), kept), ErrFrom: "PURGE", ServerUrl: sl.URL})
		}
	}
}
