package cluster

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/signal18/replication-manager/utils/state"
)

func newTestMaxscaleProxyProxy(cluster *Cluster) *MaxscaleProxy {
	prx := &MaxscaleProxy{}
	prx.ClusterGroup = cluster
	prx.Host = "maxscale1.example.com"
	prx.Port = "6603"
	prx.User = "admin"
	prx.Pass = "mariadb"
	return prx
}

func TestNewMaxscaleClient_UsesRestPortAndProtocolByDefault(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.MxsRestApi = true
	cluster.Conf.MxsRestPort = 8989
	prx := newTestMaxscaleProxyProxy(cluster)

	m := prx.newMaxscaleClient()
	if !m.UseRest {
		t.Fatalf("expected UseRest true by default")
	}
	if m.Port != "8989" {
		t.Fatalf("expected port 8989, got %s", m.Port)
	}
}

func TestNewMaxscaleClient_UsesMaxAdminPortWhenRestDisabled(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.MxsRestApi = false
	cluster.Conf.MxsRestPort = 8989
	prx := newTestMaxscaleProxyProxy(cluster)

	m := prx.newMaxscaleClient()
	if m.UseRest {
		t.Fatalf("expected UseRest false when maxscale-rest-api is disabled")
	}
	if m.Port != prx.Port {
		t.Fatalf("expected port %s (proxy.Port), got %s", prx.Port, m.Port)
	}
}

// TunnelPort is only ever wired to the MaxAdmin listener (nothing in the
// codebase re-points it at MxsRestPort), so a tunneled connection must
// always speak MaxAdmin regardless of maxscale-rest-api -- otherwise an
// existing tunneled setup would silently break under the maxscale-rest-api
// default.
func TestNewMaxscaleClient_TunnelAlwaysUsesMaxAdminRegardlessOfRestApi(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.MxsRestApi = true
	cluster.Conf.MxsRestPort = 8989
	prx := newTestMaxscaleProxyProxy(cluster)
	prx.Tunnel = true
	prx.TunnelPort = 16603

	m := prx.newMaxscaleClient()
	if m.UseRest {
		t.Fatalf("expected UseRest false on the tunnel path even with maxscale-rest-api=true")
	}
	if m.Host != "localhost" {
		t.Fatalf("expected tunnel host localhost, got %s", m.Host)
	}
	if m.Port != "16603" {
		t.Fatalf("expected tunnel port 16603, got %s", m.Port)
	}
}

// --- MaxscaleUsesPinloki mode resolution ---

func TestMaxscaleUsesPinloki_ExplicitLegacyIgnoresImageTag(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.MxsMode = "legacy"
	cluster.Conf.ProvProxMaxscaleImg = "mariadb/maxscale:23.08"
	if cluster.MaxscaleUsesPinloki() {
		t.Fatalf("expected explicit legacy mode to ignore a modern-looking tag")
	}
}

func TestMaxscaleUsesPinloki_ExplicitPinlokiIgnoresImageTag(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.MxsMode = "pinloki"
	cluster.Conf.ProvProxMaxscaleImg = "mariadb/maxscale:2.2"
	if !cluster.MaxscaleUsesPinloki() {
		t.Fatalf("expected explicit pinloki mode to ignore a legacy-looking tag")
	}
}

func TestMaxscaleUsesPinloki_AutoDetectsCalendarVersionedTagAsPinloki(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.MxsMode = "auto"
	for _, tag := range []string{"mariadb/maxscale:23.08", "mariadb/maxscale:22.08", "mariadb/maxscale:25.01", "mariadb/maxscale:6.4"} {
		cluster.Conf.ProvProxMaxscaleImg = tag
		if !cluster.MaxscaleUsesPinloki() {
			t.Fatalf("expected %q to auto-detect as pinloki (calendar-versioned majors are always >= 2.5 numerically)", tag)
		}
	}
}

func TestMaxscaleUsesPinloki_AutoDetectsOldSemverTagAsLegacy(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.MxsMode = "auto"
	for _, tag := range []string{"mariadb/maxscale:2.2", "mariadb/maxscale:2.4", "mariadb/maxscale:1.4"} {
		cluster.Conf.ProvProxMaxscaleImg = tag
		if cluster.MaxscaleUsesPinloki() {
			t.Fatalf("expected %q to auto-detect as legacy (pre-2.5)", tag)
		}
	}
}

