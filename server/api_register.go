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
)

// registerRequest is the JSON body accepted by POST /api/register.
type registerRequest struct {
	Email string `json:"email"`
	// URI is parsed as domain.subdomain.zone
	URI      string `json:"uri"`
	Password string `json:"password"`
}

// crmRegisterPayload is the body forwarded to the CRM API.
type crmRegisterPayload struct {
	Email     string `json:"email"`
	Password  string `json:"password"`
	Domain    string `json:"domain"`
	Subdomain string `json:"subdomain"`
	Zone      string `json:"zone"`
}

// handlerRegister proxies a cluster-slot registration request to the CRM API.
//
// POST /api/register
//
// Request body:
//
//	{
//	  "email":    "user@company.com",
//	  "password": "s3cr3tpass",
//	  "uri":      "mycompany.ovh.fr-1"
//	}
//
// The uri field is split on the first two dots into domain, subdomain, zone.
// The request is forwarded to the URL configured by cloud18-crm-api-url
// (default: https://api.crm.ovh-fr-2.signal18.cloud18.io).
func (repman *ReplicationManager) handlerRegister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
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
	domain, subdomain, zone := parts[0], parts[1], parts[2]

	// ----------------------------------------------------------------
	// Forward to CRM API
	// ----------------------------------------------------------------
	crmBase := strings.TrimRight(repman.Conf.Cloud18CrmApiUrl, "/")
	if crmBase == "" {
		crmBase = "https://api.crm.ovh-fr-2.signal18.cloud18.io"
	}
	crmURL := crmBase + "/api/register"

	payload := crmRegisterPayload{
		Email:     strings.TrimSpace(strings.ToLower(req.Email)),
		Password:  req.Password,
		Domain:    strings.TrimSpace(strings.ToLower(domain)),
		Subdomain: strings.TrimSpace(strings.ToLower(subdomain)),
		Zone:      strings.TrimSpace(strings.ToLower(zone)),
	}

	payloadBytes, err := json.Marshal(payload)
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

	client := &http.Client{}
	crmResp, err := client.Do(crmReq)
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
	// Relay CRM response verbatim (status code + body)
	// ----------------------------------------------------------------
	w.WriteHeader(crmResp.StatusCode)
	w.Write(respBody)
}
