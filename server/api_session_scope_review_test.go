// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"github.com/signal18/replication-manager/cluster"
	repmanmcp "github.com/signal18/replication-manager/mcp"
)

// scopeCreateToken creates a token through the API with a session.
func scopeCreateToken(t *testing.T, repman *ReplicationManager, session string, form APITokenForm) APITokenView {
	t.Helper()
	body, _ := json.Marshal(form)
	req := httptest.NewRequest(http.MethodPost, "/api/tokens", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+session)
	w := httptest.NewRecorder()
	repman.handlerMuxAPITokens(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create token: %d %s", w.Code, w.Body.String())
	}
	var v APITokenView
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	return v
}

func scopeListTokens(t *testing.T, repman *ReplicationManager, session string) []APITokenView {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+session)
	w := httptest.NewRecorder()
	repman.handlerMuxAPITokens(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list tokens: %d %s", w.Code, w.Body.String())
	}
	var out []APITokenView
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out
}

// Review 1: a scoped session lists and revokes only the tokens created from a login within
// its clusters; never a sibling's bearer string.
func TestScopedSessionListsAndRevokesItsOwnScopeTokensOnly(t *testing.T) {
	repman, _, _ := newScopeTestManager(t)
	pA := scopeLogin(t, repman, "shared", "pw-of-c1")
	pB := scopeLogin(t, repman, "shared", "pw-of-c2")
	tokA := scopeCreateToken(t, repman, pA, APITokenForm{Label: "a"})
	tokB := scopeCreateToken(t, repman, pB, APITokenForm{Label: "b"})
	inst, err := repman.createAPITokenAs("shared", "Local", APITokenForm{Label: "instance"}, "10.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}

	list := scopeListTokens(t, repman, pA)
	if len(list) != 1 || list[0].ID != tokA.ID || list[0].Token == "" {
		t.Fatalf("the c1 session must list exactly its c1 token, with its string, got %+v", list)
	}
	for _, v := range list {
		if v.ID == tokB.ID || v.ID == inst.ID {
			t.Fatal("a token created from another cluster's login, or an instance token, must be absent")
		}
	}

	revoke := func(session, id string) int {
		req := httptest.NewRequest(http.MethodDelete, "/api/tokens/"+id, nil)
		req.Header.Set("Authorization", "Bearer "+session)
		req = mux.SetURLVars(req, map[string]string{"tokenID": id})
		w := httptest.NewRecorder()
		repman.handlerMuxAPITokenRevoke(w, req)
		return w.Code
	}
	if code := revoke(pA, tokB.ID); code != http.StatusForbidden {
		t.Fatalf("the c1 session must not revoke a token of the c2 login, got %d", code)
	}
	if code := revoke(pA, inst.ID); code != http.StatusForbidden {
		t.Fatalf("the c1 session must not revoke an instance token, got %d", code)
	}
	if code := revoke(pA, tokA.ID); code != http.StatusOK {
		t.Fatalf("the c1 session revokes its own c1 token, got %d", code)
	}
}

// Review 2: the endpoints that checked the name only now refuse a scoped session elsewhere.
func TestNameOnlyReadsHonourTheSessionScope(t *testing.T) {
	repman, _, _ := newScopeTestManager(t)
	pA := scopeLogin(t, repman, "shared", "pw-of-c1")
	call := func(clusterName string) int {
		req := bearerRequest(pA, "/api/clusters/"+clusterName+"/topology/slaves/index/0")
		req = mux.SetURLVars(req, map[string]string{"clusterName": clusterName, "slaveIndex": "0"})
		w := httptest.NewRecorder()
		repman.handlerMuxSlaveIndex(w, req)
		return w.Code
	}
	if code := call("c2"); code != http.StatusForbidden {
		t.Fatalf("a scoped session must be refused on another cluster, got %d", code)
	}
	if code := call("c1"); code == http.StatusForbidden {
		t.Fatal("a scoped session passes on its own cluster")
	}
}

// Review 3, 4 and 5 (first and second review): the instance's admin with the instance's
// password covers the instance except the clusters overriding its password; a login scoped to
// some clusters, or an API token, reaches no instance-wide action.
func TestInstanceActionsNeedAnUnrestrictedLogin(t *testing.T) {
	repman, c1, c2 := newScopeTestManager(t)
	u := c2.APIUsers["admin"]
	u.Password = "c2-own-admin-pw"
	c2.APIUsers["admin"] = u
	repman.Conf.Cloud18GitUser = "owner@x.io"

	// the instance's password: instance-wide, c2 excluded
	inst := scopeLogin(t, repman, "admin", "admin-pw")
	s := repman.requestSessionScope(bearerRequest(inst, "/api/clusters/c1"))
	if s.Scoped || len(s.Excluded) != 1 || s.Excluded[0] != "c2" {
		t.Fatalf("the instance password that c2 overrides excludes c2 only, got %+v", s)
	}
	if !repman.requestUnrestricted(bearerRequest(inst, "/api/x")) {
		t.Fatal("the instance's admin stays instance-wide for the instance actions")
	}
	if ok, _ := repman.IsValidClusterACL(bearerRequest(inst, "/api/clusters/c2"), c2); ok {
		t.Fatal("the excluded cluster is refused")
	}
	if ok, _ := repman.IsValidClusterACL(bearerRequest(inst, "/api/clusters/c1"), c1); !ok {
		t.Fatal("the other clusters pass")
	}
	w := httptest.NewRecorder()
	if !repman.isGlobalSettingsAdmin(w, bearerRequest(inst, "/api/clusters/settings/actions/set/x/y"), "x") {
		t.Fatal("the instance's admin changes global settings")
	}
	claims, _ := repman.GetJWTClaims(bearerRequest(inst, "/api/x"))
	if !repman.mayLoginPeersAsInstance(bearerRequest(inst, "/api/x"), claims) {
		t.Fatal("the instance's admin logs in to the peers as the instance")
	}
	// a "*" token of that session never acts on the excluded cluster
	instTok := scopeCreateToken(t, repman, inst, APITokenForm{Label: "inst", Clusters: []string{"*"}})
	if len(instTok.OwnerExcluded) != 1 || instTok.OwnerExcluded[0] != "c2" {
		t.Fatalf("the token records the excluded cluster, got %v", instTok.OwnerExcluded)
	}
	if ok, _ := repman.IsValidClusterACL(bearerRequest(instTok.Token, "/api/clusters/c2"), c2); ok {
		t.Fatal("a token of that session never acts on the excluded cluster")
	}
	if ok, _ := repman.IsValidClusterACL(bearerRequest(instTok.Token, "/api/clusters/c1"), c1); !ok {
		t.Fatal("a token of that session acts on the other clusters")
	}

	// the admin name logged in with c2's own password: scoped to c2
	scoped := scopeLogin(t, repman, "admin", "c2-own-admin-pw")
	if s := repman.requestSessionScope(bearerRequest(scoped, "/api/x")); !s.Scoped || len(s.Clusters) != 1 || s.Clusters[0] != "c2" {
		t.Fatalf("a cluster's own admin password opens that cluster only, got %+v", s)
	}
	w = httptest.NewRecorder()
	if repman.isGlobalSettingsAdmin(w, bearerRequest(scoped, "/api/clusters/settings/actions/set/x/y"), "x") {
		t.Fatal("a scoped admin session must not change global settings")
	}
	if w.Code != http.StatusForbidden || !bytes.Contains(w.Body.Bytes(), []byte("this login is limited to clusters c2")) {
		t.Fatalf("the refusal says what the login is limited to, got %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	repman.handlerMuxPeerClusters(w, bearerRequest(scoped, "/api/clusters/peers"))
	if w.Body.String() != "[]" {
		t.Fatalf("a scoped session lists no peer cluster, got %s", w.Body.String())
	}
	claims, _ = repman.GetJWTClaims(bearerRequest(scoped, "/api/x"))
	if repman.mayLoginPeersAsInstance(bearerRequest(scoped, "/api/x"), claims) {
		t.Fatal("a scoped admin session never logs in to the peers as the instance")
	}

	gitImport := func(bearer string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/clusters/actions/fetch-from-git", nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		repman.handlerMuxFetchDynamicClustersFromGit(w, req)
		return w.Code
	}
	globalMail := func(bearer string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/mail", bytes.NewReader([]byte(`{}`)))
		req.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		repman.handlerMuxSendEmail(w, req)
		return w.Code
	}
	if code := gitImport(scoped); code != http.StatusForbidden {
		t.Fatalf("git import from a scoped admin session: want 403, got %d", code)
	}
	if code := globalMail(scoped); code != http.StatusForbidden {
		t.Fatalf("global mail from a scoped admin session: want 403, got %d", code)
	}

	// API tokens: an admin token reaches neither the git import nor the instance's peer login;
	// a token of the scoped session reaches no instance-wide action
	if code := gitImport(instTok.Token); code != http.StatusForbidden {
		t.Fatalf("git import refuses API tokens, got %d", code)
	}
	tclaims, _ := repman.GetJWTClaims(bearerRequest(instTok.Token, "/api/x"))
	if repman.mayLoginPeersAsInstance(bearerRequest(instTok.Token, "/api/x"), tclaims) {
		t.Fatal("an API token never logs in to the peers as the instance")
	}
	scopedTok := scopeCreateToken(t, repman, scoped, APITokenForm{Label: "scoped"})
	if repman.requestUnrestricted(bearerRequest(scopedTok.Token, "/api/x")) {
		t.Fatal("a token of a scoped login is not unrestricted")
	}
	if code := globalMail(scopedTok.Token); code != http.StatusForbidden {
		t.Fatalf("global mail from a token of a scoped login: want 403, got %d", code)
	}
	if repman.isCloud18Admin(bearerRequest(scopedTok.Token, "/api/register")) || repman.isCloud18Admin(bearerRequest(scoped, "/api/register")) {
		t.Fatal("the Cloud18 registration needs a login covering the instance")
	}

	// MCP: only a password login covering the instance acts as the instance
	if repman.mcpMayActAsInstance(&repmanmcp.Principal{User: "admin", AuthMethod: "token"}) {
		t.Fatal("an MCP token never acts as the instance")
	}
	if repman.mcpMayActAsInstance(&repmanmcp.Principal{User: "admin", AuthMethod: "password", SessionScoped: true, SessionClusters: []string{"c2"}}) {
		t.Fatal("a scoped MCP login never acts as the instance")
	}
	if !repman.mcpMayActAsInstance(&repmanmcp.Principal{User: "admin", AuthMethod: "password", SessionExcluded: []string{"c2"}}) {
		t.Fatal("the instance's admin over MCP acts as the instance")
	}

	// the cluster mail test goes through the cluster ACL with the session's scope
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/c1/mail", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+scoped)
	req = mux.SetURLVars(req, map[string]string{"clusterName": "c1"})
	w = httptest.NewRecorder()
	repman.handlerMuxSendEmail(w, req)
	if w.Code != http.StatusInternalServerError || !bytes.Contains(w.Body.Bytes(), []byte("No valid ACL")) {
		t.Fatalf("the cluster mail test refuses a login not covering the cluster, got %d %s", w.Code, w.Body.String())
	}
}

// Second review 3: a legacy token of the instance's Cloud18 identity, not recorded as SSO, is
// refused although the identity is passwordless now.
func TestLegacyTokenOfCloud18IdentityRefused(t *testing.T) {
	repman, c1, c2 := newScopeTestManager(t)
	for _, cl := range []*cluster.Cluster{c1, c2} {
		id := cluster.APIUser{User: "owner@x.io", Roles: map[string]bool{}}
		cl.SetUserGrants(&id, "db cluster token")
		cl.APIUsers["owner@x.io"] = id
	}
	repman.Conf.Cloud18GitUser = "owner@x.io"
	legacy := func(authType string) *APIToken {
		tok, err := repman.createAPITokenAs("owner@x.io", authType, APITokenForm{Label: "old"}, "10.0.0.1:1")
		if err != nil {
			t.Fatal(err)
		}
		st := &repman.apiTokens
		st.Lock()
		st.tokens[tok.ID].OwnerScope = ""
		st.Unlock()
		return tok
	}
	local := legacy("")
	if ok, _ := repman.IsValidClusterACL(bearerRequest(local.Token, "/api/clusters/c1"), c1); ok {
		t.Fatal("a legacy token of the Cloud18 identity not recorded as SSO must be refused")
	}
	if _, logged := repman.apiTokenOwnerRefusedLogged.Load(local.ID); !logged {
		t.Fatal("the refusal is logged once")
	}
	sso := legacy("SSO")
	if ok, _ := repman.IsValidClusterACL(bearerRequest(sso.Token, "/api/clusters/c1"), c1); !ok {
		t.Fatal("a legacy token of the Cloud18 identity recorded as SSO keeps working")
	}
}

// Second review 9: a renamed cluster, or one gone from the configuration at a restart, lets go
// of the tokens bound to its old name.
func TestTokensOfVanishedClustersRevoked(t *testing.T) {
	repman, _, _ := newScopeTestManager(t)
	pA := scopeLogin(t, repman, "shared", "pw-of-c1")
	tok := scopeCreateToken(t, repman, pA, APITokenForm{Label: "a"})
	inst, err := repman.createAPITokenAs("admin", "Local", APITokenForm{Label: "all"}, "")
	if err != nil {
		t.Fatal(err)
	}
	repman.Lock()
	delete(repman.Clusters, "c1")
	repman.Unlock()
	repman.revokeTokensOfVanishedClusters()
	st := &repman.apiTokens
	st.Lock()
	revoked, instRevoked := st.tokens[tok.ID].IsRevoked(), st.tokens[inst.ID].IsRevoked()
	st.Unlock()
	if !revoked {
		t.Fatal("a token bound to a cluster gone from the configuration is revoked")
	}
	if instRevoked {
		t.Fatal("a \"*\" token is never revoked for a vanished cluster")
	}
}

// Review 4: autologin and the dashboard never open a session as the Cloud18 identity, and a
// session opened for a per-cluster password is scoped.
func TestPasswordlessSessionsAreScopedAndRefuseTheCloud18Identity(t *testing.T) {
	repman, _, _ := newScopeTestManager(t)
	repman.Conf.APIAutologin = true
	repman.Conf.APIAutologinUser = "shared"
	repman.Conf.APITrustedNetworks = "192.0.2.0/24"
	repman.Conf.Cloud18GitUser = "shared"
	req := httptest.NewRequest(http.MethodGet, "/api/autologin", nil)
	w := httptest.NewRecorder()
	repman.autologinHandler(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("autologin as the Cloud18 identity must be refused, got %d", w.Code)
	}
	repman.Conf.APIDashboardUser = "shared"
	w = httptest.NewRecorder()
	repman.dashboardTokenHandler(w, httptest.NewRequest(http.MethodGet, "/api/dashboard-token", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("a dashboard session as the Cloud18 identity must be refused, got %d", w.Code)
	}

	info := repman.passwordSessionInfo("shared", "pw-of-c1")
	raw, _ := json.Marshal(info)
	var m map[string]interface{}
	_ = json.Unmarshal(raw, &m)
	if sc := repman.sessionScope(m); !sc.Scoped || len(sc.Clusters) != 1 || sc.Clusters[0] != "c1" {
		t.Fatalf("a session without login form is scoped like a login, got %+v", sc)
	}
}

// Review 5: a deleted cluster leaves the scope of the sessions issued before, also once a
// cluster of the same name exists again, and the tokens bound to it are revoked.
func TestDeletedClusterLeavesScopedSessionsAndRevokesBoundTokens(t *testing.T) {
	repman, c1, _ := newScopeTestManager(t)
	pA := scopeLogin(t, repman, "shared", "pw-of-c1")
	tok := scopeCreateToken(t, repman, pA, APITokenForm{Label: "a"})

	repman.markClusterDeleted("c1")
	// the same name again (the fixture's object stands for the re-created cluster)
	if ok, _ := repman.IsValidClusterACL(bearerRequest(pA, "/api/clusters/c1"), c1); ok {
		t.Fatal("a session scoped to a deleted cluster must not reach a cluster created again under its name")
	}
	if ok, _ := repman.IsValidClusterACL(bearerRequest(tok.Token, "/api/clusters/c1"), c1); ok {
		t.Fatal("a token bound to a deleted cluster must be revoked")
	}
	if got, ok := repman.parseAPITokenFromRequest(bearerRequest(tok.Token, "/api/clusters/c1")); ok {
		t.Fatalf("the bound token must be revoked, got %+v", got)
	}
	// a login after the deletion covers the new cluster
	again := scopeLogin(t, repman, "shared", "pw-of-c1")
	if ok, _ := repman.IsValidClusterACL(bearerRequest(again, "/api/clusters/c1"), c1); !ok {
		t.Fatal("a login after the re-creation covers the new cluster")
	}
}

// Review 4: an SSO login without an email from GitLab takes the address the realm authenticated,
// never a Local session; a login name that is no address gives none (the login is refused).
func TestSSOLoginEmail(t *testing.T) {
	if got := ssoLoginEmail("p@x.io"); got != "p@x.io" {
		t.Fatalf("an email login is the identity, got %q", got)
	}
	for _, bad := range []string{"admin", "", "Name <p@x.io>", "p@x.io,q@x.io"} {
		if got := ssoLoginEmail(bad); got != "" {
			t.Fatalf("%q is no email identity, got %q", bad, got)
		}
	}
}

// Review 6: whoami shows the scope's clusters only; a legacy token of a passwordless owner is an
// SSO token; the once-logged refusals follow the store.
func TestWhoAmIScopeLegacyPasswordlessAndRefusalPruning(t *testing.T) {
	repman, c1, c2 := newScopeTestManager(t)
	pA := scopeLogin(t, repman, "shared", "pw-of-c1")
	w := httptest.NewRecorder()
	repman.handlerMuxWhoAmI(w, bearerRequest(pA, "/api/whoami"))
	var who map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &who)
	grants, _ := who["grants"].(map[string]interface{})
	if _, ok := grants["c2"]; ok || grants["c1"] == nil {
		t.Fatalf("whoami of a scoped session lists its clusters only, got %v", grants)
	}

	// a passwordless owner, a token of late September 2026 (no OwnerAuthType, no OwnerScope)
	for _, cl := range []*cluster.Cluster{c1, c2} {
		sso := cluster.APIUser{User: "p@x.io", Roles: map[string]bool{}}
		cl.SetUserGrants(&sso, "db cluster token")
		cl.APIUsers["p@x.io"] = sso
	}
	old, err := repman.createAPITokenAs("p@x.io", "", APITokenForm{Label: "old"}, "10.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	st := &repman.apiTokens
	st.Lock()
	st.tokens[old.ID].OwnerScope = ""
	st.Unlock()
	if ok, _ := repman.IsValidClusterACL(bearerRequest(old.Token, "/api/clusters/c2"), c2); !ok {
		t.Fatal("a legacy token of a passwordless owner keeps working as an SSO token")
	}
	// a legacy token of an owner with a password per cluster is refused, the refusal logged once
	refused, err := repman.createAPITokenAs("shared", "", APITokenForm{Label: "old-shared"}, "10.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	st.Lock()
	st.tokens[refused.ID].OwnerScope = ""
	st.Unlock()
	if ok, _ := repman.IsValidClusterACL(bearerRequest(refused.Token, "/api/clusters/c1"), c1); ok {
		t.Fatal("a legacy token of an owner with a password per cluster is refused")
	}
	if _, logged := repman.apiTokenOwnerRefusedLogged.Load(refused.ID); !logged {
		t.Fatal("the refusal is logged once")
	}
	st.Lock()
	delete(st.tokens, refused.ID)
	err = repman.saveAPITokenStoreLocked()
	st.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, logged := repman.apiTokenOwnerRefusedLogged.Load(refused.ID); logged {
		t.Fatal("a refusal record follows its token out of the store")
	}
}
