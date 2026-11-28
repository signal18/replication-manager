// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/gtid"
	"github.com/signal18/replication-manager/utils/state"
)

// The binlog drop of the flashback rejoin (doc/implementation/cluster/REJOIN_FLASHBACK.md): after
// the flashback of an asynchronous old master, its binary log still holds the lost transactions
// past the rejoin position; repman drops it (RESET MASTER, gtid_binlog_state = the position)
// only when every guard below holds, each observed right before the drop (F5). One that fails
// keeps the binary log, opens WARN0365 naming it, and the rejoin goes on as before (with
// gtid_strict_mode the position is refused and the rejoin falls back to a reseed).

// Guards, in the order they are checked.
const (
	flashbackGuardSemiSync        = "semi-sync"         // the old master was in semi-sync at the failover
	flashbackGuardCopy            = "copy"              // the lost events were not copied first
	flashbackGuardReplay          = "replay"            // the replay did not apply exactly the saved transactions
	flashbackGuardMaxTransactions = "max-transactions"  // more lost transactions than the cap
	flashbackGuardDomain          = "domain"            // the binary log carries another GTID domain
	flashbackGuardLost            = "lost-transactions" // past the target: anything but the saved transactions
	flashbackGuardReaders         = "readers"           // someone reads that binary log
)

// flashbackDropMaxTransactionsDefault: the lost transactions of a failover under load are
// counted in hundreds (132 and 135 on a 16-thread sysbench test); beyond ten thousand, the
// flashback and the binlog drop are no longer the small repair they are meant to be, a reseed is.
const flashbackDropMaxTransactionsDefault = 10000

// flashbackDropMaxTransactions is autorejoin-async-flashback-drop-binlog-max-transactions, 0 or
// less being the default: never unbounded.
func (cluster *Cluster) flashbackDropMaxTransactions() int {
	if cluster.Conf.AutorejoinAsyncFlashbackDropBinlogMaxTransactions <= 0 {
		return flashbackDropMaxTransactionsDefault
	}
	return cluster.Conf.AutorejoinAsyncFlashbackDropBinlogMaxTransactions
}

// flashbackDropFacts is what the drop decision reads, every value observed or tracked: the
// decision itself (check) reads nothing.
type flashbackDropFacts struct {
	SemiSyncMaster  bool        // the crash's FailoverSemiSyncMasterStatus
	Copy            string      // the binlog copy the flashback read (autorejoin-backup-binlog)
	CopySize        int64       // its size, 0 when missing
	Target          string      // the rejoin position
	OwnServerID     uint64      // the old master's server_id
	BinlogState     string      // its gtid_binlog_state, read live
	Saved           []gtid.Gtid // the GTIDs of the saved lost transactions (the analysis of the copy, else the decoded copy)
	SavedErr        error       // why they could not be read
	Replayed        int         // the transactions the replay applied
	MaxTransactions int
	DumpThreads     int      // binlog dump threads on the old master, read live
	Readers         []string // replications of the topology whose source is the old master
}

// check returns the guard that failed and its values, "" when the binary log may be dropped.
func (f *flashbackDropFacts) check() (string, string) {
	if f.SemiSyncMaster {
		return flashbackGuardSemiSync, "Rpl_semi_sync_master_status was ON at the failover"
	}
	if f.CopySize <= 0 {
		return flashbackGuardCopy, fmt.Sprintf("no copy of the lost events at %s", f.Copy)
	}
	if f.SavedErr != nil {
		return flashbackGuardLost, fmt.Sprintf("the saved transactions could not be read: %s", f.SavedErr)
	}
	if f.Replayed <= 0 || f.Replayed != len(f.Saved) {
		return flashbackGuardReplay, fmt.Sprintf("%d transactions replayed, %d saved", f.Replayed, len(f.Saved))
	}
	if len(f.Saved) > f.MaxTransactions {
		return flashbackGuardMaxTransactions, fmt.Sprintf("%d lost transactions, cap %d (autorejoin-async-flashback-drop-binlog-max-transactions)", len(f.Saved), f.MaxTransactions)
	}
	binlog := *gtid.NewList(f.BinlogState)
	target := *gtid.NewList(f.Target)
	domains := map[uint64]bool{}
	for _, g := range binlog {
		domains[g.DomainID] = true
	}
	for _, g := range target {
		domains[g.DomainID] = true
	}
	if len(domains) != 1 || len(target) != 1 {
		return flashbackGuardDomain, fmt.Sprintf("gtid_binlog_state %q, target %q: one GTID domain only", f.BinlogState, f.Target)
	}
	t := target[0]
	var head *gtid.Gtid
	for i, g := range binlog {
		if g.SeqNo <= t.SeqNo {
			continue
		}
		if head != nil || g.ServerID != f.OwnServerID {
			return flashbackGuardLost, fmt.Sprintf("gtid_binlog_state %q past the target %s: not only the old master's server_id %d", f.BinlogState, f.Target, f.OwnServerID)
		}
		head = &binlog[i]
	}
	if head == nil {
		return flashbackGuardLost, fmt.Sprintf("gtid_binlog_state %q is not past the target %s", f.BinlogState, f.Target)
	}
	if want := int(head.SeqNo - t.SeqNo); want != len(f.Saved) {
		return flashbackGuardLost, fmt.Sprintf("%d transactions in the binary log past %s (to %d), %d saved", want, f.Target, head.SeqNo, len(f.Saved))
	}
	saved := append([]gtid.Gtid(nil), f.Saved...)
	sort.Slice(saved, func(i, j int) bool { return saved[i].SeqNo < saved[j].SeqNo })
	for i, g := range saved {
		if g.DomainID != t.DomainID || g.ServerID != f.OwnServerID || g.SeqNo != t.SeqNo+uint64(i)+1 {
			return flashbackGuardLost, fmt.Sprintf("saved transaction %d-%d-%d is not the %d-%d-%d the binary log holds past %s", g.DomainID, g.ServerID, g.SeqNo, t.DomainID, f.OwnServerID, t.SeqNo+uint64(i)+1, f.Target)
		}
	}
	if f.DumpThreads > 0 || len(f.Readers) > 0 {
		return flashbackGuardReaders, fmt.Sprintf("%d binlog dump threads, replications from it: %s", f.DumpThreads, strings.Join(f.Readers, ", "))
	}
	return "", ""
}

