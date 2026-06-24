package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// newAddPathRequest builds a POST request equivalent to
// /api/clusters/{cluster}/apps/{app}/deployment/paths/add with the given
// path rows as JSON body.
func newAddPathRequest(t *testing.T, repman *ReplicationManager, cl *cluster.Cluster, rows []config.PathMapping) *http.Request {
	t.Helper()
	tok := issueVolumeTestJWT(t, repman)
	body, _ := json.Marshal(rows)
	url := fmt.Sprintf("/api/clusters/%s/apps/%s/deployment/paths/add", cl.Name, volumeTestAppId)
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req = setMuxVars(req, map[string]string{
		"clusterName": cl.Name,
		"appName":     volumeTestAppId,
		"field":       "paths",
	})
	return req
}

// TestAddPath_DirectVolumeRow_V2 verifies that a direct-source volume path row
// with srcname = volume row name and srcpath "." is accepted, and that the
// backend resolves VolumeName to the volume row name.
func TestAddPath_DirectVolumeRow_V2(t *testing.T) {
	repman, cl, app := newPathMappingTestSetup(t)

	req := newAddPathRequest(t, repman, cl, []config.PathMapping{
		{Name: "web-root", DockerPath: "/var/www/html", SourceType: config.SourceVolume, SourceName: "vol-a", SourcePath: "."},
	})
	w := httptest.NewRecorder()
	repman.handlerMuxAddDeploymentFieldRow(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for direct V2 volume path, got %d: %s", w.Code, w.Body.String())
	}
	if len(app.AppConfig.Deployment.Paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(app.AppConfig.Deployment.Paths))
	}
	got := app.AppConfig.Deployment.Paths[0]
	if got.VolumeName != "vol-a" {
		t.Fatalf("expected VolumeName vol-a, got %q", got.VolumeName)
	}
	if got.SourcePath != "." {
		t.Fatalf("expected SourcePath '.', got %q", got.SourcePath)
	}
}

// TestAddPath_InheritedChildRow verifies that an inherited child path row
// (parentname set, empty source fields) is accepted and resolves VolumeName
// from the parent.
func TestAddPath_InheritedChildRow(t *testing.T) {
	repman, cl, app := newPathMappingTestSetup(t)
	// seed the parent row directly so the child has a real parent to resolve
	app.AppConfig.Deployment.Paths = config.PathMaps{
		{Name: "web-root", DockerPath: "/var/www/html", SourceType: config.SourceVolume, SourceName: "vol-a", SourcePath: ".", VolumeName: "vol-a"},
	}
	if errs := app.AppConfig.Deployment.ResolvePaths(); len(errs) > 0 {
		t.Fatalf("initial ResolvePaths: %v", errs)
	}

	req := newAddPathRequest(t, repman, cl, []config.PathMapping{
		{Name: "assets", DockerPath: "/var/www/html/assets", ParentName: "web-root"},
	})
	w := httptest.NewRecorder()
	repman.handlerMuxAddDeploymentFieldRow(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for inherited child path, got %d: %s", w.Code, w.Body.String())
	}
	if len(app.AppConfig.Deployment.Paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(app.AppConfig.Deployment.Paths))
	}
	child := app.AppConfig.Deployment.Paths[1]
	if child.SourceType != "" || child.SourceName != "" || child.SourcePath != "" {
		t.Fatalf("expected inherited child source fields empty, got srctype=%q srcname=%q srcpath=%q",
			child.SourceType, child.SourceName, child.SourcePath)
	}
	if child.VolumeName != "vol-a" {
		t.Fatalf("expected child VolumeName vol-a (inherited from parent), got %q", child.VolumeName)
	}
}

// TestAddPath_RejectsMissingDockerPath verifies that a path row without
// dockerpath is rejected.
func TestAddPath_RejectsMissingDockerPath(t *testing.T) {
	repman, cl, _ := newPathMappingTestSetup(t)

	req := newAddPathRequest(t, repman, cl, []config.PathMapping{
		{Name: "bad", SourceType: config.SourceVolume, SourceName: "vol-a", SourcePath: "."},
	})
	w := httptest.NewRecorder()
	repman.handlerMuxAddDeploymentFieldRow(w, req)

	if w.Code == http.StatusOK {
		t.Fatal("expected error for missing dockerpath, got 200")
	}
}

