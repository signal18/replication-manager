package dbhelper

import "testing"

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
