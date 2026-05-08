// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/githelper"
)

// ---------------------------------------------------------------------------
// Registration state — stored on ReplicationManager, polled via /api/register/status
// ---------------------------------------------------------------------------

const (
	RegStateIdle    = "idle"
	RegStatePending = "pending"  // GitLab account created, waiting for email confirmation
	RegStateOK      = "complete" // confirmed and Cloud18 connect flow succeeded
	RegStateTimeout = "timeout"  // user did not click the link in time
	RegStateError   = "error"    // terminal error (CRM, GitLab, etc.)
)

// RegistrationStatus is the current state of an ongoing or completed registration.
type RegistrationStatus struct {
	mu      sync.RWMutex
	State   string          `json:"state"`
	Message string          `json:"message,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

func (s *RegistrationStatus) set(state, message string, result json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.State = state
	s.Message = message
	s.Result = result
}

func (s *RegistrationStatus) snapshot() (state, message string, result json.RawMessage) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.State, s.Message, s.Result
}

// ---------------------------------------------------------------------------
// Request / payload types
// ---------------------------------------------------------------------------

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	URI      string `json:"uri"` // domain.subdomain.zone
}

type registerConfirmRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	URI      string `json:"uri"`
}

type signupRequest struct {
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	Telephone   string `json:"telephone,omitempty"`
	Phone       string `json:"phone,omitempty"`
	Tel         string `json:"tel,omitempty"`
	Company     string `json:"company,omitempty"`
	ReferrerURI string `json:"referrer_uri,omitempty"`
	URI         string `json:"uri,omitempty"`
}

type crmRegisterPayload struct {
	Email     string `json:"email"`
	Password  string `json:"password"`
	Domain    string `json:"domain"`
	Subdomain string `json:"subdomain"`
	Zone      string `json:"zone"`
}

type crmConfirmPayload struct {
	Email     string `json:"email"`
	Password  string `json:"password"`
	Domain    string `json:"domain"`
	Subdomain string `json:"subdomain"`
	Zone      string `json:"zone"`
}

type crmSignupPayload struct {
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	Telephone   string `json:"telephone,omitempty"`
	Company     string `json:"company,omitempty"`
	ReferrerURI string `json:"referrer_uri"`
}

// ---------------------------------------------------------------------------
// Polling constants
// ---------------------------------------------------------------------------

const confirmPollInterval = 10 * time.Second
const confirmPollTimeout = 5 * time.Minute

const defaultCrmBaseURL = "https://api.crm.ovh-fr-2.signal18.cloud18.io"

var basicEmailRegexp = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

var crmHTTPClient15s = &http.Client{Timeout: 15 * time.Second}
var crmHTTPClient30s = &http.Client{Timeout: 30 * time.Second}

const crmBootstrapMaxInFlight = 32

var crmBootstrapSem = make(chan struct{}, crmBootstrapMaxInFlight)

const (
	signupRatePerMinute      = 5
	signupRateBurst          = 3
	signupPromoRatePerMinute = 30
	signupPromoRateBurst     = 10
)

type ipTokenBucket struct {
	mu         sync.Mutex
	tokens     float64
	lastRefill time.Time
	lastSeen   time.Time
}

type ipRateLimiter struct {
	ratePerSecond float64
	burst         float64
	staleAfter    time.Duration
	cleanupEvery  uint64
	requestCount  atomic.Uint64
	ipBuckets     sync.Map // map[string]*ipTokenBucket
}

func newIPRateLimiter(ratePerMinute int, burst int, staleAfter time.Duration, cleanupEvery uint64) *ipRateLimiter {
	return &ipRateLimiter{
		ratePerSecond: float64(ratePerMinute) / 60.0,
		burst:         float64(burst),
		staleAfter:    staleAfter,
		cleanupEvery:  cleanupEvery,
	}
}

func (l *ipRateLimiter) allow(ip string, now time.Time) bool {
	v, _ := l.ipBuckets.LoadOrStore(ip, &ipTokenBucket{tokens: l.burst, lastRefill: now, lastSeen: now})
	b := v.(*ipTokenBucket)

	b.mu.Lock()
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.ratePerSecond
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.lastRefill = now
	}
	b.lastSeen = now
	allowed := b.tokens >= 1
	if allowed {
		b.tokens -= 1
	}
	b.mu.Unlock()

	if l.cleanupEvery > 0 && l.requestCount.Add(1)%l.cleanupEvery == 0 {
		l.cleanupStale(now)
	}

	return allowed
}

func (l *ipRateLimiter) cleanupStale(now time.Time) {
	l.ipBuckets.Range(func(key, value interface{}) bool {
		b, ok := value.(*ipTokenBucket)
		if !ok {
			l.ipBuckets.Delete(key)
			return true
		}
		b.mu.Lock()
		stale := now.Sub(b.lastSeen) > l.staleAfter
		b.mu.Unlock()
		if stale {
			l.ipBuckets.Delete(key)
		}
		return true
	})
}

func (l *ipRateLimiter) resetForTests() {
	l.ipBuckets = sync.Map{}
	l.requestCount.Store(0)
}

var signupLimiter = newIPRateLimiter(signupRatePerMinute, signupRateBurst, 15*time.Minute, 128)
var signupPromoLimiter = newIPRateLimiter(signupPromoRatePerMinute, signupPromoRateBurst, 15*time.Minute, 128)

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (repman *ReplicationManager) setSignupCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return
	}
	if repman.isAllowedSignupOrigin(origin) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		appendVaryHeader(w, "Origin")
	}
}

func appendVaryHeader(w http.ResponseWriter, value string) {
	existing := w.Header().Get("Vary")
	if existing == "" {
		w.Header().Set("Vary", value)
		return
	}
	for _, item := range strings.Split(existing, ",") {
		if strings.EqualFold(strings.TrimSpace(item), value) {
			return
		}
	}
	w.Header().Set("Vary", existing+", "+value)
}

func (repman *ReplicationManager) isAllowedSignupOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}

	if apiURL := strings.TrimSpace(repman.Conf.APIPublicURL); apiURL != "" {
		if pu, err := url.Parse(apiURL); err == nil && strings.EqualFold(host, pu.Hostname()) {
			return true
		}
	}

	if repman.PeerManager != nil && repman.handleOriginValidator(origin) {
		return true
	}

	instanceURI := strings.ToLower(strings.TrimSpace(repman.registeredInstanceURI()))
	return instanceURI != "" && host == instanceURI
}

func signupClientIP(r *http.Request) string {
	remoteIP := parseRemoteIP(r.RemoteAddr)
	if remoteIP != nil && isTrustedProxyIP(remoteIP) {
		xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
		if xff != "" {
			parts := strings.Split(xff, ",")
			if len(parts) > 0 {
				candidate := strings.TrimSpace(parts[0])
				if candidateIP := net.ParseIP(candidate); candidateIP != nil {
					return candidateIP.String()
				}
			}
		}
	}

	if remoteIP != nil {
		return remoteIP.String()
	}
	if strings.TrimSpace(r.RemoteAddr) != "" {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return "unknown"
}

func parseRemoteIP(remoteAddr string) net.IP {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if remoteAddr == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return net.ParseIP(host)
	}
	return net.ParseIP(remoteAddr)
}

func isTrustedProxyIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	if ip.IsPrivate() {
		return true
	}
	return ip.IsLinkLocalUnicast()
}

// crmBase returns the configured CRM API base URL, falling back to the default.
func (repman *ReplicationManager) crmBase() string {
	if u := strings.TrimRight(repman.Conf.Cloud18CrmApiUrl, "/"); u != "" {
		return u
	}
	return defaultCrmBaseURL
}

// registeredInstanceURI returns this instance URI when fully configured.
// Format: domain.subdomain.zone. Returns empty string when incomplete.
func (repman *ReplicationManager) registeredInstanceURI() string {
	if repman.Conf.Cloud18Domain == "" || repman.Conf.Cloud18SubDomain == "" || repman.Conf.Cloud18SubDomainZone == "" {
		return ""
	}
	return repman.Conf.Cloud18Domain + "." + repman.Conf.Cloud18SubDomain + "." + repman.Conf.Cloud18SubDomainZone
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// crmCallConfirm calls the CRM /api/register/confirm endpoint once.
func crmCallConfirm(crmBase, gitlabToken string, payload crmConfirmPayload) (int, []byte, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, crmBase+"/api/register/confirm", bytes.NewReader(b))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+gitlabToken)

	resp, err := crmHTTPClient30s.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// applyCloudConnect configures Cloud18 settings and runs InitGitConfig.
// It updates the registration status on the ReplicationManager.
func (repman *ReplicationManager) applyCloudConnect(
	domain, subdomain, zone, email, password string,
	respBody []byte,
) {
	repman.Conf.Cloud18 = false
	repman.Conf.Cloud18Domain = domain
	repman.Conf.Cloud18SubDomain = subdomain
	repman.Conf.Cloud18SubDomainZone = zone
	repman.Conf.Cloud18GitUser = email
	repman.Conf.Cloud18GitPassword = password

	var gitSecret config.Secret
	gitSecret.Value = password
	gitSecret.OldValue = repman.Conf.GetDecryptedValue("cloud18-gitlab-password")
	if repman.Conf.Secrets == nil {
		repman.Conf.Secrets = make(map[string]config.Secret)
	}
	repman.Conf.Secrets["cloud18-gitlab-password"] = gitSecret
	if repman.Conf.ImmuableFlagMap == nil {
		repman.Conf.ImmuableFlagMap = make(map[string]interface{})
	}
	repman.Conf.Cloud18 = true

	if err := repman.InitGitConfig(repman.Conf); err != nil {
		repman.Conf.Cloud18 = false
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"register: InitGitConfig failed: %s", err)

		// Still mark complete — the CRM part succeeded; connect_error is advisory
		var crmBody map[string]interface{}
		if json.Unmarshal(respBody, &crmBody) == nil {
			crmBody["connect_error"] = err.Error()
			if out, e := json.Marshal(crmBody); e == nil {
				repman.RegStatus.set(RegStateOK, "Registration complete (connect error: "+err.Error()+")", out)
				return
			}
		}
	}
	repman.RegStatus.set(RegStateOK, "Registration complete", json.RawMessage(respBody))
}

// pollConfirmLoop runs in a goroutine: polls CRM every confirmPollInterval until
// the user confirms their email or the timeout expires.
func (repman *ReplicationManager) pollConfirmLoop(crmBase string, payload crmConfirmPayload,
	domain, subdomain, zone, email, password string) {

	deadline := time.Now().Add(confirmPollTimeout)
	ticker := time.NewTicker(confirmPollInterval)
	defer ticker.Stop()

	gitlabToken := ""

	for time.Now().Before(deadline) {
		<-ticker.C

		if gitlabToken == "" {
			tok, err := githelper.GetGitLabTokenBasicAuth(email, password, repman.Conf.Verbose)
			if err != nil {
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
					"register: token exchange failed for %s: %s (retrying)", email, err)
				continue
			}
			gitlabToken = tok
		}

		status, respBody, err := crmCallConfirm(crmBase, gitlabToken, payload)
		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"register: confirm poll error: %s (retrying)", err)
			continue
		}

		if status == http.StatusCreated {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"register: email confirmed for %s — running Cloud18 connect flow", email)
			repman.applyCloudConnect(domain, subdomain, zone, email, password, respBody)
			return
		}

		if status == http.StatusBadRequest {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"register: waiting for %s to confirm email…", email)
			continue
		}

		if status == http.StatusAccepted {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"register: confirmation pending for %s (HTTP %d)", email, status)
			continue
		}

		if status == http.StatusUnauthorized {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"register: confirm unauthorized for %s, refreshing token", email)
			gitlabToken = ""
			continue
		}

		// Terminal error
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"register: confirm returned unexpected status %d: %s", status, respBody)
		repman.RegStatus.set(RegStateError,
			fmt.Sprintf("CRM confirm error (HTTP %d): %s", status, respBody), nil)
		return
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
		"register: email confirmation timeout for %s", email)
	repman.RegStatus.set(RegStateTimeout,
		fmt.Sprintf("Email confirmation timeout — please ask %s to click the confirmation link and try again", email),
		nil)
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// handlerRegister — POST /api/register  (admin JWT required)
//
// Creates the GitLab account via CRM, returns 202 immediately, then launches
// a background goroutine that polls CRM confirm every 10 s for up to 5 min.
// The client polls GET /api/register/status to track progress.
func (repman *ReplicationManager) handlerRegister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	claims, err := repman.GetJWTClaims(r)
	if err != nil || claims["User"] != "admin" {
		http.Error(w, `{"error":"administrator access required"}`, http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req registerRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}

	if req.Email == "" || req.URI == "" || req.Password == "" {
		http.Error(w, `{"error":"email, uri and password are required"}`, http.StatusBadRequest)
		return
	}

	parts := strings.SplitN(req.URI, ".", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		http.Error(w, `{"error":"uri must be in domain.subdomain.zone format (e.g. mycompany.ovh.fr-1)"}`,
			http.StatusBadRequest)
		return
	}
	domain := strings.TrimSpace(strings.ToLower(parts[0]))
	subdomain := strings.TrimSpace(strings.ToLower(parts[1]))
	zone := strings.TrimSpace(strings.ToLower(parts[2]))
	email := strings.TrimSpace(strings.ToLower(req.Email))

	crmBase := repman.crmBase()

	// Reject if a registration is already in progress
	if state, _, _ := repman.RegStatus.snapshot(); state == RegStatePending {
		http.Error(w, `{"error":"a registration is already in progress — poll /api/register/status"}`,
			http.StatusConflict)
		return
	}

	// Step 1 — create GitLab account
	step1Bytes, _ := json.Marshal(crmRegisterPayload{
		Email: email, Password: req.Password,
		Domain: domain, Subdomain: subdomain, Zone: zone,
	})
	step1Req, err := http.NewRequest(http.MethodPost, crmBase+"/api/register", bytes.NewReader(step1Bytes))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to build CRM request: %s"}`, err), http.StatusInternalServerError)
		return
	}
	step1Req.Header.Set("Content-Type", "application/json")
	step1Req.Header.Set("Accept", "application/json")

	step1Resp, err := crmHTTPClient30s.Do(step1Req)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"CRM API unreachable: %s"}`, err), http.StatusBadGateway)
		return
	}
	step1Body, _ := io.ReadAll(step1Resp.Body)
	step1Resp.Body.Close()

	if step1Resp.StatusCode != http.StatusAccepted && step1Resp.StatusCode != http.StatusOK {
		w.WriteHeader(step1Resp.StatusCode)
		w.Write(step1Body)
		return
	}

	// Mark pending and launch background poller
	repman.RegStatus.set(RegStatePending,
		fmt.Sprintf("GitLab account created — waiting for %s to confirm email (timeout %s)", email, confirmPollTimeout),
		nil)

	go repman.pollConfirmLoop(crmBase,
		crmConfirmPayload{Email: email, Password: req.Password, Domain: domain, Subdomain: subdomain, Zone: zone},
		domain, subdomain, zone, email, req.Password)

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"register: confirmation email sent to %s — background poller started", email)

	w.WriteHeader(http.StatusAccepted)
	w.Write(step1Body)
}

// handlerSignup — POST /api/signup (public, no JWT required)
//
// Proxies signup data to CRM and always derives referrer_uri from
// the registered local Cloud18 instance URI.
func (repman *ReplicationManager) handlerSignup(w http.ResponseWriter, r *http.Request) {
	repman.setSignupCORSHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"signup: method not allowed: %s", r.Method)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	clientIP := signupClientIP(r)
	if !signupLimiter.allow(clientIP, time.Now()) {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"signup: throttled request from ip=%s", clientIP)
		writeJSONError(w, http.StatusTooManyRequests, "too many requests")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"signup: failed to read request body: %s", err)
		writeJSONError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	defer r.Body.Close()

	var req signupRequest
	if err := json.Unmarshal(body, &req); err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"signup: invalid JSON body: %s", err)
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	req.FirstName = strings.TrimSpace(req.FirstName)
	req.LastName = strings.TrimSpace(req.LastName)
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Telephone = strings.TrimSpace(req.Telephone)
	req.Phone = strings.TrimSpace(req.Phone)
	req.Tel = strings.TrimSpace(req.Tel)
	req.Company = strings.TrimSpace(req.Company)
	req.ReferrerURI = strings.TrimSpace(req.ReferrerURI)
	req.URI = strings.TrimSpace(req.URI)

	if req.FirstName == "" || req.LastName == "" || req.Username == "" || req.Email == "" || req.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "first_name, last_name, username, email and password are required")
		return
	}

	if !basicEmailRegexp.MatchString(req.Email) {
		writeJSONError(w, http.StatusBadRequest, "invalid email format")
		return
	}

	// Normalize aliases
	telephone := req.Telephone
	if telephone == "" {
		telephone = req.Phone
	}
	if telephone == "" {
		telephone = req.Tel
	}

	referrerURI := req.ReferrerURI
	if referrerURI == "" {
		referrerURI = req.URI
	}
	if referrerURI == "" {
		referrerURI = repman.registeredInstanceURI()
	}

	crmPayload := crmSignupPayload{
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		Username:    req.Username,
		Email:       req.Email,
		Password:    req.Password,
		Telephone:   telephone,
		Company:     req.Company,
		ReferrerURI: referrerURI,
	}

	crmBody, err := json.Marshal(crmPayload)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"signup: failed to marshal CRM payload: %s", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to prepare CRM request")
		return
	}

	crmReq, err := http.NewRequest(http.MethodPost, repman.crmBase()+"/api/signup", bytes.NewReader(crmBody))
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"signup: failed to build CRM request: %s", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to build CRM request")
		return
	}
	crmReq.Header.Set("Content-Type", "application/json")
	crmReq.Header.Set("Accept", "application/json")

	crmResp, err := crmHTTPClient30s.Do(crmReq)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"signup: CRM unreachable: %s", err)
		writeJSONError(w, http.StatusBadGateway, "CRM API unreachable")
		return
	}
	defer crmResp.Body.Close()

	respBody, err := io.ReadAll(crmResp.Body)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"signup: failed to read CRM response: %s", err)
		writeJSONError(w, http.StatusBadGateway, "failed to read CRM response")
		return
	}

	w.WriteHeader(crmResp.StatusCode)
	w.Write(respBody)
}

// handlerSignupPromo — GET /api/signup/promo (public, no JWT required)
//
// Returns the best active promotional offer for signup onboarding
// and the required/optional field definitions expected by the CRM.
func (repman *ReplicationManager) handlerSignupPromo(w http.ResponseWriter, r *http.Request) {
	repman.setSignupCORSHeaders(w, r)
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodGet {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"signup/promo: method not allowed: %s", r.Method)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	clientIP := signupClientIP(r)
	if !signupPromoLimiter.allow(clientIP, time.Now()) {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"signup/promo: throttled request from ip=%s", clientIP)
		writeJSONError(w, http.StatusTooManyRequests, "too many requests")
		return
	}

	status, body, err := crmGetSignupPromo(repman.crmBase())
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"signup/promo: CRM unreachable: %s", err)
		writeJSONError(w, http.StatusBadGateway, "CRM API unreachable")
		return
	}

	w.WriteHeader(status)
	w.Write(body)
}

// handlerRegisterStatus — GET /api/register/status  (admin JWT required)
//
// Returns the current registration state so the GUI or CLI can poll until
// state is "complete", "timeout", or "error".
func (repman *ReplicationManager) handlerRegisterStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	claims, err := repman.GetJWTClaims(r)
	if err != nil || claims["User"] != "admin" {
		http.Error(w, `{"error":"administrator access required"}`, http.StatusForbidden)
		return
	}

	state, message, result := repman.RegStatus.snapshot()
	resp := map[string]interface{}{
		"state":   state,
		"message": message,
	}
	if result != nil {
		resp["result"] = json.RawMessage(result)
	}

	out, _ := json.Marshal(resp)
	// Return 200 always — the state field tells the client what happened
	w.WriteHeader(http.StatusOK)
	w.Write(out)
}

// handlerRegisterConfirm — POST /api/register/confirm  (admin JWT required)
//
// Manual single-shot confirm: verifies email is confirmed via CRM and runs
// the Cloud18 connect flow.  Useful if the background poller is not running
// or the client wants explicit control.
func (repman *ReplicationManager) handlerRegisterConfirm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	claims, err := repman.GetJWTClaims(r)
	if err != nil || claims["User"] != "admin" {
		http.Error(w, `{"error":"administrator access required"}`, http.StatusForbidden)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req registerConfirmRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}

	if req.Email == "" || req.URI == "" || req.Password == "" {
		http.Error(w, `{"error":"email, uri and password are required"}`, http.StatusBadRequest)
		return
	}

	parts := strings.SplitN(req.URI, ".", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		http.Error(w, `{"error":"uri must be in domain.subdomain.zone format"}`, http.StatusBadRequest)
		return
	}
	domain := strings.TrimSpace(strings.ToLower(parts[0]))
	subdomain := strings.TrimSpace(strings.ToLower(parts[1]))
	zone := strings.TrimSpace(strings.ToLower(parts[2]))
	email := strings.TrimSpace(strings.ToLower(req.Email))

	crmBase := repman.crmBase()

	gitlabToken, err := githelper.GetGitLabTokenBasicAuth(email, req.Password, repman.Conf.Verbose)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"could not obtain GitLab token: %s"}`, err), http.StatusBadGateway)
		return
	}

	status, respBody, err := crmCallConfirm(crmBase, gitlabToken, crmConfirmPayload{
		Email: email, Password: req.Password,
		Domain: domain, Subdomain: subdomain, Zone: zone,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"CRM API unreachable: %s"}`, err), http.StatusBadGateway)
		return
	}

	if status != http.StatusCreated {
		w.WriteHeader(status)
		w.Write(respBody)
		return
	}

	repman.applyCloudConnect(domain, subdomain, zone, email, req.Password, respBody)

	_, _, result := repman.RegStatus.snapshot()
	if result != nil {
		w.WriteHeader(http.StatusCreated)
		w.Write(result)
		return
	}
	w.WriteHeader(http.StatusCreated)
	w.Write(respBody)
}

// ---------------------------------------------------------------------------
// Subscription plans
// ---------------------------------------------------------------------------

type changeSubPayload struct {
	URI  string `json:"uri"`
	Plan string `json:"plan"`
}

func isAllowedOfflineInstancePlan(plan string) bool {
	switch plan {
	case "free", "support", "support-services", "partner", "developer":
		return true
	default:
		return false
	}
}

func (repman *ReplicationManager) persistInstanceSubscriptionPlan(plan, uri string) {
	repman.Conf.Cloud18SubscriptionPlan = plan
	if repman.ConfigManager != nil {
		repman.ConfigManager.SaveConfig(repman, false)
	}
	for _, cl := range repman.Clusters {
		cl.Conf.Cloud18SubscriptionPlan = plan
		if cl.ConfigManager != nil {
			cl.ConfigManager.SaveConfig(cl, false)
		}
	}
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"subscription: plan changed to %s for URI %s (persisted to config)", plan, uri)
}

func writeOfflineSubscriptionQueued(w http.ResponseWriter, plan, uri, detail string) {
	out, _ := json.Marshal(map[string]interface{}{
		"status":                    "queued_for_git_processing",
		"mode":                      "offline_fallback",
		"plan":                      plan,
		"uri":                       uri,
		"message":                   "CRM unreachable — subscription plan saved locally and queued for git processing",
		"connectivity_error_detail": detail,
	})
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write(out)
}

// crmGetPlans fetches the available subscription plans from the CRM (no auth required).
func crmGetPlans(crmBase string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, crmBase+"/api/subscriptions/plans/instance", nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := crmHTTPClient15s.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// crmGetSignupPromo fetches the best active promotional offer for signup onboarding
// from the CRM (no auth required).
func crmGetSignupPromo(crmBase string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, crmBase+"/api/signup/promo", nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := crmHTTPClient15s.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// crmGetSubscription fetches the current subscription from CRM using a GitLab PAT.
// uri is the instance URI in domain.subdomain.zone format.
func crmGetSubscription(crmBase, gitlabToken, uri string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, crmBase+"/api/subscription?uri="+uri, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+gitlabToken)
	req.Header.Set("Accept", "application/json")
	resp, err := crmHTTPClient15s.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func crmGetPersonalCredits(crmBase, gitlabToken string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, crmBase+"/api/credits/personal", nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+gitlabToken)
	req.Header.Set("Accept", "application/json")
	resp, err := crmHTTPClient15s.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func crmBootstrapSession(crmBase, gitlabToken string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodPost, crmBase+"/api/session/bootstrap", nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+gitlabToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := crmHTTPClient30s.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func crmGetDBaaSSubscription(crmBase, gitlabToken string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, crmBase+"/api/users/dbaas/subscription", nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+gitlabToken)
	req.Header.Set("Accept", "application/json")
	resp, err := crmHTTPClient15s.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func crmGetDBaaSPlans(crmBase string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, crmBase+"/api/subscriptions/plans/dbaas", nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := crmHTTPClient15s.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func crmChangeDBaaSSubscription(crmBase, gitlabToken, subscription string) (int, []byte, error) {
	payload, err := json.Marshal(map[string]string{"subscription": subscription})
	if err != nil {
		return 0, nil, err
	}

	req, err := http.NewRequest(http.MethodPut, crmBase+"/api/users/dbaas/subscription", bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+gitlabToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := crmHTTPClient15s.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func crmGetUserTransactions(crmBase, gitlabToken string, limit, offset int, direction string) (int, []byte, error) {
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		query.Set("offset", strconv.Itoa(offset))
	}
	if direction != "" {
		query.Set("direction", direction)
	}

	endpoint := crmBase + "/api/users/transactions"
	if encoded := query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+gitlabToken)
	req.Header.Set("Accept", "application/json")
	resp, err := crmHTTPClient15s.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func (repman *ReplicationManager) fetchWithSessionBootstrap(gitlabToken string, fetch func() (int, []byte, error)) (int, []byte, error) {
	status, body, err := fetch()
	if err != nil {
		return 0, nil, err
	}
	if status != http.StatusPreconditionRequired {
		return status, body, nil
	}

	bootStatus, bootBody, err := crmBootstrapSession(repman.crmBase(), gitlabToken)
	if err != nil {
		return 0, nil, err
	}
	if bootStatus < http.StatusOK || bootStatus >= http.StatusMultipleChoices {
		return bootStatus, bootBody, nil
	}

	return fetch()
}

// handlerBillingPersonal — GET /api/billing/personal (JWT required)
func (repman *ReplicationManager) handlerBillingPersonal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	gitlabToken, err := repman.GetJWTGitLabToken(r)
	if err != nil {
		writeJSONError(w, http.StatusPreconditionFailed, "gitlab sso token required")
		return
	}

	status, body, err := repman.fetchWithSessionBootstrap(gitlabToken, func() (int, []byte, error) {
		return crmGetPersonalCredits(repman.crmBase(), gitlabToken)
	})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "CRM API unreachable")
		return
	}

	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// handlerBillingSubscription — GET /api/billing/subscription (JWT required)
func (repman *ReplicationManager) handlerBillingSubscription(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	gitlabToken, err := repman.GetJWTGitLabToken(r)
	if err != nil {
		writeJSONError(w, http.StatusPreconditionFailed, "gitlab sso token required")
		return
	}

	status, body, err := crmGetDBaaSSubscription(repman.crmBase(), gitlabToken)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "CRM API unreachable")
		return
	}

	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// handlerBillingSubscriptionPlans — GET /api/billing/subscription/plans (JWT required)
func (repman *ReplicationManager) handlerBillingSubscriptionPlans(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	status, body, err := crmGetDBaaSPlans(repman.crmBase())
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "CRM API unreachable")
		return
	}

	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// handlerBillingSubscriptionChange — POST /api/billing/subscription/change (JWT required)
func (repman *ReplicationManager) handlerBillingSubscriptionChange(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	gitlabToken, err := repman.GetJWTGitLabToken(r)
	if err != nil {
		writeJSONError(w, http.StatusPreconditionFailed, "gitlab sso token required")
		return
	}

	var reqBody struct {
		Subscription string `json:"subscription"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		writeJSONError(w, http.StatusBadRequest, "subscription is required")
		return
	}

	subscription := strings.ToLower(strings.TrimSpace(reqBody.Subscription))
	if subscription == "" {
		writeJSONError(w, http.StatusBadRequest, "subscription is required")
		return
	}

	status, body, err := repman.fetchWithSessionBootstrap(gitlabToken, func() (int, []byte, error) {
		return crmChangeDBaaSSubscription(repman.crmBase(), gitlabToken, subscription)
	})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "CRM API unreachable")
		return
	}

	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// handlerBillingTransactions — GET /api/billing/transactions (JWT required)
