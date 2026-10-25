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
// (ConstCloud18IdentityClientID) and the device authorization grant (RFC 8628). A person
// connects the instance once: repman shows a code and the realm's device page, the person logs
// in there from any browser and approves. repman keeps the person's offline refresh token,
// encrypted, in a file of its working directory that git never pushes (never sent to a peer:
// each peer connects with its own login), and gets its access tokens with it. The token carries
// the audience the Cloud18 services trust (cloud18-storage) and as sub the person's permanent
// GitLab user id, so an S3 SSO archive keeps the folder it had with the GitLab login. The S3 SSO
// storages and the Cloud18 vault (vault-auth = jwt) take that token.

const (
	// ConstCloud18IdentityURL is cloud18-identity-url's default, the Cloud18 realm's issuer.
	ConstCloud18IdentityURL = "https://identity.cloud18.io/realms/cloud18"
	// ConstCloud18IdentityClientID is the realm's public client every replication-manager logs
	// in with: device grant only, no secret, no redirect URI.
	ConstCloud18IdentityClientID = "replication-manager"
	// ConstCloud18GitLabProvider is the realm's GitLab identity provider kept for replication-manager:
	// it stores the person's GitLab token (scope api), given to repman at registration (#2159).
	ConstCloud18GitLabProvider = "gitlab-repman"
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
	// the login is reported to reconnect when the realm says it ends within this: below the
	// realm's offline idle limit (30 days, no maximum lifetime), so only a refresh missing for
	// weeks (or a maximum set on the realm) brings it there
	cloud18IdentityReconnectBefore = 14 * 24 * time.Hour
	// a device login waits for the person at most this long, whatever the realm says
	cloud18DeviceMaxWait = 15 * time.Minute
	// a GitLab token the CRM just gave is handed to the next callers for this long instead of
	// asking again: a second request would revoke the first token (same name)
	cloud18GitLabGrantReuse = 5 * time.Minute
	cloud18ErrorTextMax     = 200
)

// cloud18DeviceMinInterval is the shortest wait between two polls of a device login (tests lower it).
var cloud18DeviceMinInterval = 5 * time.Second

// ErrCloud18IdentityNotSet: nobody connected this instance to the Cloud18 identity yet.
var ErrCloud18IdentityNotSet = errors.New("Cloud18 identity: this replication-manager is not connected (Cloud18 settings, Connect)")

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

