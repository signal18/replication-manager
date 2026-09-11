// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"
	"time"
)

// TestUnifiedPhysicalView is the core correctness claim of the unified model: a DB
// server (DBU) and a proxy (APU) co-located on ONE agent sum at the physical layer
// (the only correct cross-track sum), while the two unit tracks stay distinct and are
// never added. It also checks the agent headroom (capacity - combined consumed) and
// that the worst axis is reported.
func TestUnifiedPhysicalView(t *testing.T) {
	m := NewResourceManager()
	now := time.Now()

	const agent = "node-a"
	const cl = "cl1"

	// One DB server: 2 cores, 8 GiB, 500 iops, 20 GiB disk.
	dbMem := int64(8 * 1024 * 1024 * 1024)
	dbDisk := int64(20 * 1024 * 1024 * 1024)
	dbReading := m.ComputeUsedDBU(now, now, dbMem, 2.0, 500.0, dbDisk)
	dbKey := ResourceKey{Cluster: cl, Server: "db1"}
	m.SetConsumed(dbKey, &dbReading)
	m.SetServerAgent(dbKey, agent)

	// One proxy (APU): 1 core, 2 GiB, (no io), 1 GiB disk -- same agent.
	prxMem := int64(2 * 1024 * 1024 * 1024)
	prxDisk := int64(1 * 1024 * 1024 * 1024)
	prxReading := m.ComputeUsedAPU(now, now, prxMem, 1.0, prxDisk)
	prxKey := AppKey{Cluster: cl, App: "proxy1", Kind: KindProxy}
	m.SetAppConsumed(prxKey, &prxReading)
	m.SetAppAgent(prxKey, agent)

	// --- physical composition sums BOTH tracks -------------------------------
	phys := m.ClusterPhysicalConsumed(cl)
	wantMem := dbMem + prxMem
	wantCores := 3.0
	wantDisk := dbDisk + prxDisk
	if phys.MemBytes != wantMem {
		t.Errorf("cluster physical mem = %d, want %d", phys.MemBytes, wantMem)
	}
	if phys.CpuCores != wantCores {
		t.Errorf("cluster physical cores = %g, want %g", phys.CpuCores, wantCores)
	}
	if phys.DiskBytes != wantDisk {
		t.Errorf("cluster physical disk = %d, want %d", phys.DiskBytes, wantDisk)
	}
	// io is DB-only: the proxy (Compute, no IOPS lock) contributes 0.
	if phys.IoIops != 500.0 {
		t.Errorf("cluster physical io = %g, want 500 (DB only)", phys.IoIops)
	}

	// Agent physical consumed (same agent, both tracks) matches the cluster sum here.
	if ap := m.AgentPhysicalConsumed(agent); ap.MemBytes != wantMem || ap.CpuCores != wantCores {
		t.Errorf("agent physical = %+v, want mem %d cores %g", ap, wantMem, wantCores)
	}

	// --- the two unit tracks stay DISTINCT (never added) ---------------------
	view := m.ClusterResource(cl)
	if view.Dbu.Servers != 1 {
		t.Errorf("DBU track servers = %d, want 1", view.Dbu.Servers)
	}
	if view.Apu.Apu <= 0 {
		t.Errorf("APU track should be non-zero, got %g", view.Apu.Apu)
	}
	// DBU consumed comes from the DB reading only; proxy APU never leaks into it.
	if view.Dbu.Dbu != dbReading.Dbu {
		t.Errorf("cluster DBU = %g, want %g (DB only, proxy must not leak)", view.Dbu.Dbu, dbReading.Dbu)
	}
	if view.Physical.MemBytes != wantMem {
		t.Errorf("view physical mem = %d, want %d", view.Physical.MemBytes, wantMem)
	}

	// --- headroom: capacity - combined consumed, worst axis ------------------
	// Capacity: 4 cores, 16 GiB, 2000 iops, 100 GiB. cpu is scarcest here:
	// 3/4 = 75% cpu vs 10/16 = 62.5% mem.
	m.SetAgentCapacity(agent, &AgentCapacity{Cores: 4, MemMB: 16 * 1024, DiskGB: 100, Iops: 2000})
	hr := m.AgentHeadroom(agent)
	if !hr.HasCapacity {
		t.Fatal("headroom should have capacity")
	}
	if hr.Free.CpuCores != 1.0 {
		t.Errorf("free cores = %g, want 1", hr.Free.CpuCores)
	}
	if hr.WorstAxis != "cpu" {
		t.Errorf("worst axis = %q, want cpu (75%% > 62.5%% mem)", hr.WorstAxis)
	}
	if hr.WorstPct < 74.9 || hr.WorstPct > 75.1 {
		t.Errorf("worst pct = %g, want ~75", hr.WorstPct)
	}

	// One agent hosts this cluster -> exactly one agent in the view.
	if len(view.Agents) != 1 || view.Agents[0].Agent != agent {
		t.Errorf("view agents = %+v, want [%s]", view.Agents, agent)
	}
}

// TestAgentHeadroomNoCapacity: when an agent's capacity is unknown, headroom reports
// consumed but fabricates no ceiling (HasCapacity false, empty PctFull) -- the
// "never a fabricated value" rule on the resource authority.
func TestAgentHeadroomNoCapacity(t *testing.T) {
	m := NewResourceManager()
	now := time.Now()
	k := ResourceKey{Cluster: "c", Server: "s"}
	r := m.ComputeUsedDBU(now, now, 1<<30, 1.0, 100.0, 1<<30)
	m.SetConsumed(k, &r)
	m.SetServerAgent(k, "ghost")

	hr := m.AgentHeadroom("ghost")
	if hr.HasCapacity {
		t.Error("HasCapacity should be false for an agent with no declared capacity")
	}
	if len(hr.PctFull) != 0 {
		t.Errorf("PctFull should be empty without capacity, got %+v", hr.PctFull)
	}
	if hr.Consumed.CpuCores != 1.0 {
		t.Errorf("consumed should still be reported: cores = %g, want 1", hr.Consumed.CpuCores)
	}
}
