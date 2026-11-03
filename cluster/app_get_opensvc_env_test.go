package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestGetOpenSVCDeploymentAppEnvUsesWildcardMapReference(t *testing.T) {
	app := &App{Name: "app1"}

	if got := app.GetOpenSVCDeploymentAppEnv(config.VariableTypeSecret); got != "app1/*" {
		t.Fatalf("expected wildcard secret map reference, got %q", got)
	}

	if got := app.GetOpenSVCDeploymentAppEnv(config.VariableTypeEnv); got != "app1/*" {
		t.Fatalf("expected wildcard env map reference, got %q", got)
	}

	if got := app.GetOpenSVCDeploymentAppEnv("other"); got != "" {
		t.Fatalf("expected empty map reference for unsupported type, got %q", got)
	}
}

func TestOpenSVCGetAppContainerSectionUsesWildcardEnvironmentReferences(t *testing.T) {
	cluster := &Cluster{Conf: &config.Config{ProvType: "docker"}}
	app := &App{
		Name:         "app1",
		ClusterGroup: cluster,
		AppConfig: &config.AppConfig{
			Deployment: config.NewDeploymentConfig(),
		},
	}

	section := cluster.OpenSVCGetAppContainerSection(app)

	if got := section["secrets_environment"]; got != "app1/*" {
		t.Fatalf("expected wildcard secrets_environment, got %q", got)
	}

	if got := section["configs_environment"]; got != "app1/*" {
		t.Fatalf("expected wildcard configs_environment, got %q", got)
	}
}

func TestOpenSVCGetAppGitInitContainerSectionUsesWildcardEnvironmentReferences(t *testing.T) {
	cluster := &Cluster{Conf: &config.Config{ProvType: "docker"}}
	app := &App{Name: "app1", ClusterGroup: cluster}

	section := cluster.OpenSVCGetAppGitInitContainerSection(app, &config.GitClone{Name: "repo"})

	if got := section["secrets_environment"]; got != "app1/*" {
		t.Fatalf("expected wildcard secrets_environment, got %q", got)
	}

	if got := section["configs_environment"]; got != "app1/*" {
		t.Fatalf("expected wildcard configs_environment, got %q", got)
	}
}

func TestOpenSVCGetAppS3MountContainerSectionUsesWildcardEnvironmentReferences(t *testing.T) {
	cluster := &Cluster{Conf: &config.Config{ProvType: "docker"}}
	app := &App{Name: "app1", ClusterGroup: cluster}

	section := cluster.OpenSVCGetAppS3MountContainerSection(app, &config.S3Mount{Name: "bucket"})

	if got := section["secrets_environment"]; got != "app1/*" {
		t.Fatalf("expected wildcard secrets_environment, got %q", got)
	}

	if got := section["configs_environment"]; got != "app1/*" {
		t.Fatalf("expected wildcard configs_environment, got %q", got)
	}
}

// TestOpenSVCGetAppS3MountContainerSectionMountsChosenVolumePlacement covers
// Phase 15 task 7: the generated OpenSVC S3 mount container must mount
// "<VolumeName>/<VolumeDir>:/mnt" using the chosen saved volume row and
// directory, including a resolved Volume pointer with a non-"mnt" explicit
// V2 placement.
func TestOpenSVCGetAppS3MountContainerSectionMountsChosenVolumePlacement(t *testing.T) {
	cluster := &Cluster{Conf: &config.Config{ProvType: "docker"}}
	app := &App{Name: "app1", ClusterGroup: cluster}

	vol := &config.Volume{Name: "myapp-logs", PoolName: "logs", VolumeDir: "log"}
	s3m := &config.S3Mount{Name: "media", VolumeName: "myapp-logs", VolumeDir: "log/media", Volume: vol}

	section := cluster.OpenSVCGetAppS3MountContainerSection(app, s3m)

	want := "myapp-logs/log/media:/mnt:rw,rshared"
	if got := section["volume_mounts"]; got != want {
		t.Fatalf("expected volume_mounts %q, got %q", want, got)
	}
}

