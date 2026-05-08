package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// ─── classifySSOAuthError ────────────────────────────────────────────────────

func TestClassifySSOAuthError_Nil(t *testing.T) {
	if got := classifySSOAuthError(nil); got != "" {
		t.Fatalf("expected empty string for nil err, got %q", got)
	}
}

func TestClassifySSOAuthError_Transient(t *testing.T) {
	cases := []string{
		"error sending request: connection refused",
		"error creating request: bad url",
		"API error: received non-OK HTTP status 503 and failed to parse error response",
		"received non-OK HTTP status 500 ...",
		"received non-OK HTTP status 429 ...",
	}
	for _, msg := range cases {
		if got := classifySSOAuthError(errors.New(msg)); got != "transient" {
			t.Errorf("msg=%q: expected transient, got %q", msg, got)
		}
	}
}

func TestClassifySSOAuthError_CredentialMismatch(t *testing.T) {
	cases := []string{
		"API error: invalid_grant - Invalid credentials",
		"API error: invalid_grant - invalid credentials for user",
	}
	for _, msg := range cases {
		if got := classifySSOAuthError(errors.New(msg)); got != "credential_mismatch" {
			t.Errorf("msg=%q: expected credential_mismatch, got %q", msg, got)
		}
	}
}

func TestClassifySSOAuthError_NotRegistered(t *testing.T) {
	msg := "API error: invalid_grant - authenticatable_not_found"
	if got := classifySSOAuthError(errors.New(msg)); got != "not_registered" {
		t.Errorf("msg=%q: expected not_registered, got %q", msg, got)
	}
}

func TestClassifySSOAuthError_UnknownNonRetryable(t *testing.T) {
	cases := []string{
		"API error: invalid_grant - The provided authorization grant is invalid",
		"API error: invalid_grant - some other reason",
		"API error: unauthorized - user not found",   // broad "not found" → must NOT classify as not_registered
		"some completely unknown error",
		"resource owner error",                        // must NOT classify as not_registered
	}
	for _, msg := range cases {
		if got := classifySSOAuthError(errors.New(msg)); got != "unknown_non_retryable" {
			t.Errorf("msg=%q: expected unknown_non_retryable, got %q", msg, got)
		}
	}
}

// ─── LoginUpgradeStore ───────────────────────────────────────────────────────

func TestLoginUpgradeStore_CreateAndGet(t *testing.T) {
	s := newLoginUpgradeStore()
	id, job := s.createJob()
	if id == "" {
		t.Fatal("expected non-empty upgrade_id")
	}
	if job == nil {
		t.Fatal("expected non-nil job")
	}
	got, ok := s.get(id)
	if !ok {
		t.Fatal("expected job to be found in store")
	}
	if got != job {
		t.Fatal("expected same job pointer")
	}
	if got.Status != "pending" {
		t.Fatalf("expected status=pending, got %q", got.Status)
	}
}

func TestLoginUpgradeStore_MissingID(t *testing.T) {
	s := newLoginUpgradeStore()
	_, ok := s.get("nonexistent")
	if ok {
		t.Fatal("expected miss for unknown id")
	}
}

func TestLoginUpgradeStore_UniqueIDs(t *testing.T) {
	s := newLoginUpgradeStore()
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, _ := s.createJob()
		if seen[id] {
			t.Fatalf("duplicate upgrade_id generated: %s", id)
		}
		seen[id] = true
	}
}

// ─── upgradeHandler state machine ────────────────────────────────────────────

func newTestRepmanWithStore(t *testing.T) *ReplicationManager {
	t.Helper()
	repman := &ReplicationManager{}
	repman.Conf = &config.Config{}
	repman.LoginUpgradeStore = newLoginUpgradeStore()
	repman.initKeys()
	return repman
}

