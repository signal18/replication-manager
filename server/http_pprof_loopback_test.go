// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The profiler answers callers on the loopback only, whatever http-bind-address.
func TestPprofLoopbackOnly(t *testing.T) {
	served := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := loopbackOnly(served)
	for remote, want := range map[string]int{
		"127.0.0.1:5000":      http.StatusOK,
		"[::1]:5000":          http.StatusOK,
		"192.168.10.43:5000":  http.StatusNotFound, // a gateway
		"10.62.85.10:5000":    http.StatusNotFound, // a container of the same cluster
		"67.213.127.157:5000": http.StatusNotFound,
		"garbage-remote-addr": http.StatusNotFound,
	} {
		r := httptest.NewRequest(http.MethodGet, "/debug/pprof/goroutine?debug=2", nil)
		r.RemoteAddr = remote
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("%s: %d, want %d", remote, w.Code, want)
		}
	}
}
