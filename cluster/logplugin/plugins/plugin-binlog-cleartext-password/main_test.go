package main

import "testing"

// TestCleartextPasswordRe_Matches verifies that the regex captures passwords
// from the SQL forms it is designed to detect.
func TestCleartextPasswordRe_Matches(t *testing.T) {
	matches := []struct {
		sql  string
		want string // expected captured group (the password)
	}{
		{`IDENTIFIED BY 'secret'`, "secret"},
		{`identified by "mypassword"`, "mypassword"},
		{`IDENTIFIED BY 'p@$$w0rd!'`, "p@$$w0rd!"},
		{`SET PASSWORD = 'newpass'`, "newpass"},
		{`SET PASSWORD FOR 'user'@'host' = 'topsecret'`, "topsecret"},
		{`set password for 'u'@'%' = "pass123"`, "pass123"},
		// Leading/trailing whitespace around =
		{"SET PASSWORD  =  'spaces'", "spaces"},
	}
	for _, tc := range matches {
		m := cleartextPasswordRe.FindStringSubmatch(tc.sql)
		if m == nil {
			t.Errorf("regex did not match %q", tc.sql)
			continue
		}
		if m[1] != tc.want {
			t.Errorf("regex on %q: captured %q, want %q", tc.sql, m[1], tc.want)
		}
	}
}

// TestCleartextPasswordRe_NoMatch verifies that unrelated SQL is not flagged.
func TestCleartextPasswordRe_NoMatch(t *testing.T) {
	noMatch := []string{
		"SELECT * FROM users",
		"INSERT INTO t VALUES (1, 'hello')",
		"ALTER USER 'u'@'h' ACCOUNT LOCK",
		// IDENTIFIED BY without a quoted value
		"IDENTIFIED BY RANDOM PASSWORD",
	}
	for _, sql := range noMatch {
		if cleartextPasswordRe.MatchString(sql) {
			t.Errorf("regex unexpectedly matched %q", sql)
		}
	}
}
