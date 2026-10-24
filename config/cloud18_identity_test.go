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
	"sync/atomic"
	"testing"
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
	for _, bad := range []string{"x.y", testToken(`{}`), testToken(`{"preferred_username":"acme"}`), testToken(`{"sub":"a/b"}`)} {
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
