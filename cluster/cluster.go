// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"hash/crc64"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bluele/logrus_slack"
	vault "github.com/hashicorp/vault/api"

	"github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/cluster/configurator"
	"github.com/signal18/replication-manager/cluster/logplugin"
	"github.com/signal18/replication-manager/cluster/nbc"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/router/maxscale"
	"github.com/signal18/replication-manager/utils/alert/mailer"
	"github.com/signal18/replication-manager/utils/alert/pushover"
	"github.com/signal18/replication-manager/utils/alert/slackman"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/cron"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/s18log"
	sharedlog "github.com/signal18/replication-manager/utils/s18log/shared"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/tty"
	clog "github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
	logsql "github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
)

var clusterError = config.ClusterError

// A Clusters is a collection of Cluster objects
//
// swagger:response clusters
type ClustersResponse struct {
	// Cluster information message
	// in: body
	Body []Cluster
}

// A Cluster has all the information associated with the configured cluster model
// and its servers.
//
// swagger:response cluster
type ClusterResponse struct {
	// Cluster information message
	// in: body
	Body Cluster
}

type Cluster struct {
	OsUser                        *user.User             `json:"-"`
	Name                          string                 `json:"name" groups:"apps,web"`
	Tenant                        string                 `json:"tenant" groups:"web"`
	WorkingDir                    string                 `json:"workingDir" groups:"web"`
	Servers                       serverList             `json:"servers" groups:"apps"`
	LogSlaveServers               []string               `json:"logSlaveServers" groups:"web" ` //To store slave with log-slave-updates
	ServerIdList                  []string               `json:"dbServers" groups:"web"`
	Crashes                       crashList              `json:"dbServersCrashes" groups:"web"` //This will be purged on all db node up
	FailoverHistory               crashList              `json:"failoverHistory" groups:"web"`  //This will be used for PITR
	Apps                          appList                `json:"apps" groups:"apps" `
	AppIdList                     []string               `json:"appServers" groups:"web"`
	Proxies                       proxyList              `json:"proxies" groups:"apps"`
	ProxyIdList                   []string               `json:"proxyServers" groups:"web"`
	FailoverCtr                   int                    `json:"failoverCounter" groups:"web"`
	FailoverTs                    int64                  `json:"failoverLastTime" groups:"web"`
	Status                        string                 `json:"activePassiveStatus" groups:"web"`
	IsSplitBrain                  bool                   `json:"isSplitBrain" groups:"web"`
	IsSplitBrainBck               bool                   `json:"-"`
	IsFailedArbitrator            bool                   `json:"isFailedArbitrator" groups:"web"`
	IsLostMajority                bool                   `json:"isLostMajority" groups:"web"`
	IsDown                        bool                   `json:"isDown" groups:"web"`
	IsClusterDown                 bool                   `json:"isClusterDown" groups:"web"`
	IsMasterDown                  bool                   `json:"isMasterDown" groups:"web"`
	IsAllDbUp                     bool                   `json:"isAllDbUp" groups:"web"`
	IsFailable                    bool                   `json:"isFailable" groups:"web"`
	IsPostgres                    bool                   `json:"isPostgres" groups:"web"`
	IsProvision                   bool                   `json:"isProvision" groups:"web"`
	IsNeedProxiesRestart          bool                   `json:"isNeedProxiesRestart" groups:"web"`
	IsNeedProxiesReprov           bool                   `json:"isNeedProxiesReprov" groups:"web"`
	IsNeedProxiesConfigChange     bool                   `json:"isNeedProxiesConfigChange" groups:"web"`
	IsNeedDatabasesRestart        bool                   `json:"isNeedDatabasesRestart" groups:"web"`
	IsNeedDatabasesRollingRestart bool                   `json:"isNeedDatabasesRollingRestart" groups:"web"`
	IsNeedDatabasesRollingReprov  bool                   `json:"isNeedDatabasesRollingReprov" groups:"web"`
	IsNeedDatabasesReprov         bool                   `json:"isNeedDatabasesReprov" groups:"web"`
	IsNeedDatabasesConfigChange   bool                   `json:"isNeedDatabasesConfigChange" groups:"web"`
	IsNeedAppsReprov              bool                   `json:"isNeedAppsReprov" groups:"web"`
	IsGettingSlowLog              bool                   `json:"isGettingSlowLog" groups:"web"`
	IsValidBackup                 bool                   `json:"isValidBackup" groups:"web"`
	IsNotMonitoring               bool                   `json:"isNotMonitoring" groups:"web"`
	HaveSSHKeyChecked             bool                   `json:"-"`
	IsCapturing                   bool                   `json:"isCapturing" groups:"web"`
	IsGitPull                     bool                   `json:"isGitPull" groups:"web"`
	IsGitPush                     bool                   `json:"isGitPush" groups:"web"`
	IsSavingConfig                bool                   `json:"-"`
	IsNeedGitPush                 bool                   `json:"-"`
	IsExportPush                  bool                   `json:"isExportPush" groups:"web"`
	IsAlertDisable                bool                   `json:"isAlertDisable" groups:"web"`
	IsIntervention                bool                   `json:"isIntervention" groups:"web"`
	InterventionCurrent           *InterventionEntry     `json:"interventionCurrent,omitempty" groups:"web"`
	InterventionHistory           []InterventionEntry    `json:"interventionHistory" groups:"web"`
	InterventionSuppressedAlerts  int                    `json:"interventionSuppressedAlerts" groups:"web"`
	InterventionPending           *InterventionEntry     `json:"interventionPending,omitempty" groups:"web"`
	IsRefreshStaging              bool                   `json:"isRefreshStaging" groups:"web"`
	IsNeedStagingChange           bool                   `json:"isNeedStagingChange" groups:"web"`
	IsConfigPathChange            bool                   `json:"isConfigPathChange" groups:"web"`
	IsResticQueuePaused           bool                   `json:"isResticQueuePaused" groups:"web"`
	BackupSlotsInUse              int                    `json:"backupSlotsInUse" groups:"web"`
	BackupSlotsTotal              int                    `json:"backupSlotsTotal" groups:"web"`
	SchemaMonitorRequested        int32                  `json:"-"`
	Conf                          *config.Config         `json:"config" groups:"apps"`
	Confs                         *config.ConfVersion    `json:"-"`
	CleanAll                      bool                   `json:"cleanReplication" groups:"web"` //used in testing
	Topology                      string                 `json:"topology" groups:"web"`
	Uptime                        string                 `json:"uptime" groups:"web"`
	UptimeFailable                string                 `json:"uptimeFailable" groups:"web"`
	UptimeSemiSync                string                 `json:"uptimeSemisync" groups:"web"`
	MonitorSpin                   string                 `json:"monitorSpin" groups:"web"`
	WorkLoad                      config.WorkLoad        `json:"workLoad" groups:"web"`
	DockerRepos                   []config.DockerRepo    `json:"-"`
	Logrus                        *log.Logger            `json:"-"`
	LogPushover                   *log.Logger            `json:"-"`
	Log                           s18log.HttpLog         `json:"-" groups:"web"`
	LogTask                       s18log.HttpLog         `json:"-" groups:"web"`
	LogSecurity                   s18log.HttpLog         `json:"-" groups:"web"`
	LogWorkload                   s18log.HttpLog         `json:"-" groups:"web"`
	LogDDL                        s18log.HttpLog         `json:"-" groups:"web"`
	LogVariableChange             s18log.HttpLog         `json:"-" groups:"web"`
	LogSlack                      *slackman.SlackManager `json:"-"`
	JobResults                    *config.TasksMap       `json:"jobResults" groups:"web"`
	FalsePositiveChecks           map[string]bool        `json:"falsePositiveChecks" groups:"web"`
	Grants                        map[string]string      `json:"-"`
	Roles                         map[string]string      `json:"-"`
	tlog                          *s18log.TermLog        `json:"-"`
	htlog                         *s18log.HttpLog        `json:"-"`
	SQLGeneralLog                 s18log.HttpLog         `json:"sqlGeneralLog" groups:"web"`
	SQLErrorLog                   s18log.HttpLog         `json:"sqlErrorLog" groups:"web"`
	MonitorType                   map[string]string      `json:"monitorType" groups:"web"`
	TopologyType                  map[string]string      `json:"topologyType" groups:"web"`
	FSType                        map[string]bool        `json:"fsType" groups:"web"`
	DiskType                      map[string]string      `json:"diskType" groups:"web"`
	VMType                        map[string]bool        `json:"vmType" groups:"web"`
	AppS3Providers                []string               `json:"appS3Providers" groups:"web"`
	GatewayConflicts              map[string]string      `json:"gatewayConflicts" groups:"web"`
	// s3Providers groups S3 provider state and locking primitives in one place.
	// Use the accessor methods (Add/Remove/Update/GetS3ProvidersSnapshot) and
	// CRUD transaction lock helpers instead of direct field mutation.
	s3Providers    s3ProviderState
	Agents         []Agent                    `json:"agents" groups:"web"`
	AgentMaxFreq   map[string]int64           `json:"-"`
	hostList       []string                   `json:"-"`
	proxyList      []string                   `json:"-"`
	clusterList    map[string]*Cluster        `json:"-"`
	clusterOrder   []string                   `json:"-"` // ClusterList order; index = ownership priority
	deprecatedKeys map[string]map[string]bool `json:"-"`
	slaves         serverList                 `json:"slaves" groups:"apps"`
	master         *ServerMonitor             `json:"master" groups:"apps"`
	oldMaster      *ServerMonitor             `json:"oldmaster" groups:"web"`
	vmaster        *ServerMonitor             `json:"vmaster" `
	StagingServer  *ServerMonitor             `json:"-" groups:"web"`
	mxs            *maxscale.MaxScale         `json:"-"`
	CheckSumConfig map[string]hash.Hash       `json:"-"`
	//dbUser                        string                      `json:"-"`
	//oldDbUser string `json:"-"`
	//dbPass                        string                      `json:"-"`
	//oldDbPass string `json:"-"`
	//rplUser                   string                      `json:"-"`
	//rplPass                   string                      `json:"-"`
	//proxysqlUser              string                      `json:"-"`
	//proxysqlPass              string                      `json:"-"`
	StateMachine         *state.StateMachine `json:"stateMachine" groups:"web"`
	SecurityStateMachine *state.StateMachine `json:"securityStateMachine" groups:"web"`
	// WorkloadStateMachine tracks workload/performance findings from Graphite spike-detection
	// plugins (WORKLOAD severity). Kept separate from the HA StateMachine so performance
	// noise does not obscure operational cluster health or trigger false failover gates.
	WorkloadStateMachine *state.StateMachine `json:"workloadStateMachine" groups:"web"`
	// SecurityStates is a snapshot of all open security findings from the current monitoring
	// tick, serialised into the cluster JSON for the dashboard (SecurityStateMachine.CurState
	// has json:"-" so it cannot be read directly). Updated by CheckLogPlugins.
	SecurityStates []state.State `json:"securityStates" groups:"web"`
	// SecurityRemediations is the current remediation plan for all open security findings.
	// Populated alongside SecurityStates in CheckLogPlugins so the dashboard gets
	// AutoFixable flags, fix tags, and risk levels without a separate API call.
	// This is the authoritative source — the UI must not duplicate autoFixable logic.
	SecurityRemediations RemediationPlan `json:"securityRemediations" groups:"web"`
	// WorkloadRemediations is the current remediation plan for actionable workload advisories.
	// Populated alongside WorkloadStates in CheckLogPlugins.
	WorkloadRemediations RemediationPlan `json:"workloadRemediations" groups:"web"`
	// WorkloadStates is a snapshot of all open workload findings from the current monitoring
	// tick. Updated by CheckLogPlugins after all servers have been evaluated.
	WorkloadStates []state.State `json:"workloadStates" groups:"web"`
	SecurityScore  SecurityScore `json:"securityScore" groups:"web"`
	// Set by dbjob SSH scripts scanning my.cnf/.my.cnf on DB servers
	SecurityClearPwdConfig bool `json:"securityClearPwdConfig"`
	// Set by dbjob SSH scripts scanning .bash_history/.mysql_history on DB servers
	SecurityClearPwdHistory bool `json:"securityClearPwdHistory"`
	// SecurityLogrus is a dedicated logrus.Logger that writes to security.log.
	// Set by the server on cluster init. Nil when no log-file is configured.
	SecurityLogrus *log.Logger `json:"-"`
	// WorkloadLogrus is a dedicated logrus.Logger that writes to workload.log.
	// Set by the server on cluster init. Nil when no log-file is configured.
	WorkloadLogrus *log.Logger `json:"-"`
	// MaintenanceLogrus is a dedicated logrus.Logger that writes to maintenance.log.
	// Receives ConstLogModMaintenance events: backup, SST, task execution, purge, etc.
	// Set by the server on cluster init. Nil when no log-file is configured.
	MaintenanceLogrus                   *log.Logger                 `json:"-"`
	runOnceAfterTopology                bool                        `json:"-"`
	logPtr                              *os.File                    `json:"-"`
	termlength                          int                         `json:"-"`
	runUUID                             string                      `json:"-"`
	cfgGroupDisplay                     string                      `json:"-"`
	RepMgrVersion                       string                      `json:"-"`
	RepMgrHostname                      string                      `json:"-"`
	exitMsg                             string                      `json:"-"`
	exit                                atomic.Bool                 `json:"-"`
	stopOnce                            sync.Once                   `json:"-"`
	canFlashBack                        bool                        `json:"-"`
	canResticFetchRepo                  bool                        `json:"-"`
	failoverCond                        *nbc.NonBlockingChan        `json:"-"`
	switchoverCond                      *nbc.NonBlockingChan        `json:"-"`
	rejoinCond                          *nbc.NonBlockingChan        `json:"-"`
	bootstrapCond                       *nbc.NonBlockingChan        `json:"-"`
	altertableCond                      *nbc.NonBlockingChan        `json:"-"`
	addtableCond                        *nbc.NonBlockingChan        `json:"-"`
	statecloseChan                      chan state.State            `json:"-"`
	switchoverChan                      chan bool                   `json:"-"`
	errorChan                           chan error                  `json:"-"`
	testStopCluster                     bool                        `json:"-"`
	testStartCluster                    bool                        `json:"-"`
	lastmaster                          *ServerMonitor              `json:"-"`
	benchmarkType                       string                      `json:"-"`
	HaveDBTLSCert                       bool                        `json:"haveDBTLSCert" groups:"web"`
	HaveDBTLSOldCert                    bool                        `json:"haveDBTLSOldCert" groups:"web"`
	HaveAutoTLS                         bool                        `json:"haveAutoTLS" groups:"web"` // set when any server auto-detected require_secure_transport=ON via error 3159
	tlsconf                             *tls.Config                 `json:"-"`
	tlsoldconf                          *tls.Config                 `json:"-"`
	tunnel                              *ssh.Client                 `json:"-"`
	QueryRules                          map[uint32]config.QueryRule `json:"-"`
	BackupMetaMap                       *backupmgr.BackupMetaMap    `json:"backupList" groups:"web"`
	snapshotMetadata                    *snapshotMetadataManager    `json:"-"`
	reconcileSnapshotMetadataInProgress int32                       `json:"-"`
	SLAHistory                          []state.Sla                 `json:"slaHistory" groups:"web"`
	APIUsers                            map[string]APIUser          `json:"apiUsers" groups:"web"`
	Schedule                            map[string]cron.Entry       `json:"-"`
	scheduler                           *cron.Cron                  `json:"-"`
	debugLineMap                        map[string]int              `json:"-"`
	WaitingRejoin                       int                         `json:"waitingRejoin" groups:"web"`
	WaitingSwitchover                   int                         `json:"waitingSwitchover" groups:"web"`
	WaitingFailover                     int                         `json:"waitingFailover" groups:"web"`
	Configurator                        configurator.Configurator   `json:"configurator" groups:"web"`
	DiffVariables                       []VariableDiff              `json:"diffVariables" groups:"web"`
	inInitNodes                         bool                        `json:"-"`
	inOptimizeTables                    bool                        `json:"inOptimizeTables" groups:"web"`
	inAnalyzeTables                     bool                        `json:"inAnalyzeTables" groups:"web"`
	inConnectVault                      bool                        `json:"-"`
	CanInitNodes                        bool                        `json:"canInitNodes" groups:"web"`
	errorInitNodes                      error                       `json:"-"`
	CanConnectVault                     bool                        `json:"canConnectVault"`
	errorConnectVault                   error                       `json:"-"`
	SqlErrorLog                         *logsql.Logger              `json:"-"`
	SqlGeneralLog                       *logsql.Logger              `json:"-"`
	SstAvailablePorts                   map[string]string           `json:"sstAvailablePorts" groups:"web"`
	InPhysicalBackup                    bool                        `json:"inPhysicalBackup" groups:"web"`
	InLogicalBackup                     bool                        `json:"inLogicalBackup" groups:"web"`
	InBinlogBackup                      bool                        `json:"inBinlogBackup" groups:"web"`
	InResticLogicalBackup               bool                        `json:"inResticLogicalBackup" groups:"web"`
	InResticPhysicalBackup              bool                        `json:"inResticPhysicalBackup" groups:"web"`
	InResticBackup                      bool                        `json:"inResticBackup" groups:"web"`
	InRollingRestart                    bool                        `json:"inRollingRestart" groups:"web"`
	failLoadP12Cert                     bool                        `json:"-"`
	Mailer                              *mailer.Mailer              `json:"-"`
	ResticManager                       *backupmgr.ResticManager    `json:"-"`
	resolvedS3Mode                      string                      // probe-resolved S3 mode during auto startup; "" if not yet probed
	MessageChan                         chan sharedlog.Message      `json:"-"`
	ErrorConfigs                        config.ErrorConfigs         `json:"-"` //To store error config
	Partner                             *config.Partner             `json:"partner" groups:"web"`
	ServerGlobals                       *ServerGlobals              `json:"-"`
	ConfigManager                       *manager.ConfigManager      `json:"-"`
	failSendCount                       int                         `json:"-"`
	MeetUserID                          string                      `json:"-"` //To store meet user id
	ServiceTemplates                    []string                    `json:"-"` //To store application templates
	DiskStatManager                     *misc.DiskStatManager       `json:"diskStat" groups:"web"`
	RefreshTemplateMD5Chan              chan *App                   `json:"-"`
	LastDelayStatPrint                  time.Time
	sync.Mutex
	crcTable               *crc64.Table
	SlavesOldestMasterFile SlavesOldestMasterFile
	SlavesConnected        int
	clog                   *clog.Logger `json:"-"`
	*ClusterGraphite
	VersionsMap            *config.VersionsMap
	SessionManager         *tty.SessionManager `json:"-"`
	SysBenchTpcMResults    []SysBenchTpcResultPerMinute
	SysbenchHistory        SysbenchLog        `json:"sysbenchHistory"  groups:"web"`
	OpenSVCStats           atomic.Value       `json:"-"`
	OrchestratorVersion    string             `json:"-"`
	orchestratorVersionMu  sync.RWMutex       `json:"-"`
	lastOrchestratorProbe  time.Time          `json:"-"`
	opensvcPoolInfoCache   []opensvc.PoolInfo `json:"-"`
	opensvcPoolInfoCacheAt time.Time          `json:"-"`
	opensvcPoolInfoCacheMu sync.RWMutex       `json:"-"`
	// Per-cluster preserved variables (replaces ProvDBConfigPreserveVars mechanism)
	preservedVars               map[string]string          `json:"-"`
	preservedVarsExcludeServers map[string]map[string]bool `json:"-"` // varName -> {serverID -> true}
	preservedVarsLoaded         bool                       `json:"-"`
	preservedVarsMutex          sync.RWMutex               `json:"-"`
	s3SyncApplyMu               sync.Mutex                 `json:"-"`
	appListEpoch                uint64                     `json:"-"`
	secretVersionStoreMu        sync.Mutex                 `json:"-"`
	secretVersionStoreDirty     bool                       `json:"-"`
	// pluginSpikeCache holds the last DetectSpike result per server+plugin pair.
	// Keyed as "serverURL:pluginName". Prevents graphite HTTP on every tick.
	pluginSpikeCache map[string]*logplugin.SpikeCache `json:"-"`
	// wtagCounters accumulates WTAG finding Count/Total across all servers
	// during CheckLogPlugins. Reset each tick, used for cluster-level percentage.
	wtagCounters map[string][2]int64 `json:"-"`
	// pluginRegistry is a per-cluster plugin registry seeded with built-in plugins
	// and extended with external plugins loaded from cluster.WorkingDir/plugins/.
	// Kept separate from GlobalRegistry so that one cluster's external plugins do
	// not pollute the monitoring loop of other clusters.
	pluginRegistry *logplugin.Registry `json:"-"`
	// pluginRejections tracks external plugins rejected on the last reload
	// (binary name -> reason) so CheckPluginRejectionStates can assert one
	// WARN0206 state per plugin type on every monitoring tick.
	pluginRejections    map[string]string `json:"-"`
	pluginPubKeyMissing string            `json:"-"`
	pluginStateLock     sync.Mutex        `json:"-"`
	// variableDiffSignature is the identity of the last logged variable diff
	// set so MonitorVariablesDiff only logs/persists on real changes.
	variableDiffSignature string `json:"-"`
	initiated             bool
}

