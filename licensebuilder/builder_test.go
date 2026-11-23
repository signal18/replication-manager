package licensebuilder

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/cluster/logplugin"
	"github.com/signal18/replication-manager/utils/githelper"
)

// fakeTree makes a source tree with the wire file the builder reads.
func fakeTree(t *testing.T, dir string, wire string) {
	t.Helper()
	p := filepath.Join(dir, "cluster", "logplugin", "plugins", "wire")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "wire.go"), []byte("package wire\n\nconst WireVersion = "+wire+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestProcess verifies one seat end to end with the commands faked: the release's plugins and the
// seat's licence plugin are packaged with their signatures, the built plugins and the monitor's
// leftovers are removed before the test, and the package is handed to license-pull.
func TestProcess(t *testing.T) {
	tmp := t.TempDir()
	cfg := Config{SourceDir: filepath.Join(tmp, "src"), WorkDir: filepath.Join(tmp, "work"),
		OutDir: filepath.Join(tmp, "out"), DataDir: filepath.Join(tmp, "data"),
		GitBaseURL: "https://gitlab.example.test", GitToken: "SECRETTOKEN"} // the delivery's git calls go through the run stub
	fakeTree(t, cfg.SourceDir, "4")
	release := filepath.Join(cfg.WorkDir, "replication-manager-v3.1.48")
	fakeTree(t, release, "4")
	leftover := filepath.Join(cfg.DataDir, "plugins", "plugin-old")
	os.MkdirAll(filepath.Dir(leftover), 0o755)
	os.WriteFile(leftover, []byte("old"), 0o755)

	b := New(cfg, nil)
	var licenceURI, ensured string
	var gitCalls []string
	b.ensure = func(r Request) error { ensured = r.URI; return nil }
	b.run = func(dir string, env []string, name string, args ...string) error {
		if name == "git" {
			gitCalls = append(gitCalls, strings.Join(args, " "))
			return nil // the -pull delivery: nothing staged, a no-op
		}
		built := []string{"plugin-a", "plugin-b"}
		if len(args) > 0 && args[0] == "plugin-licence" {
			built = []string{"plugin-licence"}
			for _, e := range env {
				if v, ok := strings.CutPrefix(e, "LICENCE_URI="); ok {
					licenceURI = v
				}
			}
		}
		os.MkdirAll(filepath.Join(dir, "build", "plugins"), 0o755)
		os.MkdirAll(filepath.Join(dir, "share", "plugins"), 0o755)
		for _, n := range built {
			os.WriteFile(filepath.Join(dir, "build", "plugins", n), []byte("bin "+n), 0o755)
			os.WriteFile(filepath.Join(dir, "share", "plugins", n+".sig"), []byte("sig "+n), 0o644)
		}
		return nil
	}
	var tested string
	b.test = func(pkg string) ([]string, error) {
		tested = pkg
		if _, err := os.Stat(leftover); !os.IsNotExist(err) {
			t.Error("the monitor's leftover plugin was not removed before the test")
		}
		return []string{"plugin-a", "plugin-b", "plugin-licence"}, nil
	}

	exp := time.Now().AddDate(1, 0, 0).Format("200601") // a year out, so the born-expired guardrail never ages these tests out
	uri := "company." + exp + ".1"
	r := Request{Company: "company", URI: uri, Seat: 1, Expiry: exp, AirGapped: true, Version: "v3.1.48"}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	res := Result{Request: r}
	if err := b.process(&res); err != nil {
		t.Fatal(err)
	}
	if licenceURI != r.URI {
		t.Errorf("plugin-licence built with LICENCE_URI %q, want %q", licenceURI, r.URI)
	}
	if tested != uri+".tar.gz" || res.Package != tested || len(res.Loaded) != 3 {
		t.Fatalf("result %+v, tested %q", res, tested)
	}
	for _, dir := range []string{filepath.Join(release, "build", "plugins"), filepath.Join(cfg.SourceDir, "build", "plugins")} {
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("%s not cleaned after packaging: %d entries", dir, len(entries))
		}
	}
	man, err := logplugin.ReadPackageManifest(filepath.Join(cfg.OutDir, res.Package))
	if err != nil {
		t.Fatal(err)
	}
	if man.URI != r.URI || man.Wire != 4 || man.Platform != "linux/amd64" || len(man.Entries) != 6 {
		t.Fatalf("manifest %+v", man)
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, res.Package)); err != nil {
		t.Errorf("package not placed in the monitor's datadir: %v", err)
	}
	// delivered into the seat's -pull repo, as an online instance's: <company>/<YYYYMM>-<seat>-pull,
	// created first, with the package under packages/
	if ensured != uri {
		t.Errorf("the -pull project of %q not ensured (got %q)", uri, ensured)
	}
	var added string
	for _, c := range gitCalls {
		if strings.HasPrefix(c, "add ") {
			added = c
		}
	}
	if !strings.Contains(added, "packages/"+uri+".tar.gz") || !strings.Contains(added, "plugins/plugin-licence.sig") {
		t.Errorf("git add %q, want the package and the plugins", added)
	}
	if len(gitCalls) == 0 || !strings.Contains(gitCalls[0], "clone") || !strings.Contains(gitCalls[0], "/company/"+exp+"-1-pull.git") {
		t.Errorf("delivery git calls %v, want a clone of company/%s-1-pull", gitCalls, exp)
	}
}

