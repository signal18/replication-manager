// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/crypto"
)

// A secret reveal is how a password reaches a person by mail without being in the mail: the mail
// carries a one-time link, <api-public-url>/reveal#<code>, and the page shows the secret once,
// after a click, then it is gone. Used by the password rotation (the new password of a user whose
// login is an email) and by the credentials mails (sponsor, DBA, sysadmin), which carried the
// password in clear before.
//
// The code is 32 random bytes; the store keeps only its SHA-256, with the secrets, in
// WorkingDir/secret-reveals.json, the whole file encrypted with the instance's key (as
// api-tokens.json), never pushed to git. A reveal is opened once (deleted at the first open) and
// expires after api-secret-reveal-expire-hours; the store is bounded (secretRevealMax, the oldest
// dropped first) and pruned of expired reveals at every change (F3, F4). The code is in the URL
// fragment: a browser never sends it to the server or a proxy, so it is in no access log; the page
// POSTs it. A mail scanner that opens the link shows nothing: the secret needs the click.

const (
	secretRevealStore    = "secret-reveals.json"
	secretRevealCodeSize = 32
	secretRevealMax      = 256
	secretRevealPath     = "/api/secret-reveal"
)

var (
	// errSecretRevealOff: the reveal links are off, or the instance has no key to keep a secret
	errSecretRevealOff = errors.New("one-time reveal links are off (api-secret-reveal) or the instance has no encryption key")
	// errSecretRevealGone: no such code, already opened or expired (one answer for the three)
	errSecretRevealGone = errors.New("this link was already opened, has expired or does not exist")
	// errSecretRevealSession: a session link (Kind set) opens only in its person's signed-in
	// session, never on the public route: refused there without being burned
	errSecretRevealSession = errors.New("this link opens only in the signed-in session of the person who asked for it")
)

// secretRevealSessionHeader marks the public route's refusal of a session link: the page then
// opens it through the signed-in route of its kind.
const secretRevealSessionHeader = "X-Repman-Reveal-Session"

// SecretRevealItem is one secret shown by a reveal page: a label and its value.
type SecretRevealItem struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// secretReveal is a stored reveal.
type secretReveal struct {
	CodeHash string `json:"codeHash"`
	// Kind: "" for a reveal carrying its Items, opened by its code alone; otherwise a session link
	// (secretRevealKindAdminLogin): no secret stored, the value is read when it is opened, by its
	// person only, in a signed-in session, through the route of its kind
	Kind    string             `json:"kind,omitempty"`
	For     string             `json:"for"`
	Cluster string             `json:"cluster,omitempty"`
	Title   string             `json:"title"`
	Items   []SecretRevealItem `json:"items"`
	Created time.Time          `json:"created"`
	Expires time.Time          `json:"expires"`
}

type secretRevealFile struct {
	Version int            `json:"version"`
	Reveals []secretReveal `json:"reveals"`
}

// secretReveals serializes the store's reads and writes.
var secretReveals sync.Mutex

// secretRevealIndex is the hashes in the store and their expiry, kept in memory so that an unknown
// or expired code is refused without touching the disk or the store's lock: the open route is
// public, a flood of junk codes costs a map lookup. Rebuilt at every write of the store, read from
// it the first time.
var secretRevealIndex struct {
	sync.RWMutex
	path    string // the store this index is of: another store is not loaded
	expires map[string]time.Time
}

func setSecretRevealIndex(path string, reveals []secretReveal) {
	m := make(map[string]time.Time, len(reveals))
	for _, r := range reveals {
		m[r.CodeHash] = r.Expires
	}
	secretRevealIndex.Lock()
	secretRevealIndex.expires, secretRevealIndex.path = m, path
	secretRevealIndex.Unlock()
}