// PluginRegistry returns the per-cluster plugin registry, which contains both
// built-in plugins and any external plugins loaded from the cluster's plugin dir.
func (cluster *Cluster) PluginRegistry() *logplugin.Registry {
	return cluster.pluginRegistry
}

type SlavesOldestMasterFile struct {
	Prefix          string
	Suffix          int
	OldestTimestamp time.Time
	sync.Mutex
}

type ClusterSorter []*Cluster

func (a ClusterSorter) Len() int           { return len(a) }
func (a ClusterSorter) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ClusterSorter) Less(i, j int) bool { return a[i].Name < a[j].Name }

type QueryRuleSorter []config.QueryRule

func (a QueryRuleSorter) Len() int           { return len(a) }
func (a QueryRuleSorter) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a QueryRuleSorter) Less(i, j int) bool { return a[i].Id < a[j].Id }

type FullProcessListSorterByQueryTime []dbhelper.Processlist

func (a FullProcessListSorterByQueryTime) Len() int      { return len(a) }
func (a FullProcessListSorterByQueryTime) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a FullProcessListSorterByQueryTime) Less(i, j int) bool {
	return a[i].Time.Float64 > a[j].Time.Float64
}

type FullProcessListSorterByTrxTime []dbhelper.Processlist

func (a FullProcessListSorterByTrxTime) Len() int      { return len(a) }
func (a FullProcessListSorterByTrxTime) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a FullProcessListSorterByTrxTime) Less(i, j int) bool {
	if a[i].TrxTime == a[j].TrxTime {
		return a[i].Time.Float64 > a[j].Time.Float64
	} else {
		return a[i].TrxTime > a[j].TrxTime
	}
}

// The Agent describes the server where the cluster runs on.
// swagger:response agent
type Agent struct {
	Id           string `json:"id"`
	HostName     string `json:"hostName"`
	CpuCores     int64  `json:"cpuCores"`
	CpuFreq      int64  `json:"cpuFreq"`
	MemBytes     int64  `json:"memBytes"`
	MemFreeBytes int64  `json:"memFreeBytes"`
	OsKernel     string `json:"osKernel"`
	OsName       string `json:"osName"`
	Status       string `json:"status"`
	Version      string `json:"version"`
}

type Alerts struct {
	Errors   []state.StateHttp `json:"errors"`
	Warnings []state.StateHttp `json:"warnings"`
}

type Diff struct {
	Server        string `json:"serverName"`
	Role          string `json:"role"`
	VariableValue string `json:"variableValue"`
}

type VariableDiff struct {
	VariableName string `json:"variableName"`
	DiffValues   []Diff `json:"diffValues"`
}

const (
	ConstJobCreateFile string = "JOB_O_CREATE_FILE"
	ConstJobAppendFile string = "JOB_O_APPEND_FILE"
)
const (
	ConstMonitorActif   string = "A"
	ConstMonitorStandby string = "S"
)

const (
	VaultConfigStoreV2 string = "config_store_v2"
	VaultDbEngine      string = "database_engine"
)

type ClusterForm struct {
	ClusterName  string `json:"clusterName"`
	Orchestrator string `json:"orchestrator"`
	Plan         string `json:"plan"`
}

// Init initial cluster definition
func (cluster *Cluster) Init(confs *config.ConfVersion, cfgGroup string, tlog *s18log.TermLog, loghttp *s18log.HttpLog, termlength int, runUUID string, RepMgrVersion string, RepMgrHostname string) error {
	cluster.Conf = new(config.Config)
	cluster.Confs = confs
	cluster.debugLineMap = make(map[string]int)
	cluster.AgentMaxFreq = make(map[string]int64)
	cluster.ServiceTemplates = make([]string, 0)
	cluster.OpenSVCStats.Store([]opensvc.DaemonNodeStats{})
	cluster.MessageChan = make(chan sharedlog.Message, 10)

	go cluster.ConsumeMessageChan()

	*cluster.Conf = confs.ConfInit
	cluster.Conf.NormalizeBackupArchiveMode()

	cluster.tlog = tlog
	cluster.htlog = loghttp
	cluster.termlength = termlength
	cluster.Name = cfgGroup

	cluster.runUUID = runUUID
	cluster.RepMgrHostname = RepMgrHostname
	cluster.RepMgrVersion = RepMgrVersion

	cluster.InitFromConf()
	cluster.NewClusterGraphite()
	cluster.initiated = true
	return nil
}

