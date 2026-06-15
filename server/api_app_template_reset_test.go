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

// TestResetAppFromTemplate_VolumeNamesResolvedToAppName covers Phase 7: the
// reset/preview projection must run the same canonical volume merge as
// AddSeededApp/LoadAppConfig (CanonicalizeAppContent with appName =
// node.Name), so a template-owned volume row ends up named "<app>-<pool>"
// (the resolved-app convention) rather than left as the template's
// "{name}-<pool>" placeholder or its original pre-canonical name.
func TestResetAppFromTemplate_VolumeNamesResolvedToAppName(t *testing.T) {
	cl := &cluster.Cluster{Conf: &config.Config{WorkingDir: t.TempDir()}}
	node := &cluster.App{
		Name:                 "myapp",
		AppConfig:            seedAppConfigForTemplateResetTests(),
		AppClusterSubstitute: `{}`,
	}

	if err := writeLocalAppTemplate(cl.Conf.WorkingDir, "vol-naming", `
prov-app-docker-img = "templated/new:9"

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

	if err := resetAppFromTemplateWithProjection(cl, node, "vol-naming", false); err != nil {
		t.Fatalf("resetAppFromTemplateWithProjection: %v", err)
	}

	dep := node.AppConfig.Deployment
	if dep == nil || len(dep.Storages.Volumes) != 1 {
		t.Fatalf("expected 1 merged volume row, got %+v", dep)
	}
	if got := dep.Storages.Volumes[0].Name; got != "myapp-data" {
		t.Fatalf("expected volume name resolved to myapp-data, got %q", got)
	}

	if len(dep.Paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(dep.Paths))
	}
	if got := dep.Paths[0].SourceName; got != "myapp-data" {
		t.Fatalf("expected path srcname resolved to myapp-data, got %q", got)
	}
	if got := dep.Paths[0].VolumeName; got != "myapp-data" {
		t.Fatalf("expected path volumename resolved to myapp-data, got %q", got)
	}
}

// TestResetAppFromTemplate_V2MultiRowSamePoolPreserved is a Phase 11
// follow-up to TestResetAppFromTemplate_VolumeNamesResolvedToAppName: a
// V2-flagged template with two intentional same-pool volume rows (plus a
// git-clone and an s3-mount referencing those rows) must reset onto an app
// with both rows preserved -- not merged into the single "<app>-<pool>" row
// the V1 path produces -- and with each path/git-clone/s3-mount reference
// staying pointed at its own distinct row.
func TestResetAppFromTemplate_V2MultiRowSamePoolPreserved(t *testing.T) {
	cl := &cluster.Cluster{Conf: &config.Config{WorkingDir: t.TempDir()}}
	node := &cluster.App{
		Name:                 "myapp",
		AppConfig:            seedAppConfigForTemplateResetTests(),
		AppClusterSubstitute: `{}`,
	}

	if err := writeLocalAppTemplate(cl.Conf.WorkingDir, "v2-multi-row", `
app-config-version = 2

prov-app-docker-img = "templated/new:9"

[deployment]
[[deployment.storages.volumes]]
name = "myapp-data"
poolname = "data"
volumedir = "data"

[[deployment.storages.volumes]]
name = "myapp-data-logs"
poolname = "data"
volumedir = "logs"

[[deployment.storages.git-clones]]
name = "app-src"
volumename = "myapp-data"
volumedir = "data/app-src"

[[deployment.storages.s3-mounts]]
name = "media"
volumename = "myapp-data-logs"
volumedir = "logs/media"

[[deployment.paths]]
name = "root"
dockerpath = "/srv/app"
srctype = "volume"
srcname = "myapp-data"
srcpath = "."
volumename = "myapp-data"
level = 0

[[deployment.paths]]
name = "log-dir"
dockerpath = "/srv/log"
srctype = "volume"
srcname = "myapp-data-logs"
srcpath = "."
volumename = "myapp-data-logs"
level = 0
`); err != nil {
		t.Fatalf("writeLocalAppTemplate: %v", err)
	}

	if err := resetAppFromTemplateWithProjection(cl, node, "v2-multi-row", false); err != nil {
		t.Fatalf("resetAppFromTemplateWithProjection: %v", err)
	}

	if got := node.AppConfig.AppConfigVersion; got != config.AppConfigVersionV2 {
		t.Fatalf("expected AppConfigVersion %d, got %d", config.AppConfigVersionV2, got)
	}

	dep := node.AppConfig.Deployment
	if dep == nil || len(dep.Storages.Volumes) != 2 {
		t.Fatalf("expected both volume rows preserved, got %+v", dep)
	}
	if dep.Storages.Volumes[0].Name != "myapp-data" || dep.Storages.Volumes[1].Name != "myapp-data-logs" {
		t.Fatalf("expected distinct volume row names myapp-data/myapp-data-logs, got %q/%q",
			dep.Storages.Volumes[0].Name, dep.Storages.Volumes[1].Name)
	}
	if dep.Storages.Volumes[0].VolumeDir != "data" || dep.Storages.Volumes[1].VolumeDir != "logs" {
		t.Fatalf("expected volumedir left unmerged (data/logs), got %q/%q",
			dep.Storages.Volumes[0].VolumeDir, dep.Storages.Volumes[1].VolumeDir)
	}

	if len(dep.Storages.GitClones) != 1 || dep.Storages.GitClones[0].VolumeName != "myapp-data" {
		t.Fatalf("expected git-clone to keep referencing myapp-data, got %+v", dep.Storages.GitClones)
	}
	if len(dep.Storages.S3Mounts) != 1 || dep.Storages.S3Mounts[0].VolumeName != "myapp-data-logs" {
		t.Fatalf("expected s3-mount to keep referencing myapp-data-logs, got %+v", dep.Storages.S3Mounts)
	}
	// Phase 15 task 4: explicit non-"mnt" S3 placement must survive the reset
	// flow unchanged, not be snapped back to a "mnt/..." default.
	if dep.Storages.S3Mounts[0].VolumeDir != "logs/media" {
		t.Fatalf("expected s3-mount explicit volumedir \"logs/media\" preserved, got %q", dep.Storages.S3Mounts[0].VolumeDir)
	}

	if len(dep.Paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(dep.Paths))
	}
	srcnames := map[string]string{}
	for _, p := range dep.Paths {
		srcnames[p.Name] = p.SourceName
	}
	if srcnames["root"] != "myapp-data" || srcnames["log-dir"] != "myapp-data-logs" {
		t.Fatalf("expected distinct path srcnames, got %+v", srcnames)
	}
}

// TestBuildValidatedTempAppConfigFromTemplate_StampsAppConfigVersion covers
// Phase 10 task 5 for the template reset/preview flow: the temp config built
// from an unversioned template's canonicalized content (CanonicalizeAppContent
// via buildValidatedTempAppConfigFromTemplate) is flagged with
// AppConfigVersion = config.AppConfigVersionV2.
func TestBuildValidatedTempAppConfigFromTemplate_StampsAppConfigVersion(t *testing.T) {
	cl := &cluster.Cluster{Conf: &config.Config{WorkingDir: t.TempDir()}}
	node := &cluster.App{
		Name:                 "myapp",
		AppConfig:            seedAppConfigForTemplateResetTests(),
		AppClusterSubstitute: `{}`,
	}

	if err := writeLocalAppTemplate(cl.Conf.WorkingDir, "version-stamp", `
prov-app-docker-img = "templated/new:9"

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

	tempConfig, err := buildValidatedTempAppConfigFromTemplate(cl, node, "version-stamp", false)
	if err != nil {
		t.Fatalf("buildValidatedTempAppConfigFromTemplate: %v", err)
	}
	if got := tempConfig.AppConfigVersion; got != config.AppConfigVersionV2 {
		t.Fatalf("expected AppConfigVersion %d, got %d", config.AppConfigVersionV2, got)
	}
}

func TestBuildTemplateProjectionImpact_ReportsChangedTemplateOwnedFields(t *testing.T) {
	current := seedAppConfigForTemplateResetTests()
	projected := seedAppConfigForTemplateResetTests()
	projected.ProvAppDockerImg = "templated/new:1"
	projected.ProvAppRoutePort = "443"
	projected.Deployment = &config.Deployment{
		Paths: config.PathMaps{{Name: "new-root", DockerPath: "/srv/new"}},
	}

	changes := buildTemplateProjectionImpact(current, projected, "new-template")
	if len(changes) == 0 {
		t.Fatalf("expected non-empty change set")
	}

	hasField := func(field string) bool {
		for _, c := range changes {
			if c.Field == field {
				return true
			}
		}
		return false
	}

	if !hasField("ProvAppTemplate") {
		t.Fatalf("expected ProvAppTemplate in changes")
	}
	if !hasField("ProvAppDockerImg") {
		t.Fatalf("expected ProvAppDockerImg in changes")
	}
	if !hasField("ProvAppRoutePort") {
		t.Fatalf("expected ProvAppRoutePort in changes")
	}
	if !hasField("Deployment") {
		t.Fatalf("expected Deployment in changes")
	}

	if hasField("AppHost") || hasField("AppPort") || hasField("AppDbUser") {
		t.Fatalf("preserved fields should not be reported in template impact")
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
