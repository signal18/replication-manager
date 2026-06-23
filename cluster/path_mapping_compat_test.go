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

// TestResolvePath_DirectVolumeSourceChange_UpdatesVolumeName covers Path
// Storage Mapping Fix Plan Test Plan item 1: switching a direct volume path
// from one saved volume row to another must refresh VolumeName from the new
// source, not retain the old binding.
func TestResolvePath_DirectVolumeSourceChange_UpdatesVolumeName(t *testing.T) {
	deployment := config.NewDeploymentConfig()
	deployment.Storages.Volumes = config.Volumes{
		{Name: "vol-a", PoolName: "a", VolumeDir: "data"},
		{Name: "vol-b", PoolName: "b", VolumeDir: "docs"},
	}

	path := &config.PathMapping{
		Name:       "web-root",
		DockerPath: "/var/www/html",
		SourceType: config.SourceVolume,
		SourceName: "vol-a",
		SourcePath: ".",
		VolumeName: "vol-a",
	}
	deployment.Paths = config.PathMaps{path}

	if errs := deployment.ResolvePaths(); len(errs) > 0 {
		t.Fatalf("expected initial resolve to succeed, got %v", errs)
	}
	if path.VolumeName != "vol-a" {
		t.Fatalf("expected initial volumename vol-a, got %q", path.VolumeName)
	}

	// Simulate the "srcname" modify branch switching the direct volume source.
	path.SourceName = "vol-b"
	path.SourcePath = "."
	if err := deployment.ResolvePath(path); err != nil {
		t.Fatalf("expected resolve to succeed, got %v", err)
	}
	if path.VolumeName != "vol-b" {
		t.Fatalf("expected volumename to follow source change to vol-b, got %q", path.VolumeName)
	}
}

// TestResolvePath_GitSourceChange_UpdatesVolumeName covers Test Plan item 1: a
// git-backed path switched to a git clone hosted on a different volume row
// must refresh VolumeName accordingly.
func TestResolvePath_GitSourceChange_UpdatesVolumeName(t *testing.T) {
	deployment := config.NewDeploymentConfig()
	deployment.Storages.Volumes = config.Volumes{
		{Name: "vol-a", PoolName: "a", VolumeDir: "data"},
		{Name: "vol-b", PoolName: "b", VolumeDir: "docs"},
	}
	gitA := &config.GitClone{Name: "git-a", VolumeName: "vol-a", VolumeDir: "repo-a"}
	gitB := &config.GitClone{Name: "git-b", VolumeName: "vol-b", VolumeDir: "repo-b"}
	deployment.Storages.GitClones = config.GitClones{gitA, gitB}

	path := &config.PathMapping{
		Name:       "src",
		DockerPath: "/app/src",
		SourceType: config.SourceGit,
		SourceName: "git-a",
		SourcePath: gitA.GetSourcePath(),
		VolumeName: "vol-a",
	}
	deployment.Paths = config.PathMaps{path}

	if errs := deployment.ResolvePaths(); len(errs) > 0 {
		t.Fatalf("expected initial resolve to succeed, got %v", errs)
	}
	if path.VolumeName != "vol-a" {
		t.Fatalf("expected initial volumename vol-a, got %q", path.VolumeName)
	}

	path.SourceName = "git-b"
	path.SourcePath = gitB.GetSourcePath()
	if err := deployment.ResolvePath(path); err != nil {
		t.Fatalf("expected resolve to succeed, got %v", err)
	}
	if path.VolumeName != "vol-b" {
		t.Fatalf("expected volumename to follow git source change to vol-b, got %q", path.VolumeName)
	}
}

