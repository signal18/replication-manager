// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
)

// PlanUnit is the service-plan unit a ChangePlanUnits call targets. The plan is a TECHNICAL
// resource RESERVATION contract (not a billing thing): a client-set cap that reserves fleet
// physical capacity; the provisioned resource (prov-db-* / prov-proxy·app-*) lives UNDER it.
type PlanUnit string

const (
	PlanUnitDBU PlanUnit = "DBU" // database reservation  -> prov-service-plan-dbu
	PlanUnitAPU PlanUnit = "APU" // compute reservation   -> prov-service-plan-apu
	// PlanUnitBKU / PlanUnitNEU (backup / network) -- wire when their plan variable lands.
)

// ChangePlanUnits moves this cluster's reservation for ONE unit by a relative delta. The
// SIGN carries the direction, so there is a single method for db/app/proxy (no per-unit
// duplication -- T2/T7); the only per-unit-varying data is the {current, floor, setter}
// triple in planUnitSpec.
//
//	ADMIN LOCK (both directions) -> if the plan variable is pinned immutable (/etc) for this
//	              cluster it is admin-controlled and FROZEN: neither a client increase nor a
//	              client give-back may move it. Checked FIRST, before anything is written.
//	delta < 0  -> DECREASE (unlocked): allowed, clamped at the per-unit floor, applied + persisted.
//	delta > 0  -> INCREASE (unlocked, a "claim"): the external plan-claim script may still refuse
//	              it; the fleet-pool overcommit is TRACKED as GWARN016, never hard-blocked.
//
// It is validation + hooking only. The plan is a client-set config variable; the dynamic
// config manager persists it (SaveConfig) and the resource keeps living under the cap.
func (cluster *Cluster) ChangePlanUnits(unit PlanUnit, delta int) error {
	if delta == 0 {
		return nil
	}
	cur, floor, apply, err := cluster.planUnitSpec(unit)
	if err != nil {
		return err
	}
	target := cur + delta
	if target < floor {
		target = floor // a decrease never drops below the minimum reservation
	}
	if target == cur {
		return nil
	}

	// The admin lock freezes the reservation in BOTH directions: a plan variable pinned in the
	// immutable /etc config is admin-controlled, so neither a client increase nor a give-back may
	// move it. Refuse BEFORE writing anything -- otherwise the dynamic overwrite layer would
	// silently win over the lock (the bug that let a decrease bypass a locked plan).
	if !cluster.CanPlanChange(unit) {
		return fmt.Errorf("plan %s is admin-locked (immutable) for this cluster; the reservation cannot be changed", unit)
	}
	if target > cur { // increase = a claim -> the external hook may still refuse it
		if err := cluster.RunPlanClaimScript(unit, cur, target); err != nil {
			return fmt.Errorf("plan increase refused for %s by external claim script: %w", unit, err)
		}
	}

	apply(target)                                    // move the reservation variable (the contract)
	cluster.ConfigManager.SaveConfig(cluster, false) // persist via the dynamic config manager
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo,
		"Plan reservation %s changed %d -> %d (delta %+d)", unit, cur, target, delta)

	// The reservation moved; now make the RESOURCE follow it. Back-driven and centralized
	// here (never in the frontend): the caller only ever sends a signed delta.
	cluster.applyPlanResourceFollow(unit, cur, target)
	return nil
}

// applyPlanResourceFollow makes the provisioned resource follow a plan change. Whether a plan
// change also moves the resource is a method decision backed by a variable (the per-domain
// dynamic-resource flag) -- not a frontend concern:
//
//	FOLLOW-PLAN mode (dynamic-resource OFF -- the common/on-premise case): the resource tracks
//	    the reservation 1:1, BOTH directions -- align it to the new unit.
//	DYNAMIC mode (ON -- e.g. many DB instances packed on one host where we cannot hand each its
//	    full reservation): the resize loop OWNS the resource UNDER the cap. A plan increase only
//	    lifts the ceiling (DriveDynamicResize grows into it on demand); a plan decrease that
//	    drops the cap below the live resource is reconciled by the resize loop. We deliberately
//	    do NOT "align to cap" here: in dynamic mode the live resource legitimately sits below the
//	    cap, so setting it to the cap would GROW it -- the opposite of intent.
//	    TODO(dynamic-decrease): force-clamp the live resource to the new cap here if the resize
//	    loop's reconcile latency proves too slow.
//
// The per-dimension setters it calls already branch on the same variable internally (live resize
// vs reprovision), so this only decides WHETHER to align, not HOW.
func (cluster *Cluster) applyPlanResourceFollow(unit PlanUnit, cur, target int) {
	if cluster.resources == nil {
		return
	}
	switch PlanUnit(strings.ToUpper(string(unit))) {
	case PlanUnitDBU:
		if !cluster.Conf.ProvDBDynamicResource {
			cluster.alignDBResourceToPlan(target)
		}
	case PlanUnitAPU:
		// Proxies (and, once folded in, apps) have no live-resize path: the resource always
		// follows the plan directly, in both directions.
		cluster.alignProxyResourceToPlan(target)
	}
}

