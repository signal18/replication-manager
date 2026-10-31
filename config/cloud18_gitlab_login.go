// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/signal18/replication-manager/utils/githelper"
)

// Cloud18 registration (#2159): GitLab ends its password grant, so the GitLab token of the person
// registering an instance comes from GitLab's own OAuth (authorization code, the public app
// cloud18-gitlab-oauth-client-id, PKCE), GitLab logging the person in with Cloud18 (its identity
// provider, the realm): the repman server walks those pages over HTTPS with the person's Cloud18
// email and password, as a browser would. No admin right, no CRM, nothing stored: the GitLab
// account is created by GitLab at the person's first Cloud18 login. A person without a Cloud18
// account gets one through the realm's own sign-up form (Cloud18SignUp).

const (
	// ConstCloud18GitLabOAuthClientID is the public GitLab OAuth application of replication-manager.
	ConstCloud18GitLabOAuthClientID = "39dababc7f094da24ce210d1ea066a216d3d55ecdadab80720290d4ab8717c29"
	// cloud18GitLabOAuthRedirect is never opened: GitLab gives the code in it, repman reads it.
	cloud18GitLabOAuthRedirect = "http://127.0.0.1/cloud18-register"
	cloud18WebTimeout          = 30 * time.Second
)

var (
	// ErrCloud18BadCredentials: the realm refused the email and password (or the account does not exist).
	ErrCloud18BadCredentials = errors.New("Cloud18 refused the email and password")
	// ErrCloud18AccountNotReady: the account exists but waits for its email confirmation (where the
	// person also chooses the password).
	ErrCloud18AccountNotReady = errors.New("the Cloud18 account is not activated yet: confirm the email Cloud18 sent (its link lets you choose your password)")
	// ErrCloud18SecondFactor: the login must reach a level the password alone does not give
	// (cloud18-login-acr): the realm asks for a second factor, which only the browser can give.
	ErrCloud18SecondFactor = errors.New("this login needs the Cloud18 second factor (one-time code): use Sign in with Cloud18")
	// ErrCloud18AccountExists: the sign-up was refused, an account has that email.
	ErrCloud18AccountExists = errors.New("a Cloud18 account already exists with this email")
)

// cloud18Web is a cookie-keeping HTTPS client walking GitLab and realm pages; it stops before
// following a redirect to one of its loopback redirects (cloud18GitLabOAuthRedirect,
// cloud18LoginRedirect), whose code it keeps.
type cloud18Web struct {
	client *http.Client
	code   string
	state  string
}

func newCloud18Web() *cloud18Web {
	jar, _ := cookiejar.New(nil)
	w := &cloud18Web{}
	w.client = &http.Client{Timeout: cloud18WebTimeout, Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if strings.HasPrefix(req.URL.String(), cloud18GitLabOAuthRedirect) || strings.HasPrefix(req.URL.String(), cloud18LoginRedirect) {
			w.code, w.state = req.URL.Query().Get("code"), req.URL.Query().Get("state")
			return http.ErrUseLastResponse
		}
		if len(via) > 15 {
			return errors.New("too many redirects")
		}
		// never leave https (cookies and forms follow redirects), the loopback alone excepted
		if cloud18SafeURL("redirect", req.URL.String()) != nil {
			return fmt.Errorf("refused redirect to %s://%s", req.URL.Scheme, req.URL.Host)
		}
		return nil
	}}
	return w
}

func (w *cloud18Web) do(method, target string, form url.Values) (string, *url.URL, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		return "", nil, err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("User-Agent", "replication-manager")
	resp, err := w.client.Do(req)
	if err != nil {
		// the URL of a login form carries short-lived session parameters: not in an error
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = fmt.Errorf("%s %s://%s: %w", method, req.URL.Scheme, req.URL.Host, uerr.Err)
		}
		return "", nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return string(b), resp.Request.URL, nil
}

