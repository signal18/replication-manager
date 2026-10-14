// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// Reasons recorded in the reprov cookie (#1969). A reprovision recreates the volume and
// relies on the backup restore: the reason tells, once prov-db-dynamic-resource is on,
// whether the change can be applied live instead (resource reasons) or really needs the
// volume recreated (all the others).
const (
	ReprovReasonMemory        = "memory"
	ReprovReasonCores         = "cores"
	ReprovReasonDiskSize      = "disk-size"
	ReprovReasonImage         = "image"
	ReprovReasonXtrabackupImg = "xtrabackup-image"
	ReprovReasonIdentity      = "identity"
	ReprovReasonDiskType      = "disk-type"
	ReprovReasonDiskFS        = "disk-fs"
	ReprovReasonDiskPool      = "disk-pool"
	ReprovReasonDiskDevice    = "disk-device"
	ReprovReasonServiceType   = "service-type"
	ReprovReasonStorageClass  = "storage-class"
)

// reprovResourceReasons are the reasons the live resize engine applies without a
// reprovision once prov-db-dynamic-resource is on.
var reprovResourceReasons = []string{ReprovReasonMemory, ReprovReasonCores, ReprovReasonDiskSize}

// reprovCookieMu serialises every read-modify-write of a reprov cookie: setters run from
// API calls, the monitor and the reconcile at the same time (#1970 review).
var reprovCookieMu sync.Mutex

func (server *ServerMonitor) reprovCookiePath() string {
	return server.Datadir + "/@cookie_reprov"
}

// readReprovReasons returns the recorded reasons and whether the cookie exists. Callers
// hold reprovCookieMu.
func (server *ServerMonitor) readReprovReasons() ([]string, bool) {
	b, err := os.ReadFile(server.reprovCookiePath())
	if err != nil {
		return nil, false
	}
	reasons := make([]string, 0)
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !slices.Contains(reasons, l) {
			reasons = append(reasons, l)
		}
	}
	return reasons, true
}

