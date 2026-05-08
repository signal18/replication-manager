// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// License: GNU General Public License, version 3.

package server

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/alert/mailer"
	"github.com/signal18/replication-manager/utils/githelper"
	"github.com/signal18/replication-manager/utils/meethelper"
)

const (
	upgradeJobTTL              = 60 * time.Second
	upgradeJobReadyDeliveryTTL = 30 * time.Second // window from ready→consumed before expiry
	upgradeJobGracePeriod      = 2 * upgradeJobTTL
	upgradeMaxAttempts         = 3
)

var upgradeBackoffDelays = []time.Duration{250 * time.Millisecond, 750 * time.Millisecond}

// LoginUpgradeJob tracks the state of an async SSO upgrade started during login.
type LoginUpgradeJob struct {
	mu         sync.Mutex
	Status     string    // pending, ready, failed, consumed, expired
	NewJWT     string    // set on success, cleared after one-time delivery
	Reason     string    // credential_mismatch, not_registered, unknown_non_retryable, claim_mismatch, retry_exhausted
	Error      string    // internal detail, never exposed to client
	Attempts   int
	CreatedAt  time.Time
	ReadyAt    time.Time // set when SSO upgrade succeeds; governs delivery TTL
	TerminalAt time.Time // set when job first reaches a terminal state; grace period measured from here
	Owner      string    // username from the login JWT; used to bind poll requests
	SourceIP   string    // login request IP; used for soft change detection
}

// LoginUpgradeStore is an in-memory map of upgrade jobs keyed by upgrade_id.
type LoginUpgradeStore struct {
	mu           sync.RWMutex
	Jobs         map[string]*LoginUpgradeJob
	done         chan struct{} // closed by Shutdown to stop the background cleanup goroutine
	shutdownOnce sync.Once
}

func newLoginUpgradeStore() *LoginUpgradeStore {
	return &LoginUpgradeStore{
		Jobs: make(map[string]*LoginUpgradeJob),
		done: make(chan struct{}),
	}
}

// Shutdown stops the background cleanup goroutine associated with this store.
// It is safe to call multiple times (subsequent calls are no-ops).
func (s *LoginUpgradeStore) Shutdown() {
	s.shutdownOnce.Do(func() {
		if s.done != nil {
			close(s.done)
		}
	})
}

func newUpgradeID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("newUpgradeID: crypto/rand unavailable: %w", err)
	}
	return fmt.Sprintf("%x", b), nil
}

// clientIP strips the port from an addr:port string so IPs can be compared.
func clientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

func (s *LoginUpgradeStore) createJob(owner, sourceIP string) (string, *LoginUpgradeJob, error) {
	id, err := newUpgradeID()
	if err != nil {
		return "", nil, err
	}
	job := &LoginUpgradeJob{
		Status:    "pending",
		CreatedAt: time.Now(),
		Owner:     owner,
		SourceIP:  sourceIP,
	}
	s.mu.Lock()
	s.Jobs[id] = job
	s.mu.Unlock()
	return id, job, nil
}

func (s *LoginUpgradeStore) get(id string) (*LoginUpgradeJob, bool) {
	s.mu.RLock()
	job, ok := s.Jobs[id]
	s.mu.RUnlock()
	return job, ok
}

