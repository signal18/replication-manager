// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/graphite"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// Backup progress (Stéphane's strategy, 2026-10-05): one tracked state per running backup,
// written by the goroutine that runs it, read by the API and the GUI pill, never
// reconstructed afterwards. A ladder of fallbacks, each level adding precision when its
// data exists:
//
//	level "running": a backup is in flight, its kind and since when (always available)
//	level "bytes":   bytes already written to disk / streamed to the archive against the
//	                 SIZE OF THE PREVIOUS COMPLETED BACKUP of the same kind for this server
//	                 (backup metadata catalog); rate and ETA from the observed throughput
//	level "schema":  the dump's --verbose stream marks every table boundary; with the schema
//	                 monitor's per-table sizes the progress becomes Σ sizes of the tables done
//	                 over Σ all tables, with a per-table throughput (bytes/s) kept in a bounded
//	                 ring and emitted as backup.<cluster>.<server>.<schema_table>.bytes_per_s
//
// Progress is a report: it never gates anything.

const (
	BackupProgressLevelRunning = "running"
	BackupProgressLevelBytes   = "bytes"
	BackupProgressLevelSchema  = "schema"

	backupProgressTableRateRing = 64 // per-table speeds kept per running backup (bounded)
	backupProgressMaxPercent    = 99 // never 100 before the job says done
)

// BackupTableRate is one table's measured throughput.
type BackupTableRate struct {
	Table     string  `json:"table"`
	Bytes     int64   `json:"bytes"`
	Seconds   float64 `json:"seconds"`
	BytesPerS float64 `json:"bytesPerS"`
}

// BackupProgress is the tracked state of one running backup.
type BackupProgress struct {
	mu *sync.Mutex // pointer: the API copies the struct (View, snapshot)

	Key              string            `json:"key"`
	Server           string            `json:"server"`
	Kind             string            `json:"kind"` // logical | physical | binlog | archive
	Task             string            `json:"task"`
	Started          time.Time         `json:"started"`
	UpdatedAt        time.Time         `json:"updatedAt"`
	Level            string            `json:"level"`
	PreviousSize     int64             `json:"previousSize"` // size of the last completed backup of this kind, 0 unknown
	BytesDone        int64             `json:"bytesDone"`
	Percent          float64           `json:"percent"` // -1 unknown
	RateBytesPerS    float64           `json:"rateBytesPerS"`
	EtaSeconds       int64             `json:"etaSeconds"` // -1 unknown
	CurrentTable     string            `json:"currentTable"`
	TablesDone       int               `json:"tablesDone"`
	TablesTotal      int               `json:"tablesTotal"`
	TablesBytesDone  int64             `json:"tablesBytesDone"`
	TablesBytesTotal int64             `json:"tablesBytesTotal"`
	TableRates       []BackupTableRate `json:"tableRates"`

	tableSizes map[string]int64 // schema.table -> data+index bytes, from the schema monitor
	tableStart time.Time
	tableBytes int64
	tableKey   string
}

// newBackupProgress is the only constructor: level running, nothing known yet.
func newBackupProgress(key, server, kind, task string, now time.Time) *BackupProgress {
	return &BackupProgress{mu: &sync.Mutex{}, Key: key, Server: server, Kind: kind, Task: task,
		Started: now, UpdatedAt: now, Level: BackupProgressLevelRunning, Percent: -1, EtaSeconds: -1}
}

func backupProgressKey(server *ServerMonitor, kind string) string {
	return server.URL + "/" + kind
}

// StartBackupProgress opens the tracked state of a backup that just started on server.
// The previous size comes from the metadata catalog: the newest COMPLETED backup of the
// same method from this server.
func (cluster *Cluster) StartBackupProgress(server *ServerMonitor, kind, task string) *BackupProgress {
	if cluster == nil || server == nil {
		return nil
	}
	now := time.Now()
	p := newBackupProgress(backupProgressKey(server, kind), server.URL, kind, task, now)
	p.PreviousSize = cluster.previousBackupSize(server, kind)
	if p.PreviousSize > 0 {
		p.Level = BackupProgressLevelBytes
	}
	if kind == "logical" {
		p.tableSizes, p.TablesBytesTotal, p.TablesTotal = cluster.backupTableSizes(server)
	}
	cluster.backupProgress.Store(p.Key, p)
	return p
}

// EndBackupProgress forgets a finished backup (completed or failed alike: the catalog is
// the record, this is the live view).
func (cluster *Cluster) EndBackupProgress(p *BackupProgress) {
	if cluster == nil || p == nil {
		return
	}
	cluster.backupProgress.Delete(p.Key)
}

// backupProgressFor returns the running progress of this server and kind, nil otherwise.
func (cluster *Cluster) backupProgressFor(server *ServerMonitor, kind string) *BackupProgress {
	if cluster == nil || server == nil {
		return nil
	}
	if v, ok := cluster.backupProgress.Load(backupProgressKey(server, kind)); ok {
		return v.(*BackupProgress)
	}
	return nil
}

