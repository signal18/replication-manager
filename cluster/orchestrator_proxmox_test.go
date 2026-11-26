// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

const fakeProxmoxToken = "repman@pve!rolling=11111111-2222-3333-4444-555555555555"

// fakeProxmox answers the four VM endpoints of design 10.4 (status/current, start,
// shutdown, reboot), the task status and /version, checks the token, and records the calls.
type fakeProxmox struct {
	mu    sync.Mutex
	calls []string
	state map[string]string // vmid -> running/stopped
}

func (f *fakeProxmox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "PVEAPIToken="+fakeProxmoxToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.calls = append(f.calls, r.Method+" "+strings.TrimPrefix(r.URL.Path, "/api2/json"))
	write := func(v interface{}) { json.NewEncoder(w).Encode(map[string]interface{}{"data": v}) }
	p := strings.TrimPrefix(r.URL.Path, "/api2/json")
	parts := strings.Split(p, "/") // "", nodes, pve1, qemu, 101, status, action
	switch {
	case p == "/version":
		write(map[string]string{"version": "8.2.4"})
	case strings.Contains(p, "/tasks/"):
		write(map[string]string{"status": "stopped", "exitstatus": "OK"})
	case len(parts) == 7 && parts[5] == "status" && parts[6] == "current":
		write(map[string]interface{}{"status": f.state[parts[4]], "qmpstatus": f.state[parts[4]], "uptime": 120})
	case len(parts) == 7 && parts[5] == "status" && r.Method == http.MethodPost:
		switch parts[6] {
		case "start", "reboot":
			f.state[parts[4]] = "running"
		case "shutdown":
			f.state[parts[4]] = "stopped"
		case "stop":
			w.WriteHeader(http.StatusForbidden) // the hard stop is never used
			return
		}
		write("UPID:" + parts[2] + ":00000001:" + parts[6] + ":" + parts[4] + ":repman@pve!rolling:")
	default:
		w.WriteHeader(http.StatusNotImplemented)
	}
}

func proxmoxTestCluster(t *testing.T, apiURL string) *Cluster {
	t.Helper()
	conf := &config.Config{
		ProvOrchestrator:        config.ConstOrchestratorProxmox,
		ProxmoxAPIURL:           apiURL,
		ProvDbHostVmids:         "db1=pve1/101, 10.0.0.2:3306=pve2/102",
		ProvDbHostRebootTimeout: 5,
		Secrets:                 config.NewSecrets(nil),
	}
	conf.Secrets.Set("proxmox-api-token", config.Secret{Value: fakeProxmoxToken})
	cl := &Cluster{Name: "pve", Conf: conf, errorChan: make(chan error)}
	cl.ConfigStateMachine = new(state.StateMachine)
	cl.ConfigStateMachine.Init()
	return cl
}

// The host facet against a fake Proxmox API: each server resolves its VM by name or
// host:port, the actions call the endpoints of its node and vmid, the shutdown is the clean
// one, a reboot of a stopped VM starts it, a server without a VM is an error naming it.
func TestProxmoxHostFacet(t *testing.T) {
	fake := &fakeProxmox{state: map[string]string{"101": "running", "102": "stopped"}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	cl := proxmoxTestCluster(t, srv.URL)

	orch := cl.Orchestrator()
	if orch.Type() != config.ConstOrchestratorProxmox || !orch.Traits().HasHost {
		t.Fatalf("registry built %s with traits %+v", orch.Type(), orch.Traits())
	}
	if err := orch.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	db1 := &ServerMonitor{Name: "db1", Host: "10.0.0.1", Port: "3306", URL: "10.0.0.1:3306", ClusterGroup: cl}
	db2 := &ServerMonitor{Name: "db2", Host: "10.0.0.2", Port: "3306", URL: "10.0.0.2:3306", ClusterGroup: cl}
	db3 := &ServerMonitor{Name: "db3", Host: "10.0.0.3", Port: "3306", URL: "10.0.0.3:3306", ClusterGroup: cl}

	st, err := orch.HostStatus(db1)
	if err != nil || st.Node != "pve1" || st.VMID != "101" || st.State != "running" || st.Uptime != 120*time.Second {
		t.Fatalf("HostStatus db1: %+v %v", st, err)
	}
	if err := orch.StopHost(db1); err != nil {
		t.Fatalf("StopHost: %v", err)
	}
	if err := orch.StartHost(db1); err != nil {
		t.Fatalf("StartHost: %v", err)
	}
	if err := orch.RebootHost(db1); err != nil {
		t.Fatalf("RebootHost: %v", err)
	}
	// db2's VM is stopped: a reboot starts it
	if err := orch.RebootHost(db2); err != nil {
		t.Fatalf("RebootHost db2: %v", err)
	}
	if _, err := orch.HostStatus(db3); err == nil || !strings.Contains(err.Error(), "10.0.0.3:3306") {
		t.Fatalf("a server without a VM must be an error naming it, got %v", err)
	}

	fake.mu.Lock()
	calls := strings.Join(fake.calls, "\n")
	fake.mu.Unlock()
	for _, want := range []string{
		"POST /nodes/pve1/qemu/101/status/shutdown",
		"POST /nodes/pve1/qemu/101/status/start",
		"POST /nodes/pve1/qemu/101/status/reboot",
		"POST /nodes/pve2/qemu/102/status/start",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("missing %s in\n%s", want, calls)
		}
	}
	if strings.Contains(calls, "/status/stop") || strings.Contains(calls, "pve2/qemu/102/status/reboot") {
		t.Errorf("hard stop or reboot of a stopped VM called:\n%s", calls)
	}

	// the other backends answer the sentinel on the host facet
	other := &Cluster{Name: "op", Conf: &config.Config{ProvOrchestrator: config.ConstOrchestratorOnPremise}}
	if err := other.Orchestrator().RebootHost(db1); !errors.Is(err, ErrOrchestratorNotSupported) {
		t.Fatalf("on-premise RebootHost: %v", err)
	}
	// a not-supported answer of the proxmox backend names proxmox, not the embedded adapter
	if err := orch.FreezeDatabaseService(db1); err == nil || !strings.Contains(err.Error(), "(proxmox)") {
		t.Fatalf("proxmox freeze: %v", err)
	}
}

