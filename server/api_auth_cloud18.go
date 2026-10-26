// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/config"
)

// Sign in with Cloud18 (#2185): on a partner repman with a public address (config.Cloud18SSO), the
// browser signs in at the Cloud18 realm and comes back here; the realm's session signs the person in
// on every other partner. Only the browser, this repman and the realm: no repman calls another.
// No token ever travels in an address: the dashboard gets a one-time code and trades it for the
// session. See doc/implementation/cluster/CLOUD18_SSO.md.

const (
	cloud18SSOPendingTTL = 5 * time.Minute  // a sign-in started and not finished
	cloud18SSOCodeTTL    = 60 * time.Second // the one-time code the dashboard trades for the session
	cloud18SSOMax        = 1024             // per store (F4): new sign-ins refused / oldest code out at it
	// cloud18SSOCookie ties a sign-in to the browser that started it (login CSRF: a callback URL
	// of someone else's sign-in, opened in a victim's browser, finds no matching cookie)
	cloud18SSOCookie = "repman_cloud18_sso"
)

// cloud18SSOReasons are the only reasons the login page shows (never free text from the address).
const (
	cloud18SSOExpired = "expired" // the sign-in expired, or was not started in this browser
	cloud18SSORefused = "refused" // the realm did not sign the person in
	cloud18SSOToken   = "token"   // the realm token was refused
	cloud18SSOBusy    = "busy"    // too many sign-ins in progress
	cloud18SSOFailed  = "failed"  // no session could be opened
)

type cloud18SSOPending struct {
	verifier string
	nonce    string // the value of the browser's cloud18SSOCookie
	silent   bool   // a silent check (prompt=none): no realm session is not a failure
	expires  time.Time
}

type cloud18SSOCode struct {
	jwt, user string
	expires   time.Time
}

var cloud18SSO = struct {
	sync.Mutex
	pending map[string]cloud18SSOPending // state -> PKCE verifier
	codes   map[string]cloud18SSOCode    // one-time code -> session
}{pending: map[string]cloud18SSOPending{}, codes: map[string]cloud18SSOCode{}}

// pruneCloud18SSOLocked drops the ended entries (cloud18SSO held). Sign-ins in progress are never
// dropped before their end: at the bound, a new start is refused instead (cloud18SSOBusy), and the
// oldest one-time code makes room for a new one.
func pruneCloud18SSOLocked(now time.Time) {
	for k, p := range cloud18SSO.pending {
		if !now.Before(p.expires) {
			delete(cloud18SSO.pending, k)
		}
	}
	for k, c := range cloud18SSO.codes {
		if !now.Before(c.expires) {
			delete(cloud18SSO.codes, k)
		}
	}
	if len(cloud18SSO.codes) >= cloud18SSOMax {
		oldest := ""
		for k, c := range cloud18SSO.codes {
			if oldest == "" || c.expires.Before(cloud18SSO.codes[oldest].expires) {
				oldest = k
			}
		}
		delete(cloud18SSO.codes, oldest)
	}
}

// cloud18SSOFail sends the browser back to the login page with one of the fixed reasons (the detail
// goes to the security log only).
func (repman *ReplicationManager) cloud18SSOFail(w http.ResponseWriter, r *http.Request, public, reason, detail string) {
	repman.logCloud18Refusal("api_auth_failure", "cloud18-sso", r.RemoteAddr, "Sign in with Cloud18: "+reason+": "+detail)
	clearCloud18SSOCookie(w)
	http.Redirect(w, r, public+"/login?cloud18_error="+url.QueryEscape(reason), http.StatusFound)
}

func clearCloud18SSOCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cloud18SSOCookie, Value: "", Path: "/api/auth/cloud18", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
}

// handlerMuxCloud18SSO says whether the login page offers Sign in with Cloud18.
// @Summary Sign in with Cloud18: offered or not
// @Description On a partner repman with a public address (cloud18 on, plan partner, a public https api-public-url, cloud18-sso-login on), the login page offers Sign in with Cloud18 (#2185). Public.
// @Tags Auth
// @Produce json
// @Success 200 {object} map[string]interface{} "enabled, and the reason when it is not"
// @Router /api/auth/cloud18 [get]
func (repman *ReplicationManager) handlerMuxCloud18SSO(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, err := repman.Conf.Cloud18SSO()
	out := map[string]interface{}{"enabled": err == nil}
	if err != nil {
		out["reason"] = err.Error()
	}
	repman.jsonResponse(out, w)
}

