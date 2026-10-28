// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/utils/crypto"
	log "github.com/sirupsen/logrus"
)

// The Cloud18 identity (#2159): a replication-manager gets its identity by itself from the
// Cloud18 identity realm (cloud18-identity-url, Keycloak), with no client of its own, no secret
// and no redirect: every instance uses the realm's one public client
// (ConstCloud18IdentityClientID). The login is the account the instance is registered with: the
// registration signs in once with the email and password the person gives it (KeepCloud18Login,
// the password never kept), an instance registered before keeps its password and signs in with it
// when it has no login (cloud18RegisteredLogin). repman keeps the account's offline refresh token,
// encrypted, in a file of its working directory that git never pushes (never sent to a peer),
// and gets its access tokens with it. The token carries the audience the Cloud18 services trust
// (cloud18-storage) and as sub the account's permanent GitLab user id, so an S3 SSO archive keeps
// the folder it had with the GitLab login. The S3 SSO storages and the Cloud18 vault
// (vault-auth = jwt) take that token.

const (
	// ConstCloud18IdentityURL is cloud18-identity-url's default, the Cloud18 realm's issuer.
	ConstCloud18IdentityURL = "https://identity.cloud18.io/realms/cloud18"
	// ConstCloud18IdentityClientID is the realm's public client every replication-manager logs
	// in with: password and refresh grants only, no secret, no redirect URI.
	ConstCloud18IdentityClientID = "replication-manager"
	// ConstCloud18StorageAudience is the audience the Cloud18 S3 storages trust: their OpenID
	// client id, so the role MinIO derives from it is the same for every instance.
	ConstCloud18StorageAudience = "cloud18-storage"

	cloud18IdentityScope       = "openid offline_access"
	cloud18IdentityFileName    = "cloud18-identity.json"
	cloud18IdentityRenewBefore = 30 * time.Second
	cloud18IdentityHTTPTimeout = 30 * time.Second
	// a token is kept at most this long, whatever lifetime the realm gives
	cloud18IdentityMaxLifetime = 24 * time.Hour
	// the login is refreshed at least this often, so the realm's offline idle limit is never
	// reached by a running instance, whatever uses the token
	cloud18IdentityKeepAlive = 24 * time.Hour
	cloud18ErrorTextMax      = 200
)

// cloud18RegisteredRetry is the shortest wait between two sign-ins with the registered account
// the realm did not answer (out of reach, 5xx): the keep-alive asks again after it (tests lower it).
var cloud18RegisteredRetry = 5 * time.Minute

// ErrCloud18IdentityNotSet: the instance has no Cloud18 login: not registered to Cloud18.
var ErrCloud18IdentityNotSet = errors.New("Cloud18 identity: this replication-manager is not registered to Cloud18 (Cloud18 settings, Register)")

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

// cloud18Log writes one step of the Cloud18 login in the log (module general): who, what, why,
// never a token.
func (conf *Config) cloud18Log(level string, format string, args ...interface{}) {
	if !conf.IsEligibleForPrinting(ConstLogModGeneral, level) {
		return
	}
	e := log.WithFields(log.Fields{"cluster": "none", "type": "log", "module": "general"})
	switch level {
	case LvlErr:
		e.Errorf("Cloud18 identity: "+format, args...)
	case LvlWarn:
		e.Warnf("Cloud18 identity: "+format, args...)
	default:
		e.Infof("Cloud18 identity: "+format, args...)
	}
}

func cloud18ErrorText(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > cloud18ErrorTextMax {
		s = s[:cloud18ErrorTextMax] + "…"
	}
	return s
}

// cloud18RealmError reads the OAuth error of a realm's answer ("invalid_grant: Offline session
// not active"), cut to cloud18ErrorTextMax.
func cloud18RealmError(body []byte) string {
	var e struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	json.Unmarshal(body, &e)
	s := e.Error
	if e.Description != "" {
		s += ": " + e.Description
	}
	return cloud18ErrorText(s)
}

// cloud18Login is what the identity file keeps: the person's offline refresh token, encrypted
// (hash_), and who connected the instance.
type cloud18Login struct {
	Issuer       string    `json:"issuer"`
	RefreshToken string    `json:"refresh_token"`
	User         string    `json:"user"`
	Sub          string    `json:"sub"`
	ConnectedAt  time.Time `json:"connected_at"`
	// the end of the realm's offline session as last told by the realm, zero when not told
	Expires time.Time `json:"expires,omitempty"`
}

