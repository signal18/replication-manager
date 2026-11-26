// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"time"

	"github.com/signal18/replication-manager/config/credstore"
	"github.com/signal18/replication-manager/opensvc"
)

// Orchestrator drives the services of a cluster on one infrastructure: it creates, starts,
// stops and removes the database, proxy and app services, holds the shared secret the
// services read, and knows the nodes they run on. One value per orchestrator (today the
// one synthesized from the cluster's prov-orchestrator and its flat keys, see
// newOrchestratorList); the cluster's servers resolve to one of them (cluster.orchestrator).
//
// Every method is synchronous and returns its error. A facet whose concept does not exist
// on a backend returns ErrOrchestratorNotSupported, never nil. The orchestration itself
// (the fan-out, the cookies, the failover state, provisioningMutex, the client script hooks
// of prov_scripts.go) stays in prov.go and calls these methods; it is not in the interface.
// Design: doc/implementation/cluster/ORCHESTRATOR_INTERFACE.md (#333, #338, #1775).
type Orchestrator interface {
	OrchestratorInfo
	NodeInventory
	DatabaseOrchestrator
	ProxyOrchestrator
	AppOrchestrator
	SecretOrchestrator
	TerminalOrchestrator
	HostOrchestrator
}

// OrchestratorInfo identifies an orchestrator and states what it can do.
type OrchestratorInfo interface {
	// Name is the configured name; for an orchestrator synthesized from the flat keys, the type.
	Name() string
	// Type is one of config.ConstOrchestratorOpenSVC, ConstOrchestratorKubernetes,
	// ConstOrchestratorOnPremise, ConstOrchestratorLocalhost, ConstOrchestratorSlapOS.
	Type() string
	Traits() OrchestratorTraits
	// Layout is where the database service keeps its data, configuration, binaries and socket.
	Layout(server *ServerMonitor) ServiceLayout
	ProxyLayout(prx DatabaseProxy) ServiceLayout
	// Check tells whether the orchestrator answers: reachable, credentials accepted.
	Check() error
}

// OrchestratorTraits are facts about a backend, not per-cluster settings: the settings that
// refine a trait (prov-net-cni, haproxy-mode, prov-*-start-fetch-config) stay where they
// are and are tested beside it. They replace the "== OpenSVC || == Kubernetes" checks.
type OrchestratorTraits struct {
	IsContainer         bool // the services are containers: configurator/distribution.go isContainerOrchestrator, the provisioned TLS CA path, the xtrabackup image, the run-as UID
	HasDNS              bool // the services have a DNS name: prx_has.go, app_has.go HasDNS
	HasSharedSecret     bool // a secret object the services read: store_orchestrator.go
	HasPlacement        bool // the agents mean something: prov-db-agents, placementNode, CheckClusterServiceAgents
	HasPlanDisk         bool // the provisioned disk is the plan's: srv_binlog_budget.go planDiskBytes
	HasImage            bool // the service runs an image: prov_image.go ResolveDatabaseImage, the two-phase pull upgrade
	RunsJobsInContainer bool // the jobs run in a container beside the database: cluster_chk.go WARN0170 gate, cluster_sec_job_login.go
	StartFetchesConfig  bool // a start re-fetches the configuration: prov.go startReappliesProxyConfig
	ProvisionsNewHosts  bool // a child or self-service cluster can be provisioned on new servers: server_child_cluster.go, server_selfservice.go
	HasTerminal         bool // a shell into the service container: server_tty.go, api.go
	HasAtomicRestart    bool // a restart that is not a stop then a start: api_database.go restartSupportedForOrchestrator
	HasHost             bool // the backend drives the machine under a database server (a hypervisor): the HostOrchestrator facet, the host step of the rolling upgrade
}

// ServiceLayout is where a service keeps its data, configuration, binaries and socket, and
// what it binds to. It replaces the path switches of srv_cnf.go, srv_get.go, prx_get.go and
// app_get.go.
type ServiceLayout struct {
	Datadir       string
	Confdir       string
	Basedir       string
	ClientBasedir string
	Binary        string
	Socket        string
	BindAddress   string
	Domain        string
}

