package cluster

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// #2196: a port that accepts the connection and never speaks first (PostgreSQL declared as
// MySQL) is given up at the connect timeout, not at the DSN's readTimeout (an hour).
func TestConnectBoundedSilentServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	held := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			held <- c // accepted, never written to: the MySQL greeting never comes
		}
	}()
	defer func() {
		for len(held) > 0 {
			(<-held).Close()
		}
	}()
	dsn := fmt.Sprintf("root:x@tcp(%s)/?timeout=1s&readTimeout=3600s", ln.Addr())
	start := time.Now()
	db, err := connectBounded("mysql", dsn, 1)
	if err == nil {
		db.Close()
		t.Fatal("a server that never greets must not connect")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("the connect waited %s: it must give up at the connect timeout (1s)", d)
	}
}

// #2196: the gateway reading does not take the cluster lock, which TopologyDiscover holds
// while it pings: the collector of every cluster never waits on one cluster's tick.
func TestSetGatewayTrafficWhileClusterLocked(t *testing.T) {
	c := &Cluster{Name: "c1", Conf: &config.Config{}}
	c.NewClusterGraphite()
	c.Lock()
	defer c.Unlock()
	done := make(chan struct{})
	go func() {
		c.SetGatewayTraffic(1000, 10, 100, 0, 1, time.Now())
		_ = c.GetGatewayUnits()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SetGatewayTraffic waited on the cluster lock")
	}
	if c.GetGatewayUnits() == nil {
		t.Fatal("the reading must be stored")
	}
}
