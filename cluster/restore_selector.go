// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

// Package cluster — generic restore selector.
//
// One declarative selector replaces every hand-rolled backup lookup in the
// rejoin/reseed paths. A restore = filter the known backups by the HARD GATES
// (type, tool, time, proven) → rank the survivors by the PREFERENCES
// (origin, repo, safety, order) → return the best available.
//
// See doc/implementation/cluster/RESTORE_SELECTOR.md for the full model.
package cluster

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/utils/backupmgr"
)

// ---- selector value constants ----

const (
	// origin
	OriginAny    = "any"
	OriginMaster = "master"
	OriginMine   = "mine"

	// repo
	RepoAny    = "any"
	RepoLocal  = "local"
	RepoRemote = "remote"
	RepoLive   = "live"

	// time
	TimeAny             = "any"
	TimeNotAfterHead    = "notafterlastmasterbinlog" // position not beyond the master's head
	TimeNotBeforeOldest = "nobeforemasterbinlog"     // position not before the master's oldest live binlog
	TimePITR            = "pitr"                      // restore + binlog replay toward StartGtid/StartTime
)

// RestoreSelector declares WHAT backup to restore from. Grouped by field type.
//
// Hard gates (a mismatch is excluded, never relaxed): Type, Tool, Time, Proven.
// Ranking preferences (relax to best-available): Origin, Repo, Safety, Order.
type RestoreSelector struct {
	// bool
	Proven bool `json:"proven,omitempty"` // GATE: require a backup validated by test-restore (NEW: needs a verify mechanism)

	// []string — GATE whitelists: empty = none (that's what makes them gate)
	Type []string `json:"type,omitempty"` // logical | physical | parallel | compress | encrypt | gtid | can-partial-restorable
	Tool []string `json:"tool,omitempty"` // mysqldump | mariadump | mariadbbackup | xtrabackup | innodbhotbackup | mydumper

	// []string — ranking preferences: empty = any (no preference)
	Safety []string `json:"safety,omitempty"` // preserve{dbdisk,repmandisk,network,masterload,masterlock,cpu,diskio,memory} (combinable)
	Order  []string `json:"order,omitempty"`  // e.g. ["last","local"] or ["logical","last"]

	// string
	Origin    string `json:"origin,omitempty"`    // any | master | mine
	Repo      string `json:"repo,omitempty"`      // any | local | remote | live
	Time      string `json:"time,omitempty"`      // any | notafterlastmasterbinlog | nobeforemasterbinlog | pitr
	StartGtid string `json:"startGtid,omitempty"` // GTID boundary — PITR target AND backup filtering (not pitr-only)

	// time.Time
	StartTime time.Time `json:"startTime,omitempty"` // time boundary — PITR target AND backup filtering (not pitr-only)
}

// ---- method presets (doc/implementation/cluster/RESTORE_SELECTOR.md's
// "Method presets" table). Each returns a fresh value — never a shared var —
// so a caller can safely mutate .Type/.Tool after getting one back (the
// existing sel := ...; sel.Tool = [...] pattern already used at call sites).

// PresetRejoinLogical: any origin/repo, logical backups only, not beyond the
// master's binlog head, newest-then-local as the tie-break.
func PresetRejoinLogical() RestoreSelector {
	return RestoreSelector{
		Origin: OriginAny,
		Repo:   RepoAny,
		Type:   []string{"logical"},
		Time:   TimeNotAfterHead,
		Safety: []string{"any"},
		Order:  []string{"last", "local"},
	}
}

// PresetReseedFromMaster: a live mysqldump straight from the master, no
// safety constraint.
func PresetReseedFromMaster() RestoreSelector {
	return RestoreSelector{
		Origin: OriginMaster,
		Repo:   RepoLive,
		Safety: []string{"any"},
	}
}

// PresetReseedSpareMaster: the same live-from-master reseed, but ranked to
// avoid the master's network/load — relaxes to a stored backup instead when
// one is available.
func PresetReseedSpareMaster() RestoreSelector {
	return RestoreSelector{
		Origin: OriginMaster,
		Repo:   RepoLive,
		Safety: []string{"preservenetwork"},
	}
}

// PresetResticRemoteFetch: pick a backup from the restic/remote repo.
func PresetResticRemoteFetch() RestoreSelector {
	return RestoreSelector{
		Repo: RepoRemote,
	}
}

// PresetRejoinPhysical: physical backups only, newest-then-local as the
// tie-break.
func PresetRejoinPhysical() RestoreSelector {
	return RestoreSelector{
		Type:  []string{"physical"},
		Order: []string{"last", "local"},
	}
}

