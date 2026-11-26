// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "github.com/signal18/replication-manager/config"

// slapOSOrchestrator is the SlapOS backend of the Orchestrator interface. Migration step 1:
// the database methods call the SlapOS* methods of prov_slapos*.go (cookies and a SQL
// shutdown, nothing is deployed). The proxy provision and unprovision of prov_slapos_prx.go
// are empty and never report on errorChan, so a caller waiting on them would block: the
// adapter answers ErrOrchestratorNotSupported instead of waiting.
type slapOSOrchestrator struct {
	unsupportedOrchestrator
	cluster *Cluster
	name    string
}

var _ Orchestrator = (*slapOSOrchestrator)(nil)

func newSlapOSOrchestrator(cluster *Cluster, name string) *slapOSOrchestrator {
	o := &slapOSOrchestrator{cluster: cluster, name: name}
	o.unsupportedOrchestrator.orch = o
	return o
}

func (o *slapOSOrchestrator) Name() string { return o.name }
func (o *slapOSOrchestrator) Type() string { return config.ConstOrchestratorSlapOS }

func (o *slapOSOrchestrator) Traits() OrchestratorTraits {
	return OrchestratorTraits{HasImage: true}
}

func (o *slapOSOrchestrator) Layout(server *ServerMonitor) ServiceLayout {
	return o.cluster.databaseLayout(server)
}

func (o *slapOSOrchestrator) ProxyLayout(prx DatabaseProxy) ServiceLayout {
	return o.cluster.proxyLayout(prx)
}

// Check lists the partitions the configuration declares.
func (o *slapOSOrchestrator) Check() error {
	_, err := o.Nodes()
	return err
}

func (o *slapOSOrchestrator) Nodes() ([]Agent, error) { return o.cluster.SlapOSGetNodes() }

func (o *slapOSOrchestrator) ProvisionDatabaseService(server *ServerMonitor) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.SlapOSProvisionDatabaseService(server) })
}

func (o *slapOSOrchestrator) UnprovisionDatabaseService(server *ServerMonitor) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.SlapOSUnprovisionDatabaseService(server) })
}

func (o *slapOSOrchestrator) StartDatabaseService(server *ServerMonitor) error {
	return o.cluster.SlapOSStartDatabaseService(server)
}

func (o *slapOSOrchestrator) StopDatabaseService(server *ServerMonitor) error {
	return o.cluster.SlapOSStopDatabaseService(server)
}

func (o *slapOSOrchestrator) StartProxyService(prx DatabaseProxy) error {
	return o.cluster.SlapOSStartProxyService(prx)
}

func (o *slapOSOrchestrator) StopProxyService(prx DatabaseProxy) error {
	return o.cluster.SlapOSStopProxyService(prx)
}
