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

// #2262: on an instance registered to Cloud18 (mcp-server-auth auto), /api/login takes the
// admin account (the admin zone) and refuses the other local accounts, whatever their password;
// mcp-server-auth = any (the recovery in config.toml) lets them in again.
func TestLoginCloud18OnlyRefusesLocalAccounts(t *testing.T) {
	_, issuer := newRegisterFakes(t)
	repman := loginRepman(t, issuer)
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

	if !repman.Conf.Cloud18LoginOnly() {
		t.Fatal("a Cloud18 instance with mcp-server-auth auto takes Cloud18 logins only")
	}
	if code := login("admin", "admin-pw"); code != http.StatusOK {
		t.Fatalf("the admin zone's login must pass, got %d", code)
	}
	if code := login("bob", "bob-pw"); code != http.StatusForbidden {
		t.Fatalf("a local account other than the admin must be refused 403, got %d", code)
	}
	if code := login("bob", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("a wrong password stays 401, got %d", code)
	}
	repman.Conf.MCPServerAuth = config.ConstMCPAuthAny
	if code := login("bob", "bob-pw"); code != http.StatusOK {
		t.Fatalf("mcp-server-auth any lets the local accounts in again, got %d", code)
	}
}