// writeReprovReasons replaces the cookie with exactly these reasons in one rename, so a
// check never sees a window without the cookie; no reason removes it. Callers hold
// reprovCookieMu.
func (server *ServerMonitor) writeReprovReasons(reasons []string) error {
	if len(reasons) == 0 {
		if err := os.Remove(server.reprovCookiePath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	// No "@" prefix: the temporary file is never mistaken for a cookie.
	tmp := server.Datadir + "/.cookie_reprov.tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(reasons, "\n")+"\n"), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, server.reprovCookiePath())
}

// SetReprovCookieFor sets the reprov cookie and records why, one reason per line. The
// cookie stays the same file, so every HasReprovCookie check is unchanged. A cookie
// written before the reasons were recorded gets its legacy meaning (memory, cores)
// spelled out first, so adding a reason never drops it.
func (server *ServerMonitor) SetReprovCookieFor(reason string) error {
	reprovCookieMu.Lock()
	defer reprovCookieMu.Unlock()
	reasons, exists := server.readReprovReasons()
	if exists && len(reasons) == 0 {
		reasons, _ = splitReprovReasons(nil)
	}
	if exists && slices.Contains(reasons, reason) {
		return nil
	}
	err := server.writeReprovReasons(append(reasons, reason))
	if err != nil {
		if cluster := server.ClusterGroup; cluster != nil && cluster.Conf != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Reprovision cookie (%s) not recorded on %s: %s", reason, server.URL, err)
		}
	}
	return err
}

// ReprovCookieReasons returns the reasons recorded in the reprov cookie. Empty for no
// cookie, and for a cookie written before the reasons were recorded.
func (server *ServerMonitor) ReprovCookieReasons() []string {
	reprovCookieMu.Lock()
	defer reprovCookieMu.Unlock()
	reasons, _ := server.readReprovReasons()
	return reasons
}

// keepReprovReasons rewrites the cookie with only the reasons that keep the
// reprovision, and returns the live ones it cancelled. Read and rewrite happen under
// one lock, so a reason set meanwhile is never lost.
func (server *ServerMonitor) keepReprovReasons(dropLive func(live []string) []string) (cancelled []string, kept []string, err error) {
	reprovCookieMu.Lock()
	defer reprovCookieMu.Unlock()
	reasons, exists := server.readReprovReasons()
	if !exists {
		return nil, nil, nil
	}
	live, keep := splitReprovReasons(reasons)
	cancelled = dropLive(live)
	for _, r := range live {
		if !slices.Contains(cancelled, r) {
			keep = append(keep, r)
		}
	}
	return cancelled, keep, server.writeReprovReasons(keep)
}

// splitReprovReasons separates the reasons the live engine can apply from those that
// keep the reprovision. A cookie without reasons predates them: every setter that wrote
// one then was a resize, an image or a volume change, and it is treated as memory and
// cores (the restart marker of the same call, if any, stays).
func splitReprovReasons(reasons []string) (live []string, keep []string) {
	if len(reasons) == 0 {
		return []string{ReprovReasonMemory, ReprovReasonCores}, nil
	}
	for _, r := range reasons {
		if slices.Contains(reprovResourceReasons, r) {
			live = append(live, r)
		} else {
			keep = append(keep, r)
		}
	}
	return live, keep
}

// liveResizable reports whether the live resize engine reaches this server: the memory
// and cores paths skip failed and unconnected servers, the disk path down ones.
func (server *ServerMonitor) liveResizable() bool {
	return server.State != stateUnconn && !server.IsDown()
}

// ReconcileReprovWithDynamicResource runs when prov-db-dynamic-resource is switched on,
// and again from the monitor while a reachable server still holds a resource reason: the
// pending resource reprovisions are applied live instead, the least intrusive way the
// release allows (the engine falls back to a restart cookie by itself where it cannot,
// error 1238, redo log, PostgreSQL, OpenSVC v2, and re-records disk-size where the
// orchestrator has no live disk resize). The other reasons keep the reprovision.
//
// A reason is cancelled only on a server the engine reaches: a server down at the start
// keeps its cookie untouched, one lost during the run gets its reasons back, so a pending
// change never vanishes (#1970 review). One run at a time per cluster; a run already in
// progress makes this a no-op, the monitor retries.
func (cluster *Cluster) ReconcileReprovWithDynamicResource() {
	if !cluster.reprovReconcileMu.TryLock() {
		return
	}
	defer cluster.reprovReconcileMu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
				"Reprovision reconcile with prov-db-dynamic-resource aborted: %v", r)
		}
	}()
	if !cluster.Conf.ProvDBDynamicResource {
		return
	}
	diskGB, diskErr := config.ParseUnitMeasurementToInt("G,bytes,required", cluster.Conf.ProvDisk, true)
	var memory, cores, disk bool
	cancelled := make(map[*ServerMonitor][]string)
	for _, s := range cluster.Servers {
		if s == nil || !s.HasReprovCookie() || !s.liveResizable() {
			continue
		}
		live, keep, err := s.keepReprovReasons(func(live []string) []string {
			// An unreadable prov-db-disk-size cannot be applied live: the reason stays.
			if diskErr != nil {
				return slices.DeleteFunc(slices.Clone(live), func(r string) bool { return r == ReprovReasonDiskSize })
			}
			return live
		})
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
				"Reprovision of %s kept: cookie not rewritten: %s", s.URL, err)
			continue
		}
		if len(live) == 0 {
			continue
		}
		cancelled[s] = live
		memory = memory || slices.Contains(live, ReprovReasonMemory)
		cores = cores || slices.Contains(live, ReprovReasonCores)
		disk = disk || slices.Contains(live, ReprovReasonDiskSize)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"Reprovision of %s: %v applied live (prov-db-dynamic-resource), kept: %v", s.URL, live, keep)
	}
	if cores {
		if grow, due := cluster.dynamicCPUResizeDue(); due {
			cluster.ResizeDynamicResources(resizeCPU, grow)
		}
	}
	if memory {
		if grow, due := cluster.dynamicMemoryResizeDue(); due {
			cluster.ResizeDynamicResources(resizeMemory, grow)
		}
	}
	if disk {
		// Grow only, to the declared size: the orchestrator refuses a shrink (WARN0220).
		cluster.applyDiskResize(0, int(diskGB))
	}
	// A server lost during the run was skipped by the engine: its change is still pending.
	for s, live := range cancelled {
		if s.liveResizable() {
			continue
		}
		for _, r := range live {
			s.SetReprovCookieFor(r)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"Reprovision of %s: %v restored, the server went down during the live resize (retried when back)", s.URL, live)
	}
	cluster.IsNeedDatabasesReprov = cluster.HasRequestDBReprov()
}

