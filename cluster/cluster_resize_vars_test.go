// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"regexp"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

// Every variable the dynamic resize sets online is in config.DynamicResizeMemoryVariables,
// the list auto-agree releases from preserved: a variable added to resizeMemorySQL or to
// the redo statement without the list would stay frozen by a preserve again.
func TestDynamicResizeMemoryVariablesCoverResizeSQL(t *testing.T) {
	inList := map[string]bool{}
	for _, v := range config.DynamicResizeMemoryVariables {
		inList[v] = true
	}
	re := regexp.MustCompile(`SET GLOBAL ([a-z_]+) =`)
	for _, v := range []*version.Version{
		{Flavor: "MariaDB", Major: 11, Minor: 8, Release: 2},
		{Flavor: "MySQL", Major: 8, Minor: 4, Release: 0},
	} {
		_, s, done := k8sResizeTestServer(t, "vars", "db1")
		s.DBVersion = v
		stmts := append(s.resizeMemorySQL(true), s.resizeMemorySQL(false)...)
		if redo, live := s.redoLogResizeSQL(); live {
			stmts = append(stmts, redo)
		}
		for _, st := range stmts {
			m := re.FindStringSubmatch(st)
			if m == nil {
				t.Errorf("%s: unparsed statement %q", v.Flavor, st)
				continue
			}
			if !inList[m[1]] {
				t.Errorf("%s: the resize sets %s, missing from config.DynamicResizeMemoryVariables", v.Flavor, m[1])
			}
		}
		done()
	}
}
