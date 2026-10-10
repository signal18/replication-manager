package dbhelper

import (
	"strings"
	"testing"
)

// The PostgreSQL statements of a linked application's database (#1908): double-quoted
// identifiers (quotes doubled), single-quoted literals (quotes doubled), ownership first.
func TestPostgresAppDbStatements(t *testing.T) {
	if got := PostgresCreateRoleSQL(`matter"most`, "p'ass"); got != `CREATE ROLE "matter""most" WITH LOGIN PASSWORD 'p''ass'` {
		t.Fatalf("create role: %s", got)
	}
	if got := PostgresSetRolePasswordSQL("mattermost", "x"); got != `ALTER ROLE "mattermost" WITH PASSWORD 'x'` {
		t.Fatalf("set password: %s", got)
	}
	if got := PostgresCreateDatabaseSQL("mattermost"); got != `CREATE DATABASE "mattermost"` {
		t.Fatalf("create database: %s", got)
	}
	g := PostgresGrantDatabaseSQL("mattermost", "mattermost")
	if len(g) != 2 || g[0] != `ALTER DATABASE "mattermost" OWNER TO "mattermost"` || g[1] != `GRANT ALL PRIVILEGES ON DATABASE "mattermost" TO "mattermost"` {
		t.Fatalf("grant: %v", g)
	}
}

// The logged statement never carries the password, in any spelling, and an empty
// password masks nothing (#1915).
func TestMaskAppPassword(t *testing.T) {
	if got := MaskAppPassword("ALTER USER 'u'@'%' IDENTIFIED BY 'it''s'", "it's"); strings.Contains(got, "it") {
		t.Fatalf("doubled-quote spelling not masked: %q", got)
	}
	if got := MaskAppPassword(`SET PASSWORD = 'a\'b'`, "a'b"); strings.Contains(got, "a\\'b") {
		t.Fatalf("backslash spelling not masked: %q", got)
	}
	if got := MaskAppPassword("CREATE USER x IDENTIFIED BY 'plain'", "plain"); strings.Contains(got, "plain") {
		t.Fatalf("raw spelling not masked: %q", got)
	}
	if got := MaskAppPassword("CREATE USER x", ""); got != "CREATE USER x" {
		t.Fatalf("empty password must mask nothing: %q", got)
	}
	if got := PostgresSetRolePasswordSQL("r", appPasswordMask); !strings.Contains(got, "'*.*'") {
		t.Fatalf("masked postgres statement: %q", got)
	}
}
