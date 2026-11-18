// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
)

// openSVCOrchestrator is the OpenSVC backend of the Orchestrator interface. Migration step
// 1: every method calls the OpenSVC* method of prov_opensvc*.go as the switch statements of
// prov.go call it today; the methods move here as their callers go through the interface.
type openSVCOrchestrator struct {
	unsupportedOrchestrator
	cluster *Cluster
	name    string
}

var _ Orchestrator = (*openSVCOrchestrator)(nil)

func newOpenSVCOrchestrator(cluster *Cluster, name string) *openSVCOrchestrator {
	o := &openSVCOrchestrator{cluster: cluster, name: name}
	o.unsupportedOrchestrator.orch = o
	return o
}

func (o *openSVCOrchestrator) Name() string { return o.name }
func (o *openSVCOrchestrator) Type() string { return config.ConstOrchestratorOpenSVC }

func (o *openSVCOrchestrator) Traits() OrchestratorTraits {
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
		HasTerminal:         true,
		HasAtomicRestart:    true,
	}
}

func (o *openSVCOrchestrator) Layout(server *ServerMonitor) ServiceLayout {
	return o.cluster.databaseLayout(server)
}

func (o *openSVCOrchestrator) ProxyLayout(prx DatabaseProxy) ServiceLayout {
	return o.cluster.proxyLayout(prx)
}

// Check asks the daemon for its nodes: reachable, certificate or account accepted.
func (o *openSVCOrchestrator) Check() error {
	_, err := o.Nodes()
	return err
}

func (o *openSVCOrchestrator) Nodes() ([]Agent, error) { return o.cluster.OpenSVCGetNodes() }

func (o *openSVCOrchestrator) Storages() ([]Storage, error) {
	pools, err := o.cluster.OpenSVCGetPoolInfoList()
	if err != nil {
		return nil, err
	}
	storages := make([]Storage, 0, len(pools))
	for _, pool := range pools {
		storages = append(storages, Storage{Name: pool.Name, Type: "pool", Shared: pool.Shared, Capabilities: pool.Capabilities})
	}
	return storages, nil
}

func (o *openSVCOrchestrator) NodeStats() ([]NodeStats, error) {
	svc := o.cluster.OpenSVCConnect()
	return svc.GetDaemonNodeStats()
}

func (o *openSVCOrchestrator) ProvisionDatabaseService(server *ServerMonitor) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.OpenSVCProvisionDatabaseService(server) })
}

func (o *openSVCOrchestrator) UnprovisionDatabaseService(server *ServerMonitor) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.OpenSVCUnprovisionDatabaseService(server) })
}

func (o *openSVCOrchestrator) StartDatabaseService(server *ServerMonitor) error {
	return o.cluster.OpenSVCStartDatabaseService(server)
}

func (o *openSVCOrchestrator) StopDatabaseService(server *ServerMonitor) error {
	return o.cluster.OpenSVCStopDatabaseService(server)
}

// RestartDatabaseService is the atomic restart of one resource (container#jobs); without a
// resource id the caller runs stop, wait, start, as prov.go does today.
func (o *openSVCOrchestrator) RestartDatabaseService(server *ServerMonitor, node string, rid string) error {
	if rid == "" {
		return notSupported("RestartDatabaseService", o)
	}
	return o.cluster.OpenSVCRestartDatabaseService(server, node, rid)
}

// FreezeDatabaseService and UnfreezeDatabaseService are the v3 instance freeze; on the
// collector API (v2) they do nothing, as prov.go's today.
func (o *openSVCOrchestrator) FreezeDatabaseService(server *ServerMonitor) error {
	return o.cluster.FreezeDatabaseService(server)
}

func (o *openSVCOrchestrator) UnfreezeDatabaseService(server *ServerMonitor) error {
	return o.cluster.UnfreezeDatabaseService(server)
}

