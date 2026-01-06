// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
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
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bluele/logrus_slack"
	vault "github.com/hashicorp/vault/api"

	"github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/cluster/configurator"
	"github.com/signal18/replication-manager/cluster/nbc"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
	"github.com/signal18/replication-manager/opensvc"
	v3 "github.com/signal18/replication-manager/repmanv3"
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
	OsUser                        *user.User                 `json:"-"`
	Name                          string                     `json:"name" groups:"apps,web"`
	Tenant                        string                     `json:"tenant" groups:"web"`
	WorkingDir                    string                     `json:"workingDir" groups:"web"`
	Servers                       serverList                 `json:"servers" groups:"apps"`
	LogSlaveServers               []string                   `json:"logSlaveServers" groups:"web" ` //To store slave with log-slave-updates
	ServerIdList                  []string                   `json:"dbServers" groups:"web"`
	Crashes                       crashList                  `json:"dbServersCrashes" groups:"web"` //This will be purged on all db node up
	FailoverHistory               crashList                  `json:"failoverHistory" groups:"web"`  //This will be used for PITR
	Apps                          appList                    `json:"apps" groups:"apps" `
	AppIdList                     []string                   `json:"appServers" groups:"web"`
	Proxies                       proxyList                  `json:"proxies" groups:"apps"`
	ProxyIdList                   []string                   `json:"proxyServers" groups:"web"`
	FailoverCtr                   int                        `json:"failoverCounter" groups:"web"`
	FailoverTs                    int64                      `json:"failoverLastTime" groups:"web"`
	Status                        string                     `json:"activePassiveStatus" groups:"web"`
	IsSplitBrain                  bool                       `json:"isSplitBrain" groups:"web"`
	IsSplitBrainBck               bool                       `json:"-"`
	IsFailedArbitrator            bool                       `json:"isFailedArbitrator" groups:"web"`
	IsLostMajority                bool                       `json:"isLostMajority" groups:"web"`
	IsDown                        bool                       `json:"isDown" groups:"web"`
	IsClusterDown                 bool                       `json:"isClusterDown" groups:"web"`
	IsMasterDown                  bool                       `json:"isMasterDown" groups:"web"`
	IsAllDbUp                     bool                       `json:"isAllDbUp" groups:"web"`
	IsFailable                    bool                       `json:"isFailable" groups:"web"`
	IsPostgres                    bool                       `json:"isPostgres" groups:"web"`
	IsProvision                   bool                       `json:"isProvision" groups:"web"`
	IsNeedProxiesRestart          bool                       `json:"isNeedProxiesRestart" groups:"web"`
	IsNeedProxiesReprov           bool                       `json:"isNeedProxiesReprov" groups:"web"`
	IsNeedProxiesConfigChange     bool                       `json:"isNeedProxiesConfigChange" groups:"web"`
	IsNeedDatabasesRestart        bool                       `json:"isNeedDatabasesRestart" groups:"web"`
	IsNeedDatabasesRollingRestart bool                       `json:"isNeedDatabasesRollingRestart" groups:"web"`
	IsNeedDatabasesRollingReprov  bool                       `json:"isNeedDatabasesRollingReprov" groups:"web"`
	IsNeedDatabasesReprov         bool                       `json:"isNeedDatabasesReprov" groups:"web"`
	IsNeedDatabasesConfigChange   bool                       `json:"isNeedDatabasesConfigChange" groups:"web"`
	IsNeedAppsReprov              bool                       `json:"isNeedAppsReprov" groups:"web"`
	IsGettingSlowLog              bool                       `json:"isGettingSlowLog" groups:"web"`
	IsValidBackup                 bool                       `json:"isValidBackup" groups:"web"`
	IsNotMonitoring               bool                       `json:"isNotMonitoring" groups:"web"`
	IsCapturing                   bool                       `json:"isCapturing" groups:"web"`
	IsGitPull                     bool                       `json:"isGitPull" groups:"web"`
	IsGitPush                     bool                       `json:"isGitPush" groups:"web"`
	IsSavingConfig                bool                       `json:"-"`
	IsNeedGitPush                 bool                       `json:"-"`
	IsExportPush                  bool                       `json:"isExportPush" groups:"web"`
	IsAlertDisable                bool                       `json:"isAlertDisable" groups:"web"`
	IsRefreshStaging              bool                       `json:"isRefreshStaging" groups:"web"`
	IsNeedStagingChange           bool                       `json:"isNeedStagingChange" groups:"web"`
	IsConfigPathChange            bool                       `json:"isConfigPathChange" groups:"web"`
	IsResticQueuePaused           bool                       `json:"isResticQueuePaused" groups:"web"`
	Conf                          *config.Config             `json:"config" groups:"apps"`
	Confs                         *config.ConfVersion        `json:"-"`
	CleanAll                      bool                       `json:"cleanReplication" groups:"web"` //used in testing
	Topology                      string                     `json:"topology" groups:"web"`
	Uptime                        string                     `json:"uptime" groups:"web"`
	UptimeFailable                string                     `json:"uptimeFailable" groups:"web"`
	UptimeSemiSync                string                     `json:"uptimeSemisync" groups:"web"`
	MonitorSpin                   string                     `json:"monitorSpin" groups:"web"`
	WorkLoad                      config.WorkLoad            `json:"workLoad" groups:"web"`
	Logrus                        *log.Logger                `json:"-"`
	LogPushover                   *log.Logger                `json:"-"`
	Log                           s18log.HttpLog             `json:"-" groups:"web"`
	LogTask                       s18log.HttpLog             `json:"-" groups:"web"`
	LogSlack                      *slackman.SlackManager     `json:"-"`
	JobResults                    *config.TasksMap           `json:"jobResults" groups:"web"`
	FalsePositiveChecks           map[string]bool            `json:"falsePositiveChecks" groups:"web"`
	Grants                        map[string]string          `json:"-"`
	Roles                         map[string]string          `json:"-"`
	tlog                          *s18log.TermLog            `json:"-"`
	htlog                         *s18log.HttpLog            `json:"-"`
	SQLGeneralLog                 s18log.HttpLog             `json:"sqlGeneralLog" groups:"web"`
	SQLErrorLog                   s18log.HttpLog             `json:"sqlErrorLog" groups:"web"`
	MonitorType                   map[string]string          `json:"monitorType" groups:"web"`
	TopologyType                  map[string]string          `json:"topologyType" groups:"web"`
	FSType                        map[string]bool            `json:"fsType" groups:"web"`
	DiskType                      map[string]string          `json:"diskType" groups:"web"`
	VMType                        map[string]bool            `json:"vmType" groups:"web"`
	AppS3Providers                []string                   `json:"appS3Providers" groups:"web"`
	Agents                        []Agent                    `json:"agents" groups:"web"`
	AgentMaxFreq                  map[string]int64           `json:"-"`
	hostList                      []string                   `json:"-"`
	proxyList                     []string                   `json:"-"`
	clusterList                   map[string]*Cluster        `json:"-"`
	deprecatedKeys                map[string]map[string]bool `json:"-"`
	slaves                        serverList                 `json:"slaves" groups:"apps"`
	master                        *ServerMonitor             `json:"master" groups:"apps"`
	oldMaster                     *ServerMonitor             `json:"oldmaster" groups:"web"`
	vmaster                       *ServerMonitor             `json:"vmaster" `
	StagingServer                 *ServerMonitor             `json:"-" groups:"web"`
	mxs                           *maxscale.MaxScale         `json:"-"`
	CheckSumConfig                map[string]hash.Hash       `json:"-"`
	//dbUser                        string                      `json:"-"`
	//oldDbUser string `json:"-"`
	//dbPass                        string                      `json:"-"`
	//oldDbPass string `json:"-"`
	//rplUser                   string                      `json:"-"`
	//rplPass                   string                      `json:"-"`
	//proxysqlUser              string                      `json:"-"`
	//proxysqlPass              string                      `json:"-"`
	StateMachine           *state.StateMachine         `json:"stateMachine" groups:"web"`
	runOnceAfterTopology   bool                        `json:"-"`
	logPtr                 *os.File                    `json:"-"`
	termlength             int                         `json:"-"`
	runUUID                string                      `json:"-"`
	cfgGroupDisplay        string                      `json:"-"`
	RepMgrVersion          string                      `json:"-"`
	RepMgrHostname         string                      `json:"-"`
	exitMsg                string                      `json:"-"`
	exit                   bool                        `json:"-"`
	canFlashBack           bool                        `json:"-"`
	canResticFetchRepo     bool                        `json:"-"`
	failoverCond           *nbc.NonBlockingChan        `json:"-"`
	switchoverCond         *nbc.NonBlockingChan        `json:"-"`
	rejoinCond             *nbc.NonBlockingChan        `json:"-"`
	bootstrapCond          *nbc.NonBlockingChan        `json:"-"`
	altertableCond         *nbc.NonBlockingChan        `json:"-"`
	addtableCond           *nbc.NonBlockingChan        `json:"-"`
	statecloseChan         chan state.State            `json:"-"`
	switchoverChan         chan bool                   `json:"-"`
	errorChan              chan error                  `json:"-"`
	testStopCluster        bool                        `json:"-"`
	testStartCluster       bool                        `json:"-"`
	lastmaster             *ServerMonitor              `json:"-"`
	benchmarkType          string                      `json:"-"`
	HaveDBTLSCert          bool                        `json:"haveDBTLSCert" groups:"web"`
	HaveDBTLSOldCert       bool                        `json:"haveDBTLSOldCert" groups:"web"`
	tlsconf                *tls.Config                 `json:"-"`
	tlsoldconf             *tls.Config                 `json:"-"`
	tunnel                 *ssh.Client                 `json:"-"`
	QueryRules             map[uint32]config.QueryRule `json:"-"`
	Backups                []v3.Backup                 `json:"-"`
	BackupStat             v3.BackupStat               `json:"backupStat" groups:"web"`
	BackupMetaMap          *backupmgr.BackupMetaMap    `json:"backupList" groups:"web"`
	SLAHistory             []state.Sla                 `json:"slaHistory" groups:"web"`
	APIUsers               map[string]APIUser          `json:"apiUsers" groups:"web"`
	Schedule               map[string]cron.Entry       `json:"-"`
	scheduler              *cron.Cron                  `json:"-"`
	debugLineMap           map[string]int              `json:"-"`
	WaitingRejoin          int                         `json:"waitingRejoin" groups:"web"`
	WaitingSwitchover      int                         `json:"waitingSwitchover" groups:"web"`
	WaitingFailover        int                         `json:"waitingFailover" groups:"web"`
	Configurator           configurator.Configurator   `json:"configurator" groups:"web"`
	DiffVariables          []VariableDiff              `json:"diffVariables" groups:"web"`
	inInitNodes            bool                        `json:"-"`
	inOptimizeTables       bool                        `json:"inOptimizeTables" groups:"web"`
	inAnalyzeTables        bool                        `json:"inAnalyzeTables" groups:"web"`
	inConnectVault         bool                        `json:"-"`
	CanInitNodes           bool                        `json:"canInitNodes" groups:"web"`
	errorInitNodes         error                       `json:"-"`
	CanConnectVault        bool                        `json:"canConnectVault"`
	errorConnectVault      error                       `json:"-"`
	SqlErrorLog            *logsql.Logger              `json:"-"`
	SqlGeneralLog          *logsql.Logger              `json:"-"`
	SstAvailablePorts      map[string]string           `json:"sstAvailablePorts" groups:"web"`
	InPhysicalBackup       bool                        `json:"inPhysicalBackup" groups:"web"`
	InLogicalBackup        bool                        `json:"inLogicalBackup" groups:"web"`
	InBinlogBackup         bool                        `json:"inBinlogBackup" groups:"web"`
	InResticBackup         bool                        `json:"inResticBackup" groups:"web"`
	InRollingRestart       bool                        `json:"inRollingRestart" groups:"web"`
	failLoadP12Cert        bool                        `json:"-"`
	Mailer                 *mailer.Mailer              `json:"-"`
	ResticManager          *backupmgr.ResticManager    `json:"-"`
	MessageChan            chan sharedlog.Message      `json:"-"`
	ErrorConfigs           config.ErrorConfigs         `json:"-"` //To store error config
	Partner                *config.Partner             `json:"partner" groups:"web"`
	ConfigManager          *manager.ConfigManager      `json:"-"`
	failSendCount          int                         `json:"-"`
	MeetUserID             string                      `json:"-"` //To store meet user id
	ServiceTemplates       []string                    `json:"-"` //To store application templates
	DiskStatManager        *misc.DiskStatManager       `json:"diskStat" groups:"web"`
	RefreshTemplateMD5Chan chan *App                   `json:"-"`
	LastDelayStatPrint     time.Time
	sync.Mutex
	crcTable               *crc64.Table
	SlavesOldestMasterFile SlavesOldestMasterFile
	SlavesConnected        int
	clog                   *clog.Logger `json:"-"`
	*ClusterGraphite
	VersionsMap         *config.VersionsMap
	SessionManager      *tty.SessionManager `json:"-"`
	SysBenchTpcMResults []SysBenchTpcResultPerMinute
	OpenSVCStats        atomic.Value `json:"-"`
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
	VariableValue string `json:"variableValue"`
}

