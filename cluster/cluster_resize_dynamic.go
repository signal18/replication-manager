// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

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
	// CanConfigResize answers whether the resize is possible: yes (in place), no (keep
	// current size), or migration (needs relocating the instance).
	CanConfigResize(server *ServerMonitor, grow bool) (ResizeFeasibility, error)
	// ConfigResize applies the infra resize live and reports whether it was applied.
	ConfigResize(server *ServerMonitor, grow bool) (bool, error)
}

// scriptResizer is the client-overridable backend (F7): used in every
// orchestrator case when prov-db-dynamic-resource-change-script is set.
type scriptResizer struct{ cluster *Cluster }

func (r scriptResizer) CanConfigResize(server *ServerMonitor, grow bool) (ResizeFeasibility, error) {
	return r.cluster.RunDynamicResourceCanChangeScript(server, grow)
}

func (r scriptResizer) ConfigResize(server *ServerMonitor, grow bool) (bool, error) {
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

func (r openSVCResizer) CanConfigResize(server *ServerMonitor, grow bool) (ResizeFeasibility, error) {
	return r.cluster.RunDynamicResourceCanChangeScript(server, grow)
}

func (r openSVCResizer) ConfigResize(server *ServerMonitor, grow bool) (bool, error) {
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

func (r restartResizer) CanConfigResize(server *ServerMonitor, grow bool) (ResizeFeasibility, error) {
	return r.cluster.RunDynamicResourceCanChangeScript(server, grow)
}

func (r restartResizer) ConfigResize(server *ServerMonitor, grow bool) (bool, error) {
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
// dynamicResizeWindowMinutes is how long the daily window stays open after
// prov-db-dynamic-resize-daily-time, so the driver can WAIT for an off-peak dbjob (backups run
// at the same hour) to finish before resizing, instead of skipping the day.
const dynamicResizeWindowMinutes = 60

// isDynamicResizeDailyWindowNow reports whether the server-local clock is within the daily
// window [prov-db-dynamic-resize-daily-time (HH:MM), + dynamicResizeWindowMinutes). The width
// gives the driver time to wait out a running job; the once-per-day guard (LastDynamicResizeDay)
// still applies it at most once. An empty/invalid time never matches.
func (cluster *Cluster) isDynamicResizeDailyWindowNow() bool {
	t, err := time.Parse("15:04", strings.TrimSpace(cluster.Conf.ProvDBDynamicResizeDailyTime))
	if err != nil {
		return false
	}
	now := time.Now()
	start := t.Hour()*60 + t.Minute()
	cur := now.Hour()*60 + now.Minute()
	return cur >= start && cur < start+dynamicResizeWindowMinutes
}

// anyServerRunningJobs reports whether a dbjob (backup, optimize, reseed, ...) is executing on
// any monitored server — the gate that keeps a live resize from colliding with a job.
func (cluster *Cluster) anyServerRunningJobs() bool {
	for _, s := range cluster.Servers {
		if s != nil && s.IsRunningJobs {
			return true
		}
	}
	return false
}

// dynamicMemoryResizeApplyAllowedNow gates the APPLY step of a live MEMORY resize on the
// timing policy. scale-speed (default): always allowed, apply immediately (its cadence is the
// prov-db-scale-*-speed timeframe upstream). daily-time: allowed only inside the daily window,
// so any InnoDB buffer-pool-resize stall is contained to an off-peak hour; outside the window
// the apply is skipped and DriveDailyDynamicResize re-applies it when the window opens.
// CPU/IO tuning never routes through here (no stall), so it is unaffected by the policy.
func (cluster *Cluster) dynamicMemoryResizeApplyAllowedNow() bool {
	if cluster.Conf.ProvDBDynamicResizePolicy != config.ConstResizePolicyDailyTime {
		return true
	}
	return cluster.isDynamicResizeDailyWindowNow()
}

// resourceManagerAllowsGrow is the ResourceManager AUTHORITY gate on a live grow (step 1 of
// wiring the resize to the pool). Growth WITHIN the plan is always allowed and skips the gate.
// Growth PAST the plan is gated on two things, in order: the commercial overcommit budget
// (CanGrowBeyondPlan, prov-db-overcommit-pct) and -- when the node's physical capacity is known
// -- the free pool on that node (the usable ceiling must cover the per-agent consumed plus the
// extra DBU this grow claims over the plan, so we never physically exceed the metal). Best
// effort: no manager, or unknown capacity, does not block. Returns allowed + a short reason.
func (cluster *Cluster) resourceManagerAllowsGrow(server *ServerMonitor) (bool, string) {
	if cluster.resources == nil {
		return true, ""
	}
	target := cluster.GetConfigDBUPerNode().Dbu // the config target, per node
	plan := cluster.GetPlanDBUPerNode().Dbu
	if target <= plan {
		return true, "" // within the contract -- always free, never gated
	}
	// Commercial budget: the overcommit ceiling the client accepted (plan × (1+pct/100)).
	if ok, reason := cluster.resources.CanGrowBeyondPlan(target, plan, cluster.Conf.ProvDBOvercommitPct); !ok {
		return false, reason
	}
	// Physical free pool on the node -- only gate when the agent capacity is known.
	if ceiling, ok := cluster.resources.UsableCeilingDBU(server.Agent); ok {
		used := cluster.resources.ConsumedByAgent(server.Agent).Dbu
		extra := target - plan
		if used+extra > ceiling {
			return false, fmt.Sprintf("no free pool on node %s: consumed %.2f + grow %.2f > usable %.2f DBU/node",
				server.Agent, used, extra, ceiling)
		}
	}
	return true, ""
}

// isMemoryResizeInFlight reports whether a live memory resize on this server has NOT yet
// converged, so a new one must not be stacked. Guards all three races: (1) an async InnoDB
// buffer-pool resize still running (runtime INNODB_BUFFER_POOL_SIZE not yet at the configured
// target, within a tolerance for chunk-size rounding), (2) a grow issued while a shrink's cgroup
// step is still pending (PendingCgroupShrink), (3) a second SET GLOBAL while the first is running.
// The gate turns the autonomous cooldown from "wait 1 minute" into "wait until the pool has
// actually reached its new size".
func (server *ServerMonitor) isMemoryResizeInFlight() bool {
	if server.PendingCgroupShrink {
		return true
	}
	cluster := server.ClusterGroup
	if cluster == nil {
		return false
	}
	targetMB, err := strconv.ParseInt(cluster.Configurator.GetConfigInnoDBBPSize(), 10, 64)
	if err != nil || targetMB <= 0 {
		return false
	}
	runtime, err := strconv.ParseInt(server.Variables.Get("INNODB_BUFFER_POOL_SIZE"), 10, 64)
	if err != nil || runtime <= 0 {
		return false
	}
	targetBytes := targetMB * 1024 * 1024
	diff := runtime - targetBytes
	if diff < 0 {
		diff = -diff
	}
	return diff > targetBytes/20 // >5% off target => still converging
}

func (cluster *Cluster) ResizeDynamicResources(dim resizeDimension, grow bool) {
	// Live resize is gated only by its own opt-in toggle (T14): when
	// prov-db-dynamic-resource is on, a resource change is applied live
	// (SET GLOBAL + orchestrator/script feasibility) instead of a container
	// recreation — with or without a service plan. The plan is only a
	// provisioning bootstrap and does not authorize/gate the resize.
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

		// resizeMemory timing policy: under daily-time, the live memory resize (the
		// stall-prone InnoDB buffer-pool part) is applied only inside the daily window.
		// Outside it, skip -- the config target is already set and DriveDailyDynamicResize
		// re-applies it when the window opens.
		if !cluster.dynamicMemoryResizeApplyAllowedNow() {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
				"Live memory resize on %s deferred to the daily window %s (prov-db-dynamic-resize-policy=daily-time)", server.URL, cluster.Conf.ProvDBDynamicResizeDailyTime)
			cluster.logResize(server, dim, grow, false, ResizeYes, nil)
			continue
		}

		// Gated by jobs execution: never resize memory while a dbjob (backup, optimize,
		// reseed, ...) is running on this server -- a live buffer-pool resize or cgroup
		// change under a heavy job risks OOM/contention and could fail the job. Skip; the
		// next trigger (scale-speed) or the daily driver (which waits for idle) retries.
		if server.IsRunningJobs {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
				"Live memory resize on %s deferred: a dbjob is running (gated by jobs execution)", server.URL)
			cluster.logResize(server, dim, grow, false, ResizeYes, nil)
			continue
		}

		// In-flight gate: never stack a memory resize on one that has not converged (async
		// InnoDB buffer-pool resize, or a pending cgroup shrink). Skip; the next tick retries
		// once the previous resize has actually reached its new size.
		if server.isMemoryResizeInFlight() {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
				"Live memory resize on %s deferred: a previous resize is still converging (in-flight gate)", server.URL)
			cluster.logResize(server, dim, grow, false, ResizeYes, nil)
			continue
		}

		// resizeMemory: sequence the infra (cgroup) and the DB memory anti-OOM.
		rz := cluster.resourceResizer()
		if grow {
			// ResourceManager authority (step 1): gate a grow PAST the plan on the
			// commercial budget + the node's physical free pool, BEFORE the infra
			// feasibility. A within-plan grow passes through untouched. A refusal here
			// means the client must raise the plan (the IsNeedResourceCapUp claim).
			if ok, reason := cluster.resourceManagerAllowsGrow(server); !ok {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
					"Resource grow on %s refused by ResourceManager: %s", server.URL, reason)
				cluster.logResize(server, dim, true, false, ResizeNo, nil)
				continue
			}
			feas, err := rz.CanConfigResize(server, true)
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
			applied, err := rz.ConfigResize(server, true)
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
			// Feasibility gate applies to shrink too: a can-change verdict of no/
			// migration must stop a live shrink (e.g. a maintenance window).
			feas, err := rz.CanConfigResize(server, false)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
					"Resource shrink feasibility check failed on %s: %s", server.URL, err)
				cluster.logResize(server, dim, false, false, feas, nil)
				continue
			}
			if feas == ResizeNo {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
					"Resource shrink not possible on %s, keeping current size", server.URL)
				cluster.logResize(server, dim, false, false, feas, nil)
				continue
			}
			if feas == ResizeMigration {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
					"Resource shrink needs migration on %s; in-place skipped", server.URL)
				cluster.logResize(server, dim, false, false, feas, nil)
				continue
			}
			// Phase 1: free DB memory (SET GLOBAL, buffer pool down). InnoDB resizes
			// the pool ASYNCHRONOUSLY, so we DEFER the cgroup shrink — phase 2
			// (completePendingCgroupShrink, on a later monitor tick) shrinks the
			// cgroup only once the pool has actually shrunk, so we never lower the
			// cgroup below live memory (OOM). Non-blocking: a per-tick comparison.
			sql := server.resizeMemorySQL(false)
			if _, needRestart := server.ExecScriptSQL(sql); needRestart {
				server.SetRestartCookie()
			}
			server.PendingCgroupShrink = true
			// applied=false: the DB side is done but the cgroup shrink is deferred.
			cluster.logResize(server, dim, false, false, feas, sql)
		}
	}
}

