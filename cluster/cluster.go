// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 SIGNAL 18 SARL
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

	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"

	"github.com/signal18/replication-manager/cluster/nbc"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/crypto"
	"github.com/signal18/replication-manager/dbhelper"
	"github.com/signal18/replication-manager/maxscale"
	"github.com/signal18/replication-manager/misc"
	"github.com/signal18/replication-manager/state"
	"github.com/signal18/replication-manager/termlog"
)

type Cluster struct {
	hostList             []string
	proxyList            []string
	servers              serverList
	slaves               serverList
	proxies              proxyList
	crashes              crashList
	master               *ServerMonitor
	mxs                  *maxscale.MaxScale
	dbUser               string
	dbPass               string
	rplUser              string
	rplPass              string
	failoverCtr          int
	failoverTs           int64
	sme                  *state.StateMachine
	runStatus            string
	runOnceAfterTopology bool
	ignoreList           []string
	conf                 config.Config
	tlog                 *termlog.TermLog
	logPtr               *os.File
	termlength           int
	runUUID              string
	cfgGroup             string
	cfgGroupDisplay      string
	repmgrVersion        string
	repmgrHostname       string
	key                  []byte
	exitMsg              string
	exit                 bool
	CleanAll             bool
	canFlashBack         bool
	failoverCond         *nbc.NonBlockingChan
	switchoverCond       *nbc.NonBlockingChan
	rejoinCond           *nbc.NonBlockingChan
	bootstrapCond        *nbc.NonBlockingChan
	switchoverChan       chan bool
	testStopCluster      bool
	testStartCluster     bool
	clusterDown          bool
	isProvisionned       bool
	lastmaster           *ServerMonitor //saved when all cluster down
	benchmarkType        string
	openSVCServiceStatus int
	haveDBTLSCert        bool
	tlsconf              *tls.Config
}

type Test struct {
	Name       string        `json:"name"`
	Result     string        `json:"result"`
	ConfigFile string        `json:"config-file"`
	ConfigInit config.Config `json:"config-init"`
	ConfigTest config.Config `json:"config-test"`
}

const (
	stateClusterStart string = "Running starting"
	stateClusterDown  string = "Running cluster down"
	stateClusterErr   string = "Running with errors"
	stateClusterWarn  string = "Running with warnings"
	stateClusterRun   string = "Running"
)

// Init initial cluster definition
func (cluster *Cluster) Init(conf config.Config, cfgGroup string, tlog *termlog.TermLog, termlength int, runUUID string, repmgrVersion string, repmgrHostname string, key []byte) error {
	// Initialize the state machine at this stage where everything is fine.
	cluster.switchoverChan = make(chan bool)
	cluster.failoverCond = nbc.New()
	cluster.switchoverCond = nbc.New()
	cluster.rejoinCond = nbc.New()
	cluster.canFlashBack = true
	cluster.runOnceAfterTopology = true
	cluster.testStopCluster = true
	cluster.testStartCluster = true
	cluster.conf = conf
	cluster.tlog = tlog
	cluster.termlength = termlength
	cluster.cfgGroup = cfgGroup
	cluster.runUUID = runUUID
	cluster.repmgrHostname = repmgrHostname
	cluster.repmgrVersion = repmgrVersion
	cluster.key = key
	cluster.sme = new(state.StateMachine)
	cluster.runStatus = "A"
	cluster.benchmarkType = "sysbench"
	cluster.sme.Init()

	cluster.LogPrintf("INFO", "Loading database TLS certificates")
	err := cluster.loadDBCertificate()
	if err != nil {
		cluster.haveDBTLSCert = false
		cluster.LogPrintf("INFO", "Don't Have database TLS certificates")
	} else {
		cluster.haveDBTLSCert = true
		cluster.LogPrintf("INFO", "Have database TLS certificates")
	}
	cluster.newServerList()
	if cluster.conf.Interactive {
		cluster.LogPrintf("INFO", "Failover in interactive mode")
	} else {
		cluster.LogPrintf("INFO", "Failover in automatic mode")
	}
	cluster.ReloadFromSave()
	return nil
}

