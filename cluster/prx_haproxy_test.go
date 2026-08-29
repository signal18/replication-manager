// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/haproxy"
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
		HaproxyMode:            "runtimeapi",
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
		HaproxyMode:            "runtimeapi",
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
		HaproxyMode:            "runtimeapi",
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

// TestHaproxyRefreshSkipsSetMasterInStandbyMode: standby mode lists every
// server in the write backend (GetConfigProxyModule), not just the master
// under a "leader" alias, so a non-master row must not trigger SetMaster.
func TestHaproxyRefreshSkipsSetMasterInStandbyMode(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend: "service_write",
		HaproxyAPIReadBackend:  "service_read",
		HaproxyOn:              true,
		HaproxyMode:            "standby",
	}

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

	// Every server appears in service_write (standby's actual config shape),
	// not just the master.
	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "server1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_write", "server2", "DOWN", "127.0.0.1:3307"),
		haproxyStatRow("service_read", "server1", "DOWN", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "server2", "UP", "127.0.0.1:3307"),
	}, "\n")

	host, port, getCommands := startFakeHaproxy(t, statResponse)

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

	for _, c := range getCommands() {
		if strings.HasPrefix(c, "set server "+cluster.Conf.HaproxyAPIWriteBackend+"/leader") {
			t.Fatalf("Refresh() sent %q in haproxy-mode=standby, which has no \"leader\" alias to address (all commands: %v)", c, getCommands())
		}
	}
}

// TestHaproxyRefreshSkipsSetMasterFallbackInStandbyMode covers Refresh()'s
// second SetMaster call site, the "!foundMasterInStat" fallback: it fires
// when no write-backend row resolves to a known ServerMonitor at all.
func TestHaproxyRefreshSkipsSetMasterFallbackInStandbyMode(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend: "service_write",
		HaproxyAPIReadBackend:  "service_read",
		HaproxyOn:              true,
		HaproxyMode:            "standby",
	}

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

	// No rows for service_write at all -- foundMasterInStat stays false,
	// forcing the fallback branch regardless of resolution logic.
	statResponse := strings.Join([]string{
		haproxyStatRow("service_read", "server1", "DOWN", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "server2", "UP", "127.0.0.1:3307"),
	}, "\n")

	host, port, getCommands := startFakeHaproxy(t, statResponse)

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

	for _, c := range getCommands() {
		if strings.HasPrefix(c, "set server "+cluster.Conf.HaproxyAPIWriteBackend+"/leader") {
			t.Fatalf("Refresh() sent %q from the !foundMasterInStat fallback in haproxy-mode=standby, which has no \"leader\" alias to address (all commands: %v)", c, getCommands())
		}
	}
}

// TestHaproxyRefreshNeverMutatesWriteBackendInStandbyMode: standby mode
// propagates topology exclusively through Init()/Failover() (full config
// regen + reload); Refresh() must never issue a write-backend Runtime API
// command, even when a non-master row is left UP (checkmaster failed to
// exclude it, matching a real production symptom) -- correcting that is
// checkmaster's job or a full Init() regen, not Refresh()'s.
func TestHaproxyRefreshNeverMutatesWriteBackendInStandbyMode(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend: "service_write",
		HaproxyAPIReadBackend:  "service_read",
		HaproxyOn:              true,
		HaproxyMode:            "standby",
	}

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
	slave.IsSlave = true
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	// checkmaster failed to exclude the replica: both rows report UP in
	// service_write, matching the live screenshot that prompted this fix.
	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "server1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_write", "server2", "UP", "127.0.0.1:3307"),
		haproxyStatRow("service_read", "server1", "DOWN", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "server2", "UP", "127.0.0.1:3307"),
	}, "\n")

	host, port, getCommands := startFakeHaproxy(t, statResponse)

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

	for _, c := range getCommands() {
		if strings.HasPrefix(c, "set server "+cluster.Conf.HaproxyAPIWriteBackend+"/") {
			t.Fatalf("Refresh() sent %q in haproxy-mode=standby -- write-backend state must only ever change via Init()/Failover(), never from Refresh() (all commands: %v)", c, getCommands())
		}
	}

	if len(proxy.BackendsWrite) != 2 {
		t.Fatalf("expected Refresh() to still report both write-backend rows for the dashboard, got %d: %+v", len(proxy.BackendsWrite), proxy.BackendsWrite)
	}
}

// TestHaproxyRefreshNeverMutatesReadBackendInStandbyMode is
// TestHaproxyRefreshNeverMutatesWriteBackendInStandbyMode's read-backend
// counterpart. The read-backend mutation logic (broken-replication drain,
// valid-replication ready, master-reader reconciliation) predates the
// write-backend fix and was left unconditional -- the same architectural
// bug, just on the other backend: standby relies on checkslave's own
// external-check (option external-check, HAProxy's own health check
// against repman's /slave-status API) to control read-backend membership,
// so a competing Runtime API SetDrain/SetReady from Refresh() would race
// against checkslave's independent polling instead of deferring to it.
func TestHaproxyRefreshNeverMutatesReadBackendInStandbyMode(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend: "service_write",
		HaproxyAPIReadBackend:  "service_read",
		HaproxyOn:              true,
		HaproxyMode:            "standby",
	}

	master := cluster.Servers[0]
	master.Id = "master1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.ClusterGroup = cluster

	// slave1 broke replication but checkslave's external-check hasn't
	// excluded it yet (or is momentarily lagging) -- HAProxy still reports
	// it UP. In haproxy-mode=runtimeapi this would trigger a SetDrain; in
	// standby, Refresh() must leave it alone entirely.
	slave := cluster.Servers[1]
	slave.Id = "slave1"
	slave.Host = "127.0.0.1"
	slave.Port = "3307"
	slave.State = stateSlaveErr
	slave.IsSlave = true
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "server1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "server1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "server2", "UP", "127.0.0.1:3307"),
	}, "\n")

	host, port, getCommands := startFakeHaproxy(t, statResponse)

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

	for _, c := range getCommands() {
		if strings.HasPrefix(c, "set server "+cluster.Conf.HaproxyAPIReadBackend+"/") {
			t.Fatalf("Refresh() sent %q in haproxy-mode=standby -- read-backend state must be left to checkslave's external-check, never touched by Refresh() (all commands: %v)", c, getCommands())
		}
	}

	if len(proxy.BackendsRead) != 2 {
		t.Fatalf("expected Refresh() to still report both read-backend rows for the dashboard, got %d: %+v", len(proxy.BackendsRead), proxy.BackendsRead)
	}
}