// completePendingCgroupShrink is phase 2 of a live memory shrink. Phase 1 lowered
// the buffer pool via SET GLOBAL; InnoDB resizes it asynchronously, so this shrinks
// the container cgroup only once the pool has actually reached its target — never
// below live memory (anti-OOM). Called every monitor tick; when nothing is pending
// it is a single comparison, so it does not burden the monitor (F2).
func (cluster *Cluster) completePendingCgroupShrink(server *ServerMonitor) {
	if server == nil || !server.PendingCgroupShrink {
		return
	}
	if server.State == stateFailed || server.State == stateUnconn {
		return
	}
	targetMB, err := strconv.ParseInt(server.ClusterGroup.Configurator.GetConfigInnoDBBPSize(), 10, 64)
	if err != nil {
		server.PendingCgroupShrink = false
		return
	}
	runtime, _ := strconv.ParseInt(server.Variables.Get("INNODB_BUFFER_POOL_SIZE"), 10, 64)
	if runtime > targetMB*1024*1024 {
		return // buffer pool still shrinking asynchronously — wait for the next tick
	}
	// The pool has reached its target: it is now safe to shrink the cgroup.
	server.PendingCgroupShrink = false
	applied, rerr := cluster.resourceResizer().ConfigResize(server, false)
	if rerr != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
			"Deferred cgroup shrink on %s failed: %s", server.URL, rerr)
	}
	cluster.logResize(server, resizeMemory, false, applied, ResizeYes, nil)
}

