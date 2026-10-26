// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/githelper"
	"github.com/signal18/replication-manager/utils/meethelper"
)

// Cloud18 login (#2182): a person logs in with the Cloud18 account, not with GitLab's password
// grant (GitLab ends it), and what reaches the other Cloud18 instances is a realm token, which
// each one verifies by itself, not the person's password. See
// doc/implementation/cluster/CLOUD18_LOGIN.md.

// ssoLogin is a person's SSO login: the GitLab token, and the Cloud18 login when the Cloud18
// account gave it.
type ssoLogin struct {
	token   string
	cloud18 *config.Cloud18Login
}

// ssoPasswordLogin checks a person's name and password for an SSO login: the Cloud18 account
// first, then, while cloud18-gitlab-password-grant is on, GitLab's password grant (an account
// still on its GitLab password). The error is the last one tried (classifySSOAuthError).
func (repman *ReplicationManager) ssoPasswordLogin(user, password string) (ssoLogin, error) {
	err := errors.New("no Cloud18 realm (cloud18-identity-url) and GitLab's password grant is off (cloud18-gitlab-password-grant)")
	if repman.Conf.Cloud18IdentityURL != "" {
		l, cerr := repman.Conf.Cloud18Login(user, password)
		if cerr == nil {
			return ssoLogin{token: l.GitLabToken, cloud18: l}, nil
		}
		err = cerr
	}
	if !repman.Conf.Cloud18GitLabPasswordGrant {
		return ssoLogin{}, err
	}
	tok, gerr := githelper.GetGitLabTokenBasicAuth(user, password, false)
	if gerr != nil || tok == "" {
		if gerr == nil {
			gerr = errors.New("GitLab gave no token")
		}
		return ssoLogin{}, gerr
	}
	return ssoLogin{token: tok}, nil
}

// cloud18Claim is the session's Cloud18 login, the encrypted refresh token of its realm token:
// the session gets a fresh realm token from it to act on another instance.
func (l ssoLogin) cloud18Claim(conf *config.Config) string {
	if l.cloud18 == nil || l.cloud18.Refresh == "" {
		return ""
	}
	return conf.GetEncryptedString(l.cloud18.Refresh)
}

// ssoMeetUser logs the person in to meet: with the GitLab session of a Cloud18 login, else with
// the password (an account on GitLab's password grant).
func (repman *ReplicationManager) ssoMeetUser(meetUser, password string, l ssoLogin) (string, error) {
	logs := repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR")
	if l.cloud18 != nil && l.cloud18.Web != nil {
		return meethelper.CreateMeetUserClientWeb(meetUser, l.cloud18.Web, logs)
	}
	return meethelper.CreateMeetUserClient(meetUser, password, logs)
}

// cloud18TokenSessionMax bounds the cache of sessions made from realm tokens (one per live
// realm token: they last minutes).
const cloud18TokenSessionMax = 4096

type cloud18TokenSessionEntry struct {
	jwt     string
	user    config.Cloud18User
	expires time.Time
}

var cloud18TokenSessions = struct {
	sync.Mutex
	m map[string]cloud18TokenSessionEntry
}{m: map[string]cloud18TokenSessionEntry{}}

// acceptsCloud18Tokens: a registered Cloud18 instance with a realm takes realm tokens as an SSO
// identity. An instance not registered to Cloud18 never trusts the Cloud18 realm for its users.
func (repman *ReplicationManager) acceptsCloud18Tokens(raw string) bool {
	return repman.Conf.Cloud18 && raw != "" && repman.Conf.IsCloud18Token(raw)
}

