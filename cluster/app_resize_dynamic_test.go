package cluster

import (
	"errors"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// appResizer records the app resizes asked of the backend, in order.
type appResizer struct {
	calls   *[]string
	applied bool
}

func (f appResizer) CanConfigResize(*ServerMonitor, bool) (ResizeFeasibility, error) {
	return ResizeYes, nil
}
func (f appResizer) ConfigResize(*ServerMonitor, bool) (bool, error) { return true, nil }
func (f appResizer) ResizeDisk(*ServerMonitor, int) (bool, error)    { return true, nil }
func (f appResizer) ConfigResizeApp(app *App, grow bool) (bool, error) {
	dir := "shrink"
	if grow {
		dir = "grow"
	}
	*f.calls = append(*f.calls, "cap:"+app.AppConfig.ProvAppMem+":"+dir)
	return f.applied, nil
}

func liveResizeTestApp(t *testing.T) (*Cluster, *App) {
	c := &Cluster{Name: "c1", Conf: &config.Config{ProvType: "docker", ProvAppDynamicResource: true}}
	a := &App{Name: "web", ClusterGroup: c, Datadir: t.TempDir(), AppConfig: &config.AppConfig{
		ProvAppMem: "1024", ProvAppCpuCores: "2", ProvAppHATopology: "flex", ProvAppAgents: "n1,n2", Deployment: config.NewDeploymentConfig(),
	}}
	return c, a
}

// The PHP tokens of the configurator follow memory and cores.
func TestAppConfiguratorSizingPHP(t *testing.T) {
	for _, tc := range []struct {
		mem, cores                                          int
		opcache, children, minSpare, maxSpare, start, limit string
	}{
		{1024, 1, "64", "8", "1", "4", "2", "256"},     // 832/64 = 13, 8 per core
		{1024, 2, "64", "13", "1", "6", "3", "256"},    // memory bound
		{512, 2, "64", "5", "1", "2", "1", "160"},      // limit at half the pool
		{4096, 8, "256", "58", "7", "29", "18", "256"}, // opcache at its ceiling
		{256, 1, "64", "2", "1", "1", "1", "128"},      // floors
	} {
		env := appConfiguratorSizing(tc.mem, tc.cores, 0)
		got := []string{env["%%ENV:SVC_CONF_ENV_PHP_OPCACHE_MEMORY%%"], env["%%ENV:SVC_CONF_ENV_PHP_FPM_MAX_CHILDREN%%"], env["%%ENV:SVC_CONF_ENV_PHP_FPM_MIN_SPARE_SERVERS%%"],
			env["%%ENV:SVC_CONF_ENV_PHP_FPM_MAX_SPARE_SERVERS%%"], env["%%ENV:SVC_CONF_ENV_PHP_FPM_START_SERVERS%%"], env["%%ENV:SVC_CONF_ENV_PHP_MEMORY_LIMIT%%"]}
		want := []string{tc.opcache, tc.children, tc.minSpare, tc.maxSpare, tc.start, tc.limit}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%dM %d cores: got %v want %v", tc.mem, tc.cores, got, want)
		}
	}
}

func TestAppPlanGrows(t *testing.T) {
	for _, tc := range []struct {
		om, nm, oc, nc string
		grow           bool
	}{
		{"1024", "2048", "", "", true},
		{"2048", "1024", "", "", false},
		{"1G", "2048", "", "", true},
		{"", "", "1", "2", true},
		{"", "", "2", "1", false},
		{"2048", "1024", "1", "4", false}, // the memory decides the order
		{"1024", "2048", "4", "1", true},  // memory up, cores down: a grow (the cap first)
	} {
		if got := appPlanGrows(tc.om, tc.nm, tc.oc, tc.nc); got != tc.grow {
			t.Errorf("%+v: grow=%v", tc, got)
		}
	}
}

