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
	"io/ioutil"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	//"github.com/robfig/cron"

	"github.com/signal18/replication-manager/cluster/nbc"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/cron"
	"github.com/signal18/replication-manager/httplog"
	"github.com/signal18/replication-manager/maxscale"
	"github.com/signal18/replication-manager/misc"
	"github.com/signal18/replication-manager/state"
	"github.com/signal18/replication-manager/termlog"
	log "github.com/sirupsen/logrus"
)

type Cluster struct {
	hostList             []string             `mapstructure:"db-servers-list"`
	proxyList            []string             `mapstructure:"proxy-list"`
	clusterList          map[string]*Cluster  `mapstructure:"-"`
	servers              serverList           `mapstructure:"-"`
	slaves               serverList           `mapstructure:"db-servers-slaves"`
	proxies              proxyList            `mapstructure:"proxies"`
	crashes              crashList            `mapstructure:"db-servers-crashes"`
	master               *ServerMonitor       `mapstructure:"db-servers-master"`
	vmaster              *ServerMonitor       `mapstructure:"db-servers-master-virtual"`
	mxs                  *maxscale.MaxScale   `mapstructure:"-"`
	dbUser               string               `mapstructure:"db-servers-user"`
	dbPass               string               `mapstructure:"-"`
	rplUser              string               `mapstructure:"db-servers-replication-user"`
	rplPass              string               `mapstructure:"-"`
	failoverCtr          int                  `mapstructure:"failover-counter"`
	failoverTs           int64                `mapstructure:"failover-last-time"`
	sme                  *state.StateMachine  `mapstructure:"-"`
	runStatus            string               `mapstructure:"active-passive-status"`
	runOnceAfterTopology bool                 `mapstructure:"passed-fist-detection"`
	conf                 config.Config        `mapstructure:"config"`
	tlog                 *termlog.TermLog     `mapstructure:"-"`
	htlog                *httplog.HttpLog     `mapstructure:"-"`
	logPtr               *os.File             `mapstructure:"-"`
	termlength           int                  `mapstructure:"-"`
	runUUID              string               `mapstructure:"running-uuid"`
	cfgGroup             string               `mapstructure:"config-group"`
	cfgGroupDisplay      string               `mapstructure:"config-group-display"`
	repmgrVersion        string               `mapstructure:"replication-manager-version"`
	repmgrHostname       string               `mapstructure:"replication-manager-hostname"`
	key                  []byte               `mapstructure:"-"`
	exitMsg              string               `mapstructure:"-"`
	exit                 bool                 `mapstructure:"-"`
	CleanAll             bool                 `mapstructure:"clean-all"` //used in testing
	canFlashBack         bool                 `mapstructure:"can-flashback"`
	failoverCond         *nbc.NonBlockingChan `mapstructure:"-"`
	switchoverCond       *nbc.NonBlockingChan `mapstructure:"-"`
	rejoinCond           *nbc.NonBlockingChan `mapstructure:"-"`
	bootstrapCond        *nbc.NonBlockingChan `mapstructure:"-"`
	switchoverChan       chan bool            `mapstructure:"-"`
	errorChan            chan error           `mapstructure:"-"`
	testStopCluster      bool                 `mapstructure:"test-stop-cluster"`
	testStartCluster     bool                 `mapstructure:"test-start-cluster"`
	isDown               bool                 `mapstructure:"is-down"`
	isProvisionned       bool                 `mapstructure:"is-provision"`
	lastmaster           *ServerMonitor       `mapstructure:"last-master"` //saved when all cluster down
	benchmarkType        string               `mapstructure:"benchmark-type"`
	haveDBTLSCert        bool                 `mapstructure:"have-db-tls-cert"`
	tlsconf              *tls.Config          `mapstructure:"-"`
	scheduler            *cron.Cron           `mapstructure:"-"`
	schedule             []CronEntry          `mapstructure:"replication-manager-schedule"`
}

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
func (cluster *Cluster) Init(conf config.Config, cfgGroup string, tlog *termlog.TermLog, httplog *httplog.HttpLog, termlength int, runUUID string, repmgrVersion string, repmgrHostname string, key []byte) error {
	// Initialize the state machine at this stage where everything is fine.
	cluster.switchoverChan = make(chan bool)
	cluster.errorChan = make(chan error)
	cluster.failoverCond = nbc.New()
	cluster.switchoverCond = nbc.New()
	cluster.rejoinCond = nbc.New()
	cluster.canFlashBack = true
	cluster.runOnceAfterTopology = true
	cluster.testStopCluster = true
	cluster.testStartCluster = true
	cluster.conf = conf
	cluster.tlog = tlog
	cluster.htlog = httplog
	cluster.termlength = termlength
	cluster.cfgGroup = cfgGroup
	cluster.runUUID = runUUID
	cluster.repmgrHostname = repmgrHostname
	cluster.repmgrVersion = repmgrVersion
	cluster.key = key
	cluster.sme = new(state.StateMachine)
	cluster.runStatus = ConstMonitorActif
	cluster.benchmarkType = "sysbench"
	cluster.sme.Init()
	if cluster.conf.MonitorScheduler {
		cluster.LogPrintf(LvlInfo, "Starting cluster scheduler")
		cluster.scheduler = cron.New()

		if cluster.conf.SchedulerBackupLogical {
			cluster.LogPrintf(LvlInfo, "Schedule logical backup time at: %s", conf.BackupLogicalCron)
			cluster.scheduler.AddFunc(conf.BackupLogicalCron, func() {
				cluster.master.JobBackupLogical()
			})
		}
		if cluster.conf.SchedulerBackupPhysical {
			cluster.LogPrintf(LvlInfo, "Schedule physical backup time at: %s", conf.BackupPhysicalCron)
			cluster.scheduler.AddFunc(conf.BackupPhysicalCron, func() {
				cluster.master.JobBackupPhysical()
			})
		}
		if cluster.conf.SchedulerBackupPhysical {
			cluster.LogPrintf(LvlInfo, "Schedule database logs fetch time at: %s", conf.BackupDatabaseLogCron)
			cluster.scheduler.Start()
			cluster.scheduler.AddFunc(conf.BackupDatabaseLogCron, func() {
				cluster.BackupLogs()
			})
		}
		if cluster.conf.SchedulerDatabaseOptimize {
			cluster.LogPrintf(LvlInfo, "Schedule database optimize fetch time at: %s", conf.BackupDatabaseOptimizeCron)
			cluster.scheduler.Start()
			cluster.scheduler.AddFunc(conf.BackupDatabaseOptimizeCron, func() {
				cluster.Optimize()
			})
		}
	}
	cluster.LogPrintf(LvlInfo, "Loading database TLS certificates")
	err := cluster.loadDBCertificate()
	if err != nil {
		cluster.haveDBTLSCert = false
		cluster.LogPrintf(LvlInfo, "Don't Have database TLS certificates")
	} else {
		cluster.haveDBTLSCert = true
		cluster.LogPrintf(LvlInfo, "Have database TLS certificates")
	}
	cluster.newServerList()
	if cluster.conf.Interactive {
		cluster.LogPrintf(LvlInfo, "Failover in interactive mode")
	} else {
		cluster.LogPrintf(LvlInfo, "Failover in automatic mode")
	}
	err = cluster.newProxyList()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Could not set proxy list %s", err)
	}
	cluster.ReloadFromSave()
	if _, err := os.Stat(cluster.conf.WorkingDir + "/" + cluster.cfgGroup); os.IsNotExist(err) {
		os.MkdirAll(cluster.conf.WorkingDir+"/"+cluster.cfgGroup, os.ModePerm)
		cluster.CreateKey()
	}

	return nil
}

