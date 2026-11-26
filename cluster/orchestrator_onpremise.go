// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strconv"

	"github.com/signal18/replication-manager/config"
)

// onPremiseOrchestrator is the on-premise (SSH) backend of the Orchestrator interface.
// Migration step 1: every method calls the OnPremise* method of prov_onpremise*.go as
// prov.go calls it today. A host has no service definition, no shared secret, no container:
// those operations answer ErrOrchestratorNotSupported.
type onPremiseOrchestrator struct {
	unsupportedOrchestrator
	cluster *Cluster
	name    string
}

var _ Orchestrator = (*onPremiseOrchestrator)(nil)

func newOnPremiseOrchestrator(cluster *Cluster, name string) *onPremiseOrchestrator {
	o := &onPremiseOrchestrator{cluster: cluster, name: name}
	o.unsupportedOrchestrator.orch = o
	return o
}

func (o *onPremiseOrchestrator) Name() string { return o.name }
func (o *onPremiseOrchestrator) Type() string { return config.ConstOrchestratorOnPremise }

func (o *onPremiseOrchestrator) Traits() OrchestratorTraits {
	return OrchestratorTraits{HostBySSH: true}
}

func (o *onPremiseOrchestrator) Layout(server *ServerMonitor) ServiceLayout {
	return o.cluster.databaseLayout(server)
}

func (o *onPremiseOrchestrator) ProxyLayout(prx DatabaseProxy) ServiceLayout {
	return o.cluster.proxyLayout(prx)
}

// Check lists the nodes: the hosts answer on SSH when onpremise-ssh is on.
func (o *onPremiseOrchestrator) Check() error {
	_, err := o.Nodes()
	return err
}

func (o *onPremiseOrchestrator) Nodes() ([]Agent, error) { return o.cluster.OnPremiseGetNodes() }

func (o *onPremiseOrchestrator) ProvisionDatabaseService(server *ServerMonitor) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.OnPremiseProvisionDatabaseService(server) })
}

func (o *onPremiseOrchestrator) UnprovisionDatabaseService(server *ServerMonitor) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.OnPremiseUnprovisionDatabaseService(server) })
}

func (o *onPremiseOrchestrator) StartDatabaseService(server *ServerMonitor) error {
	return o.cluster.OnPremiseStartDatabaseService(server)
}

func (o *onPremiseOrchestrator) StopDatabaseService(server *ServerMonitor) error {
	return o.cluster.OnPremiseStopDatabaseService(server)
}

// UpgradeDatabaseService runs the onpremise-ssh-upgrade-db-script.
func (o *onPremiseOrchestrator) UpgradeDatabaseService(server *ServerMonitor) error {
	return o.cluster.OnPremiseUpgradeDatabaseService(server)
}

func (o *onPremiseOrchestrator) ProvisionProxyService(prx DatabaseProxy) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.OnPremiseProvisionProxyService(prx) })
}

func (o *onPremiseOrchestrator) UnprovisionProxyService(prx DatabaseProxy) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.OnPremiseUnprovisionProxyService(prx) })
}

func (o *onPremiseOrchestrator) StartProxyService(prx DatabaseProxy) error {
	return o.cluster.OnPremiseStartProxyService(prx)
}

func (o *onPremiseOrchestrator) StopProxyService(prx DatabaseProxy) error {
	return o.cluster.OnPremiseStopProxyService(prx)
}

// TerminalCommand is the SSH login on the service's host, as server_tty.go builds it.
func (o *onPremiseOrchestrator) TerminalCommand(req TerminalRequest) (TerminalCommand, error) {
	return TerminalCommand{
		Host:     req.Host,
		Port:     strconv.Itoa(o.cluster.Conf.OnPremiseSSHPort),
		Username: o.cluster.GetOnPremiseSSHUser(),
		Password: o.cluster.GetOnPremiseSSHPass(),
		Key:      o.cluster.OnPremiseGetSSHKey(),
	}, nil
}
