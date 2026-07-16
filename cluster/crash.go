// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//
//	Stephane Varoqui  <svaroqui@gmail.com>
//
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/gtid"
)

// Crash will store informations on a crash based on the replication stream
// swagger:response crash
type Crash struct {
	URL                         string
	FailoverMasterLogFile       string
	FailoverMasterLogPos        string
	NewMasterLogFile            string
	NewMasterLogPos             string
	FailoverSemiSyncSlaveStatus bool
	FailoverIOGtid              *gtid.List
	// FailoverIOGtidString is the election GTID in its usable text form
	// ("domain-server-seq"), the anchor for logical recovery of the diverged
	// old master: SET GLOBAL gtid_slave_pos = "<this>" then CHANGE MASTER …
	// master_use_gtid=slave_pos. Backfilled from FailoverIOGtid on read so it
	// survives clusterstate.json reloads.
	FailoverIOGtidString string `json:"failoverIOGtidString"`
	ElectedMasterURL     string
	UnixTimestamp               int64
	Switchover                  bool
	// Lost-events delta: captured from the diverged old master at rejoin and
	// analyzed (srv_lostevents.go). The verdict decides the recovery path.
	DeltaArchive          string `json:"deltaArchive"`
	DeltaDecoded          string `json:"deltaDecoded"`
	DeltaFlashbackDecoded string `json:"deltaFlashbackDecoded"`
	DeltaAnalyzed         bool   `json:"deltaAnalyzed"`
	DeltaFlashable        bool   `json:"deltaFlashable"`
	DeltaTransactions     int    `json:"deltaTransactions"`
	DeltaRowEvents        int    `json:"deltaRowEvents"`
	DeltaDDL              int    `json:"deltaDdl"`
	DeltaStatementDML     int    `json:"deltaStatementDml"`
	// RejoinResult is stamped when the rejoin EXECUTION ends: it moves the crash
	// out of the working set into history carrying WHY it ended. This is the loop
	// terminator — a crash with a result is done and never re-drives automatically
	// (an explicit copy history->working set re-arms exactly one more attempt).
	RejoinResult   string `json:"rejoinResult"`
	RejoinResultTs int64  `json:"rejoinResultTs"`
	// ArchiveDir is THIS crash's own crash-bin-<ts> directory — the single on-disk
	// home for the event (crash.json metadata + binlog delta once captured). Set
	// the moment the crash is known (failover on the majority, or peer-materialize
	// on the minority) so the local disk reflects the crash immediately; saveBinlog
	// fills the binlog INTO this same dir and finishRejoin stamps the result here.
	ArchiveDir string `json:"archiveDir"`
	// RejoinMethod is the operator's CHOSEN recovery method for the next (re-armed)
	// attempt, set from the GUI delta viewer via rearmRejoin. When non-empty it
	// OVERRIDES the automatic flashback/SST cascade for that one attempt, then is
	// cleared by finishRejoin. "" = automatic.
	RejoinMethod string `json:"rejoinMethod"`
}

// Operator-chosen rejoin methods (Crash.RejoinMethod), from the GUI delta viewer.
// All are runnable on ANY crash — the delta verdict informs, it does not gate.
const (
	RejoinMethodFlashback    = "flashback"          // rejoinMasterFlashBack
	RejoinMethodLogicalDump  = "logical-dump"        // RejoinDirectDump (mysqldump from master)
	RejoinMethodLogicalBkp   = "logical-backup"      // JobFlashbackLogicalBackup
	RejoinMethodPhysicalBkp  = "physical-backup"     // JobFlashbackPhysicalBackup
	RejoinMethodIgnoreForce  = "ignore-delta-force"  // discard a divergent tail, force re-slave (data loss)
	RejoinMethodResetReslave = "reset-master-reslave" // RESET MASTER on the failed slave + re-slave: clears a
	//                                                   stuck GTID/binlog position (e.g. strict-mode out-of-order
	//                                                   SlaveErr) and restarts clean replication. The manual repair.
	RejoinMethodBootstrapFTWRL = "bootstrap-repli-ftwrl" // re-bootstrap the master-slave topology, taking a short
	//                                                      FTWRL on the master before RESET MASTER. UNSAFE: it
	//                                                      briefly locks the master. Calls BootstrapReplication.
	RejoinMethodScript = "rejoin-script" // run the operator's custom autorejoin-script (direct exec). The only
	//                                      always-available custom method; behaviour is whatever the script does.
)

