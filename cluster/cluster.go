// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc64"
	"io/ioutil"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/bluele/logrus_slack"
	"github.com/go-git/go-git/v5"
	git_obj "github.com/go-git/go-git/v5/plumbing/object"
	vault "github.com/hashicorp/vault/api"

	git_https "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/pelletier/go-toml"
	"github.com/signal18/replication-manager/cluster/configurator"
	"github.com/signal18/replication-manager/cluster/nbc"
	"github.com/signal18/replication-manager/config"
	v3 "github.com/signal18/replication-manager/repmanv3"
	"github.com/signal18/replication-manager/router/maxscale"
	"github.com/signal18/replication-manager/utils/alert"
	"github.com/signal18/replication-manager/utils/cron"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/logrus/hooks/pushover"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
	log "github.com/sirupsen/logrus"
	logsql "github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
)

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
	Name                          string                `json:"name"`
	Tenant                        string                `json:"tenant"`
	WorkingDir                    string                `json:"workingDir"`
	Servers                       serverList            `json:"-"`
	ServerIdList                  []string              `json:"dbServers"`
	Crashes                       crashList             `json:"dbServersCrashes"`
	Proxies                       proxyList             `json:"-"`
	ProxyIdList                   []string              `json:"proxyServers"`
	FailoverCtr                   int                   `json:"failoverCounter"`
	FailoverTs                    int64                 `json:"failoverLastTime"`
	Status                        string                `json:"activePassiveStatus"`
	IsSplitBrain                  bool                  `json:"isSplitBrain"`
	IsSplitBrainBck               bool                  `json:"-"`
	IsFailedArbitrator            bool                  `json:"isFailedArbitrator"`
	IsLostMajority                bool                  `json:"isLostMajority"`
	IsDown                        bool                  `json:"isDown"`
	IsClusterDown                 bool                  `json:"isClusterDown"`
	IsAllDbUp                     bool                  `json:"isAllDbUp"`
	IsFailable                    bool                  `json:"isFailable"`
	IsPostgres                    bool                  `json:"isPostgres"`
	IsProvision                   bool                  `json:"isProvision"`
	IsNeedProxiesRestart          bool                  `json:"isNeedProxyRestart"`
	IsNeedProxiesReprov           bool                  `json:"isNeedProxiesRestart"`
	IsNeedDatabasesRestart        bool                  `json:"isNeedDatabasesRestart"`
	IsNeedDatabasesRollingRestart bool                  `json:"isNeedDatabasesRollingRestart"`
	IsNeedDatabasesRollingReprov  bool                  `json:"isNeedDatabasesRollingReprov"`
	IsNeedDatabasesReprov         bool                  `json:"isNeedDatabasesReprov"`
	IsValidBackup                 bool                  `json:"isValidBackup"`
	IsNotMonitoring               bool                  `json:"isNotMonitoring"`
	IsCapturing                   bool                  `json:"isCapturing"`
	Conf                          config.Config         `json:"config"`
	Confs                         *config.ConfVersion   `json:"-"`
	CleanAll                      bool                  `json:"cleanReplication"` //used in testing
	Topology                      string                `json:"topology"`
	Uptime                        string                `json:"uptime"`
	UptimeFailable                string                `json:"uptimeFailable"`
	UptimeSemiSync                string                `json:"uptimeSemisync"`
	MonitorSpin                   string                `json:"monitorSpin"`
	WorkLoad                      WorkLoad              `json:"workLoad"`
	LogPushover                   *log.Logger           `json:"-"`
	Log                           s18log.HttpLog        `json:"log"`
	LogSlack                      *log.Logger           `json:"-"`
	JobResults                    map[string]*JobResult `json:"jobResults"`
	Grants                        map[string]string     `json:"-"`
	tlog                          *s18log.TermLog       `json:"-"`
	htlog                         *s18log.HttpLog       `json:"-"`
	SQLGeneralLog                 s18log.HttpLog        `json:"sqlGeneralLog"`
	SQLErrorLog                   s18log.HttpLog        `json:"sqlErrorLog"`
	MonitorType                   map[string]string     `json:"monitorType"`
	TopologyType                  map[string]string     `json:"topologyType"`
	FSType                        map[string]bool       `json:"fsType"`
	DiskType                      map[string]string     `json:"diskType"`
	VMType                        map[string]bool       `json:"vmType"`
	Agents                        []Agent               `json:"agents"`
	hostList                      []string              `json:"-"`
	proxyList                     []string              `json:"-"`
	clusterList                   map[string]*Cluster   `json:"-"`
	slaves                        serverList            `json:"slaves"`
	master                        *ServerMonitor        `json:"master"`
	oldMaster                     *ServerMonitor        `json:"oldmaster"`
	vmaster                       *ServerMonitor        `json:"vmaster"`
	mxs                           *maxscale.MaxScale    `json:"-"`
	//dbUser                        string                      `json:"-"`
	//oldDbUser string `json:"-"`
	//dbPass                        string                      `json:"-"`
	//oldDbPass string `json:"-"`
	//rplUser                   string                      `json:"-"`
	//rplPass                   string                      `json:"-"`
	//proxysqlUser              string                      `json:"-"`
	//proxysqlPass              string                      `json:"-"`
	StateMachine              *state.StateMachine         `json:"stateMachine"`
	runOnceAfterTopology      bool                        `json:"-"`
	logPtr                    *os.File                    `json:"-"`
	termlength                int                         `json:"-"`
	runUUID                   string                      `json:"-"`
	cfgGroupDisplay           string                      `json:"-"`
	repmgrVersion             string                      `json:"-"`
	repmgrHostname            string                      `json:"-"`
	exitMsg                   string                      `json:"-"`
	exit                      bool                        `json:"-"`
	canFlashBack              bool                        `json:"-"`
	failoverCond              *nbc.NonBlockingChan        `json:"-"`
	switchoverCond            *nbc.NonBlockingChan        `json:"-"`
	rejoinCond                *nbc.NonBlockingChan        `json:"-"`
	bootstrapCond             *nbc.NonBlockingChan        `json:"-"`
	altertableCond            *nbc.NonBlockingChan        `json:"-"`
	addtableCond              *nbc.NonBlockingChan        `json:"-"`
	statecloseChan            chan state.State            `json:"-"`
	switchoverChan            chan bool                   `json:"-"`
	errorChan                 chan error                  `json:"-"`
	testStopCluster           bool                        `json:"-"`
	testStartCluster          bool                        `json:"-"`
	lastmaster                *ServerMonitor              `json:"-"`
	benchmarkType             string                      `json:"-"`
	HaveDBTLSCert             bool                        `json:"haveDBTLSCert"`
	HaveDBTLSOldCert          bool                        `json:"haveDBTLSOldCert"`
	tlsconf                   *tls.Config                 `json:"-"`
	tlsoldconf                *tls.Config                 `json:"-"`
	tunnel                    *ssh.Client                 `json:"-"`
	QueryRules                map[uint32]config.QueryRule `json:"-"`
	Backups                   []v3.Backup                 `json:"-"`
	SLAHistory                []state.Sla                 `json:"slaHistory"`
	APIUsers                  map[string]APIUser          `json:"apiUsers"`
	Schedule                  map[string]cron.Entry       `json:"-"`
	scheduler                 *cron.Cron                  `json:"-"`
	idSchedulerPhysicalBackup cron.EntryID                `json:"-"`
	idSchedulerLogicalBackup  cron.EntryID                `json:"-"`
	idSchedulerOptimize       cron.EntryID                `json:"-"`
	idSchedulerErrorLogs      cron.EntryID                `json:"-"`
	idSchedulerLogRotateTable cron.EntryID                `json:"-"`
	idSchedulerSLARotate      cron.EntryID                `json:"-"`
	idSchedulerRollingRestart cron.EntryID                `json:"-"`
	idSchedulerDbsjobsSsh     cron.EntryID                `json:"-"`
	idSchedulerRollingReprov  cron.EntryID                `json:"-"`
	WaitingRejoin             int                         `json:"waitingRejoin"`
	WaitingSwitchover         int                         `json:"waitingSwitchover"`
	WaitingFailover           int                         `json:"waitingFailover"`
	Configurator              configurator.Configurator   `json:"configurator"`
	DiffVariables             []VariableDiff              `json:"diffVariables"`
	inInitNodes               bool                        `json:"-"`
	CanInitNodes              bool                        `json:"canInitNodes"`
	errorInitNodes            error                       `json:"-"`
	inConnectVault            bool                        `json:"-"`
	CanConnectVault           bool                        `json:"canConnectVault"`
	errorConnectVault         error                       `json:"-"`
	SqlErrorLog               *logsql.Logger              `json:"-"`
	SqlGeneralLog             *logsql.Logger              `json:"-"`
	SstAvailablePorts         map[string]string           `json:"sstAvailablePorts"`
	sync.Mutex
	crcTable *crc64.Table
}

