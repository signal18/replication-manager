// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

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
		def = PresetRejoinPhysical()
		raw = strings.TrimSpace(cluster.Conf.AutorejoinBackupSelectorPhysical)
	default: // logical
		def = PresetRejoinLogical()
		raw = strings.TrimSpace(cluster.Conf.AutorejoinBackupSelectorLogical)
	}
	if raw == "" {
		// No explicit operator override: prefer the newest validated selector
		// for this method, if a manual restore has ever recorded one — this is
		// the "operator — and autorejoin-backup-selector — picks from this
		// validated set" loop the feature closes. Falls back to the coarse
		// preset default when the store is empty/missing, so behavior is
		// unchanged for any deployment without a recorded success.
		if validated := cluster.newestValidatedSelector(method); validated != nil {
			return *validated
		}
		return def
	}
	var sel RestoreSelector
	if err := json.Unmarshal([]byte(raw), &sel); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "autorejoin-backup-selector-%s invalid JSON (%s) — using default", method, err)
		return def
	}
	return sel
}

// resolveLogicalBackupSource picks a logical backup of the given tool via the
// shared RestoreSelector, mirroring the pattern already used by
// JobFlashbackLogicalBackup. Returns ("", nil, nil) when the resolver has no
// usable local pick — the caller then runs its own legacy on-disk cascade.
//
// meta is the exact BackupMetadata record backing the picked catalog entry
// (BackupCatalogEntry.Meta), when available — the selector can pick an
// OLDER BackupMetaMap entry than source's current LastBackupMeta.Logical
// (e.g. the newest backup excluded by a TimeNotAfterHead gate), so callers
// needing restore parameters (SplitUser, etc.) MUST prefer this over
// independently re-reading LastBackupMeta, which would silently describe a
// DIFFERENT backup than the one actually selected. meta is nil when the
// pick came from an on-disk-enumerated entry (no BackupMetadata backing
// it) — callers fall back to their own LastBackupMeta-based lookup then.
func (cluster *Cluster) resolveLogicalBackupSource(master, target *ServerMonitor, backtype string) (backupfile string, source *ServerMonitor, meta *backupmgr.BackupMetadata) {
	sel := cluster.getAutorejoinBackupSelector("logical")
	sel.Tool = []string{backtype} // restore dispatch is backtype-based → force the tool for consistency
	pick := ResolveRestore(cluster.buildBackupCatalog(), sel,
		ResolveContext{MasterURL: master.URL, TargetURL: target.URL, HeadGtid: masterHeadGtidString(master)})
	if pick == nil || !pick.isLocal() {
		return "", nil, nil
	}
	src := master
	if s := cluster.GetServerFromURL(pick.Server); s != nil {
		src = s
	}
	return pick.Path, src, pick.Meta
}

// legacyLogicalBackupFallback is the pre-selector on-disk/cookie cascade,
// shared by JobReseedLogicalBackupPrepare and ProcessReseedLogical so it
// exists in exactly one place: prefer the designated backup server (if its
// cookie for backtype is set and either its cached metadata or an on-disk
// file confirms a backup), else fall back to the master (metadata lookup,
// on-disk lookup, then a bare default path). Called only when
// resolveLogicalBackupSource had no usable local pick (e.g. LastBackupMeta
// hasn't populated yet on a fresh cluster). Callers still perform the final
// os.Stat/cookie-cleanup gate themselves when useMaster is true.
func (cluster *Cluster) legacyLogicalBackupFallback(master *ServerMonitor, backtype, dest string, destCandidates []string) (backupfile string, source *ServerMonitor, useMaster bool) {
	bckserver := cluster.GetBackupServer()
	if bckserver != nil && bckserver.HasBackupTypeCookie(backtype) {
		if resolved, ok := resolveLogicalBackupPathFromMeta(bckserver, backtype); ok {
			return resolved, bckserver, false
		} else if resolved, ok := findExistingBackupPath(bckserver, destCandidates); ok {
			return resolved, bckserver, false
		} else {
			//Remove false cookie
			bckserver.DelBackupTypeCookie(backtype)
		}
	}

	if resolved, ok := resolveLogicalBackupPathFromMeta(master, backtype); ok {
		backupfile = resolved
	} else if resolved, ok := findExistingBackupPath(master, destCandidates); ok {
		backupfile = resolved
	} else {
		backupfile = master.GetMyBackupDirectory() + dest
	}
	return backupfile, master, true
}

