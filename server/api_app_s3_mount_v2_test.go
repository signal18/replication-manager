package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// newVolumeTestSetupWithVolumes is a variant of newVolumeTestSetup that seeds
// an arbitrary set of saved deployment.storages.volumes rows, for tests that
// need more than one row to exercise V2 multi-volume S3 mount placement.
func newVolumeTestSetupWithVolumes(t *testing.T, appConfigVersion int, volumes config.Volumes) (*ReplicationManager, *cluster.Cluster, *cluster.App) {
	t.Helper()

	appCnf := &config.AppConfig{
		AppHost:          "app-a",
		AppPort:          "8080",
		AppConfigVersion: appConfigVersion,
		Deployment: &config.Deployment{
			Storages: config.StorageMapping{
				Volumes: volumes,
			},
		},
	}
	app := &cluster.App{
		Id:        volumeTestAppId,
		Name:      volumeTestAppId,
		Host:      "app-a",
		Port:      "8080",
		AppConfig: appCnf,
		Mutex:     &sync.Mutex{},
	}
	cl := &cluster.Cluster{
		Name: volumeTestCluster,
		Conf: &config.Config{
			WorkingDir: t.TempDir(),
		},
		WorkingDir: t.TempDir(),
	}
	cl.Conf.Apps = []*config.AppConfig{appCnf}
	cl.Apps = []*cluster.App{app}
	cl.ConfigManager = newConfigManagerForTest()
	cl.APIUsers = map[string]cluster.APIUser{
		volumeTestUser: {
			User:     volumeTestUser,
			Password: "enc",
			Grants:   map[string]bool{config.GrantAppDeployment: true},
		},
	}

	repman := &ReplicationManager{
		Clusters:    map[string]*cluster.Cluster{cl.Name: cl},
		ClusterList: []string{cl.Name},
		Conf:        &config.Config{TokenTimeout: 1},
	}
	repman.initKeys()
	return repman, cl, app
}

// newModifyS3MountFieldRequest builds a POST request equivalent to
// /api/clusters/{cluster}/apps/{app}/storages/s3Mounts/index/{index}/{key}/modify
// with {"value": newValue} as JSON body.
func newModifyS3MountFieldRequest(t *testing.T, repman *ReplicationManager, cl *cluster.Cluster, index int, key, newValue string) *http.Request {
	t.Helper()
	tok := issueVolumeTestJWT(t, repman)
	body, _ := json.Marshal(map[string]string{"value": newValue})
	url := fmt.Sprintf("/api/clusters/%s/apps/%s/storages/s3Mounts/index/%d/%s/modify", cl.Name, volumeTestAppId, index, key)
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req = setMuxVars(req, map[string]string{
		"clusterName": cl.Name,
		"appName":     volumeTestAppId,
		"field":       "s3Mounts",
		"index":       fmt.Sprintf("%d", index),
		"key":         key,
	})
	return req
}

// TestAddStorageS3Mount_V2_OmittedPlacementUsesUniqueMntRow covers Phase 15
// task 1: for a V2 app with several saved volume rows, omitting S3 placement
// still auto-selects the single row that exposes "mnt" via
// SetAppLocalMountVolume, and the autofilled VolumeDir is seeded under that
// row's "mnt" token.
func TestAddStorageS3Mount_V2_OmittedPlacementUsesUniqueMntRow(t *testing.T) {
	repman, cl, app := newVolumeTestSetupWithVolumes(t, config.AppConfigVersionV2, config.Volumes{
		{Name: "myapp-data", PoolName: "data", VolumeDir: "data"},
		{Name: "myapp-shared", PoolName: "shared", VolumeDir: "etc mnt"},
	})

	mount := config.S3Mount{
		Endpoint:  "https://minio.example.com",
		Bucket:    "backups",
		AccessKey: "AKIAEXAMPLE",
		SecretKey: "SECRETEXAMPLE",
	}
	req := newAddS3MountRequest(t, repman, cl, mount)

	w := httptest.NewRecorder()
	repman.handlerMuxAddStorage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 adding S3 mount, got %d: %s", w.Code, w.Body.String())
	}

	mounts := app.AppConfig.Deployment.Storages.S3Mounts
	if len(mounts) != 1 {
		t.Fatalf("expected 1 S3 mount, got %d", len(mounts))
	}
	got := mounts[0]
	if got.VolumeName != "myapp-shared" {
		t.Fatalf("expected default placement on myapp-shared (the only row exposing \"mnt\"), got %q", got.VolumeName)
	}
	wantDir := "mnt/" + got.Name
	if got.VolumeDir != wantDir {
		t.Fatalf("expected VolumeDir %q, got %q", wantDir, got.VolumeDir)
	}
}

