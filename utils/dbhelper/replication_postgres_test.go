package dbhelper

import "testing"

func TestPostgresRepointConninfo(t *testing.T) {
	in := "user=postgres password=s3cret channel_binding=prefer host=pg1.c.svc port=5432 sslmode=prefer"
	want := "user=postgres password=s3cret channel_binding=prefer host=pg2.c.svc port=5433 sslmode=prefer"
	if got := postgresRepointConninfo(in, "pg2.c.svc", "5433"); got != want {
		t.Fatalf("got %q", got)
	}
	// a quoted value, and a conninfo that names no host: both end on the new primary
	if got := postgresRepointConninfo("host='old one' user=u", "pg2", "5432"); got != "host=pg2 user=u port=5432" {
		t.Fatalf("got %q", got)
	}
	// "host=" inside another keyword's value is not the host keyword
	if got := postgresRepointConninfo("application_name=xhost=1 host=a port=1", "b", "2"); got != "application_name=xhost=1 host=b port=2" {
		t.Fatalf("got %q", got)
	}
	if got := postgresConninfoPass.ReplaceAllString("ALTER SYSTEM SET primary_conninfo = 'user=u password=s3cret host=a'", "password=<hidden>"); got != "ALTER SYSTEM SET primary_conninfo = 'user=u password=<hidden> host=a'" {
		t.Fatalf("password not hidden: %q", got)
	}
}