// secretRevealKnown: hash is in the store at path and not expired; ok false while the index is not
// loaded for that store.
func secretRevealKnown(path, hash string) (known, ok bool) {
	secretRevealIndex.RLock()
	defer secretRevealIndex.RUnlock()
	if secretRevealIndex.path != path {
		return false, false
	}
	exp, found := secretRevealIndex.expires[hash]
	return found && time.Now().Before(exp), true
}

// secretRevealRefusals aggregates the refused opens into one security event a minute at most: a
// flood of junk codes never writes one line each (F3, F4).
var secretRevealRefusals struct {
	sync.Mutex
	count int
	since time.Time
}

func (repman *ReplicationManager) noteSecretRevealRefused(remoteAddr string) {
	secretRevealRefusals.Lock()
	defer secretRevealRefusals.Unlock()
	now := time.Now()
	if secretRevealRefusals.since.IsZero() {
		secretRevealRefusals.since = now
	}
	secretRevealRefusals.count++
	if now.Sub(secretRevealRefusals.since) < time.Minute && secretRevealRefusals.count > 1 {
		return
	}
	repman.logSecurityEvent("secret_reveal_refused", "", remoteAddr,
		fmt.Sprintf("%d one-time secret link open(s) refused since %s (already opened, expired or unknown; last from %s)",
			secretRevealRefusals.count, secretRevealRefusals.since.UTC().Format(time.RFC3339), remoteAddr))
	secretRevealRefusals.count, secretRevealRefusals.since = 0, now
}

func (repman *ReplicationManager) secretRevealStorePath() string {
	return filepath.Join(repman.Conf.WorkingDir, secretRevealStore)
}

// secretRevealEnabled: the switch is on and the instance has a key to keep the secrets with.
func (repman *ReplicationManager) secretRevealEnabled() bool {
	if repman.Conf.SecretKey == nil {
		repman.Conf.LoadEncrytionKey()
	}
	return repman.Conf.APISecretReveal && len(repman.Conf.SecretKey) > 0
}

// secretRevealExpiry is api-secret-reveal-expire-hours, 24 when unset or not positive.
func (repman *ReplicationManager) secretRevealExpiry() time.Duration {
	if h := repman.Conf.APISecretRevealExpireHours; h > 0 {
		return time.Duration(h) * time.Hour
	}
	return 24 * time.Hour
}

// secretRevealBase is api-public-url, the base of the links: an http(s) address with a host, else
// an error (a relative or empty link would store a secret nobody can open).
func (repman *ReplicationManager) secretRevealBase() (string, error) {
	raw := strings.TrimRight(strings.TrimSpace(repman.Conf.APIPublicURL), "/")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("api-public-url %q is not an http(s) address the recipient can open", raw)
	}
	return raw, nil
}

