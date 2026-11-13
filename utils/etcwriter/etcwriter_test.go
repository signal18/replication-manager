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
	out, found, changed, err := Replace([]byte(sample), "api-credentials", `admin:n"ew\x,ops:x`, "hash_x")
	if err != nil || !found || !changed {
		t.Fatal(found, changed, err)
	}
	want := strings.Replace(sample, `  api-credentials = "admin:old,ops:x" # the users`, `  api-credentials = "admin:n\"ew\\x,ops:x" # the users`, 1)
	if string(out) != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
	if _, found, changed, _ := Replace(out, "api-credentials", `admin:n"ew\x,ops:x`, "hash_x"); !found || changed {
		t.Fatal("rewritten with the same value, or not found")
	}
	if out, _, changed, _ := Replace([]byte(sample), "mail-smtp-password", "new", ""); !changed || !strings.Contains(string(out), `mail-smtp-password = "new"`) {
		t.Fatalf("literal: %s", out)
	}
	if _, found, changed, _ := Replace([]byte(sample), "gitlab-password", "x", ""); found || changed {
		t.Fatal("a key not in the file was added")
	}
}

// A line inside a value spread over several lines is never a header nor a key: a nested array
// does not end [default], a multi-line string holding "[default]" or the key does not open it.
func TestReplaceSkipsMultiLineValues(t *testing.T) {
	in := `[default]
  some-list = [
    ["b"],
    "c",
  ]
  api-credentials = "admin:old"
[cluster1]
  notes = """
[default]
api-credentials = "admin:fake"
"""
`
	out, found, changed, err := Replace([]byte(in), "api-credentials", "admin:new", "")
	if err != nil || !found || !changed {
		t.Fatal(found, changed, err)
	}
	if !strings.Contains(string(out), `  api-credentials = "admin:new"`) || !strings.Contains(string(out), `api-credentials = "admin:fake"`) {
		t.Fatalf("got\n%s", out)
	}
}

func TestReplaceKeepsTheEncryptedForm(t *testing.T) {
	in := "[default]\napi-credentials = \"hash_abc\"\n"
	out, _, changed, err := Replace([]byte(in), "api-credentials", "admin:new", "hash_def")
	if err != nil || !changed || string(out) != "[default]\napi-credentials = \"hash_def\"\n" {
		t.Fatalf("%q %v %v", out, changed, err)
	}
	if _, _, _, err := Replace([]byte(in), "api-credentials", "admin:new", ""); err == nil {
		t.Fatal("an encrypted value written in clear")
	}
	if _, _, _, err := Replace([]byte("[default]\napi-credentials = \"\"\"\nx\"\"\"\n"), "api-credentials", "a", ""); err == nil {
		t.Fatal("a multi-line string silently skipped")
	}
}

func TestValidValue(t *testing.T) {
	for _, ko := range []string{"", "a\nb", "a\rb", "a\x00b"} {
		if ValidValue(ko) == nil {
			t.Fatalf("%q accepted", ko)
		}
	}
	if checkValue(strings.Repeat("a", MaxValue+1)) == nil {
		t.Fatal("an oversized value accepted")
	}
}

func TestReplaceInFileKeepsModeAndRefusesALink(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.toml")
	os.WriteFile(f, []byte(sample), 0o640)
	if _, changed, err := ReplaceInFile(f, "api-credentials", "admin:new,ops:x", ""); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if fi, _ := os.Stat(f); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("a temporary file left: %v", entries)
	}
	link := filepath.Join(dir, "link.toml")
	os.Symlink(f, link)
	if _, _, err := ReplaceInFile(link, "api-credentials", "admin:z,ops:x", ""); err == nil {
		t.Fatal("a symbolic link was followed")
	}
	// a write killed half-way leaves its temporary file: removed at the child's start
	os.WriteFile(filepath.Join(dir, tempPattern(f)+"123"), []byte("x"), 0o600)
	RemoveStaleTemps(f)
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatalf("stale temp kept: %v", entries)
	}
}

// A file the dropped user owns, or in a folder it owns, could be swapped between the root
// writer's read and write: refused.
func TestUnsafe(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.toml")
	os.WriteFile(f, []byte(sample), 0o600)
	if why := Unsafe(f, os.Getuid(), os.Getgid()); !strings.Contains(why, "belongs to the user") {
		t.Fatalf("own file: %q", why)
	}
	if Unsafe("config.toml", otherUID, otherGID) == "" {
		t.Fatal("a relative path accepted")
	}
	safe, refused := Split([]string{f}, os.Getuid(), os.Getgid())
	if len(safe) != 0 || refused[f] == "" {
		t.Fatal(safe, refused)
	}
	if real, why := Resolve(f, otherUID, otherGID); why != "" || real == "" {
		t.Fatalf("a file of another user, links resolved: %q %q", real, why)
	}
}

// The child's loop: a key that is not a server credential is refused, a valid one is written,
// an unsafe file is reported, an oversized request stops it.
func TestServe(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.toml")
	os.WriteFile(f, []byte(sample), 0o600)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- Serve(inR, outW, []string{f, "relative.toml"}, otherUID, otherGID, func(k string) bool { return k == "api-credentials" })
	}()
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
	res := ask(Request{Key: "api-credentials", Plain: "admin:new,ops:x"})
	if res.Err != "" || len(res.Written) != 1 || len(res.Set) != 1 || res.Refused["relative.toml"] == "" || len(res.Refused) != 1 {
		t.Fatalf("%+v", res)
	}
	b, _ := os.ReadFile(f)
	if !strings.Contains(string(b), `api-credentials = "admin:new,ops:x"`) || !strings.Contains(string(b), `monitoring-datadir = "/var/lib/replication-manager"`) {
		t.Fatalf("file:\n%s", b)
	}
	go inW.Write([]byte(strings.Repeat("x", MaxRequest+10) + "\n"))
	if err := <-done; err == nil || !strings.Contains(err.Error(), "longer than") {
		t.Fatalf("an oversized request: %v", err)
	}
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
	if res, err := l.Replace("api-credentials", "admin:z,ops:x", "", time.Second); err != nil || len(res.Written) != 1 {
		t.Fatal(res, err)
	}
	if _, err := l.Replace("monitoring-datadir", "/x", "", time.Second); err == nil {
		t.Fatal("the local writer wrote a non-credential key")
	}
}
