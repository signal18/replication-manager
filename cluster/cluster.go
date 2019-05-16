// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
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
	"golang.org/x/crypto/ssh"
)

type Cluster struct {
	Name                 string               `json:"name"`
	Servers              serverList           `json:"-"`
	ServerIdList         []string             `json:"dbServers"`
	Crashes              crashList            `json:"dbServersCrashes"`
	Proxies              proxyList            `json:"-"`
	ProxyIdList          []string             `json:"proxyServers"`
	FailoverCtr          int                  `json:"failoverCounter"`
	FailoverTs           int64                `json:"failoverLastTime"`
	Status               string               `json:"activePassiveStatus"`
	IsSplitBrain         bool                 `json:"isSplitBrain"`
	IsSplitBrainBck      bool                 `json:"-"`
	IsFailedArbitrator   bool                 `json:"isFailedArbitrator"`
	IsLostMajority       bool                 `json:"isLostMajority"`
	IsDown               bool                 `json:"isDown"`
	IsClusterDown        bool                 `json:"isClusterDown"`
	IsProvisioned        bool                 `json:"isProvisioned"`
	IsFailable           bool                 `json:"isFailable"`
	Conf                 config.Config        `json:"config"`
	CleanAll             bool                 `json:"cleanReplication"` //used in testing
	Schedule             []CronEntry          `json:"schedule"`
	DBTags               []string             `json:"dbServersTags"`
	ProxyTags            []string             `json:"proxyServersTags"`
	Topology             string               `json:"topology"`
	Uptime               string               `json:"uptime"`
	UptimeFailable       string               `json:"uptimeFailable"`
	UptimeSemiSync       string               `json:"uptimeSemisync"`
	MonitorSpin          string               `json:"monitorSpin"`
	DBTableSize          int64                `json:"dbTableSize"`
	DBIndexSize          int64                `json:"dbIndexSize"`
	Log                  s18log.HttpLog       `json:"log"`
	tlog                 *s18log.TermLog      `json:"-"`
	htlog                *s18log.HttpLog      `json:"-"`
	MonitorType          map[string]string    `json:"monitorType"`
	TopologyType         map[string]string    `json:"topologyType"`
	hostList             []string             `json:"-"`
	proxyList            []string             `json:"-"`
	clusterList          map[string]*Cluster  `json:"-"`
	slaves               serverList           `json:"-"`
	master               *ServerMonitor       `json:"-"`
	oldMaster            *ServerMonitor       `json:"-"`
	vmaster              *ServerMonitor       `json:"-"`
	mxs                  *maxscale.MaxScale   `json:"-"`
	dbUser               string               `json:"-"`
	dbPass               string               `json:"-"`
	rplUser              string               `json:"-"`
	rplPass              string               `json:"-"`
	sme                  *state.StateMachine  `json:"-"`
	runOnceAfterTopology bool                 `json:"-"`
	logPtr               *os.File             `json:"-"`
	termlength           int                  `json:"-"`
	runUUID              string               `json:"-"`
	cfgGroupDisplay      string               `json:"-"`
	repmgrVersion        string               `json:"-"`
	repmgrHostname       string               `json:"-"`
	key                  []byte               `json:"-"`
	exitMsg              string               `json:"-"`
	exit                 bool                 `json:"-"`
	canFlashBack         bool                 `json:"-"`
	failoverCond         *nbc.NonBlockingChan `json:"-"`
	switchoverCond       *nbc.NonBlockingChan `json:"-"`
	rejoinCond           *nbc.NonBlockingChan `json:"-"`
	bootstrapCond        *nbc.NonBlockingChan `json:"-"`
	altertableCond       *nbc.NonBlockingChan `json:"-"`
	addtableCond         *nbc.NonBlockingChan `json:"-"`
	statecloseChan       chan state.State     `json:"-"`
	switchoverChan       chan bool            `json:"-"`
	errorChan            chan error           `json:"-"`
	testStopCluster      bool                 `json:"-"`
	testStartCluster     bool                 `json:"-"`
	lastmaster           *ServerMonitor       `json:"-"`
	benchmarkType        string               `json:"-"`
	haveDBTLSCert        bool                 `json:"-"`
	tlsconf              *tls.Config          `json:"-"`
	scheduler            *cron.Cron           `json:"-"`
	tunnel               *ssh.Client          `json:"-"`
	sync.Mutex           `json:"-"`
}

type ClusterSorter []*Cluster

func (a ClusterSorter) Len() int           { return len(a) }
func (a ClusterSorter) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ClusterSorter) Less(i, j int) bool { return a[i].Name < a[j].Name }