type ClusterSorter []*Cluster

func (a ClusterSorter) Len() int           { return len(a) }
func (a ClusterSorter) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ClusterSorter) Less(i, j int) bool { return a[i].Name < a[j].Name }

type QueryRuleSorter []config.QueryRule

func (a QueryRuleSorter) Len() int           { return len(a) }
func (a QueryRuleSorter) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a QueryRuleSorter) Less(i, j int) bool { return a[i].Id < a[j].Id }

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

type WorkLoad struct {
	DBTableSize   int64   `json:"dbTableSize"`
	DBIndexSize   int64   `json:"dbIndexSize"`
	Connections   int     `json:"connections"`
	QPS           int64   `json:"qps"`
	CpuThreadPool float64 `json:"cpuThreadPool"`
	CpuUserStats  float64 `json:"cpuUserStats"`
	BusyTime      string
}

type Alerts struct {
	Errors   []state.StateHttp `json:"errors"`
	Warnings []state.StateHttp `json:"warnings"`
}

type JobResult struct {
	Xtrabackup            bool `json:"xtrabackup"`
	Mariabackup           bool `json:"mariabackup"`
	Zfssnapback           bool `json:"zfssnapback"`
	Optimize              bool `json:"optimize"`
	Reseedxtrabackup      bool `json:"reseedxtrabackup"`
	Reseedmariabackup     bool `json:"reseedmariabackup"`
	Reseedmysqldump       bool `json:"reseedmysqldump"`
	Flashbackxtrabackup   bool `json:"flashbackxtrabackup"`
	Flashbackmariadbackup bool `json:"flashbackmariadbackup"`
	Flashbackmysqldump    bool `json:"flashbackmysqldump"`
	Stop                  bool `json:"stop"`
	Start                 bool `json:"start"`
	Restart               bool `json:"restart"`
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

// Init initial cluster definition
func (cluster *Cluster) Init(confs *config.ConfVersion, cfgGroup string, tlog *s18log.TermLog, loghttp *s18log.HttpLog, termlength int, runUUID string, repmgrVersion string, repmgrHostname string) error {
	cluster.Confs = confs

	cluster.Conf = confs.ConfInit
	k, _ := cluster.Conf.LoadEncrytionKey()
	if k == nil {
		cluster.LogPrintf(LvlInfo, "No existing password encryption key")
		cluster.SetState("ERR00090", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["ERR00090"]), ErrFrom: "CLUSTER"})
	}
	cluster.tlog = tlog
	cluster.htlog = loghttp
	cluster.termlength = termlength
	cluster.Name = cfgGroup

	cluster.runUUID = runUUID
	cluster.repmgrHostname = repmgrHostname
	cluster.repmgrVersion = repmgrVersion

	cluster.InitFromConf()

	return nil
}

