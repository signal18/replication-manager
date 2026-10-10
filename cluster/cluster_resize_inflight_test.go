package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// The in-flight gate converges on the buffer-pool target that was ISSUED, never on the
// configurator's latest wish: a setter that moves the wish must not make a move that has
// not started look "still converging" (#1822).
func TestIsMemoryResizeInFlight_IssuedTargetOnly(t *testing.T) {
	_, s, done := k8sResizeTestServer(t, "inflight", "db1")
	defer done()
	s.Variables = config.NewStringsMap()
	s.Variables.Set("INNODB_BUFFER_POOL_SIZE", "268435456") // 256 MB running

	// Nothing issued: whatever the configurator wants, nothing is in flight.
	s.IssuedBufferPoolBytes = 0
	if s.isMemoryResizeInFlight() {
		t.Fatalf("no issued target must never be in flight")
	}
	// Issued 700 MB, runtime still 256 MB: converging.
	s.IssuedBufferPoolBytes = 700 * 1024 * 1024
	if !s.isMemoryResizeInFlight() {
		t.Fatalf("issued target far from runtime must be in flight")
	}
	// Runtime reached the issued target (within 5%): converged, and the mark is cleared.
	s.Variables.Set("INNODB_BUFFER_POOL_SIZE", "734003200")
	if s.isMemoryResizeInFlight() || s.IssuedBufferPoolBytes != 0 {
		t.Fatalf("converged runtime must clear the in-flight mark, got in-flight with issued=%d", s.IssuedBufferPoolBytes)
	}
	// A pending cgroup shrink is in flight on its own.
	s.PendingCgroupShrink = true
	if !s.isMemoryResizeInFlight() {
		t.Fatalf("pending cgroup shrink must be in flight")
	}
}