// TestHaproxyInitPopulatesWriteBackendWithLeaderOnly guards against a
// long-standing gap in Init() -- the only place haproxy-mode=standby ever
// reconciles topology, since Failover() just calls Init() again. Its server
// loop only ever called AddServer for the read backend; the write backend
// was created but never populated by this function at all (true since at
// least v3.1.40). Whatever ended up in the write backend at initial
// provisioning (e.g. every server, via the OpenSVC moduleset's unconditional
// standby-mode server list) then stayed there forever, since no later
// Init()/Failover() call ever added the new leader or removed a server that
// lost leadership -- exactly matching the production symptom of replicas
// (including ones in SLAVE_ERROR) staying UP in the write group.
func TestHaproxyInitPopulatesWriteBackendWithLeaderOnly(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave

	shareDir := t.TempDir()
	tmpl := `{{range .Backends}}
backend {{.Name}}
{{range .Servers}}    server {{.Name}} {{.Host}}:{{.Port}}
{{end}}
{{end}}`
	if err := os.WriteFile(filepath.Join(shareDir, "haproxy_config.template"), []byte(tmpl), 0644); err != nil {
		t.Fatalf("failed to write test haproxy_config.template: %v", err)
	}

	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend: "ahmad_write",
		HaproxyAPIReadBackend:  "ahmad_read",
		HaproxyOn:              true,
		HaproxyMode:            "standby",
		ShareDir:               shareDir,
		ProvOrchestrator:       config.ConstOrchestratorLocalhost,
	}

	master := cluster.Servers[0]
	master.Id = "server1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.ClusterGroup = cluster

	slave := cluster.Servers[1]
	slave.Id = "server2"
	slave.Host = "127.0.0.1"
	slave.Port = "3307"
	slave.State = stateSlave
	slave.IsSlave = true
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	datadir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(datadir, "var"), 0755); err != nil {
		t.Fatalf("failed to create datadir/var: %v", err)
	}

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Datadir:      datadir,
		Version:      "test", // non-empty, skips GetVersion()
	}}

	proxy.Init()

	rendered, err := os.ReadFile(filepath.Join(datadir, "var", "haproxy.cfg"))
	if err != nil {
		t.Fatalf("Init() did not render a config file: %v", err)
	}
	content := string(rendered)

	writeSection := haproxyBackendSection(t, content, "ahmad_write")
	if !strings.Contains(writeSection, "server server1 127.0.0.1:3306") {
		t.Fatalf("write backend does not contain the leader, want it present:\n%s", writeSection)
	}
	if strings.Contains(writeSection, "server server2 ") {
		t.Fatalf("write backend contains the non-leader replica -- Init() must only ever route writes to the current leader:\n%s", writeSection)
	}

	readSection := haproxyBackendSection(t, content, "ahmad_read")
	if !strings.Contains(readSection, "server server1 127.0.0.1:3306") || !strings.Contains(readSection, "server server2 127.0.0.1:3307") {
		t.Fatalf("read backend should still list both servers, unchanged behavior:\n%s", readSection)
	}
}