func (cluster *Cluster) InitFromConf() {
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
	cluster.CanConnectVault = true
	cluster.runOnceAfterTopology = true
	cluster.testStopCluster = true
	cluster.testStartCluster = true

	cluster.WorkingDir = cluster.Conf.WorkingDir + "/" + cluster.Name
	if cluster.Conf.Arbitration {
		cluster.Status = ConstMonitorStandby
	} else {
		cluster.Status = ConstMonitorActif
	}
	cluster.benchmarkType = "sysbench"
	cluster.Log = s18log.NewHttpLog(200)

	cluster.MonitorType = cluster.Conf.GetMonitorType()
	cluster.TopologyType = cluster.Conf.GetTopologyType()
	cluster.FSType = cluster.Conf.GetFSType()
	cluster.DiskType = cluster.Conf.GetDiskType()
	cluster.VMType = cluster.Conf.GetVMType()
	cluster.Grants = cluster.Conf.GetGrantType()

	cluster.QueryRules = make(map[uint32]config.QueryRule)
	cluster.Schedule = make(map[string]cron.Entry)
	cluster.JobResults = make(map[string]*JobResult)
	cluster.SstAvailablePorts = make(map[string]string)
	lstPort := strings.Split(cluster.Conf.SchedulerSenderPorts, ",")
	for _, p := range lstPort {
		cluster.SstAvailablePorts[p] = p
	}

	// Initialize the state machine at this stage where everything is fine.
	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()

	cluster.SetClusterCredentialsFromConfig()

	if cluster.Conf.Interactive {
		cluster.LogPrintf(LvlInfo, "Failover in interactive mode")
	} else {
		cluster.LogPrintf(LvlInfo, "Failover in automatic mode")
	}
	if _, err := os.Stat(cluster.WorkingDir); os.IsNotExist(err) {
		os.MkdirAll(cluster.Conf.WorkingDir+"/"+cluster.Name, os.ModePerm)
	}
	cluster.LogPushover = log.New()
	cluster.LogPushover.SetFormatter(&log.TextFormatter{FullTimestamp: true})

	if cluster.Conf.PushoverAppToken != "" && cluster.Conf.PushoverUserToken != "" {
		cluster.LogPushover.AddHook(
			pushover.NewHook(cluster.Conf.GetDecryptedValue("alert-pushover-app-token"), cluster.Conf.GetDecryptedValue("alert-pushover-user-token")),
		)
		cluster.LogPushover.SetLevel(log.WarnLevel)
	}

	cluster.LogSlack = log.New()
	cluster.LogSlack.SetFormatter(&log.TextFormatter{FullTimestamp: true})

	if cluster.Conf.SlackURL != "" {
		cluster.LogSlack.AddHook(&logrus_slack.SlackHook{
			HookURL:        cluster.Conf.SlackURL,
			AcceptedLevels: logrus_slack.LevelThreshold(log.WarnLevel),
			Channel:        cluster.Conf.SlackChannel,
			IconEmoji:      ":ghost:",
			Username:       cluster.Conf.SlackUser,
			Timeout:        5 * time.Second, // request timeout for calling slack api
		})
	}
	cluster.LogPrintf("START", "Replication manager started with version: %s", cluster.Conf.Version)

	if cluster.Conf.MailTo != "" {
		msg := "Replication manager started with version: " + cluster.Conf.Version
		subj := "Replication-Manager started"
		alert := alert.Alert{}
		go alert.EmailMessage(msg, subj, cluster.Conf)
	}

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
	cluster.LoadAPIUsers()

	cluster.GetPersitentState()

	err = cluster.newServerList()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Could not set server list %s", err)
	}
	err = cluster.newProxyList()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Could not set proxy list %s", err)
	}
	//Loading configuration compliances
	err = cluster.Configurator.Init(cluster.Conf)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Could not initialize configurator %s", err)
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

}

func (cluster *Cluster) initOrchetratorNodes() {
	if cluster.inInitNodes {
		return
	}
	cluster.inInitNodes = true
	defer func() { cluster.inInitNodes = false }()

	//defer cluster.insideInitNodes = false
	//cluster.LogPrintf(LvlInfo, "Loading nodes from orchestrator %s", cluster.Conf.ProvOrchestrator)
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
	default:
		log.Fatalln("prov-orchestrator not supported", cluster.Conf.ProvOrchestrator)
	}

}

