package repmanmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	log "github.com/sirupsen/logrus"
)

// fakeRepman records the ACL questions the MCP layer asks and answers them from
// a table, so the mapping tool → REST URL is tested without a real server.
type fakeRepman struct {
	clusters map[string]*cluster.Cluster
	allowed  map[string]bool // "user url" → allowed
	asked    []string
	events   []string
}

func (f *fakeRepman) GetClusters() map[string]*cluster.Cluster                 { return f.clusters }
func (f *fakeRepman) GetClusterByName(n string) *cluster.Cluster               { return f.clusters[n] }
func (f *fakeRepman) GetVersion() string                                       { return "test" }
func (f *fakeRepman) GetFullVersion() string                                   { return "test" }
func (f *fakeRepman) GetStatus() string                                        { return "running" }
func (f *fakeRepman) GetConf() *config.Config                                  { return &config.Config{} }
func (f *fakeRepman) SetClusterSetting(*cluster.Cluster, string, string) error { return nil }
func (f *fakeRepman) SwitchClusterSetting(*cluster.Cluster, string) error      { return nil }
func (f *fakeRepman) LogSecurityEvent(event, user, remote, msg string) {
	f.events = append(f.events, event)
}
func (f *fakeRepman) AuthenticateMCP(r *http.Request) (*Principal, error) {
	if r.Header.Get("Authorization") == "Bearer good" {
		return &Principal{User: "alice", AuthMethod: "token", TokenID: "t1"}, nil
	}
	return nil, http.ErrNoCookie
}
func (f *fakeRepman) AuthorizeMCP(p *Principal, clusterName string, url string) bool {
	f.asked = append(f.asked, p.User+" "+url)
	return f.allowed[p.User+" "+url]
}

func newTestMCP(auth bool, write bool) (*MCPServer, *fakeRepman) {
	f := &fakeRepman{
		clusters: map[string]*cluster.Cluster{"c1": {Name: "c1"}, "c2": {Name: "c2"}},
		allowed:  map[string]bool{},
	}
	conf := &config.Config{MCPAuthEnabled: auth, MCPWriteEnabled: write, Version: "test"}
	return NewMCPServer(f, conf, log.New()), f
}

// call sends a tools/call JSON-RPC message through the MCP server and returns
// the textual result, so the ACL wrapper installed by addTool is exercised.
func call(s *MCPServer, ctx context.Context, name string, args map[string]any) string {
	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	})
	out, _ := json.Marshal(s.mcp.HandleMessage(ctx, msg))
	return string(out)
}

func TestEveryToolHasAnACLMapping(t *testing.T) {
	s, _ := newTestMCP(true, true)
	registered := map[string]bool{}
	for _, tool := range s.mcp.ListTools() {
		registered[tool.Tool.Name] = true
		if _, ok := toolACLPaths[tool.Tool.Name]; !ok {
			t.Errorf("tool %s has no entry in toolACLPaths (it would be refused)", tool.Tool.Name)
		}
	}
	for name := range toolACLPaths {
		if !registered[name] {
			t.Errorf("toolACLPaths maps %s which is not a registered tool", name)
		}
	}
}

func TestACLURLBuilding(t *testing.T) {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"cluster_name": "c1", "server_name": "db1", "setting_name": "failover-mode", "setting_value": "manual", "snapshot_id": "abc"}
	cases := map[string]string{
		"cluster-switchover":   "/api/clusters/c1/actions/switchover",
		"get-server-variables": "/api/clusters/c1/servers/db1/variables",
		"cluster-set-setting":  "/api/clusters/c1/settings/actions/set/failover-mode/manual",
		"restic-purge":         "/api/clusters/c1/restic/purge/abc",
		"get-cluster-settings": "/api/clusters/c1",
		"get-cluster-topology": "/api/clusters/c1",
	}
	for tool, want := range cases {
		if got := aclURL("c1", toolACLPaths[tool], req); got != want {
			t.Errorf("%s: got %s want %s", tool, got, want)
		}
	}
}

