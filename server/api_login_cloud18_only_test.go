// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// #2262: on an instance registered to Cloud18 that offers Sign in with Cloud18 (mcp-server-auth
// auto), /api/login takes the admin account (the admin zone) and refuses the other local accounts
// with the 401 of a wrong password (never an oracle of it); mcp-server-auth = any (the recovery in
// config.toml) lets them in again, and so does a repman without Sign in with Cloud18 (on-premise,
// registered or not: its local accounts and their ACL stay).
func TestLoginCloud18OnlyRefusesLocalAccounts(t *testing.T) {
	_, issuer := newRegisterFakes(t)
	repman := ssoRepman(t, issuer)
	cl := &cluster.Cluster{Name: "c1", Conf: &config.Config{}, WorkingDir: t.TempDir()}
	cl.APIUsers = map[string]cluster.APIUser{
		"admin": {User: "admin", Password: "admin-pw", Grants: map[string]bool{config.GrantGlobalAdminShow: true}},
		"bob":   {User: "bob", Password: "bob-pw", Grants: map[string]bool{config.GrantDBShowVariables: true}},
	}
	repman.Clusters = map[string]*cluster.Cluster{cl.Name: cl}
	repman.ClusterList = []string{cl.Name}
	repman.clustersReady.Store(true)
	login := func(user, pw string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"`+user+`","password":"`+pw+`"}`))
		w := httptest.NewRecorder()
		repman.loginHandler(w, req)
		repman.UserAuthTry.Delete(user)
		return w.Code
	}

	if !repman.Conf.Cloud18LoginRequired() {
		t.Fatal("a Cloud18 instance with Sign in with Cloud18 and mcp-server-auth auto takes Cloud18 logins only")
	}
	if code := login("admin", "admin-pw"); code != http.StatusOK {
		t.Fatalf("the admin zone's login must pass, got %d", code)
	}
	if code := login("bob", "bob-pw"); code != http.StatusUnauthorized {
		t.Fatalf("a local account other than an admin account must be refused 401, got %d", code)
	}
	if code := login("bob", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("a wrong password: the same 401 (no oracle of the local password), got %d", code)
	}
	repman.Conf.Cloud18SSOLogin = false
	if code := login("bob", "bob-pw"); code != http.StatusOK {
		t.Fatalf("a registered repman without Sign in with Cloud18 (on-premise) keeps its local accounts, got %d", code)
	}
	repman.Conf.Cloud18SSOLogin = true
	repman.Conf.MCPServerAuth = config.ConstMCPAuthAny
	if code := login("bob", "bob-pw"); code != http.StatusOK {
		t.Fatalf("mcp-server-auth any lets the local accounts in again, got %d", code)
	}
}
