package logplugin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeTestPackage(t *testing.T, path string, entries []ManifestEntry, files map[string][]byte) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name string, data []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range files {
		add(name, data)
	}
	man := PackageManifest{Format: "cloud18-license-package/v1", Company: "company", Entries: entries}
	mj, _ := json.Marshal(man)
	add("manifest.json", mj)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExtractPackage(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "pkg.tar.gz")
	files := map[string][]byte{
		"plugins/plugin-demo":     []byte("BINARY"),
		"plugins/plugin-demo.sig": []byte("SIG"),
		"license.json":            []byte(`{"domain":"company"}`),
		"license.sig":             []byte("LICSIG"),
		"master.pub":              []byte("PUB"),
	}
	entries := []ManifestEntry{
		{Path: "plugins/plugin-demo", Dest: "plugins/plugin-demo", Kind: "plugin"},
		{Path: "plugins/plugin-demo.sig", Dest: "plugins/plugin-demo.sig", Kind: "plugin-sig"},
		{Path: "license.json", Dest: "license.json", Kind: "license"},
		{Path: "license.sig", Dest: "license.sig", Kind: "license-sig"},
		{Path: "master.pub", Dest: "master.pub", Kind: "pubkey"},
	}
	writeTestPackage(t, pkg, entries, files)

	base := filepath.Join(dir, "datadir")
	baseByKind := map[string]string{"plugin": base, "plugin-sig": base, "license": base, "license-sig": base, "pubkey": base}
	man, err := ExtractPackage(pkg, baseByKind)
	if err != nil {
		t.Fatal(err)
	}
	if man.Company != "company" || len(man.Entries) != 5 {
		t.Fatalf("manifest: %+v", man)
	}
	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(base, rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s = %q, want %q", rel, got, want)
		}
	}
	if fi, _ := os.Stat(filepath.Join(base, "plugins/plugin-demo")); fi.Mode()&0o111 == 0 {
		t.Fatal("plugin binary should be executable")
	}
}

func TestExtractPackageTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "evil.tar.gz")
	writeTestPackage(t, pkg,
		[]ManifestEntry{{Path: "evil", Dest: "../../etc/evil", Kind: "plugin"}},
		map[string][]byte{"evil": []byte("x")})
	if _, err := ExtractPackage(pkg, map[string]string{"plugin": filepath.Join(dir, "base")}); err == nil {
		t.Fatal("expected path-traversal destination to be rejected")
	}
}