func TestToolCallRunsTheClusterACL(t *testing.T) {
	s, f := newTestMCP(true, true)
	args := map[string]any{"cluster_name": "c1"}

	// No principal on the context: refused before the handler.
	out := call(s, context.Background(), "get-cluster-health", args)
	if !strings.Contains(out, "unauthenticated") {
		t.Errorf("no principal must be refused, got %s", out)
	}
	// Principal without the grant: refused, the ACL was asked with the mirrored URL.
	ctx := withPrincipal(context.Background(), &Principal{User: "alice", AuthMethod: "token"})
	out = call(s, ctx, "cluster-switchover", args)
	if !strings.Contains(out, "forbidden") {
		t.Errorf("missing grant must be refused, got %s", out)
	}
	if len(f.asked) == 0 || f.asked[len(f.asked)-1] != "alice /api/clusters/c1/actions/switchover" {
		t.Errorf("ACL must be asked with the mirrored REST URL, got %v", f.asked)
	}
	// Grant present: the handler runs (c1 has no real master, so the tool answers
	// with its own error, which proves the ACL gate was passed).
	f.allowed["alice /api/clusters/c1/actions/switchover"] = true
	out = call(s, ctx, "cluster-switchover", args)
	if strings.Contains(out, "forbidden") || strings.Contains(out, "unauthenticated") {
		t.Errorf("granted URL must reach the handler, got %s", out)
	}
	// Unknown cluster name still goes through the ACL (which refuses it).
	out = call(s, ctx, "get-cluster-health", map[string]any{"cluster_name": "nope"})
	if !strings.Contains(out, "forbidden") {
		t.Errorf("unknown cluster must be refused by the ACL, got %s", out)
	}
}

func TestListClustersFollowsVisibility(t *testing.T) {
	s, f := newTestMCP(true, false)
	ctx := withPrincipal(context.Background(), &Principal{User: "alice", AuthMethod: "token"})
	f.allowed["alice /api/clusters/c1"] = true
	out := call(s, ctx, "list-clusters", nil)
	if !strings.Contains(out, "c1") || strings.Contains(out, "c2") {
		t.Errorf("only c1 is visible, got %s", out)
	}
	// Auth off: everything visible, ACL never asked, no principal needed.
	s2, f2 := newTestMCP(false, false)
	out = call(s2, context.Background(), "list-clusters", nil)
	if !strings.Contains(out, "c1") || !strings.Contains(out, "c2") {
		t.Errorf("auth off must list every cluster, got %s", out)
	}
	if len(f2.asked) != 0 {
		t.Error("auth off must not ask the ACL")
	}
}

func TestInjectPrincipalAndMiddleware(t *testing.T) {
	s, f := newTestMCP(true, false)
	r, _ := http.NewRequest("GET", "/sse", nil)
	r.Header.Set("Authorization", "Bearer good")
	if p := principalFrom(s.injectPrincipal(context.Background(), r)); p == nil || p.User != "alice" {
		t.Error("a valid bearer must yield the principal in the context")
	}
	r.Header.Set("Authorization", "Bearer bad")
	if p := principalFrom(s.injectPrincipal(context.Background(), r)); p != nil {
		t.Error("an invalid bearer must yield no principal")
	}
	rec := &recorder{}
	authMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { rec.next = true }), f, log.New()).ServeHTTP(rec, r)
	if rec.next || rec.code != http.StatusUnauthorized || len(f.events) == 0 || f.events[0] != "mcp_auth_failure" {
		t.Errorf("bad bearer: next=%v code=%d events=%v", rec.next, rec.code, f.events)
	}
}

type recorder struct {
	code int
	next bool
	h    http.Header
}

func (r *recorder) Header() http.Header {
	if r.h == nil {
		r.h = http.Header{}
	}
	return r.h
}
func (r *recorder) Write(b []byte) (int, error) { return len(b), nil }
func (r *recorder) WriteHeader(c int)           { r.code = c }

func TestResolveServerByAnyName(t *testing.T) {
	srv := &cluster.ServerMonitor{Id: "db123", Name: "db1", Host: "db1.c1.svc", Port: "3306", URL: "db1.c1.svc:3306"}
	cl := &cluster.Cluster{Name: "c1", Servers: []*cluster.ServerMonitor{srv}}
	for _, name := range []string{"db123", "db1", "db1.c1.svc", "db1.c1.svc:3306"} {
		if got := resolveServer(cl, name); got != srv {
			t.Errorf("%q must resolve the server", name)
		}
	}
	if resolveServer(cl, "nope") != nil || resolveServer(cl, "") != nil {
		t.Error("unknown or empty name must not resolve")
	}
}
