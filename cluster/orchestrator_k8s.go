// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
)

// k8sOrchestrator is the Kubernetes backend of the Orchestrator interface. Migration step 1:
// every method calls the K8S* method of prov_k8s*.go as prov.go calls it today. The
// operations Kubernetes lacks today (freeze, the service image, the working node, the app
// services, the Secret removal, a shell in the pod) answer ErrOrchestratorNotSupported; the
// design's matrix lists which are to write.
type k8sOrchestrator struct {
	unsupportedOrchestrator
	cluster *Cluster
	name    string
}

var _ Orchestrator = (*k8sOrchestrator)(nil)

func newK8sOrchestrator(cluster *Cluster, name string) *k8sOrchestrator {
	o := &k8sOrchestrator{cluster: cluster, name: name}
	o.unsupportedOrchestrator.orch = o
	return o
}

func (o *k8sOrchestrator) Name() string { return o.name }
func (o *k8sOrchestrator) Type() string { return config.ConstOrchestratorKubernetes }

func (o *k8sOrchestrator) Traits() OrchestratorTraits {
	return OrchestratorTraits{
		IsContainer:         true,
		HasDNS:              true,
		HasSharedSecret:     true,
		HasPlacement:        true,
		HasPlanDisk:         true,
		HasImage:            true,
		RunsJobsInContainer: true,
		StartFetchesConfig:  true,
		ProvisionsNewHosts:  true,
		HasTerminal:         false,
		HasAtomicRestart:    true,
	}
}

func (o *k8sOrchestrator) Layout(server *ServerMonitor) ServiceLayout {
	return o.cluster.databaseLayout(server)
}

func (o *k8sOrchestrator) ProxyLayout(prx DatabaseProxy) ServiceLayout {
	return o.cluster.proxyLayout(prx)
}

// Check lists the nodes: the kubeconfig loads and the API server answers.
func (o *k8sOrchestrator) Check() error {
	_, err := o.Nodes()
	return err
}

func (o *k8sOrchestrator) Nodes() ([]Agent, error) { return o.cluster.K8SGetNodes() }

func (o *k8sOrchestrator) Storages() ([]Storage, error) {
	classes, err := o.cluster.K8SGetStorageClasses()
	if err != nil {
		return nil, err
	}
	storages := make([]Storage, 0, len(classes))
	for _, class := range classes {
		storages = append(storages, Storage{Name: class, Type: "storageclass"})
	}
	return storages, nil
}

func (o *k8sOrchestrator) ProvisionDatabaseService(server *ServerMonitor) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.K8SProvisionDatabaseService(server) })
}

func (o *k8sOrchestrator) UnprovisionDatabaseService(server *ServerMonitor) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.K8SUnprovisionDatabaseService(server) })
}

func (o *k8sOrchestrator) StartDatabaseService(server *ServerMonitor) error {
	return o.cluster.K8SStartDatabaseService(server)
}

func (o *k8sOrchestrator) StopDatabaseService(server *ServerMonitor) error {
	return o.cluster.K8SStopDatabaseService(server)
}

// RestartDatabaseService is the rolling pod replacement prov.go uses for a Kubernetes
// restart; node and rid have no meaning there.
func (o *k8sOrchestrator) RestartDatabaseService(server *ServerMonitor, node string, rid string) error {
	return o.cluster.K8SForceRepullDatabaseService(server)
}

func (o *k8sOrchestrator) UpdateDatabaseServiceConfig(server *ServerMonitor, forcePull bool) error {
	return o.cluster.K8SUpdateDatabaseServiceConfig(server, forcePull)
}

// RefreshDatabaseServiceDefinition is today's Deployment update: the image and its pull
// policy, not the full pod spec (#1775 lists the full spec as the gap to write).
func (o *k8sOrchestrator) RefreshDatabaseServiceDefinition(server *ServerMonitor, keepImage bool) error {
	return o.cluster.k8sUpdateDatabaseServiceConfigKeepImage(server, keepImage)
}

func (o *k8sOrchestrator) DatabaseServiceDefinition(server *ServerMonitor) ([]byte, error) {
	return json.Marshal(o.cluster.K8SGetDatabaseManifests(server))
}

func (o *k8sOrchestrator) ProvisionProxyService(prx DatabaseProxy) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.K8SProvisionProxyService(prx) })
}

func (o *k8sOrchestrator) UnprovisionProxyService(prx DatabaseProxy) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.K8SUnprovisionProxyService(prx) })
}

func (o *k8sOrchestrator) StartProxyService(prx DatabaseProxy) error {
	return o.cluster.K8SStartProxyService(prx)
}

func (o *k8sOrchestrator) StopProxyService(prx DatabaseProxy) error {
	return o.cluster.K8SStopProxyService(prx)
}

func (o *k8sOrchestrator) SecretStore() orchestratorStore { return o.cluster.k8sStore() }

func (o *k8sOrchestrator) ServerSecretStore(namespace string) (credstore.StoreWriter, error) {
	return o.cluster.ServerOrchestratorStore(config.ConstSecretKvPluginKubernetes, namespace)
}

func (o *k8sOrchestrator) InitCredentials() []initCredential { return o.cluster.initCredentials() }

func (o *k8sOrchestrator) RotateDatabasePassword(password string) error {
	client, err := o.cluster.K8SConnectAPI()
	if err != nil {
		return err
	}
	return o.cluster.k8sRotatePasswordsWithClient(client, password)
}
