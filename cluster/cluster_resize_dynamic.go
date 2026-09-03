// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	logsql "github.com/sirupsen/logrus"
	"gopkg.in/ini.v1"
)

// ResizeFeasibility is the verdict returned by the can-change feasibility gate
// (prov-db-dynamic-resource-can-change-script) before any live resize is applied.
type ResizeFeasibility string

const (
	ResizeYes       ResizeFeasibility = "yes"       // resize possible in place
	ResizeNo        ResizeFeasibility = "no"        // not possible, keep current size
	ResizeMigration ResizeFeasibility = "migration" // not in place, needs relocating the instance to a host with capacity
)

// ResourceResizer is the orchestrator capability to resize a running instance's
// provisioned resources (mem/cpu/disk/io) live (T7). Each backend implements it;
// cluster.resourceResizer() selects the right one. A client resize script always
// overrides the native backend (F7).
type ResourceResizer interface {
	// CanResize answers whether the resize is possible: yes (in place), no (keep
	// current size), or migration (needs relocating the instance).
	CanResize(server *ServerMonitor, grow bool) (ResizeFeasibility, error)
	// Resize applies the infra resize live and reports whether it was applied.
	Resize(server *ServerMonitor, grow bool) (bool, error)
}

// scriptResizer is the client-overridable backend (F7): used in every
// orchestrator case when prov-db-dynamic-resource-change-script is set.
type scriptResizer struct{ cluster *Cluster }

func (r scriptResizer) CanResize(server *ServerMonitor, grow bool) (ResizeFeasibility, error) {
	return r.cluster.RunDynamicResourceCanChangeScript(server, grow)
}

func (r scriptResizer) Resize(server *ServerMonitor, grow bool) (bool, error) {
	if r.cluster.Conf.ProvDBDynamicResourceChangeScript == "" {
		// No client script set: cannot resize the infra live here — schedule a
		// restart so the new size applies on the next boot (never grow DB memory
		// over an un-resized cgroup).
		r.cluster.LogModulePrintf(r.cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"no prov-db-dynamic-resource-change-script set, scheduling restart on %s", server.URL)
		server.SetRestartCookie()
		return false, nil
	}
	if err := r.cluster.RunDynamicResourceChangeScript(server, grow); err != nil {
		return false, err
	}
	return true, nil
}

// openSVCResizer resizes the container cgroup live through the OpenSVC PG update
// API (om3 v3). The client can-change script (if any) still gates feasibility.
type openSVCResizer struct{ cluster *Cluster }

func (r openSVCResizer) CanResize(server *ServerMonitor, grow bool) (ResizeFeasibility, error) {
	return r.cluster.RunDynamicResourceCanChangeScript(server, grow)
}

func (r openSVCResizer) Resize(server *ServerMonitor, grow bool) (bool, error) {
	return r.cluster.openSVCResize(server, grow)
}

// restartResizer has no live resize path: it schedules a restart so the new size
// applies on the next boot (K8s in-place not wired yet, or an orchestrator with
// no live-resize primitive). Returns applied=false so the DB memory is not raised
// over a cgroup that has not grown.
type restartResizer struct {
	cluster *Cluster
	reason  string
}

func (r restartResizer) CanResize(server *ServerMonitor, grow bool) (ResizeFeasibility, error) {
	return r.cluster.RunDynamicResourceCanChangeScript(server, grow)
}

func (r restartResizer) Resize(server *ServerMonitor, grow bool) (bool, error) {
	r.cluster.LogModulePrintf(r.cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
		"%s, scheduling restart on %s", r.reason, server.URL)
	server.SetRestartCookie()
	return false, nil
}

// resourceResizer returns the resizer for this cluster. A client change-script
// overrides in every orchestrator case (F7); otherwise the native per-orchestrator
// backend is used, following the prov.go orchestrator idiom (T7).
func (cluster *Cluster) resourceResizer() ResourceResizer {
	if cluster.Conf.ProvDBDynamicResourceChangeScript != "" {
		return scriptResizer{cluster}
	}
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		return openSVCResizer{cluster}
	case config.ConstOrchestratorKubernetes, config.ConstOrchestratorOnPremise,
		config.ConstOrchestratorLocalhost, config.ConstOrchestratorSlapOS:
		// For now these resize only through the client change-script; scriptResizer
		// falls back to a restart when no script is set. K8s in-place pod resize
		// (1.27+) is a follow-up that would get its own backend here.
		return scriptResizer{cluster}
	default:
		return restartResizer{cluster, "no live resource resize for this orchestrator"}
	}
}

