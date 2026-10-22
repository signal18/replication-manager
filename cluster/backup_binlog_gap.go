// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/signal18/replication-manager/config"
)

// BinlogGap is a server's lost binlog continuity in the archive (#2091): binlogs no snapshot
// holds, removed from the backup directory or never copied. Better lose binlogs than backups
// (F12): the loss is a state, kept across restarts, until a backup of the server is in the
// archive again; from that backup on the chain is whole.
type BinlogGap struct {
	Server string    `json:"server"` // the server URL
	From   string    `json:"from"`   // the first binlog lost
	To     string    `json:"to"`     // the last one
	Reason string    `json:"reason"` // why the first one was lost
	Since  time.Time `json:"since"`
}

const binlogGapsFile = "binlog-gaps.json"

func (cluster *Cluster) loadBinlogGapsLocked() {
	if cluster.binlogGaps != nil {
		return
	}
	cluster.binlogGaps = map[string]*BinlogGap{}
	if cluster.WorkingDir == "" {
		return // no working directory: kept in memory, never a file relative to the process
	}
	data, err := os.ReadFile(filepath.Join(cluster.WorkingDir, binlogGapsFile))
	if err != nil {
		return
	}
	var list []*BinlogGap
	if err := json.Unmarshal(data, &list); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Binlog gaps %s not read: %s", binlogGapsFile, err)
		return
	}
	for _, g := range list {
		if g != nil && g.Server != "" {
			cluster.binlogGaps[g.Server] = g
		}
	}
}

func (cluster *Cluster) saveBinlogGapsLocked() {
	if cluster.WorkingDir == "" {
		return
	}
	list := make([]*BinlogGap, 0, len(cluster.binlogGaps))
	for _, g := range cluster.binlogGaps {
		list = append(list, g)
	}
	data, err := json.MarshalIndent(list, "", "\t")
	if err != nil {
		return
	}
	path := filepath.Join(cluster.WorkingDir, binlogGapsFile)
	if err := os.WriteFile(path+".tmp", data, 0600); err == nil {
		err = os.Rename(path+".tmp", path)
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Binlog gaps %s not written: %s", binlogGapsFile, err)
	}
}

// noteBinlogGap records binlog of server as lost in the archive, with why; a gap already open
// grows to it. Only with an archive attached: without one, the keep window is the whole promise.
func (cluster *Cluster) noteBinlogGap(server *ServerMonitor, binlog, reason string) {
	if binlog == "" || !server.shouldRunRestic(BackupRunOptions{}) {
		return
	}
	cluster.binlogGapMu.Lock()
	defer cluster.binlogGapMu.Unlock()
	cluster.loadBinlogGapsLocked()
	g := cluster.binlogGaps[server.URL]
	if g == nil {
		g = &BinlogGap{Server: server.URL, From: binlog, To: binlog, Reason: reason, Since: time.Now()}
		cluster.binlogGaps[server.URL] = g
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Binlog continuity of %s lost in the archive from %s: %s", server.URL, binlog, reason)
	} else {
		if binlogNumber(binlog) < binlogNumber(g.From) {
			g.From = binlog
		}
		if binlogNumber(binlog) > binlogNumber(g.To) {
			g.To = binlog
		}
	}
	cluster.saveBinlogGapsLocked()
}

// clearBinlogGap: a backup of the server is in the archive, its binlog chain is whole from it,
// when the backup starts after the last binlog lost (backupBinlog, the binlog of its position,
// numbered above the gap's end): a gap noted while the backup ran, or ending in the backup's own
// binlog, stays (#2142). Without a known position the gap stays.
func (cluster *Cluster) clearBinlogGap(serverURL, backupBinlog string) {
	cluster.binlogGapMu.Lock()
	defer cluster.binlogGapMu.Unlock()
	cluster.loadBinlogGapsLocked()
	if g := cluster.binlogGaps[serverURL]; g != nil {
		if !binlogOlderThan(g.To, backupBinlog) {
			return
		}
		delete(cluster.binlogGaps, serverURL)
		cluster.saveBinlogGapsLocked()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Binlog continuity of %s in the archive whole again from its last backup (binlogs %s to %s lost before it)", serverURL, g.From, g.To)
	}
}

// BinlogGaps lists the lost binlog continuities, by server.
func (cluster *Cluster) BinlogGaps() []BinlogGap {
	cluster.binlogGapMu.Lock()
	defer cluster.binlogGapMu.Unlock()
	cluster.loadBinlogGapsLocked()
	out := make([]BinlogGap, 0, len(cluster.binlogGaps))
	for _, g := range cluster.binlogGaps {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Server < out[j].Server })
	return out
}

// binlogInSnapshotFile keeps, by server, the newest binlog copy a snapshot of its backup
// directory held: without it a restart would take every copy beyond the keep window for one
// no snapshot holds.
const binlogInSnapshotFile = "binlog-in-snapshot.json"

func (cluster *Cluster) readBinlogInSnapshotLocked() map[string]string {
	saved := map[string]string{}
	if cluster.WorkingDir == "" {
		return saved // no working directory: the queue's memory only, never a file relative to the process
	}
	data, err := os.ReadFile(filepath.Join(cluster.WorkingDir, binlogInSnapshotFile))
	if err != nil {
		return saved
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Binlog snapshots %s not read: %s", binlogInSnapshotFile, err)
		return map[string]string{}
	}
	return saved
}

// loadBinlogInSnapshot is the newest binlog copy of server a snapshot held, "" unknown.
func (cluster *Cluster) loadBinlogInSnapshot(serverURL string) string {
	cluster.binlogGapMu.Lock()
	defer cluster.binlogGapMu.Unlock()
	return cluster.readBinlogInSnapshotLocked()[serverURL]
}

func (cluster *Cluster) saveBinlogInSnapshot(serverURL, newest string) {
	if cluster.WorkingDir == "" {
		return
	}
	cluster.binlogGapMu.Lock()
	defer cluster.binlogGapMu.Unlock()
	saved := cluster.readBinlogInSnapshotLocked()
	saved[serverURL] = newest
	data, err := json.MarshalIndent(saved, "", "\t")
	if err != nil {
		return
	}
	path := filepath.Join(cluster.WorkingDir, binlogInSnapshotFile)
	if err := os.WriteFile(path+".tmp", data, 0600); err == nil {
		err = os.Rename(path+".tmp", path)
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Binlog snapshots %s not written: %s", binlogInSnapshotFile, err)
	}
}
