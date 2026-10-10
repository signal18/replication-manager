package netstream

import (
	"bytes"
	"compress/gzip"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// receive accepts one connection and returns what it read until the sender's EOF.
func receive(t *testing.T) (string, <-chan []byte) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan []byte, 1)
	go func() {
		defer l.Close()
		c, err := l.Accept()
		if err != nil {
			got <- nil
			return
		}
		defer c.Close()
		b, _ := io.ReadAll(c)
		got <- b
	}()
	return l.Addr().String(), got
}

func TestSendPlain(t *testing.T) {
	payload := strings.Repeat("hello world, I am a database dump\n", 20000) // crosses many buffers
	addr, got := receive(t)
	n, err := Send(strings.NewReader(payload), addr, Options{})
	if err != nil || n != int64(len(payload)) {
		t.Fatalf("send: n=%d err=%v", n, err)
	}
	select {
	case b := <-got:
		if string(b) != payload {
			t.Fatalf("received %d bytes, want %d", len(b), len(payload))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the receiver never saw the end of the stream")
	}
}

func TestSendGzipIsStandardGzip(t *testing.T) {
	payload := strings.Repeat("CREATE TABLE t (id int);\n", 50000)
	addr, got := receive(t)
	if _, err := Send(strings.NewReader(payload), addr, Options{Gzip: true, CompressionLevel: 1}); err != nil {
		t.Fatal(err)
	}
	b := <-got
	if len(b) == 0 || len(b) >= len(payload) {
		t.Fatalf("compressed size %d of %d", len(b), len(payload))
	}
	zr, err := gzip.NewReader(bytes.NewReader(b)) // the stdlib reader: any gzip tool reads it
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil || string(plain) != payload {
		t.Fatalf("gunzip: %v, %d bytes", err, len(plain))
	}
}

func TestSendRefusedConnection(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	if _, err := Send(strings.NewReader("x"), addr, Options{ConnectTimeout: time.Second}); err == nil || !strings.Contains(err.Error(), "connect") {
		t.Fatalf("a closed port is a connect error: %v", err)
	}
}