// NodeInventory lists what the orchestrator runs on.
type NodeInventory interface {
	Nodes() ([]Agent, error)
	// Storages lists the storage an orchestrator offers: the OpenSVC pools, the Kubernetes
	// storage classes.
	Storages() ([]Storage, error)
	NodeStats() ([]NodeStats, error)
}

// Storage is one storage an orchestrator offers. The fields are those the two existing API
// endpoints return (the OpenSVC pool info, the Kubernetes storage class name).
type Storage struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"` // "pool" (OpenSVC), "storageclass" (Kubernetes)
	Shared       bool     `json:"shared"`
	Capabilities []string `json:"capabilities"`
}

// NodeStats is the per-node usage an orchestrator reports; OpenSVC is the only producer today.
type NodeStats = opensvc.DaemonNodeStats

// DatabaseOrchestrator drives one database service.
type DatabaseOrchestrator interface {
	ProvisionDatabaseService(server *ServerMonitor) error
	UnprovisionDatabaseService(server *ServerMonitor) error
	StartDatabaseService(server *ServerMonitor) error
	StopDatabaseService(server *ServerMonitor) error
	// RestartDatabaseService is the backend's own restart (an atomic restart with a resource
	// id, a pod replacement). ErrOrchestratorNotSupported: the caller runs stop, wait, start.
	RestartDatabaseService(server *ServerMonitor, node string, rid string) error
	FreezeDatabaseService(server *ServerMonitor) error
	UnfreezeDatabaseService(server *ServerMonitor) error
	// UpdateDatabaseServiceConfig pushes the image and its pull policy.
	UpdateDatabaseServiceConfig(server *ServerMonitor, forcePull bool) error
	// RefreshDatabaseServiceDefinition re-renders and pushes the whole service definition
	// (image, resources, run args, env, volumes), #1775. keepImage keeps the image the
	// service runs, whatever prov-db-image says (a restart never changes the version).
	RefreshDatabaseServiceDefinition(server *ServerMonitor, keepImage bool) error
	// UpgradeDatabaseService is the backend's own upgrade (the on-premise script).
	// ErrOrchestratorNotSupported: the caller runs the two-phase pull.
	UpgradeDatabaseService(server *ServerMonitor) error
	// DatabaseServiceDefinition is the definition as the backend holds it.
	DatabaseServiceDefinition(server *ServerMonitor) ([]byte, error)
	// DatabaseServiceImage is the image the service runs today.
	DatabaseServiceImage(server *ServerMonitor) (string, error)
	DatabaseWorkingNode(server *ServerMonitor) (string, error)
	AbortDatabaseService(server *ServerMonitor) error
	ClearDatabaseInstanceState(server *ServerMonitor, node string) error
}

// ProxyOrchestrator drives one proxy service.
type ProxyOrchestrator interface {
	ProvisionProxyService(prx DatabaseProxy) error
	UnprovisionProxyService(prx DatabaseProxy) error
	StartProxyService(prx DatabaseProxy) error
	StopProxyService(prx DatabaseProxy) error
	RefreshProxyServiceDefinition(prx DatabaseProxy) error
	ProxyWorkingNode(prx DatabaseProxy) (string, error)
	AbortProxyService(prx DatabaseProxy) error
	ClearProxyInstanceState(prx DatabaseProxy, node string) error
}

// AppOrchestrator drives one app service.
type AppOrchestrator interface {
	ProvisionAppService(app *App) error
	UnprovisionAppService(app *App) error
	StartAppService(app *App, node string) error
	StopAppService(app *App, node string) error
	RestartAppService(app *App, node string, rid string) error
	// UpdateAppConfig pushes the variable maps and secrets without a restart.
	UpdateAppConfig(app *App) error
	// RefreshAppServiceDefinition re-renders and pushes the whole definition without a restart.
	RefreshAppServiceDefinition(app *App) error
	ProvisionAppRoutes(app *App) error
	WithdrawAppRoutes(app *App) error
	AbortAppService(app *App) error
	ClearAppInstanceState(app *App, node string) error
}

