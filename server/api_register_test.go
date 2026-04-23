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
	Text string   `json:"text"`
	HTML []string `json:"html"`
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
			combined := detail.Text + strings.Join(detail.HTML, " ")
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

// TestRegisterWorkflow exercises the async Cloud18 registration:
//
//  1. Generate a disposable inbox via mail.tm
//  2. POST /api/register → expect 202 (returns immediately, background goroutine polls CRM)
//  3. Concurrently follow the GitLab confirmation link from the email
//  4. Poll GET /api/register/status until state is "complete", "timeout", or "error"
//  5. Clean up GitLab user and group
func TestRegisterWorkflow(t *testing.T) {
	repman := &ReplicationManager{}
	repman.Conf = &config.Config{
		Cloud18CrmApiUrl: "https://api.crm.ovh-fr-2.signal18.cloud18.io",
	}

	adminToken := makeAdminJWT(t)

	email, mailToken := mailTmGenAddress(t)
	t.Logf("temp email: %s", email)

	testURI := fmt.Sprintf("testdomain.testenv.t%d", time.Now().Unix()%100000)
	testDomain := strings.SplitN(testURI, ".", 3)[0]
	password := "TestPass123!"

	gitlabAdminToken := ""
	t.Cleanup(func() {
		gitlabDeleteUserByEmail(t, gitlabAdminToken, email)
		gitlabDeleteGroup(t, gitlabAdminToken, testDomain)
	})

	// Step 1 — POST /api/register: must return 202 immediately
	t.Log("POST /api/register — expect 202 Accepted")
	reqBody := fmt.Sprintf(`{"email":%q,"password":%q,"uri":%q}`, email, password, testURI)
	req := httptest.NewRequest(http.MethodPost, "/api/register", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w := httptest.NewRecorder()
	repman.handlerRegister(w, req)

	t.Logf("register response: HTTP %d — %s", w.Code, w.Body.String())
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	// Step 2 — concurrently watch the inbox and follow the GitLab confirmation link
	go func() {
		confirmURL := mailTmWaitForConfirmation(t, mailToken, 4*time.Minute)
		t.Logf("confirmation URL found: %s — following link", confirmURL)
		resp, err := http.Get(confirmURL) //nolint:noctx
		if err != nil {
			t.Logf("WARNING: follow confirmation link: %v", err)
			return
		}
		resp.Body.Close()
		t.Logf("GitLab confirmation returned HTTP %d", resp.StatusCode)
	}()

	// Step 3 — poll GET /api/register/status until terminal state
	t.Log("polling /api/register/status …")
	pollDeadline := time.Now().Add(6 * time.Minute)
	for time.Now().Before(pollDeadline) {
		time.Sleep(10 * time.Second)

		sr := httptest.NewRequest(http.MethodGet, "/api/register/status", nil)
		sr.Header.Set("Authorization", "Bearer "+adminToken)
		sw := httptest.NewRecorder()
		repman.handlerRegisterStatus(sw, sr)

		var status map[string]interface{}
		if err := json.Unmarshal(sw.Body.Bytes(), &status); err != nil {
			t.Logf("status poll parse error: %v (retrying)", err)
			continue
		}
		state, _ := status["state"].(string)
		message, _ := status["message"].(string)
		t.Logf("status: state=%s  message=%s", state, message)

		switch state {
		case RegStateOK:
			if connectErr, ok := status["result"].(map[string]interface{})["connect_error"].(string); ok {
				t.Logf("WARNING: registration complete but connect failed: %s", connectErr)
			} else {
				t.Log("registration complete — Cloud18 connect flow succeeded")
			}
			return
		case RegStateTimeout:
			t.Fatalf("registration timed out: %s", message)
		case RegStateError:
			t.Fatalf("registration error: %s", message)
		}
	}
	t.Fatal("test deadline exceeded waiting for registration to complete")
}
