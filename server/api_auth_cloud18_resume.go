// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/signal18/replication-manager/config"
)

// Resuming a Cloud18 session after a restart (#2341). The sessions repman issues are signed with a
// key made at each start, so a restart leaves every session unverifiable while the person's realm
// session lives on. A session of a Cloud18 login carries that login (its refresh token, encrypted
// with this instance's key): the realm, asked again, says whether the login still holds, and a new
// session is given only then.
//
// Nothing of the old session is trusted but what this instance recorded and the realm confirms:
// - the person is the one of the realm's token, the role is Member as at any Cloud18 sign-in
//   (cloud18SSOSessionInfo): the old session's role and name are not read;
// - its login must decrypt with this instance's key, be one this instance issued a session for
//   (Cloud18SessionEnd: else refused without asking the realm) and be accepted by the realm;
// - its email must be the realm's;
// - its end is the one this instance recorded when it issued the session (issueCloud18Session),
//   never later: the old session is read without its signature, its exp could be forged.
// A logout drops the login (Cloud18LoginEnd), so a logged-out session never resumes.

const cloud18ResumePath = "/api/auth/cloud18/resume"

// sessionIssuer is the issuer of the sessions repman signs (issueJWTFor and the resume check).
const sessionIssuer = "https://api.replication-manager.signal18.io"

// cloud18ResumeLimiter bounds the resumes of one address (each may ask the realm): after a restart
// each person resumes once, a burst of 10 covers an office behind one address.
var cloud18ResumeLimiter = newIPRateLimiter(30, 10, 15*time.Minute, 128)

var errCloud18ResumeRefused = errors.New("this session cannot be resumed: sign in again")

// cloud18ResumeMinLogin is the shortest refresh token a resume takes: a realm's refresh tokens are
// long JWTs, a short "decryption" is another key's bytes (the cipher is not authenticated).
const cloud18ResumeMinLogin = 32

// issueCloud18Session issues a session carrying a Cloud18 login and records its end with the login
// (#2341): the end a resume after a restart is bounded by. refresh is the login's first refresh
// token ("" when the session carries none).
func (repman *ReplicationManager) issueCloud18Session(userInfo interface{}, gitlabToken, refresh string) (string, error) {
	lifetime := time.Hour * time.Duration(repman.Conf.TokenTimeout)
	session, err := repman.issueJWTFor(userInfo, gitlabToken, lifetime)
	if err == nil && refresh != "" {
		config.RecordCloud18SessionEnd(refresh, time.Now().Add(lifetime))
	}
	return session, err
}

// cloud18ResumableSession reads, without its signature, what a session gives a resume: its Cloud18
// login, its email and its end. An expired session, a local (password) session, a session without a
// Cloud18 login or a token of another shape is refused.
func (repman *ReplicationManager) cloud18ResumableSession(raw string) (login, email string, end time.Time, err error) {
	token, _, err := jwt.NewParser(jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()})).ParseUnverified(raw, jwt.MapClaims{})
	if err != nil {
		return "", "", end, errCloud18ResumeRefused
	}
	claims, _ := token.Claims.(jwt.MapClaims)
	if iss, _ := claims["iss"].(string); iss != sessionIssuer {
		return "", "", end, errCloud18ResumeRefused
	}
	exp, _ := claims["exp"].(float64)
	if exp > float64(time.Now().Add(100*365*24*time.Hour).Unix()) {
		exp = float64(time.Now().Add(100 * 365 * 24 * time.Hour).Unix()) // a forged huge exp: capped by the record anyway
	}
	end = time.Unix(int64(exp), 0)
	if exp <= 0 || !time.Now().Before(end) {
		return "", "", end, errors.New("the session has expired: sign in again")
	}
	info, _ := claims["CustomUserInfo"].(map[string]interface{})
	profile, _ := info["profile"].(string)
	email, _ = info["email"].(string)
	login, _ = info["cloud18"].(string)
	if repman.Conf.OAuthProvider == "" || !strings.Contains(profile, repman.Conf.OAuthProvider) || email == "" || login == "" {
		return "", "", end, errors.New("not a Cloud18 session: sign in again")
	}
	return login, email, end, nil
}

