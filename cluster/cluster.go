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
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	//"github.com/robfig/cron"

	"github.com/signal18/replication-manager/cluster/nbc"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/cron"
	"github.com/signal18/replication-manager/dbhelper"
	"github.com/signal18/replication-manager/httplog"
	"github.com/signal18/replication-manager/maxscale"
	"github.com/signal18/replication-manager/state"
	"github.com/signal18/replication-manager/termlog"
	log "github.com/sirupsen/logrus"
)

type Cluster struct {
	Name                 string        `json:"name"`
	Servers              serverList    `json:"-"`
	ServerIdList         []string      `json:"dbServers"`
	Crashes              crashList     `json:"dbServersCrashes"`
	Proxies              proxyList     `json:"-"`
	ProxyIdList          []string      `json:"proxyServers"`
	FailoverCtr          int           `json:"failoverCounter"`
	FailoverTs           int64         `json:"failoverLastTime"`
	Status               string        `json:"activePassiveStatus"`
	Conf                 config.Config `json:"config"`
	CleanAll             bool          `json:"cleanReplication"` //used in testing
	IsDown               bool          `json:"isDown"`
	IsProvisionned       bool          `json:"isProvisionned"`
	Schedule             []CronEntry   `json:"schedule"`
	DBTags               []string      `json:"dbServersTags"`
	ProxyTags            []string      `json:"proxyServersTags"`
	Topology             string        `json:"topology"`
	Uptime               string        `json:"uptime"`
	UptimeFailable       string        `json:"uptimeFailable"`
	UptimeSemiSync       string        `json:"uptimeSemisync"`
	MonitorSpin          string        `json:"monitorSpin"`
	hostList             []string
	proxyList            []string
	clusterList          map[string]*Cluster
	slaves               serverList
	master               *ServerMonitor
	vmaster              *ServerMonitor
	mxs                  *maxscale.MaxScale
	dbUser               string
	dbPass               string
	rplUser              string
	rplPass              string
	sme                  *state.StateMachine
	runOnceAfterTopology bool
	tlog                 *termlog.TermLog
	htlog                *httplog.HttpLog
	logPtr               *os.File
	termlength           int
	runUUID              string
	cfgGroupDisplay      string
	repmgrVersion        string
	repmgrHostname       string
	key                  []byte
	exitMsg              string
	exit                 bool
	canFlashBack         bool
	failoverCond         *nbc.NonBlockingChan
	switchoverCond       *nbc.NonBlockingChan
	rejoinCond           *nbc.NonBlockingChan
	bootstrapCond        *nbc.NonBlockingChan
	altertableCond       *nbc.NonBlockingChan
	addtableCond         *nbc.NonBlockingChan
	statecloseChan       chan state.State
	switchoverChan       chan bool
	errorChan            chan error
	testStopCluster      bool
	testStartCluster     bool
	lastmaster           *ServerMonitor
	benchmarkType        string
	haveDBTLSCert        bool
	tlsconf              *tls.Config
	scheduler            *cron.Cron
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
	// should use buffered channels or it will block
	cluster.statecloseChan = make(chan state.State, 100)
	//cluster.changetableChan = make(chan state.State, 100)

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
	cluster.Conf = conf
	cluster.tlog = tlog
	cluster.htlog = httplog
	cluster.termlength = termlength
	cluster.Name = cfgGroup
	cluster.runUUID = runUUID
	cluster.repmgrHostname = repmgrHostname
	cluster.repmgrVersion = repmgrVersion
	cluster.key = key
	cluster.sme = new(state.StateMachine)
	cluster.Status = ConstMonitorActif
	cluster.benchmarkType = "sysbench"
	cluster.DBTags = cluster.GetDatabaseTags()
	cluster.ProxyTags = cluster.GetProxyTags()

	cluster.sme.Init()
	if cluster.Conf.MonitorScheduler {
		cluster.LogPrintf(LvlInfo, "Starting cluster scheduler")
		cluster.scheduler = cron.New()

		if cluster.Conf.SchedulerBackupLogical {
			cluster.LogPrintf(LvlInfo, "Schedule logical backup time at: %s", conf.BackupLogicalCron)
			cluster.scheduler.AddFunc(conf.BackupLogicalCron, func() {
				cluster.master.JobBackupLogical()
			})
		}
		if cluster.Conf.SchedulerBackupPhysical {
			cluster.LogPrintf(LvlInfo, "Schedule physical backup time at: %s", conf.BackupPhysicalCron)
			cluster.scheduler.AddFunc(conf.BackupPhysicalCron, func() {
				cluster.master.JobBackupPhysical()
			})
		}
		if cluster.Conf.SchedulerBackupPhysical {
			cluster.LogPrintf(LvlInfo, "Schedule database logs fetch time at: %s", conf.BackupDatabaseLogCron)
			cluster.scheduler.Start()
			cluster.scheduler.AddFunc(conf.BackupDatabaseLogCron, func() {
				cluster.BackupLogs()
			})
		}
		if cluster.Conf.SchedulerDatabaseOptimize {
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
	if cluster.Conf.Interactive {
		cluster.LogPrintf(LvlInfo, "Failover in interactive mode")
	} else {
		cluster.LogPrintf(LvlInfo, "Failover in automatic mode")
	}
	err = cluster.newProxyList()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Could not set proxy list %s", err)
	}
	cluster.ReloadFromSave()
	if _, err := os.Stat(cluster.Conf.WorkingDir + "/" + cluster.Name); os.IsNotExist(err) {
		os.MkdirAll(cluster.Conf.WorkingDir+"/"+cluster.Name, os.ModePerm)
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
				if cluster.Conf.TestInjectTraffic || cluster.Conf.AutorejoinSlavePositionalHearbeat || cluster.Conf.MonitorWriteHeartbeat {
					go cluster.InjectTraffic()
				}
			}
			// switchover / failover only on Active
			cluster.CheckFailed()
			if !cluster.sme.IsInFailover() {
				cstates := cluster.sme.GetResolvedStates()
				for _, s := range cstates {

					if s.ErrKey == "WARN0074" {
						cluster.LogPrintf(LvlInfo, "Sending physical backup to reseed %s", s.ServerUrl)
						servertoreseed := cluster.GetServerFromURL(s.ServerUrl)
						m := cluster.GetMaster()
						if m != nil {
							go cluster.SSTRunSender(cluster.Conf.WorkingDir+"/"+cluster.Name+"/"+m.Id+"_xtrabackup.xbtream", servertoreseed)
						} else {
							cluster.LogPrintf(LvlErr, "No master backup for physical backup reseeding %s", s.ServerUrl)
						}
					}
					//		cluster.statecloseChan <- s
				}
				states := cluster.sme.GetStates()
				for i := range states {
					cluster.LogPrintf("STATE", states[i])
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

	if strings.Contains(cluster.Conf.ClusterConfigPath, "cluster.d") {
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

func (cluster *Cluster) ReloadFromSave() error {

	type Save struct {
		Servers string    `json:"servers"`
		Crashes crashList `json:"crashes"`
		SLA     state.Sla `json:"sla"`
	}

	var clsave Save
	file, err := ioutil.ReadFile(cluster.Conf.WorkingDir + "/" + cluster.Name + "/clusterstate.json")
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
		cluster.LogPrintf(LvlInfo, "Restoring %d crashes from file: %s\n", len(clsave.Crashes), cluster.Conf.WorkingDir+"/"+cluster.Name+".json")
	}
	cluster.Crashes = clsave.Crashes
	cluster.sme.SetSla(clsave.SLA)
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

func (cluster *Cluster) Optimize() {
	for _, s := range cluster.slaves {
		jobid, _ := s.JobOptimize()
		cluster.LogPrintf(LvlInfo, "Optimize job id %d on %s ", jobid, s.URL)
	}
}

func (cluster *Cluster) schemaMonitor() {
	if !cluster.Conf.MonitorSchemaChange && !cluster.Conf.MdbsProxyOn {
		return
	}
	cluster.sme.SetMonitorSchemaState()

	tables, err := dbhelper.GetTables(cluster.master.Conn)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Could not fetch master tables %s", err)
	}
	var duplicates []*ServerMonitor
	for _, t := range tables {
		cluster.LogPrintf(LvlDbg, "Lookup for table %s", t.Table_schema+"."+t.Table_name)
		duplicates = nil
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
							cluster.LogPrintf(LvlDbg, "Found duplicate table %s in %s", t.Table_schema+"."+t.Table_name, cl.GetMaster().URL)
						}
					}
				}
			}
			for _, pr := range cluster.Proxies {
				if cluster.Conf.MdbsProxyOn && pr.Type == proxySpider {
					cluster.createMdbsproxyVTable(pr, t, duplicates)
				}
			}
		}
	}
	cluster.master.DictTables = tables
	cluster.sme.RemoveMonitorSchemaState()
}
