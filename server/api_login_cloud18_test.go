// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
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
// wrong password is refused as a credential mismatch, never through GitLab's password grant (it is
// no longer tried); a realm without the login client still logs the person in (the GitLab token only).
func TestCloud18PasswordLogin(t *testing.T) {
	f, issuer := newRegisterFakes(t)
	repman := loginRepman(t, issuer)

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
		t.Fatalf("a wrong Cloud18 password: %v (%s)", err, classifySSOAuthError(err))
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

	// every route: the token middleware, a route checking the token itself (isValidRequest)
	var seen, mcpUser, mcpToken string
	var valid bool
	h := repman.cloud18TokenMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		repman.validateTokenMiddleware(w, r, func(w http.ResponseWriter, r *http.Request) { seen = repman.GetUserFromRequest(r) })
		valid, _ = repman.isValidRequest(r)
		if p, err := repman.AuthenticateMCP(r); err == nil {
			mcpUser, mcpToken = p.User, p.Cloud18Token
		}
	}))
	r := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	r.Header.Set("Authorization", "Bearer "+good)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || seen != "person@acme.example" || !valid {
		t.Fatalf("a realm token on the API: %d user %q valid %v", w.Code, seen, valid)
	}
	if mcpUser != "person@acme.example" || mcpToken != good {
		t.Fatalf("MCP: user %q, the realm token kept for the other instances: %v", mcpUser, mcpToken == good)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	r.Header.Set("Authorization", "Bearer "+f.realmToken("person@acme.example", config.ConstCloud18StorageAudience, true, 5*time.Minute))
	w = httptest.NewRecorder()
	seen = ""
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || seen != "" || strings.Contains(w.Body.String(), "audience") {
		t.Fatalf("a refused realm token: %d %q %q", w.Code, seen, w.Body.String())
	}

	// a normal session JWT goes through untouched
	local, _ := repman.issueJWT(struct{ Name, Role, Password string }{"admin", "Member", ""}, "")
	var gotAuth string
	pass := repman.cloud18TokenMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gotAuth = r.Header.Get("Authorization") }))
	r = httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	r.Header.Set("Authorization", "Bearer "+local)
	pass.ServeHTTP(httptest.NewRecorder(), r)
	if gotAuth != "Bearer "+local {
		t.Fatal("a non-realm bearer must not be touched")
	}
	// /api/login keeps the realm token (it verifies it itself)
	r = httptest.NewRequest(http.MethodPost, cloud18LoginPath, strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+good)
	pass.ServeHTTP(httptest.NewRecorder(), r)
	if cloud18LoginPath != "/api/login" || gotAuth != "Bearer "+good {
		t.Fatal("the middleware must leave /api/login's realm token to the handler")
	}
	// an instance not registered to Cloud18: the realm token passes through, never a session
	repman.Conf.Cloud18 = false
	r = httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	r.Header.Set("Authorization", "Bearer "+good)
	pass.ServeHTTP(httptest.NewRecorder(), r)
	repman.Conf.Cloud18 = true
	if gotAuth != "Bearer "+good {
		t.Fatal("an unregistered instance never turns a realm token into a session")
	}

	r = httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader("{}"))
	r.Header.Set("Authorization", "Bearer "+good)
	w = httptest.NewRecorder()
	repman.cloud18TokenMiddleware(http.HandlerFunc(repman.loginHandler)).ServeHTTP(w, r)
	var resp struct {
		Token string `json:"token"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &resp) != nil || resp.Token == "" {
		t.Fatalf("/api/login with a realm token: %d %s", w.Code, w.Body.String())
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
	// a 403 (an ACL denial), a 5xx: the password does not go out
	for _, code := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusBadGateway} {
		gotPassword = ""
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			if strings.Contains(string(b), "clear") {
				gotPassword = "clear"
			}
			w.WriteHeader(code)
		}))
		ou, _ := url.Parse(other.URL + "/api/login")
		status, _ := repman.peerLoginAsIdentity(ou, peerIdentity{user: "u@x.io", password: "clear", bearer: "realm-token"})
		other.Close()
		if status != code || gotPassword != "" {
			t.Errorf("HTTP %d to the token: returned as is, no password (got %d, password sent %v)", code, status, gotPassword != "")
		}
	}
}

// #2182 review: a refresh token the realm refuses is not sent again for a while (every request to
// another instance would wait on the realm); a rotating realm after the entry is rebuilt (restart)
// gives no token: the session goes on with the password.
func TestCloud18LoginRefused(t *testing.T) {
	f, issuer := newRegisterFakes(t)
	repman := loginRepman(t, issuer)
	dead := repman.Conf.GetEncryptedString("dead-refresh")
	if repman.cloud18TokenFor("", dead) != "" || repman.cloud18TokenFor("", dead) != "" {
		t.Fatal("a refused refresh token gives no token")
	}
	f.mu.Lock()
	calls := f.refreshTries
	f.mu.Unlock()
	if calls != 1 {
		t.Fatalf("the refused refresh token must reach the realm once, not again (tries %d)", calls)
	}

	f.mu.Lock()
	f.rotate = true
	f.mu.Unlock()
	claim := repman.Conf.GetEncryptedString("rt-restart@acme.example")
	if repman.cloud18TokenFor("", claim) == "" {
		t.Fatal("first refresh")
	}
	config.ForgetCloud18Logins() // an instance restart
	if repman.cloud18TokenFor("", claim) != "" {
		t.Fatal("a rotating realm revoked the session's refresh token: no realm token after a restart")
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

// #2182 review: a realm rotating refresh tokens (the used one revoked) keeps giving tokens: the
// newest refresh token is kept by the login, the first one (in the session) is never sent twice.
func TestCloud18LoginAccessRotation(t *testing.T) {
	f, issuer := newRegisterFakes(t)
	repman := loginRepman(t, issuer)
	f.mu.Lock()
	f.rotate = true
	f.mu.Unlock()
	claim := repman.Conf.GetEncryptedString("rt-rotating@acme.example")
	for i := 0; i < 3; i++ {
		if repman.cloud18TokenFor("", claim) == "" {
			t.Fatalf("refresh %d with a rotating realm gave no token", i+1)
		}
	}
	f.mu.Lock()
	n := f.refreshes
	f.mu.Unlock()
	if n != 3 {
		t.Fatalf("30 s tokens: each call refreshes with the newest refresh token, got %d refreshes", n)
	}
}

// #2182 review: a session of the Cloud18 login is not asked its password again (subscribe); an
// older SSO session is.
func TestIsCloud18LoginSession(t *testing.T) {
	if !isCloud18LoginSession(map[string]string{"Cloud18Token": "hash_x"}) || !isCloud18LoginSession(map[string]string{"Cloud18Login": "hash_y"}) {
		t.Error("a session from a realm token or a Cloud18 login is proven by the realm")
	}
	if isCloud18LoginSession(map[string]string{"Password": "hash_z"}) {
		t.Error("an older SSO session re-checks its password")
	}
}

// #2182 review: forged realm tokens cost no realm request beyond one key fetch per minute, a refused
// token is answered from memory, and the security log is bounded.
func TestCloud18TokenFlood(t *testing.T) {
	f, issuer := newRegisterFakes(t)
	repman := loginRepman(t, issuer)
	certs := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.certFetches
	}
	good := f.realmToken("person@acme.example", config.ConstCloud18APIAudience, true, 5*time.Minute)
	if _, _, err := repman.cloud18TokenSession(good); err != nil {
		t.Fatal(err)
	}
	base := certs()
	for i := 0; i < 50; i++ {
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": issuer, "aud": config.ConstCloud18APIAudience, "exp": time.Now().Add(time.Minute).Unix()})
		tok.Header["kid"] = fmt.Sprintf("forged-%d", i)
		raw, _ := tok.SignedString(f.realmKey)
		if _, _, err := repman.cloud18TokenSession(raw); err == nil {
			t.Fatal("a token naming an unknown key must be refused")
		}
	}
	if n := certs() - base; n > 1 {
		t.Fatalf("50 unknown key ids: at most one key fetch per minute, got %d", n)
	}
	expired := f.realmToken("person@acme.example", config.ConstCloud18APIAudience, true, -time.Minute)
	if _, _, err := repman.cloud18TokenSession(expired); !errors.Is(err, config.ErrCloud18TokenRefused) {
		t.Fatalf("an expired token: %v", err)
	}
	if _, _, err := repman.cloud18TokenSession(expired); !errors.Is(err, errCloud18TokenRefusedAgain) {
		t.Fatalf("a refused token is answered from memory: %v", err)
	}
	cloud18TokenLog.Lock()
	cloud18TokenLog.window, cloud18TokenLog.n = time.Now(), cloud18TokenLogMax
	cloud18TokenLog.Unlock()
	repman.logCloud18TokenRefused("api_auth_failure", "x", config.ErrCloud18TokenRefused)
	repman.logCloud18TokenRefused("api_auth_failure", "x", config.ErrCloud18TokenRefused)
	cloud18TokenLog.Lock()
	n := cloud18TokenLog.n
	cloud18TokenLog.Unlock()
	if n != cloud18TokenLogMax+2 {
		t.Fatalf("the log counter: %d", n)
	}
}

// cloud18-login-acr: a realm token below the level the instance asks is refused everywhere (a
// bearer, the browser sign-in, the Sign in form); the Sign in form, which only posts a password,
// cannot give a one-time code and says to use Sign in with Cloud18; the browser sign-in asks the
// realm for the level. Without the setting any level will do.
func TestCloud18LoginACR(t *testing.T) {
	f, issuer := newRegisterFakes(t)
	repman := loginRepman(t, issuer)
	repman.Conf.Cloud18LoginACR = "mfa, 2"

	if got := repman.Conf.Cloud18LoginACRValues(); len(got) != 2 || got[0] != "mfa" || got[1] != "2" {
		t.Fatalf("levels: %q", got)
	}
	strong := signRealmTokenACR(f.realmKey, issuer, "person@acme.example", config.ConstCloud18APIAudience, "mfa", true, 5*time.Minute)
	if _, _, err := repman.cloud18TokenSession(strong); err != nil {
		t.Fatalf("a token at the level: %v", err)
	}
	if _, err := repman.Conf.VerifyCloud18Token(signRealmTokenACR(f.realmKey, issuer, "person@acme.example", config.ConstCloud18APIAudience, "2", true, 5*time.Minute)); err != nil {
		t.Fatalf("a token at the second level named: %v", err)
	}
	for name, acr := range map[string]string{"password only": "1", "no acr": ""} {
		raw := signRealmTokenACR(f.realmKey, issuer, "other@acme.example", config.ConstCloud18APIAudience, acr, true, 5*time.Minute)
		if _, _, err := repman.cloud18TokenSession(raw); !errors.Is(err, config.ErrCloud18TokenRefused) {
			t.Errorf("%s: must be refused, got %v", name, err)
		}
	}

	// the realm asks the code: the form cannot answer it
	f.mu.Lock()
	f.otp = true
	f.mu.Unlock()
	if _, err := repman.ssoPasswordLogin("admin@acme.example", "s3cr3t-cloud18"); !errors.Is(err, config.ErrCloud18SecondFactor) {
		t.Fatalf("the realm asks a one-time code: %v", err)
	}
	// a realm that does not ask it gives a token below the level: refused as well
	f.mu.Lock()
	f.otp, f.acr = false, "1"
	f.mu.Unlock()
	if _, err := repman.ssoPasswordLogin("admin@acme.example", "s3cr3t-cloud18"); !errors.Is(err, config.ErrCloud18SecondFactor) {
		t.Fatalf("a password-level token: %v", err)
	}

	u, err := repman.Conf.Cloud18SSOAuthorizeURL("https://repman.example", "st", "ch", config.Cloud18SSOMode{})
	if err != nil || !strings.Contains(u, "acr_values=mfa+2") {
		t.Fatalf("the browser sign-in asks the level: %s %v", u, err)
	}

	repman.Conf.Cloud18LoginACR = ""
	f.mu.Lock()
	f.acr = ""
	f.mu.Unlock()
	if _, err := repman.ssoPasswordLogin("admin@acme.example", "s3cr3t-cloud18"); err != nil {
		t.Fatalf("no level asked: the form logs in: %v", err)
	}
	if u, _ := repman.Conf.Cloud18SSOAuthorizeURL("https://repman.example", "st", "ch", config.Cloud18SSOMode{}); strings.Contains(u, "acr_values") {
		t.Errorf("no level asked: no acr_values: %s", u)
	}
}
