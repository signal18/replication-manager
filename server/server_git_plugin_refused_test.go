package server

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

// TestSyncPluginsFromPullLogsRefusalOnce verifies #2233: a pulled plugin whose
// signature does not match the embedded .sig is refused and logged once; the
// same binary at the next pulls is skipped silently, a new binary is verified
// again, and a correctly signed one is installed and clears the refusal.
func TestSyncPluginsFromPullLogsRefusalOnce(t *testing.T) {
	tmp := t.TempDir()
	workingDir := filepath.Join(tmp, "data")
	shareDir := filepath.Join(tmp, "share")
	pullDir := filepath.Join(workingDir, ".pull")
	for _, dir := range []string{filepath.Join(shareDir, "plugins"), filepath.Join(pullDir, "plugins")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pubPath := filepath.Join(shareDir, "plugins", "plugin-signing.pub")
	if err := os.WriteFile(pubPath, []byte(pub), 0644); err != nil {
		t.Fatal(err)
	}
	// The image signs the build it shipped with.
	imageBuild := []byte("plugin binary of the image build")
	if err := os.WriteFile(filepath.Join(shareDir, "plugins", "plugin-test.sig"), ed25519.Sign(priv, imageBuild), 0644); err != nil {
		t.Fatal(err)
	}

	logger, hook := logtest.NewNullLogger()
	logger.SetLevel(log.DebugLevel)
	repman := &ReplicationManager{
		Conf:   &config.Config{WorkingDir: workingDir, ShareDir: shareDir, PluginSigningPublicKey: pubPath, Daemon: true, Verbose: true},
		Logrus: logger,
	}
	pulled := filepath.Join(pullDir, "plugins", "plugin-test")
	installed := filepath.Join(workingDir, "plugins", "plugin-test")
	refusals := func() int {
		n := 0
		for _, entry := range hook.AllEntries() {
			if strings.Contains(entry.Message, "skipping pull plugin plugin-test") {
				n++
			}
		}
		return n
	}
	pull := func(binary []byte) {
		t.Helper()
		if err := os.WriteFile(pulled, binary, 0755); err != nil {
			t.Fatal(err)
		}
		repman.syncPluginsFromPull(pullDir)
	}

	// A later build, not the one the image signs: refused and logged once.
	pull([]byte("plugin binary of a later build"))
	pull([]byte("plugin binary of a later build"))
	pull([]byte("plugin binary of a later build"))
	if got := refusals(); got != 1 {
		t.Fatalf("same refused binary over 3 pulls logged %d times, want 1", got)
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Fatalf("refused binary must not be installed (stat err=%v)", err)
	}

	// Another build is a new binary: verified and logged again.
	pull([]byte("plugin binary of yet another build"))
	if got := refusals(); got != 2 {
		t.Fatalf("a new refused binary logged %d refusals in total, want 2", got)
	}

	// The embedded .sig changes in place (package upgraded without a restart):
	// the same binary is checked again, and now installed.
	laterBuild := []byte("plugin binary of yet another build")
	if err := os.WriteFile(filepath.Join(shareDir, "plugins", "plugin-test.sig"), ed25519.Sign(priv, laterBuild), 0644); err != nil {
		t.Fatal(err)
	}
	pull(laterBuild)
	if data, err := os.ReadFile(installed); err != nil || string(data) != string(laterBuild) {
		t.Fatalf("binary matching the new .sig not installed (err=%v)", err)
	}
	if err := os.WriteFile(filepath.Join(shareDir, "plugins", "plugin-test.sig"), ed25519.Sign(priv, imageBuild), 0644); err != nil {
		t.Fatal(err)
	}

	// The image's own build: installed, and the refusal is forgotten.
	pull(imageBuild)
	if data, err := os.ReadFile(installed); err != nil || string(data) != string(imageBuild) {
		t.Fatalf("signed binary not installed (err=%v)", err)
	}
	if _, ok := repman.pullPluginRefused["plugin-test"]; ok {
		t.Error("installing the signed binary must clear the refusal")
	}
	// Installed and unchanged: skipped before the signature check, no new log.
	pull(imageBuild)
	if got := refusals(); got != 2 {
		t.Fatalf("an installed binary produced a refusal (total %d, want 2)", got)
	}
}