func (cluster *Cluster) InitFromConf() {
	defer cluster.LogPanicToFile("cluster")

	cluster.SqlErrorLog = logsql.New()
	cluster.SqlGeneralLog = logsql.New()
	cluster.crcTable = crc64.MakeTable(crc64.ECMA) // http://golang.org/pkg/hash/crc64/#pkg-constants
	cluster.switchoverChan = make(chan bool)
	// should use buffered channels or it will block
	cluster.statecloseChan = make(chan state.State, 100)
	cluster.errorChan = make(chan error)
	cluster.snapshotMetadata = newSnapshotMetadataManager(cluster)
	cluster.configureSnapshotMetadataExtractorConcurrency(cluster.Conf.BackupResticMetadataExtractorConcurrency)
	cluster.failoverCond = nbc.New()
	cluster.switchoverCond = nbc.New()
	cluster.rejoinCond = nbc.New()
	cluster.addtableCond = nbc.New()
	cluster.altertableCond = nbc.New()
	cluster.canFlashBack = true
	cluster.CanInitNodes = true
	cluster.canResticFetchRepo = true
	cluster.CanConnectVault = true
	cluster.runOnceAfterTopology = true
	cluster.testStopCluster = true
	cluster.testStartCluster = true
	cluster.BackupMetaMap = backupmgr.NewBackupMetaMap()
	cluster.VersionsMap = config.NewVersionsMap()

	cluster.WorkingDir = cluster.Conf.WorkingDir + "/" + cluster.Name
	cluster.InterventionHistory = make([]InterventionEntry, 0)
	cluster.LoadInterventionHistory()
	cluster.LoadSysbenchLog()
	cluster.pluginSpikeCache = make(map[string]*logplugin.SpikeCache)
	cluster.pluginRegistry = logplugin.NewRegistry()
	if cluster.Conf.Arbitration {
		cluster.Status = ConstMonitorStandby
	} else {
		cluster.Status = ConstMonitorActif
	}
	cluster.benchmarkType = "sysbench"
	cluster.Log = s18log.NewHttpLog(200)
	cluster.LogTask = s18log.NewHttpLog(200)
	cluster.LogSecurity = s18log.NewHttpLog(200)
	cluster.LogWorkload = s18log.NewHttpLog(200)
	cluster.LogDDL = s18log.NewHttpLog(200)
	cluster.LogVariableChange = s18log.NewHttpLog(200)

	cluster.MonitorType = config.GetMonitorType()
	cluster.TopologyType = config.GetTopologyType()
	cluster.FSType = config.GetFSType()
	cluster.DiskType = config.GetDiskType()
	cluster.VMType = config.GetVMType()
	cluster.Grants = config.GetGrantType()
	cluster.Roles = config.GetRoleType()

	cluster.QueryRules = make(map[uint32]config.QueryRule)
	cluster.Schedule = make(map[string]cron.Entry)
	cluster.JobResults = config.NewTasksMap()
	cluster.SstAvailablePorts = make(map[string]string)
	cluster.CheckSumConfig = make(map[string]hash.Hash)
	cluster.FalsePositiveChecks = make(map[string]bool)
	lstPort := strings.Split(cluster.Conf.SchedulerSenderPorts, ",")
	for _, p := range lstPort {
		cluster.SstAvailablePorts[p] = p
	}

	// Initialize the state machine at this stage where everything is fine.
	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()
	cluster.SecurityStateMachine = new(state.StateMachine)
	cluster.SecurityStateMachine.Init()
	// Prime the heartbeat counter so AddState does not pre-populate OldState on
	// the first call.  The main StateMachine gets heartbeats>0 naturally via
	// SetMasterUpAndSync, but Security/Workload SMs never have SLA methods called
	// on them — leaving heartbeats==0 forever causes GetLastOpenedStates() to
	// always return empty, silencing security.log and workload.log entirely.
	cluster.SecurityStateMachine.SetMasterUpAndSync(false, false, false)
	cluster.WorkloadStateMachine = new(state.StateMachine)
	cluster.WorkloadStateMachine.Init()
	cluster.WorkloadStateMachine.SetMasterUpAndSync(false, false, false)
	// k, _ := cluster.Conf.LoadEncrytionKey()
	// if k == nil {
	// 	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "No existing password encryption key")
	// 	cluster.SetState("ERR00090", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["ERR00090"]), ErrFrom: "CLUSTER"})
	// }

	if cluster.Conf.Interactive {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Failover in interactive mode")
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Failover in automatic mode")
	}

	//working directory of the cluster is working directory of server and cluster name
	if _, err := os.Stat(cluster.WorkingDir); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Creating directory  %s", cluster.WorkingDir)
		os.MkdirAll(cluster.WorkingDir, os.ModePerm)
	}
	cluster.initSnapshotMetadataPersistence()
	if err := cluster.LoadS3Providers(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"LoadS3Providers: %v", err)
	}

	cluster.SetClusterCredentialsFromConfig()
	cluster.LoadAPIUsers()
	cluster.SaveAcls()
	cluster.InitMailer()
	cluster.GetPersistentState()

	cluster.LogPushover = log.New()
	cluster.LogPushover.SetFormatter(&log.TextFormatter{FullTimestamp: true})

	if cluster.Conf.PushoverAppToken != "" && cluster.Conf.PushoverUserToken != "" {
		cluster.LogPushover.AddHook(
			pushover.NewHook(cluster.Conf.GetDecryptedValue("alert-pushover-app-token"), cluster.Conf.GetDecryptedValue("alert-pushover-user-token")),
		)
		cluster.LogPushover.SetLevel(log.WarnLevel)
	}

	cluster.LogSlack = slackman.NewSlackManager()
	cluster.LogSlack.SetFormatter(&log.TextFormatter{FullTimestamp: true})
	// Don't write to stdout
	cluster.LogSlack.SetOutput(io.Discard)

	cluster.LogSlack.SetHookConfig("slack", slackman.SlackConfig{
		URL:            cluster.Conf.SlackURL,
		AcceptedLevels: logrus_slack.LevelThreshold(log.InfoLevel), // Send Error, warning and info level (resolved) to slack
		Channel:        cluster.Conf.SlackChannel,
		User:           cluster.Conf.SlackUser,
		Icon:           ":ghost:",
		Timeout:        5 * time.Second,
	})

	cloud18fields := make(map[string]interface{})
	if cluster.Conf.Cloud18Alert {
		cloud18fields["cloud18"] = cluster.Conf.Cloud18Domain + "/" + cluster.Conf.Cloud18SubDomain + "-" + cluster.Conf.Cloud18SubDomainZone
		cloud18fields["client"] = cluster.Conf.Cloud18GitUser
	}

	cluster.LogSlack.SetHookConfig("cloud18", slackman.SlackConfig{
		URL:            cluster.Conf.Cloud18AlertSlackURL,
		AcceptedLevels: logrus_slack.LevelThreshold(log.InfoLevel), // Only send Error level to alert channel
		Channel:        cluster.Conf.Cloud18AlertSlackChannel,
		User:           cluster.Conf.Cloud18AlertSlackUser,
		Icon:           ":ghost:",
		Timeout:        5 * time.Second,
	})

	if cluster.Conf.SlackURL != "" {
		cluster.LogSlack.Activate("slack", true)
	}

	if cluster.Conf.Cloud18 && cluster.Conf.Cloud18Alert && cluster.Conf.Cloud18SubscriptionPlan != "free" {
		cluster.LogSlack.Activate("cloud18", true)
	}

	startMsg := "Replication manager cluster started with version: %s"
	if cluster.initiated {
		startMsg = "Replication manager cluster config reloaded with version: %s"
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "START", startMsg, cluster.Conf.Version)

	hookerr, err := s18log.NewRotateFileHook(s18log.RotateFileConfig{
		Filename:   cluster.WorkingDir + "/sql_error.log",
		MaxSize:    cluster.Conf.LogRotateMaxSize,
		MaxBackups: cluster.Conf.LogRotateMaxBackup,
		MaxAge:     cluster.Conf.LogRotateMaxAge,
		Level:      logsql.DebugLevel,
		Formatter: &logsql.TextFormatter{
			DisableColors:   true,
			TimestampFormat: "2006-01-02 15:04:05",
			FullTimestamp:   true,
		},
	})
	if err != nil {
		cluster.SqlErrorLog.WithError(err).Error("Can't init error sql log file")
	}
	cluster.SqlErrorLog.AddHook(hookerr)

	hookgen, err := s18log.NewRotateFileHook(s18log.RotateFileConfig{
		Filename:   cluster.WorkingDir + "/sql_general.log",
		MaxSize:    cluster.Conf.LogRotateMaxSize,
		MaxBackups: cluster.Conf.LogRotateMaxBackup,
		MaxAge:     cluster.Conf.LogRotateMaxAge,
		Level:      logsql.DebugLevel,
		Formatter: &logsql.TextFormatter{
			DisableColors:   true,
			TimestampFormat: "2006-01-02 15:04:05",
			FullTimestamp:   true,
		},
	})
	if err != nil {
		cluster.SqlGeneralLog.WithError(err).Error("Can't init general sql log file")
	}
	cluster.SqlGeneralLog.AddHook(hookgen)

	if loadErr := cluster.LoadAppConfigs(); loadErr != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr,
			"Startup app config load failed (some app configs may not have loaded): %v", loadErr)
	}

	// Configurator generates base configuration from tags, which is overridden by:
	// 1. Cluster-wide preserved variables
	// 2. Server-specific preserved variables

	// Initialize preserved variables before server initialization
	// This replaces the old ProvDBConfigPreserveVars mechanism
	if err := cluster.initPreservedVars(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"Failed to pre-initialize preserved variables: %v (will retry on demand)", err)
	}

	err = cluster.newServerList()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not set server list %s", err)
	}
	cluster.ClearOldCookies()

	err = cluster.newProxyList()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not set proxy list %s", err)
	}
	persistRebasedAppCreditCap := false
	err = cluster.newAppList()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not set app list %s", err)
	} else if cluster.rebaseAppCreditCap() {
		persistRebasedAppCreditCap = true
	}
	//Loading configuration compliances
	err = cluster.Configurator.Init(*cluster.Conf, cluster.Logrus)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not initialize configurator %s", err)
		log.Fatal("missing important file, giving up")
	}

	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorLocalhost:
		cluster.DropDBTagConfig("docker")
		cluster.DropDBTagConfig("threadpool")
		cluster.AddDBTagConfig("pkg")
	}
	//fmt.Printf("INIT CLUSTER CONF :\n")
	//cluster.Conf.PrintConf()
	cluster.initScheduler()
	cluster.CheckDefaultUser(true)
	cluster.RefreshToolVersions()
	cluster.initResticLocalDir()
	cluster.StartResticManager()
	if persistRebasedAppCreditCap {
		if _, saveErr := cluster.SaveConfigFile(); saveErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
				"Failed to reconcile credit cap at startup: %s", saveErr)
		}
	}

	cluster.Conf.TopologyTarget = cluster.GetTopologyFromConf()
}

func (cluster *Cluster) initOrchetratorNodes() {
	if cluster.inInitNodes {
		return
	}
	cluster.inInitNodes = true

	//defer cluster.insideInitNodes = false
	//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Loading nodes from orchestrator %s", cluster.Conf.ProvOrchestrator)
	switch cluster.GetOrchestrator() {
	case config.ConstOrchestratorOpenSVC:
		cluster.Agents, cluster.errorInitNodes = cluster.OpenSVCGetNodes()
	case config.ConstOrchestratorKubernetes:
		cluster.Agents, cluster.errorInitNodes = cluster.K8SGetNodes()
	case config.ConstOrchestratorSlapOS:
		cluster.Agents, cluster.errorInitNodes = cluster.SlapOSGetNodes()
	case config.ConstOrchestratorLocalhost:
		cluster.Agents, cluster.errorInitNodes = cluster.LocalhostGetNodes()
	case config.ConstOrchestratorOnPremise:
		cluster.Agents, cluster.errorInitNodes = cluster.OnPremiseGetNodes()
	default:
		log.Fatalln("prov-orchestrator not supported", cluster.Conf.ProvOrchestrator)
	}

	cluster.SetAgentsCpuCoreMem()
	cluster.SetAgentsMaxCpuFreq()
	cluster.inInitNodes = false

}

func (cluster *Cluster) initScheduler() {
	if cluster.Conf.MonitorScheduler {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Initializing cluster scheduler")
		if cluster.scheduler != nil {
			cluster.scheduler.Stop()
		}
		cluster.scheduler = cron.New()
		cluster.SetSchedulerBackupLogical()
		cluster.SetSchedulerLogsTableRotate()
		cluster.SetSchedulerBackupPhysical()
		cluster.SetSchedulerBackupLogs()
		cluster.SetSchedulerOptimize()
		cluster.SetSchedulerAnalyze()
		cluster.SetSchedulerRollingRestart()
		cluster.SetSchedulerRollingReprov()
		cluster.SetSchedulerSlaRotate()
		cluster.SetSchedulerDbJobsSsh()
		cluster.SetSchedulerAlertDisable()
		cluster.SetSchedulerMonitorSchema()
		cluster.SetSchedulerMonitorChecksum()
		if cluster.Status == ConstMonitorActif {
			cluster.scheduler.Start()
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Scheduler initialized but not started, monitor is in standby mode, will start on arbitration win, disable individual jobs as needed")
		}
	}

}

var pstates30 = []string{
	"WARN0084",             // Variables diff
	"ERR00090", "WARN0102", // Config related
	"WARN0093", "WARN0134", "WARN0145", // Restic related
	"WARN0101", "WARN0111", "WARN0112", // Backup related
	"WARN0141", "WARN0142", "WARN0143", "WARN0150", "WARN0151", // Tresholds
	"WARN0153",             // Job related
	"WARN0158",             // Job secrets mismatch
	"WARN0159", "WARN0160", // Deprecated config keys
	"CREDIT01", // Credit related
}

var pstates3600 = []string{
	"WARN0094",             // Restic
	"WARN0132", "WARN0137", // App templates
	"WARN0117", "WARN0118", "WARN0119", "WARN0120", "WARN0121", "WARN0156", "WARN0157", "WARN0167", "WARN0168", // Tools versions + compliance
}

