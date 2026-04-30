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
	"net/http"
	"regexp"
	"strings"
	"sync"
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
	ReferrerURI string `json:"referrer_uri,omitempty"`
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
	ReferrerURI string `json:"referrer_uri"`
}

// ---------------------------------------------------------------------------
// Polling constants
// ---------------------------------------------------------------------------

const confirmPollInterval = 10 * time.Second
const confirmPollTimeout = 5 * time.Minute

const defaultCrmBaseURL = "https://api.crm.ovh-fr-2.signal18.cloud18.io"

var basicEmailRegexp = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

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
func crmCallConfirm(crmBase string, payload crmConfirmPayload) (int, []byte, error) {
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

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
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

	for time.Now().Before(deadline) {
		<-ticker.C

		status, respBody, err := crmCallConfirm(crmBase, payload)
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

	step1Client := &http.Client{Timeout: 30 * time.Second}
	step1Resp, err := step1Client.Do(step1Req)
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
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodPost {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"signup: method not allowed: %s", r.Method)
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"signup: failed to read request body: %s", err)
		http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req signupRequest
	if err := json.Unmarshal(body, &req); err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"signup: invalid JSON body: %s", err)
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}

	req.FirstName = strings.TrimSpace(req.FirstName)
	req.LastName = strings.TrimSpace(req.LastName)
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	if req.FirstName == "" || req.LastName == "" || req.Username == "" || req.Email == "" || req.Password == "" {
		http.Error(w, `{"error":"first_name, last_name, username, email and password are required"}`, http.StatusBadRequest)
		return
	}

	if !basicEmailRegexp.MatchString(req.Email) {
		http.Error(w, `{"error":"invalid email format"}`, http.StatusBadRequest)
		return
	}

	crmPayload := crmSignupPayload{
		FirstName:   req.FirstName,
		LastName:    req.LastName,
		Username:    req.Username,
		Email:       req.Email,
		Password:    req.Password,
		ReferrerURI: repman.registeredInstanceURI(),
	}

	crmBody, err := json.Marshal(crmPayload)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"signup: failed to marshal CRM payload: %s", err)
		http.Error(w, `{"error":"failed to prepare CRM request"}`, http.StatusInternalServerError)
		return
	}

	crmReq, err := http.NewRequest(http.MethodPost, repman.crmBase()+"/api/signup", bytes.NewReader(crmBody))
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"signup: failed to build CRM request: %s", err)
		http.Error(w, `{"error":"failed to build CRM request"}`, http.StatusInternalServerError)
		return
	}
	crmReq.Header.Set("Content-Type", "application/json")
	crmReq.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	crmResp, err := client.Do(crmReq)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"signup: CRM unreachable: %s", err)
		http.Error(w, fmt.Sprintf(`{"error":"CRM API unreachable: %s"}`, err), http.StatusBadGateway)
		return
	}
	defer crmResp.Body.Close()

	respBody, err := io.ReadAll(crmResp.Body)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"signup: failed to read CRM response: %s", err)
		http.Error(w, `{"error":"failed to read CRM response"}`, http.StatusBadGateway)
		return
	}

	w.WriteHeader(crmResp.StatusCode)
	w.Write(respBody)
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

	status, respBody, err := crmCallConfirm(crmBase, crmConfirmPayload{
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

const (
	SubPlanFree     = "free"
	SubPlanSupport  = "support"
	SubPlanServices = "support-services"
	SubPlanPartner  = "partner"
)

type changeSubPayload struct {
	URI  string `json:"uri"`
	Plan string `json:"plan"`
}

// crmGetPlans fetches the available subscription plans from the CRM (no auth required).
func crmGetPlans(crmBase string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, crmBase+"/api/subscription/plans", nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
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
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
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
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
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
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
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
// persists the new plan in the local configuration on success.
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

	switch req.Plan {
	case SubPlanFree, SubPlanSupport, SubPlanServices, SubPlanPartner:
	default:
		http.Error(w, fmt.Sprintf(`{"error":"invalid plan %q — must be one of free, support, support-services, partner"}`, req.Plan),
			http.StatusBadRequest)
		return
	}

	tok, err := repman.gitlabTokenFromConfig()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err), http.StatusBadGateway)
		return
	}

	uri := repman.registeredInstanceURI()

	status, respBody, err := crmChangeSubscription(repman.crmBase(), tok, uri, req.Plan)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"CRM unreachable: %s"}`, err), http.StatusBadGateway)
		return
	}

	if status == http.StatusOK || status == http.StatusCreated {
		repman.Conf.Cloud18SubscriptionPlan = req.Plan
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"subscription: plan changed to %s for URI %s", req.Plan, uri)
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