// resolvePhysicalBackupSource picks a physical backup of the given tool via
// the shared RestoreSelector, mirroring resolveLogicalBackupSource. Returns
// ("", nil) when the resolver has no usable local pick — the caller then
// runs its own legacy on-disk cascade.
func (cluster *Cluster) resolvePhysicalBackupSource(master, target *ServerMonitor, backtype string) (backupfile string, source *ServerMonitor) {
	sel := cluster.getAutorejoinBackupSelector("physical")
	sel.Tool = []string{backtype} // restore dispatch is backtype-based → force the tool for consistency
	pick := ResolveRestore(cluster.buildBackupCatalog(), sel,
		ResolveContext{MasterURL: master.URL, TargetURL: target.URL, HeadGtid: masterHeadGtidString(master)})
	if pick == nil || !pick.isLocal() {
		return "", nil
	}
	src := master
	if s := cluster.GetServerFromURL(pick.Server); s != nil {
		src = s
	}
	return pick.Path, src
}

// legacyPhysicalBackupFallback is the pre-selector cookie/on-disk cascade,
// shared by JobReseedPhysicalBackup and ProcessReseedPhysical. Simpler than
// its logical counterpart: physical has one fixed filename (backtype +
// backupext), no metadata-based lookup. Called only when
// resolvePhysicalBackupSource had no usable local pick. Callers still
// perform the final os.Stat/cookie-cleanup gate themselves when useMaster
// is true.
func (cluster *Cluster) legacyPhysicalBackupFallback(master *ServerMonitor, backtype string) (backupfile string, source *ServerMonitor, useMaster bool) {
	backupext := ".xbtream"
	if cluster.Conf.CompressBackups {
		backupext = backupext + ".gz"
	}
	file := backtype + backupext

	bckserver := cluster.GetBackupServer()
	if bckserver != nil && bckserver.HasBackupTypeCookie(backtype) {
		if _, err := os.Stat(bckserver.GetMyBackupDirectory() + file); err == nil {
			return bckserver.GetMyBackupDirectory() + file, bckserver, false
		}
		//Remove false cookie
		bckserver.DelBackupTypeCookie(backtype)
	}
	return master.GetMyBackupDirectory() + file, master, true
}

// liveDumpCatalog is the synthetic, catalog-shaped source list a direct-dump
// rejoin (RejoinDirectDump, srv_rejoin.go) resolves over: not existing
// backups (Path is left empty — there is nothing on disk yet), but "dump
// this node's current data right now" candidates — the Repo:"live" leg of
// the model in doc/implementation/cluster/RESTORE_SELECTOR.md. Timestamped
// "now" so Order:"last" still behaves; Kind/Tool reflect what
// JobRejoinMysqldumpFromSource actually runs (a mysqldump).
//
// Deliberately kept OUT of buildBackupCatalog(): that catalog also feeds
// HasCatalogBackupForRejoin/resolveLogicalBackupSource/resolvePhysicalBackupSource,
// which gate on an ACTUAL backup existing. A live source is available
// whenever there's a master, so mixing it into the shared catalog would make
// those "is there a real backup" checks vacuously true.
//
// GetBackupServer() already falls back to cluster.master when no server has
// PreferedBackup set (cluster_get.go), so the dedup check below only adds a
// second entry when a DIFFERENT node is the designated backup replica.
func (cluster *Cluster) liveDumpCatalog() []BackupCatalogEntry {
	now := time.Now().Unix()
	live := func(server *ServerMonitor) BackupCatalogEntry {
		return BackupCatalogEntry{Server: server.URL, Location: RepoLive, Kind: "logical", Tool: "mysqldump", Timestamp: now}
	}

	var cat []BackupCatalogEntry
	bckserver := cluster.GetBackupServer()
	if bckserver != nil {
		cat = append(cat, live(bckserver))
	}
	if cluster.master != nil && (bckserver == nil || bckserver.URL != cluster.master.URL) {
		cat = append(cat, live(cluster.master))
	}
	return cat
}