// dynamicCPUResizeDue compares the live cores-driven variable (innodb_read_io_threads)
// with the configurator target, as dynamicMemoryResizeDue does for the buffer pool, and
// gives the direction. Without a readable value on any server (MySQL, Percona) the step
// is due as a grow: the cgroup cap it sets is absolute, the direction only orders the
// Kubernetes patch and labels the resize log.
func (cluster *Cluster) dynamicCPUResizeDue() (grow bool, due bool) {
	target, err := strconv.ParseInt(cluster.Configurator.GetConfigInnoDBReadIoThreads(), 10, 64)
	if err != nil {
		return true, true
	}
	read := false
	for _, s := range cluster.Servers {
		if s == nil || s.State == stateFailed || s.State == stateUnconn || s.Variables == nil {
			continue
		}
		live, err := strconv.ParseInt(s.Variables.Get("INNODB_READ_IO_THREADS"), 10, 64)
		if err != nil {
			continue
		}
		read = true
		if live != target {
			return target > live, true
		}
	}
	if !read {
		return true, true
	}
	return false, false
}

// SetProvDBDynamicResource sets prov-db-dynamic-resource; turning it on reconciles the
// pending resource reprovisions (off the API call: a live resize can take a while). Setting
// the current value is a no-op, so the remediation link is idempotent (#1970 review).
func (cluster *Cluster) SetProvDBDynamicResource(on bool) {
	if cluster.Conf.ProvDBDynamicResource == on {
		return
	}
	cluster.Conf.ProvDBDynamicResource = on
	if on {
		go cluster.ReconcileReprovWithDynamicResource()
	}
}

// SwitchProvDBDynamicResource toggles prov-db-dynamic-resource (Settings page switch).
func (cluster *Cluster) SwitchProvDBDynamicResource() {
	cluster.SetProvDBDynamicResource(!cluster.Conf.ProvDBDynamicResource)
}

// reprovResourceServers lists the servers whose pending reprovision holds a reason the
// live engine could apply.
func (cluster *Cluster) reprovResourceServers() []*ServerMonitor {
	servers := make([]*ServerMonitor, 0)
	for _, s := range cluster.Servers {
		if s == nil || !s.HasReprovCookie() {
			continue
		}
		if live, _ := splitReprovReasons(s.ReprovCookieReasons()); len(live) > 0 {
			servers = append(servers, s)
		}
	}
	return servers
}

// checkReprovDynamicResource runs with the config states. Off: it opens WARN0234 while a
// resource reprovision is pending (switching it on applies the change live and cancels
// the reprovision, the remediation of GetConfigRemediationPlan). On: a resource reason
// still held by a reachable server (down at the last reconcile) is reconciled now.
func (cluster *Cluster) checkReprovDynamicResource() {
	servers := cluster.reprovResourceServers()
	if len(servers) == 0 {
		return
	}
	if !cluster.Conf.ProvDBDynamicResource {
		urls := make([]string, 0, len(servers))
		for _, s := range servers {
			urls = append(urls, s.URL)
		}
		cluster.ConfigStateMachine.AddState("WARN0234", state.State{ErrType: "WARNING", ErrKey: "WARN0234", ErrFrom: "CONF",
			ErrDesc: fmt.Sprintf(clusterError["WARN0234"], strings.Join(urls, ", "))})
		return
	}
	if slices.ContainsFunc(servers, (*ServerMonitor).liveResizable) {
		go cluster.ReconcileReprovWithDynamicResource()
	}
}

// GetConfigRemediationPlan builds the remediation plan for open config states.
func (cluster *Cluster) GetConfigRemediationPlan() RemediationPlan {
	plan := RemediationPlan{Cluster: cluster.Name, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Remediations: make([]RemediationEntry, 0)}
	for _, st := range cluster.ConfigStates {
		if st.ErrKey == "ERR00116" {
			if entry, ok := cluster.systemTablesRemediation(st); ok {
				plan.Remediations = append(plan.Remediations, entry)
			}
			continue
		}
		if st.ErrKey != "WARN0234" {
			continue
		}
		plan.Remediations = append(plan.Remediations, RemediationEntry{
			ErrKey:      st.ErrKey,
			Description: st.ErrDesc,
			AutoFixable: true,
			Fixes: []RemediationFix{{
				// A set, not a switch: a stale page or a double click never turns it off.
				Type:        "settings_set",
				Description: "Enable prov-db-dynamic-resource: apply the resize live and cancel the reprovision",
				Risk:        "moderate",
				URL:         fmt.Sprintf("clusters/%s/settings/actions/set/prov-db-dynamic-resource/true", cluster.Name),
			}},
		})
	}
	plan.OpenFindings = len(cluster.ConfigStates)
	return plan
}
