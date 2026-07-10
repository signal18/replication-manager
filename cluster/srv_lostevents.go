// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

// srv_lostevents.go
// Analysis of the LOST EVENTS captured from a diverged old master after a
// failover (the delta between the election point and the old master's last
// position). The verdict decides the recovery path:
//   - flashback-able (pure row-DML): rewind the old master and rejoin clean;
//   - not flashback-able (DDL / statement events / empty capture): the gap
//     can only be closed by reseeding, or by forward-applying the delta on
//     the new master (idempotent) — an operator decision, informed by the
//     decoded statement listing this file also produces.
package cluster

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/go-mysql-org/go-mysql/replication"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// analyzeLostEvents parses the captured delta natively (the archive is
// PLAINTEXT — the server dump thread decrypts on capture) and classifies its
// content into the verdict fields of the crash record.
func (server *ServerMonitor) analyzeLostEvents(crash *Crash, archive string) {
	cluster := server.ClusterGroup
	crash.DeltaTransactions, crash.DeltaRowEvents, crash.DeltaDDL, crash.DeltaStatementDML = 0, 0, 0, 0
	crash.DeltaAnalyzed = false
	crash.DeltaFlashable = false

	parser := replication.NewBinlogParser()
	err := parser.ParseFile(archive, 4, func(e *replication.BinlogEvent) error {
		switch e.Header.EventType {
		case replication.MARIADB_GTID_EVENT, replication.GTID_EVENT:
			crash.DeltaTransactions++
		case replication.WRITE_ROWS_EVENTv0, replication.WRITE_ROWS_EVENTv1, replication.WRITE_ROWS_EVENTv2,
			replication.UPDATE_ROWS_EVENTv0, replication.UPDATE_ROWS_EVENTv1, replication.UPDATE_ROWS_EVENTv2,
			replication.DELETE_ROWS_EVENTv0, replication.DELETE_ROWS_EVENTv1, replication.DELETE_ROWS_EVENTv2:
			crash.DeltaRowEvents++
		case replication.QUERY_EVENT:
			q, ok := e.Event.(*replication.QueryEvent)
			if !ok {
				return nil
			}
			query := strings.ToUpper(strings.TrimSpace(string(q.Query)))
			switch {
			case query == "BEGIN" || query == "COMMIT" || query == "ROLLBACK" ||
				strings.HasPrefix(query, "SAVEPOINT") || strings.HasPrefix(query, "RELEASE SAVEPOINT") ||
				strings.HasPrefix(query, "XA "):
				// transaction bookkeeping, not content
			case strings.HasPrefix(query, "CREATE") || strings.HasPrefix(query, "ALTER") ||
				strings.HasPrefix(query, "DROP") || strings.HasPrefix(query, "TRUNCATE") ||
				strings.HasPrefix(query, "RENAME") || strings.HasPrefix(query, "GRANT") ||
				strings.HasPrefix(query, "REVOKE") || strings.HasPrefix(query, "ANALYZE") ||
				strings.HasPrefix(query, "OPTIMIZE") || strings.HasPrefix(query, "REPAIR") ||
				strings.HasPrefix(query, "FLUSH"):
				crash.DeltaDDL++
			default:
				// a data-changing statement logged in STATEMENT format —
				// flashback cannot reverse it either
				crash.DeltaStatementDML++
			}
		}
		return nil
	})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Lost events analysis failed on %s: %s", archive, err)
		return
	}
	crash.DeltaAnalyzed = true
	// Flashback reverses row events ONLY: any DDL or statement-format DML in
	// the delta makes the whole delta non-reversible; an empty capture has
	// nothing to reverse.
	crash.DeltaFlashable = crash.DeltaRowEvents > 0 && crash.DeltaDDL == 0 && crash.DeltaStatementDML == 0
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO",
		"Lost events of %s analyzed: %d transactions, %d row events, %d DDL, %d statement-DML — flashback-able: %t",
		crash.URL, crash.DeltaTransactions, crash.DeltaRowEvents, crash.DeltaDDL, crash.DeltaStatementDML, crash.DeltaFlashable)
}

