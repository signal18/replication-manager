// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/signal18/replication-manager/config"
)

func loginRepman(t *testing.T, issuer string) *ReplicationManager {
	t.Helper()
	repman := registerRepman(t, issuer, []byte("0123456789abcdef"))
	repman.Conf.Cloud18 = true
	repman.Conf.TokenTimeout = 1
	repman.initKeys()
	return repman
}

// #2182: a login with the Cloud18 email and password gives the GitLab token (GitLab logging the
// person in with Cloud18) and a realm token of the login client, verified by repman itself; a
// wrong password is refused as a credential mismatch, never through GitLab's password grant once
// it is off; a realm without the login client still logs the person in (the GitLab token only).
func TestCloud18PasswordLogin(t *testing.T) {
	f, issuer := newRegisterFakes(t)
	repman := loginRepman(t, issuer)
	repman.Conf.Cloud18GitLabPasswordGrant = false

	sso, err := repman.ssoPasswordLogin("admin@acme.example", "s3cr3t-cloud18")
	if err != nil || sso.token != "gl-admin%40acme.example" || sso.cloud18 == nil {
		t.Fatalf("Cloud18 login: %+v %v", sso, err)
	}
	if sso.cloud18.Access == "" || sso.cloud18.Refresh != "rt-admin@acme.example" || sso.cloud18.Web == nil {
		t.Fatalf("the login must carry the realm token, its refresh token and the GitLab session: %+v", sso.cloud18)
	}
	u, err := repman.Conf.VerifyCloud18Token(sso.cloud18.Access)
	if err != nil || u.Email != "admin@acme.example" {
		t.Fatalf("the realm token must verify: %+v %v", u, err)
	}
	if claim := sso.cloud18Claim(repman.Conf); !strings.HasPrefix(claim, "hash_") || repman.Conf.GetDecryptedPassword("cloud18-login", claim) != "rt-admin@acme.example" {
		t.Fatalf("the session keeps the refresh token encrypted: %q", claim)
	}

	_, err = repman.ssoPasswordLogin("admin@acme.example", "old-gitlab-password")
	if !errors.Is(err, config.ErrCloud18BadCredentials) || classifySSOAuthError(err) != "credential_mismatch" {
		t.Fatalf("a wrong Cloud18 password, the grant off: %v (%s)", err, classifySSOAuthError(err))
	}

	f.mu.Lock()
	f.noLoginClient = true
	f.mu.Unlock()
	sso, err = repman.ssoPasswordLogin("admin@acme.example", "s3cr3t-cloud18")
	if err != nil || sso.token == "" || sso.cloud18.Access != "" || sso.cloud18Claim(repman.Conf) != "" {
		t.Fatalf("no login client: logged in with the GitLab token only: %+v %v", sso, err)
	}
}

// #2182: a realm token is an SSO identity only on a registered Cloud18 instance, only from the
// realm it trusts, only with the replication-manager API audience, a verified email, a valid
// signature and before its end; its session reads as an SSO login of the email and keeps the token
// (encrypted) for the other instances.
func TestCloud18TokenSession(t *testing.T) {
	f, issuer := newRegisterFakes(t)
	repman := loginRepman(t, issuer)
	good := f.realmToken("person@acme.example", config.ConstCloud18APIAudience, true, 5*time.Minute)

	session, u, err := repman.cloud18TokenSession(good)
	if err != nil || u.Email != "person@acme.example" {
		t.Fatalf("a valid realm token: %v", err)
	}
	tok, err := jwt.Parse(session, func(*jwt.Token) (interface{}, error) { return jwt.ParseRSAPublicKeyFromPEM(verificationKey) })
	if err != nil {
		t.Fatal(err)
	}
	info, err := repman.GetUserInfoMap(tok)
	if err != nil || info["AuthType"] != "SSO" || info["User"] != "person@acme.example" || info["Password"] != "" {
		t.Fatalf("the session must read as the SSO login of the email, no password: %v %v", info, err)
	}
	if repman.cloud18TokenFor(info["Cloud18Token"], info["Cloud18Login"]) != good {
		t.Fatal("the session must give its realm token back for the other instances")
	}
	if again, _, _ := repman.cloud18TokenSession(good); again != session {
		t.Error("one session per realm token (cached)")
	}

	for name, raw := range map[string]string{
		"foreign audience": f.realmToken("person@acme.example", config.ConstCloud18StorageAudience, true, 5*time.Minute),
		"unverified email": f.realmToken("person@acme.example", config.ConstCloud18APIAudience, false, 5*time.Minute),
		"expired":          f.realmToken("person@acme.example", config.ConstCloud18APIAudience, true, -time.Minute),
	} {
		if _, _, err := repman.cloud18TokenSession(raw); !errors.Is(err, config.ErrCloud18TokenRefused) {
			t.Errorf("%s: must be refused, got %v", name, err)
		}
	}
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repman.cloud18TokenSession(signRealmToken(otherKey, issuer, "person@acme.example", config.ConstCloud18APIAudience, true, 5*time.Minute)); err == nil {
		t.Error("a token signed with another key (same issuer) must be refused")
	}
	if repman.acceptsCloud18Tokens(signRealmToken(f.realmKey, "https://elsewhere.example/realms/x", "person@acme.example", config.ConstCloud18APIAudience, true, 5*time.Minute)) {
		t.Error("a token of another realm is not taken as a realm token")
	}
	repman.Conf.Cloud18 = false
	if repman.acceptsCloud18Tokens(good) {
		t.Error("an instance not registered to Cloud18 never trusts the realm")
	}
}