// ResolveResticSnapshot picks a restic/remote backup via the shared
// RestoreSelector + backup catalog (PresetResticRemoteFetch), for the restic
// reseed request-builder (handlerMuxServerReseedRestic, server/api_database.go)
// to auto-select a snapshot when the operator does not pin one explicitly.
// Exported: called from the server package, across the same boundary as
// ListValidatedSelectors/AddValidatedSelector. tool may be empty (no tool
// gate). Returns "" when the resolver has no remote/restic pick — the
// caller then requires an explicit snapshot ID, same idiom as
// resolveLogicalBackupSource/resolvePhysicalBackupSource returning ("", nil)
// for their opposite (local-only) case.
func (cluster *Cluster) ResolveResticSnapshot(method, tool string) string {
	sel := PresetResticRemoteFetch()
	if method != "" {
		sel.Type = []string{method}
	}
	if tool != "" {
		sel.Tool = []string{tool}
	}
	pick := ResolveRestore(cluster.buildBackupCatalog(), sel, ResolveContext{})
	if pick == nil || pick.isLocal() {
		return ""
	}
	// The catalog carries restic snapshot IDs from historical metadata
	// (BackupMetaMap/LastBackupMeta), independent of whether that snapshot
	// still exists in the restic repo -- a purge (PurgeTask) can remove it
	// while the old metadata record (and thus the catalog row) remains.
	// Verify the pick is still actually resolvable before handing it back,
	// so handlerMuxServerReseedRestic doesn't auto-select an ID that's
	// guaranteed to fail "Snapshot with given ID not found" for something
	// the operator never chose themselves.
	if cluster.ResticManager == nil || cluster.ResticManager.GetSnapshot(pick.Path) == nil {
		return ""
	}
	// Existing != ready: a catalog entry's Kind/Tool can come straight from
	// BackupMetaMap (backupMetaToCatalog) or from SummarizeSnapshotMetadata's
	// own independent path-prefix matching against BackupMetaMap
	// (BuildSnapshotMetadataIndex) -- neither consults the separate
	// fetch/extraction status cache handlerMuxServerReseedRestic gates on
	// via RequireSnapshotMetadataReady (populated by
	// scheduleSnapshotMetadataExtraction). So a snapshot can be a fully
	// well-formed catalog candidate while its metadata extraction is still
	// pending/unknown/failed, and would otherwise reach the handler only to
	// be rejected there with a 409 for a pick the operator never made.
	if cluster.RequireSnapshotMetadataReady(pick.Path) != nil {
		return ""
	}
	return pick.Path
}