func (o *openSVCOrchestrator) UpdateDatabaseServiceConfig(server *ServerMonitor, forcePull bool) error {
	return o.cluster.OpenSVCUpdateDatabaseServiceConfig(server, forcePull)
}

// RefreshDatabaseServiceDefinition is the full re-render and push of the service definition
// (OpenSVCUpdateDatabaseTemplate), the v3 API only: the collector API has no such call.
// keepImage is the restart rule of UpgradeDatabaseDeploymentOnStart (#1861), which keeps
// calling its own copy until migration step 4 routes it here.
func (o *openSVCOrchestrator) RefreshDatabaseServiceDefinition(server *ServerMonitor, keepImage bool) error {
	svc := o.cluster.OpenSVCConnect()
	if !svc.IsV3() {
		return notSupported("RefreshDatabaseServiceDefinition", o)
	}
	if keepImage {
		if img := o.cluster.openSVCCurrentDatabaseImage(server); img != "" && img != o.cluster.Conf.ProvDbImg {
			o.cluster.LogModulePrintf(o.cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
				"Restart keeps the image %s runs (%s), prov-db-image %s is for the rolling upgrade", server.URL, img, o.cluster.Conf.ProvDbImg)
			server.DeployImageOverride = img
			defer func() { server.DeployImageOverride = "" }()
		}
	}
	return o.cluster.OpenSVCUpdateDatabaseTemplate(server)
}

func (o *openSVCOrchestrator) DatabaseServiceDefinition(server *ServerMonitor) ([]byte, error) {
	return o.cluster.GetDatabaseServiceConfig(server), nil
}

func (o *openSVCOrchestrator) DatabaseServiceImage(server *ServerMonitor) (string, error) {
	img := o.cluster.openSVCCurrentDatabaseImage(server)
	if img == "" {
		return "", fmt.Errorf("image of %s unknown: the service configuration could not be read", server.URL)
	}
	return img, nil
}

func (o *openSVCOrchestrator) DatabaseWorkingNode(server *ServerMonitor) (string, error) {
	if err := server.GetWorkingOrchestratorNode(); err != nil {
		return "", err
	}
	return server.GetWorkingAgent(), nil
}

// openSVCNotSupported turns the OpenSVC API-version refusals into the interface's sentinel;
// the OpenSVC sentinels stay until their last direct caller goes (migration step 3).
func openSVCNotSupported(err error) error {
	if errors.Is(err, ErrOpenSVCAbortNotSupported) || errors.Is(err, ErrOpenSVCClearNotSupported) {
		return fmt.Errorf("%w: %w", ErrOrchestratorNotSupported, err)
	}
	return err
}

func (o *openSVCOrchestrator) AbortDatabaseService(server *ServerMonitor) error {
	return openSVCNotSupported(o.cluster.OpenSVCAbortDatabaseService(server))
}

func (o *openSVCOrchestrator) ClearDatabaseInstanceState(server *ServerMonitor, node string) error {
	return openSVCNotSupported(o.cluster.OpenSVCClearDatabaseInstanceState(server, node))
}

func (o *openSVCOrchestrator) ProvisionProxyService(prx DatabaseProxy) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.OpenSVCProvisionProxyService(prx) })
}

func (o *openSVCOrchestrator) UnprovisionProxyService(prx DatabaseProxy) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.OpenSVCUnprovisionProxyService(prx) })
}

func (o *openSVCOrchestrator) StartProxyService(prx DatabaseProxy) error {
	return o.cluster.OpenSVCStartProxyService(prx)
}

func (o *openSVCOrchestrator) StopProxyService(prx DatabaseProxy) error {
	return o.cluster.OpenSVCStopProxyService(prx)
}

func (o *openSVCOrchestrator) ProxyWorkingNode(prx DatabaseProxy) (string, error) {
	if err := prx.GetWorkingOrchestratorNode(); err != nil {
		return "", err
	}
	return prx.GetWorkingAgent(), nil
}

