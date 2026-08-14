// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	gzip "github.com/klauspost/pgzip"
	"github.com/signal18/replication-manager/config"
)

// TestStreamCopyToFile_IdleReadTimeoutEndsStalledReceiver guards against a
// stalled/hung SST sender blocking stream_copy_to_file's Read loop forever.
// The listener's own SetDeadline (SSTRunReceiverToFile,
// SSTRunReceiverToDBLogFile) only bounds Accept(), not the connection it
// returns -- sstStreamIdleTimeout is what actually bounds this loop, which in
// turn is what lets a stalled receiver's DB log writer borrow (see
// getDBLogRotatingWriter, srv_job_logs.go) actually get released instead of
// leaving a stale, still-borrowed cache entry parked forever.
func TestStreamCopyToFile_IdleReadTimeoutEndsStalledReceiver(t *testing.T) {
	prevTimeout := sstStreamIdleTimeout
	sstStreamIdleTimeout = 50 * time.Millisecond
	t.Cleanup(func() { sstStreamIdleTimeout = prevTimeout })

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close() })

	var out bytes.Buffer
	sst := &SST{
		in:            serverConn,
		outfilewriter: &out,
		cluster: &Cluster{
			Conf: &config.Config{},
			Name: "testcluster",
		},
	}

	done := sst.stream_copy_to_file()

	select {
	case <-done:
		// Expected: the idle read deadline fired, ending the loop.
	case <-time.After(2 * time.Second):
		t.Fatal("stream_copy_to_file did not exit after the idle read deadline elapsed -- a stalled sender would leak this goroutine forever")
	}
}

// TestStreamCopyToFile_CopiesDataBeforeEOF confirms the idle-deadline change
// doesn't disturb normal operation: data written by the sender before it
// closes the connection still reaches outfilewriter.
func TestStreamCopyToFile_CopiesDataBeforeEOF(t *testing.T) {
	prevTimeout := sstStreamIdleTimeout
	t.Cleanup(func() { sstStreamIdleTimeout = prevTimeout })

	clientConn, serverConn := net.Pipe()

	var out bytes.Buffer
	sst := &SST{
		in:            serverConn,
		outfilewriter: &out,
		cluster: &Cluster{
			Conf: &config.Config{},
			Name: "testcluster",
		},
	}

	done := sst.stream_copy_to_file()

	go func() {
		clientConn.Write([]byte("hello from sender\n"))
		clientConn.Close()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream_copy_to_file did not exit after the sender closed the connection")
	}

	if got := out.String(); got != "hello from sender\n" {
		t.Fatalf("expected data written before EOF to reach outfilewriter, got %q", got)
	}
}

func newTestSSTCluster(t *testing.T) *Cluster {
	t.Helper()
	// SSTSendBuffer must be positive: both SSTRunSendFile and SSTRunSendGzip
	// size their read buffer directly off it, and a 0-length buffer makes
	// Read loop forever (io.Reader's contract lets a 0-len buffer return
	// (0, nil) without indicating EOF).
	return &Cluster{Conf: &config.Config{SSTSendBuffer: 4096}, Name: "testcluster"}
}

// drainPipeInto runs an io.Copy from serverConn into a buffer on a goroutine
// and returns the buffer plus a channel closed once the copy sees EOF (the
// client side closing its end of the net.Pipe). net.Pipe is unbuffered, so
// this must run concurrently with the sender, not after it.
func drainPipeInto(serverConn net.Conn) (*bytes.Buffer, <-chan struct{}) {
	var received bytes.Buffer
	done := make(chan struct{})
	go func() {
		io.Copy(&received, serverConn)
		close(done)
	}()
	return &received, done
}