type VariableDiff struct {
	VariableName string `json:"variableName"`
	DiffValues   []Diff `json:"diffValues"`
}

const (
	stateClusterStart string = "Running starting"
	stateClusterDown  string = "Running cluster down"
	stateClusterErr   string = "Running with errors"
	stateClusterWarn  string = "Running with warnings"
	stateClusterRun   string = "Running"
)
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

	cluster.tlog = tlog
	cluster.htlog = loghttp
	cluster.termlength = termlength
	cluster.Name = cfgGroup

	cluster.runUUID = runUUID
	cluster.RepMgrHostname = RepMgrHostname
	cluster.RepMgrVersion = RepMgrVersion

	cluster.InitFromConf()
	cluster.NewClusterGraphite()
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
	if cluster.Conf.Arbitration {
		cluster.Status = ConstMonitorStandby
	} else {
		cluster.Status = ConstMonitorActif
	}
	cluster.benchmarkType = "sysbench"
	cluster.Log = s18log.NewHttpLog(200)
	cluster.LogTask = s18log.NewHttpLog(200)

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
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Creating directory  %s", cluster.WorkingDir)
		os.MkdirAll(cluster.WorkingDir, os.ModePerm)
	}

	cluster.SetClusterCredentialsFromConfig()
	cluster.LoadAPIUsers()
	cluster.SaveAcls()
	cluster.InitMailer()
	cluster.GetPersitentState()

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

	if cluster.Conf.Cloud18 && cluster.Conf.Cloud18Alert {
		cluster.LogSlack.Activate("cloud18", true)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "START", "Replication manager started with version: %s", cluster.Conf.Version)

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

	cluster.LoadAppConfigs()

	err = cluster.newServerList()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not set server list %s", err)
	}
	cluster.ClearOldCookies()

	err = cluster.newProxyList()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not set proxy list %s", err)
	}
	err = cluster.newAppList()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not set app list %s", err)
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
	cluster.StartResticManager()

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
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting cluster scheduler")
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
		cluster.scheduler.Start()
	}

}