// decodeLostEvents renders the archived delta for human review next to the
// archive: <archive>.sql (what happened, decoded pseudo-SQL) and, when the
// delta is reversible, <archive>.flashback.sql (the exact undo mysqlbinlog
// --flashback would execute). Both are served by the lost-events API viewer.
func (server *ServerMonitor) decodeLostEvents(crash *Crash, archive string) {
	cluster := server.ClusterGroup
	if _, err := os.Stat(cluster.GetMysqlBinlogPath()); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "mysqlbinlog does not exist %s: cannot decode lost events", cluster.GetMysqlBinlogPath())
		return
	}
	out := archive + ".sql"
	if err := runBinlogDecode(cluster.GetMysqlBinlogPath(), []string{"--base64-output=decode-rows", "-vv", archive}, out); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Could not decode lost events %s: %s", archive, err)
	} else {
		crash.DeltaDecoded = out
	}
	if crash.DeltaFlashable {
		fb := archive + ".flashback.sql"
		if err := runBinlogDecode(cluster.GetMysqlBinlogPath(), []string{"--flashback", "-v", "--base64-output=decode-rows", archive}, fb); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Could not render flashback of lost events %s: %s", archive, err)
		} else {
			crash.DeltaFlashbackDecoded = fb
		}
	}
}

func runBinlogDecode(binPath string, args []string, outPath string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := exec.Command(binPath, args...)
	cmd.Stdout = f
	return cmd.Run()
}

// hasUnresolvedCrash reports whether a crash record points at a server that
// is still broken (diverged old master not yet rejoined). While true the
// crash records — and their FailoverIOGtid recovery anchor — must SURVIVE:
// purging them was how yesterday's divergence became unrecoverable.
func (cluster *Cluster) hasUnresolvedCrash() bool {
	for _, crash := range cluster.Crashes {
		if crash == nil {
			continue
		}
		srv := cluster.GetServerFromURL(crash.URL)
		if srv != nil && (srv.State == stateSlaveErr || srv.IsFailed()) {
			return true
		}
	}
	return false
}

// assertLostEventsStates keeps the divergence verdict visible per tick while
// the diverged server exists: the operator learns in one glance whether the
// last divergence is flashback-able.
func (cluster *Cluster) assertLostEventsStates() {
	for _, crash := range cluster.Crashes {
		if crash == nil || !crash.DeltaAnalyzed {
			continue
		}
		srv := cluster.GetServerFromURL(crash.URL)
		if srv == nil || (srv.State != stateSlaveErr && !srv.IsFailed()) {
			continue
		}
		if crash.DeltaFlashable {
			cluster.SetState("WARN0184", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0184"], crash.URL), ErrFrom: "REJOIN", ServerUrl: crash.URL})
		} else {
			cluster.SetState("WARN0185", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0185"], crash.URL), ErrFrom: "REJOIN", ServerUrl: crash.URL})
		}
	}
}

// GetLatestCrashForServer returns the most recent crash record for the given
// server URL — the "last divergence" the lost-events viewer displays.
func (cluster *Cluster) GetLatestCrashForServer(url string) *Crash {
	var last *Crash
	for _, crash := range cluster.Crashes {
		if crash != nil && crash.URL == url {
			last = crash
		}
	}
	return last
}

// LostEventsPage is one paginated chunk of a decoded lost-events file. The
// cursor is a BINARY POSITION in the file: the server seeks to Pos in O(1)
// (no rescan from the start — a divergence on a 100K-writes/s master decodes
// to gigabytes), returns complete lines up to the byte budget, and NextPos is
// the offset of the first byte not returned. Page forward by passing NextPos
// back; Size lets the viewer show progress and jump anywhere in the file.
type LostEventsPage struct {
	Pos     int64    `json:"pos"`
	NextPos int64    `json:"nextPos"`
	Size    int64    `json:"size"`
	EOF     bool     `json:"eof"`
	Lines   []string `json:"lines"`
}

// ReadLostEventsPage reads one page of a decoded lost-events file starting at
// byte offset pos, bounded by maxBytes, aligned to line boundaries.
func ReadLostEventsPage(path string, pos int64, maxBytes int64) (*LostEventsPage, error) {
	if maxBytes <= 0 || maxBytes > 4*1024*1024 {
		maxBytes = 256 * 1024
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	page := &LostEventsPage{Pos: pos, NextPos: pos, Size: st.Size()}
	if pos < 0 || pos >= st.Size() {
		page.EOF = true
		return page, nil
	}
	if _, err := f.Seek(pos, io.SeekStart); err != nil {
		return nil, err
	}
	buf := make([]byte, maxBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	chunk := buf[:n]
	if pos+int64(n) < st.Size() {
		// align to the last complete line so the next page starts clean
		if cut := bytes.LastIndexByte(chunk, '\n'); cut >= 0 {
			chunk = chunk[:cut+1]
		}
	}
	page.NextPos = pos + int64(len(chunk))
	page.EOF = page.NextPos >= st.Size()
	sc := bufio.NewScanner(bytes.NewReader(chunk))
	sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for sc.Scan() {
		page.Lines = append(page.Lines, sc.Text())
	}
	return page, sc.Err()
}
