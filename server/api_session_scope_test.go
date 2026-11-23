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

	log "github.com/sirupsen/logrus"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	repmanmcp "github.com/signal18/replication-manager/mcp"
)

// scopeTestKey is the instance key of the fixture: sessions carry the password encrypted with it.
var scopeTestKey = []byte("0123456789abcdef")

// newScopeTestManager: two clusters; "shared" is one account name with a different password
// on each cluster (a cluster's own list), holding every grant on both; "admin" is an account of
// the instance's list with the same password everywhere.
func newScopeTestManager(t *testing.T) (*ReplicationManager, *cluster.Cluster, *cluster.Cluster) {
	t.Helper()
	mk := func(name, sharedPassword string) *cluster.Cluster {
		cl := &cluster.Cluster{
			Name:           name,
			Conf:           &config.Config{SecretKey: scopeTestKey, Secrets: config.NewSecrets(map[string]config.Secret{})},
			APIUsers:       map[string]cluster.APIUser{},
			Grants:         config.GetGrantType(),
			Roles:          config.GetRoleType(),
			SecurityLogrus: log.New(),
		}
		shared := cluster.APIUser{User: "shared", Password: sharedPassword, Roles: map[string]bool{}}
		cl.SetUserGrants(&shared, "db cluster proxy grant token global")
		cl.APIUsers["shared"] = shared
		admin := cluster.APIUser{User: "admin", Password: "admin-pw", Roles: map[string]bool{}}
		cl.SetUserGrants(&admin, "db cluster proxy grant token global")
		cl.APIUsers["admin"] = admin
		return cl
	}
	c1 := mk("c1", "pw-of-c1")
	c2 := mk("c2", "pw-of-c2")
	repman := &ReplicationManager{
		Clusters:    map[string]*cluster.Cluster{c1.Name: c1, c2.Name: c2},
		ClusterList: []string{c1.Name, c2.Name},
		Conf: &config.Config{
			TokenTimeout:                   1,
			WorkingDir:                     t.TempDir(),
			SecretKey:                      scopeTestKey,
			APIUserTokens:                  true,
			APIUserTokensDefaultExpireDays: 120,
			Secrets:                        config.NewSecrets(map[string]config.Secret{"api-credentials": {Value: "admin:admin-pw"}}),
		},
		Logrus: log.New(),
	}
	repman.initKeys()
	repman.clustersReady.Store(true)
	return repman, c1, c2
}

func scopeLogin(t *testing.T, repman *ReplicationManager, user, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": user, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	w := httptest.NewRecorder()
	repman.loginHandler(w, req)
	repman.UserAuthTry.Delete(user)
	if w.Code != http.StatusOK {
		t.Fatalf("login %s: %d %s", user, w.Code, w.Body.String())
	}
	var out AuthTokenWithUpgrade
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Token == "" {
		t.Fatalf("login %s: no token in %s", user, w.Body.String())
	}
	return out.Token
}