// previousBackupSize is the size of the newest completed backup of the same method taken
// from this server, 0 when there is none (first run: bytes and rate only, no percentage).
func (cluster *Cluster) previousBackupSize(server *ServerMonitor, kind string) int64 {
	var method backupmgr.BackupMethod
	switch kind {
	case "logical":
		method = backupmgr.BackupMethodLogical
	case "physical":
		method = backupmgr.BackupMethodPhysical
	default:
		return 0
	}
	var best *backupmgr.BackupMetadata
	cluster.BackupMetaMap.Range(func(_, v any) bool {
		m, ok := v.(*backupmgr.BackupMetadata)
		if !ok || m == nil || !m.Completed || m.Source != server.URL || m.BackupMethod != method || m.Size <= 0 {
			return true
		}
		if best == nil || m.EndTime.After(best.EndTime) {
			best = m
		}
		return true
	})
	if best == nil {
		return 0
	}
	return best.Size
}

// backupTableSizes snapshots the schema monitor's per-table sizes (data + index) for the
// dump's source: the master's dictionary when the server is the master, its own otherwise.
func (cluster *Cluster) backupTableSizes(server *ServerMonitor) (map[string]int64, int64, int) {
	src := server
	if m := cluster.GetMaster(); m != nil && (src.DictTables == nil || len(src.DictTables.ToNewMap()) == 0) {
		src = m
	}
	if src == nil || src.DictTables == nil {
		return nil, 0, 0
	}
	sizes := make(map[string]int64)
	var total int64
	for k, t := range src.DictTables.ToNewMap() {
		if t == nil {
			continue
		}
		b := t.DataLength + t.IndexLength
		sizes[k] = b
		total += b
	}
	return sizes, total, len(sizes)
}

// AddBytes records bytes that reached the destination (level "bytes").
func (p *BackupProgress) AddBytes(n int64) {
	if p == nil || n <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.BytesDone += n
	p.refreshLocked(time.Now())
}

// SetBytes records the destination size measured from outside (a directory walked, a
// stream counter read).
func (p *BackupProgress) SetBytes(total int64) {
	if p == nil || total < 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.BytesDone = total
	p.refreshLocked(time.Now())
}

// refreshLocked recomputes rate, percent and ETA from the bytes side; the schema level,
// when armed, owns the percentage.
func (p *BackupProgress) refreshLocked(now time.Time) {
	p.UpdatedAt = now
	elapsed := now.Sub(p.Started).Seconds()
	if elapsed > 0 {
		p.RateBytesPerS = float64(p.BytesDone) / elapsed
	}
	if p.Level == BackupProgressLevelSchema {
		return
	}
	if p.PreviousSize > 0 {
		pct := float64(p.BytesDone) / float64(p.PreviousSize) * 100
		if pct > backupProgressMaxPercent {
			pct = backupProgressMaxPercent
		}
		p.Percent = pct
		if p.RateBytesPerS > 0 && p.PreviousSize > p.BytesDone {
			p.EtaSeconds = int64(float64(p.PreviousSize-p.BytesDone) / p.RateBytesPerS)
		} else {
			p.EtaSeconds = -1
		}
	}
}

// mysqldump --verbose marks every table: "-- Retrieving table structure for table t1..."
// (MariaDB prints the bare table name; a schema prefix is accepted when present).
var backupDumpTableRe = regexp.MustCompile("Retrieving table structure for table [`]?([A-Za-z0-9_$]+)(?:[`]?\\.[`]?([A-Za-z0-9_$]+))?[`]?")

// ObserveDumpLine feeds one line of the dump's verbose stream (level "schema").
func (p *BackupProgress) ObserveDumpLine(line string) {
	if p == nil {
		return
	}
	m := backupDumpTableRe.FindStringSubmatch(line)
	if m == nil {
		return
	}
	schema, table := "", m[1]
	if m[2] != "" {
		schema, table = m[1], m[2]
	}
	p.TableBoundary(schema, table, time.Now())
}

// TableBoundary closes the current table (its throughput joins the ring) and opens the
// next one. The percentage becomes the bytes of the tables done over the bytes of every
// table, ETA from the average throughput so far.
func (p *BackupProgress) TableBoundary(schema, table string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeTableLocked(now)
	key, size := p.lookupTableLocked(schema, table)
	p.tableKey, p.tableBytes, p.tableStart = key, size, now
	p.CurrentTable = key
	p.Level = BackupProgressLevelSchema
	p.UpdatedAt = now
}

// FinishTables closes the last table when the dump ends.
func (p *BackupProgress) FinishTables(now time.Time) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closeTableLocked(now)
	p.CurrentTable = ""
}

