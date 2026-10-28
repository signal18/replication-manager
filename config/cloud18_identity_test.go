// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testToken(payload string) string {
	return "h." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".s"
}

var testEncryptionKey = []byte("0123456789abcdef0123456789abcdef")

// identityConf: an instance of its own working directory, with an encryption key, set to realm
func identityConf(t *testing.T, realm string) *Config {
	t.Helper()
	return &Config{Cloud18IdentityURL: realm, WorkingDir: t.TempDir(), SecretKey: testEncryptionKey}
}

// connectedConf: an instance someone connected to realm (its login file written)
func connectedConf(t *testing.T, realm, refreshToken string) *Config {
	t.Helper()
	conf := identityConf(t, realm)
	l := cloud18Login{Issuer: strings.TrimRight(realm, "/"), RefreshToken: refreshToken, User: "acme", Sub: "82", ConnectedAt: time.Now()}
	if err := conf.writeCloud18Login(l); err != nil {
		t.Fatal(err)
	}
	return conf
}

// forgetCloud18Session drops the instance's session from memory: the next call reads the file, as after a restart
func forgetCloud18Session(conf *Config) {
	cloud18Sessions.Lock()
	delete(cloud18Sessions.m, conf.cloud18IdentityPath())
	cloud18Sessions.Unlock()
	cacheCloud18IdentityToken(conf.cloud18IdentityPath(), cloud18IdentityToken{})
}

// refreshRealm answers the refresh_token grant of the public client for refreshToken; anything
// else is refused. answer is the JSON answered.
func refreshRealm(t *testing.T, refreshToken string, answer func() string, calls *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			calls.Add(1)
		}
		r.ParseForm()
		if r.URL.Path != "/realms/cloud18/protocol/openid-connect/token" || r.Form.Get("grant_type") != "refresh_token" ||
			r.Form.Get("client_id") != ConstCloud18IdentityClientID || r.Form.Get("refresh_token") != refreshToken {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid_grant","error_description":"Offline session not active"}`)
			return
		}
		if r.Form.Get("client_secret") != "" || r.Form.Get("password") != "" {
			t.Error("no secret and no password may be sent")
		}
		fmt.Fprint(w, answer())
	}))
}

func accessAnswer(sub string, expiresIn int) string {
	return fmt.Sprintf(`{"access_token":%q,"expires_in":%d}`, testToken(`{"sub":"`+sub+`","preferred_username":"acme","aud":["cloud18-storage"]}`), expiresIn)
}

// registeredConf: an instance registered to Cloud18 with user and password, set to realm, no login kept
func registeredConf(t *testing.T, realm, user, password string) *Config {
	t.Helper()
	conf := identityConf(t, realm)
	conf.Cloud18, conf.Cloud18GitUser, conf.Cloud18GitPassword = true, user, password
	return conf
}

// passwordRealm answers the password grant of the public client for user/password (offline
// token offline-1), the refresh grant for the tokens it gave; anything else is refused.
func passwordRealm(t *testing.T, user, password *string, calls *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		r.ParseForm()
		ok := r.URL.Path == "/realms/cloud18/protocol/openid-connect/token" && r.Form.Get("client_id") == ConstCloud18IdentityClientID && r.Form.Get("client_secret") == ""
		switch {
		case ok && r.Form.Get("grant_type") == "password" && r.Form.Get("username") == *user && r.Form.Get("password") == *password &&
			strings.Contains(r.Form.Get("scope"), "offline_access"):
			fmt.Fprintf(w, `{"access_token":%q,"expires_in":300,"refresh_token":"offline-1"}`, testToken(`{"sub":"82","preferred_username":"acme"}`))
		case ok && r.Form.Get("grant_type") == "refresh_token" && r.Form.Get("refresh_token") == "offline-1" && r.Form.Get("password") == "":
			fmt.Fprint(w, accessAnswer("82", 300))
		default:
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":"invalid_grant","error_description":"Invalid user credentials"}`)
		}
	}))
}