// A grow moves the cap first and, when the cap cannot move live, arms the reprovision; a
// shrink reloads first (nothing to reload here) and moves the cap after.
func TestResizeAppDynamicResourcesOrder(t *testing.T) {
	c, a := liveResizeTestApp(t)
	var calls []string
	c.resizerOverride = appResizer{calls: &calls, applied: true}
	if err := c.ResizeAppDynamicResources(a, true); err != nil || len(calls) != 1 || a.HasReprovCookie() {
		t.Fatalf("live grow: err=%v calls=%v reprov=%v", err, calls, a.HasReprovCookie())
	}
	c.resizerOverride = appResizer{calls: &calls, applied: false}
	a.AppConfig.ProvAppMem = "2048"
	if err := c.ResizeAppDynamicResources(a, true); err != nil || !a.HasReprovCookie() {
		t.Fatalf("a grow without a live cap arms the reprovision: err=%v", err)
	}
	// off: nothing
	c.Conf.ProvAppDynamicResource = false
	calls = nil
	if err := c.ResizeAppDynamicResources(a, false); err != nil || len(calls) != 0 {
		t.Fatalf("off: %v %v", err, calls)
	}
}

// The direction is computed against the cap the service runs under (observed), not taken
// from the change that queued the apply: a cap of 2G and a plan of 1G is a shrink, whatever
// the caller guessed; the guess is used only when the cap cannot be read.
func TestResizeAppDynamicResourcesDirection(t *testing.T) {
	c, a := liveResizeTestApp(t)
	var calls []string
	c.resizerOverride = appResizer{calls: &calls, applied: true}
	c.appCapOverride = func(*App) (int, int, bool) { return 2048, 200, true }
	a.AppConfig.ProvAppMem = "1024"
	if err := c.ResizeAppDynamicResources(a, true); err != nil || strings.Join(calls, ",") != "cap:1024:shrink" {
		t.Fatalf("observed cap 2G, plan 1G: shrink order expected: %v %v", err, calls)
	}
	calls = nil
	c.appCapOverride = func(*App) (int, int, bool) { return 1024, 100, true }
	if err := c.ResizeAppDynamicResources(a, false); err != nil || strings.Join(calls, ",") != "cap:1024:grow" {
		t.Fatalf("same memory, cpu 1 -> 2 cores: grow expected: %v %v", err, calls)
	}
	calls = nil
	c.appCapOverride = func(*App) (int, int, bool) { return 0, 0, false }
	if err := c.ResizeAppDynamicResources(a, true); err != nil || strings.Join(calls, ",") != "cap:1024:grow" {
		t.Fatalf("cap unreadable: the caller's guess: %v %v", err, calls)
	}
}

// The cap in force is read from the definition: the slice keywords, else the docker caps.
func TestAppCapFromDefinition(t *testing.T) {
	for raw, want := range map[string][3]int{
		"[DEFAULT]\npg_mem_limit = 2147483648\npg_cpu_quota = 200%\n[container#app]\nrun_args = --sysctl x=1\n": {2048, 200, 1},
		"[DEFAULT]\nnodes = n1\n[container#app]\nrun_args = --memory=1024m --memory-swap=1024m --cpus=1.0\n":    {1024, 100, 1},
		"[DEFAULT]\nnodes = n1\n[container#app]\nrun_args = --sysctl x=1\n":                                     {0, 0, 0},
	} {
		m, p, ok := appCapFromDefinition(raw)
		okInt := 0
		if ok {
			okInt = 1
		}
		if [3]int{m, p, okInt} != want {
			t.Errorf("%q: %d %d %v, want %v", raw, m, p, ok, want)
		}
	}
}

// A failed apply arms the reprovision; the next good one clears what it armed.
func TestResizeAppDynamicResourcesFailureReprov(t *testing.T) {
	c, a := liveResizeTestApp(t)
	var calls []string
	c.appCapOverride = func(*App) (int, int, bool) { return 1024, 200, true }
	c.resizerOverride = failingAppResizer{}
	a.AppConfig.ProvAppMem = "2048"
	if err := c.ResizeAppDynamicResources(a, true); err == nil || !a.HasReprovCookie() || a.getLiveResizeError() == "" {
		t.Fatalf("failure: reprovision + APPERR011 expected: %v", err)
	}
	c.resizerOverride = appResizer{calls: &calls, applied: true}
	if err := c.ResizeAppDynamicResources(a, true); err != nil || a.HasReprovCookie() || a.getLiveResizeError() != "" {
		t.Fatalf("success clears what the failure armed: %v cookie=%v", err, a.HasReprovCookie())
	}
}

type failingAppResizer struct{ appResizer }