// The services of a proxmox cluster are dispatched as on-premise ones: GetOrchestrator() is
// the service family, Conf.ProvOrchestrator the configured backend.
func TestProxmoxServiceDispatchIsOnPremise(t *testing.T) {
	cl := &Cluster{Name: "pve", Conf: &config.Config{ProvOrchestrator: config.ConstOrchestratorProxmox}}
	if got := cl.GetOrchestrator(); got != config.ConstOrchestratorOnPremise {
		t.Fatalf("GetOrchestrator() = %q, want onpremise", got)
	}
	for _, typ := range []string{config.ConstOrchestratorOpenSVC, config.ConstOrchestratorKubernetes, config.ConstOrchestratorOnPremise, config.ConstOrchestratorLocalhost, config.ConstOrchestratorSlapOS} {
		if got := serviceOrchestratorType(typ); got != typ {
			t.Fatalf("serviceOrchestratorType(%q) = %q", typ, got)
		}
	}
}

func TestParseHostVMIDs(t *testing.T) {
	vms, err := parseHostVMIDs(" db1=pve1/101 ,db2=pve2/102,")
	if err != nil || len(vms) != 2 || vms["db2"] != (proxmoxVM{Node: "pve2", VMID: "102"}) {
		t.Fatalf("%+v %v", vms, err)
	}
	for _, bad := range []string{"db1", "db1=pve1", "db1=/101", "=pve1/101"} {
		if _, err := parseHostVMIDs(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// recordingHostOrchestrator is a backend with hosts that records the host calls.
type recordingHostOrchestrator struct {
	unsupportedOrchestrator
	mu        sync.Mutex
	calls     []string
	rebootErr error
	states    []string // HostStatus answers in turn, the last repeated
}

func newRecordingHostOrchestrator() *recordingHostOrchestrator {
	o := &recordingHostOrchestrator{}
	o.unsupportedOrchestrator.orch = o
	return o
}

func (o *recordingHostOrchestrator) Name() string { return "recorder" }
func (o *recordingHostOrchestrator) Type() string { return config.ConstOrchestratorProxmox }
func (o *recordingHostOrchestrator) Traits() OrchestratorTraits {
	return OrchestratorTraits{HasHost: true}
}
func (o *recordingHostOrchestrator) RebootHost(s *ServerMonitor) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, "reboot "+s.URL)
	return o.rebootErr
}
func (o *recordingHostOrchestrator) HostStatus(s *ServerMonitor) (HostStatus, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, "status "+s.URL)
	st := o.states[0]
	if len(o.states) > 1 {
		o.states = o.states[1:]
	}
	return HostStatus{Node: "pve1", VMID: "101", State: st}, nil
}