func secretRevealHash(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// loadSecretRevealsLocked reads the store; absent = empty. The caller holds secretReveals.
func (repman *ReplicationManager) loadSecretRevealsLocked() ([]secretReveal, error) {
	data, err := os.ReadFile(repman.secretRevealStorePath())
	if os.IsNotExist(err) || (err == nil && strings.TrimSpace(string(data)) == "") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p := crypto.Password{Key: repman.Conf.SecretKey, CipherText: strings.TrimSpace(string(data))}
	if err := p.Decrypt(); err != nil {
		return nil, fmt.Errorf("secret reveal store: cannot decrypt %s: %w", repman.secretRevealStorePath(), err)
	}
	var f secretRevealFile
	if err := json.Unmarshal([]byte(p.PlainText), &f); err != nil {
		return nil, fmt.Errorf("secret reveal store: cannot parse %s (wrong key?): %w", repman.secretRevealStorePath(), err)
	}
	return f.Reveals, nil
}

// saveSecretRevealsLocked drops the expired reveals and the oldest beyond secretRevealMax, then
// encrypts and atomically rewrites the store. The caller holds secretReveals.
func (repman *ReplicationManager) saveSecretRevealsLocked(reveals []secretReveal) error {
	now := time.Now()
	kept := reveals[:0:0]
	for _, r := range reveals {
		if now.Before(r.Expires) {
			kept = append(kept, r)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Created.Before(kept[j].Created) })
	if len(kept) > secretRevealMax {
		kept = append([]secretReveal(nil), kept[len(kept)-secretRevealMax:]...)
	}
	plain, err := json.Marshal(secretRevealFile{Version: 1, Reveals: kept})
	if err != nil {
		return err
	}
	p := crypto.Password{Key: repman.Conf.SecretKey, PlainText: string(plain)}
	p.Encrypt()
	if p.CipherText == "" {
		return errors.New("secret reveal store: encryption failed")
	}
	path := repman.secretRevealStorePath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(p.CipherText+"\n"), 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	setSecretRevealIndex(path, kept)
	return nil
}

// newSecretReveal keeps items for the person forWho and returns the one-time link and its expiry.
func (repman *ReplicationManager) newSecretReveal(forWho, clusterName, title string, items []SecretRevealItem) (string, time.Time, error) {
	return repman.newSecretRevealOf(secretReveal{For: forWho, Cluster: clusterName, Title: title, Items: items}, repman.secretRevealExpiry())
}

// newSecretRevealOf stores r (its code, creation and expiry set here) for ttl and returns the
// one-time link and its expiry.
func (repman *ReplicationManager) newSecretRevealOf(r secretReveal, ttl time.Duration) (string, time.Time, error) {
	if !repman.secretRevealEnabled() {
		return "", time.Time{}, errSecretRevealOff
	}
	base, err := repman.secretRevealBase()
	if err != nil {
		return "", time.Time{}, err
	}
	b := make([]byte, secretRevealCodeSize)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, err
	}
	code := base64.RawURLEncoding.EncodeToString(b)
	now := time.Now()
	r.CodeHash, r.Created, r.Expires = secretRevealHash(code), now, now.Add(ttl)
	secretReveals.Lock()
	defer secretReveals.Unlock()
	reveals, err := repman.loadSecretRevealsLocked()
	if err != nil {
		return "", time.Time{}, err
	}
	if err := repman.saveSecretRevealsLocked(append(reveals, r)); err != nil {
		return "", time.Time{}, err
	}
	return base + "/reveal#" + code, r.Expires, nil
}

// dropSecretReveal deletes the reveal of code: its mail was not sent, nobody has the link.
func (repman *ReplicationManager) dropSecretReveal(code string) error {
	h := secretRevealHash(code)
	secretReveals.Lock()
	defer secretReveals.Unlock()
	reveals, err := repman.loadSecretRevealsLocked()
	if err != nil {
		return err
	}
	rest := reveals[:0:0]
	for _, r := range reveals {
		if r.CodeHash != h {
			rest = append(rest, r)
		}
	}
	return repman.saveSecretRevealsLocked(rest)
}

// openSecretReveal returns the reveal of code and deletes it: a second open finds nothing. A
// session link (Kind set) is refused, errSecretRevealSession, and kept: its own route opens it.
func (repman *ReplicationManager) openSecretReveal(code string) (*secretReveal, error) {
	return repman.openSecretRevealIf(code, func(r *secretReveal) error {
		if r.Kind != "" {
			return errSecretRevealSession
		}
		return nil
	})
}

