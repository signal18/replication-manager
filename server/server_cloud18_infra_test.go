package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	repmanmcp "github.com/signal18/replication-manager/mcp"
	"github.com/signal18/replication-manager/peer"
)

func TestCloud18ClusterSpecNormalizeAndPlan(t *testing.T) {
	spec, err := normalizeSpec(Cloud18ClusterSpec{ClusterName: " Blab ", Apps: []string{"phpmyadmin", " "}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.ClusterName != "blab" || spec.DBImage != "mariadb:lts" || spec.DBCount != 2 || spec.Proxy != "haproxy" || len(spec.Apps) != 1 {
		t.Errorf("defaults wrong: %+v", spec)
	}
	hosts := plannedHosts(spec)
	got := []string{}
	for _, h := range hosts {
		got = append(got, h["type"]+":"+h["host"]+":"+h["port"])
	}
	// Short names only: the infrastructure appends its service domain itself.
	want := "database:db1:3306 database:db2:3306 haproxy:haproxy1:3306 app:phpmyadmin1:80"
	if strings.Join(got, " ") != want {
		t.Errorf("plan = %q, want %q", strings.Join(got, " "), want)
	}
	if hosts[3]["template"] != "phpmyadmin" {
		t.Errorf("app template must be kept: %v", hosts[3])
	}
	for _, bad := range []Cloud18ClusterSpec{
		{ClusterName: ""},
		{ClusterName: "a.b"},
		{ClusterName: "x", DBCount: 9},
		{ClusterName: "x", Proxy: "maxscale"},
	} {
		if _, err := normalizeSpec(bad); err == nil {
			t.Errorf("spec %+v must be refused", bad)
		}
	}
	if s, _ := normalizeSpec(Cloud18ClusterSpec{ClusterName: "x", Proxy: "none", DBCount: 1, Apps: []string{"none"}}); len(plannedHosts(s)) != 1 {
		t.Error("proxy none and apps none must add nothing to the one database")
	}
}

// Whose credentials reach an infrastructure: the SSO user is themselves, a local admin
// acts as the instance's identity, a local user and an API token are refused with the way in.
func TestPeerIdentityFor(t *testing.T) {
	decrypt := func(v string) string {
		if v == "enc" {
			return "clear"
		}
		return ""
	}
	cases := []struct {
		name      string
		p         *repmanmcp.Principal
		admin     bool
		instance  string
		wantMode  string
		wantUser  string
		wantInMsg string
	}{
		{"sso user is themselves", &repmanmcp.Principal{User: "u@x.io", AuthMethod: "oidc", Auth: "enc"}, false, "inst@x.io", "caller", "u@x.io", ""},
		{"sso user without credential", &repmanmcp.Principal{User: "u@x.io", AuthMethod: "oidc", Auth: ""}, false, "inst@x.io", "refused", "", "log in again"},
		{"api token refused", &repmanmcp.Principal{User: "u@x.io", AuthMethod: "token"}, true, "inst@x.io", "refused", "", "API token"},
		{"local admin acts as the instance", &repmanmcp.Principal{User: "admin", AuthMethod: "password", Auth: "x"}, true, "inst@x.io", "instance", "inst@x.io", ""},
		{"local admin on an unregistered instance", &repmanmcp.Principal{User: "admin", AuthMethod: "password"}, true, "", "refused", "", "Cloud18"},
		{"local user refused", &repmanmcp.Principal{User: "bob", AuthMethod: "password"}, false, "inst@x.io", "refused", "", "Cloud18 (GitLab) account"},
		{"nobody", nil, false, "inst@x.io", "refused", "", "unauthenticated"},
	}
	for _, c := range cases {
		got := peerIdentityFor(c.p, decrypt, c.admin, c.instance, "instpass")
		if got.mode != c.wantMode || got.user != c.wantUser {
			t.Errorf("%s: got mode %q user %q, want %q %q", c.name, got.mode, got.user, c.wantMode, c.wantUser)
		}
		if c.wantInMsg != "" && !contains(got.reason, c.wantInMsg) {
			t.Errorf("%s: reason %q must mention %q", c.name, got.reason, c.wantInMsg)
		}
		if got.mode == "caller" && got.password != "clear" {
			t.Errorf("%s: the caller's credential must be the decrypted one", c.name)
		}
	}
}

// The infrastructure that is this instance: the caller's own credential over the loopback,
// on the plain monitor port, 10001 when none is configured.
func TestPeerLoginAsLoopback(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{APIPublicURL: "https://me.example/"}}
	if _, _, err := repman.peerLoginAs(nil, "https://me.example"); err == nil {
		t.Fatal("no principal: refused")
	}
	if _, _, err := repman.peerLoginAs(&repmanmcp.Principal{User: "u@x.io", AuthMethod: "oidc"}, "https://me.example"); err == nil {
		t.Fatal("no bearer on the request: refused")
	}
	p := &repmanmcp.Principal{User: "u@x.io", AuthMethod: "token", Bearer: "tok"}
	sess, as, err := repman.peerLoginAs(p, "https://me.example")
	if err != nil || as != "u@x.io" || sess.token != "tok" || sess.base != "https://me.example" || sess.callBase != "http://127.0.0.1:10001" {
		t.Fatalf("loopback session: err=%v as=%s sess=%+v", err, as, sess)
	}
	repman.Conf.HttpPort = "10101"
	if sess, _, _ = repman.peerLoginAs(p, ""); sess.endpoint() != "http://127.0.0.1:10101" {
		t.Fatalf("an empty infrastructure is this instance, on the configured port: %s", sess.endpoint())
	}
}

// fakeInfra is a marketplace infrastructure: logins answer numbered tokens, the
// self-service status answers 401 to a token the infrastructure revoked.
type fakeInfra struct {
	srv     *httptest.Server
	logins  atomic.Int32
	revoked atomic.Value // token
	refuse  atomic.Bool
}

func newFakeInfra(t *testing.T) *fakeInfra {
	f := &fakeInfra{}
	f.revoked.Store("")
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/login":
			if f.refuse.Load() {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte("bad credentials"))
				return
			}
			n := f.logins.Add(1)
			json.NewEncoder(w).Encode(map[string]string{"token": fmt.Sprintf("t%d", n)})
		case "/api/cloud18/self-service":
			tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if tok == "" || tok == f.revoked.Load().(string) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"enabled": true, "remaining": 2, "identity": "u@x.io", "secret": "never forwarded"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func infraManager(t *testing.T, urls ...string) *ReplicationManager {
	pm := peer.NewPeerManager(60)
	for _, u := range urls {
		pm.PeerURL[u] = &peer.PeerNodeStatus{}
	}
	return &ReplicationManager{Conf: &config.Config{APIPublicURL: "https://me.example"}, PeerManager: pm}
}

// A session is reused within its TTL, the cache is bounded, and a session the
// infrastructure answers 401 to is dropped and the login done again once.
func TestPeerSessionReuseCapAndRelogin(t *testing.T) {
	f := newFakeInfra(t)
	repman := infraManager(t, f.srv.URL)
	p := &repmanmcp.Principal{User: "u@x.io", AuthMethod: "oidc", Auth: "pw"}
	if _, _, err := repman.peerLoginAs(p, "https://unknown.example"); err == nil || !strings.Contains(err.Error(), "not a known Cloud18 infrastructure") {
		t.Fatalf("unknown infrastructure: %v", err)
	}
	s1, as, err := repman.peerLoginAs(p, f.srv.URL)
	if err != nil || as != "u@x.io" || s1.token != "t1" {
		t.Fatalf("first login: err=%v as=%s sess=%+v", err, as, s1)
	}
	if s2, _, _ := repman.peerLoginAs(p, f.srv.URL); s2.token != "t1" || f.logins.Load() != 1 {
		t.Fatalf("a session within its TTL is reused, got %s after %d logins", s2.token, f.logins.Load())
	}
	// the cache is bounded: an insert at the cap drops it, never grows it
	repman.forgetPeerSession("u@x.io", f.srv.URL)
	repman.peerSessionMu.Lock()
	for i := 0; i < 1024; i++ {
		repman.peerSessions[fmt.Sprintf("x%d|y", i)] = peerSessionEntry{sess: &peerSession{}, at: time.Now()}
	}
	repman.peerSessionMu.Unlock()
	if s3, _, _ := repman.peerLoginAs(p, f.srv.URL); s3.token != "t2" {
		t.Fatalf("a forgotten session logs in again, got %s", s3.token)
	}
	repman.peerSessionMu.Lock()
	n := len(repman.peerSessions)
	repman.peerSessionMu.Unlock()
	if n != 1 {
		t.Fatalf("the cache is dropped at the cap, holds %d", n)
	}
	// the infrastructure revokes t2: the access entry logs in again, once
	f.revoked.Store("t2")
	entry := repman.cloud18InfrastructureAccessOne(p, f.srv.URL)
	if entry.Error != "" || entry.Identity != "u@x.io" || f.logins.Load() != 3 {
		t.Fatalf("401 re-login: %+v after %d logins", entry, f.logins.Load())
	}
	if entry.SelfService["enabled"] != true || entry.SelfService["secret"] != nil {
		t.Fatalf("the self-service fields an assistant decides on, nothing else: %+v", entry.SelfService)
	}
	cfg := entry.MCPServerConfig["headers"].(map[string]string)
	if cfg["Authorization"] != "Bearer t3" {
		t.Fatalf("the entry carries the fresh session: %+v", entry.MCPServerConfig)
	}
	// a login the infrastructure refuses is an error entry, not a failure of the listing
	f.refuse.Store(true)
	repman.forgetPeerSession("u@x.io", f.srv.URL)
	if entry = repman.cloud18InfrastructureAccessOne(p, f.srv.URL); entry.Error == "" || entry.Identity != "" || entry.MCPServerConfig != nil {
		t.Fatalf("a refused login is listed with its reason: %+v", entry)
	}
	if !strings.Contains(entry.Error, "HTTP 401") {
		t.Fatalf("the reason names the refusal: %s", entry.Error)
	}
}

func TestCloud18SpecPostgres(t *testing.T) {
	s, err := normalizeSpec(Cloud18ClusterSpec{ClusterName: "pg", DBImage: "postgres:17", DBCount: 3})
	if err != nil || s.Topology != config.TopoMasterSlavePgStream || len(s.Apps) != 1 || s.Apps[0] != "adminer" {
		t.Fatalf("stream default: %v %v", s, err)
	}
	h := plannedHosts(s)
	if h[0]["template"] != "postgres/postgres" || h[1]["template"] != "postgres/postgres-standby" || h[2]["template"] != "postgres/postgres-standby" || h[0]["port"] != "5432" || h[3]["type"] != "haproxy" || h[3]["port"] != "5432" {
		t.Fatalf("stream hosts: %v", h)
	}
	s, _ = normalizeSpec(Cloud18ClusterSpec{ClusterName: "pg", DBImage: "postgres:17", Topology: "master-slave-pg-logical"})
	if postgresMemberTemplate(s, 2) != "postgres/postgres-peer" {
		t.Fatalf("logical member 2: %s", postgresMemberTemplate(s, 2))
	}
	if s, _ = normalizeSpec(Cloud18ClusterSpec{ClusterName: "pg", DBImage: "postgres:17", DBCount: 1}); s.Topology != config.TopoActivePassive {
		t.Fatalf("one node: %v", s.Topology)
	}
	if _, err = normalizeSpec(Cloud18ClusterSpec{ClusterName: "pg", DBImage: "postgres:17", Proxy: "proxysql"}); err == nil {
		t.Fatal("proxysql in front of PostgreSQL must be refused")
	}
	if _, err = normalizeSpec(Cloud18ClusterSpec{ClusterName: "pg", DBImage: "postgres:17", Topology: "multi-master-wsrep"}); err == nil {
		t.Fatal("a MariaDB topology on PostgreSQL must be refused")
	}
}

func TestCloud18SpecMySQLFamily(t *testing.T) {
	for _, img := range []string{"mysql:8.4", "percona/percona-server:8.4"} {
		s, err := normalizeSpec(Cloud18ClusterSpec{ClusterName: "m", DBImage: img})
		if err != nil || plannedHosts(s)[0]["port"] != "3306" || s.Apps[0] != "phpmyadmin" {
			t.Fatalf("%s: %v %v", img, s, err)
		}
	}
}