// buildBackupCatalog assembles the unified backup catalog the RestoreSelector
// runs against, merging every backup source repman tracks: each server's full
// BackupMetaMap history (not just the last-per-kind pointer), Restic
// snapshots (via the persisted snapshot-metadata index, falling back to a
// best-effort row for snapshots that index no longer covers), and on-disk
// backup files/dirs with no surviving metadata pointer at all. One
// deduplicated row per real backup.
func (cluster *Cluster) buildBackupCatalog() []BackupCatalogEntry {
	cat := make([]BackupCatalogEntry, 0, len(cluster.Servers)*2)
	seenResticIDs := make(map[string]bool)
	seenKeys := make(map[string]bool)

	add := func(e BackupCatalogEntry) {
		if e.Path != "" {
			// Kind+Tool+Path: a single restic snapshot can resolve to more than one
			// SnapshotMetadataSummary (SummarizeSnapshotMetadata dedups by
			// method+line, not by tool), so two summaries for the same snapshot can
			// share Kind but differ in Tool — a real hard-gate dimension. Path alone
			// (or Kind+Path) would silently collapse those into one row.
			key := e.Kind + "|" + e.Tool + "|" + e.Path
			if seenKeys[key] {
				return
			}
			seenKeys[key] = true
		}
		cat = append(cat, e)
	}
	track := func(m *backupmgr.BackupMetadata) {
		if m == nil {
			return
		}
		if id := strings.TrimSpace(m.ResticSnapshotID); id != "" {
			// Plain ID key: used by enumerateResticSnapshotEntries's orphaned
			// (no metadata-index summaries) fallback, which carries no
			// Kind/Tool to differentiate against anyway. ID+kind+tool key:
			// used by its per-summary branch, so tracking ONE line of a
			// multi-line snapshot (e.g. a mysqldump BackupMetadata row) does
			// not hide a DIFFERENT line (e.g. an untracked mydumper summary
			// for the same snapshot ID).
			seenResticIDs[id] = true
			seenResticIDs[id+"|"+backupMethodToString(m.BackupMethod)+"|"+strings.ToLower(strings.TrimSpace(m.BackupTool))] = true
		}
	}

	for _, sv := range cluster.Servers {
		if sv == nil {
			continue
		}
		if m := sv.LastBackupMeta.Logical; m != nil && m.Completed {
			add(backupMetaToCatalog(sv.URL, m))
			track(m)
		}
		if m := sv.LastBackupMeta.Physical; m != nil && m.Completed {
			add(backupMetaToCatalog(sv.URL, m))
			track(m)
		}
	}

	// Full tracked backup history, not just the last per server/kind — cheap
	// since BackupMetaMap is already resident in memory (one Range() call).
	if cluster.BackupMetaMap != nil {
		cluster.BackupMetaMap.Range(func(_, value any) bool {
			m, ok := value.(*backupmgr.BackupMetadata)
			if !ok || m == nil || !m.Completed {
				return true
			}
			add(backupMetaToCatalog(m.Source, m))
			track(m)
			return true
		})
	}

	// Restic snapshots not already represented via tracked metadata.
	for _, e := range cluster.enumerateResticSnapshotEntries(seenResticIDs) {
		add(e)
	}

	// On-disk backups with no surviving metadata pointer at all.
	for _, sv := range cluster.candidateBackupServers() {
		for _, e := range enumerateOnDiskBackups(sv) {
			add(e)
		}
	}

	return cat
}

// candidateBackupServers returns every server worth scanning on disk for
// backups. This must be the FULL topology, not just master/backup-server/
// has-LastBackupMeta: enumerateOnDiskBackups exists specifically to catch
// backups with NO surviving metadata pointer, and a non-master, non-backup-
// server node with an orphaned on-disk backup and no LastBackupMeta is
// exactly the case that gating would silently miss. GetMaster/GetBackupServer
// are included defensively (a virtual master, in particular, may not
// literally be a member of cluster.Servers) — de-duplicated by URL.
func (cluster *Cluster) candidateBackupServers() []*ServerMonitor {
	seen := make(map[string]bool)
	var out []*ServerMonitor
	consider := func(sv *ServerMonitor) {
		if sv == nil || seen[sv.URL] {
			return
		}
		seen[sv.URL] = true
		out = append(out, sv)
	}
	consider(cluster.GetMaster())
	consider(cluster.GetBackupServer())
	for _, sv := range cluster.Servers {
		consider(sv)
	}
	return out
}

