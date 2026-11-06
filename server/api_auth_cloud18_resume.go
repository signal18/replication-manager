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
)

// Resuming a Cloud18 session after a restart (#2341). The sessions repman issues are signed with a
// key made at each start, so a restart leaves every session unverifiable while the person's realm
// session lives on. A session of a Cloud18 login carries that login (its refresh token, encrypted
// with this instance's key): the realm, asked again, says whether the login still holds, and a new
// session is given only then.
//
// Nothing of the old session is trusted but what the realm confirms: the person is the one of the
// realm's token, the role is Member as at any Cloud18 sign-in (cloud18SSOSessionInfo); the old
// session only gives its login (which must decrypt with this instance's key and be accepted by the
// realm), its email (which must be the realm's) and its end (a resume never makes a session last
// longer). A forged role, name or end is worthless: it is not read, or only shortens the session.

const cloud18ResumePath = "/api/auth/cloud18/resume"

const sessionIssuer = "https://api.replication-manager.signal18.io"

var errCloud18ResumeRefused = errors.New("this session cannot be resumed: sign in again")

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
	end = time.Unix(int64(exp), 0)
	if exp == 0 || !time.Now().Before(end) {
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
// @Failure 404 {string} string "cloud18-login-resume is off"
// @Router /api/auth/cloud18/resume [post]
func (repman *ReplicationManager) handlerMuxCloud18SessionResume(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if !repman.Conf.Cloud18LoginResume {
		http.Error(w, "Resuming Cloud18 sessions is off (cloud18-login-resume)", http.StatusNotFound)
		return
	}
	session, u, err := repman.cloud18ResumeSession(bearerOf(r))
	if err != nil {
		repman.logCloud18Refusal("api_auth_failure", u, r.RemoteAddr, "Cloud18 session not resumed: "+err.Error())
		http.Error(w, err.Error(), http.StatusUnauthorized)
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
	if err != nil || refresh == "" {
		return "", email, errors.New("the session's Cloud18 login is not one of this instance: sign in again")
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
