//go:build integration
// +build integration

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

// Integration test for the two-step Cloud18 registration workflow.
//
// Calls handlerRegister / handlerRegisterConfirm directly via httptest —
// no running server, no env vars required.  The only external dependencies
// are the live CRM API and GitLab (signal18.io).
//
// Run with:
//
//	go test -v -tags integration -run TestRegisterWorkflow ./server/

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/signal18/replication-manager/config"
)

// ---------------------------------------------------------------------------
// JWT helper — mint an admin token using the package-level RSA key pair.
// ---------------------------------------------------------------------------

func makeAdminJWT(t *testing.T) string {
	t.Helper()
	repman := &ReplicationManager{}
	repman.initKeys() // generates signingKey / verificationKey

	signer := jwt.New(jwt.SigningMethodRS256)
	claims := signer.Claims.(jwt.MapClaims)
	claims["iss"] = "https://api.replication-manager.signal18.io"
	claims["iat"] = time.Now().Unix()
	claims["exp"] = time.Now().Add(time.Hour).Unix()
	claims["jti"] = "1"
	claims["token"] = ""
	claims["CustomUserInfo"] = map[string]interface{}{
		"Name":     "admin",
		"Password": "repman",
		"Role":     "admin",
	}

	sk, err := jwt.ParseRSAPrivateKeyFromPEM(signingKey)
	if err != nil {
		t.Fatalf("makeAdminJWT: parse private key: %v", err)
	}
	tokenStr, err := signer.SignedString(sk)
	if err != nil {
		t.Fatalf("makeAdminJWT: sign token: %v", err)
	}
	return tokenStr
}

// ---------------------------------------------------------------------------
// mail.tm helpers — disposable email via https://api.mail.tm
// ---------------------------------------------------------------------------

const mailTmAPI = "https://api.mail.tm"

type mailTmDomain struct {
	Domain string `json:"domain"`
}

type mailTmDomainList struct {
	Members []mailTmDomain `json:"hydra:member"`
}

type mailTmAccount struct {
	ID      string `json:"id"`
	Address string `json:"address"`
}

type mailTmToken struct {
	Token string `json:"token"`
}

type mailTmMessage struct {
	ID      string `json:"id"`
	From    struct{ Address string } `json:"from"`
	Subject string `json:"subject"`
}

type mailTmMessageList struct {
	Members []mailTmMessage `json:"hydra:member"`
}

type mailTmMessageDetail struct {
	Text string `json:"text"`
	HTML string `json:"html"`
}

