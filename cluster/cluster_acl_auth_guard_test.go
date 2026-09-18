// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

func newAuthGuardCluster() *Cluster {
	return &Cluster{
		Name:     "test",
		Conf:     &config.Config{Verbose: false, Cloud18GitUser: "owner"},
		APIUsers: make(map[string]APIUser),
	}
}

// TestIsValidACL_AuthGuards covers the password/SSO auth model:
// password present => local-only; no password => SSO-only. /api/monitor is a
// public endpoint so IsURLPassACL is always true and only the auth guards decide.
func TestIsValidACL_AuthGuards(t *testing.T) {
	const pubURL = "/api/monitor"

	t.Run("local account: correct password authenticates", func(t *testing.T) {
		c := newAuthGuardCluster()
		c.APIUsers["dba"] = APIUser{User: "dba", Password: "secret"}
		if !c.IsValidACL("dba", "secret", pubURL, "password") {
			t.Fatal("local account with correct password must authenticate")
		}
	})

	t.Run("local account: wrong password rejected", func(t *testing.T) {
		c := newAuthGuardCluster()
		c.APIUsers["dba"] = APIUser{User: "dba", Password: "secret"}
		if c.IsValidACL("dba", "nope", pubURL, "password") {
			t.Fatal("wrong password must be rejected")
		}
	})

	t.Run("local account: empty submitted password rejected", func(t *testing.T) {
		c := newAuthGuardCluster()
		c.APIUsers["dba"] = APIUser{User: "dba", Password: "secret"}
		if c.IsValidACL("dba", "", pubURL, "password") {
			t.Fatal("empty submitted password must be rejected")
		}
	})

	// THE bypass: a passwordless (SSO-provisioned) account must NOT authenticate
	// via the local password path with a blank password.
	t.Run("passwordless account: blank-password local auth rejected", func(t *testing.T) {
		c := newAuthGuardCluster()
		c.APIUsers["peer@corp.com"] = APIUser{User: "peer@corp.com", Password: ""}
		if c.IsValidACL("peer@corp.com", "", pubURL, "password") {
			t.Fatal("blank-password bypass: passwordless account authenticated via local password path")
		}
	})

	t.Run("passwordless account: SSO authenticates", func(t *testing.T) {
		c := newAuthGuardCluster()
		c.APIUsers["peer@corp.com"] = APIUser{User: "peer@corp.com", Password: ""}
		if !c.IsValidACL("peer@corp.com", "", pubURL, "oidc") {
			t.Fatal("passwordless SSO account must authenticate via oidc")
		}
	})

	// Collision protection: a password-protected local account must NOT be
	// authenticated via SSO (a same-named GitLab identity can't ride its ACL).
	t.Run("local account: SSO refused (collision protection)", func(t *testing.T) {
		c := newAuthGuardCluster()
		c.APIUsers["dba"] = APIUser{User: "dba", Password: "secret"}
		if c.IsValidACL("dba", "", pubURL, "oidc") {
			t.Fatal("password-protected local account must not authenticate via SSO")
		}
	})

	// Owner exemption: the registering Cloud18GitUser carries a GitLab password
	// yet must still be allowed to SSO.
	t.Run("owner: SSO allowed despite password", func(t *testing.T) {
		c := newAuthGuardCluster()
		c.APIUsers["owner"] = APIUser{User: "owner", Password: "gitlabpass"}
		if !c.IsValidACL("owner", "", pubURL, "oidc") {
			t.Fatal("Cloud18GitUser must be allowed to SSO despite carrying a password")
		}
	})

	t.Run("unknown user rejected", func(t *testing.T) {
		c := newAuthGuardCluster()
		if c.IsValidACL("ghost", "x", pubURL, "password") {
			t.Fatal("unknown user must be rejected")
		}
	})
}

// TestIsLocalOnlyAccount checks the single-source-of-truth helper shared by
// IsValidACL's OIDC branch and the OIDC-callback collision guard.
func TestIsLocalOnlyAccount(t *testing.T) {
	c := newAuthGuardCluster() // Cloud18GitUser = "owner"
	c.APIUsers["dba"] = APIUser{User: "dba", Password: "secret"}       // local
	c.APIUsers["peer@corp.com"] = APIUser{User: "peer@corp.com"}       // SSO (no password)
	c.APIUsers["owner"] = APIUser{User: "owner", Password: "gitlabpw"} // owner, exempt

	cases := map[string]bool{
		"dba":           true,  // password-protected -> local only
		"peer@corp.com": false, // passwordless -> SSO
		"owner":         false, // Cloud18GitUser exempt despite password
		"ghost":         false, // unknown
	}
	for user, want := range cases {
		if got := c.IsLocalOnlyAccount(user); got != want {
			t.Errorf("IsLocalOnlyAccount(%q) = %v, want %v", user, got, want)
		}
	}
}