// openSVCResize writes the process-group (cgroup) memory keyword into the service
// config and re-applies it live via the om3 PG update API — no restart. Only
// available on OpenSVC v3 (pg update is a v3 action); on v2 it falls back to a
// restart. Grow safety is enforced by the caller ordering (infra before the DB
// memory raise).
func (cluster *Cluster) openSVCResize(server *ServerMonitor, grow bool) (bool, error) {
	svc := cluster.OpenSVCConnect()
	if !svc.IsV3() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"OpenSVC v2 has no live pg update, scheduling restart on %s", server.URL)
		server.SetRestartCookie()
		return false, nil
	}
	svcparts := strings.SplitN(server.ServiceName, "/", 3)
	if len(svcparts) != 3 {
		return false, fmt.Errorf("invalid service name %q, expected namespace/kind/name", server.ServiceName)
	}
	ns, kind, svcname := svcparts[0], svcparts[1], svcparts[2]

	memMB, err := config.ParseUnitMeasurementToInt("M,bytes,required", cluster.Conf.ProvMem, true)
	if err != nil {
		return false, err
	}

	// 1. Write the PG memory keyword (bytes) into the service config.
	raw, err := svc.GetObjectConfigFileV3(ns, kind, svcname)
	if err != nil {
		return false, err
	}
	cfg, err := ini.LoadSources(ini.LoadOptions{IgnoreInlineComment: true}, bytes.NewReader(raw))
	if err != nil {
		return false, fmt.Errorf("failed to parse service config for %s: %w", server.ServiceName, err)
	}
	cfg.Section("DEFAULT").Key("pg_mem_limit").SetValue(strconv.FormatInt(int64(memMB)*1024*1024, 10))
	var buf bytes.Buffer
	if _, err = cfg.WriteTo(&buf); err != nil {
		return false, err
	}
	if _, err = svc.UpdateObjectV3(ns, kind, svcname, buf.Bytes()); err != nil {
		return false, err
	}

	// 2. Apply the new cgroup limit live on the running node.
	if err := svc.PGUpdateInstanceV3(server.Agent, server.ServiceName, ""); err != nil {
		return false, err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
		"OpenSVC live cgroup resize applied on %s (pg_mem_limit=%dMB)", server.URL, memMB)
	return true, nil
}

// resizeDimension is the provisioned resource that changed; each drives its own
// SET GLOBALs. A memory change must not re-apply io tuning and vice-versa.
type resizeDimension int

const (
	resizeMemory resizeDimension = iota // prov-db-memory
	resizeIO                            // prov-db-disk-iops
	resizeCPU                           // prov-db-cpu-cores
)

func (dim resizeDimension) String() string {
	switch dim {
	case resizeMemory:
		return "memory"
	case resizeIO:
		return "io"
	case resizeCPU:
		return "cpu"
	}
	return "unknown"
}

// logResize appends one structured event to the rotating resource_resize.log
// (JSON, one event per line). It records the paramétré-axis change so that (a) the
// BO can reconcile it against the DBU (autorisé/facturé), and (b) the workload
// plugin can decide whether to return the freed resources to the shared pool or
// reclaim from it (consommé vs autorisé).
func (cluster *Cluster) logResize(server *ServerMonitor, dim resizeDimension, grow, applied bool, feas ResizeFeasibility, statements []string) {
	if cluster.ResourceResizeLog == nil {
		return
	}
	dir := "shrink"
	if grow {
		dir = "grow"
	}
	cluster.ResourceResizeLog.WithFields(logsql.Fields{
		"cluster":      cluster.Name,
		"server":       server.URL,
		"dimension":    dim.String(),
		"direction":    dir,
		"applied":      applied,
		"feasibility":  string(feas),
		"orchestrator": cluster.GetOrchestrator(),
		"prov_mem":     cluster.Conf.ProvMem,
		"prov_cores":   cluster.Conf.ProvCores,
		"prov_iops":    cluster.Conf.ProvIops,
		"statements":   statements,
	}).Info("resource resize")
}

