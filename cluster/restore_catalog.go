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
	attached := cluster.Conf.AttachedArchive()
	mounted := cluster.mountedBackupsFolder()
	// the backups refreshBackupProofs last found proven (#2023): the catalog takes no lock
	// and reads no file, it runs on the monitor tick (HasCatalogBackupForRejoin) and in a
	// rejoin; none checked yet, none proven
	var proofs map[backupProofKey]backupmgr.ArtifactFingerprint
	if p := cluster.backupProofs.Load(); p != nil {
		proofs = *p
	}
	for _, sv := range cluster.Servers {
		if sv == nil {
			continue
		}
		for _, m := range []*backupmgr.BackupMetadata{sv.LastBackupMeta.Logical, sv.LastBackupMeta.Physical} {
			// a discovered backup changed since registered, or not a backup, is not used (#2073)
			if m == nil || !m.Completed || cluster.backupUseRefusal(m) != nil {
				continue
			}
			e := backupMetaToCatalog(sv.URL, m, attached, mounted)
			// a backup only in an archive is a candidate when a restore can fetch it back now
			// (#2091): the attached archive holds it and is one replication-manager fetches from
			if e.inArchiveOnly() && (e.Snapshot == "" || !cluster.archiveFetchable()) {
				continue
			}
			// a test restore proves the file it loaded, not a snapshot of it
			_, proven := proofs[backupProofKey{sv.URL, m.Id, m.Dest}]
			e.Proven = !e.inArchiveOnly() && proven
			cat = append(cat, e)
		}
	}
	return cat
}

// backupCatalogLocation is where the backup's file is (#2091): archive when it was removed
// after archiving (only a snapshot holds it), remote when it is in the backups folder with the
// bucket mounted on it (mounted, "" when not mounted), else local, on replication-manager's
// own disk. It reads no file.
func backupCatalogLocation(m *backupmgr.BackupMetadata, mounted string) string {
	switch {
	case m.OriginalRemoved:
		return RepoArchive
	case mounted != "" && isStrictlyWithinBase(mounted, m.Dest):
		return RepoRemote
	default:
		return RepoLocal
	}
}

// archiveFetchable: a backup only in the attached archive can be fetched back from it now
// (restoreArchivedOriginal). Only a restic archive is fetched from yet: a plugin archive's
// (plakar, pbs) fetch is not wired.
func (cluster *Cluster) archiveFetchable() bool {
	if _, external := cluster.attachedExternalArchive(); external {
		return false
	}
	return cluster.Conf.BackupRestic && cluster.ResticManager != nil
}

// BackupView is a backup as the API shows it: its record, and what the restore selection
// reads of it (buildBackupCatalog): its capabilities, where its file is (local, remote or
// archive), whether the attached archive holds a snapshot of it, proven by a test restore of
// its file still unchanged, and whether a restore or reseed picks among it now (each server's
// last logical and physical backup, completed, usable, and fetchable when only archived).
type BackupView struct {
	*backupmgr.BackupMetadata
	Capabilities     []string `json:"capabilities"`
	Location         string   `json:"location"`
	Archived         bool     `json:"archived"`
	Proven           bool     `json:"proven"`
	RestoreCandidate bool     `json:"restoreCandidate"`
}

// GetBackupsView is the catalogue for the API, each backup with what the restore selection
// reads of it.
func (cluster *Cluster) GetBackupsView() map[int64]BackupView {
	attached := cluster.Conf.AttachedArchive()
	mounted := cluster.mountedBackupsFolder()
	var proofs map[backupProofKey]backupmgr.ArtifactFingerprint
	if p := cluster.backupProofs.Load(); p != nil {
		proofs = *p
	}
	// the restore selection's candidates, as it builds them
	last := map[backupProofKey]bool{}
	for _, e := range cluster.buildBackupCatalog() {
		last[backupProofKey{e.Server, e.ID, ""}] = true
	}
	out := map[int64]BackupView{}
	for id, m := range cluster.GetBackups() {
		if m == nil {
			continue
		}
		loc := backupCatalogLocation(m, mounted)
		_, proven := proofs[backupProofKey{m.Source, m.Id, m.Dest}]
		caps := m.Capabilities()
		if caps == nil {
			caps = []string{}
		}
		out[id] = BackupView{BackupMetadata: m, Capabilities: caps, Location: loc,
			Archived: strings.TrimSpace(m.ArchiveSnapshot(attached)) != "",
			Proven:   loc != RepoArchive && proven, RestoreCandidate: last[backupProofKey{m.Source, m.Id, ""}]}
	}
	return out
}

