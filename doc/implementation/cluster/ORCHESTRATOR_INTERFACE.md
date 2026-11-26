# Orchestrator plugin interface (design, refs #333, #338, #1775)

Status: design approved by the maintainer on 2026-11-18 (the decisions are recorded in
section 8; the three open points of section 8 stay open). Section 9 scopes the first
implementation pull request. No implementation code is in this branch.

The provisioning layer of replication-manager drives five backends (OpenSVC, Kubernetes,
on-premise over SSH, localhost processes, SlapOS) through `switch cluster.GetOrchestrator()`
and `if ... == config.ConstOrchestratorOpenSVC` statements spread over the `cluster`,
`server`, `mcp`, `configurator` and `regtest` packages. Law T7 asks for one Go interface
with several backends, as `DatabaseProxy` does for the routers; F6 asks that the client
can replace every action with a script. #333 (2021) asked for the interface and for named
orchestrators in the configuration; #338 for an external-script backend; #1775 for the
full service-definition refresh behind the interface. This document is the design the
next major implements.

Measured on `origin/develop` at `3fe5c6578` (2026-11-18):

```
git grep -n -E 'ConstOrchestrator|ProvOrchestrator\s*(==|!=)' -- '*.go' | grep -v _test | wc -l
227
```

Of the 227 lines: 15 are the constants and `GetOrchestratorsProv` in `config/config.go`,
16 are the regtest targets and skips; **196 are dispatch or capability checks in the
product** (`cluster/` 171, `server/` 21, `mcp/` 2, `cluster/configurator/` 6 counted in
`cluster/`). `cluster/prov.go` alone holds 79. On top of these, **27 calls from `server/`
and `mcp/` reach a backend method directly** (`mycluster.OpenSVC*`, `mycluster.K8S*`,
`mycluster.OnPremiseGetSSHKey`) with no dispatch at all: the app actions, abort and
clear-instance-state, the OpenSVC template push, the Kubernetes manifests and storage
classes, the OpenSVC pools, the SSH key for the terminal. They are the hidden half of the
inventory and are listed with the rest.

The precedent to copy is `DatabaseProxy` (`cluster/prx.go`): one interface, the
implementations in the `cluster` package (`cluster/prx_haproxy.go`, `prx_proxysql.go`,
`prx_maxscale.go`, `prx_mariadbshardproxy.go`, `prx_myproxy.go`, with the protocol clients
under `router/`), one registry (`newProxyList()`), the callers hold the interface. A
`DatabaseOrchetrator` interface (misspelled) already exists in `cluster/orchestrator.go`
since 2021 with eleven methods and no implementation: nothing calls it. The design
replaces it (T2, T19) rather than adding a second one beside it.

---

## 1. Inventory of the dispatch

Operations, where they dispatch today, which backends answer, and which backends fall
through a `default:` or a `return nil` without saying so ("silent no-op"). File paths are
relative to the repository root; line numbers are those of `3fe5c6578`.

### 1.1 Database service lifecycle