func (cluster *Cluster) Stop() {
	cluster.scheduler.Stop()
	cluster.exit = true
}
func (cluster *Cluster) Run() {

	interval := time.Second
	//ticker := time.NewTicker(interval * time.Duration(cluster.conf.MonitoringTicker))
	for cluster.exit == false {

		//select {
		//case <-ticker.C:

		//cluster.display()

		select {
		case sig := <-cluster.switchoverChan:
			if sig {
				if cluster.runStatus == "A" {
					cluster.LogPrintf(LvlInfo, "Signaling Switchover...")
					cluster.MasterFailover(false)
					cluster.switchoverCond.Send <- true
				} else {
					cluster.LogPrintf(LvlInfo, "Not in active mode, cancel switchover %s", cluster.runStatus)
				}
			}

		default:
			if cluster.conf.LogLevel > 2 {
				cluster.LogPrintf(LvlDbg, "Monitoring server loop")
				for k, v := range cluster.servers {
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

			if cluster.runOnceAfterTopology {
				if cluster.GetMaster() != nil {
					cluster.initProxies()
					cluster.runOnceAfterTopology = false
				}
			} else {
				cluster.refreshProxies()
				if cluster.conf.TestInjectTraffic || cluster.conf.AutorejoinSlavePositionalHearbeat || cluster.conf.MonitorWriteHeartbeat {
					go cluster.InjectTraffic()
				}
			}
			// switchover / failover only on Active
			cluster.CheckFailed()
			if !cluster.sme.IsInFailover() {
				states := cluster.sme.GetStates()
				for i := range states {
					cluster.LogPrintf("STATE", states[i])
				}
				cluster.sme.ClearState()
				if cluster.sme.GetHeartbeats()%60 == 0 {
					cluster.Save()
				}
			}
			time.Sleep(interval * time.Duration(cluster.conf.MonitoringTicker))

		}
		//	}
	}
}

func (cluster *Cluster) Save() error {

	type Save struct {
		Servers string    `json:"servers"`
		Crashes crashList `json:"crashes"`
		SLA     state.Sla `json:"sla"`
	}

	var clsave Save
	clsave.Crashes = cluster.crashes
	clsave.Servers = cluster.conf.Hosts
	clsave.SLA = cluster.sme.GetSla()
	saveJson, _ := json.MarshalIndent(clsave, "", "\t")
	err := ioutil.WriteFile(cluster.conf.WorkingDir+"/"+cluster.cfgGroup+"/clusterstate.json", saveJson, 0644)
	if err != nil {
		return err
	}

	if strings.Contains(cluster.conf.ClusterConfigPath, "cluster.d") {
		var myconf = make(map[string]config.Config)

		myconf[cluster.cfgGroup] = cluster.conf

		file, err := os.OpenFile(cluster.conf.ClusterConfigPath+"/"+cluster.cfgGroup+".toml", os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0666)
		if err != nil {
			if os.IsPermission(err) {
				cluster.LogPrintf(LvlInfo, "File permission denied: %s", cluster.conf.ClusterConfigPath+"/"+cluster.cfgGroup+".toml")
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

func (cluster *Cluster) ReloadFromSave() error {

	type Save struct {
		Servers string    `json:"servers"`
		Crashes crashList `json:"crashes"`
		SLA     state.Sla `json:"sla"`
	}

	var clsave Save
	file, err := ioutil.ReadFile(cluster.conf.WorkingDir + "/" + cluster.cfgGroup + "/clusterstate.json")
	if err != nil {
		cluster.LogPrintf(LvlWarn, "File error: %v\n", err)
		return err
	}
	err = json.Unmarshal(file, &clsave)
	if err != nil {
		cluster.LogPrintf(LvlErr, "File error: %v\n", err)
		return err
	}
	if len(clsave.Crashes) > 0 {
		cluster.LogPrintf(LvlInfo, "Restoring %d crashes from file: %s\n", len(clsave.Crashes), cluster.conf.WorkingDir+"/"+cluster.cfgGroup+".json")
	}
	cluster.crashes = clsave.Crashes
	cluster.sme.SetSla(clsave.SLA)
	return nil
}

func (cluster *Cluster) InitAgent(conf config.Config) {
	cluster.conf = conf
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
	cluster.conf = conf
	cluster.sme.SetFailoverState()
	cluster.newServerList()
	cluster.TopologyDiscover()
	cluster.sme.RemoveFailoverState()
}

func (cluster *Cluster) FailoverForce() error {
	sf := stateFile{Name: "/tmp/mrm" + cluster.cfgGroup + ".state"}
	err := sf.access()
	if err != nil {
		cluster.LogPrintf(LvlWarn, "Could not create state file")
	}
	err = sf.read()
	if err != nil {
		cluster.LogPrintf(LvlWarn, "Could not read values from state file:", err)
	} else {
		cluster.failoverCtr = int(sf.Count)
		cluster.failoverTs = sf.Timestamp
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
			for _, s := range cluster.servers {
				if s.State == "" {
					s.State = stateFailed
					if cluster.conf.LogLevel > 2 {
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
	if cluster.conf.FailLimit > 0 && cluster.failoverCtr >= cluster.conf.FailLimit {
		cluster.LogPrintf(LvlErr, "Failover has exceeded its configured limit of %d. Remove /tmp/mrm.state file to reinitialize the failover counter", cluster.conf.FailLimit)
		return errors.New("ERROR: Failover has exceeded its configured limit")
	}
	rem := (cluster.failoverTs + cluster.conf.FailTime) - time.Now().Unix()
	if cluster.conf.FailTime > 0 && rem > 0 {
		cluster.LogPrintf(LvlErr, "Failover time limit enforced. Next failover available in %d seconds", rem)
		return errors.New("ERROR: Failover time limit enforced")
	}
	if cluster.MasterFailover(true) {
		sf.Count++
		sf.Timestamp = cluster.failoverTs
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

func (cluster *Cluster) loadDBCertificate() error {

	if cluster.conf.HostsTLSCA == "" {
		return errors.New("No given CA certificate")
	}
	if cluster.conf.HostsTLSCLI == "" {
		return errors.New("No given Client certificate")
	}
	if cluster.conf.HostsTLSKEY == "" {
		return errors.New("No given Key certificate")
	}
	rootCertPool := x509.NewCertPool()
	pem, err := ioutil.ReadFile(cluster.conf.HostsTLSCA)
	if err != nil {
		return errors.New("Can not load database TLS Authority CA")
	}
	if ok := rootCertPool.AppendCertsFromPEM(pem); !ok {
		return errors.New("Failed to append PEM.")
	}
	clientCert := make([]tls.Certificate, 0, 1)
	certs, err := tls.LoadX509KeyPair(cluster.conf.HostsTLSCLI, cluster.conf.HostsTLSKEY)
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

	for _, server := range cluster.servers {
		defer server.Conn.Close()
	}
}

func (cluster *Cluster) ResetFailoverCtr() {
	cluster.failoverCtr = 0
	cluster.failoverTs = 0
}

func (cluster *Cluster) agentFlagCheck() {

	// if slaves option has been supplied, split into a slice.
	if cluster.conf.Hosts != "" {
		cluster.hostList = strings.Split(cluster.conf.Hosts, ",")
	} else {
		log.Fatal("No hosts list specified")
	}
	if len(cluster.hostList) > 1 {
		log.Fatal("Agent can only monitor a single host")
	}
	// validate users.
	if cluster.conf.User == "" {
		log.Fatal("No master user/pair specified")
	}
	cluster.dbUser, cluster.dbPass = misc.SplitPair(cluster.conf.User)
}

func (cluster *Cluster) BackupLogs() {
	for _, s := range cluster.servers {
		s.JobBackupErrorLog()
		s.JobBackupSlowQueryLog()
	}
}

func (cluster *Cluster) Optimize() {
	for _, s := range cluster.slaves {
		jobid, _ := s.JobOptimize()
		cluster.LogPrintf(LvlInfo, "Optimize job id %d on %s ", jobid, s.URL)
	}
}
