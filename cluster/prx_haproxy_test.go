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

// startFakeHaproxy starts a TCP listener that answers "show stat" with
// statResponse and otherwise just records the command it received (no
// response body), mirroring how HAProxy's Runtime API accepts a command with
// no meaningful stdout on success. Returns the host/port to dial and an
// accessor for the recorded commands.
func startFakeHaproxy(t *testing.T, statResponse string) (host, port string, getCommands func() []string) {
	t.Helper()
	host, port, getCommands, _ = startFakeHaproxyMutable(t, statResponse)
	return host, port, getCommands
}

// startFakeHaproxyMutable is startFakeHaproxy plus a setStatResponse setter,
// for tests that need "show stat" to return something different across
// multiple Refresh() calls against the same fake server (e.g. simulating a
// server that was dynamically added in one pass and left the cluster by the
// next).
func startFakeHaproxyMutable(t *testing.T, statResponse string) (host, port string, getCommands func() []string, setStatResponse func(string)) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake haproxy server: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	host, port, _ = net.SplitHostPort(ln.Addr().String())

	var mu sync.Mutex
	var commands []string
	resp := statResponse
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
				current := resp
				mu.Unlock()
				if cmd == "show stat" {
					c.Write([]byte(current))
				}
			}(conn)
		}
	}()

	return host, port, func() []string {
			mu.Lock()
			defer mu.Unlock()
			out := make([]string, len(commands))
			copy(out, commands)
			return out
		}, func(newResp string) {
			mu.Lock()
			defer mu.Unlock()
			resp = newResp
		}
}

// TestHaproxyReconcileAddsMissingServer covers Phase 1 of issue #1724: a
// cluster member (slave1) that HAProxy's read backend doesn't know about yet
// (e.g. it just joined the cluster) must be added via the Runtime API rather
// than requiring a reload, when haproxy-api-bootstrap-servers is enabled and
// the HAProxy version supports dynamic servers.
func TestHaproxyReconcileAddsMissingServer(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
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
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	// slave1 is absent from HAProxy's stat output entirely: it has not been
	// added to the read backend yet.
	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
	}, "\n")

	host, port, getCommands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 2.8.5-1 2023/09/01",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	commands := getCommands()
	wantAdd := "add server service_read/slave1 127.0.0.1:3307 check"
	wantDrain := "set server service_read/slave1 state drain"
	wantHealth := "enable health service_read/slave1"

	for _, want := range []string{wantAdd, wantDrain, wantHealth} {
		if cmdIndex(commands, want) < 0 {
			t.Errorf("Refresh() commands = %v, want to contain %q", commands, want)
		}
	}

	// Must come out of MAINT via DRAIN, not READY — see
	// TestHaproxyReconcileNewServerNotReadiedSamePass for why.
	addIdx := cmdIndex(commands, wantAdd)
	drainIdx := cmdIndex(commands, wantDrain)
	if addIdx >= 0 && drainIdx >= 0 && addIdx >= drainIdx {
		t.Errorf("Refresh() commands = %v, want %q before %q", commands, wantAdd, wantDrain)
	}

	if cmdIndex(commands, "set server service_read/slave1 state ready") >= 0 {
		t.Errorf("Refresh() commands = %v, want no set-ready in the same pass a server was added (eligibility for this server was never checked this pass)", commands)
	}
}

// TestHaproxyReconcileAddsMissingIPv6Server is the IPv6 counterpart to
// TestHaproxyReconcileAddsMissingServer: AddServer must bracket a bracketed
// ServerMonitor.Host (e.g. "[2001:db8::1]") correctly into the combined
// host:port token.
func TestHaproxyReconcileAddsMissingIPv6Server(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
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
	slave.Host = "[2001:db8::1]"
	slave.Port = "3307"
	slave.State = stateSlave
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
	}, "\n")

	host, port, getCommands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 2.8.5-1 2023/09/01",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	commands := getCommands()
	wantAdd := "add server service_read/slave1 [2001:db8::1]:3307 check"
	if cmdIndex(commands, wantAdd) < 0 {
		t.Errorf("Refresh() commands = %v, want to contain %q", commands, wantAdd)
	}
}

