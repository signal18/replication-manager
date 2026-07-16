package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// newResticRepman builds a ReplicationManager with JWT keys initialised and the
// named cluster registered. The cluster has restic enabled, a ResticManager with
// a paused worker, and a test user with GrantClusterProcess.
func newResticRepman(t *testing.T, clusterName string, resticEnabled bool) (*ReplicationManager, string) {
	t.Helper()

	rm := backupmgr.NewResticRepo("", nil, config.ConstLogModRestic)
	rm.PauseWorker()
	t.Cleanup(rm.ShutdownWorker)

	const (
		testUser = "restic-test-user"
		testPass = "restic-test-pass"
	)

	cl := &cluster.Cluster{
		Name: clusterName,
		Conf: &config.Config{
			BackupRestic: resticEnabled,
		},
		ResticManager: rm,
	}
	cl.APIUsers = map[string]cluster.APIUser{
		testUser: {
			User:     testUser,
			Password: testPass,
			Grants:   map[string]bool{config.GrantClusterProcess: true},
		},
	}

	repman := &ReplicationManager{
		Clusters: map[string]*cluster.Cluster{clusterName: cl},
		Conf:     &config.Config{TokenTimeout: 1},
	}
	repman.initKeys()

	tok, err := repman.issueJWT(map[string]interface{}{
		"Name":     testUser,
		"Password": testPass,
	}, "")
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}
	return repman, tok
}

// TestHandlerResticCopy_NoCluster verifies that a request for a non-existent
// cluster returns 500 before any ACL or business-logic checks run.
func TestHandlerResticCopy_NoCluster(t *testing.T) {
	repman := newTestRepmanWithCluster(t, "other-cluster", newTestClusterForAPI(t))
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/missing/restic/copy",
		strings.NewReader(`{}`))
	req = setMuxVars(req, map[string]string{"clusterName": "missing"})
	w := httptest.NewRecorder()

	repman.handlerMuxResticCopy(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for missing cluster, got %d", w.Code)
	}
}

// TestHandlerResticCopy_NoACL verifies that a request for an existing cluster
// without a valid JWT is rejected with 403.
func TestHandlerResticCopy_NoACL(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.BackupRestic = true
	repman := newTestRepmanWithCluster(t, "test-cluster", cl)
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/test-cluster/restic/copy",
		strings.NewReader(`{}`))
	req = setMuxVars(req, map[string]string{"clusterName": "test-cluster"})
	w := httptest.NewRecorder()

	repman.handlerMuxResticCopy(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without JWT, got %d", w.Code)
	}
}

