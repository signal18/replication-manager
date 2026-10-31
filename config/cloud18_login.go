// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

	"github.com/coreos/go-oidc/v3/oidc"
)

// Cloud18 login (#2182): a person logs in to replication-manager with the Cloud18 account, never
// with GitLab's password grant (GitLab ends it). The walk of the registration (cloud18GitLabWalk)
// gives the GitLab token; with the realm session it leaves, the realm then gives a token of the
// login client (authorization code, PKCE, no second password post). That realm token, not a
// password, is what goes to the other Cloud18 instances: each one verifies it by itself
// (VerifyCloud18Token: the realm's keys, issuer, expiry, the replication-manager API audience).

const (
	// ConstCloud18LoginClientID is the realm's public client of a person's login to
	// replication-manager (standard flow, PKCE, the loopback redirect never opened).
	ConstCloud18LoginClientID = "replication-manager-login"
	// ConstCloud18APIAudience is the audience every replication-manager accepts: a realm token
	// without it (minted for the S3 storage, GitLab, an instance's own identity…) is refused.
	ConstCloud18APIAudience = "replication-manager-api"
	// cloud18LoginRedirect is never opened: the realm gives the code in it, repman reads it.
	cloud18LoginRedirect = "http://127.0.0.1/cloud18-login"
	// a realm token is refreshed this long before its end
	cloud18LoginRenewBefore = 30 * time.Second
)

// ErrCloud18TokenRefused: the bearer is a token of the realm this instance trusts, but it is not
// valid here (signature, expiry, audience, unverified email).
var ErrCloud18TokenRefused = errors.New("Cloud18 token refused")

// Cloud18Login is a person's login with the Cloud18 account.
type Cloud18Login struct {
	GitLabToken string       // GitLab (scope api), for the person's GitLab calls
	Access      string       // realm token of the login client, "" when the realm gave none
	Refresh     string       // its refresh token
	Web         *http.Client // the walk, logged in to GitLab: other GitLab sign-ins (meet) reuse it
}

// Cloud18Login logs the person in with the Cloud18 email and password: the GitLab token, then the
// realm token. A realm that gives no token of the login client (client not set up) leaves a login
// with the GitLab token only: the person is logged in, the other instances get the password as
// before (the rollout of #2182).
func (conf *Config) Cloud18Login(email, password string) (*Cloud18Login, error) {
	token, w, err := conf.cloud18GitLabWalk(email, password)
	if err != nil {
		return nil, err
	}
	l := &Cloud18Login{GitLabToken: token, Web: w.client}
	if a, err := conf.cloud18LoginToken(w); err != nil {
		conf.cloud18Log(LvlWarn, "Cloud18 gave no realm token for the login of %s (the other instances get the password): %s", email, err)
	} else {
		l.Access, l.Refresh = a.AccessToken, a.RefreshToken
	}
	return l, nil
}

