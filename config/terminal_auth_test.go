// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "testing"

// #2262: by default only a local login opens a terminal; a Cloud18 login only when allowed and
// while cloud18-login-acr asks a second factor; an API token or another SSO never.
func TestTerminalAuthAllows(t *testing.T) {
	for _, c := range []struct {
		auth, acr, authType string
		cloud18, ok         bool
	}{
		{"", "", "Local", false, true},
		{"", "", "SSO", true, false},
		{"", "mfa", "SSO", true, false},
		{"", "", "Token", false, false},
		{"local", "", "Local", false, true},
		{"cloud18-mfa", "", "Local", false, false},
		{"local,cloud18-mfa", "", "SSO", true, false},
		{"local,cloud18-mfa", "mfa", "SSO", true, true},
		{"local,cloud18-mfa", "mfa", "SSO", false, false},
		{"local,cloud18-mfa", "mfa", "Token", false, false},
	} {
		conf := &Config{TerminalSessionAuth: c.auth, Cloud18LoginACR: c.acr}
		if err := conf.TerminalAuthAllows(c.authType, c.cloud18); (err == nil) != c.ok {
			t.Errorf("auth %q acr %q %s cloud18=%v: allowed=%v, want %v (%v)", c.auth, c.acr, c.authType, c.cloud18, err == nil, c.ok, err)
		}
	}
}
