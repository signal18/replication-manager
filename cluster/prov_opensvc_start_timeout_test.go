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
