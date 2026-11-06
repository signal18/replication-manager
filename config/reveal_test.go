// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/sirupsen/logrus"
)

func encrypted(t *testing.T, key []byte, clear string) string {
	t.Helper()
	p := crypto.Password{Key: key, PlainText: clear}
	p.Encrypt()
	if p.CipherText == "" {
		t.Fatal("encryption failed")
	}
	return "hash_" + p.CipherText
}

// decrypt prints the source config file as written, every encrypted value in
// clear (alone or in a user:hash_ list), to the writer only: no file is created anywhere.
func TestRevealPrintsSourceFileOnly(t *testing.T) {
	key := []byte("0123456789abcdef")
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	content := "[default]\n# a comment\ndb-servers-credential = \"" + encrypted(t, key, "root:s3cr3t") + "\"\n" +
		"api-credentials = \"admin:" + encrypted(t, key, "adm1n") + ",viewer:" + encrypted(t, key, "v1ew") + "\"\n" +
		"monitoring-ticker = 2\n"
	if err := os.WriteFile(cfg, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	conf := &Config{SecretKey: key}
	var out bytes.Buffer
	if err := conf.Reveal(&out, []string{cfg}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"# ==== " + cfg, "# a comment", `db-servers-credential = "root:s3cr3t"`, `api-credentials = "admin:adm1n,viewer:v1ew"`, "monitoring-ticker = 2"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "hash_") {
		t.Errorf("an encrypted value left as is:\n%s", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("decrypt wrote a file: %v", entries)
	}
	if err := (&Config{}).Reveal(&out, []string{cfg}); err == nil {
		t.Fatal("without the instance key: an error, nothing decrypted")
	}
}

// The output files of older releases of the decrypt command are removed: only *.reveal files in
// that command's format, never another file, a missing directory is no error.
func TestRemoveRevealFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"c1.reveal":      "Key: DbServersCredential, Decrypted Value: root:x\n",
		"c2.reveal":      "Key: X, Decrypted Value: y\n",
		"empty.reveal":   "",
		"notes.reveal":   "my own notes\n",
		"config.toml":    "Key: x, Decrypted Value: y\n",
		"c1.reveal.keep": "Key: a, Decrypted Value: b\n",
	}
	for f, content := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	removed, errs := RemoveRevealFiles(dir)
	if len(errs) != 0 || len(removed) != 2 {
		t.Fatalf("removed %v, errors %v", removed, errs)
	}
	for _, f := range []string{"notes.reveal", "empty.reveal", "config.toml", "c1.reveal.keep"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s must stay: %v", f, err)
		}
	}
	if _, errs := RemoveRevealFiles(filepath.Join(dir, "missing")); len(errs) != 0 {
		t.Fatalf("a missing directory: %v", errs)
	}
}

// The effective values of a cluster (files, environment, flags) are printed in clear, by their
// config key name.
func TestRevealEffective(t *testing.T) {
	key := []byte("0123456789abcdef")
	conf := &Config{SecretKey: key}
	conf.User = "root:" + encrypted(t, key, "fr0m-env")
	var out bytes.Buffer
	if err := conf.RevealEffective(&out, "c1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `db-servers-credential = "root:fr0m-env"`) || strings.Contains(out.String(), "hash_") {
		t.Fatalf("effective values:\n%s", out.String())
	}
}

// The files decrypt prints as root are written by the daemon's account: a control character never
// reaches the terminal raw, and a token from another key is never shown as bytes.
func TestRevealOutputIsSafe(t *testing.T) {
	key := []byte("0123456789abcdef")
	other := []byte("fedcba9876543210")
	cfg := filepath.Join(t.TempDir(), "default.toml")
	content := "note = \"\x1b]0;owned\x07\"\nfrom-other-key = \"" + encrypted(t, other, "s3cr3t-of-another-instance") + "\"\nplanted = \"" + encrypted(t, key, "\x1b[2Jcleared") + "\"\n"
	if err := os.WriteFile(cfg, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := (&Config{SecretKey: key}).Reveal(&out, []string{cfg}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Fatalf("a control character reached the output: %q", got)
	}
	if !strings.Contains(got, `\x1b]0;owned\x07`) {
		t.Fatalf("control characters are shown escaped: %q", got)
	}
	if !strings.Contains(got, "<not decryptable with this key>") {
		t.Fatalf("a token of another key or decrypting to control bytes is marked: %q", got)
	}
}

// A tab in a password is kept; a path or cluster name with control characters is escaped too.
func TestRevealKeepsTabsEscapesHeaders(t *testing.T) {
	key := []byte("0123456789abcdef")
	dir := filepath.Join(t.TempDir(), "evil\x1b[2J")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "c.toml")
	if err := os.WriteFile(cfg, []byte("pw = \""+encrypted(t, key, "a\tb")+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := (&Config{SecretKey: key}).Reveal(&out, []string{cfg}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), "pw = \"a\tb\"") {
		t.Fatalf("output: %q", out.String())
	}
	if err := (&Config{}).RevealEffective(&out, "c1"); err == nil {
		t.Fatal("no key: an error, as Reveal")
	}
}

// The key is looked for where the daemon uses it (the configured path, then the alternative
// location, as for the embed binary), and never created by the search.
func TestFindExistingKeyNeverCreates(t *testing.T) {
	extra := t.TempDir()
	conf := &Config{MonitoringKeyPath: filepath.Join(t.TempDir(), "missing.key"), ConfDirExtra: extra}
	logger := logrus.New()
	if found, err := conf.FindExistingKey(logger); found || err != nil {
		t.Fatalf("no key anywhere: found=%v err=%v", found, err)
	}
	if entries, _ := os.ReadDir(extra); len(entries) != 0 {
		t.Fatalf("the search created something: %v", entries)
	}
	alt := filepath.Join(extra, ".replication-manager.key")
	if err := os.WriteFile(alt, []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if found, err := conf.FindExistingKey(logger); !found || err != nil || conf.MonitoringKeyPath != alt {
		t.Fatalf("the alternative key is found and used: found=%v err=%v path=%s", found, err, conf.MonitoringKeyPath)
	}
}

// A *.reveal that is a FIFO never blocks the clean-up (it runs at the start of the monitor), and a
// symlink is never followed.
func TestRemoveRevealFilesNeverBlocks(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe.reveal"), 0o600); err != nil {
		t.Skip("no FIFO here:", err)
	}
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(target, []byte("Key: a, Decrypted Value: b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link.reveal")); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		RemoveRevealFiles(dir)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the clean-up blocked on a FIFO")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal("a symlink's target was removed")
	}
}

// Bidirectional and zero-width controls are escaped: they reorder or hide what the terminal shows.
func TestRevealSafeBidi(t *testing.T) {
	in := "key = " + string(rune(0x202e)) + "x" + string(rune(0x200b)) + "y"
	got := revealSafe(in)
	if strings.ContainsRune(got, 0x202e) || strings.ContainsRune(got, 0x200b) {
		t.Fatalf("a direction or zero-width character reached the output: %q", got)
	}
	if !strings.Contains(got, "\\u202e") || !strings.Contains(got, "\\u200b") {
		t.Fatalf("not shown escaped: %q", got)
	}
}