// flashbackDropPlan observes the facts after a complete replay (replayed transactions) and returns
// them when the binary log may be dropped, nil when it is kept: switched off (logged), nothing
// past the target, or a guard that failed (WARN0365).
func (server *ServerMonitor) flashbackDropPlan(crash *Crash, copy, target string, replayed int) *flashbackDropFacts {
	cluster := server.ClusterGroup
	if !cluster.Conf.AutorejoinAsyncFlashbackDropBinlog {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin flashback %s: binary log kept, autorejoin-async-flashback-drop-binlog is off", server.URL)
		return nil
	}
	f := &flashbackDropFacts{SemiSyncMaster: crash.FailoverSemiSyncMasterStatus, Copy: copy, Target: target,
		OwnServerID: uint64(server.ServerID), Replayed: replayed, MaxTransactions: cluster.flashbackDropMaxTransactions()}
	if st, err := os.Stat(copy); err == nil {
		f.CopySize = st.Size()
	}
	var logs string
	var err error
	f.BinlogState, logs, err = dbhelper.GetVariableByName(server.Conn, "GTID_BINLOG_STATE", server.DBVersion)
	cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Could not read gtid_binlog_state on %s: %s", server.URL, err)
	if err != nil {
		server.refuseFlashbackDrop(target, flashbackGuardLost, "gtid_binlog_state not read: "+err.Error())
		return nil
	}
	if parentBackupBehind(target, f.BinlogState) == "" {
		return nil // nothing past the target: nothing to drop
	}
	if crash.DeltaAnalyzed && len(crash.DeltaGTIDs) == crash.DeltaTransactions {
		f.Saved = crash.DeltaGTIDs // the analysis of the copy, just before the replay
	} else {
		f.Saved, f.SavedErr = savedLostGTIDs(crash.DeltaDecoded) // a re-armed rejoin: the decoded copy
	}
	f.DumpThreads, logs, err = dbhelper.GetBinlogDumpThreads(server.Conn, server.DBVersion)
	cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Could not count the binlog dump threads on %s: %s", server.URL, err)
	if err != nil {
		f.DumpThreads = 1 // unknown counts as a reader
	}
	f.Readers = server.replicationsFromMe()
	if guard, values := f.check(); guard != "" {
		server.refuseFlashbackDrop(target, guard, values)
		return nil
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Rejoin flashback %s: every guard passed (asynchronous, copy %s, %d transactions saved and replayed, past %s only them, one domain, no reader)", server.URL, copy, len(f.Saved), target)
	return f
}

// refuseFlashbackDrop keeps the binary log: logged and WARN0365 for a day.
func (server *ServerMonitor) refuseFlashbackDrop(target, guard, values string) {
	cluster := server.ClusterGroup
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
		"Rejoin flashback %s: binary log kept, guard %s failed: %s", server.URL, guard, values)
	server.flashbackDropRefused.Store(&flashbackDropRefusal{Target: target, Guard: guard, Values: values, At: time.Now()})
}

// replicationsFromMe are the replications of every cluster repman monitors whose source is the
// server (native channels, CDC feeds, a child cluster's link), other than its own.
func (server *ServerMonitor) replicationsFromMe() []string {
	cluster := server.ClusterGroup
	clusters := map[string]*Cluster{cluster.Name: cluster}
	for name, cl := range cluster.clusterList {
		clusters[name] = cl
	}
	var readers []string
	for _, cl := range clusters {
		if cl == nil {
			continue
		}
		for _, s := range cl.Servers {
			if s == nil || s == server {
				continue
			}
			for _, r := range s.Replications {
				src := r.SourceServer()
				if src.Server == server || (src.Server == nil && src.Host != "" && src.Host == server.Host && (src.Port == "" || src.Port == server.Port)) {
					readers = append(readers, s.URL+" ("+r.Name()+")")
				}
			}
		}
	}
	sort.Strings(readers)
	return readers
}