func TestUpgradeHandler_MissingID(t *testing.T) {
	repman := newTestRepmanWithStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/login/upgrade", nil)
	w := httptest.NewRecorder()
	repman.upgradeHandler(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestUpgradeHandler_UnknownID(t *testing.T) {
	repman := newTestRepmanWithStore(t)
	req := httptest.NewRequest(http.MethodGet, "/api/login/upgrade?upgrade_id=doesnotexist", nil)
	w := httptest.NewRecorder()
	repman.upgradeHandler(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestUpgradeHandler_Pending(t *testing.T) {
	repman := newTestRepmanWithStore(t)
	id, _ := repman.LoginUpgradeStore.createJob()
	req := httptest.NewRequest(http.MethodGet, "/api/login/upgrade?upgrade_id="+id, nil)
	w := httptest.NewRecorder()
	repman.upgradeHandler(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w.Code)
	}
}

func TestUpgradeHandler_Ready_OneTimeDelivery(t *testing.T) {
	repman := newTestRepmanWithStore(t)
	id, job := repman.LoginUpgradeStore.createJob()

	// Simulate SSO upgrade completing.
	job.mu.Lock()
	job.Status = "ready"
	job.NewJWT = "test-jwt-string"
	job.mu.Unlock()

	// First poll: expect 200 with token.
	req := httptest.NewRequest(http.MethodGet, "/api/login/upgrade?upgrade_id="+id, nil)
	w := httptest.NewRecorder()
	repman.upgradeHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first poll: expected 200, got %d", w.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode first poll response: %v", err)
	}
	if resp["token"] != "test-jwt-string" {
		t.Fatalf("expected token=test-jwt-string, got %q", resp["token"])
	}

	// Verify NewJWT cleared immediately (no replay possible).
	job.mu.Lock()
	if job.NewJWT != "" {
		t.Error("expected NewJWT cleared after one-time delivery")
	}
	if job.Status != "consumed" {
		t.Errorf("expected status=consumed, got %q", job.Status)
	}
	job.mu.Unlock()

	// Second poll: expect 410 consumed.
	req2 := httptest.NewRequest(http.MethodGet, "/api/login/upgrade?upgrade_id="+id, nil)
	w2 := httptest.NewRecorder()
	repman.upgradeHandler(w2, req2)
	if w2.Code != http.StatusGone {
		t.Fatalf("second poll: expected 410, got %d", w2.Code)
	}
	var resp2 map[string]string
	if err := json.NewDecoder(w2.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode second poll response: %v", err)
	}
	if resp2["status"] != "consumed" {
		t.Fatalf("expected status=consumed, got %q", resp2["status"])
	}
}

func TestUpgradeHandler_Failed_409WithReason(t *testing.T) {
	reasons := []string{
		"credential_mismatch",
		"not_registered",
		"unknown_non_retryable",
		"retry_exhausted",
		"claim_mismatch",
	}
	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			repman := newTestRepmanWithStore(t)
			id, job := repman.LoginUpgradeStore.createJob()
			job.mu.Lock()
			job.Status = "failed"
			job.Reason = reason
			job.mu.Unlock()

			req := httptest.NewRequest(http.MethodGet, "/api/login/upgrade?upgrade_id="+id, nil)
			w := httptest.NewRecorder()
			repman.upgradeHandler(w, req)
			if w.Code != http.StatusConflict {
				t.Fatalf("reason=%s: expected 409, got %d", reason, w.Code)
			}
			var resp map[string]interface{}
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode 409 response: %v", err)
			}
			if resp["reason"] != reason {
				t.Errorf("expected reason=%s in body, got %v", reason, resp["reason"])
			}
			// Verify no raw internal error is exposed.
			if _, ok := resp["error"]; ok {
				t.Error("internal error detail must not be in 409 body")
			}
		})
	}
}

