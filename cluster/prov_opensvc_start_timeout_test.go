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
	if err := cluster.SetProvDbStartTimeout("soon"); err == nil {
		t.Fatal("a non-duration must be refused")
	}
	if err := cluster.SetProvProxyStartTimeout(" 3m "); err != nil || cluster.Conf.ProvProxyStartTimeout != "3m" {
		t.Fatalf("a duration is stored trimmed: %v %q", err, cluster.Conf.ProvProxyStartTimeout)
	}
}