// TestAddStorageS3Mount_V2_OmittedPlacementAmbiguousFails covers Phase 15
// tasks 5/6: for a V2 app with multiple saved volume rows where none expose
// "mnt" and there is no unique default, omitting S3 placement must not
// silently pick the first row -- the add request fails with 400 and no
// S3 mount is created.
func TestAddStorageS3Mount_V2_OmittedPlacementAmbiguousFails(t *testing.T) {
	repman, cl, app := newVolumeTestSetupWithVolumes(t, config.AppConfigVersionV2, config.Volumes{
		{Name: "myapp-data", PoolName: "data", VolumeDir: "data"},
		{Name: "myapp-logs", PoolName: "logs", VolumeDir: "log"},
	})

	mount := config.S3Mount{
		Endpoint:  "https://minio.example.com",
		Bucket:    "backups",
		AccessKey: "AKIAEXAMPLE",
		SecretKey: "SECRETEXAMPLE",
	}
	req := newAddS3MountRequest(t, repman, cl, mount)

	w := httptest.NewRecorder()
	repman.handlerMuxAddStorage(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for ambiguous default S3 mount placement, got %d: %s", w.Code, w.Body.String())
	}
	if len(app.AppConfig.Deployment.Storages.S3Mounts) != 0 {
		t.Fatalf("expected no S3 mount added on ambiguous default placement, got %+v", app.AppConfig.Deployment.Storages.S3Mounts)
	}
}

// TestAddStorageS3Mount_V2_ExplicitVolumeNameSelectsNonDefaultRow covers
// Phase 15 task 2: a V2 app can explicitly place an S3 mount on a saved
// volume row other than the one SetAppLocalMountVolume would default to
// (here myapp-data exposes "mnt" and would be the default, but the caller
// explicitly selects myapp-logs instead).
func TestAddStorageS3Mount_V2_ExplicitVolumeNameSelectsNonDefaultRow(t *testing.T) {
	repman, cl, app := newVolumeTestSetupWithVolumes(t, config.AppConfigVersionV2, config.Volumes{
		{Name: "myapp-data", PoolName: "data", VolumeDir: "data mnt"},
		{Name: "myapp-logs", PoolName: "logs", VolumeDir: "log"},
	})

	mount := config.S3Mount{
		Endpoint:   "https://minio.example.com",
		Bucket:     "backups",
		AccessKey:  "AKIAEXAMPLE",
		SecretKey:  "SECRETEXAMPLE",
		VolumeName: "myapp-logs",
	}
	req := newAddS3MountRequest(t, repman, cl, mount)

	w := httptest.NewRecorder()
	repman.handlerMuxAddStorage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 adding S3 mount, got %d: %s", w.Code, w.Body.String())
	}

	mounts := app.AppConfig.Deployment.Storages.S3Mounts
	if len(mounts) != 1 {
		t.Fatalf("expected 1 S3 mount, got %d", len(mounts))
	}
	got := mounts[0]
	if got.VolumeName != "myapp-logs" {
		t.Fatalf("expected explicitly selected row myapp-logs (not the \"mnt\"-exposing myapp-data), got %q", got.VolumeName)
	}
	wantDir := "log/" + got.Name
	if got.VolumeDir != wantDir {
		t.Fatalf("expected VolumeDir %q (selected row's own S3MountSubdir()), got %q", wantDir, got.VolumeDir)
	}
}