// handlerMuxCloud18SSOStart starts the browser sign-in: a state and a PKCE verifier kept here, the
// browser sent to the realm: to sign in (login_hint: the email typed on the login page), silently
// (silent=1: the login page's check on opening, no page shown), or to sign up (signup=1).
// @Summary Sign in with Cloud18: start
// @Description Redirects the browser to the Cloud18 realm's sign-in (login_hint), silent check (silent=1) or sign-up (signup=1), back to /api/auth/cloud18/callback (#2185). Public.
// @Tags Auth
// @Param login_hint query string false "the email typed on the login page"
// @Param silent query string false "1: sign in only if the person has a realm session, nothing shown"
// @Param signup query string false "1: the realm's sign-up form"
// @Success 302 {string} string "to the realm"
// @Router /api/auth/cloud18/start [get]
func (repman *ReplicationManager) handlerMuxCloud18SSOStart(w http.ResponseWriter, r *http.Request) {
	public, err := repman.Conf.Cloud18SSO()
	if err != nil {
		http.Error(w, "Sign in with Cloud18 is not offered here: "+err.Error(), http.StatusNotFound)
		return
	}
	state, err := newUpgradeID()
	if err != nil {
		http.Error(w, "Sign in with Cloud18: no random state", http.StatusInternalServerError)
		return
	}
	nonce, err := newUpgradeID()
	if err != nil {
		http.Error(w, "Sign in with Cloud18: no random state", http.StatusInternalServerError)
		return
	}
	verifier, challenge, err := config.Cloud18PKCE()
	if err != nil {
		http.Error(w, "Sign in with Cloud18: no PKCE verifier", http.StatusInternalServerError)
		return
	}
	q := r.URL.Query()
	mode := config.Cloud18SSOMode{Hint: q.Get("login_hint"), Silent: q.Get("silent") == "1", SignUp: q.Get("signup") == "1"}
	if len(mode.Hint) > 254 {
		mode.Hint = ""
	}
	target, err := repman.Conf.Cloud18SSOAuthorizeURL(public, state, challenge, mode)
	if err != nil {
		http.Error(w, "Sign in with Cloud18: "+err.Error(), http.StatusInternalServerError)
		return
	}
	now := time.Now()
	cloud18SSO.Lock()
	pruneCloud18SSOLocked(now)
	if len(cloud18SSO.pending) >= cloud18SSOMax {
		cloud18SSO.Unlock()
		repman.cloud18SSOFail(w, r, public, cloud18SSOBusy, "too many sign-ins in progress")
		return
	}
	cloud18SSO.pending[state] = cloud18SSOPending{verifier: verifier, nonce: nonce, silent: mode.Silent && !mode.SignUp, expires: now.Add(cloud18SSOPendingTTL)}
	cloud18SSO.Unlock()
	// Lax: the realm's redirect back is a top-level GET, which carries it
	http.SetCookie(w, &http.Cookie{Name: cloud18SSOCookie, Value: nonce, Path: "/api/auth/cloud18", MaxAge: int(cloud18SSOPendingTTL.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, target, http.StatusFound)
}

// handlerMuxCloud18SSOCallback finishes the browser sign-in: the state must be one this repman
// started (once), the code is traded with its verifier, the realm token verified as any realm token
// (#2182), and the SSO session opened. The dashboard gets a one-time code, never the session.
// @Summary Sign in with Cloud18: the realm's answer
// @Description The realm sends the browser back here with a code; the session is opened and the browser sent to /login with a one-time code (#2185). Public.
// @Tags Auth
// @Param code query string true "authorization code"
// @Param state query string true "the state of /api/auth/cloud18/start"
// @Success 302 {string} string "to /login?cloud18=<one-time code>"
// @Router /api/auth/cloud18/callback [get]
func (repman *ReplicationManager) handlerMuxCloud18SSOCallback(w http.ResponseWriter, r *http.Request) {
	public, err := repman.Conf.Cloud18SSO()
	if err != nil {
		http.Error(w, "Sign in with Cloud18 is not offered here: "+err.Error(), http.StatusNotFound)
		return
	}
	q := r.URL.Query()
	now := time.Now()
	cloud18SSO.Lock()
	p, ok := cloud18SSO.pending[q.Get("state")]
	delete(cloud18SSO.pending, q.Get("state"))
	cloud18SSO.Unlock()
	if !ok || !now.Before(p.expires) {
		repman.cloud18SSOFail(w, r, public, cloud18SSOExpired, "unknown state")
		return
	}
	if c, err := r.Cookie(cloud18SSOCookie); err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(p.nonce)) != 1 {
		repman.cloud18SSOFail(w, r, public, cloud18SSOExpired, "the sign-in was not started in this browser")
		return
	}
	if e := q.Get("error"); e != "" {
		if p.silent && (e == "login_required" || e == "interaction_required" || e == "consent_required" || e == "account_selection_required") {
			// the silent check found no realm session: the login page shows its form, no failure
			clearCloud18SSOCookie(w)
			http.Redirect(w, r, public+"/login?cloud18_silent=none", http.StatusFound)
			return
		}
		repman.cloud18SSOFail(w, r, public, cloud18SSORefused, e)
		return
	}
	l, err := repman.Conf.Cloud18SSOExchange(public, q.Get("code"), p.verifier)
	if err != nil {
		repman.cloud18SSOFail(w, r, public, cloud18SSORefused, err.Error())
		return
	}
	u, err := repman.Conf.VerifyCloud18Token(l.Access)
	if err != nil {
		repman.cloud18SSOFail(w, r, public, cloud18SSOToken, err.Error())
		return
	}
	info := struct {
		Name         string
		DisplayName  string `json:"display_name"`
		Role         string
		Password     string
		Email        string `json:"email"`
		Profile      string `json:"profile"`
		MeetUserID   string `json:"meet_user_id"`
		Cloud18      string `json:"cloud18,omitempty"`
		Cloud18Token string `json:"cloud18_token"`
	}{u.Email, u.Name, "Member", "", u.Email, repman.Conf.OAuthProvider, "",
		ssoLogin{cloud18: l}.cloud18Claim(repman.Conf), repman.Conf.GetEncryptedString(l.Access)}
	jwt, err := repman.issueJWT(info, "")
	if err != nil {
		repman.cloud18SSOFail(w, r, public, cloud18SSOFailed, err.Error())
		return
	}
	code, err := newUpgradeID()
	if err != nil {
		repman.cloud18SSOFail(w, r, public, cloud18SSOFailed, err.Error())
		return
	}
	cloud18SSO.Lock()
	pruneCloud18SSOLocked(now)
	cloud18SSO.codes[code] = cloud18SSOCode{jwt: jwt, user: u.Email, expires: now.Add(cloud18SSOCodeTTL)}
	cloud18SSO.Unlock()
	clearCloud18SSOCookie(w)
	http.Redirect(w, r, public+"/login?cloud18="+code, http.StatusFound)
}

// handlerMuxCloud18SSOSession trades the one-time code of a browser sign-in for the session, once.
// @Summary Sign in with Cloud18: the session
// @Description Trades the one-time code the callback gave the dashboard for the session token, once, within 60 s (#2185). Public.
// @Tags Auth
// @Accept json
// @Produce json
// @Param body body object true "{\"code\": \"<one-time code>\"}"
// @Success 200 {object} map[string]interface{} "token, username, loginMethod cloud18"
// @Failure 401 {string} string "unknown, used or expired code"
// @Router /api/auth/cloud18/session [post]
func (repman *ReplicationManager) handlerMuxCloud18SSOSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Code == "" {
		http.Error(w, "no code", http.StatusBadRequest)
		return
	}
	now := time.Now()
	cloud18SSO.Lock()
	c, ok := cloud18SSO.codes[body.Code]
	delete(cloud18SSO.codes, body.Code)
	cloud18SSO.Unlock()
	if !ok || !now.Before(c.expires) {
		repman.logCloud18Refusal("api_auth_failure", "cloud18-sso", r.RemoteAddr, "Sign in with Cloud18: unknown, used or expired code")
		http.Error(w, "Sign in with Cloud18: the code is unknown, used or expired, sign in again", http.StatusUnauthorized)
		return
	}
	if repman.Conf.MonitoringLogAPILogin && !repman.isAPILoginSilenced(c.user) {
		repman.logSecurityEvent("api_login_success", c.user, r.RemoteAddr, "API login successful (Sign in with Cloud18)")
	}
	repman.jsonResponse(map[string]string{"token": c.jwt, "username": c.user, "loginMethod": "cloud18"}, w)
}

