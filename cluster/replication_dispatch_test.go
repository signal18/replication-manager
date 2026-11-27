package cluster

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// The lock of the replication link interface (doc/implementation/cluster/REPLICATION_INTERFACE.md):
// dbhelper's replication link helpers (the status read, start, stop, reset, CHANGE MASTER, skip)
// are called from the native backend only, cluster/replication_native.go. Every other caller goes
// through a Link of a ReplicationPlugin (server.ReplicationLink(), ReplicationChannelLink, ReplicationLinks),
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
	"StartSlaveIOThread":    true,
	"StartSlaveSQLThread":   true,
	"StopSlaveIOThread":     true,
	"StopSlaveSQLThread":    true,
	"StopAllSlaves":         true,
	"ResetSlave":            true,
	"ResetAllSlaves":        true,
	"ChangeMaster":          true,
	"SkipBinlogEvent":       true,
	// step 2b: Promote, SetPosition, WaitCaughtUp, Configure, RotateCredential
	"PostgresPromote":           true,
	"SetGTIDSlavePos":           true,
	"SetGtidPurged":             true,
	"MasterWaitGTID":            true,
	"MasterWaitGTIDReached":     true,
	"MasterPosWait":             true,
	"MasterPosWaitReached":      true,
	"SetSlaveExecMode":          true,
	"SetSlaveGTIDMode":          true,
	"SetSlaveGTIDModeStrict":    true,
	"SetSlaveHeartbeat":         true,
	"SetSlaveParallelMode":      true,
	"ChangeReplicationPassword": true,
	// step 3: the engine branches of the binlog plugin, and the group membership
	"ReplicaWaitReached":           true,
	"SetReplicaPosition":           true,
	"SetGroupReplicationPrimary":   true,
	"StartGroupReplication":        true,
	"BootstrapGroupReplication":    true,
	"SetGroupReplicationAllowlist": true,
}

var replicationLinkBackendFiles = map[string]bool{
	"cluster/replication_native.go":   true,
	"cluster/replication_binlog.go":   true,
	"cluster/replication_postgres.go": true,
	"cluster/replication_group.go":    true,
}

var replicationLinkSQLLaterNames = map[string]bool{
	"SetChannelReplicateIgnoreTable": true,
	"SetMultiSourceRepl":             true,
	"SetDefaultMasterConn":           true,
}

