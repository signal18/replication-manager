// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3.

package repmanmcp

import (
	"fmt"
	"net/http"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/golang-jwt/jwt/v5/request"
	log "github.com/sirupsen/logrus"
)

// authMiddleware wraps an http.Handler and requires a valid Bearer JWT
// signed by the replication-manager REST API on every request. The token
// is the same one issued by POST /api/login.
//
// On missing or invalid tokens the middleware writes 401 Unauthorized
// and does not call the wrapped handler. On success the request is
// forwarded unchanged.
func authMiddleware(next http.Handler, verificationKey []byte, logger *log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := request.ParseFromRequest(r, request.AuthorizationHeaderExtractor,
			func(t *jwt.Token) (interface{}, error) {
				vk, perr := jwt.ParseRSAPublicKeyFromPEM(verificationKey)
				if perr != nil {
					return nil, perr
				}
				return vk, nil
			})

		if err != nil {
			logger.Warnf("MCP auth: rejected request from %s: %v", r.RemoteAddr, err)
			w.Header().Set("WWW-Authenticate", `Bearer realm="replication-manager-mcp"`)
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, "Unauthorized: %s\n", err.Error())
			return
		}
		if !token.Valid {
			logger.Warnf("MCP auth: invalid token from %s", r.RemoteAddr)
			w.Header().Set("WWW-Authenticate", `Bearer realm="replication-manager-mcp", error="invalid_token"`)
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintln(w, "Unauthorized: token is not valid")
			return
		}

		logger.Debugf("MCP auth: accepted request from %s for %s", r.RemoteAddr, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
