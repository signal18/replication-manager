// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
)

// orchestratorConfig is one orchestrator's configuration: its type and the keys the backend
// reads, under the names the flat configuration gives them today. An orchestrator
// synthesized from a cluster's flat keys (synthesizedOrchestratorConfig) and one declared
// as a named block carry the same struct; the block form of the configuration
// ([orchestrator.<name>], design section 3.1) arrives with a later step, so nothing reads
// this struct from a file yet.
type orchestratorConfig struct {
	Name string
	Type string `mapstructure:"type"`

	// OpenSVC
	ProvHost                        string `mapstructure:"opensvc-host"`
	ProvAdminUser                   string `mapstructure:"opensvc-admin-user"`
	ProvUser                        string `mapstructure:"opensvc-user"`
	ProvCodeApp                     string `mapstructure:"opensvc-codeapp"`
	ProvOpensvcP12Certificate       string `mapstructure:"opensvc-p12-certificate"`
	ProvOpensvcUseCollectorAPI      bool   `mapstructure:"opensvc-use-collector-api"`
	ProvOpensvcCollectorAccount     string `mapstructure:"opensvc-collector-account"`
	ProvOpensvcV3ProvisionDelay     int    `mapstructure:"opensvc-v3-provision-delay"`
	ProvOpensvcImageForcePull       bool   `mapstructure:"opensvc-image-force-pull"`
	ProvOpensvcUseOrchestratedStart bool   `mapstructure:"opensvc-use-orchestrated-start"`
	ProvRegister                    bool   `mapstructure:"opensvc-register"`
	ProvOrchestratorCluster         string `mapstructure:"prov-orchestrator-cluster"`

	// Kubernetes
	KubeConfig                string `mapstructure:"kube-config"`
	ProvKubeImageForcePull    bool   `mapstructure:"prov-kube-image-force-pull"`
	ProvKubeStorageClass      string `mapstructure:"prov-kube-storage-class"`
	ProvKubeProxyStorageClass string `mapstructure:"prov-kube-proxy-storage-class"`
	ProvNetCNICluster         string `mapstructure:"prov-net-cni-cluster"`

	// on-premise
	OnPremiseSSH                 bool   `mapstructure:"onpremise-ssh"`
	OnPremiseSSHPort             int    `mapstructure:"onpremise-ssh-port"`
	OnPremiseSSHPrivateKey       string `mapstructure:"onpremise-ssh-private-key"`
	OnPremiseSSHCredential       string `mapstructure:"onpremise-ssh-credential"`
	OnPremiseSSHStartDbScript    string `mapstructure:"onpremise-ssh-start-db-script"`
	OnPremiseSSHStartProxyScript string `mapstructure:"onpremise-ssh-start-proxy-script"`
	OnPremiseSSHStopProxyScript  string `mapstructure:"onpremise-ssh-stop-proxy-script"`
	OnPremiseSSHDbJobScript      string `mapstructure:"onpremise-ssh-db-job-script"`
	OnPremiseSSHUpgradeDbScript  string `mapstructure:"onpremise-ssh-upgrade-db-script"`

	// SlapOS
	SlapOSDBPartitions         string `mapstructure:"slapos-db-partitions"`
	SlapOSProxySQLPartitions   string `mapstructure:"slapos-proxysql-partitions"`
	SlapOSHaProxyPartitions    string `mapstructure:"slapos-haproxy-partitions"`
	SlapOSMaxscalePartitions   string `mapstructure:"slapos-maxscale-partitions"`
	SlapOSShardProxyPartitions string `mapstructure:"slapos-shardproxy-partitions"`
	SlapOSSphinxPartitions     string `mapstructure:"slapos-sphinx-partitions"`
}