func (repman *ReplicationManager) handlerBillingTransactions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	gitlabToken, err := repman.GetJWTGitLabToken(r)
	if err != nil {
		writeJSONError(w, http.StatusPreconditionFailed, "gitlab sso token required")
		return
	}

	query := r.URL.Query()
	limit := 25
	if rawLimit := strings.TrimSpace(query.Get("limit")); rawLimit != "" {
		parsed, parseErr := strconv.Atoi(rawLimit)
		if parseErr != nil || parsed < 1 || parsed > 100 {
			writeJSONError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}

	offset := 0
	if rawOffset := strings.TrimSpace(query.Get("offset")); rawOffset != "" {
		parsed, parseErr := strconv.Atoi(rawOffset)
		if parseErr != nil || parsed < 0 {
			writeJSONError(w, http.StatusBadRequest, "invalid offset")
			return
		}
		offset = parsed
	}

	direction := strings.ToLower(strings.TrimSpace(query.Get("direction")))
	if direction == "" {
		direction = "desc"
	}
	if direction != "asc" && direction != "desc" {
		writeJSONError(w, http.StatusBadRequest, "invalid direction")
		return
	}

	status, body, err := repman.fetchWithSessionBootstrap(gitlabToken, func() (int, []byte, error) {
		return crmGetUserTransactions(repman.crmBase(), gitlabToken, limit, offset, direction)
	})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "CRM API unreachable")
		return
	}

	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// ensureCRMSessionBootstrapped runs a best-effort CRM bootstrap flow for SSO logins.
