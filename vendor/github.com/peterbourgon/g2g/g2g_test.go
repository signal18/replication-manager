package g2g

import (
	"expvar"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

var testExpvar = expvar.NewInt("i")

func TestPublish(t *testing.T) {
	testPub(t, "localhost:2003", NewMockGraphite(t, "tcp://:2003"))
	testPub(t, "unix:///tmp/test.sock", NewMockGraphite(t, "unix:///tmp/test.sock"))
}

func testPub(t *testing.T, address string, mock *MockGraphite) {
	// setup
	d := 25 * time.Millisecond
	var g *Graphite
	g = NewGraphite(address, d, d)

	// register, wait, check
	testExpvar.Set(34)
	g.Register("test.foo.i", testExpvar)

	time.Sleep(2 * d)
	count := mock.Count()
	if !(0 < count && count <= 2) {
		t.Errorf("expected 0 < publishes <= 2, got %d", count)
	}
	t.Logf("after %s, count=%d", 2*d, count)

	time.Sleep(2 * d)
	count = mock.Count()
	if !(1 < count && count <= 4) {
		t.Errorf("expected 1 < publishes <= 4, got %d", count)
	}
	t.Logf("after second %s, count=%d", 2*d, count)

	// teardown
	ok := make(chan bool)
	go func() {
		g.Shutdown()
		mock.Shutdown()
		ok <- true
	}()
	select {
	case <-ok:
		t.Logf("shutdown OK")
	case <-time.After(d):
		t.Errorf("timeout during shutdown")
	}

}

func TestRoundFloat(t *testing.T) {
	m := map[string]string{
		"abc":   "abc",
		"0.00.": "0.00.",
		"123":   "123",
		"1.2.3": "1.2.3",

		"1.00":        "1.00",
		"1.001":       "1.00",
		"1.00000001":  "1.00",
		"0.00001":     "0.00",
		"0.01000":     "0.01",
		"0.01999":     "0.02",
		"-1.234":      "-1.23",
		"123.456":     "123.46",
		"99999.09123": "99999.09",
	}
	for s, expected := range m {
		if got := roundFloat(s, 2); got != expected {
			t.Errorf("%s: got %s, expected %s", s, got, expected)
		}
	}
}

//
//
//

type MockGraphite struct {
	t       *testing.T
	network string
	address string
	count   int
	mtx     sync.Mutex
	ln      net.Listener
	done    chan bool
}

func NewMockGraphite(t *testing.T, address string) *MockGraphite {
	network, address := splitEndpoint(address)
	m := &MockGraphite{
		t:       t,
		network: network,
		address: address,
		count:   0,
		mtx:     sync.Mutex{},
		ln:      nil,
		done:    make(chan bool, 1),
	}
	go m.loop()
	return m
}

func (m *MockGraphite) Count() int {
	m.mtx.Lock()
	defer m.mtx.Unlock()
	return m.count
}

func (m *MockGraphite) Shutdown() {
	if m.ln != nil {
		m.ln.Close()
		<-m.done
	}
}

func (m *MockGraphite) loop() {
	ln, err := net.Listen(m.network, m.address)
	if err != nil {
		panic(err)
	}
	m.ln = ln
	for {
		conn, err := m.ln.Accept()
		if err != nil {
			m.done <- true
			return
		}
		go m.handle(conn)
	}
}

func (m *MockGraphite) handle(conn net.Conn) {
	b := make([]byte, 1024)
	for {
		n, err := conn.Read(b)
		if err != nil {
			m.t.Logf("Mock Graphite: read error: %s", err)
			return
		}
		if n > 256 {
			m.t.Errorf("Mock Graphite: read %dB: too much data", n)
			return
		}
		s := strings.TrimSpace(string(b[:n]))
		m.t.Logf("Mock Graphite: read %dB: %s", n, s)
		m.mtx.Lock()
		m.count++
		m.mtx.Unlock()
	}
}
