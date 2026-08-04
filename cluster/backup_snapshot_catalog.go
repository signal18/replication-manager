// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// SnapshotEntry is one filesystem snapshot as reported by the enterprise
// `plugin-snapshot --type=zfs list` CLI. Creation/size let repman catalogue it
// with a real timestamp and footprint.
type SnapshotEntry struct {
	Name         string `json:"name"`     // dataset@snapname
	CreationUnix int64  `json:"creation"` // snapshot creation time (unix seconds)
	SizeBytes    int64  `json:"size"`     // referenced/used bytes
}

// SnapshotListResult mirrors the JSON emitted by `plugin-snapshot list`.
type SnapshotListResult struct {
	OK        bool            `json:"ok"`
	Type      string          `json:"type"`
	Dataset   string          `json:"dataset"`
	Snapshots []SnapshotEntry `json:"snapshots"`
	Error     string          `json:"error"`
}

// snapshotMetaID derives a stable catalogue id from server+snapshot name, so
// re-ingesting the same snapshot updates its entry in place instead of
// duplicating it.
func snapshotMetaID(serverURL, name string) int64 {
	h := fnv.New64a()
	h.Write([]byte(serverURL + "|" + name))
	return int64(h.Sum64() & 0x7fffffffffffffff) // force positive
}

// IngestSnapshots turns a list of filesystem snapshots into catalogued backups
// (BackupMethodSnapshot) in the cluster BackupMetaMap, so they show in the GUI
// backupList and become restore-selectable via buildBackupCatalog. Returns the
// number of entries catalogued.
//
// Snapshots are whole-instance, fast-restore, needs-db-restart and NOT
// corruption-verified — those caps are attached in backupMetaToCatalog by method.
// A snapshot carries a gtid/binlog position only if repman captured one around
// the snapshot creation (see BinLog* fields); pre-existing (OpenSVC/manual)
// snapshots have none and catalogue as whole-instance-restore-only.
func (server *ServerMonitor) IngestSnapshots(dataset string, entries []SnapshotEntry) int {
	cluster := server.ClusterGroup
	if cluster == nil || cluster.BackupMetaMap == nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.Name == "" {
			continue
		}
		ts := time.Time{}
		if e.CreationUnix > 0 {
			ts = time.Unix(e.CreationUnix, 0)
		}
		id := snapshotMetaID(server.URL, e.Name)
		cluster.BackupMetaMap.Set(id, &backupmgr.BackupMetadata{
			Id:             id,
			StartTime:      ts,
			EndTime:        ts,
			BackupMethod:   backupmgr.BackupMethodSnapshot,
			BackupTool:     "zfs",
			BackupStrategy: backupmgr.BackupStrategyFull,
			Source:         server.URL,
			Dest:           e.Name,
			Size:           e.SizeBytes,
			Completed:      true,
		})
		n++
	}
	return n
}

// FetchBackupCatalog is the generic, multi-source backup-catalogue refresh, run
// from the monitor loop on the regular backup-refresh cadence (no dedicated
// scheduler). Each source is an action that gates itself on its own enable
// variable, mirroring ResticFetchRepo (gated by backup-restic).
func (cluster *Cluster) FetchBackupCatalog() {
	cluster.ResticFetchRepo() // restic snapshots — gated by backup-restic
	cluster.CatalogSnapshots() // fs snapshots     — gated by backup-collect-snapshots
	// future sources (storage-array snapshots, …) plug in here, each self-gated.
}

// CatalogSnapshots dispatches the zfssnapshotcatalog dbjob to the backup server
// so its filesystem snapshots are (re-)catalogued. Gated by backup-collect-snapshots.
func (cluster *Cluster) CatalogSnapshots() {
	if !cluster.Conf.BackupCollectSnapshots {
		return
	}
	srv := cluster.GetBackupServer()
	if srv == nil || srv.IsDown() {
		return
	}
	if _, err := srv.JobZFSSnapshotCatalog(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg,
			"snapshot catalogue dispatch on %s: %s", srv.URL, err)
	}
}

// IsSnapshotPayload reports whether a decrypted write-log body is a
// plugin-snapshot result (has a top-level "action") rather than a framework
// status LogEntry ({server,log}). The zfssnapshot/zfssnapshotcatalog tasks post
// both over the same write-log endpoint, so the handler uses this to route the
// snapshot JSON to the catalogue and the status lines to the log.
func IsSnapshotPayload(out []byte) bool {
	var probe struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		return false
	}
	return probe.Action != ""
}

// ParseAndIngestSnapshotList parses the JSON output of `plugin-snapshot list`
// and catalogues the snapshots it reports.
func (server *ServerMonitor) ParseAndIngestSnapshotList(out []byte) (int, error) {
	var r SnapshotListResult
	if err := json.Unmarshal(out, &r); err != nil {
		return 0, fmt.Errorf("parse plugin-snapshot list output: %w", err)
	}
	if !r.OK {
		return 0, fmt.Errorf("plugin-snapshot list failed: %s", r.Error)
	}
	return server.IngestSnapshots(r.Dataset, r.Snapshots), nil
}
