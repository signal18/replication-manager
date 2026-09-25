package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

func newTokenTestManager(t *testing.T) (*ReplicationManager, *cluster.Cluster) {
	t.Helper()
	dir := t.TempDir()
	cl := &cluster.Cluster{
		Name:     "c1",
		Conf:     &config.Config{},
		APIUsers: map[string]cluster.APIUser{},
		Grants:   config.GetGrantType(),
	}
	alice := cluster.APIUser{User: "alice", Password: "x", Roles: map[string]bool{}}
	cl.SetUserGrants(&alice, "db-show cluster-switchover grant-show token")
	cl.APIUsers["alice"] = alice
	bob := cluster.APIUser{User: "bob", Password: "y", Roles: map[string]bool{}}
	cl.SetUserGrants(&bob, "db-show token-create")
	cl.APIUsers["bob"] = bob
	repman := &ReplicationManager{
		Clusters:    map[string]*cluster.Cluster{cl.Name: cl},
		ClusterList: []string{cl.Name},
		Conf: &config.Config{
			WorkingDir:                     dir,
			SecretKey:                      []byte("0123456789abcdef"),
			APIUserTokens:                  true,
			APIUserTokensDefaultExpireDays: 120,
		},
		Logrus: log.New(),
	}
	return repman, cl
}

func bearerRequest(token, url string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, url, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.RemoteAddr = "10.0.0.9:1234"
	return r
}

