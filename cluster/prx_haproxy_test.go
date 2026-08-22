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

// startFakeHaproxy starts a fake HAProxy runtime-API server that records the
// first line of every command it receives and, only for "show stat",
// replies with statResponse. It mirrors the inline fakes above but is
// shared by the dynamic-backend self-heal tests below, which issue several
// runtime API commands per Refresh() call.
func startFakeHaproxy(t *testing.T, statResponse string) (host, port string, commands func() []string) {
	return startFakeHaproxyImpl(t, statResponse, defaultFakeHaproxyVersion, nil)
}

// startFakeHaproxyWithVersion behaves like startFakeHaproxy but answers
// "show version" with version instead of the default 3.4+ response --
// used to exercise hasDynamicBackendSupport's own fresh-fetch gating (e.g.
// a pre-3.4 HAProxy) independently of any cached proxy.Version.
func startFakeHaproxyWithVersion(t *testing.T, statResponse, version string) (host, port string, commands func() []string) {
	return startFakeHaproxyImpl(t, statResponse, version, nil)
}

// defaultFakeHaproxyVersion is what every fake HAProxy server answers
// "show version" with unless a test explicitly needs a different one --
// sufficient for hasDynamicBackendSupport(), which fetches it fresh on
// every Refresh() pass rather than trusting proxy.Version.
const defaultFakeHaproxyVersion = "HAProxy version 3.4.0-test"

// fakeHaproxyState tracks the backend/server state a test's fake HAProxy
// server mutates in response to the dynamic-backend commands under test, so
// a later "show backend"/"show stat" reflects earlier "add backend"/
// "publish backend"/"add server"/"enable server"/"del server"/
// "set server ... state maint" calls -- mirroring real HAProxy's runtime
// behavior closely enough for addDynamicBackend's and addDynamicServer's
// own existence/status verification (backendPublishedAtRuntime,
// serverStatusAtRuntime) to see something, rather than the empty response a
// fully static fake would always give them.
// dynServerState tracks one server row's status and address. addr is set
// once -- from "add server", or lazily seeded from the static baseline row
// on first "enable server"/"enable health"/"set ... state maint" -- and
// never changes: real HAProxy has no command that updates an address as a
// side effect, and a colliding "add server" is rejected outright (confirmed
// live: "Already exists a server with the same name in backend.").
type dynServerState struct {
	status string
	addr   string
}

type fakeHaproxyState struct {
	statResponse string
	version      string                               // "show version" response; hasDynamicBackendSupport fetches this fresh on every Refresh() pass rather than trusting proxy.Version
	dynBackends  map[string]bool                      // name -> published: a backend gets a "show stat" BACKEND row with status "UP (UNPUB)" as soon as "add backend" succeeds, before "publish backend" drops the "(UNPUB)" suffix
	dynServers   map[string]map[string]dynServerState // pool -> svname -> {status, addr}
}

func newFakeHaproxyState(statResponse, version string) *fakeHaproxyState {
	return &fakeHaproxyState{
		statResponse: statResponse,
		version:      version,
		dynBackends:  map[string]bool{},
		dynServers:   map[string]map[string]dynServerState{},
	}
}

// staticRowAddr returns the address field of the static baseline "show
// stat" row for pool/name, or "" if there isn't one.
func (s *fakeHaproxyState) staticRowAddr(pool, name string) string {
	for _, line := range strings.Split(s.statResponse, "\n") {
		fields := strings.Split(line, ",")
		if len(fields) > 1 && fields[0] == pool && fields[1] == name {
			return fields[len(fields)-1]
		}
	}
	return ""
}

// rowExists reports whether pool/name already has a row, dynamic or static
// -- what a real "add server" rejects a colliding name against regardless
// of which one it came from.
func (s *fakeHaproxyState) rowExists(pool, name string) bool {
	if _, ok := s.dynServers[pool][name]; ok {
		return true
	}
	return s.staticRowAddr(pool, name) != ""
}

// setDynServerStatus updates pool/name's tracked status, seeding its
// address the first time this name is touched (from the static baseline row
// if there is one, else "0.0.0.0:0") and preserving it on every later call
// -- mirrors "enable server"/"enable health"/"set server ... state maint"
// applying to any existing row, static or dynamic, without ever changing
// its address.
func (s *fakeHaproxyState) setDynServerStatus(pool, name, status string) {
	if s.dynServers[pool] == nil {
		s.dynServers[pool] = map[string]dynServerState{}
	}
	addr := s.dynServers[pool][name].addr
	if addr == "" {
		addr = s.staticRowAddr(pool, name)
	}
	if addr == "" {
		addr = "0.0.0.0:0"
	}
	s.dynServers[pool][name] = dynServerState{status: status, addr: addr}
}

// showServersStateResponse renders every tracked server (static baseline
// plus s.dynServers) in "show servers state" format: a version line, a
// "#"-prefixed header (both skipped by serverExistsAtRuntime's own comment
// handling), then one row per server with be_name at column 2 and srv_name
// at column 4 -- the columns serverExistsAtRuntime reads.
func (s *fakeHaproxyState) showServersStateResponse() string {
	lines := []string{
		"1",
		"# be_id be_name srv_id srv_name srv_addr srv_op_state srv_admin_state srv_uweight srv_iweight srv_time_since_last_change srv_check_status srv_check_result srv_check_health srv_check_state srv_agent_state bk_f_forced_id srv_f_forced_id srv_fqdn srv_port srvrecord srv_use_ssl srv_check_port srv_check_addr srv_agent_addr srv_agent_port",
	}
	seen := map[string]bool{}
	add := func(pool, name string) {
		key := pool + "/" + name
		if seen[key] {
			return
		}
		seen[key] = true
		lines = append(lines, "1 "+pool+" 1 "+name+" 0.0.0.0 2 0 100 100 0 L4OK 0 0 0 0 0 0 - 0 - 0 0 - - 0")
	}
	for _, line := range strings.Split(s.statResponse, "\n") {
		fields := strings.Split(line, ",")
		if len(fields) < 2 || fields[1] == "BACKEND" || fields[1] == "FRONTEND" {
			continue
		}
		add(fields[0], fields[1])
	}
	for pool, svs := range s.dynServers {
		for name := range svs {
			add(pool, name)
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// respond returns the bytes to write back for cmd, applying any state
// change it implies first. A nil return means this fake has nothing to say
// for cmd -- same as real HAProxy behavior for commands it doesn't
// recognize would look like from ApiCmd's perspective, the connection is
// just closed without a response.
func (s *fakeHaproxyState) respond(cmd string) []byte {
	switch {
	case strings.HasPrefix(cmd, "show version"):
		return []byte(s.version + "\n")
	case cmd == "show stat":
		// A pool/svname tracked in s.dynServers overrides the matching
		// static baseline row instead of duplicating it -- real HAProxy has
		// exactly one row per server regardless of whether "add server"
		// targeted a name that was already present (e.g. self-heal retrying
		// against a server left behind by an earlier pass's partial heal).
		consumed := make(map[string]map[string]bool)
		var lines []string
		for _, line := range strings.Split(s.statResponse, "\n") {
			if line == "" {
				continue
			}
			fields := strings.Split(line, ",")
			if len(fields) > 1 {
				pool, svname := fields[0], fields[1]
				if st, ok := s.dynServers[pool][svname]; ok {
					line = haproxyStatRow(pool, svname, st.status, st.addr)
					if consumed[pool] == nil {
						consumed[pool] = map[string]bool{}
					}
					consumed[pool][svname] = true
				}
			}
			lines = append(lines, line)
		}
		resp := strings.Join(lines, "\n")
		for name, published := range s.dynBackends {
			status := "UP (UNPUB)"
			if published {
				status = "UP"
			}
			resp += "\n" + haproxyStatRow(name, "BACKEND", status, "")
		}
		for pool, svs := range s.dynServers {
			for name, st := range svs {
				if consumed[pool][name] {
					continue
				}
				resp += "\n" + haproxyStatRow(pool, name, st.status, st.addr)
			}
		}
		return []byte(resp)
	case cmd == "show backend":
		names := make([]string, 0, len(s.dynBackends))
		for name := range s.dynBackends {
			names = append(names, name)
		}
		return []byte("# name " + strings.Join(names, " ") + "\n")
	case cmd == "show servers state":
		return []byte(s.showServersStateResponse())
	case strings.Contains(cmd, "add backend "):
		// Mirrors real HAProxy: the backend exists in runtime memory
		// (visible to "show backend"/"show stat", the latter with status
		// "UP (UNPUB)") as soon as "add backend" is accepted, before
		// "publish backend" makes it receive traffic -- see
		// addDynamicBackend.
		if name := backendNameFromCmd(cmd); name != "" {
			s.dynBackends[name] = false
		}
	case strings.HasPrefix(cmd, "publish backend "):
		if name := backendNameFromCmd(cmd); name != "" {
			if _, ok := s.dynBackends[name]; ok {
				s.dynBackends[name] = true
			}
		}
	case strings.HasPrefix(cmd, "add server "):
		// Mirrors real HAProxy: a dynamically-added server starts in
		// maintenance (disabled) at the requested address -- see
		// addDynamicServer. A name that already has a row, static or
		// dynamic, is rejected outright (confirmed live) and its existing
		// address is left untouched -- see rowExists.
		if pool, name := poolNameFromServerCmd(cmd); pool != "" && !s.rowExists(pool, name) {
			if s.dynServers[pool] == nil {
				s.dynServers[pool] = map[string]dynServerState{}
			}
			s.dynServers[pool][name] = dynServerState{status: "MAINT", addr: addrFromAddServerCmd(cmd)}
		}
	case strings.HasPrefix(cmd, "enable server "):
		// Mirrors real HAProxy: leaving maintenance alone does not start
		// health checking -- status reads the literal "no check" until
		// "enable health" follows, a genuinely separate, independent toggle.
		// Applies to any existing row, static or dynamic (see
		// setDynServerStatus) -- confirmed live that this still takes effect
		// even after a colliding "add server" was rejected for the same
		// name, leaving the row at its old address.
		if pool, name := poolNameFromServerCmd(cmd); s.rowExists(pool, name) {
			s.setDynServerStatus(pool, name, "no check")
		}
	case strings.HasPrefix(cmd, "enable health "):
		if pool, name := poolNameFromServerCmd(cmd); s.rowExists(pool, name) {
			s.setDynServerStatus(pool, name, "UP")
		}
	case strings.HasPrefix(cmd, "del server "):
		if pool, name := poolNameFromServerCmd(cmd); s.dynServers[pool] != nil {
			delete(s.dynServers[pool], name)
		}
	case strings.HasPrefix(cmd, "set server ") && strings.HasSuffix(cmd, "state maint"):
		if pool, name := poolNameFromServerCmd(cmd); s.rowExists(pool, name) {
			s.setDynServerStatus(pool, name, "MAINT")
		}
	}
	return nil
}

// poolNameFromServerCmd extracts <pool>, <name> from a command whose
// "<pool>/<name>" reference is its third space-separated token -- true for
// every server-targeting runtime command this package issues ("add server
// <pool>/<name> ...", "enable server <pool>/<name>", "del server
// <pool>/<name>", "set server <pool>/<name> ...").
func poolNameFromServerCmd(cmd string) (pool, name string) {
	fields := strings.Fields(cmd)
	if len(fields) < 3 {
		return "", ""
	}
	parts := strings.SplitN(fields[2], "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// addrFromAddServerCmd extracts the "<host>:<port>" address from an
// "add server <pool>/<name> <host>:<port> ..." command -- its fourth
// space-separated token.
func addrFromAddServerCmd(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) < 4 {
		return ""
	}
	return fields[3]
}

// startFakeHaproxyImpl is the shared core behind startFakeHaproxy and
// startFakeHaproxyWithFailures: it records every command's first line,
// forcibly resets the connection for any command matching a failPrefixes
// entry (nil/empty for startFakeHaproxy, which never fails anything), and
// otherwise responds via a fakeHaproxyState built from statResponse and
// version.
func startFakeHaproxyImpl(t *testing.T, statResponse, version string, failPrefixes []string) (host, port string, commands func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake haproxy server: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	host, port, _ = net.SplitHostPort(ln.Addr().String())

	var mu sync.Mutex
	var cmds []string
	state := newFakeHaproxyState(statResponse, version)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				line, _ := bufio.NewReader(c).ReadString('\n')
				cmd := strings.TrimRight(line, "\r\n")
				mu.Lock()
				cmds = append(cmds, cmd)
				mu.Unlock()

				for _, p := range failPrefixes {
					if strings.HasPrefix(cmd, p) {
						if tc, ok := c.(*net.TCPConn); ok {
							tc.SetLinger(0)
						}
						c.Close()
						return
					}
				}
				defer c.Close()

				mu.Lock()
				resp := state.respond(cmd)
				mu.Unlock()
				if resp != nil {
					c.Write(resp)
				}
			}(conn)
		}
	}()

	return host, port, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(cmds))
		copy(out, cmds)
		return out
	}
}