// TestOpenSVCGetAppS3MountContainerSectionMountsVolumeNameWhenUnresolved
// covers the lazily-resolved fallback in S3Mount.GetSourceVolumeName(): when
// Volume has not yet been resolved to a pointer, the container mount still
// uses VolumeName/VolumeDir as persisted.
func TestOpenSVCGetAppS3MountContainerSectionMountsVolumeNameWhenUnresolved(t *testing.T) {
	cluster := &Cluster{Conf: &config.Config{ProvType: "docker"}}
	app := &App{Name: "app1", ClusterGroup: cluster}

	s3m := &config.S3Mount{Name: "media", VolumeName: "myapp-logs", VolumeDir: "log/media"}

	section := cluster.OpenSVCGetAppS3MountContainerSection(app, s3m)

	want := "myapp-logs/log/media:/mnt:rw,rshared"
	if got := section["volume_mounts"]; got != want {
		t.Fatalf("expected volume_mounts %q, got %q", want, got)
	}
}

// #2282: a per-agent value reaches the container through a keyword scoped to that agent,
// mapping <NAME>@<agent> onto <NAME>, on top of the default import.
func conditionalTestApp(vars config.VariableMaps, dbCreds bool) (*Cluster, *App) {
	cluster := &Cluster{Conf: &config.Config{ProvType: "docker"}}
	dep := config.NewDeploymentConfig()
	dep.Variables = vars
	app := &App{
		Name:         "app1",
		ClusterGroup: cluster,
		AppConfig:    &config.AppConfig{Deployment: dep, ProvAppDbCredentials: dbCreds},
	}
	return cluster, app
}

func TestOpenSVCGetAppContainerSectionNoConditionalNoScopedKeyword(t *testing.T) {
	cluster, app := conditionalTestApp(config.VariableMaps{{Name: "X", Value: "x", Type: config.VariableTypeEnv}}, false)
	for k := range cluster.OpenSVCGetAppContainerSection(app) {
		if strings.Contains(k, "@") {
			t.Fatalf("unexpected scoped keyword %q without per-agent values", k)
		}
	}
}

func TestOpenSVCGetAppContainerSectionScopesEnvConditional(t *testing.T) {
	cluster, app := conditionalTestApp(config.VariableMaps{
		{Name: "DB_HOST", Value: "prx0", Type: config.VariableTypeEnv, Conditional: config.AVSlice{{Agent: "n1", Value: "prx1"}, {Agent: "n2", Value: "prx2"}}},
		{Name: "PORT", Value: "3306", Type: config.VariableTypeEnv, Conditional: config.AVSlice{{Agent: "n1", Value: "3307"}}},
	}, false)
	section := cluster.OpenSVCGetAppContainerSection(app)

	want := map[string]string{
		"configs_environment":    "app1/*",
		"configs_environment@n1": "app1/* DB_HOST=app1/DB_HOST@n1 PORT=app1/PORT@n1",
		"configs_environment@n2": "app1/* DB_HOST=app1/DB_HOST@n2",
		"secrets_environment":    "app1/*",
	}
	for k, v := range want {
		if got := section[k]; got != v {
			t.Fatalf("%s: expected %q, got %q", k, v, got)
		}
	}
	for _, k := range []string{"secrets_environment@n1", "secrets_environment@n2"} {
		if _, ok := section[k]; ok {
			t.Fatalf("unexpected %s for env-only per-agent values", k)
		}
	}
}

