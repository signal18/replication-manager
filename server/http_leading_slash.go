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
	"strings"

	"github.com/gorilla/mux"
)

// redispatchLeadingSlashes serves a request whose path starts with several
// slashes ("//api/health") as the same path with one leading slash, and reports
// whether it did. The routers skip path cleaning (a setting value in the path may
// start with a slash, 96678a8ad), so such a path matched nothing and fell to the
// dashboard page: a peer on an older build, whose client joins its base URL and
// "/api/..." with an extra slash, read HTML instead of /api/health (#1953). Only the
// leading slashes are collapsed; a doubled slash inside the path stays as typed.
func redispatchLeadingSlashes(router *mux.Router, w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "//") {
		return false
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/" + strings.TrimLeft(r.URL.Path, "/")
	if r.URL.RawPath != "" {
		r2.URL.RawPath = "/" + strings.TrimLeft(r.URL.RawPath, "/")
	}
	r2.RequestURI = r2.URL.RequestURI()
	router.ServeHTTP(w, r2)
	return true
}