func (cluster *Cluster) initScheduler() {
	if cluster.Conf.MonitorScheduler {
		cluster.LogPrintf(LvlInfo, "Starting cluster scheduler")
		if cluster.scheduler != nil {
			cluster.scheduler.Stop()
		}
		cluster.scheduler = cron.New()
		cluster.SetSchedulerBackupLogical()
		cluster.SetSchedulerLogsTableRotate()
		cluster.SetSchedulerBackupPhysical()
		cluster.SetSchedulerBackupLogs()
		cluster.SetSchedulerOptimize()
		cluster.SetSchedulerRollingRestart()
		cluster.SetSchedulerRollingReprov()
		cluster.SetSchedulerSlaRotate()
		cluster.SetSchedulerRollingRestart()
		cluster.SetSchedulerDbJobsSsh()
		cluster.scheduler.Start()
	}

}

func (cluster *Cluster) Run() {
	interval := time.Second

	// createKeys do nothing yet
	if _, err := os.Stat(cluster.Conf.WorkingDir + "/" + cluster.Name + "/ca-key.pem"); os.IsNotExist(err) {
		go cluster.createKeys()
	}

	for cluster.exit == false {
		if !cluster.Conf.MonitorPause {
			cluster.ServerIdList = cluster.GetDBServerIdList()
			cluster.ProxyIdList = cluster.GetProxyServerIdList()

			select {
			case sig := <-cluster.switchoverChan:
				if sig {
					if cluster.Status == "A" {
						cluster.LogPrintf(LvlInfo, "Signaling Switchover...")
						cluster.MasterFailover(false)
						cluster.switchoverCond.Send <- true
					} else {
						cluster.LogPrintf(LvlInfo, "Not in active mode, cancel switchover %s", cluster.Status)
					}
				}

			default:
				if cluster.Conf.LogLevel > 2 {
					cluster.LogPrintf(LvlDbg, "Monitoring server loop")
					if cluster.Servers[0] != nil {
						cluster.LogPrintf(LvlDbg, "Servers not nil : %v\n", cluster.Servers)
						for k, v := range cluster.Servers {
							cluster.LogPrintf(LvlDbg, "Servers loops k : %d, url : %s, state : %s, prevstate %s", k, v.URL, v.State, v.PrevState)
							cluster.LogPrintf(LvlDbg, "Server [%d]: URL: %-15s State: %6s PrevState: %6s", k, v.URL, v.State, v.PrevState)
						}
						if cluster.GetMaster() != nil {
							cluster.LogPrintf(LvlDbg, "Master [ ]: URL: %-15s State: %6s PrevState: %6s", cluster.master.URL, cluster.GetMaster().State, cluster.GetMaster().PrevState)
							for k, v := range cluster.slaves {
								cluster.LogPrintf(LvlDbg, "Slave  [%d]: URL: %-15s State: %6s PrevState: %6s", k, v.URL, v.State, v.PrevState)
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
					cluster.ResticFetchRepo()
					cluster.runOnceAfterTopology = false
				} else {

					// Preserved server state in proxy during reload config
					if !cluster.IsInFailover() {
						wg.Add(1)
						go cluster.refreshProxies(wg)
					}
					if cluster.StateMachine.SchemaMonitorEndTime+60 < time.Now().Unix() && !cluster.StateMachine.IsInSchemaMonitor() {
						go cluster.MonitorSchema()
					}
					if cluster.Conf.TestInjectTraffic || cluster.Conf.AutorejoinSlavePositionalHeartbeat || cluster.Conf.MonitorWriteHeartbeat {
						cluster.InjectProxiesTraffic()
					}
					if cluster.StateMachine.GetHeartbeats()%30 == 0 {
						go cluster.initOrchetratorNodes()
						cluster.MonitorQueryRules()
						cluster.MonitorVariablesDiff()
						cluster.ResticFetchRepo()
						cluster.IsValidBackup = cluster.HasValidBackup()
						go cluster.CheckCredentialRotation()
						cluster.CheckCanSaveDynamicConfig()
						cluster.CheckIsOverwrite()

					} else {
						cluster.StateMachine.PreserveState("WARN0093")
						cluster.StateMachine.PreserveState("WARN0084")
						cluster.StateMachine.PreserveState("WARN0095")
						cluster.StateMachine.PreserveState("WARN0101")
						cluster.StateMachine.PreserveState("ERR00090")
						cluster.StateMachine.PreserveState("WARN0102")
					}
					if !cluster.CanInitNodes {
						cluster.SetState("ERR00082", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00082"], cluster.errorInitNodes), ErrFrom: "OPENSVC"})
					}
					if !cluster.CanConnectVault {
						cluster.SetState("ERR00089", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00089"], cluster.errorConnectVault), ErrFrom: "OPENSVC"})
					}

					if cluster.StateMachine.GetHeartbeats()%36000 == 0 {
						cluster.ResticPurgeRepo()
					} else {
						cluster.StateMachine.PreserveState("WARN0094")
					}
				}
				wg.Wait()
				// AddChildServers can't be done before TopologyDiscover but need a refresh aquiring more fresh gtid vs current cluster so elelection win but server is ignored see electFailoverCandidate
				err := cluster.AddChildServers()

				if err != nil {
					cluster.LogPrintf(LvlDbg, "Fail of AddChildServers %s", err)
				}

				cluster.IsFailable = cluster.GetStatus()
				// CheckFailed trigger failover code if passing all false positiv and constraints
				cluster.CheckFailed()

				cluster.Topology = cluster.GetTopology()
				cluster.SetStatus()
				cluster.StateProcessing()

			}
		}
		time.Sleep(interval * time.Duration(cluster.Conf.MonitoringTicker))

	}
}

func (cluster *Cluster) StateProcessing() {
	if !cluster.StateMachine.IsInFailover() {
		// trigger action on resolving states
		cstates := cluster.StateMachine.GetResolvedStates()
		mybcksrv := cluster.GetBackupServer()
		master := cluster.GetMaster()
		for _, s := range cstates {
			servertoreseed := cluster.GetServerFromURL(s.ServerUrl)
			if s.ErrKey == "WARN0073" {
				for _, s := range cluster.Servers {
					s.SetBackupPhysicalCookie()
				}
			}
			if s.ErrKey == "WARN0074" {
				cluster.LogPrintf(LvlInfo, "Sending master physical backup to reseed %s", s.ServerUrl)
				if master != nil {
					if mybcksrv != nil {
						go cluster.SSTRunSender(mybcksrv.GetMyBackupDirectory()+cluster.Conf.BackupPhysicalType+".xbtream", servertoreseed)
					} else {
						go cluster.SSTRunSender(master.GetMasterBackupDirectory()+cluster.Conf.BackupPhysicalType+".xbtream", servertoreseed)
					}
				} else {
					cluster.LogPrintf(LvlErr, "No master cancel backup reseeding %s", s.ServerUrl)
				}
			}
			if s.ErrKey == "WARN0075" {
				cluster.LogPrintf(LvlInfo, "Sending master logical backup to reseed %s", s.ServerUrl)
				if master != nil {
					if mybcksrv != nil {
						go cluster.SSTRunSender(mybcksrv.GetMyBackupDirectory()+"mysqldump.sql.gz", servertoreseed)
					} else {
						go cluster.SSTRunSender(master.GetMasterBackupDirectory()+"mysqldump.sql.gz", servertoreseed)
					}
				} else {
					cluster.LogPrintf(LvlErr, "No master cancel backup reseeding %s", s.ServerUrl)
				}
			}
			if s.ErrKey == "WARN0076" {
				cluster.LogPrintf(LvlInfo, "Sending server physical backup to flashback reseed %s", s.ServerUrl)
				if mybcksrv != nil {
					go cluster.SSTRunSender(mybcksrv.GetMyBackupDirectory()+cluster.Conf.BackupPhysicalType+".xbtream", servertoreseed)
				} else {
					go cluster.SSTRunSender(servertoreseed.GetMyBackupDirectory()+cluster.Conf.BackupPhysicalType+".xbtream", servertoreseed)
				}
			}
			if s.ErrKey == "WARN0077" {

				cluster.LogPrintf(LvlInfo, "Sending logical backup to flashback reseed %s", s.ServerUrl)
				if mybcksrv != nil {
					go cluster.SSTRunSender(mybcksrv.GetMyBackupDirectory()+"mysqldump.sql.gz", servertoreseed)
				} else {
					go cluster.SSTRunSender(servertoreseed.GetMyBackupDirectory()+"mysqldump.sql.gz", servertoreseed)
				}
			}
			if s.ErrKey == "WARN0101" {
				cluster.LogPrintf(LvlInfo, "Cluster have backup")
				for _, srv := range cluster.Servers {
					if srv.HasWaitBackupCookie() {
						cluster.LogPrintf(LvlInfo, "Server %s was waiting for backup", srv.URL)
						go srv.ReseedMasterSST()
					}
				}

			}
			//		cluster.statecloseChan <- s
		}
		var states []string
		if cluster.runOnceAfterTopology {
			states = cluster.StateMachine.GetFirstStates()

		} else {
			states = cluster.StateMachine.GetStates()
		}
		for i := range states {
			cluster.LogPrintf("STATE", states[i])
		}
		// trigger action on resolving states
		ostates := cluster.StateMachine.GetOpenStates()
		for _, s := range ostates {
			cluster.CheckCapture(s)
		}

		for _, s := range cluster.StateMachine.GetLastOpenedStates() {

			cluster.CheckAlert(s)

		}

		cluster.StateMachine.ClearState()
		if cluster.StateMachine.GetHeartbeats()%60 == 0 {
			cluster.Save()
		}

	}
}

func (cluster *Cluster) Stop() {
	//	cluster.scheduler.Stop()
	cluster.Save()
	cluster.exit = true

}

func (cluster *Cluster) Save() error {
	_, file, no, ok := runtime.Caller(1)
	if ok {
		cluster.LogPrintf(LvlInfo, "Saved called from %s#%d\n", file, no)
	}
	type Save struct {
		Servers    string      `json:"servers"`
		Crashes    crashList   `json:"crashes"`
		SLA        state.Sla   `json:"sla"`
		SLAHistory []state.Sla `json:"slaHistory"`
		IsAllDbUp  bool        `json:"provisioned"`
	}

	var clsave Save
	clsave.Crashes = cluster.Crashes
	clsave.Servers = cluster.Conf.Hosts
	clsave.SLA = cluster.StateMachine.GetSla()
	clsave.IsAllDbUp = cluster.IsAllDbUp
	clsave.SLAHistory = cluster.SLAHistory

	saveJson, _ := json.MarshalIndent(clsave, "", "\t")
	err := ioutil.WriteFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/clusterstate.json", saveJson, 0644)
	if err != nil {
		return err
	}

	saveQeueryRules, _ := json.MarshalIndent(cluster.QueryRules, "", "\t")
	err = ioutil.WriteFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/queryrules.json", saveQeueryRules, 0644)
	if err != nil {
		return err
	}

	if cluster.Conf.ConfRewrite {
		//clone git repository in case its the first time
		if cluster.Conf.GitUrl != "" {
			cluster.Conf.CloneConfigFromGit(cluster.Conf.GitUrl, cluster.Conf.GitUsername, cluster.Conf.GetDecryptedValue("git-acces-token"), cluster.GetConf().WorkingDir)
		}

		//fmt.Printf("SAVE CLUSTER \n")
		//cluster.Conf.PrintConf()
		var myconf = make(map[string]config.Config)

		myconf["saved-"+cluster.Name] = cluster.Conf

		file, err := os.OpenFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/"+cluster.Name+".toml", os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
		if err != nil {
			if os.IsPermission(err) {
				cluster.LogPrintf(LvlInfo, "File permission denied: %s", cluster.Conf.WorkingDir+"/"+cluster.Name+"/"+cluster.Name+".toml")
			}
			return err
		}
		defer file.Close()

		readconf, _ := toml.Marshal(cluster.Conf)
		t, _ := toml.LoadBytes(readconf)
		s := t
		keys := t.Keys()
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
				}
			}
		}
		//to encrypt credentials before writting in the config file
		file.WriteString("[saved-" + cluster.Name + "]\ntitle = \"" + cluster.Name + "\" \n")
		s.WriteTo(file)
		//fmt.Printf("SAVE CLUSTER IMMUABLE MAP : %s", cluster.Conf.ImmuableFlagMap)
		//fmt.Printf("SAVE CLUSTER DYNAMIC MAP : %s", cluster.DynamicFlagMap)

		//to load the new generated config file in github
		if cluster.Conf.GitUrl != "" {
			cluster.PushConfigToGit(cluster.Conf.GetDecryptedValue("git-acces-token"), cluster.Conf.GitUsername, cluster.GetConf().WorkingDir, cluster.Name)
		}

		err = cluster.Overwrite()
		if err != nil {
			cluster.LogPrintf(LvlInfo, "Error during Overwriting: %s", err)
		}
	}

	return nil
}

func (cluster *Cluster) PushConfigToGit(tok string, user string, dir string, name string) {
	if cluster.Conf.LogGit {
		cluster.LogPrintf(LvlInfo, "Push to git : tok %s, dir %s, name %s\n", cluster.Conf.PrintSecret(tok), dir, name)
	}
	auth := &git_https.BasicAuth{
		Username: user, // yes, this can be anything except an empty string
		Password: tok,
	}
	path := dir
	r, err := git.PlainOpen(path)
	if err != nil && cluster.Conf.LogGit {
		cluster.LogPrintf(LvlInfo, "Git error : cannot PlainOpen : %s", err)
	}

	w, err := r.Worktree()
	if err != nil && cluster.Conf.LogGit {
		cluster.LogPrintf(LvlInfo, "Git error : cannot Worktree : %s", err)
	}

	msg := "Update " + name + ".toml file"

	// Adds the new file to the staging area.
	_, err = w.Add(name)
	if err != nil && cluster.Conf.LogGit {
		log.Errorf("Git error : cannot Add %s : %s", name+".toml", err)
	}
	_, err = w.Add(name + "/" + name + ".toml")
	if err != nil && cluster.Conf.LogGit {
		cluster.LogPrintf(LvlInfo, "Git error : cannot Add %s : %s", name+"/"+name+".toml", err)
	}

	_, err = w.Commit(msg, &git.CommitOptions{
		Author: &git_obj.Signature{
			Name: "Replication-manager",
			When: time.Now(),
		},
	})

	if err != nil && cluster.Conf.LogGit {
		log.Errorf("Git error : cannot Commit : %s", err)
	}

	// push using default options
	err = r.Push(&git.PushOptions{Auth: auth})
	if err != nil && cluster.Conf.LogGit {
		cluster.LogPrintf(LvlInfo, "Git error : cannot Push : %s", err)

	}
}

func (cluster *Cluster) Overwrite() error {

	if cluster.Conf.ConfRewrite {
		var myconf = make(map[string]config.Config)

		myconf["overwrite-"+cluster.Name] = cluster.Conf

		file, err := os.OpenFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/overwrite.toml", os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
		if err != nil {
			if os.IsPermission(err) {
				cluster.LogPrintf(LvlInfo, "File permission denied: %s", cluster.Conf.WorkingDir+"/"+cluster.Name+"/overwrite.toml")
			}
			return err
		}
		defer file.Close()

		readconf, _ := toml.Marshal(cluster.Conf)
		t, _ := toml.LoadBytes(readconf)
		s := t
		keys := t.Keys()
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
	}

	return nil
}

func (cluster *Cluster) GetEncryptedValueFromMemory(key string) string {
	switch key {
	case "api-credentials":
		var tab_ApiUser []string
		lst_Users := strings.Split(cluster.Conf.Secrets["api-credentials"].Value, ",")
		for ind := range lst_Users {
			user_pass := strings.Split(lst_Users[ind], ":")
			APIuser := cluster.APIUsers[user_pass[0]]
			tab_ApiUser = append(tab_ApiUser, APIuser.User+":"+cluster.Conf.GetEncryptedString(APIuser.Password))

		}

		return strings.Join(tab_ApiUser, ",")
	case "api-credentials-external":
		var tab_ApiUser []string
		lst_Users := strings.Split(cluster.Conf.Secrets["api-credentials-external"].Value, ",")
		for ind := range lst_Users {
			user_pass := strings.Split(lst_Users[ind], ":")
			APIuser := cluster.APIUsers[user_pass[0]]
			tab_ApiUser = append(tab_ApiUser, APIuser.User+":"+cluster.Conf.GetEncryptedString(APIuser.Password))
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
	case "mail-smtp-password":
		return cluster.Conf.GetEncryptedString(cluster.Conf.GetDecryptedValue("mail-smtp-password"))
	default:
		return ""
	}
}

func (cluster *Cluster) InitAgent(conf config.Config) {
	cluster.Conf = conf
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
	cluster.Conf = conf
	cluster.Configurator.SetConfig(conf)
	cluster.InitFromConf()
	cluster.StateMachine.SetFailoverState()
	cluster.ResetStates()

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.TopologyDiscover(wg)
	wg.Wait()

}

func (cluster *Cluster) FailoverForce() error {
	sf := stateFile{Name: "/tmp/mrm" + cluster.Name + ".state"}
	err := sf.access()
	if err != nil {
		cluster.LogPrintf(LvlWarn, "Could not create state file")
	}
	err = sf.read()
	if err != nil {
		cluster.LogPrintf(LvlWarn, "Could not read values from state file:", err)
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
					if cluster.Conf.LogLevel > 2 {
						cluster.LogPrintf(LvlDbg, "State failed set by state detection ERR00012")
					}
					cluster.master = s
				}
			}
		} else {
			return err

		}
	}
	if cluster.GetMaster() == nil {
		cluster.LogPrintf(LvlErr, "Could not find a failed server in the hosts list")
		return errors.New("ERROR: Could not find a failed server in the hosts list")
	}
	if cluster.Conf.FailLimit > 0 && cluster.FailoverCtr >= cluster.Conf.FailLimit {
		cluster.LogPrintf(LvlErr, "Failover has exceeded its configured limit of %d. Remove /tmp/mrm.state file to reinitialize the failover counter", cluster.Conf.FailLimit)
		return errors.New("ERROR: Failover has exceeded its configured limit")
	}
	rem := (cluster.FailoverTs + cluster.Conf.FailTime) - time.Now().Unix()
	if cluster.Conf.FailTime > 0 && rem > 0 {
		cluster.LogPrintf(LvlErr, "Failover time limit enforced. Next failover available in %d seconds", rem)
		return errors.New("ERROR: Failover time limit enforced")
	}
	if cluster.MasterFailover(true) {
		sf.Count++
		sf.Timestamp = cluster.FailoverTs
		err := sf.write()
		if err != nil {
			cluster.LogPrintf(LvlWarn, "Could not write values to state file:%s", err)
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
		s.JobBackupErrorLog()
		s.JobBackupSlowQueryLog()
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
	masterVariables := cluster.GetMaster().Variables
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
			slaveVariables := s.Variables
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
			cluster.LogPrintf(LvlErr, "Encoding variables diff %s", err)
			return
		}
		cluster.SetState("WARN0084", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0084"], string(jtext)), ErrFrom: "MON", ServerUrl: cluster.GetMaster().URL})
	}
}

func (cluster *Cluster) MonitorSchema() {
	if !cluster.Conf.MonitorSchemaChange {
		return
	}
	if cluster.GetMaster() == nil {
		return
	}
	if cluster.GetMaster().State == stateFailed || cluster.GetMaster().State == stateMaintenance || cluster.GetMaster().State == stateUnconn {
		return
	}
	if cluster.GetMaster().Conn == nil {
		return
	}
	cluster.StateMachine.SetMonitorSchemaState()
	cluster.GetMaster().Conn.SetConnMaxLifetime(3595 * time.Second)

	tables, tablelist, logs, err := dbhelper.GetTables(cluster.GetMaster().Conn, cluster.GetMaster().DBVersion)
	cluster.LogSQL(logs, err, cluster.GetMaster().URL, "Monitor", LvlErr, "Could not fetch master tables %s", err)
	cluster.GetMaster().Tables = tablelist

	var tableCluster []string
	var duplicates []*ServerMonitor
	var tottablesize, totindexsize int64
	for _, t := range tables {
		duplicates = nil
		tableCluster = nil
		tottablesize += t.DataLength
		totindexsize += t.IndexLength
		cluster.LogPrintf(LvlDbg, "Lookup for table %s", t.TableSchema+"."+t.TableName)

		duplicates = append(duplicates, cluster.GetMaster())
		tableCluster = append(tableCluster, cluster.GetName())
		oldtable, err := cluster.GetMaster().GetTableFromDict(t.TableSchema + "." + t.TableName)
		haschanged := false
		if err != nil {
			if err.Error() == "Empty" {
				cluster.LogPrintf(LvlDbg, "Init table %s", t.TableSchema+"."+t.TableName)
				haschanged = true
			} else {
				cluster.LogPrintf(LvlDbg, "New table %s", t.TableSchema+"."+t.TableName)
				haschanged = true
			}
		} else {
			if oldtable.TableCrc != t.TableCrc {
				haschanged = true
				cluster.LogPrintf(LvlDbg, "Change table %s", t.TableSchema+"."+t.TableName)
			}
			t.TableSync = oldtable.TableSync
		}
		// lookup other clusters
		for _, cl := range cluster.clusterList {
			if cl.GetName() != cluster.GetName() {

				m := cl.GetMaster()
				if m != nil {
					cltbldef, _ := m.GetTableFromDict(t.TableSchema + "." + t.TableName)
					if cltbldef.TableName == t.TableName {
						duplicates = append(duplicates, cl.GetMaster())
						tableCluster = append(tableCluster, cl.GetName())
						cluster.LogPrintf(LvlDbg, "Found duplicate table %s in %s", t.TableSchema+"."+t.TableName, cl.GetMaster().URL)
					}
				}
			}
		}
		t.TableClusters = strings.Join(tableCluster, ",")
		tables[t.TableSchema+"."+t.TableName] = t
		if haschanged && cluster.Conf.MdbsProxyOn {
			for _, pri := range cluster.Proxies {
				if prx, ok := pri.(*MariadbShardProxy); ok {
					if !(t.TableSchema == "replication_manager_schema" || strings.Contains(t.TableName, "_copy") == true || strings.Contains(t.TableName, "_back") == true || strings.Contains(t.TableName, "_old") == true || strings.Contains(t.TableName, "_reshard") == true) {
						cluster.LogPrintf(LvlDbg, "blabla table %s %s %s", duplicates, t.TableSchema, t.TableName)
						cluster.ShardProxyCreateVTable(prx, t.TableSchema, t.TableName, duplicates, false)
					}
				}
			}
		}
	}

	cluster.WorkLoad.DBIndexSize = totindexsize
	cluster.WorkLoad.DBTableSize = tottablesize
	cluster.GetMaster().DictTables = tables
	cluster.StateMachine.RemoveMonitorSchemaState()
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
		cluster.LogPrintf("ERROR", "Can't found elected master from server list on lost arbitration")
		return
	}
	if cluster.Conf.ArbitrationFailedMasterScript != "" {
		cluster.LogPrintf(LvlInfo, "Calling abitration failed for master script")
		out, err := exec.Command(cluster.Conf.ArbitrationFailedMasterScript, cluster.GetMaster().Host, cluster.GetMaster().Port).CombinedOutput()
		if err != nil {
			cluster.LogPrintf(LvlErr, "%s", err)
		}
		cluster.LogPrintf(LvlInfo, "Arbitration failed master script complete: %s", string(out))
	} else {
		cluster.LogPrintf(LvlInfo, "Arbitration failed attaching failed master %s to electected master :%s", cluster.GetMaster().URL, realmaster.URL)
		logs, err := cluster.GetMaster().SetReplicationGTIDCurrentPosFromServer(realmaster)
		cluster.LogSQL(logs, err, realmaster.URL, "Arbitration", LvlErr, "Failed in GTID rejoin lost master to winner master %s", err)

	}
}

