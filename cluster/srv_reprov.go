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
	"fmt"
	"os"
	"slices"
	"strings"
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

// SetReprovCookieFor sets the reprov cookie and records why, one reason per line. The
// cookie stays the same file, so every HasReprovCookie check is unchanged.
func (server *ServerMonitor) SetReprovCookieFor(reason string) error {
	if slices.Contains(server.ReprovCookieReasons(), reason) {
		return nil
	}
	f, err := os.OpenFile(server.Datadir+"/@cookie_reprov", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		if cluster := server.ClusterGroup; cluster != nil && cluster.Conf != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Create cookie (cookie_reprov) %s", err)
		}
		return err
	}
	defer f.Close()
	_, err = f.WriteString(reason + "\n")
	return err
}

// ReprovCookieReasons returns the reasons recorded in the reprov cookie. Empty for no
// cookie, and for a cookie written before the reasons were recorded.
func (server *ServerMonitor) ReprovCookieReasons() []string {
	b, err := os.ReadFile(server.Datadir + "/@cookie_reprov")
	if err != nil {
		return nil
	}
	reasons := make([]string, 0)
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			reasons = append(reasons, l)
		}
	}
	return reasons
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

// ReconcileReprovWithDynamicResource runs when prov-db-dynamic-resource is switched on: the
// pending resource reprovisions are applied live instead, the least intrusive way the
// release allows (the engine falls back to a restart cookie by itself where it cannot,
// error 1238, redo log, PostgreSQL, OpenSVC v2). The other reasons keep the reprovision.
func (cluster *Cluster) ReconcileReprovWithDynamicResource() {
	if !cluster.Conf.ProvDBDynamicResource {
		return
	}
	var memory, cores, disk bool
	for _, s := range cluster.Servers {
		if s == nil || !s.HasReprovCookie() {
			continue
		}
		live, keep := splitReprovReasons(s.ReprovCookieReasons())
		memory = memory || slices.Contains(live, ReprovReasonMemory)
		cores = cores || slices.Contains(live, ReprovReasonCores)
		disk = disk || slices.Contains(live, ReprovReasonDiskSize)
		s.DelReprovisionCookie()
		for _, r := range keep {
			s.SetReprovCookieFor(r)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"Reprovision of %s: %v applied live (prov-db-dynamic-resource), kept: %v", s.URL, live, keep)
	}
	if cores {
		cluster.ResizeDynamicResources(resizeCPU, true)
	}
	if memory {
		if grow, due := cluster.dynamicMemoryResizeDue(); due {
			cluster.ResizeDynamicResources(resizeMemory, grow)
		}
	}
	if disk {
		// Grow only, to the declared size: the orchestrator refuses a shrink (WARN0220).
		if gb, err := config.ParseUnitMeasurementToInt("G,bytes,required", cluster.Conf.ProvDisk, true); err == nil {
			cluster.applyDiskResize(0, int(gb))
		}
	}
	cluster.IsNeedDatabasesReprov = cluster.HasRequestDBReprov()
}

// SwitchProvDBDynamicResource toggles prov-db-dynamic-resource; switching it on reconciles
// the pending resource reprovisions (off the API call: a live resize can take a while).
func (cluster *Cluster) SwitchProvDBDynamicResource() {
	cluster.Conf.ProvDBDynamicResource = !cluster.Conf.ProvDBDynamicResource
	if cluster.Conf.ProvDBDynamicResource {
		go cluster.ReconcileReprovWithDynamicResource()
	}
}

// reprovResourceServers lists the servers whose pending reprovision holds a reason the
// live engine could apply.
func (cluster *Cluster) reprovResourceServers() []string {
	urls := make([]string, 0)
	for _, s := range cluster.Servers {
		if s == nil || !s.HasReprovCookie() {
			continue
		}
		if live, _ := splitReprovReasons(s.ReprovCookieReasons()); len(live) > 0 {
			urls = append(urls, s.URL)
		}
	}
	return urls
}

// assertReprovDynamicConfigState opens WARN0234 while a resource reprovision is pending
// and prov-db-dynamic-resource is off: switching it on applies the change live and cancels
// the reprovision (the remediation of GetConfigRemediationPlan).
func (cluster *Cluster) assertReprovDynamicConfigState() {
	if cluster.Conf.ProvDBDynamicResource {
		return
	}
	if urls := cluster.reprovResourceServers(); len(urls) > 0 {
		cluster.ConfigStateMachine.AddState("WARN0234", state.State{ErrType: "WARNING", ErrKey: "WARN0234", ErrFrom: "CONF",
			ErrDesc: fmt.Sprintf(clusterError["WARN0234"], strings.Join(urls, ", "))})
	}
}

// GetConfigRemediationPlan builds the remediation plan for open config states.
func (cluster *Cluster) GetConfigRemediationPlan() RemediationPlan {
	plan := RemediationPlan{Cluster: cluster.Name, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Remediations: make([]RemediationEntry, 0)}
	for _, st := range cluster.ConfigStates {
		if st.ErrKey != "WARN0234" {
			continue
		}
		plan.Remediations = append(plan.Remediations, RemediationEntry{
			ErrKey:      st.ErrKey,
			Description: st.ErrDesc,
			AutoFixable: true,
			Fixes: []RemediationFix{{
				Type:        "settings_switch",
				Description: "Enable prov-db-dynamic-resource: apply the resize live and cancel the reprovision",
				Risk:        "moderate",
				URL:         fmt.Sprintf("clusters/%s/settings/actions/switch/prov-db-dynamic-resource", cluster.Name),
			}},
		})
	}
	plan.OpenFindings = len(cluster.ConfigStates)
	return plan
}
