package graphite

import (
	"errors"
	"net"
	"testing"
	"time"
)

// fakeConn is a minimal net.Conn stub that records writes and whether a
// write deadline was set, without doing any real network I/O.
type fakeConn struct {
	net.Conn
	written          []byte
	writeDeadline    time.Time
	writeDeadlineSet bool
	writeErr         error
	deadlineErr      error
	closed           bool
}

func (f *fakeConn) Write(b []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	f.written = append(f.written, b...)
	return len(b), nil
}

func (f *fakeConn) SetWriteDeadline(t time.Time) error {
	f.writeDeadlineSet = true
	f.writeDeadline = t
	return f.deadlineErr
}

func (f *fakeConn) Close() error {
	f.closed = true
	return nil
}

func TestSendMetrics_SetsWriteDeadlineBeforeWrite(t *testing.T) {
	conn := &fakeConn{}
	g := &Graphite{Protocol: "tcp", Timeout: 5 * time.Second, conn: conn}

	before := time.Now()
	err := g.SendMetrics([]Metric{NewMetric("test.metric", "1", 1700000000)})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !conn.writeDeadlineSet {
		t.Fatal("expected SetWriteDeadline to be called before writing")
	}
	if conn.writeDeadline.Before(before.Add(g.Timeout - time.Second)) {
		t.Fatalf("write deadline %v does not reflect graphite.Timeout (%v) from %v", conn.writeDeadline, g.Timeout, before)
	}
	if len(conn.written) == 0 {
		t.Fatal("expected metric data to be written")
	}
}

func TestSendMetrics_NopSkipsWrite(t *testing.T) {
	conn := &fakeConn{}
	g := &Graphite{Protocol: "tcp", Timeout: time.Second, conn: conn, nop: true}

	if err := g.SendMetrics([]Metric{NewMetric("test.metric", "1", 1700000000)}); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if conn.writeDeadlineSet || len(conn.written) != 0 {
		t.Fatal("nop graphite must not touch the connection at all")
	}
}

func TestConnect_ReusesHealthyConnection(t *testing.T) {
	conn := &fakeConn{}
	g := &Graphite{Protocol: "tcp", conn: conn}

	if err := g.Connect(); err != nil {
		t.Fatalf("expected nil error reusing a healthy connection, got %v", err)
	}
	if g.conn != conn {
		t.Fatal("expected Connect() to keep the existing connection instead of redialing")
	}
	if conn.closed {
		t.Fatal("expected Connect() not to close a healthy, still-referenced connection")
	}
}

func TestConnect_RedialsWhenConnectionIsNil(t *testing.T) {
	// Reserve then immediately free a port so dialing it is guaranteed to be
	// refused (deterministic, unlike assuming a fixed low port is free).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve a port: %v", err)
	}
	freedPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	g := &Graphite{Protocol: "tcp", Host: "127.0.0.1", Port: freedPort, Timeout: time.Second}

	if err := g.Connect(); err == nil {
		t.Fatal("expected a dial error when conn is nil and the sink is unreachable")
	}
}

func TestSendMetrics_WriteFailureNilsConnection(t *testing.T) {
	conn := &fakeConn{writeErr: errors.New("broken pipe")}
	g := &Graphite{Protocol: "tcp", Timeout: time.Second, conn: conn}

	if err := g.SendMetrics([]Metric{NewMetric("test.metric", "1", 1700000000)}); err == nil {
		t.Fatal("expected an error from a failed write")
	}
	if g.conn != nil {
		t.Fatal("expected conn to be nilled out after a write failure so the next Connect() redials")
	}
	if !conn.closed {
		t.Fatal("expected the broken connection to be closed before being discarded")
	}
}

func TestSendMetrics_SetWriteDeadlineFailureNilsConnection(t *testing.T) {
	conn := &fakeConn{deadlineErr: errors.New("use of closed network connection")}
	g := &Graphite{Protocol: "tcp", Timeout: time.Second, conn: conn}

	if err := g.SendMetrics([]Metric{NewMetric("test.metric", "1", 1700000000)}); err == nil {
		t.Fatal("expected an error from a failed SetWriteDeadline")
	}
	if g.conn != nil {
		t.Fatal("expected conn to be nilled out after a SetWriteDeadline failure so the next Connect() redials")
	}
	if len(conn.written) != 0 {
		t.Fatal("expected no write to be attempted after SetWriteDeadline failed")
	}
}
