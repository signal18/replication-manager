package graphite

import (
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
}

func (f *fakeConn) Write(b []byte) (int, error) {
	f.written = append(f.written, b...)
	return len(b), nil
}

func (f *fakeConn) SetWriteDeadline(t time.Time) error {
	f.writeDeadlineSet = true
	f.writeDeadline = t
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
