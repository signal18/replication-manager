// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3.

package repmanmcp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/signal18/replication-manager/cluster"
)

// toJSON marshals v to a JSON string, returning an error message string on failure.
func toJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": "json encoding failed: %s"}`, err)
	}
	return string(b)
}

// clusterOrError retrieves a cluster by name, returning a tool error result if not found.
func clusterOrError(repman RepmanProvider, clusterName string) (*cluster.Cluster, *mcp.CallToolResult) {
	if clusterName == "" {
		return nil, mcp.NewToolResultError("cluster_name is required")
	}
	cl := repman.GetClusterByName(clusterName)
	if cl == nil {
		return nil, mcp.NewToolResultErrorf("cluster not found: %s", clusterName)
	}
	return cl, nil
}

// extractClusterFromURI extracts the clusterName from a repman:// URI.
// URI pattern: repman://clusters/{clusterName}/...
func extractClusterFromURI(uri string) string {
	re := regexp.MustCompile(`^repman://clusters/([^/]+)`)
	m := re.FindStringSubmatch(uri)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// extractParamFromURI extracts a named segment from a URI given a template.
// Example: extractParamFromURI("repman://clusters/mycluster/servers/myserver/status",
//
//	"repman://clusters/{clusterName}/servers/{serverName}/status")
//
// returns map{"clusterName":"mycluster","serverName":"myserver"}
func extractParamsFromURI(uri, template string) map[string]string {
	result := make(map[string]string)

	// Convert template to regex
	reTemplate := regexp.MustCompile(`\{([^}]+)\}`)
	paramNames := reTemplate.FindAllStringSubmatch(template, -1)

	pattern := reTemplate.ReplaceAllStringFunc(template, func(m string) string {
		return `([^/]+)`
	})
	// Escape everything except our capture groups
	parts := strings.Split(pattern, `([^/]+)`)
	var escaped []string
	for _, p := range parts {
		escaped = append(escaped, regexp.QuoteMeta(p))
	}
	finalPattern := strings.Join(escaped, `([^/]+)`)
	re := regexp.MustCompile(`^` + finalPattern + `$`)

	matches := re.FindStringSubmatch(uri)
	if len(matches) < 2 {
		return result
	}

	for i, param := range paramNames {
		if i+1 < len(matches) {
			result[param[1]] = matches[i+1]
		}
	}
	return result
}

// resolveServer finds a server of a cluster by any name a caller is likely to
// use: the opaque id (what GetServerFromName matches), the short name shown in
// the topology ("db1"), the host, host:port, or the URL. An assistant reads
// "name" from get-cluster-topology and passes it back, so the id alone is not
// enough.
func resolveServer(cl *cluster.Cluster, name string) *cluster.ServerMonitor {
	if cl == nil || name == "" {
		return nil
	}
	if srv := cl.GetServerFromName(name); srv != nil {
		return srv
	}
	for _, srv := range cl.Servers {
		if srv == nil {
			continue
		}
		if srv.Name == name || srv.Host == name || srv.URL == name || srv.Host+":"+srv.Port == name {
			return srv
		}
	}
	return nil
}
