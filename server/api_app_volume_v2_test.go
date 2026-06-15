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

const (
	volumeTestUser    = "volumetestuser"
	volumeTestCluster = "volume-test-cluster"
	volumeTestAppId   = "volume-test-app"
)

// newVolumeTestSetup builds a minimal ReplicationManager/Cluster/App with a
// single saved deployment.storages.volumes row on pool "data", and an
// app-config-version of either 0 (V1) or config.AppConfigVersionV2,
// depending on appConfigVersion.
func newVolumeTestSetup(t *testing.T, appConfigVersion int) (*ReplicationManager, *cluster.Cluster, *cluster.App) {
	t.Helper()

	appCnf := &config.AppConfig{
		AppHost:          "app-a",
		AppPort:          "8080",
		AppConfigVersion: appConfigVersion,
		Deployment: &config.Deployment{
			Storages: config.StorageMapping{
				Volumes: config.Volumes{
					{Name: "myapp-data", PoolName: "data", VolumeDir: "etc"},
				},
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

func issueVolumeTestJWT(t *testing.T, repman *ReplicationManager) string {
	t.Helper()
	tok, err := repman.issueJWT(struct {
		Name     string
		Role     string
		Password string
	}{volumeTestUser, "Member", "enc"}, "")
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}
	return tok
}

// newAddVolumeRequest builds a POST request equivalent to
// /api/clusters/{cluster}/apps/{app}/storages/volumes/add with the given
// volume row as JSON body.
func newAddVolumeRequest(t *testing.T, repman *ReplicationManager, cl *cluster.Cluster, vol config.Volume) *http.Request {
	t.Helper()
	tok := issueVolumeTestJWT(t, repman)
	body, _ := json.Marshal(vol)
	url := fmt.Sprintf("/api/clusters/%s/apps/%s/storages/volumes/add", cl.Name, volumeTestAppId)
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req = setMuxVars(req, map[string]string{
		"clusterName": cl.Name,
		"appName":     volumeTestAppId,
		"field":       "volumes",
	})
	return req
}

// newModifyVolumePoolnameRequest builds a POST request equivalent to
// /api/clusters/{cluster}/apps/{app}/storages/volumes/index/{index}/poolname/modify.
func newModifyVolumePoolnameRequest(t *testing.T, repman *ReplicationManager, cl *cluster.Cluster, index int, newPool string) *http.Request {
	t.Helper()
	tok := issueVolumeTestJWT(t, repman)
	body, _ := json.Marshal(map[string]string{"value": newPool})
	url := fmt.Sprintf("/api/clusters/%s/apps/%s/storages/volumes/index/%d/poolname/modify", cl.Name, volumeTestAppId, index)
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req = setMuxVars(req, map[string]string{
		"clusterName": cl.Name,
		"appName":     volumeTestAppId,
		"field":       "volumes",
		"index":       fmt.Sprintf("%d", index),
		"key":         "poolname",
	})
	return req
}

// TestAddStorageVolume_RejectsSecondRowOnUsedPoolWhenV1 covers Phase 11: for
// unflagged/V1 content (AppConfigVersion == 0), adding a second volume row
// on a pool that already has a saved row is rejected, matching the
// one-row-per-pool invariant enforced by InsertVolume.
func TestAddStorageVolume_RejectsSecondRowOnUsedPoolWhenV1(t *testing.T) {
	repman, cl, app := newVolumeTestSetup(t, 0)
	req := newAddVolumeRequest(t, repman, cl, config.Volume{Name: "myapp-data-logs", PoolName: "data", VolumeDir: "log"})

	w := httptest.NewRecorder()
	repman.handlerMuxAddStorage(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for duplicate pool on V1, got %d: %s", w.Code, w.Body.String())
	}
	if len(app.AppConfig.Deployment.Storages.Volumes) != 1 {
		t.Fatalf("expected volumes unchanged at 1 row, got %d", len(app.AppConfig.Deployment.Storages.Volumes))
	}
}

// TestAddStorageVolume_AllowsSecondRowOnUsedPoolWhenV2 covers Phase 11 task
// 9/10: once AppConfigVersion >= AppConfigVersionV2, adding a second
// intentional volume row on a pool that already has a saved row succeeds.
func TestAddStorageVolume_AllowsSecondRowOnUsedPoolWhenV2(t *testing.T) {
	repman, cl, app := newVolumeTestSetup(t, config.AppConfigVersionV2)
	req := newAddVolumeRequest(t, repman, cl, config.Volume{Name: "myapp-data-logs", PoolName: "data", VolumeDir: "log"})

	w := httptest.NewRecorder()
	repman.handlerMuxAddStorage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for duplicate pool on V2, got %d: %s", w.Code, w.Body.String())
	}
	volumes := app.AppConfig.Deployment.Storages.Volumes
	if len(volumes) != 2 {
		t.Fatalf("expected 2 volume rows, got %d", len(volumes))
	}
	if volumes[1].Name != "myapp-data-logs" || volumes[1].PoolName != "data" {
		t.Fatalf("expected second row myapp-data-logs on pool data, got %+v", volumes[1])
	}
}

// TestModifyVolumePoolname_RejectsMoveOntoUsedPoolWhenV1 covers Phase 11: for
// unflagged/V1 content, moving a row's poolname onto a pool another saved
// row already occupies is rejected.
func TestModifyVolumePoolname_RejectsMoveOntoUsedPoolWhenV1(t *testing.T) {
	repman, cl, app := newVolumeTestSetup(t, 0)
	app.AppConfig.Deployment.Storages.Volumes = append(app.AppConfig.Deployment.Storages.Volumes,
		&config.Volume{Name: "myapp-docs", PoolName: "docs", VolumeDir: "docs"})

	req := newModifyVolumePoolnameRequest(t, repman, cl, 1, "data")

	w := httptest.NewRecorder()
	repman.handlerMuxModifyStorageField(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 moving onto used pool on V1, got %d: %s", w.Code, w.Body.String())
	}
	if app.AppConfig.Deployment.Storages.Volumes[1].PoolName != "docs" {
		t.Fatalf("expected second row poolname unchanged, got %q", app.AppConfig.Deployment.Storages.Volumes[1].PoolName)
	}
}

// TestModifyVolumePoolname_AllowsMoveOntoUsedPoolWhenV2 covers Phase 11 task
// 9/10: for V2 content, moving a row's poolname onto a pool another saved
// row already occupies succeeds, producing two rows on the same pool.
func TestModifyVolumePoolname_AllowsMoveOntoUsedPoolWhenV2(t *testing.T) {
	repman, cl, app := newVolumeTestSetup(t, config.AppConfigVersionV2)
	app.AppConfig.Deployment.Storages.Volumes = append(app.AppConfig.Deployment.Storages.Volumes,
		&config.Volume{Name: "myapp-docs", PoolName: "docs", VolumeDir: "docs"})

	req := newModifyVolumePoolnameRequest(t, repman, cl, 1, "data")

	w := httptest.NewRecorder()
	repman.handlerMuxModifyStorageField(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 moving onto used pool on V2, got %d: %s", w.Code, w.Body.String())
	}
	volumes := app.AppConfig.Deployment.Storages.Volumes
	if volumes[0].PoolName != "data" || volumes[1].PoolName != "data" {
		t.Fatalf("expected both rows on pool data, got %q and %q", volumes[0].PoolName, volumes[1].PoolName)
	}
	if volumes[1].Name != "myapp-docs" {
		t.Fatalf("expected second row name unchanged, got %q", volumes[1].Name)
	}
}