var pstates30 = []string{
	"WARN0084",             // Variables diff
	"ERR00090", "WARN0102", // Config related
	"WARN0093", "WARN0095", "WARN0134", "WARN0145", // Restic related
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
	"WARN0117", "WARN0118", "WARN0119", "WARN0120", "WARN0121", "WARN0156", "WARN0157", // Tools versions
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

	for cluster.exit == false {
		if !cluster.Conf.MonitorPause {
			cluster.ServerIdList = cluster.GetDBServerIdList()
			cluster.ProxyIdList = cluster.GetProxyServerIdList()
			cluster.AppIdList = cluster.GetAppServerIdList()
			if cluster.ResticManager != nil {
				cluster.IsResticQueuePaused = cluster.ResticManager.IsPaused()
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
				wg := new(sync.WaitGroup)
				wg.Add(1)
				go cluster.TopologyDiscover(wg)
				wg.Add(1)
				go cluster.Heartbeat(wg)
				wg.Wait()
				// Heartbeat switchover or failover controller runs only on active repman

				if cluster.runOnceAfterTopology {
					// Preserved server state in proxy during reload config
					if !cluster.IsInFailover() {
						cluster.initProxies()
					}
					go cluster.initOrchetratorNodes()
					go cluster.ResticFetchRepo()
					cluster.SetRollingJobsUpgradeState()
					// Clean up any lingering restart cookies from previous runs
					cluster.CleanupRestartCookies()
					cluster.runOnceAfterTopology = false
				} else {

					// Preserved server state in proxy during reload config
					if !cluster.IsInFailover() {
						wg.Add(1)
						go cluster.refreshProxies(wg)
						go cluster.refreshApps(wg)
						cluster.CheckWaitRunJobSSH()
						cluster.CheckDummyConfigSendCookies()
						cluster.CheckRestartCookies()

						// Monitor schema when shardproxy is used
						if cluster.Conf.MdbsProxyOn && cluster.StateMachine.SchemaMonitorEndTime+60 < time.Now().Unix() {
							go cluster.MonitorSchema()
						}

						if cluster.Conf.TestInjectTraffic || cluster.Conf.TestInjectTrafficStaging || cluster.Conf.AutorejoinSlavePositionalHeartbeat || cluster.Conf.MonitorWriteHeartbeat {
							cluster.InjectProxiesTraffic()
						}

						if cluster.StateMachine.GetHeartbeats()%10 == 0 {
							cluster.CheckJobsVersion()
							cluster.MonitorTableSchemaDiff()
						} else {
							cluster.StateMachine.PreserveState("WARN0147", "WARN0164")
						}

						if cluster.StateMachine.GetHeartbeats()%30 == 0 {
							// Check if restic repo is available
							cluster.ResticFetchRepo()
							go cluster.initOrchetratorNodes()
							cluster.MonitorQueryRules()
							cluster.MonitorVariablesDiff()
							cluster.IsValidBackup = cluster.HasValidBackup()
							go cluster.CheckCredentialRotation()
							cluster.CheckCanSaveDynamicConfig()
							cluster.CheckIsOverwrite()
							cluster.CheckAllBackupEstimatedSize()
							cluster.CheckAvailableCredit()
							cluster.CheckOpenSVCTresholds()
							cluster.JobsCheckSchedulerTable()
							cluster.CheckGlobalDeprecatedKeys()
							cluster.CheckClusterDeprecatedKeys()
						} else {
							cluster.StateMachine.PreserveState(pstates30...)
						}
						if !cluster.CanInitNodes {
							cluster.SetState("ERR00082", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00082"], cluster.errorInitNodes), ErrFrom: "OPENSVC"})
						}
						if !cluster.CanConnectVault {
							cluster.SetState("ERR00089", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00089"], cluster.errorConnectVault), ErrFrom: "OPENSVC"})
						}
						if cluster.StateMachine.GetHeartbeats()%3600 == 0 {
							// Set in parallel since it will wait for fetch to finish
							go cluster.RefreshAllAppTemplateMD5()
							cluster.ResticPurgeRepo(false)
							cluster.RefreshToolVersions()
							cluster.CheckBackupToolVersions()
						} else {
							// Preserve tools if not installed or has problem
							cluster.StateMachine.PreserveState(pstates3600...)
						}
						if cluster.SlavesOldestMasterFile.Suffix == 0 {
							go cluster.CheckSlavesReplicationsPurge()
						}
						cluster.PrintDelayStat()

						if cluster.Conf.GraphiteMetrics && cluster.StateMachine.GetHeartbeats()%5 == 0 {
							cluster.SendGraphiteMetrics()
							cluster.CheckDisksUsage()
						} else {
							cluster.StateMachine.PreserveState("WARN0139", "WARN0140")
						}
					} else {
						cluster.StateMachine.PreserveState("ERR00100")
					}

					wg.Wait()
				}

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
				// CheckFailed trigger failover code if passing all false positiv and constraints
				cluster.CheckFailed()
				cluster.IsConfigPathChange = cluster.HasConfigPathChanged()
				cluster.SetStatus()
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

			// if s.ErrKey == "WARN0073" {
			// 	for _, s := range cluster.Servers {
			// 		s.SetBackupPhysicalCookie()
			// 	}
			// }
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
			if s.ErrKey == "WARN0075" {
				/*
					This action is inactive due to direct function from Job
				*/
				// //Only mysqldump exists in the script
				// task := "reseed" + cluster.Conf.BackupLogicalType
				// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sending master logical backup to reseed %s", s.ServerUrl)
				// if master != nil {
				// 	if mybcksrv != nil {
				// 		go cluster.SSTRunSender(mybcksrv.GetMyBackupDirectory()+"mysqldump.sql.gz", servertoreseed, task)
				// 	} else {
				// 		go cluster.SSTRunSender(master.GetMasterBackupDirectory()+"mysqldump.sql.gz", servertoreseed, task)
				// 	}
				// } else {
				// 	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No master cancel backup reseeding %s", s.ServerUrl)
				// }
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
			if s.ErrKey == "WARN0077" {
				/*
					This action is inactive due to direct function from rejoin
				*/
				// //Only mysqldump exists in the script
				// task := "flashback" + cluster.Conf.BackupLogicalType
				// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sending logical backup to flashback reseed %s", s.ServerUrl)
				// if mybcksrv != nil {
				// 	go cluster.SSTRunSender(mybcksrv.GetMyBackupDirectory()+"mysqldump.sql.gz", servertoreseed, task)
				// } else {
				// 	go cluster.SSTRunSender(servertoreseed.GetMyBackupDirectory()+"mysqldump.sql.gz", servertoreseed, task)
				// }
			}
			/*
				// Unused, will be split to logical and physical backup. For rejoin will still use the same ReseedMasterSST
					if s.ErrKey == "WARN0101" {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cluster have backup")
						for _, srv := range cluster.Servers {
							if srv.HasWaitBackupCookie() {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Server %s was waiting for backup", srv.URL)
								go srv.ReseedMasterSST()
							}
						}
					}
			*/
			if s.ErrKey == "WARN0111" {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cluster have logical backup")
				for _, srv := range cluster.Servers {
					if srv.HasWaitLogicalBackupCookie() {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Server %s was waiting for logical backup", srv.URL)
						go func() {
							err := srv.JobReseedLogicalBackup("default")
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

		// trigger action on resolving states
		ostates := cluster.StateMachine.GetOpenStates()
		for _, s := range ostates {
			cluster.CheckCapture(s)
		}

		for _, s := range cluster.StateMachine.GetLastOpenedStates() {

			cluster.CheckAlert(s, false)
			cluster.BashScriptOpenSate(s)

		}

		cluster.StateMachine.ClearState()
		if cluster.StateMachine.GetHeartbeats()%60 == 0 {
			cluster.ConfigManager.SaveConfig(cluster, false)
		}
	}

	cluster.CheckSendMail()
}

func (cluster *Cluster) Stop() {
	cluster.Lock()
	defer cluster.Unlock()
	if cluster.ResticManager != nil {
		cluster.ResticManager.ShutdownWorker()
	}
	cluster.CloseRefreshTemplateMD5Worker()
	cluster.ConfigManager.SaveConfig(cluster, true)
	// prevent new cycle
	cluster.exit = true
}

func (cluster *Cluster) SetIsSavingConfig(val bool) {
	cluster.IsSavingConfig = val
}

type ClusterState struct {
	Servers    string      `json:"servers"`
	Crashes    crashList   `json:"crashes"`
	SLA        state.Sla   `json:"sla"`
	SLAHistory []state.Sla `json:"slaHistory"`
	IsAllDbUp  bool        `json:"provisioned"`
}

func (cluster *Cluster) Save() error {

	_, file, no, ok := runtime.Caller(1)
	if ok {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlDbg, "Saved called from %s#%d\n", file, no)
	}

	var clsave ClusterState
	clsave.Crashes = cluster.Crashes
	clsave.Servers = cluster.Conf.Hosts
	clsave.SLA = cluster.StateMachine.GetSla()
	clsave.IsAllDbUp = cluster.IsAllDbUp
	clsave.SLAHistory = cluster.SLAHistory

	saveJson, _ := json.MarshalIndent(clsave, "", "\t")
	err := os.WriteFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/clusterstate.json", saveJson, 0644)
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
	for key, _ := range cluster.Conf.ImmuableFlagMap {
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
	for key, _ := range cluster.Conf.ImmuableFlagMap {
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
	case "api-credentials":
		var tab_ApiUser []string
		lst_Users := strings.Split(cluster.Conf.Secrets["api-credentials"].Value, ",")
		for ind := range lst_Users {
			user_pass := strings.Split(lst_Users[ind], ":")
			if APIuser, ok := cluster.APIUsers[user_pass[0]]; ok {
				tab_ApiUser = append(tab_ApiUser, APIuser.User+":"+cluster.Conf.GetEncryptedString(APIuser.Password))
			}
		}
		return strings.Join(tab_ApiUser, ",")
	case "api-credentials-external":
		var tab_ApiUser []string
		lst_Users := strings.Split(cluster.Conf.Secrets["api-credentials-external"].Value, ",")
		for ind := range lst_Users {
			user_pass := strings.Split(lst_Users[ind], ":")
			if APIuser, ok := cluster.APIUsers[user_pass[0]]; ok {
				tab_ApiUser = append(tab_ApiUser, APIuser.User+":"+cluster.Conf.GetEncryptedString(APIuser.Password))
			}
		}
		return strings.Join(tab_ApiUser, ",")
	case "db-servers-credential":
		if cluster.Conf.IsPath(cluster.Conf.User) && cluster.Conf.IsVaultUsed() {
			return ""
		}
		return cluster.GetDbUser() + ":" + cluster.Conf.GetEncryptedString(cluster.GetDbPass())
	case "monitoring-write-heartbeat-credential":
		return cluster.GetMonitorWriteHearbeatUser() + ":" + cluster.Conf.GetEncryptedString(cluster.GetMonitorWriteHeartbeatPass())
	case "onpremise-ssh-credential":
		return cluster.GetOnPremiseSSHUser() + ":" + cluster.Conf.GetEncryptedString(cluster.GetOnPremiseSSHPass())

	case "replication-credential":
		if cluster.Conf.IsPath(cluster.Conf.RplUser) && cluster.Conf.IsVaultUsed() {
			return ""
		}
		return cluster.GetRplUser() + ":" + cluster.Conf.GetEncryptedString(cluster.GetRplPass())
	case "shardproxy-credential":
		if cluster.Conf.IsPath(cluster.Conf.MdbsProxyCredential) && cluster.Conf.IsVaultUsed() {
			return ""
		}
		return cluster.GetShardUser() + ":" + cluster.Conf.GetEncryptedString(cluster.GetShardPass())
	case "backup-restic-password":
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("backup-restic-password"))
	case "haproxy-password":
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("haproxy-password"))
	case "maxscale-pass":
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("maxscale-pass"))
	case "myproxy-password":
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("proxysql-password"))
	case "proxysql-password":
		if cluster.Conf.IsPath(cluster.Conf.ProxysqlPassword) && cluster.Conf.IsVaultUsed() {
			return ""
		}
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("proxysql-password"))
	case "proxyjanitor-password":
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("proxyjanitor-password"))
	case "vault-secret-id":
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("vault-secret-id"))
	case "opensvc-p12-secret":
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("opensvc-p12-secret"))
	case "backup-restic-aws-access-secret":
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("backup-restic-aws-access-secret"))
	case "backup-streaming-aws-access-secret":
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("backup-streaming-aws-access-secret"))
	case "arbitration-external-secret":
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("arbitration-external-secret"))
	case "alert-pushover-user-token":
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("alert-pushover-user-token"))
	case "alert-pushover-app-token":
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("alert-pushover-app-token"))
	case "mail-smtp-password":
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("mail-smtp-password"))
	case "api-oauth-client-secret":
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("api-oauth-client-secret"))
	case "cloud18-gitlab-password":
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("cloud18-gitlab-password"))
	case "cloud18-dba-user-credentials":
		return cluster.GetDbaUser() + ":" + cluster.Conf.GetEncryptedString(cluster.GetDbaPass())
	case "cloud18-sponsor-user-credentials":
		return cluster.GetSponsorUser() + ":" + cluster.Conf.GetEncryptedString(cluster.GetSponsorPass())
	case "git-acces-token":
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("git-acces-token"))
	case "vault-token":
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("vault-token"))
	default:
		return ""
	}
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