// openSecretRevealIf returns the reveal of code and deletes it when check accepts it. A reveal
// check refuses is not deleted (its error is returned): a refused open burns nothing. An expired
// reveal is deleted and gone whatever check says.
func (repman *ReplicationManager) openSecretRevealIf(code string, check func(*secretReveal) error) (*secretReveal, error) {
	if !repman.secretRevealEnabled() {
		return nil, errSecretRevealOff
	}
	h := secretRevealHash(code)
	if known, ok := secretRevealKnown(repman.secretRevealStorePath(), h); ok && !known {
		return nil, errSecretRevealGone // no disk, no store lock
	}
	secretReveals.Lock()
	defer secretReveals.Unlock()
	reveals, err := repman.loadSecretRevealsLocked()
	if err != nil {
		return nil, err
	}
	if _, ok := secretRevealKnown(repman.secretRevealStorePath(), h); !ok {
		setSecretRevealIndex(repman.secretRevealStorePath(), reveals) // first open since the start: the index from the file
	}
	for i := range reveals {
		r := reveals[i]
		if r.CodeHash != h {
			continue
		}
		expired := !time.Now().Before(r.Expires)
		if !expired {
			if err := check(&r); err != nil {
				return nil, err // kept: its person may still open it
			}
		}
		rest := append(append([]secretReveal(nil), reveals[:i]...), reveals[i+1:]...)
		if err := repman.saveSecretRevealsLocked(rest); err != nil {
			return nil, err // not deleted: not shown either, the secret is shown once at most
		}
		if expired {
			return nil, errSecretRevealGone
		}
		found := r
		return &found, nil
	}
	return nil, errSecretRevealGone
}

// mailSecretReveal mails forWho a one-time link to items. The mail carries the link only, never
// a secret. An error says why no link was mailed; the caller decides what to do without it.
func (repman *ReplicationManager) mailSecretReveal(forWho, clusterName, title, intro string, items []SecretRevealItem) (time.Time, error) {
	return repman.mailSecretRevealOf(secretReveal{For: forWho, Cluster: clusterName, Title: title, Items: items}, intro, repman.secretRevealExpiry())
}

// mailSecretRevealOf stores r for ttl and mails its person (r.For) the one-time link.
func (repman *ReplicationManager) mailSecretRevealOf(r secretReveal, intro string, ttl time.Duration) (time.Time, error) {
	forWho, title := r.For, r.Title
	if !strings.Contains(forWho, "@") {
		return time.Time{}, fmt.Errorf("%s is not an email address", forWho)
	}
	if missing := repman.mailerConfigMissing(); len(missing) > 0 {
		return time.Time{}, fmt.Errorf("no mail server: missing %s", strings.Join(missing, ", "))
	}
	link, expires, err := repman.newSecretRevealOf(r, ttl)
	if err != nil {
		return time.Time{}, err
	}
	partner := repman.Partner.Name
	if partner == "" {
		partner = "Signal 18"
	}
	msg := fmt.Sprintf(`Hello,

%s

For your security, the password is not in this mail. Open this link to see it, once:

%s

The link works one time only and expires on %s. If it was already opened when you click it,
someone else saw the secret: tell your administrator at once.

Best regards,

%s
`, intro, link, expires.UTC().Format("2006-01-02 15:04 UTC"), partner)
	if err := repman.sendSecretRevealMail(msg, title, forWho); err != nil {
		// nobody has the link: the secret does not wait in the store for nothing
		if derr := repman.dropSecretReveal(link[strings.LastIndex(link, "#")+1:]); derr != nil {
			repman.Logrus.Warnf("One-time secret link of %s not mailed and not deleted (it expires on %s): %s", forWho, expires.UTC().Format(time.RFC3339), derr)
		}
		return time.Time{}, fmt.Errorf("mail not sent: %w", err)
	}
	return expires, nil
}

// sendSecretRevealMail sends the reveal mail; tests replace it.
var sendSecretRevealMail = func(repman *ReplicationManager, msg, subj, to string) error {
	return repman.SendEmailMessage(msg, subj, to, false, nil)
}

func (repman *ReplicationManager) sendSecretRevealMail(msg, subj, to string) error {
	return sendSecretRevealMail(repman, msg, subj, to)
}

// SecretRevealForm is the one-time code of a reveal link.
type SecretRevealForm struct {
	Code string `json:"code"`
}

