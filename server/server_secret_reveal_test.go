// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	log "github.com/sirupsen/logrus"
)

func revealTestManager(t *testing.T) *ReplicationManager {
	t.Helper()
	return &ReplicationManager{
		Conf: &config.Config{
			WorkingDir:      t.TempDir(),
			SecretKey:       []byte("0123456789abcdef"),
			APISecretReveal: true,
			APIPublicURL:    "https://repman.example/",
			MailFrom:        "repman@example.com",
			MailSMTPAddr:    "smtp.example:25",
		},
		Logrus: log.New(),
	}
}

// captureRevealMail replaces the mail sender for the test and returns what was sent.
func captureRevealMail(t *testing.T) *[]string {
	t.Helper()
	var sent []string
	old := sendSecretRevealMail
	sendSecretRevealMail = func(_ *ReplicationManager, msg, subj, to string) error {
		sent = append(sent, to+"\n"+subj+"\n"+msg)
		return nil
	}
	t.Cleanup(func() { sendSecretRevealMail = old })
	return &sent
}

func codeOf(link string) string {
	return link[strings.Index(link, "#")+1:]
}

// A reveal opens once, the secrets are encrypted at rest, the code is in the fragment of a link on
// api-public-url, and the store keeps only the code's hash.
func TestSecretRevealOpensOnce(t *testing.T) {
	repman := revealTestManager(t)
	link, expires, err := repman.newSecretReveal("ann@example.com", "c1", "title", []SecretRevealItem{{Label: "Password", Value: "S3cret-value"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, "https://repman.example/reveal#") || time.Until(expires) < 23*time.Hour {
		t.Fatalf("link %q expires %v", link, expires)
	}
	raw, _ := os.ReadFile(repman.secretRevealStorePath())
	if st, _ := os.Stat(repman.secretRevealStorePath()); st.Mode().Perm() != 0600 || strings.Contains(string(raw), "S3cret-value") || strings.Contains(string(raw), codeOf(link)) {
		t.Fatal("the store is readable by others, or holds the secret or the code in clear")
	}
	rev, err := repman.openSecretReveal(codeOf(link))
	if err != nil || rev.Items[0].Value != "S3cret-value" || rev.For != "ann@example.com" {
		t.Fatalf("open: %+v %v", rev, err)
	}
	if _, err := repman.openSecretReveal(codeOf(link)); !errors.Is(err, errSecretRevealGone) {
		t.Fatalf("second open: %v", err)
	}
	if _, err := repman.openSecretReveal("not-a-code"); !errors.Is(err, errSecretRevealGone) {
		t.Fatalf("unknown code: %v", err)
	}
}

// An expired reveal shows nothing; the store is bounded, the oldest dropped first.
func TestSecretRevealExpiresAndIsBounded(t *testing.T) {
	repman := revealTestManager(t)
	link, _, _ := repman.newSecretReveal("a@example.com", "c1", "t", []SecretRevealItem{{Label: "P", Value: "v"}})
	secretReveals.Lock()
	reveals, _ := repman.loadSecretRevealsLocked()
	reveals[0].Expires = time.Now().Add(-time.Minute)
	// written as is: saveSecretRevealsLocked would prune it
	repman.Conf.APISecretRevealExpireHours = 1
	secretReveals.Unlock()
	secretReveals.Lock()
	if err := repman.saveSecretRevealsLocked(reveals); err != nil {
		t.Fatal(err)
	}
	secretReveals.Unlock()
	if _, err := repman.openSecretReveal(codeOf(link)); !errors.Is(err, errSecretRevealGone) {
		t.Fatalf("expired: %v", err)
	}
	var first string
	for i := 0; i < secretRevealMax+5; i++ {
		l, _, err := repman.newSecretReveal("a@example.com", "c1", "t", []SecretRevealItem{{Label: "P", Value: "v"}})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = l
		}
	}
	secretReveals.Lock()
	reveals, _ = repman.loadSecretRevealsLocked()
	secretReveals.Unlock()
	if len(reveals) != secretRevealMax {
		t.Fatalf("%d reveals kept, max %d", len(reveals), secretRevealMax)
	}
	if _, err := repman.openSecretReveal(codeOf(first)); !errors.Is(err, errSecretRevealGone) {
		t.Fatal("the oldest reveal was not the one dropped")
	}
}

// Off, or with no key: no reveal is made, so no secret is ever mailed.
func TestSecretRevealOff(t *testing.T) {
	repman := revealTestManager(t)
	repman.Conf.APISecretReveal = false
	if _, _, err := repman.newSecretReveal("a@example.com", "c1", "t", nil); !errors.Is(err, errSecretRevealOff) {
		t.Fatalf("off: %v", err)
	}
	repman.Conf.APISecretReveal, repman.Conf.SecretKey, repman.Conf.MonitoringKeyPath = true, nil, "/nonexistent/key"
	if _, _, err := repman.newSecretReveal("a@example.com", "c1", "t", nil); !errors.Is(err, errSecretRevealOff) {
		t.Fatalf("no key: %v", err)
	}
}

// The page's route is public and answers once; the second call is a 404.
func TestSecretRevealHandler(t *testing.T) {
	repman := revealTestManager(t)
	link, _, _ := repman.newSecretReveal("ann@example.com", "c1", "Your password", []SecretRevealItem{{Label: "Password", Value: "pw"}})
	call := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, secretRevealPath, strings.NewReader(body))
		repman.handlerSecretReveal(w, r)
		return w
	}
	w := call(`{"code":"` + codeOf(link) + `"}`)
	var got SecretRevealResult
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Items[0].Value != "pw" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("first open: %d %s", w.Code, w.Body.String())
	}
	if w := call(`{"code":"` + codeOf(link) + `"}`); w.Code != http.StatusNotFound {
		t.Fatalf("second open: %d", w.Code)
	}
	if w := call(`{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("no code: %d", w.Code)
	}
	w = httptest.NewRecorder()
	repman.handlerSecretReveal(w, httptest.NewRequest(http.MethodGet, secretRevealPath+"?code=x", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", w.Code)
	}
}

// The mail carries the link, never the password; the link opens the password.
func TestMailSecretRevealCarriesNoSecret(t *testing.T) {
	repman := revealTestManager(t)
	sent := captureRevealMail(t)
	if _, err := repman.mailSecretReveal("ann@example.com", "c1", "Your new password", "Changed.", []SecretRevealItem{{Label: "Password", Value: "Very-Secret-42"}}); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 1 || strings.Contains((*sent)[0], "Very-Secret-42") || !strings.Contains((*sent)[0], "https://repman.example/reveal#") {
		t.Fatalf("mail %q", *sent)
	}
	body := (*sent)[0]
	code := strings.Fields(body[strings.Index(body, "/reveal#")+len("/reveal#"):])[0]
	if rev, err := repman.openSecretReveal(code); err != nil || rev.Items[0].Value != "Very-Secret-42" {
		t.Fatalf("open from the mail: %+v %v", rev, err)
	}
	// no address, or no mail server: no link, the caller keeps the password
	if _, err := repman.mailSecretReveal("admin", "c1", "t", "x", nil); err == nil {
		t.Fatal("a login with no address was mailed")
	}
	repman.Conf.MailSMTPAddr = ""
	if _, err := repman.mailSecretReveal("ann@example.com", "c1", "t", "x", nil); err == nil {
		t.Fatal("mailed with no mail server")
	}
}

// A rotation of a user whose login is an email: the answer no longer carries the password, the
// user gets it by mail; a login without an address keeps today's answer.
func TestRotatedPasswordMailed(t *testing.T) {
	repman := revealTestManager(t)
	sent := captureRevealMail(t)
	res := &UserRotateResult{Username: "ann@example.com", Password: "New-Pass-24", Scope: "cluster", Clusters: []string{"c1"}}
	repman.mailRotatedPassword(res, "c1")
	if res.Password != "" || res.RevealMailedTo != "ann@example.com" || res.RevealExpires == nil || len(*sent) != 1 || strings.Contains((*sent)[0], "New-Pass-24") {
		t.Fatalf("mailed rotation: %+v, mails %d", res, len(*sent))
	}
	admin := &UserRotateResult{Username: "admin", Password: "Admin-Pass", Scope: "instance"}
	repman.mailRotatedPassword(admin, "c1")
	if admin.Password != "Admin-Pass" || admin.RevealMailedTo != "" || len(*sent) != 1 {
		t.Fatalf("admin rotation: %+v", admin)
	}
	repman.Conf.MailSMTPAddr = ""
	nomail := &UserRotateResult{Username: "bob@example.com", Password: "Bob-Pass", Scope: "cluster"}
	repman.mailRotatedPassword(nomail, "c1")
	if nomail.Password != "Bob-Pass" || nomail.RevealError == "" {
		t.Fatalf("no mail server: %+v", nomail)
	}
}

// Two opens of one link at the same time: exactly one gets the secret.
func TestSecretRevealConcurrentOpen(t *testing.T) {
	repman := revealTestManager(t)
	link, _, _ := repman.newSecretReveal("a@example.com", "c1", "t", []SecretRevealItem{{Label: "P", Value: "v"}})
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			_, err := repman.openSecretReveal(codeOf(link))
			results <- err
		}()
	}
	ok := 0
	for i := 0; i < 8; i++ {
		if err := <-results; err == nil {
			ok++
		} else if !errors.Is(err, errSecretRevealGone) {
			t.Fatal(err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d opens got the secret, want 1", ok)
	}
}

// An api-public-url the recipient cannot open gives no link (the secret is not stored), so the
// rotation shows the password once instead; a mail that fails leaves no reveal behind.
func TestSecretRevealNeedsAPublicAddress(t *testing.T) {
	for _, u := range []string{"", "repman.example", "ftp://repman.example", "https://"} {
		repman := revealTestManager(t)
		repman.Conf.APIPublicURL = u
		if _, _, err := repman.newSecretReveal("a@example.com", "c1", "t", nil); err == nil {
			t.Fatalf("api-public-url %q gave a link", u)
		}
		res := &UserRotateResult{Username: "ann@example.com", Password: "pw", Scope: "cluster"}
		repman.mailRotatedPassword(res, "c1")
		if res.Password != "pw" || res.RevealError == "" || res.RevealExpires != nil {
			t.Fatalf("api-public-url %q: %+v", u, res)
		}
	}
	repman := revealTestManager(t)
	old := sendSecretRevealMail
	sendSecretRevealMail = func(*ReplicationManager, string, string, string) error { return errors.New("smtp down") }
	defer func() { sendSecretRevealMail = old }()
	if _, err := repman.mailSecretReveal("a@example.com", "c1", "t", "x", []SecretRevealItem{{Label: "P", Value: "v"}}); err == nil {
		t.Fatal("a failed mail reported sent")
	}
	secretReveals.Lock()
	left, _ := repman.loadSecretRevealsLocked()
	secretReveals.Unlock()
	if len(left) != 0 {
		t.Fatalf("%d reveal(s) left after a failed mail", len(left))
	}
}

func TestExternalUserWholeMatch(t *testing.T) {
	if externalUser("bba@x.io:pw,c@x.io:pw2", "a@x.io") || !externalUser("bba@x.io:pw,c@x.io:pw2", "c@x.io") {
		t.Fatal("external users are matched by substring")
	}
}