// TestAddPath_RejectsPartialSource_SrcTypeWithoutSrcName verifies that srctype
// without srcname is rejected.
func TestAddPath_RejectsPartialSource_SrcTypeWithoutSrcName(t *testing.T) {
	repman, cl, _ := newPathMappingTestSetup(t)

	req := newAddPathRequest(t, repman, cl, []config.PathMapping{
		{Name: "partial", DockerPath: "/var/www/html", SourceType: config.SourceVolume, SourcePath: "."},
	})
	w := httptest.NewRecorder()
	repman.handlerMuxAddDeploymentFieldRow(w, req)

	if w.Code == http.StatusOK {
		t.Fatal("expected error for srctype without srcname, got 200")
	}
}

// TestAddPath_RejectsDirectSourceWithoutSrcPath verifies that a direct-source
// row without srcpath is rejected.
func TestAddPath_RejectsDirectSourceWithoutSrcPath(t *testing.T) {
	repman, cl, _ := newPathMappingTestSetup(t)

	req := newAddPathRequest(t, repman, cl, []config.PathMapping{
		{Name: "no-srcpath", DockerPath: "/var/www/html", SourceType: config.SourceVolume, SourceName: "vol-a"},
	})
	w := httptest.NewRecorder()
	repman.handlerMuxAddDeploymentFieldRow(w, req)

	if w.Code == http.StatusOK {
		t.Fatal("expected error for direct-source row without srcpath, got 200")
	}
}

func newPathMappingTestSetup(t *testing.T) (*ReplicationManager, *cluster.Cluster, *cluster.App) {
	t.Helper()

	repman, cl, app := newVolumeTestSetupWithVolumes(t, config.AppConfigVersionV2, config.Volumes{
		{Name: "vol-a", PoolName: "data", VolumeDir: "data"},
		{Name: "vol-b", PoolName: "docs", VolumeDir: "docs"},
	})

	app.AppConfig.Deployment.Storages.GitClones = config.GitClones{
		{Name: "git-a", VolumeName: "vol-a", VolumeDir: "repo-a"},
		{Name: "git-b", VolumeName: "vol-b", VolumeDir: "repo-b"},
	}
	app.AppConfig.Deployment.Storages.S3Mounts = config.S3Mounts{
		{Name: "s3-a", VolumeName: "vol-a", VolumeDir: "uploads-a"},
		{Name: "s3-b", VolumeName: "vol-b", VolumeDir: "uploads-b"},
	}

	return repman, cl, app
}

func newModifyPathFieldRequest(t *testing.T, repman *ReplicationManager, cl *cluster.Cluster, index int, key, newValue string) *http.Request {
	t.Helper()
	tok := issueVolumeTestJWT(t, repman)
	body, _ := json.Marshal(map[string]string{"value": newValue})
	url := fmt.Sprintf("/api/clusters/%s/apps/%s/deployment/paths/index/%d/%s/modify", cl.Name, volumeTestAppId, index, key)
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req = setMuxVars(req, map[string]string{
		"clusterName": cl.Name,
		"appName":     volumeTestAppId,
		"field":       "paths",
		"index":       fmt.Sprintf("%d", index),
		"key":         key,
	})
	return req
}

func newModifyVolumeNameRequest(t *testing.T, repman *ReplicationManager, cl *cluster.Cluster, index int, newName string) *http.Request {
	t.Helper()
	tok := issueVolumeTestJWT(t, repman)
	body, _ := json.Marshal(map[string]string{"value": newName})
	url := fmt.Sprintf("/api/clusters/%s/apps/%s/storages/volumes/index/%d/name/modify", cl.Name, volumeTestAppId, index)
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req = setMuxVars(req, map[string]string{
		"clusterName": cl.Name,
		"appName":     volumeTestAppId,
		"field":       "volumes",
		"index":       fmt.Sprintf("%d", index),
		"key":         "name",
	})
	return req
}

