// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// getAutorejoinBackupSelector parses the operator's per-method
// autorejoin-backup-selector-{logical,physical} JSON into a RestoreSelector —
// the human-validated choice promoted to automatic rejoin — falling back to the
// method default when unset/invalid. This is the loop: pick+validate a restore
// manually, then set that selector here.
//
// The default digs ANY origin/repo/location: after a failover the backup is
// usually on the OLD master (or in restic/S3), never the freshly-promoted one,
// so gating on the master or on local storage would wrongly report "no backup".
// Order prefers newest then local as a tie-break, but never gates on location.
func (cluster *Cluster) getAutorejoinBackupSelector(method string) RestoreSelector {
	var def RestoreSelector
	var raw string
	switch method {
	case "physical":
		def = RestoreSelector{Type: []string{"physical"}, Origin: OriginAny, Repo: RepoAny, Order: []string{"last", "local"}}
		raw = strings.TrimSpace(cluster.Conf.AutorejoinBackupSelectorPhysical)
	default: // logical
		def = RestoreSelector{Type: []string{"logical"}, Origin: OriginAny, Repo: RepoAny, Order: []string{"last", "local"}}
		raw = strings.TrimSpace(cluster.Conf.AutorejoinBackupSelectorLogical)
	}
	if raw == "" {
		return def
	}
	var sel RestoreSelector
	if err := json.Unmarshal([]byte(raw), &sel); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "autorejoin-backup-selector-%s invalid JSON (%s) — using default", method, err)
		return def
	}
	return sel
}

// buildBackupCatalog assembles the unified backup catalog the RestoreSelector
// runs against, from what repman already tracks: each server's last logical and
// physical BackupMetadata (local, plus its Restic snapshot when enabled).
//
// First cut — one row per server per kind (the LAST backup). Multi-backup
// on-disk history and a full Restic snapshot listing are follow-ups; the "last
// per node" set is already enough to stop looking only on the master.
func (cluster *Cluster) buildBackupCatalog() []BackupCatalogEntry {
	cat := make([]BackupCatalogEntry, 0, len(cluster.Servers)*2)
	for _, sv := range cluster.Servers {
		if sv == nil {
			continue
		}
		if m := sv.LastBackupMeta.Logical; m != nil && m.Completed {
			cat = append(cat, backupMetaToCatalog(sv.URL, m))
		}
		if m := sv.LastBackupMeta.Physical; m != nil && m.Completed {
			cat = append(cat, backupMetaToCatalog(sv.URL, m))
		}
	}
	return cat
}

// backupMetaToCatalog maps one BackupMetadata (Ahmad's) to a catalog entry so
// every selector dimension can be evaluated against it.
func backupMetaToCatalog(serverURL string, m *backupmgr.BackupMetadata) BackupCatalogEntry {
	kind := "logical"
	if m.BackupMethod == backupmgr.BackupMethodPhysical {
		kind = "physical"
	}

	loc := RepoLocal
	if m.ResticEnabled && m.ResticSnapshotID != "" {
		loc = RepoRemote
	}

	var caps []string
	if m.Compressed {
		caps = append(caps, "compress")
	}
	if m.Encrypted {
		caps = append(caps, "encrypt")
	}
	if m.SplitDump || m.BackupTool == "mydumper" {
		caps = append(caps, "can-partial-restorable")
	}
	if m.BinLogGtid != "" {
		caps = append(caps, "gtid")
	}

	ts := m.EndTime.Unix()
	if ts <= 0 {
		ts = m.StartTime.Unix()
	}

	path := m.Dest
	if loc == RepoRemote && m.ResticSnapshotID != "" {
		path = m.ResticSnapshotID
	}

	return BackupCatalogEntry{
		Server:    serverURL,
		Location:  loc,
		Kind:      kind,
		Tool:      m.BackupTool,
		Caps:      caps,
		Proven:    false, // NEW verify mechanism will set this
		Timestamp: ts,
		Gtid:      m.BinLogGtid,
		BinFile:   m.BinLogFileName,
		BinPos:    strconv.FormatUint(m.BinLogFilePos, 10),
		Path:      path,
	}
}