// synthesizedOrchestratorConfig is the orchestrator a cluster's flat keys describe: named
// after its type, carrying the keys as they are. This is how every existing configuration
// keeps working unchanged (design section 3.2).
func synthesizedOrchestratorConfig(conf *config.Config) orchestratorConfig {
	if conf == nil {
		return orchestratorConfig{}
	}
	return orchestratorConfig{
		Name:                            conf.ProvOrchestrator,
		Type:                            conf.ProvOrchestrator,
		ProvHost:                        conf.ProvHost,
		ProvAdminUser:                   conf.ProvAdminUser,
		ProvUser:                        conf.ProvUser,
		ProvCodeApp:                     conf.ProvCodeApp,
		ProvOpensvcP12Certificate:       conf.ProvOpensvcP12Certificate,
		ProvOpensvcUseCollectorAPI:      conf.ProvOpensvcUseCollectorAPI,
		ProvOpensvcCollectorAccount:     conf.ProvOpensvcCollectorAccount,
		ProvOpensvcV3ProvisionDelay:     conf.ProvOpensvcV3ProvisionDelay,
		ProvOpensvcImageForcePull:       conf.ProvOpensvcImageForcePull,
		ProvOpensvcUseOrchestratedStart: conf.ProvOpensvcUseOrchestratedStart,
		ProvRegister:                    conf.ProvRegister,
		ProvOrchestratorCluster:         conf.ProvOrchestratorCluster,
		KubeConfig:                      conf.KubeConfig,
		ProvKubeImageForcePull:          conf.ProvKubeImageForcePull,
		ProvKubeStorageClass:            conf.ProvKubeStorageClass,
		ProvKubeProxyStorageClass:       conf.ProvKubeProxyStorageClass,
		ProvNetCNICluster:               conf.ProvNetCNICluster,
		OnPremiseSSH:                    conf.OnPremiseSSH,
		OnPremiseSSHPort:                conf.OnPremiseSSHPort,
		OnPremiseSSHPrivateKey:          conf.OnPremiseSSHPrivateKey,
		OnPremiseSSHCredential:          conf.OnPremiseSSHCredential,
		OnPremiseSSHStartDbScript:       conf.OnPremiseSSHStartDbScript,
		OnPremiseSSHStartProxyScript:    conf.OnPremiseSSHStartProxyScript,
		OnPremiseSSHStopProxyScript:     conf.OnPremiseSSHStopProxyScript,
		OnPremiseSSHDbJobScript:         conf.OnPremiseSSHDbJobScript,
		OnPremiseSSHUpgradeDbScript:     conf.OnPremiseSSHUpgradeDbScript,
		SlapOSDBPartitions:              conf.SlapOSDBPartitions,
		SlapOSProxySQLPartitions:        conf.SlapOSProxySQLPartitions,
		SlapOSHaProxyPartitions:         conf.SlapOSHaProxyPartitions,
		SlapOSMaxscalePartitions:        conf.SlapOSMaxscalePartitions,
		SlapOSShardProxyPartitions:      conf.SlapOSShardProxyPartitions,
		SlapOSSphinxPartitions:          conf.SlapOSSphinxPartitions,
	}
}

// newOrchestratorList builds the cluster's orchestrators, as newProxyList builds its proxies:
// today the one synthesized from prov-orchestrator and the flat keys, under its type name.
// An unknown type leaves the registry empty; Orchestrator() then answers with a
// nullOrchestrator. Nothing calls the registry's values yet: the switch statements of
// prov.go and the other files of the design's inventory still dispatch (migration step 1).
func (cluster *Cluster) newOrchestratorList() error {
	cluster.orchestratorsMutex.Lock()
	defer cluster.orchestratorsMutex.Unlock()
	cluster.Orchestrators = make(map[string]Orchestrator)
	oc := synthesizedOrchestratorConfig(cluster.Conf)
	if orch, ok := cluster.newOrchestrator(oc); ok {
		cluster.Orchestrators[oc.Name] = orch
	}
	return nil
}

// newOrchestrator builds the backend of one configuration; false for an unknown type.
func (cluster *Cluster) newOrchestrator(oc orchestratorConfig) (Orchestrator, bool) {
	switch oc.Type {
	case config.ConstOrchestratorOpenSVC:
		return newOpenSVCOrchestrator(cluster, oc.Name), true
	case config.ConstOrchestratorKubernetes:
		return newK8sOrchestrator(cluster, oc.Name), true
	case config.ConstOrchestratorOnPremise:
		return newOnPremiseOrchestrator(cluster, oc.Name), true
	case config.ConstOrchestratorLocalhost:
		return newLocalhostOrchestrator(cluster, oc.Name), true
	case config.ConstOrchestratorSlapOS:
		return newSlapOSOrchestrator(cluster, oc.Name), true
	}
	return nil, false
}