// resizeMemorySQL builds the memory-driven SET GLOBALs, valued from the
// configurator % model. Anti-OOM ordered: grow puts the buffer pool LAST (after
// the cgroup grew), shrink puts it FIRST (freeing memory before the cgroup
// shrinks). max_connections is deliberately NOT here: the connection count is
// client workload, not ours to cap.
func (server *ServerMonitor) resizeMemorySQL(grow bool) []string {
	cfg := &server.ClusterGroup.Configurator
	bufferPool := fmt.Sprintf("SET GLOBAL innodb_buffer_pool_size = %s*1024*1024", cfg.GetConfigInnoDBBPSize())

	others := []string{
		fmt.Sprintf("SET GLOBAL key_buffer_size = %s*1024*1024", cfg.GetConfigMyISAMKeyBufferSize()),
		// per-thread buffers, %-driven from the threads budget × prov-db-memory-threaded-pct
		fmt.Sprintf("SET GLOBAL tmp_table_size = %s*1024*1024", cfg.GetConfigTmpTableSize()),
		fmt.Sprintf("SET GLOBAL max_heap_table_size = %s*1024*1024", cfg.GetConfigTmpTableSize()),
		fmt.Sprintf("SET GLOBAL join_buffer_size = %s*1024*1024", cfg.GetConfigJoinBufferSize()),
		fmt.Sprintf("SET GLOBAL sort_buffer_size = %s*1024*1024", cfg.GetConfigSortBufferSize()),
	}
	if server.IsMariaDB() {
		// join_buffer_space_limit (total join memory per query) + mrr_buffer_size
		// (Multi-Range Read / BKA, the modern indexed-join buffer) are MariaDB-only.
		others = append(others,
			fmt.Sprintf("SET GLOBAL join_buffer_space_limit = %s*1024*1024", cfg.GetConfigJoinBufferSpaceLimit()),
			fmt.Sprintf("SET GLOBAL mrr_buffer_size = %s*1024*1024", cfg.GetConfigMRRBufferSize()))
		// max_session_mem_used: the native per-session cap (#1749). MariaDB-only —
		// MySQL/Percona do not have it and would error 1193. 0 = disabled (threads:0
		// share) — leave MariaDB's unlimited default untouched.
		if v := cfg.GetConfigMaxSessionMemUsedMB(); v > 0 {
			others = append(others, fmt.Sprintf("SET GLOBAL max_session_mem_used = %d*1024*1024", v))
		}
	}
	// query_cache_size: 0 = off by default (never silently enable it), and the
	// query cache was removed in MySQL/Percona 8.0, so skip it there.
	mysql8 := server.IsMySQL() && server.DBVersion != nil && server.DBVersion.Major >= 8
	if qc := cfg.GetConfigQueryCacheSize(); qc != "0" && !mysql8 {
		others = append(others, fmt.Sprintf("SET GLOBAL query_cache_size = %s*1024*1024", qc))
	}

	if grow {
		return append(others, bufferPool)
	}
	return append([]string{bufferPool}, others...)
}

// resizeIOSQL builds the iops-driven SET GLOBALs. io capacity is settable on both
// flavors; the InnoDB io threads are dynamic on MariaDB (verified 11.4) but
// restart-only on MySQL/Percona, hence the MariaDB gate.
func (server *ServerMonitor) resizeIOSQL() []string {
	cfg := &server.ClusterGroup.Configurator
	sql := []string{
		fmt.Sprintf("SET GLOBAL innodb_io_capacity = %s", cfg.GetConfigInnoDBIOCapacity()),
		fmt.Sprintf("SET GLOBAL innodb_io_capacity_max = %s", cfg.GetConfigInnoDBIOCapacityMax()),
		fmt.Sprintf("SET GLOBAL innodb_max_dirty_pages_pct = %s", cfg.GetConfigInnoDBMaxDirtyPagePct()),
		fmt.Sprintf("SET GLOBAL innodb_max_dirty_pages_pct_lwm = %s", cfg.GetConfigInnoDBMaxDirtyPagePctLwm()),
	}
	// innodb_write_io_threads is iops-driven and dynamic on MariaDB (restart-only
	// on MySQL/Percona, hence the gate). read_io_threads is cores-driven -> CPU;
	// purge_threads is a fixed constant, so neither belongs here.
	if server.IsMariaDB() {
		sql = append(sql, fmt.Sprintf("SET GLOBAL innodb_write_io_threads = %s", cfg.GetConfigInnoDBWriteIoThreads()))
	}
	return sql
}