// TestAddStorageS3Mount_V2_ExplicitVolumeDirPreservedExactly covers Phase 15
// task 3: a V2 app can explicitly choose a directory token (and a relative
// subdirectory beneath it) other than "mnt" inside the selected row, and the
// resulting VolumeDir is persisted exactly as given.
func TestAddStorageS3Mount_V2_ExplicitVolumeDirPreservedExactly(t *testing.T) {
	repman, cl, app := newVolumeTestSetupWithVolumes(t, config.AppConfigVersionV2, config.Volumes{
		{Name: "myapp-data", PoolName: "data", VolumeDir: "data mnt extra"},
	})

	mount := config.S3Mount{
		Endpoint:   "https://minio.example.com",
		Bucket:     "backups",
		AccessKey:  "AKIAEXAMPLE",
		SecretKey:  "SECRETEXAMPLE",
		VolumeName: "myapp-data",
		VolumeDir:  "extra/custom-media",
	}
	req := newAddS3MountRequest(t, repman, cl, mount)

	w := httptest.NewRecorder()
	repman.handlerMuxAddStorage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 adding S3 mount, got %d: %s", w.Code, w.Body.String())
	}

	mounts := app.AppConfig.Deployment.Storages.S3Mounts
	if len(mounts) != 1 {
		t.Fatalf("expected 1 S3 mount, got %d", len(mounts))
	}
	got := mounts[0]
	if got.VolumeName != "myapp-data" {
		t.Fatalf("expected VolumeName myapp-data, got %q", got.VolumeName)
	}
	if got.VolumeDir != "extra/custom-media" {
		t.Fatalf("expected explicit VolumeDir \"extra/custom-media\" preserved exactly (not snapped to \"mnt/...\"), got %q", got.VolumeDir)
	}
}

// TestAddStorageS3Mount_V2_ExplicitBareDirectoryTokenAppendsGeneratedName
// covers Phase 16: the "Add new" form has no Name field yet, so an explicit
// directory-token choice with a blank Sub Dir is submitted as the bare token
// (e.g. "data") rather than a full "<token>/<name>" path. The generated
// mount name must be appended under that explicit token, preserving the
// user's choice instead of falling back to the row's mnt-biased
// S3MountSubdir() suggestion.
func TestAddStorageS3Mount_V2_ExplicitBareDirectoryTokenAppendsGeneratedName(t *testing.T) {
	repman, cl, app := newVolumeTestSetupWithVolumes(t, config.AppConfigVersionV2, config.Volumes{
		{Name: "myapp-data", PoolName: "data", VolumeDir: "data mnt"},
	})

	mount := config.S3Mount{
		Endpoint:   "https://minio.example.com",
		Bucket:     "backups",
		AccessKey:  "AKIAEXAMPLE",
		SecretKey:  "SECRETEXAMPLE",
		VolumeName: "myapp-data",
		VolumeDir:  "data",
	}
	req := newAddS3MountRequest(t, repman, cl, mount)

	w := httptest.NewRecorder()
	repman.handlerMuxAddStorage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 adding S3 mount, got %d: %s", w.Code, w.Body.String())
	}

	mounts := app.AppConfig.Deployment.Storages.S3Mounts
	if len(mounts) != 1 {
		t.Fatalf("expected 1 S3 mount, got %d", len(mounts))
	}
	got := mounts[0]
	if got.VolumeName != "myapp-data" {
		t.Fatalf("expected VolumeName myapp-data, got %q", got.VolumeName)
	}
	wantDir := "data/" + got.Name
	if got.VolumeDir != wantDir {
		t.Fatalf("expected VolumeDir %q (explicit \"data\" token + generated mount name, not the mnt-biased S3MountSubdir() suggestion), got %q", wantDir, got.VolumeDir)
	}
}

