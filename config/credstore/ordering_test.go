// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package credstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// A failover to a monitor whose clock is behind: the rotation it writes sorts after the version
// it replaces, so a merge with the old active's file never makes the banned value current again.
func TestNewVersionSortsAfterTheLastWhateverTheClock(t *testing.T) {
	conf := testConf()
	key := "replication-credential"
	ahead := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	oldActive := map[string][]FileVersion{key: {{Version: 1, HashValue: conf.EncryptSecretValue("repl:compromised"), RotatedAt: ahead, WrittenBy: 1}}}
	vs := NewVersionStore(func() *config.Config { return conf }, func(k string) bool { return k == key }, nil, nil)
	vs.Load(oldActive)
	if err := vs.Set(key, "repl:banned"); err != nil {
		t.Fatal(err)
	}
	vs.Merge(oldActive)
	if v, _ := vs.Latest(key); v.Value != "repl:banned" {
		t.Fatalf("after the merge the current value is %q", v.Value)
	}
	vs.TakeConfigFileValue(key, "repl:file") // first seen: kept as before the versions
	if !vs.TakeConfigFileValue(key, "repl:edited") {
		t.Fatal("an edit of the config file is the current version")
	}
	vs.Merge(oldActive)
	if v, _ := vs.Latest(key); v.Value != "repl:edited" {
		t.Fatalf("after the merge the edit is not the current value: %q", v.Value)
	}
}

// A file that exists but cannot be read is never overwritten: the versions it holds would be
// lost. A later save reads it again, merges it and writes it.
func TestUnreadFileIsNotOverwritten(t *testing.T) {
	conf := testConf()
	key := "replication-credential"
	dir := t.TempDir()
	b := &FileBinding{DataDir: func() string { return dir }, Scope: ClusterScope("c1")}
	if err := os.MkdirAll(filepath.Dir(b.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b.Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	vs := NewVersionStore(func() *config.Config { return conf }, func(k string) bool { return k == key }, nil, nil)
	if err := b.LoadInto(vs); err == nil {
		t.Fatal("a corrupt file loads")
	}
	if err := vs.Set(key, "repl:new"); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(vs, LooksEncrypted, nil); err == nil {
		t.Fatal("an unread file was overwritten")
	}
	if raw, _ := os.ReadFile(b.Path()); string(raw) != "{not json" {
		t.Fatalf("the file changed: %q", raw)
	}
	kept := map[string][]FileVersion{key: {{Version: 1, HashValue: conf.EncryptSecretValue("repl:kept"), RotatedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)}}}
	if err := (LocalFile{DataDir: dir}).Write(ClusterScope("c1"), kept); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(vs, LooksEncrypted, nil); err != nil {
		t.Fatal(err)
	}
	file, err := (LocalFile{DataDir: dir}).Load(ClusterScope("c1"))
	if err != nil || len(file[key]) != 2 {
		t.Fatalf("the repaired file is merged, not replaced: %v, %d versions", err, len(file[key]))
	}
}

// A list is encrypted when every password in it is.
func TestLooksEncryptedEveryEntry(t *testing.T) {
	for value, want := range map[string]bool{
		"hash_abc":                    true,
		"admin:hash_a,ops:hash_b":     true,
		"admin:hash_a,ops:clear":      false,
		"admin:clear,ops:hash_b":      false,
		"hash_user:clear":             false,
		"clear":                       false,
		"":                            false,
		"admin:hash_a, ops:hash_b , ": true,
	} {
		if got := LooksEncrypted(value); got != want {
			t.Errorf("%q: %v, want %v", value, got, want)
		}
	}
}