// handlerMuxCloud18SSOLogout ends the person's Cloud18 session at the realm (every partner asks
// again) and comes back to this repman's login page. The dashboard dropped its sessions before.
// @Summary Sign in with Cloud18: logout
// @Description Redirects the browser to the realm's logout, back to /login (#2185). Public.
// @Tags Auth
// @Success 302 {string} string "to the realm's logout"
// @Router /api/auth/cloud18/logout [get]
func (repman *ReplicationManager) handlerMuxCloud18SSOLogout(w http.ResponseWriter, r *http.Request) {
	public, err := repman.Conf.Cloud18SSO()
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	target, err := repman.Conf.Cloud18SSOLogoutURL(public)
	if err != nil {
		http.Redirect(w, r, public+"/login", http.StatusFound)
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// loginMethodFor says how the login page signs a name in: "password" for an internal repman account
// (a local password, admin, a name that is not an email) or when this repman does not offer Sign in
// with Cloud18; "cloud18" otherwise (the browser goes to the realm, the email prefilled).
func (repman *ReplicationManager) loginMethodFor(user string) string {
	if _, err := repman.Conf.Cloud18SSO(); err != nil {
		return "password"
	}
	if user == "" || user == "system" || !strings.Contains(user, "@") {
		return "password"
	}
	// an internal account of that name, whatever its case (the login form may differ from the
	// account's case): the reads are safe without a lock, APIUsers is copy on write
	for _, cl := range repman.Clusters {
		for name, u := range cl.APIUsers {
			if !strings.EqualFold(name, user) || strings.EqualFold(name, repman.Conf.Cloud18GitUser) {
				continue
			}
			if u.Password != "" || u.Grants[config.GrantGlobalAdminShow] {
				return "password"
			}
		}
	}
	return "cloud18"
}

// handlerMuxLoginMethod tells the login page how to sign a name in (loginMethodFor).
// @Summary Login method of a name
// @Description "password" for an internal repman account (the login page asks its password), "cloud18" for a Cloud18 user on a partner repman with a public address (the browser goes to the Cloud18 realm, #2185). Public.
// @Tags Auth
// @Accept json
// @Produce json
// @Param body body object true "{\"username\": \"<name or email>\"}"
// @Success 200 {object} map[string]string "method: password or cloud18"
// @Router /api/login/method [post]
func (repman *ReplicationManager) handlerMuxLoginMethod(w http.ResponseWriter, r *http.Request) {
	// same origin only (the dashboard): no CORS header, another site cannot probe account names
	// from a visitor's browser
	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, "no username", http.StatusBadRequest)
		return
	}
	repman.jsonResponse(map[string]string{"method": repman.loginMethodFor(strings.TrimSpace(body.Username))}, w)
}

// registerCloud18SSORoutes adds the Sign in with Cloud18 routes (public, both routers).
func (repman *ReplicationManager) registerCloud18SSORoutes(router *mux.Router) {
	router.HandleFunc("/api/login/method", repman.handlerMuxLoginMethod).Methods(http.MethodPost)
	router.HandleFunc("/api/auth/cloud18", repman.handlerMuxCloud18SSO).Methods(http.MethodGet)
	router.HandleFunc("/api/auth/cloud18/start", repman.handlerMuxCloud18SSOStart).Methods(http.MethodGet)
	router.HandleFunc(config.Cloud18SSOCallbackPath, repman.handlerMuxCloud18SSOCallback).Methods(http.MethodGet)
	router.HandleFunc("/api/auth/cloud18/session", repman.handlerMuxCloud18SSOSession).Methods(http.MethodPost)
	router.HandleFunc("/api/auth/cloud18/logout", repman.handlerMuxCloud18SSOLogout).Methods(http.MethodGet)
}
