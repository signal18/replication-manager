// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

func TestHaproxyHasAvailableReader(t *testing.T) {
	tests := []struct {
		name     string
		topology string
		backends []Backend
		want     bool
	}{
		{
			name:     "no backends",
			topology: config.TopoMasterSlave,
			backends: nil,
			want:     false,
		},
		{
			name:     "only master entry UP",
			topology: config.TopoMasterSlave,
			backends: []Backend{
				{Host: "127.0.0.1", Port: "3306", Status: stateMaster, PrxStatus: "UP"},
			},
			want: false,
		},
		{
			name:     "master UP and slave DRAIN",
			topology: config.TopoMasterSlave,
			backends: []Backend{
				{Host: "127.0.0.1", Port: "3306", Status: stateMaster, PrxStatus: "UP"},
				{Host: "127.0.0.1", Port: "3307", Status: stateSlave, PrxStatus: "DRAIN"},
			},
			want: false,
		},
		{
			name:     "master DRAIN and slave UP",
			topology: config.TopoMasterSlave,
			backends: []Backend{
				{Host: "127.0.0.1", Port: "3306", Status: stateMaster, PrxStatus: "DRAIN"},
				{Host: "127.0.0.1", Port: "3307", Status: stateSlave, PrxStatus: "UP"},
			},
			want: true,
		},
		{
			// Regression test: a Wsrep leader's repman state is stateWsrep, never
			// stateMaster, so its read-backend row must be excluded by host/port
			// identity against cluster.GetMaster(), not by Status.
			name:     "wsrep leader only entry UP is excluded",
			topology: config.TopoMultiMasterWsrep,
			backends: []Backend{
				{Host: "127.0.0.1", Port: "3306", Status: stateWsrep, PrxStatus: "UP"},
			},
			want: false,
		},
		{
			name:     "wsrep leader UP plus a second wsrep node UP returns true",
			topology: config.TopoMultiMasterWsrep,
			backends: []Backend{
				{Host: "127.0.0.1", Port: "3306", Status: stateWsrep, PrxStatus: "UP"},
				{Host: "127.0.0.1", Port: "3307", Status: stateWsrep, PrxStatus: "UP"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := setupTestCluster(t, 2)
			defer cleanupTestCluster(t, cluster)

			cluster.Servers[0].Host = "127.0.0.1"
			cluster.Servers[0].Port = "3306"
			cluster.Servers[1].Host = "127.0.0.1"
			cluster.Servers[1].Port = "3307"

			cluster.Topology = tt.topology
			cluster.master = cluster.Servers[0]
			cluster.vmaster = cluster.Servers[0]

			proxy := &HaproxyProxy{Proxy: Proxy{BackendsRead: tt.backends, ClusterGroup: cluster}}
			if got := proxy.HasAvailableReader(); got != tt.want {
				t.Errorf("HasAvailableReader() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHaproxyMasterShouldBeReader(t *testing.T) {
	tests := []struct {
		name         string
		readOnMaster bool
		noSlave      bool
		topology     string
		backends     []Backend
		want         bool
	}{
		{
			name:         "read on master always true",
			readOnMaster: true,
			noSlave:      false,
			topology:     config.TopoMasterSlave,
			backends: []Backend{
				{Status: stateMaster, PrxStatus: "DRAIN"},
				{Status: stateSlave, PrxStatus: "UP"},
			},
			want: true,
		},
		{
			name:         "both fallbacks disabled",
			readOnMaster: false,
			noSlave:      false,
			topology:     config.TopoMasterSlave,
			backends: []Backend{
				{Status: stateMaster, PrxStatus: "DRAIN"},
			},
			want: false,
		},
		{
			name:         "no-slave fallback with an available slave reader",
			readOnMaster: false,
			noSlave:      true,
			topology:     config.TopoMasterSlave,
			backends: []Backend{
				{Status: stateMaster, PrxStatus: "DRAIN"},
				{Status: stateSlave, PrxStatus: "UP"},
			},
			want: false,
		},
		{
			name:         "no-slave fallback with no available slave reader",
			readOnMaster: false,
			noSlave:      true,
			topology:     config.TopoMasterSlave,
			backends: []Backend{
				{Status: stateMaster, PrxStatus: "DRAIN"},
				{Status: stateSlave, PrxStatus: "MAINT"},
			},
			want: true,
		},
		{
			name:         "no-slave fallback with active-passive topology",
			readOnMaster: false,
			noSlave:      true,
			topology:     config.TopoActivePassive,
			backends: []Backend{
				{Status: stateMaster, PrxStatus: "DRAIN"},
				{Status: stateSlave, PrxStatus: "UP"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := setupTestCluster(t, 2)
			defer cleanupTestCluster(t, cluster)

			cluster.StateMachine = new(state.StateMachine)
			cluster.StateMachine.Init()
			cluster.Topology = tt.topology
			cluster.Configurator.ClusterConfig.PRXServersReadOnMaster = tt.readOnMaster
			cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = tt.noSlave

			proxy := &HaproxyProxy{Proxy: Proxy{BackendsRead: tt.backends, ClusterGroup: cluster}}

			if got := proxy.masterShouldBeReader(); got != tt.want {
				t.Errorf("masterShouldBeReader() = %v, want %v", got, tt.want)
			}
		})
	}
}

// haproxyStatRow builds a "show stat" CSV row padded to 74 fields (Refresh()
// indexes up to line[73] and requires len(line) >= 73), populating only the
// columns the code reads: pxname (0), svname (1), status (17), addr (73).
func haproxyStatRow(pxname, svname, status, addr string) string {
	fields := make([]string, 74)
	fields[0], fields[1], fields[17], fields[73] = pxname, svname, status, addr
	return strings.Join(fields, ",")
}

// cmdIndex returns the index of the first occurrence of want in commands, or
// -1 if not present.
func cmdIndex(commands []string, want string) int {
	for i, c := range commands {
		if c == want {
			return i
		}
	}
	return -1
}

// TestHaproxyRefreshMasterFallbackSamePass reproduces the same-pass
// transition: a slave just went into maintenance (HAProxy stats still report
// it UP), and the master/leader's service_read entry is DRAIN. Refresh()
// must drain the slave to MAINT *and*, in the same pass, undrain the master
// so service_read is never left with zero UP entries.
func TestHaproxyRefreshMasterFallbackSamePass(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend: "service_write",
		HaproxyAPIReadBackend:  "service_read",
		HaproxyOn:              true,
	}
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true

	master := cluster.Servers[0]
	master.Id = "master1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.ClusterGroup = cluster

	slave := cluster.Servers[1]
	slave.Id = "slave1"
	slave.Host = "127.0.0.1"
	slave.Port = "3307"
	slave.State = stateMaintenance
	slave.IsMaintenance = true
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "DRAIN", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
	}, "\n")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake haproxy server: %v", err)
	}
	defer ln.Close()
	host, port, _ := net.SplitHostPort(ln.Addr().String())

	var mu sync.Mutex
	var commands []string
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				line, _ := bufio.NewReader(c).ReadString('\n')
				cmd := strings.TrimRight(line, "\r\n")
				mu.Lock()
				commands = append(commands, cmd)
				mu.Unlock()
				if cmd == "show stat" {
					c.Write([]byte(statResponse))
				}
			}(conn)
		}
	}()

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "test", // non-empty, skips GetVersion()
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	wantSlaveMaint := "set server service_read/slave1 state maint"
	wantMasterReady := "set server service_read/master1 state ready"

	hasCmd := func(want string) bool {
		for _, c := range commands {
			if c == want {
				return true
			}
		}
		return false
	}

	if !hasCmd(wantSlaveMaint) {
		t.Errorf("Refresh() commands = %v, want to contain %q", commands, wantSlaveMaint)
	}
	if !hasCmd(wantMasterReady) {
		t.Errorf("Refresh() commands = %v, want to contain %q (master should be undrained in the same pass once the slave is no longer an available reader)", commands, wantMasterReady)
	}
}

