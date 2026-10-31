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
	return errors.New("a " + strings.ToLower(authType) + " login does not open a terminal here (terminal-session-auth: " + strings.Join(allowed, ",") + ")")
}
