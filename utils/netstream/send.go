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
	"fmt"
	"io"
	"net"
	"runtime"
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
}

// Send copies in to addr (host:port) and closes the write side, so the receiver sees the
// end of the stream. It returns the bytes read from in (before compression).
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

	var out io.Writer = conn
	var gz *pgzip.Writer
	if opts.Gzip {
		level := opts.CompressionLevel
		if level < 1 || level > 9 {
			level = pgzip.DefaultCompression
		}
		if gz, err = pgzip.NewWriterLevel(conn, level); err != nil {
			return 0, fmt.Errorf("gzip writer: %w", err)
		}
		gz.SetConcurrency(1<<20, runtime.NumCPU())
		out = gz
	}

	n, err := io.Copy(out, in)
	if err != nil {
		return n, fmt.Errorf("send to %s: %w", addr, err)
	}
	if gz != nil {
		if err := gz.Close(); err != nil {
			return n, fmt.Errorf("flush gzip to %s: %w", addr, err)
		}
	}
	// half-close: the receiver reads EOF while the socket stays readable
	switch c := conn.(type) {
	case *net.TCPConn:
		err = c.CloseWrite()
	case *tls.Conn:
		err = c.CloseWrite()
	}
	if err != nil {
		return n, fmt.Errorf("close stream to %s: %w", addr, err)
	}
	return n, nil
}

// Receive listens on addr, accepts ONE connection and copies what it sends to out until the
// sender closes: the receiving side of Send, for a jobs script that has no socat (the
// stored backup streamed by replication-manager to a PostgreSQL sidecar). The listener
// waits at most accept for the connection.
func Receive(addr string, out io.Writer, accept time.Duration) (int64, error) {
	if accept <= 0 {
		accept = 10 * time.Minute
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	if tl, ok := ln.(*net.TCPListener); ok {
		tl.SetDeadline(time.Now().Add(accept))
	}
	conn, err := ln.Accept()
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	return io.Copy(out, conn)
}