// TestHaproxyInitOnlyDoesLocalWorkForLocalhostOrchestrator guards against
// Init() building, rendering, and reloading a *local* haproxy.cfg (execing
// the haproxy binary on THIS host, expecting the rendered config's
// stats-socket bind address to be reachable here) for orchestrators where
// HAProxy never runs on the repman host at all -- OpenSVC, Kubernetes, etc.
// There the real proxy's config reaches its actual container via a
// completely separate config-fetch tarball path (GetProxyConfig(), called
// unconditionally earlier in Init() for the one-time bootstrap); none of
// this function's local rendering is ever read by anything for those
// orchestrators, and the reload attempt can only ever fail (a live incident
// showed "cannot bind socket ... Cannot assign requested address" for the
// remote proxy's own address), doing so on every single state change and
// flooding the log. Only the Localhost orchestrator -- where Init() is the
// genuine, documented way to manage a co-located HAProxy process (see
// cluster/prov_localhost_haproxy.go) -- should do any of this work at all.
func TestHaproxyInitOnlyDoesLocalWorkForLocalhostOrchestrator(t *testing.T) {
	newProxy := func(t *testing.T, orchestrator string) (datadir string) {
		t.Helper()
		cluster := setupTestCluster(t, 1)
		cluster.StateMachine = new(state.StateMachine)
		cluster.StateMachine.Init()
		cluster.Topology = config.TopoMasterSlave

		shareDir := t.TempDir()
		tmpl := `{{range .Backends}}
backend {{.Name}}
{{range .Servers}}    server {{.Name}} {{.Host}}:{{.Port}}
{{end}}
{{end}}`
		if err := os.WriteFile(filepath.Join(shareDir, "haproxy_config.template"), []byte(tmpl), 0644); err != nil {
			t.Fatalf("failed to write test haproxy_config.template: %v", err)
		}

		cluster.Conf = &config.Config{
			HaproxyAPIWriteBackend: "service_write",
			HaproxyAPIReadBackend:  "service_read",
			HaproxyOn:              true,
			HaproxyMode:            "standby",
			ShareDir:               shareDir,
			ProvOrchestrator:       orchestrator,
		}

		master := cluster.Servers[0]
		master.Id = "server1"
		master.Host = "127.0.0.1"
		master.Port = "3306"
		master.State = stateMaster
		master.ClusterGroup = cluster
		cluster.master = master

		datadir = t.TempDir()
		if err := os.MkdirAll(filepath.Join(datadir, "var"), 0755); err != nil {
			t.Fatalf("failed to create datadir/var: %v", err)
		}

		proxy := &HaproxyProxy{Proxy: Proxy{
			ClusterGroup: cluster,
			Datadir:      datadir,
			Version:      "test",
		}}
		proxy.Init()
		return datadir
	}

	t.Run("non-localhost orchestrator skips rendering and the local reload", func(t *testing.T) {
		datadir := newProxy(t, config.ConstOrchestratorOpenSVC)
		if _, err := os.Stat(filepath.Join(datadir, "var", "haproxy.cfg")); !os.IsNotExist(err) {
			t.Fatalf("expected no rendered haproxy.cfg for a non-localhost orchestrator (Init() must return before Render()), stat err = %v", err)
		}
		if _, err := os.Stat(filepath.Join(datadir, "var", "haproxy.pid")); !os.IsNotExist(err) {
			t.Fatalf("expected no pid file for a non-localhost orchestrator (Reload() must be skipped), stat err = %v", err)
		}
	})

	t.Run("localhost orchestrator still renders and reloads locally", func(t *testing.T) {
		datadir := newProxy(t, config.ConstOrchestratorLocalhost)
		if _, err := os.Stat(filepath.Join(datadir, "var", "haproxy.cfg")); err != nil {
			t.Fatalf("expected a rendered haproxy.cfg for the localhost orchestrator: %v", err)
		}
		if _, err := os.Stat(filepath.Join(datadir, "var", "haproxy.pid")); err != nil {
			t.Fatalf("expected a pid file for the localhost orchestrator (SetPid() should have run): %v", err)
		}
	})
}

// TestHaproxyBackendsStateChangeReconcilesWriteBackendInStandbyMode guards
// against a gap where BackendsStateChange() -- fired on every meaningful
// server state change (cluster/srv.go), not just an actual failover or
// switchover -- only ever called Refresh(), which deliberately never
// mutates the write backend in haproxy-mode=standby. A replica that breaks
// replication without the master ever changing (e.g. Slave -> SlaveErr,
// matching a real production incident) never triggered Init() at all under
// the old code, leaving it stuck in the write backend indefinitely.
func TestHaproxyBackendsStateChangeReconcilesWriteBackendInStandbyMode(t *testing.T) {
	cluster := setupTestCluster(t, 2)
	defer cleanupTestCluster(t, cluster)

	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave

	shareDir := t.TempDir()
	tmpl := `{{range .Backends}}
backend {{.Name}}
{{range .Servers}}    server {{.Name}} {{.Host}}:{{.Port}}
{{end}}
{{end}}`
	if err := os.WriteFile(filepath.Join(shareDir, "haproxy_config.template"), []byte(tmpl), 0644); err != nil {
		t.Fatalf("failed to write test haproxy_config.template: %v", err)
	}

	cluster.Conf = &config.Config{
		HaproxyAPIWriteBackend: "ahmad_write",
		HaproxyAPIReadBackend:  "ahmad_read",
		HaproxyOn:              true,
		HaproxyMode:            "standby",
		ShareDir:               shareDir,
		ProvOrchestrator:       config.ConstOrchestratorLocalhost,
	}

	master := cluster.Servers[0]
	master.Id = "server1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.ClusterGroup = cluster

	// server2 just broke replication -- matches the real incident ("Server
	// db2 ... state transition from Slave changed to: SlaveErr"). No
	// failover happened: server1 is still master.
	slave := cluster.Servers[1]
	slave.Id = "server2"
	slave.Host = "127.0.0.1"
	slave.Port = "3307"
	slave.State = stateSlaveErr
	slave.IsSlave = true
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	statResponse := strings.Join([]string{
		haproxyStatRow("ahmad_write", "server1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("ahmad_write", "server2", "UP", "127.0.0.1:3307"),
		haproxyStatRow("ahmad_read", "server1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("ahmad_read", "server2", "DRAIN", "127.0.0.1:3307"),
	}, "\n")
	host, port, _ := startFakeHaproxy(t, statResponse)

	datadir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(datadir, "var"), 0755); err != nil {
		t.Fatalf("failed to create datadir/var: %v", err)
	}

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      datadir,
		Version:      "test",
	}}

	proxy.BackendsStateChange()

	rendered, err := os.ReadFile(filepath.Join(datadir, "var", "haproxy.cfg"))
	if err != nil {
		t.Fatalf("BackendsStateChange() did not trigger Init() to render a config file: %v", err)
	}
	writeSection := haproxyBackendSection(t, string(rendered), "ahmad_write")
	if !strings.Contains(writeSection, "server server1 127.0.0.1:3306") {
		t.Fatalf("write backend does not contain the leader:\n%s", writeSection)
	}
	if strings.Contains(writeSection, "server server2 ") {
		t.Fatalf("write backend still contains the server that broke replication -- BackendsStateChange() must reconcile the write backend via Init() even without a failover:\n%s", writeSection)
	}
}

