package licensebuilder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/cluster/logplugin"
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
		OutDir: filepath.Join(tmp, "out"), DataDir: filepath.Join(tmp, "data")}
	fakeTree(t, cfg.SourceDir, "4")
	release := filepath.Join(cfg.WorkDir, "replication-manager-v3.1.48")
	fakeTree(t, release, "4")
	leftover := filepath.Join(cfg.DataDir, "plugins", "plugin-old")
	os.MkdirAll(filepath.Dir(leftover), 0o755)
	os.WriteFile(leftover, []byte("old"), 0o755)

	b := New(cfg, nil)
	var licenceURI string
	b.run = func(dir string, env []string, name string, args ...string) error {
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
		{Company: "company", URI: "company.202001.1", AirGapped: true, Version: "v1"},                               // born-expired: the guardrail must reject
		{Company: "acme", URI: "acme.east.z1", AirGapped: false, Version: "v1"},                   // online, no expiry
		{Company: "acme", URI: "acme.east.z1", AirGapped: false, Version: "v1", Expiry: "202001"}, // online, born-expired
		{Company: "acme", URI: "acme.UP.fr1", AirGapped: false, Version: "v1", Expiry: "2099-12-31"},        // online, not lower-case dotted
		{Company: "acme", URI: "company.202612.1", AirGapped: false, Version: "v1", Expiry: "2099-12-31"},       // online uri not of company
	} {
		if err := r.Validate(); err == nil {
			t.Errorf("Validate(%+v) accepted", r)
		}
	}
	for _, r := range []Request{
		{Company: "company", URI: "company.202612.1", AirGapped: true, Version: "v1"},                                        // air-gapped seat
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
	if err := b.deliverToPull(Request{URI: "acme.east.z1"}, lic); err != nil {
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
	if err := b.deliverToPull(Request{URI: "acme.east.z1"}, nil); err == nil {
		t.Error("deliverToPull with no git config should fail")
	}
}
