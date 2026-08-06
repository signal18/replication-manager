// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"net"
	"testing"
	"time"

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