// mailTmGenAddress creates a mail.tm account and returns the email address
// and JWT token needed for subsequent inbox calls.
func mailTmGenAddress(t *testing.T) (email, token string) {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}

	// 1. Get an available domain
	resp, err := client.Get(mailTmAPI + "/domains?page=1")
	if err != nil {
		t.Fatalf("mail.tm get domains: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var domains mailTmDomainList
	if err := json.Unmarshal(body, &domains); err != nil || len(domains.Members) == 0 {
		t.Fatalf("mail.tm get domains: unexpected response: %s", body)
	}
	domain := domains.Members[0].Domain

	// 2. Create account with random address
	address := fmt.Sprintf("repman%d@%s", time.Now().UnixNano()%1000000, domain)
	password := "Repman1234!"
	payload, _ := json.Marshal(map[string]string{"address": address, "password": password})

	resp2, err := client.Post(mailTmAPI+"/accounts", "application/json", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatalf("mail.tm create account: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	var acct mailTmAccount
	if err := json.Unmarshal(body2, &acct); err != nil || acct.Address == "" {
		t.Fatalf("mail.tm create account: unexpected response: %s", body2)
	}

	// 3. Get JWT token
	resp3, err := client.Post(mailTmAPI+"/token", "application/json", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatalf("mail.tm get token: %v", err)
	}
	body3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()

	var tok mailTmToken
	if err := json.Unmarshal(body3, &tok); err != nil || tok.Token == "" {
		t.Fatalf("mail.tm get token: unexpected response: %s", body3)
	}

	return acct.Address, tok.Token
}

// mailTmWaitForConfirmation polls the mail.tm inbox until a GitLab
// confirmation email arrives or the timeout expires.
func mailTmWaitForConfirmation(t *testing.T, token string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 10 * time.Second}

	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Second)

		req, _ := http.NewRequest(http.MethodGet, mailTmAPI+"/messages?page=1", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Logf("mail.tm poll error: %v (retrying)", err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var inbox mailTmMessageList
		if err := json.Unmarshal(body, &inbox); err != nil {
			continue
		}

		for _, msg := range inbox.Members {
			if !strings.Contains(strings.ToLower(msg.From.Address), "gitlab") &&
				!strings.Contains(strings.ToLower(msg.Subject), "confirm") {
				continue
			}
			// Fetch full message body
			req2, _ := http.NewRequest(http.MethodGet, mailTmAPI+"/messages/"+msg.ID, nil)
			req2.Header.Set("Authorization", "Bearer "+token)
			resp2, err := client.Do(req2)
			if err != nil {
				continue
			}
			fullBody, _ := io.ReadAll(resp2.Body)
			resp2.Body.Close()

			var detail mailTmMessageDetail
			if err := json.Unmarshal(fullBody, &detail); err != nil {
				continue
			}
			combined := detail.Text + detail.HTML
			if url := extractGitLabConfirmURL(combined); url != "" {
				return url
			}
		}
	}
	t.Fatalf("mail.tm: no GitLab confirmation email received within %s", timeout)
	return ""
}

var gitlabConfirmRe = regexp.MustCompile(
	`https://gitlab\.signal18\.io/users/confirmation\?confirmation_token=[A-Za-z0-9_-]+`)

func extractGitLabConfirmURL(body string) string {
	return gitlabConfirmRe.FindString(body)
}

// ---------------------------------------------------------------------------
// GitLab cleanup helpers (use the admin token from repman config)
// ---------------------------------------------------------------------------

func gitlabDeleteUserByEmail(t *testing.T, adminToken, email string) {
	t.Helper()
	if adminToken == "" {
		t.Log("no GitLab admin token — skipping user cleanup")
		return
	}
	client := &http.Client{Timeout: 15 * time.Second}
	base := "https://gitlab.signal18.io"

	req, _ := http.NewRequest(http.MethodGet,
		fmt.Sprintf("%s/api/v4/users?search=%s", base, email), nil)
	req.Header.Set("PRIVATE-TOKEN", adminToken)
	resp, err := client.Do(req)
	if err != nil {
		t.Logf("GitLab cleanup: search user: %v", err)
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var users []map[string]interface{}
	if err := json.Unmarshal(body, &users); err != nil || len(users) == 0 {
		t.Logf("GitLab cleanup: user %q not found", email)
		return
	}
	userID := int(users[0]["id"].(float64))
	del, _ := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("%s/api/v4/users/%d?hard_delete=true", base, userID), nil)
	del.Header.Set("PRIVATE-TOKEN", adminToken)
	delResp, err := client.Do(del)
	if err != nil {
		t.Logf("GitLab cleanup: delete user %d: %v", userID, err)
		return
	}
	delResp.Body.Close()
	t.Logf("GitLab cleanup: deleted user %d (%s)", userID, email)
}

func gitlabDeleteGroup(t *testing.T, adminToken, groupPath string) {
	t.Helper()
	if adminToken == "" {
		return
	}
	client := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("https://gitlab.signal18.io/api/v4/groups/%s", groupPath), nil)
	req.Header.Set("PRIVATE-TOKEN", adminToken)
	resp, err := client.Do(req)
	if err != nil {
		t.Logf("GitLab cleanup: delete group %q: %v", groupPath, err)
		return
	}
	resp.Body.Close()
	t.Logf("GitLab cleanup: deleted group %q", groupPath)
}

// ---------------------------------------------------------------------------
// Test
// ---------------------------------------------------------------------------

// TestRegisterWorkflow exercises the complete two-step Cloud18 registration:
//
//  1. Generate a disposable inbox via mail.tm
//  2. POST /api/register  → expect 202
//  3. Poll mail.tm (up to 3 min) for the GitLab confirmation email
//  4. Follow the confirmation link
//  5. POST /api/register/confirm → expect 201
//  6. Clean up GitLab user and group
func TestRegisterWorkflow(t *testing.T) {
	// Build a minimal ReplicationManager with the real CRM URL
	repman := &ReplicationManager{}
	repman.Conf = &config.Config{
		Cloud18CrmApiUrl: "https://api.crm.ovh-fr-2.signal18.cloud18.io",
	}

	// Mint an admin JWT directly — no running server needed
	adminToken := makeAdminJWT(t)

	// Generate a disposable email address via mail.tm
	email, mailToken := mailTmGenAddress(t)
	t.Logf("temp email: %s", email)

	// Unique test URI to avoid zone conflicts across runs
	testURI := fmt.Sprintf("testdomain.testenv.t%d", time.Now().Unix()%100000)
	testDomain := strings.SplitN(testURI, ".", 3)[0]
	password := "TestPass123!"

	// GitLab cleanup uses the admin token from the CRM environment.
	// If not available the cleanup steps are skipped gracefully.
	gitlabAdminToken := ""

	t.Cleanup(func() {
		gitlabDeleteUserByEmail(t, gitlabAdminToken, email)
		gitlabDeleteGroup(t, gitlabAdminToken, testDomain)
	})

	// ----------------------------------------------------------------
	// Step 1 — POST /api/register
	// ----------------------------------------------------------------
	t.Log("step 1: POST /api/register")
	reqBody := fmt.Sprintf(`{"email":%q,"password":%q,"uri":%q}`, email, password, testURI)
	req1 := httptest.NewRequest(http.MethodPost, "/api/register", strings.NewReader(reqBody))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("Authorization", "Bearer "+adminToken)
	w1 := httptest.NewRecorder()
	repman.handlerRegister(w1, req1)

	t.Logf("step 1 response: HTTP %d — %s", w1.Code, w1.Body.String())
	if w1.Code != http.StatusAccepted {
		t.Fatalf("step 1: expected 202, got %d: %s", w1.Code, w1.Body.String())
	}

	// ----------------------------------------------------------------
	// Step 2 — wait for GitLab confirmation email, follow the link
	// ----------------------------------------------------------------
	t.Log("step 2: waiting for confirmation email (up to 3 minutes)...")
	confirmURL := mailTmWaitForConfirmation(t, mailToken, 3*time.Minute)
	t.Logf("step 2: confirmation URL: %s", confirmURL)

	confirmResp, err := http.Get(confirmURL)
	if err != nil {
		t.Fatalf("step 2: follow confirmation link: %v", err)
	}
	confirmResp.Body.Close()
	t.Logf("step 2: GitLab returned HTTP %d", confirmResp.StatusCode)
	if confirmResp.StatusCode >= 500 {
		t.Fatalf("step 2: confirmation link returned server error %d", confirmResp.StatusCode)
	}

	// ----------------------------------------------------------------
	// Step 3 — POST /api/register/confirm
	// ----------------------------------------------------------------
	t.Log("step 3: POST /api/register/confirm")
	req2 := httptest.NewRequest(http.MethodPost, "/api/register/confirm", strings.NewReader(reqBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+adminToken)
	w2 := httptest.NewRecorder()
	repman.handlerRegisterConfirm(w2, req2)

	t.Logf("step 3 response: HTTP %d — %s", w2.Code, w2.Body.String())
	if w2.Code != http.StatusCreated {
		t.Fatalf("step 3: expected 201, got %d: %s", w2.Code, w2.Body.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w2.Body.Bytes(), &result); err != nil {
		t.Fatalf("step 3: parse response: %v", err)
	}
	if connectErr, ok := result["connect_error"].(string); ok {
		t.Logf("WARNING: registration complete but connect failed: %s", connectErr)
	} else {
		t.Log("registration complete — Cloud18 connect flow succeeded")
	}
}