func (p *BackupProgress) closeTableLocked(now time.Time) {
	if p.tableKey == "" {
		return
	}
	secs := now.Sub(p.tableStart).Seconds()
	r := BackupTableRate{Table: p.tableKey, Bytes: p.tableBytes, Seconds: secs}
	if secs > 0 {
		r.BytesPerS = float64(p.tableBytes) / secs
	}
	p.TableRates = append(p.TableRates, r)
	if len(p.TableRates) > backupProgressTableRateRing {
		p.TableRates = p.TableRates[len(p.TableRates)-backupProgressTableRateRing:]
	}
	p.TablesDone++
	p.TablesBytesDone += p.tableBytes
	p.tableKey = ""
	if p.TablesBytesTotal > 0 {
		pct := float64(p.TablesBytesDone) / float64(p.TablesBytesTotal) * 100
		if pct > backupProgressMaxPercent {
			pct = backupProgressMaxPercent
		}
		p.Percent = pct
		elapsed := now.Sub(p.Started).Seconds()
		if elapsed > 0 && p.TablesBytesDone > 0 {
			rate := float64(p.TablesBytesDone) / elapsed
			p.EtaSeconds = int64(float64(p.TablesBytesTotal-p.TablesBytesDone) / rate)
		}
	}
}

// lookupTableLocked resolves a dumped table to the schema monitor's entry: schema.table
// when the stream gave a schema, else the single table of that name across schemas (the
// sum when the name exists in several, rare and harmless for a progress bar).
func (p *BackupProgress) lookupTableLocked(schema, table string) (string, int64) {
	if schema != "" {
		k := schema + "." + table
		return k, p.tableSizes[k]
	}
	var size int64
	matches := []string{}
	for k, b := range p.tableSizes {
		if strings.HasSuffix(k, "."+table) {
			matches = append(matches, k)
			size += b
		}
	}
	sort.Strings(matches)
	if len(matches) == 1 {
		return matches[0], size
	}
	if len(matches) > 1 {
		return table, size
	}
	return table, 0
}

// View returns a copy for the API.
func (p *BackupProgress) View() BackupProgress {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := *p
	c.mu = nil
	c.TableRates = append([]BackupTableRate(nil), p.TableRates...)
	c.tableSizes = nil
	return c
}

// snapshotBackupProgress is the per-tick view of every running backup, plus the archive
// push restic is running (its own progress, bytes_done / total_bytes), sorted by start.
func (cluster *Cluster) snapshotBackupProgress() []BackupProgress {
	out := []BackupProgress{}
	cluster.backupProgress.Range(func(_, v any) bool {
		if p, ok := v.(*BackupProgress); ok {
			out = append(out, p.View())
		}
		return true
	})
	if cluster.ResticManager != nil {
		if t := cluster.ResticManager.CurrentTaskProgress(); t != nil {
			taskName := fmt.Sprintf("%v", t.TaskType)
			p := BackupProgress{Key: "archive/" + taskName, Server: "", Kind: "archive", Task: taskName,
				Level: BackupProgressLevelBytes, Percent: -1, EtaSeconds: -1, BytesDone: t.BytesDone, PreviousSize: t.TotalBytes, UpdatedAt: time.Now()}
			if t.StartedAt != nil {
				p.Started = *t.StartedAt
			} else {
				p.Started = p.UpdatedAt
			}
			if t.TotalBytes > 0 {
				p.Percent = t.PercentDone * 100
				if p.Percent > backupProgressMaxPercent {
					p.Percent = backupProgressMaxPercent
				}
			}
			out = append(out, p)
		}
	}
	// The dump of the cluster's data comes first (the pill shows the first row), then the
	// binlog copy, then the archive push -- which belongs to the PREVIOUS backup and must
	// never pass for the running one (Stéphane saw "53 % super fast": restic's push of my
	// dump, shown as the pill while his own dump was about to start).
	rank := func(kind string) int {
		switch kind {
		case "logical", "physical":
			return 0
		case "binlog":
			return 1
		default:
			return 2
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if rank(out[i].Kind) != rank(out[j].Kind) {
			return rank(out[i].Kind) < rank(out[j].Kind)
		}
		return out[i].Started.Before(out[j].Started)
	})
	return out
}

// emitBackupTableRates writes the per-table throughput series of a finished dump:
// backup.<cluster>.<server>.<schema_table>.bytes_per_s, the history that predicts the next
// run and shows a table that got slower.
func (cluster *Cluster) emitBackupTableRates(server *ServerMonitor, p *BackupProgress) {
	if cluster == nil || p == nil || cluster.ClusterGraphite == nil {
		return
	}
	v := p.View()
	ts := time.Now().Unix()
	metrics := make([]graphite.Metric, 0, len(v.TableRates))
	for _, r := range v.TableRates {
		if r.BytesPerS <= 0 {
			continue
		}
		path := fmt.Sprintf("backup.%s.%s.%s.bytes_per_s", cluster.Name, computeTokenReplacer.Replace(server.Name), computeTokenReplacer.Replace(r.Table))
		metrics = append(metrics, graphite.NewMetric(path, strconv.FormatFloat(r.BytesPerS, 'f', 0, 64), ts))
	}
	if len(metrics) > 0 {
		cluster.AddMetrics(metrics)
	}
}