// alignDBResourceToPlan sets the per-node DB resource (cores/mem/disk/iops) from the cluster DBU
// reservation, using the ResourceManager ProfileDatabase ratios (the single ratio source -- no
// hardcoded unit constants). All DB nodes are identical (any can become master), so the plan is
// spread evenly per node.
func (cluster *Cluster) alignDBResourceToPlan(planDBU int) {
	nodes := len(cluster.Servers)
	if nodes < 1 {
		nodes = 1
	}
	perNode := planDBU / nodes
	if perNode < 1 {
		perNode = 1
	}
	r := cluster.resources.Ratios(ProfileDatabase)
	cores := int(math.Round(float64(perNode) * r.CoresPerUnit))
	if cores < 1 {
		cores = 1
	}
	memMB := int(math.Round(float64(perNode) * r.MemMBPerUnit))
	diskGB := int(math.Round(float64(perNode) * r.DiskGBPerUnit))
	cluster.SetDBCores(strconv.Itoa(cores))
	cluster.SetDBMemorySize(strconv.Itoa(memMB))
	cluster.SetDBDiskSize(strconv.Itoa(diskGB))
	if r.IopsPerUnit > 0 {
		cluster.SetDBDiskIOPS(strconv.Itoa(int(math.Round(float64(perNode) * r.IopsPerUnit))))
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo,
		"Plan DBU %d -> DB resource aligned to %dc/%dMB/%dGB per node (x%d nodes)", planDBU, cores, memMB, diskGB, nodes)
}

// alignProxyResourceToPlan sets the proxy resource from the PER-PROXY APU reservation
// (prov-proxy-apu) via the ProfileCompute ratios. Every proxy is sized to its own reservation
// (default 2 APU = 2c/2GB/20GB). Apps are a different class -- sized from their own config in
// RefreshComputePlanAPU, never here.
func (cluster *Cluster) alignProxyResourceToPlan(perProxyAPU int) {
	if perProxyAPU < 1 {
		perProxyAPU = 1
	}
	r := cluster.resources.Ratios(ProfileCompute)
	cores := int(math.Round(float64(perProxyAPU) * r.CoresPerUnit))
	if cores < 1 {
		cores = 1
	}
	memMB := int(math.Round(float64(perProxyAPU) * r.MemMBPerUnit))
	diskGB := int(math.Round(float64(perProxyAPU) * r.DiskGBPerUnit))
	cluster.SetProxyCores(strconv.Itoa(cores))
	cluster.SetProxyMemorySize(strconv.Itoa(memMB))
	cluster.SetProxyDiskSize(strconv.Itoa(diskGB))
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo,
		"Plan APU %d/proxy -> proxy resource aligned to %dc/%dMB/%dGB", perProxyAPU, cores, memMB, diskGB)
}

// planUnitSpec returns the ONLY per-unit-varying data: the current reservation, the floor
// (minimum reservation the units require), and the setter that moves the reservation.
func (cluster *Cluster) planUnitSpec(unit PlanUnit) (cur int, floor int, apply func(int), err error) {
	switch PlanUnit(strings.ToUpper(string(unit))) {
	case PlanUnitDBU:
		nodes := len(cluster.Servers)
		if nodes < 1 {
			nodes = 1
		}
		return cluster.Conf.ProvServicePlanDbu, nodes, // floor = 1 DBU per node
			func(v int) { cluster.Conf.ProvServicePlanDbu = v }, nil
	case PlanUnitAPU:
		// prov-proxy-apu is the PER-PROXY reservation the proxy configurator moves -- the proxy
		// is the controlled stateless class repman sizes. Per-proxy floor is 1 APU. Apps carry
		// their OWN per-app reservation (their AppConfig) and feed the contract rollup
		// (AppPlanByCluster) separately; they are not moved here.
		return cluster.Conf.ProvProxyApu, 1,
			func(v int) { cluster.Conf.ProvProxyApu = v }, nil
	default:
		return 0, 0, nil, fmt.Errorf("ChangePlanUnits: unit %q not supported yet", unit)
	}
}

// planFlag is the config flag name backing a unit's reservation -- the admin-lock target.
func (cluster *Cluster) planFlag(unit PlanUnit) string {
	if PlanUnit(strings.ToUpper(string(unit))) == PlanUnitAPU {
		return "prov-proxy-apu"
	}
	return "prov-service-plan-dbu"
}

// CanPlanChange reports whether the client may change this unit's reservation in EITHER
// direction. The ONLY hard refuse is the admin lock: the plan variable is pinned immutable for
// this cluster (/etc -> immutable.toml), which freezes it both ways. The fleet-pool overcommit
// is deliberately NOT gated here -- it is TRACKED as the GWARN016 global state (over-reservation
// is allowed and surfaced; the admin caps it by locking the plan immutable). Pool room is a
// signal, not a gate.
func (cluster *Cluster) CanPlanChange(unit PlanUnit) bool {
	return !cluster.IsVariableImmutable(cluster.planFlag(unit))
}

// RunPlanClaimScript invokes the client-overridable external plan-claim hook -- a DIFFERENT
// script from the resize can-change script (that one decides HOW to apply, never WHETHER to
// claim). Non-zero exit REFUSES the increase; no script configured = allowed.
// TODO: bind a `prov-plan-claim-script` config flag + args (cluster, unit, from, to); today
// it is a no-op hook so an increase is allowed unless admin-locked.
func (cluster *Cluster) RunPlanClaimScript(unit PlanUnit, from, to int) error {
	return nil
}