// backendNameFromCmd extracts the backend name from an
// "[experimental-mode on;] add backend <name> from <defaults> mode <mode>"
// or "publish backend <name>" command, or "" if the command doesn't match
// either shape.
func backendNameFromCmd(cmd string) string {
	fields := strings.Fields(cmd)
	for i, f := range fields {
		if f == "backend" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

func TestVersionSupportsDynamicBackends(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"HAProxy version 3.4.0-1c1a034 2026/06/01 - https://haproxy.org/", true},
		{"HAProxy version 3.4.5", true},
		{"HAProxy version 3.5.0", true},
		{"HAProxy version 4.0.0", true},
		{"HAProxy version 3.3.9", false},
		{"HAProxy version 2.8.5", false},
		{"", false},
		{"test", false},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			if got := versionSupportsDynamicBackends(tt.version); got != tt.want {
				t.Errorf("versionSupportsDynamicBackends(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

// startFakeHaproxyVersionOnly starts a fake HAProxy runtime-API server that
// only answers "show version" (with versionResponse) and records every
// command it receives -- used to test hasDynamicBackendSupport's own
// caching/re-fetch decision in isolation from the rest of Refresh().
func startFakeHaproxyVersionOnly(t *testing.T, versionResponse string) (host, port string, commands func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake haproxy server: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	host, port, _ = net.SplitHostPort(ln.Addr().String())

	var mu sync.Mutex
	var cmds []string
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
				cmds = append(cmds, cmd)
				mu.Unlock()
				if strings.HasPrefix(cmd, "show version") {
					c.Write([]byte(versionResponse))
				}
			}(conn)
		}
	}()

	return host, port, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(cmds))
		copy(out, cmds)
		return out
	}
}

// TestHaproxyHasDynamicBackendSupportAlwaysFetchesFresh reproduces the fix
// for a real gap: repman first reads HAProxy < 3.4, then the proxy is later
// reloaded/upgraded to 3.4+. Trusting Refresh()'s own proxy.Version cache
// (which only ever fetches once) would leave self-heal disabled forever
// after that upgrade, since nothing else re-fetches it. So
// hasDynamicBackendSupport fetches its own fresh version on every call --
// the same no-caching precedent srv.go's Refresh() already uses for
// database server versions -- and a stale/wrong pre-set proxy.Version must
// not affect the outcome at all.
func TestHaproxyHasDynamicBackendSupportAlwaysFetchesFresh(t *testing.T) {
	host, port, commands := startFakeHaproxyVersionOnly(t, "HAProxy version 3.4.0-test\n")
	haRuntime := &haproxy.Runtime{Host: host, Port: port}
	proxy := &HaproxyProxy{Proxy: Proxy{Version: "HAProxy version 2.8.5"}}

	if !proxy.hasDynamicBackendSupport(haRuntime) {
		t.Error("hasDynamicBackendSupport() = false, want true: the stale cached pre-3.4 version must not suppress a fresh fetch")
	}
	cmds := commands()
	if len(cmds) == 0 || !strings.HasPrefix(cmds[0], "show version") {
		t.Errorf("commands = %v, want a \"show version\" round trip", cmds)
	}
	if got := proxy.GetVersion(); !strings.Contains(got, "3.4.0") {
		t.Errorf("proxy.Version = %q, want the freshly-fetched version to be cached", got)
	}
}

// TestHaproxyHasDynamicBackendSupportFalseOnFetchFailure reproduces a
// transport failure (HAProxy unreachable): hasDynamicBackendSupport must
// report no support rather than falling back to any previously-cached
// version, stale or not.
func TestHaproxyHasDynamicBackendSupportFalseOnFetchFailure(t *testing.T) {
	proxy := &HaproxyProxy{Proxy: Proxy{Version: "HAProxy version 3.4.0-test"}}
	// Nothing is listening on this address, so GetVersion() fails at dial time.
	haRuntime := &haproxy.Runtime{Host: "127.0.0.1", Port: "1"}

	if proxy.hasDynamicBackendSupport(haRuntime) {
		t.Error("hasDynamicBackendSupport() = true, want false when the version fetch itself fails")
	}
}

// newSelfHealTestCluster builds a 2-server cluster (master1/slave1) wired up
// the same way the same-pass Refresh() tests above do, for use by the
// dynamic-backend self-heal tests.
func newSelfHealTestCluster(t *testing.T) (cluster *Cluster, master, slave *ServerMonitor) {
	cluster = setupTestCluster(t, 2)
	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Topology = config.TopoMasterSlave
	cluster.Conf = &config.Config{
		HaproxyMode:                   "runtimeapi",
		HaproxyAPIWriteBackend:        "service_write",
		HaproxyAPIReadBackend:         "service_read",
		HaproxyOn:                     true,
		HaproxyWritePort:              3306,
		HaproxyRuntimeDynamicBackends: true,
	}

	master = cluster.Servers[0]
	master.Id = "master1"
	master.Host = "127.0.0.1"
	master.Port = "3306"
	master.State = stateMaster
	master.ClusterGroup = cluster

	slave = cluster.Servers[1]
	slave.Id = "slave1"
	slave.Host = "127.0.0.1"
	slave.Port = "3307"
	slave.State = stateSlave
	slave.ClusterGroup = cluster

	cluster.master = master
	cluster.slaves = []*ServerMonitor{slave}

	return cluster, master, slave
}

// TestHaproxySelfHealMissingReadServer reproduces a server that was added to
// the cluster after HAProxy's runtime state was last provisioned: it has no
// row at all under service_read in "show stat", even though the backend
// itself and the other server are healthy. On HAProxy 3.4+, Refresh() must
// dynamically add it.
func TestHaproxySelfHealMissingReadServer(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		// slave1 row intentionally omitted -- never provisioned into HAProxy.
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	wantAdd := "add server service_read/slave1 127.0.0.1:3307 check inter 1000 weight 100 maxconn 2000"
	wantEnable := "enable server service_read/slave1"
	wantHealth := "enable health service_read/slave1"

	for _, want := range []string{wantAdd, wantEnable, wantHealth} {
		if cmdIndex(cmds, want) < 0 {
			t.Errorf("Refresh() commands = %v, want to contain %q", cmds, want)
		}
	}
	if cmdIndex(cmds, "add server service_write/leader 127.0.0.1:3306 check inter 1000 weight 100 maxconn 2000") >= 0 {
		t.Errorf("Refresh() commands = %v, did not want a re-add of the already-present leader server", cmds)
	}
}

