// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/signal18/replication-manager/config"
)

// SysbenchLogEntry records the context and results of a single sysbench run.
type SysbenchLogEntry struct {
	StartedAt    time.Time                    `json:"startedAt"`
	EndedAt      time.Time                    `json:"endedAt"`
	TestType     string                       `json:"testType"`     // oltp_read_write, oltp_read_only, oltp_update_index, oltp_update_non_index, tpcc
	TestMode     string                       `json:"testMode"`     // complex, simple, nontrx (legacy oltp)
	Threads      int                          `json:"threads"`
	Duration     int                          `json:"duration"`     // seconds
	TableSize    int                          `json:"tableSize"`
	Tables       int                          `json:"tables"`       // sysbench-tables
	Scale        int                          `json:"scale"`        // sysbench-scale (tpcc warehouses)
	DBFlavor     string                       `json:"dbFlavor"`     // MariaDB, MySQL, Percona, PostgreSQL
	DBVersion    string                       `json:"dbVersion"`
	ProxyType    string                       `json:"proxyType"`    // proxysql, haproxy, maxscale, myproxy
	ProxyVersion string                       `json:"proxyVersion"`
	Replicas     int                          `json:"replicas"`
	Cores        string                       `json:"cores"`        // prov-db-cpu-cores (docker cap)
	MemoryMB     string                       `json:"memoryMB"`     // prov-db-memory in MB (docker cap)
	DiskGB       string                       `json:"diskGB"`       // prov-db-disk-size in GB (docker cap)
	DBU          float64                      `json:"dbu"`          // database units: max(cores/1, mem/4096, disk/40)
	ClusterDBU   float64                      `json:"clusterDbu"`   // DBU × (replicas + 1)
	ConfigTags   string                       `json:"configTags"`   // prov-db-tags at run time
	ServicePlan  string                       `json:"servicePlan"`
	Results      []SysBenchTpcResultPerMinute `json:"results"`
	Records      []SysbenchRecord             `json:"records,omitempty"`
	AvgTPS       float64                      `json:"avgTps"`
	AvgLatency   float64                      `json:"avgLatency"`
	TotalErrors  int                          `json:"totalErrors"`
	TPSPerDBU    float64                      `json:"tpsPerDbu"`    // avgTps / clusterDBU — performance efficiency
}

// SysbenchLog holds the full history of sysbench runs for a cluster.
type SysbenchLog struct {
	Entries []SysbenchLogEntry `json:"entries"`
}