// SecretRevealResult is what a reveal link shows, once.
type SecretRevealResult struct {
	Title   string             `json:"title"`
	For     string             `json:"for"`
	Cluster string             `json:"cluster,omitempty"`
	Items   []SecretRevealItem `json:"items"`
}

// handlerSecretReveal opens a one-time reveal link. Public: the link's code is the credential.
// @Summary Open a one-time secret link
// @Description Shows the secrets of a one-time link mailed by replication-manager (a rotated password, credentials), once: the link is deleted at the first open. No login: the code from the link is the credential. 404 for a link already opened, expired or unknown, the same answer for the three.
// @Tags User
// @Accept json
// @Produce json
// @Param body body SecretRevealForm true "The code from the link (after #)"
// @Success 200 {object} SecretRevealResult "The secrets, shown once"
// @Failure 400 {string} string "No code"
// @Failure 403 {string} string "A link that opens only in its person's signed-in session (header X-Repman-Reveal-Session): not burned"
// @Failure 404 {string} string "Already opened, expired or unknown"
// @Failure 503 {string} string "One-time links are off on this instance"
// @Router /api/secret-reveal [post]
func (repman *ReplicationManager) handlerSecretReveal(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	var form SecretRevealForm
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&form); err != nil || strings.TrimSpace(form.Code) == "" {
		http.Error(w, "Invalid request: the code of the link is needed", http.StatusBadRequest)
		return
	}
	rev, err := repman.openSecretReveal(strings.TrimSpace(form.Code))
	switch {
	case errors.Is(err, errSecretRevealOff):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	case errors.Is(err, errSecretRevealGone):
		repman.noteSecretRevealRefused(r.RemoteAddr)
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	case errors.Is(err, errSecretRevealSession):
		// not burned, not an attack: the page opens it next through the signed-in route
		w.Header().Set(secretRevealSessionHeader, "true")
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	case err != nil:
		repman.Logrus.Warnf("One-time secret link: %s", err)
		http.Error(w, "The link could not be read", http.StatusInternalServerError)
		return
	}
	repman.logSecurityEvent("secret_reveal_opened", rev.For, r.RemoteAddr,
		fmt.Sprintf("One-time secret link of %s opened (%s)", rev.For, rev.Title))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SecretRevealResult{Title: rev.Title, For: rev.For, Cluster: rev.Cluster, Items: rev.Items})
}

// mailVaultTokenReveal mails cl's Vault token to its mail-to addresses, the cluster's external
// users left out, each as a one-time link of its own. It mailed the token in clear before.
func (repman *ReplicationManager) mailVaultTokenReveal(cl *cluster.Cluster) {
	token := cl.Conf.Secrets.Get("vault-token").Value
	if token == "" {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModVault, config.LvlErr, "Vault token not mailed: no vault-token")
		return
	}
	sent := 0
	for _, to := range strings.Split(cl.Conf.MailTo, ",") {
		to = strings.TrimSpace(to)
		if to == "" || externalUser(cl.Conf.APIUsersExternal, to) {
			continue
		}
		if _, err := repman.mailSecretReveal(to, cl.Name, "Replication-Manager Vault Token",
			"Here is the Vault token of cluster "+cl.Name+": it lets you see your passwords in Vault, "+cl.Conf.VaultServerAddr+".",
			[]SecretRevealItem{{Label: "Vault", Value: cl.Conf.VaultServerAddr}, {Label: "Token", Value: token}}); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModVault, config.LvlErr, "Vault token not mailed to %s: %s", to, err)
			continue
		}
		sent++
	}
	if sent == 0 {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModVault, config.LvlErr, "Vault token mailed to no one: no valid address in mail-to")
	}
}

// externalUser: login is a user of the list users ("user:password,..."), compared whole.
func externalUser(users, login string) bool {
	for _, e := range strings.Split(users, ",") {
		if u, _, _ := strings.Cut(strings.TrimSpace(e), ":"); u != "" && strings.EqualFold(u, login) {
			return true
		}
	}
	return false
}
