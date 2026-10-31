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

// #2262: on an instance registered to Cloud18 the MCP takes a Cloud18 login only by default; any
// login elsewhere, or when asked; cloud18 is a setting of its own too.
func TestMCPAuthAllows(t *testing.T) {
	for _, c := range []struct {
		mode       string
		registered bool
		method     string
		cloud18    bool
		ok         bool
	}{
		{"", true, "oidc", true, true},
		{"", true, "password", false, false},
		{"", true, "token", false, false},
		{"", true, "oidc", false, false},
		{"", false, "password", false, true},
		{"auto", false, "token", false, true},
		{"any", true, "password", false, true},
		{"cloud18", false, "password", false, false},
		{"cloud18", false, "oidc", true, true},
	} {
		conf := &Config{MCPServerAuth: c.mode, Cloud18: c.registered}
		if err := conf.MCPAuthAllows(c.method, c.cloud18); (err == nil) != c.ok {
			t.Errorf("mode %q registered %v %s cloud18=%v: allowed=%v, want %v (%v)", c.mode, c.registered, c.method, c.cloud18, err == nil, c.ok, err)
		}
	}
}
