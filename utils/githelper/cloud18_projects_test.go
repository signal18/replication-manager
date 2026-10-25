// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package githelper

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitLab answers the calls of a registration through Cloud18 (#2159) like GitLab 18.11 did
// in the live test: groups, members, projects (an existing path refused), project tokens and
// their self-rotation.
type fakeGitLab struct {
	mu       sync.Mutex
	groups   map[string]int
	members  map[string]int // "group/user" -> access level
	projects map[string]int // "group/path" -> id
	tokens   map[string]int // project token -> project id
	nextID   int
}

func newFakeGitLab(t *testing.T) (*fakeGitLab, *httptest.Server) {
	f := &fakeGitLab{groups: map[string]int{}, members: map[string]int{}, projects: map[string]int{}, tokens: map[string]int{}, nextID: 100}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	old := GitLabURL
	GitLabURL = srv.URL
	t.Cleanup(func() { GitLabURL = old; srv.Close() })
	return f, srv
}

func (f *fakeGitLab) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/api/v4")
	bearer := r.Header.Get("Authorization") == "Bearer person"
	var body map[string]interface{}
	json.NewDecoder(r.Body).Decode(&body)
	f.nextID++
	switch {
	case path == "/user" && bearer:
		fmt.Fprint(w, `{"id":2,"username":"acme","email":"admin@acme.example"}`)
	case strings.HasPrefix(path, "/groups/") && strings.Contains(path, "/members/all/") && bearer:
		parts := strings.Split(path, "/")
		if lvl, ok := f.members[parts[2]+"/"+parts[5]]; ok {
			fmt.Fprintf(w, `{"access_level":%d}`, lvl)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	case strings.HasPrefix(path, "/groups/") && r.Method == http.MethodGet && bearer:
		name := strings.TrimPrefix(path, "/groups/")
		if id, ok := f.groups[name]; ok {
			fmt.Fprintf(w, `{"id":%d}`, id)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"404 Group Not Found"}`)
	case path == "/groups" && r.Method == http.MethodPost && bearer:
		f.groups[body["path"].(string)] = f.nextID
		f.members[fmt.Sprintf("%d/2", f.nextID)] = 50
		fmt.Fprintf(w, `{"id":%d}`, f.nextID)
	case path == "/projects" && r.Method == http.MethodPost && bearer:
		key := fmt.Sprintf("%v/%v", body["namespace_id"], body["path"])
		if _, ok := f.projects[key]; ok {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"message":{"path":["has already been taken"]}}`)
			return
		}
		f.projects[key] = f.nextID
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":%d}`, f.nextID)
	case strings.HasPrefix(path, "/projects/") && strings.HasSuffix(path, "/access_tokens") && bearer:
		tok := fmt.Sprintf("glpat-%d", f.nextID)
		var pid int
		fmt.Sscanf(strings.TrimPrefix(path, "/projects/"), "%d", &pid)
		f.tokens[tok] = pid
		if fmt.Sprint(body["scopes"]) != "[write_repository self_rotate]" || fmt.Sprint(body["access_level"]) != "40" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":%d,"token":%q,"expires_at":%q}`, f.nextID, tok, body["expires_at"])
	case strings.HasSuffix(path, "/access_tokens/self/rotate"):
		old := r.Header.Get("PRIVATE-TOKEN")
		if _, ok := f.tokens[old]; !ok {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"message":"401 Unauthorized"}`)
			return
		}
		pid := f.tokens[old]
		delete(f.tokens, old)
		tok := fmt.Sprintf("glpat-%d", f.nextID)
		f.tokens[tok] = pid
		fmt.Fprintf(w, `{"token":%q,"expires_at":%q}`, tok, r.URL.Query().Get("expires_at"))
	case strings.HasPrefix(path, "/projects/") && r.Method == http.MethodGet && bearer:
		name, _ := urlUnescape(strings.TrimPrefix(path, "/projects/"))
		parts := strings.SplitN(name, "/", 2)
		if len(parts) == 2 {
			if id, ok := f.projects[fmt.Sprintf("%d/%s", f.groups[parts[0]], parts[1])]; ok {
				fmt.Fprintf(w, `{"id":%d}`, id)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	default:
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"401 Unauthorized"}`)
	}
}

func urlUnescape(s string) (string, error) {
	return strings.ReplaceAll(s, "%2F", "/"), nil
}

// #2159: a new domain is created (the person becomes its owner), an existing one needs the
// person among its members; a project path is unique (GitLab refuses it, the existing id comes
// back); project tokens are created with their scopes and renew themselves, the old one refused.
func TestCloud18GitLabRegistration(t *testing.T) {
	f, _ := newFakeGitLab(t)

	u, err := GitLabTokenUser("person")
	if err != nil || u.ID != 2 || u.Email != "admin@acme.example" {
		t.Fatalf("user %+v %v", u, err)
	}
	gid, err := GitLabEnsureDomainGroup("person", "acme", u.ID)
	if err != nil || gid == 0 {
		t.Fatalf("new domain: %d %v", gid, err)
	}
	if again, err := GitLabEnsureDomainGroup("person", "acme", u.ID); err != nil || again != gid {
		t.Fatalf("own domain: %d %v", again, err)
	}
	f.groups["other"] = 999
	if _, err := GitLabEnsureDomainGroup("person", "other", u.ID); !errors.Is(err, ErrNotDomainMember) {
		t.Fatalf("someone else's domain: %v", err)
	}
	f.members["999/2"] = 20 // reporter: cannot create projects
	if _, err := GitLabEnsureDomainGroup("person", "other", u.ID); !errors.Is(err, ErrNotDomainMember) {
		t.Fatalf("a reporter of the domain: %v", err)
	}

	pid, taken, err := GitLabCreateProjectIn("person", gid, "acme", "ovh-fr-1")
	if err != nil || taken || pid == 0 {
		t.Fatalf("new project: %d %v %v", pid, taken, err)
	}
	again, taken, err := GitLabCreateProjectIn("person", gid, "acme", "ovh-fr-1")
	if err != nil || !taken || again != pid {
		t.Fatalf("an existing path: %d %v %v (want %d taken)", again, taken, err, pid)
	}

	tok, err := GitLabCreateProjectToken("person", pid, "replication-manager acme.ovh.fr-1", time.Now().Add(364*24*time.Hour))
	if err != nil || tok.Token == "" || tok.ExpiresAt == "" {
		t.Fatalf("project token: %+v %v", tok, err)
	}
	next, err := GitLabRotateProjectToken(tok.Token, pid, time.Now().Add(364*24*time.Hour))
	if err != nil || next.Token == "" || next.Token == tok.Token {
		t.Fatalf("self-rotation: %+v %v", next, err)
	}
	if _, err := GitLabRotateProjectToken(tok.Token, pid, time.Now()); GitLabStatus(err) != http.StatusUnauthorized {
		t.Fatalf("the rotated token must be refused: %v", err)
	}
}
