// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/golang-jwt/jwt/v5/request"
	"github.com/signal18/replication-manager/config"
)

func resumeRepman(t *testing.T) (*registerFakes, *ReplicationManager) {
	t.Helper()
	config.ForgetCloud18Logins()
	t.Cleanup(config.ForgetCloud18Logins)
	t.Cleanup(config.StopKeepingCloud18Logins)
	f, issuer := newRegisterFakes(t)
	repman := loginRepman(t, issuer)
	repman.Conf.Cloud18LoginResume = true
	cloud18ResumeLimiter.resetForTests()
	return f, repman
}

// resumeCall posts the bearer to the resume route: the status and the new session.
func resumeCall(repman *ReplicationManager, bearer string) (int, string) {
	req := httptest.NewRequest(http.MethodPost, cloud18ResumePath, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	repman.handlerMuxCloud18SessionResume(rec, req)
	var body AuthTokenWithUpgrade
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body.Token
}

// sessionOf verifies a session with the current key and gives its user info and end.
func sessionOf(t *testing.T, repman *ReplicationManager, raw string) (map[string]string, time.Time, error) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/monitor", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	token, err := request.ParseFromRequest(req, request.AuthorizationHeaderExtractor, func(*jwt.Token) (interface{}, error) {
		return jwt.ParseRSAPublicKeyFromPEM(verificationKey)
	})
	if err != nil {
		return nil, time.Time{}, err
	}
	exp, _ := token.Claims.GetExpirationTime()
	info, err := repman.GetUserInfoMap(token)
	return info, exp.Time, err
}

// #2341: a Cloud18 session the restart left unverifiable (a new signing key) comes back when the
// realm still accepts its login: a Member session of the realm's person, its Cloud18 login kept,
// ending no later than the old one.
func TestCloud18SessionResume(t *testing.T) {
	_, repman := resumeRepman(t)
	claim := repman.Conf.GetEncryptedString("rt-person@acme.example")
	old, err := repman.issueJWT(repman.cloud18SSOSessionInfo(config.Cloud18User{Email: "person@acme.example", Name: "Person"}, claim, "old-access"), "")
	if err != nil {
		t.Fatal(err)
	}
	_, oldEnd, err := sessionOf(t, repman, old)
	if err != nil {
		t.Fatalf("the old session verifies before the restart: %v", err)
	}

	repman.initKeys() // the restart: a new signing key
	if _, _, err := sessionOf(t, repman, old); err == nil {
		t.Fatal("after the restart the old session no longer verifies")
	}
	code, session := resumeCall(repman, old)
	if code != http.StatusOK || session == "" {
		t.Fatalf("resumed: %d %q", code, session)
	}
	info, end, err := sessionOf(t, repman, session)
	if err != nil {
		t.Fatalf("the new session verifies with the new key: %v", err)
	}
	if info["User"] != "person@acme.example" || info["AuthType"] != "SSO" || info["Role"] != "Member" || info["Cloud18Login"] != claim {
		t.Fatalf("the new session: %+v", info)
	}
	if end.After(oldEnd) {
		t.Fatalf("a resume never makes a session last longer: %v > %v", end, oldEnd)
	}
	if repman.cloud18TokenFor(info["Cloud18Token"], info["Cloud18Login"]) == "" {
		t.Fatal("the new session acts on the other instances (a realm token)")
	}

	repman.Conf.Cloud18LoginResume = false
	if code, _ := resumeCall(repman, old); code != http.StatusNotFound {
		t.Fatalf("cloud18-login-resume off: 404, got %d", code)
	}
}

