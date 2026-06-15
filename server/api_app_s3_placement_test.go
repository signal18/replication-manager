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

// newAddS3MountRequest builds a POST request equivalent to
// /api/clusters/{cluster}/apps/{app}/storages/s3Mounts/add with the given S3
// mount row as JSON body.
func newAddS3MountRequest(t *testing.T, repman *ReplicationManager, cl *cluster.Cluster, mount config.S3Mount) *http.Request {
	t.Helper()
	tok := issueVolumeTestJWT(t, repman)
	body, _ := json.Marshal(mount)
	url := fmt.Sprintf("/api/clusters/%s/apps/%s/storages/s3Mounts/add", cl.Name, volumeTestAppId)
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req = setMuxVars(req, map[string]string{
		"clusterName": cl.Name,
		"appName":     volumeTestAppId,
		"field":       "s3Mounts",
	})
	return req
}

// TestAddStorageS3Mount_LegacyAutofillUsesVolumeS3MountSubdir covers the
// blocking issue raised after Phase 14: when S3 placement is unspecified
// (legacy autofill, task 1) and SetAppLocalMountVolume falls back to the
// app's single saved volume row (Phase 14 task 6) even though that row does
// not expose "mnt", the autofilled VolumeDir must be derived from the
// selected row's Volume.S3MountSubdir() (here "etc", the row's only/first
// token), not hardcoded to "mnt/<mount-name>".
func TestAddStorageS3Mount_LegacyAutofillUsesVolumeS3MountSubdir(t *testing.T) {
	// newVolumeTestSetup seeds a single saved volume row
	// {Name: "myapp-data", PoolName: "data", VolumeDir: "etc"} - no "mnt" token.
	repman, cl, app := newVolumeTestSetup(t, 0)

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
	if got.VolumeName != "myapp-data" {
		t.Fatalf("expected VolumeName myapp-data (the app's single saved volume row), got %q", got.VolumeName)
	}
	wantDir := "etc/" + got.Name
	if got.VolumeDir != wantDir {
		t.Fatalf("expected VolumeDir %q (Volume.S3MountSubdir() + mount name), got %q", wantDir, got.VolumeDir)
	}
}

// TestAddStorageS3Mount_ExplicitVolumeNameDerivesDirFromRowToken covers the
// Phase 14 blocking issue: when the caller explicitly selects a saved volume
// row via VolumeName but leaves VolumeDir blank, the defaulted VolumeDir must
// follow that row's own Volume.S3MountSubdir() (here "etc", the row's
// only/first token, since the row has no "mnt" token), not the hardcoded
// legacy "mnt/<mount-name>" default.
func TestAddStorageS3Mount_ExplicitVolumeNameDerivesDirFromRowToken(t *testing.T) {
	// newVolumeTestSetup seeds a single saved volume row
	// {Name: "myapp-data", PoolName: "data", VolumeDir: "etc"} - no "mnt" token.
	repman, cl, app := newVolumeTestSetup(t, 0)

	mount := config.S3Mount{
		Endpoint:   "https://minio.example.com",
		Bucket:     "backups",
		AccessKey:  "AKIAEXAMPLE",
		SecretKey:  "SECRETEXAMPLE",
		VolumeName: "myapp-data",
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
		t.Fatalf("expected VolumeName myapp-data (explicitly selected row), got %q", got.VolumeName)
	}
	wantDir := "etc/" + got.Name
	if got.VolumeDir != wantDir {
		t.Fatalf("expected VolumeDir %q (selected row's Volume.S3MountSubdir() + mount name), got %q", wantDir, got.VolumeDir)
	}
}