func (failingAppResizer) ConfigResizeApp(*App, bool) (bool, error) {
	return false, errors.New("om3 unreachable")
}

// Nodes: every agent of a flex app; the running node of a failover app, or all its agents
// with one success enough when it is not known.
func TestAppInstanceNodes(t *testing.T) {
	_, a := liveResizeTestApp(t)
	if n, any := appInstanceNodes(a); strings.Join(n, ",") != "n1,n2" || any {
		t.Fatalf("flex: %v %v", n, any)
	}
	a.AppConfig.ProvAppHATopology = "failover"
	if n, any := appInstanceNodes(a); strings.Join(n, ",") != "n1,n2" || !any {
		t.Fatalf("failover, node unknown: %v %v", n, any)
	}
	a.Agent = "n2"
	if n, any := appInstanceNodes(a); strings.Join(n, ",") != "n2" || any {
		t.Fatalf("failover on n2: %v %v", n, any)
	}
	fail := func(node string) error {
		if node == "n1" {
			return errors.New("down")
		}
		return nil
	}
	if appOnNodes([]string{"n1", "n2"}, true, fail) != nil || appOnNodes([]string{"n1", "n2"}, false, fail) == nil {
		t.Fatal("anyNode: one success is enough; otherwise every node")
	}
}

// The setters apply live when the switch is on (no reprovision cookie), keep the
// reprovision otherwise.
func TestAppSettersLiveResize(t *testing.T) {
	c, a := liveResizeTestApp(t)
	a.AppConfig.ProvAppSizingMode = config.AppSizingModeManual
	c.Conf.ProvAppDynamicResource = false
	if err := a.SetSetting("prov-app-memory", "2048"); err != nil || !a.HasReprovCookie() {
		t.Fatalf("off: the reprovision stays: %v", err)
	}
	a.DelReprovisionCookie()
	c.Conf.ProvAppDynamicResource = true
	a.liveResizePending.Store(true) // an apply already waits: the setter only coalesces
	if err := a.SetSetting("prov-app-memory", "1024"); err != nil || a.HasReprovCookie() {
		t.Fatalf("on: applied live, no reprovision: %v", err)
	}
	if a.AppConfig.ProvAppMem != "1024" {
		t.Fatalf("stored: %q", a.AppConfig.ProvAppMem)
	}
}

// task#reload runs the template's APP_RELOAD_COMMAND in the app's PID namespace.
func TestAppReloadSection(t *testing.T) {
	c, a := liveResizeTestApp(t)
	s := map[string]map[string]string{}
	c.openSVCAddAppReloadSection(s, a)
	if len(s) != 0 {
		t.Fatalf("no reload command, no task: %v", s)
	}
	a.AppConfig.Deployment.Variables = config.VariableMaps{{Name: "APP_RELOAD_COMMAND", Type: config.VariableTypeEnv, Value: "kill -USR2 1"}}
	c.openSVCAddAppReloadSection(s, a)
	task := s["task#reload"]
	if task == nil || task["pidns"] != "container#app" || task["image"] != "{env.app_img}" || task["configs_environment"] != "web/*" ||
		task["command"] != `-c 'eval "$APP_RELOAD_COMMAND"'` || task["shared"] != "" {
		t.Fatalf("task: %v", task)
	}
	a.AppConfig.ProvAppHATopology = "failover"
	c.openSVCAddAppReloadSection(s, a)
	if s["task#reload"]["shared"] != "true" {
		t.Fatal("a failover app's task is shared")
	}
}

// The slice cap of a live-resized app: its plan memory and cores; only without the docker
// cap and never for a monitored engine server.
func TestAppPGKeywords(t *testing.T) {
	c, a := liveResizeTestApp(t)
	kv := c.appPGKeywords(a)
	if kv["pg_mem_limit"] != "1073741824" || kv["pg_cpu_quota"] != "200%" {
		t.Fatalf("keywords: %v", kv)
	}
	if !c.appCapOnSlice(a) {
		t.Fatal("no docker cap: the cap is on the slice")
	}
	c.Conf.ProvDBDockerRunArgsLimit = true
	if c.appCapOnSlice(a) {
		t.Fatal("docker cap: run_args binds, not the slice")
	}
}