// TestModifyS3Mount_VolumeName_PreservesExplicitVolumeDirWhenNoDuplicate
// covers Phase 15 tasks 4/6: moving an S3 mount with an explicit non-"mnt"
// VolumeDir onto a different saved volume row must preserve that VolumeDir
// unchanged (and refresh the resolved Volume pointer) as long as the
// (newVolumeName, VolumeDir) pair is not already used by another mount.
func TestModifyS3Mount_VolumeName_PreservesExplicitVolumeDirWhenNoDuplicate(t *testing.T) {
	repman, cl, app := newVolumeTestSetupWithVolumes(t, config.AppConfigVersionV2, config.Volumes{
		{Name: "myapp-data", PoolName: "data", VolumeDir: "data mnt"},
		{Name: "myapp-logs", PoolName: "logs", VolumeDir: "log"},
	})
	app.AppConfig.Deployment.Storages.S3Mounts = config.S3Mounts{
		{
			Name:       "media",
			Endpoint:   "https://minio.example.com",
			Bucket:     "backups",
			AccessKey:  "AKIAEXAMPLE",
			SecretKey:  "SECRETEXAMPLE",
			VolumeName: "myapp-data",
			VolumeDir:  "custom/sub",
		},
	}

	req := newModifyS3MountFieldRequest(t, repman, cl, 0, "volumename", "myapp-logs")

	w := httptest.NewRecorder()
	repman.handlerMuxModifyStorageField(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 modifying volumename, got %d: %s", w.Code, w.Body.String())
	}

	got := app.AppConfig.Deployment.Storages.S3Mounts[0]
	if got.VolumeName != "myapp-logs" {
		t.Fatalf("expected VolumeName myapp-logs, got %q", got.VolumeName)
	}
	if got.VolumeDir != "custom/sub" {
		t.Fatalf("expected explicit non-default VolumeDir \"custom/sub\" preserved across volumename change, got %q", got.VolumeDir)
	}
	if got.Volume == nil || got.Volume.Name != "myapp-logs" {
		t.Fatalf("expected resolved Volume pointer refreshed to myapp-logs, got %+v", got.Volume)
	}
}

// TestModifyS3Mount_VolumeName_ResetsVolumeDirOnDuplicatePath covers Phase 15
// task 6: if moving an S3 mount onto a new saved volume row would collide
// with another mount already occupying the same (VolumeName, VolumeDir)
// pair, VolumeDir is reset via the new row's S3MountSubdir() rather than
// silently leaving a duplicate path.
func TestModifyS3Mount_VolumeName_ResetsVolumeDirOnDuplicatePath(t *testing.T) {
	repman, cl, app := newVolumeTestSetupWithVolumes(t, config.AppConfigVersionV2, config.Volumes{
		{Name: "myapp-data", PoolName: "data", VolumeDir: "data mnt"},
		{Name: "myapp-logs", PoolName: "logs", VolumeDir: "log mnt"},
	})
	app.AppConfig.Deployment.Storages.S3Mounts = config.S3Mounts{
		{
			Name:       "mediaA",
			Endpoint:   "https://minio.example.com",
			Bucket:     "backups-a",
			AccessKey:  "AKIAEXAMPLE",
			SecretKey:  "SECRETEXAMPLE",
			VolumeName: "myapp-logs",
			VolumeDir:  "mnt/shared",
		},
		{
			Name:       "mediaB",
			Endpoint:   "https://minio.example.com",
			Bucket:     "backups-b",
			AccessKey:  "AKIAEXAMPLE",
			SecretKey:  "SECRETEXAMPLE",
			VolumeName: "myapp-data",
			VolumeDir:  "mnt/shared",
		},
	}

	req := newModifyS3MountFieldRequest(t, repman, cl, 1, "volumename", "myapp-logs")

	w := httptest.NewRecorder()
	repman.handlerMuxModifyStorageField(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 modifying volumename, got %d: %s", w.Code, w.Body.String())
	}

	got := app.AppConfig.Deployment.Storages.S3Mounts[1]
	if got.VolumeName != "myapp-logs" {
		t.Fatalf("expected VolumeName myapp-logs, got %q", got.VolumeName)
	}
	wantDir := "mnt/" + got.Name
	if got.VolumeDir != wantDir {
		t.Fatalf("expected VolumeDir reset to %q on duplicate path collision, got %q", wantDir, got.VolumeDir)
	}
}