type CronEntry struct {
	Schedule string
	Next     time.Time
	Prev     time.Time
	Id       string
}

type Alerts struct {
	Errors   []state.StateHttp `json:"errors"`
	Warnings []state.StateHttp `json:"warnings"`
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
	cluster.MonitorType = map[string]string{
		"mariadb":    "database",
		"mysql":      "database",
		"percona":    "database",
		"maxscale":   "proxy",
		"proxysql":   "proxy",
		"shardproxy": "proxy",
		"haproxy":    "proxy",
		"myproxy":    "proxy",
		"extproxy":   "proxy",
		"sphinx":     "proxy",
	}
	cluster.TopologyType = map[string]string{
		topoMasterSlave:      "master-slave",
		topoBinlogServer:     "binlog-server",
		topoMultiTierSlave:   "multi-tier-slave",
		topoMultiMaster:      "multi-master",
		topoMultiMasterRing:  "multi-master-ring",
		topoMultiMasterWsrep: "multi-master-wsrep",
	}
	// Initialize the state machine at this stage where everything is fine.
	cluster.sme = new(state.StateMachine)
	cluster.sme.Init()

	cluster.Conf = conf
	if cluster.Conf.Interactive {
		cluster.LogPrintf(LvlInfo, "Failover in interactive mode")
	} else {
		cluster.LogPrintf(LvlInfo, "Failover in automatic mode")
	}
	if _, err := os.Stat(cluster.Conf.WorkingDir + "/" + cluster.Name); os.IsNotExist(err) {
		os.MkdirAll(cluster.Conf.WorkingDir+"/"+cluster.Name, os.ModePerm)
	}

	// createKeys do nothing yet
	cluster.createKeys()
	cluster.initScheduler()
	cluster.newServerList()
	err := cluster.newProxyList()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Could not set proxy list %s", err)
	}
	// Reload SLA and crashes
	cluster.GetPersitentState()

	return nil
}

func (cluster *Cluster) initScheduler() {
	if cluster.Conf.MonitorScheduler {
		cluster.LogPrintf(LvlInfo, "Starting cluster scheduler")
		cluster.scheduler = cron.New()

		if cluster.Conf.SchedulerBackupLogical {
			cluster.LogPrintf(LvlInfo, "Schedule logical backup time at: %s", cluster.Conf.BackupLogicalCron)
			cluster.scheduler.AddFunc(cluster.Conf.BackupLogicalCron, func() {
				cluster.master.JobBackupLogical()
			})
		}
		if cluster.Conf.SchedulerBackupPhysical {
			cluster.LogPrintf(LvlInfo, "Schedule physical backup time at: %s", cluster.Conf.BackupPhysicalCron)
			cluster.scheduler.AddFunc(cluster.Conf.BackupPhysicalCron, func() {
				cluster.master.JobBackupPhysical()
			})
		}
		if cluster.Conf.SchedulerDatabaseLogs {
			cluster.LogPrintf(LvlInfo, "Schedule database logs fetch time at: %s", cluster.Conf.BackupDatabaseLogCron)
			cluster.scheduler.Start()
			cluster.scheduler.AddFunc(cluster.Conf.BackupDatabaseLogCron, func() {
				cluster.BackupLogs()
			})
		}
		if cluster.Conf.SchedulerDatabaseOptimize {
			cluster.LogPrintf(LvlInfo, "Schedule database optimize fetch time at: %s", cluster.Conf.BackupDatabaseOptimizeCron)
			cluster.scheduler.Start()
			cluster.scheduler.AddFunc(cluster.Conf.BackupDatabaseOptimizeCron, func() {
				cluster.Optimize()
			})
		}
	}
}