// handlerMuxCloud18SessionResume gives a new session for one a restart left unverifiable.
// @Summary Resume a Cloud18 session after a restart
// @Description With the bearer of a Cloud18 session issued before this replication-manager restarted, gives a new session when the session has not expired and the Cloud18 realm still accepts its login (#2341). The new session is built from the realm's answer (a Member, the realm's person) and ends when the old one would have. Refused (401) for a local session, an expired one, or a login the realm refuses: the person signs in again. 404 when cloud18-login-resume is off. Public.
// @Tags Auth
// @Produce json
// @Param Authorization header string true "the old session's bearer (Bearer <token>)"
// @Success 200 {object} AuthTokenWithUpgrade "the new session"
// @Failure 401 {string} string "sign in again"
// @Failure 429 {string} string "too many resumes from this address"
// @Failure 404 {string} string "cloud18-login-resume is off"
// @Router /api/auth/cloud18/resume [post]
func (repman *ReplicationManager) handlerMuxCloud18SessionResume(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if !repman.Conf.Cloud18LoginResume {
		http.Error(w, "Resuming Cloud18 sessions is off (cloud18-login-resume)", http.StatusNotFound)
		return
	}
	// the person's address, behind the gateway too (X-Forwarded-For from a trusted proxy), as signup
	if !cloud18ResumeLimiter.allow(signupClientIP(r), time.Now()) {
		http.Error(w, "Too many requests", http.StatusTooManyRequests)
		return
	}
	session, u, err := repman.cloud18ResumeSession(bearerOf(r))
	if err != nil {
		// the detail (the realm's answer) in the security log only: the caller is not signed in
		repman.logCloud18Refusal("api_auth_failure", u, r.RemoteAddr, "Cloud18 session not resumed: "+err.Error())
		http.Error(w, errCloud18ResumeRefused.Error(), http.StatusUnauthorized)
		return
	}
	if repman.Conf.MonitoringLogAPILogin && !repman.isAPILoginSilenced(u) {
		repman.logSecurityEvent("api_login_success", u, r.RemoteAddr, "Cloud18 session resumed after a restart (the realm accepted its login)")
	}
	repman.jsonResponse(AuthTokenWithUpgrade{Token: session}, w)
}

// cloud18ResumeSession is the resume itself: the new session and the person, or why not. The
// person ("" when unknown) is for the security log.
func (repman *ReplicationManager) cloud18ResumeSession(raw string) (string, string, error) {
	if raw == "" {
		return "", "", errCloud18ResumeRefused
	}
	login, email, end, err := repman.cloud18ResumableSession(raw)
	if err != nil {
		return "", email, err
	}
	refresh, err := repman.Conf.DecryptCloud18LoginClaim(login)
	if err != nil || len(refresh) < cloud18ResumeMinLogin {
		return "", email, errors.New("the session's Cloud18 login is not one of this instance: sign in again")
	}
	recorded, ok := config.Cloud18SessionEnd(refresh)
	if !ok {
		return "", email, errors.New("no session of this Cloud18 login was issued here (or it logged out): sign in again")
	}
	if recorded.Before(end) {
		end = recorded // the old session's exp is unverified: never later than what was issued
	}
	if !time.Now().Before(end) {
		return "", email, errors.New("the session has expired: sign in again")
	}
	access, err := repman.Conf.Cloud18LoginAccess(refresh)
	if err != nil {
		return "", email, errors.New("the Cloud18 realm refused the login: sign in again (" + err.Error() + ")")
	}
	u, err := repman.Conf.VerifyCloud18Token(access)
	if err != nil {
		return "", email, errors.New("the Cloud18 realm token is refused: sign in again (" + err.Error() + ")")
	}
	if !strings.EqualFold(u.Email, email) {
		return "", email, errors.New("the Cloud18 login is another person's: sign in again")
	}
	lifetime := time.Until(end)
	if lifetime <= 0 {
		return "", email, errors.New("the session has expired: sign in again")
	}
	session, err := repman.issueJWTFor(repman.cloud18SSOSessionInfo(u, login, access), "", lifetime)
	if err != nil {
		return "", email, err
	}
	return session, u.Email, nil
}
