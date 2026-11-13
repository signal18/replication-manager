// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package etcwriter

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sample = `# the instance
[Default]
  monitoring-datadir = "/var/lib/replication-manager"
  api-credentials = "admin:old,ops:x" # the users
  mail-smtp-password = 'lit'

[cluster1]
  api-credentials = "admin:cluster"
`

func TestReplaceOnlyTheDefaultKey(t *testing.T) {
	out, changed, err := Replace([]byte(sample), "api-credentials", `admin:n"ew\x,ops:x`, "hash_x")
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	want := strings.Replace(sample, `  api-credentials = "admin:old,ops:x" # the users`, `  api-credentials = "admin:n\"ew\\x,ops:x" # the users`, 1)
	if string(out) != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
	// the same value again: nothing
	if _, changed, _ := Replace(out, "api-credentials", `admin:n"ew\x,ops:x`, "hash_x"); changed {
		t.Fatal("rewritten with the same value")
	}
	// a literal string, a key not there, another section's key
	if out, changed, _ := Replace([]byte(sample), "mail-smtp-password", "new", ""); !changed || !strings.Contains(string(out), `mail-smtp-password = "new"`) {
		t.Fatalf("literal: %s", out)
	}
	if _, changed, _ := Replace([]byte(sample), "gitlab-password", "x", ""); changed {
		t.Fatal("a key not in the file was added")
	}
}

func TestReplaceKeepsTheEncryptedForm(t *testing.T) {
	in := "[default]\napi-credentials = \"hash_abc\"\n"
	out, changed, err := Replace([]byte(in), "api-credentials", "admin:new", "hash_def")
	if err != nil || !changed || string(out) != "[default]\napi-credentials = \"hash_def\"\n" {
		t.Fatalf("%q %v %v", out, changed, err)
	}
	if _, _, err := Replace([]byte(in), "api-credentials", "admin:new", ""); err == nil {
		t.Fatal("an encrypted value written in clear")
	}
	if _, _, err := Replace([]byte("[default]\napi-credentials = \"\"\"\nx\"\"\"\n"), "api-credentials", "a", ""); err == nil {
		t.Fatal("a multi-line string silently skipped")
	}
}

func TestValidValue(t *testing.T) {
	for _, ko := range []string{"", "a\nb", "a\rb", "a\x00b"} {
		if ValidValue(ko) == nil {
			t.Fatalf("%q accepted", ko)
		}
	}
}

func TestReplaceInFileKeepsMode(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(f, []byte(sample), 0o640); err != nil {
		t.Fatal(err)
	}
	changed, err := ReplaceInFile(f, "api-credentials", "admin:new,ops:x", "")
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	fi, _ := os.Stat(f)
	if fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("a temporary file left: %v", entries)
	}
}

// The child's loop: a key that is not a server credential is refused, a valid one is
// written, the answers come in order.
func TestServe(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.toml")
	os.WriteFile(f, []byte(sample), 0o600)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go Serve(inR, outW, []string{f}, func(k string) bool { return k == "api-credentials" })
	enc := json.NewEncoder(inW)
	rd := bufio.NewReader(outR)
	ask := func(req Request) Response {
		enc.Encode(req)
		line, _ := rd.ReadBytes('\n')
		var res Response
		json.Unmarshal(line, &res)
		return res
	}
	if res := ask(Request{Key: "monitoring-datadir", Plain: "/tmp"}); res.Err == "" {
		t.Fatal("a non-credential key was written")
	}
	if res := ask(Request{Key: "api-credentials", Plain: "admin:a\nb"}); res.Err == "" {
		t.Fatal("a line break was written")
	}
	if res := ask(Request{Key: "api-credentials", Plain: "admin:new,ops:x"}); res.Err != "" || len(res.Files) != 1 {
		t.Fatalf("%+v", res)
	}
	b, _ := os.ReadFile(f)
	if !strings.Contains(string(b), `api-credentials = "admin:new,ops:x"`) || !strings.Contains(string(b), `monitoring-datadir = "/var/lib/replication-manager"`) {
		t.Fatalf("file:\n%s", b)
	}
	inW.Close()
}

func TestLocalWriterAndNoWriter(t *testing.T) {
	var w *Writer
	if _, err := w.Replace("api-credentials", "a", "", time.Second); err == nil {
		t.Fatal("a nil writer wrote")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "config.toml")
	os.WriteFile(f, []byte(sample), 0o600)
	l := Local([]string{f}, func(k string) bool { return k == "api-credentials" })
	if files, err := l.Replace("api-credentials", "admin:z,ops:x", "", time.Second); err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	if _, err := l.Replace("monitoring-datadir", "/x", "", time.Second); err == nil {
		t.Fatal("the local writer wrote a non-credential key")
	}
}