// cloud18TokenSession verifies a realm token and returns the login JWT of its person: an SSO
// session (the email, no password, the realm token kept encrypted to act on other instances),
// cached until the realm token ends. The API middleware, the MCP and /api/login take a realm
// token through it, so every route judges its person as an SSO login, through the same ACL.
func (repman *ReplicationManager) cloud18TokenSession(raw string) (string, config.Cloud18User, error) {
	sum := sha256.Sum256([]byte(raw))
	key := hex.EncodeToString(sum[:])
	now := time.Now()
	cloud18TokenSessions.Lock()
	if e, ok := cloud18TokenSessions.m[key]; ok && now.Before(e.expires) {
		cloud18TokenSessions.Unlock()
		return e.jwt, e.user, nil
	}
	cloud18TokenSessions.Unlock()
	u, err := repman.Conf.VerifyCloud18Token(raw)
	if err != nil {
		return "", u, err
	}
	info := struct {
		Name         string
		DisplayName  string `json:"display_name"`
		Role         string
		Password     string
		Email        string `json:"email"`
		Profile      string `json:"profile"`
		MeetUserID   string `json:"meet_user_id"`
		Cloud18Token string `json:"cloud18_token"`
	}{u.Email, u.Name, "Member", "", u.Email, repman.Conf.OAuthProvider, "", repman.Conf.GetEncryptedString(raw)}
	jwt, err := repman.issueJWT(info, "")
	if err != nil {
		return "", u, err
	}
	cloud18TokenSessions.Lock()
	for k, e := range cloud18TokenSessions.m {
		if !now.Before(e.expires) {
			delete(cloud18TokenSessions.m, k)
		}
	}
	if len(cloud18TokenSessions.m) >= cloud18TokenSessionMax {
		cloud18TokenSessions.m = map[string]cloud18TokenSessionEntry{}
	}
	cloud18TokenSessions.m[key] = cloud18TokenSessionEntry{jwt: jwt, user: u, expires: u.Expires}
	cloud18TokenSessions.Unlock()
	return jwt, u, nil
}

// cloud18TokenFor returns a valid realm token of a session for another instance: the realm token
// it came with (the encrypted claim, or the token itself), or one refreshed from its Cloud18 login
// (encrypted refresh token). "" when the session has neither, or the realm refuses: the caller then uses the
// password, as before #2182.
func (repman *ReplicationManager) cloud18TokenFor(tokenClaim, loginClaim string) string {
	if tokenClaim != "" {
		if t := repman.Conf.GetDecryptedPassword("cloud18-token", tokenClaim); t != "" && repman.Conf.IsCloud18Token(t) {
			return t
		}
	}
	if loginClaim == "" {
		return ""
	}
	t, err := repman.Conf.Cloud18LoginAccess(repman.Conf.GetDecryptedPassword("cloud18-login", loginClaim))
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"Cloud18 login not refreshed, the other instances get the password: %s", err)
		return ""
	}
	return t
}

// cloud18TokenLogin is /api/login with a realm token as bearer (another Cloud18 instance acting
// for its user, or a client of the realm): the session of the token's person.
func (repman *ReplicationManager) cloud18TokenLogin(w http.ResponseWriter, r *http.Request, raw string) {
	jwt, u, err := repman.cloud18TokenSession(raw)
	if err != nil {
		repman.logSecurityEvent("api_auth_failure", "cloud18-token", r.RemoteAddr, "API authentication failure: "+err.Error())
		http.Error(w, "Error logging in: Cloud18 token refused", http.StatusUnauthorized)
		return
	}
	if repman.Conf.MonitoringLogAPILogin && !repman.isAPILoginSilenced(u.Email) {
		repman.logSecurityEvent("api_login_success", u.Email, r.RemoteAddr, "API login successful (Cloud18 token)")
	}
	repman.jsonResponse(AuthTokenWithUpgrade{Token: jwt}, w)
}

// isCloud18LoginSession says whether a session (GetUserInfoMap) comes from the Cloud18 login: its
// realm token or the refresh token of its login. The realm proved it, at login or by a token
// verified on this request, so it has no password to check again (a session from a realm token
// has none); an older SSO session re-checks its password.
func isCloud18LoginSession(info map[string]string) bool {
	return info["Cloud18Token"] != "" || info["Cloud18Login"] != ""
}
