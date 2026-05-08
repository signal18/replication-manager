package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// makeAuthedUpgradeRequest builds a GET /api/login/upgrade request carrying a
// valid JWT for the given username, signed with the repman test keys.
func makeAuthedUpgradeRequest(t *testing.T, repman *ReplicationManager, upgradeID, username string) *http.Request {
	t.Helper()
	tokenStr, err := repman.issueJWT(struct {
		Name     string
		Role     string
		Password string
	}{username, "Member", "enc"}, "")
	if err != nil {
		t.Fatalf("makeAuthedUpgradeRequest: issueJWT: %v", err)
	}
	url := "/api/login/upgrade"
	if upgradeID != "" {
		url += "?upgrade_id=" + upgradeID
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	return req
}

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
		"API error: unauthorized - user not found",
		"some completely unknown error",
		"resource owner error",
	}
	for _, msg := range cases {
		if got := classifySSOAuthError(errors.New(msg)); got != "unknown_non_retryable" {
			t.Errorf("msg=%q: expected unknown_non_retryable, got %q", msg, got)
		}
	}
}

// ─── clientIP ────────────────────────────────────────────────────────────────

func TestClientIP_StripPort(t *testing.T) {
	cases := []struct{ addr, want string }{
		{"1.2.3.4:56789", "1.2.3.4"},
		{"[::1]:8080", "::1"},
		{"192.168.1.1:0", "192.168.1.1"},
		{"bare-host", "bare-host"}, // no port: returned as-is
	}
	for _, c := range cases {
		if got := clientIP(c.addr); got != c.want {
			t.Errorf("clientIP(%q) = %q, want %q", c.addr, got, c.want)
		}
	}
}

// mustCreateJob calls createJob and fatals the test if the store returns an error.
func mustCreateJob(t *testing.T, s *LoginUpgradeStore, owner, sourceIP string) (string, *LoginUpgradeJob) {
	t.Helper()
	id, job, err := s.createJob(owner, sourceIP)
	if err != nil {
		t.Fatalf("mustCreateJob: unexpected error: %v", err)
	}
	return id, job
}

// ─── LoginUpgradeStore ───────────────────────────────────────────────────────