// pkcePair is a PKCE code verifier and its S256 challenge.
func pkcePair() (string, string, error) {
	b := make([]byte, 48)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	verifier := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// cloud18Session is the identity of one instance (one working directory), in memory: the login
// read from the identity file with its refresh token decrypted, why it ended when the realm
// refused it, and the registered account the realm refused to sign in (its fingerprint: never
// asked again until the registration changes).
type cloud18Session struct {
	loaded      bool
	login       cloud18Login
	ended       string
	refreshedAt time.Time
	refused     string // fingerprint of the registered account the realm refused
	refusedWhy  string
	attemptedAt time.Time // the last sign-in with the registered account
	keepAlive   atomic.Bool
}

var cloud18Sessions = struct {
	sync.Mutex
	m map[string]*cloud18Session
}{m: map[string]*cloud18Session{}}

func (conf *Config) cloud18IdentityPath() string {
	return filepath.Join(conf.WorkingDir, cloud18IdentityFileName)
}

// cloud18SessionLocked returns the instance's session, read from the identity file the first
// time. cloud18Sessions is held by the caller.
func (conf *Config) cloud18SessionLocked() *cloud18Session {
	path := conf.cloud18IdentityPath()
	s, ok := cloud18Sessions.m[path]
	if !ok {
		s = &cloud18Session{}
		cloud18Sessions.m[path] = s
	}
	if s.loaded {
		return s
	}
	s.loaded = true
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var l cloud18Login
	if err := json.Unmarshal(data, &l); err != nil || l.RefreshToken == "" {
		s.ended = "the Cloud18 login file cannot be read"
		return s
	}
	token, err := conf.cloud18Decrypt(l.RefreshToken)
	if err != nil {
		s.ended = "the Cloud18 login cannot be decrypted with this instance's encryption key"
		return s
	}
	l.RefreshToken = token
	s.login = l
	return s
}

func (conf *Config) cloud18EncryptionKey() []byte {
	if conf.SecretKey == nil {
		conf.LoadEncrytionKey()
	}
	return conf.SecretKey
}

func (conf *Config) cloud18Decrypt(value string) (string, error) {
	key := conf.cloud18EncryptionKey()
	if key == nil || !strings.HasPrefix(value, "hash_") {
		return "", errors.New("not encrypted")
	}
	p := crypto.Password{Key: key, CipherText: strings.TrimPrefix(value, "hash_")}
	if err := p.Decrypt(); err != nil {
		return "", err
	}
	return p.PlainText, nil
}

// writeCloud18Login writes the identity file (0600, the refresh token encrypted): never in clear,
// so no encryption key, no login.
func (conf *Config) writeCloud18Login(l cloud18Login) error {
	key := conf.cloud18EncryptionKey()
	if key == nil {
		return cloud18Refused("Cloud18 identity: this replication-manager has no encryption key to keep the login")
	}
	p := crypto.Password{Key: key, PlainText: l.RefreshToken}
	p.Encrypt()
	if p.CipherText == "" {
		return cloud18Refused("Cloud18 identity: the login cannot be encrypted with this instance's encryption key")
	}
	l.RefreshToken = "hash_" + p.CipherText
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	path := conf.cloud18IdentityPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("Cloud18 identity: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("Cloud18 identity: %w", err)
	}
	return nil
}

// cloud18TokenAnswer is the part of the realm's token answer this side reads.
type cloud18TokenAnswer struct {
	AccessToken      string `json:"access_token"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int    `json:"refresh_expires_in"`
}

// lifetime is how long the access token is kept: what the realm gives, at most cloud18IdentityMaxLifetime.
func (a cloud18TokenAnswer) lifetime() (time.Duration, error) {
	if a.AccessToken == "" {
		return 0, errors.New("Cloud18 identity: the realm gave no token")
	}
	if a.ExpiresIn <= 0 {
		return 0, errors.New("Cloud18 identity: the realm gave a token without lifetime (expires_in)")
	}
	lifetime := time.Duration(a.ExpiresIn) * time.Second
	if lifetime > cloud18IdentityMaxLifetime {
		lifetime = cloud18IdentityMaxLifetime
	}
	return lifetime, nil
}

// sessionEnd is the end of the offline session the answer tells, zero when unlimited.
func (a cloud18TokenAnswer) sessionEnd(now time.Time) time.Time {
	if a.RefreshExpiresIn <= 0 {
		return time.Time{}
	}
	return now.Add(time.Duration(a.RefreshExpiresIn) * time.Second)
}

func postCloud18Realm(endpoint string, form url.Values) (int, []byte, error) {
	resp, err := (&http.Client{Timeout: cloud18IdentityHTTPTimeout}).PostForm(endpoint, form)
	if err != nil {
		return 0, nil, fmt.Errorf("Cloud18 identity: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, nil
}

func (conf *Config) cloud18Issuer() (string, error) {
	issuer := strings.TrimRight(conf.Cloud18IdentityURL, "/")
	if issuer == "" {
		return "", cloud18Refused("Cloud18 identity: no realm (cloud18-identity-url)")
	}
	if err := cloud18SafeURL("cloud18-identity-url", issuer); err != nil {
		return "", fmt.Errorf("Cloud18 identity: %w", err)
	}
	return issuer, nil
}

type cloud18IdentityToken struct {
	token, sub string
	expires    time.Time
}

// cloud18IdentityTokens caches the access token of each instance (identity file): every S3 mount
// and the vault share it until it nears its end. The map lock is never held during a request;
// fetch serializes the requests of one instance, so callers waiting on it take the token the
// first one got.
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

func cacheCloud18IdentityToken(key string, t cloud18IdentityToken) {
	cloud18IdentityTokens.Lock()
	if t.token == "" {
		delete(cloud18IdentityTokens.m, key)
	} else {
		cloud18IdentityTokens.m[key] = t
	}
	cloud18IdentityTokens.Unlock()
}

// Cloud18IdentityToken returns a valid access token of the account the instance is registered
// with and its sub claim (the folder name on the S3 SSO storages), got with the kept offline
// refresh token. With no login kept, or one the realm ended (revoked, past its lifetime), the
// instance signs in again with its registered account (cloud18RegisteredLogin).
func (conf *Config) Cloud18IdentityToken() (string, string, error) {
	key := conf.cloud18IdentityPath()
	if t, ok := cachedCloud18IdentityToken(key); ok {
		return t.token, t.sub, nil
	}
	issuer, err := conf.cloud18Issuer()
	if err != nil {
		return "", "", err
	}

	fetch := cloud18FetchLock(key)
	fetch.Lock()
	defer fetch.Unlock()
	if t, ok := cachedCloud18IdentityToken(key); ok {
		return t.token, t.sub, nil // got by the caller this one waited on
	}

	cloud18Sessions.Lock()
	s := conf.cloud18SessionLocked()
	login, ended := s.login, s.ended
	cloud18Sessions.Unlock()
	if ended != "" || login.RefreshToken == "" || login.Issuer != issuer {
		return conf.cloud18RegisteredLogin(issuer, s)
	}

	code, body, err := postCloud18Realm(issuer+"/protocol/openid-connect/token", url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {ConstCloud18IdentityClientID},
		"refresh_token": {login.RefreshToken},
	})
	if err != nil {
		conf.cloud18Log(LvlWarn, "the login of %s not refreshed, the realm is out of reach (tried again later): %s", login.User, err)
		return "", "", err
	}
	if code != http.StatusOK {
		detail := cloud18RealmError(body)
		err := cloud18Status("the realm", code, ": "+detail)
		if errors.Is(err, ErrCloud18Refused) {
			cloud18Sessions.Lock()
			if s.login.RefreshToken == login.RefreshToken {
				s.ended = detail
			}
			cloud18Sessions.Unlock()
			conf.cloud18Log(LvlWarn, "the realm ended the login of %s (%s): signing in again with the registered account", login.User, detail)
			return conf.cloud18RegisteredLogin(issuer, s)
		} else {
			conf.cloud18Log(LvlWarn, "the login of %s not refreshed, tried again later: %s", login.User, err)
		}
		return "", "", fmt.Errorf("Cloud18 identity: %w", err)
	}
	var a cloud18TokenAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		return "", "", errors.New("Cloud18 identity: the realm gave no token")
	}
	lifetime, err := a.lifetime()
	if err != nil {
		return "", "", err
	}
	sub, err := TokenSubject(a.AccessToken)
	if err != nil {
		return "", "", err
	}
	now := time.Now()
	cloud18Sessions.Lock()
	if s.login.RefreshToken == login.RefreshToken {
		changed := a.RefreshToken != "" && a.RefreshToken != login.RefreshToken
		if changed {
			s.login.RefreshToken = a.RefreshToken // a realm rotating refresh tokens
		}
		if end := a.sessionEnd(now); !end.IsZero() {
			s.login.Expires = end
		}
		s.refreshedAt = now
		if changed {
			if err := conf.writeCloud18Login(s.login); err != nil {
				cloud18Sessions.Unlock()
				return "", "", err
			}
		}
	}
	cloud18Sessions.Unlock()
	cacheCloud18IdentityToken(key, cloud18IdentityToken{token: a.AccessToken, sub: sub, expires: now.Add(lifetime)})
	return a.AccessToken, sub, nil
}

// Cloud18IdentityKeepAlive refreshes the login in the background when nothing did for
// cloud18IdentityKeepAlive: the realm's offline idle limit is never reached by a running
// instance. Cheap to call at every tick.
func (conf *Config) Cloud18IdentityKeepAlive() {
	key := conf.cloud18IdentityPath()
	user, password, registered := conf.cloud18Registered()
	cloud18Sessions.Lock()
	s := conf.cloud18SessionLocked()
	due := s.login.RefreshToken != "" && s.ended == "" && time.Since(s.refreshedAt) > cloud18IdentityKeepAlive
	// registered with no login yet (the first start of a release that keeps one): signed in now,
	// not at the first use of a storage
	due = due || (registered && s.login.RefreshToken == "" && s.refused != cloud18AccountFingerprint(user, password) &&
		time.Since(s.attemptedAt) > cloud18RegisteredRetry)
	if s.login.User != "" {
		user = s.login.User
	}
	cloud18Sessions.Unlock()
	if !due || !s.keepAlive.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.keepAlive.Store(false)
		cacheCloud18IdentityToken(key, cloud18IdentityToken{}) // a cached token would answer without the realm
		if _, sub, err := conf.Cloud18IdentityToken(); err == nil {
			conf.cloud18Log(LvlInfo, "login of %s (sub %s) refreshed", user, sub)
		}
	}()
}

// cloud18NoLoginKept: registered with no password kept and no login (registered before the
// registration made the login): registering again under the same URI makes it.
const cloud18NoLoginKept = "registered without its Cloud18 login: register this replication-manager again (same URI)"

// cloud18FetchLock serializes the requests of one instance (identity file) to the realm.
func cloud18FetchLock(key string) *sync.Mutex {
	cloud18IdentityTokens.Lock()
	defer cloud18IdentityTokens.Unlock()
	fetch, ok := cloud18IdentityTokens.fetch[key]
	if !ok {
		fetch = &sync.Mutex{}
		cloud18IdentityTokens.fetch[key] = fetch
	}
	return fetch
}

// KeepCloud18Login makes the instance's Cloud18 login with the account it is being registered with
// (the email and password the person just gave the registration, used for this sign-in only, never
// kept): the registration no longer keeps a password (#2159), so this is when the login is made.
func (conf *Config) KeepCloud18Login(user, password string) error {
	issuer, err := conf.cloud18Issuer()
	if err != nil {
		return err
	}
	fetch := cloud18FetchLock(conf.cloud18IdentityPath())
	fetch.Lock()
	defer fetch.Unlock()
	cloud18Sessions.Lock()
	s := conf.cloud18SessionLocked()
	cloud18Sessions.Unlock()
	_, _, err = conf.cloud18PasswordLogin(issuer, s, user, password)
	return err
}

// cloud18Registered is the account the instance is registered to Cloud18 with: its user and
// password (cloud18-gitlab-user, cloud18-gitlab-password), the realm's own since the accounts moved
// to it; ok false when not registered.
func (conf *Config) cloud18Registered() (string, string, bool) {
	if !conf.Cloud18 || conf.Cloud18GitUser == "" {
		return "", "", false
	}
	password := conf.GetDecryptedPassword("cloud18-gitlab-password", conf.Cloud18GitPassword)
	if password == "" || strings.HasPrefix(password, "hash_") {
		return "", "", false // no password, or one this instance's key cannot read
	}
	return conf.Cloud18GitUser, password, true
}

// cloud18AccountFingerprint names a registered account without its password.
func cloud18AccountFingerprint(user, password string) string {
	sum := sha256.Sum256([]byte(user + "\x00" + password))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// cloud18RegisteredLogin signs in with the account kept by the registration (an instance
// registered before #2159 keeps its password): cloud18PasswordLogin. Without one, the login made at
// the registration (KeepCloud18Login) is the only one; once it ends, the instance registers again.
// The caller holds the instance's fetch lock.
func (conf *Config) cloud18RegisteredLogin(issuer string, s *cloud18Session) (string, string, error) {
	user, password, ok := conf.cloud18Registered()
	if !ok {
		cloud18Sessions.Lock()
		ended := s.ended
		cloud18Sessions.Unlock()
		if ended != "" {
			return "", "", cloud18Refused("Cloud18 identity: the login ended (%s): register this replication-manager again", ended)
		}
		if conf.Cloud18 && conf.Cloud18GitUser != "" {
			return "", "", cloud18Refused("Cloud18 identity: %s", cloud18NoLoginKept)
		}
		return "", "", fmt.Errorf("%w (%w)", ErrCloud18IdentityNotSet, ErrCloud18Refused)
	}
	return conf.cloud18PasswordLogin(issuer, s, user, password)
}

// cloud18PasswordLogin signs in with user and password (the password grant of the realm's public
// client, offline_access) and keeps the login as the identity file: the refresh token is used from
// then on. An account the realm refuses is not asked again until it changes. The caller holds the
// instance's fetch lock.
func (conf *Config) cloud18PasswordLogin(issuer string, s *cloud18Session, user, password string) (string, string, error) {
	fingerprint := cloud18AccountFingerprint(user, password)
	cloud18Sessions.Lock()
	refused, why := s.refused == fingerprint, s.refusedWhy
	s.attemptedAt = time.Now()
	cloud18Sessions.Unlock()
	if refused {
		return "", "", cloud18Refused("Cloud18 identity: the realm refused the account %s is registered with (%s): register this replication-manager again", user, why)
	}
	if conf.cloud18EncryptionKey() == nil {
		return "", "", cloud18Refused("Cloud18 identity: this replication-manager has no encryption key to keep the login")
	}
	code, body, err := postCloud18Realm(issuer+"/protocol/openid-connect/token", url.Values{
		"grant_type": {"password"},
		"client_id":  {ConstCloud18IdentityClientID},
		"username":   {user},
		"password":   {password},
		"scope":      {cloud18IdentityScope},
	})
	if err != nil {
		conf.cloud18Log(LvlWarn, "no sign-in with the registered account %s, the realm is out of reach (tried again later): %s", user, err)
		return "", "", err
	}
	if code != http.StatusOK {
		detail := cloud18RealmError(body)
		err := cloud18Status("the realm", code, ": "+detail)
		if errors.Is(err, ErrCloud18Refused) {
			cloud18Sessions.Lock()
			s.refused, s.refusedWhy = fingerprint, detail
			cloud18Sessions.Unlock()
			conf.cloud18Log(LvlErr, "the realm refused the registered account %s (%s): action required, register this replication-manager again", user, detail)
		} else {
			conf.cloud18Log(LvlWarn, "no sign-in with the registered account %s, tried again later: %s", user, err)
		}
		return "", "", fmt.Errorf("Cloud18 identity: %w", err)
	}
	var a cloud18TokenAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		return "", "", errors.New("Cloud18 identity: the realm gave no token")
	}
	lifetime, err := a.lifetime()
	if err != nil {
		return "", "", err
	}
	if a.RefreshToken == "" {
		return "", "", errors.New("Cloud18 identity: the realm gave no offline token (offline_access)")
	}
	sub, err := TokenSubject(a.AccessToken)
	if err != nil {
		return "", "", err
	}
	now := time.Now()
	l := cloud18Login{Issuer: issuer, RefreshToken: a.RefreshToken, User: tokenClaim(a.AccessToken, "preferred_username"), Sub: sub, ConnectedAt: now, Expires: a.sessionEnd(now)}
	if l.User == "" {
		l.User = user
	}
	cloud18Sessions.Lock()
	defer cloud18Sessions.Unlock()
	if err := conf.writeCloud18Login(l); err != nil {
		return "", "", err
	}
	s.login, s.ended, s.refreshedAt, s.refused, s.refusedWhy = l, "", now, "", ""
	cacheCloud18IdentityToken(conf.cloud18IdentityPath(), cloud18IdentityToken{token: a.AccessToken, sub: sub, expires: now.Add(lifetime)})
	conf.cloud18Log(LvlInfo, "signed in with the registered account %s (sub %s), login kept in %s", l.User, sub, conf.cloud18IdentityPath())
	return a.AccessToken, sub, nil
}

// Cloud18IdentityStatus is the instance's Cloud18 login as the API shows it: never a token.
type Cloud18IdentityStatus struct {
	// none: not registered; registered: no login yet, made at the next use or keep-alive;
	// connected; refused: the realm refused the registered account, register again
	State       string     `json:"state"`
	User        string     `json:"user,omitempty"`
	Sub         string     `json:"sub,omitempty"`
	ConnectedAt *time.Time `json:"connectedAt,omitempty"`
	Expires     *time.Time `json:"expires,omitempty"`
	Reason      string     `json:"reason,omitempty"`
}

// HasCloud18Login says whether the instance has, or can make, its Cloud18 login on the realm it
// is set to: a login kept, or the account it is registered with (not refused by the realm).
// The login may since have ended: Cloud18IdentityToken says.
func (conf *Config) HasCloud18Login() bool {
	user, password, registered := conf.cloud18Registered()
	cloud18Sessions.Lock()
	defer cloud18Sessions.Unlock()
	s := conf.cloud18SessionLocked()
	if s.login.RefreshToken != "" && s.login.Issuer == strings.TrimRight(conf.Cloud18IdentityURL, "/") {
		return true
	}
	return registered && s.refused != cloud18AccountFingerprint(user, password)
}

// KeepCloud18LoginFile returns a function writing the identity file back as it is now, for a
// caller about to empty the working directory (a restore of the config from git); nil when there
// is no file.
func (conf *Config) KeepCloud18LoginFile() func() {
	path := conf.cloud18IdentityPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return func() {
		os.MkdirAll(filepath.Dir(path), 0700)
		os.WriteFile(path, data, 0600)
	}
}

// Cloud18IdentityStatus reads the login from memory (no request to the realm).
func (conf *Config) Cloud18IdentityStatus() Cloud18IdentityStatus {
	user, password, registered := conf.cloud18Registered()
	cloud18Sessions.Lock()
	defer cloud18Sessions.Unlock()
	s := conf.cloud18SessionLocked()
	st := Cloud18IdentityStatus{State: "none"}
	if registered {
		st.State, st.User = "registered", user
	}
	issuer := strings.TrimRight(conf.Cloud18IdentityURL, "/")
	if s.login.RefreshToken != "" && s.login.Issuer == issuer && s.ended == "" {
		st.State, st.User, st.Sub = "connected", s.login.User, s.login.Sub
		at := s.login.ConnectedAt
		st.ConnectedAt = &at
		if !s.login.Expires.IsZero() {
			end := s.login.Expires
			st.Expires = &end
		}
	}
	switch {
	case registered && s.refused == cloud18AccountFingerprint(user, password):
		st.State, st.Reason = "refused", s.refusedWhy
	case !registered && s.ended != "":
		st.State, st.Reason = "refused", s.ended
	case st.State == "none" && conf.Cloud18 && conf.Cloud18GitUser != "":
		st.State, st.User, st.Reason = "refused", conf.Cloud18GitUser, cloud18NoLoginKept
	}
	return st
}

// TokenSubject reads the sub claim of a JWT, the permanent id a storage names the folder after
// (the storage checks the signature; this side only needs the folder's name).
func TokenSubject(token string) (string, error) {
	sub := tokenClaim(token, "sub")
	if sub == "" && len(strings.Split(token, ".")) != 3 {
		return "", errors.New("Cloud18 identity: malformed token")
	}
	if sub == "" || sub == "." || sub == ".." || strings.ContainsAny(sub, "/\\") {
		return "", errors.New("Cloud18 identity: the token names no user")
	}
	return sub, nil
}

// tokenClaim reads a string claim of a JWT's payload, "" when the token or the claim is not there.
func tokenClaim(token, claim string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	v, _ := claims[claim].(string)
	return v
}
