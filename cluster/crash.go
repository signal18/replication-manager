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
)

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
	cluster.FailoverHistory.StoreLastN(moved, cluster.Conf.FailoverLogFileKeep)
	if err := moved.Save(cluster.WorkingDir + "/failover." + now.Format("20060102150405") + ".json"); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not persist rejoin history for %s: %s", url, err)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin of %s ended: %s — moved to crash history", url, result)
	return moved
}

// rejoinAlreadyAttempted reports whether history already holds a rejoin OUTCOME
// for url from THIS split window — the re-fetch / re-run guard that makes the
// rejoin truly one-shot. A stale outcome from a previous split (older than
// SplitBrainStartTs) does not count, so a genuinely new event still gets its
// attempt. An explicit rearmRejoin clears the outcome to allow one more try.
func (cluster *Cluster) rejoinAlreadyAttempted(url string) bool {
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

// RejoinMethodStatus is the per-method availability for the GUI: whether the
// method is POSSIBLE right now (config/resources), NOT whether it would succeed on
// this delta. The delta verdict only informs — an unavailable method is disabled
// with a reason; every method carries a state.
type RejoinMethodStatus struct {
	Method    string `json:"method"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
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
	mk := func(m string, ok bool, reason string) RejoinMethodStatus {
		if ok {
			reason = ""
		}
		return RejoinMethodStatus{Method: m, Available: ok, Reason: reason}
	}
	return []RejoinMethodStatus{
		mk(RejoinMethodFlashback, flashOK, "flashback not enabled (autorejoin-flashback + autorejoin-backup-binlog)"),
		mk(RejoinMethodLogicalDump, masterUp, "master unreachable"),
		mk(RejoinMethodLogicalBkp, hasLogical, "cluster has no logical backup"),
		mk(RejoinMethodPhysicalBkp, hasPhysical, "cluster has no physical backup"),
		mk(RejoinMethodResetReslave, true, ""),
		mk(RejoinMethodIgnoreForce, true, ""),
	}
}

// IsValidRejoinMethod reports whether method is a known operator rejoin method
// (or "" for automatic).
func IsValidRejoinMethod(method string) bool {
	switch method {
	case "", RejoinMethodFlashback, RejoinMethodLogicalDump, RejoinMethodLogicalBkp, RejoinMethodPhysicalBkp, RejoinMethodIgnoreForce, RejoinMethodResetReslave:
		return true
	}
	return false
}

// RearmRejoin is the exported entry (GUI/API) for an explicit operator rejoin of
// url with a chosen method. See rearmRejoin.
func (cluster *Cluster) RearmRejoin(url string, method string) bool {
	return cluster.rearmRejoin(url, method)
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
func (cluster *Cluster) LoadFailoverHistory() {
	files, err := os.ReadDir(cluster.WorkingDir)
	if err != nil {
		return
	}
	var names []string
	for _, file := range files {
		n := file.Name()
		if strings.HasPrefix(n, "failover") && strings.HasSuffix(n, ".json") {
			names = append(names, n)
		}
	}
	sort.Strings(names) // timestamped filenames sort chronologically
	var history crashList
	for _, n := range names {
		data, err := os.ReadFile(cluster.WorkingDir + "/" + n)
		if err != nil {
			continue
		}
		crash := new(Crash)
		if err := json.Unmarshal(data, crash); err != nil {
			continue
		}
		history = append(history, crash)
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