// TestHaproxySelfHealMissingWriteBackend reproduces the write backend itself
// being absent from HAProxy's runtime state (no BACKEND summary row and no
// leader row at all), while the read backend is intact. On HAProxy 3.4+,
// Refresh() must recreate and publish the backend, then add and enable the
// leader server pointed at the current master.
func TestHaproxySelfHealMissingWriteBackend(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	statResponse := strings.Join([]string{
		// service_write has no rows at all.
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	want := []string{
		"experimental-mode on; add backend service_write from dyn_defaults mode tcp",
		"publish backend service_write",
		"add server service_write/leader 127.0.0.1:3306 check inter 1000 weight 100 maxconn 2000",
		"enable server service_write/leader",
		"enable health service_write/leader",
	}
	for _, w := range want {
		if cmdIndex(cmds, w) < 0 {
			t.Errorf("Refresh() commands = %v, want to contain %q", cmds, w)
		}
	}
}

// TestHaproxySelfHealDisabledByDefault reproduces the exact same
// missing-write-backend gap as TestHaproxySelfHealMissingWriteBackend, on
// otherwise fully eligible HAProxy 3.4+ runtimeapi state, but with
// haproxy-runtime-dynamic-backends left at its default (false). Per T14,
// this new feature must ship off by default: Refresh() must not issue any
// dynamic-backend command, and must not even probe "show version" for it,
// until an operator explicitly opts in.
func TestHaproxySelfHealDisabledByDefault(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf.HaproxyRuntimeDynamicBackends = false

	statResponse := strings.Join([]string{
		// service_write has no rows at all.
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	for _, cmd := range commands() {
		if strings.HasPrefix(cmd, "show version") {
			t.Errorf("Refresh() commands = %v, did not want a \"show version\" probe while haproxy-runtime-dynamic-backends is off", commands())
		}
		if strings.HasPrefix(cmd, "add backend") || strings.HasPrefix(cmd, "publish backend") ||
			strings.HasPrefix(cmd, "add server") || strings.HasPrefix(cmd, "enable server") ||
			strings.HasPrefix(cmd, "enable health") || strings.HasPrefix(cmd, "del server") {
			t.Errorf("Refresh() issued dynamic-backend command %q while haproxy-runtime-dynamic-backends is off", cmd)
		}
	}
}

// TestHaproxySelfHealNoopOnOldHaproxy reproduces the same missing-server gap
// as TestHaproxySelfHealMissingReadServer but on a pre-3.4 HAProxy, where
// "publish backend" doesn't exist. Refresh() must not attempt any dynamic
// backend/server commands and must leave prior behavior untouched.
func TestHaproxySelfHealNoopOnOldHaproxy(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
	}, "\n")

	host, port, commands := startFakeHaproxyWithVersion(t, statResponse, "HAProxy version 2.8.5")

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 2.8.5",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	for _, cmd := range commands() {
		if strings.HasPrefix(cmd, "add server") || strings.HasPrefix(cmd, "add backend") ||
			strings.HasPrefix(cmd, "publish backend") || strings.HasPrefix(cmd, "enable server") ||
			strings.HasPrefix(cmd, "enable health") || strings.HasPrefix(cmd, "del server") {
			t.Errorf("Refresh() issued dynamic-backend command %q against a pre-3.4 HAProxy", cmd)
		}
	}
}

// TestHaproxySelfHealPrunesStaleReadServer reproduces a read backend server
// that no longer corresponds to any current cluster server (e.g. the node
// was removed from the cluster after HAProxy's runtime state was
// provisioned). On HAProxy 3.4+, Refresh() must best-effort drain it to
// maintenance and delete it, without touching the servers that are still
// part of the cluster.
func TestHaproxySelfHealPrunesStaleReadServer(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
		haproxyStatRow("service_read", "oldnode1", "UP", "127.0.0.1:3308"),
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	wantMaint := "set server service_read/oldnode1 state maint"
	wantDel := "del server service_read/oldnode1"
	maintIdx := cmdIndex(cmds, wantMaint)
	delIdx := cmdIndex(cmds, wantDel)
	if maintIdx < 0 {
		t.Errorf("Refresh() commands = %v, want to contain %q", cmds, wantMaint)
	}
	if delIdx < 0 {
		t.Errorf("Refresh() commands = %v, want to contain %q", cmds, wantDel)
	}
	if maintIdx >= 0 && delIdx >= 0 && maintIdx >= delIdx {
		t.Errorf("Refresh() commands = %v, want %q (index %d) before %q (index %d)", cmds, wantMaint, maintIdx, wantDel, delIdx)
	}
	for _, prefix := range []string{
		"add server service_read/master1",
		"add server service_read/slave1",
		"add server service_read/oldnode1",
	} {
		for _, cmd := range cmds {
			if strings.HasPrefix(cmd, prefix) {
				t.Errorf("Refresh() commands = %v, did not want a command starting with %q", cmds, prefix)
			}
		}
	}
}

// TestHaproxySelfHealSkipsIneligibleReadServer reproduces a cluster server
// that has no row at all under service_read (same gap as
// TestHaproxySelfHealMissingReadServer) but is currently in a state
// Refresh()'s own eligibility checks (stateSlaveErr, stateRelayErr,
// stateSlaveLate, stateRelayLate, IsIgnored(), stateWsrepLate,
// stateWsrepDonor) would immediately drain if it did have a row. Self-heal
// must not add and enable it -- doing so would serve live read traffic from
// a broken/lagged replica for a full Refresh() cycle before the next pass
// drains it back out.
func TestHaproxySelfHealSkipsIneligibleReadServer(t *testing.T) {
	cluster, _, slave := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	slave.State = stateSlaveErr

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		// slave1 row intentionally omitted, same as the missing-server test,
		// but this slave is in a broken-replication state.
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	for _, cmd := range commands() {
		if strings.HasPrefix(cmd, "add server service_read/slave1") {
			t.Errorf("Refresh() commands = %v, did not want slave1 (state stateSlaveErr) to be self-healed into the read backend", commands())
		}
	}
}

// TestHaproxySelfHealRefusesHostnameBackedServer reproduces a missing read
// server whose Host is an FQDN, not a literal IP. The runtime API's
// "set server ... fqdn" only works on a server that already has a static
// "resolvers" association -- there's no way to attach one to a server
// created via "add server", which has no resolvers keyword at all. So
// self-heal must refuse this server outright rather than creating a
// permanently-unresolved one while logging success: no "add server" call
// at all, and the server stays missing until an operator reload adds it
// statically (with "resolvers dns" -- see GetConfigProxyModule).
func TestHaproxySelfHealRefusesHostnameBackedServer(t *testing.T) {
	cluster, _, slave := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC // proxy.HasDNS() true, irrelevant to the fix but representative of where FQDN hosts occur

	slave.Host = "slave1.internal.example.com"

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	for _, cmd := range commands() {
		if strings.HasPrefix(cmd, "add server service_read/slave1") || strings.HasPrefix(cmd, "set server service_read/slave1 fqdn") {
			t.Errorf("Refresh() commands = %v, did not want any dynamic-server command for a hostname-backed server (no runtime way to attach DNS resolution to it)", commands())
		}
	}
}

// startFakeHaproxyWithFailures behaves like startFakeHaproxy but, for any
// command whose first line has one of failPrefixes as a prefix, forcibly
// resets the TCP connection instead of responding -- so the client-side
// Runtime method returns an error, the same as HAProxy rejecting a command
// (e.g. a stale/older HAProxy, or the backend already gone). It lets tests
// simulate one specific runtime API command failing.
func startFakeHaproxyWithFailures(t *testing.T, statResponse string, failPrefixes ...string) (host, port string, commands func() []string) {
	return startFakeHaproxyImpl(t, statResponse, defaultFakeHaproxyVersion, failPrefixes)
}

// TestHaproxySelfHealWriteFailureDoesNotBlockReadRepair reproduces both
// backends needing repair in the same Refresh() pass -- the write backend
// is entirely missing and its recreation is made to fail (simulating e.g. a
// concurrent config change or an unexpected HAProxy rejection), while the
// read backend is intact but missing a server. The read-side repair must
// still happen: the two sides are independent, and a failure on one must
// not skip the other in the same pass.
func TestHaproxySelfHealWriteFailureDoesNotBlockReadRepair(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	statResponse := strings.Join([]string{
		// service_write has no rows at all -- must be recreated, and that
		// recreation is made to fail below.
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		// slave1 row intentionally omitted.
	}, "\n")

	host, port, commands := startFakeHaproxyWithFailures(t, statResponse,
		"experimental-mode on; add backend service_write")

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	if cmdIndex(cmds, "experimental-mode on; add backend service_write from dyn_defaults mode tcp") < 0 {
		t.Fatalf("Refresh() commands = %v, want the (failing) write backend add attempt to still have been issued", cmds)
	}
	want := "add server service_read/slave1 127.0.0.1:3307 check inter 1000 weight 100 maxconn 2000"
	if cmdIndex(cmds, want) < 0 {
		t.Errorf("Refresh() commands = %v, want read-side self-heal (%q) to proceed despite the write backend recreation failing", cmds, want)
	}
	// The write backend never got created, so the leader server must not
	// have been added against it either.
	for _, cmd := range cmds {
		if strings.HasPrefix(cmd, "add server service_write/leader") {
			t.Errorf("Refresh() commands = %v, did not want a leader add after the write backend recreation failed", cmds)
		}
	}
}

// TestHaproxySelfHealReAddsReaderMasterRow reproduces the master/leader's
// own read-backend row being entirely absent (e.g. service_read was just
// recreated, or the row was otherwise never provisioned into it) while
// policy says the master should presently be a reader
// (PRXServersReadOnMaster). Without dedicated handling, the master's row
// would never exist for masterReadFound to act on, and reads would stay
// blackholed indefinitely; self-heal must add and enable it like any other
// missing server.
func TestHaproxySelfHealReAddsReaderMasterRow(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)
	cluster.Configurator.ClusterConfig.PRXServersReadOnMaster = true

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
		// master1 row intentionally omitted from service_read.
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	want := []string{
		"add server service_read/master1 127.0.0.1:3306 check inter 1000 weight 100 maxconn 2000",
		"enable server service_read/master1",
		"enable health service_read/master1",
	}
	for _, w := range want {
		if cmdIndex(cmds, w) < 0 {
			t.Errorf("Refresh() commands = %v, want to contain %q", cmds, w)
		}
	}
}

// TestHaproxySelfHealLeavesNonReaderMasterRowAbsent is the converse of
// TestHaproxySelfHealReAddsReaderMasterRow: the master/leader's read-backend
// row is absent, but policy says it should *not* presently be a reader (a
// healthy slave reader is available and neither read-on-master fallback is
// set). Self-heal must leave it absent rather than adding it and then
// immediately relying on a later pass to drain it back out.
func TestHaproxySelfHealLeavesNonReaderMasterRowAbsent(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)
	// Both fallbacks left at their zero value (false): master should not be
	// a reader while a healthy slave reader exists.

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
		// master1 row intentionally omitted from service_read.
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	for _, cmd := range commands() {
		if strings.HasPrefix(cmd, "add server service_read/master1") {
			t.Errorf("Refresh() commands = %v, did not want master1 re-added to service_read while a healthy slave reader is available and no read-on-master policy applies", commands())
		}
	}
}