// TestHaproxyReconcileNewServerNotReadiedSamePass adds a replica with broken
// replication (stateSlaveErr) and confirms it stays drained, never readied
// — not in the add pass (never went through eligibility checks), and not on
// the following pass either, once HAProxy reports it as DRAIN.
func TestHaproxyReconcileNewServerNotReadiedSamePass(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
	}
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true

	master := cluster.Servers[0]
	master.Id = "master1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.ClusterGroup = cluster

	// slave1 has broken replication: it must never be exposed as a ready
	// reader, added this pass or not.
	slave := cluster.Servers[1]
	slave.Id = "slave1"
	slave.Host = "127.0.0.1"
	slave.Port = "3307"
	slave.State = stateSlaveErr
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	masterOnlyStat := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
	}, "\n")

	host, port, getCommands, setStatResponse := startFakeHaproxyMutable(t, masterOnlyStat)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 2.8.5-1 2023/09/01",
	}}

	// First pass: slave1 gets added and drained, never readied.
	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() [1] error = %v", err)
	}
	if cmdIndex(getCommands(), "set server service_read/slave1 state ready") >= 0 {
		t.Fatalf("Refresh() [1] commands = %v, want no set-ready for a just-added server", getCommands())
	}

	// Second pass: HAProxy now reports slave1 as DRAIN (from pass 1). The
	// existing, unchanged eligibility logic must see the broken replication
	// state and leave it drained rather than promote it to ready.
	setStatResponse(strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "DRAIN", "127.0.0.1:3307"),
	}, "\n"))

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() [2] error = %v", err)
	}

	if cmdIndex(getCommands(), "set server service_read/slave1 state ready") >= 0 {
		t.Errorf("Refresh() [2] commands = %v, want slave1 (broken replication) to stay drained, not be readied", getCommands())
	}
}

// TestHaproxyReconcileSkipsWhenGated verifies the additive/gated contract:
// no "add server" is issued unless both haproxy-api-bootstrap-servers is
// enabled AND the HAProxy version supports dynamic servers.
func TestHaproxyReconcileSkipsWhenGated(t *testing.T) {
	tests := []struct {
		name        string
		bootstrapOn bool
		version     string
		haproxyMode string
	}{
		{name: "flag disabled", bootstrapOn: false, version: "HAProxy version 2.8.5-1 2023/09/01", haproxyMode: "runtimeapi"},
		{name: "version too old", bootstrapOn: true, version: "HAProxy version 2.0.14-1 2020/06/01", haproxyMode: "runtimeapi"},
		{name: "version 2.4 (below the gate)", bootstrapOn: true, version: "HAProxy version 2.4.36-1 2024/01/01", haproxyMode: "runtimeapi"},
		// standby mode names servers positionally, not by server.Id.
		{name: "haproxy-mode standby", bootstrapOn: true, version: "HAProxy version 2.8.5-1 2023/09/01", haproxyMode: "standby"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := setupTestCluster(t, 2)
			defer cleanupTestCluster(t, cluster)

			cluster.StateMachine = new(state.StateMachine)
			cluster.StateMachine.Init()
			cluster.Topology = config.TopoMasterSlave
			cluster.Conf = &config.Config{
				HaproxyAPIWriteBackend:     "service_write",
				HaproxyAPIReadBackend:      "service_read",
				HaproxyOn:                  true,
				HaproxyAPIBootstrapServers: tt.bootstrapOn,
				HaproxyMode:                tt.haproxyMode,
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
			slave.ClusterGroup = cluster

			cluster.master = master
			cluster.slaves = []*ServerMonitor{slave}

			statResponse := strings.Join([]string{
				haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
				haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
			}, "\n")

			host, port, getCommands := startFakeHaproxy(t, statResponse)

			proxy := &HaproxyProxy{Proxy: Proxy{
				ClusterGroup: cluster,
				Host:         host,
				Port:         port,
				Datadir:      t.TempDir(),
				Version:      tt.version,
			}}

			if err := proxy.Refresh(); err != nil {
				t.Fatalf("Refresh() error = %v", err)
			}

			commands := getCommands()
			if cmdIndex(commands, "add server service_read/slave1 127.0.0.1:3307 check") >= 0 {
				t.Errorf("Refresh() commands = %v, want no add server command when gated off", commands)
			}
		})
	}
}

// TestHaproxyReconcileIgnoresOverlappingBackendName confirms a "show stat"
// row from an unrelated backend whose name merely contains the managed one
// (e.g. "service_read_shadow" vs "service_read") is never treated as
// belonging to it — no Runtime API command should ever reference its
// server.
func TestHaproxyReconcileIgnoresOverlappingBackendName(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
	}
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true

	master := cluster.Servers[0]
	master.Id = "master1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = nil

	// "service_read_shadow" is a distinct backend that merely contains
	// "service_read" as a substring; "shadow1" belongs to it, not to the
	// managed "service_read" backend, and is not a cluster server either
	// way.
	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read_shadow", "shadow1", "UP", "127.0.0.1:9999"),
	}, "\n")

	host, port, getCommands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.0.26-1 2024/05/01",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	commands := getCommands()
	for _, c := range commands {
		if strings.Contains(c, "shadow1") {
			t.Errorf("Refresh() commands = %v, want no command referencing a server from an unrelated backend, got %q", commands, c)
		}
	}
}

