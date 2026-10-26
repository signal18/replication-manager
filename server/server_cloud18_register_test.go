// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/githelper"
	log "github.com/sirupsen/logrus"
)

// registerFakes plays a GitLab whose identity provider is a Cloud18 realm, as validated live
// (#2159): GitLab's OAuth sends an unknown browser to its sign-in page, "Sign in with Cloud18" to
// the realm's login form, the realm back to GitLab's callback, GitLab answers the authorization on
// its "Redirecting" page (non-https redirect), and the code is traded with PKCE. The realm also has
// its sign-up form (an account without password, waiting for its email confirmation).
type registerFakes struct {
	mu         sync.Mutex
	accounts   map[string]string // email -> password ("" = not activated yet)
	gitlabPw   map[string]string // GitLab user -> GitLab password (an instance registered the old way)
	signedUp   []string
	challenges map[string]string // code -> PKCE challenge
	nextID     int
	groups     map[string]int
	projects   map[string]int
	files      map[int]string
	logins     int
	revoked    []string
	signIns    int                     // POSTs to GitLab's password sign-in
	evilAction string                  // the realm login page posts there instead (a page that changed hands)
	failCreate int                     // the next project token creations that fail
	tokens     map[string][]*fakeToken // ".../projects/<id>/access_tokens" -> the project's tokens
	// the realm's login client (#2182)
	noLoginClient bool              // the realm has no replication-manager-login client (not set up yet)
	realmKey      *rsa.PrivateKey   // signs the realm tokens, published at .../certs
	issuer        string            // the realm's issuer
	loginCodes    map[string]string // code -> PKCE challenge|email
	refreshes     int               // refresh grants answered
	refreshTries  int               // refresh grants asked, refused ones included
	certFetches   int               // fetches of the realm's keys
	rotate        bool              // each refresh gives a new refresh token and revokes the used one
	shortTokens   bool              // access tokens of 30 s: every call refreshes
	liveRefresh   map[string]bool   // refresh tokens still valid, when rotating
}

type fakeToken struct {
	id     int
	name   string
	active bool
}