func TestCreateAPITokenNarrowsToOwnerGrantsAndDefaultsExpiry(t *testing.T) {
	repman, _ := newTokenTestManager(t)
	tok, err := repman.createAPIToken("alice", APITokenForm{Label: "ci", Grants: "db-show"}, "10.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Token == "" || tok.ID == "" {
		t.Fatal("token string and id must be set")
	}
	if tok.Clusters[0] != "*" {
		t.Errorf("empty cluster list means global scope, got %v", tok.Clusters)
	}
	if d := time.Until(tok.ExpiresAt); d < 119*24*time.Hour || d > 121*24*time.Hour {
		t.Errorf("default expiry must be ~120 days, got %v", d)
	}
	// A grant the owner does not hold is refused.
	if _, err := repman.createAPIToken("alice", APITokenForm{Label: "bad", Grants: "cluster-failover"}, ""); err == nil {
		t.Error("embedding a grant the owner does not hold must fail")
	}
	// Unknown cluster / no account there.
	if _, err := repman.createAPIToken("alice", APITokenForm{Label: "bad", Clusters: []string{"nope"}}, ""); err == nil {
		t.Error("unknown cluster must fail")
	}
	// Never expires.
	never, err := repman.createAPIToken("alice", APITokenForm{Label: "forever", ExpireDays: -1}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !never.ExpiresAt.IsZero() {
		t.Error("expireDays -1 means no expiry")
	}
	if len(never.Grants) == 0 {
		t.Error("empty grants means all of the owner's grants, compacted")
	}
}

func TestParseAPITokenFromRequestAndACL(t *testing.T) {
	repman, cl := newTokenTestManager(t)
	tok, err := repman.createAPIToken("alice", APITokenForm{Label: "ci", Grants: "db-show", Clusters: []string{"c1"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	r := bearerRequest(tok.Token, "/api/clusters/c1/servers/db1/3306/variables")
	got, ok := repman.parseAPITokenFromRequest(r)
	if !ok || got.ID != tok.ID {
		t.Fatal("a freshly minted token must parse")
	}
	if repman.GetUserFromRequest(r) != "alice" {
		t.Error("GetUserFromRequest must return the owner for a token")
	}
	if _, err := repman.DecryptJWTPassword(r); err == nil {
		t.Error("a token carries no password")
	}
	// ACL: a public endpoint of c1 passes; global settings are out of scope for a
	// cluster-scoped token.
	if ok, user := repman.IsValidClusterACL(bearerRequest(tok.Token, "/api/clusters/c1"), cl); !ok || user != "alice" {
		t.Errorf("token must pass the cluster's public endpoint, ok=%v user=%q", ok, user)
	}
	if ok, _ := repman.IsValidClusterACL(bearerRequest(tok.Token, "/api/clusters/settings/actions/switch/x"), cl); ok {
		t.Error("a cluster-scoped token must not reach global settings")
	}
	// Direct grant checks see the narrowed view: switchover is held by alice but
	// not embedded.
	u, ok := repman.requestACLUser(bearerRequest(tok.Token, "/api/clusters/c1/actions/switchover"), cl)
	if !ok {
		t.Fatal("requestACLUser must resolve")
	}
	if u.Grants[config.GrantClusterSwitchover] {
		t.Error("cluster-switchover is not embedded: must be false")
	}
	if !u.Grants[config.GrantDBShowVariables] {
		t.Error("db-show-variables is embedded and held: must be true")
	}
	// Last used is stamped.
	if v := repman.listAPITokens("alice", "", true); len(v) != 1 || v[0].LastUsedAt.IsZero() {
		t.Error("last used must be stamped after a request")
	}
	// The RSA login path is untouched: a garbage bearer is not a token.
	if _, ok := repman.parseAPITokenFromRequest(bearerRequest("garbage", "/api/clusters/c1")); ok {
		t.Error("garbage must not parse")
	}
}

func TestAPITokenRevokeExpiryDisabledAndKeyRotation(t *testing.T) {
	repman, cl := newTokenTestManager(t)
	tok, err := repman.createAPIToken("alice", APITokenForm{Label: "ci"}, "")
	if err != nil {
		t.Fatal(err)
	}
	r := bearerRequest(tok.Token, "/api/clusters/c1")
	// Bob cannot revoke alice's token without token-manage.
	bobTok, _ := repman.createAPIToken("bob", APITokenForm{Label: "bob"}, "")
	if _, err := repman.revokeAPIToken(tok.ID, "bob", bearerRequest(bobTok.Token, "/api/tokens/"+tok.ID)); err == nil {
		t.Error("another user without token-manage must not revoke")
	}
	// Owner revokes: the token dies immediately, principal dropped.
	if _, err := repman.revokeAPIToken(tok.ID, "alice", r); err != nil {
		t.Fatal(err)
	}
	if _, ok := repman.parseAPITokenFromRequest(r); ok {
		t.Error("a revoked token must not parse")
	}
	if _, ok := cl.GetTokenPrincipal(tok.ID); ok {
		t.Error("revocation must drop the registered principal")
	}
	if _, err := repman.revokeAPIToken("missing", "alice", r); err != errAPITokenNotFound {
		t.Errorf("unknown id: want errAPITokenNotFound, got %v", err)
	}
	// Expired token (store record in the past) is refused.
	exp, _ := repman.createAPIToken("alice", APITokenForm{Label: "exp", ExpireDays: 1}, "")
	repman.apiTokens.Lock()
	repman.apiTokens.tokens[exp.ID].ExpiresAt = time.Now().Add(-time.Hour)
	repman.apiTokens.Unlock()
	if _, ok := repman.parseAPITokenFromRequest(bearerRequest(exp.Token, "/api/clusters/c1")); ok {
		t.Error("an expired record must not parse")
	}
	// Off-switch.
	live, _ := repman.createAPIToken("alice", APITokenForm{Label: "live"}, "")
	repman.Conf.APIUserTokens = false
	if _, ok := repman.parseAPITokenFromRequest(bearerRequest(live.Token, "/api/clusters/c1")); ok {
		t.Error("api-user-tokens=false must refuse every token")
	}
	repman.Conf.APIUserTokens = true
	// Owner deleted: token dies.
	delete(cl.APIUsers, "alice")
	if _, ok := repman.parseAPITokenFromRequest(bearerRequest(live.Token, "/api/clusters/c1")); ok {
		t.Error("a token whose owner no longer exists must not parse")
	}
	cl.APIUsers["alice"] = cluster.APIUser{User: "alice", Password: "x", Grants: map[string]bool{}, Roles: map[string]bool{}}
	// Key rotation: the signature no longer verifies and the store cannot be read.
	repman.Conf.SecretKey = []byte("fedcba9876543210")
	if _, ok := repman.parseAPITokenFromRequest(bearerRequest(live.Token, "/api/clusters/c1")); ok {
		t.Error("a rotated key must reject tokens signed with the old one")
	}
}

func TestAPITokenStoreEncryptedAndReloaded(t *testing.T) {
	repman, _ := newTokenTestManager(t)
	tok, err := repman.createAPIToken("alice", APITokenForm{Label: "persist", Grants: "db-show"}, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repman.Conf.WorkingDir, apiTokenStore)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" || containsAny(string(raw), tok.Token, "alice", "persist") {
		t.Error("the store on disk must be encrypted: no token, user or label in clear")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0600 {
		t.Errorf("store must be 0600, got %v", fi.Mode().Perm())
	}
	// A fresh manager on the same dir and key reloads the token and accepts it.
	fresh := &ReplicationManager{
		Clusters:    repman.Clusters,
		ClusterList: repman.ClusterList,
		Conf:        repman.Conf,
		Logrus:      log.New(),
	}
	got, ok := fresh.parseAPITokenFromRequest(bearerRequest(tok.Token, "/api/clusters/c1"))
	if !ok || got.Label != "persist" || got.Token != tok.Token {
		t.Fatal("token must reload from the encrypted store with its string intact")
	}
	// Listing for the owner includes the token string; admin listing per cluster does not.
	if v := fresh.listAPITokens("alice", "", true); len(v) != 1 || v[0].Token != tok.Token {
		t.Error("owner listing must include the token string")
	}
	if v := fresh.listAPITokens("", "c1", false); len(v) != 1 || v[0].Token != "" {
		t.Error("cluster listing must not include the token string")
	}
	// The git-sync ignore list must cover the store.
	// (checked by name only: server_git.go adds "api-tokens.json")
	if apiTokenStore != "api-tokens.json" {
		t.Error("store file name changed: update server_git.go ignore list and docs")
	}
}

func TestTokenURLInScope(t *testing.T) {
	cl := &cluster.Cluster{Name: "c1"}
	scoped := &APIToken{Clusters: []string{"c1"}}
	global := &APIToken{Clusters: []string{"*"}}
	cases := []struct {
		t    *APIToken
		url  string
		want bool
	}{
		{scoped, "/api/clusters/c1", true},
		{scoped, "/api/clusters/c1/servers", true},
		{scoped, "/api/clusters/c10/servers", false},
		{scoped, "/api/clusters/settings/actions/switch/x", false},
		{scoped, "/api/clusters", false},
		{global, "/api/clusters", true},
		{global, "/api/clusters/settings/actions/switch/x", true},
	}
	for _, c := range cases {
		if got := tokenURLInScope(c.t, cl, c.url); got != c.want {
			t.Errorf("scope %v url %s: got %v want %v", c.t.Clusters, c.url, got, c.want)
		}
	}
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if n != "" && contains(s, n) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestAPITokenReviewFixes(t *testing.T) {
	repman, cl := newTokenTestManager(t)
	// #1836 review 1: a token-authenticated GET /api/tokens never returns token strings.
	full, err := repman.createAPIToken("alice", APITokenForm{Label: "full"}, "")
	if err != nil {
		t.Fatal(err)
	}
	narrow, err := repman.createAPIToken("alice", APITokenForm{Label: "narrow", Grants: "db-show"}, "")
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := bearerRequest(narrow.Token, "/api/tokens")
	repman.handlerMuxAPITokens(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/tokens via token: %d %s", rr.Code, rr.Body.String())
	}
	if contains(rr.Body.String(), full.Token) || contains(rr.Body.String(), narrow.Token) {
		t.Error("a token-authenticated listing must not carry any token string")
	}
	rr = httptest.NewRecorder()
	req = bearerRequest(narrow.Token, "/api/tokens")
	req.Method = http.MethodPost
	repman.handlerMuxAPITokens(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("a token must not mint tokens, got %d", rr.Code)
	}

	// review 3: a cluster-scoped token whose owner holds cluster-grant can revoke
	// another user's token covering that cluster, from /api/tokens/{id}.
	admin := cluster.APIUser{User: "root", Password: "z", Roles: map[string]bool{}}
	cl.SetUserGrants(&admin, "token db-show")
	cl.APIUsers["root"] = admin
	adminTok, err := repman.createAPIToken("root", APITokenForm{Label: "ops", Clusters: []string{"c1"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	bobTok, _ := repman.createAPIToken("bob", APITokenForm{Label: "bob", Clusters: []string{"c1"}}, "")
	if _, err := repman.revokeAPIToken(bobTok.ID, "root", bearerRequest(adminTok.Token, "/api/tokens/"+bobTok.ID)); err != nil {
		t.Errorf("cluster-scoped token with token-manage must revoke a token on its cluster: %v", err)
	}

	// review 4: revoked records past retention are purged on save, fresh ones kept.
	repman.apiTokens.Lock()
	repman.apiTokens.tokens[bobTok.ID].RevokedAt = time.Now().Add(-apiTokenRetention - time.Hour)
	_ = repman.saveAPITokenStoreLocked()
	_, stillThere := repman.apiTokens.tokens[bobTok.ID]
	repman.apiTokens.Unlock()
	if stillThere {
		t.Error("a revoked record older than the retention must be purged")
	}
	if _, err := repman.revokeAPIToken(full.ID, "alice", bearerRequest(full.Token, "/api/tokens/"+full.ID)); err != nil {
		t.Fatal(err)
	}
	if v := repman.listAPITokens("alice", "", false); len(v) < 2 {
		t.Error("a freshly revoked record stays for the audit trail")
	}
}

func TestAPITokenStoreLoadFailureDoesNotWipe(t *testing.T) {
	// review 2: a corrupt/undecryptable store must not be marked loaded-empty and
	// then overwritten by the next save.
	repman, _ := newTokenTestManager(t)
	tok, err := repman.createAPIToken("alice", APITokenForm{Label: "keep"}, "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repman.Conf.WorkingDir, apiTokenStore)
	good, _ := os.ReadFile(path)
	// A fresh manager with the WRONG key cannot read the store: every operation
	// fails, nothing is written.
	bad := &ReplicationManager{
		Clusters:    repman.Clusters,
		ClusterList: repman.ClusterList,
		Conf: &config.Config{
			WorkingDir:    repman.Conf.WorkingDir,
			SecretKey:     []byte("wrongwrongwrong1"),
			APIUserTokens: true,
		},
		Logrus: log.New(),
	}
	if _, ok := bad.parseAPITokenFromRequest(bearerRequest(tok.Token, "/api/clusters/c1")); ok {
		t.Fatal("wrong key must not authenticate")
	}
	if _, err := bad.createAPIToken("alice", APITokenForm{Label: "new"}, ""); err == nil {
		t.Error("creating on an unreadable store must fail, not wipe it")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(good) {
		t.Fatal("the store on disk was rewritten after a failed load")
	}
	bad.apiTokens.Lock()
	loaded := bad.apiTokens.loaded
	bad.apiTokens.Unlock()
	if loaded {
		t.Error("a failed load must not mark the store loaded")
	}
	// The right key still reads everything.
	if _, ok := repman.parseAPITokenFromRequest(bearerRequest(tok.Token, "/api/clusters/c1")); !ok {
		t.Error("original manager must still authenticate")
	}
}

func TestAPITokenOnClustersListAndMiddleware(t *testing.T) {
	repman, cl := newTokenTestManager(t)
	global, err := repman.createAPIToken("alice", APITokenForm{Label: "global"}, "")
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := repman.createAPIToken("alice", APITokenForm{Label: "scoped", Clusters: []string{"c1"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	// /api/clusters lists the clusters the token may see: all for a global token,
	// none for a cluster-scoped one (the list endpoint is not under a cluster path).
	rr := httptest.NewRecorder()
	repman.handlerMuxClusters(rr, bearerRequest(global.Token, "/api/clusters"))
	if rr.Code != http.StatusOK || !contains(rr.Body.String(), cl.Name) {
		t.Errorf("global token must list clusters: %d %s", rr.Code, rr.Body.String()[:min(80, len(rr.Body.String()))])
	}
	rr = httptest.NewRecorder()
	repman.handlerMuxClusters(rr, bearerRequest(scoped.Token, "/api/clusters"))
	if rr.Code != http.StatusOK || contains(rr.Body.String(), cl.Name) {
		t.Errorf("cluster-scoped token must not see the global list: %d", rr.Code)
	}
	// Claims map names the owner and marks the auth type.
	claims, err := repman.GetJWTClaims(bearerRequest(global.Token, "/api/clusters"))
	if err != nil || claims["User"] != "alice" || claims["AuthType"] != "Token" {
		t.Errorf("claims for a token: %v %v", claims, err)
	}
	// Middleware: valid token passes, a revoked one gets a clean 401.
	passed := false
	repman.validateTokenMiddleware(httptest.NewRecorder(), bearerRequest(global.Token, "/api/clusters"), func(http.ResponseWriter, *http.Request) { passed = true })
	if !passed {
		t.Error("a valid token must pass the middleware")
	}
	if _, err := repman.revokeAPIToken(global.ID, "alice", bearerRequest(global.Token, "/api/tokens/"+global.ID)); err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	passed = false
	repman.validateTokenMiddleware(rr, bearerRequest(global.Token, "/api/clusters"), func(http.ResponseWriter, *http.Request) { passed = true })
	if passed || rr.Code != http.StatusUnauthorized || !contains(rr.Body.String(), "revoked") {
		t.Errorf("a revoked token must get a clean 401: passed=%v code=%d body=%q", passed, rr.Code, rr.Body.String())
	}
	// Global identity for the aggregate endpoints: scoped token refused, global accepted.
	if _, ok := repman.resolveGlobalRequestIdentity(bearerRequest(scoped.Token, "/api/clusters/jobs")); ok {
		t.Error("a cluster-scoped token must not resolve a global identity")
	}
	fresh, _ := repman.createAPIToken("alice", APITokenForm{Label: "global2"}, "")
	id, ok := repman.resolveGlobalRequestIdentity(bearerRequest(fresh.Token, "/api/clusters/jobs"))
	if !ok || id.AuthMethod != "token" || !cluster.IsTokenPrincipal(id.Username) {
		t.Errorf("global token identity: %+v ok=%v", id, ok)
	}
	if !cl.IsValidACL(id.Username, "", "/api/clusters/c1", "token") {
		t.Error("the global identity principal must be registered on the cluster")
	}
}

func TestAPITokenGrantsAndSystemAccount(t *testing.T) {
	repman, cl := newTokenTestManager(t)
	// No token-create: cannot issue.
	carol := cluster.APIUser{User: "carol", Password: "c", Roles: map[string]bool{}}
	cl.SetUserGrants(&carol, "db-show")
	cl.APIUsers["carol"] = carol
	if _, err := repman.createAPIToken("carol", APITokenForm{Label: "x"}, ""); err == nil {
		t.Error("a user without token-create must not issue tokens")
	}
	// The system service account never issues tokens, even with the grant in the map.
	sys := cluster.APIUser{User: "system", Password: "k", Roles: map[string]bool{}}
	cl.SetUserGrants(&sys, "db proxy token")
	cl.APIUsers["system"] = sys
	if _, err := repman.createAPIToken("system", APITokenForm{Label: "x"}, ""); err == nil {
		t.Error("system must never issue tokens")
	}
	// The grants are stripped from system on every user load.
	cl.Conf.Secrets = map[string]config.Secret{"api-credentials": {Value: "system:k"}, "api-credentials-external": {Value: ""}}
	cl.Conf.APIUsersACLAllow = "system:db proxy token"
	if err := cl.LoadAPIUsers(); err != nil {
		t.Fatal(err)
	}
	if u := cl.APIUsers["system"]; u.Grants[config.GrantTokenCreate] || u.Grants[config.GrantTokenManage] {
		t.Error("token grants must be stripped from system on load")
	}
	if !cl.APIUsers["system"].Grants[config.GrantDBShowVariables] {
		t.Error("other grants of system must be untouched")
	}
	// token-manage lists other users' tokens on a cluster; token-create alone does not.
	cl.APIUsers["alice"] = func() cluster.APIUser {
		a := cluster.APIUser{User: "alice", Password: "x", Roles: map[string]bool{}}
		cl.SetUserGrants(&a, "db-show token")
		return a
	}()
	cl.APIUsers["bob"] = func() cluster.APIUser {
		b := cluster.APIUser{User: "bob", Password: "y", Roles: map[string]bool{}}
		cl.SetUserGrants(&b, "db-show token-create")
		return b
	}()
	if !cl.IsURLPassACL("alice", "/api/clusters/c1/tokens", false) {
		t.Error("token-manage must open the cluster tokens listing")
	}
	if cl.IsURLPassACL("bob", "/api/clusters/c1/tokens", false) {
		t.Error("token-create alone must not open the cluster tokens listing")
	}
}

func TestMCPAuthenticateAndAuthorize(t *testing.T) {
	repman, cl := newTokenTestManager(t)
	tok, err := repman.createAPIToken("alice", APITokenForm{Label: "mcp", Grants: "db-show", Clusters: []string{"c1"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := repman.AuthenticateMCP(bearerRequest(tok.Token, "/sse"))
	if err != nil || p == nil || p.User != "alice" || p.AuthMethod != "token" || p.TokenID != tok.ID {
		t.Fatalf("token must authenticate: %+v %v", p, err)
	}
	// Public cluster endpoint and a db-show read pass; an action does not; another cluster is out of scope.
	if !repman.AuthorizeMCP(p, "c1", "/api/clusters/c1") {
		t.Error("cluster visibility must pass")
	}
	if !repman.AuthorizeMCP(p, "c1", "/api/clusters/c1/servers/db1/variables") {
		t.Error("db-show read must pass")
	}
	if repman.AuthorizeMCP(p, "c1", "/api/clusters/c1/actions/switchover") {
		t.Error("switchover is not embedded: must be refused")
	}
	if repman.AuthorizeMCP(p, "c2", "/api/clusters/c2") {
		t.Error("unknown / out-of-scope cluster must be refused")
	}
	// Garbage bearer: no principal.
	if p, err := repman.AuthenticateMCP(bearerRequest("garbage", "/sse")); err == nil || p != nil {
		t.Error("garbage bearer must not authenticate")
	}
	// Revoked token: refused with a clear error.
	if _, err := repman.revokeAPIToken(tok.ID, "alice", bearerRequest(tok.Token, "/api/tokens/"+tok.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := repman.AuthenticateMCP(bearerRequest(tok.Token, "/sse")); err == nil || !contains(err.Error(), "revoked") {
		t.Errorf("revoked token must be refused clearly, got %v", err)
	}
	_ = cl
}