func TestProcessWireMismatch(t *testing.T) {
	tmp := t.TempDir()
	cfg := Config{SourceDir: filepath.Join(tmp, "src"), WorkDir: filepath.Join(tmp, "work"), OutDir: filepath.Join(tmp, "out"), DataDir: filepath.Join(tmp, "data")}
	fakeTree(t, cfg.SourceDir, "5")
	fakeTree(t, filepath.Join(cfg.WorkDir, "replication-manager-v3.1.40"), "4")
	b := New(cfg, nil)
	b.run = func(string, []string, string, ...string) error { return nil }
	uri := "company." + time.Now().AddDate(1, 0, 0).Format("200601") + ".1"
	res := Result{Request: Request{Company: "company", URI: uri, AirGapped: true, Version: "v3.1.40", Platform: "linux/amd64"}}
	if err := b.process(&res); err == nil || !strings.Contains(err.Error(), "wire") {
		t.Fatalf("wire mismatch: err %v", err)
	}
}

func TestValidate(t *testing.T) {
	for _, r := range []Request{
		{Company: "company", URI: "company.2026-12.1", AirGapped: true, Version: "v1"},
		{Company: "other", URI: "company.202612.1", AirGapped: true, Version: "v1"},
		{Company: "company", URI: "company.202612.1", AirGapped: true, Version: "../x"},
		{Company: "company", URI: "company.202001.1", AirGapped: true, Version: "v1"},                     // born-expired: the guardrail must reject
		{Company: "acme", URI: "acme.east.z1", AirGapped: false, Version: "v1"},                           // online, no expiry
		{Company: "acme", URI: "acme.east.z1", AirGapped: false, Version: "v1", Expiry: "202001"},         // online, born-expired
		{Company: "acme", URI: "acme.UP.fr1", AirGapped: false, Version: "v1", Expiry: "2099-12-31"},      // online, not lower-case dotted
		{Company: "acme", URI: "company.202612.1", AirGapped: false, Version: "v1", Expiry: "2099-12-31"}, // online uri not of company
	} {
		if err := r.Validate(); err == nil {
			t.Errorf("Validate(%+v) accepted", r)
		}
	}
	for _, r := range []Request{
		{Company: "company", URI: "company.202612.1", AirGapped: true, Version: "v1"},                      // air-gapped seat
		{Company: "acme", URI: "acme.east.z1", AirGapped: false, Version: "v3.1.48", Expiry: "2099-12-31"}, // online, YYYY-MM-DD expiry
		{Company: "acme", URI: "acme.east.z1", AirGapped: false, Version: "v3.1.48", Expiry: "209912"},     // online, YYYYMM expiry
	} {
		if err := r.Validate(); err != nil {
			t.Errorf("Validate(%+v) rejected: %v", r, err)
		}
	}
}

