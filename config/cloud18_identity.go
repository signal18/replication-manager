// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// The Cloud18 identity (#2159): a replication-manager registered to Cloud18 is a client of the
// Cloud18 identity realm (cloud18-identity-url, Keycloak), with its own client id and secret
// pushed by the BO at registration. It gets its tokens with the client_credentials grant: no
// person's password, no GitLab password. The token carries the audience the Cloud18 services
// trust (cloud18-storage) and as sub the instance's permanent GitLab user id (the client's
// cloud18-storage scope), so an S3 SSO archive keeps the folder it had with the GitLab login.
// The S3 SSO storages and the Cloud18 vault (vault-auth = jwt) take that token.

const (
	// ConstCloud18IdentityURL is cloud18-identity-url's default, the Cloud18 realm's issuer.
	ConstCloud18IdentityURL = "https://identity.cloud18.io/realms/cloud18"
	// ConstCloud18StorageAudience is the audience the Cloud18 S3 storages trust: their OpenID
	// client id, so the role MinIO derives from it is the same for every instance.
	ConstCloud18StorageAudience = "cloud18-storage"

	cloud18IdentityRenewBefore = 30 * time.Second
	cloud18IdentityHTTPTimeout = 30 * time.Second
)

// ErrCloud18IdentityNotSet: the instance has no Cloud18 identity client yet.
var ErrCloud18IdentityNotSet = errors.New("Cloud18 identity: this replication-manager has no client (cloud18-identity-client-id)")

type cloud18IdentityToken struct {
	token, sub string
	expires    time.Time
}

// cloud18IdentityTokens caches one token per realm and client: every S3 mount and the vault
// share it until it nears its end.
var cloud18IdentityTokens = struct {
	sync.Mutex
	m map[string]cloud18IdentityToken
}{m: map[string]cloud18IdentityToken{}}

// Cloud18IdentityToken returns a valid access token of this instance's Cloud18 identity client
// and its sub claim (the folder name of the instance on the S3 SSO storages).
func (conf *Config) Cloud18IdentityToken() (string, string, error) {
	issuer := strings.TrimRight(conf.Cloud18IdentityURL, "/")
	clientID := conf.Cloud18IdentityClientID
	if issuer == "" || clientID == "" {
		return "", "", ErrCloud18IdentityNotSet
	}
	key := issuer + "\x00" + clientID
	cloud18IdentityTokens.Lock()
	defer cloud18IdentityTokens.Unlock()
	if t, ok := cloud18IdentityTokens.m[key]; ok && time.Until(t.expires) > cloud18IdentityRenewBefore {
		return t.token, t.sub, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {conf.GetDecryptedValue("cloud18-identity-client-secret")},
	}
	resp, err := (&http.Client{Timeout: cloud18IdentityHTTPTimeout}).PostForm(issuer+"/protocol/openid-connect/token", form)
	if err != nil {
		return "", "", fmt.Errorf("Cloud18 identity: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("Cloud18 identity: the realm refused client %s (answered %d)", clientID, resp.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return "", "", errors.New("Cloud18 identity: the realm gave no token")
	}
	sub, err := TokenSubject(tok.AccessToken)
	if err != nil {
		return "", "", err
	}
	cloud18IdentityTokens.m[key] = cloud18IdentityToken{token: tok.AccessToken, sub: sub, expires: time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)}
	return tok.AccessToken, sub, nil
}

// TokenSubject reads the sub claim of a JWT, the permanent id a storage names the folder after
// (the storage checks the signature; this side only needs the folder's name).
func TokenSubject(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("Cloud18 identity: malformed token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("Cloud18 identity: malformed token")
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", errors.New("Cloud18 identity: malformed token")
	}
	if claims.Sub == "" || strings.ContainsAny(claims.Sub, "/\\") {
		return "", errors.New("Cloud18 identity: the token names no user")
	}
	return claims.Sub, nil
}