func TestUpgradeHandler_Expired_410(t *testing.T) {
	repman := newTestRepmanWithStore(t)
	id, job := repman.LoginUpgradeStore.createJob()
	job.mu.Lock()
	job.Status = "pending"
	job.CreatedAt = time.Now().Add(-upgradeJobTTL - time.Second) // simulate TTL elapsed
	job.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/login/upgrade?upgrade_id="+id, nil)
	w := httptest.NewRecorder()
	repman.upgradeHandler(w, req)
	if w.Code != http.StatusGone {
		t.Fatalf("expected 410 for expired job, got %d", w.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode 410 response: %v", err)
	}
	if resp["status"] != "expired" {
		t.Fatalf("expected status=expired, got %q", resp["status"])
	}
}

// ─── cleanup goroutine ───────────────────────────────────────────────────────

func TestCleanupExpiredJobs(t *testing.T) {
	repman := newTestRepmanWithStore(t)

	// A fresh pending job — should NOT be deleted.
	freshID, _ := repman.LoginUpgradeStore.createJob()

	// An expired job older than grace period — should be deleted.
	expiredID, expiredJob := repman.LoginUpgradeStore.createJob()
	expiredJob.mu.Lock()
	expiredJob.Status = "expired"
	expiredJob.CreatedAt = time.Now().Add(-(upgradeJobGracePeriod + time.Second))
	expiredJob.mu.Unlock()

	// A consumed job older than grace period — should be deleted.
	consumedID, consumedJob := repman.LoginUpgradeStore.createJob()
	consumedJob.mu.Lock()
	consumedJob.Status = "consumed"
	consumedJob.CreatedAt = time.Now().Add(-(upgradeJobGracePeriod + time.Second))
	consumedJob.mu.Unlock()

	repman.cleanupExpiredLoginUpgradeJobs()

	if _, ok := repman.LoginUpgradeStore.get(freshID); !ok {
		t.Error("fresh pending job should still be in store")
	}
	if _, ok := repman.LoginUpgradeStore.get(expiredID); ok {
		t.Error("expired grace-period-exceeded job should have been removed")
	}
	if _, ok := repman.LoginUpgradeStore.get(consumedID); ok {
		t.Error("consumed grace-period-exceeded job should have been removed")
	}
}

func TestCleanupMarksPendingAsExpired(t *testing.T) {
	repman := newTestRepmanWithStore(t)
	id, job := repman.LoginUpgradeStore.createJob()
	job.mu.Lock()
	job.CreatedAt = time.Now().Add(-(upgradeJobTTL + time.Second))
	job.mu.Unlock()

	repman.cleanupExpiredLoginUpgradeJobs()

	got, ok := repman.LoginUpgradeStore.get(id)
	if !ok {
		// Within grace period so it should still exist, just expired.
		t.Fatal("expected job still present within grace period")
	}
	got.mu.Lock()
	status := got.Status
	got.mu.Unlock()
	if status != "expired" {
		t.Fatalf("expected status=expired after TTL, got %q", status)
	}
}

// ─── issueJWT ────────────────────────────────────────────────────────────────

func TestIssueJWT_NonEmptyToken(t *testing.T) {
	repman := &ReplicationManager{}
	repman.Conf = &config.Config{TokenTimeout: 1}
	repman.initKeys()

	userInfo := struct {
		Name     string
		Role     string
		Password string
	}{"alice", "Member", "enc-pw"}

	tok, err := repman.issueJWT(userInfo, "gitlab-token-abc")
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}
	if tok == "" {
		t.Fatal("expected non-empty JWT")
	}
}

func TestIssueJWT_SSO_IncludesGitLabToken(t *testing.T) {
	repman := &ReplicationManager{}
	repman.Conf = &config.Config{TokenTimeout: 1}
	repman.initKeys()

	userInfo := struct {
		Name       string
		Role       string
		Password   string
		Email      string `json:"email"`
		Profile    string `json:"profile"`
		MeetUserID string `json:"meet_user_id"`
	}{"alice", "Member", "enc", "alice@example.com", "https://gitlab.signal18.io", "mid"}

	tok, err := repman.issueJWT(userInfo, "sso-token-xyz")
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}

	// Verify the token claim round-trips correctly.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	gitlabTok, err := repman.GetJWTGitLabToken(req)
	if err != nil {
		t.Fatalf("GetJWTGitLabToken: %v", err)
	}
	if gitlabTok != "sso-token-xyz" {
		t.Fatalf("expected sso-token-xyz, got %q", gitlabTok)
	}
}

