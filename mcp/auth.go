// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package repmanmcp

import (
	"fmt"
	"net/http"

	log "github.com/sirupsen/logrus"
)

// authMiddleware gates the SSE and message endpoints: the bearer must resolve to
// a principal through the server's own authentication (an interactive login JWT
// from POST /api/login, or a user-issued API token, #1835). The per-tool
// authorization happens later in acl.go; this only refuses anonymous callers.
func authMiddleware(next http.Handler, repman RepmanProvider, logger *log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := repman.AuthenticateMCP(r)
		if err != nil || p == nil {
			reason := "no valid bearer"
			if err != nil {
				reason = err.Error()
			}
			logger.Warnf("MCP auth: rejected request from %s: %s", r.RemoteAddr, reason)
			repman.LogSecurityEvent("mcp_auth_failure", "", r.RemoteAddr, "MCP request rejected: "+reason)
			w.Header().Set("WWW-Authenticate", `Bearer realm="replication-manager-mcp", error="invalid_token"`)
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, "Unauthorized: %s\n", reason)
			return
		}
		logger.Debugf("MCP auth: %s from %s on %s", p.String(), r.RemoteAddr, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
