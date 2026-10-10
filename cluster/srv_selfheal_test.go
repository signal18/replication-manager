// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/signal18/replication-manager/utils/state"
)

// TestMaybeSelfHealOversizedDBLog verifies that a runaway repman-side collected
// DB log is moved aside, the active file reclaimed (recreated empty), a
// compressed sample kept, and WARN0208 raised -- see maybeSelfHealOversizedDBLog.
func TestMaybeSelfHealOversizedDBLog(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogsWithHost(t, tmp, "node1", "3306")
	server.ClusterGroup.StateMachine = new(state.StateMachine)
	server.ClusterGroup.StateMachine.Init()

	dir := filepath.Join(tmp, "logs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	logfile := filepath.Join(dir, "log_slow_query.log")
	if err := os.WriteFile(logfile, []byte("runaway slow log content\n"), 0600); err != nil {
		t.Fatalf("seed logfile: %v", err)
	}

	// Shrink the ceiling so any non-empty file counts as a runaway.
	orig := selfHealDBLogCeilingMB
	selfHealDBLogCeilingMB = 0
	defer func() { selfHealDBLogCeilingMB = orig }()

	server.maybeSelfHealOversizedDBLog(logfile)

	// Active file recreated empty so the tailer attaches to a fresh log.
	fi, err := os.Stat(logfile)
	if err != nil {
		t.Fatalf("expected active log recreated, stat: %v", err)
	}
	if fi.Size() != 0 {
		t.Fatalf("expected fresh empty active log, got %d bytes", fi.Size())
	}

	// WARN0208 raised so the remediation is recorded, not silent.
	if !server.ClusterGroup.StateMachine.IsInState("WARN0208") {
		t.Fatalf("expected WARN0208 self-heal state to be raised")
	}

	// A compressed sample appears and the uncompressed backup is removed
	// (background goroutine).
	deadline := time.Now().Add(3 * time.Second)
	for {
		gz, _ := filepath.Glob(filepath.Join(dir, "log_slow_query_oversize_*.log.gz"))
		raw, _ := filepath.Glob(filepath.Join(dir, "log_slow_query_oversize_*.log"))
		if len(gz) == 1 && len(raw) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected exactly one .gz sample and no uncompressed backup; gz=%v raw=%v", gz, raw)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestMaybeSelfHealOversizedDBLog_LeavesSmallFileAlone verifies a normal-sized
// collected log is never touched.
func TestMaybeSelfHealOversizedDBLog_LeavesSmallFileAlone(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogsWithHost(t, tmp, "node1", "3306")
	server.ClusterGroup.StateMachine = new(state.StateMachine)
	server.ClusterGroup.StateMachine.Init()

	logfile := filepath.Join(tmp, "log_slow_query.log")
	content := []byte("a few normal slow-query lines\n")
	if err := os.WriteFile(logfile, content, 0600); err != nil {
		t.Fatalf("seed logfile: %v", err)
	}

	// Default ceiling (1 GB): a tiny file must be left untouched.
	server.maybeSelfHealOversizedDBLog(logfile)

	got, err := os.ReadFile(logfile)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("small file was modified: got %q", string(got))
	}
	if server.ClusterGroup.StateMachine.IsInState("WARN0208") {
		t.Fatalf("WARN0208 must not be raised for a normal-sized log")
	}
}