// TestModifyPathField_SrcTypeTransitionClearsStaleFields_WarningOnly verifies
// the API's explicit warning-only choice for field-by-field srctype edits:
// switching source type clears SourceName/SourcePath/VolumeName, returns 200,
// and persists the intentionally incomplete row for a follow-up srcname edit.
func TestModifyPathField_SrcTypeTransitionClearsStaleFields_WarningOnly(t *testing.T) {
	repman, cl, app := newPathMappingTestSetup(t)
	app.AppConfig.Deployment.Paths = config.PathMaps{
		{Name: "web-root", DockerPath: "/var/www/html", SourceType: config.SourceVolume, SourceName: "vol-a", SourcePath: ".", VolumeName: "vol-a"},
	}
	if errs := app.AppConfig.Deployment.ResolvePaths(); len(errs) > 0 {
		t.Fatalf("initial ResolvePaths: %v", errs)
	}

	req := newModifyPathFieldRequest(t, repman, cl, 0, "srctype", string(config.SourceGit))
	w := httptest.NewRecorder()
	repman.handlerMuxModifyDeploymentField(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for srctype transition, got %d: %s", w.Code, w.Body.String())
	}
	got := app.AppConfig.Deployment.Paths[0]
	if got.SourceType != config.SourceGit {
		t.Fatalf("expected SourceType git, got %q", got.SourceType)
	}
	if got.SourceName != "" || got.SourcePath != "" || got.VolumeName != "" {
		t.Fatalf("expected incomplete transition with cleared source fields, got srcname=%q srcpath=%q volumename=%q", got.SourceName, got.SourcePath, got.VolumeName)
	}
}

// TestModifyPathField_VolumeSourceChangeUpdatesVolumeName covers the actual
// path modify API flow: choosing another direct saved volume row should update
// SourcePath to "." and refresh VolumeName to the selected row.
func TestModifyPathField_VolumeSourceChangeUpdatesVolumeName(t *testing.T) {
	repman, cl, app := newPathMappingTestSetup(t)
	app.AppConfig.Deployment.Paths = config.PathMaps{
		{Name: "web-root", DockerPath: "/var/www/html", SourceType: config.SourceVolume, SourceName: "vol-a", SourcePath: ".", VolumeName: "vol-a"},
	}
	if errs := app.AppConfig.Deployment.ResolvePaths(); len(errs) > 0 {
		t.Fatalf("initial ResolvePaths: %v", errs)
	}

	req := newModifyPathFieldRequest(t, repman, cl, 0, "srcname", "vol-b")
	w := httptest.NewRecorder()
	repman.handlerMuxModifyDeploymentField(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for direct volume source change, got %d: %s", w.Code, w.Body.String())
	}
	got := app.AppConfig.Deployment.Paths[0]
	if got.SourceName != "vol-b" || got.SourcePath != "." || got.VolumeName != "vol-b" {
		t.Fatalf("expected srcname=vol-b srcpath='.' volumename=vol-b, got srcname=%q srcpath=%q volumename=%q", got.SourceName, got.SourcePath, got.VolumeName)
	}
}

// TestModifyPathField_SrcPathBlankResetUsesCurrentSource covers the actual API
// reset branch for all three source types.
func TestModifyPathField_SrcPathBlankResetUsesCurrentSource(t *testing.T) {
	tests := []struct {
		name     string
		path     *config.PathMapping
		wantPath string
	}{
		{
			name:     "volume",
			path:     &config.PathMapping{Name: "web-root", DockerPath: "/var/www/html", SourceType: config.SourceVolume, SourceName: "vol-a", SourcePath: "custom/sub", VolumeName: "vol-a"},
			wantPath: ".",
		},
		{
			name:     "git",
			path:     &config.PathMapping{Name: "src", DockerPath: "/app/src", SourceType: config.SourceGit, SourceName: "git-a", SourcePath: "custom/sub", VolumeName: "vol-a"},
			wantPath: "/repo-a",
		},
		{
			name:     "s3",
			path:     &config.PathMapping{Name: "uploads", DockerPath: "/app/uploads", SourceType: config.SourceS3, SourceName: "s3-a", SourcePath: "custom/sub", VolumeName: "vol-a"},
			wantPath: "/uploads-a",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repman, cl, app := newPathMappingTestSetup(t)
			app.AppConfig.Deployment.Paths = config.PathMaps{tc.path}
			if errs := app.AppConfig.Deployment.ResolvePaths(); len(errs) > 0 {
				t.Fatalf("initial ResolvePaths: %v", errs)
			}

			req := newModifyPathFieldRequest(t, repman, cl, 0, "srcpath", "")
			w := httptest.NewRecorder()
			repman.handlerMuxModifyDeploymentField(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200 resetting srcpath, got %d: %s", w.Code, w.Body.String())
			}
			got := app.AppConfig.Deployment.Paths[0]
			if got.SourcePath != tc.wantPath {
				t.Fatalf("expected reset srcpath %q, got %q", tc.wantPath, got.SourcePath)
			}
		})
	}
}