// DriveDailyDynamicResize is the daily-time policy's per-tick driver (called from SetStatus).
// Under prov-db-dynamic-resize-policy=daily-time it reconciles live memory to the provisioned
// target ONCE per day, when the server-local clock reaches prov-db-dynamic-resize-daily-time,
// so a resize that came due during the day (or was deferred by the apply gate) lands in the
// off-peak window instead of at an arbitrary moment. No-op for scale-speed, off outside the
// window, and at most one apply per day (LastDynamicResizeDay).
func (cluster *Cluster) DriveDailyDynamicResize() {
	if !cluster.Conf.ProvDBDynamicResource || cluster.Conf.ProvDBDynamicResizePolicy != config.ConstResizePolicyDailyTime {
		return
	}
	if !cluster.isDynamicResizeDailyWindowNow() {
		return
	}
	today := time.Now().Format("2006-01-02")
	if cluster.LastDynamicResizeDay == today {
		return // already reconciled in today's window
	}
	grow, due := cluster.dynamicMemoryResizeDue()
	if !due {
		cluster.LastDynamicResizeDay = today // nothing to reconcile today; done
		return
	}
	// A resize is due. Gated by jobs execution: wait for any running dbjob (a backup often
	// runs at this same off-peak hour) to finish before applying -- do NOT mark the day done,
	// so we retry on the next tick within the window and land at the first idle moment.
	if cluster.anyServerRunningJobs() {
		return
	}
	cluster.LastDynamicResizeDay = today
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
		"Daily-time window (%s), servers idle: reconciling live memory to the provisioned target", cluster.Conf.ProvDBDynamicResizeDailyTime)
	cluster.ResizeDynamicResources(resizeMemory, grow)
}