func (cluster *Cluster) Stop() {
	cluster.exit = true
}
func (cluster *Cluster) Run() {

	interval := time.Second
	ticker := time.NewTicker(interval * time.Duration(cluster.conf.MonitoringTicker))
	for cluster.exit == false {

		select {
		case <-ticker.C:

			//cluster.display()

			if cluster.conf.LogLevel > 2 {
				cluster.LogPrintf("DEBUG", "Monitoring server loop")
				for k, v := range cluster.servers {
					cluster.LogPrintf("DEBUG", "Server [%d]: URL: %-15s State: %6s PrevState: %6s", k, v.URL, v.State, v.PrevState)
				}
				if cluster.master != nil {
					cluster.LogPrintf("DEBUG", "Master [ ]: URL: %-15s State: %6s PrevState: %6s", cluster.master.URL, cluster.master.State, cluster.master.PrevState)
					for k, v := range cluster.slaves {
						cluster.LogPrintf("DEBUG", "Slave  [%d]: URL: %-15s State: %6s PrevState: %6s", k, v.URL, v.State, v.PrevState)
					}
				}
			}

			cluster.TopologyDiscover()

			if cluster.runOnceAfterTopology {
				if cluster.master != nil {
					cluster.initProxies()
					cluster.runOnceAfterTopology = false
				}
			} else {
				cluster.refreshProxies()
				if cluster.conf.TestInjectTraffic {
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
			}

			select {
			case sig := <-cluster.switchoverChan:
				if sig {
					if cluster.runStatus == "A" {
						cluster.LogPrint("Signaling Switchover..")
						cluster.MasterFailover(false)
						cluster.switchoverCond.Send <- true
					} else {
						cluster.LogPrintf("INFO", "Not in active mode, cancel switchover %s", cluster.runStatus)
					}
				}

			default:
				//do nothing
			}
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
	clsave.Crashes = cluster.crashes
	clsave.Servers = cluster.conf.Hosts
	clsave.SLA = cluster.sme.GetSla()
	saveJson, _ := json.MarshalIndent(clsave, "", "\t")
	err := ioutil.WriteFile(cluster.conf.WorkingDir+"/"+cluster.cfgGroup+".json", saveJson, 0644)
	if err != nil {
		return err
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
	file, err := ioutil.ReadFile(cluster.conf.WorkingDir + "/" + cluster.cfgGroup + ".json")
	if err != nil {
		cluster.LogPrintf("WARN", "File error: %v\n", err)
		return err
	}
	err = json.Unmarshal(file, &clsave)
	if err != nil {
		cluster.LogPrintf("ERROR", "File error: %v\n", err)
		return err
	}
	if len(clsave.Crashes) > 0 {
		cluster.LogPrintf("INFO", "Restoring %d crashes from file: %s\n", len(clsave.Crashes), cluster.conf.WorkingDir+"/"+cluster.cfgGroup+".json")
	}
	cluster.crashes = clsave.Crashes
	cluster.sme.SetSla(clsave.SLA)
	return nil
}

func (cluster *Cluster) InitAgent(conf config.Config) (*sqlx.DB, error) {
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
	db, err := dbhelper.MemDBConnect()
	if err != nil {
		log.WithError(err).Error("Error opening database connection")
		return nil, err
	}

	return db, nil
}

func (cluster *Cluster) SetCfgGroupDisplay(cfgGroup string) {
	cluster.cfgGroupDisplay = cfgGroup
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
		cluster.LogPrintf("WARNING", "Could not create state file")
	}
	err = sf.read()
	if err != nil {
		cluster.LogPrintf("WARNING", "Could not read values from state file:", err)
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
						cluster.LogPrintf("DEBUG", "State failed set by state detection ERR00012")
					}
					cluster.master = s
				}
			}
		} else {
			return err

		}
	}
	if cluster.master == nil {
		cluster.LogPrintf("ERROR", "Could not find a failed server in the hosts list")
		return errors.New("ERROR: Could not find a failed server in the hosts list")
	}
	if cluster.conf.FailLimit > 0 && cluster.failoverCtr >= cluster.conf.FailLimit {
		cluster.LogPrintf("ERROR", "Failover has exceeded its configured limit of %d. Remove /tmp/mrm.state file to reinitialize the failover counter", cluster.conf.FailLimit)
		return errors.New("ERROR: Failover has exceeded its configured limit")
	}
	rem := (cluster.failoverTs + cluster.conf.FailTime) - time.Now().Unix()
	if cluster.conf.FailTime > 0 && rem > 0 {
		cluster.LogPrintf("ERROR", "Failover time limit enforced. Next failover available in %d seconds", rem)
		return errors.New("ERROR: Failover time limit enforced")
	}
	if cluster.MasterFailover(true) {
		sf.Count++
		sf.Timestamp = cluster.failoverTs
		err := sf.write()
		if err != nil {
			cluster.LogPrintf("WARN", "Could not write values to state file:%s", err)
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

// Check that mandatory flags have correct values. This is not part of the state machine and mandatory flags
// must lead to Fatal errors if initialized with wrong values.

func (cluster *Cluster) repmgrFlagCheck() error {
	if cluster.conf.LogFile != "" {
		var err error
		cluster.logPtr, err = os.OpenFile(cluster.conf.LogFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
		if err != nil {
			cluster.LogPrintf("ERROR", "Failed opening logfile, disabling for the rest of the session")
			cluster.conf.LogFile = ""
		}
	}
	// if slaves option has been supplied, split into a slice.
	if cluster.conf.Hosts != "" {
		cluster.hostList = strings.Split(cluster.conf.Hosts, ",")
	} else {
		cluster.LogPrintf("ERROR", "No hosts list specified")
		return errors.New("No hosts list specified")
	}

	// validate users
	if cluster.conf.User == "" {
		cluster.LogPrintf("ERROR", "No master user/pair specified")
		return errors.New("No master user/pair specified")
	}
	cluster.dbUser, cluster.dbPass = misc.SplitPair(cluster.conf.User)

	if cluster.conf.RplUser == "" {
		cluster.LogPrintf("ERROR", "No replication user/pair specified")
		return errors.New("No replication user/pair specified")
	}
	cluster.rplUser, cluster.rplPass = misc.SplitPair(cluster.conf.RplUser)

	if cluster.key != nil {
		p := crypto.Password{Key: cluster.key}
		p.CipherText = cluster.dbPass
		p.Decrypt()
		cluster.dbPass = p.PlainText
		p.CipherText = cluster.rplPass
		p.Decrypt()
		cluster.rplPass = p.PlainText
	}

	if cluster.conf.IgnoreSrv != "" {
		cluster.ignoreList = strings.Split(cluster.conf.IgnoreSrv, ",")
	}

	// Check if preferred master is included in Host List
	pfa := strings.Split(cluster.conf.PrefMaster, ",")
	if len(pfa) > 1 {
		cluster.LogPrintf("ERROR", "Prefmaster option takes exactly one argument")
		return errors.New("Prefmaster option takes exactly one argument")
	}
	ret := func() bool {
		for _, v := range cluster.hostList {
			if v == cluster.conf.PrefMaster {
				return true
			}
		}
		return false
	}
	if ret() == false && cluster.conf.PrefMaster != "" {
		cluster.LogPrintf("ERROR", "Preferred master is not included in the hosts option")
		return errors.New("Prefmaster option takes exactly one argument")
	}
	return nil
}

func (cluster *Cluster) IsInHostList(host string) bool {
	for _, v := range cluster.hostList {
		if v == host {
			return true
		}
	}
	return false
}

func (cluster *Cluster) ToggleInteractive() {
	if cluster.conf.Interactive == true {
		cluster.conf.Interactive = false
		cluster.LogPrintf("INFO", "Failover monitor switched to automatic mode")
	} else {
		cluster.conf.Interactive = true
		cluster.LogPrintf("INFO", "Failover monitor switched to manual mode")
	}
}

func (cluster *Cluster) SetInteractive(check bool) {
	cluster.conf.Interactive = check
}

func (cluster *Cluster) SetTraffic(traffic bool) {
	cluster.SetBenchMethod("table")
	cluster.PrepareBench()
	cluster.conf.TestInjectTraffic = traffic
}

func (cluster *Cluster) GetTraffic() bool {
	return cluster.conf.TestInjectTraffic
}

func (cluster *Cluster) SetBenchMethod(m string) {
	cluster.benchmarkType = m
}

func (cluster *Cluster) SetPrefMaster(PrefMaster string) {
	cluster.conf.PrefMaster = PrefMaster
}

func (cluster *Cluster) ResetFailoverCtr() {
	cluster.failoverCtr = 0
	cluster.failoverTs = 0
}

func (cluster *Cluster) SetFailoverCtr(failoverCtr int) {
	cluster.failoverCtr = failoverCtr
}

func (cluster *Cluster) SetFailoverTs(failoverTs int64) {
	cluster.failoverTs = failoverTs
}

func (cluster *Cluster) SetCheckFalsePositiveHeartbeat(CheckFalsePositiveHeartbeat bool) {
	cluster.conf.CheckFalsePositiveHeartbeat = CheckFalsePositiveHeartbeat
}

func (cluster *Cluster) GetServers() serverList {
	return cluster.servers
}

func (cluster *Cluster) GetSlaves() serverList {
	return cluster.slaves
}
func (cluster *Cluster) GetProxies() proxyList {
	return cluster.proxies
}
func (cluster *Cluster) GetMaster() *ServerMonitor {
	return cluster.master
}

func (cluster *Cluster) GetConf() config.Config {
	return cluster.conf
}

func (cluster *Cluster) GetWaitTrx() int64 {
	return cluster.conf.SwitchWaitTrx
}

func (cluster *Cluster) GetStateMachine() *state.StateMachine {
	return cluster.sme
}

func (cluster *Cluster) GetMasterFailCount() int {
	return cluster.master.FailCount
}

func (cluster *Cluster) GetFailoverCtr() int {
	return cluster.failoverCtr
}

func (cluster *Cluster) GetFailoverTs() int64 {
	return cluster.failoverTs
}

func (cluster *Cluster) GetRunStatus() string {
	return cluster.runStatus
}

func (cluster *Cluster) IsMasterFailed() bool {
	if cluster.master.State == stateFailed {
		return true
	} else {
		return false
	}
}

func (cluster *Cluster) SetFailRestartUnsafe(check bool) {
	cluster.conf.FailRestartUnsafe = check
}

func (cluster *Cluster) SetSlavesReadOnly(check bool) {
	for _, sl := range cluster.slaves {
		dbhelper.SetReadOnly(sl.Conn, check)
	}
}
func (cluster *Cluster) SetReadOnly(check bool) {
	cluster.conf.ReadOnly = check
}

func (cluster *Cluster) SwitchReadOnly() {
	cluster.conf.ReadOnly = !cluster.conf.ReadOnly
}

func (cluster *Cluster) SetRplChecks(check bool) {
	cluster.conf.RplChecks = check
}
func (cluster *Cluster) SwitchRplChecks() {
	cluster.conf.RplChecks = !cluster.conf.RplChecks
}

func (cluster *Cluster) SetRplMaxDelay(delay int64) {
	cluster.conf.FailMaxDelay = delay
}

func (cluster *Cluster) SetCleanAll(check bool) {
	cluster.CleanAll = check
}
func (cluster *Cluster) SwitchCleanAll() {
	cluster.CleanAll = !cluster.CleanAll
}

func (cluster *Cluster) GetRplChecks() bool {
	return cluster.conf.RplChecks
}

func (cluster *Cluster) GetMaxFail() int {
	return cluster.conf.MaxFail
}

func (cluster *Cluster) SetFailLimit(limit int) {
	cluster.conf.FailLimit = limit
}

func (cluster *Cluster) SetFailTime(time int64) {
	cluster.conf.FailTime = time
}

func (cluster *Cluster) SetMasterStateFailed() {
	cluster.master.State = stateFailed
}

func (cluster *Cluster) SetFailSync(check bool) {
	cluster.conf.FailSync = check
}

func (cluster *Cluster) SwitchFailSync() {
	cluster.conf.FailSync = !cluster.conf.FailSync
}

func (cluster *Cluster) GetFailSync() bool {
	return cluster.conf.FailSync
}

func (cluster *Cluster) SetSwitchSync(check bool) {
	cluster.conf.SwitchSync = check
}

func (cluster *Cluster) SwitchSwitchoverSync() {
	cluster.conf.SwitchSync = !cluster.conf.SwitchSync
}

func (cluster *Cluster) GetSwitchSync() bool {
	return cluster.conf.SwitchSync
}

func (cluster *Cluster) SetLogLevel(level int) {
	cluster.conf.LogLevel = level
}
func (cluster *Cluster) GetLogLevel() int {
	return cluster.conf.LogLevel
}
func (cluster *Cluster) SwitchVerbosity() {
	if cluster.GetLogLevel() > 0 {
		cluster.SetLogLevel(0)
	} else {
		cluster.SetLogLevel(4)
	}
}

func (cluster *Cluster) SetRejoin(check bool) {
	cluster.conf.Autorejoin = check
}
func (cluster *Cluster) SwitchRejoin() {
	cluster.conf.Autorejoin = !cluster.conf.Autorejoin
}

func (cluster *Cluster) GetRejoin() bool {
	return cluster.conf.Autorejoin
}

func (cluster *Cluster) SetRejoinDump(check bool) {
	cluster.conf.AutorejoinMysqldump = check
}
func (cluster *Cluster) SwitchRejoinDump() {
	cluster.conf.AutorejoinMysqldump = !cluster.conf.AutorejoinMysqldump
}
func (cluster *Cluster) GetRejoinDump() bool {
	return cluster.conf.AutorejoinMysqldump
}

func (cluster *Cluster) SetRejoinBackupBinlog(check bool) {
	cluster.conf.AutorejoinBackupBinlog = check
}
func (cluster *Cluster) SwitchRejoinBackupBinlog() {
	cluster.conf.AutorejoinBackupBinlog = !cluster.conf.AutorejoinBackupBinlog
}
func (cluster *Cluster) GetRejoinBackupBinlog() bool {
	return cluster.conf.AutorejoinBackupBinlog
}

func (cluster *Cluster) SetRejoinSemisync(check bool) {
	cluster.conf.AutorejoinSemisync = check
}
func (cluster *Cluster) SwitchRejoinSemisync() {
	cluster.conf.AutorejoinSemisync = !cluster.conf.AutorejoinSemisync
}
func (cluster *Cluster) GetRejoinSemisync() bool {
	return cluster.conf.AutorejoinSemisync
}

func (cluster *Cluster) SetRejoinFlashback(check bool) {
	cluster.conf.AutorejoinFlashback = check
}
func (cluster *Cluster) SwitchRejoinFlashback() {
	cluster.conf.AutorejoinFlashback = !cluster.conf.AutorejoinFlashback
}

// topology setter
func (cluster *Cluster) SetMultiTierSlave(multitierslave bool) {
	cluster.conf.MultiTierSlave = multitierslave
}

func (cluster *Cluster) SetForceSlaveNoGtid(forceslavenogtid bool) {
	cluster.conf.ForceSlaveNoGtid = forceslavenogtid
}

func (cluster *Cluster) SetMultiMaster(multimaster bool) {
	cluster.conf.MultiMaster = multimaster
}
func (cluster *Cluster) SetBinlogServer(binlogserver bool) {
	cluster.conf.MxsBinlogOn = binlogserver
}

func (cluster *Cluster) SetMasterReadOnly() {
	if cluster.GetMaster() != nil {
		err := dbhelper.SetReadOnly(cluster.master.Conn, true)
		if err != nil {
			cluster.LogPrintf("ERROR", "Could not set  master as read-only, %s", err)
		}
	}
}

func (cluster *Cluster) GetRejoinFlashback() bool {
	return cluster.conf.AutorejoinFlashback
}

func (cluster *Cluster) GetName() string {
	return cluster.cfgGroup
}

func (cluster *Cluster) SetTestMode(check bool) {
	cluster.conf.Test = check
}

func (cluster *Cluster) GetTestMode() bool {
	return cluster.conf.Test
}

func (cluster *Cluster) SetTestStopCluster(check bool) {
	cluster.testStopCluster = check
}

func (cluster *Cluster) SetActiveStatus(status string) {
	cluster.runStatus = status
}
func (cluster *Cluster) SetTestStartCluster(check bool) {
	cluster.testStartCluster = check
}

func (cluster *Cluster) GetDbUser() string {
	return cluster.dbUser
}

func (cluster *Cluster) GetDbPass() string {
	return cluster.dbPass
}

func (cluster *Cluster) Close() {

	for _, server := range cluster.servers {
		defer server.Conn.Close()
	}
}

func (cluster *Cluster) SetLogStdout() {
	cluster.conf.Daemon = true
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
