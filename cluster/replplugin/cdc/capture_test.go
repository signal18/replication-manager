// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/cluster/replplugin/wire"
)

// The pause on max_pending_mb is told while the capture holds its lock (flushLocked): the
// callback must not take it, and the pause must end once there is room again.
func TestWaitRoomPauseWithLockHeld(t *testing.T) {
	dir := t.TempDir()
	// 2 KB of checkpoints waiting, a bound of 1 KB
	if err := os.WriteFile(filepath.Join(dir, wire.CheckpointName(1)), make([]byte, 2048), 0640); err != nil {
		t.Fatal(err)
	}
	c := &capture{cfg: wire.Config{MaxPendingMB: 1}}
	c.w = &checkpointWriter{dir: dir, maxPending: 1024, paused: func(p int64) { c.pausedMB.Store(p >> 10) }}
	c.mu.Lock() // as flushLocked
	done := make(chan struct{})
	go func() { c.w.waitRoom(); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for c.pausedMB.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the pause was never told")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if d := pausedDescription("", c.pausedMB.Load(), 1); !strings.HasPrefix(d, "paused: ") {
		t.Fatalf("description %q", d)
	}
	os.Remove(filepath.Join(dir, wire.CheckpointName(1))) // applied: room again
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("waitRoom did not return once there was room")
	}
	c.mu.Unlock()
	if c.pausedMB.Load() != 0 {
		t.Fatal("the pause did not end")
	}
	if d := pausedDescription("paused: 3 MB of checkpoints", 0, 1); d != "" {
		t.Fatalf("an ended pause is still said: %q", d)
	}
	if d := pausedDescription("a DDL", 0, 1); d != "a DDL" {
		t.Fatalf("another description is kept: %q", d)
	}
}
