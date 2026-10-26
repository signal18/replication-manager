// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

func ssoRepman(t *testing.T, issuer string) *ReplicationManager {
	t.Helper()
	repman := loginRepman(t, issuer)
	repman.Conf.Cloud18GitUser = "owner@partner.example"
	repman.Conf.Cloud18SubscriptionPlan = "partner"
	repman.Conf.APIPublicURL = "https://repman.partner.example/"
	repman.Conf.Cloud18SSOLogin = true
	return repman
}

// #2185: Sign in with Cloud18 only on a registered partner with a public https address and the
// switch on; everywhere else the login form alone.
func TestCloud18SSOScope(t *testing.T) {
	_, issuer := newRegisterFakes(t)
	repman := ssoRepman(t, issuer)
	if public, err := repman.Conf.Cloud18SSO(); err != nil || public != "https://repman.partner.example" {
		t.Fatalf("a partner with a public address: %q %v", public, err)
	}
	for name, change := range map[string]func(c *config.Config){
		"switch off":         func(c *config.Config) { c.Cloud18SSOLogin = false },
		"not registered":     func(c *config.Config) { c.Cloud18 = false },
		"no registered user": func(c *config.Config) { c.Cloud18GitUser = "" },
		"not a partner":      func(c *config.Config) { c.Cloud18SubscriptionPlan = "developer" },
		"loopback default":   func(c *config.Config) { c.APIPublicURL = "https://127.0.0.1:10005" },
		"private address":    func(c *config.Config) { c.APIPublicURL = "https://10.60.50.66:10005" },
		"plain http":         func(c *config.Config) { c.APIPublicURL = "http://repman.partner.example" },
		"no realm":           func(c *config.Config) { c.Cloud18IdentityURL = "" },
	} {
		r := ssoRepman(t, issuer)
		change(r.Conf)
		if _, err := r.Conf.Cloud18SSO(); err == nil {
			t.Errorf("%s: Sign in with Cloud18 must not be offered", name)
		}
	}
}

// noFollow answers a request without following its redirect.
var noFollow = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// #2185: the browser round trip: start keeps a state and a PKCE verifier and sends the browser to
// the realm; the realm (the person already signed in there) answers with a code; the callback
// checks the state (once), trades the code, verifies the token and gives the dashboard a one-time
// code, traded once for an SSO session of the email; logout goes to the realm's logout, back to
// the login page.
func TestCloud18SSORoundTrip(t *testing.T) {
	f, issuer := newRegisterFakes(t)
	repman := ssoRepman(t, issuer)
	public := "https://repman.partner.example"

	w := httptest.NewRecorder()
	repman.handlerMuxCloud18SSO(w, httptest.NewRequest(http.MethodGet, "/api/auth/cloud18", nil))
	if !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatalf("status: %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	repman.handlerMuxCloud18SSOStart(w, httptest.NewRequest(http.MethodGet, "/api/auth/cloud18/start", nil))
	realmURL, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusFound || !strings.HasPrefix(realmURL.String(), issuer+"/protocol/openid-connect/auth?") ||
		realmURL.Query().Get("redirect_uri") != public+config.Cloud18SSOCallbackPath || realmURL.Query().Get("code_challenge_method") != "S256" ||
		realmURL.Query().Get("client_id") != config.ConstCloud18LoginClientID {
		t.Fatalf("start: %d %s", w.Code, realmURL)
	}
	state := realmURL.Query().Get("state")

	// the realm: the person has a Cloud18 session there
	req, _ := http.NewRequest(http.MethodGet, realmURL.String(), nil)
	req.AddCookie(&http.Cookie{Name: "KEYCLOAK_IDENTITY", Value: url.QueryEscape("person@partner.example")})
	resp, err := noFollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	back, _ := url.Parse(resp.Header.Get("Location"))
	if back.Query().Get("state") != state || back.Query().Get("code") == "" {
		t.Fatalf("realm answer: %s", back)
	}

	w = httptest.NewRecorder()
	repman.handlerMuxCloud18SSOCallback(w, httptest.NewRequest(http.MethodGet, config.Cloud18SSOCallbackPath+"?"+back.RawQuery, nil))
	login, _ := url.Parse(w.Header().Get("Location"))
	code := login.Query().Get("cloud18")
	if w.Code != http.StatusFound || login.Path != "/login" || code == "" || strings.Contains(login.String(), "eyJ") {
		t.Fatalf("callback: %d %s (a one-time code, never a token)", w.Code, login)
	}

	w = httptest.NewRecorder()
	repman.handlerMuxCloud18SSOCallback(w, httptest.NewRequest(http.MethodGet, config.Cloud18SSOCallbackPath+"?"+back.RawQuery, nil))
	if !strings.Contains(w.Header().Get("Location"), "cloud18_error=") {
		t.Fatalf("the same state twice must fail: %s", w.Header().Get("Location"))
	}

	w = httptest.NewRecorder()
	repman.handlerMuxCloud18SSOSession(w, httptest.NewRequest(http.MethodPost, "/api/auth/cloud18/session", strings.NewReader(`{"code":"`+code+`"}`)))
	var s struct{ Token, Username, LoginMethod string }
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &s) != nil || s.Token == "" || s.Username != "person@partner.example" || s.LoginMethod != "cloud18" {
		t.Fatalf("session: %d %s", w.Code, w.Body.String())
	}
	r := httptest.NewRequest(http.MethodGet, "/api/clusters", nil)
	r.Header.Set("Authorization", "Bearer "+s.Token)
	if user := repman.GetUserFromRequest(r); user != "person@partner.example" {
		t.Fatalf("the session is the SSO login of the email: %q", user)
	}
	w = httptest.NewRecorder()
	repman.handlerMuxCloud18SSOSession(w, httptest.NewRequest(http.MethodPost, "/api/auth/cloud18/session", strings.NewReader(`{"code":"`+code+`"}`)))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("a one-time code is used once: %d", w.Code)
	}

	w = httptest.NewRecorder()
	repman.handlerMuxCloud18SSOLogout(w, httptest.NewRequest(http.MethodGet, "/api/auth/cloud18/logout", nil))
	out, _ := url.Parse(w.Header().Get("Location"))
	if !strings.HasPrefix(out.String(), issuer+"/protocol/openid-connect/logout?") || out.Query().Get("post_logout_redirect_uri") != public+"/login" {
		t.Fatalf("logout: %s", out)
	}
	_ = f
}