// TestHaproxyReconcileRemovesStaleServer covers removal on HAProxy >= 3.0: a
// server HAProxy still lists in the read backend but that no longer belongs
// to the cluster must be drained, waited on, and deleted via the Runtime
// API — statically bootstrapped or dynamically added, the same way.
func TestHaproxyReconcileRemovesStaleServer(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
	}
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true

	master := cluster.Servers[0]
	master.Id = "master1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = nil

	// "decommissioned1" is a read-backend entry HAProxy still has, but its
	// host (127.0.0.1:9999) matches no server in the current cluster.
	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "decommissioned1", "UP", "127.0.0.1:9999"),
	}, "\n")

	host, port, getCommands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.0.26-1 2024/05/01",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	commands := getCommands()
	wantMaint := "set server service_read/decommissioned1 state maint"
	wantWait := "wait 2000 srv-removable service_read/decommissioned1"
	wantDel := "del server service_read/decommissioned1"

	for _, want := range []string{wantMaint, wantWait, wantDel} {
		if cmdIndex(commands, want) < 0 {
			t.Errorf("Refresh() commands = %v, want to contain %q", commands, want)
		}
	}

	maintIdx := cmdIndex(commands, wantMaint)
	waitIdx := cmdIndex(commands, wantWait)
	delIdx := cmdIndex(commands, wantDel)
	if maintIdx >= 0 && waitIdx >= 0 && delIdx >= 0 {
		if !(maintIdx < waitIdx && waitIdx < delIdx) {
			t.Errorf("Refresh() commands = %v, want maint before wait before del", commands)
		}
	}
}

// TestHaproxyReconcileRemovesStaleServerWithoutWaitBelowVersion3 covers the
// removal fallback below HAProxy 3.0, where "wait srv-removable" doesn't
// exist: removal must skip straight from drain to DelServer.
func TestHaproxyReconcileRemovesStaleServerWithoutWaitBelowVersion3(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
	}
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true

	master := cluster.Servers[0]
	master.Id = "master1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = nil

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "decommissioned1", "UP", "127.0.0.1:9999"),
	}, "\n")

	host, port, getCommands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 2.6.32-1 2024/01/01",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	commands := getCommands()
	wantMaint := "set server service_read/decommissioned1 state maint"
	wantDel := "del server service_read/decommissioned1"

	if cmdIndex(commands, wantMaint) < 0 {
		t.Errorf("Refresh() commands = %v, want to contain %q", commands, wantMaint)
	}
	if cmdIndex(commands, wantDel) < 0 {
		t.Errorf("Refresh() commands = %v, want to contain %q", commands, wantDel)
	}
	if cmdIndex(commands, "wait 2000 srv-removable service_read/decommissioned1") >= 0 {
		t.Errorf("Refresh() commands = %v, want no wait srv-removable below HAProxy 3.0", commands)
	}
}

// TestHaproxyReconcileUpdatesChangedAddress covers a server that kept its
// repman Id but changed address (e.g. re-provisioned under a new IP): the
// existing HAProxy entry must have its address updated via the Runtime API
// rather than being left stale or churned through add/del.
func TestHaproxyReconcileUpdatesChangedAddress(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
	}
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true

	master := cluster.Servers[0]
	master.Id = "master1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.ClusterGroup = cluster

	// slave1 kept its Id but was reprovisioned onto a new address; HAProxy
	// still has the old one on file.
	slave := cluster.Servers[1]
	slave.Id = "slave1"
	slave.Host = "127.0.0.1"
	slave.Port = "3399"
	slave.State = stateSlave
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
	}, "\n")

	host, port, getCommands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 2.8.5-1 2023/09/01",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	commands := getCommands()
	wantAddrUpdate := "set server service_read/slave1 addr 127.0.0.1 port 3399"
	if cmdIndex(commands, wantAddrUpdate) < 0 {
		t.Errorf("Refresh() commands = %v, want to contain %q", commands, wantAddrUpdate)
	}
	if cmdIndex(commands, "add server service_read/slave1 127.0.0.1:3399 check") >= 0 {
		t.Errorf("Refresh() commands = %v, want no add server for an address change on a known Id", commands)
	}
}

// TestHaproxyReconcileNoFalseMismatchForUnchangedIPv6Address confirms an
// unchanged bracketed IPv6 address is not flagged as mismatched: no
// address-update command should be issued.
func TestHaproxyReconcileNoFalseMismatchForUnchangedIPv6Address(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
	}
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true

	master := cluster.Servers[0]
	master.Id = "master1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.ClusterGroup = cluster

	// IPv6, bracketed, matching how ServerMonitor.Host stores it elsewhere
	// in this codebase (see misc.Unbracket call sites).
	slave := cluster.Servers[1]
	slave.Id = "slave1"
	slave.Host = "[2001:db8::1]"
	slave.Port = "3307"
	slave.State = stateSlave
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "UP", "[2001:db8::1]:3307"),
	}, "\n")

	host, port, getCommands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 2.8.5-1 2023/09/01",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	commands := getCommands()
	for _, c := range commands {
		if strings.HasPrefix(c, "set server service_read/slave1 addr") || strings.HasPrefix(c, "set server service_read/slave1 fqdn") {
			t.Errorf("Refresh() commands = %v, want no address reconciliation for an unchanged IPv6 address, got %q", commands, c)
		}
	}
}

