package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// Database, jobs and proxy containers carry the start timeout of their setting, 2m when
// unset: the orchestrator default is 5s and an image pull after a purge exceeds it (#1924).
func TestOpenSVCContainerStartTimeout(t *testing.T) {
	cluster := &Cluster{Conf: &config.Config{ProvType: "docker"}}
	server := &ServerMonitor{ClusterGroup: cluster}
	if got := server.OpenSVCGetDBContainerSection()["start_timeout"]; got != "2m" {
		t.Fatalf("db container default: want 2m, got %q", got)
	}
	if got := server.openSVCGetJobsContainerSection("")["start_timeout"]; got != "2m" {
		t.Fatalf("jobs container default: want 2m, got %q", got)
	}
	cluster.Conf.ProvDbStartTimeout = "5m"
	if got := server.OpenSVCGetDBContainerSection()["start_timeout"]; got != "5m" {
		t.Fatalf("db container follows prov-db-start-timeout: want 5m, got %q", got)
	}
	cluster.Conf.ProvProxyStartTimeout = "90s"
	if got := cluster.proxyStartTimeout(); got != "90s" {
		t.Fatalf("proxy timeout follows prov-proxy-start-timeout: want 90s, got %q", got)
	}
	for _, bad := range []string{"soon", "0", "0s", "-5m", ""} {
		if err := cluster.SetProvDbStartTimeout(bad); err == nil {
			t.Fatalf("%q must be refused: not a positive duration", bad)
		}
	}
	if err := cluster.SetProvProxyStartTimeout(" 3m "); err != nil || cluster.Conf.ProvProxyStartTimeout != "3m" {
		t.Fatalf("a duration is stored trimmed: %v %q", err, cluster.Conf.ProvProxyStartTimeout)
	}
}

// The proxy template's container#prx carries the timeout for every proxy family that goes
// through the section map (all of them: OpenSVCGetProxyTemplateV2 and V3 build on it).
func TestOpenSVCProxyTemplateCarriesStartTimeout(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{ProvProxType: "docker", ProvType: "docker", ProvProxDiskType: "volume", ProvProxyStartTimeout: "3m"}
	proxy := &HaproxyProxy{Proxy: Proxy{ClusterGroup: cluster}}
	sections := cluster.OpenSVCGetProxyTemplateSectionMap("db1:3306", proxy)
	prx, ok := sections["container#prx"]
	if !ok {
		t.Fatal("the proxy template must carry a container#prx section")
	}
	if got := prx["start_timeout"]; got != "3m" {
		t.Fatalf("container#prx start_timeout: want 3m, got %q", got)
	}
}

// The pause container of every kind carries its kind's start timeout, and every service a
// start priority: databases (engine servers included) before proxies before apps.
func TestOpenSVCPauseContainerTimeoutAndPriority(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{ProvType: "docker", ProvProxType: "docker", ProvProxDiskType: "volume", ProvDbStartTimeout: "3m", ProvProxyStartTimeout: "4m", ProvAppStartTimeout: "5m"}
	if got := cluster.OpenSVCGetNamespaceContainerSection(cluster.dbStartTimeout())["start_timeout"]; got != "3m" {
		t.Fatalf("database pause container start_timeout: want 3m, got %q", got)
	}
	if got := cluster.OpenSVCGetNamespaceContainerSection(cluster.proxyStartTimeout())["start_timeout"]; got != "4m" {
		t.Fatalf("proxy pause container start_timeout: want 4m, got %q", got)
	}
	if got := cluster.OpenSVCGetNamespaceContainerSection("")["start_timeout"]; got != "2m" {
		t.Fatalf("unset timeout falls back to 2m, got %q", got)
	}
	srv := cluster.Servers[0]
	srv.ClusterGroup = cluster
	if got := srv.OpenSVCGetDBDefaultSection()["priority"]; got != openSVCPriorityDatabase {
		t.Fatalf("database priority: want %s, got %q", openSVCPriorityDatabase, got)
	}
	prx := &HaproxyProxy{Proxy: Proxy{ClusterGroup: cluster}}
	if got := prx.OpenSVCGetProxyDefaultSection()["priority"]; got != openSVCPriorityProxy {
		t.Fatalf("proxy priority: want %s, got %q", openSVCPriorityProxy, got)
	}
	app := &App{Name: "web1", ClusterGroup: cluster, AppConfig: &config.AppConfig{AppHost: "web1", ProvAppAgents: "n1"}}
	if got := cluster.OpenSVCGetAppDefaultSection(app)["priority"]; got != openSVCPriorityApp {
		t.Fatalf("app priority: want %s, got %q", openSVCPriorityApp, got)
	}
	app.AppConfig.ProvAppConfigurator = "postgres"
	if got := cluster.OpenSVCGetAppDefaultSection(app)["priority"]; got != openSVCPriorityDatabase {
		t.Fatalf("engine server priority: want %s, got %q", openSVCPriorityDatabase, got)
	}
	sections := cluster.OpenSVCGetProxyTemplateSectionMap("db1:3306", prx)
	if got := sections["container#01"]["start_timeout"]; got != "4m" {
		t.Fatalf("proxy template pause container start_timeout: want 4m, got %q", got)
	}
	cluster.Conf.MonitoringSystemResources = true
	if got := cluster.OpenSVCGetSensorContainerSection("database", "db1")["start_timeout"]; got != "3m" {
		t.Fatalf("database sensor start_timeout: want 3m, got %q", got)
	}
	if got := cluster.OpenSVCGetSensorContainerSection(string(KindProxy), "prx1")["start_timeout"]; got != "4m" {
		t.Fatalf("proxy sensor start_timeout: want 4m, got %q", got)
	}
	app.AppConfig.ProvAppStartTimeout = "7m"
	if got := cluster.OpenSVCGetAppSensorContainerSection(app, "k")["start_timeout"]; got != "7m" {
		t.Fatalf("app sensor follows the app's own timeout: want 7m, got %q", got)
	}
	// the database template's container#01 is this same section with dbStartTimeout
	// (GenerateDBTemplateMap needs a live orchestrator for its pools and is not unit-tested)
}