func (cluster *Cluster) Run() {
	defer cluster.LogPanicToFile("cluster")
	interval := time.Second

	// createKeys do nothing yet
	if _, err := os.Stat(cluster.Conf.WorkingDir + "/" + cluster.Name + "/ca-key.pem"); os.IsNotExist(err) {
		os.MkdirAll(cluster.Conf.WorkingDir+"/"+cluster.Name, os.ModePerm)
		go cluster.createKeys()
	}

	cluster.Lock()
	cluster.Topology = config.TopoUnknown
	cluster.Unlock()
	cluster.MarkSecretVersionStoreDirty()

	for !cluster.exit.Load() {
		if !cluster.Conf.MonitorPause {
			cluster.ReconcileSecretVersionStore()
			cluster.ServerIdList = cluster.GetDBServerIdList()
			cluster.ProxyIdList = cluster.GetProxyServerIdList()
			cluster.AppIdList = cluster.GetAppServerIdList()
			if cluster.ResticManager != nil {
				cluster.IsResticQueuePaused = cluster.ResticManager.IsPaused()
			}
			if cluster.ServerGlobals != nil && cluster.ServerGlobals.BackupSemaphore != nil {
				cluster.BackupSlotsInUse = len(cluster.ServerGlobals.BackupSemaphore)
				cluster.BackupSlotsTotal = cap(cluster.ServerGlobals.BackupSemaphore)
			}
			go cluster.CheckDefaultUser(false)

			if cluster.HasBadConfigMeasurement() {
				cluster.SetState("WARN0135", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0135"], cluster.ErrorConfigs), ErrFrom: "CONFIG"})
			}

			select {
			case sig := <-cluster.switchoverChan:
				if sig {
					if cluster.Status == "A" {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Signaling Switchover...")
						cluster.MasterFailover(false)
						cluster.switchoverCond.Send <- true
					} else {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Not in active mode, cancel switchover %s", cluster.Status)
					}
				}

			default:
				if cluster.Conf.LogLevel > 2 {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Monitoring server loop")
					if len(cluster.Servers) > 0 {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Servers not nil : %v\n", cluster.Servers)
						for k, v := range cluster.Servers {
							cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Servers loops k : %d, url : %s, state : %s, prevstate %s", k, v.URL, v.State, v.PrevState)
							cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Server [%d]: URL: %-15s State: %6s PrevState: %6s", k, v.URL, v.State, v.PrevState)
						}
						if m := cluster.GetMaster(); m != nil {
							cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Master [ ]: URL: %-15s State: %6s PrevState: %6s", m.URL, m.State, m.PrevState)
							for k, v := range cluster.slaves {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Slave  [%d]: URL: %-15s State: %6s PrevState: %6s", k, v.URL, v.State, v.PrevState)
							}
						}
					}
				}
				// Phase 1: topology discovery must complete before anything reads Servers
				wg := new(sync.WaitGroup)
				wg.Add(1)
				go cluster.TopologyDiscover(wg)
				wg.Wait()

				cluster.CheckInterventionSchedule()

				if cluster.runOnceAfterTopology {
					if !cluster.IsInFailover() {
						cluster.initProxies()
					}
					go cluster.initOrchetratorNodes()
					go cluster.ResticFetchRepo()
					cluster.SetRollingJobsUpgradeState()
					cluster.CleanupRestartCookies()
					cluster.ReloadLogPlugins()
					if master := cluster.GetMaster(); master != nil {
						cachedTables := master.DictTables.ToNewMap()
						if len(cachedTables) == 0 {
							go cluster.MonitorSchema()
						} else {
							var tottablesize, totindexsize int64
							for _, t := range cachedTables {
								tottablesize += t.DataLength
								totindexsize += t.IndexLength
							}
							cluster.WorkLoad.DBTableSize = tottablesize
							cluster.WorkLoad.DBIndexSize = totindexsize
						}
					}
					cluster.runOnceAfterTopology = false
				} else {

					if !cluster.IsInFailover() {
						goRun := func(fn func()) {
							wg.Add(1)
							go func() {
								defer wg.Done()
								fn()
							}()
						}
						heartbeats := cluster.StateMachine.GetHeartbeats()

						// Fire-and-forget: long-running, no synchronization needed
						if cluster.Conf.MdbsProxyOn {
							go cluster.MonitorSchema()
						}
						if heartbeats%30 == 0 {
							go cluster.initOrchetratorNodes()
							go cluster.CheckCredentialRotation()
						}
						if heartbeats%3600 == 0 {
							go cluster.RefreshAllAppTemplateMD5()
						}
						if cluster.Conf.BackupReconcileInterval > 0 && heartbeats%int64(cluster.Conf.BackupReconcileInterval) == 0 {
							cluster.ReconcileSnapshotMetadataAsync()
						}

						// Phase 2: parallel reads of stable topology — Phase 3 depends on these
						goRun(cluster.ArbitratorHandler)
						wg.Add(2)
						go cluster.refreshProxies(wg)
						go cluster.refreshApps(wg)
						if heartbeats%10 == 0 {
							goRun(cluster.MonitorTableSchemaDiff)
						}
						if heartbeats%30 == 0 {
							goRun(cluster.ResticFetchRepo)
							goRun(cluster.MonitorVariablesDiff)
							goRun(cluster.MonitorVariablesChange)
						}
						wg.Wait()

						// Phase 3: parallel — depends on Phase 2, independent of each other
						if heartbeats%30 == 0 {
							goRun(cluster.MonitorQueryRules)
						}
						if cluster.Conf.TestInjectTraffic || cluster.Conf.TestInjectTrafficStaging || cluster.Conf.AutorejoinSlavePositionalHeartbeat || cluster.Conf.MonitorWriteHeartbeat {
							goRun(cluster.InjectProxiesTraffic)
						}
						if heartbeats%3600 == 0 {
							goRun(func() { cluster.ResticPurgeRepo(false) })
							goRun(cluster.RefreshToolVersions)
							goRun(cluster.CheckBackupToolVersions)
							goRun(cluster.CheckComplianceUpdate)
							goRun(cluster.ReloadDockerRepos)
						}
						goRun(cluster.CheckAppsCredit)
						goRun(cluster.CheckWaitRunJobSSH)
						goRun(cluster.CheckDummyConfigSendCookies)
						goRun(cluster.CheckRestartContainerCookies)
						goRun(cluster.PrintDelayStat)
						if cluster.SlavesOldestMasterFile.Suffix == 0 {
							goRun(cluster.CheckSlavesReplicationsPurge)
						}
						if heartbeats%10 == 0 {
							goRun(cluster.CheckJobsVersion)
						}
						if heartbeats%30 == 0 {
							goRun(func() { cluster.IsValidBackup = cluster.HasValidBackup() })
							goRun(cluster.CheckCanSaveDynamicConfig)
							goRun(cluster.CheckIsOverwrite)
							goRun(cluster.CheckAllBackupEstimatedSize)
							goRun(cluster.CheckAvailableCredit)
							goRun(cluster.CheckOpenSVCTresholds)
							goRun(cluster.JobsCheckSchedulerTable)
							goRun(cluster.CheckOnPremiseSSHKey)
							goRun(cluster.CheckConfiguratorPrerequisites)
							goRun(cluster.CheckGlobalDeprecatedKeys)
							goRun(cluster.CheckClusterDeprecatedKeys)
							goRun(cluster.CheckClusterServiceAgents)
						}
						if cluster.Conf.GraphiteMetrics && heartbeats%5 == 0 {
							goRun(func() { cluster.SendGraphiteMetrics() })
							goRun(cluster.CheckDisksUsage)
						}
						wg.Wait()

						// PreserveState for non-running ticks (fast, no I/O)
						if heartbeats%10 != 0 {
							cluster.StateMachine.PreserveState("WARN0147", "WARN0164")
						}
						if heartbeats%30 != 0 {
							cluster.StateMachine.PreserveState(pstates30...)
						}
						if heartbeats%3600 != 0 {
							cluster.StateMachine.PreserveState(pstates3600...)
						}
						if !(cluster.Conf.GraphiteMetrics && heartbeats%5 == 0) {
							cluster.StateMachine.PreserveState("WARN0139", "WARN0140")
						}
						if !cluster.CanInitNodes {
							cluster.SetState("ERR00082", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00082"], cluster.errorInitNodes), ErrFrom: "OPENSVC"})
						}
						if !cluster.CanConnectVault {
							cluster.SetState("ERR00089", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00089"], cluster.errorConnectVault), ErrFrom: "OPENSVC"})
						}
					} else {
						cluster.StateMachine.PreserveState("ERR00100")
					}
				}

				cluster.EmitAppErrors()

				if cluster.HasDiscoverTopologyMismatchTarget() {
					cluster.SetState("ERR00092", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00092"], cluster.Name, cluster.Topology, cluster.Conf.TopologyTarget), ErrFrom: "TOPO"})
				}

				// AddChildServers can't be done before TopologyDiscover but need a refresh aquiring more fresh gtid vs current cluster so election win but server is ignored see electFailoverCandidate
				err := cluster.AddChildServers()

				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Fail of AddChildServers %s", err)
				}

				cluster.IsFailable = cluster.GetStatus()
				cluster.IsMasterDown = cluster.GetMaster() == nil || cluster.GetMaster().IsFailed()
				cluster.CheckDBCredentials()
				// Run generic log-tailer plugin checks (errorlog / sqlerrorlog / slowlog 24h)
				cluster.CheckLogPlugins()
				// CheckFailed trigger failover code if passing all false positiv and constraints
				cluster.CheckFailed()
				cluster.IsConfigPathChange = cluster.HasConfigPathChanged()
				cluster.SetStatus()
				cluster.CheckBackupStates()
				cluster.CheckResticErrors()
				cluster.CheckPluginRejectionStates()
				cluster.StateProcessing()
				cluster.CheckHasFailCertLoadP12()
				go cluster.GetSlowLogTable() // prevent blocking cycle
			}
		}

		if cluster.clog != nil {
			clevel := config.ToLogrusLevel(cluster.Conf.LogGraphiteLevel)
			if cluster.clog.GetLevel() != clevel {
				cluster.clog.SetLevel(clevel)
			}
		}

		time.Sleep(interval * time.Duration(cluster.Conf.MonitoringTicker))

	}
}

func (cluster *Cluster) StateProcessing() {
	if !cluster.StateMachine.IsInFailover() {
		// trigger action on resolving states
		cstates := cluster.StateMachine.GetResolvedStates()
		for _, s := range cstates {
			//Remove from captured state if already resolved, so it will capture next occurence
			cluster.GetStateMachine().CapturedState.Delete(s.ErrKey)
			servertoreseed := cluster.GetServerFromURL(s.ServerUrl)

			if s.ErrKey == "WARN0073" || s.ErrKey == "WARN0175" {
				cluster.ServerGlobals.ReleaseBackupSlot()
			}
			if s.ErrKey == "WARN0074" && servertoreseed != nil {
				task := "reseed" + cluster.Conf.BackupPhysicalType

				err := servertoreseed.ProcessReseedPhysical(task)
				if err != nil {
					servertoreseed.JobsUpdateState(task, err.Error(), 2, 1)
					if servertoreseed.HasReseedingState(task) {
						servertoreseed.SetInReseedBackup("")
					}
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Fail of processing reseed for %s: %s", servertoreseed.URL, err)
				}
			}

			if s.ErrKey == "WARN0075" && servertoreseed != nil {
				task := "reseed" + cluster.Conf.BackupLogicalType
				if servertoreseed.JobResults.Get(task) == nil {
					servertoreseed.JobResults.Callback(func(key string, value *config.Task) bool {
						switch key {
						case "reseed" + config.ConstBackupLogicalTypeMysqldump, "reseed" + config.ConstBackupLogicalTypeMydumper:
							task = key
							return false
						default:
							return true
						}
					})
				}

				err := servertoreseed.ProcessReseedLogical(task)
				if err != nil {
					servertoreseed.JobsUpdateState(task, err.Error(), 5, 1)
					if servertoreseed.HasReseedingState(task) {
						servertoreseed.SetInReseedBackup("")
					}
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Fail of processing logical reseed for %s: %s", servertoreseed.URL, err)
				}
			}

			if s.ErrKey == "WARN0076" && servertoreseed != nil {
				task := "flashback" + cluster.Conf.BackupPhysicalType
				err := servertoreseed.ProcessFlashbackPhysical(task)
				if err != nil {
					servertoreseed.JobsUpdateState(task, err.Error(), 2, 1)
					if servertoreseed.HasReseedingState(task) {
						servertoreseed.SetInReseedBackup("")
					}
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Fail of processing flashback for %s: %s", servertoreseed.URL, err)
				}
			}

			if s.ErrKey == "WARN0111" {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cluster have logical backup")
				for _, srv := range cluster.Servers {
					if srv.HasWaitLogicalBackupCookie() {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Server %s was waiting for logical backup", srv.URL)
						go func() {
							err := srv.JobReseedLogicalBackup(context.Background(), "default")
							if err != nil {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Logical reseed on %s error: %s", srv.URL, err.Error())
							}
						}()
					}
				}
			}
			if s.ErrKey == "WARN0112" {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cluster have physical backup")
				for _, srv := range cluster.Servers {
					if srv.HasWaitPhysicalBackupCookie() {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Server %s was waiting for physical backup", srv.URL)
						go func() {
							err := srv.JobReseedPhysicalBackup("default")
							if err != nil {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, err.Error())
							}
						}()
					}
				}
			}
			if s.ErrKey == "WARN0148" && servertoreseed != nil {
				go servertoreseed.UpgradeJobsScript()
			}

			if s.ErrKey == "WARN0155" {
				go cluster.RollingJobsUpgrade()
			}

			if s.ErrKey == "WARN0163" {
				go cluster.MonitorSchema()
			}

			//		cluster.statecloseChan <- s
			cluster.CheckAlert(s, true)
			cluster.BashScriptCloseSate(s)
		}

		//Replace old state print
		cluster.LogPrintAllStates()
		cluster.LogPrintAllWorkloadStates()
		cluster.LogPrintAllSecurityStates()

		// trigger action on resolving states
		ostates := cluster.StateMachine.GetOpenStates()
		for _, s := range ostates {
			cluster.CheckCapture(s)
		}

		for _, s := range cluster.StateMachine.GetLastOpenedStates() {
			if s.ErrKey == "WARN0166" {
				server := cluster.GetServerFromURL(s.ServerUrl)
				if server == nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn,
						"Restic reseed queued but server not found: %s", s.ServerUrl)
				} else {
					req, ok := server.DequeueResticReseed()
					if !ok {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn,
							"Restic reseed queued but no pending request for %s", server.URL)
						continue
					}
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo,
						"Dequeued restic reseed request for %s snapshot=%s method=%s strategy=%s",
						server.URL, req.SnapshotID, req.Method, req.Strategy)
					go func(srv *ServerMonitor, request ResticReseedRequest) {
						err := srv.JobReseedFromRestic(request.SnapshotID, request.Method, request.Strategy, request.Options)
						if err != nil {
							if srv.HasAnyReseedingState() {
								srv.SetInReseedBackup("")
							}
							if srv.HasWaitResticReseedCookie() {
								if err := srv.DelWaitResticReseedCookie(); err != nil {
									cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn,
										"Failed to clear restic reseed cookie after error for %s snapshot=%s: %s", srv.URL, request.SnapshotID, err)
								}
							}
							cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr,
								"Restic reseed failed for %s snapshot=%s: %s", srv.URL, request.SnapshotID, err)
							return
						}
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo,
							"Restic reseed completed for %s snapshot=%s", srv.URL, request.SnapshotID)
					}(server, req)
				}
			}

			cluster.CheckAlert(s, false)
			cluster.BashScriptOpenSate(s)

		}

		cluster.StateMachine.ClearState()
		cluster.WorkloadStateMachine.ClearState()
		cluster.SecurityStateMachine.ClearState()
		if cluster.StateMachine.GetHeartbeats()%60 == 0 && cluster.IsActive() {
			cluster.ConfigManager.SaveConfig(cluster, false)
		}
	}

	cluster.CheckSendMail()
}

func (cluster *Cluster) Stop() {
	cluster.stopOnce.Do(func() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Stop: signaling exit")
		cluster.exit.Store(true)

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Stop: stopping scheduler")
		if cluster.scheduler != nil {
			cluster.scheduler.Stop()
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Stop: closing template MD5 worker")
		cluster.CloseRefreshTemplateMD5Worker()

		if cluster.ResticManager != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Stop: unmounting restic repo")
			if err := cluster.ResticManager.UnmountRepo(); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Restic unmount on shutdown failed: %s", err)
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Stop: shutting down restic worker")
			cluster.ResticManager.ShutdownWorker()
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Stop: saving config")
		cluster.ConfigManager.SaveConfig(cluster, true)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Stop: done")
	})
}

func (cluster *Cluster) SetIsSavingConfig(val bool) {
	cluster.IsSavingConfig = val
}

// SecurityScore holds the compliance status for each security check.
// Each field maps to a tag that can be contributed by an external plugin
// via ScoreCheck.Tag. Score (0-100) is the percentage of passing checks.
//
// When multiple servers in a cluster report the same tag (e.g. HasAuditPlugins),
// the cluster-level result is the AND of all per-server results: every server
// must pass for the check to count as passing. This prevents a replica that
// has a feature from masking a master that does not.
type SecurityScore struct {
	HasSSL              bool   `json:"hasSSL"`
	HasZeroSSL          bool   `json:"hasZeroSSL"`
	HasTableEncryption  bool   `json:"hasTableEncryption"`
	HasBinlogEncryption bool   `json:"hasBinlogEncryption"`
	HasTmpEncryption    bool   `json:"hasTmpEncryption"`
	HasBackupEncryption bool   `json:"hasBackupEncryption"`
	HasAuditPlugins     bool   `json:"hasAuditPlugins"`
	NoEmptyPassword     bool   `json:"noEmptyPassword"`
	HasPrepareStatement bool   `json:"hasPrepareStatement"`
	HasStrongPwd        bool   `json:"hasStrongPwd"`
	HasProxies          bool   `json:"hasProxies"`
	HasParsecPlugins    bool   `json:"hasParsecPlugins"`
	HasPasswordRotation bool   `json:"hasPasswordRotation"`
	NoWeakAuthPlugin    bool   `json:"noWeakAuthPlugin"`
	NoHostnameGrants    bool   `json:"noHostnameGrants"`
	NoClearPwdConfigs   bool   `json:"noClearPwdConfigs"`
	NoClearPwdHistory   bool   `json:"noClearPwdHistory"`
	NoClearPwdBinlogs   bool   `json:"noClearPwdBinlogs"`
	HasLastLTS          bool   `json:"hasLastLTS"`
	Score               int    `json:"score"` // 0-100
	Grade               string `json:"grade"` // A/B/C/D/F

	// applied tracks which tags have received at least one result this tick.
	// Not exported; reset to nil when SecurityScore is zeroed each tick.
	applied map[string]struct{}
}

// ApplyCheck merges a per-server score result into the cluster-level score.
//
// AND semantics: the first call for a tag sets the value; subsequent calls
// (from other servers in the same tick) AND with the accumulated value.
// This ensures a cluster only passes a check when every server passes it —
// a replica that has the audit plugin cannot mask a master that does not.
//
// Unknown tags are silently ignored so new plugins don't break old repman builds.
func (s *SecurityScore) ApplyCheck(tag string, pass bool) {
	if s.applied == nil {
		s.applied = make(map[string]struct{})
	}
	_, seen := s.applied[tag]
	if seen {
		// AND: once any server fails this check the cluster fails it.
		pass = s.getCheck(tag) && pass
	}
	s.applied[tag] = struct{}{}
	s.setCheck(tag, pass)
}

func (s *SecurityScore) getCheck(tag string) bool {
	switch tag {
	case "HasSSL":
		return s.HasSSL
	case "HasZeroSSL":
		return s.HasZeroSSL
	case "HasTableEncryption":
		return s.HasTableEncryption
	case "HasBinlogEncryption":
		return s.HasBinlogEncryption
	case "HasTmpEncryption":
		return s.HasTmpEncryption
	case "HasBackupEncryption":
		return s.HasBackupEncryption
	case "HasAuditPlugins":
		return s.HasAuditPlugins
	case "NoEmptyPassword":
		return s.NoEmptyPassword
	case "HasPrepareStatement":
		return s.HasPrepareStatement
	case "HasStrongPwd":
		return s.HasStrongPwd
	case "HasProxies":
		return s.HasProxies
	case "HasParsecPlugins":
		return s.HasParsecPlugins
	case "HasPasswordRotation":
		return s.HasPasswordRotation
	case "NoWeakAuthPlugin":
		return s.NoWeakAuthPlugin
	case "NoHostnameGrants":
		return s.NoHostnameGrants
	case "NoClearPwdConfigs":
		return s.NoClearPwdConfigs
	case "NoClearPwdHistory":
		return s.NoClearPwdHistory
	case "NoClearPwdBinlogs":
		return s.NoClearPwdBinlogs
	case "HasLastLTS":
		return s.HasLastLTS
	}
	return false
}

func (s *SecurityScore) setCheck(tag string, pass bool) {
	switch tag {
	case "HasSSL":
		s.HasSSL = pass
	case "HasZeroSSL":
		s.HasZeroSSL = pass
	case "HasTableEncryption":
		s.HasTableEncryption = pass
	case "HasBinlogEncryption":
		s.HasBinlogEncryption = pass
	case "HasTmpEncryption":
		s.HasTmpEncryption = pass
	case "HasBackupEncryption":
		s.HasBackupEncryption = pass
	case "HasAuditPlugins":
		s.HasAuditPlugins = pass
	case "NoEmptyPassword":
		s.NoEmptyPassword = pass
	case "HasPrepareStatement":
		s.HasPrepareStatement = pass
	case "HasStrongPwd":
		s.HasStrongPwd = pass
	case "HasProxies":
		s.HasProxies = pass
	case "HasParsecPlugins":
		s.HasParsecPlugins = pass
	case "HasPasswordRotation":
		s.HasPasswordRotation = pass
	case "NoWeakAuthPlugin":
		s.NoWeakAuthPlugin = pass
	case "NoHostnameGrants":
		s.NoHostnameGrants = pass
	case "NoClearPwdConfigs":
		s.NoClearPwdConfigs = pass
	case "NoClearPwdHistory":
		s.NoClearPwdHistory = pass
	case "NoClearPwdBinlogs":
		s.NoClearPwdBinlogs = pass
	case "HasLastLTS":
		s.HasLastLTS = pass
	}
}

