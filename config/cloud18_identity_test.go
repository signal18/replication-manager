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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testToken(payload string) string {
	return "h." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".s"
}

// #2159: the instance gets its token with the client_credentials grant of its own client, keeps
// it while it is valid, and reads its sub (the folder on the S3 SSO storages)
func TestCloud18IdentityToken(t *testing.T) {
	var calls atomic.Int32
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/realms/cloud18/protocol/openid-connect/token" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		r.ParseForm()
		if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_id") != "repman-acme" || r.Form.Get("client_secret") != "s3cr3t" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Form.Get("password") != "" || r.Form.Get("username") != "" {
			t.Error("no password grant field may be sent")
		}
		fmt.Fprintf(w, `{"access_token":%q,"expires_in":300}`, testToken(`{"sub":"82","aud":["cloud18-storage"]}`))
	}))
	defer realm.Close()

	conf := &Config{Cloud18IdentityURL: realm.URL + "/realms/cloud18/", Cloud18IdentityClientID: "repman-acme"}
	conf.Secrets = NewSecrets(map[string]Secret{"cloud18-identity-client-secret": {Value: "s3cr3t"}})
	tok, sub, err := conf.Cloud18IdentityToken()
	if err != nil || sub != "82" || tok == "" {
		t.Fatalf("token %q sub %q err %v", tok, sub, err)
	}
	if _, _, err := conf.Cloud18IdentityToken(); err != nil || calls.Load() != 1 {
		t.Fatalf("a valid token is reused: calls %d err %v", calls.Load(), err)
	}

	other := &Config{Cloud18IdentityURL: realm.URL + "/realms/cloud18", Cloud18IdentityClientID: "repman-other"}
	other.Secrets = NewSecrets(map[string]Secret{"cloud18-identity-client-secret": {Value: "wrong"}})
	if _, _, err := other.Cloud18IdentityToken(); err == nil {
		t.Fatal("a refused client must fail")
	}

	if _, _, err := (&Config{Cloud18IdentityURL: realm.URL}).Cloud18IdentityToken(); !errors.Is(err, ErrCloud18IdentityNotSet) {
		t.Fatalf("no client: %v", err)
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

// #2159: vault-auth = jwt logs in at auth/jwt/login with the identity token, the role being the
// identity client id, and keeps the vault's token
func TestVaultJWTLogin(t *testing.T) {
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"access_token":%q,"expires_in":300}`, testToken(`{"sub":"2"}`))
	}))
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

	conf := &Config{VaultServerAddr: vault.URL, VaultAuth: "jwt", Cloud18IdentityURL: realm.URL, Cloud18IdentityClientID: "repman-vault-test"}
	conf.Secrets = NewSecrets(map[string]Secret{"cloud18-identity-client-secret": {Value: "x"}})
	client, err := conf.GetVaultConnection()
	if err != nil {
		t.Fatal(err)
	}
	if client.Token() != "vault-token" || gotRole != "repman-vault-test" || gotJWT == "" {
		t.Fatalf("token %q role %q jwt set %v", client.Token(), gotRole, gotJWT != "")
	}
}

// #2159: the GitLab token of a new version comes from the Cloud18 CRM, for the identity token, with
// the instance's token name; no GitLab password is sent anywhere
func TestCloud18GitLabToken(t *testing.T) {
	identity := testToken(`{"sub":"2","azp":"repman-acme"}`)
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"access_token":%q,"expires_in":300}`, identity)
	}))
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

	conf := &Config{Cloud18IdentityURL: realm.URL + "/gitlab-token-test", Cloud18IdentityClientID: "repman-acme", Cloud18CrmApiUrl: crm.URL + "/"}
	conf.Secrets = NewSecrets(map[string]Secret{"cloud18-identity-client-secret": {Value: "x"}})
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

// identityConf: a client of realm with a secret
func identityConf(realm, client string) *Config {
	conf := &Config{Cloud18IdentityURL: realm, Cloud18IdentityClientID: client}
	conf.Secrets = NewSecrets(map[string]Secret{"cloud18-identity-client-secret": {Value: "x"}})
	return conf
}