// upgradeHandler serves GET /api/login/upgrade?upgrade_id=<id>
func (repman *ReplicationManager) upgradeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Fix 2: answer CORS preflight before any other processing.
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Fix 1: require the local JWT issued at login; bind poll to its owner.
	owner := repman.GetUserFromRequest(r)
	if owner == "" {
		http.Error(w, `{"error":"authorization required"}`, http.StatusUnauthorized)
		return
	}

	id := r.URL.Query().Get("upgrade_id")
	if id == "" {
		http.Error(w, `{"error":"missing upgrade_id"}`, http.StatusBadRequest)
		return
	}

	if repman.LoginUpgradeStore == nil {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	job, ok := repman.LoginUpgradeStore.get(id)
	if !ok {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		return
	}

	job.mu.Lock()
	defer job.mu.Unlock()

	// Fix 1 (cont.): verify the polling client owns this job.
	if job.Owner != owner {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}

	// Fix 1 (soft): log if source IP changed (mobile/VPN can legitimately change IP).
	if job.SourceIP != "" && clientIP(r.RemoteAddr) != clientIP(job.SourceIP) {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn,
			"SSO upgrade poll from different IP: job=%s poll=%s user=%s",
			clientIP(job.SourceIP), clientIP(r.RemoteAddr), owner)
	}

	// Fix 3: expire only pending jobs on the pending TTL; ready jobs have their
	// own delivery window measured from when the upgrade completed.
	if job.Status == "pending" && time.Since(job.CreatedAt) > upgradeJobTTL {
		job.Status = "expired"
		job.TerminalAt = time.Now()
	}
	if job.Status == "ready" && !job.ReadyAt.IsZero() && time.Since(job.ReadyAt) > upgradeJobReadyDeliveryTTL {
		job.Status = "expired"
		job.TerminalAt = time.Now()
	}

	switch job.Status {
	case "pending":
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"status": "pending"})

	case "ready":
		newJWT := job.NewJWT
		job.Status = "consumed"
		job.TerminalAt = time.Now()
		job.NewJWT = "" // one-time delivery; clear immediately to prevent replay
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"token": newJWT})

	case "consumed":
		w.WriteHeader(http.StatusGone)
		json.NewEncoder(w).Encode(map[string]string{"status": "consumed"})

	case "expired":
		w.WriteHeader(http.StatusGone)
		json.NewEncoder(w).Encode(map[string]string{"status": "expired"})

	case "failed":
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "failed",
			"reason": job.Reason,
		})

	default:
		http.Error(w, `{"error":"unknown job state"}`, http.StatusInternalServerError)
	}
}

// cleanupExpiredLoginUpgradeJobs sweeps the upgrade store:
//   - marks pending/ready jobs as expired once they exceed upgradeJobTTL (60s)
//   - retains consumed/expired/failed jobs for an additional grace period
//     (upgradeJobGracePeriod = 2×TTL) so that repeat polls return a
//     deterministic 410 response rather than 404; after the grace period
//     the marker is removed entirely
func (repman *ReplicationManager) cleanupExpiredLoginUpgradeJobs() {
	if repman.LoginUpgradeStore == nil {
		return
	}
	repman.LoginUpgradeStore.mu.Lock()
	defer repman.LoginUpgradeStore.mu.Unlock()

	for id, job := range repman.LoginUpgradeStore.Jobs {
		job.mu.Lock()
		age := time.Since(job.CreatedAt)

		// Fix 3: separate TTLs for pending and ready states.
		if job.Status == "pending" && age > upgradeJobTTL {
			job.Status = "expired"
			job.TerminalAt = time.Now()
		}
		if job.Status == "ready" && !job.ReadyAt.IsZero() && time.Since(job.ReadyAt) > upgradeJobReadyDeliveryTTL {
			job.Status = "expired"
			job.TerminalAt = time.Now()
		}

		// Delete once the grace period has passed since the job became terminal.
		// Measuring from TerminalAt (not CreatedAt) ensures the full grace window
		// is always available for repeat polls, regardless of how long the job
		// spent in pending/ready before reaching its terminal state.
		isTerminal := job.Status == "consumed" || job.Status == "expired" || job.Status == "failed"
		shouldDelete := isTerminal && !job.TerminalAt.IsZero() && time.Since(job.TerminalAt) > upgradeJobGracePeriod
		job.mu.Unlock()

		if shouldDelete {
			delete(repman.LoginUpgradeStore.Jobs, id)
		}
	}
}

// startLoginUpgradeCleanup launches the background goroutine that sweeps
// stale upgrade jobs every 30 seconds. The goroutine exits when the store's
// done channel is closed via LoginUpgradeStore.Shutdown().
func (repman *ReplicationManager) startLoginUpgradeCleanup() {
	done := repman.LoginUpgradeStore.done
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				repman.cleanupExpiredLoginUpgradeJobs()
			case <-done:
				return
			}
		}
	}()
}

