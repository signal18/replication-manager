package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestGetOpenSVCDeploymentPathMappingRendersMultiVolumeMounts(t *testing.T) {
	deployment := config.NewDeploymentConfig()
	deployment.Storages.Volumes = config.Volumes{
		{Name: "data-volume", PoolName: "data", VolumeDir: "data"},
		{Name: "docs-volume", PoolName: "docs", VolumeDir: "docs"},
	}
	deployment.Paths = config.PathMaps{
		{
			Name:       "web-root",
			DockerPath: "/var/www/html",
			SourceType: config.SourceVolume,
			SourceName: "data-volume",
			SourcePath: ".",
			VolumeName: "data-volume",
		},
		{
			Name:       "documents",
			DockerPath: "/var/www/documents",
			SourceType: config.SourceVolume,
			SourceName: "docs-volume",
			SourcePath: ".",
			VolumeName: "docs-volume",
		},
	}

	if errs := deployment.ResolvePaths(); len(errs) > 0 {
		t.Fatalf("expected deployment paths to resolve, got %v", errs)
	}

	cluster := &Cluster{Name: "test", Conf: &config.Config{}}
	app := &App{Name: "dolibarr", AppConfig: &config.AppConfig{Deployment: deployment}}

	got := cluster.GetOpenSVCDeploymentPathMapping(app)
	tokens := strings.Fields(got)
	if len(tokens) != 2 {
		t.Fatalf("expected 2 mount mappings, got %d: %q", len(tokens), got)
	}

	expected := map[string]bool{
		"{name}-data:/var/www/html":      false,
		"{name}-docs:/var/www/documents": false,
	}
	for _, token := range tokens {
		if _, ok := expected[token]; ok {
			expected[token] = true
		}
	}
	for token, found := range expected {
		if !found {
			t.Fatalf("expected mount mapping %q in %q", token, got)
		}
	}
}

func TestResolvePathsParentNameByPathName(t *testing.T) {
	deployment := config.NewDeploymentConfig()
	deployment.Storages.Volumes = config.Volumes{
		{Name: "data-volume", PoolName: "data", VolumeDir: "data"},
	}

	parent := &config.PathMapping{
		Name:       "web-root",
		DockerPath: "/var/www/html",
		SourceType: config.SourceVolume,
		SourceName: "data-volume",
		SourcePath: ".",
		VolumeName: "data-volume",
	}
	child := &config.PathMapping{
		Name:       "assets",
		ParentName: "web-root",
		DockerPath: "/var/www/html/assets",
	}

	deployment.Paths = config.PathMaps{parent, child}
	if errs := deployment.ResolvePaths(); len(errs) > 0 {
		t.Fatalf("expected deployment paths to resolve, got %v", errs)
	}

	if child.Parent != parent {
		t.Fatalf("expected parent to resolve by name, got %#v", child.Parent)
	}
	if child.VolumeName != parent.VolumeName {
		t.Fatalf("expected child volume %q to inherit from parent %q", child.VolumeName, parent.VolumeName)
	}
}

func TestResolvePathsParentNameByDockerPath_IsRejected(t *testing.T) {
	deployment := config.NewDeploymentConfig()
	deployment.Storages.Volumes = config.Volumes{
		{Name: "data-volume", PoolName: "data", VolumeDir: "data"},
	}

	parent := &config.PathMapping{
		Name:       "web-root",
		DockerPath: "/var/www/html",
		SourceType: config.SourceVolume,
		SourceName: "data-volume",
		SourcePath: ".",
		VolumeName: "data-volume",
	}
	child := &config.PathMapping{
		Name:       "assets",
		ParentName: "/var/www/html",
		DockerPath: "/var/www/html/assets",
	}

	deployment.Paths = config.PathMaps{parent, child}
	if errs := deployment.ResolvePaths(); len(errs) == 0 {
		t.Fatalf("expected resolve error for dockerpath-based parentname, got nil")
	}
}

func TestResolvePathsRootSourcePathSlash_IsRejected(t *testing.T) {
	deployment := config.NewDeploymentConfig()
	deployment.Storages.Volumes = config.Volumes{
		{Name: "data-volume", PoolName: "data", VolumeDir: "data"},
	}

	path := &config.PathMapping{
		Name:       "web-root",
		DockerPath: "/var/www/html",
		SourceType: config.SourceVolume,
		SourceName: "data-volume",
		SourcePath: "/",
		VolumeName: "data-volume",
	}
	deployment.Paths = config.PathMaps{path}

	if errs := deployment.ResolvePaths(); len(errs) == 0 {
		t.Fatalf("expected resolve error for srcpath='/', got nil")
	}
}

func TestPathMapsSort_UsesLevelThenPath(t *testing.T) {
	paths := config.PathMaps{
		{Name: "child-b", Level: 1, ParentName: "root", DockerPath: "/root/b"},
		{Name: "root", Level: 0, DockerPath: "/root"},
		{Name: "child-a", Level: 1, ParentName: "root", DockerPath: "/root/a"},
	}

	paths.Sort()

	if paths[0].Name != "root" {
		t.Fatalf("expected root path first, got %q", paths[0].Name)
	}
	if paths[1].Name != "child-a" || paths[2].Name != "child-b" {
		t.Fatalf("expected level-1 paths sorted by dockerpath, got %q then %q", paths[1].Name, paths[2].Name)
	}
}
