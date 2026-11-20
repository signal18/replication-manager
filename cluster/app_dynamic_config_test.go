package cluster

import (
	"encoding/base64"
	"os/exec"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func phpTestApp(volumes ...*config.Volume) (*Cluster, *App) {
	c := &Cluster{Name: "phptest", Conf: &config.Config{ProvType: "docker"}}
	a := &App{Name: "web1", ClusterGroup: c, AppConfig: &config.AppConfig{
		ProvAppApplyDynamicConfig: true, ProvAppMem: "1024", ProvAppCpuCores: "2", ProvAppHATopology: "flex",
		Deployment: config.NewDeploymentConfig(),
	}}
	a.AppConfig.Deployment.Storages.Volumes = volumes
	return c, a
}

// The formulas: opcache memory/16 in [64,256], max_children = (memory - reserved - opcache) /
// process within [2, 8 per core], spare servers and start in between, memory_limit 4 x process
// within [128, pool/2].
func TestComputeAppPHPSizing(t *testing.T) {
	for _, tc := range []struct {
		name                                      string
		mem, cores, process, reserved             int
		opcache, children, min, max, start, limit int
	}{
		// 1024 - 128 - 64 = 832 / 64 = 13 workers (under 16 for 2 cores)
		{"1G 2 cores defaults", 1024, 2, 0, 0, 64, 13, 1, 6, 3, 256},
		// 4096 - 128 - 256 = 3712 / 64 = 58, capped at 8 x 2 cores
		{"4G 2 cores, CPU bound", 4096, 2, 0, 0, 256, 16, 2, 8, 5, 256},
		// same memory, 8 cores: the memory bound wins
		{"4G 8 cores, memory bound", 4096, 8, 64, 128, 256, 58, 7, 29, 18, 256},
		// 512 - 128 - 64 = 320 / 64 = 5; memory_limit 256 within [128, 160] -> 160
		{"512M small", 512, 1, 0, 0, 64, 5, 1, 2, 1, 160},
		// below two workers the floor holds, memory_limit at its 128 floor
		{"256M tiny", 256, 1, 0, 0, 64, 2, 1, 1, 1, 128},
		// a 512M image reserve (memcached) and heavy 128M workers: (2048-512-128)/128 = 11
		{"2G heavy workers", 2048, 4, 128, 512, 128, 11, 1, 5, 3, 512},
		// zero cores and memory take the floors, never a division by zero or a negative pool
		{"empty plan", 0, 0, 0, 0, 64, 2, 1, 1, 1, 128},
	} {
		s := computeAppPHPSizing(tc.mem, tc.cores, tc.process, tc.reserved)
		if s.OpcacheMB != tc.opcache || s.MaxChildren != tc.children || s.MinSpare != tc.min || s.MaxSpare != tc.max || s.StartServers != tc.start || s.MemoryLimitMB != tc.limit {
			t.Errorf("%s: got opcache=%d children=%d spare=%d-%d start=%d limit=%d, want %d %d %d-%d %d %d",
				tc.name, s.OpcacheMB, s.MaxChildren, s.MinSpare, s.MaxSpare, s.StartServers, s.MemoryLimitMB,
				tc.opcache, tc.children, tc.min, tc.max, tc.start, tc.limit)
		}
		// php-fpm refuses a pool where min_spare <= start <= max_spare <= max_children does not hold
		if !(1 <= s.MinSpare && s.MinSpare <= s.StartServers && s.StartServers <= s.MaxSpare && s.MaxSpare <= s.MaxChildren) {
			t.Errorf("%s: invalid php-fpm pool %+v", tc.name, s)
		}
		if s.MaxRequests != 500 || s.PoolMB < 0 {
			t.Errorf("%s: %+v", tc.name, s)
		}
	}
}

// The fragments carry every computed value, the pool name and no unresolved token.
func TestRenderAppPHPFragments(t *testing.T) {
	s := computeAppPHPSizing(1024, 2, 0, 0)
	pool, ini := renderAppPHPFragments(s, "www")
	for _, want := range []string{"[global]\nprocess_control_timeout = 30s\n", "[www]\npm = dynamic\n", "pm.max_children = 13\n", "pm.start_servers = 3\n",
		"pm.min_spare_servers = 1\n", "pm.max_spare_servers = 6\n", "pm.max_requests = 500\n", "(1024M, 2 cores)"} {
		if !strings.Contains(pool, want) {
			t.Errorf("pool fragment misses %q:\n%s", want, pool)
		}
	}
	for _, want := range []string{"memory_limit = 256M\n", "opcache.memory_consumption = 64\n"} {
		if !strings.Contains(ini, want) {
			t.Errorf("ini fragment misses %q:\n%s", want, ini)
		}
	}
	if strings.Contains(pool+ini, "%%") {
		t.Fatalf("unresolved token:\n%s%s", pool, ini)
	}
}

// The apply script embeds both fragments, the directories and the plan, and is valid sh.
func TestAppPHPApplyScript(t *testing.T) {
	c, a := phpTestApp(&config.Volume{Name: "web1-data"})
	a.AppConfig.ProvAppPhpFpmPoolDir = "/etc/php/8.4/fpm/pool.d/"
	s, pool, ini, script64 := c.appPHPConfig(a, 0)
	b, err := base64.StdEncoding.DecodeString(script64)
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)
	for _, want := range []string{pool, ini, "POOL_DIR='/etc/php/8.4/fpm/pool.d'\n", "INI_DIR='/usr/local/etc/php/conf.d'\n", "PLAN_MB=1024\n", "MAX_CHILDREN=13\n",
		`"php-fpm: master process"*`, `kill -USR2 "$master"`, "/sys/fs/cgroup/memory.max", "/repman-php/zzz-repman-sizing.conf", "/repman-php/zzz-repman-sizing.ini"} {
		if !strings.Contains(strings.ReplaceAll(script, "$DIR", "/repman-php"), want) {
			t.Errorf("script misses %q", want)
		}
	}
	if s.MaxChildren != 13 {
		t.Fatalf("sizing of the app plan: %+v", s)
	}
	if sh, err := exec.LookPath("sh"); err == nil {
		cmd := exec.Command(sh, "-n")
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("sh -n: %s %s\n%s", err, out, script)
		}
	}
}