// Fix 1: a password valid on one cluster opens a session on that cluster only.
func TestLocalLoginSessionIsScopedToTheClustersOfItsPassword(t *testing.T) {
	repman, c1, c2 := newScopeTestManager(t)
	session := scopeLogin(t, repman, "shared", "pw-of-c1")

	if ok, _ := repman.IsValidClusterACL(bearerRequest(session, "/api/clusters/c1"), c1); !ok {
		t.Fatal("the session must pass on the cluster its password authenticated")
	}
	if ok, _ := repman.IsValidClusterACL(bearerRequest(session, "/api/clusters/c2"), c2); ok {
		t.Fatal("the session must be refused on a cluster where that password is not the account's")
	}
	if _, ok := repman.requestACLUser(bearerRequest(session, "/api/clusters/c2/actions/switchover"), c2); ok {
		t.Fatal("a direct grant check must not see the account on a cluster outside the session's scope")
	}
	if _, ok := repman.requestACLUser(bearerRequest(session, "/api/clusters/c1/actions/switchover"), c1); !ok {
		t.Fatal("a direct grant check sees the account on its own cluster")
	}
	claims, err := repman.GetJWTClaims(bearerRequest(session, "/api/clusters/c1"))
	if err != nil || claims["SessionScope"] != sessionScopeClusters || claims["SessionClusters"] != "c1" {
		t.Fatalf("the claims map must expose the scope, got %v %v", claims, err)
	}
	if repman.isAdminUserOn("shared", []string{"c3"}, true) {
		t.Fatal("the scoped admin check reads the session's clusters only")
	}
	if !repman.isAdminUserOn("shared", []string{"c1"}, true) {
		t.Fatal("the scoped admin check sees the grant on the session's cluster")
	}

	// the other cluster's password: the other cluster only
	other := scopeLogin(t, repman, "shared", "pw-of-c2")
	if ok, _ := repman.IsValidClusterACL(bearerRequest(other, "/api/clusters/c1"), c1); ok {
		t.Fatal("the c2 password must not open c1")
	}

	// MCP principal: same scope
	p, err := repman.AuthenticateMCP(bearerRequest(session, "/mcp"))
	if err != nil {
		t.Fatal(err)
	}
	if !p.SessionScoped || !p.AllowsCluster("c1") || p.AllowsCluster("c2") {
		t.Fatalf("the MCP principal must carry the session's scope, got %+v", p)
	}
	if repman.AuthorizeMCP(p, "c2", "/api/clusters/c2") {
		t.Fatal("MCP must refuse a cluster outside the session's scope")
	}
}

// Fix 1: an account of the instance's list with the instance's password covers every cluster.
func TestInstanceCredentialSessionIsInstanceWide(t *testing.T) {
	repman, c1, c2 := newScopeTestManager(t)
	session := scopeLogin(t, repman, "admin", "admin-pw")
	for _, cl := range []*cluster.Cluster{c1, c2} {
		if ok, _ := repman.IsValidClusterACL(bearerRequest(session, "/api/clusters/"+cl.Name), cl); !ok {
			t.Fatalf("the instance's account must pass on %s", cl.Name)
		}
	}
	if _, restricted := repman.requestSessionClusters(bearerRequest(session, "/api/clusters/c1")); restricted {
		t.Fatal("an instance credential's session carries no cluster scope")
	}
	p := &repmanmcp.Principal{}
	if !p.AllowsCluster("any") {
		t.Fatal("a principal without scope covers every cluster")
	}
}

