// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package etcwriter

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMain is also the child: started by Start (the test binary is the running binary), it
// serves as replication-manager's hidden command does.
func TestMain(m *testing.M) {
	if len(os.Args) > 5 && os.Args[1] == Command {
		// Command --uid N --gid N files...
		uid, _ := strconv.Atoi(os.Args[3])
		gid, _ := strconv.Atoi(os.Args[5])
		if err := Serve(os.Stdin, os.NewFile(3, "answers"), os.Args[6:], uid, gid, func(k string) bool { return k == "api-credentials" }); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// otherUID and otherGID stand for the user replication-manager drops to, when the test's
// files must be safe from it: no file of the test belongs to them.
const otherUID, otherGID = 64999, 64999

// The child process: requests on its stdin, answers on file descriptor 3, two in a row.
func TestChildProcess(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.toml")
	os.WriteFile(f, []byte(sample), 0o600)
	w, err := Start([]string{f}, otherUID, otherGID)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Replace("monitoring-datadir", "/x", "", 5*time.Second); err == nil {
		t.Fatal("the child wrote a non-credential key")
	}
	res, err := w.Replace("api-credentials", "admin:child,ops:x", "", 5*time.Second)
	if err != nil || len(res.Written) != 1 || len(res.Set) != 1 {
		t.Fatal(res, err)
	}
	b, _ := os.ReadFile(f)
	if !strings.Contains(string(b), `api-credentials = "admin:child,ops:x"`) {
		t.Fatalf("file:\n%s", b)
	}
}
