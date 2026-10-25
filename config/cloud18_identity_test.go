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
	"path/filepath"
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

// #2159: a person connects the instance with the device grant of the public client (no secret,
// no redirect): repman shows the code and the realm's page, polls until approved, keeps the
// offline token encrypted in a 0600 file, and gets access tokens with it
func TestCloud18DeviceLogin(t *testing.T) {
	defer func(d time.Duration) { cloud18DeviceMinInterval = d }(cloud18DeviceMinInterval)
	cloud18DeviceMinInterval = 10 * time.Millisecond
	var polls, refreshes atomic.Int32
	var realmURL string
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("client_id") != ConstCloud18IdentityClientID || r.Form.Get("client_secret") != "" || r.Form.Get("redirect_uri") != "" {
			t.Errorf("public client only: %v", r.Form)
		}
		switch {
		case r.URL.Path == "/realms/cloud18/protocol/openid-connect/auth/device":
			if !strings.Contains(r.Form.Get("scope"), "offline_access") {
				t.Errorf("scope %q", r.Form.Get("scope"))
			}
			fmt.Fprintf(w, `{"device_code":"dev-1","user_code":"WDJB-MJHT","verification_uri":%q,"verification_uri_complete":%q,"expires_in":600,"interval":0}`,
				realmURL+"/realms/cloud18/device", realmURL+"/realms/cloud18/device?user_code=WDJB-MJHT")
		case r.Form.Get("grant_type") == "urn:ietf:params:oauth:grant-type:device_code" && r.Form.Get("device_code") == "dev-1":
			if polls.Add(1) < 3 {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":"authorization_pending"}`)
				return
			}
			fmt.Fprintf(w, `{"access_token":%q,"expires_in":300,"refresh_token":"offline-1","refresh_expires_in":0}`, testToken(`{"sub":"82","preferred_username":"acme"}`))
		case r.Form.Get("grant_type") == "refresh_token" && r.Form.Get("refresh_token") == "offline-1":
			refreshes.Add(1)
			fmt.Fprint(w, accessAnswer("82", 300))
		default:
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid_grant"}`)
		}
	}))
	defer realm.Close()
	realmURL = realm.URL

	conf := identityConf(t, realm.URL+"/realms/cloud18")
	if st := conf.Cloud18IdentityStatus(); st.State != "none" || conf.HasCloud18Login() {
		t.Fatalf("before: %+v", st)
	}
	if _, _, err := conf.Cloud18IdentityToken(); !errors.Is(err, ErrCloud18IdentityNotSet) || !errors.Is(err, ErrCloud18Refused) {
		t.Fatalf("not connected: %v", err)
	}
	d, err := conf.StartCloud18DeviceLogin()
	if err != nil || d.UserCode != "WDJB-MJHT" || !strings.HasSuffix(d.VerificationURI, "/realms/cloud18/device") {
		t.Fatalf("device %+v %v", d, err)
	}
	if st := conf.Cloud18IdentityStatus(); st.State != "pending" || st.Device == nil || st.Device.UserCode != "WDJB-MJHT" {
		t.Fatalf("pending: %+v", st)
	}
	var st Cloud18IdentityStatus
	for i := 0; i < 200; i++ {
		if st = conf.Cloud18IdentityStatus(); st.State != "pending" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st.State != "connected" || st.User != "acme" || st.Sub != "82" || st.Expires != nil {
		t.Fatalf("after approval: %+v", st)
	}

	info, err := os.Stat(conf.cloud18IdentityPath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("login file %v %v", info, err)
	}
	data, _ := os.ReadFile(conf.cloud18IdentityPath())
	if strings.Contains(string(data), "offline-1") || !strings.Contains(string(data), `"refresh_token": "hash_`) {
		t.Fatalf("the offline token must be kept encrypted: %s", data)
	}

	if _, sub, err := conf.Cloud18IdentityToken(); err != nil || sub != "82" || refreshes.Load() != 0 {
		t.Fatalf("the approval's token is used first: %q %v %d", sub, err, refreshes.Load())
	}
	forgetCloud18Session(conf) // a restart
	if _, sub, err := conf.Cloud18IdentityToken(); err != nil || sub != "82" || refreshes.Load() != 1 {
		t.Fatalf("after a restart the kept login gives the token: %q %v %d", sub, err, refreshes.Load())
	}
}

