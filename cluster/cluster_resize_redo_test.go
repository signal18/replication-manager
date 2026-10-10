package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/utils/version"
)

// The redo statement exists only on releases that resize it online; other known
// releases are restart-only, an unknown release is neither. resizeMemorySQL never
// carries it: applyRedoLogResize runs it on its own, after the memory statements.
func TestRedoLogResizeSQL_ByRelease(t *testing.T) {
	cases := []struct {
		name        string
		v           *version.Version
		wantPrefix  string
		restartOnly bool
	}{
		{"mariadb 10.11 live", &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11}, "SET GLOBAL innodb_log_file_size = ", false},
		{"mariadb 10.9 live", &version.Version{Flavor: "MariaDB", Major: 10, Minor: 9}, "SET GLOBAL innodb_log_file_size = ", false},
		{"mariadb 10.6 restart", &version.Version{Flavor: "MariaDB", Major: 10, Minor: 6, Release: 20}, "", true},
		{"mysql 8.0.30 live", &version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 30}, "SET GLOBAL innodb_redo_log_capacity = ", false},
		{"mysql 8.0.29 restart", &version.Version{Flavor: "MySQL", Major: 8, Minor: 0, Release: 29}, "", true},
		{"percona 8.4 live", &version.Version{Flavor: "Percona", Major: 8, Minor: 4, Release: 0}, "SET GLOBAL innodb_redo_log_capacity = ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, s, done := k8sResizeTestServer(t, "redo", "db1")
			defer done()
			s.DBVersion = c.v
			stmt, live := s.redoLogResizeSQL()
			if c.wantPrefix == "" {
				if live || stmt != "" {
					t.Fatalf("no live redo statement expected on %s, got %q", c.name, stmt)
				}
			} else if !live || !strings.HasPrefix(stmt, c.wantPrefix) || !strings.HasSuffix(stmt, "*1024*1024") {
				t.Fatalf("want live %q..., got live=%v %q", c.wantPrefix, live, stmt)
			}
			if got := s.redoLogRestartOnly(); got != c.restartOnly {
				t.Fatalf("redoLogRestartOnly = %v, want %v", got, c.restartOnly)
			}
			for _, grow := range []bool{true, false} {
				for _, st := range s.resizeMemorySQL(grow) {
					if strings.Contains(st, "innodb_log_file_size") || strings.Contains(st, "innodb_redo_log_capacity") {
						t.Fatalf("resizeMemorySQL must not carry the redo statement, got %q", st)
					}
				}
			}
		})
	}
	_, s, done := k8sResizeTestServer(t, "redo", "db2")
	defer done()
	s.DBVersion = nil
	if _, live := s.redoLogResizeSQL(); live || s.redoLogRestartOnly() {
		t.Fatalf("unknown release must neither resize live nor demand a restart")
	}
}

// A live redo statement that fails (here: the sqlmock connection rejects every
// Exec) must fall back to the restart cookie, never leave the redo silently unsized.
// A restart-only release gets the cookie straight away; an unknown release nothing.
func TestApplyRedoLogResize_FallsBackToRestart(t *testing.T) {
	_, s, done := k8sResizeTestServer(t, "redo", "db1")
	defer done()
	s.DBVersion = &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 2}
	if s.HasRestartCookie() {
		t.Fatalf("no cookie expected before the resize")
	}
	if got := s.applyRedoLogResize(); len(got) != 1 || !strings.HasPrefix(got[0], "SET GLOBAL innodb_log_file_size = ") {
		t.Fatalf("the issued statement must be returned for the resize log, got %v", got)
	}
	if !s.HasRestartCookie() {
		t.Fatalf("a failed live redo resize must schedule a restart")
	}
	s.DelRestartCookie()

	s.DBVersion = &version.Version{Flavor: "MariaDB", Major: 10, Minor: 6, Release: 20}
	if got := s.applyRedoLogResize(); got != nil || !s.HasRestartCookie() {
		t.Fatalf("a restart-only release must get the cookie and no statement, got %v", got)
	}
	s.DelRestartCookie()

	s.DBVersion = nil
	if got := s.applyRedoLogResize(); got != nil || s.HasRestartCookie() {
		t.Fatalf("an unknown release must get neither, got %v cookie=%v", got, s.HasRestartCookie())
	}
}