// enumerateResticSnapshotEntries returns one BackupCatalogEntry per
// (snapshot, kind, tool) line not already represented by a tracked
// BackupMetadata row (per seenResticIDs). Snapshots with a resolvable
// metadata summary get full fields; snapshots whose metadata has since been
// pruned (or was never captured) are synthesized with best-effort fields —
// no Kind/Tool/Server — which gives them reduced selector applicability.
// That degradation is logged, not treated as an error.
//
// Dedup is per-line, not per-snapshot-ID: a single restic snapshot can carry
// more than one SnapshotMetadataSummary (e.g. a mysqldump line and a
// separate mydumper line taken together), and tracking a BackupMetadata row
// for only ONE of those lines must not hide the others — so the multi-
// summary branch below checks seenResticIDs[id+"|"+kind+"|"+tool] per
// summary, not the coarse snap.Id-only key. The single-row orphaned
// fallback (no index summaries at all) has no Kind/Tool to key on, so it
// still uses the plain snap.Id check: any tracked row for that ID already
// gives a more precise catalog entry than this best-effort one would.
func (cluster *Cluster) enumerateResticSnapshotEntries(seenResticIDs map[string]bool) []BackupCatalogEntry {
	snapshots := cluster.GetSnapshots()
	if len(snapshots) == 0 {
		return nil
	}
	index := cluster.BuildSnapshotMetadataIndex(snapshots)
	var entries []BackupCatalogEntry
	for i := range snapshots {
		snap := &snapshots[i]
		if snap.Id == "" {
			continue
		}
		if summaries := index[snap.Id]; len(summaries) > 0 {
			for _, summary := range summaries {
				kind := strings.ToLower(strings.TrimSpace(summary.BackupMethod))
				tool := strings.ToLower(strings.TrimSpace(summary.BackupTool))
				if seenResticIDs[snap.Id+"|"+kind+"|"+tool] {
					continue
				}
				entries = append(entries, cluster.snapshotSummaryToCatalog(snap, summary))
			}
			continue
		}
		if seenResticIDs[snap.Id] {
			continue
		}
		ts, _ := time.Parse(time.RFC3339Nano, snap.Time)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg,
			"Restic snapshot %s has no matching backup metadata; adding to catalog with reduced selector applicability", snap.ShortId)
		entries = append(entries, BackupCatalogEntry{
			Location:  RepoRemote,
			Timestamp: ts.Unix(),
			Path:      snap.Id,
		})
	}
	return entries
}

// snapshotSummaryToCatalog maps a restic snapshot plus its resolved metadata
// summary onto a catalog row. Position fields (Gtid/BinFile/BinPos) aren't
// tracked by SnapshotMetadataSummary today, so they're left zero — such rows
// vacuously pass TimeNotAfterHead and fail TimeNotBeforeOldest, an accepted
// limitation of restic-only visibility (same tradeoff documented on
// enumerateOnDiskBackups for bare directory entries).
func (cluster *Cluster) snapshotSummaryToCatalog(snap *backupmgr.BackupSnapshot, summary *SnapshotMetadataSummary) BackupCatalogEntry {
	ts := summary.EndTime.Unix()
	if ts <= 0 {
		ts = summary.StartTime.Unix()
	}
	if ts <= 0 {
		if parsed, err := time.Parse(time.RFC3339Nano, snap.Time); err == nil {
			ts = parsed.Unix()
		}
	}
	var caps []string
	if summary.Compressed {
		caps = append(caps, "compress")
	}
	return BackupCatalogEntry{
		Server:    cluster.resolveServerURLForDest(summary.Dest),
		Location:  RepoRemote,
		Kind:      summary.BackupMethod,
		Tool:      summary.BackupTool,
		Caps:      caps,
		Timestamp: ts,
		Path:      snap.Id,
	}
}