| Operation | Site (file:function) | OpenSVC | Kubernetes | on-premise | localhost | SlapOS | Silent no-op |
|---|---|---|---|---|---|---|---|
| provision (all) | `cluster/prov.go:ProvisionServices` | yes | yes | yes | yes | cookie only | unknown type falls through `default:` then blocks on `errorChan` |
| provision (one) | `cluster/prov.go:InitDatabaseService` | yes | yes | yes | yes | cookie only | unknown type returns nil |
| unprovision (all) | `cluster/prov.go:Unprovision` | yes | yes | yes | yes | cookie only | unknown type falls through |
| unprovision (one) | `cluster/prov.go:UnprovisionDatabaseService` | yes | yes | yes | default branch | cookie only | none: `default:` is localhost |
| start | `cluster/prov.go:StartDatabaseService` | yes | yes | SSH script | process | cookie only | unknown type: "No valid orchestrator" |
| stop | `cluster/prov.go:StopDatabaseService` | yes | yes | `OnPremiseStop` (SQL SHUTDOWN) | **`OnPremiseStop`**, not `LocalhostStop` | SQL shutdown | unknown type: error |
| clean stop (upgrade) | `cluster/prov.go:StopDatabaseServiceClean` | through stop | through stop | through stop | through stop | through stop | none |
| restart | `cluster/prov.go:RestartDatabaseService` | atomic restart with `rid` | pod repull | stop, wait, start | stop, wait, start | stop, wait, start | none |
| restart allowed (API) | `server/api_database.go:restartSupportedForOrchestrator` | yes | yes | 501 | 501 | 501 | the HTTP 501 is explicit |
| freeze / unfreeze | `cluster/prov.go:FreezeDatabaseService`, `UnfreezeDatabaseService` | v3 only | **nil** | **nil** | **nil** | **nil** | every other backend returns nil |
| upgrade (one) | `cluster/prov.go:UpgradeDatabaseService` | two-phase pull in the caller | two-phase pull | SSH upgrade script | two-phase pull (no image: no-op) | two-phase pull (no-op) | localhost and SlapOS run a pull cycle that changes nothing |
| abort | `server/api_database.go:2783` -> `OpenSVCAbortDatabaseService` | yes | - | - | - | - | API handler is OpenSVC-only, no dispatch |
| clear instance state | `server/api_database.go:2833` -> `OpenSVCClearDatabaseInstanceState` | yes | - | - | - | - | same |
| working node of a service | `cluster/srv.go:GetWorkingOrchestratorNode`, `cluster/prx.go:(*Proxy).GetWorkingOrchestratorNode` | yes | **nil** | nil | nil | nil | Kubernetes has a node (the pod's) and does not report it |
| placement node | `cluster/prov.go:placementNode` | yes | through agent | agent | agent | agent | none |
| database agent | `cluster/prov.go:GetDatabaseAgent`, `GetDatabaseAgentNames`, `GetAgentInOrchetrator` | agents | nodes | hosts | localhost | partitions | none (generic over `cluster.Agents`) |
| bootstrap jobs table (test) | `cluster/prov.go:Bootstrap:122` | yes | **no** | no | no | no | Kubernetes runs the same jobs containers |

### 1.2 Service definition (#1775)

| Operation | Site | OpenSVC | Kubernetes | on-premise | localhost | SlapOS | Silent no-op |
|---|---|---|---|---|---|---|---|
| update image / pull policy | `cluster/prov.go:UpdateDatabaseServiceConfig` | `image_pull_policy` | image + pull policy | **nil** | **nil** | **nil** | three |
| full definition refresh | `cluster/prov.go:UpgradeDatabaseDeploymentOnStart` | v3: `OpenSVCUpdateDatabaseTemplate`; v2: nil | `k8sUpdateDatabaseServiceConfigKeepImage` (image and pull policy only, not the full spec) | **nil** | **nil** | **nil** | the issue #1775 |
| full definition refresh (API) | `server/api_database.go:2934` `update-opensvc-template` -> `OpenSVCUpdateDatabaseTemplate` | yes | - | - | - | - | OpenSVC-named action, no dispatch |
| rolling plan: push definitions | `cluster/cluster_roll_plan.go:215,291` | yes | **no** | n/a (plan ends at 264) | no | no | Kubernetes definitions are not pushed by the plan |
| rolling restart/upgrade step | `cluster/cluster_roll.go:437,492` | generic | `isKubernetes` branch | `rollingUpgradeOnPremise` | generic | generic | none |
| current image of the service | `cluster/prov.go:openSVCCurrentDatabaseImage` | yes | - | - | - | - | OpenSVC-only helper, called from the OpenSVC branch |
| service definition read (API) | `server/api_database.go:4768` | `GetDatabaseServiceConfig` | `K8SGetDatabaseManifests` | default: error | error | error | explicit |
| image resolution | `cluster/prov_image.go:ResolveDatabaseImage:50` | yes | yes | **skipped** | yes | yes | on-premise has no image: correct, but by name |

### 1.3 Proxy services

| Operation | Site | OpenSVC | Kubernetes | on-premise | localhost | SlapOS | Silent no-op |
|---|---|---|---|---|---|---|---|
| provision / unprovision / start / stop | `cluster/prov.go:ProvisionServices`, `InitProxyService`, `Unprovision`, `UnprovisionProxyService`, `StopProxyService`, `StartProxyService` | yes | yes | yes | yes | cookie only | unknown type falls through; `proxyServiceOrchestrator` routes HAProxy standby to localhost whatever the cluster |
| start reapplies config | `cluster/prov.go:startReappliesProxyConfig` | if fetch-config | if fetch-config | false | true | false | capability by name |
| abort / clear instance state | `server/api_proxy.go:218,268` -> `OpenSVC*ProxyService` | yes | - | - | - | - | OpenSVC-only handlers |
| proxy FQDN | `cluster/prx_haproxy.go:273`, `prx_maxscale.go:53`, `prx_proxysql.go:51` | `prov-orchestrator-cluster` | `k8sClusterDomain` | n/a | n/a | n/a | capability by name |
| local HAProxy render | `cluster/prx_haproxy.go:359` | no | no | no | yes (and standby) | no | capability by name |
| ProxySQL default user | `cluster/cluster_get.go:799` | external | external | external | admin | external | by name |
| bind address, datadir, confdir | `cluster/prx_get.go:174,211,214,222` | `/var/lib/<type>` | same | same | same | partition paths | layout by name |
| DNS presence | `cluster/prx_has.go:80` | true | true | by host | by host | by host | capability by name |

### 1.4 App services

| Operation | Site | OpenSVC | others | Silent no-op |
|---|---|---|---|---|
| provision | `cluster/prov.go:InitAppService` | yes | **return nil** | four backends |
| unprovision | `server/api_app.go:1182`, `mcp/tools_app.go:153` -> `OpenSVCUnprovisionAppService` | yes | mcp answers "implemented for the OpenSVC orchestrator only" | API has no check |
| start / stop / restart | `server/api_app.go:699,766,856`, `mcp/tools_app.go:130,187,201-205` | yes | mcp refuses; API calls OpenSVC whatever the cluster | API |
| abort / clear | `server/api_app.go:906,956` | yes | - | - |
| update config / template / route | `server/api_app.go:1055,1104,1146,3501` | yes | - | - |
| drop app: gateway routes, DNS | `cluster/cluster_del.go:228` | yes | **no** | Kubernetes apps keep their routes |
| layout, bind, DNS | `cluster/app_get.go:501,514,517,525`, `app_has.go:81` | as proxies | as proxies | by name |
| compute sensor units | `cluster/cluster_compute_sensor.go:204` | v3 | **no** | by name |

### 1.5 Cluster level and bootstrap

| Operation | Site | OpenSVC | Kubernetes | on-premise | localhost | SlapOS | Note |
|---|---|---|---|---|---|---|---|
| nodes / agents inventory | `cluster/cluster.go:initOrchetratorNodes:1104` | `OpenSVCGetNodes` | `K8SGetNodes` | `OnPremiseGetNodes` | `LocalhostGetNodes` | `SlapOSGetNodes` | unknown type: `log.Fatalln` (kills the monitor, F2) |
| storage inventory (API) | `server/api_cluster.go:10892,10939` | `OpenSVCGetPoolInfoList` | `K8SGetStorageClasses` | - | - | - | two endpoints, no dispatch |
| daemon node stats | `cluster/prov.go:ReloadOpenSVCDaemonNodeStats`, `cluster/cluster_get.go:GetOpenSVCStats:1845`, `cluster/cluster_chk.go:CheckOpenSVCTresholds:1294`, `server/server.go:2671,2676` | yes | no | no | no | no | OpenSVC-only, by name |
| service agents check | `cluster/cluster_chk.go:CheckClusterServiceAgents:1472` | yes | **no** | no | no | no | Kubernetes pods move too |
| jobs version / jobs containers | `cluster/cluster_chk.go:CheckJobsVersion:1425`, `cluster_set.go:SetRollingJobsUpgradeState:3089`, `srv_job_postgres.go:255`, `srv_wal_archive.go:97` | yes | **no** | n/a | n/a | n/a | Kubernetes runs jobs containers too |
| dbjobs need scheduler + SSH | `cluster/cluster_chk.go:1352,1404` | no | no | yes | yes | no | capability by name |
| certificate load check | `cluster/cluster_chk.go:CheckHasFailCertLoadP12:1504` | yes | - | - | - | - | OpenSVC-only, correct |
| orchestrator default | `cluster/cluster_set.go:SetProvOrchestrator:2208` | | | fallback | | | an unknown value becomes on-premise with an ERROR log and no state |
| add servers / proxies in test mode | `cluster/cluster_set.go:1814-1900` | `dbN` | `dbN` | none | free port | `dbN` | by name |
| DB tags for localhost | `cluster/cluster.go:1044` | | | | pkg, no docker | | by name |
| service plan DBU | `cluster/cluster.go:1603` | plan | plan | computed | computed | plan | by name |
| hosts known | `cluster/cluster_has.go:229` | | | true | | | by name |
| Galera SST account check | `cluster/cluster_sec_galera_sst.go:30` | yes | yes | skipped | yes | yes | by name |
| jobs login from container | `cluster/cluster_sec_job_login.go:395` | docker/podman | true | false | false | false | by name |
| child / staging cluster | `server/server_child_cluster.go:120` | yes | yes | refused | refused | refused | explicit error |
| self-service | `server/server_selfservice.go:56,418` | yes | yes | refused | refused | refused | explicit |
| bootstrap DR URL | `cluster/bootstrap_dr.go:98` | v3 | error | error | error | error | explicit "OpenSVC cluster API only" |
| domain of a service | `cluster/cluster_get.go:GetDomain:372,378`, `GetDomainHeadCluster:391,397` | `prov-orchestrator-cluster` | `k8sClusterDomain` | "" | "" | "" | by name |
| plan disk for the binlog budget | `cluster/srv_binlog_budget.go:152` | yes | yes | 0 | 0 | 0 | by name |
| provisioned TLS CA path | `cluster/srv_set.go:1165` | yes | yes | own | own | own | by name |
| container orchestrator | `cluster/configurator/distribution.go:isContainerOrchestrator:128` | yes | yes | no | no | no | by name |
| config rendering paths | `cluster/configurator/configurator.go:962,1383,1393,1397`, `cluster/srv_cnf.go:147-186`, `cluster/srv_get.go:208,1519` | container paths | container paths | host paths | `server.Datadir` | partition paths | layout by name |
| settings refused by orchestrator | `cluster/cluster_set.go:2239,2278` (xtrabackup image, run-as UID) | yes | yes | refused | refused | refused | explicit |
| Kubernetes resource sensor | `cluster/prov_k8s_db.go:962` | - | yes | - | - | - | correct, by name |
| OpenSVC pg cap | `cluster/cluster_resize_dynamic.go:1744,1758` | v3 | - | - | - | - | correct, by name |

### 1.6 Resize and dynamic resources

| Operation | Site | OpenSVC | Kubernetes | on-premise | localhost | SlapOS | Note |
|---|---|---|---|---|---|---|---|
| resizer selection | `cluster/cluster_resize_dynamic.go:resourceResizer:135` | `openSVCResizer` | `k8sResizer` or script | script | script | script | already an interface (`resourceResizer`); the switch picks the implementation |
| cpu step | `cluster/cluster_resize_dynamic.go:794,801` | v3 cgroup | pod patch | - | - | - | by name inside a generic loop |
| client change script | `cluster/prov_scripts.go:RunDynamicResourceChangeScript` | override | override | override | override | override | F6: already orchestrator-agnostic |

### 1.7 Secrets, credentials, password rotation

This is the inventory of today: a shared secret, a server credential store, the init
credentials, the root-password rotation and its drift check exist for OpenSVC and
Kubernetes only, and the on-premise, localhost and SlapOS clusters have none of it. **It is the
gap the design closes**: the secret facet is mandatory on every backend (2.5, section 11), and
the on-premise backend gets it in step 2, with the client script, because the paying client of
section 10 runs on-premise.

| Operation | Site | OpenSVC | Kubernetes | others | Note |
|---|---|---|---|---|---|
| orchestrator secret writer | `cluster/store_orchestrator.go:orchestratorStoreWriter:210` | `openSVCStore` | `k8sStore` | nil | already an interface (`orchestratorStore`); callers test for nil |
| server credential store | `cluster/store_orchestrator.go:ServerOrchestratorStore:287` | yes | yes | error | explicit |
| init credentials | `cluster/store_orchestrator.go:initCredentials:318` | `REPLICATION_MANAGER_PASSWORD` | `REPMAN_AUTH_HEADER` | nil | explicit |
| rotate root password | `cluster/prov.go:ProvisionRotatePasswords:1331` | `writeOpenSVCRootPassword` | `k8sRotatePasswordsWithClient` | **nil** | three silent |
| admin password follows rotation | `cluster/cluster_user_rotate.go:192` | queue | queue | nothing | by name |
| root password drift check | `cluster/cluster_secret_kv.go:checkOpenSVCRootPassword:1244` | yes | **no** | no | Kubernetes has the same secret and no drift check |
| unprovision secret | `cluster/prov.go:Unprovision:454` | `OpenSVCUnprovisionSecret` | **no** | no | the Kubernetes Secret stays after unprovision |
| mandatory server stores | `server/server_secret_kv.go:336` | yes | yes | - | table by type |

### 1.8 Terminal and exec

| Operation | Site | OpenSVC | others | Note |
|---|---|---|---|---|
| terminal enabled | `server/server.go:1933,1994` | kept | forced off at load | by name, before the cluster exists |
| terminal in a container | `server/server_tty.go:53,64`, `server/api.go:2714` (gotty) | container `rid` | refused | by name |
| SSH key for the terminal | `server/server_tty.go:151,241` -> `OnPremiseGetSSHKey` | - | on-premise | direct call |

### 1.9 Settings and GUI

`config.GetOrchestratorsProv` (`config/config.go:3157`) lists the five types with their
availability from `prov-orchestrator-enable`; the dashboard reads `provOrchestrator` in 33
places of `share/dashboard_react/src` (the Top, Home, Settings, ClusterDB, Terminal, Apps
and DBServers pages) to show or hide the provisioning actions and the terminal.

---

## 2. The interface

### 2.1 Shape

One value per orchestrator, several facets. A backend implements every facet; a facet
whose concept does not exist on that backend returns the sentinel (2.3), never nil. Callers
hold the facet they need, as `DatabaseProxy` callers hold `DatabaseProxy`.

```go
// cluster/orchestrator.go (replaces the 2021 DatabaseOrchetrator stub)

// Orchestrator drives the services of a cluster on one infrastructure: it creates,
// starts, stops and removes the database, proxy and app services, holds the shared
// secret the services read, and knows the nodes they run on. One value per named
// orchestrator (3.1); the cluster's servers resolve to one of them (3.3).
type Orchestrator interface {
	OrchestratorInfo
	NodeInventory
	DatabaseOrchestrator
	ProxyOrchestrator
	AppOrchestrator
	SecretOrchestrator
	TerminalOrchestrator
	HostOrchestrator // from step 3b: the VM under a server, for a hypervisor backend (10.4)
}

type OrchestratorInfo interface {
	Name() string            // the configured name ("infra1"), or the type for a synthesized one
	Type() string            // config.ConstOrchestratorOpenSVC, ...Kubernetes, ...OnPremise, ...Localhost, ...SlapOS, ...Script
	Traits() OrchestratorTraits
	Layout(server *ServerMonitor) ServiceLayout
	ProxyLayout(prx DatabaseProxy) ServiceLayout
	Check() error            // reachability and credentials, for the state WARN0326 (2.4)
}

// OrchestratorTraits replaces the "== OpenSVC || == Kubernetes" capability checks (1.5).
// A trait is a fact about the backend, not a per-cluster setting: the settings that
// refine it (prov-net-cni, haproxy-mode) stay where they are and are tested beside it.
type OrchestratorTraits struct {
	IsContainer         bool // configurator/distribution.go isContainerOrchestrator; TLS CA path; xtrabackup image; run-as UID
	HasDNS              bool // prx_has.go, app_has.go HasDNS
	HasSharedSecret     bool // store_orchestrator.go: a secret object the services read
	HasPlacement        bool // agents mean something: prov-db-agents, placementNode, CheckClusterServiceAgents
	HasPlanDisk         bool // srv_binlog_budget.go planDiskBytes
	HasImage            bool // prov_image.go ResolveDatabaseImage; the two-phase pull upgrade
	RunsJobsInContainer bool // cluster_chk.go WARN0170 gate, cluster_sec_job_login.go
	StartFetchesConfig  bool // prov.go startReappliesProxyConfig (with prov-*-start-fetch-config)
	ProvisionsNewHosts  bool // server_child_cluster.go, server_selfservice.go
	HasTerminal         bool // server_tty.go, api.go: a shell in the service container
	HasAtomicRestart    bool // restartSupportedForOrchestrator
}

// ServiceLayout replaces the path switches of srv_cnf.go, srv_get.go, prx_get.go and
// app_get.go: where the service keeps its data, configuration, binaries and socket, and
// what it binds to.
type ServiceLayout struct {
	Datadir, Confdir, Basedir, ClientBasedir, Binary, Socket, BindAddress, Domain string
}

type NodeInventory interface {
	Nodes() ([]Agent, error)        // cluster.go initOrchetratorNodes; the Agent type stays
	Storages() ([]Storage, error)   // OpenSVC pools, Kubernetes storage classes (api_cluster.go)
	NodeStats() ([]NodeStats, error) // OpenSVC daemon node stats; ErrOrchestratorNotSupported elsewhere
}

type DatabaseOrchestrator interface {
	ProvisionDatabaseService(server *ServerMonitor) error
	UnprovisionDatabaseService(server *ServerMonitor) error
	StartDatabaseService(server *ServerMonitor) error
	StopDatabaseService(server *ServerMonitor) error
	RestartDatabaseService(server *ServerMonitor, node, rid string) error // ErrOrchestratorNotSupported: the caller runs stop, wait, start
	FreezeDatabaseService(server *ServerMonitor) error
	UnfreezeDatabaseService(server *ServerMonitor) error
	UpdateDatabaseServiceConfig(server *ServerMonitor, forcePull bool) error      // image and pull policy
	RefreshDatabaseServiceDefinition(server *ServerMonitor, keepImage bool) error // #1775: the whole definition
	UpgradeDatabaseService(server *ServerMonitor) error // the backend's own upgrade (on-premise script); ErrOrchestratorNotSupported: the caller runs the two-phase pull
	DatabaseServiceDefinition(server *ServerMonitor) ([]byte, error) // the definition as the backend holds it
	DatabaseServiceImage(server *ServerMonitor) (string, error)      // the image the service runs
	DatabaseWorkingNode(server *ServerMonitor) (string, error)
	AbortDatabaseService(server *ServerMonitor) error
	ClearDatabaseInstanceState(server *ServerMonitor, node string) error // the node the existing OpenSVC method takes
}

type ProxyOrchestrator interface {
	ProvisionProxyService(prx DatabaseProxy) error
	UnprovisionProxyService(prx DatabaseProxy) error
	StartProxyService(prx DatabaseProxy) error
	StopProxyService(prx DatabaseProxy) error
	RestartProxyService(prx DatabaseProxy, node string, rid string) error // step 3: the backend's own restart, as the database's
	RefreshProxyServiceDefinition(prx DatabaseProxy) error
	ProxyWorkingNode(prx DatabaseProxy) (string, error)
	ProxyServiceImage(prx DatabaseProxy) (string, error) // step 3: the image or the package version the proxy runs
	AbortProxyService(prx DatabaseProxy) error
	ClearProxyInstanceState(prx DatabaseProxy, node string) error
}

type AppOrchestrator interface {
	ProvisionAppService(app *App) error
	UnprovisionAppService(app *App) error
	StartAppService(app *App, node string) error
	StopAppService(app *App, node string) error
	RestartAppService(app *App, node, rid string) error
	UpdateAppConfig(app *App) error            // variable maps and secrets, no restart
	RefreshAppServiceDefinition(app *App) error // the whole definition, no restart
	ProvisionAppRoutes(app *App) error          // the app's routes, as OpenSVCProvisionRoute takes the app
	WithdrawAppRoutes(app *App) error           // cluster_del.go
	AbortAppService(app *App) error
	ClearAppInstanceState(app *App, node string) error
}

type SecretOrchestrator interface {
	SecretStore() orchestratorStore // the existing interface of store_orchestrator.go; nil never: a backend without a secret returns a writer whose every method returns ErrOrchestratorNotSupported
	ServerSecretStore(namespace string) (credstore.StoreWriter, error)
	InitCredentials() []initCredential
	RotateDatabasePassword(password string) error
	UnprovisionSecret() error
}

type TerminalOrchestrator interface {
	TerminalCommand(req TerminalRequest) (TerminalCommand, error) // the shell into a service container, or the SSH login on its host
	DefaultContainer(target TerminalTarget) (string, bool)
}

// TerminalTarget is the kind of service a terminal session opens on: "server", "proxy", "app",
// the values server/server_tty.go routes on (it reads them from here).
type TerminalTarget string

// TerminalRequest names the service: a shell needs the service's name (a container) or its
// host (SSH), which a (target, rid) pair alone does not give.
type TerminalRequest struct {
	Target      TerminalTarget
	Host        string // for a shell on the host (SSH)
	ServiceName string // for a shell in the container
	Agent       string // the node the service runs on, when known
	RID         string // the container resource; empty for DefaultContainer
}

// TerminalCommand is how the shell is reached: a URL the orchestrator serves (gotty), or an
// SSH login. utils/tty has no command type, so the interface declares its own; the fields a
// backend does not use stay empty.
type TerminalCommand struct {
	URL, Node, Version              string // a shell in the container
	Host, Port, Username, Password, Key string // a shell on the host
}
```

What stays out of the interface, on purpose:

- **The orchestration itself.** `ProvisionServices`, `Unprovision`, `RestartDatabaseService`'s
  stop-wait-start, `UpgradeDatabaseService`'s two-phase pull, the rolling loops, the
  cookies, the failover state, the `provisioningMutex`, the client script hooks
  (`ProvisionDatabaseScript` and the others of `cluster/prov_scripts.go`) are the
  orchestration layer in `cluster/prov.go`. They call the interface; they are not in it.
  The Galera provision order and the engine-app case of a PostgreSQL member stay there too.
- **The resizer.** `resourceResizer` (`cluster/cluster_resize_dynamic.go`) is already an
  interface with four implementations; the switch that picks one becomes
  `cluster.orchestrator(server).Resizer()` on `OrchestratorInfo` later, not in the first
  steps. Listed in 5.
- **The scripts of F6.** The `prov-db-*-script` and `prov-proxy-*-script` hooks run around
  every backend's operation, as today. They are not the script backend of #338 (section 4):
  a hook runs in addition to the backend; the script backend is the backend.

### 2.2 Synchronous methods, the `errorChan` stays in the orchestration layer

Every backend method returns its error. The `go backend(); err := <-cluster.errorChan`
pattern, its ~90 sends inside the backends and the cross-talk class of #1769
(`doc/implementation/cluster/ERRORCHAN_PROVISIONING.md`) move to one place:

```go
// cluster/prov.go
func (cluster *Cluster) provisionDatabaseAsync(orch DatabaseOrchestrator, server *ServerMonitor) {
	go func() { cluster.errorChan <- orch.ProvisionDatabaseService(server) }()
}
```

A backend can then no longer send twice for one operation (the intra-op multi-send of
#1769): it has no channel. The fan-out and its serialisation (`provisioningMutex`) are
unchanged. This is the largest behaviour-preserving change of the migration (5, step 2);
decided (section 8, decision 1): synchronous, in step 2.

### 2.3 The sentinel

```go
// ErrOrchestratorNotSupported: the orchestrator has no such operation (a freeze on
// Kubernetes, an app service on on-premise, a shared secret on localhost). Returned,
// never nil: the caller decides between its generic path, a refusal to the user, or a
// state. Wrapped with the operation and the orchestrator name: errors.Is tests it.
var ErrOrchestratorNotSupported = errors.New("operation not supported by this orchestrator")
```

Named like `ErrOpenSVCAbortNotSupported` and `ErrOpenSVCClearNotSupported`
(`cluster/prov_opensvc_db_failover.go`), which it replaces. Three caller patterns, and
only three:

1. **Generic fallback**: `RestartDatabaseService` not supported -> stop, wait, start;
   `UpgradeDatabaseService` not supported -> two-phase pull; `FreezeDatabaseService` not
   supported -> continue (logged at DEBUG, as the OpenSVC v2 no-op is today).
2. **Refusal to the user**: an API action answers HTTP 501 with the error text (the
   `restartSupportedForOrchestrator` 501 today); the MCP tool answers its error result.
3. **A state** when the monitor needed the operation: `RotateDatabasePassword` not
   supported while a rotation is pending keeps WARN0275 open with the reason, as the
   failed write does today (2.4).

A `default:` that returns nil for an unknown orchestrator type disappears with the
switches: the registry refuses an unknown type at load (3.4).

### 2.4 States and logs (T3, T5)

Level and domain: an orchestrator failure is about one server's service, so a server-level
state in the HA machine (the main `StateMachine`), as the provisioning states are today
(WARN0170, WARN0275, WARN0324). Log module `config.ConstLogModOrchestrator`, as every
backend logs today. New states, numbered after the last ones (WARN0324, ERR00121):

| State | Level | Severity | When | Clears |
|---|---|---|---|---|
| ERR00122 | server | ERROR | an orchestrator operation the monitor or the user asked for failed on this server (provision, start, refresh): the service cannot do its job until someone acts. Text: `Orchestrator %s: %s of %s failed: %s` | the next successful operation of the same kind on the server |
| WARN0326 | cluster | WARNING | `Check()` of a configured orchestrator fails (unreachable, bad credentials, script missing or not executable): every provisioning action of the cluster will fail. Text: `Orchestrator %s (%s) unreachable: %s` | the next successful check |
| WARN0327 | cluster | WARNING | a configuration names an orchestrator that does not exist, or a server names one of another type than the cluster's and the operation needs the cluster's (3.3). Text: `Server %s names orchestrator %s: %s` | the configuration is fixed |
| WARN0342 | server | WARNING | the server is frozen for a rolling operation on a backend without a freeze of its own (the frozen flag, 2.5): the monitor rejoins, restarts and reprovisions nothing on it. Text: `Server %s frozen for a rolling operation: no rejoin, restart or reprovision until unfrozen` | `UnfreezeDatabaseService` |
| WARN0328 | server (the proxy) | WARNING | the proxy's runtime differs from its configuration (12.2): a materialization failed, a runtime change was refused for want of a hook, or the drift check found a difference. Text: `Proxy %s: runtime differs from configuration (%s): %s` | the materialization succeeds, or runtime and configuration agree |

The existing states keep their meaning: WARN0275 (the orchestrator secret lags the
rotation), WARN0170 (dbjobs need a scheduler and SSH), WARN0324 (jobs never reached
replication-manager). The error text never carries a secret (F10): the backends already
log the key, not the value.

### 2.5 Backend x method matrix

The rule, the maintainer's: **every operation of the interface has an implementation on every
backend.** A cell says **today** (the code exists and moves behind the method as is) or
**step N** with how it is done and what it reuses (T2). A cell says "not applicable" only
when the concept does not exist for that backend, and then only in the short table at the
end of this section, with its reason; such a method returns `ErrOrchestratorNotSupported`,
and a caller that depended on it opens a tracked state. **hook** = the script backend runs
the client's script with that action (4.2, 10.3, 11.3). Steps: 2 the database lifecycle and
the secret facet, 2b the script backend, 3 the proxy and app services on every backend, 3b
the Proxmox API backend, 4 the service definition, 5 the inventory, traits, layout and
terminal, 6 the configuration model (section 5).

| Method | OpenSVC | Kubernetes | on-premise | localhost | SlapOS | script |
|---|---|---|---|---|---|---|
| `Check` | step 1 (the nodes answer) | step 1 | step 1 (SSH dial) | step 1 | step 1 | hook `check` |
| `Traits`, `Layout`, `ProxyLayout` | step 1 | step 1 | step 1 | step 1 | step 1 | hook `traits`, `layout` |
| `Nodes` | today | today | today (`OnPremiseGetNodes`) | today | today (`SlapOSGetNodes`, the partitions) | hook `nodes` |
| `Storages` | today (pools) | today (storage classes) | step 5: `df` of the data directory's filesystem on each host over `OnPremiseConnect` | step 5: `df` locally | step 5: the partition's filesystem over the host session (11.3) | hook `storages` |
| `NodeStats` | today | step 5: the metrics API (`metrics.k8s.io`, node CPU and memory) | step 5: `/proc/loadavg`, `/proc/meminfo`, `/proc/stat` over the session, into a neutral `NodeStats` | step 5: the same locally | step 5: over the host session | hook `node-stats` |
| `ProvisionDatabaseService` | today | today | today | today | step 3: the two empty methods filled: the configurator's SlapOS paths (`configurator.go:1397`) rendered into the partition's `etc/mysql`, the partition's service started through the host session (`slapos node supervisorctl` of the partition), the result returned (no `errorChan` send: step 2 has removed them) | hook |
| `UnprovisionDatabaseService` | today | today | today | today | step 3: the service stopped and the partition's data directory emptied through the host session | hook |
| `StartDatabaseService` | today | today | today | today | today (cookie), step 3: the partition's service | hook |
| `StopDatabaseService` | today | today | today | step 2: `LocalhostStopDatabaseService` (exists, the switch never calls it) | today (SQL shutdown) | hook |
| `RestartDatabaseService` | today (`rid`) | today (repull) | step 2: `onpremise-ssh-restart-db-script` when set, else the backend's own stop then `OnPremiseStartDatabaseService` over one session | step 2: the process stopped then `LocalhostStartDatabaseService` | step 3: the partition's service restarted | hook `restart` |
| `FreezeDatabaseService`, `UnfreezeDatabaseService` | today (v3); v2: the frozen flag | step 2: the frozen flag | step 2: the frozen flag | step 2 | step 2 | hook `freeze`/`unfreeze`, else the flag |
| `UpdateDatabaseServiceConfig` | today | today | step 4: the release the next upgrade installs, pinned on the host (`<onpremise-credentials-dir>/../DB_RELEASE`, the repository URL from `configurator/distribution.go` `ResolveRepoURL`) | step 4: the same locally | step 3: the partition's software release | hook `update-service-config` |
| `RefreshDatabaseServiceDefinition` | today (`OpenSVCUpdateDatabaseTemplate`) | step 4: the full Deployment spec, not only the image (#1775) | step 4: the configurator's bundle (`config.tar.gz`) pushed to the host over the session, the service reloaded by the start script | step 4: the bundle written locally, the process reloaded | step 3: the bundle into the partition | hook `refresh-service-definition` |
| `UpgradeDatabaseService` | step 2: the backend runs the two-phase pull itself (`UpdateDatabaseServiceConfig(forcePull)`, restart, clean), so the caller has no fallback | step 2: the same | today (`onpremise-ssh-upgrade-db-script`) | step 2: the upgrade script run locally, same contract | step 3: the partition's software release changed and the service restarted | hook `upgrade` |
| `DatabaseServiceDefinition` | today (`GetDatabaseServiceConfig`) | today (`K8SGetDatabaseManifests`) | step 4: the rendered bundle | step 4 | step 3 | hook `service-definition` |
| `DatabaseServiceImage` | today | step 4: the pod spec's image (`k8sFindDatabasePod`) | step 2: `server.DBVersion` when up, `mysqld --version` over the session when down | step 2: the same locally | step 3: the partition's binary version | hook `service-image` |
| `DatabaseWorkingNode` | today | step 2: the pod's node (`k8sFindDatabasePod`) | step 2: the host (`server.Host`) | step 2: the monitor host | step 3: the partition's host | hook `working-node` |
| `AbortDatabaseService` | today (v3) | step 2: cancel the in-flight wait (`K8SWaitRolloutComplete`, `k8sWaitDeploymentGone` contexts) | step 2: close the session running the operation (the SSH command dies) | step 2: kill the in-flight local command | step 3 | hook `abort` |
| `ClearDatabaseInstanceState` | today (v3) | step 2: delete the failing pod, the Deployment recreates it | step 2: the restart and wait cookies removed, ERR00122 cleared, the start script run again | step 2 | step 3 | hook `clear` |
| `ProvisionProxyService` | today | today | today for HAProxy and ProxySQL (`OnPremiseProvisionHaProxyService`, `OnPremiseProvisionProxySQLService`, `OnPremiseProvisionBootsrapProxy`); step 3: MaxScale and the shard proxy, the packages from the distribution repositories, the configuration from `GetProxyConfig` | today for HAProxy and ProxySQL (`LocalhostProvisionHaProxyService`, `LocalhostProvisionProxySQLService`); step 3: MaxScale and the shard proxy as local processes | step 3: the two empty methods filled: the partition's proxy configured from `GetProxyConfig` and started through the host session, the error returned | hook |
| `UnprovisionProxyService` | today | today | today HAProxy/ProxySQL; step 3 the others | today HAProxy/ProxySQL; step 3 the others | step 3 | hook |
| `StartProxyService`, `StopProxyService` | today | today | today | today | today (cookie), step 3: the partition's service | hook |
| `RestartProxyService` (added in step 3) | step 3: the instance restart through the API (as the database's `OpenSVCRestartDatabaseService`) | step 3: a rollout restart of the Deployment | step 3: stop then start over one session (`OnPremiseStopHaproxyService`, `OnPremiseStartHaProxyService`, the ProxySQL pair) | step 3: stop then start locally | step 3 | hook `restart` |
| `RefreshProxyServiceDefinition` (the materialization of 12.2) | step 3: `OpenSVCGetProxyTemplateV3` pushed with the database's update call | step 3: the ConfigMap and the Deployment spec | step 3: the regenerated configuration written over the session and the proxy reloaded, the first fix of the client's case (12.3) | today: `HaproxyProxy.Init()` regenerates and reloads (`startReappliesProxyConfig`) | step 3 | hook `refresh-service-definition`, or the client's `prov-proxy-config-change-script` when not in charge (12.1) |
| `ProxyWorkingNode` | today | step 3: the pod's node | step 3: the host (`prx.GetHost()`) | step 3: the monitor host | step 3: the partition's host | hook `working-node` |
| `ProxyServiceImage` (added in step 3) | step 3: `env.docker_image` of the proxy service (`openSVCConfigValue`) | today (`k8sProxyImage`) | step 3: `haproxy -v`, `proxysql --version`, `maxscale --version` over the session | step 3: the same locally | step 3 | hook `service-image` |
| `AbortProxyService`, `ClearProxyInstanceState` | today (v3) | step 3: as the database's | step 3: as the database's | step 3 | step 3 | hook |
| `ProvisionAppService`, `UnprovisionAppService`, `StartAppService`, `StopAppService`, `RestartAppService` | today | step 3: a Deployment and a Service from the app template (image, command, environment, volumes) | step 3: a container on the agent host over the session (`docker run` from the template's image, command, environment and volumes; `prov-docker-daemon-private` already names the daemon), or a host process when the template has no image | step 3: the same locally | step 3: a partition service | hook |
| `UpdateAppConfig`, `RefreshAppServiceDefinition` | today | step 3: the Deployment patched | step 3: the container recreated with the new environment and definition | step 3 | step 3 | hook |
| `ProvisionAppRoutes`, `WithdrawAppRoutes` | today (the gateway's HAProxy fragments) | step 3: an Ingress | step 3: the fragment on the cluster's own HAProxy on the host (`withdrawGatewayRoutes` generalized to that gateway) | step 3: the local HAProxy | step 3 | hook |
| `AbortAppService`, `ClearAppInstanceState` | today (v3) | step 3 | step 3 | step 3 | step 3 | hook |
| `SecretStore` | today (`openSVCStore`) | today (`k8sStore`) | step 2: `hostCredentialStore`, the credentials directory on each database host (11.2) | step 2: `localCredentialStore` (11.3) | step 2: the host store over the SlapOS channel (11.3) | hook `secret-write`, value on fd 3 (11.3) |
| `ServerSecretStore` | today | today | step 2: the same directory, prefixed keys | step 2 | step 2 | hook `secret-write` with the server prefix |
| `InitCredentials` | today | today | step 2: `REPLICATION_MANAGER_PASSWORD`, `JOBS_API_KEY` in the directory | step 2 | step 2 | hook `init-credentials` |
| `RotateDatabasePassword` | today | today | step 2: the file rewritten over the session, `_OLD` kept | step 2 | step 2 | hook `rotate-root`, values on fd 3 and 4 |
| `UnprovisionSecret` | today | step 2: the cluster Secret deleted | step 2: the directory removed over the session | step 2 | step 2 | hook `secret-remove` |
| root-password drift check (WARN0275) | today | step 2: `ReadCurrent` on the Secret | step 2: `ReadCurrent` reads the file over the session | step 2 | step 2 | hook `secret-check` |
| `TerminalCommand` | today (gotty) | step 5: an exec into the pod through the API | today (SSH) | step 5: a local shell | step 5: SSH to the partition's host (the on-premise command) | hook `terminal`, stdout: the command |
| `DefaultContainer` | today (`container#db`, `container#app`) | step 5: the pod's database container name | step 5: no container: the service unit's name, the shell opens on the host | step 5: the same | step 5: the partition | hook `terminal` |
| `HostOrchestrator` (step 3b, 10.4) | not applicable (below) | not applicable | not applicable | not applicable | not applicable | hook `host-*` |

**The frozen flag.** Where the orchestrator has no freeze (Kubernetes, the hosts, the
partitions, OpenSVC v2), `FreezeDatabaseService` sets a server cookie `freeze` (the cookie
mechanism of `createCookie`, as `cookiePGCapPending`) that replication-manager's own
orchestration honours: no automatic rejoin, no restart, no reprovision of a frozen server
from the monitor, and the state WARN0342 (2.4) on the server while it is set;
`UnfreezeDatabaseService` removes the cookie and the state. The rolling loop freezes before
its stop and unfreezes after its start on every backend, so what the OpenSVC v3 freeze
protects (opensvc/om3#1142) is protected everywhere repman is the one that acts.

**Not applicable, and why** (the only cells allowed to answer `ErrOrchestratorNotSupported`
once the steps have landed):

| Method | Backends | Why | What a dependent caller does |
|---|---|---|---|
| `HostOrchestrator` (`StopHost`, `StartHost`, `RebootHost`, `HostStatus`) | OpenSVC, Kubernetes, on-premise, localhost, SlapOS | the backend does not own the machine under the server; only a hypervisor backend (Proxmox, 10.4) or the client's script does | the rolling upgrade with `prov-db-rolling-upgrade-host` on such a backend opens WARN0342 naming the server and skips the host step |
| `AbortDatabaseService`, `AbortProxyService`, `AbortAppService`, `Clear*InstanceState` | OpenSVC through the collector API (v2) | the v2 API has no abort and no instance-state clear; v2 is not developed further | the API action answers 501 with the reason; no state, the user asked |
| `secret-check`, `secret-remove` answering exit 3 | script | the client's tooling keeps no copy of the secret to check or remove | the drift check records WARN0275 as unverifiable and does not rewrite; the unprovision logs the absence |

Everything marked **step N** is a gap the inventory found: the operation another backend has
and this one lacks silently today. The matrix test (5.3) carries each cell with its status
(`implemented`, `todo` with the step, `notApplicable` for the table above) and a step cannot
land while a cell it owns is still `todo`.

### 2.6 One dispatch layer, one set of states (F7, T3, T5)

The maintainer's expectation of the single interface: **what the orchestrators do becomes
trackable by the state machine.** The requirement, stated: every orchestrator call goes
through the registry and its dispatch layer (`cluster.Orchestrator()`,
`cluster.orchestrator(server)`, `cluster.proxyOrchestrator(prx)`, the per-server sequence of
10.2), and **that layer is the only place where orchestration states open and close**,
uniformly for every backend, per target (server, proxy, app, cluster) and per operation. A
backend returns errors and values; it never opens a state, never logs as the only trace. One
set of codes for all backends, in the existing numbering, the existing ones reused:

| Condition | State | Level | Opened by the dispatch layer when | Cleared when |
|---|---|---|---|---|
| running | WARN0329 (INFO) | server, proxy, app, cluster | an operation starts: `Orchestrator %s: %s of %s running since %s` (the start time kept in the state) | the operation returns |
| failed | ERR00122 (ERROR) | the target | the backend returned an error: `Orchestrator %s: %s of %s failed: %s`, the backend's error text | the next successful operation of the same kind on the target |
| refused, not supported | WARN0331 (WARNING) | the target | a caller depended on an operation the backend answers `ErrOrchestratorNotSupported` to (2.3, pattern 3): `Orchestrator %s: %s not supported on %s, %s` (what the caller did instead) | the caller no longer needs it, or the configuration changes |
| slow | WARN0330 (WARNING) | the target | an operation runs past its threshold (`prov-orchestrator-slow-threshold`, seconds, default 300, T16): `Orchestrator %s: %s of %s running for %s, threshold %s` | the operation returns |
| unreachable | WARN0326 (WARNING) | cluster | `Check()` fails (2.4) | `Check()` succeeds |
| misnamed | WARN0327 (WARNING) | cluster | the configuration names an orchestrator wrongly (2.4, 3.3) | the configuration is fixed |
| frozen | WARN0342 (WARNING) | server | the frozen flag (2.5) | unfreeze |
| secret drift | WARN0275 (WARNING) | cluster | the shared secret differs from the reference (11.1) | the rewrite succeeds |
| proxy drift | WARN0328 (WARNING) | proxy | the proxy's runtime differs from its configuration (12.2) | they agree |

The dispatch layer wraps each call: it opens WARN0329 before, measures, opens WARN0330 past
the threshold, maps the return to ERR00122 or WARN0331 or clears, and logs the one line that
names the backend, the target, the operation and the duration (module `orchestrator`). The
GUI's orchestrator view reads these states per target: what is running, since when, what
failed and why, what the backend cannot do here, what drifted. An operator never has to open
a backend's log to learn one of those.

**The gap today:** the backend files open states themselves, 18 calls in 7 files
(`prov_k8s_db.go` 9, `prov_opensvc_app.go` 2, `prov_opensvc.go` 2, `prov_k8s_prx.go` 2,
`prov_opensvc_prx.go` 1, `prov_opensvc_db.go` 1, `prov_opensvc_cert.go` 1; the codes
WARN0212, WARN0215, ERR00082, WARN0275, WARN0099, WARN0045, WARN0046, ERR00109, ERR00110,
ERR00113 among them, several backend-specific). Each moves to the dispatch layer with the
step that routes its operation (the secret ones in step 2, the Kubernetes resource sensor's in
step 5), expressed in the codes above or kept as a general state the layer opens from the
backend's returned error. The dispatch lock (5.3) refuses a `SetState`, `AddState` or
`PreserveState` in a backend file from step 2 on, the 18 of today being the allowed list
that shrinks.

---

## 3. Configuration model (#333)

### 3.1 Named orchestrators

A named orchestrator is a TOML table whose keys are **the same keys the flat configuration
uses today**, without renaming: the OpenSVC block carries `opensvc-host`,
`opensvc-admin-user`, `opensvc-user`, `opensvc-codeapp`, `opensvc-p12-certificate`,
`opensvc-p12-secret`, `opensvc-use-collector-api`, `opensvc-collector-account`,
`opensvc-v3-provision-delay`, `opensvc-image-force-pull`, `opensvc-use-orchestrated-start`,
`opensvc-register`, `prov-orchestrator-cluster`; the Kubernetes block `kube-config`,
`prov-kube-image-force-pull`, `prov-kube-storage-class`, `prov-kube-proxy-storage-class`,
`prov-net-cni-cluster`; the on-premise block `onpremise-ssh`, `onpremise-ssh-port`,
`onpremise-ssh-private-key`, `onpremise-ssh-credential` and the five
`onpremise-ssh-*-script`; the SlapOS block `slapos-config` and the `slapos-*-partitions`;
the script block the keys of section 4. One new key per block: `type`.

```toml
[orchestrator.infra1]
type = "opensvc"
opensvc-host = "collector.example:1215"
opensvc-admin-user = "hash_..."          # a secret, encrypted as in the flat form (F9)
opensvc-p12-secret = "hash_..."
prov-orchestrator-cluster = "infra1"

[orchestrator.infra2]
type = "kube"
kube-config = "/etc/replication-manager/kube/infra2.yaml"
prov-kube-storage-class = "fast"

[orchestrator.lab]
type = "script"
prov-orchestrator-script = "/usr/local/bin/repman-orchestrator"
prov-orchestrator-script-timeout = 600

[cluster1]
prov-orchestrator = "infra1"
db-servers-hosts = "db1,db2,db3"

[cluster2]
prov-orchestrator = "infra2"
```

Why the same key names (T19, T2): the synthesized orchestrator of 3.2 is then the identity
transform, the encryption of secrets (`GetDecryptedValue("opensvc-p12-secret")`) and the
immutable-key logic (`ImmuableMap`) apply by key name as they do today, and the user
documentation of each key stays valid. The alternative, short names inside the block
(`host`, `admin-user`), is a rename of fourteen keys and a second documentation.

Where it lives: in `config.Config` as `Orchestrators map[string]OrchestratorConfig`
(`mapstructure:"orchestrator" toml:"orchestrator" json:"orchestrators" scope:"server"`).
`OrchestratorConfig` is a struct with the `Type` field and the subset of the `Config`
fields above, with the same tags, so one `mapstructure` decode fills both. Every key is a
Viper key (T16): `orchestrator.infra1.opensvc-host`. Hot reload and the API: the blocks are
server-scope settings, changed through the global settings API and GUI (`GET/POST/DELETE
/api/orchestrators[/{name}]`, Swagger, grant `GrantGlobalSettings` in
`globalSettingsACLRules`; T6), immutable when set in `/etc` as every setting. They are
written by `repman.SaveCallBack()` only (T25).

Discovery: `DiscoverClusters` (`server/server.go:1535`) treats every top-level table as a
cluster, except `default`, `saved-default` and `overwrite-default`. `orchestrator` joins
that list. A cluster named `orchestrator` is refused at load (6).

### 3.2 Compatibility: the synthesized orchestrator

An existing configuration carries `prov-orchestrator = "opensvc"` (a type) and the flat
keys at cluster level or in `[DEFAULT]`. It keeps working unchanged:

- When `prov-orchestrator` names no `[orchestrator.<name>]` block and is one of the five
  types, the registry builds a **synthesized orchestrator named after the type** from the
  cluster's flat keys. Its `Name()` is the type. Two clusters with the same type and
  different flat keys (two OpenSVC collectors) get two synthesized orchestrators, one per
  cluster, as today.
- A `prov-orchestrator` value equal to a block name uses that block.
- A block name equal to a type (`[orchestrator.opensvc]`) is refused at load: ambiguous.
- `prov-orchestrator-enable` keeps its meaning (which types the instance offers) and the
  GUI list (`GetOrchestratorsProv`) grows the named blocks beside the types.
- `GetOrchestrator()` keeps returning the string the configuration carries, so the 33
  dashboard uses and the regtest targets (`OrchestratorTarget`) match on it until the
  major; `cluster.Orchestrator().Type()` is what the code compares, never the name.

The flat keys are not deprecated by this design. They are the per-cluster form; the blocks
are the shared form. Deprecating the flat form is a separate decision for a later major.

### 3.3 Per-server override (a later step)

#333 and #338 want a server mapped to its own orchestrator. Today the only per-server
provisioning facts are ordered lists aligned on `db-servers-hosts` (`prov-db-agents`,
round-robin). The key, cluster level, `host=name` pairs, comma separated, as the
`prov-db-agents` family is a comma list:

```toml
[cluster1]
prov-orchestrator = "infra1"
prov-db-orchestrators = "db3=infra2"    # db1 and db2 stay on infra1
```

Resolution: `cluster.orchestrator(server)` returns the server's override, else the
cluster's. The orchestration layer resolves per server inside its loops
(`ProvisionServices`, `Unprovision`), so a cluster can span two OpenSVC collectors or an
OpenSVC and a Kubernetes site. `cluster.orchestrator(server)` exists from step 1 (it
returns the cluster's orchestrator until the key exists); the key itself comes after the
major's steps of section 5 (decision 2, section 8).

**The one rule.** A server may be placed on another orchestrator than its cluster's only
when the other servers of its cluster reach it at a private address: replication, the
proxies' backends and the monitor's connection use that address, and nothing else. The
rule is checked at load for each `host=name` pair (the server's host is an address of a
private range, or resolves to one) and refused with WARN0327 otherwise. It is a rule on
the addresses, not on the types: two OpenSVC sites, or an OpenSVC and a Kubernetes site,
are both allowed when the rule holds. The cluster-level facets (`Nodes`, `SecretStore`,
`InitCredentials`, `UnprovisionSecret`) run on every distinct orchestrator of the cluster
(the shared secret is written on each), so a server's init container logs in through its
own orchestrator's secret.

**The expected mechanism, outside replication-manager.** Two orchestrators each have a
gateway (the HAProxy replication-manager already provisions, or the site's own) with an
internal address. A tunnel between the two gateways, over those internal addresses, lets
each gateway route its orchestrator's private network to the other: the database servers
of one site reach those of the other at their private addresses, as they reach their own.
No database port is published; the nodes see nothing new (their routes do not change, the
gateway's do); replication-manager only needs each server's private address, which the
rule above requires. Backups and reseeds do not need the path: their streams go through
the receivers of replication-manager (the backup and reseed sockets of the monitor host),
on the connection the servers already have. The tunnel, its keys and its routes are the
infrastructure's and the orchestrators' job, not a replication-manager feature; this
document only states what replication-manager needs from them (a private address per
server) and what it checks (the rule).

**The tunnel, concretely: the OpenSVC `strongswan` template, om3 rc64 or later.** On
OpenSVC the gateway-to-gateway tunnel is the OpenSVC template `opensvc_template/strongswan`
v2: an IPsec (strongSwan) tunnel between the two gateways' internal addresses, routing each
side's private network to the other. It exists only on OpenSVC **om3 rc64 or later**. An
instance whose OpenSVC agents are older than om3 rc64 cannot use the per-server map: at load
the check reads the agents' version (`GetOrchestratorVersion`, the version the daemon reports)
and, below om3 rc64, refuses each `host=name` pair with WARN0327, the state text saying why
(`Server %s names orchestrator %s: the cross-orchestrator private path needs the OpenSVC
strongswan template, om3 rc64 or later, the agents run %s`). Deploying the template and the
tunnel stays out of replication-manager's scope: the infrastructure's operators instantiate
it on the gateways; replication-manager checks the version and the private address, nothing
more. On a non-OpenSVC orchestrator (a Kubernetes site, a hypervisor) the tunnel is the
site's own, and only the private-address rule applies.

The proxies and apps follow the cluster orchestrator; there is no per-proxy override (the
HAProxy standby exception of `proxyServiceOrchestrator` stays as the one routing rule,
expressed as the localhost orchestrator).

### 3.4 Load-time validation

The registry (`cluster.newOrchestratorList()`, built in `cluster.Init` beside
`newProxyList()`) refuses at load, with a WARN0327 state and the cluster held in the
"configuration invalid" condition it already has for a bad topology: an unknown type, an
unknown name, a name equal to a type, a per-server override whose server has no private
address (the rule of 3.3), a script path that is not an executable file. It never calls `log.Fatalln` (`cluster/cluster.go:1116` does
today for an unknown orchestrator: F2).

---

## 4. The script backend (#338)

Type `script`. A first-class backend, not a hook: when a cluster's orchestrator is of type
`script`, every method of section 2 runs the one configured executable with the operation
as its first argument. The client owns the whole provisioning (F6); replication-manager
keeps the orchestration (order, cookies, states, waits).

### 4.1 Settings (T16, T19)

| Key | Type | Default | Meaning |
|---|---|---|---|
| `prov-orchestrator-script` | string | "" | path of the executable; required for the type |
| `prov-orchestrator-script-timeout` | int (seconds) | 600 | one invocation's budget; `provision` and `unprovision` use it, the others use a third of it, as `backup-restic-timeout` is one number for the family |
| `prov-orchestrator-script-env` | string | "" | `KEY=VALUE,KEY=VALUE` added to the environment, for the client's own parameters (an API endpoint, a site name); never a secret, which belongs in `secret-kv` and reaches the script through the versioned store |

The three keys live in the flat form (cluster level or `[DEFAULT]`) and in an
`[orchestrator.<name>]` block of `type = "script"`, as every other backend's keys.

### 4.2 Contract

**Invocation**: `<script> <action> <kind> <host> <port> <cluster>`. The positional form of
the existing hooks (`cluster/prov_scripts.go`: host, port, user, password, cluster) minus
the credentials, which never go on the command line (F10: visible in `ps`).

- `action`: `provision`, `unprovision`, `start`, `stop`, `restart`, `freeze`, `unfreeze`,
  `update-service-config`, `refresh-service-definition`, `upgrade`, `service-definition`,
  `service-image`, `working-node`, `rotate-passwords`, `nodes`, `storages`, `traits`,
  `layout`, `check`. One action per method of section 2; the method name in kebab case
  without the kind.
- `kind`: `database`, `proxy`, `app`, `cluster` (for `nodes`, `storages`, `check`,
  `rotate-passwords`, `traits`).
- `host`, `port`: the service's; empty for `cluster`.

**Environment**: `cluster.GetExecEnv()` as every script hook gets today
(`REPLICATION_MANAGER_URL`, `REPLICATION_MANAGER_USER`, `REPLICATION_MANAGER_PASSWORD`,
`REPLICATION_MANAGER_CLUSTER_NAME`), plus:

| Variable | Value |
|---|---|
| `REPLICATION_MANAGER_ORCHESTRATOR` | the orchestrator's name |
| `REPLICATION_MANAGER_ACTION`, `REPLICATION_MANAGER_KIND` | as argv, for scripts that read env only |
| `REPLICATION_MANAGER_SERVER_HOST`, `REPLICATION_MANAGER_SERVER_PORT`, `REPLICATION_MANAGER_SERVER_NAME`, `REPLICATION_MANAGER_SERVER_ID` | the service |
| `REPLICATION_MANAGER_DB_USER`, `REPLICATION_MANAGER_DB_PASSWORD` | the database credential (#338: "bdd user, bdd password"), in the environment only |
| `REPLICATION_MANAGER_PROXY_USER`, `REPLICATION_MANAGER_PROXY_PASSWORD` | for `kind = proxy` |
| `REPLICATION_MANAGER_FORCE_PULL`, `REPLICATION_MANAGER_KEEP_IMAGE` | `true`/`false` for `update-service-config` and `refresh-service-definition` |
| `REPLICATION_MANAGER_NODE`, `REPLICATION_MANAGER_RID` | for `restart` and the app actions |
| `REPLICATION_MANAGER_NEW_PASSWORD` | for `rotate-passwords` |
| the `prov-orchestrator-script-env` pairs | as given |

The existing hook variables of the resize scripts (`REPMAN_*`, `cluster/prov_scripts.go`)
keep their names there; the backend uses the `REPLICATION_MANAGER_*` prefix of
`GetExecEnv` (T19: the prefix of the environment this process exports). Unifying the two
prefixes is not in this design.

**Standard input**: for `kind = database`, the JSON of the server as the API serves it
(`GET /api/clusters/{cluster}/servers/{server}`, the `ServerMonitor` marshalled with its
`json` tags: `Pass` is `json:"-"` already, so the password is absent, F10). For `proxy`,
the proxy's JSON (`Pass` is `json:"-"` too); for `app`, the app's; for `cluster`, the
cluster's configuration JSON as `GET /api/clusters/{cluster}` serves it, with the secrets
redacted as that endpoint redacts them. The script may ignore stdin.

**Standard output**: logged line by line at DEBUG in module `orchestrator`, attributed to
the server (`server.copyLogs`) as the hooks do. Actions that answer something read stdout
as data: `nodes` and `storages` a JSON array (`[]Agent`, `[]Storage`); `service-definition`
raw bytes; `service-image`, `working-node` the last non-empty line; `traits` a JSON object
with the field names of `OrchestratorTraits`; `layout` a JSON object with the field names
of `ServiceLayout`; `check` nothing. **Standard error** is logged at WARNING line by line.

**Exit codes**: `0` success; `3` not supported (`ErrOrchestratorNotSupported`: the caller
takes its generic path or refuses, 2.3); any other non-zero, or a kill on timeout, is a
failure: the error carries the action, the host and the last stderr line, state ERR00122
opens on the server (2.4), and the operation reports the error to its caller exactly as a
Kubernetes API error would.

**Logging (F11)**: the full command line is logged at INFO before the run, as the hooks
log theirs; it carries no secret, so no masking is needed. The environment is not logged.
The user reproduces the action by running the same command with the same
`REPLICATION_MANAGER_*` environment, the credentials supplied by hand.

**Concurrency**: the backend runs one script per method call; the orchestration layer
already runs provisioning per server in parallel under `provisioningMutex`. A script that
cannot run concurrently takes its own lock. Each run has the timeout above: no run blocks
the monitor without a deadline (F2).

### 4.3 What #338 asked that this changes

#338 lists "bdd user, bdd password" as script parameters: they are passed, in the
environment, not on the command line (F10). #338 lists "json representation of the
replication-manager server (the database password is hidden)": on stdin, as the API
already redacts it. #338 wants "the default orchestrator for a cluster should be call
external scripts": today the default is `onpremise` (`opensvc` when built with OpenSVC).
Changing the default is a user-visible change; it is decision 3 of section 8 and, if taken,
belongs in section 6.

---

## 5. Migration plan

Strangler: the interface and the registry arrive first with every backend behind an
adapter that calls today's methods; then one operation group per pull request moves its
callers to the interface and deletes its switches; the backend methods move file by file
(`prov_opensvc_db.go` -> `orchestrator_opensvc.go` and so on) as their callers disappear.
Each step leaves `develop` releasable: the interface and the switches coexist until a
group's switches are all gone, and that group's regtests run on both paths before the
switches are deleted.

The regtest matrix per step (T13) is: **OpenSVC** on the dev instances (the regtest
framework on a provisioned cluster), **Kubernetes** on a `kind` cluster (the Kubernetes
provisioning was verified against `kind`, `KUBERNETES_PROVISIONING.md`; no CI job runs it
today, so the first step adds the `kind` job to the regtest workflow), **on-premise** with
the Docker regtest framework (real MariaDB, MySQL and Percona containers, the on-premise
backend driving them over SSH or, from step 7, the script backend driving `docker`). The
localhost and SlapOS backends have no regtest and get none here: localhost is the
developers' path, SlapOS is cookie-only; both are covered by the matrix unit test.

| Step | Pull request content | Switches removed | Regtests (T13) | Unit tests |
|---|---|---|---|---|
| 0 | this document | 0 | - | - |
| 1 | `cluster/orchestrator.go`: the interfaces of 2.1, `ErrOrchestratorNotSupported`, the states of 2.4 in `config/error.go`; five adapters (`orchestrator_opensvc.go`, `_k8s.go`, `_onpremise.go`, `_localhost.go`, `_slapos.go`) that call today's methods; `newOrchestratorList()` building the synthesized orchestrator per cluster (3.2); `cluster.orchestrator(server)`; the `DatabaseOrchetrator` stub deleted. No caller changes. | 0 | the full suite once per orchestrator, unchanged behaviour expected | `TestOrchestratorMatrix` (5.3); compile-time `var _ Orchestrator = (*openSVCOrchestrator)(nil)` per backend; `TestOrchestratorSynthesized`: a flat configuration and its block form build equal orchestrators |
| 2 | database lifecycle through the interface: `ProvisionServices`, `InitDatabaseService`, `Unprovision`, `UnprovisionDatabaseService`, `Start`, `Stop`, `StopClean`, `Restart`, `Freeze`, `Unfreeze`, `UpgradeDatabaseService` in `cluster/prov.go`; the `errorChan` sends leave the five backends (2.2); the localhost stop bug of 1.1 fixed by construction | ~55 (prov.go) | OpenSVC: provision, unprovision, start/stop, restart with rid, rolling restart; Kubernetes on kind: `testK8SStopStartDatabaseService`, `testK8SProvisionSchedulerVolumeBinding`, `testJobsRoundTrip`; on-premise: the failover and switchover suite with `prov-db-*-script` hooks set | `TestProvisionReportsOnce` (one send per operation, from the orchestration layer); the existing `prov_unprovision_test.go`, `prov_opensvc_start_timeout_test.go`, `prov_k8s_concurrent_start_test.go` rewired on the adapters |
| 3 | **proxy and app services on every backend** (the maintainer's rule, 2.5) and **a route changed at runtime materialized in the configuration** (section 12: `prov-proxy-apply-dynamic-config` and `prov-proxy-config-change-script` modelled on `prov-db-apply-dynamic-config`, the refusal with WARN0328, the drift check per tick, the on-premise HAProxy file first): `InitProxyService`, `UnprovisionProxyService`, `StartProxyService`, `StopProxyService`, `InitAppService` through the interface with `cluster.proxyOrchestrator(prx)` (9.4); `RestartProxyService` and `ProxyServiceImage` added to the facet; on-premise MaxScale and shard proxy on the host (the distribution repositories, `GetProxyConfig`), localhost the same as processes, SlapOS's empty provision and unprovision filled (the partition's service through the host session, the error returned); `ProxyWorkingNode`, abort and clear on every backend; the app services as a Deployment on Kubernetes, a container on the agent host over the session on on-premise (or a host process without an image), the same locally, a partition service on SlapOS, their routes on the cluster's own HAProxy; the 15 direct OpenSVC app, abort and clear calls of `server/api_app.go`, `api_proxy.go`, `api_database.go` and `mcp/tools_app.go` go through `AppOrchestrator` / `ProxyOrchestrator` and answer 501 on `ErrOrchestratorNotSupported` (the MCP "OpenSVC only" strings go) | ~25 | OpenSVC: `testAppConditionalVariablesOpenSVC`, `testAppConditionalProxiesOpenSVC`, proxy provision and start/stop; Kubernetes: proxy provision on kind; on-premise: proxy start/stop hooks | `TestAppActionsRefusedWithoutAppOrchestrator` (a guard test as `cluster_tst_proxy_guard_test.go`) |
| 3b | the Proxmox API backend (10.4): the `HostOrchestrator` facet and its four matrix rows, `HasHost`, the host step of the rolling upgrade behind `prov-db-rolling-upgrade-host`, the client's `prov-db-host-upgrade-script` hook, `orchestrator_proxmox.go` over the on-premise service operations, its configuration keys, the script backend's `host-*` actions | 0 | the rolling upgrade with the host step on a Proxmox lab or the client's cluster (10.4); the on-premise framework for the sequence without a host | `TestProxmoxHostFacet` against a fake Proxmox API, `TestRollingSequenceHostStep` on a recording fake backend (reboot between stop and upgrade, timeout, server without a vmid) |
| 4 | service definition (#1775): `UpdateDatabaseServiceConfig`, `UpgradeDatabaseDeploymentOnStart` -> `RefreshDatabaseServiceDefinition`; the Kubernetes full-spec refresh and the on-premise config push written; the generic action `update-service-definition` on servers and apps (Swagger, ACL `GrantProvDBProvision` / the app grant, GUI button renamed), `update-opensvc-template` kept as an alias until the major; `cluster_roll.go`, `cluster_roll_plan.go` use the method on every orchestrator | ~15 | OpenSVC: rolling upgrade, `testOpenSVCUpgradeWarnRecovery`, `testRollingReprovReseed`; Kubernetes on kind: rolling upgrade with a changed `prov-db-docker-run-args` observed in the pod spec; on-premise: `rollingUpgradeOnPremise` with the SSH upgrade script | `TestRefreshDefinitionRendersCurrentConfig` per backend with the fake clients already used by `prov_k8s_*_test.go` |
| 5 | inventory, secrets, terminal, traits, layout: `initOrchetratorNodes` (no `Fatalln`), `Storages`, `NodeStats`, `store_orchestrator.go` through `SecretOrchestrator`, `ProvisionRotatePasswords`, `cluster_user_rotate.go`, `UnprovisionSecret` on Kubernetes written, `server_tty.go` and `api.go` through `TerminalOrchestrator`, the ~40 capability checks of 1.5 through `Traits()`, the path switches of `srv_cnf.go`, `srv_get.go`, `prx_get.go`, `app_get.go`, `configurator.go` through `Layout()`; the resizer selection through the orchestrator | ~75 | OpenSVC: rotation (`testProvisionReplicationUser`, the secret-versions tests), terminal; Kubernetes on kind: rotation, unprovision leaves no Secret; on-premise: the SSH terminal, the dbjobs WARN0170 gate | `TestTraitsMatchLegacyChecks`: for each of the five types, each trait equals the boolean the deleted check computed; `TestLayoutMatchesLegacyPaths` |
| 6 | configuration model (3.1, 3.2, 3.4): `Orchestrators` in `config.Config`, `[orchestrator.<name>]` reserved in `DiscoverClusters`, `prov-orchestrator` accepting a name, load-time validation, the orchestrators API, Swagger, ACL and GUI page | 0 | the full suite on a configuration converted to blocks, and on the flat one | `TestOrchestratorNamedResolution` (name, type, unknown, ambiguous), `TestOrchestratorConfigRoundTrip` (save and reload of a block, secrets encrypted) |
| 2 (secrets) | the secret facet on every backend (section 11), in the same pull request as step 2's lifecycle routing or the next one: `hostCredentialStore` for on-premise (the credentials directory on each database host over the SSH session of the provisioning), `localCredentialStore` for localhost, the on-premise store over the SlapOS channel for SlapOS, the Kubernetes `UnprovisionSecret` and drift check; `orchestratorStoreWriter`, `initCredentials`, `ProvisionRotatePasswords`, `checkOpenSVCRootPassword` and `checkInitCredentials` become calls of `cluster.Orchestrator()`'s secret facet and lose their type switches; the step 1 adapters' not-supported writers are the recorded gap this step closes | ~15 (store_orchestrator.go, cluster_secret_kv.go, cluster_user_rotate.go, prov.go rotate) | on-premise framework: provision writes the directory, the jobs run with the key from it, a rotation rewrites it with `_OLD`, the drift check opens and clears WARN0275, unprovision removes it; OpenSVC and Kubernetes rotation unchanged | `TestHostCredentialStoreWriteAll` on a fake SSH session, `TestRotationRewritesHostCredentials`, the matrix test rows of 2.5 turned to implemented |
| 2b | the script backend (4) as `orchestrator_script.go`, in the same pull request as step 2 or the next one: the first client of the interface needs it (section 10), so it is the first new backend, before any other work on the proxies, the apps or the configuration model; the rolling restart, upgrade and reprov per-server sequence goes through the interface in the same step (10.2); its secret facet is the five hooks of 11.3 | 0 | on-premise framework with `prov-orchestrator = "script"` and a reference script `share/scripts/orchestrator-docker.sh` that drives `docker` for the regtest containers: the failover, switchover, provision and rolling suites | `TestScriptOrchestratorContract` with a fake script: argv, env, stdin JSON without password, exit 0/3/other, timeout kill, stdout data actions, no secret in the logged command; `TestRollingSequenceHooks`: the per-server sequence calls the interface in the order of 10.2 on a recording fake backend |
| 7 | the reference script run on a real on-premise cluster (the client case of section 10), its findings folded into section 4 | 0 | the client's own rolling upgrade, observed | - |
| 8 (the major) | the remaining switches deleted; `update-opensvc-template` alias removed; `GetOrchestrator()` returns the name, `GetOrchestratorType()` the type, the dashboard and `OrchestratorTarget` moved to the type; the backend files renamed | the rest (~25: the explicit refusals that stay as `Traits` tests, `GetOrchestratorsProv`) | the full matrix | `TestNoOrchestratorDispatchOutsideRegistry` (5.3) made strict |
| later | the per-server override (3.3): `prov-db-orchestrators`, the private-address rule checked at load, the cluster-level facets run on every distinct orchestrator of the cluster | 0 | two orchestrators of the regtest framework (two Docker networks, a route between them) with one server on the second | `TestOrchestratorPerServerRule` (private address accepted, public refused, unknown name refused) |

Step order is not negotiable in four places: 1 before everything, 2 before 4 (the refresh
rides the rolling loop that step 2 rewires), 2b right after 2 (the script backend is the
first new backend because a paying client's rolling upgrade needs it, section 10, so it
does not wait for the configuration model: in its flat form, `prov-orchestrator = "script"`
and the three keys of 4.1 at cluster level, it needs no block), and 3 after 2b (the Proxmox
backend, the objective of this work, rides the rolling sequence 2b proves on the client's
cluster). Steps 3b, 5 and 6 can be reordered. The per-server override comes after the major, when a client case brings the two
orchestrators and the path between them (3.3).

Issue tracking (T9): #333 covers steps 1, 2, 3, 5, 6; #1775 step 4; #338 step 7. The gaps
of 2.5 marked **write** are filed as issues when their step opens, with `opensvc`,
`Kubernetes`, `onpremise` labels, not now (no issue for undelivered work). One open pull
request per step and per session (T29).

### 5.3 The two tests that lock the matrix

`TestOrchestratorMatrix` (`cluster/orchestrator_matrix_test.go`): a table of every (type,
method) with the expected status `implemented`, `notSupported`, `todo`. For each cell it
calls the method on the backend built from a minimal `config.Config` with a fake client
(the Kubernetes fake clientset already used by `prov_k8s_test.go`; an OpenSVC collector
stub; a script that exits 3) and asserts `implemented` returns no
`ErrOrchestratorNotSupported`, `notSupported` returns it, `todo` is listed in the document.
A new method added to the interface without a row fails the test: the matrix of 2.5 and the
test are one.

`TestNoOrchestratorDispatchOutsideRegistry` (`cluster/orchestrator_dispatch_test.go`):
parses the `cluster`, `server` and `mcp` packages with `go/ast` and fails on a comparison
to `config.ConstOrchestrator*` or a call to a backend-prefixed method (`OpenSVC*`, `K8S*`,
`OnPremise*`, `Localhost*`, `SlapOS*`) outside the allowed files (the adapters, the
registry, the explicit-refusal list). The allowed list shrinks at each step and is empty
at step 8. This is the lock: a switch cannot come back.

---

## 6. What the next major changes for users, and the off-switch (T14)

Nothing changes for a configuration that is not touched: the flat keys and
`prov-orchestrator = "<type>"` keep working through the synthesized orchestrator (3.2),
with the same behaviour on every operation that works today. The changes:

- A cluster named `orchestrator` is refused at load (the TOML table is reserved, 3.1).
- The API action `update-opensvc-template` (servers and apps) is renamed
  `update-service-definition`; the old path answers until the major and is removed at the
  major. The dashboard button carries the new name from step 4.
- `GET /api/clusters/{cluster}` returns `provOrchestrator` as the configured value (a type
  or a name); `provOrchestratorType` is added. At the major the dashboard switches to the
  type for its show/hide logic.
- An unknown `prov-orchestrator` value no longer falls back to `onpremise` with a log line
  (`SetProvOrchestrator`) nor kills the process (`initOrchetratorNodes`): the cluster loads
  with WARN0327 and no provisioning action (3.4).
- Operations that were silent no-ops on a backend (1.1 to 1.7) either work (the **write**
  cells of 2.5) or answer "not supported" explicitly: an API caller sees HTTP 501 where it
  saw 200 with nothing done. The localhost stop calls the localhost stop.
- The MCP app tools answer the same "not supported" error on every non-OpenSVC
  orchestrator as today, from the sentinel instead of a string check: no change for the
  caller.
- The default `prov-orchestrator` does not change (`onpremise`, `opensvc` on a build with
  OpenSVC): the script backend is an opt-in type (decision 3, section 8).

Off-switch during the transition: `prov-orchestrator-legacy-dispatch` (bool, default
`false`, `scope:"server"`). When true, the orchestration layer calls the switch-based
functions of the current release for the operation groups whose step landed in that
release, instead of the interface. It exists for one release per step (the release that
carries the step), is removed when the step's switches are deleted (the next release), and
is documented as such. It is a refactor's safety net, not a feature switch: the interface
is not a feature a user turns off, and the perpetual-monitoring laws are untouched by it
(no state, buffer or bound depends on the dispatch). The script backend, the named blocks
and the per-server map are features and need no switch beyond not configuring them
(`type = "script"`, a block, `prov-db-orchestrators` empty).

---

## 7. Documentation and interfaces to ship with each step (T6, T8)

- User documentation (docs.signal18.io): the named orchestrators and the `[orchestrator]`
  table (step 6), the script backend contract (step 7), the generic
  `update-service-definition` action (step 4), the two new states (step 1).
- This document is updated at each step: the inventory rows of a group removed, the matrix
  cells moved from **write**/**today** to **implemented**.
- Swagger regenerated for every new or renamed endpoint (`doc/implementation/server/API_SWAGGER.md`).
- The dashboard: the orchestrators page in Settings (step 6), the renamed action (step 4),
  the type-based show/hide (step 8), and in step 3 the proxy configuration switch and its hook
  beside `prov-db-apply-dynamic-config` in the cluster settings, with "runtime differs from
  configuration" (WARN0328) on the proxy's card (section 12).

---

## 8. Decisions (taken by the maintainer, 2026-11-18)

1. **Synchronous backends (2.2): yes.** The backend methods return their error; the
   `errorChan` goroutine wrapper lives in `cluster/prov.go` only; done in step 2. This
   removes the ~90 sends from the backends and closes the #1769 class by construction.
2. **Configuration (3): yes to the blocks, the override later.** Top-level
   `[orchestrator.<name>]` blocks carrying today's key names, `prov-orchestrator` naming a
   block or a type, a flat configuration synthesized into an orchestrator named after its
   type. The per-server override `prov-db-orchestrators` stays in the design as a later
   step (after the major), gated on one rule and not on a mechanism: a server may be placed
   on another orchestrator only when the other servers of its cluster reach it at a private
   address (3.3). The path between the orchestrators is the infrastructure's job.
3. **Script backend (4): opt-in type in this major.** `onpremise` stays the default
   (`opensvc` on a build with OpenSVC); revisited once the reference script has run on a
   real cluster.

Open points, kept open (the Kubernetes column of the matrix is under a separate review):
whether an `AppOrchestrator` for Kubernetes is wanted (today apps are OpenSVC-only; the
matrix lists it as `write or n/a`); whether `NodeStats` on Kubernetes reads the metrics API
or stays `n/a`; the name of the generic action (`update-service-definition` here,
`update-service-template` in #1775).

---

## 9. Step 1, scoped: the interface, the registry, the adapters

The first implementation pull request. It changes no caller and no behaviour: after it,
every switch of section 1 still runs; the interface exists beside them, implemented by
five adapters that call today's methods, and the two tests of 5.3 exist with their
allowed list equal to today's inventory. It references #333 (T9).

### 9.1 Files

| File | Content |
|---|---|
| `cluster/orchestrator.go` | rewritten: the seven interfaces of 2.1, `OrchestratorTraits`, `ServiceLayout`, `Storage`, `NodeStats`, `TerminalTarget`, `TerminalRequest`, `TerminalCommand`, `ErrOrchestratorNotSupported`, `notSupported(op string, orch OrchestratorInfo) error` (the wrap helper). The 2021 `Orchetrator` struct, `DatabaseOrchetrator` and `orchestratorList` are deleted. |
| `cluster/orchestrator_registry.go` | `newOrchestratorList()` called from `cluster.Init` beside `newProxyList()`; `cluster.Orchestrators map[string]Orchestrator` (the synthesized one under its type name) and its `orchestratorsMutex`; `cluster.Orchestrator() Orchestrator` (the cluster's, built on the spot when `prov-orchestrator` changed since `Init`, a `nullOrchestrator` for an unknown type: never nil); `cluster.orchestrator(server *ServerMonitor) Orchestrator` (returns the cluster's until 3.3 exists); `orchestratorConfig` and `synthesizedOrchestratorConfig` (the flat keys as a block, test only); `unsupportedOrchestrator`, the base every adapter embeds: a method an adapter does not declare falls through to it and answers the sentinel, so "not supported" is the absence of a method, visible in the file; `notSupportedStore`; `awaitProvisioning`; `databaseLayout`, `proxyLayout` (today's getters). The load-time refusal of an unknown type with WARN0327 instead of `log.Fatalln` is NOT in this step (it is a behaviour change: step 5). |
| `cluster/orchestrator_opensvc.go` | `type openSVCOrchestrator struct{ unsupportedOrchestrator; cluster *Cluster; name string }`; every method calls the existing `OpenSVC*` method or the existing helper (`openSVCStore`, `openSVCCurrentDatabaseImage`, `GetDatabaseServiceConfig`, `OpenSVCGetPoolInfoList`, `GetGottyServer`); `Freeze`/`Unfreeze` call `FreezeDatabaseService`/`UnfreezeDatabaseService` (the v2 nil kept, documented); `RefreshDatabaseServiceDefinition` is the v3 `OpenSVCUpdateDatabaseTemplate` with the `keepImage` rule, `notSupported` on v2; `RestartDatabaseService` with an empty `rid` returns `notSupported` (the caller's stop-wait-start stays in prov.go); `UpgradeDatabaseService` and `RefreshProxyServiceDefinition` are not declared (2.5). The methods that today report on `errorChan` (the database, proxy and app provision and unprovision) go through `awaitProvisioning`: `go run(); return <-cluster.errorChan`, received on the cluster's channel under the caller's `provisioningMutex` exactly as the switch statements receive it today (a private channel is not possible while the sends are inside the backends); the adapter is synchronous and today's code is untouched until step 2 removes the sends and deletes the helper. |
| `cluster/orchestrator_k8s.go` | `type k8sOrchestrator struct{ cluster *Cluster }`; the `K8S*` methods; `Storages` from `K8SGetStorageClasses`; `DatabaseServiceDefinition` from `K8SGetDatabaseManifests`; `Freeze`, `Unfreeze`, `NodeStats`, `DatabaseServiceImage`, `DatabaseWorkingNode`, `UnprovisionSecret`, the app methods, `Abort`/`Clear`, `TerminalCommand` return `notSupported` (the **write** cells of 2.5 are not written here). |
| `cluster/orchestrator_onpremise.go` | `type onPremiseOrchestrator struct{ cluster *Cluster }`; `OnPremise*` methods, `UpgradeDatabaseService` from `OnPremiseUpgradeDatabaseService`, `TerminalCommand` from `OnPremiseGetSSHKey` and the SSH command of `server_tty.go` (moved as a helper, the handler still builds its own in this step); the rest `notSupported`. |
| `cluster/orchestrator_localhost.go` | `type localhostOrchestrator struct{ cluster *Cluster }`; `Localhost*` methods; `StopDatabaseService` calls `LocalhostStopDatabaseService` (the adapter is correct; the switch keeps calling the on-premise stop until step 2, so no behaviour changes in this step: the discrepancy is a row of the matrix test marked `legacyDiffers`). |
| `cluster/orchestrator_slapos.go` | `type slapOSOrchestrator struct{ ... }`; the `SlapOS*` database methods and the proxy start and stop; the proxy provision and unprovision are not declared: today's methods are empty and never report (2.5). |
| `config/error.go` | ERR00122, WARN0326, WARN0327 texts (2.4). Not raised by anything in this step. |
| `cluster/cluster.go` | the `Orchestrators` field and its mutex; the `newOrchestratorList()` call in `Init` beside `newProxyList()`. |
| `server/server_tty.go` | its three `terminalTarget*` constants read `cluster.TerminalTarget*` (the one cross-package move). |
| `cluster/orchestrator_matrix_test.go` | `TestOrchestratorMatrix`, `TestOrchestratorNullAnswersEveryMethod` (9.3). |
| `cluster/orchestrator_dispatch_test.go` | `TestNoOrchestratorDispatchOutsideRegistry` (9.3) with the allowed list equal to the measure of today: 710 lines in 75 files. |
| `cluster/orchestrator_registry_test.go` | `TestOrchestratorSynthesized`, `TestOrchestratorRegistryBuildsEveryType`, `TestOrchestratorResolvesClusterOrchestrator`, `TestOrchestratorNotSupportedWraps`. |

No other change in `server/`, `mcp/`, `regtest/`, `config/config.go` (no new setting: the
off-switch of section 6 arrives with step 2, the first step that reroutes a caller), no
Swagger, no ACL, no GUI (no endpoint changes), no user documentation (nothing a user can
see; T8 is satisfied by this document until step 2). Implemented as pull request #2408.

### 9.2 The declaration

Exactly the Go of 2.1, with these fixed decisions:

- Package `cluster`, unexported adapters, exported interfaces and sentinel (as
  `DatabaseProxy` and `HaproxyProxy`). The adapters hold `cluster *Cluster` because every
  backend method is a `Cluster` method today; the field goes when the methods move (steps
  2 to 5).
- Compile-time checks at the top of each adapter file:
  `var _ Orchestrator = (*openSVCOrchestrator)(nil)` and the same for the four others.
- `Storage` is a new struct `{Name, Type string; Shared bool; Capabilities []string}`: what
  the two existing API endpoints hold (the OpenSVC `PoolInfo` has a name, a shared flag and
  capabilities; the Kubernetes storage class a name). There is no capacity or free space to
  report today; the endpoints keep their own types in this step.
- `NodeStats` aliases `opensvc.DaemonNodeStats` in this step (the only producer); a neutral
  struct comes with the Kubernetes answer, if any (open point).
- `TerminalTarget` carries the `"server"`, `"proxy"`, `"app"` values of
  `server/server_tty.go`, which now reads its own `terminalTarget*` constants from
  `cluster.TerminalTarget*`: the one cross-package move of the step, three lines.
- `notSupported(op string, orch OrchestratorInfo) error` returns
  `fmt.Errorf("%w: %s on %s (%s)", ErrOrchestratorNotSupported, op, orch.Name(), orch.Type())`.
  The OpenSVC adapter turns `ErrOpenSVCAbortNotSupported` and `ErrOpenSVCClearNotSupported`
  into errors that are both sentinels (`openSVCNotSupported`); their declarations stay
  until step 3 removes their last direct caller.
- The adapters do not spell out an unsupported method: they embed `unsupportedOrchestrator`
  and declare only what they implement. A reader of `orchestrator_k8s.go` sees the
  Kubernetes column of 2.5 as the list of declared methods; the matrix test reads it the
  same way.

### 9.3 Tests

- `TestOrchestratorMatrix`: a table method -> backend type -> status with one cell per
  method of 2.5 and per backend (`implemented`, `notSupported`, `legacyDiffers`: the
  localhost stop, right in the adapter, different in the switch of prov.go). The source of
  truth for "implemented" is the adapter file's AST: a method the file declares on its
  receiver is implemented, one it leaves to `unsupportedOrchestrator` is not. The
  `notSupported` cells are called through reflection with zero arguments on a backend built
  from a `config.Config` with `ProvOrchestrator` set, and must answer an error for which
  `errors.Is(err, ErrOrchestratorNotSupported)` (the `SecretStore` cell: its store's
  `ReadCurrent` answers it). The implemented cells are not called: they reach real
  orchestrators and wait on them; the regtest suite is what proves them (T13). A method of
  the interface without a row fails the test (reflection over the method set), and a row
  that names no method fails too. 50 methods, 5 backends, 250 cells: OpenSVC 48 implemented
  and 2 not supported, Kubernetes 24 and 26, on-premise 17 and 33, localhost 15 and 35,
  SlapOS 13 and 37. `TestOrchestratorNullAnswersEveryMethod` calls every method of the null
  orchestrator the same way.
- `TestNoOrchestratorDispatchOutsideRegistry`: `go/parser` over `cluster`, `server`,
  `mcp`, `cluster/configurator` (non-test files); a dispatch line is a line holding a
  `config.ConstOrchestrator*` selector, a `BinaryExpr` comparing a `ProvOrchestrator`
  selector to a string literal, or a `CallExpr` through a selector to a method named
  `OpenSVC*`, `K8S*`, `k8s*`, `OnPremise*`, `Localhost*` or `SlapOS*`. The distinct lines
  are counted per file and compared to the allowed list for equality: a file above its count
  added a switch, a file below it removed one and must lower its count in the same pull
  request, a file not listed is refused, a listed file with no line left must leave the
  list. From step 2 the same test carries a second rule (2.6): **no backend file opens a
  state** — a call to `SetState`, `AddState` or `PreserveState` in `prov_*.go`,
  `store_orchestrator*.go` or `orchestrator_*.go` fails it; the 18 calls of today in 7 files
  are that rule's allowed list, counted per file, shrinking with the steps as the dispatch
  list does. Measured at step 1 and locked: **710 lines in 75 files**. The grep of the preamble
  (227) sees the type comparisons only; the measure adds the backend-method calls, which
  are the dispatch the lock is for (the 27 direct calls from `server/` and `mcp/`, the
  backends calling one another). The heaviest files: `prov.go` 156, `prov_k8s_db.go` 57,
  `prov_opensvc_db.go` 49, `prov_opensvc_prx.go` 46, `prov_opensvc_app.go` 45. Each later
  step lowers its files' counts; at step 8 the list is the adapters and the registry.
- `TestOrchestratorSynthesized`: a cluster with `ProvOrchestrator = "opensvc"` and the flat
  keys, and the same keys as an `orchestratorConfig` block (the struct exists in this step
  for the test only, unexported, without the `config.Config` field: that field is step 6);
  `synthesizedOrchestratorConfig` of the flat keys equals the block, both build one
  orchestrator whose `Name()`, `Type()` and `Traits()` are equal, and the synthesized one is
  named after its type. `TestOrchestratorRegistryBuildsEveryType`: the five types build
  their backend under their name.
- `TestOrchestratorResolvesClusterOrchestrator`: `cluster.orchestrator(server)` returns
  `cluster.Orchestrator()` for every server (nil included); a cluster with an unknown
  `ProvOrchestrator` gets an empty registry and `cluster.Orchestrator()` returns a
  `nullOrchestrator`, never nil and never registered, whose methods return the sentinel; a
  type changed at runtime is built on the spot; a cluster without `Conf` (a test fixture)
  answers the null orchestrator too. `TestOrchestratorNotSupportedWraps`: the wrap text and
  the two OpenSVC sentinels being both sentinels.
- `go vet`, the existing unit tests of the `cluster` package, and the regtest suite once
  per orchestrator with no change expected (T13): OpenSVC on a dev instance, Kubernetes on
  kind, on-premise in the Docker framework, each on the step's head and on the `develop`
  it branched from, the gate being identical results. The adapters are not on any caller's
  path yet, so the regtest run is the proof that the step touched nothing: it is recorded
  in the pull request, as T28 asks.

Size, as implemented (#2408): 1,996 lines added (the interface file 236, the registry 405,
the five adapters 704, the three test files 661, the rest in `cluster.go`, `config/error.go`
and `server/server_tty.go`), one pull request, one review.

### 9.4 What step 2 inherits from the review of step 1

The two reviews of #2408 (both approve, no blocker) leave step 2 these points, recorded
here so the step is scoped from them and not rediscovered:

- **Per-proxy resolution.** `proxyServiceOrchestrator(prx)` (`prov.go`) routes an HAProxy in
  `haproxy-mode = standby` to the localhost backend whatever the cluster runs on. The
  registry resolves per cluster and per server; nothing resolves a proxy. Step 2 adds
  `cluster.proxyOrchestrator(prx DatabaseProxy) Orchestrator`, which returns the localhost
  orchestrator for that case and the cluster's otherwise, and routes `InitProxyService`,
  `UnprovisionProxyService`, `StartProxyService`, `StopProxyService` through it; the matrix
  test gets a row for the standby case. Rerouting them through `cluster.Orchestrator()` alone
  would be a behaviour change.
- **The sentinel on a provisioning path is a logged skip, not a failed provision.** When a
  caller of `ProvisionProxyService` or `UnprovisionProxyService` meets
  `ErrOrchestratorNotSupported` (the SlapOS proxies), it logs it at INFO, sets no error and
  leaves no failover state behind: a provision that does not exist is not a provision that
  failed (2.3, pattern 1).
- **`awaitProvisioning` has no bound.** `go run(); return <-cluster.errorChan` is as safe as
  the wrapped method's "send exactly once on every path", which today's callers already rely
  on. Step 2 deletes the helper with the sends; until then it is called by nothing. The
  pre-existing hang of a SlapOS cluster with a proxy (the two empty methods) is a bug of the
  current releases, to file as its own issue, not fixed inside the migration.
- **The lock's adapter counts do not move.** `orchestrator_*.go` and
  `orchestrator_registry.go` are the files that may name a type: their counts in
  `orchestratorDispatchAllowed` stay as they are through the migration (they grow only
  when a backend method moves in), and a migration step lowers the other files' counts.
- **The cache is keyed by name.** `Orchestrator()` returns the cached backend as long as
  `prov-orchestrator` is unchanged; the adapters read `cluster.Conf` live, so a flat key
  changed at runtime is seen. When step 6 reads `orchestratorConfig` into the backends, a
  changed block must rebuild its orchestrator (compare the config, or rebuild on reload); the
  stale entry of a type changed at runtime stays in the map, bounded by the five types.
- **`orchestratorConfig` copies secrets** (`onpremise-ssh-credential`, the p12 certificate
  path and, in step 6, its secret). It is never logged or serialized; step 6 gives it a
  redacting `String()` before anything reads it from a file (F10).
- **Two goroutines may build the same backend** on a cache miss; the last write wins and
  both are equivalent. Benign, noted.

---

## 10. The first new backends after step 1: the client script (step 2b), the Proxmox API (step 3b)

### 10.1 The case and the objective

A client on Proxmox runs their databases on-premise and orchestrates everything with
Ansible. Their requirement: during a replication-manager rolling restart, upgrade the
database **and** the host kernel at the same time, which means a reboot of each server's
host (its VM), one server after the other, the replicas first, the master last after a
switchover. replication-manager drives no hypervisor today and has no step for "the host"
in its rolling paths: the on-premise backend knows SSH into the database host, and nothing
above it.

The objective of this work is a **Proxmox API backend**: replication-manager drives the
VM under each database server itself (stop, start, reboot, status) through the hypervisor
API, and the rolling paths get a host step. It is step 3b (10.4), after the proxy and app
services exist on every backend (step 3). Its first step is the
**script backend** of #338 (step 2b, 10.2 and 10.3): the same rolling sequence, each
per-server step a call of the client's script, which runs the Ansible playbook they drive
everything with today, the host reboot included. The client keeps the orchestration they
have (F6) and moves to the Proxmox backend when it exists, by changing the cluster's
orchestrator; replication-manager keeps, on both, what only it knows: the order, the
election, the waits on replication, the maintenance flags, the states. The script backend
comes first because the client needs the rolling upgrade now and drives Proxmox from
Ansible already; the Proxmox backend comes second because the rolling sequence and the host
facet it needs are then in place and proven on the client's cluster.

### 10.2 The rolling paths through the interface

Today (`cluster/cluster_roll.go` on `develop`), the three rolling actions run a per-server
sequence each, by hand, with the type switches inside the functions they call:

| Action | Per replica, then switchover, then the old master | Where the type decides today |
|---|---|---|
| `RollingRestart` | maintenance on; wait for a running binlog backup; `FreezeDatabaseService`; `StopDatabaseService`; `WaitDatabaseFailed`; `UpgradeDatabaseDeploymentOnStart(keepImage)`; `StartDatabaseWaitRejoin`; `UnfreezeDatabaseService`; `WaitSyncToMaster`; maintenance off. A one-server cluster: `rollingRestartMasterInPlace`, the same without the switchover. | the freeze (OpenSVC v3 only), the stop, the deployment refresh, the start |
| `RollingUpgrade` | on-premise: `rollingUpgradeOnPremise`: maintenance on; `StopDatabaseServiceClean` (the SQL prelude, then the stop); `WaitDatabaseFailed`; `UpgradeDatabaseService` (the `onpremise-ssh-upgrade-db-script`, which upgrades the packages, runs the upgrade tool and starts the database); `WaitDatabaseStart`; `WaitSyncToMaster`; maintenance off. Container backends: `rollingUpgradeStopUpdateStart` twice (pull, clean): `UpdateDatabaseServiceConfig`; freeze; stop; `WaitDatabaseFailed`; `StartDatabaseWaitRejoin`; unfreeze. | `cluster_roll.go:492` picks the on-premise path by type; `rollingUpgradeStopUpdateStart` tests Kubernetes by type |
| `RollingReprov` | maintenance on; `UnprovisionDatabaseService`; `WaitDatabaseFailed`; `InitDatabaseService`; `StartDatabaseWaitRejoin`; `waitRollingReseed`; `WaitSyncToMaster`; maintenance off. | the provision and unprovision switches of `prov.go` |

Step 2 (with 2b) makes the three actions one per-server sequence, `rollingServer(server,
mode)` with `mode` in `restart`, `upgrade`, `reprov`, whose backend calls are the
interface's and nothing else:

```
maintenance on (repman)
wait for a running binlog backup of the server (repman)
orch.FreezeDatabaseService(server)                  ErrOrchestratorNotSupported: go on unfrozen
reprov:   orch.UnprovisionDatabaseService(server)
else:     orch.StopDatabaseService(server)          upgrade: after the SQL prelude of StopDatabaseServiceClean
WaitDatabaseFailed (repman: the monitor sees the server down)
upgrade:  orch.UpgradeDatabaseService(server)       ErrOrchestratorNotSupported: UpdateDatabaseServiceConfig(forcePull) then start, the two-phase pull of today
restart:  orch.RefreshDatabaseServiceDefinition(server, keepImage=true)   ErrOrchestratorNotSupported: go on
reprov:   orch.ProvisionDatabaseService(server)
orch.StartDatabaseService(server)                   unless the upgrade step started it (the backend says so by leaving the server up: WaitDatabaseStart below is the check)
WaitDatabaseStart, WaitRejoin, reprov: waitRollingReseed (repman)
orch.UnfreezeDatabaseService(server)
WaitSyncToMaster (repman: replication caught up)
maintenance off (repman)
```

The replicas run it one by one; then `SwitchoverWaitTest` (repman's election: the
candidate, the checks, the proxies); then the old master runs it. What repman still does
itself and no backend touches: the order and the switchover, the maintenance flag per
server (set before, cleared after, restored on every error path as today), the waits
(`WaitDatabaseFailed`, `WaitDatabaseStart`, `WaitRejoin`, `WaitSyncToMaster`,
`waitRollingReseed`, the binlog backup), the failover state of the cluster, the states of
the servers (ERR00122 on a failed step, 2.4), the client hooks of `prov_scripts.go` around
each backend call, the cookies, the logs. A backend sees one server at a time and the
action asked of it; it never sees the cluster's order.

### 10.3 The client's steps as the script's actions

With `prov-orchestrator = "script"` on the cluster, every call of 10.2 is
`<script> <action> database <host> <port> <cluster>` (4.2). The client's rolling upgrade
is then their playbook behind five actions:

| Action the script receives | When, in 10.2 | What the client's playbook does | Exit 3 (not supported) means |
|---|---|---|---|
| `freeze` | before the stop | nothing to freeze on a plain host: exit 3 | repman goes on unfrozen, as on Kubernetes |
| `stop` | after the maintenance flag and the binlog wait; `upgrade` mode: after repman's SQL prelude (`innodb_fast_shutdown = 0`, `SHUTDOWN WAIT FOR ALL SLAVES` on a master) | stop the database service on the host | not allowed here: a `stop` that cannot be done fails the server's step (ERR00122), the rolling stops at this server with its maintenance flag on |
| `upgrade` | `upgrade` mode only, once the monitor sees the server down | **the host step**: upgrade the kernel and the packages, reboot the VM (through Proxmox, Ansible, anything), wait for the host to be back, upgrade the database packages, start the database, run the upgrade tool. The server must be reachable on its port when the playbook returns: `WaitDatabaseStart` is repman's check | repman runs the container backends' two-phase pull: not what this client wants; their script answers 0 |
| `refresh-service-definition` | `restart` mode only, once the server is down | exit 3: a host has no service definition to push | repman goes on |
| `start` | after the upgrade step, or right after the stop in `restart` mode | start the database service if the upgrade step did not | repman's check is the same wait: a `start` that answers 0 on an already running server is right |
| `restart` | never in a rolling path (the rolling never asks a backend for its own restart: it needs the stop and the start apart to wait in between) | - | - |
| `provision`, `unprovision` | `reprov` mode | rebuild the server's data directory, or the VM | exit 3: the reprov is refused on that cluster |

The host reboot therefore lives in the `upgrade` action, which the **rolling upgrade**
action of the GUI, the API and the MCP runs; the rolling restart (`restart` mode) stops and
starts without upgrading anything, on this backend as on every other. The client who wants
a kernel upgrade runs a rolling upgrade: that is what the action means on every backend
(a new image on the containers, the upgrade script on-premise, the playbook here). The
on-premise backend's `onpremise-ssh-upgrade-db-script` is the same contract over SSH; the
script backend's is the same contract on the monitor host, with the Ansible inventory the
client already has.

**What the script receives** (4.2, with three additions for the rolling context):

- argv: the action, `database`, the server's host and port, the cluster name;
- environment: `REPLICATION_MANAGER_URL`, `REPLICATION_MANAGER_USER`,
  `REPLICATION_MANAGER_PASSWORD` (the API login, so the playbook can read the cluster or
  ask for a state through the API), `REPLICATION_MANAGER_CLUSTER_NAME`,
  `REPLICATION_MANAGER_ORCHESTRATOR`, `REPLICATION_MANAGER_ACTION`, `REPLICATION_MANAGER_KIND`,
  `REPLICATION_MANAGER_SERVER_HOST`, `REPLICATION_MANAGER_SERVER_PORT`,
  `REPLICATION_MANAGER_SERVER_NAME`, `REPLICATION_MANAGER_SERVER_ID`,
  `REPLICATION_MANAGER_DB_USER`, `REPLICATION_MANAGER_DB_PASSWORD`, and for the rolling
  paths: `REPLICATION_MANAGER_ROLLING` (`restart`, `upgrade`, `reprov`; empty outside a
  rolling action), `REPLICATION_MANAGER_ROLLING_STEP` (`n/total`, the server's rank in the
  run), `REPLICATION_MANAGER_SERVER_ROLE` (`master`, `slave`, `standalone`: what the
  monitor sees at the moment of the call, so a playbook can treat the old master's step
  differently). The `prov-orchestrator-script-env` pairs come last: the Proxmox API
  endpoint, the inventory path, anything the playbook wants, never a secret (4.1).
- stdin: the server's JSON as `GET /api/clusters/<cluster>/servers/<server>` serves it:
  `id`, `url`, `host`, `port`, `state`, `isMaster`, `replicationRole`, `dbVersion`,
  `agent`, `serviceName`, the replication status, the variables the monitor holds; the
  password is absent (F10). A playbook reads it with `jq` or the Ansible `from_json`
  filter and needs nothing else from repman.
- stdout is logged at DEBUG and stderr at WARNING, attributed to the server; the exit code
  decides (4.2). `prov-orchestrator-script-timeout` bounds each action: a kernel upgrade
  with a reboot takes minutes, so this client sets it to their reboot budget (the default
  600 s, a third of it for the other actions, 4.1).

**How the client switches it on, per cluster** (T14, T16): three keys in the cluster's
section, nothing instance-wide:

```toml
[cluster1]
prov-orchestrator = "script"
prov-orchestrator-script = "/usr/local/bin/repman-rolling"      # runs ansible-playbook with the action
prov-orchestrator-script-timeout = 1800                          # a reboot and a package upgrade
prov-orchestrator-script-env = "ANSIBLE_INVENTORY=/etc/ansible/hosts,PROXMOX_API=https://pve.example:8006"
```

Another cluster of the same instance stays on its own orchestrator; the setting is
reloadable like every other, and immutable when set under `/etc`. Turning it off is
`prov-orchestrator` back to its previous value. Before step 2b exists, the same client can
already approximate the flow with the hooks of `prov_scripts.go` (`prov-db-stop-script`,
`prov-db-start-script`), which run around the on-premise backend's own stop and start:
they cannot replace them and have no `upgrade` step, so they do not cover the reboot;
that is the gap step 2b closes.

### 10.4 Step 3b: the Proxmox API backend and the host facet

The database service and its host are two things: the on-premise backend stops and starts
the service on a host it does not own; a hypervisor backend owns the host (the VM) and
nothing inside it. So the Proxmox backend is not a fifth way to stop a database: it is the
on-premise backend's service operations (SSH into the guest, as today) **plus a new facet
for the host**, which only a backend that drives a hypervisor implements:

```go
// HostOrchestrator (step 3b) is the host under a database server, for a backend that
// drives a hypervisor. The other backends answer ErrOrchestratorNotSupported; the script
// backend maps it to the host-* actions (10.3).
type HostOrchestrator interface {
	StopHost(server *ServerMonitor) error
	StartHost(server *ServerMonitor) error
	RebootHost(server *ServerMonitor) error
	HostStatus(server *ServerMonitor) (HostStatus, error)
}

// HostStatus is what the hypervisor says of the VM: its node, its state, its uptime.
type HostStatus struct {
	Node   string // the hypervisor node the VM runs on
	VMID   string
	State  string // "running", "stopped", "paused", "unknown"
	Uptime time.Duration
}
```

`Orchestrator` (2.1) embeds `HostOrchestrator` from step 3b; `unsupportedOrchestrator`
answers the four methods for the backends without hosts, and the matrix test gets the four
rows. `OrchestratorTraits` gets `HasHost bool`: the rolling sequence reads it.

**The rolling upgrade with a host step** (10.2, `upgrade` mode, on a backend whose
`Traits().HasHost` is true and whose cluster has `prov-db-rolling-upgrade-host = true`):

```
orch.StopDatabaseService(server)             the service, inside the guest (SSH, as on-premise)
WaitDatabaseFailed (repman)
prov-db-host-upgrade-script (repman runs the client's hook: the kernel and package upgrade
                             inside the guest, F6: the hypervisor cannot upgrade a guest kernel)
orch.RebootHost(server)                      the VM, through the hypervisor API
wait orch.HostStatus(server).State == running, then the guest answers SSH (repman, bounded by prov-db-host-reboot-timeout)
orch.UpgradeDatabaseService(server)          the database packages and the upgrade tool, inside the guest
orch.StartDatabaseService(server)            unless the upgrade step started it
WaitDatabaseStart, WaitRejoin, WaitSyncToMaster (repman)
```

Without the host step (the setting off, or a backend without hosts) the sequence is the one
of 10.2. The host step is a feature with its own off-switch (T14: `prov-db-rolling-upgrade-host`,
default `false`), and the host upgrade itself is the client's script (F6): repman reboots,
it does not patch a kernel. A `RebootHost` that fails, or a host that does not come back
within the timeout, is ERR00122 on the server, the rolling stops at it with its maintenance
flag on, as any failed step.

**The backend.** `orchestrator_proxmox.go`, `type proxmoxOrchestrator`, embedding the
on-premise adapter's service operations (SSH into the guest for provision, start, stop,
upgrade, the nodes as the configured hosts) and implementing `HostOrchestrator` through the
Proxmox REST API (`/nodes/{node}/qemu/{vmid}/status/{stop,start,reboot,current}`, token
authentication). Its traits are the on-premise ones plus `HasHost`. `Nodes()` lists the
hypervisor nodes (`/nodes`) so the inventory shows where each VM runs; `Storages()` the
storages of the node, `n/a` otherwise as on-premise.

**Its configuration** (T16, T19), the flat form at cluster level or a block of
`type = "proxmox"`:

| Key | Meaning |
|---|---|
| `proxmox-api-url` | the API endpoint |
| `proxmox-api-token` | the token (`user@realm!tokenid=secret`), a secret: encrypted in the file (F9), never logged (F10) |
| `proxmox-api-insecure` | accept the API's certificate without verification (a lab); default `false` |
| `prov-db-host-vmids` | `host=node/vmid` pairs, comma separated (`db1=pve1/101,db2=pve2/102`), the VM under each database server; a server without one has no host step (WARN0327 names it when the host step is on) |
| `prov-db-rolling-upgrade-host` | the host step in the rolling upgrade (above); default `false` |
| `prov-db-host-upgrade-script` | the client's host upgrade hook, run inside the guest step above; empty: no upgrade, a reboot only |
| `prov-db-host-reboot-timeout` | seconds for the VM to be `running` again and the guest to answer; default 600 |

The on-premise SSH keys (`onpremise-ssh-*`) stay the guest access. The client of 10.1
moves from the script backend to this one by changing `prov-orchestrator` from `script` to
`proxmox` and adding the keys above; their playbook's host step becomes
`prov-db-host-upgrade-script`, the reboot becomes repman's. Its secret facet is the
on-premise one (11.2): the credentials directory inside each guest, over SSH.

**Regtest (T13).** A Proxmox lab the project reaches (one node, three VMs, the on-premise
Docker framework inside them is not enough: the host step needs real VMs): the rolling
upgrade with the host step on and off, a VM that does not come back (the timeout and the
state), a server without a vmid. Until the lab exists the backend's unit tests run against
a fake Proxmox API (`httptest.Server` answering the four endpoints) and the matrix test;
the T13 column is filled on the client's cluster, with their agreement, or on the lab.

**The script backend's host actions.** From step 3b the script backend (4.2) maps the facet
to four actions, `host-stop`, `host-start`, `host-reboot`, `host-status` (stdout: the
`HostStatus` JSON), so a client without a Proxmox backend yet gets the same rolling
sequence with the host step: their script answers `host-reboot` with the playbook that
reboots the VM, and `upgrade` no longer has to do it.

### 10.5 Step 3b as implemented (#2442)

The Proxmox backend landed before steps 2, 2b and 3, because the paying client of 10.1 needs
it and its service side is the on-premise one, already behind the interface. What was built,
and where it differs from 10.4:

- **The facet.** `HostOrchestrator` and `HostStatus` in `cluster/orchestrator.go`, embedded
  in `Orchestrator`; `OrchestratorTraits.HasHost`; `unsupportedOrchestrator` answers the four
  methods with the sentinel, so every existing backend is unchanged. The matrix test has the
  four rows and a `proxmox` column, derived from the on-premise one (it embeds the on-premise
  adapter) plus the host rows implemented; `matrixFiles` lists both files of the backend.
- **The backend.** `cluster/orchestrator_proxmox.go`: `proxmoxOrchestrator` embeds
  `*onPremiseOrchestrator` (its `unsupportedOrchestrator.orch` repointed so a not-supported
  error names proxmox), overrides `Name`, `Type`, `Traits` (on-premise plus `HasHost`) and
  `Check` (`GET /version` with the token). `Nodes()` stays the on-premise one (the database
  hosts, what `cluster.Agents` shows); listing the hypervisor nodes (`/nodes`) is in the
  client and not wired into the inventory yet. `StopHost` uses `/status/shutdown`, the clean
  shutdown, not the `/status/stop` of 10.4: a hard power-off of a guest that holds a database
  is never what an operator wants from this button. `RebootHost` of a stopped VM starts it.
  Each action waits for its Proxmox task (`/nodes/{node}/tasks/{upid}/status`) until
  `hostActionDeadline(server)`, and logs the `pvesh` command that reproduces it (F11). A
  failed status read before a reboot is logged (WARNING) and the reboot asked anyway.
- **The API client.** `utils/proxmox`: seven calls, token in the `Authorization` header
  only, every request bounded (30 s), answers capped at 1 MB, no error carries the token. The
  PBS client of the archive plugin is another product's API and internal to that plugin, so
  nothing is shared with it.
- **The dispatch bridge.** Until step 2 routes the callers through the interface, ~120 call
  sites switch on `cluster.GetOrchestrator()`. `GetOrchestrator()` now returns
  `serviceOrchestratorType(prov-orchestrator)` (registry file): `onpremise` for `proxmox`, the
  type itself otherwise, so every switch treats a proxmox cluster as an on-premise one, with
  no new case anywhere (the dispatch lock counts only the registry's three lines and the
  backend's `Type()`). The configured value stays `Conf.ProvOrchestrator` (the GUI reads it),
  and `cluster.Orchestrator()` resolves it to the proxmox backend, so the host facet is
  reached through the interface only. The direct comparisons of `Conf.ProvOrchestrator` in
  `server/` test `opensvc` or `kube` only and are unaffected.
- **The rolling upgrade.** `cluster/cluster_roll_host.go`, `rollingUpgradeHostStep(server)`,
  called by `rollingUpgradeOnPremise` between `WaitDatabaseFailed` and
  `UpgradeDatabaseService`, for the replicas and the old master; an error restores the
  server's maintenance flag and stops the run, as the other steps of that function, and names
  the VM (`node/vmid`) as possibly stopped or mid-reboot. It runs
  `prov-db-host-upgrade-script` through `runGuestScript` (the SSH script runner the on-premise
  upgrade now shares, `prov_onpremise_db.go`), `RebootHost`, then waits for `HostStatus`
  `running` and a TCP answer on the guest's SSH port. The reboot and the wait share one
  deadline (`prov-db-host-reboot-timeout` from the reboot): the step stores it per server in
  `cluster.hostStepDeadlines`, the backend's task wait reads it through
  `hostActionDeadline`, and the wait gets what the reboot left, never a second timeout.
  The wait accepts only a new boot (the reboot race of the second review): the step reads
  the VM's uptime before asking the reboot (`hostBootBefore`), and `hostBoot.rebooted` takes
  the VM back only when its uptime is lower than before or no longer than the time since the
  request (plus 2 s of rounding), with the state `running` and the SSH port answering. The old
  guest's uptime only grows from before, so it never passes; a VM whose status could not be
  read before is judged on the time since the request alone. The Proxmox uptime is used
  rather than the guest's `boot_id` over SSH: it comes from the same API call as the state,
  and needs no session into a guest that is going down. Two limits, both failing closed (the step times out, the
  database is never upgraded on a guest that did not reboot): the rule needs the Proxmox
  uptime to restart on an API reboot (the QEMU process is restarted, so it should: the T13
  run checks it, with and without the guest agent); and a VM booted less than 2 s before the
  request is taken back only by a poll that lands while its new uptime is still lower than
  that, which the 2 s poll can miss (the upgrade script runs in the guest just before, so a
  guest that young does not happen in practice). A status the hypervisor refuses (4xx: the
  token, a privilege, no such VM) ends the wait at once; a network error or a 5xx is retried
  until the deadline. The full `rollingServer` sequence of
  10.2 is still step 2's.
- **States.** `CheckRollingUpgradeHost`, each tick, config domain, WARNING, no I/O:
  `WARN0350` (host step on, backend without hosts; the design's WARN0342), `WARN0351` (servers
  without a VM; the design's WARN0327, a code taken since). A failed host step is a log line
  at ERROR and the rolling stops at the server: the per-server failure state of 2.4 is step
  2's (ERR00122 is used by the licence plugin today, the design's code needs a new number).
- **Settings and API.** The seven keys of 10.4 (`config.go`, `AddFlags`), settable by the
  settings API (the three free-text values in base64 like the other paths; the token through
  the credential chain, `SetProxmoxAPIToken`, masked in the change log; the two switches
  also by value for an idempotent Ansible call). Four endpoints in
  `server/api_database_host.go` (`actions/host-stop`, `host-start`, `host-reboot`, `GET
  host-status`), ACL rules reusing `db-stop`, `db-start`, both, and `db-show-status`; the
  actions run in the background (one per server at a time, 409 otherwise) and answer 202.
  The shutdown and the reboot are refused (409) by `HostActionRefusal` while the database on
  the VM runs, unless the server is in maintenance, and always on a running master; no
  override in this version. The rolling step does not ask it: it stopped the database. GUI:
  the server menu entries and the settings rows on a proxmox cluster. User doc:
  `doc/proxmox_orchestrator.md`.
- **Off-switch (T14).** `proxmox` is not in the default `prov-orchestrator-enable`; the host
  step has `prov-db-rolling-upgrade-host`, default false.
- **Follow-ups (review of #2447).** A cluster-scoped context for the background host
  actions and the host wait, so they end with the cluster instead of their timeout (they are
  bounded today); a `proxmox-api-ca` setting so a self-signed Proxmox certificate is trusted
  without `proxmox-api-insecure`.
- **Tests.** `TestProxmoxHostFacet` against a fake Proxmox API (`httptest`, the four VM
  endpoints, the task status, `/version`, token checked), `TestRollingSequenceHostStep` on a
  recording backend (off, on, server without a VM, reboot error, host not back within the
  timeout), `TestCheckRollingUpgradeHost`, `TestProxmoxServiceDispatchIsOnPremise`, the
  client's own tests in `utils/proxmox`. The T13 column (a Proxmox lab or the client's
  cluster) is open: no Proxmox VE host is reachable from the project's infrastructure today.

---

## 11. The secret facet on every backend: on-premise first

### 11.1 The rule

The `SecretOrchestrator` facet (2.1) is mandatory: `SecretStore`, `ServerSecretStore`,
`InitCredentials`, `RotateDatabasePassword`, `UnprovisionSecret`, and the root-password
drift check that `checkOpenSVCRootPassword` does for OpenSVC alone today, exist on every
backend. No backend answers `ErrOrchestratorNotSupported` to them once its step has landed;
until then the step 1 adapter's not-supported writer is the recorded gap of 2.5, and the
matrix test turns each cell to implemented with the step. What the facet guarantees,
whatever the backend:

- the cluster's services read the database root password, the jobs key and the
  replication-manager login from **one place the orchestrator owns**, written by
  replication-manager through the credential chain's version queue (`credstore.StoreWriter`,
  `WriteAll`), never by hand;
- a rotation reaches that place as it reaches the Vault entries, through the same queue, with
  the superseded value kept as `<KEY>_OLD` (as `openSVCStore` and `k8sStore` do);
- a drift between that place and replication-manager's reference is a state (WARN0275),
  rewritten at every tick until it clears; a failed write, a failed removal, a failed read is
  a state (ERR00122 on the server, WARN0326 on the orchestrator), never a log line alone
  (F7, T5);
- an unprovision removes it.

The one interface is the existing one: `orchestratorStore` (`store_orchestrator.go`) =
`credstore.StoreWriter` + `ReadCurrent(key)`. The on-premise, localhost and SlapOS backends
implement it; the script backend maps it to hooks. `orchestratorStoreWriter()`,
`initCredentials()`, `ProvisionRotatePasswords()`, `checkOpenSVCRootPassword()` and
`checkInitCredentials()` lose their type switches and call `cluster.Orchestrator()`'s facet.

### 11.2 On-premise: the credentials directory on each database host

What OpenSVC gives a database service today is a secret object (`<cluster>/sec/env`:
`MYSQL_ROOT_PASSWORD`, `JOBS_API_KEY`, `REPLICATION_MANAGER_PASSWORD`, `SHARDPROXY_ROOT_PASSWORD`)
projected as files in the container at `/credentials` (`{name}-sec/:/credentials` in
`OpenSVCGetVolumeDataSection`'s mounts, the `secrets` keyword of the `-sec` volume,
`prov_opensvc_db.go:1372-1383`). The jobs CLI already reads the key there
(`utils/jobapi/stream.go:99-104`: `$JOBS_API_KEY`, else `/credentials/JOBS_API_KEY`), and so
does the init container for its login. The on-premise backend gives the host the same thing:

- **Where:** a credentials directory on each database host, `/etc/replication-manager/credentials/`
  by default, root-owned, mode 0700, one file per key, mode 0600: `MYSQL_ROOT_PASSWORD`,
  `JOBS_API_KEY`, `REPLICATION_MANAGER_PASSWORD` (the login the config fetch uses, as the
  OpenSVC init container's), and the `<KEY>_OLD` files of a rotation. Setting
  `onpremise-credentials-dir` (T16, T19: the `onpremise-` family), default as above; a
  host where it cannot be created (not root, read-only) is ERR00122 on the server at provision.
- **How it is written:** over the SSH session the on-premise provisioning already opens,
  `OnPremiseConnect(server)` (`prov_onpremise_db.go:36`, `go-sshclient`, key or password from
  `onpremise-ssh-credential`), the one `JobRunViaSSH` (`srv_job.go:1543`) runs the jobs script
  through. The writer is `hostCredentialStore` (`cluster/store_orchestrator_onpremise.go`),
  implementing `orchestratorStore`: `WriteAll` opens the session once per call, writes each
  item to `<dir>/<KEY>.tmp` through the session's stdin (never on a command line, F10), moves
  it into place, keeps the previous content as `<KEY>_OLD`, with the version queue's timeout
  (`storeTimeout`); `ReadCurrent` reads the file over the same session. A server entry's store
  (`ServerOrchestratorStore`) is the same directory with the instance prefix on the file name
  (`<instance>_<KEY>`), as the OpenSVC and Kubernetes server stores prefix their keys.
- **Init credentials:** `initCredentials()` returns, for on-premise, `REPLICATION_MANAGER_PASSWORD`
  (the admin login the config fetch and the jobs use, `onlyIfPresent` false: there is no
  service definition to map it, the file is the mapping) and `JOBS_API_KEY`; `checkInitCredentials`
  repairs them once per activation as it does for OpenSVC. The jobs script reads
  `/credentials` today only inside a container: `JobRunViaSSH` exports the key in the SSH
  environment (`GetSshEnv`). Step 2 makes the SSH path read the directory too: `GetSshEnv`
  points `JOBS_API_KEY` and `MYSQL_ROOT_PASSWORD` at `<dir>/` when the directory exists (the
  CLI's `/credentials` lookup gains the directory as a second location, `utils/jobapi/stream.go`),
  so one script, one credentials path, every orchestrator.
- **Rotation:** `RotatePasswords` (`cluster_sec.go:228`) is orchestrator-independent until the
  orchestrator step: the chain sets the new values (`SetAllRevertible`), the SQL changes the
  accounts (`SetUserPassword`), then `ProvisionRotatePasswords(password)` hands the password
  to the orchestrator. On-premise that is `hostCredentialStore.WriteAll` of `MYSQL_ROOT_PASSWORD`
  on every database host of the cluster, the old value to `MYSQL_ROOT_PASSWORD_OLD`, through the
  queue (`queueOrchestrator`), so a host that does not answer is retried and WARN0275 stays open
  until it does; the same for `REPLICATION_MANAGER_PASSWORD` when the admin password changes
  (`cluster_user_rotate.go`).
- **Drift check:** `checkOpenSVCRootPassword` becomes `checkOrchestratorRootPassword`: it reads
  `ReadCurrent("MYSQL_ROOT_PASSWORD")` on every host, compares with `GetDbPass()`, opens WARN0275
  naming the host on a difference and queues the write; the text of WARN0275 names the store
  (`the credentials directory of <host>`) instead of the OpenSVC object. The check runs in the
  background, never on the tick, bounded by `storeTimeout`, as today.
- **Unprovision:** `UnprovisionSecret` removes the directory over SSH; a host it cannot reach
  leaves WARN0326 and the directory, named, for the operator.
- **What does not change:** the database itself (no SQL beyond the rotation's own), the client's
  scripts (`prov-db-*-script` keep running around each step), the immutable keys in `/etc`.
  Nothing of the host is read but the directory.

### 11.3 Localhost, script, SlapOS

- **Localhost:** `localCredentialStore` is `hostCredentialStore` without the SSH session: the
  directory is `<server.Datadir>/credentials/` (the layout's data directory, 2.1), written with
  the local file API, same files, same `_OLD`, same drift check, same removal at unprovision.
  The jobs run locally and read it through the same `GetSshEnv` path.
- **Script backend:** five hooks of the one script (4.2), kind `database` per server or
  `cluster` for the cluster's own store, the value **never on argv** (F10): `secret-write`
  (stdin: the server JSON, then the value on file descriptor 3, `REPLICATION_MANAGER_SECRET_KEY`
  the key name in the environment), `secret-remove` (the key, or the whole store for `cluster`),
  `init-credentials` (writes the two login keys; same contract as `secret-write`, called once per
  activation), `rotate-root` (the new password on fd 3, the old one on fd 4 for the `_OLD` the
  client keeps), `secret-check` (prints on stdout the current value of the key named in the
  environment, read by `ReadCurrent`; exit 3 = the client keeps no copy, the drift check then
  records WARN0275 as "unverifiable" and does not rewrite). The client of section 10 answers
  them with the Ansible task that writes their own vault or the host file; the reference script
  of step 2b writes the on-premise directory.
- **SlapOS:** SlapOS offers no secret object the services read; its databases are processes in
  partitions on hosts replication-manager reaches the same way as on-premise ones. The SlapOS
  backend inherits `hostCredentialStore` with the partition's directory as the location
  (`<partition>/etc/replication-manager/credentials/`, from `slapos-db-partitions`) over its own
  channel: the SlapOS backend has no SSH session today (its methods are cookies and a SQL
  shutdown), so step 2 gives it the on-premise session when `onpremise-ssh` is on for the
  cluster, and records ERR00122 on the server otherwise. SlapOS is not on any client's path; it
  gets the facet because the rule has no exception, not because a client waits.

### 11.4 What step 2 ships for the secret facet

`cluster/store_orchestrator_onpremise.go` (`hostCredentialStore`, `localCredentialStore`),
the five script hooks in `orchestrator_script.go`, the rewiring of `orchestratorStoreWriter`,
`initCredentials`, `ProvisionRotatePasswords`, `checkOpenSVCRootPassword`,
`checkInitCredentials`, `cluster_user_rotate.go` and `Unprovision` to the facet, the
`onpremise-credentials-dir` setting, the jobs CLI's second credentials location, the WARN0275
text naming the store, and the matrix rows of 2.5 turned to implemented. T13: the on-premise
Docker framework — provision writes the directory, the jobs run with the key from it, a
rotation rewrites it with `_OLD`, the drift check opens and clears WARN0275 when a file is
altered on the host, unprovision removes it; OpenSVC and Kubernetes rotation unchanged. Unit
tests: `TestHostCredentialStoreWriteAll` on a fake SSH session, `TestRotationRewritesHostCredentials`,
`TestScriptSecretHooks` (the value never in argv, the fd contract, exit 3 on `secret-check`).

---

## 12. Proxies: a route changed at runtime is materialized in the configuration

### 12.1 The principle (the maintainer's)

**If replication-manager changes a route in a proxy, whatever the orchestrator, the route
must be materialized in the configuration; if the backend cannot, the change is disabled for
that orchestrator.** The case behind it, at an on-premise client: replication-manager changed
HAProxy at runtime (the Runtime API), the configuration file on the host was never
regenerated, a hand restart of HAProxy reloaded the old file and the routes went back to the
wrong state. Nothing said so until the application failed.

It is the same concept as the database configurator's: replication-manager says whether it
is in charge of a configuration or not. For the databases that is
`prov-db-apply-dynamic-config` (`ProvDBApplyDynamicConfig`, "Dynamic database config
change"): on, replication-manager generates, pushes and reconciles the database configuration
(the configurator, the config bundle, the dynamic variables); off, it drives the database and
leaves the configuration to the client. The proxies get the sibling by the same transform:

| Setting | Field | Meaning |
|---|---|---|
| `prov-proxy-apply-dynamic-config` (bool, default the value of `prov-db-apply-dynamic-config`) | `ProvProxyApplyDynamicConfig`, json `provProxyApplyDynamicConfig` | **in charge:** replication-manager generates the proxy configuration (`GetProxyConfig`, the templates of `share/`), pushes it where the proxy reads it at start (`RefreshProxyServiceDefinition`: the OpenSVC template, the Kubernetes ConfigMap and Deployment, the file over SSH on-premise, the local file, the partition), and checks the drift. **not in charge:** replication-manager drives the proxy at runtime only; the client's hook materializes the change; without a hook the runtime change is refused. |
| `prov-proxy-config-change-script` (string, default empty) | `ProvProxyConfigChangeScript` | the client's hook that regenerates and reloads the proxy configuration when replication-manager is not in charge (an Ansible run, for instance), named as `prov-db-dynamic-resource-change-script` is: `<family>-<concept>-change-script`. It receives on stdin the proxy's JSON and the cluster's server list with their roles, as `GET /api/clusters/<cluster>/topology/proxies` and `.../topology/servers` serve them, the proxy's host and port on argv, the credentials in the environment (4.2); exit 0 = materialized, 3 = not supported, else failed. |

The two switches are one concept and share one place in the GUI: the cluster settings'
"configuration" group where `prov-db-apply-dynamic-config` sits, with the proxy switch and
its hook beside it, and the states of 12.2 shown on the proxy's card.

### 12.2 The transaction, the refusal, the drift

Every runtime change replication-manager makes to a proxy (12.3 lists them) is followed, in
the same operation, by the materialization of the regenerated configuration where the proxy
reads it at start:

- **in charge:** `RefreshProxyServiceDefinition(prx)` of the cluster's orchestrator (2.1),
  the backend's own push (2.5), through `cluster.proxyOrchestrator(prx)` (9.4);
- **not in charge, hook set:** the hook runs with the current server list; exit 0 closes the
  operation;
- **not in charge, no hook:** the runtime change is **not applied**: the method returns
  `ErrOrchestratorNotSupported` with the reason "proxy configuration not managed and no
  `prov-proxy-config-change-script`", and the state WARN0328 opens on the proxy; the feature
  (a failover's route switch, a maintenance, a weight) is visibly disabled for that proxy
  until the operator sets the switch or the hook. A failover never silently leaves a proxy
  pointing at the old master: the refusal is logged as the failover's own error on that
  proxy, and the state says what to set.

The runtime change and the materialization are one transaction from replication-manager's
point of view: a materialization that fails (the push refused, the file not written, the
hook exit non-zero) opens WARN0328 on the proxy, naming the proxy, the backend's target (the
template, the ConfigMap, the file, the hook) and the error; the GUI shows "runtime differs
from configuration" on the proxy. The runtime change itself is kept (the application is
routed right now); the state says the next restart of the proxy will not be.

**The drift check, per tick** (`CheckProxyConfigDrift`, in the proxy check loop of
`prx_chk.go`): the proxy's running definition (the HAProxy Runtime API's `show servers state`,
ProxySQL's `runtime_mysql_servers`, MaxScale's REST servers, the shard proxy's server
tables) compared with the configuration replication-manager expects for the current topology
(`GetProxyConfig` rendered in memory, not written); a difference opens WARN0328 with the
differing servers, whoever wrote the file, and clears it when they agree. The check reads,
never writes. The on-premise case of 12.1 would have been a state the day it happened.

| State | Level | Severity | When | Clears |
|---|---|---|---|---|
| WARN0328 | server (the proxy) | WARNING | the proxy's runtime differs from its configuration: a materialization failed, or a runtime change was refused for want of a hook, or the drift check found a difference. Text: `Proxy %s: runtime differs from configuration (%s): %s` | the materialization succeeds, or the drift check finds them equal |

### 12.3 Inventory of today's runtime changes without a regenerated configuration

| Proxy | Runtime change today | Where it is made | Configuration today, per orchestrator |
|---|---|---|---|
| HAProxy | `add server`, `del server`, `set server ... addr`, `set server ... fqdn`, `set server ... state maint/ready`, `set weight` through the Runtime API (stats socket) | `cluster/prx_haproxy.go`: `reconcileReadBackendServers`, `completeOrRollbackPendingAdd`, `removeReadBackendServer`, `SetMaintenance`, `setReadBackendMaintenance`, `Failover`, `BackendsStateChange`; `router/haproxy/runtime_api.go`, `runtime.go` | **localhost and `haproxy-mode = standby`:** `HaproxyProxy.Init()` renders `haproxy_config.template` to the local `haproxy.cfg` and reloads (`prx_haproxy.go:306-364`): materialized. **OpenSVC, Kubernetes:** the container fetches the generated configuration at start when `prov-proxy-start-fetch-config` is on (`startReappliesProxyConfig`): materialized at the next start, not written at the change. **On-premise:** `OnPremiseProvisionHaProxyService` copies `/bootstrap/etc/haproxy.cfg` to `/etc/haproxy/haproxy.cfg` and starts the service at provision (`prov_onpremise_haproxy.go:24`); **nothing rewrites it afterwards**: the client's case. `startReappliesProxyConfig` says so (on-premise never reapplies on start). **The first fix of step 3.** |
| ProxySQL | `INSERT`/`UPDATE`/`DELETE mysql_servers`, `LOAD MYSQL SERVERS TO RUNTIME`, the query rules, the users, through the admin interface | `router/proxysql/proxysql.go`: `AddServerAsReader`, `AddServerAsWriter`, `SetOffline`, `SetOnline`, `SetReader`, `SetWriter`, `DropReader`, `DropWriter`, `ReplaceWriter`, `AddUser`, `LoadQueryRulesToRuntime`; `cluster/prx_proxysql.go` | the proxy persists its own runtime through `SAVE MYSQL SERVERS TO DISK` (`proxysql.go:518`), `SAVE ... VARIABLES TO DISK` (533, 538): materialized in ProxySQL's own database on every orchestrator, as long as the proxy starts from that database and not from a configuration file (`prov-proxy-start-fetch-config` on OpenSVC and Kubernetes re-fetches the generated one). The drift check covers the case where a restart loads the file instead. |
| MaxScale | `SetServer`, `ClearServer` (maintenance, drain), `SetMasterAcceptReads`, `RestartMonitor` through the REST API (`PATCH`) or maxadmin | `router/maxscale/maxscale.go` | MaxScale persists REST changes in its own persisted configuration directory (its behaviour, to be verified in step 3); the generated `maxscale.cnf` is fetched at start on OpenSVC and Kubernetes; on-premise and localhost the file is the provision's. |
| Shard proxy (Spider) | the server and connection definitions changed in SQL on failover and backend change | `cluster/prx_mariadbshardproxy.go`: `Failover`, `BackendsStateChange` | the definitions live in the proxy's own system tables: materialized by the database itself. |
| MyProxy | in-process routing | `cluster/prx_myproxy.go` | the proxy is replication-manager's own process: its configuration is replication-manager's state; nothing to materialize (the one proxy outside the rule, and the reason it is not in the not-applicable table of 2.5: there is no file to drift from). |

Step 3 (section 5) carries the rule: the on-premise HAProxy first (the client's case), then
the on-premise ProxySQL and MaxScale file paths, then the switch, the hook, the refusal and
the drift check on every backend. **T13 scenario, on the on-premise Docker framework:** with
`prov-proxy-apply-dynamic-config` off and the hook set to a reference script that regenerates
the proxy configuration from the server list it receives: a switchover moves the writer, the
proxy's configuration on the host changes in the same operation, the proxy is restarted by
hand (the container restarted), the route survives; with the hook unset: the switchover's
route change on that proxy is refused, WARN0328 opens, the proxy keeps the old route and says
so; with the switch on: replication-manager writes the file over SSH and the same restart
keeps the route; a file altered by hand on the host opens WARN0328 at the next tick.
