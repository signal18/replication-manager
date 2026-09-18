package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/utils/version"
)

// The redo log follows a memory step live only on releases that resize it online,
// as the LAST statement (after the buffer pool) on both grow and shrink; other
// releases get no statement and a restart-only signal.
func TestResizeMemorySQL_RedoLogByRelease(t *testing.T) {
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
			cl, s, done := k8sResizeTestServer(t, "redo", "db1")
			defer done()
			_ = cl
			s.DBVersion = c.v
			for _, grow := range []bool{true, false} {
				sql := s.resizeMemorySQL(grow)
				last := sql[len(sql)-1]
				if c.wantPrefix == "" {
					for _, st := range sql {
						if strings.Contains(st, "innodb_log_file_size") || strings.Contains(st, "innodb_redo_log_capacity") {
							t.Fatalf("grow=%v: no redo statement expected on %s, got %q", grow, c.name, st)
						}
					}
				} else if !strings.HasPrefix(last, c.wantPrefix) || !strings.HasSuffix(last, "*1024*1024") {
					t.Fatalf("grow=%v: redo must be the last statement %q..., got %q", grow, c.wantPrefix, last)
				}
			}
			if got := s.redoLogRestartOnly(); got != c.restartOnly {
				t.Fatalf("redoLogRestartOnly = %v, want %v", got, c.restartOnly)
			}
		})
	}
	// Unknown release: no statement and no restart signal either.
	_, s, done := k8sResizeTestServer(t, "redo", "db2")
	defer done()
	s.DBVersion = nil
	if _, live := s.redoLogResizeSQL(); live || s.redoLogRestartOnly() {
		t.Fatalf("unknown release must neither resize live nor demand a restart")
	}
}