// Compute recalculates Score and Grade from the boolean fields.
func (s *SecurityScore) Compute() {
	checks := []bool{
		s.HasSSL, s.HasZeroSSL, s.HasTableEncryption, s.HasBinlogEncryption,
		s.HasTmpEncryption, s.HasBackupEncryption, s.HasAuditPlugins,
		s.NoEmptyPassword, s.HasPrepareStatement, s.HasStrongPwd, s.HasProxies,
		s.HasParsecPlugins, s.HasPasswordRotation, s.NoWeakAuthPlugin, s.NoHostnameGrants, s.NoClearPwdConfigs,
		s.NoClearPwdHistory, s.NoClearPwdBinlogs, s.HasLastLTS,
	}
	passed := 0
	for _, c := range checks {
		if c {
			passed++
		}
	}
	s.Score = passed * 100 / len(checks)
	switch {
	case s.Score >= 90:
		s.Grade = "A"
	case s.Score >= 75:
		s.Grade = "B"
	case s.Score >= 60:
		s.Grade = "C"
	case s.Score >= 40:
		s.Grade = "D"
	default:
		s.Grade = "F"
	}
}

type ClusterState struct {
	Servers       string    `json:"servers"`
	Crashes       crashList `json:"crashes"`
	IsAllDbUp     bool      `json:"provisioned"`
	RepmgrVersion string    `json:"repmgrVersion"`
	RepmgrArch    string    `json:"repmgrArch"`
	RepmgrOS      string    `json:"repmgrOS"`
	// Peer health fields — consumed by the BO to build peer.json with health data,
	// removing the need for direct peer-to-peer HTTP polling.
	IsDown        bool `json:"isDown"`
	IsMasterDown  bool `json:"isMasterDown"`
	IsFailable    bool `json:"isFailable"`
	IsProvisioned bool `json:"isProvisioned"`
}

// recomputeAppCredits recomputes Cloud18ApplicationCreditsUsed and
// Cloud18ApplicationCreditsPlanned from the current per-app values.
// Must be called without the cluster lock held.
func (cluster *Cluster) recomputeAppCredits() {
	cluster.Lock()
	used := 0
	planned := 0
	for _, app := range cluster.Apps {
		used += app.AppConfig.ProvAppCreditUsed
		planned += app.AppConfig.ProvAppCreditPlanned
	}
	cluster.Unlock()
	cluster.Conf.Cloud18ApplicationCreditsUsed = used
	cluster.Conf.Cloud18ApplicationCreditsPlanned = planned
}

// rebaseAppCreditCap raises Cloud18ApplicationCredits to the current used total
// when actual provisioned usage has outgrown the persisted cap.
func (cluster *Cluster) rebaseAppCreditCap() bool {
	if cluster.Conf.Cloud18ApplicationCreditsUsed <= cluster.Conf.Cloud18ApplicationCredits {
		return false
	}
	cluster.Conf.Cloud18ApplicationCredits = cluster.Conf.Cloud18ApplicationCreditsUsed
	return true
}

// ClearAppProvisionedCredits zeros the used credits for app, removes its provision
// cookie, refreshes cluster totals, and persists the cleared state so restart
// rebuilds stay correct. Call this after a successful unprovision.
func (cluster *Cluster) ClearAppProvisionedCredits(app *App) {
	if app == nil {
		return
	}
	app.AppConfig.ProvAppCreditUsed = 0
	app.DelProvisionCookie()
	// Mark explicitly unprovisioned so the status-cycle backfill in IsAppProvisioned
	// does not re-credit the app if it still briefly appears running.
	app.SetUnprovisionCookie()
	cluster.recomputeAppCredits()
	if _, err := cluster.SaveApp(app, ""); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlErr, "Failed to persist credit clear for %s: %s", app.Name, err)
	}
}

// StartBillingCycle is called when a new sponsorship cycle is accepted.
// It recomputes totals from apps and, if current usage is below the cap,
// lowers the cap to match usage (including zero), then persists the new cap.
// Failures are logged but never propagated — sponsorship is already committed
// by the time this runs, so a persist failure must not misreport the outcome.
func (cluster *Cluster) StartBillingCycle() {
	cluster.recomputeAppCredits()
	used := cluster.Conf.Cloud18ApplicationCreditsUsed
	cap := cluster.Conf.Cloud18ApplicationCredits
	if cap > 0 && used < cap {
		cluster.Conf.Cloud18ApplicationCredits = used
		if _, err := cluster.SaveConfigFile(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to persist billing cycle cap: %s", err)
		}
	}
}

type ClusterSLAState struct {
	SLA        state.Sla   `json:"sla"`
	SLAHistory []state.Sla `json:"slaHistory"`
}

func (cluster *Cluster) Save() error {

	_, file, no, ok := runtime.Caller(1)
	if ok {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlDbg, "Saved called from %s#%d\n", file, no)
	}

	var clsave ClusterState
	clsave.Crashes = cluster.Crashes
	clsave.Servers = cluster.Conf.Hosts
	clsave.IsAllDbUp = cluster.IsAllDbUp
	clsave.RepmgrVersion = cluster.RepMgrVersion
	clsave.RepmgrArch = cluster.Conf.GoArch
	clsave.RepmgrOS = cluster.Conf.GoOS
	clsave.IsDown = cluster.IsDown
	clsave.IsMasterDown = cluster.GetMaster() == nil || cluster.GetMaster().State == "Failed"
	clsave.IsFailable = !cluster.IsNotMonitoring && cluster.StateMachine.GetHeartbeats() > 0
	clsave.IsProvisioned = cluster.IsAllDbUp

	saveJson, _ := json.MarshalIndent(clsave, "", "\t")
	err := os.WriteFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/clusterstate.json", saveJson, 0644)
	if err != nil {
		return err
	}

	var slasave ClusterSLAState
	slasave.SLA = cluster.StateMachine.GetSla()
	slasave.SLAHistory = cluster.SLAHistory

	saveSLAJson, _ := json.MarshalIndent(slasave, "", "\t")
	err = os.WriteFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/sla.json", saveSLAJson, 0644)
	if err != nil {
		return err
	}

	saveQeueryRules, _ := json.MarshalIndent(cluster.QueryRules, "", "\t")
	err = os.WriteFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/queryrules.json", saveQeueryRules, 0644)
	if err != nil {
		return err
	}

	// Sort them so it will not push if no changes are made
	slices.SortStableFunc(cluster.Agents, func(a, b Agent) int {
		if a.Id < b.Id {
			return -1
		} else if a.Id > b.Id {
			return 1
		} else {
			return 0
		}
	})

	saveAgents, _ := json.MarshalIndent(cluster.Agents, "", "\t")

	err = os.WriteFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/agents.json", saveAgents, 0644)
	if err != nil {
		return err
	}

	has_changed := false

	if cluster.Conf.ConfRewrite {
		// Check and inject config
		cluster.CheckInjectConfig()

		// Save the main configuration file
		changed, err := cluster.SaveConfigFile()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during save cluster config: %s", err)
			return err
		}

		if changed {
			has_changed = true
		}

		// Checksum decrypted value to prevent unnecessary file
		new_ih, err := cluster.Conf.GetImmutableChecksum()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during checksum immutable config: %s", err)
		}
		old_ih, ok := cluster.CheckSumConfig["plain-immutable"]

		new_sh, err := cluster.Conf.GetSecretChecksum()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during checksum secret config: %s", err)
		}
		old_sh, ok2 := cluster.CheckSumConfig["plain-secret"]

		non_secret_change := !ok || !bytes.Equal(old_ih.Sum(nil), new_ih.Sum(nil))
		secret_change := !ok2 || !bytes.Equal(old_sh.Sum(nil), new_sh.Sum(nil))
		if non_secret_change {
			cluster.CheckSumConfig["plain-immutable"] = new_ih
		}

		if secret_change {
			cluster.CheckSumConfig["plain-secret"] = new_sh
		}

		// Only save if the value is changed
		if non_secret_change || secret_change {

			has_changed = true
			// Save the immutable configuration file
			_, err := cluster.SaveImmutableConfig()
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during save cluster immutable config: %s", err)
				return err
			}

			// Save the cache configuration file
			if err := cluster.SaveCacheConfig(); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during save cluster cache config: %s", err)
				return err
			}
		}

		_, err = cluster.Overwrite()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during Overwriting: %s", err)
		}

		changed, _ = cluster.SaveAppConfigs()
		if changed {
			has_changed = true
		}
	}

	if has_changed {
		cluster.IsNeedGitPush = true
	}

	return nil
}

func (cluster *Cluster) SaveConfigFile() (bool, error) {
	var has_changed bool

	filePath := cluster.Conf.WorkingDir + "/" + cluster.Name + "/" + cluster.Name + ".toml"
	header := "[saved-" + cluster.Name + "]\ntitle = \"" + cluster.Name + "\" \n"

	// Marshal and write TOML configuration
	readconf, err := toml.Marshal(*cluster.Conf)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error marshalling toml: %s", err)
		return false, err
	}

	// Load TOML and sort keys
	t, err := toml.LoadBytes(readconf)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error loading toml: %s", err)
		return false, err
	}

	s := t
	keys := t.Keys()
	keys = misc.SortKeysAsc(keys)

	// Write sorted values to file
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
	if err != nil {
		if os.IsPermission(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "File permission denied: %s", filePath)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error opening file: %s", err)
		}
		return false, err
	}
	defer file.Close()

	// Write header
	file.WriteString(header)

	for _, key := range keys {
		_, ok := cluster.Conf.ImmuableFlagMap[key]
		if ok {
			s.Delete(key)
		} else {
			v, ok := cluster.Conf.DefaultFlagMap[key]
			if ok && fmt.Sprintf("%v", s.Get(key)) == fmt.Sprintf("%v", v) {
				s.Delete(key)
			} else if !ok {
				s.Delete(key)
			} else if _, ok = cluster.Conf.Secrets[key]; ok {
				s.Delete(key)
				//to encrypt credentials before writting in the config file
				encrypt_val := cluster.GetEncryptedValueFromMemory(key)
				file.WriteString(key + " = \"" + encrypt_val + "\"\n")

			}
		}
	}

	s.WriteTo(file)
	//fmt.Printf("SAVE CLUSTER IMMUABLE MAP : %s", cluster.Conf.ImmuableFlagMap)
	//fmt.Printf("SAVE CLUSTER DYNAMIC MAP : %s", cluster.Conf.DynamicFlagMap)
	new_h := md5.New()
	if _, err := io.Copy(new_h, file); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during Overwriting: %s", err)
	}

	h, ok := cluster.CheckSumConfig["saved"]
	if !ok {
		has_changed = true
	}
	if ok && !bytes.Equal(h.Sum(nil), new_h.Sum(nil)) {
		has_changed = true
	}

	cluster.CheckSumConfig["saved"] = new_h

	return has_changed, nil
}

func (cluster *Cluster) SaveImmutableConfig() (bool, error) {
	var has_changed bool

	// Get Sorted Keys
	keys := make([]string, 0)
	for key := range cluster.Conf.ImmuableFlagMap {
		keys = append(keys, key)
	}

	keys = misc.SortKeysAsc(keys)

	// Open file and
	file2, err := os.OpenFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/immutable.toml", os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
	if err != nil {
		if os.IsPermission(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "File permission denied: %s", cluster.Conf.WorkingDir+"/"+cluster.Name+"/immutable.toml")
		}
		return false, err
	}
	defer file2.Close()

	for _, key := range keys {
		val := cluster.Conf.ImmuableFlagMap[key]
		_, ok := cluster.Conf.Secrets[key]
		if ok {
			encrypt_val := cluster.GetEncryptedValueFromMemory(key)
			file2.WriteString(key + " = \"" + encrypt_val + "\"\n")
		} else {
			if fmt.Sprintf("%T", val) == "string" {
				file2.WriteString(key + " = \"" + fmt.Sprintf("%v", val) + "\"\n")
			} else {
				file2.WriteString(key + " = " + fmt.Sprintf("%v", val) + "\n")
			}
		}
	}

	new_h := md5.New()
	if _, err := io.Copy(new_h, file2); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during Overwriting: %s", err)
	}

	h, ok := cluster.CheckSumConfig["immutable"]
	if !ok {
		has_changed = true
	}
	if ok && !bytes.Equal(h.Sum(nil), new_h.Sum(nil)) {
		has_changed = true
	}

	cluster.CheckSumConfig["immutable"] = new_h

	return has_changed, nil
}

func (cluster *Cluster) SaveCacheConfig() error {
	filePath := cluster.Conf.WorkingDir + "/" + cluster.Name + "/cache.toml"
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
	if err != nil {
		if os.IsPermission(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "File permission denied: %s", filePath)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlErr, "Error opening file: %s", err)
		}
		return err
	}
	defer file.Close()

	keys := make([]string, 0)
	for key := range cluster.Conf.ImmuableFlagMap {
		keys = append(keys, key)
	}

	keys = misc.SortKeysAsc(keys)

	for _, key := range keys {
		if _, ok := cluster.Conf.Secrets[key]; ok {
			encrypt_val := cluster.GetEncryptedValueFromMemory(key)
			file.WriteString(key + " = \"" + encrypt_val + "\"\n")
		}
	}

	return nil
}

func (cluster *Cluster) Overwrite() (bool, error) {
	var has_changed bool

	if cluster.Conf.ConfRewrite {
		file, err := os.OpenFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/overwrite.toml", os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
		if err != nil {
			if os.IsPermission(err) {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "File permission denied: %s", cluster.Conf.WorkingDir+"/"+cluster.Name+"/overwrite.toml")
			}
			return false, err
		}
		defer file.Close()

		readconf, _ := toml.Marshal(*cluster.Conf)
		t, _ := toml.LoadBytes(readconf)
		s := t
		keys := t.Keys()
		keys = misc.SortKeysAsc(keys)

		for _, key := range keys {
			v, ok := cluster.Conf.ImmuableFlagMap[key]
			if !ok {
				s.Delete(key)
			} else {

				if ok && fmt.Sprintf("%v", s.Get(key)) == fmt.Sprintf("%v", v) && (cluster.Conf.Secrets[key].Value == cluster.Conf.Secrets[key].OldValue || cluster.Conf.Secrets[key].OldValue == "") {
					s.Delete(key)
				} else if _, ok = cluster.Conf.Secrets[key]; ok && cluster.Conf.Secrets[key].Value != v {
					v := cluster.GetEncryptedValueFromMemory(key)
					if v != "" {
						s.Set(key, v)
					} else {
						s.Delete(key)
					}
				}

			}

		}

		file.WriteString("[overwrite-" + cluster.Name + "]\n")
		s.WriteTo(file)

		new_h := md5.New()
		if _, err := io.Copy(new_h, file); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlWarn, "Error during Overwriting: %s", err)
		}

		h, ok := cluster.CheckSumConfig["overwrite"]
		if !ok {
			has_changed = true
		}
		if ok && !bytes.Equal(h.Sum(nil), new_h.Sum(nil)) {
			has_changed = true
		}

		cluster.CheckSumConfig["overwrite"] = new_h

	}

	return has_changed, nil
}