// resolveServerURLForDest maps a backup destination path back to the server
// it was taken from, by prefix-matching against each server's (non-creating)
// backup directory path. Returns "" if no server matches (e.g. the server has
// since left the topology) — the entry still resolves, just without an
// Origin preference to rank on.
func (cluster *Cluster) resolveServerURLForDest(dest string) string {
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return ""
	}
	for _, sv := range cluster.Servers {
		if sv == nil {
			continue
		}
		if strings.HasPrefix(dest, sv.GetMyBackupDirectoryPath()) {
			return sv.URL
		}
	}
	return ""
}

// onDiskBackupPattern recognizes one on-disk backup naming convention this
// codebase writes (see backup_helpers.go's resolveMysqldumpDest and
// srv_job_backup.go's physical/mydumper/dumpling dest-building) so a bare
// file/dir listing — no surviving meta.json pointer — can still be mapped to
// a catalog row. The single optional capture group is the ad-hoc "<ts>"
// timestamp those call sites splice into the name between the base and its
// extension (e.g. "mysqldump.1700000000.sql.gz").
type onDiskBackupPattern struct {
	re   *regexp.Regexp
	kind string
	tool string
	caps []string
}

func onDiskBackupPatterns(physicalTool string) []onDiskBackupPattern {
	patterns := []onDiskBackupPattern{
		{re: regexp.MustCompile(`^mysqldump(?:\.(\d+))?\.sql\.gz$`), kind: "logical", tool: config.ConstBackupLogicalTypeMysqldump},
		{re: regexp.MustCompile(`^splitdump(?:\.(\d+))?$`), kind: "logical", tool: config.ConstBackupLogicalTypeMysqldump, caps: []string{"can-partial-restorable"}},
		{re: regexp.MustCompile(`^mydumper(?:\.(\d+))?$`), kind: "logical", tool: config.ConstBackupLogicalTypeMydumper, caps: []string{"can-partial-restorable"}},
		{re: regexp.MustCompile(`^dumpling(?:\.(\d+))?$`), kind: "logical", tool: config.ConstBackupLogicalTypeDumpling},
	}
	if physicalTool != "" {
		patterns = append(patterns, onDiskBackupPattern{
			re:   regexp.MustCompile(`^` + regexp.QuoteMeta(physicalTool) + `(?:\.(\d+))?\.xbtream(?:\.gz)?$`),
			kind: "physical",
			tool: physicalTool,
		})
	}
	return patterns
}

// enumerateOnDiskBackups lists a server's backup directory for payloads that
// have no surviving LastBackupMeta/BackupMetaMap pointer (deleted/corrupted
// meta.json, or a backup copied in by hand). Best-effort only: Timestamp
// comes from the ad-hoc "<ts>" filename component when present, else file
// mtime; Gtid/BinFile/BinPos are left zero (see snapshotSummaryToCatalog for
// the same documented tradeoff). Uses the non-creating path getter — this is
// read-only enumeration and must not create backup directories as a side
// effect of building the catalog.
func enumerateOnDiskBackups(server *ServerMonitor) []BackupCatalogEntry {
	if server == nil {
		return nil
	}
	cluster := server.ClusterGroup
	dir := server.GetMyBackupDirectoryPath()
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	patterns := onDiskBackupPatterns(cluster.Conf.BackupPhysicalType)

	var out []BackupCatalogEntry
	for _, de := range dirEntries {
		name := de.Name()
		for _, p := range patterns {
			m := p.re.FindStringSubmatch(name)
			if m == nil {
				continue
			}
			var ts int64
			if m[1] != "" {
				ts, _ = strconv.ParseInt(m[1], 10, 64)
			} else if info, err := de.Info(); err == nil {
				ts = info.ModTime().Unix()
			}
			out = append(out, BackupCatalogEntry{
				Server:    server.URL,
				Location:  RepoLocal,
				Kind:      p.kind,
				Tool:      p.tool,
				Caps:      p.caps,
				Timestamp: ts,
				Path:      filepath.Join(dir, name),
			})
			break
		}
	}
	return out
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
		Meta:      m,
	}
}