// TestHaproxyReconcileUpdatesChangedIPv6Address is the IPv6 counterpart to
// TestHaproxyReconcileUpdatesChangedAddress: a genuine IPv6 address change
// must be reconciled via SetServerAddr's "addr" path with the host
// unbracketed, not misrouted to "fqdn".
func TestHaproxyReconcileUpdatesChangedIPv6Address(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
	}
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true

	master := cluster.Servers[0]
	master.Id = "master1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.ClusterGroup = cluster

	// slave1 kept its Id but was reprovisioned onto a new IPv6 address;
	// HAProxy still has the old one on file.
	slave := cluster.Servers[1]
	slave.Id = "slave1"
	slave.Host = "[2001:db8::2]"
	slave.Port = "3307"
	slave.State = stateSlave
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "UP", "[2001:db8::1]:3307"),
	}, "\n")

	host, port, getCommands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 2.8.5-1 2023/09/01",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	commands := getCommands()
	wantAddrUpdate := "set server service_read/slave1 addr 2001:db8::2 port 3307"
	if cmdIndex(commands, wantAddrUpdate) < 0 {
		t.Errorf("Refresh() commands = %v, want to contain %q (unbracketed host, addr not fqdn)", commands, wantAddrUpdate)
	}
	if cmdIndex(commands, "set server service_read/slave1 fqdn 2001:db8::2 port 3307") >= 0 {
		t.Errorf("Refresh() commands = %v, want IPv6 address change to use the addr path, not fqdn", commands)
	}
}

// TestHaproxyReconcileSkipsAddressUpdateOnDNSCluster confirms address
// reconciliation is skipped for a server whose own Host is a hostname/FQDN
// (not a literal IP) — driven by slave.Host itself, with the "dns" proxy
// tag also set to prove the skip is due to the server's Host, not merely
// proxy.HasDNS(). See TestHaproxyReconcileUpdatesChangedAddressForIPServerBehindDNSProxy
// for the IP-based counterpart.
func TestHaproxyReconcileSkipsAddressUpdateOnDNSCluster(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
	}
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true
	// Forces proxy.HasDNS() == true without needing proxy.Host itself to be
	// a resolvable hostname (the fake server below is still dialed by IP).
	cluster.Configurator.ProxyTags = []string{"dns"}

	master := cluster.Servers[0]
	master.Id = "master1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.ClusterGroup = cluster

	slave := cluster.Servers[1]
	slave.Id = "slave1"
	slave.Host = "db-slave1.internal"
	slave.Port = "3307"
	slave.State = stateSlave
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	// HAProxy reports slave1's resolved connection IP, not the FQDN
	// cluster.Servers has for it — this must not be treated as a mismatch.
	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "UP", "10.0.0.42:3307"),
	}, "\n")

	host, port, getCommands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 2.8.5-1 2023/09/01",
	}}

	if !proxy.HasDNS() {
		t.Fatalf("test setup error: expected proxy.HasDNS() to be true")
	}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	commands := getCommands()
	for _, c := range commands {
		if strings.HasPrefix(c, "set server service_read/slave1 addr") || strings.HasPrefix(c, "set server service_read/slave1 fqdn") {
			t.Errorf("Refresh() commands = %v, want no address reconciliation on a DNS-backed cluster, got %q", commands, c)
		}
	}
}

// TestHaproxyReconcileUpdatesChangedAddressForIPServerBehindDNSProxy
// confirms a backend member with a literal IP still gets address
// reconciliation even when proxy.HasDNS() is true for reasons unrelated to
// that member (e.g. the proxy host itself is DNS-named).
func TestHaproxyReconcileUpdatesChangedAddressForIPServerBehindDNSProxy(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
	}
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true
	// Forces proxy.HasDNS() == true for a reason entirely unrelated to
	// slave1's own address (an IP): e.g. the same tag OpenSVC/Kubernetes
	// deployments set, or simply proxy.Host being a hostname elsewhere.
	cluster.Configurator.ProxyTags = []string{"dns"}

	master := cluster.Servers[0]
	master.Id = "master1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.ClusterGroup = cluster

	// slave1 kept its Id but was reprovisioned onto a new IP address;
	// HAProxy still has the old one on file. Its Host is a literal IP, not
	// a hostname — the DNS-ness of the proxy/orchestrator is irrelevant to
	// whether this specific update is safe.
	slave := cluster.Servers[1]
	slave.Id = "slave1"
	slave.Host = "10.0.0.99"
	slave.Port = "3307"
	slave.State = stateSlave
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "UP", "10.0.0.42:3307"),
	}, "\n")

	host, port, getCommands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 2.8.5-1 2023/09/01",
	}}

	if !proxy.HasDNS() {
		t.Fatalf("test setup error: expected proxy.HasDNS() to be true")
	}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	commands := getCommands()
	wantAddrUpdate := "set server service_read/slave1 addr 10.0.0.99 port 3307"
	if cmdIndex(commands, wantAddrUpdate) < 0 {
		t.Errorf("Refresh() commands = %v, want to contain %q (an IP-based member must be reconciled even though proxy.HasDNS() is true)", commands, wantAddrUpdate)
	}
}