// #2225: a registered instance signs in with its registered account (the password grant of the
// public client, offline_access) and keeps the login: later tokens come from the refresh token,
// never the password again; the file survives a restart.
func TestCloud18RegisteredLogin(t *testing.T) {
	var calls atomic.Int32
	user, password := "acme@example.com", "s3cret"
	realm := passwordRealm(t, &user, &password, &calls)
	defer realm.Close()
	conf := registeredConf(t, realm.URL+"/realms/cloud18", user, password)
	if !conf.HasCloud18Login() || conf.Cloud18IdentityStatus().State != "registered" {
		t.Fatalf("a registered instance can have its login: %+v", conf.Cloud18IdentityStatus())
	}
	token, sub, err := conf.Cloud18IdentityToken()
	if err != nil || sub != "82" || token == "" {
		t.Fatalf("sign-in with the registered account: %q %v", sub, err)
	}
	if st := conf.Cloud18IdentityStatus(); st.State != "connected" || st.User != "acme" {
		t.Errorf("after the sign-in: %+v", st)
	}
	data, _ := os.ReadFile(conf.cloud18IdentityPath())
	if strings.Contains(string(data), password) || strings.Contains(string(data), "offline-1") {
		t.Error("the login file holds the password or the refresh token in clear")
	}
	forgetCloud18Session(conf) // a restart: the kept login, refreshed, no password
	password = "changed"
	if _, sub, err := conf.Cloud18IdentityToken(); err != nil || sub != "82" {
		t.Fatalf("the kept login after a restart: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("one sign-in then one refresh, got %d requests", calls.Load())
	}
}

// #2225: an account the realm refuses is not asked again until the registration changes, the
// status says to register again; a realm out of reach is tried again; a login the realm ends
// signs in again with the registered account; an instance not registered has no login.
func TestCloud18RegisteredLoginRefused(t *testing.T) {
	var calls atomic.Int32
	user, password := "acme@example.com", "right"
	realm := passwordRealm(t, &user, &password, &calls)
	defer realm.Close()
	issuer := realm.URL + "/realms/cloud18"

	conf := registeredConf(t, issuer, user, "wrong")
	if _, _, err := conf.Cloud18IdentityToken(); !errors.Is(err, ErrCloud18Refused) {
		t.Fatalf("a refused account: %v", err)
	}
	conf.Cloud18IdentityToken()
	conf.Cloud18IdentityKeepAlive()
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 1 {
		t.Errorf("a refused account is asked once: %d requests", calls.Load())
	}
	if st := conf.Cloud18IdentityStatus(); st.State != "refused" || !strings.Contains(st.Reason, "Invalid user credentials") || conf.HasCloud18Login() {
		t.Errorf("a refused account: %+v", st)
	}
	conf.Cloud18GitPassword = "right" // registered again
	if _, _, err := conf.Cloud18IdentityToken(); err != nil || conf.Cloud18IdentityStatus().State != "connected" {
		t.Fatalf("a new registration signs in: %v", err)
	}

	ended := registeredConf(t, issuer, user, password)
	l := cloud18Login{Issuer: issuer, RefreshToken: "revoked", User: "acme", Sub: "82", ConnectedAt: time.Now()}
	if err := ended.writeCloud18Login(l); err != nil {
		t.Fatal(err)
	}
	if _, sub, err := ended.Cloud18IdentityToken(); err != nil || sub != "82" {
		t.Fatalf("a login the realm ended signs in again with the registered account: %v", err)
	}

	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	offline := registeredConf(t, down.URL+"/realms/cloud18", user, password)
	if _, _, err := offline.Cloud18IdentityToken(); err == nil || errors.Is(err, ErrCloud18Refused) {
		t.Errorf("a realm out of reach is transient: %v", err)
	}
	if st := offline.Cloud18IdentityStatus(); st.State != "registered" {
		t.Errorf("a realm out of reach refuses nothing: %+v", st)
	}
	// the realm out of reach is not asked again at each use, only after cloud18RegisteredRetry
	if _, _, err := offline.Cloud18IdentityToken(); err == nil || !strings.Contains(err.Error(), "asked again in") {
		t.Errorf("a second use right after: %v", err)
	}

	// registered by a release that keeps no password: the registration makes the login
	noPassword := registeredConf(t, issuer, user, "")
	if st := noPassword.Cloud18IdentityStatus(); st.State != "refused" || st.Reason != cloud18NoLoginKept {
		t.Errorf("registered without a login: %+v", st)
	}
	if _, _, err := noPassword.Cloud18IdentityToken(); !errors.Is(err, ErrCloud18Refused) {
		t.Errorf("registered without a login: %v", err)
	}
	if err := noPassword.KeepCloud18Login(user, password); err != nil {
		t.Fatalf("the registration's sign-in: %v", err)
	}
	if st := noPassword.Cloud18IdentityStatus(); st.State != "connected" || !noPassword.HasCloud18Login() {
		t.Errorf("after the registration's sign-in: %+v", st)
	}
	if strings.Contains(noPassword.Cloud18GitPassword, password) {
		t.Error("the registration's password is kept")
	}

	notRegistered := identityConf(t, issuer)
	if _, _, err := notRegistered.Cloud18IdentityToken(); !errors.Is(err, ErrCloud18IdentityNotSet) || notRegistered.HasCloud18Login() {
		t.Errorf("not registered: %v", err)
	}
	if st := notRegistered.Cloud18IdentityStatus(); st.State != "none" {
		t.Errorf("not registered: %+v", st)
	}
}

// reconnect, not asked again; a realm out of reach or failing is transient and keeps it
func TestCloud18IdentityRefusedOrTransient(t *testing.T) {
	for _, c := range []struct {
		code    int
		refused bool
	}{{400, true}, {401, true}, {403, true}, {429, false}, {503, false}, {500, false}} {
		var calls atomic.Int32
		realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(c.code)
			fmt.Fprint(w, `{"error":"invalid_grant","error_description":"Offline session not active"}`)
		}))
		conf := connectedConf(t, realm.URL+"/realms/cloud18", "offline-1")
		_, _, err := conf.Cloud18IdentityToken()
		if err == nil || errors.Is(err, ErrCloud18Refused) != c.refused {
			t.Errorf("realm %d: refused=%v, want %v (%v)", c.code, errors.Is(err, ErrCloud18Refused), c.refused, err)
		}
		st := conf.Cloud18IdentityStatus()
		if c.refused && (st.State != "refused" || !strings.Contains(st.Reason, "Offline session not active")) {
			t.Errorf("realm %d: %+v", c.code, st)
		}
		if !c.refused && st.State != "connected" {
			t.Errorf("realm %d: a transient failure ends nothing: %+v", c.code, st)
		}
		conf.Cloud18IdentityToken()
		if c.refused && calls.Load() != 1 {
			t.Errorf("realm %d: an ended login is not asked again (%d requests)", c.code, calls.Load())
		}
		realm.Close()
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	if _, _, err := connectedConf(t, closed.URL, "x").Cloud18IdentityToken(); err == nil || errors.Is(err, ErrCloud18Refused) {
		t.Errorf("a realm out of reach is transient: %v", err)
	}
	if _, _, err := (&Config{}).Cloud18IdentityToken(); !errors.Is(err, ErrCloud18Refused) {
		t.Errorf("no realm is a refusal: %v", err)
	}
}