var (
	csrfMetaRe      = regexp.MustCompile(`name="csrf-token" content="([^"]+)"`)
	kcLoginFormRe   = regexp.MustCompile(`<form[^>]*id="kc-form-login"[^>]*action="([^"]+)"`)
	kcSignUpFormRe  = regexp.MustCompile(`<form[^>]*id="kc-register-form"[^>]*action="([^"]+)"`)
	kcPageTitleRe   = regexp.MustCompile(`(?s)<h1[^>]*id="kc-page-title"[^>]*>(.*?)</h1>`)
	kcMessageRe     = regexp.MustCompile(`(?s)(?:input-error|kc-feedback-text|pf-v5-c-alert__title|pf-m-error)[^>]*>(.*?)</`)
	redirectLinkRe  = regexp.MustCompile(`href="(` + regexp.QuoteMeta(cloud18GitLabOAuthRedirect) + `\?[^"]+)"`)
	htmlTagRe       = regexp.MustCompile(`<[^>]+>|\s+`)
	kcVerifyEmailRe = regexp.MustCompile(`(?i)verify your email|email verification|update password|required action`)
)

// kcOTPFormRe is the realm's one-time code page, by its Keycloak theme id: a custom theme or another
// second factor (WebAuthn, recovery codes) does not match, and the login then fails with a generic
// message, still closed (VerifyCloud18Token refuses a token below the level).
var kcOTPFormRe = regexp.MustCompile(`<form[^>]*id="kc-otp-login-form"`)

// ErrCloud18PageChanged: a GitLab or realm page no longer has the form repman fills (its layout
// changed, or a step was added): the registration must be adapted.
var ErrCloud18PageChanged = errors.New("the GitLab or Cloud18 page layout changed")