// #2341: what is refused, so the person signs in again: no bearer, an expired session, a local
// session, a login not encrypted by this instance, a login the realm refuses, another person's
// login. A forged role or name is never carried over.
func TestCloud18SessionResumeRefusals(t *testing.T) {
	_, repman := resumeRepman(t)
	person := config.Cloud18User{Email: "person@acme.example", Name: "Person"}
	mk := func(info interface{}, lifetime time.Duration) string {
		raw, err := repman.issueJWTFor(info, "", lifetime)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	local := struct {
		Name     string
		Role     string
		Password string
	}{"admin", "admin", repman.Conf.GetEncryptedString("pw")}
	cases := map[string]string{
		"no bearer":        "",
		"not a token":      "not-a-jwt",
		"expired":          mk(repman.cloud18SSOSessionInfo(person, repman.Conf.GetEncryptedString("rt-person@acme.example"), "a"), -time.Minute),
		"local session":    mk(local, time.Hour),
		"login in clear":   mk(repman.cloud18SSOSessionInfo(person, "rt-person@acme.example", "a"), time.Hour),
		"realm refuses":    mk(repman.cloud18SSOSessionInfo(person, repman.Conf.GetEncryptedString("revoked"), "a"), time.Hour),
		"another's login":  mk(repman.cloud18SSOSessionInfo(person, repman.Conf.GetEncryptedString("rt-other@acme.example"), "a"), time.Hour),
		"no Cloud18 login": mk(repman.cloud18SSOSessionInfo(person, "", "a"), time.Hour),
	}
	repman.initKeys()
	for name, raw := range cases {
		if code, session := resumeCall(repman, raw); code != http.StatusUnauthorized || session != "" {
			t.Errorf("%s: 401 and no session, got %d %q", name, code, session)
		}
	}

	// a forged role and name next to a real login: the new session is the realm's person, a Member
	forged := mk(struct {
		Name    string
		Role    string
		Email   string `json:"email"`
		Profile string `json:"profile"`
		Cloud18 string `json:"cloud18"`
	}{"admin", "admin", "person@acme.example", repman.Conf.OAuthProvider, repman.Conf.GetEncryptedString("rt-person@acme.example")}, time.Hour)
	repman.initKeys()
	code, session := resumeCall(repman, forged)
	if code != http.StatusOK {
		t.Fatalf("a real login resumes: %d", code)
	}
	info, _, err := sessionOf(t, repman, session)
	if err != nil || info["User"] != "person@acme.example" || info["Role"] != "Member" {
		t.Fatalf("the forged role and name are not carried over: %+v %v", info, err)
	}
}

// #2341: with a realm rotating refresh tokens, the first refresh token (the one the session
// carries) is revoked once used; the kept logins give the newest one after the restart, so the
// session still resumes. Without them it would not.
func TestCloud18SessionResumeRotatingRealm(t *testing.T) {
	f, repman := resumeRepman(t)
	f.mu.Lock()
	f.rotate = true
	f.mu.Unlock()
	if _, err := repman.Conf.LoadCloud18Logins(); err != nil {
		t.Fatal(err)
	}
	claim := repman.Conf.GetEncryptedString("rt-rotating@acme.example")
	old, _ := repman.issueJWT(repman.cloud18SSOSessionInfo(config.Cloud18User{Email: "rotating@acme.example"}, claim, "a"), "")
	path := filepath.Join(repman.Conf.WorkingDir, "cloud18-logins.json")
	if repman.cloud18TokenFor("", claim) == "" { // the login in use: the realm rotated its refresh token
		t.Fatal("the login gives a token before the restart")
	}
	// a rotation is written at once (no shutdown flush on a hard stop), well before the 5 s batch
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the rotated refresh token is not written at once")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// the restart: an empty cache read back from the file, a new key
	config.ForgetCloud18Logins()
	if n, err := repman.Conf.LoadCloud18Logins(); err != nil || n != 1 {
		t.Fatalf("the login comes back: %d %v", n, err)
	}
	repman.initKeys()
	if code, session := resumeCall(repman, old); code != http.StatusOK || session == "" {
		t.Fatalf("the session resumes with the newest refresh token: %d", code)
	}

	// without the kept logins, the first refresh token is revoked: no resume
	config.ForgetCloud18Logins()
	repman.initKeys()
	if code, _ := resumeCall(repman, old); code != http.StatusUnauthorized {
		t.Fatalf("without the kept logins a rotating realm refuses: %d", code)
	}
}

// #2341 review: the resume asks the realm, so one address is bounded (a burst of 10, then 429).
func TestCloud18SessionResumeRateLimited(t *testing.T) {
	_, repman := resumeRepman(t)
	cloud18ResumeLimiter.resetForTests()
	t.Cleanup(cloud18ResumeLimiter.resetForTests)
	limited := false
	for i := 0; i < 30; i++ {
		if code, _ := resumeCall(repman, "not-a-jwt"); code == http.StatusTooManyRequests {
			limited = true
			if i < 10 {
				t.Fatalf("limited after %d calls, the burst is 10", i)
			}
			break
		}
	}
	if !limited {
		t.Fatal("30 resumes from one address in a row: limited")
	}
}