// TestModifyPathField_UnresolvedSourceName_WarningOnlyClearsVolumeName
// verifies the API's warning-only persistence contract for an unresolved
// srcname edit: the new source name is persisted, stale VolumeName is cleared,
// and the request still returns 200.
func TestModifyPathField_UnresolvedSourceName_WarningOnlyClearsVolumeName(t *testing.T) {
	repman, cl, app := newPathMappingTestSetup(t)
	app.AppConfig.Deployment.Paths = config.PathMaps{
		{Name: "web-root", DockerPath: "/var/www/html", SourceType: config.SourceVolume, SourceName: "vol-a", SourcePath: ".", VolumeName: "vol-a"},
	}
	if errs := app.AppConfig.Deployment.ResolvePaths(); len(errs) > 0 {
		t.Fatalf("initial ResolvePaths: %v", errs)
	}

	req := newModifyPathFieldRequest(t, repman, cl, 0, "srcname", "missing-vol")
	w := httptest.NewRecorder()
	repman.handlerMuxModifyDeploymentField(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for unresolved source warning-only path edit, got %d: %s", w.Code, w.Body.String())
	}
	got := app.AppConfig.Deployment.Paths[0]
	if got.SourceName != "missing-vol" {
		t.Fatalf("expected SourceName missing-vol, got %q", got.SourceName)
	}
	if got.VolumeName != "" {
		t.Fatalf("expected stale VolumeName cleared for unresolved source, got %q", got.VolumeName)
	}
}

// TestModifyVolumeName_PreservesInheritedChildSourceFields verifies the API
// rename flow does not rewrite inherited child paths as if they had a direct
// volume source of their own, while still refreshing both parent and child
// VolumeName to the renamed row.
func TestModifyVolumeName_PreservesInheritedChildSourceFields(t *testing.T) {
	repman, cl, app := newPathMappingTestSetup(t)
	app.AppConfig.Deployment.Paths = config.PathMaps{
		{Name: "web-root", DockerPath: "/var/www/html", SourceType: config.SourceVolume, SourceName: "vol-a", SourcePath: ".", VolumeName: "vol-a"},
		{Name: "assets", ParentName: "web-root", DockerPath: "/var/www/html/assets"},
	}
	if errs := app.AppConfig.Deployment.ResolvePaths(); len(errs) > 0 {
		t.Fatalf("initial ResolvePaths: %v", errs)
	}

	req := newModifyVolumeNameRequest(t, repman, cl, 0, "vol-renamed")
	w := httptest.NewRecorder()
	repman.handlerMuxModifyStorageField(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 renaming volume, got %d: %s", w.Code, w.Body.String())
	}
	parent := app.AppConfig.Deployment.Paths[0]
	child := app.AppConfig.Deployment.Paths[1]
	if parent.SourceName != "vol-renamed" || parent.VolumeName != "vol-renamed" {
		t.Fatalf("expected parent to follow rename, got srcname=%q volumename=%q", parent.SourceName, parent.VolumeName)
	}
	if child.SourceType != "" || child.SourceName != "" || child.SourcePath != "" {
		t.Fatalf("expected inherited child source fields unchanged/empty, got srctype=%q srcname=%q srcpath=%q", child.SourceType, child.SourceName, child.SourcePath)
	}
	if child.VolumeName != "vol-renamed" {
		t.Fatalf("expected child inherited volumename vol-renamed, got %q", child.VolumeName)
	}
}