func TestOpenSVCGetAppContainerSectionScopesSecretConditional(t *testing.T) {
	cluster, app := conditionalTestApp(config.VariableMaps{
		{Name: "PASS", Value: "p", Type: config.VariableTypeSecret, Conditional: config.AVSlice{{Agent: "n1", Value: "p1"}}},
	}, true)
	section := cluster.OpenSVCGetAppContainerSection(app)

	if got, want := section["secrets_environment@n1"], "app1/* env/MYSQL_ROOT_PASSWORD PASS=app1/PASS@n1"; got != want {
		t.Fatalf("secrets_environment@n1: expected %q, got %q", want, got)
	}
	if _, ok := section["configs_environment@n1"]; ok {
		t.Fatal("unexpected configs_environment@n1 for a secret-only per-agent value")
	}
}

func TestOpenSVCGetAppContainerSectionSkipsUnmappableConditional(t *testing.T) {
	cluster, app := conditionalTestApp(config.VariableMaps{
		{Name: "X", Type: config.VariableTypeEnv, Conditional: config.AVSlice{{Agent: "", Value: "a"}, {Agent: "bad node", Value: "b"}}},
		{Name: "BAD NAME", Type: config.VariableTypeEnv, Conditional: config.AVSlice{{Agent: "n1", Value: "c"}}},
	}, false)
	for k := range cluster.OpenSVCGetAppContainerSection(app) {
		if strings.Contains(k, "@") {
			t.Fatalf("unexpected scoped keyword %q for an unmappable name or agent", k)
		}
	}
}

func TestStaleConditionalKeys(t *testing.T) {
	vars := config.VariableMaps{
		{Name: "DB_HOST", Type: config.VariableTypeEnv, Conditional: config.AVSlice{{Agent: "n1"}}},
		{Name: "PASS", Type: config.VariableTypeSecret, Conditional: config.AVSlice{{Agent: "n2"}}},
		{Name: "MAIL@HOST", Type: config.VariableTypeEnv},
	}
	cfgKeys := []string{"DB_HOST", "DB_HOST@n1", "DB_HOST@n2", "PASS@n1", "MAIL@HOST", "GIT_REPO", "OTHER@n2"}
	if got, want := strings.Join(staleConditionalKeys(vars, cfgKeys, false), ","), "DB_HOST@n2"; got != want {
		t.Fatalf("cfg: expected stale %q, got %q", want, got)
	}
	secKeys := []string{"PASS", "PASS@n1", "PASS@n2", "DB_HOST@n2"}
	if got, want := strings.Join(staleConditionalKeys(vars, secKeys, true), ","), "PASS@n1"; got != want {
		t.Fatalf("sec: expected stale %q, got %q", want, got)
	}
}

// #2282 on OpenSVC v2: the environment keywords set on the live object are the ones the
// template renders; the per-agent keywords of nodes without a value are unset.
func TestAppEnvironmentKeywordsV2(t *testing.T) {
	section := map[string]string{
		"configs_environment":    "app1/*",
		"configs_environment@n1": "app1/* X=app1/X@n1",
		"configs_environment@n3": "app1/* X=app1/X@n3", // an agent that is not a cluster node
		"secrets_environment":    "app1/*",
		"image":                  "{env.app_img}",
	}
	set, unset := appEnvironmentKeywordsV2(section, []string{"n1", "n2"})
	wantSet := []string{
		"container#app.configs_environment=app1/*",
		"container#app.configs_environment@n1=app1/* X=app1/X@n1",
		"container#app.configs_environment@n3=app1/* X=app1/X@n3",
		"container#app.secrets_environment=app1/*",
	}
	wantUnset := []string{
		"container#app.configs_environment@n2",
		"container#app.secrets_environment@n1",
		"container#app.secrets_environment@n2",
	}
	if strings.Join(set, "\n") != strings.Join(wantSet, "\n") {
		t.Fatalf("set:\n%s\nwant:\n%s", strings.Join(set, "\n"), strings.Join(wantSet, "\n"))
	}
	if strings.Join(unset, "\n") != strings.Join(wantUnset, "\n") {
		t.Fatalf("unset:\n%s\nwant:\n%s", strings.Join(unset, "\n"), strings.Join(wantUnset, "\n"))
	}
}
