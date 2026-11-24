// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
)

// The admin login by one-time link (ADMIN_LOGIN_REVEAL.md). The admin zone (global settings)
// takes the local admin account only (isGlobalSettingsAdmin). An instance's sysops or extsysops
// who signs in with Cloud18 asks for that login: a one-time link goes to the email of its own
// Cloud18 session, never to an address from the request, and opens only in that same person's
// signed-in session, while it still holds the server credentials grant on every cluster. It is
// the one-time secret link (server_secret_reveal.go) of a kind of its own: the store keeps the
// code's hash and no secret, the value is read when the link is opened, from the instance's list
// the login decides on. A reveal never rotates anything.

const (
	// secretRevealKindAdminLogin: a session link to the instance's admin login
	secretRevealKindAdminLogin = "admin-login"
	// adminLoginUser is the account of the admin zone (isGlobalSettingsAdmin)
	adminLoginUser = "admin"
	// adminLoginRevealRequestersMax bounds the rate limiter's people (F4, T18)
	adminLoginRevealRequestersMax = 1024
)

var (
	errAdminLoginRevealOff     = errors.New("the admin login by one-time link is off (api-secret-reveal-admin-login, api-secret-reveal) or the instance has no encryption key")
	errAdminLoginRevealSession = errors.New("the admin login is sent to a person signed in with Cloud18 only, at the email of that session")
	errAdminLoginRevealOther   = errors.New("this link was sent to another person: it opens only in the Cloud18 session of the person who asked for it")
	errAdminLoginNone          = errors.New("the instance's list (api-credentials) has no admin account with a password")
	errAdminLoginNotInForce    = errors.New("the admin password of the instance's list is not the one the clusters accept yet: try again in a minute")
)

// AdminLoginRevealSent is the answer to a request: where the link went, masked, and when it stops
// working. Never the value, never the link.
type AdminLoginRevealSent struct {
	MailedTo string    `json:"mailedTo"`
	Expires  time.Time `json:"expires"`
}

// adminLoginRevealRequests: the links each person asked for in the last hour (the rate limit).
var adminLoginRevealRequests struct {
	sync.Mutex
	at map[string][]time.Time
}