// The sizing never exceeds the docker cap the container runs under: a grow over it waits
// for the reprovision that moves the cap.
func TestAppPHPSizingWithinCap(t *testing.T) {
	c, a := phpTestApp(&config.Volume{Name: "v"})
	a.AppConfig.ProvAppMem = "2048"
	if s := c.AppPHPSizing(a, 0); s.MemoryMB != 2048 || s.MaxChildren != 16 {
		t.Fatalf("plan: %+v", s)
	}
	if s := c.AppPHPSizing(a, 1024); s.MemoryMB != 1024 || s.MaxChildren != 13 {
		t.Fatalf("within the 1024M cap: %+v", s)
	}
	if s := c.AppPHPSizing(a, 4096); s.MemoryMB != 2048 {
		t.Fatalf("a cap above the plan changes nothing: %+v", s)
	}
	for runArgs, want := range map[string]int{
		" --memory=1024m --memory-swap=1024m --cpus=1.0": 1024,
		"--cpus=2.0":         0,
		"":                   0,
		"--memory-swap=512m": 0,
	} {
		if got := appRunArgsMemoryCapMB(runArgs); got != want {
			t.Errorf("run_args %q: cap %d, want %d", runArgs, got, want)
		}
	}
}

// A burst of plan changes queues at most one apply: the switch is off in the cluster under
// test's orchestrator path only through AppDynamicConfigEnabled, so the coalescing flag is
// checked directly.
func TestAppDynamicConfigCoalesces(t *testing.T) {
	_, a := phpTestApp(&config.Volume{Name: "v"})
	a.dynamicConfigMutex.Lock() // an apply is running
	a.dynamicConfigPending.Store(true)
	for i := 0; i < 100; i++ {
		a.onAppPlanChange() // must return at once: one apply already waits
	}
	if !a.dynamicConfigPending.Load() {
		t.Fatal("the waiting apply is still pending")
	}
	a.dynamicConfigMutex.Unlock()
	a.setDynamicConfigStatus("ok")
	if !strings.HasSuffix(a.DynamicConfigStatus, " UTC: ok") {
		t.Fatalf("status: %q", a.DynamicConfigStatus)
	}
}