func (c *Cluster) AddProxy(prx DatabaseProxy) {
	prx.SetCluster(c)
	prx.SetID()
	prx.SetDataDir()
	prx.SetServiceName(c.Name)
	c.LogPrintf(LvlInfo, "New proxy monitored %s: %s:%s", prx.GetType(), prx.GetHost(), prx.GetPort())
	prx.SetState(stateSuspect)
	c.Proxies = append(c.Proxies, prx)
}

func (cluster *Cluster) ConfigDiscovery() error {
	server := cluster.GetMaster()
	if server != nil {
		cluster.LogPrintf(LvlErr, "Cluster configurartion discovery can ony be done on a valid leader")
		return errors.New("Cluster configurartion discovery can ony be done on a valid leader")
	}
	cluster.Configurator.ConfigDiscovery(server.Variables, server.Plugins)
	cluster.SetDBCoresFromConfigurator()
	cluster.SetDBMemoryFromConfigurator()
	cluster.SetDBIOPSFromConfigurator()
	cluster.SetTagsFromConfigurator()
	return nil
}

func (cluster *Cluster) ReloadCertificates() {
	cluster.LogPrintf(LvlInfo, "Reload cluster TLS certificates")
	for _, srv := range cluster.Servers {
		srv.CertificatesReload()
	}
	for _, pri := range cluster.Proxies {
		pri.CertificatesReload()
	}
}

