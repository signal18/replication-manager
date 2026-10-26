// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"fmt"
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
	_, issuer := newRegisterFakes(t)
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
	var browser *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == cloud18SSOCookie {
			browser = c
		}
	}
	if browser == nil || !browser.HttpOnly || !browser.Secure || browser.SameSite != http.SameSiteLaxMode {
		t.Fatalf("start must tie the sign-in to this browser with an HttpOnly, Secure, Lax cookie: %+v", browser)
	}
	callback := func(rawQuery string, c *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, config.Cloud18SSOCallbackPath+"?"+rawQuery, nil)
		if c != nil {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		repman.handlerMuxCloud18SSOCallback(rec, req)
		return rec
	}

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

	w = callback(back.RawQuery, browser)
	login, _ := url.Parse(w.Header().Get("Location"))
	code := login.Query().Get("cloud18")
	if w.Code != http.StatusFound || login.Path != "/login" || code == "" || strings.Contains(login.String(), "eyJ") {
		t.Fatalf("callback: %d %s (a one-time code, never a token)", w.Code, login)
	}

	w = callback(back.RawQuery, browser)
	if !strings.Contains(w.Header().Get("Location"), "cloud18_error=expired") {
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

// #2185 review: a sign-in is tied to the browser that started it (login CSRF): the callback URL of
// someone's sign-in, opened in another browser, opens no session; a valid state with a bad code is
// refused; the reasons shown are the fixed ones.
func TestCloud18SSOOtherBrowser(t *testing.T) {
	_, issuer := newRegisterFakes(t)
	repman := ssoRepman(t, issuer)
	start := func() (string, *http.Cookie) {
		w := httptest.NewRecorder()
		repman.handlerMuxCloud18SSOStart(w, httptest.NewRequest(http.MethodGet, "/api/auth/cloud18/start", nil))
		u, _ := url.Parse(w.Header().Get("Location"))
		for _, c := range w.Result().Cookies() {
			if c.Name == cloud18SSOCookie {
				return u.Query().Get("state"), c
			}
		}
		t.Fatal("no browser cookie")
		return "", nil
	}
	state, _ := start()
	w := httptest.NewRecorder()
	repman.handlerMuxCloud18SSOCallback(w, httptest.NewRequest(http.MethodGet, config.Cloud18SSOCallbackPath+"?code=kc-code-x&state="+state, nil))
	if !strings.Contains(w.Header().Get("Location"), "cloud18_error=expired") {
		t.Fatalf("another browser (no cookie): %s", w.Header().Get("Location"))
	}
	state, mine := start()
	other := &http.Cookie{Name: cloud18SSOCookie, Value: "someone-else"}
	req := httptest.NewRequest(http.MethodGet, config.Cloud18SSOCallbackPath+"?code=kc-code-x&state="+state, nil)
	req.AddCookie(other)
	w = httptest.NewRecorder()
	repman.handlerMuxCloud18SSOCallback(w, req)
	if !strings.Contains(w.Header().Get("Location"), "cloud18_error=expired") {
		t.Fatalf("another browser's cookie: %s", w.Header().Get("Location"))
	}
	// someone's valid sign-in cookie on another sign-in's callback
	stateA, cookieA := start()
	_, cookieB := start()
	req = httptest.NewRequest(http.MethodGet, config.Cloud18SSOCallbackPath+"?code=kc-code-x&state="+stateA, nil)
	req.AddCookie(cookieB)
	w = httptest.NewRecorder()
	repman.handlerMuxCloud18SSOCallback(w, req)
	if !strings.Contains(w.Header().Get("Location"), "cloud18_error=expired") {
		t.Fatalf("another valid sign-in's cookie: %s", w.Header().Get("Location"))
	}
	_, _ = mine, cookieA

	state, mine = start()
	req = httptest.NewRequest(http.MethodGet, config.Cloud18SSOCallbackPath+"?code=not-a-code&state="+state, nil)
	req.AddCookie(mine)
	w = httptest.NewRecorder()
	repman.handlerMuxCloud18SSOCallback(w, req)
	if !strings.Contains(w.Header().Get("Location"), "cloud18_error=refused") {
		t.Fatalf("a valid state with a bad code: %s", w.Header().Get("Location"))
	}
}

// #2185 review (F4): sign-ins in progress are never dropped before their end: at the bound a new
// start is refused (busy); the one-time codes keep the newest; /session refuses bad bodies.
func TestCloud18SSOBounds(t *testing.T) {
	_, issuer := newRegisterFakes(t)
	repman := ssoRepman(t, issuer)
	cloud18SSO.Lock()
	saved := cloud18SSO.pending
	cloud18SSO.pending = map[string]cloud18SSOPending{}
	for i := 0; i < cloud18SSOMax; i++ {
		cloud18SSO.pending[fmt.Sprintf("s%d", i)] = cloud18SSOPending{expires: time.Now().Add(time.Minute)}
	}
	cloud18SSO.Unlock()
	w := httptest.NewRecorder()
	repman.handlerMuxCloud18SSOStart(w, httptest.NewRequest(http.MethodGet, "/api/auth/cloud18/start", nil))
	cloud18SSO.Lock()
	kept := len(cloud18SSO.pending)
	cloud18SSO.pending = saved
	cloud18SSO.Unlock()
	if !strings.Contains(w.Header().Get("Location"), "cloud18_error=busy") || kept != cloud18SSOMax {
		t.Fatalf("at the bound: %s, %d sign-ins kept", w.Header().Get("Location"), kept)
	}

	cloud18SSO.Lock()
	cloud18SSO.codes = map[string]cloud18SSOCode{}
	for i := 0; i < cloud18SSOMax; i++ {
		cloud18SSO.codes[fmt.Sprintf("c%d", i)] = cloud18SSOCode{expires: time.Now().Add(time.Duration(i+1) * time.Second)}
	}
	pruneCloud18SSOLocked(time.Now())
	_, oldestKept := cloud18SSO.codes["c0"]
	n := len(cloud18SSO.codes)
	cloud18SSO.codes = map[string]cloud18SSOCode{}
	cloud18SSO.Unlock()
	if n != cloud18SSOMax-1 || oldestKept {
		t.Fatalf("codes at the bound: the oldest goes (%d, oldest kept %v)", n, oldestKept)
	}

	for name, body := range map[string]string{"malformed": "{", "no code": "{}", "oversized": `{"code":"` + strings.Repeat("x", 8192) + `"}`} {
		w = httptest.NewRecorder()
		repman.handlerMuxCloud18SSOSession(w, httptest.NewRequest(http.MethodPost, "/api/auth/cloud18/session", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s body: %d", name, w.Code)
		}
	}
}

// #2185: the login page asks the name first: an internal account (a name that is not an email, a
// local password account, an admin other than the registered user) is signed in with its password
// here; a Cloud18 user goes to the realm; a repman without Sign in with Cloud18 always asks the
// password.
func TestLoginMethod(t *testing.T) {
	_, issuer := newRegisterFakes(t)
	repman := ssoRepman(t, issuer)
	for name, want := range map[string]string{"admin": "password", "system": "password", "": "password",
		"person@partner.example": "cloud18", "owner@partner.example": "cloud18"} {
		if got := repman.loginMethodFor(name); got != want {
			t.Errorf("%q: %s, want %s", name, got, want)
		}
	}
	repman.Conf.Cloud18SubscriptionPlan = "free"
	if repman.loginMethodFor("person@partner.example") != "password" {
		t.Error("a repman without Sign in with Cloud18 asks the password")
	}
	w := httptest.NewRecorder()
	repman.handlerMuxLoginMethod(w, httptest.NewRequest(http.MethodPost, "/api/login/method", strings.NewReader(`{"username":"admin"}`)))
	if !strings.Contains(w.Body.String(), `"method":"password"`) {
		t.Fatalf("endpoint: %s", w.Body.String())
	}
}

// #2185: the silent check (prompt=none) and the email prefill reach the realm; a silent check that
// finds no realm session sends the page back to its form, not as a failure; sign-up goes to the
// realm's sign-up form.
func TestCloud18SSOSilentAndHint(t *testing.T) {
	_, issuer := newRegisterFakes(t)
	repman := ssoRepman(t, issuer)
	start := func(query string) (*url.URL, *http.Cookie) {
		w := httptest.NewRecorder()
		repman.handlerMuxCloud18SSOStart(w, httptest.NewRequest(http.MethodGet, "/api/auth/cloud18/start?"+query, nil))
		u, _ := url.Parse(w.Header().Get("Location"))
		for _, c := range w.Result().Cookies() {
			if c.Name == cloud18SSOCookie {
				return u, c
			}
		}
		return u, nil
	}
	u, _ := start("login_hint=" + url.QueryEscape("person@partner.example"))
	if u.Query().Get("login_hint") != "person@partner.example" || u.Query().Get("prompt") != "" {
		t.Fatalf("sign in with the email prefilled: %s", u)
	}
	u, _ = start("signup=1")
	if !strings.HasSuffix(u.Path, "/protocol/openid-connect/registrations") {
		t.Fatalf("sign up: %s", u)
	}
	u, c := start("silent=1")
	if u.Query().Get("prompt") != "none" {
		t.Fatalf("silent check: %s", u)
	}
	req := httptest.NewRequest(http.MethodGet, config.Cloud18SSOCallbackPath+"?error=login_required&state="+u.Query().Get("state"), nil)
	req.AddCookie(c)
	w := httptest.NewRecorder()
	repman.handlerMuxCloud18SSOCallback(w, req)
	if loc := w.Header().Get("Location"); !strings.HasSuffix(loc, "/login?cloud18_silent=none") {
		t.Fatalf("no realm session on a silent check: back to the form, no error: %s", loc)
	}
	// the same answer to a sign-in the person started is a refusal
	u, c = start("")
	req = httptest.NewRequest(http.MethodGet, config.Cloud18SSOCallbackPath+"?error=login_required&state="+u.Query().Get("state"), nil)
	req.AddCookie(c)
	w = httptest.NewRecorder()
	repman.handlerMuxCloud18SSOCallback(w, req)
	if !strings.Contains(w.Header().Get("Location"), "cloud18_error=refused") {
		t.Fatalf("a refused sign-in: %s", w.Header().Get("Location"))
	}
}