// cloud18LoginToken asks the realm, with the session the walk opened, for a token of the login
// client: the realm answers the authorization at once (the person just logged in) with a code.
func (conf *Config) cloud18LoginToken(w *cloud18Web) (cloud18TokenAnswer, error) {
	var a cloud18TokenAnswer
	issuer, err := conf.cloud18Issuer()
	if err != nil {
		return a, err
	}
	verifier, challenge, err := pkcePair()
	if err != nil {
		return a, err
	}
	w.code, w.state = "", ""
	q := url.Values{"client_id": {ConstCloud18LoginClientID}, "redirect_uri": {cloud18LoginRedirect}, "response_type": {"code"},
		"scope": {"openid email profile"}, "state": {"repman-login"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	if _, _, err := w.do(http.MethodGet, issuer+"/protocol/openid-connect/auth?"+q.Encode(), nil); err != nil {
		return a, fmt.Errorf("Cloud18: %w", err)
	}
	if w.code == "" {
		return a, fmt.Errorf("the realm gave no code to client %s", ConstCloud18LoginClientID)
	}
	if w.state != "repman-login" {
		return a, errors.New("the realm answered an authorization that is not this one (state)")
	}
	code, body, err := postCloud18Realm(issuer+"/protocol/openid-connect/token", url.Values{"grant_type": {"authorization_code"},
		"code": {w.code}, "redirect_uri": {cloud18LoginRedirect}, "client_id": {ConstCloud18LoginClientID}, "code_verifier": {verifier}})
	if err != nil {
		return a, err
	}
	if code != http.StatusOK {
		return a, cloud18Status("the realm", code, ": "+cloud18RealmError(body))
	}
	if err := json.Unmarshal(body, &a); err != nil || a.AccessToken == "" {
		return a, errors.New("the realm gave no token")
	}
	return a, nil
}

// Cloud18User is the person a verified realm token stands for.
type Cloud18User struct {
	Email   string
	Name    string
	Expires time.Time
}

var cloud18Verifiers = struct {
	sync.Mutex
	m map[string]*oidc.IDTokenVerifier
}{m: map[string]*oidc.IDTokenVerifier{}}

// cloud18Verifier verifies tokens of the realm cloud18-identity-url against its published keys
// (fetched once, again when a token names a key it does not know).
func (conf *Config) cloud18Verifier() (*oidc.IDTokenVerifier, error) {
	issuer, err := conf.cloud18Issuer()
	if err != nil {
		return nil, err
	}
	cloud18Verifiers.Lock()
	defer cloud18Verifiers.Unlock()
	if v, ok := cloud18Verifiers.m[issuer]; ok {
		return v, nil
	}
	keys := oidc.NewRemoteKeySet(context.Background(), issuer+"/protocol/openid-connect/certs")
	v := oidc.NewVerifier(issuer, keys, &oidc.Config{ClientID: ConstCloud18APIAudience})
	cloud18Verifiers.m[issuer] = v
	return v, nil
}

// IsCloud18Token says whether a bearer claims to come from the realm this instance trusts (its
// issuer, unverified): only such a bearer is verified as a realm token.
func (conf *Config) IsCloud18Token(raw string) bool {
	issuer, err := conf.cloud18Issuer()
	if err != nil || strings.Count(raw, ".") != 2 {
		return false
	}
	return strings.TrimRight(tokenClaim(raw, "iss"), "/") == issuer
}

// cloud18Keys are the key ids the realm publishes, fetched at most once per cloud18KeysRefetch:
// a token naming another key is refused without asking the realm again (forged tokens with random
// key ids would otherwise make every request fetch the realm's keys). The set is replaced only by a
// successful fetch; a failed one keeps it and is retried after cloud18KeysRetry. The lock is never
// held during the fetch (one fetch at a time). Keys come from the realm's certs URL, the one the
// signature check (cloud18Verifier) uses too.
var cloud18Keys = struct {
	sync.Mutex
	issuer   string
	kids     map[string]bool
	fetched  time.Time
	failed   bool
	fetching bool
}{}

// cloud18KeysRefetch is how often an unknown key id may refetch the realm's keys, cloud18KeysRetry
// how soon after a failed fetch (variables: tests lower them).
var (
	cloud18KeysRefetch = time.Minute
	cloud18KeysRetry   = 10 * time.Second
)

// ErrCloud18KeysUnavailable: the realm's keys could not be read (realm out of reach): the token
// is not judged, so its refusal is not remembered.
var ErrCloud18KeysUnavailable = errors.New("the Cloud18 realm's keys are out of reach")

func (conf *Config) cloud18KeyKnown(issuer, kid string) (bool, error) {
	cloud18Keys.Lock()
	same := cloud18Keys.issuer == issuer
	if same && cloud18Keys.kids[kid] {
		cloud18Keys.Unlock()
		return true, nil
	}
	wait := cloud18KeysRefetch
	if cloud18Keys.failed {
		wait = cloud18KeysRetry
	}
	if cloud18Keys.fetching || (same && time.Since(cloud18Keys.fetched) < wait) {
		failed := cloud18Keys.fetching || (same && cloud18Keys.failed)
		cloud18Keys.Unlock()
		if failed {
			return false, ErrCloud18KeysUnavailable
		}
		return false, nil
	}
	cloud18Keys.fetching, cloud18Keys.fetched = true, time.Now()
	cloud18Keys.Unlock()

	kids, err := fetchCloud18Kids(issuer)

	cloud18Keys.Lock()
	defer cloud18Keys.Unlock()
	cloud18Keys.fetching = false
	if err != nil {
		if !same {
			cloud18Keys.issuer, cloud18Keys.kids = issuer, map[string]bool{}
		}
		cloud18Keys.failed = true
		return false, ErrCloud18KeysUnavailable
	}
	cloud18Keys.issuer, cloud18Keys.kids, cloud18Keys.failed = issuer, kids, false
	return kids[kid], nil
}

func fetchCloud18Kids(issuer string) (map[string]bool, error) {
	resp, err := (&http.Client{Timeout: cloud18IdentityHTTPTimeout}).Get(issuer + "/protocol/openid-connect/certs")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, err
	}
	kids := map[string]bool{}
	for _, k := range set.Keys {
		kids[k.Kid] = true
	}
	if len(kids) == 0 {
		return nil, errors.New("no keys")
	}
	return kids, nil
}

// tokenHeader reads a field of a JWT's header, unverified ("" when absent).
func tokenHeader(token, field string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ""
	}
	var h map[string]interface{}
	if json.Unmarshal(b, &h) != nil {
		return ""
	}
	v, _ := h[field].(string)
	return v
}