// ─── isAdminUser ─────────────────────────────────────────────────────────────

func newClusterWithUser(username string, grants map[string]bool) *cluster.Cluster {
	cl := &cluster.Cluster{}
	cl.APIUsers = map[string]cluster.APIUser{
		username: {User: username, Grants: grants},
	}
	return cl
}

func TestIsAdminUser_Admin(t *testing.T) {
	repman := &ReplicationManager{}
	repman.Clusters = map[string]*cluster.Cluster{
		"c1": newClusterWithUser("admin", map[string]bool{
			config.GrantGlobalAdminShow: true,
		}),
	}
	if !repman.isAdminUser("admin") {
		t.Error("expected admin to be detected as admin")
	}
}

func TestIsAdminUser_NonAdmin(t *testing.T) {
	repman := &ReplicationManager{}
	repman.Clusters = map[string]*cluster.Cluster{
		"c1": newClusterWithUser("alice", map[string]bool{
			config.GrantGlobalAdminShow: false,
		}),
	}
	if repman.isAdminUser("alice") {
		t.Error("expected alice not to be an admin")
	}
}

func TestIsAdminUser_UnknownUser(t *testing.T) {
	repman := &ReplicationManager{}
	repman.Clusters = map[string]*cluster.Cluster{
		"c1": newClusterWithUser("admin", map[string]bool{
			config.GrantGlobalAdminShow: true,
		}),
	}
	if repman.isAdminUser("nobody") {
		t.Error("expected unknown user not to be detected as admin")
	}
}

// ─── resolveUserEmail ─────────────────────────────────────────────────────────

func TestResolveUserEmail_EmailUsername(t *testing.T) {
	repman := &ReplicationManager{}
	if got := repman.resolveUserEmail("alice@example.com"); got != "alice@example.com" {
		t.Errorf("expected alice@example.com, got %q", got)
	}
}

func TestResolveUserEmail_NoMatch(t *testing.T) {
	repman := &ReplicationManager{}
	repman.Clusters = map[string]*cluster.Cluster{
		"c1": newClusterWithUser("alice", map[string]bool{}),
	}
	if got := repman.resolveUserEmail("alice"); got != "" {
		t.Errorf("expected empty string for non-email username, got %q", got)
	}
}

// ─── AuthTokenWithUpgrade response shape ─────────────────────────────────────

func TestAuthTokenWithUpgrade_OmitsUpgradeIDWhenEmpty(t *testing.T) {
	resp := AuthTokenWithUpgrade{Token: "tok", UpgradeID: ""}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["upgrade_id"]; ok {
		t.Error("upgrade_id should be omitted when empty")
	}
}

func TestAuthTokenWithUpgrade_IncludesUpgradeIDWhenSet(t *testing.T) {
	resp := AuthTokenWithUpgrade{Token: "tok", UpgradeID: "abc123"}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["upgrade_id"] != "abc123" {
		t.Errorf("expected upgrade_id=abc123, got %v", m["upgrade_id"])
	}
}

// ─── upgrade job TTL enforcement at handler level ────────────────────────────

func TestUpgradeHandler_PendingBecomesExpiredAtHandler(t *testing.T) {
	repman := newTestRepmanWithStore(t)
	id, job := repman.LoginUpgradeStore.createJob()
	job.mu.Lock()
	job.CreatedAt = time.Now().Add(-(upgradeJobTTL + time.Second))
	job.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/login/upgrade?upgrade_id=%s", id), nil)
	w := httptest.NewRecorder()
	repman.upgradeHandler(w, req)
	if w.Code != http.StatusGone {
		t.Fatalf("expected 410 for TTL-exceeded pending job at handler, got %d", w.Code)
	}
}