// ensureLoginUpgradeInfra initializes the in-memory login upgrade store and
// cleanup goroutine exactly once in a concurrency-safe way. The cleanup
// goroutine is only started when the store itself is created here; if
// LoginUpgradeStore was set externally (e.g., in tests) the caller owns the
// cleanup lifecycle.
//
// Returns true only when this call had to create the store lazily.
func (repman *ReplicationManager) ensureLoginUpgradeInfra() bool {
	created := false
	repman.LoginUpgradeInitOnce.Do(func() {
		if repman.LoginUpgradeStore == nil {
			repman.LoginUpgradeStore = newLoginUpgradeStore()
			repman.startLoginUpgradeCleanup()
			created = true
		}
	})
	return created
}

// classifySSOAuthError maps a GitLab auth error to one of the contract reason
// codes. Returns "" for success (err == nil), or one of:
//
//	"transient"               – network/5xx/429, may retry
//	"credential_mismatch"     – GitLab explicit "invalid credentials" signal
//	"not_registered"          – GitLab explicit "authenticatable_not_found" signal
//	"unknown_non_retryable"   – all other auth failures (invalid_grant, 401, etc.)
//
// Only exact GitLab error_description phrases are mapped to credential_mismatch or
// not_registered; everything else remains unknown_non_retryable to avoid false
// security alerts on ambiguous responses.
func classifySSOAuthError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())

	// Network / transport failures are transient (retryable).
	if strings.HasPrefix(msg, "error sending request") ||
		strings.HasPrefix(msg, "error creating request") {
		return "transient"
	}

	// HTTP 5xx or 429 responses that could not be parsed as an OAuth error body.
	// The githelper formats these as "received non-OK HTTP status N ...".
	if strings.Contains(msg, "non-ok http status 5") ||
		strings.Contains(msg, "non-ok http status 429") {
		return "transient"
	}

	// Explicit GitLab wrong-credential signal.
	// GitLab sets error_description="Invalid credentials" for a known user with
	// a wrong password on the resource-owner password grant.
	if strings.Contains(msg, "invalid credentials") {
		return "credential_mismatch"
	}

	// Explicit GitLab user-not-registered signal.
	// GitLab sets error_description to a string containing "authenticatable_not_found"
	// when the username does not exist in the provider.
	if strings.Contains(msg, "authenticatable_not_found") {
		return "not_registered"
	}

	// Everything else (invalid_grant without a discriminating description, generic
	// 401, parse errors, etc.) is treated as ambiguous and must not trigger a
	// credential-mismatch alert.
	return "unknown_non_retryable"
}

// resolveUserEmail returns a best-effort email address for the given username.
// If the username looks like an email it is returned directly; otherwise the
// cluster APIUsers are searched for a matching GitUser that carries an email.
func (repman *ReplicationManager) resolveUserEmail(username string) string {
	if strings.Contains(username, "@") {
		return username
	}
	for _, cl := range repman.Clusters {
		if u, ok := cl.APIUsers[username]; ok && strings.Contains(u.GitUser, "@") {
			return u.GitUser
		}
	}
	return ""
}

// emitSSOLoginSecurityAlert fires a Cloud18 Slack alert and sends an email to
// the user when local auth succeeded but SSO authentication was explicitly
// rejected (credential_mismatch). The alert is a security notification so
// operators can review the login event.
func (repman *ReplicationManager) emitSSOLoginSecurityAlert(username, email, remoteAddr string) {
	uri := repman.registeredInstanceURI()
	if uri == "" {
		uri = "unknown-instance"
	}

	// Security log (always).
	repman.logSecurityEvent("api_login_local_sso_mismatch", username, remoteAddr,
		fmt.Sprintf("Local user login detected with user: %s", username))

	// Cloud18 / Slack — concise operational message with structured fields.
	cloudMsg := fmt.Sprintf("Local user login detected with user: %s", username)
	for _, cl := range repman.Clusters {
		if cl.LogSlack != nil && cl.LogSlack.IsHookActive("cloud18") {
			cl.LogSlack.WithFields(map[string]interface{}{
				"event":        "api_login_local_sso_mismatch",
				"username":     username,
				"user_email":   email,
				"source_ip":    remoteAddr,
				"instance_uri": uri,
				"timestamp":    time.Now().UTC().Format(time.RFC3339),
			}).Warn(cloudMsg)
			break
		}
	}

	// Email — user-facing, with reassurance and escalation lines.
	if repman.Mailer != nil && email != "" {
		account := email
		if account == "" {
			account = username
		}
		body := fmt.Sprintf(
			"Security Notice\n\n"+
				"We detected a local login using your account %s on instance %s.\n\n"+
				"Timestamp: %s\n"+
				"Source IP: %s\n\n"+
				"SSO authentication could not be completed for the same login request.\n\n"+
				"Please ignore this message if this was you.\n"+
				"If this was not you, please contact support immediately.",
			account, uri,
			time.Now().UTC().Format(time.RFC1123),
			remoteAddr,
		)
		edata := mailer.Email{
			To:      email,
			Subject: "Security Notice: Local login detected",
			Message: body,
		}
		if err := repman.Mailer.SendEmailMessage(edata); err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn,
				"SSO security alert: could not send email to %s: %v", email, err)
		}
	}
}