// #2159: the login goes to an https realm only (http to the loopback for tests); a login of
// another realm is not used; a token needs a lifetime, kept 24 h at most; a rotated refresh
// token is kept (also across a restart); the end the realm tells is shown
func TestCloud18IdentityChecks(t *testing.T) {
	for _, u := range []string{"http://identity.example.com/realms/cloud18", "ftp://identity.example.com", "identity.example.com"} {
		if _, _, err := connectedConf(t, u, "x").Cloud18IdentityToken(); !errors.Is(err, ErrCloud18Refused) {
			t.Errorf("%s: %v", u, err)
		}
	}

	var calls atomic.Int32
	answer := accessAnswer("7", 0)
	realm := refreshRealm(t, "offline-1", func() string { return answer }, &calls)
	defer realm.Close()
	issuer := realm.URL + "/realms/cloud18"

	moved := connectedConf(t, issuer, "offline-1")
	moved.Cloud18IdentityURL = realm.URL + "/realms/other"
	if _, _, err := moved.Cloud18IdentityToken(); !errors.Is(err, ErrCloud18IdentityNotSet) || calls.Load() != 0 {
		t.Errorf("a login of another realm: %v, %d calls", err, calls.Load())
	}

	if _, _, err := connectedConf(t, issuer, "offline-1").Cloud18IdentityToken(); err == nil {
		t.Error("a token without lifetime must be refused")
	}
	answer = accessAnswer("7", 10*365*24*3600)
	conf := connectedConf(t, issuer, "offline-1")
	if _, _, err := conf.Cloud18IdentityToken(); err != nil {
		t.Fatal(err)
	}
	cloud18IdentityTokens.Lock()
	got := cloud18IdentityTokens.m[conf.cloud18IdentityPath()].expires
	cloud18IdentityTokens.Unlock()
	if time.Until(got) > cloud18IdentityMaxLifetime {
		t.Errorf("lifetime not clamped: %s", time.Until(got))
	}

	rotating := refreshRealm(t, "offline-1", func() string {
		return fmt.Sprintf(`{"access_token":%q,"expires_in":300,"refresh_token":"offline-2","refresh_expires_in":%d}`, testToken(`{"sub":"7"}`), 3*24*3600)
	}, nil)
	defer rotating.Close()
	conf = connectedConf(t, rotating.URL+"/realms/cloud18", "offline-1")
	if _, _, err := conf.Cloud18IdentityToken(); err != nil {
		t.Fatal(err)
	}
	if st := conf.Cloud18IdentityStatus(); st.State != "connected" || st.Expires == nil {
		t.Errorf("a login whose end the realm tells: %+v", st)
	}
	forgetCloud18Session(conf)
	cloud18Sessions.Lock()
	kept := conf.cloud18SessionLocked().login.RefreshToken
	cloud18Sessions.Unlock()
	if kept != "offline-2" {
		t.Errorf("the rotated refresh token is not kept: %q", kept)
	}
}