// Orchestrator is the cluster's orchestrator: the registered one named by prov-orchestrator,
// built on the spot when the setting changed since newOrchestratorList ran, a
// nullOrchestrator (every method ErrOrchestratorNotSupported) for an unknown type: never nil.
func (cluster *Cluster) Orchestrator() Orchestrator {
	name := ""
	if cluster.Conf != nil {
		name = cluster.Conf.ProvOrchestrator
	}
	cluster.orchestratorsMutex.RLock()
	orch, ok := cluster.Orchestrators[name]
	cluster.orchestratorsMutex.RUnlock()
	if ok {
		return orch
	}
	orch, ok = cluster.newOrchestrator(synthesizedOrchestratorConfig(cluster.Conf))
	if !ok {
		return newNullOrchestrator(cluster, name)
	}
	cluster.orchestratorsMutex.Lock()
	if cluster.Orchestrators == nil {
		cluster.Orchestrators = make(map[string]Orchestrator)
	}
	cluster.Orchestrators[name] = orch
	cluster.orchestratorsMutex.Unlock()
	return orch
}

// orchestrator is the orchestrator a server is placed on: the cluster's, until the
// per-server override exists (design section 3.3).
func (cluster *Cluster) orchestrator(server *ServerMonitor) Orchestrator {
	return cluster.Orchestrator()
}

// awaitProvisioning runs one of today's provisioning methods, which report through
// cluster.errorChan instead of returning, and returns what it reports. The caller holds
// cluster.provisioningMutex, as every receiver of errorChan does today (#1769). Migration
// step 2 makes the methods return their error and deletes this helper.
func (cluster *Cluster) awaitProvisioning(run func()) error {
	go run()
	return <-cluster.errorChan
}

// databaseLayout is the layout today's per-server getters compute; the adapters delegate to
// them until migration step 5 turns the getters around.
func (cluster *Cluster) databaseLayout(server *ServerMonitor) ServiceLayout {
	layout := ServiceLayout{Domain: cluster.GetDomain()}
	if server == nil {
		return layout
	}
	layout.Datadir = server.GetDatabaseDatadir()
	layout.Confdir = server.GetDatabaseConfdir()
	layout.Basedir = server.GetDatabaseBasedir()
	layout.ClientBasedir = server.GetDatabaseClientBasedir()
	layout.Binary = server.GetDatabaseBinary()
	layout.Socket = server.GetDatabaseSocket()
	layout.BindAddress = server.GetBindAddress()
	return layout
}

// proxyLayout is the layout today's proxy getters compute.
func (cluster *Cluster) proxyLayout(prx DatabaseProxy) ServiceLayout {
	layout := ServiceLayout{Domain: cluster.GetDomain()}
	if prx == nil {
		return layout
	}
	layout.BindAddress = prx.GetBindAddress()
	if p, ok := prx.(interface {
		GetConfigDatadir() string
		GetConfigConfigdir() string
	}); ok {
		layout.Datadir = p.GetConfigDatadir()
		layout.Confdir = p.GetConfigConfigdir()
	}
	return layout
}

// unsupportedOrchestrator answers ErrOrchestratorNotSupported to every operation. Each
// backend embeds it and declares the methods it implements; the methods it does not declare
// fall through to these. orch is the embedding backend, for the error's name and type.
type unsupportedOrchestrator struct {
	orch OrchestratorInfo
}

func (u *unsupportedOrchestrator) Nodes() ([]Agent, error) { return nil, notSupported("Nodes", u.orch) }
func (u *unsupportedOrchestrator) Storages() ([]Storage, error) {
	return nil, notSupported("Storages", u.orch)
}
func (u *unsupportedOrchestrator) NodeStats() ([]NodeStats, error) {
	return nil, notSupported("NodeStats", u.orch)
}
func (u *unsupportedOrchestrator) Check() error                            { return notSupported("Check", u.orch) }
func (u *unsupportedOrchestrator) Layout(*ServerMonitor) ServiceLayout     { return ServiceLayout{} }
func (u *unsupportedOrchestrator) ProxyLayout(DatabaseProxy) ServiceLayout { return ServiceLayout{} }