// savedGTIDLine is a GTID event header of mariadb-binlog's decoded output:
// "#261127 19:54:50 server id 11626367  end_log_pos 261982468 CRC32 0x475763b7 	GTID 1-11626367-1203981 trans".
var savedGTIDLine = regexp.MustCompile(`^#\d{6} .*\sGTID (\d+)-(\d+)-(\d+)`)

// savedLostGTIDs reads the GTIDs of the lost transactions repman saved with the crash from the
// decoded copy (Crash.DeltaDecoded), when the analysis did not record them (a rejoin re-armed
// after a restart).
func savedLostGTIDs(decoded string) ([]gtid.Gtid, error) {
	if decoded == "" {
		return nil, fmt.Errorf("no decoded copy of the lost events")
	}
	f, err := os.Open(decoded)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []gtid.Gtid
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadSlice('\n')
		if len(line) > 0 && line[0] == '#' {
			if m := savedGTIDLine.FindSubmatch(line); m != nil {
				d, _ := strconv.ParseUint(string(m[1]), 10, 32)
				s, _ := strconv.ParseUint(string(m[2]), 10, 32)
				n, _ := strconv.ParseUint(string(m[3]), 10, 64)
				out = append(out, gtid.Gtid{DomainID: d, ServerID: s, SeqNo: n})
			}
		}
		for err == bufio.ErrBufferFull { // a long line (an annotated statement): skip its rest
			_, err = r.ReadSlice('\n')
		}
		if err != nil {
			break
		}
	}
	return out, nil
}

// flashbackTxCounter counts, on the replay's input, the transactions that carry row events: a
// START TRANSACTION (or BEGIN) block with a BINLOG statement, ended by COMMIT. The empty block
// mariadb-binlog writes at the end of its output is not one.
type flashbackTxCounter struct {
	line          []byte // the start of the current line, enough to recognise it
	full          bool   // the current line's start is complete
	inTx, hasRows bool
	count         int
}

func (c *flashbackTxCounter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' {
			c.endLine()
			continue
		}
		if !c.full {
			c.line = append(c.line, b)
			if len(c.line) >= 24 {
				c.full = true
			}
		}
	}
	return len(p), nil
}

func (c *flashbackTxCounter) endLine() {
	l := c.line
	switch {
	case bytes.HasPrefix(l, []byte("START TRANSACTION")), bytes.HasPrefix(l, []byte("BEGIN")):
		c.inTx, c.hasRows = true, false
	case bytes.HasPrefix(l, []byte("BINLOG '")):
		c.hasRows = c.hasRows || c.inTx
	case bytes.HasPrefix(l, []byte("COMMIT")):
		if c.inTx && c.hasRows {
			c.count++
		}
		c.inTx = false
	}
	c.line, c.full = c.line[:0], false
}

// Transactions is the count of row transactions seen.
func (c *flashbackTxCounter) Transactions() int { return c.count }

// flashbackBinlogReset is the binary log the flashback rejoin dropped on a rewound old master:
// the GTID it was set to, when, the binlogs that were on the server, the lost transactions and
// their copy (WARN0364).
type flashbackBinlogReset struct {
	GTID         string
	At           time.Time
	From, To     string
	Transactions int
	Copy         string
}

// flashbackDropRefusal is a binlog drop a guard refused (WARN0365).
type flashbackDropRefusal struct {
	Target, Guard, Values string
	At                    time.Time
}

const flashbackDropRefusalShown = 24 * time.Hour

// checkFlashbackBinlogResetState, every tick: WARN0364 while the binary log the flashback rejoin
// dropped has no backup of the server after it (a completed one, logical or physical, started
// after the drop); WARN0365 for a day after a guard refused a drop.
func (server *ServerMonitor) checkFlashbackBinlogResetState() {
	cluster := server.ClusterGroup
	if r := server.flashbackDropRefused.Load(); r != nil {
		if time.Since(r.At) > flashbackDropRefusalShown {
			server.flashbackDropRefused.CompareAndSwap(r, nil)
		} else {
			cluster.SetState("WARN0365", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0365"], server.URL, r.At.Format(time.RFC3339), r.Guard, r.Values), ErrFrom: "REJOIN", ServerUrl: server.URL})
		}
	}
	r := server.flashbackBinlogReset.Load()
	if r == nil {
		return
	}
	for _, method := range []string{"logical", "physical"} {
		if _, meta := server.GetLatestBackupMeta(method, false); meta != nil && meta.Completed && meta.StartTime.After(r.At) {
			server.flashbackBinlogReset.CompareAndSwap(r, nil)
			return
		}
	}
	cluster.SetState("WARN0364", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0364"], server.URL, r.At.Format(time.RFC3339), r.Transactions, r.GTID, r.Copy, r.From, r.To), ErrFrom: "JOB", ServerUrl: server.URL})
}
