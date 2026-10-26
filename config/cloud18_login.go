// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

// VerifyCloud18Token verifies a realm token (signature against the realm's keys, issuer, expiry,
// the replication-manager API audience) and returns its person, who must have a verified email.
func (conf *Config) VerifyCloud18Token(raw string) (Cloud18User, error) {
	var u Cloud18User
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

type cloud18LoginAccess struct {
	token   string
	expires time.Time
}

// cloud18LoginTokens caches the realm token of each login (by its refresh token) until it nears
// its end: one refresh per login, not one per request to another instance.
var cloud18LoginTokens = struct {
	sync.Mutex
	m map[string]cloud18LoginAccess
}{m: map[string]cloud18LoginAccess{}}

// Cloud18LoginAccess returns a valid realm token of a login from its refresh token.
func (conf *Config) Cloud18LoginAccess(refresh string) (string, error) {
	if refresh == "" {
		return "", errors.New("no Cloud18 login")
	}
	sum := sha256.Sum256([]byte(refresh))
	key := hex.EncodeToString(sum[:])
	now := time.Now()
	cloud18LoginTokens.Lock()
	for k, t := range cloud18LoginTokens.m {
		if !now.Before(t.expires) {
			delete(cloud18LoginTokens.m, k) // bounded: only live logins stay
		}
	}
	if t, ok := cloud18LoginTokens.m[key]; ok && t.expires.Sub(now) > cloud18LoginRenewBefore {
		cloud18LoginTokens.Unlock()
		return t.token, nil
	}
	cloud18LoginTokens.Unlock()
	issuer, err := conf.cloud18Issuer()
	if err != nil {
		return "", err
	}
	code, body, err := postCloud18Realm(issuer+"/protocol/openid-connect/token", url.Values{"grant_type": {"refresh_token"},
		"client_id": {ConstCloud18LoginClientID}, "refresh_token": {refresh}})
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", cloud18Status("the realm", code, ": "+cloud18RealmError(body))
	}
	var a cloud18TokenAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		return "", errors.New("the realm gave no token")
	}
	lifetime, err := a.lifetime()
	if err != nil {
		return "", err
	}
	cloud18LoginTokens.Lock()
	cloud18LoginTokens.m[key] = cloud18LoginAccess{token: a.AccessToken, expires: now.Add(lifetime)}
	cloud18LoginTokens.Unlock()
	return a.AccessToken, nil
}