func (cluster *Cluster) Run() {

	interval := time.Second

	for cluster.exit == false {
		cluster.ServerIdList = cluster.GetDBServerIdList()
		cluster.ProxyIdList = cluster.GetProxyServerIdList()
		cluster.Uptime = cluster.GetStateMachine().GetUptime()
		cluster.UptimeFailable = cluster.GetStateMachine().GetUptimeFailable()
		cluster.UptimeSemiSync = cluster.GetStateMachine().GetUptimeSemiSync()
		cluster.MonitorSpin = fmt.Sprintf("%d ", cluster.GetStateMachine().GetHeartbeats())
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
			cluster.TopologyDiscover()
			cluster.IsFailable = cluster.GetStatus()
			if cluster.runOnceAfterTopology {
				if cluster.GetMaster() != nil {
					cluster.initProxies()
					cluster.runOnceAfterTopology = false
				}
			} else {
				cluster.refreshProxies()
				if cluster.sme.SchemaMonitorEndTime+60 < time.Now().Unix() && !cluster.sme.IsInSchemaMonitor() {
					go cluster.schemaMonitor()
				}
				if cluster.Conf.TestInjectTraffic || cluster.Conf.AutorejoinSlavePositionalHeartbeat || cluster.Conf.MonitorWriteHeartbeat {
					go cluster.InjectTraffic()
				}
				go cluster.variableMonitor()
			}

			// split brain management
			if cluster.Conf.Arbitration {
				if cluster.IsSplitBrain {
					err := cluster.SetArbitratorReport()
					if err != nil {
						cluster.SetState("WARN0081", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0081"], err), ErrFrom: "ARB"})
					}
					if cluster.IsSplitBrainBck != cluster.IsSplitBrain {
						time.Sleep(5 * time.Second)
					}
					i := 1
					for i <= 3 {
						i++
						err = cluster.GetArbitratorElection()
						if err != nil {
							cluster.SetState("WARN0082", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0082"], err), ErrFrom: "ARB"})
						} else {
							break //break the loop on success retry 3 times
						}
					}
				}
				cluster.IsSplitBrainBck = cluster.IsSplitBrain
			}

			// switchover / failover only on Active
			cluster.CheckFailed()
			if !cluster.sme.IsInFailover() {
				// trigger action on resolving states
				cstates := cluster.sme.GetResolvedStates()
				for _, s := range cstates {
					if s.ErrKey == "WARN0074" {
						cluster.LogPrintf(LvlInfo, "Sending master physical backup to reseed %s", s.ServerUrl)
						servertoreseed := cluster.GetServerFromURL(s.ServerUrl)
						m := cluster.GetMaster()
						if m != nil {
							go cluster.SSTRunSender(cluster.Conf.WorkingDir+"/"+cluster.Name+"/"+m.Id+"_xtrabackup.xbtream", servertoreseed)
						} else {
							cluster.LogPrintf(LvlErr, "No master backup for physical backup reseeding %s", s.ServerUrl)
						}
					}
					if s.ErrKey == "WARN0075" {
						cluster.LogPrintf(LvlInfo, "Sending master logical backup to reseed %s", s.ServerUrl)
						servertoreseed := cluster.GetServerFromURL(s.ServerUrl)
						m := cluster.GetMaster()
						if m != nil {
							go cluster.SSTRunSender(cluster.Conf.WorkingDir+"/"+cluster.Name+"/"+m.Id+"_mysqldump.sql.gz", servertoreseed)
						} else {
							cluster.LogPrintf(LvlErr, "No master backup for logical backup reseeding %s", s.ServerUrl)
						}
					}
					if s.ErrKey == "WARN0076" {
						cluster.LogPrintf(LvlInfo, "Sending server physical backup to flashback reseed %s", s.ServerUrl)
						servertoreseed := cluster.GetServerFromURL(s.ServerUrl)

						go cluster.SSTRunSender(cluster.Conf.WorkingDir+"/"+cluster.Name+"/"+servertoreseed.Id+"_xtrabackup.xbtream", servertoreseed)

					}
					if s.ErrKey == "WARN0077" {
						cluster.LogPrintf(LvlInfo, "Sending logical backup to flashback reseed %s", s.ServerUrl)
						servertoreseed := cluster.GetServerFromURL(s.ServerUrl)
						go cluster.SSTRunSender(cluster.Conf.WorkingDir+"/"+cluster.Name+"/"+servertoreseed.Id+"_mysqldump.sql.gz", servertoreseed)
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
			cluster.Topology = cluster.GetTopology()
			time.Sleep(interval * time.Duration(cluster.Conf.MonitoringTicker))
		}
	}
}

func (cluster *Cluster) Stop() {
	cluster.scheduler.Stop()
	cluster.exit = true
}

func (cluster *Cluster) Save() error {

	type Save struct {
		Servers string    `json:"servers"`
		Crashes crashList `json:"crashes"`
		SLA     state.Sla `json:"sla"`
	}

	var clsave Save
	clsave.Crashes = cluster.Crashes
	clsave.Servers = cluster.Conf.Hosts
	clsave.SLA = cluster.sme.GetSla()
	saveJson, _ := json.MarshalIndent(clsave, "", "\t")
	err := ioutil.WriteFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/clusterstate.json", saveJson, 0644)
	if err != nil {
		return err
	}

	if strings.Contains(cluster.Conf.ClusterConfigPath, "cluster.d") && cluster.Conf.ConfRewrite {
		var myconf = make(map[string]config.Config)

		myconf[cluster.Name] = cluster.Conf

		file, err := os.OpenFile(cluster.Conf.ClusterConfigPath+"/"+cluster.Name+".toml", os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
		if err != nil {
			if os.IsPermission(err) {
				cluster.LogPrintf(LvlInfo, "File permission denied: %s", cluster.Conf.ClusterConfigPath+"/"+cluster.Name+".toml")
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
	cluster.TopologyDiscover()
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
	err = cluster.TopologyDiscover()
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

// Deprecated tentative to auto generate self signed certificates
func (cluster *Cluster) loadDBCertificate() error {

	if cluster.Conf.HostsTLSCA == "" {
		return errors.New("No given CA certificate")
	}
	if cluster.Conf.HostsTLSCLI == "" {
		return errors.New("No given Client certificate")
	}
	if cluster.Conf.HostsTLSKEY == "" {
		return errors.New("No given Key certificate")
	}
	rootCertPool := x509.NewCertPool()
	pem, err := ioutil.ReadFile(cluster.Conf.HostsTLSCA)
	if err != nil {
		return errors.New("Can not load database TLS Authority CA")
	}
	if ok := rootCertPool.AppendCertsFromPEM(pem); !ok {
		return errors.New("Failed to append PEM.")
	}
	clientCert := make([]tls.Certificate, 0, 1)
	certs, err := tls.LoadX509KeyPair(cluster.Conf.HostsTLSCLI, cluster.Conf.HostsTLSKEY)
	if err != nil {
		return errors.New("Can not load database TLS X509 key pair")
	}
	clientCert = append(clientCert, certs)
	cluster.tlsconf = &tls.Config{
		RootCAs:            rootCertPool,
		Certificates:       clientCert,
		InsecureSkipVerify: true,
	}
	return nil
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

func (cluster *Cluster) ResetCrashes() {
	cluster.Crashes = nil
}
func (cluster *Cluster) Optimize() {
	for _, s := range cluster.slaves {
		jobid, _ := s.JobOptimize()
		cluster.LogPrintf(LvlInfo, "Optimize job id %d on %s ", jobid, s.URL)
	}
}

func (cluster *Cluster) variableMonitor() {
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
	}
	variablesdiff := ""
	for k, v := range masterVariables {

		for _, s := range cluster.slaves {
			slaveVariables := s.Variables
			if slaveVariables[k] != v && exceptVariables[k] != true {
				variablesdiff += "+ Master Variable: " + k + " -> " + v + "\n"
				variablesdiff += "- Slave: " + s.URL + " -> " + slaveVariables[k] + "\n"
			}

		}
	}
	if variablesdiff != "" {
		cluster.SetState("WARN0084", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0084"], variablesdiff), ErrFrom: "MON", ServerUrl: cluster.GetMaster().URL})
	}
}

func (cluster *Cluster) schemaMonitor() {
	if !cluster.Conf.MonitorSchemaChange {
		return
	}
	if cluster.master == nil {
		return
	}
	if cluster.master.State == stateFailed || cluster.master.State == stateMaintenance {
		return
	}
	cluster.sme.SetMonitorSchemaState()

	tables, tablelist, err := dbhelper.GetTables(cluster.master.Conn)
	cluster.master.Tables = tablelist
	if err != nil {
		cluster.LogPrintf(LvlErr, "Could not fetch master tables %s", err)
	}
	var duplicates []*ServerMonitor
	var tableCluster []string

	var tottablesize, totindexsize int64
	for _, t := range tables {

		tottablesize += t.Data_length
		totindexsize += t.Index_length
		cluster.LogPrintf(LvlDbg, "Lookup for table %s", t.Table_schema+"."+t.Table_name)
		tableCluster = nil
		duplicates = nil
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
		} else if oldtable.Table_crc != t.Table_crc {
			haschanged = true
			cluster.LogPrintf(LvlDbg, "Change table %s", t.Table_schema+"."+t.Table_name)
		}
		if haschanged {

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
			for _, pr := range cluster.Proxies {
				if cluster.Conf.MdbsProxyOn && pr.Type == proxySpider {
					cluster.ShardProxyCreateVTable(pr, t.Table_schema, t.Table_name, duplicates, false)
				}
			}
		}
	}
	cluster.DBIndexSize = totindexsize
	cluster.DBTableSize = tottablesize
	cluster.master.DictTables = tables
	cluster.sme.RemoveMonitorSchemaState()
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
		cluster.LogPrintf(LvlInfo, "Arbitration failed attaching failed master %s to electected master :%s", cluster.GetMaster().DSN, realmaster.DSN)
		err := cluster.GetMaster().SetReplicationGTIDCurrentPosFromServer(realmaster)
		if err != nil {
			cluster.LogPrintf("ERROR", "Failed in GTID rejoin lost master to winner master %s", err)
		}
	}

}
