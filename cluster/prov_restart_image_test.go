package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// #1861: a rolling restart renders the deployment with the image the service runs,
// read from the orchestrator's current configuration, never from prov-db-image.
func TestOpenSVCConfigValue(t *testing.T) {
	raw := "[DEFAULT]\nnodes = n1\n\n[env]\n# comment\nnodes = n1\nsize = 20g\ndocker_image = mariadb:11.8\n\n[container#db]\nimage = {env.docker_image}\n"
	if v := openSVCConfigValue(raw, "env", "docker_image"); v != "mariadb:11.8" {
		t.Fatalf("env.docker_image: %q", v)
	}
	if v := openSVCConfigValue(raw, "container#db", "image"); v != "{env.docker_image}" {
		t.Fatalf("container image: %q", v)
	}
	if v := openSVCConfigValue(raw, "env", "missing"); v != "" {
		t.Fatalf("missing key must be empty: %q", v)
	}
	if v := openSVCConfigValue(raw, "DEFAULT", "nodes"); v != "n1" {
		t.Fatalf("explicit [DEFAULT]: %q", v)
	}
	// om3 rewrites DEFAULT without its header: the leading keys are DEFAULT's, and only theirs
	om3 := "nodes        = n1 n2\npg_cpu_quota = 100%\npg_mem_limit = 536870912\n\n[container#app]\npg_mem_limit = 1\nimage = php\n"
	if v := openSVCConfigValue(om3, "DEFAULT", "pg_mem_limit"); v != "536870912" {
		t.Fatalf("header-less DEFAULT: %q", v)
	}
	if v := openSVCConfigValue(om3, "container#app", "pg_mem_limit"); v != "1" {
		t.Fatalf("a section key after the DEFAULT block: %q", v)
	}
	if v := openSVCConfigValue(om3, "env", "pg_mem_limit"); v != "" {
		t.Fatalf("the DEFAULT block is not another section's: %q", v)
	}
	if m, p, ok := appCapFromDefinition(om3); !ok || m != 512 || p != 100 {
		t.Fatalf("cap in force from an om3-rewritten definition: %d %d %v", m, p, ok)
	}
}

func TestDBEnvSectionHonoursImageOverride(t *testing.T) {
	c := &Cluster{Name: "t", Conf: &config.Config{ProvDbImg: "mariadb:latest"}}
	s := &ServerMonitor{ClusterGroup: c, DeployImageOverride: "mariadb:11.8"}
	if img := s.deployImage(); img != "mariadb:11.8" {
		t.Fatalf("override must win: %s", img)
	}
	s.DeployImageOverride = ""
	if img := s.deployImage(); img != "mariadb:latest" {
		t.Fatalf("without override the declared image: %s", img)
	}
}
