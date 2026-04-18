package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

func TestResetAppFromTemplate_ParseErrorDoesNotMutateConfig(t *testing.T) {
	cl := &cluster.Cluster{Conf: &config.Config{WorkingDir: t.TempDir()}}
	node := &cluster.App{
		AppConfig:            seedAppConfigForTemplateResetTests(),
		AppClusterSubstitute: `{}`,
	}

	if err := writeLocalAppTemplate(cl.Conf.WorkingDir, "parse-error", `
prov-app-docker-img = "{{ missing.key }}"
`); err != nil {
		t.Fatalf("writeLocalAppTemplate: %v", err)
	}

	originalDockerImg := node.AppConfig.ProvAppDockerImg
	originalTemplate := node.AppConfig.ProvAppTemplate
	originalDeployment := node.AppConfig.Deployment

	err := resetAppFromTemplateWithProjection(cl, node, "parse-error", false)
	if err == nil {
		t.Fatalf("expected parse error, got nil")
	}

	if node.AppConfig.ProvAppDockerImg != originalDockerImg {
		t.Fatalf("docker image mutated on parse error: got %q want %q", node.AppConfig.ProvAppDockerImg, originalDockerImg)
	}
	if node.AppConfig.ProvAppTemplate != originalTemplate {
		t.Fatalf("template mutated on parse error: got %q want %q", node.AppConfig.ProvAppTemplate, originalTemplate)
	}
	if node.AppConfig.Deployment != originalDeployment {
		t.Fatalf("deployment mutated on parse error")
	}
}

func TestResetAppFromTemplate_ResolveFailureDoesNotMutateConfig(t *testing.T) {
	cl := &cluster.Cluster{Conf: &config.Config{WorkingDir: t.TempDir()}}
	node := &cluster.App{
		AppConfig:            seedAppConfigForTemplateResetTests(),
		AppClusterSubstitute: `{}`,
	}

	if err := writeLocalAppTemplate(cl.Conf.WorkingDir, "resolve-error", `
prov-app-docker-img = "templated/image:1"

[deployment]
[[deployment.paths]]
name = "child"
parentname = "does-not-exist"
dockerpath = "/var/www/html/child"
`); err != nil {
		t.Fatalf("writeLocalAppTemplate: %v", err)
	}

	originalDockerImg := node.AppConfig.ProvAppDockerImg
	originalTemplate := node.AppConfig.ProvAppTemplate
	originalDeployment := node.AppConfig.Deployment

	err := resetAppFromTemplateWithProjection(cl, node, "resolve-error", false)
	if err == nil {
		t.Fatalf("expected resolve failure, got nil")
	}

	if node.AppConfig.ProvAppDockerImg != originalDockerImg {
		t.Fatalf("docker image mutated on resolve failure: got %q want %q", node.AppConfig.ProvAppDockerImg, originalDockerImg)
	}
	if node.AppConfig.ProvAppTemplate != originalTemplate {
		t.Fatalf("template mutated on resolve failure: got %q want %q", node.AppConfig.ProvAppTemplate, originalTemplate)
	}
	if node.AppConfig.Deployment != originalDeployment {
		t.Fatalf("deployment mutated on resolve failure")
	}
}

func TestResetAppFromTemplate_PreservedFieldsRemainPreserved(t *testing.T) {
	cl := &cluster.Cluster{Conf: &config.Config{WorkingDir: t.TempDir()}}
	node := &cluster.App{
		AppConfig:            seedAppConfigForTemplateResetTests(),
		AppClusterSubstitute: `{}`,
	}

	if err := writeLocalAppTemplate(cl.Conf.WorkingDir, "preserve-fields", `
prov-app-service-type = "api"
prov-app-docker-img = "templated/image:2"
prov-app-docker-cmd = "run --foreground"
prov-app-memory = "4G"
prov-app-cpu-cores = "4"
prov-app-disk-size = "50G"
prov-app-disk-type = "ssd"
prov-app-route-addr = "10.0.0.7"
prov-app-route-port = "443"
prov-app-route-mask = "24"
prov-app-agents = "agent-b"
prov-app-ha-topology = "flex"
prov-app-agents-failover = "agent-c"
`); err != nil {
		t.Fatalf("writeLocalAppTemplate: %v", err)
	}

	if err := resetAppFromTemplateWithProjection(cl, node, "preserve-fields", false); err != nil {
		t.Fatalf("resetAppFromTemplateWithProjection: %v", err)
	}

	if node.AppConfig.AppHost != "orig-host" || node.AppConfig.AppPort != "8443" || node.AppConfig.AppHostsIPV6 != "::1" {
		t.Fatalf("app identity fields must be preserved, got host=%q port=%q ipv6=%q", node.AppConfig.AppHost, node.AppConfig.AppPort, node.AppConfig.AppHostsIPV6)
	}
	if node.AppConfig.AppDbUser != "orig-user" || node.AppConfig.AppDbPass != "orig-pass" || node.AppConfig.AppDbSchema != "orig-schema" {
		t.Fatalf("db fields must be preserved, got user=%q pass=%q schema=%q", node.AppConfig.AppDbUser, node.AppConfig.AppDbPass, node.AppConfig.AppDbSchema)
	}
	if !node.AppConfig.AppS3Provider {
		t.Fatalf("app s3 provider flag must be preserved")
	}
	if node.AppConfig.ProvAppCreditUsed != 11 || node.AppConfig.ProvAppCreditPlanned != 22 {
		t.Fatalf("credit fields must be preserved, got used=%d planned=%d", node.AppConfig.ProvAppCreditUsed, node.AppConfig.ProvAppCreditPlanned)
	}
}