// VerifyCloud18Token verifies a realm token (signature against the realm's keys, issuer, expiry,
// the replication-manager API audience) and returns its person, who must have a verified email.
// An expired token, or one naming a key the realm does not publish, is refused before any request
// to the realm.
func (conf *Config) VerifyCloud18Token(raw string) (Cloud18User, error) {
	var u Cloud18User
	issuer, err := conf.cloud18Issuer()
	if err != nil {
		return u, err
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if parts := strings.Split(raw, "."); len(parts) == 3 {
		if b, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
			json.Unmarshal(b, &claims)
		}
	}
	if claims.Exp == 0 || time.Unix(int64(claims.Exp), 0).Before(time.Now()) {
		return u, fmt.Errorf("%w: expired or without end", ErrCloud18TokenRefused)
	}
	kid := tokenHeader(raw, "kid")
	if kid == "" {
		return u, fmt.Errorf("%w: no signing key id", ErrCloud18TokenRefused)
	}
	known, err := conf.cloud18KeyKnown(issuer, kid)
	if err != nil {
		return u, err // not judged: not remembered as refused
	}
	if !known {
		return u, fmt.Errorf("%w: unknown signing key", ErrCloud18TokenRefused)
	}
	v, err := conf.cloud18Verifier()
	if err != nil {
		return u, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cloud18IdentityHTTPTimeout)
	defer cancel()
	t, err := v.Verify(ctx, raw)
	if err != nil {
		return u, fmt.Errorf("%w: %s", ErrCloud18TokenRefused, err)
	}
	var c struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := t.Claims(&c); err != nil {
		return u, fmt.Errorf("%w: %s", ErrCloud18TokenRefused, err)
	}
	if c.Email == "" || !c.EmailVerified {
		return u, fmt.Errorf("%w: no verified email", ErrCloud18TokenRefused)
	}
	return Cloud18User{Email: strings.ToLower(c.Email), Name: c.Name, Expires: t.Expiry}, nil
}

// cloud18LoginEntry is the realm token of one login (by its first refresh token), the newest
// refresh token the realm gave (a realm rotating them refuses the first one once used) and how
// long that refresh token lives. mu serializes the refreshes of one login.
type cloud18LoginEntry struct {
	mu         sync.Mutex
	token      string
	expires    time.Time
	refresh    string
	refreshEnd time.Time
	seen       time.Time
	refused    time.Time // the realm refused the refresh token: not asked again before this
	refusedErr error
}

// cloud18LoginRefusedWait is how long a refresh token the realm refused (revoked, its session
// ended) is not sent again: every request to another instance would otherwise wait on the realm
// (a variable: tests lower it).
var cloud18LoginRefusedWait = time.Minute

// cloud18LoginTokensMax bounds the logins kept (F4): past it, the least recently used one goes.
const cloud18LoginTokensMax = 4096

// cloud18LoginTokens caches the realm token of each login until it nears its end: one refresh
// per login and token lifetime, not one per request to another instance. An entry lives as long
// as its refresh token, so a rotated refresh token is not lost when the access token ends.
// The session keeps the first refresh token: the realm must let a refresh token be used again
// (Keycloak's "Revoke Refresh Token" off, its default). With a realm rotating them, an entry
// rebuilt (restart, eviction) holds a revoked token: the session goes on with the password, as
// before #2182, until the person logs in again.
var cloud18LoginTokens = struct {
	sync.Mutex
	m map[string]*cloud18LoginEntry
}{m: map[string]*cloud18LoginEntry{}}

