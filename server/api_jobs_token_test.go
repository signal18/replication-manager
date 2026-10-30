package server

import (
	"net/http/httptest"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// A secret-login token names the cluster's jobs principal: IsValidClusterACL lets it reach the
// job endpoints of its cluster and refuses the rest, as the token minted by secretLoginHandler.
func TestIsValidClusterACLJobsToken(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{}}
	repman.initKeys()
	cl := &cluster.Cluster{Name: "c1", Conf: &config.Config{}, APIUsers: map[string]cluster.APIUser{}, Grants: config.GetGrantType()}
	sys := cluster.APIUser{User: "system", Password: "x", Roles: map[string]bool{}}
	cl.SetUserGrants(&sys, "db proxy "+config.GrantClusterResourceSensor)
	cl.APIUsers["system"] = sys
	cl.EnsureJobsPrincipal()

	signer := jwt.New(jwt.SigningMethodRS256)
	claims := signer.Claims.(jwt.MapClaims)
	claims["iat"] = time.Now().Unix()
	claims["exp"] = time.Now().Add(15 * time.Minute).Unix()
	claims["CustomUserInfo"] = map[string]interface{}{"Name": cl.JobsPrincipalName(), "Role": "Member", "Password": ""}
	sk, err := jwt.ParseRSAPrivateKeyFromPEM(signingKey)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := signer.SignedString(sk)
	if err != nil {
		t.Fatal(err)
	}
	check := func(url string) bool {
		r := httptest.NewRequest("POST", url, nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		ok, _ := repman.IsValidClusterACL(r, cl)
		return ok
	}
	if !check("/api/clusters/c1/servers/db1/3306/actions/job-state/backup/done") {
		t.Fatal("a jobs token must reach the job state endpoint")
	}
	if check("/api/clusters/c1/servers/db1/3306/actions/backup-physical") {
		t.Fatal("a jobs token must not start a backup")
	}
	if check("/api/clusters/c1/actions/switchover") {
		t.Fatal("a jobs token must not switch over")
	}
}