func (repman *ReplicationManager) ensureCRMSessionBootstrapped(gitlabToken string) {
	if strings.TrimSpace(gitlabToken) == "" {
		return
	}

	status, _, err := crmGetPersonalCredits(repman.crmBase(), gitlabToken)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"crm bootstrap: credits lookup failed: %s", err)
		return
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"crm bootstrap: credits lookup returned status %d", status)

	if status != http.StatusPreconditionRequired {
		return
	}

	bootStatus, _, err := crmBootstrapSession(repman.crmBase(), gitlabToken)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"crm bootstrap: session bootstrap call failed: %s", err)
		return
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"crm bootstrap: session bootstrap returned status %d", bootStatus)

	if bootStatus < http.StatusOK || bootStatus >= http.StatusMultipleChoices {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"crm bootstrap: session bootstrap unsuccessful (status %d), skipping credits retry", bootStatus)
		return
	}

	retryStatus, _, err := crmGetPersonalCredits(repman.crmBase(), gitlabToken)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"crm bootstrap: credits retry failed: %s", err)
		return
	}

	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"crm bootstrap: credits retry returned status %d", retryStatus)
}

// ensureCRMSessionBootstrappedAsync starts CRM bootstrap flow in a background
// goroutine so authentication responses are not blocked by CRM latencies.
func (repman *ReplicationManager) ensureCRMSessionBootstrappedAsync(gitlabToken string) {
	if repman == nil || strings.TrimSpace(gitlabToken) == "" {
		return
	}

	select {
	case crmBootstrapSem <- struct{}{}:
		// proceed
	default:
		if repman.Conf != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"crm bootstrap: skipped async bootstrap, too many in-flight requests")
		}
		return
	}

	go func() {
		defer func() {
			<-crmBootstrapSem
			if r := recover(); r != nil {
				if repman.Conf != nil {
					repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
						"crm bootstrap: recovered panic in async flow: %v", r)
				}
			}
		}()

		repman.ensureCRMSessionBootstrapped(gitlabToken)
	}()
}

