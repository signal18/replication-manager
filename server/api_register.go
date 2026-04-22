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
	"strings"

	"github.com/signal18/replication-manager/config"
)

// registerRequest is the JSON body accepted by POST /api/register (step 1).
type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// URI is parsed as domain.subdomain.zone  (e.g. "mycompany.ovh.fr-1")
	URI string `json:"uri"`
}

// registerConfirmRequest is the JSON body accepted by POST /api/register/confirm (step 2).
type registerConfirmRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	URI      string `json:"uri"`
}

// crmRegisterPayload is the body forwarded to the CRM API for step 1.
type crmRegisterPayload struct {
	Email     string `json:"email"`
	Password  string `json:"password"`
	Domain    string `json:"domain"`
	Subdomain string `json:"subdomain"`
	Zone      string `json:"zone"`
}

// crmConfirmPayload is the body forwarded to the CRM API for step 2.
type crmConfirmPayload struct {
	Email     string `json:"email"`
	Password  string `json:"password"`
	Domain    string `json:"domain"`
	Subdomain string `json:"subdomain"`
	Zone      string `json:"zone"`
}

// handlerRegister is step 1 of the registration workflow.
//
// POST /api/register  (admin JWT required)
//
// Request body:
//
//	{
//	  "email":    "user@company.com",
//	  "password": "s3cr3tpass",
//	  "uri":      "mycompany.ovh.fr-1"
//	}
//
// Forwards the request to the CRM API which creates a GitLab account and
// lets GitLab send its own email confirmation link to the user.
// Returns 202 Accepted — the client should prompt the user to confirm their
// email and then call POST /api/register/confirm.
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
		http.Error(w,
			`{"error":"uri must be in domain.subdomain.zone format (e.g. mycompany.ovh.fr-1)"}`,
			http.StatusBadRequest)
		return
	}
	domain := strings.TrimSpace(strings.ToLower(parts[0]))
	subdomain := strings.TrimSpace(strings.ToLower(parts[1]))
	zone := strings.TrimSpace(strings.ToLower(parts[2]))
	email := strings.TrimSpace(strings.ToLower(req.Email))

	crmBase := strings.TrimRight(repman.Conf.Cloud18CrmApiUrl, "/")
	if crmBase == "" {
		crmBase = "https://api.crm.ovh-fr-2.signal18.cloud18.io"
	}

	payload := crmRegisterPayload{
		Email:     email,
		Password:  req.Password,
		Domain:    domain,
		Subdomain: subdomain,
		Zone:      zone,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, `{"error":"internal error encoding request"}`, http.StatusInternalServerError)
		return
	}

	crmReq, err := http.NewRequest(http.MethodPost, crmBase+"/api/register", bytes.NewReader(payloadBytes))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to build CRM request: %s"}`, err), http.StatusInternalServerError)
		return
	}
	crmReq.Header.Set("Content-Type", "application/json")
	crmReq.Header.Set("Accept", "application/json")

	crmClient := &http.Client{}
	crmResp, err := crmClient.Do(crmReq)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"CRM API unreachable: %s"}`, err), http.StatusBadGateway)
		return
	}
	defer crmResp.Body.Close()

	respBody, err := io.ReadAll(crmResp.Body)
	if err != nil {
		http.Error(w, `{"error":"failed to read CRM response"}`, http.StatusBadGateway)
		return
	}

	// Relay the CRM response verbatim (202 = email sent, or error codes).
	w.WriteHeader(crmResp.StatusCode)
	w.Write(respBody)
}

// handlerRegisterConfirm is step 2 of the registration workflow.
//
// POST /api/register/confirm  (admin JWT required)
//
// Called after the user has clicked the GitLab confirmation link in their
// email.  Forwards to the CRM which verifies the account is confirmed,
// then creates the group and projects.  On CRM 201 the handler runs the
// Cloud18 connect flow (InitGitConfig) without requiring a restart.
//
// Request body:
//
//	{
//	  "email":    "user@company.com",
//	  "password": "s3cr3tpass",
//	  "uri":      "mycompany.ovh.fr-1"
//	}
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
		http.Error(w,
			`{"error":"uri must be in domain.subdomain.zone format (e.g. mycompany.ovh.fr-1)"}`,
			http.StatusBadRequest)
		return
	}
	domain := strings.TrimSpace(strings.ToLower(parts[0]))
	subdomain := strings.TrimSpace(strings.ToLower(parts[1]))
	zone := strings.TrimSpace(strings.ToLower(parts[2]))
	email := strings.TrimSpace(strings.ToLower(req.Email))

	crmBase := strings.TrimRight(repman.Conf.Cloud18CrmApiUrl, "/")
	if crmBase == "" {
		crmBase = "https://api.crm.ovh-fr-2.signal18.cloud18.io"
	}

	payload := crmConfirmPayload{
		Email:     email,
		Password:  req.Password,
		Domain:    domain,
		Subdomain: subdomain,
		Zone:      zone,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, `{"error":"internal error encoding request"}`, http.StatusInternalServerError)
		return
	}

	crmReq, err := http.NewRequest(http.MethodPost, crmBase+"/api/register/confirm", bytes.NewReader(payloadBytes))
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"failed to build CRM request: %s"}`, err), http.StatusInternalServerError)
		return
	}
	crmReq.Header.Set("Content-Type", "application/json")
	crmReq.Header.Set("Accept", "application/json")

	crmClient := &http.Client{}
	crmResp, err := crmClient.Do(crmReq)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"CRM API unreachable: %s"}`, err), http.StatusBadGateway)
		return
	}
	defer crmResp.Body.Close()

	respBody, err := io.ReadAll(crmResp.Body)
	if err != nil {
		http.Error(w, `{"error":"failed to read CRM response"}`, http.StatusBadGateway)
		return
	}

	if crmResp.StatusCode != http.StatusCreated {
		w.WriteHeader(crmResp.StatusCode)
		w.Write(respBody)
		return
	}

	// ----------------------------------------------------------------
	// CRM confirmed — configure Cloud18 and run connect flow
	// ----------------------------------------------------------------

	repman.Conf.Cloud18 = false
	repman.Conf.Cloud18Domain = domain
	repman.Conf.Cloud18SubDomain = subdomain
	repman.Conf.Cloud18SubDomainZone = zone
	repman.Conf.Cloud18GitUser = email
	repman.Conf.Cloud18GitPassword = req.Password
	var gitSecret config.Secret
	gitSecret.Value = req.Password
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
			"register/confirm: InitGitConfig failed after CRM registration: %s", err)

		var crmBody map[string]interface{}
		if json.Unmarshal(respBody, &crmBody) == nil {
			crmBody["connect_error"] = err.Error()
			if out, e := json.Marshal(crmBody); e == nil {
				w.WriteHeader(http.StatusCreated)
				w.Write(out)
				return
			}
		}
		w.WriteHeader(http.StatusCreated)
		w.Write(respBody)
		return
	}

	w.WriteHeader(http.StatusCreated)
	w.Write(respBody)
}