func TestMaxscaleUsesPinloki_AutoFallsBackToLegacyWhenTagUnparseable(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.MxsMode = "auto"
	for _, tag := range []string{"mariadb/maxscale:latest", "mariadb/maxscale", "myregistry.example.com:5000/mariadb/maxscale:custom-build"} {
		cluster.Conf.ProvProxMaxscaleImg = tag
		if cluster.MaxscaleUsesPinloki() {
			t.Fatalf("expected unparseable tag %q to fall back to legacy (the safe, non-breaking default)", tag)
		}
	}
}

func TestMaxscaleUsesPinloki_UnsetModeDefaultsToAutoDetection(t *testing.T) {
	cluster := newTestCluster("k8stest")
	// MxsMode left as the struct zero value (""), matching a test cluster
	// that never went through AddFlags -- must behave like "auto", not
	// silently force one choice.
	cluster.Conf.ProvProxMaxscaleImg = "mariadb/maxscale:23.08"
	if !cluster.MaxscaleUsesPinloki() {
		t.Fatalf("expected unset MxsMode to auto-detect from the image tag")
	}
	cluster.Conf.ProvProxMaxscaleImg = "mariadb/maxscale:2.2"
	if cluster.MaxscaleUsesPinloki() {
		t.Fatalf("expected unset MxsMode to auto-detect the legacy tag too")
	}
}

func TestMaxscaleUsesPinloki_RegistryPortNotMistakenForTag(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.Conf.MxsMode = "auto"
	// myregistry.example.com:5000/mariadb/maxscale has no tag at all (bare
	// "latest" pull) -- the ":5000" must not be parsed as if it were one.
	cluster.Conf.ProvProxMaxscaleImg = "myregistry.example.com:5000/mariadb/maxscale"
	if cluster.MaxscaleUsesPinloki() {
		t.Fatalf("expected a registry port to never be mistaken for a version tag")
	}
}

// --- Init(): must not fight a running MaxScale monitor over server state ---

// newTestMaxscaleInitCluster wires a cluster+proxy pair pointed at a REST
// mock and a one-master-one-slave topology, the minimum Init() needs to run
// past its early-return guards.
func newTestMaxscaleInitCluster(t *testing.T, name string, srv *httptest.Server) (*Cluster, *MaxscaleProxy) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("could not split httptest server address: %s", err)
	}
	restPort, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("could not parse httptest server port: %s", err)
	}

	cluster := newTestCluster(name)
	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Conf.MxsOn = true
	cluster.Conf.MxsRestApi = true
	cluster.Conf.MxsRestPort = restPort
	cluster.Conf.MxsDisableMonitor = false

	prx := &MaxscaleProxy{}
	prx.ClusterGroup = cluster
	prx.Host = host
	prx.Port = "6603"
	prx.User = "admin"
	prx.Pass = "mariadb"

	master := &ServerMonitor{Id: "master", Host: "master", Port: "3306", URL: "master:3306", ClusterGroup: cluster, MxsServerName: "server1"}
	slave := &ServerMonitor{Id: "slave", Host: "slave", Port: "3306", URL: "slave:3306", ClusterGroup: cluster, MxsServerName: "server2", State: stateSlave}
	cluster.master = master
	cluster.Servers = []*ServerMonitor{master, slave}

	return cluster, prx
}