// crmChangeSubscription changes the plan for a specific URI on CRM using a GitLab PAT.
func crmChangeSubscription(crmBase, gitlabToken, uri, plan string) (int, []byte, error) {
	b, _ := json.Marshal(changeSubPayload{URI: uri, Plan: plan})
	req, err := http.NewRequest(http.MethodPost, crmBase+"/api/subscription", bytes.NewReader(b))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+gitlabToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := crmHTTPClient15s.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// crmUnregister drops the GitLab projects for the given URI via the CRM.
func crmUnregister(crmBase, gitlabToken, uri string) (int, []byte, error) {
	type unregPayload struct {
		URI string `json:"uri"`
	}
	b, _ := json.Marshal(unregPayload{URI: uri})
	req, err := http.NewRequest(http.MethodPost, crmBase+"/api/unregister", bytes.NewReader(b))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+gitlabToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := crmHTTPClient30s.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// gitlabTokenFromConfig obtains a GitLab PAT from the stored Cloud18 credentials,
// using the same exchange the rest of the codebase performs for git operations.
func (repman *ReplicationManager) gitlabTokenFromConfig() (string, error) {
	email := repman.Conf.Cloud18GitUser
	password := repman.Conf.GetDecryptedValue("cloud18-gitlab-password")
	if email == "" || password == "" {
		return "", fmt.Errorf("cloud18 credentials not configured")
	}
	tok, err := githelper.GetGitLabTokenBasicAuth(email, password, repman.Conf.Verbose)
	if err != nil {
		return "", fmt.Errorf("could not obtain GitLab token: %w", err)
	}
	return tok, nil
}

// handlerGetSubscriptionPlans — GET /api/register/subscription/plans  (admin JWT required)
//
// Proxies the CRM plan catalog so the frontend always reflects what the CRM offers.
func (repman *ReplicationManager) handlerGetSubscriptionPlans(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	claims, err := repman.GetJWTClaims(r)
	if err != nil || claims["User"] != "admin" {
		http.Error(w, `{"error":"administrator access required"}`, http.StatusForbidden)
		return
	}

	status, body, err := crmGetPlans(repman.crmBase())
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"CRM unreachable: %s"}`, err), http.StatusBadGateway)
		return
	}
	w.WriteHeader(status)
	w.Write(body)
}

// handlerGetSubscription — GET /api/register/subscription  (admin JWT required)
//
// Uses the stored GitLab PAT to query the CRM for the current subscription
// plan and confirmed email address.
func (repman *ReplicationManager) handlerGetSubscription(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	claims, err := repman.GetJWTClaims(r)
	if err != nil || claims["User"] != "admin" {
		http.Error(w, `{"error":"administrator access required"}`, http.StatusForbidden)
		return
	}

	if !repman.Conf.Cloud18 {
		http.Error(w, `{"error":"not registered to Cloud18"}`, http.StatusPreconditionFailed)
		return
	}

	tok, err := repman.gitlabTokenFromConfig()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadGateway)
		return
	}

	uri := repman.registeredInstanceURI()

	status, body, err := crmGetSubscription(repman.crmBase(), tok, uri)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"CRM unreachable: %s"}`, err), http.StatusBadGateway)
		return
	}
	w.WriteHeader(status)
	w.Write(body)
}