// #2159: concurrent callers of one instance make one request; a slow realm does not hold up
// another instance
func TestCloud18IdentityConcurrent(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-release
		fmt.Fprint(w, accessAnswer("9", 300))
	}))
	defer slow.Close()
	fast := refreshRealm(t, "offline-fast", func() string { return accessAnswer("10", 300) }, nil)
	defer fast.Close()
	slowConf := connectedConf(t, slow.URL+"/realms/cloud18", "offline-slow")
	fastConf := connectedConf(t, fast.URL+"/realms/cloud18", "offline-fast")

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, sub, err := slowConf.Cloud18IdentityToken()
			if err == nil && sub != "9" {
				err = fmt.Errorf("sub %q", sub)
			}
			errs <- err
		}()
	}
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	if _, _, err := fastConf.Cloud18IdentityToken(); err != nil || time.Since(start) > 2*time.Second {
		t.Errorf("another instance waited for the slow realm: %s %v", time.Since(start), err)
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("%d requests for one instance, want 1", calls.Load())
	}
}

// #2159: keep-alive refreshes a login nothing used for a day; a restore of the working directory
// keeps the file
func TestCloud18IdentityKeepAlive(t *testing.T) {
	var refreshes atomic.Int32
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshes.Add(1)
		fmt.Fprint(w, accessAnswer("82", 300))
	}))
	defer realm.Close()
	conf := connectedConf(t, realm.URL+"/realms/cloud18", "offline-1")

	conf.Cloud18IdentityKeepAlive()
	for i := 0; i < 200 && refreshes.Load() == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if refreshes.Load() != 1 {
		t.Fatalf("keep-alive of a login never refreshed: %d", refreshes.Load())
	}
	for i := 0; i < 200; i++ {
		cloud18Sessions.Lock()
		s := conf.cloud18SessionLocked()
		done := !s.keepAlive.Load()
		cloud18Sessions.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	conf.Cloud18IdentityKeepAlive()
	time.Sleep(50 * time.Millisecond)
	if refreshes.Load() != 1 {
		t.Errorf("a login refreshed now is not refreshed again: %d", refreshes.Load())
	}

	restore := conf.KeepCloud18LoginFile()
	os.RemoveAll(conf.WorkingDir)
	restore()
	forgetCloud18Session(conf)
	if !conf.HasCloud18Login() {
		t.Error("the login file is not kept across a restore")
	}
}

// #2159: the folder is the token's permanent sub, never a username, never a path
func TestTokenSubject(t *testing.T) {
	if u, err := TokenSubject(testToken(`{"sub":"42","preferred_username":"acme"}`)); err != nil || u != "42" {
		t.Fatalf("user %q %v", u, err)
	}
	for _, bad := range []string{"x.y", testToken(`{}`), testToken(`{"preferred_username":"acme"}`), testToken(`{"sub":"a/b"}`), testToken(`{"sub":"."}`), testToken(`{"sub":".."}`)} {
		if _, err := TokenSubject(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

// #2159: vault-auth = jwt logs in at auth/jwt/login with the identity token, the role being
// vault-role-id or else the realm's client id, and keeps the vault's token
func TestVaultJWTLogin(t *testing.T) {
	realm := refreshRealm(t, "offline-v", func() string { return accessAnswer("2", 300) }, nil)
	defer realm.Close()
	var gotRole, gotJWT string
	vault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/jwt/login" {
			http.NotFound(w, r)
			return
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		gotRole, gotJWT = body["role"], body["jwt"]
		fmt.Fprint(w, `{"auth":{"client_token":"vault-token"}}`)
	}))
	defer vault.Close()

	conf := connectedConf(t, realm.URL+"/realms/cloud18", "offline-v")
	conf.VaultServerAddr, conf.VaultAuth = vault.URL, "jwt"
	client, err := conf.GetVaultConnection()
	if err != nil {
		t.Fatal(err)
	}
	if client.Token() != "vault-token" || gotRole != ConstCloud18IdentityClientID || gotJWT == "" {
		t.Fatalf("token %q role %q jwt set %v", client.Token(), gotRole, gotJWT != "")
	}
}
