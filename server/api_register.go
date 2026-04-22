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

// registerRequest is the JSON body accepted by POST /api/register.
type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// URI is parsed as domain.subdomain.zone  (e.g. "mycompany.ovh.fr-1")
	URI string `json:"uri"`
}

// crmRegisterPayload is the body forwarded to the CRM API.
type crmRegisterPayload struct {
	Email     string `json:"email"`
	Password  string `json:"password"`
	Domain    string `json:"domain"`
	Subdomain string `json:"subdomain"`
	Zone      string `json:"zone"`
}

// handlerRegister registers a new cluster slot on the Signal18 DBAaS platform.
//
// POST /api/register  (no authentication required)
//
// Request body:
//
//	{
//	  "email":    "user@company.com",
//	  "password": "s3cr3tpass",
//	  "uri":      "mycompany.ovh.fr-1"
//	}
//
// On success (CRM API returns 201) the handler:
//  1. Sets Cloud18Domain, Cloud18SubDomain, Cloud18SubDomainZone from the URI
//  2. Sets Cloud18GitUser / Cloud18GitPassword from email / password
//  3. Enables Cloud18 and calls InitGitConfig — the same "connect" flow triggered
//     by setting cloud18=true in global settings — which authenticates with GitLab,
//     creates a personal access token, sets up Git URLs, and clones the config repo.
func (repman *ReplicationManager) handlerRegister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// ----------------------------------------------------------------
	// Admin-only endpoint
	// ----------------------------------------------------------------
	claims, err := repman.GetJWTClaims(r)
	if err != nil || claims["User"] != "admin" {
		http.Error(w, `{"error":"administrator access required"}`, http.StatusForbidden)
		return
	}

	// ----------------------------------------------------------------
	// Parse request body
	// ----------------------------------------------------------------
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

	// ----------------------------------------------------------------
	// Parse URI → domain.subdomain.zone
	// ----------------------------------------------------------------
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

	// ----------------------------------------------------------------
	// Forward registration to CRM API
	// ----------------------------------------------------------------
	crmBase := strings.TrimRight(repman.Conf.Cloud18CrmApiUrl, "/")
	if crmBase == "" {
		crmBase = "https://api.crm.ovh-fr-2.signal18.cloud18.io"
	}
	crmURL := crmBase + "/api/register"

	crmPayload := crmRegisterPayload{
		Email:     email,
		Password:  req.Password,
		Domain:    domain,
		Subdomain: subdomain,
		Zone:      zone,
	}

	payloadBytes, err := json.Marshal(crmPayload)
	if err != nil {
		http.Error(w, `{"error":"internal error encoding request"}`, http.StatusInternalServerError)
		return
	}

	crmReq, err := http.NewRequest(http.MethodPost, crmURL, bytes.NewReader(payloadBytes))
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

	// ----------------------------------------------------------------
	// On failure: relay CRM response verbatim and stop
	// ----------------------------------------------------------------
	if crmResp.StatusCode != http.StatusCreated {
		w.WriteHeader(crmResp.StatusCode)
		w.Write(respBody)
		return
	}

	// ----------------------------------------------------------------
	// Registration succeeded — configure Cloud18 and run connect flow
	// ----------------------------------------------------------------

	// Set domain/subdomain/zone (guard against Cloud18 already being on)
	repman.Conf.Cloud18 = false
	repman.Conf.Cloud18Domain = domain
	repman.Conf.Cloud18SubDomain = subdomain
	repman.Conf.Cloud18SubDomainZone = zone
	repman.Conf.Cloud18GitUser = email
	repman.Conf.Cloud18GitPassword = req.Password
	var gitSecret config.Secret
	gitSecret.Value = req.Password
	gitSecret.OldValue = repman.Conf.GetDecryptedValue("cloud18-gitlab-password")
	repman.Conf.Secrets["cloud18-gitlab-password"] = gitSecret
	repman.Conf.Cloud18 = true

	// InitGitConfig is the same function called when setting cloud18=true in
	// global settings. It authenticates with GitLab via basic auth, obtains a
	// personal access token, idempotently creates the Git projects, sets GitUrl /
	// GitUrlPull, and clones the config repo if needed.
	if err := repman.InitGitConfig(repman.Conf); err != nil {
		repman.Conf.Cloud18 = false
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"register: InitGitConfig failed after CRM registration: %s", err)

		// Return the CRM success body but add a connect_error field so the
		// caller knows registration succeeded but connect needs a retry.
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

	// Both registration and connect succeeded — relay the CRM 201 body.
	w.WriteHeader(http.StatusCreated)
	w.Write(respBody)
}