// TestResolvePath_S3SourceChange_UpdatesVolumeName covers Test Plan item 1: an
// s3-backed path switched to an s3 mount hosted on a different volume row
// must refresh VolumeName accordingly.
func TestResolvePath_S3SourceChange_UpdatesVolumeName(t *testing.T) {
	deployment := config.NewDeploymentConfig()
	deployment.Storages.Volumes = config.Volumes{
		{Name: "vol-a", PoolName: "a", VolumeDir: "data"},
		{Name: "vol-b", PoolName: "b", VolumeDir: "docs"},
	}
	s3A := &config.S3Mount{Name: "s3-a", VolumeName: "vol-a", VolumeDir: "uploads-a"}
	s3B := &config.S3Mount{Name: "s3-b", VolumeName: "vol-b", VolumeDir: "uploads-b"}
	deployment.Storages.S3Mounts = config.S3Mounts{s3A, s3B}

	path := &config.PathMapping{
		Name:       "uploads",
		DockerPath: "/app/uploads",
		SourceType: config.SourceS3,
		SourceName: "s3-a",
		SourcePath: s3A.GetSourcePath(),
		VolumeName: "vol-a",
	}
	deployment.Paths = config.PathMaps{path}

	if errs := deployment.ResolvePaths(); len(errs) > 0 {
		t.Fatalf("expected initial resolve to succeed, got %v", errs)
	}
	if path.VolumeName != "vol-a" {
		t.Fatalf("expected initial volumename vol-a, got %q", path.VolumeName)
	}

	path.SourceName = "s3-b"
	path.SourcePath = s3B.GetSourcePath()
	if err := deployment.ResolvePath(path); err != nil {
		t.Fatalf("expected resolve to succeed, got %v", err)
	}
	if path.VolumeName != "vol-b" {
		t.Fatalf("expected volumename to follow s3 source change to vol-b, got %q", path.VolumeName)
	}
}