// TestHaproxyRefreshMasterDrainSamePass reproduces the reverse same-pass
// transition: a slave just came back from maintenance (repman state is
// stateSlave, not in maintenance) but HAProxy stats still report it MAINT,
// while the master/leader's service_read entry is UP (the no-slave fallback
// reader). Refresh() must bring the slave back to ready and, in the same
// pass, re-drain the master since a slave reader is available again.
func TestHaproxyRefreshMasterDrainSamePass(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend: "service_write",
		HaproxyAPIReadBackend:  "service_read",
		HaproxyOn:              true,
	}
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true

	master := cluster.Servers[0]
	master.Id = "master1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.ClusterGroup = cluster

	slave := cluster.Servers[1]
	slave.Id = "slave1"
	slave.Host = "127.0.0.1"
	slave.Port = "3307"
	slave.State = stateSlave
	slave.IsMaintenance = false
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "MAINT", "127.0.0.1:3307"),
	}, "\n")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake haproxy server: %v", err)
	}
	defer ln.Close()
	host, port, _ := net.SplitHostPort(ln.Addr().String())

	var mu sync.Mutex
	var commands []string
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				line, _ := bufio.NewReader(c).ReadString('\n')
				cmd := strings.TrimRight(line, "\r\n")
				mu.Lock()
				commands = append(commands, cmd)
				mu.Unlock()
				if cmd == "show stat" {
					c.Write([]byte(statResponse))
				}
			}(conn)
		}
	}()

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "test", // non-empty, skips GetVersion()
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	wantSlaveReady := "set server service_read/slave1 state ready"
	wantMasterDrain := "set server service_read/master1 state drain"

	hasCmd := func(want string) bool {
		for _, c := range commands {
			if c == want {
				return true
			}
		}
		return false
	}

	if !hasCmd(wantSlaveReady) {
		t.Errorf("Refresh() commands = %v, want to contain %q", commands, wantSlaveReady)
	}
	if !hasCmd(wantMasterDrain) {
		t.Errorf("Refresh() commands = %v, want to contain %q (master should be re-drained in the same pass once the slave is an available reader again)", commands, wantMasterDrain)
	}
}

