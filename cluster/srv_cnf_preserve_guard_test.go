// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// #2278: even if an empty-key preserved variable reaches the map (bypassing the parser
// guard), WritePreservedVariables must never write a fatal bare "loose_=" line.
func TestWritePreservedVariablesSkipsEmptyKey(t *testing.T) {
	cl, srv := newTestClusterServer(t)
	srv.Datadir = filepath.Join(cl.Conf.WorkingDir, "dd")
	if err := os.MkdirAll(srv.Datadir, 0755); err != nil {
		t.Fatal(err)
	}
	srv.VariablesMap = config.NewVariablesMap()
	srv.VariablesMap.SetPreservedValue("", "true")             // the poison
	srv.VariablesMap.SetPreservedValue("binlog_format", "ROW") // a legitimate one

	if err := srv.WritePreservedVariables(); err != nil {
		t.Fatalf("WritePreservedVariables: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(srv.Datadir, "01_preserved.cnf"))
	if err != nil {
		t.Fatalf("read preserved: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "loose_=") || strings.HasPrefix(l, "=") {
			t.Fatalf("writer emitted a fatal bare line: %q", l)
		}
	}
	if !strings.Contains(string(data), "[mysqld]") {
		t.Fatal("preserved file was not written")
	}
}
