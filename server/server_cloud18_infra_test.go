package server

import (
	"strings"
	"testing"
)

func TestCloud18ClusterSpecNormalizeAndPlan(t *testing.T) {
	spec, err := normalizeSpec(Cloud18ClusterSpec{ClusterName: " Blab ", Apps: []string{"phpmyadmin", " "}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.ClusterName != "blab" || spec.DBImage != "mariadb:lts" || spec.DBCount != 2 || spec.Proxy != "haproxy" || len(spec.Apps) != 1 {
		t.Errorf("defaults wrong: %+v", spec)
	}
	hosts := plannedHosts(spec)
	got := []string{}
	for _, h := range hosts {
		got = append(got, h["type"]+":"+h["host"]+":"+h["port"])
	}
	// Short names only: the infrastructure appends its service domain itself.
	want := "database:db1:3306 database:db2:3306 haproxy:haproxy1:3306 app:phpmyadmin1:80"
	if strings.Join(got, " ") != want {
		t.Errorf("plan = %q, want %q", strings.Join(got, " "), want)
	}
	if hosts[3]["template"] != "phpmyadmin" {
		t.Errorf("app template must be kept: %v", hosts[3])
	}
	for _, bad := range []Cloud18ClusterSpec{
		{ClusterName: ""},
		{ClusterName: "a.b"},
		{ClusterName: "x", DBCount: 9},
		{ClusterName: "x", Proxy: "maxscale"},
	} {
		if _, err := normalizeSpec(bad); err == nil {
			t.Errorf("spec %+v must be refused", bad)
		}
	}
	if s, _ := normalizeSpec(Cloud18ClusterSpec{ClusterName: "x", Proxy: "none", DBCount: 1}); len(plannedHosts(s)) != 1 {
		t.Error("proxy none must add no proxy")
	}
}
