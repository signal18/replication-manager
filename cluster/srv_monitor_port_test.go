// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

func newMonitorPortTestServer(extraPort int, port string) *ServerMonitor {
	c := &Cluster{Name: "c1", Conf: &config.Config{Timeout: 2, ReadTimeout: 2, DbServersMonitorExtraPort: extraPort}}
	s := &ServerMonitor{URL: "127.0.0.1:" + port, Host: "127.0.0.1", Port: port, User: "u", Pass: "p", ClusterGroup: c, TLSConfigUsed: ConstTLSCurrentConfig}
	c.Servers = []*ServerMonitor{s}
	s.SetDSN()
	return s
}

// closedPort returns a local port nothing listens on: a connection there is refused.
func closedPort(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	return port
}

// Without db-servers-monitor-extra-port the DSN stays on the db-servers-hosts port and
// there is no service DSN to fall back to (#2150).
func TestMonitorExtraPortUnsetKeepsServicePort(t *testing.T) {
	s := newMonitorPortTestServer(0, "3306")
	if !strings.Contains(s.DSN, "tcp(127.0.0.1:3306)") {
		t.Fatalf("DSN should use the service port, got %q", s.DSN)
	}
	if dsn := s.extraPortFallback.getServiceDSN(); dsn != "" {
		t.Fatalf("no service DSN expected without an extra port, got %q", dsn)
	}
	if got := s.monitorExtraPort(); got != "3306" {
		t.Fatalf("monitorExtraPort = %q, want 3306", got)
	}
}

// With the extra port, repman's DSN goes there and the service DSN keeps the port of
// db-servers-hosts; server.Port, used by proxies and replication, does not move.
func TestMonitorExtraPortMovesOnlyTheMonitoringDSN(t *testing.T) {
	s := newMonitorPortTestServer(3308, "3306")
	if !strings.Contains(s.DSN, "tcp(127.0.0.1:3308)") {
		t.Fatalf("DSN should use the extra port, got %q", s.DSN)
	}
	if dsn := s.extraPortFallback.getServiceDSN(); !strings.Contains(dsn, "tcp(127.0.0.1:3306)") {
		t.Fatalf("service DSN should use the service port, got %q", dsn)
	}
	if s.Port != "3306" {
		t.Fatalf("server.Port moved to %q, proxies and replication would follow it", s.Port)
	}
}

// The extra port is the same as the service port: nothing to fall back to.
func TestMonitorExtraPortEqualToServicePort(t *testing.T) {
	s := newMonitorPortTestServer(3306, "3306")
	if dsn := s.extraPortFallback.getServiceDSN(); dsn != "" {
		t.Fatalf("no service DSN expected when both ports are equal, got %q", dsn)
	}
}

// Through an SSH tunnel the DSN goes to the tunnel port: the extra port is not used.
func TestMonitorExtraPortIgnoredThroughTunnel(t *testing.T) {
	s := newMonitorPortTestServer(3308, "3306")
	s.ClusterGroup.Conf.TunnelHost = "bastion:22"
	if got := s.monitorExtraPort(); got != "3306" {
		t.Fatalf("monitorExtraPort through a tunnel = %q, want the service port", got)
	}
}

// Setting it live rebuilds every server's DSN, the next Ping connects there.
func TestSetMonitorExtraPortRebuildsDSN(t *testing.T) {
	s := newMonitorPortTestServer(0, "3306")
	s.ClusterGroup.SetMonitorExtraPort(3308)
	if !strings.Contains(s.DSN, "tcp(127.0.0.1:3308)") || s.extraPortFallback.getServiceDSN() == "" {
		t.Fatalf("DSN not rebuilt on the extra port: %q / %q", s.DSN, s.extraPortFallback.getServiceDSN())
	}
	s.ClusterGroup.SetMonitorExtraPort(0)
	if !strings.Contains(s.DSN, "tcp(127.0.0.1:3306)") || s.extraPortFallback.getServiceDSN() != "" {
		t.Fatalf("DSN not rebuilt back on the service port: %q / %q", s.DSN, s.extraPortFallback.getServiceDSN())
	}
}

// Only a port that does not answer or is full sends repman to the service port: an
// access denied would be the same there.
func TestExtraPortUnusable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"network", errors.New("dial tcp 127.0.0.1:3308: connect: connection refused"), true},
		{"too many connections", &mysql.MySQLError{Number: 1040}, true},
		{"access denied", &mysql.MySQLError{Number: 1045}, false},
	}
	for _, c := range cases {
		if got := extraPortUnusable(c.err); got != c.want {
			t.Errorf("%s: extraPortUnusable = %v, want %v", c.name, got, c.want)
		}
	}
}

