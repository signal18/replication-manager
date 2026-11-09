// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"testing"
)

// #2360: only ProxySQL's "admin can only connect locally" refusal sends a connection to external.
func TestIsAdminLocalOnly(t *testing.T) {
	if !isAdminLocalOnly(errors.New("Error 1040 (42000): User 'admin' can only connect locally")) {
		t.Fatal("the local-only refusal was not recognised")
	}
	for _, err := range []error{nil, errors.New("Error 1045 (28000): Access denied"), errors.New("dial tcp: no such host")} {
		if isAdminLocalOnly(err) {
			t.Fatalf("%v taken for the local-only refusal", err)
		}
	}
}

// #2360: a rotation gives the new password to every login the proxy has (the single login it
// wrote deleted external, repman's only way into a ProxySQL 2+ proxy), adds the configured user
// when it is not one of them, and falls back to the single login when the logins are unknown.
func TestAdminCredentials(t *testing.T) {
	cases := []struct {
		logins []string
		user   string
		want   string
	}{
		{[]string{"admin", "external"}, "admin", "admin:pw;external:pw"},
		{[]string{"admin", "external"}, "external", "admin:pw;external:pw"},
		{[]string{"admin", "external"}, "ops", "admin:pw;external:pw;ops:pw"},
		{[]string{"admin"}, "admin", "admin:pw"},
		{nil, "admin", "admin:pw"},
		{[]string{"radmin", "admin"}, "admin", "radmin:pw;admin:pw"},
	}
	for _, c := range cases {
		if got := adminCredentials(c.logins, c.user, "pw"); got != c.want {
			t.Errorf("adminCredentials(%v, %q) = %q, want %q", c.logins, c.user, got, c.want)
		}
	}
}
