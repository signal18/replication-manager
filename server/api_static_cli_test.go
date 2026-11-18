package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/config"
)

// The CLI download is served from disk, registered before the embedded /static/ prefix as the
// embed-mode routers do: the embedded dashboard never holds the binary.
func TestStaticCliServedFromDiskBeforeEmbeddedStatic(t *testing.T) {
	dir := t.TempDir()
	cli := filepath.Join(dir, "replication-manager-cli")
	body := []byte("#!/bin/sh\necho cli\n")
	if err := os.WriteFile(cli, body, 0755); err != nil {
		t.Fatal(err)
	}
	repman := &ReplicationManager{Conf: &config.Config{ReplicationManagerCliPath: cli}, Fullversion: "v-test"}
	embeddedHasCli := false
	embedded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if embeddedHasCli && r.URL.Path == staticCliPath {
			w.Write([]byte("embedded"))
			return
		}
		http.NotFound(w, r)
	})
	router := mux.NewRouter()
	router.PathPrefix("/static/configurator/bin/").Handler(repman.handlerStatic(repman.handlerStaticCli(embedded)))
	router.PathPrefix("/static/").Handler(repman.handlerStatic(embedded))

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	rec := get(staticCliPath)
	if rec.Code != http.StatusOK || rec.Body.String() != string(body) {
		t.Fatalf("CLI: code %d body %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Length") != strconv.Itoa(len(body)) || rec.Header().Get("Etag") != "v-test" {
		t.Fatalf("CLI headers: length %q etag %q", rec.Header().Get("Content-Length"), rec.Header().Get("Etag"))
	}
	if rec := get("/static/configurator/bin/other"); rec.Code != http.StatusNotFound {
		t.Fatalf("other file under bin/: code %d, want 404", rec.Code)
	}
	if rec := get("/static/configurator/opensvc/bootstrap"); rec.Code != http.StatusNotFound {
		t.Fatalf("other static path must stay on the embedded handler, got %d", rec.Code)
	}

	// not on disk: the embedded copy when the build has one (source build), else 404 (image)
	repman.Conf.ReplicationManagerCliPath = filepath.Join(dir, "missing")
	if rec := get(staticCliPath); rec.Code != http.StatusNotFound {
		t.Fatalf("missing CLI, none embedded: code %d, want 404", rec.Code)
	}
	embeddedHasCli = true
	if rec := get(staticCliPath); rec.Code != http.StatusOK || rec.Body.String() != "embedded" {
		t.Fatalf("missing CLI, embedded copy: code %d body %q", rec.Code, rec.Body.String())
	}
	embeddedHasCli = false
	repman.Conf.ReplicationManagerCliPath = dir
	if rec := get(staticCliPath); rec.Code != http.StatusNotFound {
		t.Fatalf("CLI path is a directory: code %d, want 404", rec.Code)
	}
}