// A refused extra port starts the fallback window: the connection is tried on the
// service port, and the extra port is not tried again before monitorExtraPortRetry.
func TestRefusedExtraPortStartsFallbackWindow(t *testing.T) {
	extra, _ := strconv.Atoi(closedPort(t))
	s := newMonitorPortTestServer(extra, closedPort(t))
	conn, port, err := s.getNewDBConnPort()
	if err == nil {
		conn.Close()
		t.Fatal("expected both ports refused")
	}
	if port != s.Port {
		t.Fatalf("port = %q, want the service port %q", port, s.Port)
	}
	inFallback, reason := s.extraPortFallback.active(time.Now())
	if !inFallback || !strings.Contains(reason, "refused") {
		t.Fatalf("fallback window not started: %v %q", inFallback, reason)
	}
	if inFallback, _ := s.extraPortFallback.active(time.Now().Add(monitorExtraPortRetry + time.Second)); inFallback {
		t.Fatal("fallback window should end after monitorExtraPortRetry")
	}
}

// The pool only moves when the connection came on another port than the pool's.
func TestMoveGlobalPoolSamePortIsNoop(t *testing.T) {
	s := newMonitorPortTestServer(3308, "3306")
	s.poolPort = "3308"
	if s.moveGlobalPool(nil, "3306") {
		t.Fatal("a nil connection must not become the pool")
	}
	if s.moveGlobalPool(s.Conn, "3308") {
		t.Fatal("no pool to move yet")
	}
}

// fakeServicePort makes the service port answer, through sqlmock, and returns the DSNs it
// was asked for.
func fakeServicePort(t *testing.T) *[]string {
	asked := &[]string{}
	previous := connectServicePort
	connectServicePort = func(dsn string, seconds int) (*sqlx.DB, error) {
		*asked = append(*asked, dsn)
		db, _, err := sqlmock.New()
		if err != nil {
			return nil, err
		}
		return sqlx.NewDb(db, "mysql"), nil
	}
	t.Cleanup(func() { connectServicePort = previous })
	return asked
}

// The case the fallback exists for: the extra port refuses, the service port answers.
// repman gets a connection on the service port, WARN0267 opens, and inside the window
// the next connection goes to the service port without trying the extra port again.
func TestRefusedExtraPortFallsBackToServicePort(t *testing.T) {
	asked := fakeServicePort(t)
	extra, _ := strconv.Atoi(closedPort(t))
	s := newMonitorPortTestServer(extra, "3306")
	s.ClusterGroup.StateMachine = new(state.StateMachine)
	s.ClusterGroup.StateMachine.Init()

	conn, port, err := s.getNewDBConnPort()
	if err != nil {
		t.Fatalf("expected the service port to answer, got %v", err)
	}
	defer conn.Close()
	if port != "3306" {
		t.Fatalf("port = %q, want the service port", port)
	}
	if len(*asked) != 1 || !strings.Contains((*asked)[0], "tcp(127.0.0.1:3306)") {
		t.Fatalf("service port not used: %v", *asked)
	}
	if !s.ClusterGroup.StateMachine.IsInState(state.BuildStateKey("WARN0267", s.URL)) {
		t.Fatal("WARN0267 not raised")
	}

	started := time.Now()
	conn2, port2, err := s.getNewDBConnPort()
	if err != nil || port2 != "3306" {
		t.Fatalf("inside the window: port %q err %v", port2, err)
	}
	conn2.Close()
	if len(*asked) != 2 {
		t.Fatalf("second connection should go to the service port, asked %v", *asked)
	}
	if time.Since(started) > time.Second {
		t.Fatal("the extra port was tried again inside the window")
	}
}

// A connection opened on another port than the pool's becomes the pool.
func TestMoveGlobalPoolSwapsOnPortChange(t *testing.T) {
	s := newMonitorPortTestServer(3308, "3306")
	s.ClusterGroup.Conf.Verbose = false
	oldDB, _, _ := sqlmock.New()
	newDB, _, _ := sqlmock.New()
	defer newDB.Close()
	s.Conn = sqlx.NewDb(oldDB, "mysql")
	s.poolPort = "3308"
	conn := sqlx.NewDb(newDB, "mysql")
	if !s.moveGlobalPool(conn, "3306") {
		t.Fatal("the pool should move to the service port connection")
	}
	if s.Conn != conn || s.poolPort != "3306" {
		t.Fatalf("pool not swapped: poolPort %q", s.poolPort)
	}
}