func TestRecordBounded(t *testing.T) {
	b := New(Config{}, nil)
	for i := 0; i < resultsMax+10; i++ {
		b.record(Result{Request: Request{URI: "u"}, State: "done", Finished: time.Now()})
	}
	if n := len(b.Status()["results"].([]Result)); n != resultsMax {
		t.Fatalf("%d results kept, want %d", n, resultsMax)
	}
}

func TestDeliverToPull(t *testing.T) {
	cfg := Config{GitBaseURL: "https://gitlab.example.test", GitUser: "oauth2", GitToken: "SECRETTOKEN"}
	b := New(cfg, nil)
	var calls [][]string
	b.run = func(dir string, env []string, name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		for _, a := range args {
			if a == "add" { // by now the plugin + .sig are written into dir/plugins
				if _, err := os.Stat(filepath.Join(dir, "plugins", "plugin-licence")); err != nil {
					t.Errorf("plugin binary not written: %v", err)
				}
				if _, err := os.Stat(filepath.Join(dir, "plugins", "plugin-licence.sig")); err != nil {
					t.Errorf("plugin .sig not written: %v", err)
				}
			}
			if a == "diff" {
				return os.ErrInvalid // simulate staged changes so commit+push proceed
			}
		}
		return nil
	}
	lic := []Plugin{{Name: "plugin-licence", Data: []byte("bin"), Sig: []byte("sig")}}
	if err := b.deliverToPull(Request{URI: "acme.east.z1"}, lic, nil); err != nil {
		t.Fatalf("deliverToPull: %v", err)
	}
	var clone, push bool
	for _, c := range calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "clone") {
			clone = true
			if !strings.Contains(j, "acme/east-z1-pull.git") {
				t.Errorf("clone targets the wrong repo: %v", c)
			}
		}
		if strings.Contains(j, "push") {
			push = true
		}
		for _, a := range c { // the token must never ride the remote URL
			if strings.HasPrefix(a, "https://") && strings.Contains(a, cfg.GitToken) {
				t.Errorf("token leaked into a URL arg: %q", a)
			}
		}
	}
	if !clone || !push {
		t.Errorf("expected a clone and a push, got %v", calls)
	}
}

func TestDeliverToPullUnconfigured(t *testing.T) {
	b := New(Config{}, nil) // no GitBaseURL/GitToken
	if err := b.deliverToPull(Request{URI: "acme.east.z1"}, nil, nil); err == nil {
		t.Error("deliverToPull with no git config should fail")
	}
}