// runAsyncSSOUpgrade performs the async SSO upgrade in a background goroutine.
// On success it marks the job ready with the new JWT. On credential_mismatch it
// emits a security alert so operators can review the login event. Other failure
// reasons are logged only.
func (repman *ReplicationManager) runAsyncSSOUpgrade(
	username, password, remoteAddr string,
	job *LoginUpgradeJob,
) {
	var (
		ssoToken   string
		failReason string
		lastErr    error
	)

	for attempt := 1; attempt <= upgradeMaxAttempts; attempt++ {
		job.mu.Lock()
		job.Attempts = attempt
		job.mu.Unlock()

		var err error
		ssoToken, err = githelper.GetGitLabTokenBasicAuth(username, password, false)
		if err == nil && ssoToken != "" {
			break // success
		}

		lastErr = err
		failReason = classifySSOAuthError(err)

		// Non-retryable → stop immediately.
		if failReason != "transient" {
			ssoToken = ""
			break
		}

		// Transient → backoff before next attempt.
		if attempt < upgradeMaxAttempts {
			time.Sleep(upgradeBackoffDelays[attempt-1])
		}
	}

	if ssoToken == "" {
		// Retries exhausted without a non-transient classification.
		if failReason == "transient" {
			failReason = "retry_exhausted"
		}
		if failReason == "" {
			failReason = "unknown_non_retryable"
		}
		job.mu.Lock()
		job.Status = "failed"
		job.Reason = failReason
		job.TerminalAt = time.Now()
		if lastErr != nil {
			job.Error = lastErr.Error()
		}
		job.mu.Unlock()

		if failReason == "credential_mismatch" {
			resolvedEmail := repman.resolveUserEmail(username)
			repman.emitSSOLoginSecurityAlert(username, resolvedEmail, remoteAddr)
		} else {
			repman.logSecurityEvent("api_login_sso_upgrade_"+failReason, username, remoteAddr,
				"SSO upgrade did not complete: "+failReason)
		}
		return
	}

	// SSO succeeded — build the upgrade JWT.
	email, _ := githelper.GetGitLabUserEmail(ssoToken, false)
	meetUser := email
	if meetUser == "" {
		meetUser = username
	}

	meetPassword := password
	meetUserID, err := meethelper.CreateMeetUserClient(meetUser, meetPassword,
		repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr,
			"SSO upgrade: error retrieving meet token: %v", err)
	}

	var userInfo interface{}
	if email != "" {
		userInfo = struct {
			Name       string
			Role       string
			Password   string
			Email      string `json:"email"`
			Profile    string `json:"profile"`
			MeetUserID string `json:"meet_user_id"`
		}{username, "Member", repman.Conf.GetEncryptedString(password), email, repman.Conf.OAuthProvider, meetUserID}
	} else {
		userInfo = struct {
			Name       string
			Role       string
			Password   string
			MeetUserID string `json:"meet_user_id"`
		}{username, "Member", repman.Conf.GetEncryptedString(password), meetUserID}
	}

	newJWT, err := repman.issueJWT(userInfo, ssoToken)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr,
			"SSO upgrade: error issuing JWT: %v", err)
		job.mu.Lock()
		job.Status = "failed"
		job.Reason = "claim_mismatch"
		job.TerminalAt = time.Now()
		job.Error = err.Error()
		job.mu.Unlock()
		return
	}

	job.mu.Lock()
	job.Status = "ready"
	job.NewJWT = newJWT
	job.ReadyAt = time.Now()
	job.mu.Unlock()

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlInfo,
		"SSO upgrade completed for user %s", username)
}