func (cluster *Cluster) ResetStates() {
	cluster.LogPrintf(LvlInfo, "Reload cluster TLS certificates")
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
	cluster.clusterList = nil
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

	cluster.newServerList()
	cluster.newProxyList()
	cluster.StateMachine.RemoveFailoverState()
	cluster.initProxies()
}

func (cluster *Cluster) DecryptSecretsFromVault() {
	for k, v := range cluster.Conf.Secrets {
		origin_value := v.Value
		var secret config.Secret
		secret.Value = fmt.Sprintf("%v", origin_value)
		if cluster.Conf.IsVaultUsed() && cluster.Conf.IsPath(secret.Value) {
			//	cluster.LogPrintf(LvlInfo, "Decrypting all the secret variables on Vault")
			vault_config := vault.DefaultConfig()
			vault_config.Address = cluster.Conf.VaultServerAddr
			client, err := cluster.Conf.GetVaultConnection()
			if err == nil {
				if cluster.Conf.VaultMode == VaultConfigStoreV2 {
					vault_value, err := cluster.Conf.GetVaultCredentials(client, secret.Value, k)
					if err != nil {
						cluster.LogPrintf(LvlWarn, "Unable to get %s Vault secret: %v", k, err)
					} else if vault_value != "" {
						secret.Value = vault_value
					}
				}
			} else {
				cluster.LogPrintf(LvlErr, "Unable to initialize AppRole auth method: %v", err)
			}
			cluster.Conf.Secrets[k] = secret
		}
	}
}
