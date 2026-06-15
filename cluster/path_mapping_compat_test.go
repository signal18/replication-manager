package cluster

import (
	"strconv"
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

	// GetAppVolumeName now returns the saved row's Name directly (the
	// runtime/provisioned identity), not a pool-derived {name}-<pool> name.
	expected := map[string]bool{
		"data-volume:/var/www/html":      false,
		"docs-volume:/var/www/documents": false,
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

// TestGetOpenSVCDeploymentPathMappingUsesPathsOwnVolumeRow covers Phase 5
// task 5: the disk name for a path mapping must come from the volume row
// resolved by the path's own VolumeName (deployment.GetVolumeByName), not
// from a pool-derived lookup. With a legacy, not-yet-canonicalized config
// where multiple rows still share a pool, GetVolumeByPool would return the
// first row for that pool, which can differ from the row the path actually
// references.
func TestGetOpenSVCDeploymentPathMappingUsesPathsOwnVolumeRow(t *testing.T) {
	deployment := config.NewDeploymentConfig()
	deployment.Storages.Volumes = config.Volumes{
		{Name: "first-row", PoolName: "data", VolumeDir: "etc"},
		{Name: "second-row", PoolName: "data", VolumeDir: "data"},
	}
	deployment.Paths = config.PathMaps{
		{
			Name:       "web-root",
			DockerPath: "/var/www/html",
			SourceType: config.SourceVolume,
			SourceName: "second-row",
			SourcePath: ".",
			VolumeName: "second-row",
		},
	}

	if errs := deployment.ResolvePaths(); len(errs) > 0 {
		t.Fatalf("expected deployment paths to resolve, got %v", errs)
	}

	cluster := &Cluster{Name: "test", Conf: &config.Config{}}
	app := &App{Name: "dolibarr", AppConfig: &config.AppConfig{Deployment: deployment}}

	got := cluster.GetOpenSVCDeploymentPathMapping(app)
	want := "second-row:/var/www/html"
	if got != want {
		t.Fatalf("expected mount mapping %q, got %q", want, got)
	}
}

// TestGetOpenSVCDeploymentPathMappingRendersMultiRowSamePoolMounts covers
// Phase 11 task 7: two intentional V2 rows sharing a poolname, each
// referenced by its own deployment.Paths entry, must both resolve and both
// produce their own "<rowName>:<dockerpath>" mount mapping.
func TestGetOpenSVCDeploymentPathMappingRendersMultiRowSamePoolMounts(t *testing.T) {
	deployment := config.NewDeploymentConfig()
	deployment.Storages.Volumes = config.Volumes{
		{Name: "myapp-data", PoolName: "data", VolumeDir: "etc"},
		{Name: "myapp-data-logs", PoolName: "data", VolumeDir: "log"},
	}
	deployment.Paths = config.PathMaps{
		{
			Name:       "web-root",
			DockerPath: "/var/www/html",
			SourceType: config.SourceVolume,
			SourceName: "myapp-data",
			SourcePath: ".",
			VolumeName: "myapp-data",
		},
		{
			Name:       "log-dir",
			DockerPath: "/var/log/app",
			SourceType: config.SourceVolume,
			SourceName: "myapp-data-logs",
			SourcePath: ".",
			VolumeName: "myapp-data-logs",
		},
	}

	if errs := deployment.ResolvePaths(); len(errs) > 0 {
		t.Fatalf("expected deployment paths to resolve, got %v", errs)
	}

	appcnf := &config.AppConfig{AppConfigVersion: config.AppConfigVersionV2, Deployment: deployment}
	cluster := &Cluster{Name: "test", Conf: &config.Config{}}
	app := &App{Name: "myapp", AppConfig: appcnf}

	got := cluster.GetOpenSVCDeploymentPathMapping(app)
	tokens := strings.Fields(got)
	if len(tokens) != 2 {
		t.Fatalf("expected 2 mount mappings, got %d: %q", len(tokens), got)
	}

	expected := map[string]bool{
		"myapp-data:/var/www/html":     false,
		"myapp-data-logs:/var/log/app": false,
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

	volumes := app.GetVolumes(true)
	wantVolumes := []string{"myapp-data", "myapp-data-logs"}
	if len(volumes) != len(wantVolumes) || volumes[0] != wantVolumes[0] || volumes[1] != wantVolumes[1] {
		t.Fatalf("expected GetVolumes() = %v, got %v", wantVolumes, volumes)
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

// TestOpenSVCAppVolumeSection_MultiDirectoryVolumeDir covers Phase 5/9: a
// merged multi-pool volume row (VolumeDir = "etc log var data") must be
// rendered as separate OpenSVC "directories" entries, not a single literal
// directory name containing spaces.
func TestOpenSVCAppVolumeSection_MultiDirectoryVolumeDir(t *testing.T) {
	vol := &config.Volume{Name: "myapp-data", PoolName: "data", VolumeDir: "etc log var data"}

	got := openSVCAppVolumeSection(vol, nil, true)

	if got["name"] != "myapp-data" {
		t.Fatalf("expected name myapp-data, got %q", got["name"])
	}
	if got["pool"] != "data" {
		t.Fatalf("expected pool data, got %q", got["pool"])
	}
	if got["shared"] != "true" {
		t.Fatalf("expected shared=true for shared pool, got %q", got["shared"])
	}
	if want := "data etc log var"; got["directories"] != want {
		t.Fatalf("expected directories %q, got %q", want, got["directories"])
	}
}

// TestOpenSVCAppVolumeSection_PathDerivedDirectoryMergedWithVolumeDir covers
// merging VolumeDir tokens with directories derived from
// deployment.Paths.GetVolumeDirs(), deduplicated and sorted, and confirms the
// "shared" key is omitted for a non-shared pool.
func TestOpenSVCAppVolumeSection_PathDerivedDirectoryMergedWithVolumeDir(t *testing.T) {
	vol := &config.Volume{Name: "myapp-data", PoolName: "data", VolumeDir: "data"}
	pathmap := map[string][]string{
		"myapp-data": {"data/uploads/file.txt", "logs/"},
	}

	got := openSVCAppVolumeSection(vol, pathmap, false)

	if _, ok := got["shared"]; ok {
		t.Fatalf("expected no shared key for non-shared pool, got %q", got["shared"])
	}
	if want := "data data/uploads logs"; got["directories"] != want {
		t.Fatalf("expected directories %q, got %q", want, got["directories"])
	}
}

// TestOpenSVCAppVolumeSection_MultiRowSamePoolDistinctSections covers Phase
// 11 task 8: two intentional V2 rows sharing a poolname must each produce
// their own "volume#N" section -- mirroring the per-row loop in
// OpenSVCGetAppVolumeSections -- with distinct name/directories but the same
// pool.
func TestOpenSVCAppVolumeSection_MultiRowSamePoolDistinctSections(t *testing.T) {
	volumes := config.Volumes{
		{Name: "myapp-data", PoolName: "data", VolumeDir: "etc"},
		{Name: "myapp-data-logs", PoolName: "data", VolumeDir: "log"},
	}

	basemap := make(map[string]map[string]string)
	for i, vol := range volumes {
		basemap["volume#"+strconv.Itoa(i+1)] = openSVCAppVolumeSection(vol, nil, true)
	}

	first := basemap["volume#1"]
	second := basemap["volume#2"]

	if first["name"] != "myapp-data" || second["name"] != "myapp-data-logs" {
		t.Fatalf("expected distinct names, got %q and %q", first["name"], second["name"])
	}
	if first["pool"] != "data" || second["pool"] != "data" {
		t.Fatalf("expected both rows to share pool %q, got %q and %q", "data", first["pool"], second["pool"])
	}
	if first["directories"] != "etc" || second["directories"] != "log" {
		t.Fatalf("expected distinct directories, got %q and %q", first["directories"], second["directories"])
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
