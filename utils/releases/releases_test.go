package releases

import "testing"

func TestResolveTargets(t *testing.T) {
	tb, src, err := Load("")
	if err != nil || src != "built-in" {
		t.Fatalf("built-in table must load: %v %s", err, src)
	}
	cur, _ := ParseLine("mariadb:11.4")
	if cur != (Line{11, 4}) {
		t.Fatalf("parse image tag: %v", cur)
	}
	cases := []struct{ target, explicit, want string }{
		{"patch", "", "11.4"}, {"next-minor", "", "11.5"}, {"next-lts", "", "11.8"}, {"next-major", "", "12.0"}, {"version", "10.11.9", "10.11"},
	}
	for _, c := range cases {
		got, err := tb.Resolve("mariadb", cur, c.target, c.explicit)
		if err != nil || got.String() != c.want {
			t.Errorf("%s from 11.4: got %v %v, want %s", c.target, got, err, c.want)
		}
	}
	if l, _ := tb.Resolve("mariadb", Line{10, 11}, "next-lts", ""); l.String() != "11.4" {
		t.Errorf("next-lts after 10.11 must be 11.4, got %s", l)
	}
	if l, _ := tb.Resolve("mysql", Line{8, 0}, "next-lts", ""); l.String() != "8.4" {
		t.Errorf("mysql next-lts after 8.0 must be 8.4, got %s", l)
	}
	if _, err := tb.Resolve("mariadb", Line{12, 3}, "next-major", ""); err == nil {
		t.Errorf("no major after 12 must be an error")
	}
	if _, err := tb.Resolve("mariadb", cur, "sideways", ""); err == nil {
		t.Errorf("unknown target must be an error")
	}
	if !tb.IsLTS("mariadb", Line{11, 8}) || tb.IsLTS("mariadb", Line{11, 5}) {
		t.Errorf("LTS table: 11.8 yes, 11.5 no")
	}
	if FlavorOfImage("docker.io/library/mariadb:11.4") != "mariadb" || FlavorOfImage("percona/percona-server:8.4") != "percona" {
		t.Errorf("flavor from image")
	}
	if r, tag := SplitImage("registry.local:5000/db/mariadb:11.4"); r != "registry.local:5000/db/mariadb" || tag != "11.4" {
		t.Errorf("split image with a registry port: %s %s", r, tag)
	}
}