// #2159 review: a 4xx of the realm is a refusal (Cloud18 may be turned off), a 5xx, 429 or a
// network error is transient (it must not be)
func TestCloud18IdentityRefusedOrTransient(t *testing.T) {
	for _, c := range []struct {
		code    int
		refused bool
	}{{401, true}, {400, true}, {403, true}, {429, false}, {503, false}, {500, false}} {
		realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(c.code) }))
		_, _, err := identityConf(realm.URL, "repman-status").Cloud18IdentityToken()
		realm.Close()
		if err == nil || errors.Is(err, ErrCloud18Refused) != c.refused {
			t.Errorf("realm %d: refused=%v, want %v (%v)", c.code, errors.Is(err, ErrCloud18Refused), c.refused, err)
		}
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	if _, _, err := identityConf(closed.URL, "repman-status").Cloud18IdentityToken(); err == nil || errors.Is(err, ErrCloud18Refused) {
		t.Errorf("a realm out of reach is transient: %v", err)
	}
	if _, _, err := (&Config{}).Cloud18IdentityToken(); !errors.Is(err, ErrCloud18Refused) || !errors.Is(err, ErrCloud18IdentityNotSet) {
		t.Errorf("no client is a refusal: %v", err)
	}
}

// #2159 review: the secret goes to an https realm only (http to the loopback for tests), never
// empty; a token needs a lifetime, kept 24 h at most
func TestCloud18IdentityChecks(t *testing.T) {
	var calls atomic.Int32
	expires := 0
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprintf(w, `{"access_token":%q,"expires_in":%d}`, testToken(`{"sub":"7"}`), expires)
	}))
	defer realm.Close()

	for _, u := range []string{"http://identity.example.com/realms/cloud18", "ftp://identity.example.com", "identity.example.com"} {
		if _, _, err := identityConf(u, "repman-checks").Cloud18IdentityToken(); !errors.Is(err, ErrCloud18Refused) {
			t.Errorf("%s: %v", u, err)
		}
	}
	noSecret := &Config{Cloud18IdentityURL: realm.URL, Cloud18IdentityClientID: "repman-checks"}
	if _, _, err := noSecret.Cloud18IdentityToken(); !errors.Is(err, ErrCloud18Refused) || calls.Load() != 0 {
		t.Errorf("empty secret: %v, %d calls", err, calls.Load())
	}
	if _, _, err := identityConf(realm.URL, "repman-checks").Cloud18IdentityToken(); err == nil {
		t.Error("a token without lifetime must be refused")
	}
	expires = 10 * 365 * 24 * 3600
	if _, _, err := identityConf(realm.URL, "repman-checks").Cloud18IdentityToken(); err != nil {
		t.Fatal(err)
	}
	cloud18IdentityTokens.Lock()
	got := cloud18IdentityTokens.m[realm.URL+"\x00repman-checks"].expires
	cloud18IdentityTokens.Unlock()
	if time.Until(got) > cloud18IdentityMaxLifetime {
		t.Errorf("lifetime not clamped: %s", time.Until(got))
	}
}

// #2159 review: concurrent callers of one client make one request; a slow realm does not hold
// up the callers of another client
func TestCloud18IdentityConcurrent(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-release
		fmt.Fprintf(w, `{"access_token":%q,"expires_in":300}`, testToken(`{"sub":"9"}`))
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"access_token":%q,"expires_in":300}`, testToken(`{"sub":"10"}`))
	}))
	defer fast.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, sub, err := identityConf(slow.URL, "repman-slow").Cloud18IdentityToken()
			if err == nil && sub != "9" {
				err = fmt.Errorf("sub %q", sub)
			}
			errs <- err
		}()
	}
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	if _, _, err := identityConf(fast.URL, "repman-fast").Cloud18IdentityToken(); err != nil || time.Since(start) > 2*time.Second {
		t.Errorf("another client waited for the slow realm: %s %v", time.Since(start), err)
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
		t.Errorf("%d requests for one client, want 1", calls.Load())
	}
}

// #2159 review: the CRM's GitLab token requests are serialized and one token is handed to
// concurrent callers (a second request would revoke the first); 5xx transient, 4xx refused,
// error text cut; the CRM URL https; a grant without token or user refused
func TestCloud18GitLabTokenChecks(t *testing.T) {
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"access_token":%q,"expires_in":300}`, testToken(`{"sub":"2"}`))
	}))
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
	conf := identityConf(realm.URL+"/crm-checks", "repman-crm")

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