// The host step of the rolling upgrade on a recording backend: off by default (no call), on
// it reboots then waits for running and the guest's SSH port; a server without a VM is
// skipped; a reboot error and a host that does not come back are errors that stop the run.
func TestRollingSequenceHostStep(t *testing.T) {
	saveInterval, saveReach := hostWaitInterval, hostGuestReachable
	defer func() { hostWaitInterval, hostGuestReachable = saveInterval, saveReach }()
	hostWaitInterval = time.Millisecond
	reached := 0
	hostGuestReachable = func(string, time.Duration) error { reached++; return nil }

	cl := proxmoxTestCluster(t, "https://pve.invalid:8006")
	rec := newRecordingHostOrchestrator()
	rec.states = []string{"stopped", "running"}
	cl.Orchestrators = map[string]Orchestrator{config.ConstOrchestratorProxmox: rec}
	db1 := &ServerMonitor{Name: "db1", Host: "10.0.0.1", Port: "3306", URL: "10.0.0.1:3306", ClusterGroup: cl}
	db3 := &ServerMonitor{Name: "db3", Host: "10.0.0.3", Port: "3306", URL: "10.0.0.3:3306", ClusterGroup: cl}

	// off (T14): nothing called
	if err := cl.rollingUpgradeHostStep(db1); err != nil || len(rec.calls) != 0 {
		t.Fatalf("host step off: %v %v", err, rec.calls)
	}
	cl.Conf.ProvDbRollingUpgradeHost = true
	if err := cl.rollingUpgradeHostStep(db1); err != nil {
		t.Fatalf("host step: %v", err)
	}
	want := "reboot 10.0.0.1:3306,status 10.0.0.1:3306,status 10.0.0.1:3306"
	if got := strings.Join(rec.calls, ","); got != want || reached != 1 {
		t.Fatalf("calls %s (reached %d), want %s", got, reached, want)
	}
	// a server without a VM: skipped
	rec.calls = nil
	if err := cl.rollingUpgradeHostStep(db3); err != nil || len(rec.calls) != 0 {
		t.Fatalf("server without a VM: %v %v", err, rec.calls)
	}
	// the reboot fails: the step fails
	rec.rebootErr = errors.New("VM locked")
	if err := cl.rollingUpgradeHostStep(db1); err == nil || !strings.Contains(err.Error(), "VM locked") {
		t.Fatalf("reboot error: %v", err)
	}
	// the host does not come back within the timeout
	rec.rebootErr = nil
	rec.states = []string{"stopped"}
	cl.Conf.ProvDbHostRebootTimeout = 1
	start := time.Now()
	if err := cl.rollingUpgradeHostStep(db1); err == nil || !strings.Contains(err.Error(), "not back") {
		t.Fatalf("timeout: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("the wait is not bounded by prov-db-host-reboot-timeout: %s", time.Since(start))
	}
}

// CheckRollingUpgradeHost: nothing while the step is off; WARN0350 on a backend without
// hosts; WARN0351 naming the servers without a VM.
func TestCheckRollingUpgradeHost(t *testing.T) {
	cl := proxmoxTestCluster(t, "https://pve.invalid:8006")
	db1 := &ServerMonitor{Name: "db1", Host: "10.0.0.1", Port: "3306", URL: "10.0.0.1:3306", ClusterGroup: cl}
	db3 := &ServerMonitor{Name: "db3", Host: "10.0.0.3", Port: "3306", URL: "10.0.0.3:3306", ClusterGroup: cl}
	cl.Servers = serverList{db1, db3}

	cl.CheckRollingUpgradeHost()
	if cl.ConfigStateMachine.IsInState("WARN0350") || cl.ConfigStateMachine.IsInState("WARN0351") {
		t.Fatal("a state while the host step is off")
	}
	cl.Conf.ProvDbRollingUpgradeHost = true
	cl.CheckRollingUpgradeHost()
	if !cl.ConfigStateMachine.IsInState("WARN0351") {
		t.Fatal("WARN0351 not opened for db3")
	}
	if cl.ConfigStateMachine.IsInState("WARN0350") {
		t.Fatal("WARN0350 on a backend with hosts")
	}

	onprem := proxmoxTestCluster(t, "")
	onprem.Conf.ProvOrchestrator = config.ConstOrchestratorOnPremise
	onprem.Conf.ProvDbRollingUpgradeHost = true
	onprem.CheckRollingUpgradeHost()
	if !onprem.ConfigStateMachine.IsInState("WARN0350") {
		t.Fatal("WARN0350 not opened on on-premise")
	}
}

// The host routes have their ACL rules: the shutdown needs db-stop, the start db-start, the
// reboot both, the status db-show-status; none is reachable without its grant.
func TestHostRoutesACL(t *testing.T) {
	cl := setupACLTestCluster()
	cl.APIUsers["user_show_status"] = APIUser{User: "user_show_status", Grants: map[string]bool{config.GrantDBShowStatus: true}}
	base := "/api/clusters/test/servers/db1"
	for _, tc := range []struct {
		user, url string
		want      bool
	}{
		{"user_stop_only", base + "/actions/host-stop", true},
		{"user_start_only", base + "/actions/host-stop", false},
		{"user_start_only", base + "/actions/host-start", true},
		{"user_stop_only", base + "/actions/host-start", false},
		{"user_start_stop", base + "/actions/host-reboot", true},
		{"user_start_only", base + "/actions/host-reboot", false},
		{"user_stop_only", base + "/actions/host-reboot", false},
		{"user_show_status", base + "/host-status", true},
		{"user_start_stop", base + "/host-status", false},
	} {
		if got := cl.IsURLPassACL(tc.user, tc.url, false); got != tc.want {
			t.Errorf("%s on %s: got %v, want %v", tc.user, tc.url, got, tc.want)
		}
	}
}
