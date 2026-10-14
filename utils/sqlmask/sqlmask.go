// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Package sqlmask masks the secrets a SQL text can carry before replication-manager
// stores, logs or serves it (#2025): a statement repman did not write comes back from
// the database verbatim (a replica's Last_SQL_Error, the SQL error log, the slow log, the
// processlist), with whatever password literal it held. No dependency: the dbhelper, the
// cluster code and the external plugins share it.
package sqlmask

import (
	"regexp"
	"strings"
)

// Mask replaces every secret literal of the text.
const Mask = "*****"

// a quoted literal, also cut short (an error message truncates the statement): up to its
// closing quote or the end of the text
const literal = `'(?:[^'\\]|\\.|'')*(?:'|$)|"(?:[^"\\]|\\.|"")*(?:"|$)`

var rules = []struct {
	re   *regexp.Regexp
	repl string
}{
	// CREATE/ALTER USER, GRANT: IDENTIFIED BY 'pw', IDENTIFIED BY PASSWORD 'hash',
	// IDENTIFIED WITH plugin BY|AS 'secret'
	{regexp.MustCompile(`(?i)(IDENTIFIED\s+(?:(?:VIA|WITH)\s+[\w'"` + "`" + `]+\s+)?(?:BY|AS|USING)\s+(?:PASSWORD\s+)?)(?:` + literal + `)`), "${1}'" + Mask + "'"},
	// PASSWORD('pw'), OLD_PASSWORD('pw'), and the USING PASSWORD('pw') of IDENTIFIED VIA
	{regexp.MustCompile(`(?i)(\bPASSWORD\s*\(\s*)(?:` + literal + `)`), "${1}'" + Mask + "'"},
	// SET PASSWORD [FOR user] = 'pw'
	{regexp.MustCompile(`(?i)(\bSET\s+PASSWORD\b[^=]*=\s*)(?:` + literal + `)`), "${1}'" + Mask + "'"},
	// CHANGE MASTER / CHANGE REPLICATION SOURCE: MASTER_PASSWORD='pw', SOURCE_PASSWORD='pw'
	{regexp.MustCompile(`(?i)(\b(?:MASTER|SOURCE)_PASSWORD\s*=\s*)(?:` + literal + `)`), "${1}'" + Mask + "'"},
	// connection strings and command lines: password='p w', --password="pw"
	{regexp.MustCompile(`(?i)(\b(?:password|pwd)=)(?:` + literal + `)`), "${1}'" + Mask + "'"},
	// and unquoted: password=pw, pwd=pw
	{regexp.MustCompile(`(?i)(\b(?:password|pwd)=)[^;,\s'"]+`), "${1}" + Mask},
}

// keywords: every rule needs one of them; a text without any is returned untouched,
// without running the expressions (slow log and processlist lines, the hot paths)
var keywords = []string{"identified", "password", "pwd"}

// Secrets returns the text with every password literal it carries replaced by Mask,
// the statement otherwise intact.
func Secrets(s string) string {
	if s == "" {
		return s
	}
	lower := strings.ToLower(s)
	found := false
	for _, k := range keywords {
		if strings.Contains(lower, k) {
			found = true
			break
		}
	}
	if !found {
		return s
	}
	for _, r := range rules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}