// resizeCPUSQL builds the cores-driven SET GLOBALs. innodb_read_io_threads is
// sized from the core count and is dynamic on MariaDB (restart-only on MySQL).
func (server *ServerMonitor) resizeCPUSQL() []string {
	cfg := &server.ClusterGroup.Configurator
	if server.IsMariaDB() {
		return []string{fmt.Sprintf("SET GLOBAL innodb_read_io_threads = %s", cfg.GetConfigInnoDBReadIoThreads())}
	}
	return nil
}

// ResizeDynamicResources applies a live resource resize to every monitored server,
// gated by prov-db-dynamic-resource. It sequences the infra resize (orchestrator
// backend, via resourceResizer) and the DB resize (SET GLOBAL) anti-OOM:
//   - grow: check feasibility -> infra grow (must report applied) -> raise DB memory
//   - shrink: lower DB memory first -> infra shrink
//
// On a "no"/"migration" verdict, or when the infra could not resize live, the DB
// memory is not raised (never OOM). Restart-only SET GLOBALs (error 1238) fall
// back to a restart cookie.
func (cluster *Cluster) ResizeDynamicResources(dim resizeDimension, grow bool) {
	if !cluster.Conf.ProvDBDynamicResource {
		return
	}
	dir := "shrink"
	if grow {
		dir = "grow"
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
		"Live resource resize: %s %s (prov-db-dynamic-resource)", dim.String(), dir)

	for _, server := range cluster.Servers {
		if server == nil || server.State == stateFailed || server.State == stateUnconn {
			continue
		}
		// The live resize builds MySQL/MariaDB SET GLOBAL statements. PostgreSQL is
		// a different world (ALTER SYSTEM + pg_reload_conf, and shared_buffers is
		// restart-only): fall back to a restart so the regenerated config applies.
		// A live PG backend (work_mem / effective_cache_size) is a follow-up.
		if server.DBVersion != nil && server.DBVersion.IsPostgreSQL() {
			server.SetRestartCookie()
			continue
		}
		// IO and CPU tuning are pure DB SET GLOBALs — no cgroup change, no
		// feasibility gate, no grow/shrink ordering. Restart-only vars fall back
		// via error 1238. (The cgroup cpu limit resize is a follow-up, like pg_cpu.)
		if dim != resizeMemory {
			var sql []string
			switch dim {
			case resizeIO:
				sql = server.resizeIOSQL()
			case resizeCPU:
				sql = server.resizeCPUSQL()
			}
			if len(sql) > 0 {
				if _, needRestart := server.ExecScriptSQL(sql); needRestart {
					server.SetRestartCookie()
				}
			}
			cluster.logResize(server, dim, grow, true, ResizeYes, sql)
			continue
		}

		// resizeMemory: sequence the infra (cgroup) and the DB memory anti-OOM.
		rz := cluster.resourceResizer()
		if grow {
			feas, err := rz.CanResize(server, true)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
					"Resource grow feasibility check failed on %s: %s", server.URL, err)
				cluster.logResize(server, dim, true, false, feas, nil)
				continue
			}
			if feas == ResizeNo {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
					"Resource grow not possible on %s, keeping current size", server.URL)
				cluster.logResize(server, dim, true, false, feas, nil)
				continue
			}
			if feas == ResizeMigration {
				// Host lacks capacity: the instance would need relocating. In-place
				// resize is skipped; migration orchestration + tracked state (T5) is
				// a follow-up (#1765/#1760 §10).
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
					"Resource grow needs migration on %s (host lacks capacity); in-place skipped", server.URL)
				cluster.logResize(server, dim, true, false, feas, nil)
				continue
			}
			applied, err := rz.Resize(server, true)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
					"Resource grow on %s failed, keeping current DB memory: %s", server.URL, err)
				cluster.logResize(server, dim, true, false, feas, nil)
				continue
			}
			if !applied {
				cluster.logResize(server, dim, true, false, feas, nil) // native path scheduled a restart
				continue
			}
			sql := server.resizeMemorySQL(true)
			if _, needRestart := server.ExecScriptSQL(sql); needRestart {
				server.SetRestartCookie()
			}
			cluster.logResize(server, dim, true, true, feas, sql)
		} else {
			sql := server.resizeMemorySQL(false)
			if _, needRestart := server.ExecScriptSQL(sql); needRestart {
				server.SetRestartCookie()
			}
			applied := true
			if _, err := rz.Resize(server, false); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
					"Resource shrink on %s failed: %s", server.URL, err)
				applied = false
			}
			cluster.logResize(server, dim, false, applied, ResizeYes, sql)
		}
	}
}
