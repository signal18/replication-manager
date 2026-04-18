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