// SecretOrchestrator is the shared secret the services of a cluster read.
type SecretOrchestrator interface {
	// SecretStore is the cluster's secret as a copy sink (store_orchestrator.go). Never nil: a
	// backend without a shared secret returns a writer whose every method returns
	// ErrOrchestratorNotSupported.
	SecretStore() orchestratorStore
	ServerSecretStore(namespace string) (credstore.StoreWriter, error)
	InitCredentials() []initCredential
	RotateDatabasePassword(password string) error
	UnprovisionSecret() error
}

// TerminalOrchestrator opens a shell on a service: in its container, or on its host.
type TerminalOrchestrator interface {
	TerminalCommand(req TerminalRequest) (TerminalCommand, error)
	// DefaultContainer is the container a shell opens in when the request names none.
	DefaultContainer(target TerminalTarget) (string, bool)
}

// TerminalTarget is the kind of service a terminal session opens on. The values are the
// ones server/server_tty.go routes on.
type TerminalTarget string

const (
	TerminalTargetServer TerminalTarget = "server"
	TerminalTargetProxy  TerminalTarget = "proxy"
	TerminalTargetApp    TerminalTarget = "app"
)

// TerminalRequest names the service a shell opens on.
type TerminalRequest struct {
	Target      TerminalTarget
	Host        string // the service's host, for a shell on the host (SSH)
	ServiceName string // the orchestrator's service name, for a shell in the container
	Agent       string // the node the service runs on, when known
	RID         string // the container resource; empty for DefaultContainer
}

// TerminalCommand is how the shell is reached: a URL served by the orchestrator (a shell in
// the container), or an SSH login on the host. The fields a backend does not use stay empty.
type TerminalCommand struct {
	URL      string
	Node     string
	Version  string
	Host     string
	Port     string
	Username string
	Password string
	Key      string
}

// HostOrchestrator is the host under a database server (its VM), for a backend that drives a
// hypervisor (design section 10.4). The other backends answer ErrOrchestratorNotSupported. The
// service on the host stays the DatabaseOrchestrator's: a host backend owns the machine and
// nothing inside it. Each method returns once the hypervisor reports the action done, bounded
// by prov-db-host-reboot-timeout.
type HostOrchestrator interface {
	// StopHost shuts the VM down cleanly (never a hard power-off: the guest holds a database).
	StopHost(server *ServerMonitor) error
	StartHost(server *ServerMonitor) error
	// RebootHost reboots the VM: a clean shutdown, then a start.
	RebootHost(server *ServerMonitor) error
	HostStatus(server *ServerMonitor) (HostStatus, error)
}

// HostStatus is what the hypervisor says of the VM under a server.
type HostStatus struct {
	Node   string        `json:"node"` // the hypervisor node the VM runs on
	VMID   string        `json:"vmid"`
	State  string        `json:"state"` // "running", "stopped", "paused", "unknown"
	Uptime time.Duration `json:"-"`     // the API serves it in seconds (handlerMuxServerHostStatus)
}

// ErrOrchestratorNotSupported: the orchestrator has no such operation (a freeze on
// Kubernetes, an app service on on-premise, a shared secret on localhost). Returned, never
// nil: the caller decides between its generic path, a refusal to the user (HTTP 501), or a
// state. Wrapped with the operation and the orchestrator: errors.Is tests it.
var ErrOrchestratorNotSupported = errors.New("operation not supported by this orchestrator")

// notSupported is the error every unsupported method returns.
func notSupported(op string, orch OrchestratorInfo) error {
	return fmt.Errorf("%w: %s on %s (%s)", ErrOrchestratorNotSupported, op, orch.Name(), orch.Type())
}