// TestHaproxySelfHealMasterRowSeesSameHealedSlave reproduces the case where,
// in a single Refresh() pass, both the master's read-backend row AND a
// slave's read-backend row are missing at the same time, under the no-slave
// read-on-master fallback. The read-eligible loop runs first and restores
// the slave -- masterShouldBeReader()'s HasAvailableReader() check for the
// master-row step right after it must see that just-restored slave in the
// very same pass, not the pre-heal snapshot from "show stat" (which had no
// available reader at all). Without that, the no-slave fallback would
// incorrectly conclude no reader is available and add the master as a
// reader transiently, even though the slave was actually just fixed.
func TestHaproxySelfHealMasterRowSeesSameHealedSlave(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		// Both master1 and slave1 rows intentionally omitted from
		// service_read -- neither has ever been provisioned into it.
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	wantSlaveAdd := "add server service_read/slave1 127.0.0.1:3307 check inter 1000 weight 100 maxconn 2000"
	if cmdIndex(cmds, wantSlaveAdd) < 0 {
		t.Fatalf("Refresh() commands = %v, want to contain %q (test setup: slave1 must actually get self-healed for this test to be meaningful)", cmds, wantSlaveAdd)
	}
	for _, cmd := range cmds {
		if strings.HasPrefix(cmd, "add server service_read/master1") {
			t.Errorf("Refresh() commands = %v, did not want master1 added to service_read: slave1 was restored earlier in the same pass and must count as an available reader", cmds)
		}
	}
}

// TestHaproxySelfHealLiteralIPIgnoresHasDNS reproduces a HasDNS()-true
// deployment (e.g. OpenSVC/Kubernetes orchestration) where the specific
// cluster server being self-healed nonetheless has a literal IP as its
// Host, not an FQDN -- HasDNS() is a proxy-level flag and says nothing
// about any individual server's own host. addDynamicServer must decide
// whether a server needs to be refused as hostname-backed (see
// TestHaproxySelfHealRefusesHostnameBackedServer) the same way SetMaster
// decides FQDN handling for the write backend's leader -- by whether the
// host itself parses as an IP -- not by proxy.HasDNS(). Otherwise a
// literal-IP server would be wrongly refused (or, before that fix, routed
// through a placeholder address), which is not equivalent to just handing
// HAProxy the real IP directly.
func TestHaproxySelfHealLiteralIPIgnoresHasDNS(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC // forces proxy.HasDNS() true
	// The slave's Host is left at its default "127.0.0.1" from newSelfHealTestCluster.

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		// slave1 row intentionally omitted.
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if !proxy.HasDNS() {
		t.Fatal("test setup error: proxy.HasDNS() = false, want true")
	}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	wantAdd := "add server service_read/slave1 127.0.0.1:3307 check inter 1000 weight 100 maxconn 2000"
	if cmdIndex(cmds, wantAdd) < 0 {
		t.Errorf("Refresh() commands = %v, want to contain %q (a literal-IP server must be added with its real address even when proxy.HasDNS() is true)", cmds, wantAdd)
	}
	for _, cmd := range cmds {
		if strings.HasPrefix(cmd, "add server service_read/slave1 0.0.0.0") {
			t.Errorf("Refresh() commands = %v, did not want the FQDN placeholder path for a literal-IP server", cmds)
		}
		if strings.HasPrefix(cmd, "set server service_read/slave1 fqdn") {
			t.Errorf("Refresh() commands = %v, did not want a \"set server ... fqdn\" call for a literal-IP server", cmds)
		}
	}
}

// TestHaproxySelfHealPartialFailureNotCountedAsAvailable reproduces
// addDynamicServer's AddServer step succeeding but EnableServer failing
// (e.g. a transient runtime API error). The server is still effectively in
// maintenance/unusable, so it must not be counted as an available reader
// for this same pass's master-row decision: with the no-slave fallback
// enabled and no other reader, the master must still be added, exactly as
// it would be if self-heal hadn't attempted the slave at all.
func TestHaproxySelfHealPartialFailureNotCountedAsAvailable(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		// Both master1 and slave1 rows intentionally omitted.
	}, "\n")

	host, port, commands := startFakeHaproxyWithFailures(t, statResponse,
		"enable server service_read/slave1")

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	if cmdIndex(cmds, "enable server service_read/slave1") < 0 {
		t.Fatalf("Refresh() commands = %v, want the (failing) enable attempt to still have been issued", cmds)
	}
	wantMasterAdd := "add server service_read/master1 127.0.0.1:3306 check inter 1000 weight 100 maxconn 2000"
	if cmdIndex(cmds, wantMasterAdd) < 0 {
		t.Errorf("Refresh() commands = %v, want %q: slave1's enable step failed, so it must not count as an available reader and the no-slave fallback must still add the master", cmds, wantMasterAdd)
	}
}

// startFakeHaproxyRejectingBackendCreation behaves like startFakeHaproxy but
// simulates a HAProxy process that predates the "dyn_defaults" defaults
// section: "add backend"/"publish backend" succeed at the transport level
// (no error) -- exactly what real HAProxy does even when it rejects the
// command with plain CLI response text, since ApiCmd only ever errors on a
// transport failure -- but the backend never actually appears in "show
// stat". This reproduces the dyn_defaults live-process dependency that
// addDynamicBackend must detect via backendPublishedAtRuntime rather than
// trusting a nil err.
func startFakeHaproxyRejectingBackendCreation(t *testing.T, statResponse string) (host, port string, commands func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake haproxy server: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	host, port, _ = net.SplitHostPort(ln.Addr().String())

	var mu sync.Mutex
	var cmds []string
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
				cmds = append(cmds, cmd)
				mu.Unlock()
				switch {
				case strings.HasPrefix(cmd, "show version"):
					c.Write([]byte(defaultFakeHaproxyVersion + "\n"))
				case cmd == "show stat":
					c.Write([]byte(statResponse))
				case cmd == "show backend":
					c.Write([]byte("# name\n"))
				case strings.Contains(cmd, "add backend "):
					c.Write([]byte("Can't create the backend: unknown defaults section 'dyn_defaults'.\n"))
				case strings.HasPrefix(cmd, "publish backend "):
					c.Write([]byte("No such backend.\n"))
				}
			}(conn)
		}
	}()

	return host, port, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(cmds))
		copy(out, cmds)
		return out
	}
}

