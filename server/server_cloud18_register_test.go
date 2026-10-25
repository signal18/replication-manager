// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/githelper"
	log "github.com/sirupsen/logrus"
)

// registerFakes is a Cloud18 realm (refresh + identity broker) and a GitLab (groups, projects,
// project tokens, raw files) for a registration through Cloud18 (#2159).
type registerFakes struct {
	mu        sync.Mutex
	linked    bool
	nextID    int
	groups    map[string]int
	projects  map[string]int    // "group/path" -> id
	files     map[int]string    // project id -> default.toml
	tokens    map[string]string // created project tokens -> project id (as text)
	brokerHit int
}

func newRegisterFakes(t *testing.T) (*registerFakes, string) {
	f := &registerFakes{linked: true, nextID: 10, groups: map[string]int{}, projects: map[string]int{}, files: map[int]string{}, tokens: map[string]string{}}
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"2","preferred_username":"acme"}`))
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/protocol/openid-connect/token"):
			fmt.Fprintf(w, `{"access_token":"h.%s.s","expires_in":300}`, payload)
		case strings.HasSuffix(r.URL.Path, "/broker/"+config.ConstCloud18GitLabProvider+"/token"):
			f.brokerHit++
			if !f.linked {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"errorMessage":"User is not linked with identity provider"}`)
				return
			}
			fmt.Fprint(w, `{"access_token":"person","expires_in":7200}`)
		default:
			http.NotFound(w, r)
		}
	}))
	gitlab := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		path := strings.TrimPrefix(r.URL.EscapedPath(), "/api/v4")
		if r.Header.Get("Authorization") != "Bearer person" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		f.nextID++
		switch {
		case path == "/user":
			fmt.Fprint(w, `{"id":2,"username":"acme","email":"admin@acme.example"}`)
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
		case strings.HasSuffix(path, "/access_tokens"):
			tok := fmt.Sprintf("glpat-%d", f.nextID)
			f.tokens[tok] = path
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"token":%q,"expires_at":%q}`, tok, body["expires_at"])
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
	}))
	old := githelper.GitLabURL
	githelper.GitLabURL = gitlab.URL
	t.Cleanup(func() { githelper.GitLabURL = old; realm.Close(); gitlab.Close() })
	return f, realm.URL + "/realms/cloud18"
}

// registerRepman is an instance someone connected to the realm (its login file written).
func registerRepman(t *testing.T, issuer string, key []byte) *ReplicationManager {
	t.Helper()
	dir := t.TempDir()
	conf := &config.Config{WorkingDir: dir, SecretKey: key, Cloud18IdentityURL: issuer, OAuthProvider: "https://gitlab.example"}
	conf.Secrets = config.NewSecrets(nil)
	data, _ := json.Marshal(map[string]string{"issuer": issuer, "refresh_token": conf.GetEncryptedString("offline-1"), "user": "acme", "sub": "2"})
	if err := os.WriteFile(filepath.Join(dir, "cloud18-identity.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return &ReplicationManager{Conf: conf, Clusters: map[string]*cluster.Cluster{}, Logrus: log.New()}
}

func regResult(t *testing.T, repman *ReplicationManager) (string, string, cloud18RegisterResult) {
	t.Helper()
	state, msg, raw := repman.RegStatus.snapshot()
	var r cloud18RegisterResult
	json.Unmarshal(raw, &r)
	return state, msg, r
}

// #2159: a registration through Cloud18 declares the instance in GitLab with the person's GitLab
// token from the realm (never kept): new URI created; the same URI again from the same instance
// (its key decrypts the saved config) reconnects; from another instance it is refused; a Cloud18
// account without GitLab linked is asked to link one.
func TestCloud18Register(t *testing.T) {
	f, issuer := newRegisterFakes(t)
	key := []byte("0123456789abcdef")

	first := registerRepman(t, issuer, key)
	first.cloud18Register("acme", "ovh", "fr-1", "free")
	state, msg, res := regResult(t, first)
	if state != RegStateOK || res.Reattach {
		t.Fatalf("new URI: %s %q %+v", state, msg, res)
	}
	c := first.Conf
	if !c.Cloud18 || !c.Cloud18GitProjects() || c.Cloud18GitPullProjectID == 0 || c.Cloud18Domain != "acme" || c.Cloud18GitUser != "admin@acme.example" {
		t.Fatalf("settings: cloud18 %v projects %d/%d domain %q user %q", c.Cloud18, c.Cloud18GitProjectID, c.Cloud18GitPullProjectID, c.Cloud18Domain, c.Cloud18GitUser)
	}
	main, pull := c.GetDecryptedValue("git-acces-token"), c.GitPullToken()
	if !strings.HasPrefix(main, "glpat-") || !strings.HasPrefix(pull, "glpat-") || main == pull || c.Cloud18GitTokenExpires == "" {
		t.Fatalf("project tokens: main %q pull %q end %q", main, pull, c.Cloud18GitTokenExpires)
	}
	if c.GitUrl != "https://gitlab.example/acme/ovh-fr-1.git" || c.GitUrlPull != "https://gitlab.example/acme/ovh-fr-1-pull.git" {
		t.Fatalf("git urls %q %q", c.GitUrl, c.GitUrlPull)
	}
	for _, s := range []string{fmt.Sprint(c.GetDecryptedValue("git-acces-token")), c.GitPullToken()} {
		if s == "person" {
			t.Fatal("the person's GitLab token must never be kept")
		}
	}

	// the repository now holds the saved config, encrypted with the instance's key
	f.mu.Lock()
	f.files[c.Cloud18GitProjectID] = "git-acces-token = \"" + c.GetEncryptedString("glpat-saved-token-0001") + "\"\n"
	f.mu.Unlock()

	again := registerRepman(t, issuer, key) // same key: the same instance reinstalled, or a peer
	again.cloud18Register("acme", "ovh", "fr-1", "")
	if state, msg, res := regResult(t, again); state != RegStateOK || !res.Reattach || again.Conf.Cloud18GitProjectID != c.Cloud18GitProjectID {
		t.Fatalf("reconnect: %s %q %+v project %d", state, msg, res, again.Conf.Cloud18GitProjectID)
	}

	stranger := registerRepman(t, issuer, []byte("fedcba9876543210"))
	stranger.cloud18Register("acme", "ovh", "fr-1", "")
	if state, msg, _ := regResult(t, stranger); state != RegStateTaken || stranger.Conf.Cloud18 || stranger.Conf.Cloud18GitProjects() {
		t.Fatalf("another instance's URI: %s %q", state, msg)
	}

	f.mu.Lock()
	f.linked = false
	f.mu.Unlock()
	unlinked := registerRepman(t, issuer, key)
	unlinked.cloud18Register("acme", "ovh", "fr-2", "")
	if state, _, res := regResult(t, unlinked); state != RegStateLink || !strings.Contains(res.LinkURL, "/account/") {
		t.Fatalf("no GitLab linked: %s %+v", state, res)
	}
}
