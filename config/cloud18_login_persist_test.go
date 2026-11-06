// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #2341: the live Cloud18 logins are kept across a restart, encrypted, keyed by the hash of their
// first refresh token (never the token), and read back at start; ended and refused logins are not
// kept; another key reads nothing back; cloud18-login-resume off removes the file.
func TestCloud18LoginsKept(t *testing.T) {
	defer ForgetCloud18Logins()
	defer StopKeepingCloud18Logins()
	ForgetCloud18Logins()
	conf := &Config{WorkingDir: t.TempDir(), SecretKey: []byte("0123456789abcdef"), Cloud18LoginResume: true}
	if n, err := conf.LoadCloud18Logins(); err != nil || n != 0 {
		t.Fatalf("no file yet: %d %v", n, err)
	}
	now := time.Now()
	live := cloud18LoginEntryFor("rt-first@acme.example", now)
	live.refresh = "rt-rotated@acme.example" // a realm rotating refresh tokens
	ended := cloud18LoginEntryFor("rt-ended@acme.example", now)
	ended.refreshEnd = now.Add(-time.Minute)
	refused := cloud18LoginEntryFor("rt-refused@acme.example", now)
	refused.refused, refused.refusedErr = now.Add(time.Minute), ErrCloud18Refused
	FlushCloud18Logins()

	data, err := os.ReadFile(filepath.Join(conf.WorkingDir, cloud18LoginsFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, clear := range []string{"rt-first", "rt-rotated", "rt-ended", "rt-refused"} {
		if strings.Contains(string(data), clear) {
			t.Fatalf("a refresh token in clear on disk: %s", clear)
		}
	}
	if fi, _ := os.Stat(filepath.Join(conf.WorkingDir, cloud18LoginsFileName)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("the file is 0600: %v", fi.Mode().Perm())
	}

	// the restart: the cache is empty, then read back
	ForgetCloud18Logins()
	if n, err := conf.LoadCloud18Logins(); err != nil || n != 1 {
		t.Fatalf("one live login comes back (ended and refused ones were not kept): %d %v", n, err)
	}
	e := cloud18LoginEntryFor("rt-first@acme.example", time.Now())
	if e.refresh != "rt-rotated@acme.example" {
		t.Fatalf("the login found by its first refresh token gives the newest one: %q", e.refresh)
	}

	// another instance's key: nothing comes back
	ForgetCloud18Logins()
	other := &Config{WorkingDir: conf.WorkingDir, SecretKey: []byte("fedcba9876543210"), Cloud18LoginResume: true}
	if n, _ := other.LoadCloud18Logins(); n != 0 {
		t.Fatalf("another key decrypts nothing: %d", n)
	}

	// off: the file is removed, nothing is read back or written
	conf.Cloud18LoginResume = false
	if _, err := conf.LoadCloud18Logins(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(conf.WorkingDir, cloud18LoginsFileName)); !os.IsNotExist(err) {
		t.Fatal("cloud18-login-resume off removes the kept logins")
	}
	cloud18LoginEntryFor("rt-after@acme.example", time.Now())
	FlushCloud18Logins()
	if _, err := os.Stat(filepath.Join(conf.WorkingDir, cloud18LoginsFileName)); !os.IsNotExist(err) {
		t.Fatal("off: nothing is written")
	}
}

// #2341: the writes of the cache are batched: many changes, one write after the delay.
func TestCloud18LoginsBatched(t *testing.T) {
	defer ForgetCloud18Logins()
	defer StopKeepingCloud18Logins()
	ForgetCloud18Logins()
	old := cloud18LoginsSaveDelay
	cloud18LoginsSaveDelay = 50 * time.Millisecond
	defer func() { cloud18LoginsSaveDelay = old }()
	conf := &Config{WorkingDir: t.TempDir(), SecretKey: []byte("0123456789abcdef"), Cloud18LoginResume: true}
	if _, err := conf.LoadCloud18Logins(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		cloud18LoginEntryFor("rt-"+string(rune('a'+i))+"@acme.example", time.Now())
	}
	path := filepath.Join(conf.WorkingDir, cloud18LoginsFileName)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("not written before the delay")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			ForgetCloud18Logins()
			if n, _ := conf.LoadCloud18Logins(); n != 20 {
				t.Fatalf("the 20 logins in one write: %d", n)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("never written")
}