// TestHaproxySelfHealMissingBackendVerifiesExistenceAfterCreate reproduces a
// HAProxy process that hasn't been reloaded since "dyn_defaults" was added
// to the config: HAProxy rejects "add backend ... from dyn_defaults ..."
// with plain CLI text, not a socket error, so ApiCmd sees no err. Without
// verifying against "show backend", addDynamicBackend would wrongly report
// success and self-heal would go on to try adding the leader server into a
// backend that doesn't actually exist.
func TestHaproxySelfHealMissingBackendVerifiesExistenceAfterCreate(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	statResponse := strings.Join([]string{
		// service_write has no rows at all.
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
	}, "\n")

	host, port, commands := startFakeHaproxyRejectingBackendCreation(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	if cmdIndex(cmds, "experimental-mode on; add backend service_write from dyn_defaults mode tcp") < 0 {
		t.Fatalf("Refresh() commands = %v, want the (rejected) write backend add attempt to still have been issued", cmds)
	}
	for _, cmd := range cmds {
		if strings.HasPrefix(cmd, "add server service_write/leader") {
			t.Errorf("Refresh() commands = %v, did not want a leader add against a backend that show backend confirms doesn't actually exist", cmds)
		}
	}
}

// TestHaproxySelfHealSkipsLeaderWhenNoMasterKnown reproduces the write
// backend needing recreation while no master is currently known
// (cluster.GetMaster() == nil). Self-heal must not create the "leader" row
// at all in this case: a dynamically-added server starts in maintenance by
// default, but if it were instead created and *enabled* against a
// placeholder address, writes could be routed to a target that isn't the
// real master, instead of cleanly failing until one exists. Leaving the row
// absent also means self-heal's own !sawWriteLeader check simply retries
// every future Refresh() pass and creates+enables it correctly, in one
// step, the moment a valid master becomes known.
func TestHaproxySelfHealSkipsLeaderWhenNoMasterKnown(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)
	cluster.master = nil
	cluster.vmaster = nil

	statResponse := strings.Join([]string{
		// service_write has no rows at all.
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if cluster.GetMaster() != nil {
		t.Fatal("test setup error: cluster.GetMaster() != nil, want nil")
	}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	if cmdIndex(cmds, "experimental-mode on; add backend service_write from dyn_defaults mode tcp") < 0 {
		t.Fatalf("Refresh() commands = %v, want the write backend itself to still be recreated", cmds)
	}
	for _, cmd := range cmds {
		if strings.HasPrefix(cmd, "add server service_write/leader") {
			t.Errorf("Refresh() commands = %v, did not want a leader row created while no master is known (found %q)", cmds, cmd)
		}
	}
}

// TestHaproxySelfHealRefusesHostnameBackedMaster reproduces the write
// backend needing its leader server recreated while the current master's
// Host is an FQDN. addDynamicServer refuses any hostname-backed server
// outright (see TestHaproxySelfHealRefusesHostnameBackedServer), and the
// write-leader call site checks this itself before ever calling it, so no
// "add server" attempt is made at all for the leader in this case.
func TestHaproxySelfHealRefusesHostnameBackedMaster(t *testing.T) {
	cluster, master, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)
	master.Host = "master1.internal.example.com"

	statResponse := strings.Join([]string{
		// service_write has no rows at all.
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	for _, cmd := range commands() {
		if strings.HasPrefix(cmd, "add server service_write/leader") {
			t.Errorf("Refresh() commands = %v, did not want any add-server attempt for a hostname-backed master (found %q)", commands(), cmd)
		}
	}
}

// TestHaproxySelfHealSkipsStagingProxy reproduces a staging proxy
// (cluster.Conf.TopologyStaging + proxy.IsInStaging()) whose write and read
// backends are both missing at runtime. Staging targets a different backend
// entirely (cluster.Conf.HaproxyStagingBackend, pointed at the standalone
// staging server) and a different read-eligibility rule (only that one
// server should ever be UP, regardless of replication state) -- neither of
// which selfHealDynamicBackends understands. It must not act on a staging
// proxy at all: recreating HaproxyAPIWriteBackend/HaproxyAPIReadBackend (the
// wrong backend for staging) and adding ordinary replicas by replication
// state (exactly what staging mode drains them for) would both be wrong.
func TestHaproxySelfHealSkipsStagingProxy(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf.TopologyStaging = true

	statResponse := "" // both service_write and service_read entirely missing

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
		IsStaging:    true,
	}}

	if !proxy.IsInStaging() {
		t.Fatal("test setup error: proxy.IsInStaging() = false, want true")
	}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	for _, cmd := range commands() {
		if strings.HasPrefix(cmd, "add backend") || strings.HasPrefix(cmd, "publish backend") || strings.HasPrefix(cmd, "add server") {
			t.Errorf("Refresh() commands = %v, did not want any dynamic-backend self-heal command on a staging proxy (found %q)", commands(), cmd)
		}
		// Same as standby mode: Refresh() checks staging itself before ever
		// calling hasDynamicBackendSupport, so a staging proxy shouldn't pay
		// for the "show version" round trip every pass either.
		if strings.HasPrefix(cmd, "show version") {
			t.Errorf("Refresh() commands = %v, did not want a \"show version\" probe on a staging proxy (found %q)", commands(), cmd)
		}
	}
}

// TestHaproxySelfHealSkipsLeaderWhenMasterInMaintenance reproduces the
// write backend disappearing while the current master is already in
// maintenance. Unlike the read backend, Refresh()'s main "show stat" loop
// has no same-pass write-side maintenance reconciliation (that only happens
// via the separate SetMaintenance() method, triggered elsewhere by state
// transitions, not by this pass), so self-heal must not create -- and
// definitely must not enable -- the "leader" row here: doing so could route
// write traffic to a maintenance master indefinitely, since nothing else in
// this pass, or any future one absent another real maintenance transition,
// would correct it back out. Leaving the row absent lets self-heal's own
// !sawWriteLeader check simply retry every future pass instead.
func TestHaproxySelfHealSkipsLeaderWhenMasterInMaintenance(t *testing.T) {
	cluster, master, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)
	master.IsMaintenance = true

	statResponse := strings.Join([]string{
		// service_write has no rows at all.
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	if cmdIndex(cmds, "experimental-mode on; add backend service_write from dyn_defaults mode tcp") < 0 {
		t.Fatalf("Refresh() commands = %v, want the write backend itself to still be recreated", cmds)
	}
	for _, cmd := range cmds {
		if strings.HasPrefix(cmd, "add server service_write/leader") {
			t.Errorf("Refresh() commands = %v, did not want a leader row created while the master is in maintenance (found %q)", cmds, cmd)
		}
	}
}

// TestHaproxySelfHealSkipsMasterReadRowWhenInMaintenance reproduces the
// master's read-backend row being missing while the master is currently in
// maintenance and read-on-master policy would otherwise call for it. This
// row was absent from the very snapshot that triggered self-heal, so unlike
// an ordinary maintenance transition (handled by the separate
// SetMaintenance() method), there is nothing else in this Refresh() pass to
// correct a wrongly-added row back out -- self-heal must not add it at all,
// matching how the ordinary-replica loop already skips maintenance servers
// entirely rather than adding then draining them.
func TestHaproxySelfHealSkipsMasterReadRowWhenInMaintenance(t *testing.T) {
	cluster, master, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)
	master.IsMaintenance = true
	cluster.Configurator.ClusterConfig.PRXServersReadOnMaster = true

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		// master1 row intentionally omitted from service_read.
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	for _, cmd := range commands() {
		if strings.HasPrefix(cmd, "add server service_read/master1") {
			t.Errorf("Refresh() commands = %v, did not want master1 added to service_read while it is in maintenance, even with PRXServersReadOnMaster set", commands())
		}
	}
}

// TestHaproxySelfHealVerifiesStaleServerDeletion reproduces the stale-server
// prune path's DelServer call: ApiCmd only ever errors on a transport
// failure, never on HAProxy rejecting a command with plain CLI text (the
// same limitation addDynamicBackend already works around for "add backend"
// -- see TestHaproxySelfHealMissingBackendVerifiesExistenceAfterCreate), so
// a rejected "del server" (e.g. against a statically-declared row, which it
// can never remove) would look identical to a successful one from err
// alone. This confirms the prune loop now issues a "show servers state"
// verification call after "del server" instead of trusting a nil err.
func TestHaproxySelfHealVerifiesStaleServerDeletion(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
		haproxyStatRow("service_read", "oldnode1", "UP", "127.0.0.1:3308"),
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	delIdx := cmdIndex(cmds, "del server service_read/oldnode1")
	showIdx := cmdIndex(cmds, "show servers state")
	if delIdx < 0 {
		t.Fatalf("Refresh() commands = %v, want %q", cmds, "del server service_read/oldnode1")
	}
	if showIdx < 0 {
		t.Errorf("Refresh() commands = %v, want a \"show servers state\" verification call after del server", cmds)
	}
	if showIdx >= 0 && showIdx <= delIdx {
		t.Errorf("Refresh() commands = %v, want \"show servers state\" (index %d) after \"del server ...\" (index %d)", cmds, showIdx, delIdx)
	}
}

// TestHaproxySelfHealCleansUpPartiallyHealedLeader reproduces AddServer
// succeeding for the write backend's leader but EnableServer failing right
// after (e.g. a transient runtime API error). Unlike the read backend,
// Refresh() has no reconciliation that would ever notice and fix a leader
// stuck in maintenance -- and once the row exists, sawWriteLeader would see
// it on every future pass and never retry. Self-heal must clean the
// half-added row back up (maintenance, then delete) so the next pass
// retries the whole add+enable sequence from scratch.
func TestHaproxySelfHealCleansUpPartiallyHealedLeader(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	statResponse := strings.Join([]string{
		// service_write has no rows at all.
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
	}, "\n")

	host, port, commands := startFakeHaproxyWithFailures(t, statResponse,
		"enable server service_write/leader")

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	addIdx := cmdIndex(cmds, "add server service_write/leader 127.0.0.1:3306 check inter 1000 weight 100 maxconn 2000")
	enableIdx := cmdIndex(cmds, "enable server service_write/leader")
	if addIdx < 0 {
		t.Fatalf("Refresh() commands = %v, want the leader to still have been added", cmds)
	}
	if enableIdx < 0 {
		t.Fatalf("Refresh() commands = %v, want the (failing) enable attempt to still have been issued", cmds)
	}

	maintIdx := cmdIndex(cmds, "set server service_write/leader state maint")
	delIdx := cmdIndex(cmds, "del server service_write/leader")
	if maintIdx < 0 {
		t.Errorf("Refresh() commands = %v, want a cleanup \"set server service_write/leader state maint\" after the failed enable", cmds)
	}
	if delIdx < 0 {
		t.Errorf("Refresh() commands = %v, want a cleanup \"del server service_write/leader\" after the failed enable", cmds)
	}
	if delIdx >= 0 && delIdx <= enableIdx {
		t.Errorf("Refresh() commands = %v, want the cleanup delete (index %d) after the failed enable (index %d)", cmds, delIdx, enableIdx)
	}
}

// TestHaproxySelfHealWriteLeaderStaleAddressFixedByExistingMasterCheck
// reproduces a "leader" row that reports a healthy status ("UP") but at a
// stale address -- here, the previous master's address, left behind by a
// failover that never got a chance to update it live. writeLeaderRowHealthy
// alone can't catch this (the row genuinely is "UP"), so sawWriteLeader is
// true and selfHealDynamicBackends' own write-leader branch (gated on
// !sawWriteLeader) does nothing here, unlike the read side's
// GetServerFromName+literal-IP cross-check.
//
// That's fine: this address gets corrected by a separate, pre-existing
// mechanism that runs on every Refresh() pass regardless of
// HaproxyRuntimeDynamicBackends -- the same "show stat" parse loop looks up
// the row's *current* address via GetServerFromURL, and (finding it belongs
// to a known server that isn't the master) calls SetMaster, which issues a
// live "set server .../leader addr ... port ..." -- a command that, unlike
// AddServer, can safely update an existing row's address without a cutover.
// This test pins that behavior down so a future change can't silently drop
// it without a self-heal test noticing.
func TestHaproxySelfHealWriteLeaderStaleAddressFixedByExistingMasterCheck(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		// Stale: "leader" is healthy but still points at slave1's address
		// (127.0.0.1:3307), not the current master's (127.0.0.1:3306).
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3307"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "DRAIN", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	wantFix := "set server service_write/leader addr 127.0.0.1 port 3306"
	if cmdIndex(cmds, wantFix) < 0 {
		t.Fatalf("Refresh() commands = %v, want %q (the stale leader address fixed via SetMaster)", cmds, wantFix)
	}
	for _, cmd := range cmds {
		if strings.HasPrefix(cmd, "add server service_write/leader") {
			t.Errorf("Refresh() commands = %v, did not want self-heal to attempt adding \"leader\" while its row is already present and reports healthy (found %q)", cmds, cmd)
		}
	}
}

// startFakeHaproxyRejectingServerCreation behaves like startFakeHaproxy but
// simulates HAProxy rejecting "add server" with plain CLI text: the
// transport succeeds (err == nil) and a response is written, but the
// server is never actually registered, so a subsequent "show stat" won't
// list it. The add-server counterpart to
// startFakeHaproxyRejectingBackendCreation, confirming addDynamicServer
// verifies the row exists via serverStatusAtRuntime rather than trusting a
// nil err from AddServer alone.
func startFakeHaproxyRejectingServerCreation(t *testing.T, statResponse string) (host, port string, commands func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake haproxy server: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	host, port, _ = net.SplitHostPort(ln.Addr().String())

	var mu sync.Mutex
	var cmds []string
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
				cmds = append(cmds, cmd)
				mu.Unlock()
				switch {
				case strings.HasPrefix(cmd, "show version"):
					c.Write([]byte(defaultFakeHaproxyVersion + "\n"))
				case cmd == "show stat":
					c.Write([]byte(statResponse))
				case strings.HasPrefix(cmd, "add server "):
					c.Write([]byte("Backend does not use a dynamic load-balancing algorithm.\n"))
				}
			}(conn)
		}
	}()

	return host, port, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(cmds))
		copy(out, cmds)
		return out
	}
}

// TestHaproxySelfHealVerifiesServerAdditionSucceeded reproduces "add
// server" being rejected by HAProxy with plain CLI text: the transport
// succeeds (err == nil), but the server is never actually registered, so a
// subsequent "show stat" doesn't list it. addDynamicServer must not treat
// this as success -- no "enable server"/"enable health" follow-up for a
// server that was never actually added.
func TestHaproxySelfHealVerifiesServerAdditionSucceeded(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		// slave1 row intentionally omitted.
	}, "\n")

	host, port, commands := startFakeHaproxyRejectingServerCreation(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	if cmdIndex(cmds, "add server service_read/slave1 127.0.0.1:3307 check inter 1000 weight 100 maxconn 2000") < 0 {
		t.Fatalf("Refresh() commands = %v, want the (rejected) add-server attempt to still have been issued", cmds)
	}
	for _, cmd := range cmds {
		if strings.HasPrefix(cmd, "enable server service_read/slave1") || strings.HasPrefix(cmd, "enable health service_read/slave1") {
			t.Errorf("Refresh() commands = %v, did not want enable calls for a server show stat confirms was never actually added (found %q)", cmds, cmd)
		}
	}
}