func (cluster *Cluster) GetEncryptedValueFromMemory(key string) string {
	switch key {
	case "api-credentials", "api-credentials-external":
		var tab_ApiUser []string
		lst_Users := strings.Split(cluster.Conf.Secrets[key].Value, ",")
		for ind := range lst_Users {
			user_pass := strings.Split(lst_Users[ind], ":")
			if APIuser, ok := cluster.APIUsers[user_pass[0]]; ok {
				tab_ApiUser = append(tab_ApiUser, APIuser.User+":"+APIuser.Password)
			}
		}
		return cluster.Conf.GetStableEncryptedValue(key, strings.Join(tab_ApiUser, ","))
	case "db-servers-credential":
		if cluster.Conf.IsPath(cluster.Conf.User) && cluster.Conf.IsVaultUsed() {
			return ""
		}
		return cluster.Conf.GetStableEncryptedValue(key, cluster.GetDbUser()+":"+cluster.GetDbPass())
	case "monitoring-write-heartbeat-credential":
		return cluster.Conf.GetStableEncryptedValue(key, cluster.GetMonitorWriteHearbeatUser()+":"+cluster.GetMonitorWriteHeartbeatPass())
	case "onpremise-ssh-credential":
		return cluster.Conf.GetStableEncryptedValue(key, cluster.GetOnPremiseSSHUser()+":"+cluster.GetOnPremiseSSHPass())

	case "replication-credential":
		if cluster.Conf.IsPath(cluster.Conf.RplUser) && cluster.Conf.IsVaultUsed() {
			return ""
		}
		return cluster.Conf.GetStableEncryptedValue(key, cluster.GetRplUser()+":"+cluster.GetRplPass())
	case "shardproxy-credential":
		if cluster.Conf.IsPath(cluster.Conf.MdbsProxyCredential) && cluster.Conf.IsVaultUsed() {
			return ""
		}
		return cluster.Conf.GetStableEncryptedValue(key, cluster.GetShardUser()+":"+cluster.GetShardPass())
	case "backup-restic-password":
		return cluster.Conf.GetStableEncryptedValue("backup-restic-password", cluster.Conf.GetDecryptedValue("backup-restic-password"))
	case "haproxy-password":
		return cluster.Conf.GetStableEncryptedValue("haproxy-password", cluster.Conf.GetDecryptedValue("haproxy-password"))
	case "maxscale-pass":
		return cluster.Conf.GetStableEncryptedValue("maxscale-pass", cluster.Conf.GetDecryptedValue("maxscale-pass"))
	case "myproxy-password":
		return cluster.Conf.GetStableEncryptedValue("proxysql-password", cluster.Conf.GetDecryptedValue("proxysql-password"))
	case "proxysql-password":
		if cluster.Conf.IsPath(cluster.Conf.ProxysqlPassword) && cluster.Conf.IsVaultUsed() {
			return ""
		}
		return cluster.Conf.GetStableEncryptedValue("proxysql-password", cluster.Conf.GetDecryptedValue("proxysql-password"))
	case "proxyjanitor-password":
		return cluster.Conf.GetStableEncryptedValue("proxyjanitor-password", cluster.Conf.GetDecryptedValue("proxyjanitor-password"))
	case "vault-secret-id":
		return cluster.Conf.GetStableEncryptedValue("vault-secret-id", cluster.Conf.GetDecryptedValue("vault-secret-id"))
	case "opensvc-p12-secret":
		return cluster.Conf.GetStableEncryptedValue("opensvc-p12-secret", cluster.Conf.GetDecryptedValue("opensvc-p12-secret"))
	case "backup-restic-aws-access-secret":
		return cluster.Conf.GetStableEncryptedValue("backup-restic-aws-access-secret", cluster.Conf.GetDecryptedValue("backup-restic-aws-access-secret"))
	case "backup-streaming-aws-access-secret":
		return cluster.Conf.GetStableEncryptedValue("backup-streaming-aws-access-secret", cluster.Conf.GetDecryptedValue("backup-streaming-aws-access-secret"))
	case "arbitration-external-secret":
		return cluster.Conf.GetStableEncryptedValue("arbitration-external-secret", cluster.Conf.GetDecryptedValue("arbitration-external-secret"))
	case "alert-pushover-user-token":
		return cluster.Conf.GetStableEncryptedValue("alert-pushover-user-token", cluster.Conf.GetDecryptedValue("alert-pushover-user-token"))
	case "alert-pushover-app-token":
		return cluster.Conf.GetStableEncryptedValue("alert-pushover-app-token", cluster.Conf.GetDecryptedValue("alert-pushover-app-token"))
	case "mail-smtp-password":
		return cluster.Conf.GetStableEncryptedValue("mail-smtp-password", cluster.Conf.GetDecryptedValue("mail-smtp-password"))
	case "api-oauth-client-secret":
		return cluster.Conf.GetStableEncryptedValue("api-oauth-client-secret", cluster.Conf.GetDecryptedValue("api-oauth-client-secret"))
	case "cloud18-gitlab-password":
		return cluster.Conf.GetStableEncryptedValue("cloud18-gitlab-password", cluster.Conf.GetDecryptedValue("cloud18-gitlab-password"))
	case "cloud18-dba-user-credentials":
		return cluster.Conf.GetStableEncryptedValue(key, cluster.GetDbaUser()+":"+cluster.GetDbaPass())
	case "cloud18-sponsor-user-credentials":
		return cluster.Conf.GetStableEncryptedValue(key, cluster.GetSponsorUser()+":"+cluster.GetSponsorPass())
	case "git-acces-token":
		return cluster.Conf.GetStableEncryptedValue("git-acces-token", cluster.Conf.GetDecryptedValue("git-acces-token"))
	case "vault-token":
		return cluster.Conf.GetStableEncryptedValue("vault-token", cluster.Conf.GetDecryptedValue("vault-token"))
	default:
		return ""
	}
}

func (cluster *Cluster) configureSnapshotMetadataExtractorConcurrency(concurrency int) {
	manager := cluster.getSnapshotMetadataManager()
	if manager == nil {
		return
	}
	if concurrency <= 0 {
		concurrency = snapshotMetadataExtractorConcurrency
	}
	var logDeferred bool
	var logApplied bool
	var inFlight int
	var previous int
	manager.extractorSemMu.Lock()
	if manager.extractorSem == nil {
		manager.extractorSem = make(chan struct{}, concurrency)
		manager.extractorSemCap = concurrency
		manager.extractorSemPending = 0
		manager.extractorSemMu.Unlock()
		return
	}
	previous = manager.extractorSemCap
	if previous == concurrency {
		manager.extractorSemPending = 0
		manager.extractorSemMu.Unlock()
		return
	}
	inFlight = len(manager.extractorSem)
	if inFlight > 0 {
		manager.extractorSemPending = concurrency
		logDeferred = true
		manager.extractorSemMu.Unlock()
	} else {
		manager.extractorSem = make(chan struct{}, concurrency)
		manager.extractorSemCap = concurrency
		manager.extractorSemPending = 0
		logApplied = true
		manager.extractorSemMu.Unlock()
	}
	if logDeferred {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo,
			"Deferring snapshot metadata extractor concurrency update (in-flight=%d): current=%d requested=%d",
			inFlight, previous, concurrency)
	}
	if logApplied {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo,
			"Snapshot metadata extractor concurrency set to %d (previous %d)", concurrency, previous)
	}
}

func (cluster *Cluster) tryApplySnapshotMetadataExtractorConcurrency() {
	manager := cluster.getSnapshotMetadataManager()
	if manager == nil {
		return
	}
	var pending int
	var previous int
	manager.extractorSemMu.Lock()
	pending = manager.extractorSemPending
	if pending == 0 {
		manager.extractorSemMu.Unlock()
		return
	}
	if manager.extractorSem != nil && len(manager.extractorSem) > 0 {
		manager.extractorSemMu.Unlock()
		return
	}
	previous = manager.extractorSemCap
	manager.extractorSem = make(chan struct{}, pending)
	manager.extractorSemCap = pending
	manager.extractorSemPending = 0
	manager.extractorSemMu.Unlock()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo,
		"Snapshot metadata extractor concurrency updated to %d (previous %d)", pending, previous)
}

func (cluster *Cluster) getSnapshotMetadataManager() *snapshotMetadataManager {
	if cluster == nil {
		return nil
	}
	if cluster.snapshotMetadata == nil {
		cluster.snapshotMetadata = newSnapshotMetadataManager(cluster)
	}
	if cluster.snapshotMetadata.cache == nil {
		cluster.snapshotMetadata.cache = newSnapshotMetadataCache()
	}
	return cluster.snapshotMetadata
}

func (cluster *Cluster) InitAgent(conf config.Config) {
	*cluster.Conf = conf
	cluster.agentFlagCheck()
	if conf.LogFile != "" {
		var err error
		cluster.logPtr, err = os.Create(conf.LogFile)
		if err != nil {
			log.Error("Cannot open logfile, disabling for the rest of the session")
			conf.LogFile = ""
		}
	}
}

func (cluster *Cluster) ReloadConfig(conf config.Config) {
	cluster.Lock()
	*cluster.Conf = conf
	cluster.Unlock()

	cluster.StateMachine.SetFailoverState()
	cluster.ResetStates()
	cluster.InitFromConf()

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.TopologyDiscover(wg)
	wg.Wait()
	cluster.ServerIdList = cluster.GetDBServerIdList()
	cluster.StateMachine.RemoveFailoverState()

	// Phase 3a: refresh cached intra-cluster conflict state.
	cluster.RefreshGatewayConflicts()
	cluster.WithdrawConflictedGatewayRoutes()

	// Phase 3b: cache cross-cluster conflicts and withdraw stale fragments —
	// same policy as 3a and startup.  Peer routes come only from clusters that
	// appear before this one in clusterOrder (first-wins priority).
	// RefreshGatewayConflicts above replaced the map with fresh intra-conflicts;
	// MarkGatewayConflicts below adds cross-cluster ones without overwriting them.
	gw := strings.ToLower(strings.TrimSpace(cluster.Conf.Cloud18GatewayService))
	if gw != "" {
		var priorRoutes [][]config.Route
		for _, name := range cluster.clusterOrder {
			if name == cluster.Name {
				break
			}
			peer, ok := cluster.clusterList[name]
			if !ok || peer == nil {
				continue
			}
			if strings.ToLower(strings.TrimSpace(peer.Conf.Cloud18GatewayService)) == gw {
				priorRoutes = append(priorRoutes, peer.OwnGatewayRoutes(gw)...)
			}
		}
		if conflicts, _ := cluster.DetectCrossClusterGatewayConflicts(priorRoutes); len(conflicts) > 0 {
			cluster.MarkGatewayConflicts(conflicts)
			cluster.WithdrawConflictedGatewayRoutes()
		}
	}
}

func (cluster *Cluster) FailoverForce() error {
	sf := stateFile{Name: "/tmp/mrm" + cluster.Name + ".state"}
	err := sf.access()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Could not create state file")
	}
	err = sf.read()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Could not read values from state file:", err)
	} else {
		cluster.FailoverCtr = int(sf.Count)
		cluster.FailoverTs = sf.Timestamp
	}
	cluster.newServerList()
	//if err != nil {
	//	return err
	//}

	wg := new(sync.WaitGroup)
	wg.Add(1)
	err = cluster.TopologyDiscover(wg)
	wg.Wait()

	if err != nil {
		for _, s := range cluster.StateMachine.GetStates() {
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[:i]
			}
			cluster.LogPrint(s)
		}
		// Test for ERR00012 - No master detected
		if cluster.StateMachine.CurState.Search("ERR00012") {
			for _, s := range cluster.Servers {
				if s.State == "" {
					s.SetState(stateFailed)
					// if cluster.Conf.LogLevel > 2 {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "State failed set by state detection ERR00012")
					// }
					cluster.master = s
				}
			}
		} else {
			return err

		}
	}
	if cluster.GetMaster() == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not find a failed server in the hosts list")
		return errors.New("ERROR: Could not find a failed server in the hosts list")
	}
	if cluster.Conf.FailLimit > 0 && cluster.FailoverCtr >= cluster.Conf.FailLimit {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failover has exceeded its configured limit of %d. Remove /tmp/mrm.state file to reinitialize the failover counter", cluster.Conf.FailLimit)
		return errors.New("ERROR: Failover has exceeded its configured limit")
	}
	rem := (cluster.FailoverTs + cluster.Conf.FailTime) - time.Now().Unix()
	if cluster.Conf.FailTime > 0 && rem > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failover time limit enforced. Next failover available in %d seconds", rem)
		return errors.New("ERROR: Failover time limit enforced")
	}
	if cluster.MasterFailover(true) {
		sf.Count++
		sf.Timestamp = cluster.FailoverTs
		err := sf.write()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Could not write values to state file:%s", err)
		}
	}
	return nil
}

func (cluster *Cluster) SwitchOver() {
	cluster.switchoverChan <- true
}

func (cluster *Cluster) Close() {

	for _, server := range cluster.Servers {
		defer server.Conn.Close()
	}
}

func (cluster *Cluster) ResetFailoverCtr() {
	cluster.FailoverCtr = 0
	cluster.FailoverTs = 0

	for _, server := range cluster.Servers {
		server.DelayStat.ResetDelayStat()
	}
}

func (cluster *Cluster) agentFlagCheck() {

	// if slaves option has been supplied, split into a slice.
	if cluster.Conf.Hosts != "" {
		cluster.hostList = strings.Split(cluster.Conf.Hosts, ",")
	} else {
		log.Fatal("No hosts list specified")
	}
	if len(cluster.hostList) > 1 {
		log.Fatal("Agent can only monitor a single host")
	}

}

func (cluster *Cluster) BackupLogs() {
	if !cluster.Conf.SchedulerDatabaseLogs {
		return
	}
	for _, s := range cluster.Servers {
		if s != nil {
			s.JobBackupErrorLog()
			s.JobBackupSlowQueryLog()
			s.JobBackupSqlErrorLog()
			s.JobBackupAuditLog()
		}

	}
}

func (cluster *Cluster) ClearOldCookies() {
	for _, s := range cluster.Servers {
		if s == nil {
			continue
		}

		s.DelWaitAuditlogCookie()
		s.DelWaitErrorlogCookie()
		s.DelWaitSqlErrorlogCookie()
		s.DelWaitSlowqueryCookie()
	}
}
func (cluster *Cluster) RotateLogs() {
	for _, s := range cluster.Servers {
		s.RotateSystemLogs()
	}
}

func (cluster *Cluster) ResetCrashes() {
	cluster.Crashes = nil
}

