// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this source distribution for more information.

package cluster

import (
	"math"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func smtNear(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestSMTCoreEquivalentsAndQuota(t *testing.T) {
	bso := Agent{HostName: "s18-pa3-srv1", CpuCores: 48, CpuThreads: 96} // 2 threads per core
	rdem := Agent{HostName: "saga-fr-11", CpuCores: 32, CpuThreads: 32}  // no SMT
	vm := Agent{HostName: "cl1n1", CpuCores: 128, CpuThreads: 0}         // threads not reported
	for _, tt := range []struct {
		name     string
		a        Agent
		gain     float64
		capacity float64
		factor   float64
	}{
		{"BSO gain 1.15", bso, 1.15, 55.2, 2 / 1.15},
		{"BSO gain 1 (not accounted)", bso, 1, 48, 1},
		{"BSO gain 0 (not accounted)", bso, 0, 48, 1},
		{"BSO gain above threads per core is clamped", bso, 3, 96, 1},
		{"no SMT", rdem, 1.15, 32, 1},
		{"threads unknown", vm, 1.15, 128, 1},
	} {
		if got := tt.a.CoreEquivalents(tt.gain); !smtNear(got, tt.capacity) {
			t.Errorf("%s: capacity %.2f, want %.2f", tt.name, got, tt.capacity)
		}
		if got := tt.a.CPUQuotaFactor(tt.gain); !smtNear(got, tt.factor) {
			t.Errorf("%s: quota factor %.3f, want %.3f", tt.name, got, tt.factor)
		}
	}
}

func TestCPUQuotaOnNode(t *testing.T) {
	cl := &Cluster{Conf: &config.Config{ResourceManagerSmtGain: 1.15, ProvCores: "2"},
		Agents: []Agent{{HostName: "smt", CpuCores: 48, CpuThreads: 96}, {HostName: "plain", CpuCores: 32, CpuThreads: 32}}}

	if got := cl.cpuQuotaCoresOnNode("smt", 2); !smtNear(got, 3.478) {
		t.Errorf("SMT node: %.3f logical CPUs, want 3.478 for 2 DBU cores", got)
	}
	for _, node := range []string{"plain", "unknown", ""} {
		if got := cl.cpuQuotaCoresOnNode(node, 2); got != 2 {
			t.Errorf("node %q: %.3f, want 2 unchanged", node, got)
		}
	}
	if got := OpenSVCCPUQuotaKeyword(cl.cpuQuotaCoresOnNode("smt", 2)); got == OpenSVCCPUQuotaKeyword(2) {
		t.Errorf("SMT node keeps the 2-core quota %q", got)
	}
	plain := &ServerMonitor{Agent: "plain"}
	smt := &ServerMonitor{Agent: "plain", WorkingAgent: "smt"} // running node wins over placement
	if got := cl.dockerCPUsOnNode(plain); got != "2.0" {
		t.Errorf("docker --cpus without SMT = %q, want the unchanged 2.0", got)
	}
	if got := cl.dockerCPUsOnNode(smt); got != "3.48" {
		t.Errorf("docker --cpus on the SMT node = %q, want 3.48", got)
	}
}

// The quota follows the instance's gain held by the ResourceManager, live, never a
// per-cluster copy (review of #1959: no write into cl.Conf from the API goroutine).
func TestCPUQuotaFollowsManagerGain(t *testing.T) {
	cl := &Cluster{Conf: &config.Config{ResourceManagerSmtGain: 1},
		Agents: []Agent{{HostName: "smt", CpuCores: 48, CpuThreads: 96}}}
	rm := NewResourceManager()
	rm.SetSmtGain(1.15)
	cl.SetResourceManager(rm)
	if got := cl.cpuQuotaCoresOnNode("smt", 2); !smtNear(got, 3.478) {
		t.Fatalf("manager gain 1.15: %.3f logical CPUs, want 3.478", got)
	}
	rm.SetSmtGain(1)
	if got := cl.cpuQuotaCoresOnNode("smt", 2); got != 2 {
		t.Fatalf("manager gain 1 (SMT not accounted): %.3f, want the 2 cores unchanged", got)
	}
}