// TestHaproxyReconcileSkipsAddingMembersOnDNSCluster confirms that
// add-missing (unlike address reconciliation, and unlike stale removal — see
// TestHaproxyReconcileStillDrainsStaleServerOnDNSCluster and
// TestHaproxyReconcileMarksServerNonPurgeableAfterDelServerRefusal) is
// skipped entirely when proxy.HasDNS() is true: GetConfigProxyModule appends
// "resolvers dns" to every bootstrapped read-backend server line in that
// case, but a runtime "add server" call can't attach "resolvers" itself, so
// an entry added that way would silently stop tracking DNS changes — worse
// than not adding it.
func TestHaproxyReconcileSkipsAddingMembersOnDNSCluster(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
	}
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true
	// Forces proxy.HasDNS() == true, same as the other DNS-gated tests above.
	cluster.Configurator.ProxyTags = []string{"dns"}

	master := cluster.Servers[0]
	master.Id = "master1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.ClusterGroup = cluster

	// slave1 is missing from HAProxy's stat output — would normally trigger
	// AddServer, must not be attempted here.
	slave := cluster.Servers[1]
	slave.Id = "slave1"
	slave.Host = "127.0.0.1"
	slave.Port = "3307"
	slave.State = stateSlave
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
	}, "\n")

	host, port, getCommands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.0.26-1 2024/05/01",
	}}

	if !proxy.HasDNS() {
		t.Fatalf("test setup error: expected proxy.HasDNS() to be true")
	}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	commands := getCommands()
	wantAdd := "add server service_read/slave1 127.0.0.1:3307 check"
	if cmdIndex(commands, wantAdd) >= 0 {
		t.Errorf("Refresh() commands = %v, want no %q on a resolver-backed (HasDNS) cluster", commands, wantAdd)
	}
}

// TestHaproxyReconcileStillDrainsStaleServerOnDNSCluster confirms that
// removal of a stale read-backend entry is NOT blanket-skipped on a
// proxy.HasDNS() == true cluster the way adding a missing member is (see
// TestHaproxyReconcileSkipsAddingMembersOnDNSCluster): draining
// (SetMaintenance) never touches "resolvers" and always succeeds regardless
// of DNS config, and it's the safety-critical half of removal — the part
// that actually stops read traffic from reaching a decommissioned node.
// Skipping it here would leave a decommissioned node serving live read
// traffic indefinitely. Deletion is also still attempted (not
// proxy-wide-skipped): the fake server below has no reason to refuse it,
// unlike TestHaproxyReconcileMarksServerNonPurgeableAfterDelServerRefusal.
func TestHaproxyReconcileStillDrainsStaleServerOnDNSCluster(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
	}
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true
	cluster.Configurator.ProxyTags = []string{"dns"}

	master := cluster.Servers[0]
	master.Id = "master1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = nil

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "decommissioned1", "UP", "127.0.0.1:9999"),
	}, "\n")

	host, port, getCommands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.0.26-1 2024/05/01",
	}}

	if !proxy.HasDNS() {
		t.Fatalf("test setup error: expected proxy.HasDNS() to be true")
	}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	commands := getCommands()
	wantMaint := "set server service_read/decommissioned1 state maint"
	wantDel := "del server service_read/decommissioned1"
	for _, want := range []string{wantMaint, wantDel} {
		if cmdIndex(commands, want) < 0 {
			t.Errorf("Refresh() commands = %v, want to contain %q even though proxy.HasDNS() is true (only adding new members is DNS-gated, not removing stale ones)", commands, want)
		}
	}
}