// buildVariableChangeIgnoreSet builds the set of variable names to exclude from
// temporal change detection. Merges three sources:
// 1. Static config list (monitoring-variable-change-ignore)
// 2. MariaDB 10.1+: GLOBAL_VALUE_ORIGIN = 'AUTO' from INFORMATION_SCHEMA
// 3. MySQL 8.0+: SET_USER IS NULL from performance_schema.variables_info
func (cluster *Cluster) buildVariableChangeIgnoreSet(srv *ServerMonitor) map[string]bool {
	ignore := make(map[string]bool)

	// 1. Static config list
	if cluster.Conf.MonitorVariableChangeIgnore != "" {
		for _, v := range strings.Split(cluster.Conf.MonitorVariableChangeIgnore, ",") {
			v = strings.TrimSpace(strings.ToUpper(v))
			if v != "" {
				ignore[v] = true
			}
		}
	}

	// 2. Dynamic: query auto-tuned variables from the server
	if srv.Conn != nil {
		if srv.IsMariaDB() && srv.DBVersion.GreaterEqual("10.1") {
			// MariaDB 10.1+ exposes GLOBAL_VALUE_ORIGIN in INFORMATION_SCHEMA
			rows, err := srv.Conn.Query("SELECT UPPER(VARIABLE_NAME) FROM INFORMATION_SCHEMA.SYSTEM_VARIABLES WHERE GLOBAL_VALUE_ORIGIN = 'AUTO'")
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var name string
					if rows.Scan(&name) == nil {
						ignore[name] = true
					}
				}
			}
		} else if srv.DBVersion.IsMySQLOrPerconaGreater8() {
			// MySQL 8.0+: performance_schema.variables_info tracks who set each variable
			rows, err := srv.Conn.Query("SELECT UPPER(VARIABLE_NAME) FROM performance_schema.variables_info WHERE SET_USER IS NULL AND SET_TIME IS NOT NULL")
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var name string
					if rows.Scan(&name) == nil {
						ignore[name] = true
					}
				}
			}
		}
	}

	return ignore
}

// variableExceptions lists variables that legitimately differ between servers
// and should be excluded from cross-server diff (MonitorVariablesDiff).
var variableExceptions = map[string]bool{
	"PORT":                true,
	"SERVER_ID":           true,
	"PID_FILE":            true,
	"WSREP_NODE_NAME":     true,
	"LOG_BIN_INDEX":       true,
	"LOG_BIN_BASENAME":    true,
	"LOG_ERROR":           true,
	"READ_ONLY":           true,
	"IN_TRANSACTION":      true,
	"GTID_SLAVE_POS":      true,
	"GTID_CURRENT_POS":    true,
	"GTID_BINLOG_POS":     true,
	"GTID_BINLOG_STATE":   true,
	"GENERAL_LOG_FILE":    true,
	"TIMESTAMP":           true,
	"SLOW_QUERY_LOG_FILE": true,
	"REPORT_HOST":         true,
	"SERVER_UUID":         true,
	"GTID_PURGED":         true,
	"HOSTNAME":            true,
	"SUPER_READ_ONLY":     true,
	"GTID_EXECUTED":       true,
	"WSREP_DATA_HOME_DIR": true,
	"REPORT_PORT":         true,
	"SOCKET":              true,
	"DATADIR":             true,
	"THREAD_POOL_SIZE":    true,
	"RELAY_LOG":           true,
	"RELAY_LOG_BASENAME":  true,
	"RELAY_LOG_INDEX":     true,
	"LOG_SLOW_QUERY_FILE": true,
	"PLUGIN_DIR":          true,
	"SERVER_UID":          true,
}

func (cluster *Cluster) MonitorVariablesDiff() {
	if !cluster.Conf.MonitorVariableDiff || cluster.GetMaster() == nil {
		return
	}
	masterVariables := cluster.GetMaster().Variables.ToNewMap()
	exceptVariables := variableExceptions
	hasDiff := false
	var alldiff []VariableDiff
	for k, v := range masterVariables {
		var myvardiff VariableDiff
		var myvalues []Diff
		myvalues = append(myvalues, Diff{Server: cluster.GetMaster().URL, Role: "leader", VariableValue: v})
		for _, s := range cluster.slaves {
			slaveVariables := s.Variables.ToNewMap()
			if slaveVariables[k] != v && !exceptVariables[k] {
				myvalues = append(myvalues, Diff{Server: s.URL, Role: "replica", VariableValue: slaveVariables[k]})
				hasDiff = true
			}
		}
		if len(myvalues) > 1 {
			myvardiff.VariableName = k
			myvardiff.DiffValues = myvalues
			alldiff = append(alldiff, myvardiff)
		}
	}
	if hasDiff {
		// Deterministic order: alldiff is built from map iteration, which is
		// random per run — sort so the signature, log order and state
		// description are stable across checks.
		sort.Slice(alldiff, func(i, j int) bool { return alldiff[i].VariableName < alldiff[j].VariableName })
		sig := variableDiffSignature(alldiff)
		changed := sig != cluster.variableDiffSignature
		cluster.variableDiffSignature = sig
		cluster.DiffVariables = alldiff
		// Log details and persist the file only when the diff set actually
		// changed — re-logging identical diffs on every check flooded the log.
		if changed {
			cluster.SaveVariableDiff(alldiff)
			for _, d := range alldiff {
				for _, dv := range d.DiffValues {
					if dv.Role != "leader" {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
							"Variable %s differs on %s (%s): %s", d.VariableName, dv.Server, dv.Role, dv.VariableValue)
					}
				}
			}
		}
		// Re-assert the state on every run so it stays open while the diff persists.
		cluster.SetState("WARN0084", state.State{
			ErrType:   "WARNING",
			ErrDesc:   fmt.Sprintf(clusterError["WARN0084"], cluster.FormatVariableDiffTable(alldiff)),
			ErrFrom:   "MON",
			ServerUrl: cluster.GetMaster().URL,
		})
	} else {
		cluster.DiffVariables = nil
		cluster.variableDiffSignature = ""
	}
}

// variableDiffSignature returns a stable identity for a variable diff set so
// MonitorVariablesDiff can detect real changes between runs.
func variableDiffSignature(diffs []VariableDiff) string {
	var b strings.Builder
	for _, d := range diffs {
		for _, dv := range d.DiffValues {
			fmt.Fprintf(&b, "%s|%s|%s\n", d.VariableName, dv.Server, dv.VariableValue)
		}
	}
	return b.String()
}

func (cluster *Cluster) FormatVariableDiffTable(diffs []VariableDiff) string {
	varW, srvW, roleW, valW := len("Variable"), len("Server"), len("Role"), len("Value")
	type row struct{ v, s, r, val string }
	var rows []row
	for _, d := range diffs {
		for _, dv := range d.DiffValues {
			r := row{d.VariableName, dv.Server, dv.Role, dv.VariableValue}
			if len(r.v) > varW {
				varW = len(r.v)
			}
			if len(r.s) > srvW {
				srvW = len(r.s)
			}
			if len(r.r) > roleW {
				roleW = len(r.r)
			}
			if len(r.val) > valW {
				valW = len(r.val)
			}
			rows = append(rows, r)
		}
	}
	f := fmt.Sprintf("%%-%ds | %%-%ds | %%-%ds | %%-%ds", varW, srvW, roleW, valW)
	sep := fmt.Sprintf(f, strings.Repeat("-", varW), strings.Repeat("-", srvW), strings.Repeat("-", roleW), strings.Repeat("-", valW))
	sep = strings.ReplaceAll(sep, " | ", "-+-")
	var b strings.Builder
	fmt.Fprintf(&b, f, "Variable", "Server", "Role", "Value")
	b.WriteString("\n")
	b.WriteString(sep)
	for _, r := range rows {
		b.WriteString("\n")
		fmt.Fprintf(&b, f, r.v, r.s, r.r, r.val)
	}
	return b.String()
}

// SaveVariableDiff persists the variable diff to a JSON file in the cluster's working directory.
func (cluster *Cluster) SaveVariableDiff(diff []VariableDiff) {
	path := filepath.Join(cluster.WorkingDir, "variable-diff.json")
	data, err := json.MarshalIndent(diff, "", "  ")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Encoding variable diff: %s", err)
		return
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Writing variable-diff.json: %s", err)
	}
}

// MonitorVariablesChange detects when a variable value changes over time on any
// server (temporal change, not cross-server diff). Compares current Variables
// with PrevVariables snapshot and calls the monitoring-variable-change-script.
func (cluster *Cluster) MonitorVariablesChange() {
	if !cluster.Conf.MonitorVariableChange {
		return
	}
	for _, srv := range cluster.Servers {
		if srv == nil || srv.State == stateFailed || srv.State == stateUnconn || srv.Variables == nil {
			// Reset snapshot on failure/disconnect so recovery diff is clean
			if srv != nil && srv.PrevVariables != nil && (srv.State == stateFailed || srv.State == stateUnconn) {
				srv.PrevVariables = nil
			}
			continue
		}
		if srv.PrevVariables == nil {
			// First run or post-recovery: snapshot current variables, no diff yet
			srv.PrevVariables = config.FromStringSyncMap(nil, srv.Variables)
			continue
		}
		currentVars := srv.Variables.ToNewMap()
		prevVars := srv.PrevVariables.ToNewMap()

		// Build ignore set: static config + dynamic SQL query
		ignoreSet := cluster.buildVariableChangeIgnoreSet(srv)

		var sb strings.Builder
		sb.WriteString("--- " + srv.URL + " (before)\n")
		sb.WriteString("+++ " + srv.URL + " (after)\n")
		changeCount := 0

		// Detect changed and removed variables
		for k, old := range prevVars {
			if ignoreSet[k] {
				continue
			}
			if v, exists := currentVars[k]; !exists {
				sb.WriteString("- " + k + " = " + old + "\n")
				changeCount++
			} else if old != v {
				sb.WriteString("- " + k + " = " + old + "\n")
				sb.WriteString("+ " + k + " = " + v + "\n")
				changeCount++
			}
		}
		// Detect new variables (in current but not in prev)
		for k, v := range currentVars {
			if ignoreSet[k] {
				continue
			}
			if _, exists := prevVars[k]; !exists {
				sb.WriteString("+ " + k + " = " + v + "\n")
				changeCount++
			}
		}

		if changeCount > 0 {
			diffStr := sb.String()
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"Variable change detected on %s: %d variable(s) changed", srv.URL, changeCount)
			cluster.LogVariableChange.Add(s18log.HttpMessage{
				Group:     cluster.Name,
				Level:     "INFO",
				Timestamp: time.Now().Format("2006-01-02 15:04:05"),
				Text:      fmt.Sprintf("Server %s: %d variable(s) changed\n%s", srv.URL, changeCount, diffStr),
			})
			cluster.BashScriptVariableChange(srv.URL, diffStr)
		}

		// Update snapshot
		srv.PrevVariables = config.FromStringSyncMap(nil, srv.Variables)
	}
}

func (cluster *Cluster) MonitorMasterTableSchema() error {
	cmaster := cluster.GetMaster()
	if cmaster == nil {
		return fmt.Errorf("No master found")
	}

	if cmaster.State == stateFailed || cmaster.State == stateMaintenance || cmaster.State == stateUnconn {
		return fmt.Errorf("Master is not in a valid state")
	}
	if cmaster.Conn == nil {
		return fmt.Errorf("Master connection is not established")
	}

	loglevel := config.LvlInfo
	// Shardproxy will increase the intensity of monitoring, so set to debug
	if cluster.Conf.MdbsProxyOn {
		loglevel = config.LvlDbg
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, loglevel, "Monitoring master table schema on %s", cmaster.URL)
	cmaster.Conn.SetConnMaxLifetime(3595 * time.Second)

	tables, tablelist, logs, err := dbhelper.GetTables(cmaster.Conn, cmaster.DBVersion, cluster.Conf.MonitorSchemaColumns, cluster.Conf.MonitorSchemaIndexes, cluster.Conf.MonitorSchemaScanTimeout)
	cluster.LogSQL(logs, err, cmaster.URL, "Monitor", config.LvlDbg, "Could not fetch master tables %s", err)
	if err != nil {
		return err
	}
	cmaster.Tables = tablelist

	var tableCluster []string
	var duplicates []*ServerMonitor
	var tottablesize, totindexsize int64
	for _, t := range tables {
		duplicates = nil
		tableCluster = nil
		tottablesize += t.DataLength
		totindexsize += t.IndexLength
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Lookup for table %s", t.TableSchema+"."+t.TableName)

		duplicates = append(duplicates, cmaster)
		tableCluster = append(tableCluster, cluster.GetName())
		oldtable, err := cmaster.GetTableFromDict(t.TableSchema + "." + t.TableName)
		haschanged := false
		changeType := ""
		if err != nil {
			if err.Error() == "Empty" {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Init table %s", t.TableSchema+"."+t.TableName)
				haschanged = true
				changeType = "init"
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "New table %s", t.TableSchema+"."+t.TableName)
				haschanged = true
				changeType = "new"
			}
		} else {
			if oldtable.TableCrc != t.TableCrc {
				haschanged = true
				changeType = "altered"
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Change table %s", t.TableSchema+"."+t.TableName)
			}
			t.TableSync = oldtable.TableSync
		}

		if haschanged && changeType != "init" {
			var oldCols []dbhelper.Column
			if err == nil {
				oldCols = oldtable.TableColumns
			}
			diff := columnDiff(t.TableSchema, t.TableName, oldCols, t.TableColumns)
			cluster.LogDDL.Add(s18log.HttpMessage{
				Group:     cluster.Name,
				Level:     "INFO",
				Timestamp: time.Now().Format("2006-01-02 15:04:05"),
				Text:      fmt.Sprintf("Server %s: %s %s.%s\n%s", cmaster.URL, changeType, t.TableSchema, t.TableName, diff),
			})
			cluster.BashScriptSchemaChange(cmaster.URL, t.TableSchema, t.TableName, changeType, oldCols, t.TableColumns)
		}

		// If shardproxy is enabled, check for duplicates in child clusters
		if cluster.Conf.MdbsProxyOn {
			for _, cl := range cluster.clusterList {
				if cl.Conf.MdbsProxyOn && cl.Conf.ClusterHead == cluster.Name {
					m := cl.GetMaster()
					if m != nil {
						cltbldef, _ := m.GetTableFromDict(t.TableSchema + "." + t.TableName)
						if cltbldef.TableName == t.TableName {
							duplicates = append(duplicates, m)
							tableCluster = append(tableCluster, cl.GetName())
							cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Found duplicate table %s in %s", t.TableSchema+"."+t.TableName, m.URL)
						}
					}
				}
			}
			t.TableClusters = strings.Join(tableCluster, ",")
			tables[t.TableSchema+"."+t.TableName] = t

			if haschanged {
				for _, pri := range cluster.Proxies {
					if prx, ok := pri.(*MariadbShardProxy); ok {
						if t.TableSchema != "replication_manager_schema" &&
							!strings.Contains(t.TableName, "_copy") &&
							!strings.Contains(t.TableName, "_back") &&
							!strings.Contains(t.TableName, "_old") &&
							!strings.Contains(t.TableName, "_reshard") {
							cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "blabla table %s %s %s", duplicates, t.TableSchema, t.TableName)
							cluster.ShardProxyCreateVTable(prx, t.TableSchema, t.TableName, duplicates, false)
						}
					}
				}
			}
		}
	}

	// Detect dropped tables: in old cache but not in current scan
	oldTables := cmaster.DictTables.ToNewMap()
	for fqn, oldT := range oldTables {
		if _, exists := tables[fqn]; !exists {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Dropped table %s", fqn)
			diff := columnDiff(oldT.TableSchema, oldT.TableName, oldT.TableColumns, nil)
			cluster.LogDDL.Add(s18log.HttpMessage{
				Group:     cluster.Name,
				Level:     "INFO",
				Timestamp: time.Now().Format("2006-01-02 15:04:05"),
				Text:      fmt.Sprintf("Server %s: dropped %s\n%s", cmaster.URL, fqn, diff),
			})
			cluster.BashScriptSchemaChange(cmaster.URL, oldT.TableSchema, oldT.TableName, "dropped", oldT.TableColumns, nil)
		}
	}

	cluster.WorkLoad.DBIndexSize = totindexsize
	cluster.WorkLoad.DBTableSize = tottablesize
	cmaster.DictTables = dbhelper.FromNormalTablesMap(cmaster.DictTables, tables)

	return nil
}

