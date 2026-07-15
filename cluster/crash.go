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
}

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

// crashMaxVerdictAge bounds how long a crash may drive an AUTOMATIC rejoin: an
// election verdict is only actionable around its own split window, never hours
// later (2026-07-15 07:10: a 7h-old crash re-condemned db1 through every
// transient non-slave classification during a replication bootstrap).
const crashMaxVerdictAge = 900 // seconds, same scale as the simulator's sbMax

// getFreshCrashForLoser returns the FRESHEST crash designating loserURL as the
// defeated server and masterURL as the elected winner, provided it is recent
// enough to act on automatically. Older verdicts stay visible (GUI, operator)
// but never drive automation.
func (cluster *Cluster) getFreshCrashForLoser(loserURL string, masterURL string) *Crash {
	var best *Crash
	now := time.Now().Unix()
	for _, cr := range cluster.Crashes {
		if cr.URL != loserURL || cr.ElectedMasterURL != masterURL || cr.Switchover {
			continue
		}
		if now-cr.UnixTimestamp > crashMaxVerdictAge {
			continue
		}
		if best == nil || cr.UnixTimestamp > best.UnixTimestamp {
			best = cr
		}
	}
	return best
}

// consumeServedCrashes drops every crash whose server is now observed as a
// healthy slave of the very master the election designated: the verdict has
// served its purpose and must never speak twice.
func (cluster *Cluster) consumeServedCrashes() {
	if len(cluster.Crashes) == 0 {
		return
	}
	kept := make([]*Crash, 0, len(cluster.Crashes))
	for _, cr := range cluster.Crashes {
		sv := cluster.GetServerFromURL(cr.URL)
		if sv != nil && sv.IsSlave && !sv.IsFailed() {
			if m, _ := cluster.GetMasterFromReplication(sv); m != nil && m.URL == cr.ElectedMasterURL {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlInfo, "Crash verdict for %s consumed: rejoined as slave of elected master %s", cr.URL, cr.ElectedMasterURL)
				continue
			}
		}
		kept = append(kept, cr)
	}
	cluster.Crashes = kept
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
