// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package etcwriter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain is also the child: started by Start (the test binary is the running binary), it
// serves as replication-manager's hidden command does.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == Command {
		if err := Serve(os.Stdin, os.NewFile(3, "answers"), os.Args[2:], func(k string) bool { return k == "api-credentials" }); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// The child process: requests on its stdin, answers on file descriptor 3, two in a row.
func TestChildProcess(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.toml")
	os.WriteFile(f, []byte(sample), 0o600)
	w, err := Start([]string{f})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Replace("monitoring-datadir", "/x", "", 5*time.Second); err == nil {
		t.Fatal("the child wrote a non-credential key")
	}
	files, err := w.Replace("api-credentials", "admin:child,ops:x", "", 5*time.Second)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	b, _ := os.ReadFile(f)
	if !strings.Contains(string(b), `api-credentials = "admin:child,ops:x"`) {
		t.Fatalf("file:\n%s", b)
	}
}