// TestSSTRunSendFile_CountsBytesAndSetsTotal guards the byte/rate tracking
// wired into the raw (non-decompressing) file send path: bytes sent go over
// the wire unmodified, so the on-disk file size is a trustworthy total for
// the progress bar's percent/denominator.
func TestSSTRunSendFile_CountsBytesAndSetsTotal(t *testing.T) {
	cluster := newTestSSTCluster(t)
	sv := &ServerMonitor{Host: "dest", SSTPort: "9999"}

	content := []byte("the quick brown fox jumps over the lazy dog\n")
	tmpfile := filepath.Join(t.TempDir(), "backup.xbtream")
	if err := os.WriteFile(tmpfile, content, 0644); err != nil {
		t.Fatalf("failed to write test backup file: %v", err)
	}

	clientConn, serverConn := net.Pipe()
	received, drained := drainPipeInto(serverConn)

	err := cluster.SSTRunSendFile(clientConn, tmpfile, sv, newReseedProgressSink(sv))
	clientConn.Close()
	<-drained

	if err != nil {
		t.Fatalf("SSTRunSendFile failed: %v", err)
	}
	if got := sv.reseedBytes.Load(); got != int64(len(content)) {
		t.Fatalf("expected reseedBytes=%d, got %d", len(content), got)
	}
	if got := sv.reseedTotal.Load(); got != int64(len(content)) {
		t.Fatalf("expected reseedTotal=%d (on-disk size, sent unmodified), got %d", len(content), got)
	}
	if received.String() != string(content) {
		t.Fatalf("received data mismatch: got %q, want %q", received.String(), content)
	}
}