// TestVolumeRename_PropagatesToDependentAndChildPaths covers Test Plan item 2
// (volume rename refreshes dependent path volume binding) and item 8 (child
// paths inherit the refreshed VolumeName via cascade), plus item 6
// (provisioning follows the refreshed VolumeName).
func TestVolumeRename_PropagatesToDependentAndChildPaths(t *testing.T) {
	deployment := config.NewDeploymentConfig()
	vol := &config.Volume{Name: "data-volume", PoolName: "data", VolumeDir: "data"}
	deployment.Storages.Volumes = config.Volumes{vol}

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
		t.Fatalf("expected initial resolve to succeed, got %v", errs)
	}
	if parent.VolumeName != "data-volume" || child.VolumeName != "data-volume" {
		t.Fatalf("expected both paths bound to data-volume initially, got parent=%q child=%q", parent.VolumeName, child.VolumeName)
	}

	// Simulate the volumes "name" modify handler's rename flow: paths whose
	// current VolumeName matches the old name are re-resolved, but only direct
	// volume-source paths rewrite SourceName to the new row name. Inherited
	// child paths keep empty source fields and pick up the renamed VolumeName
	// via ResolvePath()'s parent cascade.
	oldName := vol.Name
	paths := deployment.GetVolumePaths(oldName)
	vol.Name = "renamed-volume"
	for _, p := range paths {
		if p.SourceType == config.SourceVolume && p.SourceName == oldName {
			p.SourceName = vol.Name
		}
		deployment.ResolvePath(p)
	}

	if parent.VolumeName != "renamed-volume" {
		t.Fatalf("expected parent volumename to follow rename, got %q", parent.VolumeName)
	}
	if child.VolumeName != "renamed-volume" {
		t.Fatalf("expected child volumename to follow rename via cascade, got %q", child.VolumeName)
	}

	cluster := &Cluster{Name: "test", Conf: &config.Config{}}
	app := &App{Name: "myapp", AppConfig: &config.AppConfig{Deployment: deployment}}
	got := cluster.GetOpenSVCDeploymentPathMapping(app)
	tokens := strings.Fields(got)
	if len(tokens) != 2 {
		t.Fatalf("expected 2 mount mappings, got %d: %q", len(tokens), got)
	}
	expected := map[string]bool{
		"renamed-volume:/var/www/html":        false,
		"renamed-volume:/var/www/html/assets": false,
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

// TestResolveGitPaths_VolumeNameReassignment_UpdatesDependentPath covers Test
// Plan item 2: a Git Clone volumename reassignment must refresh the
// VolumeName of paths that source from it.
func TestResolveGitPaths_VolumeNameReassignment_UpdatesDependentPath(t *testing.T) {
	deployment := config.NewDeploymentConfig()
	volA := &config.Volume{Name: "vol-a", PoolName: "a", VolumeDir: "data"}
	volB := &config.Volume{Name: "vol-b", PoolName: "b", VolumeDir: "docs"}
	deployment.Storages.Volumes = config.Volumes{volA, volB}

	gc := &config.GitClone{Name: "git-x", VolumeName: "vol-a", VolumeDir: "repo"}
	deployment.Storages.GitClones = config.GitClones{gc}

	path := &config.PathMapping{
		Name:       "src",
		DockerPath: "/app/src",
		SourceType: config.SourceGit,
		SourceName: "git-x",
		SourcePath: gc.GetSourcePath(),
		VolumeName: "vol-a",
	}
	deployment.Paths = config.PathMaps{path}

	if errs := deployment.ResolvePaths(); len(errs) > 0 {
		t.Fatalf("expected initial resolve to succeed, got %v", errs)
	}
	if path.VolumeName != "vol-a" {
		t.Fatalf("expected initial volumename vol-a, got %q", path.VolumeName)
	}

	// Simulate the gitClones "volumename" modify handler.
	gc.VolumeName = "vol-b"
	gc.Volume = volB
	deployment.ResolveGitPaths(gc.Name)

	if path.VolumeName != "vol-b" {
		t.Fatalf("expected dependent path volumename to follow git clone reassignment, got %q", path.VolumeName)
	}
}

// TestResolveS3MountPaths_VolumeNameReassignment_UpdatesDependentPath covers
// Test Plan item 2: an S3 Mount volumename reassignment must refresh the
// VolumeName of paths that source from it.
func TestResolveS3MountPaths_VolumeNameReassignment_UpdatesDependentPath(t *testing.T) {
	deployment := config.NewDeploymentConfig()
	volA := &config.Volume{Name: "vol-a", PoolName: "a", VolumeDir: "data"}
	volB := &config.Volume{Name: "vol-b", PoolName: "b", VolumeDir: "docs"}
	deployment.Storages.Volumes = config.Volumes{volA, volB}

	s3m := &config.S3Mount{Name: "s3-x", VolumeName: "vol-a", VolumeDir: "uploads"}
	deployment.Storages.S3Mounts = config.S3Mounts{s3m}

	path := &config.PathMapping{
		Name:       "uploads",
		DockerPath: "/app/uploads",
		SourceType: config.SourceS3,
		SourceName: "s3-x",
		SourcePath: s3m.GetSourcePath(),
		VolumeName: "vol-a",
	}
	deployment.Paths = config.PathMaps{path}

	if errs := deployment.ResolvePaths(); len(errs) > 0 {
		t.Fatalf("expected initial resolve to succeed, got %v", errs)
	}
	if path.VolumeName != "vol-a" {
		t.Fatalf("expected initial volumename vol-a, got %q", path.VolumeName)
	}

	// Simulate the s3Mounts "volumename" modify handler.
	s3m.VolumeName = "vol-b"
	s3m.Volume = volB
	deployment.ResolveS3MountPaths(s3m.Name)

	if path.VolumeName != "vol-b" {
		t.Fatalf("expected dependent path volumename to follow s3 mount reassignment, got %q", path.VolumeName)
	}
}

// TestResolvePath_UnresolvedSource_ClearsStaleVolumeName covers Test Plan item
// 5: a path edit that fails re-resolution because its source no longer
// exists must not leave a stale VolumeName behind, so provisioning does not
// silently mount the wrong volume.
func TestResolvePath_UnresolvedSource_ClearsStaleVolumeName(t *testing.T) {
	deployment := config.NewDeploymentConfig()
	deployment.Storages.Volumes = config.Volumes{
		{Name: "vol-a", PoolName: "a", VolumeDir: "data"},
	}

	path := &config.PathMapping{
		Name:       "web-root",
		DockerPath: "/var/www/html",
		SourceType: config.SourceVolume,
		SourceName: "nonexistent-vol",
		SourcePath: ".",
		VolumeName: "vol-a", // stale binding from before the source changed
	}
	deployment.Paths = config.PathMaps{path}

	if err := deployment.ResolvePath(path); err == nil {
		t.Fatalf("expected resolve error for unresolved source, got nil")
	}
	if path.VolumeName != "" {
		t.Fatalf("expected stale volumename to be cleared on unresolved source, got %q", path.VolumeName)
	}
}

// TestResolvePath_SrcTypeTransition_ClearsStaleVolumeName covers Test Plan
// item 7: changing srctype must not leave a stale VolumeName from the
// previous source type, even while the row is momentarily incomplete pending
// a follow-up srcname edit.
func TestResolvePath_SrcTypeTransition_ClearsStaleVolumeName(t *testing.T) {
	deployment := config.NewDeploymentConfig()
	deployment.Storages.Volumes = config.Volumes{
		{Name: "vol-a", PoolName: "a", VolumeDir: "data"},
	}

	path := &config.PathMapping{
		Name:       "web-root",
		DockerPath: "/var/www/html",
		SourceType: config.SourceVolume,
		SourceName: "vol-a",
		SourcePath: ".",
		VolumeName: "vol-a",
	}
	deployment.Paths = config.PathMaps{path}
	if errs := deployment.ResolvePaths(); len(errs) > 0 {
		t.Fatalf("expected initial resolve to succeed, got %v", errs)
	}

	// Simulate the "srctype" modify handler's volume -> git transition: reset
	// source name/path/volume together.
	path.SourceType = config.SourceGit
	path.SourceName = ""
	path.SourcePath = ""
	path.VolumeName = ""

	if err := deployment.ResolvePath(path); err == nil {
		t.Fatalf("expected resolve error for incomplete srctype transition, got nil")
	}
	if path.VolumeName != "" {
		t.Fatalf("expected no stale volumename to survive srctype transition, got %q", path.VolumeName)
	}
}

// TestResolvePaths_ChildOfUnresolvedParentReportsInheritedError covers the
// hardening fix in ResolvePaths pass 2: when a parent path's direct source
// fails to resolve, a child path that depends on inherited VolumeName should
// not silently keep/accept an empty binding. The child now reports its own
// inherited-resolution error as well.
func TestResolvePaths_ChildOfUnresolvedParentReportsInheritedError(t *testing.T) {
	deployment := config.NewDeploymentConfig()
	deployment.Paths = config.PathMaps{
		{
			Name:       "web-root",
			DockerPath: "/var/www/html",
			SourceType: config.SourceVolume,
			SourceName: "missing-volume",
			SourcePath: ".",
			VolumeName: "stale-volume",
		},
		{
			Name:       "assets",
			ParentName: "web-root",
			DockerPath: "/var/www/html/assets",
			VolumeName: "stale-volume",
		},
	}

	errs := deployment.ResolvePaths()
	if len(errs) < 2 {
		t.Fatalf("expected parent and child resolution errors, got %v", errs)
	}
	if deployment.Paths[0].VolumeName != "" {
		t.Fatalf("expected unresolved parent volumename cleared, got %q", deployment.Paths[0].VolumeName)
	}
	if deployment.Paths[1].VolumeName != "" {
		t.Fatalf("expected child inherited volumename cleared, got %q", deployment.Paths[1].VolumeName)
	}

	foundChildErr := false
	for _, err := range errs {
		if strings.Contains(err.Error(), "inherited volume not resolved") && strings.Contains(err.Error(), "web-root") {
			foundChildErr = true
			break
		}
	}
	if !foundChildErr {
		t.Fatalf("expected inherited child resolution error in %v", errs)
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

// TestOpenSVCAppVolumeSection_UsesEnvSizeWhenNoOverride confirms that a volume
// with a blank Size emits "{env.size}" so provisioning falls back to the
// app-wide disk size.
func TestOpenSVCAppVolumeSection_UsesEnvSizeWhenNoOverride(t *testing.T) {
	vol := &config.Volume{Name: "myapp-data", PoolName: "data", VolumeDir: "data", Size: ""}
	section := openSVCAppVolumeSection(vol, map[string][]string{}, false)
	if section["size"] != "{env.size}" {
		t.Fatalf("expected size={env.size} for blank override, got %q", section["size"])
	}
}

// TestOpenSVCAppVolumeSection_UsesOverrideSizeWhenSet confirms that a volume
// with a non-blank Size emits the override value with a "g" suffix, matching
// the format used by cluster.GetAppDisk.
func TestOpenSVCAppVolumeSection_UsesOverrideSizeWhenSet(t *testing.T) {
	vol := &config.Volume{Name: "myapp-data", PoolName: "data", VolumeDir: "data", Size: "10"}
	section := openSVCAppVolumeSection(vol, map[string][]string{}, false)
	if section["size"] != "10g" {
		t.Fatalf("expected size=10g for override=10, got %q", section["size"])
	}
}

// TestOpenSVCAppVolumeSection_SharedVolume confirms that the size override
// is independent of the shared flag.
func TestOpenSVCAppVolumeSection_SharedVolume(t *testing.T) {
	vol := &config.Volume{Name: "shared-data", PoolName: "shared", VolumeDir: "data", Size: "20"}
	section := openSVCAppVolumeSection(vol, map[string][]string{}, true)
	if section["size"] != "20g" {
		t.Fatalf("expected size=20g for shared override=20, got %q", section["size"])
	}
	if section["shared"] != "true" {
		t.Fatalf("expected shared=true, got %q", section["shared"])
	}
}

// TestOpenSVCAppVolumeSection_SizeRendering documents the rendering contract of
// the low-level helper: blank → {env.size}, valid normalized → <n>g, raw
// invalid → <invalid>g.  This shows why provision-only validation must live
// upstream (in validateAppVolumeSizes) rather than here, so preview/MD5
// paths are not affected.
func TestOpenSVCAppVolumeSection_SizeRendering(t *testing.T) {
	cases := []struct {
		size string
		want string
	}{
		{"", "{env.size}"},
		{"10", "10g"},
		{"badvalue", "badvalueg"},
	}
	for _, tc := range cases {
		vol := &config.Volume{Name: "vol", PoolName: "data", VolumeDir: "data", Size: tc.size}
		section := openSVCAppVolumeSection(vol, map[string][]string{}, false)
		if section["size"] != tc.want {
			t.Errorf("openSVCAppVolumeSection size=%q: got %q, want %q", tc.size, section["size"], tc.want)
		}
	}
}

// TestValidateAppVolumeSizes_RejectsInvalidSize verifies that the provision-only
// validator returns an error for an invalid persisted size, including the app
// name, volume name, and raw value in the message.
func TestValidateAppVolumeSizes_RejectsInvalidSize(t *testing.T) {
	app := &App{
		AppConfig: &config.AppConfig{
			AppHost: "myapp",
			Deployment: func() *config.Deployment {
				d := config.NewDeploymentConfig()
				d.Storages.Volumes = config.Volumes{
					{Name: "myapp-data", PoolName: "data", VolumeDir: "data", Size: "badvalue"},
				}
				return d
			}(),
		},
	}

	err := validateAppVolumeSizes(app)
	if err == nil {
		t.Fatal("validateAppVolumeSizes: expected error for invalid size, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"myapp", "myapp-data", "badvalue"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
}

// TestValidateAppVolumeSizes_AllowsBlankSize verifies that a volume with no
// size override is accepted (blank means inherit the app-wide default).
func TestValidateAppVolumeSizes_AllowsBlankSize(t *testing.T) {
	app := &App{
		AppConfig: &config.AppConfig{
			AppHost: "myapp",
			Deployment: func() *config.Deployment {
				d := config.NewDeploymentConfig()
				d.Storages.Volumes = config.Volumes{
					{Name: "myapp-data", PoolName: "data", VolumeDir: "data", Size: ""},
				}
				return d
			}(),
		},
	}
	if err := validateAppVolumeSizes(app); err != nil {
		t.Fatalf("validateAppVolumeSizes: unexpected error for blank size: %v", err)
	}
}

// TestValidateAppVolumeSizes_AllowsValidSizes verifies that correctly formatted
// size strings ("10", "10G", "2T") are accepted by the provision validator.
func TestValidateAppVolumeSizes_AllowsValidSizes(t *testing.T) {
	for _, size := range []string{"10", "10G", "2T"} {
		app := &App{
			AppConfig: &config.AppConfig{
				AppHost: "myapp",
				Deployment: func() *config.Deployment {
					d := config.NewDeploymentConfig()
					d.Storages.Volumes = config.Volumes{
						{Name: "myapp-data", PoolName: "data", VolumeDir: "data", Size: size},
					}
					return d
				}(),
			},
		}
		if err := validateAppVolumeSizes(app); err != nil {
			t.Errorf("validateAppVolumeSizes: unexpected error for size=%q: %v", size, err)
		}
	}
}