// TestAddPath_MultiPathSameVolumeRow verifies that two direct path rows
// referencing the same volume row but with different srcpath subpaths are both
// accepted by the add-path API, and that each row resolves VolumeName to the
// shared volume row name while preserving its own distinct SourcePath.
func TestAddPath_MultiPathSameVolumeRow(t *testing.T) {
	repman, cl, app := newPathMappingTestSetup(t)

	// Add the first path: vol-a mapped at subdir "data"
	req := newAddPathRequest(t, repman, cl, []config.PathMapping{
		{Name: "data-path", DockerPath: "/docker/data", SourceType: config.SourceVolume, SourceName: "vol-a", SourcePath: "data"},
	})
	w := httptest.NewRecorder()
	repman.handlerMuxAddDeploymentFieldRow(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for first path on vol-a, got %d: %s", w.Code, w.Body.String())
	}

	// Add the second path: same vol-a, different subdir "config"
	req = newAddPathRequest(t, repman, cl, []config.PathMapping{
		{Name: "config-path", DockerPath: "/docker/config", SourceType: config.SourceVolume, SourceName: "vol-a", SourcePath: "config"},
	})
	w = httptest.NewRecorder()
	repman.handlerMuxAddDeploymentFieldRow(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for second path on vol-a, got %d: %s", w.Code, w.Body.String())
	}

	paths := app.AppConfig.Deployment.Paths
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}

	// Both rows must resolve to the same VolumeName but keep distinct SourcePaths
	if paths[0].VolumeName != "vol-a" || paths[1].VolumeName != "vol-a" {
		t.Fatalf("expected both paths to resolve VolumeName vol-a, got %q and %q", paths[0].VolumeName, paths[1].VolumeName)
	}
	sourcePaths := map[string]bool{paths[0].SourcePath: true, paths[1].SourcePath: true}
	if !sourcePaths["data"] || !sourcePaths["config"] {
		t.Fatalf("expected SourcePaths {data, config}, got %q and %q", paths[0].SourcePath, paths[1].SourcePath)
	}
}

// TestAddPath_RootAndSubdirOnSameVolumeRow verifies that a root mapping
// (srcpath ".") and a subdir mapping (srcpath "config") on the same volume
// row can coexist and are both accepted by the add-path API.
func TestAddPath_RootAndSubdirOnSameVolumeRow(t *testing.T) {
	repman, cl, app := newPathMappingTestSetup(t)

	req := newAddPathRequest(t, repman, cl, []config.PathMapping{
		{Name: "root-path", DockerPath: "/docker/root", SourceType: config.SourceVolume, SourceName: "vol-a", SourcePath: "."},
	})
	w := httptest.NewRecorder()
	repman.handlerMuxAddDeploymentFieldRow(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for root path on vol-a, got %d: %s", w.Code, w.Body.String())
	}

	req = newAddPathRequest(t, repman, cl, []config.PathMapping{
		{Name: "config-path", DockerPath: "/docker/config", SourceType: config.SourceVolume, SourceName: "vol-a", SourcePath: "config"},
	})
	w = httptest.NewRecorder()
	repman.handlerMuxAddDeploymentFieldRow(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for subdir path on vol-a, got %d: %s", w.Code, w.Body.String())
	}

	paths := app.AppConfig.Deployment.Paths
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}
	sourcePaths := map[string]bool{paths[0].SourcePath: true, paths[1].SourcePath: true}
	if !sourcePaths["."] || !sourcePaths["config"] {
		t.Fatalf("expected SourcePaths {., config}, got %q and %q", paths[0].SourcePath, paths[1].SourcePath)
	}
}

// TestAddPath_RejectsBareRootRow verifies that a path row with no srctype and
// no parentname is rejected by the add-path API — such a row cannot be
// provisioned by OpenSVC since it has no source and no inherited volume.
func TestAddPath_RejectsBareRootRow(t *testing.T) {
	repman, cl, _ := newPathMappingTestSetup(t)

	req := newAddPathRequest(t, repman, cl, []config.PathMapping{
		{Name: "bare", DockerPath: "/docker/bare"},
	})
	w := httptest.NewRecorder()
	repman.handlerMuxAddDeploymentFieldRow(w, req)

	if w.Code == http.StatusOK {
		t.Fatal("expected error for bare root row (no srctype, no parentname), got 200")
	}
}

