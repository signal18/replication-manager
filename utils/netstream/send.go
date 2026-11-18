// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

// Package netstream sends a byte stream to a TCP receiver: what the jobs scripts did
// with `socat -u STDIN TCP:host:port`, without depending on socat being in the
// database image (the PostgreSQL image has neither socat nor nc). Used by
// `replication-manager-cli stream`, which the init container already delivers next to
// the jobs script.
package netstream

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/klauspost/pgzip"
)

// Options of one send. The zero value is a plain TCP copy.
type Options struct {
	TLS              bool          // wrap the connection in TLS (the receiver's OPENSSL listener)
	TLSSkipVerify    bool          // accept the receiver's certificate unchecked, like socat verify=0
	Gzip             bool          // compress on the sender, parallel gzip (standard gzip format)
	CompressionLevel int           // gzip level 1..9, 0 = default
	ConnectTimeout   time.Duration // 0 = 10s
	Header           []byte        // written first, uncompressed (the job stream header), nil for none
}

// ErrReceiverClosed: the receiver closed the connection before the end of the stream (a
// backup cancelled or failed on its side). Send returns it as soon as that happens, while
// in may still be sending nothing (a dump client blocked on a lock): the sender is then
// stopped by its caller, not left running under its lock.
var ErrReceiverClosed = errors.New("the receiver closed the stream")

// ErrRefused: the listener took no stream, every connection failed its check (the job
// stream header).
var ErrRefused = errors.New("stream refused")

// Send copies in to addr (host:port) and closes the write side, so the receiver sees the
// end of the stream. It returns the bytes read from in (before compression).
//
// The stream is two-way, as socat's was: the connection is read too, so Send ends with
// ErrReceiverClosed when the receiver closes it first, without waiting for in. What the
// receiver sends is discarded. The copy from in is then left behind, blocked at most until
// in ends (the CLI exits at once).
func Send(in io.Reader, addr string, opts Options) (int64, error) {
	timeout := opts.ConnectTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout}
	var conn net.Conn
	var err error
	if opts.TLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{InsecureSkipVerify: opts.TLSSkipVerify})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return 0, fmt.Errorf("connect %s: %w", addr, err)
	}
	defer conn.Close()
	if len(opts.Header) > 0 {
		if _, err := conn.Write(opts.Header); err != nil {
			return 0, fmt.Errorf("send to %s: %w", addr, err)
		}
	}

	closed := make(chan struct{})
	go func() {
		io.Copy(io.Discard, conn)
		close(closed)
	}()
	counted := &countingReader{r: in}
	sent := make(chan error, 1)
	go func() { sent <- copyTo(conn, counted, opts) }()

	select {
	case err = <-sent:
	case <-closed:
		select {
		case err = <-sent: // ended together: the copy's own outcome
		default:
			return counted.n.Load(), fmt.Errorf("send to %s: %w", addr, ErrReceiverClosed)
		}
	}
	if err != nil {
		return counted.n.Load(), fmt.Errorf("send to %s: %w", addr, err)
	}
	return counted.n.Load(), nil
}

// copyTo writes in to conn, through parallel gzip when asked, and closes the write side.
func copyTo(conn net.Conn, in io.Reader, opts Options) error {
	var out io.Writer = conn
	var gz *pgzip.Writer
	if opts.Gzip {
		level := opts.CompressionLevel
		if level < 1 || level > 9 {
			level = pgzip.DefaultCompression
		}
		var err error
		if gz, err = pgzip.NewWriterLevel(conn, level); err != nil {
			return fmt.Errorf("gzip writer: %w", err)
		}
		// blocks of 1 MiB, one per CPU: the backup is the job's whole work
		gz.SetConcurrency(1<<20, runtime.NumCPU())
		out = gz
	}
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if gz != nil {
		if err := gz.Close(); err != nil {
			return fmt.Errorf("flush gzip: %w", err)
		}
	}
	// half-close: the receiver reads EOF, the connection stays open for its close
	switch c := conn.(type) {
	case *net.TCPConn:
		return c.CloseWrite()
	case *tls.Conn:
		return c.CloseWrite()
	}
	return nil
}

type countingReader struct {
	r io.Reader
	n atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// Receive listens on addr, accepts ONE connection and copies what it sends to out until the
// sender closes: the receiving side of Send, for a jobs script that has no socat (the
// stored backup streamed by replication-manager to a PostgreSQL sidecar). The listener
// waits at most accept for the connection.
func Receive(addr string, out io.Writer, accept time.Duration) (int64, error) {
	return ReceiveChecked(addr, out, accept, nil)
}

// ReceiveChecked is Receive taking only a connection check accepts (the job stream header,
// read from the connection before its data): a refused connection is closed and the listener
// waits for the next one, within the same accept time. ErrRefused (with the last refusal) is
// returned when none was accepted.
func ReceiveChecked(addr string, out io.Writer, accept time.Duration, check func(io.Reader) error) (int64, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return AcceptChecked(ln, out, accept, check)
}

// AcceptChecked is ReceiveChecked on a listener already open: the caller opens it before it
// asks replication-manager to send (the jobs upgrade, the config), so the sender never finds
// the port closed.
func AcceptChecked(ln net.Listener, out io.Writer, accept time.Duration, check func(io.Reader) error) (int64, error) {
	if accept <= 0 {
		accept = 10 * time.Minute
	}
	if tl, ok := ln.(*net.TCPListener); ok {
		tl.SetDeadline(time.Now().Add(accept))
	}
	var refused error
	for {
		conn, err := ln.Accept()
		if err != nil {
			if refused != nil {
				return 0, fmt.Errorf("%w: %v", ErrRefused, refused)
			}
			return 0, err
		}
		if check != nil {
			conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			refused = check(conn)
			conn.SetReadDeadline(time.Time{})
			if refused != nil {
				conn.Close()
				continue
			}
		}
		defer conn.Close()
		return io.Copy(out, conn)
	}
}