// TestHaproxyAddServerToDoesNotWrapTypedNilError guards against Init()'s
// addServerTo closure regressing to Go's classic typed-nil-in-interface
// trap: haproxy.Config.AddServer returns *haproxy.Error (a concrete pointer
// type), and returning that value directly from a function whose signature
// is `error` wraps a nil *Error in a non-nil error interface, so `err !=
// nil` is true even on success. That exact regression made every "Failed to
// add server" log line fire on every successful AddServer call, with the
// error itself printing as "<nil>" -- this reproduces the same call shape
// against the real router/haproxy types and asserts a successful add
// produces a genuinely nil error.
func TestHaproxyAddServerToDoesNotWrapTypedNilError(t *testing.T) {
	c := &haproxy.Config{}
	c.InitializeConfig()
	if err := c.AddBackend(&haproxy.Backend{Name: "b", Mode: "tcp"}); err != nil {
		t.Fatalf("test setup: AddBackend failed: %v", err)
	}

	// Mirrors addServerTo's fixed shape in Init() (cluster/prx_haproxy.go).
	addServerTo := func(backend, name, host string, port int) error {
		if err := c.AddServer(backend, &haproxy.ServerDetail{
			Name: name, Host: host, Port: port,
			Weight: 100, MaxConn: 2000, Check: true, CheckInterval: 1000,
		}); err != nil {
			return err
		}
		return nil
	}

	if err := addServerTo("b", "server1", "127.0.0.1", 3306); err != nil {
		t.Fatalf("addServerTo returned a non-nil error on a successful AddServer: %v (typed-nil-in-interface regression)", err)
	}
}

// haproxyBackendSection extracts the text of a single "backend <name>" block
// from a rendered haproxy.cfg, up to (but not including) the next "backend "
// line.
func haproxyBackendSection(t *testing.T, content, name string) string {
	t.Helper()
	marker := "backend " + name + "\n"
	idx := strings.Index(content, marker)
	if idx == -1 {
		t.Fatalf("rendered config does not contain backend %q:\n%s", name, content)
	}
	rest := content[idx+len(marker):]
	if next := strings.Index(rest, "backend "); next != -1 {
		rest = rest[:next]
	}
	return rest
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

// TestHaproxyReconcileDelServerSuccessResponseNotMisreported pins a second
// real bug found running this branch live against HAProxy 3.0, the DelServer
// counterpart to TestHaproxyReconcileAddServerSuccessResponseCompletesSequence:
// "del server" also replies with a non-empty confirmation on success
// ("Server deleted.") unlike SetMaintenance/WaitSrvRemovable/SetDrain/
// EnableHealth. Routing that response through the generic haproxyCmdFailed
// misreported every successful stale-server removal as a failure ("HAProxy
// could not remove server ...: Server deleted.") even though the server was
// actually gone — confusing, and would have kept a removed-but-not-yet-
// reconciled svname eligible for another (also misreported) attempt next
// pass instead of just disappearing from "show stat" as it should.
// startFakeHaproxy's default empty-body response happens to be correct for
// SetMaintenance/WaitSrvRemovable, which is why this needs its own fake
// server using the real HAProxy text, same as the AddServer test above.
func TestHaproxyReconcileDelServerSuccessResponseNotMisreported(t *testing.T) {
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
				case strings.HasPrefix(cmd, "del server"):
					// The real HAProxy Runtime API success text (verified
					// by hand against haproxy:3.0) — non-empty despite
					// success.
					c.Write([]byte("Server deleted.\n\n"))
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
		t.Fatalf("Refresh() error = %v", err)
	}

	mu.Lock()
	gotCommands := append([]string(nil), commands...)
	mu.Unlock()

	wantDel := "del server service_read/decommissioned1"
	if cmdIndex(gotCommands, wantDel) < 0 {
		t.Errorf("Refresh() commands = %v, want to contain %q", gotCommands, wantDel)
	}

	// The bug's symptom was a misleading error log on genuine success, not a
	// functional retry loop, so assert on the thing that actually matters:
	// this svname must not be marked non-purgeable (that's reserved for
	// haproxyNonPurgeableServerMsg specifically — "Server deleted." isn't
	// that message, and misreading a plain success as any kind of failure
	// here would be its own bug).
	if proxy.isNonPurgeableReadServer("decommissioned1") {
		t.Errorf("decommissioned1 marked non-purgeable after a successful delete response, want not marked")
	}
}

// TestHaproxySetStateLogLevelDowngradesNoSuchServer pins a fourth real
// issue found running this branch live: setReadBackendMaintenance's
// SetReady call raced ServerMonitor.DelMaintenance() (called synchronously,
// independent of the monitor loop's own tick) against the read-backend row
// actually being deleted moments earlier — genuinely absent, not a
// misreported success like the Add/Del/WaitSrvRemovable cases above — and
// HAProxy correctly replied "No such server." That's an expected,
// self-correcting race (nothing to set ready on; the next Refresh() pass
// reconciles it either way), not an operational problem, but it was logged
// at LvlErr — indistinguishable from a real failure to anyone reading the
// log, which is exactly what prompted this downgrade.
func TestHaproxySetStateLogLevelDowngradesNoSuchServer(t *testing.T) {
	if got := haproxySetStateLogLevel("No such server.\n\n", true); got != config.LvlDbg {
		t.Errorf("haproxySetStateLogLevel(%q, true) = %q, want %q", "No such server.\n\n", got, config.LvlDbg)
	}
	if got := haproxySetStateLogLevel("Failed.\n\n", true); got != config.LvlErr {
		t.Errorf("haproxySetStateLogLevel(%q, true) = %q, want %q (a real failure must still alarm)", "Failed.\n\n", got, config.LvlErr)
	}
	// Without reconcileReadBackendServers actively running
	// (HaproxyAPIBootstrapServers off, the default), nothing corrects a
	// persistent "No such server" mismatch — it must stay LvlErr, not be
	// silently downgraded, or anyone not using the new feature loses their
	// only error-visibility signal for it.
	if got := haproxySetStateLogLevel("No such server.\n\n", false); got != config.LvlErr {
		t.Errorf("haproxySetStateLogLevel(%q, false) = %q, want %q (nothing self-corrects this when reconciliation is off)", "No such server.\n\n", got, config.LvlErr)
	}
}

