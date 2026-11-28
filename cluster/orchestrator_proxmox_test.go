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
	"github.com/signal18/replication-manager/utils/proxmox"
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
	// rebootTakes: the time RebootHost takes; deadlineSeen: the deadline a host action of
	// the backend would use while the reboot runs (hostActionDeadline)
	rebootTakes  time.Duration
	uptime       func() time.Duration // the VM's uptime; nil: an hour before the reboot, then the time since it
	rebootedAt   time.Time
	statusErr    error // HostStatus answers it once the reboot was asked
	deadlineSeen time.Time
	cluster      *Cluster
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
	o.rebootedAt = time.Now()
	if o.cluster != nil {
		o.deadlineSeen = o.cluster.hostActionDeadline(s)
	}
	time.Sleep(o.rebootTakes)
	return o.rebootErr
}
func (o *recordingHostOrchestrator) HostStatus(s *ServerMonitor) (HostStatus, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, "status "+s.URL)
	if o.statusErr != nil && !o.rebootedAt.IsZero() {
		return HostStatus{State: "unknown"}, o.statusErr
	}
	st := o.states[0]
	if len(o.states) > 1 {
		o.states = o.states[1:]
	}
	up := time.Hour
	switch {
	case o.uptime != nil:
		up = o.uptime()
	case !o.rebootedAt.IsZero():
		up = time.Since(o.rebootedAt)
	}
	return HostStatus{Node: "pve1", VMID: "101", State: st, Uptime: up}, nil
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
	rec.states = []string{"running", "stopped", "running"} // before the reboot, then the wait
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
	want := "status 10.0.0.1:3306,reboot 10.0.0.1:3306,status 10.0.0.1:3306,status 10.0.0.1:3306"
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
	if err := cl.rollingUpgradeHostStep(db1); err == nil || !strings.Contains(err.Error(), "VM locked") ||
		!strings.Contains(err.Error(), "pve1/101") || !strings.Contains(err.Error(), "may be stopped or mid-reboot") {
		t.Fatalf("reboot error must name the VM and say it may be stopped or mid-reboot: %v", err)
	}
	// the host does not come back within the timeout
	rec.rebootErr = nil
	rec.states = []string{"stopped"}
	cl.Conf.ProvDbHostRebootTimeout = 1
	start := time.Now()
	if err := cl.rollingUpgradeHostStep(db1); err == nil || !strings.Contains(err.Error(), "not back") || !strings.Contains(err.Error(), "pve1/101") {
		t.Fatalf("timeout: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("the wait is not bounded by prov-db-host-reboot-timeout: %s", time.Since(start))
	}

	// one deadline for the reboot and the wait: a reboot that takes most of the timeout
	// leaves the wait the rest, not a fresh timeout; the backend's own action uses it too
	rec.cluster = cl
	rec.rebootTakes = 700 * time.Millisecond
	start = time.Now()
	if err := cl.rollingUpgradeHostStep(db1); err == nil {
		t.Fatal("a host that never comes back must fail")
	}
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("reboot + wait took %s, more than one prov-db-host-reboot-timeout (1s)", elapsed)
	}
	if rec.deadlineSeen.IsZero() || rec.deadlineSeen.After(start.Add(1100*time.Millisecond)) {
		t.Fatalf("the backend's action deadline %s is not the step's (start %s + 1s)", rec.deadlineSeen, start)
	}
	if _, ok := cl.hostStepDeadlines.Load(db1.URL); ok {
		t.Fatal("the step's deadline must be cleared when it ends")
	}
}

// The reboot race: the reboot was asked but the old guest is still up (running, SSH
// answering, its uptime still growing from before). It is not "back": the step waits for a
// new boot and fails at the deadline. Without the boot identity the old guest passed.
func TestRollingHostStepOldGuestStillUp(t *testing.T) {
	saveInterval, saveReach := hostWaitInterval, hostGuestReachable
	defer func() { hostWaitInterval, hostGuestReachable = saveInterval, saveReach }()
	hostWaitInterval = 10 * time.Millisecond
	hostGuestReachable = func(string, time.Duration) error { return nil }

	cl := proxmoxTestCluster(t, "https://pve.invalid:8006")
	cl.Conf.ProvDbRollingUpgradeHost = true
	cl.Conf.ProvDbHostRebootTimeout = 1
	rec := newRecordingHostOrchestrator()
	rec.states = []string{"running"}
	bootedAt := time.Now().Add(-time.Hour)
	rec.uptime = func() time.Duration { return time.Since(bootedAt) } // never rebooted
	cl.Orchestrators = map[string]Orchestrator{config.ConstOrchestratorProxmox: rec}
	db1 := &ServerMonitor{Name: "db1", Host: "10.0.0.1", Port: "3306", URL: "10.0.0.1:3306", ClusterGroup: cl}

	err := cl.rollingUpgradeHostStep(db1)
	if err == nil || !strings.Contains(err.Error(), "has not rebooted yet") {
		t.Fatalf("the old guest still up must not count as back: %v", err)
	}

	// the guest reboots during the wait: back once its uptime restarts
	rebootAt := time.Now().Add(300 * time.Millisecond)
	rec.uptime = func() time.Duration {
		if time.Now().Before(rebootAt) {
			return time.Since(bootedAt)
		}
		return time.Since(rebootAt)
	}
	if err := cl.rollingUpgradeHostStep(db1); err != nil {
		t.Fatalf("a guest that reboots within the deadline is back: %v", err)
	}
}

