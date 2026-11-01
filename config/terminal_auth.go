// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"errors"
	"slices"
	"strings"
)

// A terminal is a shell (on the repman host, or in a database, proxy or app container): only the
// logins terminal-session-auth names open one (#2262).
const (
	// ConstTerminalAuthLocal: an account of this repman (api-credentials), logged in here.
	ConstTerminalAuthLocal = "local"
	// ConstTerminalAuthCloud18MFA: a Cloud18 login, only while cloud18-login-acr asks a second
	// factor (every Cloud18 login of this repman then proved it).
	ConstTerminalAuthCloud18MFA = "cloud18-mfa"
)

// The logins the MCP accepts (mcp-server-auth, #2262).
const (
	ConstMCPAuthAuto    = "auto"    // cloud18 on an instance registered to Cloud18, any otherwise
	ConstMCPAuthCloud18 = "cloud18" // a Cloud18 login or an API token, never a local password login
	ConstMCPAuthAny     = "any"     // every login of the API
)

// MCPAuthAllows says whether the MCP takes a login: authMethod is the MCP principal's (password,
// oidc, token), cloud18 whether its SSO login is a Cloud18 one. Nil when allowed, else why.
func (conf *Config) MCPAuthAllows(authMethod string, cloud18 bool) error {
	// an API token (scoped, revocable, its grants narrowed) stays valid: only the session of a
	// local password login is refused
	if conf.Cloud18LoginOnly() && authMethod != "token" && !(authMethod == "oidc" && cloud18) {
		return errors.New("this MCP accepts Cloud18 logins and API tokens only (mcp-server-auth): sign in with Cloud18 or use an API token")
	}
	return nil
}

// Cloud18LoginOnly says whether this repman takes Cloud18 logins only (mcp-server-auth resolves
// to cloud18: auto on an instance registered to Cloud18). Its MCP refuses local password
// sessions (MCPAuthAllows); the login follows it only where Sign in with Cloud18 is on
// (Cloud18LoginRequired). API tokens stay valid. Recovery: mcp-server-auth = "any" in config.toml.
func (conf *Config) Cloud18LoginOnly() bool {
	switch strings.ToLower(strings.TrimSpace(conf.MCPServerAuth)) {
	case "", ConstMCPAuthAuto:
		return conf.Cloud18
	case ConstMCPAuthAny:
		return false
	default:
		return true // cloud18, or an unknown value (a typo): fails closed, never open
	}
}

// Cloud18LoginRequired says whether the login takes Cloud18 logins only (#2262): Cloud18LoginOnly
// on a repman that offers Sign in with Cloud18 (cloud18-sso-login, a public address: Cloud18SSO).
// The login page goes straight to Cloud18, the user and password form is the admin zone's
// (/login?admin), and /api/login refuses local accounts other than admin accounts and `system`.
// A repman without Sign in with Cloud18 (an on-premise one, registered or not) keeps its local
// accounts and their ACL.
func (conf *Config) Cloud18LoginRequired() bool {
	if _, err := conf.Cloud18SSO(); err != nil {
		return false
	}
	return conf.Cloud18LoginOnly()
}

// TerminalAuthAllows says whether a session may open a terminal: authType is the session's
// (Local, SSO, Token), cloud18 whether its SSO login is a Cloud18 one. Nil when allowed, else why.
func (conf *Config) TerminalAuthAllows(authType string, cloud18 bool) error {
	allowed := strings.FieldsFunc(conf.TerminalSessionAuth, func(r rune) bool { return r == ',' || r == ' ' })
	if len(allowed) == 0 {
		allowed = []string{ConstTerminalAuthLocal}
	}
	switch {
	case authType == "Local" && slices.Contains(allowed, ConstTerminalAuthLocal):
		return nil
	case authType == "SSO" && cloud18 && slices.Contains(allowed, ConstTerminalAuthCloud18MFA):
		if len(conf.Cloud18LoginACRValues()) == 0 {
			return errors.New("a Cloud18 login opens a terminal only with a second factor (cloud18-login-acr is not set)")
		}
		return nil
	}
	kind := map[string]string{"Local": "local", "SSO": "single sign-on", "Token": "API token"}[authType]
	if kind == "" {
		kind = "unknown"
	}
	return errors.New("this login (" + kind + ") does not open a terminal here (terminal-session-auth: " + strings.Join(allowed, ",") + ")")
}
