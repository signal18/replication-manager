// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file contains all plugin-related dbhelper lookups, kept together
// (rather than split across e.g. performance.go) so the two are never edited
// independently and drift out of sync:
//
//   - GetPlugins: pooled *sqlx.DB, returns the full plugin map. Used only to
//     refresh a monitoring-side cache (server.Plugins, cluster/srv.go) on a
//     periodic tick -- can be stale or disabled (MonitorPlugins=false).
//   - GetPluginStatusConn: pinned *sqlx.Conn, resolves a single named plugin's
//     live status. Used for restore-time truth (execSplitdumpSingle's
//     INSTALL PLUGIN skip guard, cluster/srv_job_backup.go) -- callers that
//     need "is this ACTIVE right now, on this connection" must use this, not
//     the cache GetPlugins feeds.
package dbhelper

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// GetPlugins returns every plugin the server reports, keyed by name. Feeds
// only the monitoring-side cache (server.Plugins) -- restore-time code that
// needs a live, single-plugin answer must use GetPluginStatusConn below
// instead, not this function's result.
func GetPlugins(db *sqlx.DB, myver *version.Version) (map[string]*Plugin, string, error) {
	vars := make(map[string]*Plugin)
	query := `SHOW PLUGINS`
	if myver.IsMariaDB() {
		query = `SHOW PLUGINS soname`
	}

	rows, err := db.Queryx(query)
	if err != nil {
		return nil, query, errors.New("Could not get queries")
	}
	defer rows.Close()
	for rows.Next() {
		var v Plugin
		err := rows.Scan(&v.Name, &v.Status, &v.Type, &v.Library, &v.License)
		if err != nil {
			return nil, query, errors.New("Could not get results from plugins scan")
		}
		vars[v.Name] = &v
	}
	return vars, query, nil
}

// PluginLookupStatus is the resolved outcome of a live, single-plugin
// existence lookup on a pinned restore connection.
type PluginLookupStatus int

const (
	PluginAbsent PluginLookupStatus = iota
	PluginActive
	PluginNotInstalled
	PluginPresentNotActive
	PluginAmbiguous
)

// GetPluginStatusConn runs the same SHOW PLUGINS query GetPlugins issues, but
// over a pinned *sqlx.Conn and resolved to a single named plugin's status
// instead of the full plugin map. It is the restore-time-truth counterpart to
// GetPlugins: callers deciding whether to skip an INSTALL PLUGIN statement
// during catalogue replay must use this, not a monitoring cache.
//
// name is matched case-insensitively against the server's reported plugin
// names: MariaDB/MySQL plugin identifiers are effectively case-insensitive
// (INSTALL PLUGIN accepts any case, and SHOW PLUGINS reports its own
// canonical casing which need not match the case a dump's INSTALL PLUGIN
// statement used) -- an exact-case comparison here would misreport an
// already-ACTIVE plugin as absent purely from a case mismatch, causing the
// caller to re-run INSTALL PLUGIN and recreate the failure this lookup exists
// to avoid.
func GetPluginStatusConn(ctx context.Context, conn *sqlx.Conn, name string, myver *version.Version) (status PluginLookupStatus, observedStatus string, err error) {
	query := `SHOW PLUGINS`
	if myver.IsMariaDB() {
		query = `SHOW PLUGINS soname`
	}

	rows, err := conn.QueryxContext(ctx, query)
	if err != nil {
		return PluginAbsent, "", fmt.Errorf("could not get plugins: %w", err)
	}
	defer rows.Close()

	matches := 0
	for rows.Next() {
		var v Plugin
		if err := rows.Scan(&v.Name, &v.Status, &v.Type, &v.Library, &v.License); err != nil {
			return PluginAbsent, "", fmt.Errorf("could not scan plugins result: %w", err)
		}
		if strings.EqualFold(v.Name, name) {
			matches++
			observedStatus = v.Status
		}
	}
	if err := rows.Err(); err != nil {
		return PluginAbsent, "", fmt.Errorf("error iterating plugins result: %w", err)
	}

	switch {
	case matches == 0:
		return PluginAbsent, "", nil
	case matches > 1:
		return PluginAmbiguous, observedStatus, nil
	case strings.EqualFold(observedStatus, "ACTIVE"):
		return PluginActive, observedStatus, nil
	case strings.EqualFold(observedStatus, "NOT INSTALLED"):
		// SHOW PLUGINS can list a known-but-uninstalled plugin (e.g.
		// QUERY_RESPONSE_TIME) with this status even though matches==1 --
		// distinct from PluginAbsent (no row at all) and from a genuinely
		// suspicious PluginPresentNotActive (e.g. DISABLED). Both PluginAbsent
		// and PluginNotInstalled are safe for the caller to run INSTALL
		// PLUGIN against; only PluginPresentNotActive is treated as fatal.
		return PluginNotInstalled, observedStatus, nil
	default:
		return PluginPresentNotActive, observedStatus, nil
	}
}
