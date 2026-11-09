// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"testing"
)

func adminLoginTestProxy(t *testing.T, user string, provisioned bool) *ProxySQLProxy {
	t.Helper()
	prx := &ProxySQLProxy{Proxy: Proxy{User: user, Datadir: t.TempDir()}}
	if provisioned {
		if err := os.WriteFile(prx.Datadir+"/@cookie_prov", nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return prx
}

// #2360: a proxy replication-manager provisioned is reached as external when the configured login
// is the default admin (ProxySQL 2+ refuses admin from another host); any other proxy, or another
// configured login, is used as configured.
func TestProxySQLAdminLogin(t *testing.T) {
	cases := []struct {
		user        string
		provisioned bool
		want        string
	}{
		{"admin", true, "external"},
		{"admin", false, "admin"},
		{"external", true, "external"},
		{"ops", true, "ops"},
		{"ops", false, "ops"},
	}
	for _, c := range cases {
		if got := adminLoginTestProxy(t, c.user, c.provisioned).adminLogin(); got != c.want {
			t.Errorf("user %q provisioned=%v: adminLogin() = %q, want %q", c.user, c.provisioned, got, c.want)
		}
	}
}

// #2360: a rotation on a provisioned proxy keeps both of its logins (the single login it wrote
// deleted external and locked replication-manager out of a ProxySQL 2+ proxy); another proxy
// gets the single login, as before.
func TestProxySQLAdminCredentials(t *testing.T) {
	cases := []struct {
		user        string
		provisioned bool
		want        string
	}{
		{"admin", true, "admin:pw;external:pw"},
		{"external", true, "admin:pw;external:pw"},
		{"ops", true, "admin:pw;external:pw;ops:pw"},
		{"admin", false, "admin:pw"},
		{"ops", false, "ops:pw"},
	}
	for _, c := range cases {
		if got := adminLoginTestProxy(t, "admin", c.provisioned).adminCredentials(c.user, "pw"); got != c.want {
			t.Errorf("user %q provisioned=%v: adminCredentials() = %q, want %q", c.user, c.provisioned, got, c.want)
		}
	}
}
