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
	"io/ioutil"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/signal18/replication-manager/cluster/nbc"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/maxscale"
	"github.com/signal18/replication-manager/utils/cron"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
	log "github.com/sirupsen/logrus"
	logsqlerr "github.com/sirupsen/logrus"
	logsqlgen "github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
)

type Cluster struct {
	Name                          string                      `json:"name"`
	Tenant                        string                      `json:"tenant"`
	WorkingDir                    string                      `json:"workingDir"`
	Servers                       serverList                  `json:"-"`
	ServerIdList                  []string                    `json:"dbServers"`
	Crashes                       crashList                   `json:"dbServersCrashes"`
	Proxies                       proxyList                   `json:"-"`
	ProxyIdList                   []string                    `json:"proxyServers"`
	FailoverCtr                   int                         `json:"failoverCounter"`
	FailoverTs                    int64                       `json:"failoverLastTime"`
	Status                        string                      `json:"activePassiveStatus"`
	IsSplitBrain                  bool                        `json:"isSplitBrain"`
	IsSplitBrainBck               bool                        `json:"-"`
	IsFailedArbitrator            bool                        `json:"isFailedArbitrator"`
	IsLostMajority                bool                        `json:"isLostMajority"`
	IsDown                        bool                        `json:"isDown"`
	IsClusterDown                 bool                        `json:"isClusterDown"`
	IsAllDbUp                     bool                        `json:"isAllDbUp"`
	IsFailable                    bool                        `json:"isFailable"`
	IsPostgres                    bool                        `json:"isPostgres"`
	IsProvision                   bool                        `json:"isProvision"`
	IsNeedProxiesRestart          bool                        `json:"isNeedProxyRestart"`
	IsNeedProxiesReprov           bool                        `json:"isNeedProxiesRestart"`
	IsNeedDatabasesRestart        bool                        `json:"isNeedDatabasesRestart"`
	IsNeedDatabasesRollingRestart bool                        `json:"isNeedDatabasesRollingRestart"`
	IsNeedDatabasesRollingReprov  bool                        `json:"isNeedDatabasesRollingReprov"`
	IsNeedDatabasesReprov         bool                        `json:"isNeedDatabasesReprov"`
	IsNotMonitoring               bool                        `json:"isNotMonitoring"`
	IsCapturing                   bool                        `json:"isCapturing"`
	Conf                          config.Config               `json:"config"`
	CleanAll                      bool                        `json:"cleanReplication"` //used in testing
	ConfigDBTags                  []Tag                       `json:"configTags"`       //from module
	ConfigPrxTags                 []Tag                       `json:"configPrxTags"`    //from module
	DBTags                        []string                    `json:"dbServersTags"`    //from conf
	ProxyTags                     []string                    `json:"proxyServersTags"`
	Topology                      string                      `json:"topology"`
	Uptime                        string                      `json:"uptime"`
	UptimeFailable                string                      `json:"uptimeFailable"`
	UptimeSemiSync                string                      `json:"uptimeSemisync"`
	MonitorSpin                   string                      `json:"monitorSpin"`
	DBTableSize                   int64                       `json:"dbTableSize"`
	DBIndexSize                   int64                       `json:"dbIndexSize"`
	Connections                   int                         `json:"connections"`
	QPS                           int64                       `json:"qps"`
	Log                           s18log.HttpLog              `json:"log"`
	JobResults                    map[string]*JobResult       `json:"jobResults"`
	Grants                        map[string]string           `json:"-"`
	tlog                          *s18log.TermLog             `json:"-"`
	htlog                         *s18log.HttpLog             `json:"-"`
	SQLGeneralLog                 s18log.HttpLog              `json:"sqlGeneralLog"`
	SQLErrorLog                   s18log.HttpLog              `json:"sqlErrorLog"`
	MonitorType                   map[string]string           `json:"monitorType"`
	TopologyType                  map[string]string           `json:"topologyType"`
	FSType                        map[string]bool             `json:"fsType"`
	DiskType                      map[string]string           `json:"diskType"`
	VMType                        map[string]bool             `json:"vmType"`
	Agents                        []Agent                     `json:"agents"`
	hostList                      []string                    `json:"-"`
	proxyList                     []string                    `json:"-"`
	clusterList                   map[string]*Cluster         `json:"-"`
	slaves                        serverList                  `json:"-"`
	master                        *ServerMonitor              `json:"-"`
	oldMaster                     *ServerMonitor              `json:"-"`
	vmaster                       *ServerMonitor              `json:"-"`
	mxs                           *maxscale.MaxScale          `json:"-"`
	dbUser                        string                      `json:"-"`
	dbPass                        string                      `json:"-"`
	rplUser                       string                      `json:"-"`
	rplPass                       string                      `json:"-"`
	sme                           *state.StateMachine         `json:"-"`
	runOnceAfterTopology          bool                        `json:"-"`
	logPtr                        *os.File                    `json:"-"`
	termlength                    int                         `json:"-"`
	runUUID                       string                      `json:"-"`
	cfgGroupDisplay               string                      `json:"-"`
	repmgrVersion                 string                      `json:"-"`
	repmgrHostname                string                      `json:"-"`
	key                           []byte                      `json:"-"`
	exitMsg                       string                      `json:"-"`
	exit                          bool                        `json:"-"`
	canFlashBack                  bool                        `json:"-"`
	failoverCond                  *nbc.NonBlockingChan        `json:"-"`
	switchoverCond                *nbc.NonBlockingChan        `json:"-"`
	rejoinCond                    *nbc.NonBlockingChan        `json:"-"`
	bootstrapCond                 *nbc.NonBlockingChan        `json:"-"`
	altertableCond                *nbc.NonBlockingChan        `json:"-"`
	addtableCond                  *nbc.NonBlockingChan        `json:"-"`
	statecloseChan                chan state.State            `json:"-"`
	switchoverChan                chan bool                   `json:"-"`
	errorChan                     chan error                  `json:"-"`
	testStopCluster               bool                        `json:"-"`
	testStartCluster              bool                        `json:"-"`
	lastmaster                    *ServerMonitor              `json:"-"`
	benchmarkType                 string                      `json:"-"`
	HaveDBTLSCert                 bool                        `json:"haveDBTLSCert"`
	HaveDBTLSOldCert              bool                        `json:"haveDBTLSOldCert"`
	tlsconf                       *tls.Config                 `json:"-"`
	tlsoldconf                    *tls.Config                 `json:"-"`
	tunnel                        *ssh.Client                 `json:"-"`
	DBModule                      config.Compliance           `json:"-"`
	ProxyModule                   config.Compliance           `json:"-"`
	QueryRules                    map[uint32]config.QueryRule `json:"-"`
	Backups                       []Backup                    `json:"-"`
	SLAHistory                    []state.Sla                 `json:"slaHistory"`
	APIUsers                      map[string]APIUser          `json:"apiUsers"`
	Schedule                      map[string]cron.Entry       `json:"-"`
	scheduler                     *cron.Cron                  `json:"-"`
	idSchedulerPhysicalBackup     cron.EntryID                `json:"-"`
	idSchedulerLogicalBackup      cron.EntryID                `json:"-"`
	idSchedulerOptimize           cron.EntryID                `json:"-"`
	idSchedulerErrorLogs          cron.EntryID                `json:"-"`
	idSchedulerLogRotateTable     cron.EntryID                `json:"-"`
	idSchedulerSLARotate          cron.EntryID                `json:"-"`
	idSchedulerRollingRestart     cron.EntryID                `json:"-"`
	idSchedulerDbsjobsSsh         cron.EntryID                `json:"-"`
	idSchedulerRollingReprov      cron.EntryID                `json:"-"`
	WaitingRejoin                 int                         `json:"waitingRejoin"`
	WaitingSwitchover             int                         `json:"waitingSwitchover"`
	WaitingFailover               int                         `json:"waitingFailover"`
	DiffVariables                 []VariableDiff              `json:"diffVariables"`
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

type Tag struct {
	Id       uint   `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
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

// Init initial cluster definition
func (cluster *Cluster) Init(conf config.Config, cfgGroup string, tlog *s18log.TermLog, log *s18log.HttpLog, termlength int, runUUID string, repmgrVersion string, repmgrHostname string, key []byte) error {
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
	cluster.runOnceAfterTopology = true
	cluster.testStopCluster = true
	cluster.testStartCluster = true
	cluster.tlog = tlog
	cluster.htlog = log
	cluster.termlength = termlength
	cluster.Name = cfgGroup
	cluster.WorkingDir = conf.WorkingDir + "/" + cluster.Name
	cluster.runUUID = runUUID
	cluster.repmgrHostname = repmgrHostname
	cluster.repmgrVersion = repmgrVersion
	cluster.key = key
	if conf.Arbitration {
		cluster.Status = ConstMonitorStandby
	} else {
		cluster.Status = ConstMonitorActif
	}
	cluster.benchmarkType = "sysbench"
	cluster.Log = s18log.NewHttpLog(200)
	cluster.MonitorType = conf.GetMonitorType()
	cluster.TopologyType = conf.GetTopologyType()
	cluster.FSType = conf.GetFSType()
	cluster.DiskType = conf.GetDiskType()
	cluster.VMType = conf.GetVMType()
	cluster.Grants = conf.GetGrantType()
	cluster.QueryRules = make(map[uint32]config.QueryRule)
	cluster.Schedule = make(map[string]cron.Entry)
	cluster.JobResults = make(map[string]*JobResult)
	// Initialize the state machine at this stage where everything is fine.
	cluster.sme = new(state.StateMachine)
	cluster.sme.Init()
	cluster.Conf = conf
	if cluster.Conf.Interactive {
		cluster.LogPrintf(LvlInfo, "Failover in interactive mode")
	} else {
		cluster.LogPrintf(LvlInfo, "Failover in automatic mode")
	}
	if _, err := os.Stat(cluster.WorkingDir); os.IsNotExist(err) {
		os.MkdirAll(cluster.Conf.WorkingDir+"/"+cluster.Name, os.ModePerm)
	}

	hookerr, err := s18log.NewRotateFileHook(s18log.RotateFileConfig{
		Filename:   cluster.WorkingDir + "/sql_error.log",
		MaxSize:    cluster.Conf.LogRotateMaxSize,
		MaxBackups: cluster.Conf.LogRotateMaxBackup,
		MaxAge:     cluster.Conf.LogRotateMaxAge,
		Level:      logsqlerr.DebugLevel,
		Formatter: &logsqlerr.TextFormatter{
			DisableColors:   true,
			TimestampFormat: "2006-01-02 15:04:05",
			FullTimestamp:   true,
		},
	})
	if err != nil {
		logsqlerr.WithError(err).Error("Can't init error sql log file")
	}
	logsqlerr.AddHook(hookerr)

	hookgen, err := s18log.NewRotateFileHook(s18log.RotateFileConfig{
		Filename:   cluster.WorkingDir + "/sql_general.log",
		MaxSize:    cluster.Conf.LogRotateMaxSize,
		MaxBackups: cluster.Conf.LogRotateMaxBackup,
		MaxAge:     cluster.Conf.LogRotateMaxAge,
		Level:      logsqlerr.DebugLevel,
		Formatter: &logsqlgen.TextFormatter{
			DisableColors:   true,
			TimestampFormat: "2006-01-02 15:04:05",
			FullTimestamp:   true,
		},
	})
	if err != nil {
		logsqlgen.WithError(err).Error("Can't init general sql log file")
	}
	logsqlgen.AddHook(hookgen)
	cluster.LoadAPIUsers()
	// createKeys do nothing yet
	cluster.createKeys()
	cluster.GetPersitentState()

	cluster.newServerList()
	err = cluster.newProxyList()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Could not set proxy list %s", err)
	}
	//Loading configuration compliances
	cluster.LoadDBModules()
	cluster.LoadPrxModules()
	cluster.ConfigDBTags = cluster.GetDBModuleTags()
	cluster.ConfigPrxTags = cluster.GetProxyModuleTags()

	switch cluster.Conf.ProvOrchestrator {
	case config.ConstOrchestratorLocalhost:
		cluster.DropDBTagConfig("docker")
		cluster.DropDBTagConfig("threadpool")
		cluster.AddDBTagConfig("pkg")
	}

	return nil
}
func (cluster *Cluster) initOrchetratorNodes() {

	//cluster.LogPrintf(LvlInfo, "Loading nodes from orchestrator %s", cluster.Conf.ProvOrchestrator)
	switch cluster.Conf.ProvOrchestrator {
	case config.ConstOrchestratorOpenSVC:
		cluster.Agents, _ = cluster.OpenSVCGetNodes()
	case config.ConstOrchestratorKubernetes:
		cluster.Agents, _ = cluster.K8SGetNodes()
	case config.ConstOrchestratorSlapOS:
		cluster.Agents, _ = cluster.SlapOSGetNodes()
	case config.ConstOrchestratorLocalhost:
		cluster.Agents, _ = cluster.LocalhostGetNodes()
	case config.ConstOrchestratorOnPremise:
	default:
		log.Fatalln("prov-orchestrator not supported", cluster.Conf.ProvOrchestrator)
	}

}

func (cluster *Cluster) initScheduler() {
	if cluster.Conf.MonitorScheduler {
		cluster.LogPrintf(LvlInfo, "Starting cluster scheduler")
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
	cluster.initScheduler()
	interval := time.Second

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
					for k, v := range cluster.Servers {
						cluster.LogPrintf(LvlDbg, "Server [%d]: URL: %-15s State: %6s PrevState: %6s", k, v.URL, v.State, v.PrevState)
					}
					if cluster.master != nil {
						cluster.LogPrintf(LvlDbg, "Master [ ]: URL: %-15s State: %6s PrevState: %6s", cluster.master.URL, cluster.master.State, cluster.master.PrevState)
						for k, v := range cluster.slaves {
							cluster.LogPrintf(LvlDbg, "Slave  [%d]: URL: %-15s State: %6s PrevState: %6s", k, v.URL, v.State, v.PrevState)
						}
					}
				}
				wg := new(sync.WaitGroup)
				wg.Add(1)
				go cluster.TopologyDiscover(wg)
				wg.Add(1)
				go cluster.Heartbeat(wg)
				// Heartbeat switchover or failover controller runs only on active repman

				if cluster.runOnceAfterTopology {
					cluster.initProxies()
					cluster.initOrchetratorNodes()
					cluster.ResticFetchRepo()
					cluster.runOnceAfterTopology = false
				} else {
					wg.Add(1)
					go cluster.refreshProxies(wg)
					if cluster.sme.SchemaMonitorEndTime+60 < time.Now().Unix() && !cluster.sme.IsInSchemaMonitor() {
						go cluster.MonitorSchema()
					}
					if cluster.Conf.TestInjectTraffic || cluster.Conf.AutorejoinSlavePositionalHeartbeat || cluster.Conf.MonitorWriteHeartbeat {
						cluster.InjectProxiesTraffic()
					}
					if cluster.sme.GetHeartbeats()%30 == 0 {
						cluster.initOrchetratorNodes()
						cluster.MonitorQueryRules()
						cluster.MonitorVariablesDiff()
						cluster.ResticFetchRepo()

					} else {
						cluster.sme.PreserveState("WARN0093")
						cluster.sme.PreserveState("WARN0084")
						cluster.sme.PreserveState("WARN0095")
						cluster.sme.PreserveState("ERR00082")
					}
					if cluster.sme.GetHeartbeats()%36000 == 0 {
						cluster.ResticPurgeRepo()
					} else {
						cluster.sme.PreserveState("WARN0094")
					}
				}

				wg.Wait()

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
	if !cluster.sme.IsInFailover() {
		// trigger action on resolving states
		cstates := cluster.sme.GetResolvedStates()
		mybcksrv := cluster.GetBackupServer()
		master := cluster.GetMaster()
		for _, s := range cstates {
			servertoreseed := cluster.GetServerFromURL(s.ServerUrl)
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
			//		cluster.statecloseChan <- s
		}
		states := cluster.sme.GetStates()
		for i := range states {
			cluster.LogPrintf("STATE", states[i])
		}
		// trigger action on resolving states
		ostates := cluster.sme.GetOpenStates()
		for _, s := range ostates {
			cluster.CheckCapture(s)
		}
		cluster.sme.ClearState()
		if cluster.sme.GetHeartbeats()%60 == 0 {
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
	clsave.SLA = cluster.sme.GetSla()
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
		var myconf = make(map[string]config.Config)

		myconf["saved-"+cluster.Name] = cluster.Conf

		file, err := os.OpenFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/config.toml", os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
		if err != nil {
			if os.IsPermission(err) {
				cluster.LogPrintf(LvlInfo, "File permission denied: %s", cluster.Conf.WorkingDir+"/"+cluster.Name+"/config.toml")
			}
			return err
		}
		defer file.Close()
		err = toml.NewEncoder(file).Encode(myconf)
		if err != nil {
			return err
		}
	}

	return nil
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
	return
}

func (cluster *Cluster) ReloadConfig(conf config.Config) {
	cluster.Conf = conf
	cluster.sme.SetFailoverState()
	cluster.newServerList()
	cluster.newProxyList()
	wg := new(sync.WaitGroup)
	wg.Add(1)
	cluster.TopologyDiscover(wg)
	wg.Wait()
	cluster.sme.RemoveFailoverState()
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
		for _, s := range cluster.sme.GetStates() {
			cluster.LogPrint(s)
		}
		// Test for ERR00012 - No master detected
		if cluster.sme.CurState.Search("ERR00012") {
			for _, s := range cluster.Servers {
				if s.State == "" {
					s.State = stateFailed
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
	if cluster.master == nil {
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
	if cluster.master == nil {
		return
	}
	if cluster.master.State == stateFailed || cluster.master.State == stateMaintenance || cluster.master.State == stateUnconn {
		return
	}
	if cluster.master.Conn == nil {
		return
	}
	cluster.sme.SetMonitorSchemaState()
	cluster.master.Conn.SetConnMaxLifetime(3595 * time.Second)

	tables, tablelist, logs, err := dbhelper.GetTables(cluster.master.Conn, cluster.master.DBVersion)
	cluster.LogSQL(logs, err, cluster.master.URL, "Monitor", LvlErr, "Could not fetch master tables %s", err)
	cluster.master.Tables = tablelist

	var tableCluster []string
	var duplicates []*ServerMonitor
	var tottablesize, totindexsize int64
	for _, t := range tables {
		duplicates = nil
		tableCluster = nil
		tottablesize += t.Data_length
		totindexsize += t.Index_length
		cluster.LogPrintf(LvlDbg, "Lookup for table %s", t.Table_schema+"."+t.Table_name)

		duplicates = append(duplicates, cluster.GetMaster())
		tableCluster = append(tableCluster, cluster.GetName())
		oldtable, err := cluster.master.GetTableFromDict(t.Table_schema + "." + t.Table_name)
		haschanged := false
		if err != nil {
			if err.Error() == "Empty" {
				cluster.LogPrintf(LvlDbg, "Init table %s", t.Table_schema+"."+t.Table_name)
				haschanged = true
			} else {
				cluster.LogPrintf(LvlDbg, "New table %s", t.Table_schema+"."+t.Table_name)
				haschanged = true
			}
		} else {
			if oldtable.Table_crc != t.Table_crc {
				haschanged = true
				cluster.LogPrintf(LvlDbg, "Change table %s", t.Table_schema+"."+t.Table_name)
			}
			t.Table_sync = oldtable.Table_sync
		}
		// lookup other clusters
		for _, cl := range cluster.clusterList {
			if cl.GetName() != cluster.GetName() {

				m := cl.GetMaster()
				if m != nil {
					cltbldef, _ := m.GetTableFromDict(t.Table_schema + "." + t.Table_name)
					if cltbldef.Table_name == t.Table_name {
						duplicates = append(duplicates, cl.GetMaster())
						tableCluster = append(tableCluster, cl.GetName())
						cluster.LogPrintf(LvlDbg, "Found duplicate table %s in %s", t.Table_schema+"."+t.Table_name, cl.GetMaster().URL)
					}
				}
			}
		}
		t.Table_clusters = strings.Join(tableCluster, ",")
		tables[t.Table_schema+"."+t.Table_name] = t
		if haschanged {
			for _, pr := range cluster.Proxies {
				if cluster.Conf.MdbsProxyOn && pr.Type == config.ConstProxySpider {
					if !(t.Table_schema == "replication_manager_schema" || strings.Contains(t.Table_name, "_copy") == true || strings.Contains(t.Table_name, "_back") == true || strings.Contains(t.Table_name, "_old") == true || strings.Contains(t.Table_name, "_reshard") == true) {
						cluster.LogPrintf(LvlDbg, "blabla table %s %s %s", duplicates, t.Table_schema, t.Table_name)
						cluster.ShardProxyCreateVTable(pr, t.Table_schema, t.Table_name, duplicates, false)
					}
				}
			}
		}
	}
	cluster.DBIndexSize = totindexsize
	cluster.DBTableSize = tottablesize
	cluster.master.DictTables = tables
	cluster.sme.RemoveMonitorSchemaState()
}

func (cluster *Cluster) MonitorQueryRules() {
	if !cluster.Conf.MonitorQueryRules {
		return
	}
	for _, prx := range cluster.Proxies {
		if cluster.Conf.ProxysqlOn && prx.Type == config.ConstProxySqlproxy {
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
		out, err := exec.Command(cluster.Conf.ArbitrationFailedMasterScript, cluster.master.Host, cluster.master.Port).CombinedOutput()
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

func (cluster *Cluster) LoadDBModules() {
	file := cluster.Conf.ShareDir + "/opensvc/moduleset_mariadb.svc.mrm.db.json"
	jsonFile, err := os.Open(file)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Failed opened module %s %s", file, err)
	}
	cluster.LogPrintf(LvlInfo, "Loading database configurator config %s", file)
	// defer the closing of our jsonFile so that we can parse it later on
	defer jsonFile.Close()

	byteValue, _ := ioutil.ReadAll(jsonFile)

	err = json.Unmarshal([]byte(byteValue), &cluster.DBModule)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Failed unmarshal file %s %s", file, err)
	}

}

func (cluster *Cluster) LoadPrxModules() {

	file := cluster.Conf.ShareDir + "/opensvc/moduleset_mariadb.svc.mrm.proxy.json"
	jsonFile, err := os.Open(file)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Failed opened module %s %s", file, err)
	}
	cluster.LogPrintf(LvlInfo, "Loading proxies configurator config %s", file)
	// defer the closing of our jsonFile so that we can parse it later on
	defer jsonFile.Close()

	byteValue, _ := ioutil.ReadAll(jsonFile)

	err = json.Unmarshal([]byte(byteValue), &cluster.ProxyModule)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Failed unmarshal file %s %s", file, err)
	}

}

func (cluster *Cluster) ConfigDiscovery() error {

	if cluster.master == nil {
		return errors.New("No master in topology")
	}
	innodbmem, err := strconv.ParseUint(cluster.master.Variables["INNODB_BUFFER_POOL_SIZE"], 10, 64)
	if err != nil {
		return err
	}
	totalmem := innodbmem
	myisammem, err := strconv.ParseUint(cluster.master.Variables["KEY_BUFFER_SIZE"], 10, 64)
	if err != nil {
		return err
	}
	totalmem += myisammem
	qcmem, err := strconv.ParseUint(cluster.master.Variables["QUERY_CACHE_SIZE"], 10, 64)
	if err != nil {
		return err
	}
	if qcmem == 0 {
		cluster.AddDBTag("noquerycache")
	}
	totalmem += qcmem
	ariamem := uint64(0)
	if _, ok := cluster.master.Variables["ARIA_PAGECACHE_BUFFER_SIZE"]; ok {
		ariamem, err = strconv.ParseUint(cluster.master.Variables["ARIA_PAGECACHE_BUFFER_SIZE"], 10, 64)
		if err != nil {
			return err
		}
		totalmem += ariamem
	}
	tokumem := uint64(0)
	if _, ok := cluster.master.Variables["TOKUDB_CACHE_SIZE"]; ok {
		cluster.AddDBTag("tokudb")
		tokumem, err = strconv.ParseUint(cluster.master.Variables["TOKUDB_CACHE_SIZE"], 10, 64)
		if err != nil {
			return err
		}
		totalmem += tokumem
	}
	s3mem := uint64(0)
	if _, ok := cluster.master.Variables["S3_PAGECACHE_BUFFER_SIZE"]; ok {
		cluster.AddDBTag("s3")
		tokumem, err = strconv.ParseUint(cluster.master.Variables["S3_PAGECACHE_BUFFER_SIZE"], 10, 64)
		if err != nil {
			return err
		}
		totalmem += s3mem
	}

	rocksmem := uint64(0)
	if _, ok := cluster.master.Variables["ROCKSDB_BLOCK_CACHE_SIZE"]; ok {
		cluster.AddDBTag("myrocks")
		tokumem, err = strconv.ParseUint(cluster.master.Variables["ROCKSDB_BLOCK_CACHE_SIZE"], 10, 64)
		if err != nil {
			return err
		}
		totalmem += rocksmem
	}

	sharedmempcts, _ := cluster.Conf.GetMemoryPctShared()
	totalmem = totalmem + totalmem*uint64(sharedmempcts["threads"])/100
	cluster.SetDBMemorySize(strconv.FormatUint((totalmem / 1024 / 1024), 10))
	cluster.SetDBCores(cluster.master.Variables["THREAD_POOL_SIZE"])

	if cluster.master.Variables["INNODB_DOUBLEWRITE"] == "OFF" {
		cluster.AddDBTag("nodoublewrite")
	}
	if cluster.master.Variables["INNODB_FLUSH_LOG_AT_TRX_COMMIT"] != "1" && cluster.master.Variables["SYNC_BINLOG"] != "1" {
		cluster.AddDBTag("nodurable")
	}
	if cluster.master.Variables["INNODB_FLUSH_METHOD"] != "O_DIRECT" {
		cluster.AddDBTag("noodirect")
	}
	if cluster.master.Variables["LOG_BIN_COMPRESS"] == "ON" {
		cluster.AddDBTag("compressbinlog")
	}
	if cluster.master.Variables["INNODB_DEFRAGMENT"] == "ON" {
		cluster.AddDBTag("autodefrag")
	}
	if cluster.master.Variables["INNODB_COMPRESSION_DEFAULT"] == "ON" {
		cluster.AddDBTag("compresstable")
	}

	if cluster.master.HasInstallPlugin("BLACKHOLE") {
		cluster.AddDBTag("blackhole")
	}
	if cluster.master.HasInstallPlugin("QUERY_RESPONSE_TIME") {
		cluster.AddDBTag("userstats")
	}
	if cluster.master.HasInstallPlugin("SQL_ERROR_LOG") {
		cluster.AddDBTag("sqlerror")
	}
	if cluster.master.HasInstallPlugin("METADATA_LOCK_INFO") {
		cluster.AddDBTag("metadatalocks")
	}
	if cluster.master.HasInstallPlugin("SERVER_AUDIT") {
		cluster.AddDBTag("audit")
	}
	if cluster.master.Variables["SLOW_QUERY_LOG"] == "ON" {
		cluster.AddDBTag("slow")
	}
	if cluster.master.Variables["GENERAL_LOG"] == "ON" {
		cluster.AddDBTag("general")
	}
	if cluster.master.Variables["PERFORMANCE_SCHEMA"] == "ON" {
		cluster.AddDBTag("pfs")
	}
	if cluster.master.Variables["LOG_OUTPUT"] == "TABLE" {
		cluster.AddDBTag("logtotable")
	}

	if cluster.master.HasInstallPlugin("CONNECT") {
		cluster.AddDBTag("connect")
	}
	if cluster.master.HasInstallPlugin("SPIDER") {
		cluster.AddDBTag("spider")
	}
	if cluster.master.HasInstallPlugin("SPHINX") {
		cluster.AddDBTag("sphinx")
	}
	if cluster.master.HasInstallPlugin("MROONGA") {
		cluster.AddDBTag("mroonga")
	}
	if cluster.master.HasWsrep() {
		cluster.AddDBTag("wsrep")
	}
	//missing in compliance
	if cluster.master.HasInstallPlugin("ARCHIVE") {
		cluster.AddDBTag("archive")
	}

	if cluster.master.HasInstallPlugin("CRACKLIB_PASSWORD_CHECK") {
		cluster.AddDBTag("pwdcheckcracklib")
	}
	if cluster.master.HasInstallPlugin("SIMPLE_PASSWORD_CHECK") {
		cluster.AddDBTag("pwdchecksimple")
	}

	if cluster.master.Variables["LOCAL_INFILE"] == "ON" {
		cluster.AddDBTag("localinfile")
	}
	if cluster.master.Variables["SKIP_NAME_RESOLVE"] == "OFF" {
		cluster.AddDBTag("resolvdns")
	}
	if cluster.master.Variables["READ_ONLY"] == "ON" {
		cluster.AddDBTag("readonly")
	}
	if cluster.master.Variables["HAVE_SSL"] == "YES" {
		cluster.AddDBTag("ssl")
	}

	if cluster.master.Variables["BINLOG_FORMAT"] == "STATEMENT" {
		cluster.AddDBTag("statement")
	}
	if cluster.master.Variables["BINLOG_FORMAT"] == "ROW" {
		cluster.AddDBTag("row")
	}
	if cluster.master.Variables["LOG_BIN"] == "OFF" {
		cluster.AddDBTag("nobinlog")
	}
	if cluster.master.Variables["LOG_BIN"] == "OFF" {
		cluster.AddDBTag("nobinlog")
	}
	if cluster.master.Variables["LOG_SLAVE_UPDATES"] == "OFF" {
		cluster.AddDBTag("nologslaveupdates")
	}
	if cluster.master.Variables["RPL_SEMI_SYNC_MASTER_ENABLED"] == "ON" {
		cluster.AddDBTag("semisync")
	}
	if cluster.master.Variables["GTID_STRICT_MODE"] == "ON" {
		cluster.AddDBTag("gtidstrict")
	}
	if strings.Contains(cluster.master.Variables["SLAVE_TYPE_COVERSIONS"], "ALL_NON_LOSSY") || strings.Contains(cluster.master.Variables["SLAVE_TYPE_COVERSIONS"], "ALL_LOSSY") {
		cluster.AddDBTag("lossyconv")
	}
	if cluster.master.Variables["SLAVE_EXEC_MODE"] == "IDEMPOTENT" {
		cluster.AddDBTag("idempotent")
	}

	//missing in compliance
	if strings.Contains(cluster.master.Variables["OPTIMIZER_SWITCH"], "SUBQUERY_CACHE=ON") {
		cluster.AddDBTag("subquerycache")
	}
	if strings.Contains(cluster.master.Variables["OPTIMIZER_SWITCH"], "SEMIJOIN_WITH_CACHE=ON") {
		cluster.AddDBTag("semijoincache")
	}
	if strings.Contains(cluster.master.Variables["OPTIMIZER_SWITCH"], "FIRSTMATCH=ON") {
		cluster.AddDBTag("firstmatch")
	}
	if strings.Contains(cluster.master.Variables["OPTIMIZER_SWITCH"], "EXTENDED_KEYS=ON") {
		cluster.AddDBTag("extendedkeys")
	}
	if strings.Contains(cluster.master.Variables["OPTIMIZER_SWITCH"], "LOOSESCAN=ON") {
		cluster.AddDBTag("loosescan")
	}
	if strings.Contains(cluster.master.Variables["OPTIMIZER_SWITCH"], "INDEX_CONDITION_PUSHDOWN=OFF") {
		cluster.AddDBTag("noicp")
	}
	if strings.Contains(cluster.master.Variables["OPTIMIZER_SWITCH"], "IN_TO_EXISTS=OFF") {
		cluster.AddDBTag("nointoexists")
	}
	if strings.Contains(cluster.master.Variables["OPTIMIZER_SWITCH"], "DERIVED_MERGE=OFF") {
		cluster.AddDBTag("noderivedmerge")
	}
	if strings.Contains(cluster.master.Variables["OPTIMIZER_SWITCH"], "DERIVED_WITH_KEYS=OFF") {
		cluster.AddDBTag("noderivedwithkeys")
	}
	if strings.Contains(cluster.master.Variables["OPTIMIZER_SWITCH"], "MRR=OFF") {
		cluster.AddDBTag("nomrr")
	}
	if strings.Contains(cluster.master.Variables["OPTIMIZER_SWITCH"], "OUTER_JOIN_WITH_CACHE=OFF") {
		cluster.AddDBTag("noouterjoincache")
	}
	if strings.Contains(cluster.master.Variables["OPTIMIZER_SWITCH"], "SEMI_JOIN_WITH_CACHE=OFF") {
		cluster.AddDBTag("nosemijoincache")
	}
	if strings.Contains(cluster.master.Variables["OPTIMIZER_SWITCH"], "TABLE_ELIMINATION=OFF") {
		cluster.AddDBTag("notableelimination")
	}
	if strings.Contains(cluster.master.Variables["SQL_MODE"], "ORACLE") {
		cluster.AddDBTag("sqlmodeoracle")
	}
	if cluster.master.Variables["SQL_MODE"] == "" {
		cluster.AddDBTag("sqlmodeunstrict")
	}
	//index_merge=on
	//index_merge_union=on,
	//index_merge_sort_union=on
	//index_merge_intersection=on
	//index_merge_sort_intersection=off
	//engine_condition_pushdown=on
	//materialization=on
	//semijoin=on
	//partial_match_rowid_merge=on
	//partial_match_table_scan=on,
	//mrr_cost_based=off
	//mrr_sort_keys=on,
	//join_cache_incremental=on,
	//join_cache_hashed=on,
	//join_cache_bka=on,
	//optimize_join_buffer_size=on,
	//orderby_uses_equalities=on
	//condition_pushdown_for_derived=on
	//split_materialized=on//
	//condition_pushdown_for_subquery=on,
	//rowid_filter=on
	//condition_pushdown_from_having=on

	if cluster.master.Variables["TX_ISOLATION"] == "READ-COMMITTED" {
		cluster.AddDBTag("readcommitted")
	}
	//missing
	if cluster.master.Variables["TX_ISOLATION"] == "READ-UNCOMMITTED" {
		cluster.AddDBTag("readuncommitted")
	}
	if cluster.master.Variables["TX_ISOLATION"] == "REPEATABLE-READ" {
		cluster.AddDBTag("reapeatableread")
	}
	if cluster.master.Variables["TX_ISOLATION"] == "SERIALIZED" {
		cluster.AddDBTag("serialized")
	}

	if cluster.master.Variables["JOIN_CACHE_LEVEL"] == "8" {
		cluster.AddDBTag("hashjoin")
	}
	if cluster.master.Variables["JOIN_CACHE_LEVEL"] == "6" {
		cluster.AddDBTag("mrrjoin")
	}
	if cluster.master.Variables["JOIN_CACHE_LEVEL"] == "2" {
		cluster.AddDBTag("nestedjoin")
	}
	if cluster.master.Variables["LOWER_CASE_TABLE_NAMES"] == "1" {
		cluster.AddDBTag("lowercasetable")
	}
	if cluster.master.Variables["USER_STAT_TABLES"] == "PREFERABLY_FOR_QUERIES" {
		cluster.AddDBTag("eits")
	}

	if cluster.master.Variables["CHARACTER_SET_SERVER"] == "UTF8MB4" {
		if strings.Contains(cluster.master.Variables["COLLATION_SERVER"], "_ci") {
			cluster.AddDBTag("bm4ci")
		} else {
			cluster.AddDBTag("bm4cs")
		}
	}
	if cluster.master.Variables["CHARACTER_SET_SERVER"] == "UTF8" {
		if strings.Contains(cluster.master.Variables["COLLATION_SERVER"], "_ci") {
			cluster.AddDBTag("utf8ci")
		} else {
			cluster.AddDBTag("utf8cs")
		}
	}

	//slave_parallel_mode = optimistic
	/*

		tmpmem, err := strconv.ParseUint(cluster.master.Variables["TMP_TABLE_SIZE"], 10, 64)
		if err != nil {
			return err
		}
			qttmp, err := strconv.ParseUint(cluster.master.Variables["MAX_TMP_TABLES"], 10, 64)
			if err != nil {
				return err
			}
			tmpmem = tmpmem * qttmp
			totalmem += tmpmem

			cores, err := strconv.ParseUint(cluster.master.Variables["THREAD_POOL_SIZE"], 10, 64)
			if err != nil {
				return err
			}

			joinmem, err := strconv.ParseUint(cluster.master.Variables["JOIN_BUFFER_SPACE_LIMIT"], 10, 64)
			joinmem = joinmem * cores

			sortmem, err := strconv.ParseUint(cluster.master.Variables["SORT_BUFFER_SIZE"], 10, 64)
	*/
	//
	//	containermem = containermem * int64(sharedmempcts["innodb"]) / 100

	return nil
}
