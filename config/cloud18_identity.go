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
	"net"
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
	// a token is kept at most this long, whatever lifetime the realm gives
	cloud18IdentityMaxLifetime = 24 * time.Hour
	// a GitLab token the CRM just gave is handed to the next callers for this long instead of
	// asking again: a second request would revoke the first token (same name)
	cloud18GitLabGrantReuse = 5 * time.Minute
	cloud18ErrorTextMax     = 200
)

// ErrCloud18IdentityNotSet: the instance has no Cloud18 identity client yet.
var ErrCloud18IdentityNotSet = errors.New("Cloud18 identity: this replication-manager has no client (cloud18-identity-client-id)")

// ErrCloud18Refused marks a definitive answer (a 4xx of the realm or the CRM, a missing or
// unsafe setting): retrying gives the same. Any other error (network, timeout, 5xx, 429) is
// transient: a caller must not turn Cloud18 off for it.
var ErrCloud18Refused = errors.New("refused")

func cloud18Refused(format string, args ...interface{}) error {
	return fmt.Errorf("%w: "+format, append([]interface{}{ErrCloud18Refused}, args...)...)
}

// cloud18Status sorts a non-200 answer: 4xx refused, except 408 and 429 (transient, as 5xx).
func cloud18Status(what string, code int, detail string) error {
	if code >= 400 && code < 500 && code != http.StatusRequestTimeout && code != http.StatusTooManyRequests {
		return cloud18Refused("%s answered %d%s", what, code, detail)
	}
	return fmt.Errorf("%s answered %d%s", what, code, detail)
}

// cloud18SafeURL refuses a URL a secret or a token would be sent to in clear: https only,
// plain http to the loopback alone.
func cloud18SafeURL(setting, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return cloud18Refused("%s is not a URL", setting)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
	}
	return cloud18Refused("%s must be https", setting)
}

func cloud18ErrorText(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > cloud18ErrorTextMax {
		s = s[:cloud18ErrorTextMax] + "…"
	}
	return s
}

type cloud18IdentityToken struct {
	token, sub string
	expires    time.Time
}

// cloud18IdentityTokens caches one token per realm and client (the key holds no secret: a
// changed secret takes effect when the token is renewed): every S3 mount and the vault share
// it until it nears its end. The map lock is never held during a request; fetch serializes the
// requests of one client, so callers waiting on it take the token the first one got.
var cloud18IdentityTokens = struct {
	sync.Mutex
	m     map[string]cloud18IdentityToken
	fetch map[string]*sync.Mutex
}{m: map[string]cloud18IdentityToken{}, fetch: map[string]*sync.Mutex{}}

func cachedCloud18IdentityToken(key string) (cloud18IdentityToken, bool) {
	cloud18IdentityTokens.Lock()
	defer cloud18IdentityTokens.Unlock()
	t, ok := cloud18IdentityTokens.m[key]
	return t, ok && time.Until(t.expires) > cloud18IdentityRenewBefore
}

// Cloud18IdentityToken returns a valid access token of this instance's Cloud18 identity client
// and its sub claim (the folder name of the instance on the S3 SSO storages).
func (conf *Config) Cloud18IdentityToken() (string, string, error) {
	issuer := strings.TrimRight(conf.Cloud18IdentityURL, "/")
	clientID := conf.Cloud18IdentityClientID
	if issuer == "" || clientID == "" {
		return "", "", fmt.Errorf("%w (%w)", ErrCloud18IdentityNotSet, ErrCloud18Refused)
	}
	key := issuer + "\x00" + clientID
	if t, ok := cachedCloud18IdentityToken(key); ok {
		return t.token, t.sub, nil
	}
	if err := cloud18SafeURL("cloud18-identity-url", issuer); err != nil {
		return "", "", fmt.Errorf("Cloud18 identity: %w", err)
	}
	secret := conf.GetDecryptedValue("cloud18-identity-client-secret")
	if secret == "" {
		return "", "", cloud18Refused("Cloud18 identity: client %s has no secret (cloud18-identity-client-secret)", clientID)
	}

	cloud18IdentityTokens.Lock()
	fetch, ok := cloud18IdentityTokens.fetch[key]
	if !ok {
		fetch = &sync.Mutex{}
		cloud18IdentityTokens.fetch[key] = fetch
	}
	cloud18IdentityTokens.Unlock()
	fetch.Lock()
	defer fetch.Unlock()
	if t, ok := cachedCloud18IdentityToken(key); ok {
		return t.token, t.sub, nil // got by the caller this one waited on
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {secret},
	}
	resp, err := (&http.Client{Timeout: cloud18IdentityHTTPTimeout}).PostForm(issuer+"/protocol/openid-connect/token", form)
	if err != nil {
		return "", "", fmt.Errorf("Cloud18 identity: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("Cloud18 identity: %w", cloud18Status("the realm, for client "+clientID+",", resp.StatusCode, ""))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return "", "", errors.New("Cloud18 identity: the realm gave no token")
	}
	if tok.ExpiresIn <= 0 {
		return "", "", errors.New("Cloud18 identity: the realm gave a token without lifetime (expires_in)")
	}
	lifetime := time.Duration(tok.ExpiresIn) * time.Second
	if lifetime > cloud18IdentityMaxLifetime {
		lifetime = cloud18IdentityMaxLifetime
	}
	sub, err := TokenSubject(tok.AccessToken)
	if err != nil {
		return "", "", err
	}
	cloud18IdentityTokens.Lock()
	cloud18IdentityTokens.m[key] = cloud18IdentityToken{token: tok.AccessToken, sub: sub, expires: time.Now().Add(lifetime)}
	cloud18IdentityTokens.Unlock()
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
	if claims.Sub == "" || claims.Sub == "." || claims.Sub == ".." || strings.ContainsAny(claims.Sub, "/\\") {
		return "", errors.New("Cloud18 identity: the token names no user")
	}
	return claims.Sub, nil
}

// Cloud18GitLabGrant is the GitLab token the Cloud18 CRM gives this instance for its identity
// token: an impersonation token of the user the instance acts for (scopes api, write_repository).
type Cloud18GitLabGrant struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
	UserID    int    `json:"user_id"`
	Username  string `json:"username"`
}