// cloud18LoginEntryFor returns the entry of a login, created when new; the cache is pruned of
// ended logins and bounded.
func cloud18LoginEntryFor(refresh string, now time.Time) *cloud18LoginEntry {
	sum := sha256.Sum256([]byte(refresh))
	key := hex.EncodeToString(sum[:])
	cloud18LoginTokens.Lock()
	defer cloud18LoginTokens.Unlock()
	if e, ok := cloud18LoginTokens.m[key]; ok {
		e.seen = now
		return e
	}
	for k, e := range cloud18LoginTokens.m {
		if !e.refreshEnd.IsZero() && !now.Before(e.refreshEnd) {
			delete(cloud18LoginTokens.m, k)
		}
	}
	if len(cloud18LoginTokens.m) >= cloud18LoginTokensMax {
		oldest := ""
		for k, e := range cloud18LoginTokens.m {
			if oldest == "" || e.seen.Before(cloud18LoginTokens.m[oldest].seen) {
				oldest = k
			}
		}
		delete(cloud18LoginTokens.m, oldest)
	}
	e := &cloud18LoginEntry{refresh: refresh, seen: now, refreshEnd: now.Add(cloud18IdentityMaxLifetime)}
	cloud18LoginTokens.m[key] = e
	return e
}

// ForgetCloud18Logins drops the kept logins, as an instance restart does (tests).
func ForgetCloud18Logins() {
	cloud18LoginTokens.Lock()
	cloud18LoginTokens.m = map[string]*cloud18LoginEntry{}
	cloud18LoginTokens.Unlock()
}

// Cloud18LoginAccess returns a valid realm token of a login from its refresh token.
func (conf *Config) Cloud18LoginAccess(refresh string) (string, error) {
	if refresh == "" {
		return "", errors.New("no Cloud18 login")
	}
	now := time.Now()
	e := cloud18LoginEntryFor(refresh, now)
	e.mu.Lock()
	defer e.mu.Unlock()
	now = time.Now() // after waiting on a concurrent refresh
	if e.token != "" && e.expires.Sub(now) > cloud18LoginRenewBefore {
		return e.token, nil // also the token a concurrent caller just got
	}
	if now.Before(e.refused) {
		return "", e.refusedErr
	}
	issuer, err := conf.cloud18Issuer()
	if err != nil {
		return "", err
	}
	code, body, err := postCloud18Realm(issuer+"/protocol/openid-connect/token", url.Values{"grant_type": {"refresh_token"},
		"client_id": {ConstCloud18LoginClientID}, "refresh_token": {e.refresh}})
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		err := cloud18Status("the realm", code, ": "+cloud18RealmError(body))
		if code == http.StatusUnauthorized || (code == http.StatusBadRequest && strings.Contains(string(body), "invalid_grant")) {
			// the refresh token is refused (invalid_grant): not again for a while; a network
			// error or a 5xx is tried again at the next call
			e.token, e.refused, e.refusedErr = "", now.Add(cloud18LoginRefusedWait), err
		}
		return "", err
	}
	var a cloud18TokenAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		return "", errors.New("the realm gave no token")
	}
	lifetime, err := a.lifetime()
	if err != nil {
		return "", err
	}
	e.refused, e.refusedErr = time.Time{}, nil
	e.token, e.expires = a.AccessToken, now.Add(lifetime)
	if a.RefreshToken != "" {
		e.refresh = a.RefreshToken // a realm rotating refresh tokens
	}
	if end := a.sessionEnd(now); !end.IsZero() {
		e.refreshEnd = end
	}
	return e.token, nil
}

// Sign in with Cloud18 (#2185): on a registered repman with a public https address, the browser
// signs in at the realm itself (authorization code, PKCE) and comes back to the repman's callback;
// the realm's session then signs the person in on every other such repman without a password. No
// repman talks to another. The other repmans keep the Sign in form only (Cloud18Login).

