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