func newRegisterFakes(t *testing.T) (*registerFakes, string) {
	realmKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &registerFakes{accounts: map[string]string{"admin@acme.example": "s3cr3t-cloud18"}, gitlabPw: map[string]string{"admin@acme.example": "old-gitlab-password"}, challenges: map[string]string{},
		nextID: 10, groups: map[string]int{}, projects: map[string]int{}, files: map[int]string{}, tokens: map[string][]*fakeToken{},
		realmKey: realmKey, loginCodes: map[string]string{}}
	var gitlabURL, realmURL string

	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		r.ParseForm()
		switch r.URL.Path {
		case "/realms/cloud18/login":
			action := realmURL
			if f.evilAction != "" {
				action = f.evilAction
			}
			fmt.Fprintf(w, `<form id="kc-form-login" action="%s/realms/cloud18/login-actions/authenticate?session_code=1&amp;tab_id=2" method="post">`, action)
		case "/realms/cloud18/login-actions/authenticate":
			pw, ok := f.accounts[r.Form.Get("username")]
			switch {
			case ok && pw == "":
				fmt.Fprint(w, `<h1 id="kc-page-title">Email verification</h1><div class="kc-feedback-text">You need to verify your email address to activate your account.</div>`)
			case ok && pw == r.Form.Get("password"):
				f.logins++
				http.SetCookie(w, &http.Cookie{Name: "KEYCLOAK_IDENTITY", Value: url.QueryEscape(r.Form.Get("username")), Path: "/realms/cloud18/"})
				http.Redirect(w, r, gitlabURL+"/users/auth/openid_connect/callback?code=kc&user="+url.QueryEscape(r.Form.Get("username")), http.StatusFound)
			default:
				fmt.Fprintf(w, `<span class="input-error">Invalid username or password.</span><form id="kc-form-login" action="%s/realms/cloud18/login-actions/authenticate" method="post">`, realmURL)
			}
		case "/realms/cloud18/protocol/openid-connect/auth":
			c, err := r.Cookie("KEYCLOAK_IDENTITY")
			if err != nil || f.noLoginClient || r.Form.Get("client_id") != config.ConstCloud18LoginClientID || r.Form.Get("code_challenge_method") != "S256" {
				http.Error(w, "Client not found.", 400)
				return
			}
			email, _ := url.QueryUnescape(c.Value)
			code := fmt.Sprintf("kc-code-%d", f.nextID)
			f.nextID++
			f.loginCodes[code] = r.Form.Get("code_challenge") + "|" + email
			http.Redirect(w, r, r.Form.Get("redirect_uri")+"?code="+code+"&state="+url.QueryEscape(r.Form.Get("state")), http.StatusFound)
		case "/realms/cloud18/protocol/openid-connect/token":
			email := ""
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				parts := strings.SplitN(f.loginCodes[r.Form.Get("code")], "|", 2)
				sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				if len(parts) != 2 || base64.RawURLEncoding.EncodeToString(sum[:]) != parts[0] || r.Form.Get("client_id") != config.ConstCloud18LoginClientID {
					http.Error(w, `{"error":"invalid_grant"}`, 400)
					return
				}
				delete(f.loginCodes, r.Form.Get("code"))
				email = parts[1]
			case "refresh_token":
				f.refreshTries++
				rt := r.Form.Get("refresh_token")
				if !strings.HasPrefix(rt, "rt-") || (f.rotate && f.liveRefresh != nil && !f.liveRefresh[rt] && f.refreshes > 0) {
					http.Error(w, `{"error":"invalid_grant","error_description":"Invalid refresh token"}`, 400)
					return
				}
				f.refreshes++
				email = strings.SplitN(strings.TrimPrefix(rt, "rt-"), "#", 2)[0]
				if f.rotate {
					if f.liveRefresh == nil {
						f.liveRefresh = map[string]bool{}
					}
					delete(f.liveRefresh, rt)
					next := fmt.Sprintf("rt-%s#%d", email, f.refreshes)
					f.liveRefresh[next] = true
					fmt.Fprintf(w, `{"access_token":%q,"expires_in":30,"refresh_token":%q,"refresh_expires_in":1800}`,
						f.realmToken(email, config.ConstCloud18APIAudience, true, 30*time.Second), next)
					return
				}
			default:
				http.Error(w, `{"error":"unsupported_grant_type"}`, 400)
				return
			}
			life := 300
			if f.shortTokens {
				life = 30
			}
			fmt.Fprintf(w, `{"access_token":%q,"expires_in":%d,"refresh_token":%q,"refresh_expires_in":1800}`,
				f.realmToken(email, config.ConstCloud18APIAudience, true, time.Duration(life)*time.Second), life, "rt-"+email)
		case "/realms/cloud18/protocol/openid-connect/certs":
			f.certFetches++
			pub := f.realmKey.PublicKey
			fmt.Fprintf(w, `{"keys":[{"kty":"RSA","kid":"k1","use":"sig","alg":"RS256","n":%q,"e":%q}]}`,
				base64.RawURLEncoding.EncodeToString(pub.N.Bytes()), base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()))
		case "/realms/cloud18/protocol/openid-connect/registrations":
			fmt.Fprintf(w, `<form id="kc-register-form" action="%s/realms/cloud18/login-actions/registration?session_code=3" method="post">`, realmURL)
		case "/realms/cloud18/login-actions/registration":
			email := r.Form.Get("email")
			if _, ok := f.accounts[email]; ok {
				fmt.Fprint(w, `<span id="input-error-email" class="input-error">Email already exists.</span><form id="kc-register-form" action="x">`)
				return
			}
			if r.Form.Get("company") == "" || r.Form.Get("phoneNumber") == "" || r.Form.Get("password") != "" {
				fmt.Fprint(w, `<span class="input-error">Please specify this field.</span><form id="kc-register-form" action="x">`)
				return
			}
			f.accounts[email] = ""
			f.signedUp = append(f.signedUp, email)
			fmt.Fprint(w, `<h1 id="kc-page-title">Email verification</h1>`)
		default:
			http.NotFound(w, r)
		}
	}))
	gitlab := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		r.ParseForm()
		path := r.URL.EscapedPath()
		switch {
		case path == "/oauth/authorize":
			if c, err := r.Cookie("gl_session"); err == nil && c.Value != "" {
				if r.Form.Get("client_id") != config.ConstCloud18GitLabOAuthClientID || r.Form.Get("code_challenge_method") != "S256" {
					http.Error(w, "bad authorize", 400)
					return
				}
				code := fmt.Sprintf("code-%d", f.nextID)
				f.nextID++
				f.challenges[code] = r.Form.Get("code_challenge") + "|" + c.Value
				fmt.Fprintf(w, `<h1>Redirecting</h1><a href="%s?code=%s&amp;state=%s">Click here</a>`, r.Form.Get("redirect_uri"), code, r.Form.Get("state"))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "after", Value: url.QueryEscape(r.URL.String()), Path: "/"})
			http.Redirect(w, r, "/users/sign_in", http.StatusFound)
		case path == "/users/sign_in" && r.Method == http.MethodPost:
			f.signIns++
			if pw, ok := f.gitlabPw[r.Form.Get("user[login]")]; ok && pw == r.Form.Get("user[password]") && r.Form.Get("authenticity_token") == "csrf+1" {
				http.SetCookie(w, &http.Cookie{Name: "gl_session", Value: url.QueryEscape(r.Form.Get("user[login]")), Path: "/"})
				after, _ := r.Cookie("after")
				back, _ := url.QueryUnescape(after.Value)
				http.Redirect(w, r, back, http.StatusFound)
				return
			}
			fmt.Fprint(w, `<meta name="csrf-token" content="csrf&#43;1"><input name="user[login]"><input name="user[password]">`)
		case path == "/users/sign_in":
			fmt.Fprint(w, `<meta name="csrf-token" content="csrf&#43;1"><input name="user[login]"><input name="user[password]">`)
		case path == "/users/auth/openid_connect" && r.Method == http.MethodPost:
			if r.Form.Get("authenticity_token") != "csrf+1" {
				http.Error(w, "csrf", 422)
				return
			}
			http.Redirect(w, r, realmURL+"/realms/cloud18/login", http.StatusFound)
		case path == "/users/auth/openid_connect/callback":
			http.SetCookie(w, &http.Cookie{Name: "gl_session", Value: url.QueryEscape(r.Form.Get("user")), Path: "/"})
			after, _ := r.Cookie("after")
			back, _ := url.QueryUnescape(after.Value)
			http.Redirect(w, r, back, http.StatusFound)
		case path == "/oauth/token":
			parts := strings.SplitN(f.challenges[r.Form.Get("code")], "|", 2)
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if len(parts) != 2 || base64.RawURLEncoding.EncodeToString(sum[:]) != parts[0] {
				http.Error(w, `{"error":"invalid_grant"}`, 400)
				return
			}
			delete(f.challenges, r.Form.Get("code"))
			fmt.Fprintf(w, `{"access_token":"gl-%s","token_type":"Bearer","expires_in":7200}`, parts[1])
		case strings.HasPrefix(path, "/api/v4/"):
			f.api(w, r, strings.TrimPrefix(path, "/api/v4"))
		default:
			http.NotFound(w, r)
		}
	}))
	gitlabURL, realmURL = gitlab.URL, realm.URL
	f.issuer = realm.URL + "/realms/cloud18"
	old := githelper.GitLabURL
	githelper.GitLabURL = gitlab.URL
	t.Cleanup(func() { githelper.GitLabURL = old; realm.Close(); gitlab.Close() })
	return f, realm.URL + "/realms/cloud18"
}