// TestHaproxyReconcileMarksServerNonPurgeableAfterDelServerRefusal confirms
// that once HAProxy's Runtime API refuses "del server" with its
// non-purgeable message (e.g. because the entry carries a "resolvers"
// clause — see haproxyNonPurgeableServerMsg), reconcileReadBackendServers
// stops retrying DelServer/WaitSrvRemovable for that svname on later passes,
// while still re-issuing SetMaintenance every pass (the safety-critical
// part — see TestHaproxyReconcileStillDrainsStaleServerOnDNSCluster).
func TestHaproxyReconcileMarksServerNonPurgeableAfterDelServerRefusal(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
	}
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true

	master := cluster.Servers[0]
	master.Id = "master1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = nil

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "decommissioned1", "UP", "127.0.0.1:9999"),
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
				switch {
				case cmd == "show stat":
					c.Write([]byte(statResponse))
				case cmd == "del server service_read/decommissioned1":
					c.Write([]byte("Failed. This server cannot be removed at runtime due to other configuration elements pointing to it.\n"))
				}
			}(conn)
		}
	}()

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.0.26-1 2024/05/01",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() [pass 1] error = %v", err)
	}

	mu.Lock()
	pass1 := append([]string(nil), commands...)
	commands = nil
	mu.Unlock()

	wantMaint := "set server service_read/decommissioned1 state maint"
	wantWait := "wait 2000 srv-removable service_read/decommissioned1"
	wantDel := "del server service_read/decommissioned1"
	for _, want := range []string{wantMaint, wantWait, wantDel} {
		if cmdIndex(pass1, want) < 0 {
			t.Errorf("Refresh() [pass 1] commands = %v, want to contain %q", pass1, want)
		}
	}

	if !proxy.isNonPurgeableReadServer("decommissioned1") {
		t.Fatalf("after Refresh() [pass 1], expected decommissioned1 to be marked non-purgeable")
	}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() [pass 2] error = %v", err)
	}

	mu.Lock()
	pass2 := append([]string(nil), commands...)
	mu.Unlock()

	if cmdIndex(pass2, wantMaint) < 0 {
		t.Errorf("Refresh() [pass 2] commands = %v, want to still contain %q (draining must keep retrying even for a known non-purgeable server)", pass2, wantMaint)
	}
	for _, unwanted := range []string{wantWait, wantDel} {
		if cmdIndex(pass2, unwanted) >= 0 {
			t.Errorf("Refresh() [pass 2] commands = %v, want no %q once decommissioned1 is known non-purgeable", pass2, unwanted)
		}
	}
}

// TestHaproxyReconcileRollsBackServerWhenDrainFailsAfterAdd confirms that if
// AddServer succeeds but SetDrain fails, the server is removed via the
// Runtime API (not left behind still in MAINT) so the add sequence retries
// cleanly next pass.
func TestHaproxyReconcileRollsBackServerWhenDrainFailsAfterAdd(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
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
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
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
				switch {
				case cmd == "show stat":
					c.Write([]byte(statResponse))
				case cmd == "set server service_read/slave1 state drain":
					// AddServer succeeded (empty response, not asserted
					// here) but the very next step fails.
					c.Write([]byte("some transient HAProxy error\n"))
				}
			}(conn)
		}
	}()

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 2.8.5-1 2023/09/01",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	mu.Lock()
	gotCommands := append([]string(nil), commands...)
	mu.Unlock()

	wantAdd := "add server service_read/slave1 127.0.0.1:3307 check"
	wantDrainAttempt := "set server service_read/slave1 state drain"
	wantRollbackMaint := "set server service_read/slave1 state maint"
	wantRollbackDel := "del server service_read/slave1"

	for _, want := range []string{wantAdd, wantDrainAttempt, wantRollbackMaint, wantRollbackDel} {
		if cmdIndex(gotCommands, want) < 0 {
			t.Errorf("Refresh() commands = %v, want to contain %q", gotCommands, want)
		}
	}

	if cmdIndex(gotCommands, "set server service_read/slave1 state ready") >= 0 {
		t.Errorf("Refresh() commands = %v, want no set-ready for a server whose add sequence failed and was rolled back", gotCommands)
	}
}

// TestHaproxyReconcileRollsBackServerWhenEnableHealthFailsAfterAdd is the
// EnableHealth counterpart: AddServer and SetDrain succeed but EnableHealth
// fails, which must trigger the same rollback rather than leave a
// DRAIN server with health checks never activated.
func TestHaproxyReconcileRollsBackServerWhenEnableHealthFailsAfterAdd(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
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
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
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
				switch {
				case cmd == "show stat":
					c.Write([]byte(statResponse))
				case cmd == "enable health service_read/slave1":
					// AddServer and SetDrain both succeed (empty response,
					// not asserted here); only health activation fails.
					c.Write([]byte("some transient HAProxy error\n"))
				}
			}(conn)
		}
	}()

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 2.8.5-1 2023/09/01",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	mu.Lock()
	gotCommands := append([]string(nil), commands...)
	mu.Unlock()

	wantAdd := "add server service_read/slave1 127.0.0.1:3307 check"
	wantDrain := "set server service_read/slave1 state drain"
	wantHealthAttempt := "enable health service_read/slave1"
	wantRollbackMaint := "set server service_read/slave1 state maint"
	wantRollbackDel := "del server service_read/slave1"

	for _, want := range []string{wantAdd, wantDrain, wantHealthAttempt, wantRollbackMaint, wantRollbackDel} {
		if cmdIndex(gotCommands, want) < 0 {
			t.Errorf("Refresh() commands = %v, want to contain %q", gotCommands, want)
		}
	}

	if cmdIndex(gotCommands, "set server service_read/slave1 state ready") >= 0 {
		t.Errorf("Refresh() commands = %v, want no set-ready for a server whose health-check activation failed and was rolled back", gotCommands)
	}
}