// TestHaproxyReconcileReadBackendServersActiveRequiresAllConditions pins a
// code-review finding: reconcileReadBackendServersActive (and therefore
// haproxySetStateLogLevel's "No such server" downgrade) originally checked
// only cluster.Conf.HaproxyAPIBootstrapServers, but reconcileReadBackendServers
// itself also no-ops for an unsupported HAProxy version or a non-runtimeapi
// haproxy-mode, and separately skips just the add branch on a resolver-backed
// (HasDNS()) proxy — any one of those means a missing/renamed read-backend
// row is NOT actually self-correcting, so the "No such server" downgrade's
// premise doesn't hold. All four conditions must hold for
// reconcileReadBackendServersActive to report true.
func TestHaproxyReconcileReadBackendServersActiveRequiresAllConditions(t *testing.T) {
	newBaseCluster := func(t *testing.T) *Cluster {
		cluster := setupTestCluster(t, 1)
		cluster.StateMachine = new(state.StateMachine)
		cluster.StateMachine.Init()
		cluster.Conf = &config.Config{
			HaproxyAPIWriteBackend:     "service_write",
			HaproxyAPIReadBackend:      "service_read",
			HaproxyOn:                  true,
			HaproxyAPIBootstrapServers: true,
			HaproxyMode:                "runtimeapi",
		}
		return cluster
	}

	tests := []struct {
		name    string
		mutate  func(cluster *Cluster, proxy *HaproxyProxy)
		want    bool
		explain string
	}{
		{
			name:    "all conditions hold",
			mutate:  func(cluster *Cluster, proxy *HaproxyProxy) {},
			want:    true,
			explain: "baseline: bootstrap on, supported version, runtimeapi, no DNS",
		},
		{
			name: "bootstrap-servers off (the default)",
			mutate: func(cluster *Cluster, proxy *HaproxyProxy) {
				cluster.Conf.HaproxyAPIBootstrapServers = false
			},
			want: false,
		},
		{
			name: "HAProxy version below 2.6",
			mutate: func(cluster *Cluster, proxy *HaproxyProxy) {
				proxy.Version = "HAProxy version 2.4.0-1 2021/01/01"
			},
			want:    false,
			explain: "reconcileReadBackendServers' own version gate no-ops the whole function",
		},
		{
			name: "haproxy-mode != runtimeapi",
			mutate: func(cluster *Cluster, proxy *HaproxyProxy) {
				cluster.Conf.HaproxyMode = "standby"
			},
			want:    false,
			explain: "reconcileReadBackendServers' own mode gate no-ops the whole function",
		},
		{
			name: "resolver-backed proxy (HasDNS)",
			mutate: func(cluster *Cluster, proxy *HaproxyProxy) {
				cluster.Configurator.ProxyTags = []string{"dns"}
			},
			want:    false,
			explain: "skipAddingMembers skips exactly the add branch that would self-correct a missing row",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := newBaseCluster(t)
			defer cleanupTestCluster(t, cluster)

			proxy := &HaproxyProxy{Proxy: Proxy{
				ClusterGroup: cluster,
				Host:         "127.0.0.1",
				Port:         "1999",
				Datadir:      t.TempDir(),
				Version:      "HAProxy version 3.0.26-1 2024/05/01",
			}}

			tt.mutate(cluster, proxy)

			if got := proxy.reconcileReadBackendServersActive(); got != tt.want {
				t.Errorf("reconcileReadBackendServersActive() = %v, want %v (%s)", got, tt.want, tt.explain)
			}
		})
	}
}

