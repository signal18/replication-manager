package cluster

import (
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