func (o *openSVCOrchestrator) AbortProxyService(prx DatabaseProxy) error {
	return openSVCNotSupported(o.cluster.OpenSVCAbortProxyService(prx))
}

func (o *openSVCOrchestrator) ClearProxyInstanceState(prx DatabaseProxy, node string) error {
	return openSVCNotSupported(o.cluster.OpenSVCClearProxyInstanceState(prx, node))
}

func (o *openSVCOrchestrator) ProvisionAppService(app *App) error {
	return o.cluster.awaitProvisioning(func() { o.cluster.OpenSVCProvisionAppService(app) })
}

func (o *openSVCOrchestrator) UnprovisionAppService(app *App) error {
	return o.cluster.OpenSVCUnprovisionAppService(app)
}

func (o *openSVCOrchestrator) StartAppService(app *App, node string) error {
	return o.cluster.OpenSVCStartAppService(app, node)
}

func (o *openSVCOrchestrator) StopAppService(app *App, node string) error {
	return o.cluster.OpenSVCStopAppService(app, node)
}

func (o *openSVCOrchestrator) RestartAppService(app *App, node string, rid string) error {
	return o.cluster.OpenSVCRestartAppService(app, node, rid)
}

func (o *openSVCOrchestrator) UpdateAppConfig(app *App) error {
	return o.cluster.OpenSVCUpdateAppConfig(app)
}

func (o *openSVCOrchestrator) RefreshAppServiceDefinition(app *App) error {
	return o.cluster.OpenSVCUpdateAppTemplate(app)
}

func (o *openSVCOrchestrator) ProvisionAppRoutes(app *App) error {
	return o.cluster.OpenSVCProvisionRoute(app)
}

func (o *openSVCOrchestrator) WithdrawAppRoutes(app *App) error {
	return o.cluster.withdrawGatewayRoutes(app)
}

func (o *openSVCOrchestrator) AbortAppService(app *App) error {
	return openSVCNotSupported(o.cluster.OpenSVCAbortAppService(app))
}

func (o *openSVCOrchestrator) ClearAppInstanceState(app *App, node string) error {
	return openSVCNotSupported(o.cluster.OpenSVCClearAppInstanceState(app, node))
}

func (o *openSVCOrchestrator) SecretStore() orchestratorStore { return o.cluster.openSVCStore() }

func (o *openSVCOrchestrator) ServerSecretStore(namespace string) (credstore.StoreWriter, error) {
	return o.cluster.ServerOrchestratorStore(config.ConstSecretKvPluginOpenSVC, namespace)
}

func (o *openSVCOrchestrator) InitCredentials() []initCredential { return o.cluster.initCredentials() }

func (o *openSVCOrchestrator) RotateDatabasePassword(password string) error {
	return o.cluster.writeOpenSVCRootPassword(o.cluster.OpenSVCConnect(), password)
}

func (o *openSVCOrchestrator) UnprovisionSecret() error {
	o.cluster.OpenSVCUnprovisionSecret()
	return nil
}

// TerminalCommand is the shell served in the service's container (gotty), as api.go opens it.
func (o *openSVCOrchestrator) TerminalCommand(req TerminalRequest) (TerminalCommand, error) {
	rid := req.RID
	if rid == "" {
		var ok bool
		if rid, ok = o.DefaultContainer(req.Target); !ok {
			return TerminalCommand{}, fmt.Errorf("no default container for a %s terminal", req.Target)
		}
	}
	url, node, version := o.cluster.GetGottyServer(req.ServiceName, rid, req.Agent)
	return TerminalCommand{URL: url, Node: node, Version: version}, nil
}

// DefaultContainer: the database and app containers; a proxy terminal names its resource.
func (o *openSVCOrchestrator) DefaultContainer(target TerminalTarget) (string, bool) {
	switch target {
	case TerminalTargetServer:
		return "container#db", true
	case TerminalTargetApp:
		return "container#app", true
	}
	return "", false
}
