package cluster

import (
	"testing"
	"time"
)

// TestSetAutoTLSUnderClusterLock: the 3159 auto-TLS path runs inside the server
// Pings of TopologyDiscover, which holds the cluster mutex. Recording HaveAutoTLS
// there must not need that mutex, or the monitor deadlocks (curepipe, 2026-10-14).
func TestSetAutoTLSUnderClusterLock(t *testing.T) {
	cluster := &Cluster{}
	cluster.Lock()
	defer cluster.Unlock()

	done := make(chan struct{})
	go func() {
		cluster.SetAutoTLS(true)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SetAutoTLS blocked on the cluster mutex held by TopologyDiscover")
	}
	if !cluster.HaveAutoTLS {
		t.Fatal("HaveAutoTLS not set")
	}
}