// Rejoin method classes for the GUI: two axes, data-safety × duration. "safe"
// reconciles/rewinds without discarding good data; "unsafe" discards the diverged
// tail or resets/locks the master. "short" is near-instant (rewind/reset/script);
// "long" reseeds a full dataset from a dump or backup.
const (
	RejoinClassSafeShort   = "safe-short"
	RejoinClassSafeLong    = "safe-long"
	RejoinClassUnsafeShort = "unsafe-short"
	RejoinClassUnsafeLong  = "unsafe-long"
)

// RejoinMethodClass returns the safety×duration class of a rejoin method (see the
// RejoinClass* constants), used by the GUI to group the operator methods.
func RejoinMethodClass(method string) string {
	switch method {
	case RejoinMethodFlashback, RejoinMethodScript:
		return RejoinClassSafeShort
	case RejoinMethodLogicalDump, RejoinMethodLogicalBkp, RejoinMethodPhysicalBkp:
		return RejoinClassSafeLong
	case RejoinMethodIgnoreForce, RejoinMethodBootstrapFTWRL, RejoinMethodResetReslave:
		return RejoinClassUnsafeShort
	}
	return ""
}

// Rejoin result codes (Crash.RejoinResult). "" = not yet attempted.
const (
	RejoinResultSuccess       = "success"            // re-slaved under the elected master
	RejoinResultNoDivergence  = "no-divergence"      // no crash/no delta: re-slaved on current GTID
	RejoinResultNotFlashback  = "not-flashback-able" // diverged tail is DDL/statement: manual repair
	RejoinResultNoMethod      = "no-rejoin-method"   // not flashback-able and no SST method armed
	RejoinResultPeerUnreached = "peer-unreachable"   // minority could not fetch the verdict (transient split)
	RejoinResultFailed        = "failed"             // generic execution failure
)

// Collection of Crash reports
// swagger:response crashList
type crashList []*Crash

func (clist *crashList) Purge(keep int) {
	if keep <= 0 {
		*clist = nil
		return
	}
	if len(*clist) > keep {
		*clist = (*clist)[len(*clist)-keep:]
	}
}

func (clist *crashList) StoreLastN(cr *Crash, keep int) {
	*clist = append(*clist, cr)
	clist.Purge(keep)
}

func (clist *crashList) GetLatest() *Crash {
	size := len(*clist)
	if size < 1 {
		return nil
	}

	return (*clist)[size-1]
}

func (cluster *Cluster) newCrash(*Crash) (*Crash, error) {
	crash := new(Crash)
	crash.UnixTimestamp = time.Now().Unix()
	return crash, nil
}

// ensureCrashArchive creates THIS crash's own crash-bin-<ts> dir (if it does not
// have one yet) and writes its crash.json metadata — so a crash is on local disk
// the moment it is known (option B), synchronized before any rejoin. Idempotent:
// if the crash already has an ArchiveDir it just rewrites crash.json there.
// Returns the dir. Uses the crash's UnixTimestamp for the name so the same event
// maps to the same dir on any node.
func (cluster *Cluster) ensureCrashArchive(crash *Crash) string {
	if crash == nil {
		return ""
	}
	if crash.ArchiveDir == "" {
		ts := crash.UnixTimestamp
		if ts == 0 {
			ts = time.Now().Unix()
		}
		crash.ArchiveDir = cluster.WorkingDir + "/crash-bin-" + time.Unix(ts, 0).Format("20060102150405")
	}
	if err := os.MkdirAll(crash.ArchiveDir, 0777); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not create crash archive dir %s: %s", crash.ArchiveDir, err)
		return crash.ArchiveDir
	}
	if err := crash.Save(crash.ArchiveDir + "/crash.json"); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not write crash metadata %s/crash.json: %s", crash.ArchiveDir, err)
	}
	cluster.pruneCrashArchives(cluster.Conf.FailoverLogFileKeep)
	return crash.ArchiveDir
}