// TestHandlerResticCopy_BadJSON verifies that a malformed JSON body returns 400.
func TestHandlerResticCopy_BadJSON(t *testing.T) {
	repman, tok := newResticRepman(t, "test-cluster", true)
	req := httptest.NewRequest(http.MethodPost,
		"/api/clusters/test-cluster/restic/copy",
		strings.NewReader(`{not valid json`))
	req = setMuxVars(req, map[string]string{"clusterName": "test-cluster"})
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", tok))
	w := httptest.NewRecorder()

	repman.handlerMuxResticCopy(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad JSON, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandlerResticCopy_InvalidMode verifies that a valid-JSON body with an
// unrecognised source mode returns 400.
func TestHandlerResticCopy_InvalidMode(t *testing.T) {
	repman, tok := newResticRepman(t, "test-cluster", true)
	body := `{"source":{"mode":"unsupported","repository":"/x","password":"p"}}`
	req := httptest.NewRequest(http.MethodPost,
		"/api/clusters/test-cluster/restic/copy",
		strings.NewReader(body))
	req = setMuxVars(req, map[string]string{"clusterName": "test-cluster"})
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", tok))
	w := httptest.NewRecorder()

	repman.handlerMuxResticCopy(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid mode, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandlerResticCopy_ValidRequest verifies that a well-formed, authenticated
// request returns 200 and queues the task.
func TestHandlerResticCopy_ValidRequest(t *testing.T) {
	repman, tok := newResticRepman(t, "test-cluster", true)
	body := `{"source":{"mode":"restic-local","repository":"/src","password":"srcpass"}}`
	req := httptest.NewRequest(http.MethodPost,
		"/api/clusters/test-cluster/restic/copy",
		strings.NewReader(body))
	req = setMuxVars(req, map[string]string{"clusterName": "test-cluster"})
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", tok))
	w := httptest.NewRecorder()

	repman.handlerMuxResticCopy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid request, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "queued") {
		t.Errorf("expected response body to mention 'queued', got: %s", w.Body.String())
	}
}

// ── handlerMuxResticCreateBucket tests ────────────────────────────────────────

// newFakeS3BucketServer starts a minimal path-style S3 stub answering
// HeadBucket/CreateBucket for handlerMuxResticCreateBucket tests. It does not
// validate SigV4 signatures - these tests exercise ACL/routing/resolution
// wiring, not AWS request authentication.
func newFakeS3BucketServer(t *testing.T, existingBuckets ...string) *httptest.Server {
	t.Helper()
	buckets := map[string]bool{}
	for _, b := range existingBuckets {
		buckets[b] = true
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bucket := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)[0]
		switch r.Method {
		case http.MethodHead:
			if buckets[bucket] {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut:
			buckets[bucket] = true
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotImplemented)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newCreateBucketRepman builds a ReplicationManager with JWT keys initialised
// and the named cluster registered, with a single API user granted exactly
// the given grants. Unlike newResticRepman, conf is caller-supplied so tests
// can set arbitrary S3/legacy/auto-mode fields.
func newCreateBucketRepman(t *testing.T, clusterName string, conf *config.Config, grants map[string]bool) (*ReplicationManager, string) {
	t.Helper()

	const (
		testUser = "create-bucket-test-user"
		testPass = "create-bucket-test-pass"
	)

	cl := &cluster.Cluster{
		Name: clusterName,
		Conf: conf,
	}
	cl.APIUsers = map[string]cluster.APIUser{
		testUser: {
			User:     testUser,
			Password: testPass,
			Grants:   grants,
		},
	}

	repman := &ReplicationManager{
		Clusters: map[string]*cluster.Cluster{clusterName: cl},
		Conf:     &config.Config{TokenTimeout: 1},
	}
	repman.initKeys()

	tok, err := repman.issueJWT(map[string]interface{}{
		"Name":     testUser,
		"Password": testPass,
	}, "")
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}
	return repman, tok
}

func newS3BucketTestConf(endpoint, bucket string) *config.Config {
	conf := &config.Config{
		BackupResticS3Mode:         config.ConstResticS3ModeNew,
		BackupResticAwsBucket:      bucket,
		BackupResticAwsEndpoint:    endpoint,
		BackupResticAwsAccessKeyId: "AKIAFAKE",
		BackupResticAwsRegion:      "us-east-1",
	}
	conf.Secrets = map[string]config.Secret{
		"backup-restic-aws-access-secret": {Value: "secret"},
	}
	return conf
}

func TestHandlerResticCreateBucket_NoCluster(t *testing.T) {
	repman := newTestRepmanWithCluster(t, "other-cluster", newTestClusterForAPI(t))
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/missing/restic/create-bucket", nil)
	req = setMuxVars(req, map[string]string{"clusterName": "missing"})
	w := httptest.NewRecorder()

	repman.handlerMuxResticCreateBucket(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for missing cluster, got %d", w.Code)
	}
}

func TestHandlerResticCreateBucket_NoACL(t *testing.T) {
	cl := newTestClusterForAPI(t)
	repman := newTestRepmanWithCluster(t, "test-cluster", cl)
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/test-cluster/restic/create-bucket", nil)
	req = setMuxVars(req, map[string]string{"clusterName": "test-cluster"})
	w := httptest.NewRecorder()

	repman.handlerMuxResticCreateBucket(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without JWT, got %d", w.Code)
	}
}

func TestHandlerResticCreateBucket_ProcessOnlyUserDenied(t *testing.T) {
	srv := newFakeS3BucketServer(t)
	conf := newS3BucketTestConf(srv.URL, "some-bucket")
	repman, tok := newCreateBucketRepman(t, "test-cluster", conf, map[string]bool{config.GrantClusterProcess: true})
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/test-cluster/restic/create-bucket", nil)
	req = setMuxVars(req, map[string]string{"clusterName": "test-cluster"})
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", tok))
	w := httptest.NewRecorder()

	repman.handlerMuxResticCreateBucket(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for GrantClusterProcess-only user, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlerResticCreateBucket_BackupUserAllowed(t *testing.T) {
	srv := newFakeS3BucketServer(t)
	conf := newS3BucketTestConf(srv.URL, "missing-bucket")
	repman, tok := newCreateBucketRepman(t, "test-cluster", conf, map[string]bool{config.GrantDBBackup: true})
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/test-cluster/restic/create-bucket", nil)
	req = setMuxVars(req, map[string]string{"clusterName": "test-cluster"})
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", tok))
	w := httptest.NewRecorder()

	repman.handlerMuxResticCreateBucket(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for db-backup user, got %d: %s", w.Code, w.Body.String())
	}
	var result cluster.ResticEnsureBucketResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !result.Created {
		t.Errorf("expected created=true for missing bucket, got %+v", result)
	}
	if result.Bucket != "missing-bucket" {
		t.Errorf("expected bucket=missing-bucket, got %q", result.Bucket)
	}
}

func TestHandlerResticCreateBucket_NonS3ConfigRejected(t *testing.T) {
	conf := &config.Config{
		BackupResticS3Mode:     config.ConstResticS3ModeAuto,
		BackupResticAwsBucket:  "",
		BackupResticRepository: "/local/path/not/s3",
	}
	repman, tok := newCreateBucketRepman(t, "test-cluster", conf, map[string]bool{config.GrantDBBackup: true})
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/test-cluster/restic/create-bucket", nil)
	req = setMuxVars(req, map[string]string{"clusterName": "test-cluster"})
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", tok))
	w := httptest.NewRecorder()

	repman.handlerMuxResticCreateBucket(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-S3 config, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlerResticCreateBucket_UnresolvableAutoModeRejected(t *testing.T) {
	conf := &config.Config{
		BackupResticS3Mode:     config.ConstResticS3ModeAuto,
		BackupResticAwsBucket:  "",
		BackupResticRepository: "",
	}
	repman, tok := newCreateBucketRepman(t, "test-cluster", conf, map[string]bool{config.GrantDBBackup: true})
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/test-cluster/restic/create-bucket", nil)
	req = setMuxVars(req, map[string]string{"clusterName": "test-cluster"})
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", tok))
	w := httptest.NewRecorder()

	repman.handlerMuxResticCreateBucket(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unresolvable auto mode, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandlerResticCreateBucket_ResticDisabledStillWorks verifies pre-provisioning
// works even when backup-restic itself is not yet enabled.
func TestHandlerResticCreateBucket_ResticDisabledStillWorks(t *testing.T) {
	srv := newFakeS3BucketServer(t)
	conf := newS3BucketTestConf(srv.URL, "preprovision-bucket")
	conf.BackupRestic = false
	repman, tok := newCreateBucketRepman(t, "test-cluster", conf, map[string]bool{config.GrantDBBackup: true})
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/test-cluster/restic/create-bucket", nil)
	req = setMuxVars(req, map[string]string{"clusterName": "test-cluster"})
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", tok))
	w := httptest.NewRecorder()

	repman.handlerMuxResticCreateBucket(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 when restic disabled but S3 config saved, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandlerResticCreateBucket_InactiveRepositoryTypeStillWorks verifies the
// saved S3 target is used even when a different repository type (local) is
// the currently active one.
func TestHandlerResticCreateBucket_InactiveRepositoryTypeStillWorks(t *testing.T) {
	srv := newFakeS3BucketServer(t)
	conf := newS3BucketTestConf(srv.URL, "inactive-type-bucket")
	conf.BackupArchiveMode = config.ConstBackupArchiveModeResticLocal
	conf.BackupResticLocalRepository = "/var/lib/repman/backup/archive"
	repman, tok := newCreateBucketRepman(t, "test-cluster", conf, map[string]bool{config.GrantDBBackup: true})
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/test-cluster/restic/create-bucket", nil)
	req = setMuxVars(req, map[string]string{"clusterName": "test-cluster"})
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", tok))
	w := httptest.NewRecorder()

	repman.handlerMuxResticCreateBucket(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 when S3 saved but local is active, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlerResticCreateBucket_LegacyURLMode(t *testing.T) {
	srv := newFakeS3BucketServer(t)
	conf := &config.Config{
		BackupResticS3Mode:         config.ConstResticS3ModeLegacy,
		BackupResticRepository:     "s3:" + srv.URL + "/legacy-bucket",
		BackupResticAwsAccessKeyId: "AKIAFAKE",
		BackupResticAwsRegion:      "us-east-1",
	}
	conf.Secrets = map[string]config.Secret{
		"backup-restic-aws-access-secret": {Value: "secret"},
	}
	repman, tok := newCreateBucketRepman(t, "test-cluster", conf, map[string]bool{config.GrantDBBackup: true})
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/test-cluster/restic/create-bucket", nil)
	req = setMuxVars(req, map[string]string{"clusterName": "test-cluster"})
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", tok))
	w := httptest.NewRecorder()

	repman.handlerMuxResticCreateBucket(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for legacy S3 URL mode, got %d: %s", w.Code, w.Body.String())
	}
	var result cluster.ResticEnsureBucketResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result.Bucket != "legacy-bucket" {
		t.Errorf("expected bucket=legacy-bucket, got %q", result.Bucket)
	}
}

// TestHandlerResticCreateBucket_ResponseDistinguishesCreatedTrueFalse verifies
// the first call against a missing bucket reports created=true, and a second
// call against the same (now-existing) bucket reports created=false.
func TestHandlerResticCreateBucket_ResponseDistinguishesCreatedTrueFalse(t *testing.T) {
	srv := newFakeS3BucketServer(t)
	conf := newS3BucketTestConf(srv.URL, "idempotent-bucket")
	repman, tok := newCreateBucketRepman(t, "test-cluster", conf, map[string]bool{config.GrantDBBackup: true})

	doRequest := func() cluster.ResticEnsureBucketResult {
		req := httptest.NewRequest(http.MethodPost, "/api/clusters/test-cluster/restic/create-bucket", nil)
		req = setMuxVars(req, map[string]string{"clusterName": "test-cluster"})
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", tok))
		w := httptest.NewRecorder()
		repman.handlerMuxResticCreateBucket(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var result cluster.ResticEnsureBucketResult
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		return result
	}

	first := doRequest()
	if !first.Created {
		t.Errorf("expected created=true on first call, got %+v", first)
	}
	second := doRequest()
	if second.Created {
		t.Errorf("expected created=false on second (idempotent) call, got %+v", second)
	}
}