// realmToken is a token of the realm for email, signed with its key (call with f.mu held or
// before the fakes serve).
func (f *registerFakes) realmToken(email, aud string, verified bool, life time.Duration) string {
	return signRealmToken(f.realmKey, f.issuer, email, aud, verified, life)
}

// signRealmToken is a realm token signed with key for issuer.
func signRealmToken(key *rsa.PrivateKey, issuer, email, aud string, verified bool, life time.Duration) string {
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": issuer, "aud": []string{aud, "account"}, "azp": config.ConstCloud18LoginClientID,
		"sub": "kc-" + email, "email": email, "email_verified": verified, "name": "A Person", "exp": time.Now().Add(life).Unix(), "iat": time.Now().Unix()})
	tok.Header["kid"] = "k1"
	s, err := tok.SignedString(key)
	if err != nil {
		panic(err)
	}
	return s
}

// api is the GitLab API part: only the person's token (gl-<email>) is accepted.
func (f *registerFakes) api(w http.ResponseWriter, r *http.Request, path string) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer gl-") {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)
	f.nextID++
	switch {
	case path == "/user":
		email, _ := url.QueryUnescape(strings.TrimPrefix(auth, "Bearer gl-"))
		fmt.Fprintf(w, `{"id":2,"username":"acme","email":%q}`, email)
	case strings.Contains(path, "/members/all/"):
		fmt.Fprint(w, `{"access_level":50}`)
	case strings.HasPrefix(path, "/groups/"):
		if id, ok := f.groups[strings.TrimPrefix(path, "/groups/")]; ok {
			fmt.Fprintf(w, `{"id":%d}`, id)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	case path == "/groups":
		f.groups[body["path"].(string)] = f.nextID
		fmt.Fprintf(w, `{"id":%d}`, f.nextID)
	case path == "/projects" && r.Method == http.MethodPost:
		key := fmt.Sprintf("%v/%v", body["namespace_id"], body["path"])
		if _, ok := f.projects[key]; ok {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"message":{"path":["has already been taken"]}}`)
			return
		}
		f.projects[key] = f.nextID
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":%d}`, f.nextID)
	case strings.HasSuffix(path, "/access_tokens/self/rotate"):
		w.WriteHeader(http.StatusUnauthorized)
	case strings.Contains(path, "/access_tokens") && r.Method == http.MethodGet:
		// the project's tokens as created, under their real names, plus someone else's
		list := []map[string]interface{}{{"id": 8, "name": "someone else", "active": true, "revoked": false}}
		for _, tk := range f.tokens[strings.SplitN(path, "?", 2)[0]] {
			list = append(list, map[string]interface{}{"id": tk.id, "name": tk.name, "active": tk.active, "revoked": !tk.active})
		}
		json.NewEncoder(w).Encode(list)
	case strings.Contains(path, "/access_tokens/") && r.Method == http.MethodDelete:
		f.revoked = append(f.revoked, path)
		project := path[:strings.LastIndex(path, "/")]
		for _, tk := range f.tokens[project] {
			if strings.HasSuffix(path, fmt.Sprintf("/%d", tk.id)) {
				tk.active = false
			}
		}
		w.WriteHeader(http.StatusNoContent)
	case strings.HasSuffix(path, "/access_tokens") && f.failCreate > 0:
		f.failCreate--
		w.WriteHeader(http.StatusInternalServerError)
	case strings.HasSuffix(path, "/access_tokens"):
		id := 1000 + f.nextID
		f.tokens[path] = append(f.tokens[path], &fakeToken{id: id, name: fmt.Sprint(body["name"]), active: true})
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":%d,"token":"glpat-%d","expires_at":%q}`, id, f.nextID, body["expires_at"])
	case strings.Contains(path, "/repository/files/"):
		var pid int
		fmt.Sscanf(strings.TrimPrefix(path, "/projects/"), "%d", &pid)
		if c, ok := f.files[pid]; ok {
			fmt.Fprint(w, c)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	case strings.HasPrefix(path, "/projects/"):
		name := strings.ReplaceAll(strings.TrimPrefix(path, "/projects/"), "%2F", "/")
		parts := strings.SplitN(name, "/", 2)
		if id, ok := f.projects[fmt.Sprintf("%d/%s", f.groups[parts[0]], parts[1])]; ok {
			fmt.Fprintf(w, `{"id":%d}`, id)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func registerRepman(t *testing.T, issuer string, key []byte) *ReplicationManager {
	t.Helper()
	conf := &config.Config{WorkingDir: t.TempDir(), SecretKey: key, Cloud18IdentityURL: issuer, OAuthProvider: "https://gitlab.example"}
	conf.Secrets = config.NewSecrets(nil)
	return &ReplicationManager{Conf: conf, Clusters: map[string]*cluster.Cluster{}, Logrus: log.New()}
}

func regResult(t *testing.T, repman *ReplicationManager) (string, string, cloud18RegisterResult) {
	t.Helper()
	state, msg, raw := repman.RegStatus.snapshot()
	var r cloud18RegisterResult
	json.Unmarshal(raw, &r)
	return state, msg, r
}

// #2159: a registration with the admin's Cloud18 email and password declares the instance in
// GitLab with the person's GitLab token (GitLab logging them in with Cloud18; never kept): new URI
// created; the same URI from the same instance (its key decrypts the saved config) reconnects; from
// another instance it is refused; a wrong password is refused; an unknown email with the account
// details gets a Cloud18 account (no password sent: chosen from the confirmation email).
func TestCloud18Register(t *testing.T) {
	f, issuer := newRegisterFakes(t)
	key := []byte("0123456789abcdef")

	first := registerRepman(t, issuer, key)
	first.cloud18Register("acme", "ovh", "fr-1", "free", "admin@acme.example", "s3cr3t-cloud18", nil)
	state, msg, res := regResult(t, first)
	if state != RegStateOK || res.Reattach || res.User != "admin@acme.example" {
		t.Fatalf("new URI: %s %q %+v", state, msg, res)
	}
	c := first.Conf
	if !c.Cloud18 || !c.Cloud18GitProjects() || c.Cloud18GitPullProjectID == 0 || c.Cloud18Domain != "acme" || c.Cloud18GitUser != "admin@acme.example" || c.Cloud18SubscriptionPlan != "free" {
		t.Fatalf("settings: cloud18 %v projects %d/%d domain %q user %q plan %q", c.Cloud18, c.Cloud18GitProjectID, c.Cloud18GitPullProjectID, c.Cloud18Domain, c.Cloud18GitUser, c.Cloud18SubscriptionPlan)
	}
	main, pull := c.GetDecryptedValue("git-acces-token"), c.GitPullToken()
	if !strings.HasPrefix(main, "glpat-") || !strings.HasPrefix(pull, "glpat-") || main == pull || c.Cloud18GitTokenExpires == "" {
		t.Fatalf("project tokens: main %q pull %q end %q", main, pull, c.Cloud18GitTokenExpires)
	}
	if c.GitUrl != "https://gitlab.example/acme/ovh-fr-1.git" || c.GitUrlPull != "https://gitlab.example/acme/ovh-fr-1-pull.git" {
		t.Fatalf("git urls %q %q", c.GitUrl, c.GitUrlPull)
	}
	if c.GetDecryptedValue("cloud18-gitlab-password") != "" || strings.HasPrefix(main, "gl-") {
		t.Fatal("neither the Cloud18 password nor the person's GitLab token may be kept")
	}

	f.mu.Lock()
	f.files[c.Cloud18GitProjectID] = "git-acces-token = \"" + c.GetEncryptedString("glpat-saved-token-0001") + "\"\n"
	f.mu.Unlock()
	again := registerRepman(t, issuer, key)
	again.cloud18Register("acme", "ovh", "fr-1", "", "admin@acme.example", "s3cr3t-cloud18", nil)
	if state, msg, res := regResult(t, again); state != RegStateOK || !res.Reattach || again.Conf.Cloud18GitProjectID != c.Cloud18GitProjectID {
		t.Fatalf("reconnect: %s %q %+v", state, msg, res)
	}

	stranger := registerRepman(t, issuer, []byte("fedcba9876543210"))
	stranger.cloud18Register("acme", "ovh", "fr-1", "", "admin@acme.example", "s3cr3t-cloud18", nil)
	if state, msg, _ := regResult(t, stranger); state != RegStateTaken || stranger.Conf.Cloud18 || stranger.Conf.Cloud18GitProjects() {
		t.Fatalf("another instance's URI: %s %q", state, msg)
	}

	wrong := registerRepman(t, issuer, key)
	wrong.cloud18Register("acme", "ovh", "fr-2", "", "admin@acme.example", "wrong", nil)
	if state, _, _ := regResult(t, wrong); state != RegStateCredentials || wrong.Conf.Cloud18 {
		t.Fatalf("a wrong password: %s", state)
	}
	wrong.cloud18Register("acme", "ovh", "fr-2", "", "admin@acme.example", "wrong", &config.Cloud18SignUpForm{FirstName: "A", LastName: "B", Company: "Acme", Phone: "+33100000000"})
	if state, msg, _ := regResult(t, wrong); state != RegStateCredentials || !strings.Contains(msg, "exists") {
		t.Fatalf("sign-up of an existing account: %s %q", state, msg)
	}

	newcomer := registerRepman(t, issuer, key)
	newcomer.cloud18Register("newco", "ovh", "fr-1", "", "new@newco.example", "", &config.Cloud18SignUpForm{FirstName: "New", LastName: "Comer", Company: "NewCo", Phone: "+33100000001"})
	if state, msg, _ := regResult(t, newcomer); state != RegStateVerify || len(f.signedUp) != 1 || f.signedUp[0] != "new@newco.example" {
		t.Fatalf("sign-up: %s %q %v", state, msg, f.signedUp)
	}
	newcomer.cloud18Register("newco", "ovh", "fr-1", "", "new@newco.example", "chosen-from-email", nil)
	if state, _, _ := regResult(t, newcomer); state != RegStateVerify {
		t.Fatalf("an account not activated yet: %s", state)
	}
	f.mu.Lock()
	f.accounts["new@newco.example"] = "chosen-from-email" // the person confirmed the email and chose the password
	f.mu.Unlock()
	newcomer.cloud18Register("newco", "ovh", "fr-1", "", "new@newco.example", "chosen-from-email", nil)
	if state, msg, _ := regResult(t, newcomer); state != RegStateOK || newcomer.Conf.Cloud18GitUser != "new@newco.example" {
		t.Fatalf("after the confirmation: %s %q", state, msg)
	}
}

// #2159 review: the Cloud18 password goes only to the realm's own host (a login form posting
// elsewhere is refused, nothing sent); a re-register keeps the previous project tokens when the new
// ones cannot be made.
func TestCloud18RegisterSafety(t *testing.T) {
	f, issuer := newRegisterFakes(t)
	key := []byte("0123456789abcdef")

	var evilHits int
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { evilHits++ }))
	defer evil.Close()
	f.mu.Lock()
	f.evilAction = evil.URL
	f.mu.Unlock()
	rm := registerRepman(t, issuer, key)
	rm.cloud18Register("acme", "ovh", "fr-1", "", "admin@acme.example", "s3cr3t-cloud18", nil)
	if state, msg, _ := regResult(t, rm); state != RegStateError || evilHits != 0 || !strings.Contains(msg, "outside") {
		t.Fatalf("a login form posting elsewhere: %s %q, %d requests there", state, msg, evilHits)
	}
	f.mu.Lock()
	f.evilAction = ""
	f.mu.Unlock()

	f.mu.Lock()
	f.revoked, f.failCreate = nil, 1
	f.mu.Unlock()
	again := registerRepman(t, issuer, key)
	f.mu.Lock()
	f.files[11] = "git-acces-token = \"" + again.Conf.GetEncryptedString("glpat-saved-token-0001") + "\"\n"
	f.mu.Unlock()
	again.cloud18Register("acme", "ovh", "fr-1", "", "admin@acme.example", "s3cr3t-cloud18", nil)
	if state, _, _ := regResult(t, again); state != RegStateError || len(f.revoked) != 0 {
		t.Fatalf("a failed token creation must leave the previous tokens: %s, revoked %v", state, f.revoked)
	}
}

// #2159: a registered instance registers again only under its own URI (a reconnect); another URI
// is refused before anything is asked to the realm or GitLab (seen on dev: the clone kept its
// old repository with the new repositories' tokens).
func TestCloud18RegisterOtherURIRefused(t *testing.T) {
	f, issuer := newRegisterFakes(t)
	rm := registerRepman(t, issuer, []byte("0123456789abcdef"))
	rm.cloud18Register("acme", "ovh", "fr-1", "", "admin@acme.example", "s3cr3t-cloud18", nil)
	if state, msg, _ := regResult(t, rm); state != RegStateOK {
		t.Fatalf("setup: %s %q", state, msg)
	}
	logins := f.logins
	rm.cloud18Register("acme", "ovh", "fr-9", "", "admin@acme.example", "s3cr3t-cloud18", nil)
	if state, msg, _ := regResult(t, rm); state != RegStateError || !strings.Contains(msg, "unregister it first") || f.logins != logins || rm.Conf.Cloud18SubDomainZone != "fr-1" {
		t.Fatalf("another URI: %s %q, logins %d -> %d, zone %q", state, msg, logins, f.logins, rm.Conf.Cloud18SubDomainZone)
	}
	rm.cloud18Register("acme", "ovh", "fr-1", "", "admin@acme.example", "s3cr3t-cloud18", nil)
	if state, msg, res := regResult(t, rm); state != RegStateOK || !res.Reattach {
		t.Fatalf("the same URI again is a reconnect: %s %q", state, msg)
	}
}

// #2159: the peers of an HA pair share the URI; each one's project tokens carry its own name (its
// arbitration unique id), so a peer's reconnect never revokes the other's tokens.
func TestCloud18PeerTokensNotRevoked(t *testing.T) {
	f, issuer := newRegisterFakes(t)
	key := []byte("0123456789abcdef")
	first := registerRepman(t, issuer, key)
	first.cloud18Register("acme", "ovh", "fr-1", "", "admin@acme.example", "s3cr3t-cloud18", nil)
	if state, msg, _ := regResult(t, first); state != RegStateOK {
		t.Fatalf("setup: %s %q", state, msg)
	}
	f.mu.Lock()
	f.revoked = nil
	f.mu.Unlock()
	peer := registerRepman(t, issuer, key)
	peer.Conf.ArbitrationSasUniqueId = 2
	peer.cloud18Register("acme", "ovh", "fr-1", "", "admin@acme.example", "s3cr3t-cloud18", nil)
	if state, msg, _ := regResult(t, peer); state != RegStateOK {
		t.Fatalf("the peer: %s %q", state, msg)
	}
	if len(f.revoked) != 0 {
		t.Fatalf("the peer revoked the first instance's tokens: %v", f.revoked)
	}
	names := map[string]int{}
	for _, list := range f.tokens {
		for _, tk := range list {
			if tk.active {
				names[tk.name]++
			}
		}
	}
	if names["replication-manager acme-ovh-fr-1-0"] != 2 || names["replication-manager acme-ovh-fr-1-2"] != 2 {
		t.Fatalf("each peer has its own tokens on both projects: %v", names)
	}
}
