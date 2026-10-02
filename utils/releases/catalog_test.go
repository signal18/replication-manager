package releases

import "testing"

func testCatalog() *Catalog {
	tags := []string{"latest", "lts", "13.0.2", "13.0", "12.3.3", "12.3", "12.0.2", "11.8.9", "11.8.8", "11.8", "11.4.13", "11.4", "11.5.2", "10.11.15", "13.1.1-rc", "11.8.9-noble"}
	var t []Tag
	for _, n := range tags {
		t = append(t, Tag{Name: n})
	}
	return &Catalog{Table: Table{LTS: map[string][]string{"mariadb": {"10.11", "11.4", "11.8", "12.3"}}}, Tags: map[string][]Tag{"mariadb": t}, Source: "test"}
}

func TestCatalogMethods(t *testing.T) {
	c := testCatalog()
	l := func(s string) Line { x, _ := ParseLine(s); return x }
	check := func(name, got string, err error, want string) {
		t.Helper()
		if err != nil || got != want {
			t.Errorf("%s = %q (%v), want %q", name, got, err, want)
		}
	}
	v, err := c.GetLastMinor("mariadb", l("11.8"))
	check("GetLastMinor 11.8", v, err, "11.8.9")
	v, err = c.GetNextMinor("mariadb", l("11.4"))
	check("GetNextMinor 11.4", v, err, "11.5.2")
	v, err = c.GetNextMajor("mariadb", l("11.8"))
	check("GetNextMajor 11.8", v, err, "12.0.2")
	v, err = c.GetNextMajorLTS("mariadb", "mariadb", l("11.4"))
	check("GetNextMajorLTS 11.4", v, err, "11.8.9")
	v, err = c.GetLastMajorLTS("mariadb", "mariadb")
	check("GetLastMajorLTS", v, err, "12.3.3")
	if _, err := c.GetNextMinor("mariadb", l("11.8")); err == nil {
		t.Error("no line after 11.8 in the 11 series")
	}
	for tag, want := range map[string]string{"11.8.9": "11.8.9", "11.8": "11.8.9", "latest": "13.0.2", "lts": "12.3.3", "8.0.41-debian": "8.0.41-debian",
		"noble": "noble", "12.9": "12.9", "": ""} { // not found: the input comes back unchanged
		if v := c.Resolve("mariadb", "mariadb", tag); v != want {
			t.Errorf("Resolve %q = %q, want %q", tag, v, want)
		}
	}
	if v := c.Resolve("mysql", "mysql", "latest"); v != "latest" {
		t.Errorf("unknown repository: latest comes back unchanged, got %q", v)
	}
	// digests pin latest exactly when the delivered list carries them
	c.Tags["mariadb"] = append(c.Tags["mariadb"], Tag{Name: "latest", Digest: "sha256:x"}, Tag{Name: "12.3.3", Digest: "sha256:x"})
	if v := c.Resolve("mariadb", "mariadb", "latest"); v != "12.3.3" {
		t.Errorf("Resolve latest by digest = %q", v)
	}
	for target, want := range map[string]string{"patch": "11.8.9", "last-minor": "11.8.9", "next-major": "12.0.2", "next-lts": "12.3.3", "last-lts": "12.3.3"} {
		v, err := c.Target("mariadb", "mariadb", l("11.8"), target, "")
		check("Target "+target, v, err, want)
	}
	for target, want := range map[string]string{"previous-minor": "11.5.2", "previous-major": "11.8.9"} {
		cur := "12.0"
		if target == "previous-minor" {
			cur = "11.8"
		}
		v, err := c.Target("mariadb", "mariadb", l(cur), target, "")
		check("Target "+target, v, err, want)
	}
	v, err = c.Target("mariadb", "mariadb", l("11.4"), "version", "11.8")
	check("Target version 11.8", v, err, "11.8.9")
	v, err = c.Target("mariadb", "mariadb", l("11.4"), "version", "11.8.8")
	check("Target version 11.8.8", v, err, "11.8.8")
	v, err = c.Target("mariadb", "mariadb", l("11.4"), "version", "11.8.7")
	check("Target version absent from the list comes back unchanged", v, err, "11.8.7")
	v, err = c.Target("mariadb", "mariadb", l("11.4"), "version", "12.9")
	check("Target version line absent from the list comes back unchanged", v, err, "12.9")
	if _, err := c.Target("mariadb", "mariadb", l("11.4"), "sideways", ""); err == nil {
		t.Error("unknown target must fail")
	}
}