// #2159: a login the person refuses or lets expire keeps nothing and says why; a device page
// outside the realm is refused; no encryption key, no login (never kept in clear)
func TestCloud18DeviceLoginRefused(t *testing.T) {
	defer func(d time.Duration) { cloud18DeviceMinInterval = d }(cloud18DeviceMinInterval)
	cloud18DeviceMinInterval = 10 * time.Millisecond
	page := ""
	answer := `{"error":"access_denied"}`
	var realmURL string
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/auth/device") {
			p := page
			if p == "" {
				p = realmURL + "/realms/cloud18/device"
			}
			fmt.Fprintf(w, `{"device_code":"d","user_code":"C","verification_uri":%q,"expires_in":600}`, p)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, answer)
	}))
	defer realm.Close()
	realmURL = realm.URL
	issuer := realm.URL + "/realms/cloud18"

	for _, c := range []struct{ answer, reason string }{
		{`{"error":"access_denied"}`, "the login was refused"},
		{`{"error":"expired_token"}`, "the code expired before it was approved"},
	} {
		answer = c.answer
		conf := identityConf(t, issuer)
		if _, err := conf.StartCloud18DeviceLogin(); err != nil {
			t.Fatal(err)
		}
		var st Cloud18IdentityStatus
		for i := 0; i < 200; i++ {
			if st = conf.Cloud18IdentityStatus(); st.State != "pending" {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if st.State != "none" || st.Reason != c.reason {
			t.Errorf("%s: %+v", c.answer, st)
		}
		if _, err := os.Stat(conf.cloud18IdentityPath()); !os.IsNotExist(err) {
			t.Errorf("%s: a login file was written", c.answer)
		}
	}

	page = "https://phishing.example.com/device"
	if _, err := identityConf(t, issuer).StartCloud18DeviceLogin(); !errors.Is(err, ErrCloud18Refused) {
		t.Errorf("a page outside the realm: %v", err)
	}
	page = ""
	noKey := &Config{Cloud18IdentityURL: issuer, WorkingDir: t.TempDir(), MonitoringKeyPath: filepath.Join(t.TempDir(), "none.key")}
	if _, err := noKey.StartCloud18DeviceLogin(); !errors.Is(err, ErrCloud18Refused) {
		t.Errorf("no encryption key: %v", err)
	}
	if _, err := identityConf(t, "http://identity.example.com/realms/cloud18").StartCloud18DeviceLogin(); !errors.Is(err, ErrCloud18Refused) {
		t.Errorf("a realm in clear: %v", err)
	}
}

// #2159: a realm refusing the kept login (revoked, ended) ends it: refused, reported to
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
		if c.refused && (st.State != "reconnect" || !strings.Contains(st.Reason, "Offline session not active")) {
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
// token is kept (also across a restart); a login the realm says ends soon is reported
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
	if st := conf.Cloud18IdentityStatus(); st.State != "reconnect" || st.Expires == nil || !strings.Contains(st.Reason, "ends on") {
		t.Errorf("a login ending in 3 days: %+v", st)
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

// #2159: disconnect forgets the login (file, token) and ends it at the realm; keep-alive
// refreshes a login nothing used for a day; a restore of the working directory keeps the file
func TestCloud18IdentityDisconnectKeepAlive(t *testing.T) {
	var revoked string
	var refreshes atomic.Int32
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if strings.HasSuffix(r.URL.Path, "/revoke") {
			revoked = r.Form.Get("token")
			return
		}
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

	if err := conf.DisconnectCloud18Identity(); err != nil {
		t.Fatal(err)
	}
	if revoked != "offline-1" {
		t.Errorf("revoked %q", revoked)
	}
	if _, err := os.Stat(conf.cloud18IdentityPath()); !os.IsNotExist(err) {
		t.Error("the login file is still there")
	}
	if st := conf.Cloud18IdentityStatus(); st.State != "none" || conf.HasCloud18Login() {
		t.Errorf("after disconnect: %+v", st)
	}
	if _, _, err := conf.Cloud18IdentityToken(); !errors.Is(err, ErrCloud18IdentityNotSet) {
		t.Errorf("after disconnect: %v", err)
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

// #2159: the GitLab token of a new version comes from the Cloud18 CRM, for the identity token, with
// the instance's token name; no GitLab password is sent anywhere
func TestCloud18GitLabToken(t *testing.T) {
	identity := testToken(`{"sub":"2","azp":"replication-manager"}`)
	realm := refreshRealm(t, "offline-g", func() string { return fmt.Sprintf(`{"access_token":%q,"expires_in":300}`, identity) }, nil)
	defer realm.Close()
	var gotAuth, gotName string
	crm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/instances/gitlab-token" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		gotName = body["token_name"]
		fmt.Fprint(w, `{"token":"glpat-x","expires_at":"2026-10-31","user_id":2,"username":"svaroqui"}`)
	}))
	defer crm.Close()

	conf := connectedConf(t, realm.URL+"/realms/cloud18", "offline-g")
	conf.Cloud18CrmApiUrl = crm.URL + "/"
	g, err := conf.Cloud18GitLabToken("signal18-dev3-ovh2-1")
	if err != nil || g.Token != "glpat-x" || g.UserID != 2 || g.Username != "svaroqui" {
		t.Fatalf("grant %+v err %v", g, err)
	}
	if gotAuth != "Bearer "+identity || gotName != "signal18-dev3-ovh2-1" {
		t.Fatalf("auth %q name %q", gotAuth, gotName)
	}

	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"authorization_required"}`)
	}))
	defer refused.Close()
	conf.Cloud18CrmApiUrl = refused.URL
	if _, err := conf.Cloud18GitLabToken("n"); err == nil {
		t.Fatal("a refused identity gets no token")
	}
}

// #2159 review: the CRM's GitLab token requests are serialized and one token is handed to
// concurrent callers (a second request would revoke the first); 5xx transient, 4xx refused,
// error text cut; the CRM URL https; a grant without token or user refused
func TestCloud18GitLabTokenChecks(t *testing.T) {
	realm := refreshRealm(t, "offline-c", func() string { return accessAnswer("2", 300) }, nil)
	defer realm.Close()
	var calls atomic.Int32
	answer := `{"token":"glpat-1","user_id":2,"username":"u"}`
	code := 200
	crm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(code)
		fmt.Fprint(w, answer)
	}))
	defer crm.Close()
	conf := connectedConf(t, realm.URL+"/realms/cloud18", "offline-c")

	conf.Cloud18CrmApiUrl = crm.URL
	var wg sync.WaitGroup
	tokens := make(chan string, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, err := conf.Cloud18GitLabToken("inst-1")
			if err != nil {
				t.Error(err)
			}
			tokens <- g.Token
		}()
	}
	wg.Wait()
	close(tokens)
	for tok := range tokens {
		if tok != "glpat-1" {
			t.Errorf("token %q", tok)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("%d CRM requests, want 1", calls.Load())
	}

	for _, c := range []struct {
		code    int
		answer  string
		refused bool
	}{{503, `{"error":"down"}`, false}, {401, `{"error":"` + strings.Repeat("x", 5000) + `"}`, true}} {
		code, answer = c.code, c.answer
		_, err := conf.Cloud18GitLabToken(fmt.Sprintf("inst-%d", c.code))
		if err == nil || errors.Is(err, ErrCloud18Refused) != c.refused {
			t.Errorf("CRM %d: %v", c.code, err)
		}
		if err != nil && len(err.Error()) > 400 {
			t.Errorf("CRM error text not cut: %d bytes", len(err.Error()))
		}
	}
	code = 200
	for _, bad := range []string{`{"user_id":2}`, `{"token":"glpat-2","user_id":0}`} {
		answer = bad
		if _, err := conf.Cloud18GitLabToken("inst-" + bad); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
	conf.Cloud18CrmApiUrl = "http://crm.example.com"
	if _, err := conf.Cloud18GitLabToken("inst-http"); !errors.Is(err, ErrCloud18Refused) {
		t.Errorf("a CRM in clear: %v", err)
	}
}