// formTarget is the action of a form found on a page, accepted only on host (the realm's or
// GitLab's) over https (the loopback alone in clear): a password is never posted anywhere else.
func formTarget(action, host string) (string, error) {
	target := html.UnescapeString(action)
	u, err := url.Parse(target)
	if err != nil || u.Host != host {
		return "", fmt.Errorf("a form posts outside %s: refused", host)
	}
	if err := cloud18SafeURL("form", target); err != nil {
		return "", err
	}
	return target, nil
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

func pageText(s string) string {
	return strings.TrimSpace(htmlTagRe.ReplaceAllString(html.UnescapeString(s), " "))
}

// kcMessages are the error and info lines of a realm page.
func kcMessages(page string) string {
	var out []string
	for _, m := range kcMessageRe.FindAllStringSubmatch(page, 6) {
		if t := pageText(m[1]); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, "; ")
}

// gitlabOAuthStart opens GitLab's OAuth authorization of the public application (PKCE): without
// a session GitLab answers its sign-in page. It returns that page and the PKCE verifier.
func (conf *Config) gitlabOAuthStart(w *cloud18Web) (gitlab, page, verifier string, err error) {
	gitlab = strings.TrimRight(githelper.GitLabURL, "/")
	if err = cloud18SafeURL("GitLab", gitlab); err != nil {
		return
	}
	verifier, challenge, err := pkcePair()
	if err != nil {
		return
	}
	q := url.Values{"client_id": {ConstCloud18GitLabOAuthClientID}, "redirect_uri": {cloud18GitLabOAuthRedirect}, "response_type": {"code"},
		"scope": {"api"}, "state": {"repman"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	page, _, err = w.do(http.MethodGet, gitlab+"/oauth/authorize?"+q.Encode(), nil)
	if err != nil {
		err = fmt.Errorf("GitLab: %w", err)
	}
	return
}

// gitlabOAuthFinish reads the authorization code (a redirect, or GitLab's "Redirecting" page for
// a non-https redirect) and trades it for a token; "" when the page carries no code.
func (conf *Config) gitlabOAuthFinish(w *cloud18Web, gitlab, page, verifier string) (string, error) {
	if w.code == "" {
		if m := redirectLinkRe.FindStringSubmatch(page); m != nil {
			if u, err := url.Parse(html.UnescapeString(m[1])); err == nil {
				w.code, w.state = u.Query().Get("code"), u.Query().Get("state")
			}
		}
	}
	if w.code == "" {
		return "", nil
	}
	if w.state != "repman" {
		return "", errors.New("GitLab answered an authorization that is not this one (state)")
	}
	resp, err := (&http.Client{Timeout: cloud18WebTimeout}).PostForm(gitlab+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {w.code},
		"redirect_uri": {cloud18GitLabOAuthRedirect}, "client_id": {ConstCloud18GitLabOAuthClientID}, "code_verifier": {verifier}})
	if err != nil {
		return "", fmt.Errorf("GitLab: %w", err)
	}
	defer resp.Body.Close()
	var t struct {
		AccessToken string `json:"access_token"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&t) != nil || t.AccessToken == "" {
		return "", fmt.Errorf("GitLab gave no token (HTTP %d)", resp.StatusCode)
	}
	return t.AccessToken, nil
}

// Cloud18GitLabLogin returns a GitLab token (scope api, 2 h) of the Cloud18 account email,
// GitLab logging the person in with Cloud18. The token is for the calls of a registration only.
func (conf *Config) Cloud18GitLabLogin(email, password string) (string, error) {
	token, _, err := conf.cloud18GitLabWalk(email, password)
	return token, err
}

// cloud18GitLabWalk logs the person in to GitLab with Cloud18 and returns the GitLab token and
// the walk, whose cookies hold the person's GitLab and realm sessions (Cloud18Login goes on with
// them, no second password post).
func (conf *Config) cloud18GitLabWalk(email, password string) (string, *cloud18Web, error) {
	if _, err := conf.cloud18Issuer(); err != nil {
		return "", nil, err
	}
	w := newCloud18Web()
	gitlab, page, verifier, err := conf.gitlabOAuthStart(w)
	if err != nil {
		return "", nil, err
	}
	csrf := csrfMetaRe.FindStringSubmatch(page)
	if csrf == nil {
		return "", nil, fmt.Errorf("GitLab: no sign-in page (%w)", ErrCloud18PageChanged)
	}
	// "Sign in with Cloud18", then the realm's login form with the Cloud18 email and password
	page, at, err := w.do(http.MethodPost, gitlab+"/users/auth/openid_connect", url.Values{"authenticity_token": {html.UnescapeString(csrf[1])}})
	if err != nil {
		return "", nil, fmt.Errorf("GitLab: %w", err)
	}
	login := kcLoginFormRe.FindStringSubmatch(page)
	if login == nil {
		return "", nil, fmt.Errorf("GitLab did not send to the Cloud18 login (%s): %w", at.Host, ErrCloud18PageChanged)
	}
	issuer, _ := conf.cloud18Issuer()
	target, err := formTarget(login[1], hostOf(issuer))
	if err != nil {
		return "", nil, fmt.Errorf("Cloud18 login: %w", err)
	}
	page, at, err = w.do(http.MethodPost, target, url.Values{"username": {email}, "password": {password}, "credentialId": {""}})
	if err != nil {
		return "", nil, fmt.Errorf("Cloud18: %w", err)
	}
	token, err := conf.gitlabOAuthFinish(w, gitlab, page, verifier)
	if err != nil {
		return "", nil, err
	}
	if token != "" {
		conf.cloud18Log(LvlInfo, "%s logged in to GitLab with Cloud18", email)
		return token, w, nil
	}
	switch {
	case kcLoginFormRe.MatchString(page):
		conf.cloud18Log(LvlWarn, "Cloud18 refused the login of %s: %s", email, kcMessages(page))
		return "", nil, ErrCloud18BadCredentials
	case kcVerifyEmailRe.MatchString(page):
		return "", nil, ErrCloud18AccountNotReady
	case kcOTPFormRe.MatchString(page):
		return "", nil, ErrCloud18SecondFactor
	}
	return "", nil, gitlabStop(page, at)
}

var gitlabFlashRe = regexp.MustCompile(`(?s)class="[^"]*(?:gl-alert-body|flash-[a-z]+|gl-alert-title)[^"]*"[^>]*>(.*?)</`)

// gitlabFlash is the text of GitLab's alerts and flash messages on a page.
func gitlabFlash(page string) string {
	var out []string
	for _, m := range gitlabFlashRe.FindAllStringSubmatch(page, 6) {
		if t := pageText(m[1]); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, "; ")
}

// gitlabStop says why GitLab gave no authorization after a successful login.
func gitlabStop(page string, at *url.URL) error {
	// the visible messages only: a page's navigation or footer links "Terms" everywhere
	low := strings.ToLower(gitlabFlash(page) + " " + kcMessages(page))
	switch {
	case strings.Contains(low, "blocked"):
		return errors.New("GitLab: the account is blocked")
	case strings.Contains(low, "pending approval"):
		return errors.New("GitLab: the account waits for an administrator's approval")
	case strings.Contains(low, "terms"):
		return errors.New("GitLab asks to accept its terms: log in once to GitLab with Cloud18 and accept them")
	}
	msg := kcMessages(page)
	if msg == "" {
		msg = at.Host + at.Path
	}
	return fmt.Errorf("GitLab gave no authorization (%s)", cloud18ErrorText(msg))
}

// Cloud18SignUpForm is what the realm's sign-up asks (no password: the person chooses it from
// the confirmation email).
type Cloud18SignUpForm struct {
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Company   string `json:"company"`
	Phone     string `json:"phone"`
}

// Cloud18SignUp creates a Cloud18 account through the realm's own sign-up form, as anyone can on
// the site: the realm then sends the confirmation email, whose link activates the account and
// lets the person choose the password.
func (conf *Config) Cloud18SignUp(f Cloud18SignUpForm) error {
	issuer, err := conf.cloud18Issuer()
	if err != nil {
		return err
	}
	w := newCloud18Web()
	_, challenge, err := pkcePair()
	if err != nil {
		return err
	}
	q := url.Values{"client_id": {"account-console"}, "response_type": {"code"}, "scope": {"openid"}, "redirect_uri": {issuer + "/account/"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"repman"}}
	page, _, err := w.do(http.MethodGet, issuer+"/protocol/openid-connect/registrations?"+q.Encode(), nil)
	if err != nil {
		return fmt.Errorf("Cloud18: %w", err)
	}
	form := kcSignUpFormRe.FindStringSubmatch(page)
	if form == nil {
		return fmt.Errorf("Cloud18: no sign-up form, the sign-up is closed or %w", ErrCloud18PageChanged)
	}
	target, err := formTarget(form[1], hostOf(issuer))
	if err != nil {
		return fmt.Errorf("Cloud18 sign-up: %w", err)
	}
	username := f.Email
	if i := strings.Index(username, "@"); i > 0 {
		username = username[:i]
	}
	page, _, err = w.do(http.MethodPost, target, url.Values{"username": {username}, "email": {f.Email},
		"firstName": {f.FirstName}, "lastName": {f.LastName}, "company": {f.Company}, "phoneNumber": {f.Phone}})
	if err != nil {
		return fmt.Errorf("Cloud18: %w", err)
	}
	title := ""
	if m := kcPageTitleRe.FindStringSubmatch(page); m != nil {
		title = pageText(m[1])
	}
	if kcVerifyEmailRe.MatchString(title) || kcVerifyEmailRe.MatchString(kcMessages(page)) {
		conf.cloud18Log(LvlInfo, "Cloud18 account created for %s, waiting for its email confirmation", f.Email)
		return nil
	}
	msg := kcMessages(page)
	if strings.Contains(strings.ToLower(msg), "exists") {
		return ErrCloud18AccountExists
	}
	if msg == "" {
		msg = title
	}
	return fmt.Errorf("Cloud18 refused the sign-up: %s", cloud18ErrorText(msg))
}