// pruneCrashArchives bounds crash-bin dirs to the <keep> most recent (dir name is
// timestamped, sorts chronologically) — the archive AND its crash.json go as a
// unit, keeping history synchronized with disk.
func (cluster *Cluster) pruneCrashArchives(keep int) {
	if keep <= 0 {
		keep = 3
	}
	entries, err := os.ReadDir(cluster.WorkingDir)
	if err != nil {
		return
	}
	var archives []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "crash-bin-") {
			archives = append(archives, e.Name())
		}
	}
	if len(archives) <= keep {
		return
	}
	sort.Strings(archives)
	for _, name := range archives[:len(archives)-keep] {
		if err := os.RemoveAll(cluster.WorkingDir + "/" + name); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not prune crash archive %s: %s", name, err)
			continue
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Pruned crash archive %s (keeping the %d most recent)", name, keep)
	}
}

// finishRejoin is the LOOP TERMINATOR: it ends a rejoin CYCLE for url. It finds
// the working crash for url, stamps the rejoin RESULT, moves it OUT of the working
// set (cluster.Crashes) into durable history (FailoverHistory + failover.<ts>.json),
// and returns the moved record so the caller can raise the visible state. After
// this the working set has no crash for url, so nothing re-drives automatically —
// the only way to try again is rearmRejoin (an explicit copy back). If there is no
// working crash for url (the no-crash / no-divergence outcome) it still records a
// history marker carrying the result so the outcome is visible and one-shot.
func (cluster *Cluster) finishRejoin(url string, result string) *Crash {
	var moved *Crash
	kept := make([]*Crash, 0, len(cluster.Crashes))
	for _, cr := range cluster.Crashes {
		if cr != nil && cr.URL == url && moved == nil {
			moved = cr
			continue
		}
		kept = append(kept, cr)
	}
	now := time.Now()
	if moved == nil {
		// No working crash (e.g. no-divergence rejoin): synthesize a minimal
		// history marker so the outcome is durable, visible and one-shot.
		electedURL := ""
		if m := cluster.GetMaster(); m != nil {
			electedURL = m.URL
		}
		moved = &Crash{URL: url, UnixTimestamp: now.Unix(), ElectedMasterURL: electedURL}
	}
	moved.RejoinResult = result
	moved.RejoinResultTs = now.Unix()
	cluster.Crashes = kept
	// Stamp the outcome INTO the crash's own archive dir (crash-bin/crash.json) —
	// the single record for this real crash. No separate failover.<ts>.json, no
	// append: that was what printed the same crash X times. The dir already exists
	// (created when the crash became known); ensureCrashArchive rewrites crash.json
	// with the result. A crash with NO archive at all (pure no-divergence, never
	// materialized) leaves no disk record — it is not a crash.
	if moved.ArchiveDir == "" && moved.DeltaArchive != "" {
		moved.ArchiveDir = filepath.Dir(moved.DeltaArchive)
	}
	if moved.ArchiveDir != "" {
		cluster.ensureCrashArchive(moved)
	}
	// Rebuild history from disk so it reflects the real archived crashes (deduped,
	// one per dir) rather than an accumulating in-memory list.
	cluster.LoadFailoverHistory()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin of %s ended: %s", url, result)
	return moved
}

// rejoinAlreadyAttempted reports whether history already holds a rejoin OUTCOME
// for url from THIS split window — the re-fetch / re-run guard that makes the
// rejoin truly one-shot. A stale outcome from a previous split (older than
// SplitBrainStartTs) does not count, so a genuinely new event still gets its
// attempt. An explicit rearmRejoin clears the outcome to allow one more try.
func (cluster *Cluster) rejoinAlreadyAttempted(url string) bool {
	// An explicitly re-armed crash in the working set (a chosen method, no result
	// yet) OVERRIDES the one-shot: the operator asked for one more attempt. This is
	// what makes the manual rejoin runnable after an automatic attempt already
	// finished — without it, the history result below would block forever.
	for _, cr := range cluster.Crashes {
		if cr != nil && cr.URL == url && cr.RejoinMethod != "" && cr.RejoinResult == "" {
			return false
		}
	}
	for _, cr := range cluster.FailoverHistory {
		if cr == nil || cr.URL != url || cr.RejoinResult == "" {
			continue
		}
		if cluster.SplitBrainStartTs > 0 && cr.RejoinResultTs < cluster.SplitBrainStartTs {
			continue // outcome predates this split — a new event may retry
		}
		// peer-unreachable is RETRYABLE, not a terminal attempt: the verdict was
		// never obtained, so the next tick must try the fetch again (transient
		// split / peer momentarily down). All other results are terminal one-shot.
		if cr.RejoinResult == RejoinResultPeerUnreached {
			continue
		}
		return true
	}
	return false
}