// backupProofKey names a backup in the proof snapshot: its server, id and file (an id is a
// unix time, two servers' backups can share it).
type backupProofKey struct {
	server string
	id     int64
	dest   string
}

// backupProofWalkMax bounds the entries refreshBackupProofs reads of a backup folder: a
// larger splitdump is not proven.
const backupProofWalkMax = 100000

// refreshBackupProofs checks, for each server's last backups that passed a test restore,
// that the file it loaded is still on disk unchanged (its fingerprint, a bounded folder
// walk), and publishes the proven ones as a new snapshot for buildBackupCatalog: at most two
// per server. It runs on its own, off the monitor tick (Run, every 30 heartbeats), and one at
// a time: a call while one runs returns at once, the catalog reading the last snapshot. It
// waits for backupMetaMutex and reads files here, where nothing waits for it.
func (cluster *Cluster) refreshBackupProofs() {
	if !cluster.backupProofRefreshing.CompareAndSwap(false, true) {
		return
	}
	defer cluster.backupProofRefreshing.Store(false)
	type proofFile struct {
		key backupProofKey
		rec backupmgr.ArtifactFingerprint
	}
	var files []proofFile
	for _, sv := range cluster.Servers {
		if sv == nil {
			continue
		}
		sv.backupMetaMutex.Lock()
		for _, m := range []*backupmgr.BackupMetadata{sv.LastBackupMeta.Logical, sv.LastBackupMeta.Physical} {
			if m != nil && m.Completed && backupProven(m) && cluster.backupUseRefusal(m) == nil {
				files = append(files, proofFile{backupProofKey{sv.URL, m.Id, m.Dest}, *m.Verification.Artifact})
			}
		}
		sv.backupMetaMutex.Unlock()
	}
	proofs := make(map[backupProofKey]backupmgr.ArtifactFingerprint, len(files))
	for _, f := range files {
		if artifactUnchangedBounded(f.key.dest, &f.rec, backupProofWalkMax) {
			proofs[f.key] = f.rec
		}
	}
	cluster.backupProofs.Store(&proofs)
}

// provenStillUnchanged checks again, when a restore is about to use it, that the file of a
// catalog entry proven by the last refresh is unchanged: the refresh runs every 30
// heartbeats, the file may have changed since. No lock: the fingerprint is the snapshot's.
func (cluster *Cluster) provenStillUnchanged(e *BackupCatalogEntry) bool {
	p := cluster.backupProofs.Load()
	if e == nil || !e.Proven || p == nil {
		return false
	}
	rec, ok := (*p)[backupProofKey{e.Server, e.ID, e.Path}]
	return ok && artifactUnchangedBounded(e.Path, &rec, backupProofWalkMax)
}

// backupMetaToCatalog maps one BackupMetadata (Ahmad's) to a catalog entry so
// every selector dimension can be evaluated against it.
//
// attached is the attached archive's name: Snapshot is its snapshot of the backup; a backup
// held by a detached archive only is restored from it once attached. mounted is the backups
// folder when the bucket is mounted on it (backupCatalogLocation).
func backupMetaToCatalog(serverURL string, m *backupmgr.BackupMetadata, attached, mounted string) BackupCatalogEntry {
	kind := "logical"
	if m.BackupMethod == backupmgr.BackupMethodPhysical {
		kind = "physical"
	}

	loc := backupCatalogLocation(m, mounted)
	caps := m.Capabilities()

	ts := m.EndTime.Unix()
	if ts <= 0 {
		ts = m.StartTime.Unix()
	}

	return BackupCatalogEntry{
		ID:        m.Id,
		Server:    serverURL,
		Location:  loc,
		Kind:      kind,
		Tool:      m.BackupTool,
		Caps:      caps,
		Timestamp: ts,
		Gtid:      m.BinLogGtid,
		BinFile:   m.BinLogFileName,
		BinPos:    strconv.FormatUint(m.BinLogFilePos, 10),
		Path:      m.Dest,
		Snapshot:  strings.TrimSpace(m.ArchiveSnapshot(attached)),
	}
}

// backupProven: the backup passed a test restore, the restore level of a backup
// verification (#2023), and that run recorded the file it loaded (Artifact), which
// refreshBackupProofs checks is still on disk unchanged. An integrity pass reads the
// backup but loads it nowhere. Read meta under its server's backupMetaMutex.
func backupProven(m *backupmgr.BackupMetadata) bool {
	v := m.Verification
	return v != nil && v.Level == config.ConstBackupVerificationLevelRestore && v.Status == backupmgr.BackupVerificationPassed && v.Artifact != nil
}