func (u *unsupportedOrchestrator) ProvisionDatabaseService(*ServerMonitor) error {
	return notSupported("ProvisionDatabaseService", u.orch)
}
func (u *unsupportedOrchestrator) UnprovisionDatabaseService(*ServerMonitor) error {
	return notSupported("UnprovisionDatabaseService", u.orch)
}
func (u *unsupportedOrchestrator) StartDatabaseService(*ServerMonitor) error {
	return notSupported("StartDatabaseService", u.orch)
}
func (u *unsupportedOrchestrator) StopDatabaseService(*ServerMonitor) error {
	return notSupported("StopDatabaseService", u.orch)
}
func (u *unsupportedOrchestrator) RestartDatabaseService(*ServerMonitor, string, string) error {
	return notSupported("RestartDatabaseService", u.orch)
}
func (u *unsupportedOrchestrator) FreezeDatabaseService(*ServerMonitor) error {
	return notSupported("FreezeDatabaseService", u.orch)
}
func (u *unsupportedOrchestrator) UnfreezeDatabaseService(*ServerMonitor) error {
	return notSupported("UnfreezeDatabaseService", u.orch)
}
func (u *unsupportedOrchestrator) UpdateDatabaseServiceConfig(*ServerMonitor, bool) error {
	return notSupported("UpdateDatabaseServiceConfig", u.orch)
}
func (u *unsupportedOrchestrator) RefreshDatabaseServiceDefinition(*ServerMonitor, bool) error {
	return notSupported("RefreshDatabaseServiceDefinition", u.orch)
}
func (u *unsupportedOrchestrator) UpgradeDatabaseService(*ServerMonitor) error {
	return notSupported("UpgradeDatabaseService", u.orch)
}
func (u *unsupportedOrchestrator) DatabaseServiceDefinition(*ServerMonitor) ([]byte, error) {
	return nil, notSupported("DatabaseServiceDefinition", u.orch)
}
func (u *unsupportedOrchestrator) DatabaseServiceImage(*ServerMonitor) (string, error) {
	return "", notSupported("DatabaseServiceImage", u.orch)
}
func (u *unsupportedOrchestrator) DatabaseWorkingNode(*ServerMonitor) (string, error) {
	return "", notSupported("DatabaseWorkingNode", u.orch)
}
func (u *unsupportedOrchestrator) AbortDatabaseService(*ServerMonitor) error {
	return notSupported("AbortDatabaseService", u.orch)
}
func (u *unsupportedOrchestrator) ClearDatabaseInstanceState(*ServerMonitor, string) error {
	return notSupported("ClearDatabaseInstanceState", u.orch)
}

func (u *unsupportedOrchestrator) ProvisionProxyService(DatabaseProxy) error {
	return notSupported("ProvisionProxyService", u.orch)
}
func (u *unsupportedOrchestrator) UnprovisionProxyService(DatabaseProxy) error {
	return notSupported("UnprovisionProxyService", u.orch)
}
func (u *unsupportedOrchestrator) StartProxyService(DatabaseProxy) error {
	return notSupported("StartProxyService", u.orch)
}
func (u *unsupportedOrchestrator) StopProxyService(DatabaseProxy) error {
	return notSupported("StopProxyService", u.orch)
}
func (u *unsupportedOrchestrator) RefreshProxyServiceDefinition(DatabaseProxy) error {
	return notSupported("RefreshProxyServiceDefinition", u.orch)
}
func (u *unsupportedOrchestrator) ProxyWorkingNode(DatabaseProxy) (string, error) {
	return "", notSupported("ProxyWorkingNode", u.orch)
}
func (u *unsupportedOrchestrator) AbortProxyService(DatabaseProxy) error {
	return notSupported("AbortProxyService", u.orch)
}
func (u *unsupportedOrchestrator) ClearProxyInstanceState(DatabaseProxy, string) error {
	return notSupported("ClearProxyInstanceState", u.orch)
}

