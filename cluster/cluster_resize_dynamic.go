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

// ResizeFeasibility is the verdict returned by the can-change feasibility gate
// (prov-db-dynamic-resource-can-change-script) before any live resize is applied.
type ResizeFeasibility string

const (
	ResizeYes       ResizeFeasibility = "yes"       // resize possible in place
	ResizeNo        ResizeFeasibility = "no"        // not possible, keep current size
	ResizeMigration ResizeFeasibility = "migration" // not in place, needs relocating the instance to a host with capacity
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
//
// The infra resize (cgroup/disk/io) and the DB resize (SET GLOBAL) are sequenced
// anti-OOM. On GROW the infra must actually grow first — and report that it was
// POSSIBLE — before we raise the DB memory; if it is not possible we keep the
// current size (never OOM). On SHRINK we free DB memory first, then shrink infra.
func (cluster *Cluster) ResizeDynamicResources(grow bool) {
	if !cluster.Conf.ProvDBDynamicResource {
		return
	}
	for _, server := range cluster.Servers {
		if server == nil || server.State == stateFailed || server.State == stateUnconn {
			continue
		}
		if grow {
			applied, err := cluster.ResizeDatabaseResources(server, true)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
					"Resource grow reported not possible on %s, keeping current DB memory: %s", server.URL, err)
				continue
			}
			if !applied {
				// Native path could not resize live (restart cookie already set by
				// ResizeDatabaseResources); do NOT raise DB memory over a cgroup that
				// has not grown.
				continue
			}
			if _, needRestart := server.ExecScriptSQL(server.resizeDynamicResourceSQL(true)); needRestart {
				server.SetRestartCookie()
			}
		} else {
			if _, needRestart := server.ExecScriptSQL(server.resizeDynamicResourceSQL(false)); needRestart {
				server.SetRestartCookie()
			}
			if _, err := cluster.ResizeDatabaseResources(server, false); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
					"Resource shrink on %s failed: %s", server.URL, err)
			}
		}
	}
}

// ResizeDatabaseResources changes the four provisioned resources (mem, cpu, disk,
// io) of a RUNNING server live and reports whether the resize was POSSIBLE
// (applied). Dispatch follows the prov.go orchestrator idiom (T7). A client resize
// script OVERRIDES in every orchestrator case (F7 — every orchestrated action must
// be client-overridable); its exit status is its feasibility answer (success =
// possible/applied). With no script, the native per-orchestrator path is used.
func (cluster *Cluster) ResizeDatabaseResources(server *ServerMonitor, grow bool) (bool, error) {
	// Feasibility gate first, in every orchestrator case: is the resize possible?
	switch feas, err := cluster.RunDynamicResourceCanChangeScript(server, grow); {
	case err != nil:
		return false, err
	case feas == ResizeNo:
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
			"Resource resize reported not possible (no) on %s, keeping current size", server.URL)
		return false, nil
	case feas == ResizeMigration:
		// Not resizable in place: the host lacks capacity and the instance would
		// need relocating. In-place resize is skipped here; the migration
		// orchestration + tracked state (T5) is a follow-up (#1765/#1760 §10).
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"Resource resize needs migration on %s (host lacks capacity); in-place resize skipped", server.URL)
		return false, nil
	}
	// ResizeYes: apply. A client change script overrides in every case (F7).
	if cluster.Conf.ProvDBDynamicResourceChangeScript != "" {
		if err := cluster.RunDynamicResourceChangeScript(server, grow); err != nil {
			return false, err
		}
		return true, nil
	}
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		return cluster.OpenSVCResizeDatabaseResources(server, grow)
	case config.ConstOrchestratorKubernetes:
		// In-place pod resize (K8s 1.27+ InPlacePodVerticalScaling) is not wired
		// yet; fall back to a restart so the new requests/limits apply on reschedule.
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"K8s live resource resize not wired, scheduling restart on %s", server.URL)
		server.SetRestartCookie()
		return false, nil
	default:
		server.SetRestartCookie()
		return false, nil
	}
}

// OpenSVCResizeDatabaseResources resizes the container cgroup limits (mem/cpu/io)
// live through the OpenSVC API. TODO(#1765): wire the exact cgroup keyword through
// SetServiceConfigKeys once the OpenSVC resource key is confirmed; until then it
// falls back to a restart so the new limits apply on the next boot rather than
// risking an OOM on a live grow. Returns (false) so ResizeDynamicResources does
// not raise DB memory over an un-resized cgroup.
func (cluster *Cluster) OpenSVCResizeDatabaseResources(server *ServerMonitor, grow bool) (bool, error) {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
		"OpenSVC live cgroup resize not yet wired, scheduling restart on %s", server.URL)
	server.SetRestartCookie()
	return false, nil
}
