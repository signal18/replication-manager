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
