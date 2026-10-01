package releases

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// An override without the newer "lines" key keeps its own LTS list and takes the
// built-in lines, so next-minor / next-major still resolve.
func TestLoadOverrideWithoutLines(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lts-versions.json"), []byte(`{"updated":"2026-01-01","lts":{"mariadb":["10.11","11.4","11.8","12.3"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tb, src, err := Load(dir)
	if err != nil || src == "built-in" {
		t.Fatalf("override must load: %v %s", err, src)
	}
	if !tb.IsLTS("mariadb", Line{12, 3}) || tb.IsLTS("mariadb", Line{10, 6}) {
		t.Fatalf("the override's LTS list wins: %v", tb.LTS)
	}
	if l, err := tb.Resolve("mariadb", Line{11, 8}, "next-major", ""); err != nil || l.String() != "12.0" {
		t.Fatalf("built-in lines must fill the missing key: %v %v", l, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lts-versions.json"), []byte(`{broken`), 0o644); err != nil {
		t.Fatal(err)
	}
	if tb, src, err := Load(dir); err != nil || !strings.Contains(src, "unreadable") || len(tb.Lines["mariadb"]) == 0 {
		t.Fatalf("a broken override falls back to the built-in table: %v %s", err, src)
	}
}

func TestResolveTag(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"token":"t"}`)) })
	mux.HandleFunc("/v2/library/mariadb/manifests/", func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/v2/library/mariadb/manifests/") {
		case "latest":
			w.Header().Set("Docker-Content-Digest", "sha256:aaa")
		case "lts":
			w.Header().Set("Docker-Content-Digest", "sha256:bbb")
		case "11.8":
			w.Header().Set("Docker-Content-Digest", "sha256:ccc")
		case "orphan":
			w.Header().Set("Docker-Content-Digest", "sha256:ddd")
		default:
			w.WriteHeader(404)
			return
		}
	})
	mux.HandleFunc("/v2/repositories/library/mariadb/tags", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"next":"","results":[
		 {"name":"latest","digest":"sha256:aaa"},{"name":"13.0.2-noble","digest":"sha256:aaa"},{"name":"13.0","digest":"sha256:aaa"},{"name":"13.0.2","digest":"sha256:aaa"},
		 {"name":"lts","digest":"sha256:bbb"},{"name":"12.3.3-noble","digest":"sha256:bbb"},
		 {"name":"11.8","digest":"sha256:ccc"},{"name":"11.8.9","digest":"sha256:ccc"},
		 {"name":"orphan","digest":"sha256:ddd"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	registryAuthBase, registryBase, hubAPIBase = srv.URL, srv.URL, srv.URL
	defer func() {
		registryAuthBase, registryBase, hubAPIBase = "https://auth.docker.io", "https://registry-1.docker.io", "https://hub.docker.com"
	}()
	cases := map[string]string{"latest": "13.0.2", "lts": "12.3.3-noble", "11.8": "11.8.9", "orphan": "orphan@sha256:ddd", "11.4.5": "11.4.5"}
	for tag, want := range cases {
		got, _, checked, err := ResolveTag(context.Background(), "mariadb", tag)
		if err != nil || !checked || got != want {
			t.Errorf("%s: got %q checked=%v err=%v, want %q", tag, got, checked, err, want)
		}
	}
	if _, _, _, err := ResolveTag(context.Background(), "mariadb", "nope"); err == nil {
		t.Error("missing tag must fail")
	}
	if _, _, checked, _ := ResolveTag(context.Background(), "registry.example.com/x/mariadb", "latest"); checked {
		t.Error("another registry is not queried")
	}
	for tag, want := range map[string]bool{"11.8.9": true, "8.0.41-debian": true, "11.8": false, "latest": false, "lts": false, "11": false, "latest@sha256:aaa": true} {
		if IsExplicitTag(tag) != want {
			t.Errorf("IsExplicitTag(%s) != %v", tag, want)
		}
	}
}