// TestHaproxyReconcileBlocksReadyAfterRollbackFails covers the case where
// AddServer succeeds, the drain/health steps fail, and the rollback also
// fails: the leftover row must stay blocked from promotion indefinitely,
// not just for the pass it was added on. Runs two Refresh() passes — the
// second reports the leftover row as a healthy-looking DRAIN replica — and
// confirms it's still refused while removal keeps being retried.
func TestHaproxyReconcileBlocksReadyAfterRollbackFails(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
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
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	masterOnlyStat := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
	}, "\n")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake haproxy server: %v", err)
	}
	defer ln.Close()
	host, port, _ := net.SplitHostPort(ln.Addr().String())

	var mu sync.Mutex
	var commands []string
	statResponse := masterOnlyStat
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
				current := statResponse
				mu.Unlock()
				switch {
				case cmd == "show stat":
					c.Write([]byte(current))
				case cmd == "set server service_read/slave1 state drain":
					// SetDrain (both the initial attempt and every retry)
					// always fails, forcing the rollback path every pass.
					c.Write([]byte("some transient HAProxy error\n"))
				case cmd == "del server service_read/slave1":
					// The rollback's own removal also always fails, so the
					// server can never be cleanly deleted either.
					c.Write([]byte("some transient HAProxy error\n"))
				}
			}(conn)
		}
	}()

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 2.8.5-1 2023/09/01",
	}}

	// First pass: add succeeds, drain fails, rollback (maint succeeds, del
	// fails) also fails. The server must remain marked pending.
	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() [1] error = %v", err)
	}
	if !proxy.isPendingReadServer("slave1") {
		t.Fatalf("expected slave1 to remain marked pending after a failed add sequence and a failed rollback")
	}

	mu.Lock()
	firstPassDelCount := 0
	for _, c := range commands {
		if c == "del server service_read/slave1" {
			firstPassDelCount++
		}
	}
	mu.Unlock()
	if firstPassDelCount == 0 {
		t.Fatalf("expected at least one del server attempt in the first pass")
	}

	// Second pass: HAProxy now reports the leftover row as DRAIN with
	// otherwise healthy replication — exactly the state the generic
	// eligibility logic (the "valid replication and DRAIN" block) would
	// normally promote to ready.
	mu.Lock()
	statResponse = strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "DRAIN", "127.0.0.1:3307"),
	}, "\n")
	mu.Unlock()

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() [2] error = %v", err)
	}

	if !proxy.isPendingReadServer("slave1") {
		t.Errorf("expected slave1 to still be marked pending after a second failed rollback")
	}

	mu.Lock()
	gotCommands := append([]string(nil), commands...)
	secondPassDelCount := 0
	for _, c := range commands {
		if c == "del server service_read/slave1" {
			secondPassDelCount++
		}
	}
	mu.Unlock()

	if cmdIndex(gotCommands, "set server service_read/slave1 state ready") >= 0 {
		t.Errorf("Refresh() commands = %v, want slave1 never readied while its rollback keeps failing, even though HAProxy reports it as a healthy-looking DRAIN replica", gotCommands)
	}
	if secondPassDelCount <= firstPassDelCount {
		t.Errorf("expected removal to be retried again on the second pass (first pass del attempts=%d, cumulative after second pass=%d)", firstPassDelCount, secondPassDelCount)
	}
}

// TestHaproxyReconcileAddServerErrorResponseStopsEnable covers HAProxy
// returning a plain-text error over a successfully accepted TCP connection
// (the Runtime API's normal failure mode — err is nil at the transport
// level). AddServer failing this way must not be treated as success: the
// dependent set-ready / enable health calls must not fire.
func TestHaproxyReconcileAddServerErrorResponseStopsEnable(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
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
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
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
				switch {
				case cmd == "show stat":
					c.Write([]byte(statResponse))
				case strings.HasPrefix(cmd, "add server"):
					// A real HAProxy Runtime API error: TCP-level success,
					// non-empty plain-text failure body.
					c.Write([]byte("Can't add this server, adding a server requires either an IP address or a resolvable FQDN.\n"))
				}
			}(conn)
		}
	}()

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 2.8.5-1 2023/09/01",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	mu.Lock()
	gotCommands := append([]string(nil), commands...)
	mu.Unlock()

	if cmdIndex(gotCommands, "set server service_read/slave1 state ready") >= 0 {
		t.Errorf("Refresh() commands = %v, want no set-ready after a failed add server", gotCommands)
	}
	if cmdIndex(gotCommands, "enable health service_read/slave1") >= 0 {
		t.Errorf("Refresh() commands = %v, want no enable health after a failed add server", gotCommands)
	}
}

