// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"fmt"
	"time"

	"github.com/signal18/replication-manager/config"
	jwt "github.com/golang-jwt/jwt/v5"
)

// Generic API token.
//
// A self-contained service/access token, distinct from the interactive RSA JWT:
//   - signed HS256 with the PERSISTENT config.SecretKey (monitoring-key-path), so it
//     survives repman restarts (the RSA signing key is regenerated every startup) and
//     rotates on purpose via monitoring-secret-versioning;
//   - embeds its own authority: the subject, the grant list, and a cluster scope
//     ("*" = all), plus an optional expiry -- so a leaked token can do exactly what
//     it carries and nothing more (least privilege);
//   - identity-agnostic: the same token serves the auto-built `system` user, the
//     auto-built job/sensor identities (resource-sensor grant, cluster-scoped, injected
//     into the jobs container), and AI-model users (sub `ai:<name>`, scoped grants).
//
// Used directly as `Authorization: Bearer <token>` -- no secret-login round-trip.
// validateTokenMiddleware accepts it (alongside the RSA JWT); the ACL enforces the
// embedded grants + cluster scope.

// APITokenTypApi marks a token's `typ` claim as a generic API token (vs the RSA login).
const APITokenTypApi = "api"

// Well-known token subjects / kinds (the `knd` claim), for auto-built identities.
const (
	APITokenKindSystem = "system" // the internal system user (grants: db proxy)
	APITokenKindJob     = "job"    // a dbjob / compute sensor (grant: resource-sensor)
	APITokenKindAI      = "ai"     // an AI-model user (scoped grants)
	APITokenKindUser    = "user"   // a human-issued service token
)

// APITokenClaims is the embedded authority of a generic API token.
type APITokenClaims struct {
	Subject string   // who: e.g. "system", "job:<cluster>", "ai:<name>"
	Kind    string   // one of APITokenKind*
	Grants  []string // the embedded grant list the ACL enforces
	Cluster string   // cluster scope; "*" = all clusters
}

// MintAPIToken issues a generic API token embedding the given authority, signed HS256
// with the persistent config.SecretKey. ttl <= 0 means no expiry (auto-built service
// tokens); pass a real ttl for AI-model / human service tokens.
func (repman *ReplicationManager) MintAPIToken(c APITokenClaims, ttl time.Duration) (string, error) {
	key := repman.Conf.SecretKey
	if len(key) == 0 {
		return "", fmt.Errorf("cannot mint API token: no persistent secret key (set monitoring-key-path)")
	}
	if c.Cluster == "" {
		c.Cluster = "*"
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"typ":     APITokenTypApi,
		"sub":     c.Subject,
		"knd":     c.Kind,
		"grants":  c.Grants,
		"cluster": c.Cluster,
		"iat":     now.Unix(),
	}
	if ttl > 0 {
		claims["exp"] = now.Add(ttl).Unix()
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
}

// apiTokenClaimsFrom extracts the embedded authority from a parsed generic API token.
// Returns ok=false if the token is not a generic API token (e.g. an interactive RSA JWT).
func apiTokenClaimsFrom(token *jwt.Token) (APITokenClaims, bool) {
	if token == nil {
		return APITokenClaims{}, false
	}
	mc, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return APITokenClaims{}, false
	}
	if typ, _ := mc["typ"].(string); typ != APITokenTypApi {
		return APITokenClaims{}, false
	}
	out := APITokenClaims{Cluster: "*"}
	out.Subject, _ = mc["sub"].(string)
	out.Kind, _ = mc["knd"].(string)
	if cl, ok := mc["cluster"].(string); ok && cl != "" {
		out.Cluster = cl
	}
	if raw, ok := mc["grants"].([]interface{}); ok {
		for _, g := range raw {
			if s, ok := g.(string); ok {
				out.Grants = append(out.Grants, s)
			}
		}
	}
	return out, true
}

// InScope reports whether the token's cluster scope covers clusterName.
func (c APITokenClaims) InScope(clusterName string) bool {
	return c.Cluster == "*" || c.Cluster == clusterName
}

// GetJobToken auto-builds the (no-expiry) generic API token for a cluster's dbjob /
// compute sensor: subject "job:<cluster>", grant resource-sensor, scoped to that
// cluster. Deterministic given the persistent key + cluster, so it is regenerated on
// demand for both validation and injection into the jobs container -- no storage; it
// rotates only when the key rotates.
func (repman *ReplicationManager) GetJobToken(clusterName string) (string, error) {
	return repman.MintAPIToken(APITokenClaims{
		Subject: "job:" + clusterName,
		Kind:    APITokenKindJob,
		Grants:  []string{config.GrantClusterResourceSensor},
		Cluster: clusterName,
	}, 0)
}
