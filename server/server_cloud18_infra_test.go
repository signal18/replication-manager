package server

import (
	repmanmcp "github.com/signal18/replication-manager/mcp"
	"strings"
	"testing"
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
	if s, _ := normalizeSpec(Cloud18ClusterSpec{ClusterName: "x", Proxy: "none", DBCount: 1}); len(plannedHosts(s)) != 1 {
		t.Error("proxy none must add no proxy")
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