// TestHaproxyReconcileSkipsServerInMaintenance ensures a cluster server that
// is intentionally in maintenance is not added to the read backend.
func TestHaproxyReconcileSkipsServerInMaintenance(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend:     "service_write",
		HaproxyAPIReadBackend:      "service_read",
		HaproxyOn:                  true,
		HaproxyAPIBootstrapServers: true,
		HaproxyMode:                "runtimeapi",
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
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
	}, "\n")

	host, port, getCommands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 2.8.5-1 2023/09/01",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	commands := getCommands()
	if cmdIndex(commands, "add server service_read/slave1 127.0.0.1:3307 check") >= 0 {
		t.Errorf("Refresh() commands = %v, want no add server command for a server in maintenance", commands)
	}
}

// TestHaproxyRefreshDialsIPv6RuntimeAPIEndpoint confirms the Runtime API
// control-plane dial itself (ApiCmdWithTimeout) is IPv6-safe, not just
// backend-member addresses. proxy.Host is deliberately *unbracketed*
// ("::1", not "[::1]"): a naive "host + \":\" + port" concatenation happens
// to still parse when the host is already bracketed, so only the
// unbracketed form actually exercises the bug. Skips if the environment has
// no IPv6 loopback.
func TestHaproxyRefreshDialsIPv6RuntimeAPIEndpoint(t *testing.T) {
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback not available in this environment: %v", err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	cluster := setupTestCluster(t, 1)
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
	cluster.master = master
	cluster.slaves = nil

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
	}, "\n")

	var mu sync.Mutex
	var gotShowStat bool
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				line, _ := bufio.NewReader(c).ReadString('\n')
				if strings.TrimRight(line, "\r\n") == "show stat" {
					mu.Lock()
					gotShowStat = true
					mu.Unlock()
					c.Write([]byte(statResponse))
				}
			}(conn)
		}
	}()

	// Unbracketed — see the doc comment above for why that specific form
	// is what actually exercises the bug.
	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         "::1",
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "test", // non-empty, skips GetVersion()
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v (Runtime API dial to an IPv6 endpoint should succeed)", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !gotShowStat {
		t.Errorf("expected the fake IPv6 HAProxy listener to receive \"show stat\", got nothing — the Runtime API dial likely failed")
	}
}

// TestHaproxyRefreshDoesNotForceStatusWhenMaintenanceCorrectionSkipped
// marks a healthy slave pending (as it would be after a failed add
// sequence), has HAProxy report it as MAINT, and confirms the in-memory
// status stays MAINT rather than being forced to UP when
// setReadBackendMaintenance refuses the transition.
func TestHaproxyRefreshDoesNotForceStatusWhenMaintenanceCorrectionSkipped(t *testing.T) {
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

	// Repman considers slave1 healthy and not in maintenance; HAProxy will
	// report it as MAINT (e.g. left behind by an unrelated prior action).
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

	host, port, getCommands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "test", // non-empty, skips GetVersion()
	}}

	// Simulates slave1 being left pending by a prior failed Runtime API add
	// sequence (see TestHaproxyReconcileBlocksReadyAfterRollbackFails for
	// how that arises in practice) — same-package access to the unexported
	// tracking map used here to isolate this specific code path.
	proxy.markPendingReadServer("slave1")

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	if cmdIndex(getCommands(), "set server service_read/slave1 state ready") >= 0 {
		t.Fatalf("test setup error: expected setReadBackendMaintenance to skip the ready transition for a pending server")
	}

	if proxy.HasAvailableReader() {
		t.Errorf("HasAvailableReader() = true, want false: slave1's status must not have been forced to UP when the maintenance correction was skipped")
	}

	found := false
	for _, b := range proxy.BackendsRead {
		if b.Svname == "slave1" {
			found = true
			if b.PrxStatus != "MAINT" {
				t.Errorf("slave1 PrxStatus = %q, want %q (must not be forced to UP when setReadBackendMaintenance did not actually ready it)", b.PrxStatus, "MAINT")
			}
		}
	}
	if !found {
		t.Fatalf("test setup error: slave1 not found in proxy.BackendsRead")
	}
}
