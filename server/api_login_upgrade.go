// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// License: GNU General Public License, version 3.

package server

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
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
	upgradeJobTTL        = 60 * time.Second
	upgradeJobGracePeriod = 2 * upgradeJobTTL
	upgradeMaxAttempts   = 3
)

var upgradeBackoffDelays = []time.Duration{250 * time.Millisecond, 750 * time.Millisecond}

// LoginUpgradeJob tracks the state of an async SSO upgrade started during login.
type LoginUpgradeJob struct {
	mu        sync.Mutex
	Status    string    // pending, ready, failed, consumed, expired
	NewJWT    string    // set on success, cleared after one-time delivery
	Reason    string    // credential_mismatch, not_registered, unknown_non_retryable, claim_mismatch, retry_exhausted
	Error     string    // internal detail, never exposed to client
	Attempts  int
	CreatedAt time.Time
}

// LoginUpgradeStore is an in-memory map of upgrade jobs keyed by upgrade_id.
type LoginUpgradeStore struct {
	mu   sync.RWMutex
	Jobs map[string]*LoginUpgradeJob
}

func newLoginUpgradeStore() *LoginUpgradeStore {
	return &LoginUpgradeStore{
		Jobs: make(map[string]*LoginUpgradeJob),
	}
}

func newUpgradeID() string {
	b := make([]byte, 16)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(256))
		if err != nil {
			b[i] = 0
		} else {
			b[i] = byte(n.Int64())
		}
	}
	return fmt.Sprintf("%x", b)
}

func (s *LoginUpgradeStore) createJob() (string, *LoginUpgradeJob) {
	id := newUpgradeID()
	job := &LoginUpgradeJob{
		Status:    "pending",
		CreatedAt: time.Now(),
	}
	s.mu.Lock()
	s.Jobs[id] = job
	s.mu.Unlock()
	return id, job
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
	w.Header().Set("Content-Type", "application/json")

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

	// Mark TTL-exceeded pending/ready jobs as expired before responding.
	if (job.Status == "pending" || job.Status == "ready") && time.Since(job.CreatedAt) > upgradeJobTTL {
		job.Status = "expired"
	}

	switch job.Status {
	case "pending":
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"status": "pending"})

	case "ready":
		newJWT := job.NewJWT
		job.Status = "consumed"
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

		// Mark pending/ready jobs as expired if TTL exceeded.
		if (job.Status == "pending" || job.Status == "ready") && age > upgradeJobTTL {
			job.Status = "expired"
		}

		// Delete once the grace period has passed (gives repeat polls a
		// deterministic 410 response before the marker disappears).
		shouldDelete := age > upgradeJobGracePeriod &&
			(job.Status == "consumed" || job.Status == "expired" || job.Status == "failed")
		job.mu.Unlock()

		if shouldDelete {
			delete(repman.LoginUpgradeStore.Jobs, id)
		}
	}
}

// startLoginUpgradeCleanup launches the background goroutine that sweeps
// stale upgrade jobs every 30 seconds. Call once during server init.
func (repman *ReplicationManager) startLoginUpgradeCleanup() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			repman.cleanupExpiredLoginUpgradeJobs()
		}
	}()
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
// If the username already looks like an email it is returned directly.
// Otherwise the function searches cluster APIUsers for a matching GitUser that
// carries an email; failing that it returns "".
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

// runAsyncSSOUpgrade performs the async SSO upgrade in a background goroutine.
// On success it marks the job ready with the new JWT. On failure it records the
// reason and, for credential_mismatch, emits a Cloud18 + email alert.
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
		if lastErr != nil {
			job.Error = lastErr.Error()
		}
		job.mu.Unlock()

		if failReason == "credential_mismatch" {
			resolvedEmail := repman.resolveUserEmail(username)
			repman.emitSSOCredentialMismatchAlert(username, resolvedEmail, remoteAddr)
		} else {
			repman.logSecurityEvent("api_login_local_sso_"+failReason, username, remoteAddr,
				"SSO upgrade completed with non-critical result: "+failReason)
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
		job.Error = err.Error()
		job.mu.Unlock()
		return
	}

	job.mu.Lock()
	job.Status = "ready"
	job.NewJWT = newJWT
	job.mu.Unlock()

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlInfo,
		"SSO upgrade completed for user %s", username)
}

// emitSSOCredentialMismatchAlert fires a Cloud18 Slack alert and sends an email
// to the user when local auth succeeds but SSO fails with confirmed credential mismatch.
func (repman *ReplicationManager) emitSSOCredentialMismatchAlert(username, email, remoteAddr string) {
	fields := map[string]interface{}{
		"event":      "api_login_local_sso_mismatch",
		"username":   username,
		"user_email": email,
		"source_ip":  remoteAddr,
		"auth_path":  "local_success_sso_credential_mismatch",
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
	}
	msg := fmt.Sprintf("Security: local login succeeded but SSO credential mismatch for user %s from %s", username, remoteAddr)

	repman.logSecurityEvent("api_login_local_sso_mismatch", username, remoteAddr, msg)

	// Cloud18/Slack alert via first cluster with an active cloud18 hook.
	for _, cl := range repman.Clusters {
		if cl.LogSlack != nil && cl.LogSlack.IsHookActive("cloud18") {
			cl.LogSlack.WithFields(logrusFields(fields)).Warn(msg)
			break
		}
	}

	// Email notification to the affected user.
	if repman.Mailer != nil && email != "" {
		body := fmt.Sprintf(
			"Security Notice\n\nTimestamp: %s\nSource IP: %s\n\n"+
				"Your local replication-manager login succeeded but the SSO (GitLab) login failed with a credential mismatch.\n\n"+
				"If you did not change your SSO password recently, please verify your credentials and rotate them if this was unexpected.\n\n"+
				"Please contact your administrator if you need assistance.",
			time.Now().UTC().Format(time.RFC1123), remoteAddr,
		)
		edata := mailer.Email{
			To:      email,
			Subject: "Security Notice: Local login succeeded but SSO login failed",
			Message: body,
		}
		if err := repman.Mailer.SendEmailMessage(edata); err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlWarn,
				"SSO mismatch alert: could not send email to %s: %v", email, err)
		}
	}
}

// logrusFields converts a map[string]interface{} to logrus.Fields.
func logrusFields(m map[string]interface{}) map[string]interface{} {
	return m
}