// rearmRejoin is the EXPLICIT retry: copy the newest history crash for url back
// into the working set with its result cleared and the operator-chosen method set,
// granting exactly ONE more attempt with that method. This is the only way a
// finished rejoin runs again — never automatic. Wired to the GUI/API rejoin action.
// method "" means automatic. Returns false if there is no history record to re-arm.
func (cluster *Cluster) rearmRejoin(url string, method string) bool {
	var newest *Crash
	for _, cr := range cluster.FailoverHistory {
		if cr == nil || cr.URL != url {
			continue
		}
		if newest == nil || cr.UnixTimestamp >= newest.UnixTimestamp {
			newest = cr
		}
	}
	if newest == nil {
		return false
	}
	mat := *newest
	mat.RejoinResult = ""
	mat.RejoinResultTs = 0
	mat.RejoinMethod = method
	cluster.Crashes = append(cluster.Crashes, &mat)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin re-armed for %s (explicit, method=%q): copied from history to working set for one more attempt", url, method)
	return true
}

// LatestCrashURL returns the diverged-server URL of the most recent crash in the
// failover history, or "" if there is none. Resolves the default rejoin target when
// the operator triggers a cluster rejoin without naming a server.
func (cluster *Cluster) LatestCrashURL() string {
	var newest *Crash
	for _, cr := range cluster.FailoverHistory {
		if cr == nil {
			continue
		}
		if newest == nil || cr.UnixTimestamp >= newest.UnixTimestamp {
			newest = cr
		}
	}
	if newest == nil {
		return ""
	}
	return newest.URL
}