// A status the hypervisor refuses (4xx: token, privilege, no such VM) ends the wait at once
// instead of retrying until the deadline.
func TestRollingHostStepStatusRefusedFailsFast(t *testing.T) {
	saveInterval, saveReach := hostWaitInterval, hostGuestReachable
	defer func() { hostWaitInterval, hostGuestReachable = saveInterval, saveReach }()
	hostWaitInterval = 10 * time.Millisecond
	hostGuestReachable = func(string, time.Duration) error { return nil }

	cl := proxmoxTestCluster(t, "https://pve.invalid:8006")
	cl.Conf.ProvDbRollingUpgradeHost = true
	cl.Conf.ProvDbHostRebootTimeout = 5
	rec := newRecordingHostOrchestrator()
	rec.states = []string{"running"}
	rec.statusErr = &proxmox.APIError{StatusCode: http.StatusForbidden, Message: "Permission check failed (VM.Audit)"}
	cl.Orchestrators = map[string]Orchestrator{config.ConstOrchestratorProxmox: rec}
	db1 := &ServerMonitor{Name: "db1", Host: "10.0.0.1", Port: "3306", URL: "10.0.0.1:3306", ClusterGroup: cl}

	start := time.Now()
	err := cl.rollingUpgradeHostStep(db1)
	if err == nil || !strings.Contains(err.Error(), "refuses the status") || !strings.Contains(err.Error(), "VM.Audit") {
		t.Fatalf("a refused status must end the step with its reason: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("a refused status was retried until the deadline (%s)", time.Since(start))
	}
	// a rate limit (429) or a timeout (408), from a proxy under load, is retried
	cl.Conf.ProvDbHostRebootTimeout = 1
	for _, code := range []int{http.StatusTooManyRequests, http.StatusRequestTimeout} {
		rec.rebootedAt = time.Time{}
		rec.statusErr = &proxmox.APIError{StatusCode: code, Message: "busy"}
		err := cl.rollingUpgradeHostStep(db1)
		if err == nil || strings.Contains(err.Error(), "refuses the status") || !strings.Contains(err.Error(), "not back") {
			t.Fatalf("%d must be retried until the deadline: %v", code, err)
		}
	}
}

func TestHostBootRebooted(t *testing.T) {
	asked := time.Now()
	now := asked.Add(30 * time.Second)
	b := hostBoot{uptime: time.Hour, known: true, asked: asked}
	if b.rebooted(time.Hour+30*time.Second, now) {
		t.Error("the old guest (uptime grown from before) counted as rebooted")
	}
	if !b.rebooted(10*time.Second, now) {
		t.Error("a new boot (uptime lower than before) not seen")
	}
	// the status before the reboot could not be read: a boot after the request counts
	u := hostBoot{asked: asked}
	if !u.rebooted(20*time.Second, now) || u.rebooted(time.Hour, now) {
		t.Error("unknown boot: uptime against the time since the request")
	}
	// a VM booted within the slack before the reboot: only a lower uptime proves a new boot
	young := hostBoot{uptime: time.Second, known: true, asked: asked}
	if young.rebooted(31*time.Second, now) {
		t.Error("a young old guest counted as rebooted")
	}
}

// The API's shutdown and reboot are refused while the database on the VM runs, unless the
// server is in maintenance, and always on a running master; a stopped database is allowed.
func TestHostActionRefusal(t *testing.T) {
	cl := proxmoxTestCluster(t, "https://pve.invalid:8006")
	master := &ServerMonitor{Name: "db1", Id: "db1", URL: "10.0.0.1:3306", State: stateMaster, ClusterGroup: cl}
	replica := &ServerMonitor{Name: "db2", Id: "db2", URL: "10.0.0.2:3306", State: stateSlave, ClusterGroup: cl}
	cl.master = master

	if err := cl.HostActionRefusal(replica); err == nil || !strings.Contains(err.Error(), "not in maintenance") {
		t.Fatalf("running replica, no maintenance: %v", err)
	}
	replica.IsMaintenance = true
	if err := cl.HostActionRefusal(replica); err != nil {
		t.Fatalf("replica in maintenance: %v", err)
	}
	master.IsMaintenance = true
	if err := cl.HostActionRefusal(master); err == nil || !strings.Contains(err.Error(), "running master") {
		t.Fatalf("running master, even in maintenance: %v", err)
	}
	master.State = stateFailed
	if err := cl.HostActionRefusal(master); err != nil {
		t.Fatalf("stopped master: %v", err)
	}
	replica.IsMaintenance = false
	replica.State = stateFailed
	if err := cl.HostActionRefusal(replica); err != nil {
		t.Fatalf("stopped replica: %v", err)
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