// SaveSysbenchLog writes the sysbench history to disk.
func (cluster *Cluster) SaveSysbenchLog() error {
	path := filepath.Join(cluster.WorkingDir, "sysbench.json")
	os.MkdirAll(filepath.Dir(path), 0755)

	data, err := json.MarshalIndent(cluster.SysbenchHistory, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// LoadSysbenchLog reads the sysbench history from disk.
func (cluster *Cluster) LoadSysbenchLog() {
	path := filepath.Join(cluster.WorkingDir, "sysbench.json")
	bytes, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var log SysbenchLog
	if err := json.Unmarshal(bytes, &log); err != nil {
		return
	}
	cluster.SysbenchHistory = log
}

// LogSysbenchRun captures the context and results of a sysbench run and appends it to history.
func (cluster *Cluster) LogSysbenchRun(testType string, testMode string, threads int, tableSize int, duration int, startedAt time.Time, records []SysbenchRecord) {
	dbNodes := len(cluster.Servers) // all database nodes (master + replicas)
	entry := SysbenchLogEntry{
		StartedAt:   startedAt,
		EndedAt:     time.Now(),
		TestType:    testType,
		TestMode:    testMode,
		Threads:     threads,
		Duration:    duration,
		TableSize:   tableSize,
		Tables:      cluster.Conf.SysbenchTables,
		Scale:       cluster.Conf.SysbenchScale,
		ConfigTags:  cluster.Conf.ProvTags,
		ServicePlan: cluster.Conf.ProvServicePlan,
		Replicas:    len(cluster.slaves),
		Cores:       cluster.Conf.ProvCores,
		MemoryMB:    cluster.Conf.ProvMem,
		DiskGB:      cluster.Conf.ProvDisk,
		Records:     records,
	}

	// Compute DBU: 1 credit = 1 core / 4GB RAM / 40GB NVMe
	cores, _ := strconv.ParseFloat(cluster.Conf.ProvCores, 64)
	memMB, _ := strconv.ParseFloat(cluster.Conf.ProvMem, 64)
	diskGB, _ := strconv.ParseFloat(cluster.Conf.ProvDisk, 64)
	entry.DBU = math.Max(cores, math.Max(memMB/4096, diskGB/40))
	entry.ClusterDBU = entry.DBU * float64(dbNodes)

	// DB flavor + version from master
	if master := cluster.GetMaster(); master != nil && master.DBVersion != nil {
		entry.DBFlavor = master.DBVersion.Flavor
		entry.DBVersion = master.DBVersion.ToString()
	}

	// Proxy info
	proxies := cluster.GetProxies()
	if len(proxies) > 0 {
		prx := proxies[0]
		entry.ProxyType = prx.GetType()
		entry.ProxyVersion = prx.GetVersion()
	}

	// Compute averages from records
	if len(records) > 0 {
		var totalTPS, totalLatency float64
		var totalErrors int
		for _, r := range records {
			totalTPS += r.TPS
			totalLatency += r.Latency
			totalErrors += int(r.ErrorPerSec)
		}
		entry.AvgTPS = totalTPS / float64(len(records))
		entry.AvgLatency = totalLatency / float64(len(records))
		entry.TotalErrors = totalErrors
	}

	// TPS per DBU — performance efficiency metric
	if entry.ClusterDBU > 0 && entry.AvgTPS > 0 {
		entry.TPSPerDBU = entry.AvgTPS / entry.ClusterDBU
	}

	// Append TPCM results
	entry.Results = cluster.SysBenchTpcMResults

	cluster.SysbenchHistory.Entries = append(cluster.SysbenchHistory.Entries, entry)
	cluster.SaveSysbenchLog()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "BENCH",
		"Sysbench run logged: %s/%s threads=%d avgTPS=%.1f avgLatency=%.2fms flavor=%s/%s proxy=%s/%s replicas=%d DBU=%.1f clusterDBU=%.1f TPS/DBU=%.2f tags=%s",
		testType, testMode, threads, entry.AvgTPS, entry.AvgLatency, entry.DBFlavor, entry.DBVersion,
		entry.ProxyType, entry.ProxyVersion, entry.Replicas, entry.DBU, entry.ClusterDBU, entry.TPSPerDBU, entry.ConfigTags)
}

// SysbenchCompareResult holds multiple runs and a graphite render URL for comparison.
type SysbenchCompareResult struct {
	Runs        []SysbenchLogEntry `json:"runs"`
	Metric      string             `json:"metric"`
	GraphiteURL string             `json:"graphiteUrl"`
}

// GetLastSysbenchRuns returns the last N runs from history (most recent first).
func (cluster *Cluster) GetLastSysbenchRuns(n int) []SysbenchLogEntry {
	entries := cluster.SysbenchHistory.Entries
	if n <= 0 || n > 10 {
		n = 10
	}
	if len(entries) < n {
		n = len(entries)
	}
	// Return most recent first
	result := make([]SysbenchLogEntry, n)
	for i := 0; i < n; i++ {
		result[i] = entries[len(entries)-1-i]
	}
	return result
}

// CompareSysbenchRuns builds a graphite render URL that overlays a chosen metric
// from the selected runs, using timeShift to align all runs to the most recent one.
// metric is a graphite metric suffix like "mysql_global_status_questions".
// runIndices are positions in the history (0 = oldest). Pass nil for last 10.
func (cluster *Cluster) CompareSysbenchRuns(metric string, runIndices []int) (*SysbenchCompareResult, error) {
	entries := cluster.SysbenchHistory.Entries
	if len(entries) == 0 {
		return nil, fmt.Errorf("no sysbench runs recorded")
	}

	// Default: last 10 runs
	if len(runIndices) == 0 {
		start := len(entries) - 10
		if start < 0 {
			start = 0
		}
		for i := start; i < len(entries); i++ {
			runIndices = append(runIndices, i)
		}
	}

	// Validate indices and collect runs
	var runs []SysbenchLogEntry
	for _, idx := range runIndices {
		if idx < 0 || idx >= len(entries) {
			return nil, fmt.Errorf("run index %d out of range (0-%d)", idx, len(entries)-1)
		}
		runs = append(runs, entries[idx])
	}

	result := &SysbenchCompareResult{
		Runs:   runs,
		Metric: metric,
	}

	// Build graphite render URL
	apiURL := cluster.graphiteAPIURL()
	if apiURL == "" {
		return result, nil
	}
	master := cluster.GetMaster()
	if master == nil {
		return result, nil
	}
	hostname := master.graphiteHostname()
	fullMetric := fmt.Sprintf("mysql.%s.%s", hostname, metric)

	// Align all runs to the most recent one's time window
	newest := runs[0]
	for _, r := range runs {
		if r.StartedAt.After(newest.StartedAt) {
			newest = r
		}
	}

	u, _ := url.Parse(apiURL + "/render/")
	q := u.Query()
	q.Set("format", "json")
	q.Set("noCache", "1")
	q.Set("from", strconv.FormatInt(newest.StartedAt.Unix(), 10))
	q.Set("until", strconv.FormatInt(newest.EndedAt.Unix(), 10))

	for i, run := range runs {
		label := fmt.Sprintf("Run%d %s %dt %s/%s", i+1, run.TestType, run.Threads, run.DBFlavor, run.DBVersion)
		if run.StartedAt.Equal(newest.StartedAt) {
			q.Add("target", fmt.Sprintf("alias(%s,'%s')", fullMetric, label))
		} else {
			shift := newest.StartedAt.Sub(run.StartedAt)
			q.Add("target", fmt.Sprintf("alias(timeShift(%s,'%ds'),'%s')", fullMetric, int(shift.Seconds()), label))
		}
	}

	u.RawQuery = q.Encode()
	result.GraphiteURL = u.String()

	return result, nil
}