// The service gets the key files in the first volume, their mounts in container#app and
// task#phpconfig in the PID namespace of container#app; nothing when off or without volume.
func TestAppDynamicConfigSections(t *testing.T) {
	c, a := phpTestApp(&config.Volume{Name: "web1-data"})
	sections := map[string]map[string]string{
		"volume#1":      {"name": "web1-data", "directories": "etc var"},
		"container#app": {"volume_mounts": "web1-data/var:/var/www/html"},
	}
	c.openSVCAddAppDynamicConfigSections(sections, a)
	if sections["volume#1"]["directories"] != "etc var repman-php" ||
		sections["volume#1"]["configs"] != "web1/APP_PHP_FPM_POOL:repman-php/zzz-repman-sizing.conf web1/APP_PHP_INI:repman-php/zzz-repman-sizing.ini" {
		t.Fatalf("volume: %v", sections["volume#1"])
	}
	if sections["container#app"]["volume_mounts"] != "web1-data/var:/var/www/html web1-data/repman-php/zzz-repman-sizing.conf:/usr/local/etc/php-fpm.d/zzz-repman-sizing.conf web1-data/repman-php/zzz-repman-sizing.ini:/usr/local/etc/php/conf.d/zzz-repman-sizing.ini" {
		t.Fatalf("container#app mounts: %q", sections["container#app"]["volume_mounts"])
	}
	task := sections["task#phpconfig"]
	if task == nil || task["pidns"] != "container#app" || task["image"] != "{env.app_img}" || task["volume_mounts"] != "web1-data/repman-php:/repman-php" ||
		task["configs_environment"] != "web1/APP_PHP_CONFIG" || !strings.Contains(task["command"], `"${REPMAN_PHP_CONFIG:-$APP_PHP_CONFIG}" | base64 -d | sh`) || task["shared"] != "" {
		t.Fatalf("task: %v", task)
	}
	// a failover app has its volume on one node: the task is shared like the container
	a.AppConfig.ProvAppHATopology = "failover"
	c.openSVCAddAppDynamicConfigSections(sections, a)
	if sections["task#phpconfig"]["shared"] != "true" || sections["volume#1"]["directories"] != "etc var repman-php" {
		t.Fatalf("failover: %v %v", sections["task#phpconfig"], sections["volume#1"])
	}

	for name, mk := range map[string]func() (*Cluster, *App){
		"off": func() (*Cluster, *App) {
			c, a := phpTestApp(&config.Volume{Name: "v"})
			a.AppConfig.ProvAppApplyDynamicConfig = false
			return c, a
		},
		"no volume": func() (*Cluster, *App) { return phpTestApp() },
		"engine server": func() (*Cluster, *App) {
			c, a := phpTestApp(&config.Volume{Name: "v"})
			a.AppConfig.ProvAppConfigurator = "postgres"
			return c, a
		},
	} {
		c, a := mk()
		s := map[string]map[string]string{"volume#1": {"directories": "data"}, "container#app": {}}
		if name == "no volume" {
			delete(s, "volume#1")
		}
		c.openSVCAddAppDynamicConfigSections(s, a)
		if _, ok := s["task#phpconfig"]; ok || len(s["container#app"]) != 0 {
			t.Fatalf("%s: nothing must be added: %v", name, s)
		}
	}
}

// The settings validate their values; the switch needs a volume.
func TestAppDynamicConfigSettings(t *testing.T) {
	_, a := phpTestApp(&config.Volume{Name: "v"})
	a.Datadir = t.TempDir() // the reprovision cookie of the switch
	a.AppConfig.ProvAppApplyDynamicConfig = false
	if err := a.SwitchSetting("prov-app-apply-dynamic-config"); err != nil || !a.AppConfig.ProvAppApplyDynamicConfig {
		t.Fatalf("switch on: %v", err)
	}
	for key, bad := range map[string]string{
		"prov-app-php-fpm-pool-dir":    "relative/dir",
		"prov-app-php-ini-dir":         "/etc/php:/x",
		"prov-app-php-fpm-pool":        "www]\nx",
		"prov-app-php-process-memory":  "lots",
		"prov-app-php-reserved-memory": "-1",
	} {
		if err := a.SetSetting(key, bad); err == nil {
			t.Errorf("%s=%q accepted", key, bad)
		}
	}
	a.AppConfig.ProvAppApplyDynamicConfig = false // no live apply from the setters in this test
	for key, good := range map[string]string{"prov-app-php-fpm-pool-dir": "/etc/php/8.4/fpm/pool.d", "prov-app-php-fpm-pool": "www", "prov-app-php-process-memory": "96M", "prov-app-php-reserved-memory": "1G"} {
		if err := a.SetSetting(key, good); err != nil {
			t.Errorf("%s=%q refused: %v", key, good, err)
		}
	}
	if a.AppConfig.ProvAppPhpReservedMemory != "1G" || appMeasureMB(a.AppConfig.ProvAppPhpReservedMemory) != 1024 || appMeasureMB("96M") != 96 {
		t.Fatalf("memory values: %+v", a.AppConfig)
	}
	_, noVolume := phpTestApp()
	noVolume.AppConfig.ProvAppApplyDynamicConfig = false
	if err := noVolume.SetSetting("prov-app-apply-dynamic-config", "true"); err == nil {
		t.Fatal("an app without volume cannot hold the PHP sizing")
	}
}