// startFakeHaproxyRejectingHealthEnable behaves like startFakeHaproxy (same
// dynamic add/enable-server tracking via fakeHaproxyState) but "enable
// health" is accepted at the transport level (err == nil) without ever
// taking effect -- the same CLI-text-rejection shape as
// startFakeHaproxyRejectingServerCreation, targeted at the specific
// "EnableServer succeeded, EnableHealth didn't" gap: the server is left at
// "no check" indefinitely, out of maintenance but with no active health
// verification ever confirming it's actually reachable.
func startFakeHaproxyRejectingHealthEnable(t *testing.T, statResponse string) (host, port string, commands func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake haproxy server: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	host, port, _ = net.SplitHostPort(ln.Addr().String())

	var mu sync.Mutex
	var cmds []string
	state := newFakeHaproxyState(statResponse, defaultFakeHaproxyVersion)
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
				cmds = append(cmds, cmd)
				mu.Unlock()

				if strings.HasPrefix(cmd, "enable health ") {
					// Accepted at the transport level but never actually
					// applied -- state.respond is deliberately not called,
					// so dynServers never marks this UP.
					c.Write([]byte("Health checks already disabled.\n"))
					return
				}
				mu.Lock()
				resp := state.respond(cmd)
				mu.Unlock()
				if resp != nil {
					c.Write(resp)
				}
			}(conn)
		}
	}()

	return host, port, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(cmds))
		copy(out, cmds)
		return out
	}
}

// TestHaproxySelfHealDetectsRejectedEnableHealth reproduces EnableServer
// succeeding (the server genuinely leaves maintenance) while EnableHealth
// is rejected by HAProxy with plain CLI text (err == nil). The server is
// left at "no check" -- out of maintenance, but with no active health
// check ever confirming it's actually reachable. addDynamicServer must not
// treat this as success: checking only for "" (row gone) and "MAINT"
// (still disabled) would miss it, since "no check" is neither.
func TestHaproxySelfHealDetectsRejectedEnableHealth(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		// master1 and slave1 rows intentionally omitted.
	}, "\n")

	host, port, commands := startFakeHaproxyRejectingHealthEnable(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	if cmdIndex(cmds, "enable health service_read/slave1") < 0 {
		t.Fatalf("Refresh() commands = %v, want the (rejected) enable-health attempt to still have been issued", cmds)
	}
	// A server left at "no check" must not be counted as an available
	// reader: the no-slave master fallback must still fire, exactly as it
	// would if slave1 had never been touched at all.
	wantMasterAdd := "add server service_read/master1 127.0.0.1:3306 check inter 1000 weight 100 maxconn 2000"
	if cmdIndex(cmds, wantMasterAdd) < 0 {
		t.Errorf("Refresh() commands = %v, want %q: slave1's health check was never actually enabled, so it must not count as an available reader and the no-slave fallback must still add the master", cmds, wantMasterAdd)
	}
}

// TestHaproxySelfHealSkipsStandbyMode reproduces a "standby" HaproxyMode
// deployment on HAProxy 3.4+. Standby mode names servers "server1",
// "server2", ... by loop index (see GetConfigProxyModule), not runtimeapi's
// "leader"/Id-based convention this whole feature assumes. Self-heal must
// not run at all in standby mode -- acting on it would misread every
// existing "server1"/"server2" row as missing (since none match "leader"
// or match a cluster server's Id) and either add duplicate rows under the
// wrong names or prune the real ones as "stale". Init()'s own
// HaproxyMode == "standby" branch already reconciles standby's config by
// fully re-rendering and reloading it, not by patching runtime state
// incrementally, so self-heal has nothing useful to do here anyway.
func TestHaproxySelfHealSkipsStandbyMode(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf.HaproxyMode = "standby"

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "server1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "server1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "server2", "UP", "127.0.0.1:3307"),
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	for _, cmd := range commands() {
		if strings.HasPrefix(cmd, "add backend") || strings.HasPrefix(cmd, "publish backend") ||
			strings.HasPrefix(cmd, "add server") || strings.HasPrefix(cmd, "del server") {
			t.Errorf("Refresh() commands = %v, did not want any dynamic-backend self-heal command in standby mode (found %q)", commands(), cmd)
		}
		// Refresh() checks HaproxyMode/staging itself before ever calling
		// hasDynamicBackendSupport, so standby mode -- which selfHealDynamicBackends
		// would just no-op on anyway -- shouldn't pay for the "show version"
		// round trip every pass either.
		if strings.HasPrefix(cmd, "show version") {
			t.Errorf("Refresh() commands = %v, did not want a \"show version\" probe in standby mode (found %q)", commands(), cmd)
		}
	}
}

// startFakeHaproxyReportingDownAfterEnable behaves like startFakeHaproxy
// (same add/enable-server tracking via fakeHaproxyState) but EnableHealth
// transitions the server's status to a "DOWN ..."-style string instead of
// "UP" -- reproducing a health check resolving (or HAProxy's usual
// optimistic default for an unconfirmed server not applying) before
// addDynamicServer's post-enable verification runs. Confirms that
// verification is an allowlist ("status starts with UP"), not a denylist
// of only "", "MAINT", "no check".
func startFakeHaproxyReportingDownAfterEnable(t *testing.T, statResponse string) (host, port string, commands func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake haproxy server: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	host, port, _ = net.SplitHostPort(ln.Addr().String())

	var mu sync.Mutex
	var cmds []string
	state := newFakeHaproxyState(statResponse, defaultFakeHaproxyVersion)
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
				cmds = append(cmds, cmd)
				mu.Unlock()

				if strings.HasPrefix(cmd, "enable health ") {
					mu.Lock()
					if pool, name := poolNameFromServerCmd(cmd); state.rowExists(pool, name) {
						state.setDynServerStatus(pool, name, "DOWN 1/3")
					}
					mu.Unlock()
					c.Write([]byte("\n"))
					return
				}
				mu.Lock()
				resp := state.respond(cmd)
				mu.Unlock()
				if resp != nil {
					c.Write(resp)
				}
			}(conn)
		}
	}()

	return host, port, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(cmds))
		copy(out, cmds)
		return out
	}
}

// TestHaproxySelfHealRejectsDownStatusAfterEnable reproduces a newly
// enabled server whose status reads "DOWN ..." right after EnableHealth --
// neither "", "MAINT", nor "no check", the case a denylist of only those
// three strings would miss. addDynamicServer's verification must still
// treat this as not-yet-usable: the no-slave master fallback must still
// fire.
func TestHaproxySelfHealRejectsDownStatusAfterEnable(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)
	cluster.Configurator.ClusterConfig.PRXServersReadOnMasterNoSlave = true

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		// master1 and slave1 rows intentionally omitted.
	}, "\n")

	host, port, commands := startFakeHaproxyReportingDownAfterEnable(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	if cmdIndex(cmds, "enable health service_read/slave1") < 0 {
		t.Fatalf("Refresh() commands = %v, want the enable-health attempt to still have been issued", cmds)
	}
	wantMasterAdd := "add server service_read/master1 127.0.0.1:3306 check inter 1000 weight 100 maxconn 2000"
	if cmdIndex(cmds, wantMasterAdd) < 0 {
		t.Errorf("Refresh() commands = %v, want %q: slave1 reported DOWN after enable, so it must not count as an available reader and the no-slave fallback must still add the master", cmds, wantMasterAdd)
	}
}

// startFakeHaproxyRejectingBackendPublish behaves like startFakeHaproxy
// (same "add backend" tracking via fakeHaproxyState, so the backend shows
// up in "show stat" as "UP (UNPUB)") but "publish backend" is accepted at
// the transport level (err == nil) without ever taking effect -- the
// backend stays unpublished. Confirms addDynamicBackend's verification
// checks published state via "show stat", not just existence via
// "show backend" (which lists an unpublished backend too).
func startFakeHaproxyRejectingBackendPublish(t *testing.T, statResponse string) (host, port string, commands func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake haproxy server: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	host, port, _ = net.SplitHostPort(ln.Addr().String())

	var mu sync.Mutex
	var cmds []string
	state := newFakeHaproxyState(statResponse, defaultFakeHaproxyVersion)
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
				cmds = append(cmds, cmd)
				mu.Unlock()

				if strings.HasPrefix(cmd, "publish backend ") {
					// Accepted at the transport level but never actually
					// applied -- state.respond is deliberately not called,
					// so dynBackends never flips to published.
					c.Write([]byte("Backend already published or otherwise unavailable.\n"))
					return
				}
				mu.Lock()
				resp := state.respond(cmd)
				mu.Unlock()
				if resp != nil {
					c.Write(resp)
				}
			}(conn)
		}
	}()

	return host, port, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(cmds))
		copy(out, cmds)
		return out
	}
}