// TestDeliverToPullAirGapped pushes an air-gapped seat's plugins with real git into the seat's
// -pull repository, a local bare repository standing for <GitBaseURL>/<company>/<YYYYMM>-<seat>-pull.git
// and EMPTY, as the back office creates it: every plugin and its .sig land under plugins/ in one
// commit, the same plugins again are a no-op, and the token never appears in a commit.
func TestDeliverToPullAirGapped(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	bare := filepath.Join(tmp, "remote", "acme", "202612-1-pull.git")
	gitT := func(dir string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	os.MkdirAll(bare, 0o755)
	gitT(bare, "init", "--bare")

	b := New(Config{GitBaseURL: "file://" + filepath.Join(tmp, "remote"), GitToken: "SECRETTOKEN"}, nil)
	r := Request{Company: "acme", URI: "acme.202612.1", AirGapped: true}
	plugins := []Plugin{{Name: "plugin-a", Data: []byte("bin a"), Sig: []byte("sig a")},
		{Name: "plugin-licence", Data: []byte("bin licence"), Sig: []byte("sig licence")}}
	pkg := map[string][]byte{"packages/acme.202612.1.tar.gz": []byte("package")}
	if err := b.deliverToPull(r, plugins, pkg); err != nil {
		t.Fatalf("deliverToPull: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(check0(t, gitT, tmp, bare), "packages", "acme.202612.1.tar.gz")); err != nil || string(got) != "package" {
		t.Fatalf("package not delivered under packages/: %v %q", err, got)
	}
	check := filepath.Join(tmp, "check")
	gitT(tmp, "clone", bare, check)
	for _, p := range plugins {
		got, err := os.ReadFile(filepath.Join(check, "plugins", p.Name))
		if err != nil || string(got) != string(p.Data) {
			t.Fatalf("%s not delivered under plugins/: %v %q", p.Name, err, got)
		}
		if sig, err := os.ReadFile(filepath.Join(check, "plugins", p.Name+".sig")); err != nil || string(sig) != string(p.Sig) {
			t.Fatalf("%s.sig not delivered: %v %q", p.Name, err, sig)
		}
	}
	if n := gitT(check, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("%s commits, want 1", n)
	}
	// the same plugins again: nothing to commit
	if err := b.deliverToPull(r, plugins, pkg); err != nil {
		t.Fatalf("deliverToPull again: %v", err)
	}
	gitT(check, "pull", "-q")
	if n := gitT(check, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("%s commits after the same plugins, want 1", n)
	}
	// a rebuilt package of the same seat (only its build time differs) is not staged again (F3)
	first, err := WritePackage("acme", "acme.202612.1", 4, "linux/amd64", time.Unix(1000, 0), plugins)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, _ := WritePackage("acme", "acme.202612.1", 4, "linux/amd64", time.Unix(2000, 0), plugins)
	if err := b.deliverToPull(r, plugins, map[string][]byte{"packages/acme.202612.1.tar.gz": first}); err != nil {
		t.Fatal(err)
	}
	if err := b.deliverToPull(r, plugins, map[string][]byte{"packages/acme.202612.1.tar.gz": rebuilt}); err != nil {
		t.Fatal(err)
	}
	gitT(check, "pull", "-q")
	if n := gitT(check, "rev-list", "--count", "HEAD"); n != "2" {
		t.Fatalf("%s commits after a package and its rebuild, want 2 (the rebuild is not a new blob)", n)
	}
	if strings.Contains(gitT(check, "log", "--format=%B"), "SECRETTOKEN") {
		t.Error("token leaked into a commit")
	}
}

// TestSamePackage: two builds of one seat differing only by their build time are the same package;
// another plugin content, expiry or an unreadable package is not.
func TestSamePackage(t *testing.T) {
	plugins := []Plugin{{Name: "plugin-licence", Data: []byte("bin"), Sig: []byte("sig")}}
	a, _ := WritePackage("acme", "acme.202612.1", 4, "linux/amd64", time.Unix(1000, 0), plugins)
	b, _ := WritePackage("acme", "acme.202612.1", 4, "linux/amd64", time.Unix(2000, 0), plugins)
	if !samePackage(a, b) {
		t.Error("a rebuild (only Built differs) should be the same package")
	}
	changed, _ := WritePackage("acme", "acme.202612.1", 4, "linux/amd64", time.Unix(1000, 0), []Plugin{{Name: "plugin-licence", Data: []byte("bin2"), Sig: []byte("sig")}})
	other, _ := WritePackage("acme", "acme.202712.1", 4, "linux/amd64", time.Unix(1000, 0), plugins)
	for name, pkg := range map[string][]byte{"plugin content": changed, "expiry": other, "unreadable": []byte("x")} {
		if samePackage(a, pkg) {
			t.Errorf("%s changed, yet the same package", name)
		}
	}
}

// check0 clones the bare repository into a fresh directory and returns it.
func check0(t *testing.T, gitT func(dir string, args ...string) string, tmp, bare string) string {
	dir, err := os.MkdirTemp(tmp, "check0-")
	if err != nil {
		t.Fatal(err)
	}
	gitT(tmp, "clone", bare, dir)
	return dir
}

// fakeGitLabAPI answers the calls of ensurePullProject like GitLab: a group lookup, a project
// creation (an existing path refused with 400 "has already been taken") and a project lookup. It
// records every call, so a test sees that no group is ever created.
type fakeGitLabAPI struct {
	groups   map[string]int
	projects map[string]map[string]interface{} // "group/path" -> creation body
	calls    []string
}

func newFakeGitLabAPI(t *testing.T) *fakeGitLabAPI {
	f := &fakeGitLabAPI{groups: map[string]int{"acme": 7}, projects: map[string]map[string]interface{}{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.EscapedPath(), "/api/v4")
		f.calls = append(f.calls, r.Method+" "+path)
		if r.Header.Get("Authorization") != "Bearer SECRETTOKEN" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/groups/"):
			if id, ok := f.groups[strings.TrimPrefix(path, "/groups/")]; ok {
				fmt.Fprintf(w, `{"id":%d}`, id)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && path == "/projects":
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)
			key := ""
			for g, id := range f.groups {
				if float64(id) == body["namespace_id"] {
					key = g + "/" + body["path"].(string)
				}
			}
			if _, ok := f.projects[key]; ok {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"message":{"path":["has already been taken"]}}`)
				return
			}
			f.projects[key] = body
			fmt.Fprint(w, `{"id":42}`)
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/projects/"):
			fmt.Fprint(w, `{"id":42}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	old := githelper.GitLabURL
	githelper.GitLabURL = srv.URL
	t.Cleanup(func() { githelper.GitLabURL = old; srv.Close() })
	return f
}

// TestEnsurePullProject: an air-gapped seat's -pull project is created private in the client's
// existing group, once (the second build finds it), and a missing group fails without creating it.
func TestEnsurePullProject(t *testing.T) {
	f := newFakeGitLabAPI(t)
	b := New(Config{GitBaseURL: githelper.GitLabURL, GitToken: "SECRETTOKEN"}, nil)
	r := Request{Company: "acme", URI: "acme.202612.1", AirGapped: true}
	if err := New(Config{GitToken: "SECRETTOKEN"}, nil).ensurePullProject(r); err == nil || !strings.Contains(err.Error(), "git-base-url") {
		t.Fatalf("no git base URL: %v", err)
	}
	if err := b.ensurePullProject(r); err != nil {
		t.Fatalf("ensurePullProject: %v", err)
	}
	body, ok := f.projects["acme/202612-1-pull"]
	if !ok || body["visibility"] != "private" || body["name"] != "202612-1-pull" {
		t.Fatalf("project not created private in the group: %v", f.projects)
	}
	if err := b.ensurePullProject(r); err != nil {
		t.Fatalf("ensurePullProject on an existing project: %v", err)
	}
	if len(f.projects) != 1 {
		t.Fatalf("%d projects, want 1", len(f.projects))
	}
	err := b.ensurePullProject(Request{Company: "other", URI: "other.202612.1", AirGapped: true})
	if err == nil || !strings.Contains(err.Error(), "no GitLab group other") {
		t.Fatalf("missing group: %v", err)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "POST /groups") || strings.HasPrefix(c, "DELETE") {
			t.Errorf("the builder must never create a group or delete anything: %s", c)
		}
		if strings.Contains(c, "SECRETTOKEN") {
			t.Errorf("token in a URL: %s", c)
		}
	}
	if err := New(Config{}, nil).ensurePullProject(r); err == nil {
		t.Error("ensurePullProject with no token should fail")
	}
}