// TestSSTRunSendGzip_CountsBytesButLeavesTotalUnknown guards the semantics
// decision for the decompress-then-send path: bytes sent (decompressed)
// never matches the compressed file's on-disk size, so reseedTotal must stay
// 0 (unknown) even though bytes are still counted correctly.
func TestSSTRunSendGzip_CountsBytesButLeavesTotalUnknown(t *testing.T) {
	cluster := newTestSSTCluster(t)
	sv := &ServerMonitor{Host: "dest", SSTPort: "9999"}

	content := []byte("the quick brown fox jumps over the lazy dog, repeated for a bit of length\n")
	tmpfile := filepath.Join(t.TempDir(), "backup.gz")
	f, err := os.Create(tmpfile)
	if err != nil {
		t.Fatalf("failed to create test backup file: %v", err)
	}
	gz := gzip.NewWriter(f)
	if _, err := gz.Write(content); err != nil {
		t.Fatalf("failed to write gzip content: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("failed to close backup file: %v", err)
	}

	clientConn, serverConn := net.Pipe()
	received, drained := drainPipeInto(serverConn)

	err = cluster.SSTRunSendGzip(clientConn, tmpfile, sv, newReseedProgressSink(sv))
	clientConn.Close()
	<-drained

	if err != nil {
		t.Fatalf("SSTRunSendGzip failed: %v", err)
	}
	if got := sv.reseedBytes.Load(); got != int64(len(content)) {
		t.Fatalf("expected reseedBytes=%d (decompressed size), got %d", len(content), got)
	}
	if got := sv.reseedTotal.Load(); got != 0 {
		t.Fatalf("expected reseedTotal to stay 0 (unknown) for decompress-then-send, got %d", got)
	}
	if received.String() != string(content) {
		t.Fatalf("received data mismatch: got %q, want %q", received.String(), content)
	}
}

// TestSSTRunSendFile_NilProgressLeavesReseedCountersUntouched guards the
// cross-feature contamination bug found in review: SSTRunSender's family is
// shared with non-reseed transfers (UpgradeJobsScript in srv_job.go, the
// dummy-config sender in srv_cnf.go). If a server has a real reseed's
// progress active (reseedBytes/reseedTotal already non-zero from an earlier
// SST send) and one of those unrelated sends fires on the same server before
// the reseed's own SetInReseedBackup("") cleanup, it must not touch those
// counters -- an ambient check like sv.IsReseeding!="" couldn't tell the two
// cases apart (both would be true simultaneously). A nil *SSTProgressSink
// from the caller is what actually prevents it: SSTRunSendFile has no
// fallback path back to sv.reseedBytes to accidentally take.
func TestSSTRunSendFile_NilProgressLeavesReseedCountersUntouched(t *testing.T) {
	cluster := newTestSSTCluster(t)
	sv := &ServerMonitor{Host: "dest", SSTPort: "9999"}

	// Simulate an active reseed's progress already in flight, as
	// WaitAndSendSST's beginReseedProgress + prior SST writes would leave it.
	const priorBytes, priorTotal = int64(999000), int64(999999)
	sv.reseedBytes.Store(priorBytes)
	sv.reseedTotal.Store(priorTotal)

	content := []byte("unrelated dbjobs_new script upload, nothing to do with any reseed\n")
	tmpfile := filepath.Join(t.TempDir(), "dbjobs_new")
	if err := os.WriteFile(tmpfile, content, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	clientConn, serverConn := net.Pipe()
	received, drained := drainPipeInto(serverConn)

	err := cluster.SSTRunSendFile(clientConn, tmpfile, sv, nil)
	clientConn.Close()
	<-drained

	if err != nil {
		t.Fatalf("SSTRunSendFile failed: %v", err)
	}
	if received.String() != string(content) {
		t.Fatalf("received data mismatch: got %q, want %q", received.String(), content)
	}
	if got := sv.reseedBytes.Load(); got != priorBytes {
		t.Fatalf("expected reseedBytes untouched at %d (an unrelated in-progress reseed's counter), got %d", priorBytes, got)
	}
	if got := sv.reseedTotal.Load(); got != priorTotal {
		t.Fatalf("expected reseedTotal untouched at %d, got %d", priorTotal, got)
	}
}

// TestSstSendStream_SetsTotalWhenNotDecompressing guards the streaming path's
// total semantics for the common case: no on-the-fly decompression, so the
// opener's expectedSize is exactly what ends up on the wire.
func TestSstSendStream_SetsTotalWhenNotDecompressing(t *testing.T) {
	cluster := newTestSSTCluster(t)
	sv := &ServerMonitor{Host: "dest", SSTPort: "9999"}

	content := []byte("stream me please, this is the payload\n")
	opener := func() (io.ReadCloser, int64, error) {
		return io.NopCloser(bytes.NewReader(content)), int64(len(content)), nil
	}

	clientConn, serverConn := net.Pipe()
	received, drained := drainPipeInto(serverConn)

	err := cluster.sstSendStream(clientConn, "source.bin", opener, sv, false, newReseedProgressSink(sv))
	clientConn.Close()
	<-drained

	if err != nil {
		t.Fatalf("sstSendStream failed: %v", err)
	}
	if got := sv.reseedBytes.Load(); got != int64(len(content)) {
		t.Fatalf("expected reseedBytes=%d, got %d", len(content), got)
	}
	if got := sv.reseedTotal.Load(); got != int64(len(content)) {
		t.Fatalf("expected reseedTotal=%d (expectedSize, not decompressing), got %d", len(content), got)
	}
	if received.String() != string(content) {
		t.Fatalf("received data mismatch: got %q, want %q", received.String(), content)
	}
}

// TestSstSendStream_LeavesTotalUnknownWhenDecompressing guards the streaming
// path's equivalent of the SSTRunSendGzip case: decompressing on the fly
// means the opener's expectedSize (the compressed source size) doesn't match
// bytes actually sent (decompressed), so reseedTotal must stay 0.
func TestSstSendStream_LeavesTotalUnknownWhenDecompressing(t *testing.T) {
	cluster := newTestSSTCluster(t)
	sv := &ServerMonitor{Host: "dest", SSTPort: "9999"}

	content := []byte("decompressed payload, made a bit longer so gzip framing overhead is not the whole story\n")
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(content); err != nil {
		t.Fatalf("failed to write gzip content: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}

	opener := func() (io.ReadCloser, int64, error) {
		return io.NopCloser(bytes.NewReader(compressed.Bytes())), int64(compressed.Len()), nil
	}

	clientConn, serverConn := net.Pipe()
	received, drained := drainPipeInto(serverConn)

	err := cluster.sstSendStream(clientConn, "source.bin.gz", opener, sv, true, newReseedProgressSink(sv))
	clientConn.Close()
	<-drained

	if err != nil {
		t.Fatalf("sstSendStream failed: %v", err)
	}
	if got := sv.reseedBytes.Load(); got != int64(len(content)) {
		t.Fatalf("expected reseedBytes=%d (decompressed size actually sent), got %d", len(content), got)
	}
	if got := sv.reseedTotal.Load(); got != 0 {
		t.Fatalf("expected reseedTotal to stay 0 (source expected size doesn't match decompressed sent bytes), got %d", got)
	}
	if received.String() != string(content) {
		t.Fatalf("received data mismatch: got %q, want %q", received.String(), content)
	}
}