// #2182: the API middleware and /api/login take a realm token: the request goes on as the SSO
// session of its person; a refused token stops there.
func TestCloud18TokenAPIAndLogin(t *testing.T) {
	f, issuer := newRegisterFakes(t)
	repman := loginRepman(t, issuer)
	repman.clustersReady.Store(true)
	good := f.realmToken("person@acme.example", config.ConstCloud18APIAudience, true, 5*time.Minute)

	var seen string
	next := func(w http.ResponseWriter, r *http.Request) { seen = repman.GetUserFromRequest(r) }
	r := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	r.Header.Set("Authorization", "Bearer "+good)
	w := httptest.NewRecorder()
	repman.validateTokenMiddleware(w, r, next)
	if w.Code != http.StatusOK || seen != "person@acme.example" {
		t.Fatalf("middleware: %d user %q", w.Code, seen)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	r.Header.Set("Authorization", "Bearer "+f.realmToken("person@acme.example", config.ConstCloud18StorageAudience, true, 5*time.Minute))
	w = httptest.NewRecorder()
	seen = ""
	repman.validateTokenMiddleware(w, r, next)
	if w.Code != http.StatusUnauthorized || seen != "" {
		t.Fatalf("a refused realm token: %d %q", w.Code, seen)
	}

	r = httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+good)
	w = httptest.NewRecorder()
	repman.loginHandler(w, r)
	var resp struct {
		Token string `json:"token"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &resp) != nil || resp.Token == "" {
		t.Fatalf("/api/login with a realm token: %d %s", w.Code, w.Body.String())
	}

	p, err := repman.AuthenticateMCP(r)
	if err != nil || p.User != "person@acme.example" || p.AuthMethod != "oidc" || p.Cloud18Token != good {
		t.Fatalf("MCP: %+v %v", p, err)
	}
}

// #2182: another instance is logged into with the caller's realm token; an instance on an older
// release refuses it and is logged into with the password; no password and a refused token: the
// refusal.
func TestPeerLoginAsIdentity(t *testing.T) {
	var gotBearer, gotPassword string
	newer := true
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var c userCredentials
		json.Unmarshal(b, &c)
		switch {
		case newer && r.Header.Get("Authorization") == "Bearer realm-token":
			gotBearer = "realm-token"
			w.Write([]byte(`{"token":"peer-session"}`))
		case c.Password == "clear":
			gotPassword = c.Password
			w.Write([]byte(`{"token":"peer-session-password"}`))
		default:
			http.Error(w, "Error logging in: Invalid credentials", http.StatusUnauthorized)
		}
	}))
	defer peer.Close()
	repman := &ReplicationManager{Conf: &config.Config{}}
	u, _ := url.Parse(peer.URL + "/api/login")

	status, body := repman.peerLoginAsIdentity(u, peerIdentity{user: "u@x.io", password: "clear", bearer: "realm-token"})
	if status != http.StatusOK || !strings.Contains(string(body), "peer-session\"") || gotBearer == "" || gotPassword != "" {
		t.Fatalf("a newer instance: the realm token, not the password: %d %s", status, body)
	}
	newer = false
	status, body = repman.peerLoginAsIdentity(u, peerIdentity{user: "u@x.io", password: "clear", bearer: "realm-token"})
	if status != http.StatusOK || !strings.Contains(string(body), "peer-session-password") {
		t.Fatalf("an older instance: the password: %d %s", status, body)
	}
	if status, _ := repman.peerLoginAsIdentity(u, peerIdentity{user: "u@x.io", bearer: "realm-token"}); status != http.StatusUnauthorized {
		t.Fatalf("no password and a refused token: %d", status)
	}
}

// #2182: a session's Cloud18 login gives a realm token from its refresh token, refreshed once per
// token lifetime, not once per call; a refresh the realm refuses gives none (the password is used).
func TestCloud18LoginAccess(t *testing.T) {
	f, issuer := newRegisterFakes(t)
	repman := loginRepman(t, issuer)
	claim := repman.Conf.GetEncryptedString("rt-person@acme.example")
	a := repman.cloud18TokenFor("", claim)
	b := repman.cloud18TokenFor("", claim)
	if a == "" || a != b {
		t.Fatalf("refreshed tokens: %q %q", a, b)
	}
	f.mu.Lock()
	n := f.refreshes
	f.mu.Unlock()
	if n != 1 {
		t.Fatalf("one refresh for both calls, got %d", n)
	}
	if repman.cloud18TokenFor("", repman.Conf.GetEncryptedString("revoked")) != "" {
		t.Fatal("a refused refresh gives no token")
	}
}