// TestModifyS3Mount_VolumeDir_BlankResetUsesVolumeS3MountSubdir covers Phase
// 15 tasks 4/6: resetting VolumeDir to blank on the modify path must default
// via the current row's S3MountSubdir() (mnt as a suggestion only), not a
// hardcoded "mnt/<name>".
func TestModifyS3Mount_VolumeDir_BlankResetUsesVolumeS3MountSubdir(t *testing.T) {
	repman, cl, app := newVolumeTestSetupWithVolumes(t, config.AppConfigVersionV2, config.Volumes{
		{Name: "myapp-data", PoolName: "data", VolumeDir: "etc"},
	})
	app.AppConfig.Deployment.Storages.S3Mounts = config.S3Mounts{
		{
			Name:       "media",
			Endpoint:   "https://minio.example.com",
			Bucket:     "backups",
			AccessKey:  "AKIAEXAMPLE",
			SecretKey:  "SECRETEXAMPLE",
			VolumeName: "myapp-data",
			VolumeDir:  "mnt/old-media",
		},
	}

	req := newModifyS3MountFieldRequest(t, repman, cl, 0, "volumedir", "")

	w := httptest.NewRecorder()
	repman.handlerMuxModifyStorageField(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 resetting volumedir, got %d: %s", w.Code, w.Body.String())
	}

	got := app.AppConfig.Deployment.Storages.S3Mounts[0]
	wantDir := "etc/" + got.Name
	if got.VolumeDir != wantDir {
		t.Fatalf("expected blank volumedir reset to %q (Volume.S3MountSubdir(), not hardcoded \"mnt/...\"), got %q", wantDir, got.VolumeDir)
	}
}

// TestModifyS3Mount_OtherFieldChange_PreservesExplicitNonMntPlacement covers
// Phase 15 task 4: modifying an unrelated field (e.g. region) must not snap
// an explicit non-"mnt" VolumeName/VolumeDir placement back to a default.
func TestModifyS3Mount_OtherFieldChange_PreservesExplicitNonMntPlacement(t *testing.T) {
	repman, cl, app := newVolumeTestSetupWithVolumes(t, config.AppConfigVersionV2, config.Volumes{
		{Name: "myapp-data", PoolName: "data", VolumeDir: "data mnt"},
	})
	app.AppConfig.Deployment.Storages.S3Mounts = config.S3Mounts{
		{
			Name:       "media",
			Endpoint:   "https://minio.example.com",
			Bucket:     "backups",
			AccessKey:  "AKIAEXAMPLE",
			SecretKey:  "SECRETEXAMPLE",
			VolumeName: "myapp-data",
			VolumeDir:  "data/custom-media",
		},
	}

	req := newModifyS3MountFieldRequest(t, repman, cl, 0, "region", "eu-west-1")

	w := httptest.NewRecorder()
	repman.handlerMuxModifyStorageField(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 modifying region, got %d: %s", w.Code, w.Body.String())
	}

	got := app.AppConfig.Deployment.Storages.S3Mounts[0]
	if got.VolumeName != "myapp-data" || got.VolumeDir != "data/custom-media" {
		t.Fatalf("expected explicit placement myapp-data/data/custom-media preserved, got %q/%q", got.VolumeName, got.VolumeDir)
	}
	if got.Region != "eu-west-1" {
		t.Fatalf("expected region updated to eu-west-1, got %q", got.Region)
	}
}
