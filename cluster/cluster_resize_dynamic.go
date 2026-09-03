// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"

	"github.com/signal18/replication-manager/config"
)

// resizeDynamicResourceSQL builds the SET GLOBAL statements for the
// resource-driven variables MariaDB accepts at runtime, valued from the
// configurator % model so they track prov-db-memory / cpu / io. This is the
// direct path — no config regeneration, no compliance, no cnf reading.
//
// max_connections is deliberately NOT here: the connection count is client
// workload, not ours to cap (a client may legitimately hold thousands of idle
// connections). We size the memory each session/engine may use, never how many
// sessions the client opens.
//
// Ordering is anti-OOM. The cgroup resize itself is external (orchestrator /
// prov-db-resize-script, #1760); this only drives the in-server SET GLOBAL:
//   - grow:   buffer pool LAST, after the cgroup has grown, so InnoDB never
//     claims memory the cgroup does not yet have.
//   - shrink: buffer pool FIRST, freeing memory before the cgroup shrinks.
func (server *ServerMonitor) resizeDynamicResourceSQL(grow bool) []string {
	cfg := &server.ClusterGroup.Configurator
	bufferPool := fmt.Sprintf("SET GLOBAL innodb_buffer_pool_size = %s*1024*1024", cfg.GetConfigInnoDBBPSize())

	// Non-buffer-pool caps: always safe to (re)apply in any direction.
	others := []string{
		fmt.Sprintf("SET GLOBAL key_buffer_size = %s*1024*1024", cfg.GetConfigMyISAMKeyBufferSize()),
		fmt.Sprintf("SET GLOBAL innodb_io_capacity = %s", cfg.GetConfigInnoDBIOCapacity()),
		fmt.Sprintf("SET GLOBAL innodb_io_capacity_max = %s", cfg.GetConfigInnoDBIOCapacityMax()),
		fmt.Sprintf("SET GLOBAL innodb_max_dirty_pages_pct = %s", cfg.GetConfigInnoDBMaxDirtyPagePct()),
		fmt.Sprintf("SET GLOBAL innodb_max_dirty_pages_pct_lwm = %s", cfg.GetConfigInnoDBMaxDirtyPagePctLwm()),
	}
	// max_session_mem_used: the native per-session cap (#1749). 0 = disabled
	// (threads:0 share) — leave MariaDB's unlimited default untouched.
	if v := cfg.GetConfigMaxSessionMemUsedMB(); v > 0 {
		others = append(others, fmt.Sprintf("SET GLOBAL max_session_mem_used = %d*1024*1024", v))
	}
	// query_cache_size: 0 = off by default, do not silently enable it.
	if qc := cfg.GetConfigQueryCacheSize(); qc != "0" {
		others = append(others, fmt.Sprintf("SET GLOBAL query_cache_size = %s*1024*1024", qc))
	}

	if grow {
		return append(others, bufferPool)
	}
	return append([]string{bufferPool}, others...)
}

// ResizeDynamicResources applies the resource-driven SET GLOBAL set live to every
// monitored server, in anti-OOM order (see resizeDynamicResourceSQL). Restart-only
// variables report MySQL error 1238 (handled by ExecScriptSQL) and fall back to a
// restart cookie, so a resize never silently drops them. Gated by its own
// off-switch prov-db-dynamic-resource: when off, callers keep the reprov/restart
// path (this is a distinct feature from prov-db-apply-dynamic-config, which
// drives tag-change SET GLOBALs).
func (cluster *Cluster) ResizeDynamicResources(grow bool) {
	if !cluster.Conf.ProvDBDynamicResource {
		return
	}
	for _, server := range cluster.Servers {
		if server == nil || server.State == stateFailed || server.State == stateUnconn {
			continue
		}
		_, needRestart := server.ExecScriptSQL(server.resizeDynamicResourceSQL(grow))
		if needRestart {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"Dynamic resize on %s hit a restart-only variable, scheduling restart", server.URL)
			server.SetRestartCookie()
		}
	}
}