// #2185: a callback with a state this repman never started, a refused sign-in, an expired code: the
// browser goes back to the login page with a reason, no session.
func TestCloud18SSORefusals(t *testing.T) {
	_, issuer := newRegisterFakes(t)
	repman := ssoRepman(t, issuer)
	w := httptest.NewRecorder()
	repman.handlerMuxCloud18SSOCallback(w, httptest.NewRequest(http.MethodGet, config.Cloud18SSOCallbackPath+"?code=x&state=forged", nil))
	if w.Code != http.StatusFound || !strings.Contains(w.Header().Get("Location"), "/login?cloud18_error=") {
		t.Fatalf("an unknown state: %d %s", w.Code, w.Header().Get("Location"))
	}

	w = httptest.NewRecorder()
	repman.handlerMuxCloud18SSOStart(w, httptest.NewRequest(http.MethodGet, "/api/auth/cloud18/start", nil))
	realmURL, _ := url.Parse(w.Header().Get("Location"))
	w = httptest.NewRecorder()
	repman.handlerMuxCloud18SSOCallback(w, httptest.NewRequest(http.MethodGet, config.Cloud18SSOCallbackPath+"?error=access_denied&state="+realmURL.Query().Get("state"), nil))
	if !strings.Contains(w.Header().Get("Location"), "cloud18_error=") {
		t.Fatalf("a refused sign-in: %s", w.Header().Get("Location"))
	}

	cloud18SSO.Lock()
	cloud18SSO.codes["old"] = cloud18SSOCode{jwt: "x", user: "u", expires: time.Now().Add(-time.Second)}
	cloud18SSO.Unlock()
	w = httptest.NewRecorder()
	repman.handlerMuxCloud18SSOSession(w, httptest.NewRequest(http.MethodPost, "/api/auth/cloud18/session", strings.NewReader(`{"code":"old"}`)))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("an expired code: %d", w.Code)
	}

	off := ssoRepman(t, issuer)
	off.Conf.Cloud18SubscriptionPlan = "free"
	w = httptest.NewRecorder()
	off.handlerMuxCloud18SSOStart(w, httptest.NewRequest(http.MethodGet, "/api/auth/cloud18/start", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("not offered: %d", w.Code)
	}
}
