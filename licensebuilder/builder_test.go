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
		{Company: "company", URI: "company.202612.1", AirGapped: false, Version: "v1"},
		{Company: "company", URI: "company.202001.1", AirGapped: true, Version: "v1"}, // born-expired: the guardrail must reject
	} {
		if err := r.Validate(); err == nil {
			t.Errorf("Validate(%+v) accepted", r)
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
