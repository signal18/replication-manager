package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// #1858: monitoring-add-monitor-script vetoes with its first output line, carries the
// shared contract in argv and env; monitoring-drop-monitor-script only informs.
func TestMonitorHookScripts(t *testing.T) {
	c := &Cluster{Name: "t", Conf: &config.Config{}, resources: NewResourceManager()}
	c.Conf.ProvDbImg = "mariadb:11.4"
	c.Conf.ProvCores = "2"
	c.Conf.ProvMem = "8192"
	c.Conf.ProvDisk = "40"
	c.Conf.ProvIops = "2000"
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	envFile := filepath.Join(dir, "env")
	veto := filepath.Join(dir, "veto.sh")
	if err := os.WriteFile(veto, []byte("#!/bin/sh\necho \"$@\" > "+argvFile+"\nenv | grep ^REPMAN_ > "+envFile+"\necho \"no room for $3\"\necho detail\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c.Conf.MonitoringAddMonitorScript = veto
	h := c.monitorHookDatabase("db4", "3306", nil)
	if h.Version != "11.4" || h.Flavor != "mariadb" || h.Units != "2" || h.MemMB != "8192" || h.DiskGB != "40" || h.Unit != "dbu" {
		t.Fatalf("database hook from the declared image and axes: %+v", h)
	}
	err := c.RunAddMonitorScript(h)
	if err == nil || !strings.Contains(err.Error(), "no room for db4:3306") || strings.Contains(err.Error(), "detail") {
		t.Fatalf("the veto must carry the first output line only, got %v", err)
	}
	argv, _ := os.ReadFile(argvFile)
	if strings.TrimSpace(string(argv)) != "t database db4:3306 11.4 2" {
		t.Fatalf("argv = cluster type name version units, got %q", argv)
	}
	env, _ := os.ReadFile(envFile)
	for _, want := range []string{"REPMAN_MONITOR_PHASE=add", "REPMAN_MONITOR_TYPE=database", "REPMAN_MONITOR_HOST=db4", "REPMAN_MONITOR_PORT=3306", "REPMAN_MONITOR_FLAVOR=mariadb", "REPMAN_RESOURCE_CORES=2", "REPMAN_RESOURCE_MEMORY_MB=8192", "REPMAN_RESOURCE_DISK_GB=40", "REPMAN_RESOURCE_IOPS=2000", "REPMAN_RESOURCE_UNIT=dbu", "REPMAN_RESOURCE_UNITS=2"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("env must carry %s, got:\n%s", want, env)
		}
	}
	// Allowing script, then no script.
	allow := filepath.Join(dir, "allow.sh")
	if err := os.WriteFile(allow, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c.Conf.MonitoringAddMonitorScript = allow
	if err := c.RunAddMonitorScript(h); err != nil {
		t.Fatalf("an allowing script must pass: %v", err)
	}
	c.Conf.MonitoringAddMonitorScript = ""
	if err := c.RunAddMonitorScript(h); err != nil {
		t.Fatalf("no script must pass: %v", err)
	}
	// Drop: a failing script does not block, it is logged; the phase is drop.
	c.Conf.MonitoringDropMonitorScript = veto
	c.RunDropMonitorScript(h)
	env, _ = os.ReadFile(envFile)
	if !strings.Contains(string(env), "REPMAN_MONITOR_PHASE=drop") {
		t.Fatalf("drop phase must be in env: %s", env)
	}
}

// The proxy and app descriptions: declared image tag, per-kind axes, apu units.
func TestMonitorHookDescriptions(t *testing.T) {
	c := &Cluster{Name: "t", Conf: &config.Config{}, resources: NewResourceManager()}
	c.Conf.ProvProxProxysqlImg = "proxysql/proxysql:2.6"
	c.Conf.ProvProxCores = "1"
	c.Conf.ProvProxMem = "2048"
	c.Conf.ProvProxDisk = "10"
	h := c.monitorHookProxy(config.ConstProxySqlproxy, "proxysql1", "3306", nil)
	if h.Type != "proxy" || h.Version != "2.6" || h.Flavor != config.ConstProxySqlproxy || h.Units != "1" || h.Unit != "apu" {
		t.Fatalf("proxy hook: %+v", h)
	}
	cnf := &config.AppConfig{AppHost: "pma", AppPort: "80", ProvAppDockerImg: "phpmyadmin:5.2", ProvAppCpuCores: "2", ProvAppMem: "4096", ProvAppDisk: "20"}
	h = c.monitorHookApp(cnf, nil)
	if h.Type != "app" || h.Name != "pma:80" || h.Version != "5.2" || h.Flavor != "phpmyadmin" || h.Units != "2" || h.MemMB != "4096" {
		t.Fatalf("app hook: %+v", h)
	}
	if h := c.monitorHookApp(nil, nil); h.Type != "app" || h.Name != "" {
		t.Fatalf("nil app config must not panic: %+v", h)
	}
}
