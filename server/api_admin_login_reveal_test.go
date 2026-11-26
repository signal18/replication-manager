// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	log "github.com/sirupsen/logrus"
)

const (
	adminRevealPass     = "Adm1n-pass-in-force"
	adminRevealProvider = "https://gitlab.example"
	adminRevealSend     = "/api/clusters/settings/actions/admin-login-reveal/send"
	adminRevealOpen     = "/api/clusters/settings/actions/admin-login-reveal/open"
)

// logCapture keeps every log line written through the hooked loggers.
type logCapture struct {
	sync.Mutex
	lines []string
}

func (c *logCapture) Levels() []log.Level { return log.AllLevels }
func (c *logCapture) Fire(e *log.Entry) error {
	s, _ := e.String()
	c.Lock()
	c.lines = append(c.lines, s)
	c.Unlock()
	return nil
}
func (c *logCapture) all() string {
	c.Lock()
	defer c.Unlock()
	return strings.Join(c.lines, "\n")
}

// adminRevealManager: two clusters, the instance's list with the admin and the Cloud18 people,
// each person with the default grants of its role, one-time links and a mail server set.
func adminRevealManager(t *testing.T) (*ReplicationManager, *logCapture, *[]string) {
	t.Helper()
	people := map[string]string{
		"sys@acme.example":     config.RoleSysOps,
		"ext@acme.example":     config.RoleExtSysOps,
		"dba@acme.example":     config.RoleDBOps,
		"extdba@acme.example":  config.RoleExtDBOps,
		"sponsor@acme.example": config.RoleSponsor,
	}
	list, acl := "admin:"+adminRevealPass+",ops:second", serverLevelACL
	for who, role := range people {
		list += "," + who + ":"
		acl += "," + who + ":" + config.GetDefaultGrants(role)
	}
	repman := newServerLevelTestManager(t, list)
	for _, cl := range repman.Clusters {
		cl.Conf.APIUsersACLAllow = acl
		cl.LoadAPIUsers()
	}
	repman.Conf.OAuthProvider, repman.Conf.TokenTimeout = adminRevealProvider, 1
	repman.Conf.APISecretReveal, repman.Conf.APISecretRevealAdminLogin = true, true
	repman.Conf.APIPublicURL, repman.Conf.MailFrom, repman.Conf.MailSMTPAddr = "https://repman.example", "repman@example.com", "smtp.example:25"
	repman.initKeys()
	capture := &logCapture{}
	repman.Logrus.AddHook(capture)
	repman.SecurityLogrus = log.New()
	repman.SecurityLogrus.AddHook(capture)
	adminLoginRevealRequests.Lock()
	adminLoginRevealRequests.at = nil
	adminLoginRevealRequests.Unlock()
	return repman, capture, captureRevealMail(t)
}

