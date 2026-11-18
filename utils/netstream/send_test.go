package netstream

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
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

// The two-way point: a receiver that closes first (a backup cancelled on replication-manager's
// side) ends the send at once, while the input sends nothing (a dump client waiting on a lock).
func TestSendEndsWhenTheReceiverClosesFirst(t *testing.T) {
	for _, gz := range []bool{false, true} {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			c, err := l.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 5)
			io.ReadFull(c, buf) // the first bytes, then the receiver gives up
			c.Close()
		}()
		in, w := io.Pipe() // never closed: the input stays silent after its first bytes
		go w.Write([]byte("first"))
		start := time.Now()
		_, err = Send(in, l.Addr().String(), Options{Gzip: gz})
		l.Close()
		if !errors.Is(err, ErrReceiverClosed) {
			t.Fatalf("gzip %v: a receiver closing first is ErrReceiverClosed, got %v", gz, err)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Fatalf("gzip %v: the send ended %s after the receiver closed", gz, d)
		}
		w.Close()
	}
}

// A receiver that answers or keeps the connection open after the end changes nothing: the
// send ends with its input.
func TestSendIgnoresWhatTheReceiverSends(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	got := make(chan []byte, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		c.Write([]byte("hello from the receiver"))
		b, _ := io.ReadAll(c)
		got <- b
		time.Sleep(200 * time.Millisecond)
		c.Close()
	}()
	if n, err := Send(strings.NewReader("payload"), l.Addr().String(), Options{Header: []byte("H\n")}); err != nil || n != 7 {
		t.Fatalf("send: %d %v", n, err)
	}
	if b := <-got; string(b) != "H\npayload" {
		t.Fatalf("received %q", b)
	}
}

func TestAcceptCheckedRefusesAndTakesTheNextStream(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	check := func(r io.Reader) error {
		b := make([]byte, 3)
		if _, err := io.ReadFull(r, b); err != nil || string(b) != "OK\n" {
			return fmt.Errorf("bad header %q", b)
		}
		return nil
	}
	go func() {
		Send(strings.NewReader("forged"), ln.Addr().String(), Options{Header: []byte("NO\n")})
		Send(strings.NewReader("good"), ln.Addr().String(), Options{Header: []byte("OK\n")})
	}()
	var out bytes.Buffer
	if n, err := AcceptChecked(ln, &out, 5*time.Second, check); err != nil || out.String() != "good" || n != 4 {
		t.Fatalf("accepted %q (%d, %v), want the second stream only", out.String(), n, err)
	}

	ln2, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln2.Close()
	go Send(strings.NewReader("forged"), ln2.Addr().String(), Options{Header: []byte("NO\n")})
	if _, err := AcceptChecked(ln2, &out, time.Second, check); !errors.Is(err, ErrRefused) {
		t.Fatalf("no stream accepted within the time is ErrRefused, got %v", err)
	}
}