// Regression: Init() used to unconditionally push master/running/slave
// states via SetServer/ClearServer regardless of whether MaxScale's own
// monitor was actively watching these servers. Live-verified against a real
// MaxScale 2.4.10: the REST API refuses that with 403 ("The server is
// monitored, so only the maintenance status can be set/cleared manually"),
// and it also unconditionally fired ERR00017 ("Unable to fetch MaxScale
// monitoring information") even though a monitor WAS found -- just not
// disabled, which is the normal, healthy default. Confirms the fixed Init()
// makes no manual server-state PUT calls when a monitor is running and
// maxscale-disable-monitor is off.
func TestMaxscaleInit_MonitorRunningAndNotDisabled_SkipsManualServerPushes(t *testing.T) {
	var putCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/servers":
			w.Write([]byte(`{"data":[]}`))
		case r.Method == "GET" && r.URL.Path == "/v1/monitors":
			w.Write([]byte(`{"data":[{"id":"MySQL-Monitor","type":"monitors","attributes":{"state":"Running"}}]}`))
		case r.Method == "PUT":
			putCalls++
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"errors":[{"detail":"The server is monitored, so only the maintenance status can be set/cleared manually."}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cluster, prx := newTestMaxscaleInitCluster(t, "mxs-init-monitor-on", srv)
	prx.Init()

	if putCalls != 0 {
		t.Fatalf("expected no manual server-state PUT calls while a monitor is running and not disabled, got %d", putCalls)
	}
	if cluster.StateMachine.IsInState("ERR00017") {
		t.Fatalf("expected ERR00017 not to fire when a monitor was actually found (just left running)")
	}
}

// Preserves the pre-existing behavior for the genuine ERR00017 case: no
// monitor found at all means MaxScale has nothing else driving its routing,
// so repman's manual server-state pushes remain the only thing keeping it
// correct.
func TestMaxscaleInit_NoMonitorFound_StillDrivesServerStateManually(t *testing.T) {
	var putCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/servers":
			w.Write([]byte(`{"data":[]}`))
		case r.Method == "GET" && r.URL.Path == "/v1/monitors":
			w.Write([]byte(`{"data":[]}`))
		case r.Method == "PUT":
			putCalls++
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	_, prx := newTestMaxscaleInitCluster(t, "mxs-init-no-monitor", srv)
	prx.Init()

	if putCalls == 0 {
		t.Fatalf("expected repman to still drive server state manually when no MaxScale monitor is found")
	}
}

// --- MaxscaleUsesMaxinfo: the maxinfo HTTP plugin doesn't exist in pinloki-mode MaxScale ---

func TestMaxscaleUsesMaxinfo_FalseWhenMethodIsMaxadmin(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Conf.MxsGetInfoMethod = "maxadmin"
	cluster.Conf.MxsMode = "legacy"
	prx := newTestMaxscaleProxyProxy(cluster)

	if prx.MaxscaleUsesMaxinfo() {
		t.Fatalf("expected false when maxscale-get-info-method is not maxinfo")
	}
}

func TestMaxscaleUsesMaxinfo_TrueForLegacyWithMaxinfoRequested(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Conf.MxsGetInfoMethod = "maxinfo"
	cluster.Conf.MxsMode = "legacy"
	prx := newTestMaxscaleProxyProxy(cluster)

	if !prx.MaxscaleUsesMaxinfo() {
		t.Fatalf("expected true: legacy MaxScale still ships the maxinfo plugin")
	}
	if cluster.StateMachine.IsInState("WARN0211") {
		t.Fatalf("expected no WARN0211 when maxinfo is actually usable")
	}
}

// Regression: maxscale-get-info-method=maxinfo used to be honored
// unconditionally, regardless of MaxScale version. Pinloki (2.5+) dropped the
// maxinfo plugin along with cli/debugcli, so GetMaxInfoServers/
// GetMaxInfoMonitors would dial a port nothing listens on, every monitoring
// tick, with no indication why. Confirms the fallback fires and raises
// WARN0211 instead.
func TestMaxscaleUsesMaxinfo_FalseAndWarnsForPinlokiWithMaxinfoRequested(t *testing.T) {
	cluster := newTestCluster("k8stest")
	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.Conf.MxsGetInfoMethod = "maxinfo"
	cluster.Conf.MxsMode = "pinloki"
	prx := newTestMaxscaleProxyProxy(cluster)

	if prx.MaxscaleUsesMaxinfo() {
		t.Fatalf("expected false: pinloki MaxScale has no maxinfo plugin")
	}
	if !cluster.StateMachine.IsInState("WARN0211") {
		t.Fatalf("expected WARN0211 to be raised for the maxinfo+pinloki combination")
	}
}

// --- Refresh: BackendsWrite must reflect the actual write group, not every configured candidate ---

// Regression: Write-Connection-Router (readconnroute + router_options=master)
// lists every server as a configured candidate -- required so a post-failover
// master is already known to the router -- but only the server MaxScale
// currently reports as Master ever receives a write connection. Refresh()
// used to push every server into proxy.BackendsWrite unconditionally, which
// the dashboard renders as the proxy's "write group" -- live-verified this
// showed both slaves alongside the master under the WRITE group in the GUI,
// which is misleading (and was flagged as such: "why the write group of
// maxscale has all serves?"). Confirms Refresh() now filters BackendsWrite to
// only the server(s) MaxScale itself reports as Master.
func TestMaxscaleRefresh_BackendsWrite_OnlyIncludesReportedMaster(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/servers":
			w.Write([]byte(`{"data":[
				{"id":"server1","type":"servers","attributes":{"state":"Master, Running","parameters":{"address":"db1","port":3306},"statistics":{"connections":1}}},
				{"id":"server2","type":"servers","attributes":{"state":"Slave, Running","parameters":{"address":"db2","port":3306},"statistics":{"connections":0}}},
				{"id":"server3","type":"servers","attributes":{"state":"Slave, Running","parameters":{"address":"db3","port":3306},"statistics":{"connections":0}}}
			]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cluster, prx := newTestMaxscaleInitCluster(t, "mxs-refresh-write-group", srv)
	db1 := &ServerMonitor{Id: "db1", Host: "db1", Port: "3306", URL: "db1:3306", ClusterGroup: cluster, State: stateMaster}
	db2 := &ServerMonitor{Id: "db2", Host: "db2", Port: "3306", URL: "db2:3306", ClusterGroup: cluster, State: stateSlave}
	db3 := &ServerMonitor{Id: "db3", Host: "db3", Port: "3306", URL: "db3:3306", ClusterGroup: cluster, State: stateSlave}
	cluster.master = db1
	cluster.Servers = []*ServerMonitor{db1, db2, db3}

	if err := prx.Refresh(); err != nil {
		t.Fatalf("Refresh() returned an error: %s", err)
	}

	if len(prx.BackendsWrite) != 1 {
		t.Fatalf("expected exactly 1 backend in the write group (the reported Master), got %d: %+v", len(prx.BackendsWrite), prx.BackendsWrite)
	}
	if prx.BackendsWrite[0].Host != "db1" {
		t.Fatalf("expected the write group's only member to be the Master (db1), got %s", prx.BackendsWrite[0].Host)
	}
}

// Regression: Read-Write-Connection-Router (readwritesplit, master_accept_reads=1)
// distributes reads across every Running server, master included -- unlike the
// write group, there is no single "the reader". Confirms Refresh() populates
// BackendsRead with the full Running candidate pool rather than leaving it
// empty (as it did before this fix) or filtering it down like the write group.
func TestMaxscaleRefresh_BackendsRead_IncludesAllRunningServers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/servers":
			w.Write([]byte(`{"data":[
				{"id":"server1","type":"servers","attributes":{"state":"Master, Running","parameters":{"address":"db1","port":3306},"statistics":{"connections":1}}},
				{"id":"server2","type":"servers","attributes":{"state":"Slave, Running","parameters":{"address":"db2","port":3306},"statistics":{"connections":0}}},
				{"id":"server3","type":"servers","attributes":{"state":"Down","parameters":{"address":"db3","port":3306},"statistics":{"connections":0}}}
			]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cluster, prx := newTestMaxscaleInitCluster(t, "mxs-refresh-read-group", srv)
	db1 := &ServerMonitor{Id: "db1", Host: "db1", Port: "3306", URL: "db1:3306", ClusterGroup: cluster, State: stateMaster}
	db2 := &ServerMonitor{Id: "db2", Host: "db2", Port: "3306", URL: "db2:3306", ClusterGroup: cluster, State: stateSlave}
	db3 := &ServerMonitor{Id: "db3", Host: "db3", Port: "3306", URL: "db3:3306", ClusterGroup: cluster, State: stateFailed}
	cluster.master = db1
	cluster.Servers = []*ServerMonitor{db1, db2, db3}

	if err := prx.Refresh(); err != nil {
		t.Fatalf("Refresh() returned an error: %s", err)
	}

	if len(prx.BackendsRead) != 2 {
		t.Fatalf("expected 2 backends in the read group (both Running servers, master included), got %d: %+v", len(prx.BackendsRead), prx.BackendsRead)
	}
	gotHosts := map[string]bool{}
	for _, b := range prx.BackendsRead {
		gotHosts[b.Host] = true
	}
	if !gotHosts["db1"] || !gotHosts["db2"] {
		t.Fatalf("expected read group to include both db1 (Master, Running) and db2 (Slave, Running), got %+v", prx.BackendsRead)
	}
	if gotHosts["db3"] {
		t.Fatalf("expected read group to exclude db3 (Down), got %+v", prx.BackendsRead)
	}
}