// TestHaproxySelfHealDetectsUnpublishedBackend reproduces "add backend"
// succeeding (the backend genuinely exists, and its "show stat" BACKEND
// row confirms it, status "UP (UNPUB)") while "publish backend" is
// rejected by HAProxy with plain CLI text (err == nil). "show backend"
// alone can't distinguish this -- an unpublished backend is still listed
// there -- so addDynamicBackend must not go on to add the leader server
// into a backend frontends still won't route to.
func TestHaproxySelfHealDetectsUnpublishedBackend(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	statResponse := strings.Join([]string{
		// service_write has no rows at all.
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
	}, "\n")

	host, port, commands := startFakeHaproxyRejectingBackendPublish(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	if cmdIndex(cmds, "experimental-mode on; add backend service_write from dyn_defaults mode tcp") < 0 {
		t.Fatalf("Refresh() commands = %v, want the write backend add attempt to still have been issued", cmds)
	}
	if cmdIndex(cmds, "publish backend service_write") < 0 {
		t.Fatalf("Refresh() commands = %v, want the (rejected) publish attempt to still have been issued", cmds)
	}
	for _, cmd := range cmds {
		if strings.HasPrefix(cmd, "add server service_write/leader") {
			t.Errorf("Refresh() commands = %v, did not want a leader add against a backend that's still unpublished (found %q)", cmds, cmd)
		}
	}
}

// TestHaproxySelfHealRetriesUnpublishedBackendOnLaterPass reproduces the gap
// TestHaproxySelfHealDetectsUnpublishedBackend leaves uncovered: a backend
// that was left unpublished by some *earlier* Refresh() pass (e.g. a
// "publish backend" that raced a HAProxy reload, or was rejected on a pass
// this test doesn't itself simulate), so the very first "show stat" this
// pass parses already contains the "UP (UNPUB)" BACKEND row -- unlike the
// sibling test, which reproduces the rejection happening during this same
// pass's own add+publish attempt. Without checking the row's own status,
// sawWriteBackend would see the row and consider the write backend "already
// there," permanently silencing any further "publish backend" retry while
// still going on to add a leader server into a backend nothing can ever
// route to. Refresh() must instead treat an unpublished BACKEND row the
// same as a missing one and keep retrying publication.
func TestHaproxySelfHealRetriesUnpublishedBackendOnLaterPass(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP (UNPUB)", ""),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	if cmdIndex(cmds, "publish backend service_write") < 0 {
		t.Fatalf("Refresh() commands = %v, want a retried \"publish backend\" for a BACKEND row already present but still unpublished from an earlier pass", cmds)
	}
	for _, cmd := range cmds {
		if strings.HasPrefix(cmd, "add server service_write/leader") {
			t.Errorf("Refresh() commands = %v, did not want a leader add against a backend the fake never actually published (found %q)", cmds, cmd)
		}
	}
}

// TestHaproxySelfHealRetriesUnhealthyReadServer reproduces a read server row
// left stuck at "no check" by an earlier pass's partial heal (AddServer and
// EnableServer succeeded, EnableHealth didn't -- see
// TestHaproxySelfHealDetectsRejectedEnableHealth for that same-pass
// detection). Once that row exists, readSvnamesSeen alone can't tell "fixed"
// apart from "still broken": Refresh()'s own SetDrain/SetReady/
// SetMaintenance reconciliation only reacts to "UP"/"DRAIN"/"MAINT", none of
// which "no check" is, so nothing else in this codebase would ever recover
// it. Self-heal must keep retrying such a row on every later pass instead of
// treating its mere presence as done.
func TestHaproxySelfHealRetriesUnhealthyReadServer(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "no check", "127.0.0.1:3307"),
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	wantRetry := []string{
		"enable server service_read/slave1",
		"enable health service_read/slave1",
	}
	for _, w := range wantRetry {
		if cmdIndex(cmds, w) < 0 {
			t.Errorf("Refresh() commands = %v, want a retried %q for a read server row stuck at \"no check\" from an earlier pass's partial heal", cmds, w)
		}
	}
}

// TestHaproxySelfHealSyntheticReadRowCarriesMetricFields reproduces the same
// missing-read-server gap as TestHaproxySelfHealMissingReadServer, but
// checks the synthetic Backend selfHealDynamicBackends appends to
// proxy.BackendsRead once the add succeeds, not just the runtime API
// commands issued. FetchStats() (cluster/prx.go) reads PrxName/PrxByteOut/
// PrxConnections/PrxLatency off every proxy.BackendsRead entry unconditionally,
// on this same pass (refreshProxies calls it right after Refresh() returns)
// -- so a synthetic entry left at its Go zero value for those fields would
// emit a Graphite metric under a blank server identity ("ro-" with nothing
// after it) and blank-valued, malformed metric lines for the very reader
// this pass just healed.
func TestHaproxySelfHealSyntheticReadRowCarriesMetricFields(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		// slave1 row intentionally omitted -- never provisioned into HAProxy.
	}, "\n")

	host, port, _ := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	var synthetic *Backend
	for i := range proxy.BackendsRead {
		if proxy.BackendsRead[i].Svname == "slave1" {
			synthetic = &proxy.BackendsRead[i]
		}
	}
	if synthetic == nil {
		t.Fatalf("proxy.BackendsRead = %+v, want a synthetic entry for the just-healed slave1", proxy.BackendsRead)
	}
	if synthetic.PrxName == "" {
		t.Error("synthetic BackendsRead entry has empty PrxName, would emit a Graphite metric under a blank server identity")
	}
	for name, got := range map[string]string{
		"PrxByteOut":     synthetic.PrxByteOut,
		"PrxConnections": synthetic.PrxConnections,
		"PrxLatency":     synthetic.PrxLatency,
	} {
		if got == "" {
			t.Errorf("synthetic BackendsRead entry has empty %s, would emit a blank-valued, malformed Graphite metric line", name)
		}
	}
}

// startFakeHaproxyRejectingLeaderCleanupDelete behaves like startFakeHaproxy
// (same fakeHaproxyState-backed add/enable-server tracking) but two
// commands are accepted at the transport level (err == nil) without ever
// taking effect: "enable health service_write/leader" (so addDynamicServer
// fails its post-enable check, the row stuck at "no check", and
// cleanupFailedDynamicServer runs) and cleanupFailedDynamicServer's own
// "del server service_write/leader" (so its own cleanup attempt fails too,
// leaving the row behind -- SetMaintenance still succeeds, so it's left at
// "MAINT" -- exactly the scenario cleanupFailedDynamicServer's "will keep
// retrying" log names but nothing before this test actually forced).
func startFakeHaproxyRejectingLeaderCleanupDelete(t *testing.T, statResponse string) (host, port string, commands func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake haproxy server: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	host, port, _ = net.SplitHostPort(ln.Addr().String())

	var mu sync.Mutex
	var cmds []string
	state := newFakeHaproxyState(statResponse, defaultFakeHaproxyVersion)
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
				cmds = append(cmds, cmd)
				mu.Unlock()

				switch cmd {
				case "enable health service_write/leader":
					c.Write([]byte("Health checks already disabled.\n"))
					return
				case "del server service_write/leader":
					c.Write([]byte("The server still has connections attached to it.\n"))
					return
				}
				mu.Lock()
				resp := state.respond(cmd)
				mu.Unlock()
				if resp != nil {
					c.Write(resp)
				}
			}(conn)
		}
	}()

	return host, port, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(cmds))
		copy(out, cmds)
		return out
	}
}

// TestHaproxySelfHealRetriesWriteLeaderAfterCleanupDeleteFails reproduces
// cleanupFailedDynamicServer itself failing to actually remove a
// partially-healed "leader" row (its own "del server" rejected with plain
// CLI text, err == nil): SetMaintenance still succeeds first, so the row is
// left behind at "MAINT", not gone. Without requiring the row's status to
// be "UP" (see writeLeaderRowHealthy), sawWriteLeader would see this
// leftover MAINT row on the next pass and consider the leader "already
// handled," permanently silencing any further retry -- writes would stay
// blackholed forever after this one transient double-failure, contradicting
// cleanupFailedDynamicServer's own "will keep retrying" log line.
func TestHaproxySelfHealRetriesWriteLeaderAfterCleanupDeleteFails(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	statResponse := strings.Join([]string{
		// service_write has no rows at all -- leader missing, backend itself
		// gets recreated by this same pass via the shared fakeHaproxyState.
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
	}, "\n")

	host, port, commands := startFakeHaproxyRejectingLeaderCleanupDelete(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	// Pass 1: add succeeds, enable health is rejected (row stuck at "no
	// check"), cleanup's own maintenance+delete leaves it at "MAINT".
	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() pass 1 error = %v", err)
	}
	wantAdd := "add server service_write/leader 127.0.0.1:3306 check inter 1000 weight 100 maxconn 2000"
	if cmdIndex(commands(), wantAdd) < 0 {
		t.Fatalf("pass 1 commands = %v, want the leader add attempt", commands())
	}
	if cmdIndex(commands(), "del server service_write/leader") < 0 {
		t.Fatalf("pass 1 commands = %v, want cleanup's (rejected) delete attempt", commands())
	}

	// Pass 2: the leftover MAINT row is still present. Without the fix,
	// sawWriteLeader would see it and never retry.
	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() pass 2 error = %v", err)
	}

	addCount := 0
	for _, c := range commands() {
		if c == wantAdd {
			addCount++
		}
	}
	if addCount < 2 {
		t.Errorf("commands across both passes = %v, want a second %q attempt on pass 2 after cleanup's delete failed to actually remove the stuck row", commands(), wantAdd)
	}
}

// TestHaproxySelfHealRetryUpdatesExistingReadRowNotDuplicate reproduces a
// read server row that already exists but is unhealthy ("no check", left
// behind by an earlier pass's partial heal): the main "show stat" parse
// loop above already appended a proxy.BackendsRead entry for it with that
// stale status before self-heal runs at all. When this pass's retry
// succeeds, self-heal must update that existing entry in place, not append
// a second one -- a duplicate would make FetchStats() (cluster/prx.go) emit
// two sets of Graphite metrics for the same reader this same pass, and
// anything else reading proxy.BackendsRead (the status API, the dashboard)
// would show the server twice.
func TestHaproxySelfHealRetryUpdatesExistingReadRowNotDuplicate(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "no check", "127.0.0.1:3307"),
	}, "\n")

	host, port, _ := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	count := 0
	var status string
	for _, b := range proxy.BackendsRead {
		if b.Svname == "slave1" {
			count++
			status = b.PrxStatus
		}
	}
	if count != 1 {
		t.Fatalf("proxy.BackendsRead has %d entries for slave1 (%+v), want exactly 1 -- a retried heal must update the existing row, not append a duplicate", count, proxy.BackendsRead)
	}
	if status != "UP" {
		t.Errorf("slave1's BackendsRead entry has PrxStatus %q, want \"UP\" reflecting the successful retry", status)
	}
}