// ssoToken is the session JWT of a Cloud18 sign-in of email.
func ssoToken(t *testing.T, repman *ReplicationManager, email string) string {
	t.Helper()
	tok, err := repman.issueJWT(struct {
		Name     string
		Role     string
		Password string
		Email    string `json:"email"`
		Profile  string `json:"profile"`
	}{email, "Member", "", email, adminRevealProvider}, "")
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func adminRevealCall(repman *ReplicationManager, h http.HandlerFunc, url, token string, body interface{}) *httptest.ResponseRecorder {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

// codeOfMail is the one-time code of the last mail sent.
func codeOfMail(t *testing.T, sent *[]string) string {
	t.Helper()
	if len(*sent) == 0 {
		t.Fatal("no mail sent")
	}
	m := (*sent)[len(*sent)-1]
	i := strings.Index(m, "/reveal#")
	if i < 0 {
		t.Fatalf("no link in the mail: %s", m)
	}
	return strings.Fields(m[i+len("/reveal#"):])[0]
}

// Only the instance's sysops and extsysops signed in with Cloud18 are sent the link, at their own
// session's email; a dbops, an extdbops, a sponsor, a local login (the admin included) and no
// login are refused, and nothing is mailed to them.
func TestAdminLoginRevealSendRoles(t *testing.T) {
	repman, _, sent := adminRevealManager(t)
	for who, want := range map[string]int{
		"sys@acme.example":     http.StatusOK,
		"ext@acme.example":     http.StatusOK,
		"dba@acme.example":     http.StatusForbidden,
		"extdba@acme.example":  http.StatusForbidden,
		"sponsor@acme.example": http.StatusForbidden,
	} {
		before := len(*sent)
		w := adminRevealCall(repman, repman.handlerMuxAdminLoginRevealSend, adminRevealSend, ssoToken(t, repman, who), nil)
		if w.Code != want {
			t.Errorf("%s: %d, want %d (%s)", who, w.Code, want, w.Body.String())
			continue
		}
		if want != http.StatusOK {
			if len(*sent) != before {
				t.Errorf("%s: a mail was sent", who)
			}
			continue
		}
		if !strings.HasPrefix((*sent)[len(*sent)-1], who+"\n") {
			t.Errorf("%s: mailed to %q", who, strings.SplitN((*sent)[len(*sent)-1], "\n", 2)[0])
		}
		var res AdminLoginRevealSent
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res.MailedTo != who[:1]+"***@acme.example" {
			t.Errorf("%s: answer %s", who, w.Body.String())
		}
		if d := time.Until(res.Expires); d > 15*time.Minute || d < 14*time.Minute {
			t.Errorf("%s: expires in %v, want 15 min", who, d)
		}
		if strings.Contains(w.Body.String(), adminRevealPass) || strings.Contains(w.Body.String(), "/reveal#") {
			t.Errorf("%s: the answer carries the value or the link", who)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: no no-store", who)
		}
	}
	for name, tok := range map[string]string{
		"local admin": issueGlobalSettingsTestJWT(t, repman, "admin"),
		"local ops":   issueGlobalSettingsTestJWT(t, repman, "ops"),
		"no login":    "",
	} {
		before := len(*sent)
		if w := adminRevealCall(repman, repman.handlerMuxAdminLoginRevealSend, adminRevealSend, tok, nil); w.Code != http.StatusForbidden || len(*sent) != before {
			t.Errorf("%s: %d, %d mail(s)", name, w.Code, len(*sent)-before)
		}
	}
}

// The link opens once, for the same person only, and shows the admin login the login decides on
// at that moment (not a copy made when it was asked for); another person, a dbops, a local login
// or the public route never burn it. Nothing is rotated. Neither the value nor the code reaches a
// log line, and the mail carries the link only.
func TestAdminLoginRevealOpen(t *testing.T) {
	repman, capture, sent := adminRevealManager(t)
	sys := ssoToken(t, repman, "sys@acme.example")
	if w := adminRevealCall(repman, repman.handlerMuxAdminLoginRevealSend, adminRevealSend, sys, nil); w.Code != http.StatusOK {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	code := codeOfMail(t, sent)
	if strings.Contains((*sent)[0], adminRevealPass) {
		t.Fatal("the mail carries the password")
	}
	listBefore := repman.Conf.DecryptedAPIUserList()

	// the public route refuses a session link and keeps it
	pub := httptest.NewRecorder()
	repman.handlerSecretReveal(pub, httptest.NewRequest(http.MethodPost, secretRevealPath, strings.NewReader(`{"code":"`+code+`"}`)))
	if pub.Code != http.StatusForbidden || pub.Header().Get(secretRevealSessionHeader) != "true" || strings.Contains(pub.Body.String(), adminRevealPass) {
		t.Fatalf("public route: %d %q", pub.Code, pub.Body.String())
	}
	// another sysops, a dbops, a local admin: refused, the link kept
	for name, c := range map[string]struct {
		tok  string
		want int
	}{
		"other sysops": {ssoToken(t, repman, "ext@acme.example"), http.StatusForbidden},
		"dbops":        {ssoToken(t, repman, "dba@acme.example"), http.StatusForbidden},
		"local admin":  {issueGlobalSettingsTestJWT(t, repman, "admin"), http.StatusForbidden},
	} {
		w := adminRevealCall(repman, repman.handlerMuxAdminLoginRevealOpen, adminRevealOpen, c.tok, SecretRevealForm{Code: code})
		if w.Code != c.want || strings.Contains(w.Body.String(), adminRevealPass) {
			t.Fatalf("%s: %d %q", name, w.Code, w.Body.String())
		}
	}
	// the login changes after the link was asked for: the open shows the value in force
	const newPass = "N3w-admin-pass-in-force"
	if err := repman.setInstanceUserPassword("admin", newPass); err != nil {
		t.Fatal(err)
	}
	listBefore = repman.Conf.DecryptedAPIUserList()
	w := adminRevealCall(repman, repman.handlerMuxAdminLoginRevealOpen, adminRevealOpen, sys, SecretRevealForm{Code: code})
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("open: %d %s", w.Code, w.Body.String())
	}
	var res SecretRevealResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || len(res.Items) != 2 || res.Items[0].Value != "admin" || res.Items[1].Value != newPass {
		t.Fatalf("open: %s", w.Body.String())
	}
	// the value is the one that logs in: a cluster accepts it, the admin zone takes it
	for _, cl := range repman.clustersSnapshot() {
		if !cl.IsValidACLQuiet("admin", res.Items[1].Value, "/api/login", "submitted-password") {
			t.Fatalf("%s does not accept the value shown", cl.Name)
		}
	}
	if !repman.isInstanceCredential("admin", res.Items[1].Value) {
		t.Fatal("the admin zone does not take the value shown")
	}
	if repman.Conf.DecryptedAPIUserList() != listBefore {
		t.Fatal("the open changed the list: a reveal never rotates")
	}
	// burned
	if w := adminRevealCall(repman, repman.handlerMuxAdminLoginRevealOpen, adminRevealOpen, sys, SecretRevealForm{Code: code}); w.Code != http.StatusNotFound {
		t.Fatalf("second open: %d", w.Code)
	}
	logs := capture.all()
	for _, secret := range []string{adminRevealPass, newPass, code} {
		if strings.Contains(logs, secret) {
			t.Fatalf("a log line carries a secret or the code")
		}
	}
	for _, ev := range []string{"admin_login_reveal_requested", "admin_login_reveal_mailed", "admin_login_reveal_refused", "admin_login_reveal_opened"} {
		if !strings.Contains(logs, ev) {
			t.Errorf("no %s security event", ev)
		}
	}
}

// An expired link is refused and gone; the links of the other kind never open on this route.
func TestAdminLoginRevealExpiryAndKind(t *testing.T) {
	repman, _, sent := adminRevealManager(t)
	sys := ssoToken(t, repman, "sys@acme.example")
	repman.Conf.APISecretRevealAdminLoginExpireMinutes = 1
	if w := adminRevealCall(repman, repman.handlerMuxAdminLoginRevealSend, adminRevealSend, sys, nil); w.Code != http.StatusOK {
		t.Fatalf("send: %d", w.Code)
	}
	code := codeOfMail(t, sent)
	// age it past its expiry in the store
	secretReveals.Lock()
	reveals, _ := repman.loadSecretRevealsLocked()
	for i := range reveals {
		reveals[i].Expires = time.Now().Add(-time.Second)
	}
	plain := reveals
	secretReveals.Unlock()
	// write without the save's pruning: the expired link is still on disk
	writeRevealsRaw(t, repman, plain)
	if w := adminRevealCall(repman, repman.handlerMuxAdminLoginRevealOpen, adminRevealOpen, sys, SecretRevealForm{Code: code}); w.Code != http.StatusNotFound {
		t.Fatalf("expired: %d %s", w.Code, w.Body.String())
	}
	// an ordinary reveal (a rotated password) is not opened here, and stays for its own page
	link, _, err := repman.newSecretReveal("sys@acme.example", "", "t", []SecretRevealItem{{Label: "Password", Value: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if w := adminRevealCall(repman, repman.handlerMuxAdminLoginRevealOpen, adminRevealOpen, sys, SecretRevealForm{Code: codeOf(link)}); w.Code != http.StatusNotFound {
		t.Fatalf("ordinary reveal on the admin route: %d", w.Code)
	}
	if rev, err := repman.openSecretReveal(codeOf(link)); err != nil || rev.Items[0].Value != "x" {
		t.Fatalf("the ordinary reveal was burned: %v", err)
	}
}

// writeRevealsRaw encrypts and writes reveals as they are, expired ones included.
func writeRevealsRaw(t *testing.T, repman *ReplicationManager, reveals []secretReveal) {
	t.Helper()
	secretReveals.Lock()
	defer secretReveals.Unlock()
	saved := make([]secretReveal, len(reveals))
	copy(saved, reveals)
	// the save drops expired reveals: keep them by saving with a far expiry, then patch the index
	for i := range saved {
		saved[i].Expires = time.Now().Add(time.Hour)
	}
	if err := repman.saveSecretRevealsLocked(saved); err != nil {
		t.Fatal(err)
	}
	setSecretRevealIndex(repman.secretRevealStorePath(), reveals)
}

// The off-switch refuses, and the rate limit stops the sixth link of a person in an hour.
func TestAdminLoginRevealSwitchAndRate(t *testing.T) {
	repman, _, sent := adminRevealManager(t)
	sys := ssoToken(t, repman, "sys@acme.example")
	repman.Conf.APISecretRevealAdminLogin = false
	if w := adminRevealCall(repman, repman.handlerMuxAdminLoginRevealSend, adminRevealSend, sys, nil); w.Code != http.StatusServiceUnavailable || len(*sent) != 0 {
		t.Fatalf("off: %d", w.Code)
	}
	repman.Conf.APISecretRevealAdminLogin = true
	for i := 0; i < 5; i++ {
		if w := adminRevealCall(repman, repman.handlerMuxAdminLoginRevealSend, adminRevealSend, sys, nil); w.Code != http.StatusOK {
			t.Fatalf("link %d: %d", i+1, w.Code)
		}
	}
	if w := adminRevealCall(repman, repman.handlerMuxAdminLoginRevealSend, adminRevealSend, sys, nil); w.Code != http.StatusTooManyRequests || len(*sent) != 5 {
		t.Fatalf("sixth link: %d, %d mails", w.Code, len(*sent))
	}
	// another person has its own count
	if w := adminRevealCall(repman, repman.handlerMuxAdminLoginRevealSend, adminRevealSend, ssoToken(t, repman, "ext@acme.example"), nil); w.Code != http.StatusOK {
		t.Fatalf("other person: %d", w.Code)
	}
	if repman.adminLoginRevealAllow("SYS@acme.example", time.Now()) {
		t.Fatal("the limit depends on the case of the email")
	}
	if !repman.adminLoginRevealAllow("sys@acme.example", time.Now().Add(61*time.Minute)) {
		t.Fatal("the limit does not end after an hour")
	}
}

// A link that could not be mailed does not count: a missing mail server never uses up the hour.
func TestAdminLoginRevealFailedMailNotCounted(t *testing.T) {
	repman, _, sent := adminRevealManager(t)
	sys := ssoToken(t, repman, "sys@acme.example")
	repman.Conf.MailSMTPAddr = ""
	for i := 0; i < 7; i++ {
		if w := adminRevealCall(repman, repman.handlerMuxAdminLoginRevealSend, adminRevealSend, sys, nil); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("no mail server, try %d: %d", i+1, w.Code)
		}
	}
	repman.Conf.MailSMTPAddr = "smtp.example:25"
	if w := adminRevealCall(repman, repman.handlerMuxAdminLoginRevealSend, adminRevealSend, sys, nil); w.Code != http.StatusOK || len(*sent) != 1 {
		t.Fatalf("after the failures: %d", w.Code)
	}
}

// The rate limiter is bounded: its people never grow past adminLoginRevealRequestersMax.
func TestAdminLoginRevealRateBounded(t *testing.T) {
	repman, _, _ := adminRevealManager(t)
	now := time.Now()
	for i := 0; i < adminLoginRevealRequestersMax+50; i++ {
		repman.adminLoginRevealAllow(strings.Repeat("x", i%7)+string(rune('a'+i%26))+time.Duration(i).String(), now)
	}
	adminLoginRevealRequests.Lock()
	n := len(adminLoginRevealRequests.at)
	adminLoginRevealRequests.Unlock()
	if n > adminLoginRevealRequestersMax {
		t.Fatalf("%d people kept", n)
	}
}

func TestMaskEmail(t *testing.T) {
	for in, want := range map[string]string{"jane@acme.example": "j***@acme.example", "x": "***", "@d": "***"} {
		if got := maskEmail(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

// The value shown is the one the login decides on: a list the clusters have not loaded yet is
// refused (not in force), a list without an admin password is said, never shown.
func TestInstanceAdminLoginInForce(t *testing.T) {
	repman, _, sent := adminRevealManager(t)
	if p, err := repman.instanceAdminLogin(); err != nil || p != adminRevealPass {
		t.Fatalf("in force: %v", err)
	}
	repman.Conf.Secrets = config.NewSecrets(map[string]config.Secret{"api-credentials": {Value: "admin:not-loaded-yet"}})
	if _, err := repman.instanceAdminLogin(); err != errAdminLoginNotInForce {
		t.Fatalf("not loaded: %v", err)
	}
	repman.Conf.Secrets = config.NewSecrets(map[string]config.Secret{"api-credentials": {Value: "ops:x"}})
	if _, err := repman.instanceAdminLogin(); err != errAdminLoginNone {
		t.Fatalf("no admin: %v", err)
	}
	if w := adminRevealCall(repman, repman.handlerMuxAdminLoginRevealSend, adminRevealSend, ssoToken(t, repman, "sys@acme.example"), nil); w.Code != http.StatusConflict || len(*sent) != 0 {
		t.Fatalf("no admin, send: %d", w.Code)
	}
}