// BackupCatalogEntry is one row of the unified backup catalog: any backup we
// took, local or repo, self-describing so every selector dimension can be
// evaluated against it.
type BackupCatalogEntry struct {
	Server    string   // node it was taken from            -> origin
	Location  string   // local | remote (repo)             -> repo
	Kind      string   // logical | physical                -> type
	Tool      string   // mysqldump | mydumper | mariadbbackup | ...  -> tool
	Caps      []string // parallel, compress, encrypt, gtid, can-partial-restorable
	Proven    bool     // validated by a test-restore (NEW mechanism sets it)
	Timestamp int64    // unix seconds                       -> order "last"
	Gtid      string   // captured position (master-relative) -> time gates
	BinFile   string
	BinPos    string
	Path      string // local path or repo/snapshot id

	// IsDirectory is the one true "single file vs multi-file/directory"
	// layout signal, derived via isDirectoryBackupLayout (restore_catalog.go)
	// from Tool + the source's SplitDump flag -- NOT inferred from Tool
	// alone. Tool=="mysqldump" cannot by itself distinguish a genuine
	// single-file dump from a splitdump-mode one (srv_job_backup.go:3184
	// sets SplitDump as a bool orthogonal to BackupTool). Both
	// ResolveResticSnapshot's requireSingleFile and
	// resolveResticReseedStrategy's dump-strategy auto-selection
	// (srv_job_restic.go) must consult this field rather than each
	// independently re-deriving (and potentially disagreeing about) layout
	// from Tool.
	IsDirectory bool

	// Meta, when non-nil, is the exact BackupMetadata record this row was
	// derived from (set only for BackupMetaMap/LastBackupMeta-sourced
	// entries in backupMetaToCatalog; restic-summary and on-disk-enumerated
	// entries have no full BackupMetadata to reference, and leave this nil).
	//
	// Consumers that need restore parameters the catalog doesn't itself
	// model (SplitUser, etc.) MUST prefer this over independently re-reading
	// a server's LastBackupMeta.Logical/Physical: buildBackupCatalog can
	// pick an OLDER BackupMetaMap entry than a server's current
	// LastBackupMeta (e.g. the newest backup excluded by a TimeNotAfterHead
	// gate), and re-reading LastBackupMeta would silently apply a DIFFERENT
	// backup's parameters to the one actually selected. Callers should fall
	// back to the old LastBackupMeta-based lookup only when Meta is nil.
	Meta *backupmgr.BackupMetadata
}

func (e BackupCatalogEntry) isLocal() bool { return e.Location == "" || e.Location == RepoLocal }

// ResolveContext carries the live facts the resolver needs beyond the catalog:
// the master's binlog window (for the time gates) and which URLs count as
// "master"/"mine" (for the origin preference).
type ResolveContext struct {
	OldestGtid string // oldest GTID the master still serves
	HeadGtid   string // the master's current GTID head
	MasterURL  string // resolves origin=master
	TargetURL  string // resolves origin=mine (the node being reseeded)
}

// masterHeadGtidString returns the master's current GTID head for
// ResolveContext.HeadGtid, or "" when unknown. gtid.List.Sprint has a value
// receiver, so calling it through a nil *gtid.List (early cluster discovery,
// or a master that hasn't captured a GTID) panics — guard it here rather than
// at every call site.
func masterHeadGtidString(master *ServerMonitor) string {
	if master == nil || master.GTIDBinlogPos == nil {
		return ""
	}
	return master.GTIDBinlogPos.Sprint()
}

// ResolveRestore returns the best backup for the selector, or nil if nothing
// passes the hard gates. Never errors on a preference — it relaxes and ranks.
func ResolveRestore(catalog []BackupCatalogEntry, sel RestoreSelector, ctx ResolveContext) *BackupCatalogEntry {
	cands := make([]BackupCatalogEntry, 0, len(catalog))
	for _, e := range catalog {
		if gatePass(sel, e, ctx) {
			cands = append(cands, e)
		}
	}
	if len(cands) == 0 {
		return nil
	}
	slices.SortFunc(cands, func(a, b BackupCatalogEntry) int { return rankCmp(sel, ctx, a, b) })
	best := cands[0]
	return &best
}

// ---- hard gates (exclude, never relax) ----

func gatePass(sel RestoreSelector, e BackupCatalogEntry, ctx ResolveContext) bool {
	// proven — a boolean gate
	if sel.Proven && !e.Proven {
		return false
	}
	// type — AND: the entry must satisfy EVERY requested kind/cap (empty = none required)
	for _, t := range sel.Type {
		if !entryHasType(e, t) {
			return false
		}
	}
	// tool — membership: the entry's tool must be one of the allowed (empty = none required)
	if len(sel.Tool) > 0 && !slices.Contains(sel.Tool, e.Tool) {
		return false
	}
	// time — replayability window vs the master's binlog range
	return timeGatePass(sel, e, ctx)
}

// entryHasType reports whether the entry satisfies one `type` value — either its
// Kind (logical/physical) or one of its Caps (parallel/compress/encrypt/gtid/…).
func entryHasType(e BackupCatalogEntry, t string) bool {
	if e.Kind == t {
		return true
	}
	return slices.Contains(e.Caps, t)
}