func (cluster *Cluster) MonitorVariablesDiff() {
	if !cluster.Conf.MonitorVariableDiff || cluster.GetMaster() == nil {
		return
	}
	masterVariables := cluster.GetMaster().Variables.ToNewMap()
	exceptVariables := map[string]bool{
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
	variablesdiff := ""
	var alldiff []VariableDiff
	for k, v := range masterVariables {
		var myvardiff VariableDiff
		var myvalues []Diff
		var mastervalue Diff
		mastervalue.Server = cluster.GetMaster().URL
		mastervalue.VariableValue = v
		myvalues = append(myvalues, mastervalue)
		for _, s := range cluster.slaves {
			slaveVariables := s.Variables.ToNewMap()
			if slaveVariables[k] != v && exceptVariables[k] != true {
				var slavevalue Diff
				slavevalue.Server = s.URL
				slavevalue.VariableValue = slaveVariables[k]
				myvalues = append(myvalues, slavevalue)
				variablesdiff += "+ Master Variable: " + k + " -> " + v + "\n"
				variablesdiff += "- Slave: " + s.URL + " -> " + slaveVariables[k] + "\n"
			}
		}
		if len(myvalues) > 1 {
			myvardiff.VariableName = k
			myvardiff.DiffValues = myvalues
			alldiff = append(alldiff, myvardiff)
		}
	}
	if variablesdiff != "" {
		cluster.DiffVariables = alldiff
		jtext, err := json.MarshalIndent(alldiff, " ", "\t")
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Encoding variables diff %s", err)
			return
		}
		cluster.SetState("WARN0084", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0084"], string(jtext)), ErrFrom: "MON", ServerUrl: cluster.GetMaster().URL})
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

	tables, tablelist, logs, err := dbhelper.GetTables(cmaster.Conn, cmaster.DBVersion, cluster.Conf.MonitorSchemaColumns, cluster.Conf.MonitorSchemaIndexes)
	cluster.LogSQL(logs, err, cmaster.URL, "Monitor", config.LvlDbg, "Could not fetch master tables %s", err)
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
		if err != nil {
			if err.Error() == "Empty" {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Init table %s", t.TableSchema+"."+t.TableName)
				haschanged = true
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "New table %s", t.TableSchema+"."+t.TableName)
				haschanged = true
			}
		} else {
			if oldtable.TableCrc != t.TableCrc {
				haschanged = true
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Change table %s", t.TableSchema+"."+t.TableName)
			}
			t.TableSync = oldtable.TableSync
		}

		// If shardproxy is enabled, check for duplicates in child clusters
		if cluster.Conf.MdbsProxyOn {
			for _, cl := range cluster.clusterList {
				if cl.Conf.MdbsProxyOn && cl.Conf.ClusterHead == cluster.Name {
					m := cl.GetMaster()
					if m != nil {
						cltbldef, _ := m.GetTableFromDict(t.TableSchema + "." + t.TableName)
						if cltbldef.TableName == t.TableName {
							duplicates = append(duplicates, cl.GetMaster())
							tableCluster = append(tableCluster, cl.GetName())
							cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Found duplicate table %s in %s", t.TableSchema+"."+t.TableName, cl.GetMaster().URL)
						}
					}
				}
			}
			t.TableClusters = strings.Join(tableCluster, ",")
			tables[t.TableSchema+"."+t.TableName] = t

			if haschanged {
				for _, pri := range cluster.Proxies {
					if prx, ok := pri.(*MariadbShardProxy); ok {
						if !(t.TableSchema == "replication_manager_schema" || strings.Contains(t.TableName, "_copy") == true || strings.Contains(t.TableName, "_back") == true || strings.Contains(t.TableName, "_old") == true || strings.Contains(t.TableName, "_reshard") == true) {
							cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "blabla table %s %s %s", duplicates, t.TableSchema, t.TableName)
							cluster.ShardProxyCreateVTable(prx, t.TableSchema, t.TableName, duplicates, false)
						}
					}
				}
			}
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
	tables, tablelist, logs, err := dbhelper.GetTables(sl.Conn, sl.DBVersion, cluster.Conf.MonitorSchemaColumns, cluster.Conf.MonitorSchemaIndexes)
	cluster.LogSQL(logs, err, sl.URL, "Monitor", config.LvlDbg, "Could not fetch slave tables %s", err)
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

	for tblname, mtbl := range masterTables {
		if cluster.IsInSchemaIgnore(tblname) {
			ignored = append(ignored, tblname)
			continue
		}
		stbl, ok := slTables[tblname]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("Table %s missing on slave %s", tblname, sl.URL))
			continue
		}
		if mtbl.TableCrc != stbl.TableCrc {
			tbldiffs := make([]string, 0)
			if mtbl.TableColumnsCrc64 != stbl.TableColumnsCrc64 {
				tbldiffs = append(tbldiffs, "columns: (", strings.Join(mtbl.ColumnDiffs(stbl, sl.URL), ", "), ") ")
			}
			if mtbl.TableIndexesCrc64 != stbl.TableIndexesCrc64 {
				tbldiffs = append(tbldiffs, "indexes: (", strings.Join(mtbl.IndexDiffs(stbl, sl.URL), ", "), ") ")
			}
			diffs = append(diffs, fmt.Sprintf("Table %s differs on slave %s -> %s", tblname, sl.URL, strings.Join(tbldiffs, " ")))
		}
	}

	for tblname, _ := range slTables {
		_, ok := masterTables[tblname]
		if !ok {
			if cluster.IsInSchemaIgnore(tblname) {
				ignored = append(ignored, tblname)
				continue
			}
			ignored = append(ignored, fmt.Sprintf("Extra table %s found on slave %s", tblname, sl.URL))
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
	if !cluster.Conf.MonitorSchemaChange {
		return
	}

	if cluster.StateMachine.IsInSchemaMonitor() {
		return
	}

	loglevel := config.LvlInfo
	// Shardproxy will increase the intensity of monitoring, so set to debug
	if cluster.Conf.MdbsProxyOn {
		loglevel = config.LvlDbg
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, loglevel, "Starting schema monitoring")

	cluster.StateMachine.SetMonitorSchemaState()
	defer cluster.StateMachine.RemoveMonitorSchemaState()

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
					duplicates := strings.Split(clrule.Proxies, ",")
					found := false
					for _, prxid := range duplicates {
						if prx.Id == prxid {
							found = true
						}
					}
					if !found {
						duplicates = append(duplicates, prx.Id)
					}
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
	if cluster.Conf.ArbitrationFailedMasterScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Calling abitration failed for master script")
		out, err := exec.Command(cluster.Conf.ArbitrationFailedMasterScript, cluster.GetMaster().Host, cluster.GetMaster().Port).CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s", err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Arbitration failed master script complete: %s", string(out))
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Arbitration failed attaching failed master %s to electected master :%s", cluster.GetMaster().URL, realmaster.URL)
		logs, err := cluster.GetMaster().SetReplicationGTIDCurrentPosFromServer(realmaster)
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

func (c *Cluster) AddApp(app *App) {
	app.SetCluster(c)
	app.SetID()
	app.SetDataDir()
	app.SetServiceName(c.Name)
	app.SetDefaultRoute(c.Conf.Cloud18Domain, c.Conf.Cloud18SubDomain, c.Conf.Cloud18SubDomainZone, c.Name)
	c.LogModulePrintf(c.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "New application monitored %s: %s:%s", app.GetType(), app.GetHost(), app.GetPort())
	app.SetState(stateSuspect)
	c.Apps = append(c.Apps, app)

	if app.AppConfig.ProvAppCreditPlanned == 0 {
		app.AppConfig.ProvAppCreditPlanned = len(app.GetAppAgents())
	}

	if app.AppConfig.ProvAppCreditPlanned > app.AppConfig.ProvAppCreditUsed {
		c.Conf.Cloud18ApplicationCreditsUsed += app.AppConfig.ProvAppCreditPlanned
		if app.HasProvisionCookie() {
			app.SetReprovCookie()
		}
	} else {
		c.Conf.Cloud18ApplicationCreditsUsed += app.AppConfig.ProvAppCreditUsed
	}
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
