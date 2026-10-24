package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
)

// The app token (#2158): read-only, scoped to its cluster, refused out of scope on the
// topology reads, revocable by the owner path.
func TestAppAPITokenScopeAndRevoke(t *testing.T) {
	repman, cl := newTokenTestManager(t)
	admin := cluster.APIUser{User: appAPITokenOwner, Password: "z", Roles: map[string]bool{}}
	cl.SetUserGrants(&admin, "cluster-show-routes token-create")
	cl.APIUsers[appAPITokenOwner] = admin
	repman.Conf.AppAPITokenExpireDays = 1825

	id, token, expires, err := repman.issueAppAPIToken("c1", "status")
	if err != nil {
		t.Fatal(err)
	}
	if id == "" || token == "" || expires.IsZero() {
		t.Fatalf("id %q expires %v", id, expires)
	}

	in := httptest.NewRecorder()
	if repman.refuseAPITokenOutOfScope(in, bearerRequest(token, "/api/clusters/c1/topology/apps"), "c1") {
		t.Fatal("the app's own cluster is in scope")
	}
	out := httptest.NewRecorder()
	if !repman.refuseAPITokenOutOfScope(out, bearerRequest(token, "/api/clusters/c2/topology/apps"), "c2") || out.Code != http.StatusForbidden {
		t.Fatalf("another cluster: refused 403, got %d", out.Code)
	}
	if repman.refuseAPITokenOutOfScope(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/clusters/c2/topology/apps", nil), "c2") {
		t.Fatal("a request without an API token is left to the login checks")
	}

	if err := repman.revokeAppAPIToken(id); err != nil {
		t.Fatal(err)
	}
	if _, ok := repman.parseAPITokenFromRequest(bearerRequest(token, "/api/clusters/c1/topology/apps")); ok {
		t.Fatal("a revoked token no longer parses")
	}
	if err := repman.revokeAppAPIToken(id); err != nil {
		t.Fatal("revoking a token already gone is not an error")
	}
}

func TestAppAPITokenLifetimeAndLabel(t *testing.T) {
	repman, cl := newTokenTestManager(t)
	admin := cluster.APIUser{User: appAPITokenOwner, Password: "z", Roles: map[string]bool{}}
	cl.SetUserGrants(&admin, "cluster-show-routes token-create")
	cl.APIUsers[appAPITokenOwner] = admin

	repman.Conf.AppAPITokenExpireDays = -3
	if _, _, _, err := repman.issueAppAPIToken("c1", "status"); err == nil {
		t.Fatal("a negative lifetime is refused")
	}

	repman.Conf.AppAPITokenExpireDays = 0
	_, _, expires, err := repman.issueAppAPIToken("c1", strings.Repeat("é", 40))
	if err != nil {
		t.Fatal(err)
	}
	if !expires.IsZero() {
		t.Fatalf("0 = never expires, got %v", expires)
	}
	for _, tok := range repman.listAPITokens(appAPITokenOwner, "", false) {
		if len(tok.Label) > 64 || !utf8.ValidString(tok.Label) {
			t.Fatalf("label %q: at most 64 bytes, valid UTF-8", tok.Label)
		}
	}
}

// The status app's SSO list: the app token reads the emails of the users holding
// cluster-show-routes, nothing else; a token without the grant, or for another cluster, is refused.
func TestRouteViewers(t *testing.T) {
	repman, cl := newTokenTestManager(t)
	admin := cluster.APIUser{User: appAPITokenOwner, Password: "z", Roles: map[string]bool{}}
	cl.SetUserGrants(&admin, "cluster-show-routes token-create")
	cl.APIUsers[appAPITokenOwner] = admin
	viewer := cluster.APIUser{User: "Viewer@x.io", Roles: map[string]bool{}}
	cl.SetUserGrants(&viewer, "cluster-show-routes")
	cl.APIUsers["Viewer@x.io"] = viewer
	other := cluster.APIUser{User: "dba@x.io", Roles: map[string]bool{}}
	cl.SetUserGrants(&other, "db-show")
	cl.APIUsers["dba@x.io"] = other
	repman.Conf.AppAPITokenExpireDays = 1825

	get := func(token, clusterName string) *httptest.ResponseRecorder {
		r := bearerRequest(token, "/api/clusters/"+clusterName+"/users/route-viewers")
		r = mux.SetURLVars(r, map[string]string{"clusterName": clusterName})
		rr := httptest.NewRecorder()
		repman.handlerMuxRouteViewers(rr, r)
		return rr
	}

	_, token, _, err := repman.issueAppAPIToken("c1", "status")
	if err != nil {
		t.Fatal(err)
	}
	rr := get(token, "c1")
	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != `["viewer@x.io"]` {
		t.Fatalf("app token: %d %s", rr.Code, rr.Body.String())
	}
	if rr := get(token, "c2"); rr.Code != http.StatusForbidden && rr.Code != http.StatusNotFound {
		t.Fatalf("another cluster: %d", rr.Code)
	}

	noGrant, err := repman.createAPIToken("alice", APITokenForm{Label: "ci", Grants: "db-show", Clusters: []string{"c1"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if rr := get(noGrant.Token, "c1"); rr.Code != http.StatusForbidden {
		t.Fatalf("token without cluster-show-routes: %d %s", rr.Code, rr.Body.String())
	}
}
