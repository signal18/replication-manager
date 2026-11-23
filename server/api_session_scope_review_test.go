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

// Review 3 and 4: an instance account whose password a cluster overrides gets a scoped session,
// which reaches no instance-wide action.
func TestInstanceActionsNeedAnUnrestrictedLogin(t *testing.T) {
	repman, _, c2 := newScopeTestManager(t)
	u := c2.APIUsers["admin"]
	u.Password = "c2-own-admin-pw"
	c2.APIUsers["admin"] = u

	scoped := scopeLogin(t, repman, "admin", "admin-pw")
	clusters, restricted := repman.requestSessionClusters(bearerRequest(scoped, "/api/clusters/c1"))
	if !restricted || len(clusters) != 1 || clusters[0] != "c1" {
		t.Fatalf("an instance password that a cluster overrides opens the other clusters only, got %v %v", clusters, restricted)
	}
	if repman.requestUnrestricted(bearerRequest(scoped, "/api/x")) {
		t.Fatal("a scoped session is not unrestricted")
	}
	w := httptest.NewRecorder()
	if repman.isGlobalSettingsAdmin(w, bearerRequest(scoped, "/api/clusters/settings/actions/set/x/y"), "x") {
		t.Fatal("a scoped admin session must not change global settings")
	}
	req := bearerRequest(scoped, "/api/clusters/peers")
	w = httptest.NewRecorder()
	repman.handlerMuxPeerClusters(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "[]" {
		t.Fatalf("a scoped session lists no peer cluster, got %d %s", w.Code, w.Body.String())
	}

	// the instance account with its password everywhere: unrestricted
	c2.APIUsers["admin"] = cluster.APIUser{User: "admin", Password: "admin-pw", Roles: u.Roles, Grants: u.Grants}
	full := scopeLogin(t, repman, "admin", "admin-pw")
	if !repman.requestUnrestricted(bearerRequest(full, "/api/x")) {
		t.Fatal("the instance account with its password on every cluster is unrestricted")
	}
	w = httptest.NewRecorder()
	if !repman.isGlobalSettingsAdmin(w, bearerRequest(full, "/api/clusters/settings/actions/set/x/y"), "x") {
		t.Fatal("the instance's admin changes global settings")
	}

	// a token created from a scoped session is restricted too
	pA := scopeLogin(t, repman, "shared", "pw-of-c1")
	tok := scopeCreateToken(t, repman, pA, APITokenForm{Label: "a"})
	if repman.requestUnrestricted(bearerRequest(tok.Token, "/api/clusters/c1")) {
		t.Fatal("a token of a scoped login is not unrestricted")
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
	clusters, restricted := repman.sessionClusters(m)
	if !restricted || len(clusters) != 1 || clusters[0] != "c1" {
		t.Fatalf("a session without login form is scoped like a login, got %v %v", clusters, restricted)
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