func timeGatePass(sel RestoreSelector, e BackupCatalogEntry, ctx ResolveContext) bool {
	switch sel.Time {
	case "", TimeAny, TimePITR:
		// pitr reaches any older backup by replaying archived binlogs forward, so
		// it does not exclude on the window here (the replay step handles it).
		return true
	case TimeNotAfterHead:
		return gtidCmp(e.Gtid, ctx.HeadGtid) <= 0
	case TimeNotBeforeOldest:
		return gtidCmp(e.Gtid, ctx.OldestGtid) >= 0
	default:
		return true
	}
}

// gtidCmp compares two MariaDB GTID strings by sequence, returning -1/0/1.
// First-cut: single-domain compare on the seq of the highest domain-server
// element. TODO(restore-selector): use utils/gtid for full multi-domain
// ordering before trusting the window gates in production.
func gtidCmp(a, b string) int {
	sa, sb := maxGtidSeq(a), maxGtidSeq(b)
	switch {
	case sa < sb:
		return -1
	case sa > sb:
		return 1
	default:
		return 0
	}
}

func maxGtidSeq(g string) uint64 {
	var max uint64
	for _, part := range strings.Split(g, ",") {
		f := strings.Split(strings.TrimSpace(part), "-")
		if len(f) == 0 {
			continue
		}
		if seq, err := strconv.ParseUint(f[len(f)-1], 10, 64); err == nil && seq > max {
			max = seq
		}
	}
	return max
}

// ---- ranking (relax to best-available) ----

// rankCmp orders two survivors: Order keys first (in listed priority), then the
// origin/repo/safety preferences as tie-breakers. Returns <0 if a should sort
// before b (a is the better pick).
func rankCmp(sel RestoreSelector, ctx ResolveContext, a, b BackupCatalogEntry) int {
	for _, k := range sel.Order {
		if c := orderCmp(k, a, b); c != 0 {
			return c
		}
	}
	if c := prefCmp(sel, ctx, a, b); c != 0 {
		return c
	}
	// stable final tie-break: newest first
	return int(b.Timestamp - a.Timestamp)
}

func orderCmp(key string, a, b BackupCatalogEntry) int {
	switch key {
	case "last": // newest first
		return int(b.Timestamp - a.Timestamp)
	case "local": // local before remote
		return boolPref(a.isLocal(), b.isLocal())
	case "logical", "physical": // matching Kind first
		return boolPref(a.Kind == key, b.Kind == key)
	default:
		// unknown order key: prefer entries advertising it as a cap
		return boolPref(slices.Contains(a.Caps, key), slices.Contains(b.Caps, key))
	}
}

// prefCmp scores the origin/repo/safety preferences — each favours some
// candidates but never excludes (the "ranks, not gates" rule).
func prefCmp(sel RestoreSelector, ctx ResolveContext, a, b BackupCatalogEntry) int {
	if sel.Origin != "" && sel.Origin != OriginAny {
		if c := boolPref(originMatch(sel, ctx, a), originMatch(sel, ctx, b)); c != 0 {
			return c
		}
	}
	if sel.Repo != "" && sel.Repo != RepoAny {
		if c := boolPref(repoMatch(sel.Repo, a), repoMatch(sel.Repo, b)); c != 0 {
			return c
		}
	}
	if len(sel.Safety) > 0 {
		if c := safetyScore(sel, ctx, b) - safetyScore(sel, ctx, a); c != 0 {
			return c
		}
	}
	return 0
}

func originMatch(sel RestoreSelector, ctx ResolveContext, e BackupCatalogEntry) bool {
	switch sel.Origin {
	case OriginMaster:
		return e.Server == ctx.MasterURL
	case OriginMine:
		return e.Server == ctx.TargetURL
	default:
		return true
	}
}

func repoMatch(repo string, e BackupCatalogEntry) bool {
	if repo == RepoLocal {
		return e.isLocal()
	}
	return e.Location == repo
}

// safetyScore is a coarse first-cut: local backups avoid network, stored
// backups (or a live dump sourced from something other than the master)
// avoid master load. Refined once the strategy costs are modelled.
//
// isLiveFromMaster is what lets preservemasterload/preservemasterlock
// discriminate between the two "live" candidates a direct-dump rejoin
// resolves over (see liveDumpCatalog/resolveLiveDumpSource,
// srv_rejoin.go): both share Location==RepoLive, so Repo alone can't tell
// a live dump FROM the master apart from one sourced from the designated
// backup replica — only ctx.MasterURL + the entry's Server can.
func safetyScore(sel RestoreSelector, ctx ResolveContext, e BackupCatalogEntry) int {
	s := 0
	isLiveFromMaster := e.Location == RepoLive && ctx.MasterURL != "" && e.Server == ctx.MasterURL
	for _, tag := range sel.Safety {
		switch tag {
		case "preservenetwork":
			if e.isLocal() {
				s++
			}
		case "preservemasterload", "preservemasterlock":
			if !isLiveFromMaster { // a stored backup, or a live dump NOT sourced from the master, does not touch the live master
				s++
			}
		}
	}
	return s
}

func boolPref(a, b bool) int {
	switch {
	case a && !b:
		return -1
	case !a && b:
		return 1
	default:
		return 0
	}
}