// TestHaproxyReconcileBudgetDefersExcessWork pins the fix for a code-review
// finding: reconcileReadBackendServers used to issue every stale/missing
// server's Runtime API calls (SetMaintenance -> WaitSrvRemovable ->
// DelServer, or AddServer -> SetDrain -> EnableHealth) fully sequentially
// with no bound, and it runs inside the same goroutine cluster.refreshProxies
// wg.Wait()s on before the rest of the monitoring tick proceeds — a pass
// with several stale/missing servers could stall the whole cluster's
// monitoring tick by multiples of a single Runtime API round trip,
// delaying failover/switchover detection (DEVELOPMENT_LAWS.md F2-F4).
// haproxyReconcileBudget now bounds this: forced here to an
// already-elapsed deadline (deterministic, no reliance on real elapsed
// time — avoids a flaky sleep-based test) to confirm the add side is
// deferred entirely, but the removal side's safety-critical drain
// (SetMaintenance) is NOT — only WaitSrvRemovable/DelServer are — then
// restored to confirm the deferred add work isn't lost, just picked up on
// the very next pass. See also
// TestHaproxyReconcileRemovalDeadlineIsIndependentOfAddDeadline for why
// drain can't be deadline-gated the same way AddServer is.
func TestHaproxyReconcileBudgetDefersExcessWork(t *testing.T) {
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

	// slave1 is missing from HAProxy's stat output — would normally trigger
	// AddServer, must be deferred instead while the budget is exhausted.
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

	// decommissioned2 is pre-marked non-purgeable, as if a previous pass
	// already learned this from HAProxy's own refusal — its skippedRemoves
	// accounting (and therefore WARN0209) must stay accurate regardless of
	// budget, not just for entries the budget-gated loop actually reaches
	// (map iteration order is randomized, so which stale svnames get
	// reached before an exhausted budget varies pass to pass).
	proxy.markNonPurgeableReadServer("decommissioned2")
	knownToHaproxyWithGhost := map[string]bool{"decommissioned1": true, "decommissioned2": true}

	origBudget := haproxyReconcileBudget
	haproxyReconcileBudget = -1 * time.Second // already elapsed before the function even starts
	defer func() { haproxyReconcileBudget = origBudget }()

	// reconcileReadBackendServers directly, not Refresh(): decommissioned2
	// isn't a real "show stat" row (it doesn't need to be — its only role
	// here is to already be marked non-purgeable), so driving this through
	// Refresh()'s own stat-parsing would require fabricating a matching row
	// for no benefit.
	haRuntime := haproxy.Runtime{Host: host, Port: port}
	proxy.Version = "HAProxy version 3.0.26-1 2024/05/01"
	proxy.reconcileReadBackendServers(haRuntime, knownToHaproxyWithGhost, map[string]string{}, map[string]string{})

	commands := getCommands()
	// The add side defers entirely (no AddServer attempt at all).
	if cmdIndex(commands, "add server service_read/slave1 127.0.0.1:3307 check") >= 0 {
		t.Errorf("Refresh() commands = %v, want no add server while the reconcile budget is already exhausted", commands)
	}
	// The removal side still drains decommissioned1 — SetMaintenance is
	// never deadline-gated (see removeReadBackendServer's doc comment) —
	// but does NOT get as far as WaitSrvRemovable/DelServer, which are.
	if cmdIndex(commands, "set server service_read/decommissioned1 state maint") < 0 {
		t.Errorf("Refresh() commands = %v, want set maint for decommissioned1 even while the budget is exhausted (drain must never be skipped)", commands)
	}
	for _, unwanted := range []string{
		"wait 2000 srv-removable service_read/decommissioned1",
		"del server service_read/decommissioned1",
	} {
		if cmdIndex(commands, unwanted) >= 0 {
			t.Errorf("Refresh() commands = %v, want no %q while the reconcile budget is already exhausted", commands, unwanted)
		}
	}

	if !cluster.StateMachine.CurState.Search("WARN0209") {
		t.Errorf("expected WARN0209 to still be reported this pass (decommissioned2's non-purgeable skip must be counted regardless of budget)")
	}
	if !cluster.StateMachine.CurState.Search("WARN0210") {
		t.Errorf("expected WARN0210 to be reported when the reconcile budget is exhausted mid-pass")
	}

	// Restore the budget and confirm the deferred add isn't lost — it
	// completes on the very next pass.
	haproxyReconcileBudget = origBudget
	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	commands = getCommands()
	wantAdd := "add server service_read/slave1 127.0.0.1:3307 check"
	if cmdIndex(commands, wantAdd) < 0 {
		t.Errorf("Refresh() commands = %v, want to contain %q once the budget is available again (deferred work must not be lost)", commands, wantAdd)
	}
}

// TestHaproxyReconcileBudgetCheckedInsideHelpers pins a second, more subtle
// half of the same code-review finding as TestHaproxyReconcileBudgetDefersExcessWork:
// the budget must be checked *between* completeOrRollbackPendingAdd's own
// Runtime API calls (SetDrain, then EnableHealth), not just once by the
// caller before entering the helper — a single server whose SetDrain call
// alone consumes the whole remaining budget (a slow/wedged Runtime API
// socket, exactly the scenario haproxyReconcileBudget exists for) must not
// then also run EnableHealth past the deadline. The fake server here
// deliberately delays its SetDrain response so the budget genuinely elapses
// *during* the helper call, not just between per-server loop iterations —
// and asserts WARN0210 still fires even though this is the only server in
// the pass, so no later loop iteration's own deadline check could have set
// deadlineHit instead.
func TestHaproxyReconcileBudgetCheckedInsideHelpers(t *testing.T) {
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

	const drainDelay = 40 * time.Millisecond

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
					c.Write([]byte("New server registered.\n\n"))
				case cmd == "set server service_read/slave1 state drain":
					// Long enough to reliably outlast the budget below,
					// short enough this test still runs fast.
					time.Sleep(drainDelay)
					c.Write([]byte("\n"))
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

	origBudget := haproxyReconcileBudget
	// Comfortably longer than the cheap setup work before the add branch
	// (so the OUTER per-server check still passes normally) but much
	// shorter than drainDelay, so the budget is only exceeded partway
	// through completeOrRollbackPendingAdd itself.
	haproxyReconcileBudget = 5 * time.Millisecond
	defer func() { haproxyReconcileBudget = origBudget }()

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	mu.Lock()
	gotCommands := append([]string(nil), commands...)
	mu.Unlock()

	// Confirm the test actually reached the code path it claims to be
	// exercising, not just that EnableHealth happens to be absent for some
	// unrelated reason (e.g. AddServer or SetDrain never being reached at
	// all would also produce no "enable health" call).
	for _, want := range []string{
		"add server service_read/slave1 127.0.0.1:3307 check",
		"set server service_read/slave1 state drain",
	} {
		if cmdIndex(gotCommands, want) < 0 {
			t.Fatalf("Refresh() commands = %v, want to contain %q (test setup didn't reach the point this test needs to exercise)", gotCommands, want)
		}
	}

	if cmdIndex(gotCommands, "enable health service_read/slave1") >= 0 {
		t.Errorf("Refresh() commands = %v, want no enable health call once the budget elapsed during the preceding SetDrain call", gotCommands)
	}
	if !cluster.StateMachine.CurState.Search("WARN0210") {
		t.Errorf("expected WARN0210 to be reported even though slave1 was the only (and therefore last) server processed this pass")
	}
}

