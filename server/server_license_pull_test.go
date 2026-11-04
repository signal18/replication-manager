package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster/logplugin"
	"github.com/signal18/replication-manager/cluster/logplugin/plugins/wire"
	"github.com/signal18/replication-manager/config"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

// writeLicensePackage writes a package tar.gz: each plugin (binary + .sig) and the manifest.
func writeLicensePackage(t *testing.T, path string, plugins map[string][2][]byte, extra []logplugin.ManifestEntry) {
	t.Helper()
	writeLicensePackageFor(t, path, wire.WireVersion, runtime.GOOS+"/"+runtime.GOARCH, plugins, extra)
}

func writeLicensePackageFor(t *testing.T, path string, wireVersion int, platform string, plugins map[string][2][]byte, extra []logplugin.ManifestEntry) {
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
	man := logplugin.PackageManifest{Format: "cloud18-license-package/v1", Wire: wireVersion, Platform: platform}
	for name, p := range plugins {
		add("plugins/"+name, p[0])
		add("sigs/"+name+".sig", p[1])
		man.Entries = append(man.Entries,
			logplugin.ManifestEntry{Path: "plugins/" + name, Dest: name, Kind: "plugin"},
			logplugin.ManifestEntry{Path: "sigs/" + name + ".sig", Dest: name + ".sig", Kind: "plugin-sig"})
	}
	man.Entries = append(man.Entries, extra...)
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

// TestLicensePull verifies #2309: a package in the datadir is placed where a pull puts it and
// its signed plugins are installed; the same package again changes nothing (checksum); a
// plugin whose signature fails is refused and kept as the refusal; a package outside the
// datadir, an unsafe manifest destination and a repman that pulls its -pull repo are refused.
func TestLicensePull(t *testing.T) {
	tmp := t.TempDir()
	workingDir := filepath.Join(tmp, "data")
	shareDir := filepath.Join(tmp, "share")
	for _, dir := range []string{workingDir, filepath.Join(shareDir, "plugins")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pubPath := filepath.Join(shareDir, "plugins", "plugin-signing.pub")
	if err := os.WriteFile(pubPath, []byte(pub), 0o644); err != nil {
		t.Fatal(err)
	}
	logger, _ := logtest.NewNullLogger()
	logger.SetLevel(log.DebugLevel)
	repman := &ReplicationManager{
		Conf:   &config.Config{WorkingDir: workingDir, ShareDir: shareDir, PluginSigningPublicKey: pubPath, Daemon: true, Verbose: true},
		Logrus: logger,
	}

	good := []byte("plugin binary signed by our key")
	pkg := filepath.Join(workingDir, "package.tar.gz")
	writeLicensePackage(t, pkg, map[string][2][]byte{"plugin-good": {good, ed25519.Sign(priv, good)}}, nil)

	res, err := repman.LicensePull("package.tar.gz") // relative to the datadir
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Loaded) != 1 || res.Loaded[0] != "plugin-good" || len(res.Refused) != 0 || res.Unchanged {
		t.Fatalf("first license-pull = %+v, want plugin-good loaded", res)
	}
	if data, err := os.ReadFile(filepath.Join(workingDir, logplugin.PluginDirName, "plugin-good")); err != nil || string(data) != string(good) {
		t.Fatalf("signed plugin not installed (err=%v)", err)
	}
	if repman.licensePullRefused != "" || repman.licensePullChecksum != res.Checksum || res.Checksum == "" {
		t.Fatalf("after a loaded package: refused %q, checksum %q (result %q)", repman.licensePullRefused, repman.licensePullChecksum, res.Checksum)
	}

	// the same package again: unchanged, nothing loaded
	res, err = repman.LicensePull(pkg)
	if err != nil || !res.Unchanged {
		t.Fatalf("same package = %+v, %v, want unchanged", res, err)
	}

	// a plugin signed by another key is refused, and the refusal is kept
	_, otherPriv, _ := ed25519.GenerateKey(nil)
	forged := []byte("plugin binary signed by another key")
	writeLicensePackage(t, pkg, map[string][2][]byte{"plugin-forged": {forged, ed25519.Sign(otherPriv, forged)}}, nil)
	res, err = repman.LicensePull(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Refused) != 1 || res.Refused[0] != "plugin-forged" || len(res.Loaded) != 0 {
		t.Fatalf("forged package = %+v, want plugin-forged refused", res)
	}
	if !strings.Contains(repman.licensePullRefused, "plugin-forged") {
		t.Fatalf("refusal not kept: %q", repman.licensePullRefused)
	}
	if _, err := os.Stat(filepath.Join(workingDir, logplugin.PluginDirName, "plugin-forged")); !os.IsNotExist(err) {
		t.Fatalf("forged plugin installed (stat err=%v)", err)
	}

	// an unsafe manifest destination is refused before anything is written outside
	writeLicensePackage(t, pkg, nil, []logplugin.ManifestEntry{{Path: "manifest.json", Dest: "../../escape", Kind: "config"}})
	if _, err := repman.LicensePull(pkg); err == nil {
		t.Fatal("a destination out of the pull dir must be refused")
	}

	// a package built for another wire or platform is refused before anything is placed
	writeLicensePackageFor(t, pkg, wire.WireVersion-1, runtime.GOOS+"/"+runtime.GOARCH, map[string][2][]byte{"plugin-good": {good, ed25519.Sign(priv, good)}}, nil)
	if _, err := repman.LicensePull(pkg); err == nil || !strings.Contains(err.Error(), "wire") {
		t.Fatalf("package of another wire: err %v", err)
	}
	writeLicensePackageFor(t, pkg, wire.WireVersion, "plan9/386", map[string][2][]byte{"plugin-good": {good, ed25519.Sign(priv, good)}}, nil)
	if _, err := repman.LicensePull(pkg); err == nil || !strings.Contains(err.Error(), "platform") {
		t.Fatalf("package of another platform: err %v", err)
	}

	// a package outside the datadir is refused
	outside := filepath.Join(tmp, "outside.tar.gz")
	writeLicensePackage(t, outside, map[string][2][]byte{"plugin-good": {good, ed25519.Sign(priv, good)}}, nil)
	if _, err := repman.LicensePull(outside); err == nil || !strings.Contains(err.Error(), "not in the datadir") {
		t.Fatalf("package outside the datadir: err %v", err)
	}

	// a repman that pulls its -pull repo gets the package through it
	repman.Conf.GitUrlPull = "https://example.invalid/pull.git"
	if _, err := repman.LicensePull("package.tar.gz"); err == nil || !strings.Contains(err.Error(), "pulls") {
		t.Fatalf("repman with git-url-pull: err %v", err)
	}
	if !strings.Contains(repman.licensePullRefused, "pulls") {
		t.Fatalf("refusal not kept: %q", repman.licensePullRefused)
	}
}
