// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/base64"
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