// TestHaproxyReconcileRemovalDeadlineIsIndependentOfAddDeadline pins the
// core fix for a code-review finding: reconcileReadBackendServers used to
// share a single deadline between the add/update loop and the removal
// loop. Since add/update always runs first, a sustained add backlog could
// consume the entire shared budget every single pass, leaving removal's
// WaitSrvRemovable/DelServer zero time, indefinitely.
//
// This asserts on WaitSrvRemovable/DelServer specifically, not
// SetMaintenance: SetMaintenance is never deadline-gated at all (see
// removeReadBackendServer), so its presence alone can't distinguish "removal
// has its own fresh deadline" from "removal isn't deadline-gated in this
// area either" — a weaker claim this test doesn't intend to make. If
// removeDeadline instead inherited a deadline already exhausted by the add
// loop's slow AddServer call, the internal check right after SetMaintenance
// (see removeReadBackendServer) would trip immediately and skip
// WaitSrvRemovable/DelServer entirely; asserting they're both reached is
// what actually distinguishes a fresh deadline from a stale, inherited one.
// The fake server delays slave1's AddServer response long enough to exhaust
// a deliberately tiny haproxyReconcileBudget entirely within the add loop
// before the removal loop even starts.
func TestHaproxyReconcileRemovalDeadlineIsIndependentOfAddDeadline(t *testing.T) {
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

	// decommissioned1 is stale (no matching cluster.Servers entry) —
	// removal work, competing with slave1's add work for budget.
	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "decommissioned1", "UP", "127.0.0.1:9999"),
	}, "\n")

	const addDelay = 40 * time.Millisecond

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
					// Long enough that addDeadline is fully spent before
					// the add loop even finishes with slave1, let alone
					// before the removal loop starts.
					time.Sleep(addDelay)
					c.Write([]byte("New server registered.\n\n"))
				case cmd == "set server service_read/decommissioned1 state maint":
					c.Write([]byte("\n"))
				case cmd == "wait 2000 srv-removable service_read/decommissioned1":
					c.Write([]byte("Done.\n\n"))
				case cmd == "del server service_read/decommissioned1":
					c.Write([]byte("Server deleted.\n\n"))
				}
			}(conn)
		}
	}()

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		// >= 3.0, so WaitSrvRemovable is part of the sequence this test
		// needs to observe.
		Version: "HAProxy version 3.0.26-1 2024/05/01",
	}}

	origBudget := haproxyReconcileBudget
	// Shorter than addDelay, so addDeadline is exhausted entirely within
	// the add loop's single AddServer call. Comfortably longer than the
	// fake server's near-instant responses to the removal-loop commands,
	// so removeDeadline (if genuinely fresh) has time to complete them.
	haproxyReconcileBudget = 5 * time.Millisecond
	defer func() { haproxyReconcileBudget = origBudget }()

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	mu.Lock()
	gotCommands := append([]string(nil), commands...)
	mu.Unlock()

	// Confirm the add loop actually reached AddServer (and therefore spent
	// its budget on the slow response) before drawing any conclusion from
	// the removal loop's behavior.
	if cmdIndex(gotCommands, "add server service_read/slave1 127.0.0.1:3307 check") < 0 {
		t.Fatalf("Refresh() commands = %v, want to contain the add attempt for slave1 (test setup didn't reach the point this test needs to exercise)", gotCommands)
	}

	for _, want := range []string{
		"set server service_read/decommissioned1 state maint",
		"wait 2000 srv-removable service_read/decommissioned1",
		"del server service_read/decommissioned1",
	} {
		if cmdIndex(gotCommands, want) < 0 {
			t.Errorf("Refresh() commands = %v, want to contain %q — removeDeadline must be its own fresh deadline, not one already exhausted by the add loop's slow AddServer call", gotCommands, want)
		}
	}
}