// dynamicMemoryResizeDue reports whether any up server's live InnoDB buffer pool differs from
// the provisioned target (prov-db-memory -> GetConfigInnoDBBPSize, in MB), and the direction.
// Lets the daily driver skip a no-op reconcile and decide grow vs shrink.
func (cluster *Cluster) dynamicMemoryResizeDue() (grow bool, due bool) {
	targetMB, err := strconv.ParseInt(cluster.Configurator.GetConfigInnoDBBPSize(), 10, 64)
	if err != nil {
		return false, false
	}
	for _, s := range cluster.Servers {
		if s == nil || s.State == stateFailed || s.State == stateUnconn {
			continue
		}
		liveBytes, _ := strconv.ParseInt(s.Variables.Get("INNODB_BUFFER_POOL_SIZE"), 10, 64)
		liveMB := liveBytes / (1024 * 1024)
		if liveMB != targetMB {
			return targetMB > liveMB, true
		}
	}
	return false, false
}

// DriveAutonomousResize is the AUTONOMOUS trigger for the dynamic resource resize: it turns a
// sustained saturation SIGNAL into an actual resize, closing the loop (saturation -> state ->
// resize) instead of only observing. Gated by the SAME prov-db-dynamic-resource switch -- the
// dynamic resize IS autonomous by design, there is no separate opt-in. Called per monitor tick
// from SetStatus.
//
// Scope (first cut): in-plan MEMORY grow -- memory is the one axis with a live cgroup resize
// (pg_mem_limit), and its trigger is now buffer-pool PRESSURE (checkBufferPoolPressure / #1), not
// occupancy. When any server has a sustained in-plan mem grow due (CanScaleConfigInPlan(true)),
// raise the cluster prov-db-memory by +1 DBU step toward the per-node plan ceiling
// (GetDBContainerMemoryCapMB); headroom lives in the buffer-pool being a fraction of prov-db-memory.
// The apply goes through SetDBMemorySize -> ResizeDynamicResources (already ResourceManager- and
// jobs-gated, anti-OOM ordered). One step per scale-up window (cooldown); skipped in failover.
// dynamicResizeQPSMarginPct is the throughput improvement a memory step must buy
// to count as "memory still helps". Below this, the last memory grow is treated as
// a plateau and the hill-climb escalates to IOPS (the genuine-IO bottleneck).
const dynamicResizeQPSMarginPct = 5.0

