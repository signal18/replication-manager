package cluster

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The lock of the replication link interface (doc/implementation/cluster/REPLICATION_INTERFACE.md):
// dbhelper's replication link helpers (the status read, start, stop, reset, CHANGE MASTER, skip)
// are called from the native backend only, cluster/replication_native.go. Every other caller goes
// through a ReplicationLink (server.ReplicationLink(), ReplicationChannelLink, ReplicationLinks),
// so a link change is recorded as a state (T27) and a CDC feed is a link like any other.
//
// replicationLinkSQL are the helpers the interface covers. replicationLinkSQLLater are the link
// helpers not behind it yet (the replication credentials, the filters, the exec and GTID modes,
// the heartbeat): the files that may still call them and how many lines do, a list that only
// shrinks, as the orchestrator's lock (orchestrator_dispatch_test.go).
var replicationLinkSQL = map[string]bool{
	"GetAllSlavesStatus":    true,
	"GetChannelSlaveStatus": true,
	"GetSlaveStatus":        true,
	"StartSlave":            true,
	"StopSlave":             true,
	"StopSlaveIOThread":     true,
	"StopSlaveSQLThread":    true,
	"StopAllSlaves":         true,
	"ResetSlave":            true,
	"ResetAllSlaves":        true,
	"ChangeMaster":          true,
	"SkipBinlogEvent":       true,
}

var replicationLinkBackendFiles = map[string]bool{
	"cluster/replication_native.go": true,
}

var replicationLinkSQLLaterNames = map[string]bool{
	"ChangeReplicationPassword":      true,
	"SetSlaveExecMode":               true,
	"SetSlaveGTIDMode":               true,
	"SetSlaveGTIDModeStrict":         true,
	"SetSlaveHeartbeat":              true,
	"SetChannelReplicateIgnoreTable": true,
	"SetMultiSourceRepl":             true,
	"SetDefaultMasterConn":           true,
}

// measured at the step that introduced the interface (#2453); lower it when a call moves
var replicationLinkSQLLater = map[string]int{
	"cluster/cluster_analytic_child.go":   2, // the analytic child's replicate_ignore_table
	"cluster/cluster_columnstore_child.go": 1, // the ColumnStore child's replicate_ignore_table
	"cluster/prov.go":                     1, // the bootstrap's default_master_connection
	"cluster/spider.go":                   1, // the Spider shard's multi-source channel
	"cluster/srv_chk.go":                  4, // the exec and GTID modes the monitor enforces
	"cluster/srv_heartbeat.go":            1, // the managed channel's heartbeat period
	"cluster/srv_rejoin.go":               1, // the replication password after a rotation
	"cluster/srv_set.go":                  1, // default_master_connection
}

// replicationLinkSQLLines returns, per helper set, the lines of one file that name a helper of
// dbhelper (a call or a function value).
func replicationLinkSQLLines(t *testing.T, path string) (covered, later map[int]string) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	covered, later = map[int]string{}, map[int]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "dbhelper" {
			return true
		}
		line := fset.Position(sel.Pos()).Line
		switch {
		case replicationLinkSQL[sel.Sel.Name]:
			covered[line] = sel.Sel.Name
		case replicationLinkSQLLaterNames[sel.Sel.Name]:
			later[line] = sel.Sel.Name
		}
		return true
	})
	return covered, later
}

func replicationLinkFiles(t *testing.T) map[string]string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, dir := range []string{"cluster", "cluster/configurator", "server", "mcp", "regtest"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			files[dir+"/"+name] = filepath.Join(root, dir, name)
		}
	}
	return files
}

func TestNoReplicationLinkSQLOutsideBackend(t *testing.T) {
	var problems []string
	laterCounts := map[string]int{}
	for rel, path := range replicationLinkFiles(t) {
		covered, later := replicationLinkSQLLines(t, path)
		if !replicationLinkBackendFiles[rel] {
			for line, name := range covered {
				problems = append(problems, fmt.Sprintf("%s:%d: dbhelper.%s outside the native backend: go through server.ReplicationLink() (cluster/replication.go)", rel, line, name))
			}
		}
		if len(later) > 0 && !replicationLinkBackendFiles[rel] {
			laterCounts[rel] = len(later)
		}
	}
	for rel, got := range laterCounts {
		want, ok := replicationLinkSQLLater[rel]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s: %d lines call a replication link helper not behind the interface yet, not in the allowed list: add the operation to ReplicationLink instead", rel, got))
		case got > want:
			problems = append(problems, fmt.Sprintf("%s: %d lines call a replication link helper not behind the interface yet, %d allowed", rel, got, want))
		case got < want:
			problems = append(problems, fmt.Sprintf("%s: %d lines, %d allowed: lower the allowed count so the list keeps shrinking", rel, got, want))
		}
	}
	for rel, want := range replicationLinkSQLLater {
		if _, ok := laterCounts[rel]; !ok {
			problems = append(problems, fmt.Sprintf("%s: in the allowed list (%d) but has no such line left: remove it", rel, want))
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

// The native link calls nothing but the dbhelper helper of its operation, on its own channel,
// and records the change it makes (T27); a successful start drops it.
func TestNativeLinkRecordsChanges(t *testing.T) {
	c, sv := reseedLinkFixture()
	link := sv.ReplicationChannelLink("belair")
	if link.Kind() != ReplicationKindNative || link.Name() != "belair" {
		t.Fatalf("kind %s name %q", link.Kind(), link.Name())
	}
	// no connection: Stop refuses before any SQL, and records nothing
	if _, err := link.Stop(LinkBySwitchover); err == nil {
		t.Fatal("a stop without a connection must fail")
	}
	if len(sv.linkChangeList()) != 0 {
		t.Fatal("nothing recorded for a stop that never ran")
	}
	sv.recordLinkChange("belair", linkOpStop, LinkBySwitchover)
	sv.recordLinkChange("crm", linkOpChangeSource, LinkByRejoin)
	sv.recordLinkChange("belair", linkOpResetPositions, LinkByReseed) // replaces belair's
	got := sv.linkChangeList()
	if len(got) != 2 || got[1].channel != "belair" || got[1].op != linkOpResetPositions {
		t.Fatalf("one record per channel, the last change: %+v", got)
	}
	c.Conf.MonitoringTicker = 0
	sv.checkLinkChanges()
	if !stateOpen(c, "WARN0352") {
		t.Fatal("WARN0352 while the changed links do not run")
	}
	sv.clearLinkChange("belair")
	sv.clearLinkChange("crm")
	if len(sv.linkChangeList()) != 0 {
		t.Fatal("a start drops the record")
	}
}

func stateOpen(c *Cluster, key string) bool {
	for _, st := range c.StateMachine.GetOpenStates() {
		if st.ErrKey == key {
			return true
		}
	}
	return false
}