// TestHaproxyReconcileAddressCorrectionIgnoresBudget pins an explicit
// production-safety decision made after code review: correcting a
// backend's address is safety-critical, the same class as draining a
// stale server, and is therefore never deadline-gated — not deferred to a
// later pass like AddServer/WaitSrvRemovable/DelServer are. HAProxy keeps
// routing read traffic to the *previous* address until SetServerAddr
// lands; after a re-IP/reprovision that address may be unreachable
// (self-limiting via health checks) or, worse, may have been reassigned to
// a completely different host — traffic silently reaching the wrong
// target. This forces the reconcile budget to an already-elapsed deadline
// and asserts the address update still happens anyway, and that WARN0210
// does NOT fire at all: address correction never contributes to
// deadlineHit, so with no other missing/stale servers in this test, there
// is nothing left for WARN0210 to report.
func TestHaproxyReconcileAddressCorrectionIgnoresBudget(t *testing.T) {
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
	// still has the old one on file — the address-update branch, not add
	// or remove.
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

	origBudget := haproxyReconcileBudget
	haproxyReconcileBudget = -1 * time.Second // already elapsed before the function even starts
	defer func() { haproxyReconcileBudget = origBudget }()

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	commands := getCommands()
	if cmdIndex(commands, "set server service_read/slave1 addr 127.0.0.1 port 3399") < 0 {
		t.Errorf("Refresh() commands = %v, want the address update to still happen even though the reconcile budget is already exhausted — SetServerAddr is safety-critical and must never be deferred", commands)
	}

	// This proxy has no other missing/stale servers, so if WARN0210 fires
	// at all it can only be because the address-update branch wrongly
	// contributed to deadlineHit.
	for _, s := range *cluster.StateMachine.CurState {
		if s.ErrKey == "WARN0210" {
			t.Errorf("WARN0210 fired (%q) even though address correction is not supposed to be deadline-gated", s.ErrDesc)
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
		t.Errorf("Refresh() [pass 2] commands = %v, want to still contain %q (this fake server always reports decommissioned1 as \"UP\", never \"MAINT\", so draining must keep retrying — see TestHaproxyReconcileSkipsRedundantDrainForConfirmedMaintNonPurgeableServer for the case where HAProxy confirms MAINT)", pass2, wantMaint)
	}
	for _, unwanted := range []string{wantWait, wantDel} {
		if cmdIndex(pass2, unwanted) >= 0 {
			t.Errorf("Refresh() [pass 2] commands = %v, want no %q once decommissioned1 is known non-purgeable", pass2, unwanted)
		}
	}
}

// TestHaproxyReconcileSkipsRedundantDrainForConfirmedMaintNonPurgeableServer
// pins the fix for a code-review finding: removeReadBackendServer always
// issued SetMaintenance unconditionally, even for a svname already known
// non-purgeable from a previous pass. Once WARN0209 becomes a persistent,
// ongoing condition (several stale entries HAProxy will never let go of —
// exactly what it exists to report), that meant a full Runtime API round
// trip per such entry, every pass, forever — scaling linearly with count
// and reintroducing the unbounded-pass-time risk haproxyReconcileBudget
// exists to prevent, for this one case the per-pass deadline check can't
// help with (SetMaintenance runs before any deadline check). The fix reads
// this same pass's own "show stat" status for the svname (zero extra
// Runtime API cost, already fetched) and skips the redundant call only
// when it's already non-purgeable AND already confirmed MAINT — otherwise
// (see the test above) it still re-drains, in case something external
// re-armed it.
func TestHaproxyReconcileSkipsRedundantDrainForConfirmedMaintNonPurgeableServer(t *testing.T) {
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

	var mu sync.Mutex
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
				resp := statResponse
				mu.Unlock()
				switch {
				case cmd == "show stat":
					c.Write([]byte(resp))
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

	// Pass 1: decommissioned1 reports UP, gets the full drain/wait/delete
	// sequence, DelServer refuses, and it's marked non-purgeable.
	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() [pass 1] error = %v", err)
	}
	if !proxy.isNonPurgeableReadServer("decommissioned1") {
		t.Fatalf("after Refresh() [pass 1], expected decommissioned1 to be marked non-purgeable")
	}

	// Pass 2: HAProxy now confirms decommissioned1 is actually sitting in
	// MAINT (as pass 1's own SetMaintenance call would genuinely leave it,
	// unlike the always-"UP" fake server in the test above) — the
	// redundant SetMaintenance call must be skipped entirely.
	mu.Lock()
	statResponse = strings.Join([]string{
		haproxyStatRow("service_write", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "decommissioned1", "MAINT", "127.0.0.1:9999"),
	}, "\n")
	commands = nil
	mu.Unlock()

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() [pass 2] error = %v", err)
	}

	mu.Lock()
	pass2 := append([]string(nil), commands...)
	mu.Unlock()

	for _, unwanted := range []string{
		"set server service_read/decommissioned1 state maint",
		"wait 2000 srv-removable service_read/decommissioned1",
		"del server service_read/decommissioned1",
	} {
		if cmdIndex(pass2, unwanted) >= 0 {
			t.Errorf("Refresh() [pass 2] commands = %v, want no %q once HAProxy's own \"show stat\" confirms decommissioned1 is already MAINT and non-purgeable", pass2, unwanted)
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

// TestHaproxyReconcileAddServerSuccessResponseCompletesSequence pins the fix
// for a real bug found running this branch against a live HAProxy 3.0
// container: "add server" replies with a non-empty confirmation on success
// ("New server registered.") unlike every other admin command reconciled
// here, which reply with an empty body. Routing that response through the
// generic haproxyCmdFailed (any non-empty body = error) misclassified every
// successful add as a failure — SetDrain/EnableHealth
// (completeOrRollbackPendingAdd) never ran, so the newly added server sat
// in HAProxy fully live (not MAINT/DRAIN) with health checks permanently
// disabled ("no check" in "show stat"), while repman itself kept retrying
// "add server" every pass and logging "Already exists a server with the
// same name in backend." forever. startFakeHaproxy's default empty-body
// response for every non-"show stat" command happens to be correct for
// every other command, which is why no earlier test caught this — this one
// uses the real HAProxy success text instead.
func TestHaproxyReconcileAddServerSuccessResponseCompletesSequence(t *testing.T) {
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
					// The real HAProxy Runtime API success text (verified by
					// hand against haproxy:3.0) — non-empty despite success.
					c.Write([]byte("New server registered.\n\n"))
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
	wantHealth := "enable health service_read/slave1"
	for _, want := range []string{wantAdd, wantDrain, wantHealth} {
		if cmdIndex(gotCommands, want) < 0 {
			t.Errorf("Refresh() commands = %v, want to contain %q (add succeeded, sequence must complete)", gotCommands, want)
		}
	}

	// The bug retried "add server" every pass because it never recognized
	// success; confirm this single Refresh() pass issues it only once.
	addCount := 0
	for _, c := range gotCommands {
		if c == wantAdd {
			addCount++
		}
	}
	if addCount != 1 {
		t.Errorf("Refresh() issued %q %d times, want exactly once", wantAdd, addCount)
	}

	if proxy.isPendingReadServer("slave1") {
		t.Errorf("slave1 still marked pending after a successful add+drain+enable-health sequence")
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