// Cloud18SSOCallbackPath is the callback route of the browser sign-in, under api-public-url (the
// realm's client lists it for each such repman).
const Cloud18SSOCallbackPath = "/api/auth/cloud18/callback"

// Cloud18SSO says whether this repman offers Sign in with Cloud18, and if not why: registered to
// Cloud18 (whatever its plan), a public https address (api-public-url, not the loopback default nor
// a private address), a realm, and cloud18-sso-login on. It returns the public address.
func (conf *Config) Cloud18SSO() (string, error) {
	switch {
	case !conf.Cloud18SSOLogin:
		return "", errors.New("off (cloud18-sso-login)")
	case !conf.Cloud18 || conf.Cloud18GitUser == "":
		return "", errors.New("not registered to Cloud18")
	}
	if _, err := conf.cloud18Issuer(); err != nil {
		return "", err
	}
	public := strings.TrimRight(strings.TrimSpace(conf.APIPublicURL), "/")
	u, err := url.Parse(public)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return "", errors.New("no public https address (api-public-url)")
	}
	if ip := net.ParseIP(u.Hostname()); u.Hostname() == "localhost" || (ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified())) {
		return "", errors.New("api-public-url is not a public address")
	}
	return public, nil
}

// Cloud18SSOMode is how the browser goes to the realm: to sign in (the email prefilled when the
// person typed it), silently (prompt=none: signed in only if the person already has a realm session,
// the realm answers login_required otherwise, nothing shown), or to sign up (the realm's own form).
type Cloud18SSOMode struct {
	Hint   string // login_hint: the email typed on repman's login page, never a password
	Silent bool
	SignUp bool
}

// Cloud18SSOAuthorizeURL is where the browser goes: the realm's authorization (or sign-up) for the
// login client, back to this repman's callback.
func (conf *Config) Cloud18SSOAuthorizeURL(public, state, challenge string, mode Cloud18SSOMode) (string, error) {
	issuer, err := conf.cloud18Issuer()
	if err != nil {
		return "", err
	}
	q := url.Values{"client_id": {ConstCloud18LoginClientID}, "redirect_uri": {public + Cloud18SSOCallbackPath}, "response_type": {"code"},
		"scope": {"openid email profile"}, "state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	if mode.Hint != "" {
		q.Set("login_hint", mode.Hint)
	}
	endpoint := "/protocol/openid-connect/auth?"
	switch {
	case mode.SignUp:
		endpoint = "/protocol/openid-connect/registrations?"
	case mode.Silent:
		q.Set("prompt", "none")
	}
	return issuer + endpoint + q.Encode(), nil
}

// Cloud18SSOExchange trades the code of the browser sign-in for the realm tokens.
func (conf *Config) Cloud18SSOExchange(public, code, verifier string) (*Cloud18Login, error) {
	issuer, err := conf.cloud18Issuer()
	if err != nil {
		return nil, err
	}
	status, body, err := postCloud18Realm(issuer+"/protocol/openid-connect/token", url.Values{"grant_type": {"authorization_code"},
		"code": {code}, "redirect_uri": {public + Cloud18SSOCallbackPath}, "client_id": {ConstCloud18LoginClientID}, "code_verifier": {verifier}})
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, cloud18Status("the realm", status, ": "+cloud18RealmError(body))
	}
	var a cloud18TokenAnswer
	if err := json.Unmarshal(body, &a); err != nil || a.AccessToken == "" {
		return nil, errors.New("the realm gave no token")
	}
	return &Cloud18Login{Access: a.AccessToken, Refresh: a.RefreshToken}, nil
}

// Cloud18SSOLogoutURL ends the person's realm session (every such repman asks again) and comes back to
// this repman's login page.
func (conf *Config) Cloud18SSOLogoutURL(public string) (string, error) {
	issuer, err := conf.cloud18Issuer()
	if err != nil {
		return "", err
	}
	q := url.Values{"client_id": {ConstCloud18LoginClientID}, "post_logout_redirect_uri": {public + "/login"}}
	return issuer + "/protocol/openid-connect/logout?" + q.Encode(), nil
}

// Cloud18PKCE is a PKCE verifier and its S256 challenge, for the browser sign-in.
func Cloud18PKCE() (string, string, error) {
	return pkcePair()
}
