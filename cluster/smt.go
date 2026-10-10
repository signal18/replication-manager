// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this source distribution for more information.

package cluster

import "strconv"

// SMT (hyperthreading) and the DBU core (#1958).
//
// Cloud18 sells real resources: a DBU core is a real core. The CPU cap of a
// container is a quota in logical CPUs (threads), and on an SMT node two
// threads share one physical core: with both busy, each delivers about 55-60%
// of a core (bso-fr-1, Xeon Gold 6342, sysbench: x1.09 cpu, x1.19 memory).
// With resource-manager-smt-gain = g (the throughput of a core with all its
// threads busy, relative to one thread):
//   - capacity: an SMT node is worth cores x g core equivalents;
//   - quota: a DBU core gets threads-per-core / g logical CPUs on that node.
// A node without SMT (threads = cores, or threads unknown) and a gain of 1 or
// less leave both as they were.

// HasSMT reports whether the agent's node runs more threads than cores.
func (a Agent) HasSMT() bool {
	return a.CpuCores > 0 && a.CpuThreads > a.CpuCores
}

// ThreadsPerCore is the node's logical CPUs per physical core, 1 without SMT.
func (a Agent) ThreadsPerCore() float64 {
	if !a.HasSMT() {
		return 1
	}
	return float64(a.CpuThreads) / float64(a.CpuCores)
}

// smtGain clamps the configured gain to what the node can give: at least 1 (no
// loss), at most its threads per core (each thread a full core).
func (a Agent) smtGain(gain float64) float64 {
	if gain < 1 {
		return 1
	}
	if tpc := a.ThreadsPerCore(); gain > tpc {
		return tpc
	}
	return gain
}

// CoreEquivalents is the node's CPU capacity in real cores: cores x gain on an
// SMT node, its cores otherwise.
func (a Agent) CoreEquivalents(gain float64) float64 {
	if !a.HasSMT() || gain <= 1 {
		return float64(a.CpuCores)
	}
	return float64(a.CpuCores) * a.smtGain(gain)
}

// CPUQuotaFactor is the logical CPUs of quota one DBU core needs on the node to
// deliver a real core: threads per core / gain on an SMT node, 1 otherwise.
func (a Agent) CPUQuotaFactor(gain float64) float64 {
	if !a.HasSMT() || gain <= 1 {
		return 1
	}
	return a.ThreadsPerCore() / a.smtGain(gain)
}

// smtGainInForce is the instance's SMT gain, from the ResourceManager; the cluster copy only
// without a manager (unit tests).
func (cluster *Cluster) smtGainInForce() float64 {
	if cluster.resources != nil {
		if g := cluster.resources.SmtGain(); g > 0 {
			return g
		}
	}
	return cluster.Conf.ResourceManagerSmtGain
}

// cpuQuotaCoresOnNode turns a container's cores into the logical CPUs of quota
// on the node it runs on. An unknown node keeps the cores.
func (cluster *Cluster) cpuQuotaCoresOnNode(node string, cores float64) float64 {
	if node == "" {
		return cores
	}
	for _, a := range cluster.Agents {
		if a.HostName == node {
			return cores * a.CPUQuotaFactor(cluster.smtGainInForce())
		}
	}
	return cores
}

// serverNode is the node a server runs on, or is placed on.
func serverNode(server *ServerMonitor) string {
	if server == nil {
		return ""
	}
	if server.WorkingAgent != "" {
		return server.WorkingAgent
	}
	return server.Agent
}

// dockerCPUsOnNode renders prov-db-cpu-cores as docker --cpus on the server's
// node, in logical CPUs (cpuQuotaCoresOnNode). A value that does not parse is
// passed as before.
func (cluster *Cluster) dockerCPUsOnNode(server *ServerMonitor) string {
	cores, err := strconv.ParseFloat(cluster.Conf.ProvCores, 64)
	if err != nil {
		return cluster.Conf.ProvCores + ".0"
	}
	q := cluster.cpuQuotaCoresOnNode(serverNode(server), cores)
	if q == cores {
		return cluster.Conf.ProvCores + ".0" // unchanged rendering without SMT
	}
	return strconv.FormatFloat(q, 'f', 2, 64)
}