// Cloud18GitLabToken asks the Cloud18 CRM (cloud18-crm-api-url, POST /api/instances/gitlab-token)
// for the GitLab token this instance pushes and pulls its configuration repositories with, in
// exchange for its identity token (#2159): a new replication-manager keeps no GitLab password.
// The CRM revokes the instance's previous token of the same name: call it when the saved token no
// longer works and at the rotation, never at every start. The requests are serialized, and a
// token got less than cloud18GitLabGrantReuse ago is returned again rather than asked for (and
// the first one revoked) twice. ErrCloud18Refused marks a definitive refusal.
func (conf *Config) Cloud18GitLabToken(tokenName string) (Cloud18GitLabGrant, error) {
	cloud18GitLabGrants.Lock()
	defer cloud18GitLabGrants.Unlock()
	key := conf.Cloud18CrmApiUrl + "\x00" + tokenName
	if g, ok := cloud18GitLabGrants.last[key]; ok && time.Since(g.at) < cloud18GitLabGrantReuse {
		return g.grant, nil
	}
	g, err := conf.requestCloud18GitLabToken(tokenName)
	if err != nil {
		return g, err
	}
	cloud18GitLabGrants.last = map[string]cloud18GitLabGrantAt{key: {grant: g, at: time.Now()}}
	return g, nil
}

type cloud18GitLabGrantAt struct {
	grant Cloud18GitLabGrant
	at    time.Time
}

// cloud18GitLabGrants: the last GitLab token the CRM gave, and the lock serializing the requests
var cloud18GitLabGrants = struct {
	sync.Mutex
	last map[string]cloud18GitLabGrantAt
}{}

func (conf *Config) requestCloud18GitLabToken(tokenName string) (Cloud18GitLabGrant, error) {
	crm := strings.TrimRight(conf.Cloud18CrmApiUrl, "/")
	if crm == "" {
		return Cloud18GitLabGrant{}, cloud18Refused("Cloud18 GitLab token: no CRM (cloud18-crm-api-url)")
	}
	if err := cloud18SafeURL("cloud18-crm-api-url", crm); err != nil {
		return Cloud18GitLabGrant{}, fmt.Errorf("Cloud18 GitLab token: %w", err)
	}
	identity, _, err := conf.Cloud18IdentityToken()
	if err != nil {
		return Cloud18GitLabGrant{}, err
	}
	body, _ := json.Marshal(map[string]string{"token_name": tokenName})
	req, err := http.NewRequest(http.MethodPost, crm+"/api/instances/gitlab-token", strings.NewReader(string(body)))
	if err != nil {
		return Cloud18GitLabGrant{}, err
	}
	req.Header.Set("Authorization", "Bearer "+identity)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: cloud18IdentityHTTPTimeout}).Do(req)
	if err != nil {
		return Cloud18GitLabGrant{}, fmt.Errorf("Cloud18 GitLab token: %w", err)
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(answer, &e)
		detail := ""
		if t := cloud18ErrorText(e.Error); t != "" {
			detail = ": " + t
		}
		return Cloud18GitLabGrant{}, fmt.Errorf("Cloud18 GitLab token: %w", cloud18Status("the CRM", resp.StatusCode, detail))
	}
	var g Cloud18GitLabGrant
	if err := json.Unmarshal(answer, &g); err != nil || g.Token == "" || g.UserID <= 0 {
		return Cloud18GitLabGrant{}, errors.New("Cloud18 GitLab token: the CRM gave no token")
	}
	return g, nil
}
