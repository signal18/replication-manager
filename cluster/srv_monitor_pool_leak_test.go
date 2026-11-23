// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/sirupsen/logrus"
)

// openSQLPools counts the database/sql pools still open in the process: each one runs
// its connectionOpener goroutine until it is closed.
func openSQLPools() int {
	buf := make([]byte, 1<<22)
	n := runtime.Stack(buf, true)
	return strings.Count(string(buf[:n]), "database/sql.(*DB).connectionOpener")
}

// waitOpenSQLPoolsAtMost polls until at most want pools are open (a closed pool's
// goroutine exits asynchronously), and returns the last count.
func waitOpenSQLPoolsAtMost(want int) int {
	got := openSQLPools()
	for deadline := time.Now().Add(2 * time.Second); got > want && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
		got = openSQLPools()
	}
	return got
}

// A monitoring connect that fails must not leave its pool open. The monitor connects to
// every server at every tick: a server that does not answer leaked one pool (and its
// connectionOpener goroutine) per tick, millions after a few days, until the monitor ran
// out of memory.
func TestFailedMonitorConnectClosesItsPool(t *testing.T) {
	cases := []struct {
		name   string
		listen func(t *testing.T) string
	}{
		{"refused", closedPort},
		{"closed after accept", acceptAndClosePort},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := tc.listen(t)
			c := &Cluster{Name: "c1", Conf: &config.Config{Timeout: 1, ReadTimeout: 1}}
			s := &ServerMonitor{URL: "127.0.0.1:" + port, Host: "127.0.0.1", Port: port, User: "u", Pass: "p", ClusterGroup: c, TLSConfigUsed: ConstTLSCurrentConfig}
			c.Servers = []*ServerMonitor{s}
			s.SetDSN()

			before := waitOpenSQLPoolsAtMost(0)
			const ticks = 20
			returned := 0
			for i := 0; i < ticks; i++ {
				// Ping drops the connection of a failed connect without closing it.
				conn, err := s.GetNewDBConn()
				if err == nil {
					t.Fatal("expected the connect to fail")
				}
				if conn != nil {
					returned++
				}
			}
			if returned > 0 {
				t.Errorf("%d of %d failed connects returned a pool: a caller dropping it on error leaks it", returned, ticks)
			}
			if after := waitOpenSQLPoolsAtMost(before); after > before {
				t.Fatalf("%d failed connects left %d pools open (connectionOpener goroutines %d -> %d)", ticks, after-before, before, after)
			}
		})
	}
}

// The monitor tick on failing servers keeps the number of open pools bounded: Ping runs
// many times on a server that refuses the connection, one that closes it during the
// handshake, and one that answers access denied (1045, Ping's early return), and no pool
// is left open by any of them. A stability guard for the whole connect path of Ping.
func TestPingOnFailingServersLeavesNoPool(t *testing.T) {
	cases := []struct {
		name   string
		listen func(t *testing.T) string
	}{
		{"refused", closedPort},
		{"closed after accept", acceptAndClosePort},
		{"access denied 1045", accessDeniedPort},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := tc.listen(t)
			c := &Cluster{
				Name:         "c1",
				Conf:         &config.Config{Timeout: 1, ReadTimeout: 1, CheckType: "tcp", MaxFail: 3, WorkingDir: t.TempDir()},
				StateMachine: &state.StateMachine{},
				Logrus:       logrus.New(),
			}
			c.StateMachine.Init()
			s := &ServerMonitor{URL: "127.0.0.1:" + port, Host: "127.0.0.1", Port: port, User: "u", Pass: "p", ClusterGroup: c, TLSConfigUsed: ConstTLSCurrentConfig, Datadir: t.TempDir()}
			c.Servers = []*ServerMonitor{s}
			s.SetDSN()

			before := waitOpenSQLPoolsAtMost(0)
			const ticks = 30
			for i := 0; i < ticks; i++ {
				var wg sync.WaitGroup
				wg.Add(1)
				s.Ping(&wg)
				wg.Wait()
			}
			if s.Conn != nil {
				t.Fatal("a server that never answered was given a global pool")
			}
			if after := waitOpenSQLPoolsAtMost(before); after > before {
				t.Fatalf("%d monitor ticks on a failing server left %d pools open (connectionOpener goroutines %d -> %d)", ticks, after-before, before, after)
			}
		})
	}
}

// accessDeniedPort returns a local port whose server greets with an access denied error
// (1045), the error of a monitoring user whose password is wrong.
func accessDeniedPort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	payload := append([]byte{0xff, 0x15, 0x04, '#'}, []byte("28000Access denied for user 'u'")...)
	packet := append([]byte{byte(len(payload)), byte(len(payload) >> 8), byte(len(payload) >> 16), 0}, payload...)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Write(packet)
			c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return port
}

// acceptAndClosePort returns a local port that accepts a connection and closes it at
// once: the handshake fails after the dial succeeded.
func acceptAndClosePort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return port
}
