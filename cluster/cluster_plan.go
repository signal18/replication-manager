// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
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
//	delta < 0  -> DECREASE: always allowed, clamped at the per-unit floor, applied + persisted.
//	delta > 0  -> INCREASE (a "claim"): VALIDATED first -- CanPlanIncrease (admin immutable
//	              lock; the fleet-pool overcommit is TRACKED as GWARN016, never hard-blocked)
//	              + the external plan-claim script -- then applied + persisted.
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

	if target > cur { // increase = a claim -> validate + hook
		if !cluster.CanPlanIncrease(unit) {
			return fmt.Errorf("plan increase refused for %s: reservation is locked (immutable) by the admin", unit)
		}
		if err := cluster.RunPlanClaimScript(unit, cur, target); err != nil {
			return fmt.Errorf("plan increase refused for %s by external claim script: %w", unit, err)
		}
	}

	apply(target)                                    // move the reservation variable (the contract)
	cluster.ConfigManager.SaveConfig(cluster, false) // persist via the dynamic config manager
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo,
		"Plan reservation %s changed %d -> %d (delta %+d)", unit, cur, target, delta)
	return nil
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
		floor := len(cluster.Proxies) + len(cluster.Apps) // floor = 1 APU per proxy/app
		if floor < 1 {
			floor = 1
		}
		return cluster.Conf.ProvServicePlanApu, floor,
			func(v int) { cluster.Conf.ProvServicePlanApu = v }, nil
	default:
		return 0, 0, nil, fmt.Errorf("ChangePlanUnits: unit %q not supported yet", unit)
	}
}

// CanPlanIncrease reports whether the client may RAISE this unit's reservation. The ONLY
// hard refuse is the admin lock: the plan variable is immutable for this cluster (/etc ->
// immutable.toml). The fleet-pool overcommit is deliberately NOT gated here -- it is TRACKED
// as the GWARN016 global state (over-reservation is allowed and surfaced; the admin caps it
// by locking the plan immutable). Pool room is a signal, not a gate.
func (cluster *Cluster) CanPlanIncrease(unit PlanUnit) bool {
	flag := "prov-service-plan-dbu"
	if PlanUnit(strings.ToUpper(string(unit))) == PlanUnitAPU {
		flag = "prov-service-plan-apu"
	}
	return !cluster.IsVariableImmutable(flag)
}

// RunPlanClaimScript invokes the client-overridable external plan-claim hook -- a DIFFERENT
// script from the resize can-change script (that one decides HOW to apply, never WHETHER to
// claim). Non-zero exit REFUSES the increase; no script configured = allowed.
// TODO: bind a `prov-plan-claim-script` config flag + args (cluster, unit, from, to); today
// it is a no-op hook so an increase is allowed unless admin-locked.
func (cluster *Cluster) RunPlanClaimScript(unit PlanUnit, from, to int) error {
	return nil
}