// adminLoginRevealAllow records a request of who at now and answers whether it is within
// api-secret-reveal-admin-login-max-per-hour (5 when not positive). Requests older than an hour
// are dropped at every call, and the map is bounded (adminLoginRevealRequestersMax): beyond it,
// the people with no request in the last hour go first, then any.
func (repman *ReplicationManager) adminLoginRevealAllow(who string, now time.Time) bool {
	limit := repman.Conf.APISecretRevealAdminLoginMaxPerHour
	if limit <= 0 {
		limit = 5
	}
	l := &adminLoginRevealRequests
	l.Lock()
	defer l.Unlock()
	if l.at == nil {
		l.at = map[string][]time.Time{}
	}
	for k, ts := range l.at {
		kept := make([]time.Time, 0, len(ts))
		for _, t := range ts {
			if now.Sub(t) < time.Hour {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(l.at, k)
		} else {
			l.at[k] = kept
		}
	}
	for k := range l.at {
		if len(l.at) < adminLoginRevealRequestersMax {
			break
		}
		if k != who {
			delete(l.at, k)
		}
	}
	if len(l.at[who]) >= limit {
		return false
	}
	l.at[who] = append(l.at[who], now)
	return true
}

// adminLoginRevealEnabled: the switch, and the one-time links it is built on.
func (repman *ReplicationManager) adminLoginRevealEnabled() bool {
	return repman.Conf.APISecretRevealAdminLogin && repman.secretRevealEnabled()
}

// adminLoginRevealExpiry is api-secret-reveal-admin-login-expire-minutes, 15 when not positive.
func (repman *ReplicationManager) adminLoginRevealExpiry() time.Duration {
	if m := repman.Conf.APISecretRevealAdminLoginExpireMinutes; m > 0 {
		return time.Duration(m) * time.Minute
	}
	return 15 * time.Minute
}

// adminLoginRevealRequester is the person of the request: a Cloud18 session (never a local
// login, an API token or a service token) of an email address, holding the server credentials
// grant on every cluster (the instance's sysops and extsysops: never a sponsor, dbops or
// extdbops). On a refusal it answers and logs the security event, and returns ok false.
func (repman *ReplicationManager) adminLoginRevealRequester(w http.ResponseWriter, r *http.Request, action string) (string, bool) {
	claims, err := repman.GetJWTClaims(r)
	if err != nil || claims["AuthType"] != "SSO" || !strings.Contains(claims["Email"], "@") || claims["Email"] != claims["User"] {
		user := ""
		if claims != nil {
			user = claims["User"]
		}
		repman.logSecurityEvent("admin_login_reveal_refused", user, r.RemoteAddr, "Admin login "+action+" refused: not a Cloud18 session")
		http.Error(w, errAdminLoginRevealSession.Error(), http.StatusForbidden)
		return "", false
	}
	email := claims["Email"]
	if _, ok := repman.serverCredentialsACL(w, r); !ok {
		repman.logSecurityEvent("admin_login_reveal_refused", email, r.RemoteAddr,
			"Admin login "+action+" refused: the server credentials grant is needed on every cluster (sysops, extsysops)")
		return "", false
	}
	return email, true
}

// instanceAdminLogin is the admin password the login decides on: the admin account's in the
// instance's list (api-credentials, through the credential chain), which the admin zone takes
// (isInstanceCredential), and which a cluster accepts (its loaded users, cluster.IsValidACL).
// A list just changed and not loaded by any cluster yet is not shown: it would not log in.
func (repman *ReplicationManager) instanceAdminLogin() (string, error) {
	pass, ok := config.APIUserListPassword(repman.Conf.DecryptedAPIUserList(), adminLoginUser)
	if !ok || pass == "" {
		return "", errAdminLoginNone
	}
	for _, cl := range repman.clustersSnapshot() {
		if u, ok := cl.GetAPIUsers()[adminLoginUser]; ok && u.Password != "" && subtle.ConstantTimeCompare([]byte(u.Password), []byte(pass)) == 1 {
			return pass, nil
		}
	}
	return "", errAdminLoginNotInForce
}

// maskEmail shows an address enough to recognize it: its first character and its domain.
func maskEmail(addr string) string {
	local, domain, ok := strings.Cut(addr, "@")
	if !ok || local == "" {
		return "***"
	}
	return local[:1] + "***@" + domain
}

// handlerMuxAdminLoginRevealSend mails the requester a one-time link to the admin login.
// @Summary Send me the admin login
// @Description Mails a one-time link to the local admin login (user and password of the admin zone) to the email of the caller's own Cloud18 session, never to an address from the request. The instance's sysops and extsysops signed in with Cloud18 only: the server credentials grant on every cluster; a local login or an API token is refused. The link expires after api-secret-reveal-admin-login-expire-minutes (15), opens once, and only in the same person's signed-in session. The answer never carries the value nor the link: the masked address and the expiry. Nothing is rotated. At most api-secret-reveal-admin-login-max-per-hour links (5) per person.
// @Tags User
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {object} AdminLoginRevealSent "Where the link went (masked) and its expiry"
// @Failure 403 {string} string "Not a Cloud18 session, or the server credentials grant missing on a cluster"
// @Failure 409 {string} string "No admin password in the instance's list, or not in force yet"
// @Failure 429 {string} string "Too many links asked for in the last hour"
// @Failure 503 {string} string "Off (api-secret-reveal-admin-login, api-secret-reveal), no encryption key, no mail server or no api-public-url"
// @Router /api/clusters/settings/actions/admin-login-reveal/send [post]
func (repman *ReplicationManager) handlerMuxAdminLoginRevealSend(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	email, ok := repman.adminLoginRevealRequester(w, r, "link")
	if !ok {
		return
	}
	refuse := func(code int, why string) {
		repman.logSecurityEvent("admin_login_reveal_refused", email, r.RemoteAddr, "Admin login link refused: "+why)
		http.Error(w, why, code)
	}
	if !repman.adminLoginRevealEnabled() {
		refuse(http.StatusServiceUnavailable, errAdminLoginRevealOff.Error())
		return
	}
	if _, err := repman.instanceAdminLogin(); err != nil {
		refuse(http.StatusConflict, err.Error())
		return
	}
	if !repman.adminLoginRevealAllow(email, time.Now()) {
		refuse(http.StatusTooManyRequests, "too many admin login links asked for in the last hour: use the last one mailed, or try again later")
		return
	}
	repman.logSecurityEvent("admin_login_reveal_requested", email, r.RemoteAddr, "Admin login asked for by one-time link by "+email)
	expires, err := repman.mailSecretRevealOf(secretReveal{Kind: secretRevealKindAdminLogin, For: email, Title: "replication-manager admin login"},
		"You asked for the admin login of this replication-manager (its admin zone: global settings). The link opens only in your own Cloud18 session ("+email+"): sign in with Cloud18 in the browser where you open it. Nothing was changed: the login stays as it is.",
		repman.adminLoginRevealExpiry())
	if err != nil {
		refuse(http.StatusServiceUnavailable, "admin login link not mailed: "+err.Error())
		return
	}
	repman.logSecurityEvent("admin_login_reveal_mailed", email, r.RemoteAddr,
		fmt.Sprintf("Admin login one-time link mailed to %s, expires %s", email, expires.UTC().Format(time.RFC3339)))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AdminLoginRevealSent{MailedTo: maskEmail(email), Expires: expires})
}