// currentClusterQPS returns a per-cycle query rate proxy for the cluster, taken from
// the master's Queries counter delta (GetStatusDeltaValue is already per monitor tick).
// It is a proxy, not an absolute QPS: only before/after comparisons over equal-length
// cycles are meaningful, which is exactly what the hill-climb needs.
func (cluster *Cluster) currentClusterQPS() float64 {
	m := cluster.GetMaster()
	if m == nil {
		return 0
	}
	return float64(m.GetStatusDeltaValue("QUERIES"))
}

// DriveAutonomousResize is the QPS-driven in-plan escalation. It grows the axis the
// database is actually binding on, one +1 DBU step per scale-up window, using QPS as
// the objective so the DB tells us which resource is short instead of us guessing:
//
//	Priority 1  CPU  -- if cores are pinned, nothing else moves QPS; grow CPU.
//	Priority 2  MEM  -- the first throughput lever: a bigger buffer pool cuts BOTH
//	                    read misses (disk reads) and forced eviction (disk writes),
//	                    so IO pressure is usually relieved by memory, not IOPS. Keep
//	                    growing memory while each step buys >dynamicResizeQPSMarginPct QPS.
//	Priority 3  IOPS -- the last resort: taken only once another memory step stops
//	                    improving QPS (the working set now fits / the cache is no
//	                    longer the bottleneck), i.e. the IO is genuine, not eviction.
//
// Every step is bounded by the per-node plan ceiling (in-plan only; a plan cap-up is a
// separate commercial decision). Gated by prov-db-dynamic-resource; never runs during a
// failover, never stacks on an unconverged memory resize.
func (cluster *Cluster) DriveAutonomousResize() {
	if !cluster.Conf.ProvDBDynamicResource || cluster.IsInFailover() {
		return
	}
	d, err := time.ParseDuration(cluster.Conf.ScaleUpConfigInPlanSpeed)
	if err != nil || d < time.Minute {
		d = time.Minute
	}
	if !cluster.lastAutonomousResize.IsZero() && time.Since(cluster.lastAutonomousResize) < d {
		return // cooldown: at most one +1 DBU step per scale-up window (also the QPS-feedback window)
	}
	// Observe which axes are saturated against config, cluster-wide. Also honour the
	// in-flight gate: never start another step while a memory resize is still converging.
	cpuDue, memDue, ioDue := false, false, false
	for _, s := range cluster.Servers {
		if s == nil {
			continue
		}
		if s.isMemoryResizeInFlight() {
			return
		}
		for _, a := range s.CanScaleConfigInPlan(true) {
			switch a {
			case "cpu":
				cpuDue = true
			case "mem":
				memDue = true
			case "io":
				ioDue = true
			}
		}
	}
	if !cpuDue && !memDue && !ioDue {
		cluster.lastAutonomousGrowAxis = "" // nothing constrained: reset the hill-climb memory
		return
	}

	qps := cluster.currentClusterQPS()

	// Priority 1 -- CPU is binding: neither memory nor IOPS moves QPS while cores are pinned.
	if cpuDue {
		if cluster.growAxisInPlan("cpu", qps) {
			return
		}
		// CPU already at the plan ceiling: fall through -- a memory/IO step may still help.
	}

	// Priority 2/3 -- throughput-limited. Memory first; IOPS only on a memory plateau.
	escalateToIO := false
	if cluster.lastAutonomousGrowAxis == "mem" {
		threshold := cluster.qpsBeforeAutonomousGrow * (1.0 + dynamicResizeQPSMarginPct/100.0)
		if qps <= threshold {
			escalateToIO = true // the previous memory step did not buy throughput -> genuine IO
		}
	}
	if !escalateToIO && (memDue || ioDue) {
		if cluster.growAxisInPlan("mem", qps) {
			return
		}
		escalateToIO = ioDue // memory exhausted at the plan ceiling: escalate to IOPS
	}
	if escalateToIO && ioDue {
		cluster.growAxisInPlan("io", qps)
	}
}