// measured at the step that introduced the interface (#2453); lower it when a call moves
var replicationLinkSQLLater = map[string]int{
	"cluster/cluster_analytic_child.go":    2, // the analytic child's replicate_ignore_table
	"cluster/cluster_columnstore_child.go": 1, // the ColumnStore child's replicate_ignore_table
	"cluster/prov.go":                      1, // the bootstrap's default_master_connection
	"cluster/spider.go":                    1, // the Spider shard's multi-source channel
	"cluster/srv_set.go":                   1, // default_master_connection
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
			problems = append(problems, fmt.Sprintf("%s: %d lines call a replication link helper not behind the interface yet, not in the allowed list: add the operation to ReplicationPlugin instead", rel, got))
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

// nativeRows are rows as native replications detached from a server (a struct literal).
func nativeRows(rows ...dbhelper.SlaveStatus) ReplicationList {
	return (*ServerMonitor)(nil).replicationsOfRows(rows...)
}

// replicationsOfRows are rows as the monitor stores them: native replications of server.
func (server *ServerMonitor) replicationsOfRows(rows ...dbhelper.SlaveStatus) ReplicationList {
	out := ReplicationList{}
	for _, row := range rows {
		if server == nil {
			out = append(out, newNativeObject(replicationBase{manifest: mariadbNativeManifest, name: row.ConnectionName.String, row: row}))
			continue
		}
		out = append(out, server.newNativeReplication(row.ConnectionName.String, row))
	}
	return out
}

// The native link calls nothing but the dbhelper helper of its operation, on its own channel,
// and records the change it makes (T27); a successful start drops it.
func TestNativeLinkRecordsChanges(t *testing.T) {
	c, sv := reseedLinkFixture()
	link := sv.ReplicationChannelLink("belair")
	if KindOf(link) != ReplicationKindNative || link.Name() != "belair" || !CanFailover(link) {
		t.Fatalf("kind %s name %q", KindOf(link), link.Name())
	}
	// no connection: every verb that changes the link refuses before any SQL, and records nothing
	for name, op := range map[string]func() (string, error){
		"Stop":            func() (string, error) { return link.Stop(LinkBySwitchover) },
		"StopCopyEvents":  func() (string, error) { return link.StopCopyEvents(LinkBySwitchover) },
		"StopApplyEvents": func() (string, error) { return link.StopApplyEvents(LinkBySwitchover) },
		"ResetPositions":  func() (string, error) { return link.ResetPositions(LinkBySwitchover) },
	} {
		if _, err := op(); err == nil {
			t.Fatalf("%s without a connection must fail", name)
		}
	}
	if len(sv.linkChangeList()) != 0 {
		t.Fatal("nothing recorded for a change that never ran")
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

// The registry gives the two built-in plugins, a null plugin for an unknown name (never nil), and
// an object refuses an action its plugin's manifest does not list.
func TestReplicationPluginRegistry(t *testing.T) {
	c, sv := reseedLinkFixture()
	if err := c.newReplicationPluginList(); err != nil {
		t.Fatal(err)
	}
	for name, kind := range map[string]ReplicationKind{ReplicationPluginMariaDBNative: ReplicationKindNative, ReplicationPluginCDC: ReplicationKindCDC} {
		m := c.ReplicationPlugin(name).Manifest()
		if m.Name != name || ReplicationKind(m.Type) != kind || len(m.Topologies) == 0 || len(m.Actions) == 0 {
			t.Errorf("%s: manifest %+v", name, m)
		}
	}
	native := c.ReplicationPlugin(ReplicationPluginMariaDBNative).Manifest()
	for _, a := range []string{ReplicationActionStartCopyEvents, ReplicationActionStopCopyEvents, ReplicationActionStartApplyEvents, ReplicationActionStopApplyEvents, ReplicationActionChangeSource, ReplicationActionRejoin,
		ReplicationActionPromote, ReplicationActionSetPosition, ReplicationActionWaitCaughtUp, ReplicationActionConfigure, ReplicationActionRotateCredential} {
		if !native.Can(a) {
			t.Errorf("native: %s not in its manifest", a)
		}
	}
	for _, a := range []string{ReplicationActionBootstrap, ReplicationActionCreate} {
		if native.Can(a) {
			t.Errorf("native: %s listed before step 5", a)
		}
	}
	if err := c.ReplicationPlugin(ReplicationPluginMariaDBNative).Bootstrap(c, "master-slave", sv); !errors.Is(err, ErrReplicationNotSupported) {
		t.Errorf("native Bootstrap: want ErrReplicationNotSupported (step 5), got %v", err)
	}
	if _, err := c.ReplicationPlugin(ReplicationPluginMariaDBNative).Create(Endpoint{Host: "db2.belair", Port: "3306"}, sv, "belair", LinkByTest); !errors.Is(err, ErrReplicationNotSupported) {
		t.Errorf("native Create: want ErrReplicationNotSupported (step 5), got %v", err)
	}
	unknownPlugin := c.ReplicationPlugin("repl-nowhere-nowhere-native")
	if unknownPlugin == nil || unknownPlugin.Manifest().Name != "repl-nowhere-nowhere-native" {
		t.Fatalf("unknown plugin: %+v", unknownPlugin)
	}
	// an entry of an unknown plugin (decoded from JSON): every verb refused
	unknown := &replicationBase{manifest: unknownPlugin.Manifest(), target: sv, name: "x"}
	if _, err := unknown.Stop(LinkByTest); !errors.Is(err, ErrReplicationNotSupported) {
		t.Errorf("unknown plugin Stop: want ErrReplicationNotSupported, got %v", err)
	}
	if err := unknown.Rejoin(); !errors.Is(err, ErrReplicationNotSupported) {
		t.Errorf("unknown plugin Rejoin: want ErrReplicationNotSupported, got %v", err)
	}
	if CanFailover(unknown) {
		t.Error("the failover never moves a replication whose plugin can not promote")
	}
	if (*Cluster)(nil).ReplicationPlugin(ReplicationPluginMariaDBNative).Manifest().Name != ReplicationPluginMariaDBNative {
		t.Error("a nil cluster still resolves a built-in plugin")
	}
}

// The ends of a native replication: its source resolved among this cluster's servers, then the
// other clusters' (a parent), else an external address.
func TestReplicationEnds(t *testing.T) {
	c, sv := reseedLinkFixture()
	db2 := &ServerMonitor{Host: "db2.belair", Port: "3306", URL: "db2.belair:3306", ClusterGroup: c}
	c.Servers = serverList{sv, db2}
	parent := &Cluster{Name: "parent", Conf: &config.Config{}}
	p1 := &ServerMonitor{Host: "db1.parent", Port: "3306", URL: "db1.parent:3306", ClusterGroup: parent}
	parent.Servers = serverList{p1}
	c.clusterList = map[string]*Cluster{"belair": c, "parent": parent}
	sv.Replications = sv.replicationsOfRows(channel("belair", "db2.belair", "Yes", "Yes"), channel("parent", "db1.parent", "Yes", "Yes"), channel("ext", "client.example", "Yes", "Yes"))
	for _, want := range []struct {
		name    string
		cluster *Cluster
		server  *ServerMonitor
		url     string
	}{{"belair", c, db2, "db2.belair:3306"}, {"parent", parent, p1, "db1.parent:3306"}, {"ext", nil, nil, "client.example:3306"}} {
		r := sv.Replications.Named(want.name)
		if r.SourceCluster() != want.cluster || r.SourceServer().Server != want.server || r.SourceServer().URL() != want.url {
			t.Errorf("%s: source %v %+v", want.name, r.SourceCluster(), r.SourceServer())
		}
		if r.TargetServer() != sv || r.TargetCluster() != c || r.Plugin().Name != ReplicationPluginMariaDBNative {
			t.Errorf("%s: target %v %v", want.name, r.TargetServer(), r.TargetCluster())
		}
	}
	if sv.ReplicationLink() != sv.Replications.Named("belair") {
		t.Error("the managed link is the object of the monitor's read")
	}
	if fresh := sv.ReplicationChannelLink("new"); fresh.Name() != "new" || fresh.TargetServer() != sv || fresh.SourceServer().URL() != "" {
		t.Errorf("a channel not read yet: %+v", fresh)
	}
}

// The JSON does not break: each entry of replications keeps every key of the row, with its
// value, and adds the plugin and the four ends; decoded back, it is the same.
func TestReplicationJSONKeepsTheRowKeys(t *testing.T) {
	c, sv := reseedLinkFixture()
	db2 := &ServerMonitor{Host: "db2.belair", Port: "3306", URL: "db2.belair:3306", ClusterGroup: c}
	c.Servers = serverList{sv, db2}
	row := channel("belair", "db2.belair", "Yes", "No")
	row.Kind = string(ReplicationKindNative)
	row.MasterServerID = 7
	row.GtidSlavePos = sql.NullString{String: "0-1-9", Valid: true}
	sv.Replications = sv.replicationsOfRows(row)

	want, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var rowKeys map[string]json.RawMessage
	if err := json.Unmarshal(want, &rowKeys); err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(sv.Replications)
	if err != nil {
		t.Fatal(err)
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(got, &entries); err != nil || len(entries) != 1 {
		t.Fatalf("%s %v", got, err)
	}
	e := entries[0]
	for k, v := range rowKeys {
		if string(e[k]) != string(v) {
			t.Errorf("key %q: %s, today %s", k, e[k], v)
		}
	}
	for k, v := range map[string]string{"plugin": ReplicationPluginMariaDBNative, "sourceCluster": "belair", "sourceServer": "db2.belair:3306", "targetCluster": "belair", "targetServer": "db1.child:3306"} {
		var s string
		if err := json.Unmarshal(e[k], &s); err != nil || s != v {
			t.Errorf("%s: %s, want %q", k, e[k], v)
		}
	}
	if len(e) != len(rowKeys)+5 {
		t.Errorf("%d keys, want the row's %d and 5", len(e), len(rowKeys))
	}

	// the API clients and the gRPC double marshal decode it back
	var back ReplicationList
	if err := json.Unmarshal(got, &back); err != nil || len(back) != 1 {
		t.Fatalf("decode: %v %d", err, len(back))
	}
	if back[0].Row() != row || back[0].Name() != "belair" || KindOf(back[0]) != ReplicationKindNative {
		t.Fatalf("decoded: %+v", back[0].Row())
	}
	again, _ := json.Marshal(back)
	if string(again) != string(got) {
		t.Fatalf("decode then encode changed it:\n%s\n%s", got, again)
	}
	if _, err := back[0].Stop(LinkByTest); err == nil {
		t.Fatal("a decoded copy acts on no server")
	}
	// a server's JSON
	var srv struct {
		Replications []map[string]json.RawMessage `json:"replications"`
	}
	b, _ := json.Marshal(sv)
	if err := json.Unmarshal(b, &srv); err != nil || len(srv.Replications) != 1 || string(srv.Replications[0]["connectionName"]) != string(rowKeys["connectionName"]) {
		t.Fatalf("server JSON: %v %+v", err, srv.Replications)
	}
	var decoded ServerMonitor
	if err := json.Unmarshal(b, &decoded); err != nil || len(decoded.Replications) != 1 {
		t.Fatalf("a ServerMonitor decodes (the CLI, the gRPC API): %v", err)
	}
}