func (u *unsupportedOrchestrator) ProvisionAppService(*App) error {
	return notSupported("ProvisionAppService", u.orch)
}
func (u *unsupportedOrchestrator) UnprovisionAppService(*App) error {
	return notSupported("UnprovisionAppService", u.orch)
}
func (u *unsupportedOrchestrator) StartAppService(*App, string) error {
	return notSupported("StartAppService", u.orch)
}
func (u *unsupportedOrchestrator) StopAppService(*App, string) error {
	return notSupported("StopAppService", u.orch)
}
func (u *unsupportedOrchestrator) RestartAppService(*App, string, string) error {
	return notSupported("RestartAppService", u.orch)
}
func (u *unsupportedOrchestrator) UpdateAppConfig(*App) error {
	return notSupported("UpdateAppConfig", u.orch)
}
func (u *unsupportedOrchestrator) RefreshAppServiceDefinition(*App) error {
	return notSupported("RefreshAppServiceDefinition", u.orch)
}
func (u *unsupportedOrchestrator) ProvisionAppRoutes(*App) error {
	return notSupported("ProvisionAppRoutes", u.orch)
}
func (u *unsupportedOrchestrator) WithdrawAppRoutes(*App) error {
	return notSupported("WithdrawAppRoutes", u.orch)
}
func (u *unsupportedOrchestrator) AbortAppService(*App) error {
	return notSupported("AbortAppService", u.orch)
}
func (u *unsupportedOrchestrator) ClearAppInstanceState(*App, string) error {
	return notSupported("ClearAppInstanceState", u.orch)
}

func (u *unsupportedOrchestrator) SecretStore() orchestratorStore {
	return &notSupportedStore{orch: u.orch}
}
func (u *unsupportedOrchestrator) ServerSecretStore(string) (credstore.StoreWriter, error) {
	return nil, notSupported("ServerSecretStore", u.orch)
}
func (u *unsupportedOrchestrator) InitCredentials() []initCredential { return nil }
func (u *unsupportedOrchestrator) RotateDatabasePassword(string) error {
	return notSupported("RotateDatabasePassword", u.orch)
}
func (u *unsupportedOrchestrator) UnprovisionSecret() error {
	return notSupported("UnprovisionSecret", u.orch)
}

func (u *unsupportedOrchestrator) TerminalCommand(TerminalRequest) (TerminalCommand, error) {
	return TerminalCommand{}, notSupported("TerminalCommand", u.orch)
}
func (u *unsupportedOrchestrator) DefaultContainer(TerminalTarget) (string, bool) { return "", false }

// notSupportedStore is the secret store of a backend without a shared secret: every write
// and read answers ErrOrchestratorNotSupported, so a caller never holds a nil store.
type notSupportedStore struct {
	orch OrchestratorInfo
}

func (s *notSupportedStore) WriteAll(_ context.Context, items []credstore.StoreItem, _ credstore.Timeout) ([]credstore.StoreItem, error) {
	return items, notSupported("SecretStore.WriteAll", s.orch)
}

func (s *notSupportedStore) ReadCurrent(string) (string, bool, error) {
	return "", false, notSupported("SecretStore.ReadCurrent", s.orch)
}

// compile-time check: notSupportedStore is an orchestratorStore.
var _ orchestratorStore = (*notSupportedStore)(nil)

// nullOrchestrator stands for an unknown prov-orchestrator: it has a name and no type, and
// every operation answers ErrOrchestratorNotSupported, so a caller never dereferences nil.
type nullOrchestrator struct {
	unsupportedOrchestrator
	cluster *Cluster
	name    string
}

var _ Orchestrator = (*nullOrchestrator)(nil)

func newNullOrchestrator(cluster *Cluster, name string) *nullOrchestrator {
	n := &nullOrchestrator{cluster: cluster, name: name}
	n.unsupportedOrchestrator.orch = n
	return n
}

func (n *nullOrchestrator) Name() string               { return n.name }
func (n *nullOrchestrator) Type() string               { return "" }
func (n *nullOrchestrator) Traits() OrchestratorTraits { return OrchestratorTraits{} }
func (n *nullOrchestrator) Check() error               { return notSupported("Check", n) }
func (n *nullOrchestrator) Layout(server *ServerMonitor) ServiceLayout {
	return n.cluster.databaseLayout(server)
}
func (n *nullOrchestrator) ProxyLayout(prx DatabaseProxy) ServiceLayout {
	return n.cluster.proxyLayout(prx)
}