// Fix 2: a token created from a restricted session is bound to its clusters, "*" included.
func TestTokenFromScopedSessionNeverWidens(t *testing.T) {
	repman, c1, c2 := newScopeTestManager(t)
	session := scopeLogin(t, repman, "shared", "pw-of-c1")

	create := func(form APITokenForm) (*httptest.ResponseRecorder, APITokenView) {
		body, _ := json.Marshal(form)
		req := httptest.NewRequest(http.MethodPost, "/api/tokens", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+session)
		w := httptest.NewRecorder()
		repman.handlerMuxAPITokens(w, req)
		var v APITokenView
		_ = json.Unmarshal(w.Body.Bytes(), &v)
		return w, v
	}

	w, v := create(APITokenForm{Label: "all", Clusters: []string{"*"}})
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if v.OwnerScope != apiTokenOwnerClusters || len(v.OwnerClusters) != 1 || v.OwnerClusters[0] != "c1" {
		t.Fatalf("the record must keep the login's clusters, got %q %v", v.OwnerScope, v.OwnerClusters)
	}
	if len(v.Clusters) != 1 || v.Clusters[0] != "c1" {
		t.Fatalf("\"*\" from a restricted login means its clusters, got %v", v.Clusters)
	}
	if ok, _ := repman.IsValidClusterACL(bearerRequest(v.Token, "/api/clusters/c2"), c2); ok {
		t.Fatal("the token must be refused on a cluster its owner's login did not cover")
	}
	if ok, _ := repman.IsValidClusterACL(bearerRequest(v.Token, "/api/clusters/c1"), c1); !ok {
		t.Fatal("the token must pass on its cluster")
	}

	if w, _ := create(APITokenForm{Label: "other", Clusters: []string{"c2"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("a token for a cluster the login does not cover must be refused, got %d", w.Code)
	}

	// a record edited to "*" (or any scope) still never leaves the owner's clusters
	st := &repman.apiTokens
	st.Lock()
	st.tokens[v.ID].Clusters = []string{"*"}
	st.Unlock()
	if ok, _ := repman.IsValidClusterACL(bearerRequest(v.Token, "/api/clusters/c2"), c2); ok {
		t.Fatal("OwnerClusters bounds the scope whatever Clusters say")
	}
	if got, ok := repman.parseAPITokenFromRequest(bearerRequest(v.Token, "/api/clusters/c1")); !ok || got.IsGlobal() {
		t.Fatal("a bound token is never global")
	}
}

// Fix 2: tokens issued before the owner scope existed.
func TestLegacyTokenOwnerRules(t *testing.T) {
	repman, c1, c2 := newScopeTestManager(t)
	legacy := func(owner, authType, from string) string {
		tok, err := repman.createAPITokenAs(owner, authType, APITokenForm{Label: "old", Clusters: []string{"*"}}, from)
		if err != nil {
			t.Fatal(err)
		}
		st := &repman.apiTokens
		st.Lock()
		st.tokens[tok.ID].OwnerScope = "" // as written by an older release
		st.Unlock()
		return tok.Token
	}

	// the shared account has a password per cluster: refused
	shared := legacy("shared", "Local", "10.0.0.1:1")
	if ok, _ := repman.IsValidClusterACL(bearerRequest(shared, "/api/clusters/c1"), c1); ok {
		t.Fatal("a legacy token of an account with a password per cluster must be refused")
	}
	// the instance's account (one password everywhere): kept
	admin := legacy("admin", "Local", "10.0.0.1:1")
	if ok, _ := repman.IsValidClusterACL(bearerRequest(admin, "/api/clusters/c2"), c2); !ok {
		t.Fatal("a legacy token of an account with one password everywhere keeps working")
	}
	// an app token repman issued: kept
	app := legacy("shared", "Local", "app c1/web")
	if ok, _ := repman.IsValidClusterACL(bearerRequest(app, "/api/clusters/c1"), c1); !ok {
		t.Fatal("a legacy app token keeps working")
	}
	// an SSO owner: kept
	sso := cluster.APIUser{User: "u@x.io", Roles: map[string]bool{}}
	c1.SetUserGrants(&sso, "db cluster token")
	c1.APIUsers["u@x.io"] = sso
	ssoTok := legacy("u@x.io", "SSO", "10.0.0.1:1")
	if ok, _ := repman.IsValidClusterACL(bearerRequest(ssoTok, "/api/clusters/c1"), c1); !ok {
		t.Fatal("a legacy SSO token keeps working")
	}
}

// A removed owner loses its tokens: repman asks no external service, the user lists decide.
func TestRemovedOwnerLosesItsTokens(t *testing.T) {
	repman, c1, _ := newScopeTestManager(t)
	sso := cluster.APIUser{User: "u@x.io", Roles: map[string]bool{}}
	c1.SetUserGrants(&sso, "db cluster token")
	c1.APIUsers["u@x.io"] = sso
	tok, err := repman.createAPITokenAs("u@x.io", "SSO", APITokenForm{Label: "mcp", Clusters: []string{"c1"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := repman.IsValidClusterACL(bearerRequest(tok.Token, "/api/clusters/c1"), c1); !ok {
		t.Fatal("the token works while its owner is listed")
	}
	delete(c1.APIUsers, "u@x.io")
	if ok, _ := repman.IsValidClusterACL(bearerRequest(tok.Token, "/api/clusters/c1"), c1); ok {
		t.Fatal("an owner removed from the lists loses its tokens")
	}
}

// Maintainer requirement: a "*" token of an SSO session or of an instance-account session
// covers a cluster added after it was created; only a token of a password-scoped session stays
// bound to the clusters of its login.
func TestGlobalTokensCoverClustersAddedLater(t *testing.T) {
	repman, c1, c2 := newScopeTestManager(t)
	// an SSO identity on both clusters
	for _, cl := range []*cluster.Cluster{c1, c2} {
		sso := cluster.APIUser{User: "u@x.io", Roles: map[string]bool{}}
		cl.SetUserGrants(&sso, "db cluster proxy token")
		cl.APIUsers["u@x.io"] = sso
	}
	ssoTok, err := repman.createAPITokenAs("u@x.io", "SSO", APITokenForm{Label: "sso", Clusters: []string{"*"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	adminSession := scopeLogin(t, repman, "admin", "admin-pw")
	if _, scoped := repman.requestSessionClusters(bearerRequest(adminSession, "/api/clusters/c1")); scoped {
		t.Fatal("fixture: the instance account's session is instance-wide")
	}
	adminTok := scopeCreateToken(t, repman, adminSession, APITokenForm{Label: "admin", Clusters: []string{"*"}})
	scopedSession := scopeLogin(t, repman, "shared", "pw-of-c1")
	scopedTok := scopeCreateToken(t, repman, scopedSession, APITokenForm{Label: "scoped", Clusters: []string{"*"}})

	// a cluster added after the tokens, holding the same accounts
	c3 := &cluster.Cluster{
		Name:           "c3",
		Conf:           c1.Conf,
		APIUsers:       map[string]cluster.APIUser{},
		Grants:         c1.Grants,
		Roles:          c1.Roles,
		SecurityLogrus: c1.SecurityLogrus,
	}
	for _, name := range []string{"admin", "shared", "u@x.io"} {
		c3.APIUsers[name] = c1.APIUsers[name]
	}
	repman.Lock()
	repman.Clusters["c3"] = c3
	repman.ClusterList = append(repman.ClusterList, "c3")
	repman.Unlock()

	if ok, _ := repman.IsValidClusterACL(bearerRequest(ssoTok.Token, "/api/clusters/c3"), c3); !ok {
		t.Fatal("a \"*\" token of an SSO session covers a cluster added later")
	}
	if ok, _ := repman.IsValidClusterACL(bearerRequest(adminTok.Token, "/api/clusters/c3"), c3); !ok {
		t.Fatal("a \"*\" token of an instance-account session covers a cluster added later")
	}
	if ok, _ := repman.IsValidClusterACL(bearerRequest(scopedTok.Token, "/api/clusters/c3"), c3); ok {
		t.Fatal("a token of a password-scoped session stays bound to its login's clusters")
	}

	// deleting another cluster changes nothing for the "*" tokens
	repman.markClusterDeleted("c2")
	for _, tok := range []string{ssoTok.Token, adminTok.Token} {
		if ok, _ := repman.IsValidClusterACL(bearerRequest(tok, "/api/clusters/c3"), c3); !ok {
			t.Fatal("a cluster deletion never revokes a \"*\" token")
		}
	}
}

// The instance's Cloud18 identity never logs in with a local password, whatever a list holds.
func TestLoginRefusesLocalPasswordOfCloud18Identity(t *testing.T) {
	repman, _, _ := newScopeTestManager(t)
	repman.Conf.Cloud18GitUser = "shared"
	body, _ := json.Marshal(map[string]string{"username": "shared", "password": "pw-of-c1"})
	req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	w := httptest.NewRecorder()
	repman.loginHandler(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("a local password under the Cloud18 identity must be refused 401, got %d", w.Code)
	}

	// the cluster level alone refuses it too: the clusters know the identity, the instance's
	// configuration does not name it here, and the stored password is still in memory
	repman2, c1, c2 := newScopeTestManager(t)
	c1.Conf.Cloud18GitUser, c2.Conf.Cloud18GitUser = "shared", "shared"
	if repman2.Conf.Cloud18GitUser != "" {
		t.Fatal("fixture: the instance's configuration must not name the identity")
	}
	req = httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	w = httptest.NewRecorder()
	repman2.loginHandler(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("the clusters' IsCloud18Identity must refuse the local password, got %d", w.Code)
	}
}