// RejoinMethodStatus is the per-method availability for the GUI: whether the
// method is POSSIBLE right now (config/resources), NOT whether it would succeed on
// this delta. The delta verdict only informs — an unavailable method is disabled
// with a reason; every method carries a state.
type RejoinMethodStatus struct {
	Method    string `json:"method"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
	Class     string `json:"class"` // safety×duration group for the GUI (RejoinMethodClass)
}

// RejoinMethodsStatus reports, for the GUI delta viewer, which operator rejoin
// methods are possible right now and why not. Gated on config/resources only —
// flashback on config, logical/physical on an existing backup, dump on a reachable
// master; reset/ignore are always possible.
func (cluster *Cluster) RejoinMethodsStatus() []RejoinMethodStatus {
	master := cluster.GetMaster()
	hasLogical := master != nil && master.HasBackupLogicalCookie()
	hasPhysical := master != nil && master.HasBackupPhysicalCookie()
	flashOK := cluster.Conf.AutorejoinFlashback && cluster.Conf.AutorejoinBackupBinlog
	masterUp := master != nil && !master.IsDown()
	hasScript := cluster.Conf.RejoinScript != ""
	// WARN0188 = the old master diverged (IO 1236) so reset-master-reslave would
	// re-slave from an invalid position on the new master — disable it (unsafe).
	diverged := false
	for _, st := range cluster.StateMachine.GetOpenStates() {
		if st.ErrKey == "WARN0188" {
			diverged = true
			break
		}
	}
	mk := func(m string, ok bool, reason string) RejoinMethodStatus {
		if ok {
			reason = ""
		}
		return RejoinMethodStatus{Method: m, Available: ok, Reason: reason, Class: RejoinMethodClass(m)}
	}
	return []RejoinMethodStatus{
		mk(RejoinMethodFlashback, flashOK, "flashback not enabled (autorejoin-flashback + autorejoin-backup-binlog)"),
		mk(RejoinMethodLogicalDump, masterUp, "master unreachable"),
		mk(RejoinMethodLogicalBkp, hasLogical, "cluster has no logical backup"),
		mk(RejoinMethodPhysicalBkp, hasPhysical, "cluster has no physical backup"),
		mk(RejoinMethodResetReslave, !diverged, "replication would point to an invalid position on the new master (old master diverged, err 1236) — use flashback or reseed"),
		mk(RejoinMethodScript, hasScript, "no autorejoin-script configured"),
		mk(RejoinMethodIgnoreForce, true, ""),
		mk(RejoinMethodBootstrapFTWRL, masterUp, "master unreachable (FTWRL + RESET MASTER needs the master)"),
	}
}

// IsValidRejoinMethod reports whether method is a known operator rejoin method
// (or "" for automatic).
func IsValidRejoinMethod(method string) bool {
	switch method {
	case "", RejoinMethodFlashback, RejoinMethodLogicalDump, RejoinMethodLogicalBkp, RejoinMethodPhysicalBkp, RejoinMethodIgnoreForce, RejoinMethodResetReslave, RejoinMethodBootstrapFTWRL, RejoinMethodScript:
		return true
	}
	return false
}

// IsUnsafeRejoinMethod reports whether a rejoin method is destructive/disruptive to
// the cluster — it either discards the diverged tail (ignore-delta-force: data loss)
// or resets/locks the master (bootstrap-repli-ftwrl: brief FTWRL on the master). These
// route through the /actions/unsafe-rejoin verb and require the cluster-rejoin-unsafe
// grant on top of cluster-failover. It is the single source of truth for which verb a
// method must use.
func IsUnsafeRejoinMethod(method string) bool {
	switch method {
	case RejoinMethodIgnoreForce, RejoinMethodBootstrapFTWRL, RejoinMethodResetReslave:
		return true
	}
	return false
}

// RearmRejoin is the exported entry (GUI/API) for an explicit operator rejoin of
// url with a chosen method. See rearmRejoin.
func (cluster *Cluster) RearmRejoin(url string, method string) bool {
	return cluster.rearmRejoin(url, method)
}

// ProcessArmedRejoins runs any operator-armed rejoin — a working-set crash carrying a
// chosen method with no result yet — via the unified RejoinMaster path. Called every
// tick and, crucially, NOT gated on Conf.Autorejoin or a Failed->up edge: the operator
// explicitly asked for it, so it must run even with auto-rejoin off and the server a
// healthy attached slave (the case where nothing else calls RejoinMaster). One-shot:
// finishRejoin records the result, after which this is a no-op until the next re-arm.
func (cluster *Cluster) ProcessArmedRejoins() {
	if !cluster.IsActive() || cluster.IsSplitBrain || cluster.StateMachine.IsInFailover() {
		return
	}
	for _, server := range cluster.Servers {
		if server == nil {
			continue
		}
		cr := cluster.getCrashFromJoiner(server.URL)
		if cr != nil && cr.RejoinMethod != "" && cr.RejoinResult == "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Operator-armed rejoin of %s via %q — running", server.URL, cr.RejoinMethod)
			// async: a reseed can run for hours/days; never block the monitor loop.
			// RejoinMaster's rejoinInProgress guard makes the per-tick spawn safe.
			go server.RejoinMaster()
		}
	}
}

func (cluster *Cluster) getCrashFromJoiner(URL string) *Crash {
	for _, cr := range cluster.Crashes {
		if cr.URL == URL {
			return cr
		}
	}
	return nil
}

func (cluster *Cluster) getCrashFromMaster(URL string) *Crash {
	for _, cr := range cluster.Crashes {
		if cr.ElectedMasterURL == URL {
			return cr
		}
	}
	return nil
}

// GetCrashes return crashes, with the election GTID backfilled into its
// usable text form so the logical recovery anchor is directly readable.
func (cluster *Cluster) GetCrashes() crashList {
	for _, cr := range cluster.Crashes {
		if cr != nil && cr.FailoverIOGtidString == "" && cr.FailoverIOGtid != nil {
			cr.FailoverIOGtidString = cr.FailoverIOGtid.Sprint()
		}
	}
	return cluster.Crashes
}

func (crash *Crash) delete(cl *crashList) {
	lsm := *cl
	for k, s := range lsm {
		if crash.URL == s.URL {
			lsm[k] = lsm[len(lsm)-1]
			lsm[len(lsm)-1] = nil
			lsm = lsm[:len(lsm)-1]
			break
		}
	}
	*cl = lsm
}

func (crash *Crash) Save(path string) error {
	saveJson, _ := json.MarshalIndent(crash, "", "\t")
	err := os.WriteFile(path, saveJson, 0644)
	if err != nil {
		return err
	}
	return nil
}

// GetLastFailoverFile returns the path of the most recent failover.<ts>.json
// crash record in dir (filenames are timestamped, so the lexical max is the
// newest), or "" if none. This is the durable crash history the GUI reads and
// the file the rejoin path re-Saves the delta verdict into.
func GetLastFailoverFile(dir string) string {
	files, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	last := ""
	for _, file := range files {
		n := file.Name()
		if strings.HasPrefix(n, "failover") && strings.HasSuffix(n, ".json") && n > last {
			last = n
		}
	}
	if last == "" {
		return ""
	}
	return dir + "/" + last
}

// LoadFailoverHistory populates FailoverHistory from the durable
// failover.<ts>.json records on disk (oldest first). These are the real crash
// HISTORY — unlike cluster.Crashes, which is the recovery working set purged
// when the cluster heals. FailoverHistory is otherwise in-memory only and
// empty after a restart; this makes the crash viewer show history across
// restarts. Read-only: it never writes or touches replication.
// LoadFailoverHistory rebuilds the crash history by scanning the crash-bin-<ts>
// archive dirs on disk and reading each dir's crash.json — so history is a pure
// reflection of the real crashes archived on disk: SYNCHRONIZED by construction,
// deduplicated (one per dir), surviving restart, pruned as a unit with its binlog.
// (Replaced the old scan of accumulating failover.<ts>.json, which drifted out of
// sync with the pruned archives — 10 json vs 3 archives, duplicates, dead links.)
func (cluster *Cluster) LoadFailoverHistory() {
	entries, err := os.ReadDir(cluster.WorkingDir)
	if err != nil {
		return
	}
	// Backward-compat: index the OLD failover.<ts>.json metadata by the crash-bin
	// dir their DeltaArchive points into, so an archive that predates crash.json
	// (its binlog + .sql are still on disk) is NOT lost — it is migrated in place.
	metaByDir := make(map[string]*Crash)
	var dirs []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() && strings.HasPrefix(n, "crash-bin-") {
			dirs = append(dirs, n)
			continue
		}
		if !e.IsDir() && strings.HasPrefix(n, "failover") && strings.HasSuffix(n, ".json") {
			if data, err := os.ReadFile(cluster.WorkingDir + "/" + n); err == nil {
				cr := new(Crash)
				if json.Unmarshal(data, cr) == nil && cr.DeltaArchive != "" {
					metaByDir[filepath.Base(filepath.Dir(cr.DeltaArchive))] = cr
				}
			}
		}
	}
	sort.Strings(dirs) // timestamped dir names sort chronologically
	var history crashList
	for _, d := range dirs {
		dirPath := cluster.WorkingDir + "/" + d
		if data, err := os.ReadFile(dirPath + "/crash.json"); err == nil {
			crash := new(Crash)
			if json.Unmarshal(data, crash) == nil {
				history = append(history, crash)
			}
			continue
		}
		// No crash.json — migrate from the matching old failover.json if we have it.
		if cr, ok := metaByDir[d]; ok {
			cr.ArchiveDir = dirPath
			if err := cr.Save(dirPath + "/crash.json"); err == nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Migrated legacy crash %s into %s/crash.json", cr.URL, dirPath)
			}
			history = append(history, cr)
		}
		// else: archive with no recoverable metadata — leave it on disk, skip.
	}
	cluster.FailoverHistory = history
}

func (crash *Crash) Purge(path string, keep int) error {
	drop := make(map[string]int)

	files, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	i := 0
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "failover") {
			i++
			drop[file.Name()] = i
		}
	}
	for key, value := range drop {

		if value < len(drop)-keep {
			os.Remove(path + "/" + key)
		}

	}
	return nil
}