// handlerChangeSubscription — POST /api/register/subscription  (admin JWT required)
//
// Validates the requested plan, calls the CRM to change the subscription, and
// persists the new plan in local configuration on success.
// When CRM (or token exchange) is unreachable, strict offline fallback accepts
// only known plans and queues local persistence for later git-based processing.
func (repman *ReplicationManager) handlerChangeSubscription(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	claims, err := repman.GetJWTClaims(r)
	if err != nil || claims["User"] != "admin" {
		http.Error(w, `{"error":"administrator access required"}`, http.StatusForbidden)
		return
	}

	if !repman.Conf.Cloud18 {
		http.Error(w, `{"error":"not registered to Cloud18"}`, http.StatusPreconditionFailed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req changeSubPayload
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	req.Plan = strings.TrimSpace(strings.ToLower(req.Plan))

	if req.Plan == "" {
		http.Error(w, `{"error":"plan is required"}`,
			http.StatusBadRequest)
		return
	}

	uri := repman.registeredInstanceURI()
	tok, err := repman.gitlabTokenFromConfig()
	if err != nil {
		if isAllowedOfflineInstancePlan(req.Plan) {
			repman.persistInstanceSubscriptionPlan(req.Plan, uri)
			writeOfflineSubscriptionQueued(w, req.Plan, uri, err.Error())
			return
		}
		writeJSONError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid plan %q for offline mode — must be one of free, support, support-services, partner, developer", req.Plan))
		return
	}

	status, respBody, err := crmChangeSubscription(repman.crmBase(), tok, uri, req.Plan)
	if err != nil {
		if isAllowedOfflineInstancePlan(req.Plan) {
			repman.persistInstanceSubscriptionPlan(req.Plan, uri)
			writeOfflineSubscriptionQueued(w, req.Plan, uri, err.Error())
			return
		}
		writeJSONError(w, http.StatusBadRequest,
			fmt.Sprintf("CRM unreachable and plan %q is not allowed in offline mode — must be one of free, support, support-services, partner, developer", req.Plan))
		return
	}

	if status == http.StatusOK || status == http.StatusCreated {
		repman.persistInstanceSubscriptionPlan(req.Plan, uri)
	}

	w.WriteHeader(status)
	w.Write(respBody)
}

// handlerUnregister — POST /api/register/unregister  (admin JWT required)
//
// Drops the GitLab projects for this instance's URI via the CRM, then clears
// the Cloud18 flag so the URI fields become editable again.  The user can
// then change the URI and re-register.
func (repman *ReplicationManager) handlerUnregister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	claims, err := repman.GetJWTClaims(r)
	if err != nil || claims["User"] != "admin" {
		http.Error(w, `{"error":"administrator access required"}`, http.StatusForbidden)
		return
	}

	if !repman.Conf.Cloud18 {
		http.Error(w, `{"error":"not registered to Cloud18"}`, http.StatusPreconditionFailed)
		return
	}

	tok, err := repman.gitlabTokenFromConfig()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadGateway)
		return
	}

	uri := repman.registeredInstanceURI()

	status, respBody, err := crmUnregister(repman.crmBase(), tok, uri)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"CRM unreachable: %s"}`, err), http.StatusBadGateway)
		return
	}

	if status != http.StatusOK {
		w.WriteHeader(status)
		w.Write(respBody)
		return
	}

	// CRM confirmed — clear Cloud18 state so URI fields become editable
	repman.Conf.Cloud18 = false
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"unregister: Cloud18 disconnected for URI %s", uri)

	w.WriteHeader(http.StatusOK)
	w.Write(respBody)
}