func (cluster *Cluster) MonitorAllSlavesTableSchema() {
	for _, sl := range cluster.slaves {
		cluster.MonitorSlaveTableSchema(sl)
	}
}

func (cluster *Cluster) MonitorSlaveTableSchema(sl *ServerMonitor) error {
	if sl.State == stateFailed || sl.State == stateMaintenance || sl.State == stateUnconn {
		return fmt.Errorf("Slave is not in a valid state")
	}

	if sl.Conn == nil {
		return fmt.Errorf("Slave connection is not established")
	}

	loglevel := config.LvlInfo
	// Shardproxy will increase the intensity of monitoring, so set to debug
	if cluster.Conf.MdbsProxyOn {
		loglevel = config.LvlDbg
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, loglevel, "Monitoring slave table schema on %s", sl.URL)

	sl.Conn.SetConnMaxLifetime(3595 * time.Second)
	tables, tablelist, logs, err := dbhelper.GetTables(sl.Conn, sl.DBVersion, cluster.Conf.MonitorSchemaColumns, cluster.Conf.MonitorSchemaIndexes, cluster.Conf.MonitorSchemaScanTimeout)
	cluster.LogSQL(logs, err, sl.URL, "Monitor", config.LvlDbg, "Could not fetch slave tables %s", err)
	if err != nil {
		return err
	}
	sl.Tables = tablelist
	sl.DictTables = dbhelper.FromNormalTablesMap(sl.DictTables, tables)

	return nil
}

func (cluster *Cluster) CompareSchemaBetweenMasterAndSlave(sl *ServerMonitor) ([]string, []string) {
	diffs := make([]string, 0)
	ignored := make([]string, 0)

	if cluster.GetMaster() == nil || sl == nil {
		return diffs, ignored
	}

	masterTables := cluster.GetMaster().DictTables.ToNewMap()
	slTables := sl.DictTables.ToNewMap()

	masterKeys := make([]string, 0, len(masterTables))
	for tblname := range masterTables {
		masterKeys = append(masterKeys, tblname)
	}
	slices.Sort(masterKeys)

	for _, tblname := range masterKeys {
		mtbl := masterTables[tblname]
		if cluster.IsInSchemaIgnore(tblname) {
			ignored = append(ignored, tblname)
			continue
		}
		stbl, ok := slTables[tblname]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("Table %s missing on slave %s", tblname, sl.URL))
			continue
		}

		tbldiffs := make([]string, 0, 3)
		if mtbl.TableCrc != stbl.TableCrc {
			metaDiffs := make([]string, 0, 4)
			if mtbl.Engine != stbl.Engine {
				metaDiffs = append(metaDiffs, fmt.Sprintf("engine %s -> %s", mtbl.Engine, stbl.Engine))
			}
			if mtbl.RowFormat != stbl.RowFormat {
				metaDiffs = append(metaDiffs, fmt.Sprintf("row_format %s -> %s", mtbl.RowFormat, stbl.RowFormat))
			}
			if mtbl.TableCollation != stbl.TableCollation {
				metaDiffs = append(metaDiffs, fmt.Sprintf("collation %s -> %s", mtbl.TableCollation, stbl.TableCollation))
			}
			if mtbl.CreateOptions != stbl.CreateOptions {
				metaDiffs = append(metaDiffs, fmt.Sprintf("create_options %s -> %s", mtbl.CreateOptions, stbl.CreateOptions))
			}
			if len(metaDiffs) > 0 {
				tbldiffs = append(tbldiffs, "metadata: ("+strings.Join(metaDiffs, ", ")+")")
			}

			columnsAvailable := mtbl.TableColumnsCrc64 != 0 && stbl.TableColumnsCrc64 != 0 && mtbl.TableColumnMap != nil && stbl.TableColumnMap != nil
			indexesAvailable := mtbl.TableIndexesCrc64 != 0 && stbl.TableIndexesCrc64 != 0 && mtbl.TableIndexMap != nil && stbl.TableIndexMap != nil
			if mtbl.TableColumnsCrc64 != stbl.TableColumnsCrc64 {
				if columnsAvailable {
					coldiffs := mtbl.ColumnDiffs(stbl, sl.URL)
					if len(coldiffs) > 0 {
						tbldiffs = append(tbldiffs, "columns: ("+strings.Join(coldiffs, ", ")+")")
					}
				} else {
					tbldiffs = append(tbldiffs, "columns: diff (details unavailable)")
				}
			}
			if mtbl.TableIndexesCrc64 != stbl.TableIndexesCrc64 {
				if indexesAvailable {
					idxdiffs := mtbl.IndexDiffs(stbl, sl.URL)
					if len(idxdiffs) > 0 {
						tbldiffs = append(tbldiffs, "indexes: ("+strings.Join(idxdiffs, ", ")+")")
					}
				} else {
					tbldiffs = append(tbldiffs, "indexes: diff (details unavailable)")
				}
			}
			if len(tbldiffs) == 0 {
				if !columnsAvailable && !indexesAvailable {
					tbldiffs = append(tbldiffs, "table crc differs; column/index details unavailable")
				} else {
					tbldiffs = append(tbldiffs, "table crc differs, no column or index differences found")
				}
			}
		}
		if len(tbldiffs) > 0 {
			diffs = append(diffs, fmt.Sprintf("Table %s differs on slave %s -> %s", tblname, sl.URL, strings.Join(tbldiffs, " ")))
		}
	}

	slaveKeys := make([]string, 0, len(slTables))
	for tblname := range slTables {
		slaveKeys = append(slaveKeys, tblname)
	}
	slices.Sort(slaveKeys)

	for _, tblname := range slaveKeys {
		_, ok := masterTables[tblname]
		if !ok {
			if cluster.IsInSchemaIgnore(tblname) {
				ignored = append(ignored, tblname)
				continue
			}
			diffs = append(diffs, fmt.Sprintf("Extra table %s found on slave %s", tblname, sl.URL))
		}
	}

	return diffs, ignored
}

func (cluster *Cluster) MonitorTableSchemaDiff() {
	if !cluster.Conf.MonitorSchemaChange {
		return
	}

	if !cluster.Conf.MonitorSchemaOnReplicas {
		return
	}

	for _, sl := range cluster.slaves {
		if sl == nil {
			continue
		}

		diffs, _ := cluster.CompareSchemaBetweenMasterAndSlave(sl)
		if len(diffs) > 0 {
			cluster.SetState("WARN0164", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0164"], sl.URL, strings.Join(diffs, "\n")), ErrFrom: "MON", ServerUrl: sl.URL})
		}
	}
}

func (cluster *Cluster) MonitorSchema() {
	if !cluster.Conf.MonitorSchemaChange && atomic.LoadInt32(&cluster.SchemaMonitorRequested) == 0 {
		return
	}
	if cluster.StateMachine.IsInSchemaMonitor() {
		return
	}
	// give workload time
	if !(cluster.StateMachine.SchemaMonitorEndTime+60 < time.Now().Unix()) {
		return
	}
	if atomic.LoadInt32(&cluster.SchemaMonitorRequested) == 1 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"Schema monitoring requested; bypassing config gate for this run.")
	}

	loglevel := config.LvlInfo
	// Shardproxy will increase the intensity of monitoring, so set to debug
	if cluster.Conf.MdbsProxyOn {
		loglevel = config.LvlDbg
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, loglevel, "Starting schema monitoring")

	cluster.StateMachine.SetMonitorSchemaState()
	defer func() {
		cluster.StateMachine.RemoveMonitorSchemaState()
		atomic.StoreInt32(&cluster.SchemaMonitorRequested, 0)
	}()

	err := cluster.MonitorMasterTableSchema()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error during schema monitoring: %s", err)
	}

	if cluster.Conf.MonitorSchemaOnReplicas {
		cluster.MonitorAllSlavesTableSchema()
	}
}

func (cluster *Cluster) MonitorQueryRules() {
	if !cluster.Conf.MonitorQueryRules {
		return
	}
	// exit early
	if !cluster.Conf.ProxysqlOn {
		return
	}
	for _, pri := range cluster.Proxies {
		if prx, ok := pri.(*ProxySQLProxy); ok {
			qr := prx.QueryRules
			for _, rule := range qr {
				var myRule config.QueryRule
				if clrule, ok := cluster.QueryRules[rule.Id]; ok {
					myRule = clrule

					// Handle existing proxies list
					var proxyIds []string
					if clrule.Proxies != "" {
						proxyIds = strings.Split(clrule.Proxies, ",")
					}

					// Check if current proxy already tracked
					found := false
					for _, prxid := range proxyIds {
						if prx.Id == prxid {
							found = true
							break
						}
					}

					if !found {
						proxyIds = append(proxyIds, prx.Id)
					}

					myRule.Proxies = strings.Join(proxyIds, ",")
				} else {
					myRule.Id = rule.Id
					myRule.UserName = rule.UserName
					myRule.Digest = rule.Digest
					myRule.Match_Digest = rule.Match_Digest
					myRule.Match_Pattern = rule.Match_Pattern
					myRule.MirrorHostgroup = rule.MirrorHostgroup
					myRule.DestinationHostgroup = rule.DestinationHostgroup
					myRule.Multiplex = rule.Multiplex
					myRule.Proxies = prx.Id
				}
				cluster.QueryRules[rule.Id] = myRule
			}
		}
	}
}

// Arbitration Only works for GTID now need crash info fetch from arbitrator to do better
func (cluster *Cluster) LostArbitration(realmasterurl string) {

	//need to join real master via change master
	realmaster := cluster.GetServerFromURL(realmasterurl)
	if realmaster == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Can't found elected master from server list on lost arbitration")
		return
	}
	master := cluster.GetMaster()
	if master == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Can't found failed master on lost arbitration")
		return
	}
	if cluster.Conf.ArbitrationFailedMasterScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Calling abitration failed for master script")
		out, err := exec.Command(cluster.Conf.ArbitrationFailedMasterScript, master.Host, master.Port).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s", err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Arbitration failed master script complete: %s", string(out))
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Arbitration failed attaching failed master %s to electected master :%s", master.URL, realmaster.URL)
		logs, err := master.SetReplicationGTIDCurrentPosFromServer(realmaster)
		cluster.LogSQL(logs, err, realmaster.URL, "Arbitration", config.LvlErr, "Failed in GTID rejoin lost master to winner master %s", err)

	}
}

func (c *Cluster) AddProxy(prx DatabaseProxy) {
	prx.SetCluster(c)
	prx.SetID()
	prx.SetDataDir()
	prx.SetServiceName(c.Name)
	c.LogModulePrintf(c.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "New proxy monitored %s: %s:%s", prx.GetType(), prx.GetHost(), prx.GetPort())
	prx.SetState(stateSuspect)
	c.Proxies = append(c.Proxies, prx)
}

func (c *Cluster) AddApp(app *App) error {
	if err := c.initializeAppForRegistration(app); err != nil {
		return err
	}
	c.Lock()
	c.Apps = append(c.Apps, app)
	c.bumpAppListVersion()
	c.Unlock()

	c.recomputeAppCredits()
	if app.AppConfig.ProvAppCreditPlanned > app.AppConfig.ProvAppCreditUsed {
		if app.HasProvisionCookie() {
			app.SetReprovCookie()
		}
	}
	return nil
}

func (cluster *Cluster) ConfigDiscovery() error {
	master := cluster.GetMaster()
	if master == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cluster configuration discovery can only be done on a valid leader")
		return errors.New("Cluster configuration discovery can only be done on a valid leader")
	}
	cluster.Configurator.ConfigDiscovery(master.Variables, master.Plugins)
	cluster.SetDBCoresFromConfigurator()
	cluster.SetDBMemoryFromConfigurator()
	cluster.SetDBIOPSFromConfigurator()
	cluster.SetTagsFromConfigurator()
	return nil
}

func (cluster *Cluster) ReloadCertificates() {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Reload cluster TLS certificates")
	for _, srv := range cluster.Servers {
		srv.CertificatesReload()
	}
	for _, pri := range cluster.Proxies {
		pri.CertificatesReload()
	}
}

// appListVersion returns a monotonic version for Cluster.Apps structural changes.
// It is used by sync/apply flows to detect stale snapshots without taking
// additional coarse cluster locks in hot paths.
func (cluster *Cluster) appListVersion() uint64 {
	return atomic.LoadUint64(&cluster.appListEpoch)
}

func (cluster *Cluster) bumpAppListVersion() {
	atomic.AddUint64(&cluster.appListEpoch, 1)
}

func (cluster *Cluster) ResetStates() {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Reset cluster states")
	cluster.SetUnDiscovered()
	cluster.slaves = nil
	cluster.master = nil
	cluster.oldMaster = nil
	cluster.vmaster = nil
	//cluster.Servers = nil
	//cluster.Proxies = nil
	//
	cluster.ServerIdList = nil
	//cluster.hostList = nil
	//cluster.clusterList = nil
	cluster.proxyList = nil
	cluster.ProxyIdList = nil
	//cluster.FailoverCtr = 0
	cluster.SetFailoverCtr(0)
	//cluster.FailoverTs = 0
	cluster.SetFailTime(0)
	cluster.WorkLoad.Connections = 0
	cluster.WorkLoad.CpuThreadPool = 0.0
	cluster.WorkLoad.CpuUserStats = 0.0
	cluster.SLAHistory = nil
	//
	cluster.Crashes = nil

	cluster.IsAllDbUp = false
	cluster.IsDown = true
	cluster.IsClusterDown = true
	cluster.IsProvision = false
	cluster.IsNotMonitoring = true

	cluster.canFlashBack = true
	cluster.CanInitNodes = true
	cluster.CanConnectVault = true
	cluster.runOnceAfterTopology = true
	cluster.testStopCluster = true
	cluster.testStartCluster = true

	//cluster.StateMachine.RemoveFailoverState()
}

func (cluster *Cluster) DecryptSecretsFromVault() {
	// Only proceed if Vault is being used
	if !cluster.Conf.IsVaultUsed() {
		return
	}

	for k, v := range cluster.Conf.Secrets {
		origin_value := v.Value
		var secret config.Secret
		secret.Value = fmt.Sprintf("%v", origin_value)
		if cluster.Conf.IsPath(secret.Value) {
			//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault,LvlInfo, "Decrypting all the secret variables on Vault")
			vault_config := vault.DefaultConfig()
			vault_config.Address = cluster.Conf.VaultServerAddr
			client, err := cluster.Conf.GetVaultConnection()
			if err == nil {
				if cluster.Conf.VaultMode == VaultConfigStoreV2 {
					vault_value, err := cluster.Conf.GetVaultCredentials(client, secret.Value, k)
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlWarn, "Unable to get %s Vault secret: %v", k, err)
					} else if vault_value != "" {
						secret.Value = vault_value
					}
				}
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModVault, config.LvlErr, "Unable to initialize AppRole auth method: %v", err)
			}
			cluster.Conf.Secrets[k] = secret
		}
	}
}

func (cluster *Cluster) RefreshDatabaseConfigs() error {
	for _, srv := range cluster.Servers {
		if srv == nil {
			continue
		}

		err := srv.GetDatabaseConfig()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Could not refresh database config for %s: %s", srv.URL, err)
		}
	}

	return nil
}
