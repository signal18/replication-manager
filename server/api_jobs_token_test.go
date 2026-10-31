package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// A secret-login token names the cluster's jobs principal and its server: IsValidClusterACL
// lets it reach the job endpoints of that server and refuses the rest (another server, the
// config editor's endpoints, a cluster action), as the token minted by secretLoginHandler. A
// `system` login's token names the sensor principal; a session naming bare `system` is refused.
func TestIsValidClusterACLJobsToken(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{}}
	repman.initKeys()
	cl := &cluster.Cluster{Name: "c1", Conf: &config.Config{}, APIUsers: map[string]cluster.APIUser{}, Grants: config.GetGrantType()}
	cl.Servers = []*cluster.ServerMonitor{{URL: "db1:3306", Host: "db1", Port: "3306", Id: "dbA"}, {URL: "db2:3306", Host: "db2", Port: "3306", Id: "dbB"}}
	sys := cluster.APIUser{User: "system", Password: "x", Roles: map[string]bool{}}
	cl.SetUserGrants(&sys, "db proxy "+config.GrantClusterResourceSensor)
	cl.APIUsers["system"] = sys
	cl.EnsureJobsPrincipal()

	sk, err := jwt.ParseRSAPrivateKeyFromPEM(signingKey)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(info map[string]interface{}) string {
		signer := jwt.New(jwt.SigningMethodRS256)
		claims := signer.Claims.(jwt.MapClaims)
		claims["iat"] = time.Now().Unix()
		claims["exp"] = time.Now().Add(15 * time.Minute).Unix()
		claims["CustomUserInfo"] = info
		tok, err := signer.SignedString(sk)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	jobs := sign(map[string]interface{}{"Name": cl.JobsPrincipalName(), "Role": "Member", "Password": "", "Server": "db1:3306"})
	sensor := sign(map[string]interface{}{"Name": cluster.SensorPrincipalName(), "Role": "Member", "Password": ""})
	bare := sign(map[string]interface{}{"Name": "system", "Role": "Member", "Password": ""})

	router := mux.NewRouter()
	var ok bool
	h := func(w http.ResponseWriter, r *http.Request) { ok, _ = repman.IsValidClusterACL(r, cl) }
	router.HandleFunc("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/{rest:.*}", h)
	router.HandleFunc("/api/clusters/{clusterName}/servers/{serverName}/{rest:.*}", h)
	router.HandleFunc("/api/clusters/{clusterName}/{rest:.*}", h)
	check := func(tok, url string) bool {
		ok = false
		r := httptest.NewRequest("POST", url, nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		router.ServeHTTP(httptest.NewRecorder(), r)
		return ok
	}
	for _, url := range []string{
		"/api/clusters/c1/servers/db1/3306/actions/job-state/backup/done",
		"/api/clusters/c1/servers/db1/3306/config-receiver",
		"/api/clusters/c1/servers/db1/3306/config-dummy-sender",
		"/api/clusters/c1/servers/dbA/config-receiver",
	} {
		if !check(jobs, url) {
			t.Errorf("a jobs token must reach %s", url)
		}
	}
	for _, url := range []string{
		"/api/clusters/c1/servers/db2/3306/actions/job-state/backup/done", // another server
		"/api/clusters/c1/servers/db2/3306/config-receiver",
		"/api/clusters/c1/servers/db1/3306/actions/backup-physical",
		"/api/clusters/c1/servers/db1/3306/config", // the config editor's
		"/api/clusters/c1/settings/actions/apply-dynamic-config",
		"/api/clusters/c1/settings/actions/add-db-tag/x",
		"/api/clusters/c1/secret-kv-list",
		"/api/clusters/c1/actions/switchover",
	} {
		if check(jobs, url) {
			t.Errorf("a jobs token must not reach %s", url)
		}
	}
	if check(sensor, "/api/clusters/c1/servers/db1/3306/actions/job-state/backup/done") || check(sensor, "/api/clusters/c1/servers/db1/3306/config-receiver") {
		t.Error("a sensor token must not reach the job endpoints")
	}
	if check(bare, "/api/clusters/c1/servers/db1/3306/actions/job-state/backup/done") {
		t.Error("a session naming bare system must be refused")
	}
}
