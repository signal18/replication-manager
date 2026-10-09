// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this source distribution for more information.

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

// The routers' setup: no path cleaning, an /api route, a setting route whose
// value may start with a slash, and the NotFoundHandler with the redispatch.
func newSlashTestRouter() *mux.Router {
	router := mux.NewRouter()
	router.SkipClean(true)
	router.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("health")) })
	router.HandleFunc("/api/clusters/{c}/settings/actions/set/{name}/{value:.*}", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("value=" + mux.Vars(r)["value"]))
	})
	router.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if redispatchLeadingSlashes(router, w, r) {
			return
		}
		if len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api" {
			http.NotFound(w, r)
		} else {
			http.Redirect(w, r, "/", http.StatusFound)
		}
	})
	return router
}

func serveSlash(router *mux.Router, path string) (int, string) {
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "http://x"+path, nil))
	return w.Code, w.Body.String()
}

func TestLeadingDoubleSlashServedAsAPI(t *testing.T) {
	r := newSlashTestRouter()
	for _, p := range []string{"/api/health", "//api/health", "///api/health"} {
		if code, body := serveSlash(r, p); code != 200 || body != "health" {
			t.Errorf("%s: %d %q, want the health route", p, code, body)
		}
	}
}

func TestDoubleSlashInsideSettingValueKept(t *testing.T) {
	r := newSlashTestRouter()
	code, body := serveSlash(r, "/api/clusters/c1/settings/actions/set/monitoring-add-monitor-script//tmp/x.sh")
	if code != 200 || body != "value=/tmp/x.sh" {
		t.Fatalf("%d %q, want the value with its leading slash", code, body)
	}
	if code, _ := serveSlash(r, "/nothing"); code != http.StatusFound {
		t.Fatalf("non-API path: %d, want the redirect to the dashboard", code)
	}
}