// TestHaproxySelfHealDetectsStaleReadServerAddress reproduces a row that
// already exists under the right svname but the wrong address: "add server"
// is rejected, address untouched, but EnableServer/EnableHealth still apply
// to it (both confirmed live), reaching "UP" while still pointing at the
// wrong endpoint. addDynamicServer's status check alone would call this a
// successful heal; it must also confirm the address matches.
func TestHaproxySelfHealDetectsStaleReadServerAddress(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		// slave1's row exists under the right svname but at a stale
		// address -- the cluster's real slave1 is 127.0.0.1:3307.
		haproxyStatRow("service_read", "slave1", "no check", "10.0.0.9:9999"),
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	if cmdIndex(cmds, "add server service_read/slave1 127.0.0.1:3307 check inter 1000 weight 100 maxconn 2000") < 0 {
		t.Fatalf("Refresh() commands = %v, want the (rejected) add-server retry to still have been issued", cmds)
	}
	if cmdIndex(cmds, "enable health service_read/slave1") < 0 {
		t.Fatalf("Refresh() commands = %v, want the enable-health attempt to still have been issued against the stale row", cmds)
	}

	for _, b := range proxy.BackendsRead {
		if b.Svname == "slave1" && b.PrxStatus == "UP" && b.Host == "127.0.0.1" && b.Port == "3307" {
			t.Errorf("proxy.BackendsRead = %+v, did not want slave1 reported healed at the correct address -- HAProxy's runtime row is still pointing at the stale 10.0.0.9:9999, and \"add server\" cannot correct it", proxy.BackendsRead)
		}
	}
}

// TestHaproxySelfHealMasterReadRowVisibleSamePass extends
// TestHaproxySelfHealReAddsReaderMasterRow by checking proxy.BackendsRead
// after Refresh(), not just the commands issued: the master's own read row
// must get the same immediate upsertHealedReadRow treatment as an ordinary
// replica, or repman's own view (metrics, API, dashboard) lags a cycle
// behind HAProxy's already-correct routing.
func TestHaproxySelfHealMasterReadRowVisibleSamePass(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)
	cluster.Configurator.ClusterConfig.PRXServersReadOnMaster = true

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
		// master1 row intentionally omitted from service_read.
	}, "\n")

	host, port, _ := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	for _, b := range proxy.BackendsRead {
		if b.Svname == "master1" {
			if b.PrxStatus != "UP" {
				t.Errorf("master1's same-pass BackendsRead entry has PrxStatus %q, want \"UP\"", b.PrxStatus)
			}
			return
		}
	}
	t.Errorf("proxy.BackendsRead = %+v, want a same-pass entry for the just-healed master1 read row", proxy.BackendsRead)
}

// TestHaproxySelfHealServerExistsAtRuntimeParsesShowServersState directly
// proves serverExistsAtRuntime's "show servers state" CSV parsing -- be_name
// at column 2, srv_name at column 4 -- genuinely distinguishes a server the
// response lists from one it doesn't, against the shared fake's own
// showServersStateResponse rendering. Every other self-heal test only
// checks that "del server" and "show servers state" were issued in the
// right order, never that this parsing itself tells "still there" apart
// from "gone".
func TestHaproxySelfHealServerExistsAtRuntimeParsesShowServersState(t *testing.T) {
	statResponse := strings.Join([]string{
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "oldnode1", "UP", "127.0.0.1:3308"),
	}, "\n")
	host, port, _ := startFakeHaproxy(t, statResponse)
	haRuntime := &haproxy.Runtime{Host: host, Port: port}
	proxy := &HaproxyProxy{}

	if !proxy.serverExistsAtRuntime(haRuntime, "service_read", "oldnode1") {
		t.Error("serverExistsAtRuntime() = false, want true for a server \"show servers state\" genuinely lists")
	}
	if proxy.serverExistsAtRuntime(haRuntime, "service_read", "nonexistent") {
		t.Error("serverExistsAtRuntime() = true, want false for a server \"show servers state\" does not list")
	}
}

// startFakeHaproxyRejectingServerDeletion behaves like startFakeHaproxy
// (same "show servers state" rendering via fakeHaproxyState, so a rejected
// deletion still shows up there) but "del server service_read/oldnode1" is
// accepted at the transport level (err == nil) without ever taking effect.
func startFakeHaproxyRejectingServerDeletion(t *testing.T, statResponse string) (host, port string, commands func() []string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake haproxy server: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	host, port, _ = net.SplitHostPort(ln.Addr().String())

	var mu sync.Mutex
	var cmds []string
	state := newFakeHaproxyState(statResponse, defaultFakeHaproxyVersion)
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
				cmds = append(cmds, cmd)
				mu.Unlock()

				if cmd == "del server service_read/oldnode1" {
					c.Write([]byte("The server still has connections attached to it.\n"))
					return
				}
				mu.Lock()
				resp := state.respond(cmd)
				mu.Unlock()
				if resp != nil {
					c.Write(resp)
				}
			}(conn)
		}
	}()

	return host, port, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(cmds))
		copy(out, cmds)
		return out
	}
}

// TestHaproxySelfHealRetriesRejectedServerDeletion reproduces the prune
// loop's "del server" being rejected with plain CLI text (err == nil) --
// oldnode1's row is never actually removed, and must be retried next pass
// rather than treated as deleted just because err was nil.
func TestHaproxySelfHealRetriesRejectedServerDeletion(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "slave1", "UP", "127.0.0.1:3307"),
		haproxyStatRow("service_read", "oldnode1", "UP", "127.0.0.1:3308"),
	}, "\n")

	host, port, commands := startFakeHaproxyRejectingServerDeletion(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() pass 1 error = %v", err)
	}
	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() pass 2 error = %v", err)
	}

	delCount := 0
	for _, c := range commands() {
		if c == "del server service_read/oldnode1" {
			delCount++
		}
	}
	if delCount < 2 {
		t.Errorf("commands across both passes = %v, want a second \"del server service_read/oldnode1\" attempt on pass 2 after pass 1's was rejected", commands())
	}
}

// TestHaproxySelfHealDetectsHealthyStaleReadServerAddress covers what
// TestHaproxySelfHealDetectsStaleReadServerAddress didn't: a row at the
// right svname but wrong address, with a status readServerRowHealthy
// already treats as healthy (UP/DRAIN/MAINT). Without also comparing the
// address against the cluster server looked up by Id, readSvnamesUnhealthy
// would never get set, and the row would look "already handled" forever.
func TestHaproxySelfHealDetectsHealthyStaleReadServerAddress(t *testing.T) {
	for _, status := range []string{"UP", "DRAIN", "MAINT"} {
		t.Run(status, func(t *testing.T) {
			cluster, _, _ := newSelfHealTestCluster(t)
			defer cleanupTestCluster(t, cluster)

			statResponse := strings.Join([]string{
				haproxyStatRow("service_write", "BACKEND", "UP", ""),
				haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
				haproxyStatRow("service_read", "BACKEND", "UP", ""),
				haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
				// slave1's row is healthy (status) but at a stale address --
				// the cluster's real slave1 is 127.0.0.1:3307.
				haproxyStatRow("service_read", "slave1", status, "10.0.0.9:9999"),
			}, "\n")

			host, port, commands := startFakeHaproxy(t, statResponse)

			proxy := &HaproxyProxy{Proxy: Proxy{
				ClusterGroup: cluster,
				Host:         host,
				Port:         port,
				Datadir:      t.TempDir(),
				Version:      "HAProxy version 3.4.0-test",
			}}

			if err := proxy.Refresh(); err != nil {
				t.Fatalf("Refresh() error = %v", err)
			}

			cmds := commands()
			if cmdIndex(cmds, "add server service_read/slave1 127.0.0.1:3307 check inter 1000 weight 100 maxconn 2000") < 0 {
				t.Fatalf("Refresh() commands = %v, want a retry attempt against the stale row despite its healthy %q status", cmds, status)
			}

			for _, b := range proxy.BackendsRead {
				if b.Svname == "slave1" && b.Host == "127.0.0.1" && b.Port == "3307" {
					t.Errorf("proxy.BackendsRead = %+v, did not want slave1 reported healed at the correct address -- HAProxy's runtime row is still pointing at the stale 10.0.0.9:9999", proxy.BackendsRead)
				}
			}
		})
	}
}

// TestHaproxySelfHealAddDynamicServerAcceptsIPv6 reproduces an IPv6-backed
// cluster server: the address must be bracketed both in the "add server"
// command and in the post-add comparison against HAProxy's own reported
// address ("[::1]:3307", confirmed live) -- a bare "host+\":\"+port"
// comparison would never match, wrongly rejecting every IPv6 server as a
// stale-address mismatch.
func TestHaproxySelfHealAddDynamicServerAcceptsIPv6(t *testing.T) {
	cluster, _, _ := newSelfHealTestCluster(t)
	defer cleanupTestCluster(t, cluster)
	cluster.Servers[1].Host = "::1"

	statResponse := strings.Join([]string{
		haproxyStatRow("service_write", "BACKEND", "UP", ""),
		haproxyStatRow("service_write", "leader", "UP", "127.0.0.1:3306"),
		haproxyStatRow("service_read", "BACKEND", "UP", ""),
		haproxyStatRow("service_read", "master1", "UP", "127.0.0.1:3306"),
		// slave1 row intentionally omitted -- never provisioned into HAProxy.
	}, "\n")

	host, port, commands := startFakeHaproxy(t, statResponse)

	proxy := &HaproxyProxy{Proxy: Proxy{
		ClusterGroup: cluster,
		Host:         host,
		Port:         port,
		Datadir:      t.TempDir(),
		Version:      "HAProxy version 3.4.0-test",
	}}

	if err := proxy.Refresh(); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	cmds := commands()
	wantAdd := "add server service_read/slave1 [::1]:3307 check inter 1000 weight 100 maxconn 2000"
	if cmdIndex(cmds, wantAdd) < 0 {
		t.Fatalf("Refresh() commands = %v, want the IPv6 address bracketed in the add-server command: %q", cmds, wantAdd)
	}
	wantEnable := []string{"enable server service_read/slave1", "enable health service_read/slave1"}
	for _, w := range wantEnable {
		if cmdIndex(cmds, w) < 0 {
			t.Errorf("Refresh() commands = %v, want %q -- the IPv6 server must be confirmed healed (not wrongly rejected as a stale-address mismatch), so want to contain %q", cmds, w, w)
		}
	}

	found := false
	for _, b := range proxy.BackendsRead {
		if b.Svname == "slave1" {
			found = true
			if b.PrxStatus != "UP" {
				t.Errorf("slave1's BackendsRead entry has PrxStatus %q, want \"UP\": the bracketed IPv6 address must be recognized as matching, not rejected as stale", b.PrxStatus)
			}
		}
	}
	if !found {
		t.Errorf("proxy.BackendsRead = %+v, want an entry for the newly-healed IPv6 slave1", proxy.BackendsRead)
	}
}
