package server

import "testing"

// #2196: addserver with the postgresql type declares host:port/postgres, as the PostgreSQL
// app declares itself; the other types keep host:port.
func TestSeededServerHost(t *testing.T) {
	cases := []struct{ typ, want string }{
		{"postgresql", "pg1:5432/postgres"},
		{"mariadb", "pg1:5432"},
		{"mysql", "pg1:5432"},
		{"", "pg1:5432"},
	}
	for _, c := range cases {
		if got := seededServerHost(c.typ, "pg1", "5432"); got != c.want {
			t.Fatalf("%q: got %q, want %q", c.typ, got, c.want)
		}
	}
}