// cloud18Device is a device login waiting for the person.
type cloud18Device struct {
	login      Cloud18DeviceLogin
	deviceCode string
	verifier   string // PKCE (S256): the public client requires it
	interval   time.Duration
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
// refused it, and the device login in progress.
type cloud18Session struct {
	loaded      bool
	login       cloud18Login
	ended       string
	refreshedAt time.Time
	device      *cloud18Device
	deviceError string
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

// Cloud18IdentityToken returns a valid access token of the person who connected this instance
// and its sub claim (the folder name on the S3 SSO storages), got with the kept offline refresh
// token. A refusal of the realm (the login ended, revoked or past its lifetime) ends the login
// until someone connects the instance again.
func (conf *Config) Cloud18IdentityToken() (string, string, error) {
	key := conf.cloud18IdentityPath()
	if t, ok := cachedCloud18IdentityToken(key); ok {
		return t.token, t.sub, nil
	}
	issuer, err := conf.cloud18Issuer()
	if err != nil {
		return "", "", err
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

	cloud18Sessions.Lock()
	s := conf.cloud18SessionLocked()
	login, ended := s.login, s.ended
	cloud18Sessions.Unlock()
	if ended != "" {
		return "", "", cloud18Refused("Cloud18 identity: the login ended (%s): connect this replication-manager again", ended)
	}
	if login.RefreshToken == "" || login.Issuer != issuer {
		return "", "", fmt.Errorf("%w (%w)", ErrCloud18IdentityNotSet, ErrCloud18Refused)
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
			conf.cloud18Log(LvlErr, "the realm ended the login of %s (%s): action required, connect this replication-manager again", login.User, detail)
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
	cloud18Sessions.Lock()
	s := conf.cloud18SessionLocked()
	due := s.login.RefreshToken != "" && s.ended == "" && time.Since(s.refreshedAt) > cloud18IdentityKeepAlive
	user := s.login.User
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

// Cloud18DeviceLogin is what the person needs to connect the instance: the code to type on the
// realm's device page.
type Cloud18DeviceLogin struct {
	UserCode                string    `json:"userCode"`
	VerificationURI         string    `json:"verificationUri"`
	VerificationURIComplete string    `json:"verificationUriComplete,omitempty"`
	ExpiresAt               time.Time `json:"expiresAt"`
}

// StartCloud18DeviceLogin starts connecting the instance (the device authorization grant): the
// realm gives a code for the person, and repman polls the realm in the background until the
// person approves, refuses, or the code expires. A new start replaces a login still waiting.
func (conf *Config) StartCloud18DeviceLogin() (Cloud18DeviceLogin, error) {
	issuer, err := conf.cloud18Issuer()
	if err != nil {
		return Cloud18DeviceLogin{}, err
	}
	if conf.cloud18EncryptionKey() == nil {
		return Cloud18DeviceLogin{}, cloud18Refused("Cloud18 identity: this replication-manager has no encryption key to keep the login")
	}
	verifier, challenge, err := pkcePair()
	if err != nil {
		return Cloud18DeviceLogin{}, err
	}
	code, body, err := postCloud18Realm(issuer+"/protocol/openid-connect/auth/device", url.Values{
		"client_id":             {ConstCloud18IdentityClientID},
		"scope":                 {cloud18IdentityScope},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	})
	if err != nil {
		return Cloud18DeviceLogin{}, err
	}
	if code != http.StatusOK {
		return Cloud18DeviceLogin{}, fmt.Errorf("Cloud18 identity: %w", cloud18Status("the realm", code, ": "+cloud18RealmError(body)))
	}
	var a struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.Unmarshal(body, &a); err != nil || a.DeviceCode == "" || a.UserCode == "" || a.VerificationURI == "" {
		return Cloud18DeviceLogin{}, errors.New("Cloud18 identity: the realm gave no device code")
	}
	// the page the person opens comes from the realm: only the realm's own https pages
	for _, u := range []string{a.VerificationURI, a.VerificationURIComplete} {
		if u != "" && !strings.HasPrefix(u, issuer+"/") {
			return Cloud18DeviceLogin{}, cloud18Refused("Cloud18 identity: the realm gave a device page outside the realm")
		}
	}
	wait := time.Duration(a.ExpiresIn) * time.Second
	if wait <= 0 || wait > cloud18DeviceMaxWait {
		wait = cloud18DeviceMaxWait
	}
	interval := time.Duration(a.Interval) * time.Second
	if interval < cloud18DeviceMinInterval {
		interval = cloud18DeviceMinInterval
	}
	d := &cloud18Device{
		login:      Cloud18DeviceLogin{UserCode: a.UserCode, VerificationURI: a.VerificationURI, VerificationURIComplete: a.VerificationURIComplete, ExpiresAt: time.Now().Add(wait)},
		deviceCode: a.DeviceCode,
		verifier:   verifier,
		interval:   interval,
	}
	cloud18Sessions.Lock()
	s := conf.cloud18SessionLocked()
	s.device, s.deviceError = d, ""
	cloud18Sessions.Unlock()
	conf.cloud18Log(LvlInfo, "device login started: open %s and enter the code %s before %s", d.login.VerificationURI, d.login.UserCode, d.login.ExpiresAt.Format("15:04:05"))
	go conf.pollCloud18Device(issuer, s, d)
	return d.login, nil
}

// pollCloud18Device asks the realm for the person's tokens until the person approves (the login
// is kept), refuses or the code expires (the error is kept for the status), or a new login or a
// disconnect replaced this one.
func (conf *Config) pollCloud18Device(issuer string, s *cloud18Session, d *cloud18Device) {
	interval := d.interval
	unreachable := false
	stop := func(e string) {
		cloud18Sessions.Lock()
		current := s.device == d
		if current {
			s.device, s.deviceError = nil, e
		}
		cloud18Sessions.Unlock()
		if current {
			conf.cloud18Log(LvlWarn, "device login not completed: %s", e)
		}
	}
	for {
		time.Sleep(interval)
		cloud18Sessions.Lock()
		current := s.device == d
		cloud18Sessions.Unlock()
		if !current {
			return
		}
		if time.Now().After(d.login.ExpiresAt) {
			stop("the code expired before it was approved")
			return
		}
		code, body, err := postCloud18Realm(issuer+"/protocol/openid-connect/token", url.Values{
			"grant_type":    {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code":   {d.deviceCode},
			"client_id":     {ConstCloud18IdentityClientID},
			"code_verifier": {d.verifier},
		})
		if err != nil || code >= 500 || code == http.StatusTooManyRequests {
			if !unreachable {
				unreachable = true
				conf.cloud18Log(LvlWarn, "the realm is out of reach while the device login waits (asked again until the code expires): %v %d", err, code)
			}
			continue // the realm out of reach: asked again until the code expires
		}
		if code != http.StatusOK {
			var e struct {
				Error string `json:"error"`
			}
			json.Unmarshal(body, &e)
			switch e.Error {
			case "authorization_pending":
				continue
			case "slow_down":
				interval += 5 * time.Second
				continue
			case "access_denied":
				stop("the login was refused")
			case "expired_token":
				stop("the code expired before it was approved")
			default:
				stop("the realm answered " + cloud18RealmError(body))
			}
			return
		}
		if err := conf.keepCloud18DeviceLogin(issuer, s, d, body); err != nil {
			stop(err.Error())
		}
		return
	}
}

func (conf *Config) keepCloud18DeviceLogin(issuer string, s *cloud18Session, d *cloud18Device, body []byte) error {
	var a cloud18TokenAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		return errors.New("the realm gave no token")
	}
	lifetime, err := a.lifetime()
	if err != nil {
		return err
	}
	if a.RefreshToken == "" {
		return errors.New("the realm gave no offline token (offline_access)")
	}
	sub, err := TokenSubject(a.AccessToken)
	if err != nil {
		return err
	}
	now := time.Now()
	l := cloud18Login{Issuer: issuer, RefreshToken: a.RefreshToken, User: tokenClaim(a.AccessToken, "preferred_username"), Sub: sub, ConnectedAt: now, Expires: a.sessionEnd(now)}
	cloud18Sessions.Lock()
	defer cloud18Sessions.Unlock()
	if s.device != d {
		return nil // replaced meanwhile: the newer login decides
	}
	if err := conf.writeCloud18Login(l); err != nil {
		return err
	}
	s.login, s.ended, s.refreshedAt, s.device, s.deviceError = l, "", now, nil, ""
	cacheCloud18IdentityToken(conf.cloud18IdentityPath(), cloud18IdentityToken{token: a.AccessToken, sub: sub, expires: now.Add(lifetime)})
	conf.cloud18Log(LvlInfo, "connected as %s (sub %s), login kept in %s", l.User, sub, conf.cloud18IdentityPath())
	return nil
}

// DisconnectCloud18Identity forgets the login (the identity file and the cached token) and ends
// the offline session at the realm, best effort; a device login waiting is dropped.
func (conf *Config) DisconnectCloud18Identity() error {
	path := conf.cloud18IdentityPath()
	cloud18Sessions.Lock()
	s := conf.cloud18SessionLocked()
	token := s.login.RefreshToken
	s.login, s.ended, s.device, s.deviceError, s.refreshedAt = cloud18Login{}, "", nil, "", time.Time{}
	cloud18Sessions.Unlock()
	cacheCloud18IdentityToken(path, cloud18IdentityToken{})
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Cloud18 identity: %w", err)
	}
	if token != "" {
		if issuer, err := conf.cloud18Issuer(); err == nil {
			code, _, err := postCloud18Realm(issuer+"/protocol/openid-connect/revoke", url.Values{
				"client_id":       {ConstCloud18IdentityClientID},
				"token":           {token},
				"token_type_hint": {"refresh_token"},
			})
			if err != nil || code != http.StatusOK {
				conf.cloud18Log(LvlWarn, "login forgotten here, the realm did not confirm its end (%v %d): end it in the realm", err, code)
			} else {
				conf.cloud18Log(LvlInfo, "login forgotten and ended in the realm")
			}
		}
	}
	return nil
}

// Cloud18IdentityStatus is the instance's Cloud18 login as the API shows it: never a token.
type Cloud18IdentityStatus struct {
	// none: nobody connected it; pending: a device login waits for the person; connected;
	// reconnect: the login ended or ends soon, someone must connect again
	State       string              `json:"state"`
	User        string              `json:"user,omitempty"`
	Sub         string              `json:"sub,omitempty"`
	ConnectedAt *time.Time          `json:"connectedAt,omitempty"`
	Expires     *time.Time          `json:"expires,omitempty"`
	Reason      string              `json:"reason,omitempty"`
	Device      *Cloud18DeviceLogin `json:"device,omitempty"`
}

// HasCloud18Login says whether someone connected this instance to the realm it is set to
// (the login may since have ended: Cloud18IdentityToken says).
func (conf *Config) HasCloud18Login() bool {
	cloud18Sessions.Lock()
	defer cloud18Sessions.Unlock()
	s := conf.cloud18SessionLocked()
	return s.login.RefreshToken != "" && s.login.Issuer == strings.TrimRight(conf.Cloud18IdentityURL, "/")
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
	cloud18Sessions.Lock()
	defer cloud18Sessions.Unlock()
	s := conf.cloud18SessionLocked()
	st := Cloud18IdentityStatus{State: "none", Reason: s.deviceError}
	issuer := strings.TrimRight(conf.Cloud18IdentityURL, "/")
	if s.login.RefreshToken != "" && s.login.Issuer == issuer {
		st.State, st.User, st.Sub = "connected", s.login.User, s.login.Sub
		at := s.login.ConnectedAt
		st.ConnectedAt = &at
		if !s.login.Expires.IsZero() {
			end := s.login.Expires
			st.Expires = &end
			if time.Until(end) < cloud18IdentityReconnectBefore {
				st.State, st.Reason = "reconnect", "the login ends on "+end.Format("2006-01-02")
			}
		}
	}
	if s.ended != "" {
		st.State, st.Reason = "reconnect", s.ended
	}
	if s.device != nil {
		d := s.device.login
		st.State, st.Device = "pending", &d
	}
	return st
}

// ErrCloud18GitLabNotLinked: the person's Cloud18 account has no GitLab account linked through
// ConstCloud18GitLabProvider; registering a repman needs one.
var ErrCloud18GitLabNotLinked = errors.New("your Cloud18 account has no GitLab account linked: link one (Cloud18 account, Linked accounts, GitLab), then register again")

// Cloud18LinkAccountURL is the realm page where the person links a GitLab account.
func (conf *Config) Cloud18LinkAccountURL() string {
	return strings.TrimRight(conf.Cloud18IdentityURL, "/") + "/account/account-security/linked-accounts"
}

// Cloud18GitLabBrokerToken returns the GitLab token the realm keeps for the person who connected
// this instance (identity broker, ConstCloud18GitLabProvider): only for the GitLab calls of a
// registration, never stored. The realm refusing it means no GitLab account is linked.
func (conf *Config) Cloud18GitLabBrokerToken() (string, error) {
	issuer, err := conf.cloud18Issuer()
	if err != nil {
		return "", err
	}
	access, _, err := conf.Cloud18IdentityToken()
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodGet, issuer+"/broker/"+ConstCloud18GitLabProvider+"/token", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := (&http.Client{Timeout: cloud18IdentityHTTPTimeout}).Do(req)
	if err != nil {
		return "", fmt.Errorf("Cloud18 identity: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound:
		conf.cloud18Log(LvlWarn, "no GitLab account linked to the Cloud18 login (%s)", cloud18ErrorText(string(body)))
		return "", fmt.Errorf("%w (%w)", ErrCloud18GitLabNotLinked, ErrCloud18Refused)
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("Cloud18 identity: GitLab token: %w", cloud18Status("the realm", resp.StatusCode, ": "+cloud18ErrorText(string(body))))
	}
	var t struct {
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(body, &t) != nil || t.AccessToken == "" {
		if v, err := url.ParseQuery(string(body)); err == nil {
			t.AccessToken = v.Get("access_token")
		}
	}
	if t.AccessToken == "" {
		return "", errors.New("Cloud18 identity: the realm gave no GitLab token")
	}
	return t.AccessToken, nil
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