func TestResetAppFromTemplate_TemplateOwnedFieldsAreUpdated(t *testing.T) {
	cl := &cluster.Cluster{Conf: &config.Config{WorkingDir: t.TempDir()}}
	node := &cluster.App{
		AppConfig:            seedAppConfigForTemplateResetTests(),
		AppClusterSubstitute: `{}`,
	}

	if err := writeLocalAppTemplate(cl.Conf.WorkingDir, "owned-fields", `
prov-app-service-type = "worker"
prov-app-docker-img = "templated/new:9"
prov-app-docker-cmd = "start --all"
prov-app-memory = "8G"
prov-app-cpu-cores = "8"
prov-app-disk-size = "120G"
prov-app-disk-type = "nvme"
prov-app-route-addr = "192.168.55.10"
prov-app-route-port = "9443"
prov-app-route-mask = "32"
prov-app-agents = "agent-1,agent-2"
prov-app-ha-topology = "failover"
prov-app-agents-failover = "agent-2"

[deployment]
[[deployment.storages.volumes]]
name = "data-volume"
poolname = "data"
volumedir = "data"

[[deployment.paths]]
name = "root"
dockerpath = "/srv/app"
srctype = "volume"
srcname = "data-volume"
srcpath = "."
volumename = "data-volume"
`); err != nil {
		t.Fatalf("writeLocalAppTemplate: %v", err)
	}

	if err := resetAppFromTemplateWithProjection(cl, node, "owned-fields", false); err != nil {
		t.Fatalf("resetAppFromTemplateWithProjection: %v", err)
	}

	if node.AppConfig.ProvAppTemplate != "owned-fields" {
		t.Fatalf("template not updated: got %q", node.AppConfig.ProvAppTemplate)
	}
	if node.AppConfig.ProvAppDockerImg != "templated/new:9" || node.AppConfig.ProvAppDockerCmd != "start --all" {
		t.Fatalf("docker fields not updated: img=%q cmd=%q", node.AppConfig.ProvAppDockerImg, node.AppConfig.ProvAppDockerCmd)
	}
	if node.AppConfig.ProvAppType != "worker" || node.AppConfig.ProvAppMem != "8G" || node.AppConfig.ProvAppCpuCores != "8" {
		t.Fatalf("service sizing fields not updated: type=%q mem=%q cores=%q", node.AppConfig.ProvAppType, node.AppConfig.ProvAppMem, node.AppConfig.ProvAppCpuCores)
	}
	if node.AppConfig.ProvAppDisk != "120G" || node.AppConfig.ProvAppDiskType != "nvme" {
		t.Fatalf("disk fields not updated: size=%q type=%q", node.AppConfig.ProvAppDisk, node.AppConfig.ProvAppDiskType)
	}
	if node.AppConfig.ProvAppRouteAddr != "192.168.55.10" || node.AppConfig.ProvAppRoutePort != "9443" || node.AppConfig.ProvAppRouteMask != "32" {
		t.Fatalf("route fields not updated: addr=%q port=%q mask=%q", node.AppConfig.ProvAppRouteAddr, node.AppConfig.ProvAppRoutePort, node.AppConfig.ProvAppRouteMask)
	}
	if node.AppConfig.ProvAppAgents != "agent-1,agent-2" || node.AppConfig.ProvAppHATopology != "failover" || node.AppConfig.ProvAppAgentsFailover != "agent-2" {
		t.Fatalf("agent/ha fields not updated: agents=%q ha=%q failover=%q", node.AppConfig.ProvAppAgents, node.AppConfig.ProvAppHATopology, node.AppConfig.ProvAppAgentsFailover)
	}
	if node.AppConfig.Deployment == nil || len(node.AppConfig.Deployment.Paths) != 1 || node.AppConfig.Deployment.Paths[0].DockerPath != "/srv/app" {
		t.Fatalf("deployment not updated from template: %+v", node.AppConfig.Deployment)
	}
}

func seedAppConfigForTemplateResetTests() *config.AppConfig {
	return &config.AppConfig{
		ProvAppType:           "old-type",
		ProvAppMem:            "1G",
		ProvAppCpuCores:       "1",
		ProvAppDisk:           "10G",
		ProvAppDiskType:       "hdd",
		ProvAppDockerImg:      "orig/image:1",
		ProvAppDockerCmd:      "orig-cmd",
		ProvAppRouteAddr:      "127.0.0.1",
		ProvAppRoutePort:      "80",
		ProvAppRouteMask:      "0",
		ProvAppTemplate:       "orig-template",
		ProvAppAgents:         "orig-agent",
		ProvAppHATopology:     "orig-ha",
		ProvAppAgentsFailover: "orig-failover",
		ProvAppCreditUsed:     11,
		ProvAppCreditPlanned:  22,
		AppHost:               "orig-host",
		AppHostsIPV6:          "::1",
		AppPort:               "8443",
		AppDbUser:             "orig-user",
		AppDbPass:             "orig-pass",
		AppDbSchema:           "orig-schema",
		AppS3Provider:         true,
		Deployment: &config.Deployment{
			Paths: config.PathMaps{
				{Name: "orig-root", DockerPath: "/orig"},
			},
		},
	}
}

func writeLocalAppTemplate(workingDir, templateName, content string) error {
	path := filepath.Join(workingDir, ".templates", "apps", templateName+".toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}