func TestLoginUpgradeStore_CreateAndGet(t *testing.T) {
	s := newLoginUpgradeStore()
	id, job := mustCreateJob(t, s, "alice", "1.2.3.4:0")
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
	if got.Owner != "alice" {
		t.Errorf("expected Owner=alice, got %q", got.Owner)
	}
	if got.SourceIP != "1.2.3.4:0" {
		t.Errorf("expected SourceIP=1.2.3.4:0, got %q", got.SourceIP)
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
		id, _ := mustCreateJob(t, s, "u", "")
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
	repman.Conf = &config.Config{TokenTimeout: 1} // non-zero so JWTs don't expire immediately
	repman.LoginUpgradeStore = newLoginUpgradeStore()
	repman.initKeys()
	t.Cleanup(func() { repman.LoginUpgradeStore.Shutdown() })
	return repman
}

// ── auth / security ──

func TestUpgradeHandler_OPTIONS_Preflight(t *testing.T) {
	repman := newTestRepmanWithStore(t)
	req := httptest.NewRequest(http.MethodOptions, "/api/login/upgrade", nil)
	w := httptest.NewRecorder()
	repman.upgradeHandler(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for OPTIONS, got %d", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("expected Access-Control-Allow-Methods header on OPTIONS")
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Error("expected Access-Control-Allow-Headers header on OPTIONS")
	}
}

func TestUpgradeHandler_NoAuth_401(t *testing.T) {
	repman := newTestRepmanWithStore(t)
	id, _ := mustCreateJob(t, repman.LoginUpgradeStore, "alice", "")
	req := httptest.NewRequest(http.MethodGet, "/api/login/upgrade?upgrade_id="+id, nil)
	// deliberately no Authorization header
	w := httptest.NewRecorder()
	repman.upgradeHandler(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with no auth, got %d", w.Code)
	}
}

func TestUpgradeHandler_WrongUser_403(t *testing.T) {
	repman := newTestRepmanWithStore(t)
	id, _ := mustCreateJob(t, repman.LoginUpgradeStore, "alice", "")
	// request signed as "bob", not the owner "alice"
	req := makeAuthedUpgradeRequest(t, repman, id, "bob")
	w := httptest.NewRecorder()
	repman.upgradeHandler(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for wrong user, got %d", w.Code)
	}
}

// ── missing / unknown id ──

func TestUpgradeHandler_MissingID(t *testing.T) {
	repman := newTestRepmanWithStore(t)
	req := makeAuthedUpgradeRequest(t, repman, "", "alice")
	w := httptest.NewRecorder()
	repman.upgradeHandler(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestUpgradeHandler_UnknownID(t *testing.T) {
	repman := newTestRepmanWithStore(t)
	req := makeAuthedUpgradeRequest(t, repman, "doesnotexist", "alice")
	w := httptest.NewRecorder()
	repman.upgradeHandler(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// ── state machine ──

func TestUpgradeHandler_Pending(t *testing.T) {
	repman := newTestRepmanWithStore(t)
	id, _ := mustCreateJob(t, repman.LoginUpgradeStore, "alice", "")
	w := httptest.NewRecorder()
	repman.upgradeHandler(w, makeAuthedUpgradeRequest(t, repman, id, "alice"))
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", w.Code)
	}
}

func TestUpgradeHandler_Ready_OneTimeDelivery(t *testing.T) {
	repman := newTestRepmanWithStore(t)
	id, job := mustCreateJob(t, repman.LoginUpgradeStore, "alice", "")

	job.mu.Lock()
	job.Status = "ready"
	job.NewJWT = "test-jwt-string"
	job.ReadyAt = time.Now()
	job.mu.Unlock()

	// First poll: expect 200 with token.
	w := httptest.NewRecorder()
	repman.upgradeHandler(w, makeAuthedUpgradeRequest(t, repman, id, "alice"))
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
	w2 := httptest.NewRecorder()
	repman.upgradeHandler(w2, makeAuthedUpgradeRequest(t, repman, id, "alice"))
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
			id, job := mustCreateJob(t, repman.LoginUpgradeStore, "alice", "")
			job.mu.Lock()
			job.Status = "failed"
			job.Reason = reason
			job.mu.Unlock()

			w := httptest.NewRecorder()
			repman.upgradeHandler(w, makeAuthedUpgradeRequest(t, repman, id, "alice"))
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
			if _, ok := resp["error"]; ok {
				t.Error("internal error detail must not be in 409 body")
			}
		})
	}
}

func TestUpgradeHandler_Expired_410(t *testing.T) {
	repman := newTestRepmanWithStore(t)
	id, job := mustCreateJob(t, repman.LoginUpgradeStore, "alice", "")
	job.mu.Lock()
	job.Status = "pending"
	job.CreatedAt = time.Now().Add(-upgradeJobTTL - time.Second)
	job.mu.Unlock()

	w := httptest.NewRecorder()
	repman.upgradeHandler(w, makeAuthedUpgradeRequest(t, repman, id, "alice"))
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

// Fix 3: ready job must NOT be expired by the pending TTL — it has its own window.
func TestUpgradeHandler_ReadyJob_NotExpiredByPendingTTL(t *testing.T) {
	repman := newTestRepmanWithStore(t)
	id, job := mustCreateJob(t, repman.LoginUpgradeStore, "alice", "")
	job.mu.Lock()
	// Simulate SSO completing just before the pending TTL would expire.
	job.CreatedAt = time.Now().Add(-upgradeJobTTL - time.Second)
	job.Status = "ready"
	job.NewJWT = "sso-jwt"
	job.ReadyAt = time.Now() // just became ready; delivery window is fresh
	job.mu.Unlock()

	w := httptest.NewRecorder()
	repman.upgradeHandler(w, makeAuthedUpgradeRequest(t, repman, id, "alice"))
	if w.Code != http.StatusOK {
		t.Fatalf("ready job past pending TTL should still deliver 200, got %d", w.Code)
	}
}

// Fix 3: ready job expires after its own delivery TTL.
func TestUpgradeHandler_ReadyJob_ExpiresAfterDeliveryTTL(t *testing.T) {
	repman := newTestRepmanWithStore(t)
	id, job := mustCreateJob(t, repman.LoginUpgradeStore, "alice", "")
	job.mu.Lock()
	job.Status = "ready"
	job.NewJWT = "sso-jwt"
	job.ReadyAt = time.Now().Add(-upgradeJobReadyDeliveryTTL - time.Second)
	job.mu.Unlock()

	w := httptest.NewRecorder()
	repman.upgradeHandler(w, makeAuthedUpgradeRequest(t, repman, id, "alice"))
	if w.Code != http.StatusGone {
		t.Fatalf("ready job past delivery TTL should return 410, got %d", w.Code)
	}
}

// ─── cleanup goroutine ───────────────────────────────────────────────────────

func TestCleanupExpiredJobs(t *testing.T) {
	repman := newTestRepmanWithStore(t)

	freshID, _ := mustCreateJob(t, repman.LoginUpgradeStore, "u", "")

	expiredID, expiredJob := mustCreateJob(t, repman.LoginUpgradeStore, "u", "")
	expiredJob.mu.Lock()
	expiredJob.Status = "expired"
	expiredJob.TerminalAt = time.Now().Add(-(upgradeJobGracePeriod + time.Second))
	expiredJob.mu.Unlock()

	consumedID, consumedJob := mustCreateJob(t, repman.LoginUpgradeStore, "u", "")
	consumedJob.mu.Lock()
	consumedJob.Status = "consumed"
	consumedJob.TerminalAt = time.Now().Add(-(upgradeJobGracePeriod + time.Second))
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
	id, job := mustCreateJob(t, repman.LoginUpgradeStore, "u", "")
	job.mu.Lock()
	job.CreatedAt = time.Now().Add(-(upgradeJobTTL + time.Second))
	job.mu.Unlock()

	repman.cleanupExpiredLoginUpgradeJobs()

	got, ok := repman.LoginUpgradeStore.get(id)
	if !ok {
		t.Fatal("expected job still present within grace period")
	}
	got.mu.Lock()
	status := got.Status
	got.mu.Unlock()
	if status != "expired" {
		t.Fatalf("expected status=expired after TTL, got %q", status)
	}
}

// Fix 3: cleanup must not expire a ready job based on pending TTL.
func TestCleanupDoesNotExpireReadyJobByPendingTTL(t *testing.T) {
	repman := newTestRepmanWithStore(t)
	id, job := mustCreateJob(t, repman.LoginUpgradeStore, "u", "")
	job.mu.Lock()
	job.Status = "ready"
	job.NewJWT = "jwt"
	job.CreatedAt = time.Now().Add(-(upgradeJobTTL + time.Second))
	job.ReadyAt = time.Now() // fresh delivery window
	job.mu.Unlock()

	repman.cleanupExpiredLoginUpgradeJobs()

	got, ok := repman.LoginUpgradeStore.get(id)
	if !ok {
		t.Fatal("ready job within delivery window should still be in store")
	}
	got.mu.Lock()
	status := got.Status
	got.mu.Unlock()
	if status != "ready" {
		t.Fatalf("ready job should remain ready, got %q", status)
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
		"c1": newClusterWithUser("admin", map[string]bool{config.GrantGlobalAdminShow: true}),
	}
	if !repman.isAdminUser("admin") {
		t.Error("expected admin to be detected as admin")
	}
}

func TestIsAdminUser_NonAdmin(t *testing.T) {
	repman := &ReplicationManager{}
	repman.Clusters = map[string]*cluster.Cluster{
		"c1": newClusterWithUser("alice", map[string]bool{config.GrantGlobalAdminShow: false}),
	}
	if repman.isAdminUser("alice") {
		t.Error("expected alice not to be an admin")
	}
}

func TestIsAdminUser_UnknownUser(t *testing.T) {
	repman := &ReplicationManager{}
	repman.Clusters = map[string]*cluster.Cluster{
		"c1": newClusterWithUser("admin", map[string]bool{config.GrantGlobalAdminShow: true}),
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
	id, job := mustCreateJob(t, repman.LoginUpgradeStore, "alice", "")
	job.mu.Lock()
	job.CreatedAt = time.Now().Add(-(upgradeJobTTL + time.Second))
	job.mu.Unlock()

	w := httptest.NewRecorder()
	repman.upgradeHandler(w, makeAuthedUpgradeRequest(t, repman, id, "alice"))
	if w.Code != http.StatusGone {
		t.Fatalf("expected 410 for TTL-exceeded pending job at handler, got %d", w.Code)
	}
}