// TestAddPath_UnnamedRowsFallbackNamesAreUnique verifies that when two unnamed
// path rows targeting different dockerpaths are added to the same volume row,
// both succeed and their server-generated fallback names are distinct.
func TestAddPath_UnnamedRowsFallbackNamesAreUnique(t *testing.T) {
	repman, cl, app := newPathMappingTestSetup(t)

	req := newAddPathRequest(t, repman, cl, []config.PathMapping{
		{DockerPath: "/docker/data", SourceType: config.SourceVolume, SourceName: "vol-a", SourcePath: "data"},
	})
	w := httptest.NewRecorder()
	repman.handlerMuxAddDeploymentFieldRow(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for first unnamed row, got %d: %s", w.Code, w.Body.String())
	}

	req = newAddPathRequest(t, repman, cl, []config.PathMapping{
		{DockerPath: "/docker/config", SourceType: config.SourceVolume, SourceName: "vol-a", SourcePath: "config"},
	})
	w = httptest.NewRecorder()
	repman.handlerMuxAddDeploymentFieldRow(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for second unnamed row, got %d: %s", w.Code, w.Body.String())
	}

	paths := app.AppConfig.Deployment.Paths
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}
	if paths[0].Name == "" || paths[1].Name == "" {
		t.Fatalf("expected non-empty fallback names, got %q and %q", paths[0].Name, paths[1].Name)
	}
	if paths[0].Name == paths[1].Name {
		t.Fatalf("expected distinct fallback names, both got %q", paths[0].Name)
	}
}

// TestAddPath_UnnamedRowsSameSanitizedToken verifies that two unnamed path rows
// whose dockerpaths differ only in leading/trailing slash formatting (which the
// old sanitization-based fallback would have collapsed to the same name) both
// succeed and receive distinct fallback names.
func TestAddPath_UnnamedRowsSameSanitizedToken(t *testing.T) {
	repman, cl, app := newPathMappingTestSetup(t)

	// These two paths differ only in a trailing slash; old sanitizer collapses both
	// to "docker-data", producing a name collision. The hash-based fallback must
	// give each a distinct name.
	for _, dp := range []string{"/docker/data", "docker/data/"} {
		req := newAddPathRequest(t, repman, cl, []config.PathMapping{
			{DockerPath: dp, SourceType: config.SourceVolume, SourceName: "vol-a", SourcePath: "data"},
		})
		w := httptest.NewRecorder()
		repman.handlerMuxAddDeploymentFieldRow(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for dockerpath %q, got %d: %s", dp, w.Code, w.Body.String())
		}
	}

	paths := app.AppConfig.Deployment.Paths
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}
	if paths[0].Name == paths[1].Name {
		t.Fatalf("expected distinct fallback names for formatting variants, both got %q", paths[0].Name)
	}
}

// TestAddPath_DuplicateNameRejected verifies that InsertPath rejects a second
// path row whose explicit name collides with an existing row's name, even when
// the dockerpaths differ.
func TestAddPath_DuplicateNameRejected(t *testing.T) {
	repman, cl, app := newPathMappingTestSetup(t)
	app.AppConfig.Deployment.Paths = config.PathMaps{
		{Name: "web-root", DockerPath: "/var/www/html", SourceType: config.SourceVolume, SourceName: "vol-a", SourcePath: ".", VolumeName: "vol-a"},
	}
	if errs := app.AppConfig.Deployment.ResolvePaths(); len(errs) > 0 {
		t.Fatalf("initial ResolvePaths: %v", errs)
	}

	req := newAddPathRequest(t, repman, cl, []config.PathMapping{
		{Name: "web-root", DockerPath: "/var/www/assets", SourceType: config.SourceVolume, SourceName: "vol-a", SourcePath: "assets"},
	})
	w := httptest.NewRecorder()
	repman.handlerMuxAddDeploymentFieldRow(w, req)

	if w.Code == http.StatusOK {
		t.Fatal("expected error for duplicate path name, got 200")
	}
}
