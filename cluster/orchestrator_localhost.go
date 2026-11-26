// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "github.com/signal18/replication-manager/config"

// localhostOrchestrator is the localhost (processes on the monitor host) backend of the
// Orchestrator interface. Migration step 1: every method calls the Localhost* method of
// prov_localhost*.go. StopDatabaseService calls LocalhostStopDatabaseService, which the
// switch of prov.go does not (it stops a localhost server with the on-premise stop): the
// adapter is right and the switch keeps its behaviour until step 2 reroutes it.
type localhostOrchestrator struct {
	unsupportedOrchestrator
	cluster *Cluster
	name    string
}

var _ Orchestrator = (*localhostOrchestrator)(nil)

func newLocalhostOrchestrator(cluster *Cluster, name string) *localhostOrchestrator {
	o := &localhostOrchestrator{cluster: cluster, name: name}
	o.unsupportedOrchestrator.orch = o
	return o
}

func (o *localhostOrchestrator) Name() string { return o.name }
func (o *localhostOrchestrator) Type() string { return config.ConstOrchestratorLocalhost }

func (o *localhostOrchestrator) Traits() OrchestratorTraits {
	return OrchestratorTraits{
		HasImage:           true,
		StartFetchesConfig: true,
	}
}

func (o *localhostOrchestrator) Layout(server *ServerMonitor) ServiceLayout {
	return o.cluster.databaseLayout(server)
}

func (o *localhostOrchestrator) ProxyLayout(prx DatabaseProxy) ServiceLayout {
	return o.cluster.proxyLayout(prx)
}

// Check lists the node: the monitor host itself.
func (o *localhostOrchestrator) Check() error {
	_, err := o.Nodes()
	return err
}

func (o *localhostOrchestrator) Nodes() ([]Agent, error) { return o.cluster.LocalhostGetNodes() }

func (o *localhostOrchestrator) ProvisionDatabaseService(server *ServerMonitor) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.LocalhostProvisionDatabaseService(server) })
}

func (o *localhostOrchestrator) UnprovisionDatabaseService(server *ServerMonitor) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.LocalhostUnprovisionDatabaseService(server) })
}

func (o *localhostOrchestrator) StartDatabaseService(server *ServerMonitor) error {
	return o.cluster.LocalhostStartDatabaseService(server)
}

func (o *localhostOrchestrator) StopDatabaseService(server *ServerMonitor) error {
	return o.cluster.LocalhostStopDatabaseService(server)
}

func (o *localhostOrchestrator) ProvisionProxyService(prx DatabaseProxy) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.LocalhostProvisionProxyService(prx) })
}

func (o *localhostOrchestrator) UnprovisionProxyService(prx DatabaseProxy) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.LocalhostUnprovisionProxyService(prx) })
}

func (o *localhostOrchestrator) StartProxyService(prx DatabaseProxy) error {
	return o.cluster.LocalhostStartProxyService(prx)
}

func (o *localhostOrchestrator) StopProxyService(prx DatabaseProxy) error {
	return o.cluster.LocalhostStopProxyService(prx)
}