// handlerMuxAdminLoginRevealOpen opens a one-time admin login link in its person's session.
// @Summary Open a one-time admin login link
// @Description Shows the local admin login (user, password) once, as the login decides on it at this moment, to the person the link was mailed to: the same Cloud18 session email, still holding the server credentials grant on every cluster. The link is deleted at this open. Another person, a local login or an API token is refused and the link kept; an expired, used or unknown link gets 404. Nothing is rotated.
// @Tags User
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param body body SecretRevealForm true "The code from the link (after #)"
// @Success 200 {object} SecretRevealResult "The admin login, shown once"
// @Failure 400 {string} string "No code"
// @Failure 403 {string} string "Not a Cloud18 session, the server credentials grant missing, or a link of another person (kept)"
// @Failure 404 {string} string "Already opened, expired or unknown"
// @Failure 409 {string} string "No admin password in the instance's list, or not in force yet (kept)"
// @Failure 503 {string} string "Off (api-secret-reveal-admin-login, api-secret-reveal) or no encryption key"
// @Router /api/clusters/settings/actions/admin-login-reveal/open [post]
func (repman *ReplicationManager) handlerMuxAdminLoginRevealOpen(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	email, ok := repman.adminLoginRevealRequester(w, r, "link open")
	if !ok {
		return
	}
	if !repman.adminLoginRevealEnabled() {
		repman.logSecurityEvent("admin_login_reveal_refused", email, r.RemoteAddr, "Admin login link open refused: "+errAdminLoginRevealOff.Error())
		http.Error(w, errAdminLoginRevealOff.Error(), http.StatusServiceUnavailable)
		return
	}
	var form SecretRevealForm
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&form); err != nil || strings.TrimSpace(form.Code) == "" {
		http.Error(w, "Invalid request: the code of the link is needed", http.StatusBadRequest)
		return
	}
	var pass string
	_, err := repman.openSecretRevealIf(strings.TrimSpace(form.Code), func(rev *secretReveal) error {
		if rev.Kind != secretRevealKindAdminLogin {
			return errSecretRevealGone // another kind of link is never opened here
		}
		if !strings.EqualFold(rev.For, email) {
			return errAdminLoginRevealOther
		}
		var verr error
		pass, verr = repman.instanceAdminLogin()
		return verr
	})
	switch {
	case err == nil:
	case errors.Is(err, errSecretRevealGone):
		repman.noteSecretRevealRefused(r.RemoteAddr)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	case errors.Is(err, errAdminLoginRevealOther):
		repman.logSecurityEvent("admin_login_reveal_refused", email, r.RemoteAddr, "Admin login link open refused: the link was mailed to another person (kept for that person)")
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	case errors.Is(err, errAdminLoginNone), errors.Is(err, errAdminLoginNotInForce):
		repman.logSecurityEvent("admin_login_reveal_refused", email, r.RemoteAddr, "Admin login link open refused (kept): "+err.Error())
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case errors.Is(err, errSecretRevealOff):
		http.Error(w, errAdminLoginRevealOff.Error(), http.StatusServiceUnavailable)
		return
	default:
		repman.Logrus.Warnf("Admin login one-time link: %s", err)
		http.Error(w, "The link could not be read", http.StatusInternalServerError)
		return
	}
	repman.logSecurityEvent("admin_login_reveal_opened", email, r.RemoteAddr, "Admin login one-time link opened by "+email)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SecretRevealResult{Title: "replication-manager admin login", For: email,
		Items: []SecretRevealItem{{Label: "Login", Value: adminLoginUser}, {Label: "Password", Value: pass}}})
}
