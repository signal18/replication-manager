// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	t.Cleanup(cloud18ResumeLimiter.resetForTests)
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

// the fake realm's refresh tokens are "rt-<email>": long emails, as a real refresh token is long
const (
	resumePerson = "person.resume.case@acme.example"
	resumeOther  = "other.resume.case@acme.example"
	resumeRotate = "rotating.resume.case@acme.example"
)

// issued is a Cloud18 session issued as the sign-in paths issue it: its end recorded with its login.
func issued(t *testing.T, repman *ReplicationManager, email, refresh string) string {
	t.Helper()
	raw, err := repman.issueCloud18Session(repman.cloud18SSOSessionInfo(config.Cloud18User{Email: email}, repman.Conf.GetEncryptedString(refresh), "a"), "", refresh)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// forgeExp rewrites the exp of a session without signing it again (the resume reads it unverified).
func forgeExp(t *testing.T, raw string, exp time.Time) string {
	t.Helper()
	parts := strings.Split(raw, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	claims["exp"] = exp.Unix()
	payload, _ = json.Marshal(claims)
	parts[1] = base64.RawURLEncoding.EncodeToString(payload)
	return strings.Join(parts, ".")
}

// #2341: a Cloud18 session the restart left unverifiable (a new signing key) comes back when this
// instance issued it and the realm still accepts its login: a Member session of the realm's person,
// its Cloud18 login kept, ending when the issued session ends.
func TestCloud18SessionResume(t *testing.T) {
	_, repman := resumeRepman(t)
	refresh := "rt-" + resumePerson
	old := issued(t, repman, resumePerson, refresh)
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
	if info["User"] != resumePerson || info["AuthType"] != "SSO" || info["Role"] != "Member" || repman.Conf.GetDecryptedPassword("cloud18-login", info["Cloud18Login"]) != refresh {
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

// #2341 review: the old session is read without its signature, so its exp is never trusted: a
// far-future exp gives a session ending when the issued one ends; an issued session already ended,
// its exp forged into the future, is refused.
func TestCloud18SessionResumeForgedEnd(t *testing.T) {
	_, repman := resumeRepman(t)
	refresh := "rt-" + resumePerson
	old := issued(t, repman, resumePerson, refresh)
	_, issuedEnd, _ := sessionOf(t, repman, old)

	repman.initKeys()
	code, session := resumeCall(repman, forgeExp(t, old, time.Now().Add(10*365*24*time.Hour)))
	if code != http.StatusOK {
		t.Fatalf("a real login resumes: %d", code)
	}
	_, end, err := sessionOf(t, repman, session)
	if err != nil || end.After(issuedEnd) {
		t.Fatalf("a forged far exp is capped at the issued end: %v > %v (%v)", end, issuedEnd, err)
	}

	// the issued session has ended: its exp forged into the future does not bring it back
	config.RecordCloud18SessionEnd(refresh, time.Time{}) // (no effect: an end is only moved later)
	config.ForgetCloud18Logins()
	config.RecordCloud18SessionEnd(refresh, time.Now().Add(-time.Minute))
	if code, _ := resumeCall(repman, forgeExp(t, old, time.Now().Add(time.Hour))); code != http.StatusUnauthorized {
		t.Fatalf("an ended session with a forged exp: 401, got %d", code)
	}
}

// #2341: what is refused, so the person signs in again: no bearer, an expired session, a local
// session, a login not encrypted by this instance, a login the realm refuses, another person's
// login, a login this instance issued no session for (never asking the realm), a short login, a
// logged-out one. A forged role or name is never carried over.
func TestCloud18SessionResumeRefusals(t *testing.T) {
	f, repman := resumeRepman(t)
	person := config.Cloud18User{Email: resumePerson}
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
	revoked := "revoked-refresh-token-of-a-person-0123456789"
	config.RecordCloud18SessionEnd(revoked, time.Now().Add(time.Hour))
	otherRefresh := "rt-" + resumeOther
	config.RecordCloud18SessionEnd(otherRefresh, time.Now().Add(time.Hour))
	cases := map[string]string{
		"no bearer":        "",
		"not a token":      "not-a-jwt",
		"expired":          mk(repman.cloud18SSOSessionInfo(person, repman.Conf.GetEncryptedString("rt-"+resumePerson), "a"), -time.Minute),
		"local session":    mk(local, time.Hour),
		"login in clear":   mk(repman.cloud18SSOSessionInfo(person, "rt-"+resumePerson, "a"), time.Hour),
		"realm refuses":    mk(repman.cloud18SSOSessionInfo(person, repman.Conf.GetEncryptedString(revoked), "a"), time.Hour),
		"another's login":  mk(repman.cloud18SSOSessionInfo(person, repman.Conf.GetEncryptedString(otherRefresh), "a"), time.Hour),
		"no Cloud18 login": mk(repman.cloud18SSOSessionInfo(person, "", "a"), time.Hour),
		"short login":      mk(repman.cloud18SSOSessionInfo(person, repman.Conf.GetEncryptedString("rt-x@a.b"), "a"), time.Hour),
	}
	repman.initKeys()
	for name, raw := range cases {
		if code, session := resumeCall(repman, raw); code != http.StatusUnauthorized || session != "" {
			t.Errorf("%s: 401 and no session, got %d %q", name, code, session)
		}
	}

	// a login this instance never issued a session for: refused without asking the realm
	cloud18ResumeLimiter.resetForTests() // the cases above used the burst of this address
	f.mu.Lock()
	before := f.refreshes
	f.mu.Unlock()
	never := mk(repman.cloud18SSOSessionInfo(person, repman.Conf.GetEncryptedString("rt-never.issued.here@acme.example"), "a"), time.Hour)
	if code, _ := resumeCall(repman, never); code != http.StatusUnauthorized {
		t.Fatalf("a login never issued here: 401, got %d", code)
	}
	f.mu.Lock()
	after := f.refreshes
	f.mu.Unlock()
	if after != before {
		t.Fatalf("a login never issued here never reaches the realm: %d refreshes", after-before)
	}

	// a forged role and name next to a real, issued login: the new session is the realm's person
	refresh := "rt-" + resumePerson
	config.RecordCloud18SessionEnd(refresh, time.Now().Add(time.Hour))
	forged := mk(struct {
		Name    string
		Role    string
		Email   string `json:"email"`
		Profile string `json:"profile"`
		Cloud18 string `json:"cloud18"`
	}{"admin", "admin", resumePerson, repman.Conf.OAuthProvider, repman.Conf.GetEncryptedString(refresh)}, time.Hour)
	repman.initKeys()
	code, session := resumeCall(repman, forged)
	if code != http.StatusOK {
		t.Fatalf("a real login resumes: %d", code)
	}
	info, _, err := sessionOf(t, repman, session)
	if err != nil || info["User"] != resumePerson || info["Role"] != "Member" {
		t.Fatalf("the forged role and name are not carried over: %+v %v", info, err)
	}

	// logged out: the login is dropped, its session never resumes
	if err := repman.Conf.Cloud18LoginEnd(refresh); err != nil {
		t.Fatalf("logout: %v", err)
	}
	repman.initKeys()
	if code, _ := resumeCall(repman, forged); code != http.StatusUnauthorized {
		t.Fatalf("a logged-out session: 401, got %d", code)
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
	refresh := "rt-" + resumeRotate
	old := issued(t, repman, resumeRotate, refresh)
	path := filepath.Join(repman.Conf.WorkingDir, "cloud18-logins.json")
	_ = os.Remove(path)                                                            // the issued session's batch write may not have run: only the rotation counts
	if repman.cloud18TokenFor("", repman.Conf.GetEncryptedString(refresh)) == "" { // the realm rotates
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

	// without the kept logins: nothing recorded, the first refresh token revoked: no resume
	config.ForgetCloud18Logins()
	repman.initKeys()
	if code, _ := resumeCall(repman, old); code != http.StatusUnauthorized {
		t.Fatalf("without the kept logins: 401, got %d", code)
	}
}

// #2341 review: the resume asks the realm, so one address is bounded (a burst of 10, then 429).
func TestCloud18SessionResumeRateLimited(t *testing.T) {
	_, repman := resumeRepman(t)
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