// growAxisInPlan raises one config axis by +1 DBU, clamped to the per-node plan ceiling,
// records it as the last autonomous grow (with the pre-grow QPS for the next window's
// plateau check), and applies it live via the axis setter. Returns false without acting
// when the axis is already at its plan ceiling (in-plan grow exhausted). NOTE: memory is
// physically effective now (buffer-pool grow); cpu/io currently only re-tune SET GLOBAL
// vars -- the container cpu/io cgroup resize (pg_cpus / io weight) is the follow-up that
// makes those steps add real capacity.
func (cluster *Cluster) growAxisInPlan(axis string, qps float64) bool {
	planPerNode := cluster.GetPlanDBUPerNode().Dbu
	switch axis {
	case "mem":
		curMB, _ := config.ParseUnitMeasurementToInt("M,bytes,required", cluster.Conf.ProvMem, true)
		ceilMB := cluster.GetDBContainerMemoryCapMB()
		if int(curMB) >= ceilMB {
			return false
		}
		newMB := int(curMB) + 4096 // +1 DBU of memory
		if newMB > ceilMB {
			newMB = ceilMB
		}
		cluster.recordAutonomousGrow("mem", qps)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"Autonomous in-plan MEMORY grow (first throughput lever, relieves read misses + eviction): prov-db-memory %dMB -> %dMB (ceiling %dMB)", curMB, newMB, ceilMB)
		cluster.SetDBMemorySize(strconv.Itoa(newMB))
		return true
	case "cpu":
		cur, _ := strconv.Atoi(cluster.Conf.ProvCores)
		ceil := int(math.Floor(planPerNode)) // 1 core per DBU
		if ceil < 1 {
			ceil = 1
		}
		if cur >= ceil {
			return false
		}
		newC := cur + 1
		if newC > ceil {
			newC = ceil
		}
		cluster.recordAutonomousGrow("cpu", qps)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"Autonomous in-plan CPU grow (cores pinned, cpu-bound): prov-db-cpu-cores %d -> %d (ceiling %d)", cur, newC, ceil)
		cluster.SetDBCores(strconv.Itoa(newC))
		return true
	case "io":
		cur, _ := strconv.Atoi(cluster.Conf.ProvIops)
		ceil := int(planPerNode * 1000) // 1000 IOPS per DBU
		if cur >= ceil {
			return false
		}
		newI := cur + 1000 // +1 DBU of IOPS
		if newI > ceil {
			newI = ceil
		}
		cluster.recordAutonomousGrow("io", qps)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"Autonomous in-plan IO grow (memory plateaued, genuine IO bottleneck): prov-db-disk-iops %d -> %d (ceiling %d)", cur, newI, ceil)
		cluster.SetDBDiskIOPS(strconv.Itoa(newI))
		return true
	}
	return false
}

func (cluster *Cluster) recordAutonomousGrow(axis string, qps float64) {
	cluster.lastAutonomousResize = time.Now()
	cluster.lastAutonomousGrowAxis = axis
	cluster.qpsBeforeAutonomousGrow = qps
}