// TestHaproxyRefreshMasterStaleMaintSamePass reproduces a third same-pass
// transition: the master/leader is healthy and not in maintenance
// (srv.IsMaintenance == false), but its service_read entry is stuck at MAINT
// in HAProxy, while a slave is already up and serving reads. The generic
// maintenance-reconciliation block un-maints the master's read entry to
// "ready" first; Refresh() must then, in the same pass, re-drain it since
// masterShouldBeReader() is false (a slave reader is available).
func TestHaproxyRefreshMasterStaleMaintSamePass(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend: "service_write",
		HaproxyAPIReadBackend:  "service_read",
		HaproxyOn:              true,
	}
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true

	master := cluster.Servers[0]
	master.Id = "master1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.IsMaintenance = false
	master.ClusterGroup = cluster

	slave := cluster.Servers[1]
	slave.Id = "slave1"
	slave.Host = "127.0.0.1"
	slave.Port = "3307"
	slave.State = stateSlave
	slave.IsMaintenance = false
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "MAINT", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
	}, "\n")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake haproxy server: %v", err)
	}
	defer ln.Close()
	host, port, _ := net.SplitHostPort(ln.Addr().String())

	var mu sync.Mutex
	var commands []string
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				line, _ := bufio.NewReader(c).ReadString('\n')
				cmd := strings.TrimRight(line, "\r\n")
				mu.Lock()
				commands = append(commands, cmd)
				mu.Unlock()
				if cmd == "show stat" {
					c.Write([]byte(statResponse))
				}
			}(conn)
		}
	}()

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "test", // non-empty, skips GetVersion()
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	wantMasterReady := "set server service_read/master1 state ready"
	wantMasterDrain := "set server service_read/master1 state drain"

	hasCmd := func(want string) bool {
		for _, c := range commands {
			if c == want {
				return true
			}
		}
		return false
	}

	if !hasCmd(wantMasterReady) {
		t.Errorf("Refresh() commands = %v, want to contain %q", commands, wantMasterReady)
	}
	if !hasCmd(wantMasterDrain) {
		t.Errorf("Refresh() commands = %v, want to contain %q (master should be re-drained in the same pass after the stale MAINT is reconciled, since a slave reader is available)", commands, wantMasterDrain)
	}

	readyIdx := cmdIndex(commands, wantMasterReady)
	drainIdx := cmdIndex(commands, wantMasterDrain)
	if readyIdx < 0 || drainIdx < 0 {
		t.Fatalf("Refresh() commands = %v, want both %q and %q present", commands, wantMasterReady, wantMasterDrain)
	}
	if readyIdx >= drainIdx {
		t.Errorf("Refresh() commands = %v, want %q (index %d) before %q (index %d)", commands, wantMasterReady, readyIdx, wantMasterDrain, drainIdx)
	}
}
