package cluster

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/jmoiron/sqlx"

	"github.com/tanji/replication-manager/cluster/nbc"
	"github.com/tanji/replication-manager/config"
	"github.com/tanji/replication-manager/crypto"
	"github.com/tanji/replication-manager/dbhelper"
	"github.com/tanji/replication-manager/maxscale"
	"github.com/tanji/replication-manager/misc"
	"github.com/tanji/replication-manager/state"
	"github.com/tanji/replication-manager/termlog"
)

type Cluster struct {
	hostList             []string
	servers              serverList
	slaves               serverList
	master               *ServerMonitor
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
	mxs                  *maxscale.MaxScale
}

//var switchoverChan = make(chan bool)

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
	cluster.sme.Init()
	err := cluster.repmgrFlagCheck()
	if err != nil {
		return err
	}

	cluster.newServerList()
	if cluster.conf.Interactive {
		cluster.LogPrintf("INFO : Monitor started in manual mode")
	} else {
		cluster.LogPrintf("INFO : Monitor started in automatic mode")
	}
	return nil
}

func (cluster *Cluster) InitAgent(conf config.Config) (*ServerMonitor, error) {
	cluster.agentFlagCheck()
	if conf.LogFile != "" {
		var err error
		cluster.logPtr, err = os.Create(conf.LogFile)
		if err != nil {
			cluster.LogPrint("ERROR: Error opening logfile, disabling for the rest of the session.")
			conf.LogFile = ""
		}
	}
	db, err := cluster.newServerMonitor(conf.Hosts)
	if err != nil {
		log.WithError(err).Error("Error opening database connection")
		return nil, err
	}

	return db, nil
}

func (cluster *Cluster) SetCfgGroupDisplay(cfgGroup string) {
	cluster.cfgGroupDisplay = cfgGroup
}

func (cluster *Cluster) FailoverForce() error {
	sf := stateFile{Name: "/tmp/mrm" + cluster.cfgGroup + ".state"}
	err := sf.access()
	if err != nil {
		cluster.LogPrint("WARN : Could not create state file")
	}
	err = sf.read()
	if err != nil {
		cluster.LogPrint("WARN : Could not read values from state file:", err)
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
		for _, s := range cluster.sme.GetState() {
			cluster.LogPrint(s)
		}
		// Test for ERR00012 - No master detected
		if cluster.sme.CurState.Search("ERR00012") {
			for _, s := range cluster.servers {
				if s.State == "" {
					s.State = stateFailed
					if cluster.conf.LogLevel > 2 {
						cluster.LogPrint("DEBUG: State failed set by state detection ERR00012")
					}
					cluster.master = s
				}
			}
		} else {
			return err

		}
	}
	if cluster.master == nil {
		cluster.LogPrint("ERROR: Could not find a failed server in the hosts list")
		return errors.New("ERROR: Could not find a failed server in the hosts list")
	}
	if cluster.conf.FailLimit > 0 && cluster.failoverCtr >= cluster.conf.FailLimit {
		cluster.LogPrintf("ERROR: Failover has exceeded its configured limit of %d. Remove /tmp/mrm.state file to reinitialize the failover counter", cluster.conf.FailLimit)
		return errors.New("ERROR: Failover has exceeded its configured limit")
	}
	rem := (cluster.failoverTs + cluster.conf.FailTime) - time.Now().Unix()
	if cluster.conf.FailTime > 0 && rem > 0 {
		cluster.LogPrintf("ERROR: Failover time limit enforced. Next failover available in %d seconds", rem)
		return errors.New("ERROR: Failover time limit enforced")
	}
	if cluster.MasterFailover(true) {
		sf.Count++
		sf.Timestamp = cluster.failoverTs
		err := sf.write()
		if err != nil {
			cluster.LogPrintf("WARN : Could not write values to state file:%s", err)
		}
	}
	return nil
}

func (cluster *Cluster) Stop() {
	cluster.exit = true
}
func (cluster *Cluster) Run() {

	/*	cluster.mxs = maxscale.MaxScale{Host: cluster.conf.MxsHost, Port: cluster.conf.MxsPort, User: cluster.conf.MxsUser, Pass: cluster.conf.MxsPass}
		if cluster.conf.MxsOn {
			err := cluster.mxs.Connect()
			if err != nil {
				cluster.LogPrint("ERROR: Could not connect to MaxScale:", err)
			}
		}*/
	interval := time.Second
	ticker := time.NewTicker(interval * time.Duration(cluster.conf.MonitoringTicker))
	for cluster.exit == false {

		select {
		case <-ticker.C:
			if cluster.sme.IsDiscovered() == false {
				if cluster.conf.LogLevel > 2 {
					cluster.LogPrint("DEBUG: Discovering topology loop")
				}
				cluster.pingServerList()
				cluster.TopologyDiscover()
				states := cluster.sme.GetState()
				for i := range states {
					cluster.LogPrint(states[i])
				}
			}
			cluster.display()
			if cluster.sme.CanMonitor() {
				/* run once */
				if cluster.runOnceAfterTopology {
					if cluster.master != nil {
						if cluster.conf.HaproxyOn {
							cluster.initHaproxy()
						}
						if cluster.conf.MxsOn {
							cluster.initMaxscale(nil)
						}
						cluster.runOnceAfterTopology = false
					}
				}

				if cluster.conf.LogLevel > 2 {
					cluster.LogPrint("DEBUG: Monitoring server loop")
					for k, v := range cluster.servers {
						cluster.LogPrintf("DEBUG: Server [%d]: URL: %-15s State: %6s PrevState: %6s", k, v.URL, v.State, v.PrevState)
					}
					if cluster.master != nil {
						cluster.LogPrintf("DEBUG: Master [ ]: URL: %-15s State: %6s PrevState: %6s", cluster.master.URL, cluster.master.State, cluster.master.PrevState)
						for k, v := range cluster.slaves {
							cluster.LogPrintf("DEBUG: Slave  [%d]: URL: %-15s State: %6s PrevState: %6s", k, v.URL, v.State, v.PrevState)
						}
					}
				}
				wg := new(sync.WaitGroup)
				for _, server := range cluster.servers {
					wg.Add(1)
					go server.check(wg)
				}
				wg.Wait()
				cluster.pingServerList()
				cluster.TopologyDiscover()
				states := cluster.sme.GetState()
				for i := range states {
					cluster.LogPrint(states[i])
				}
				cluster.checkfailed()
				select {
				case sig := <-cluster.switchoverChan:
					if sig {
						cluster.MasterFailover(false)
						cluster.switchoverCond.Send <- true
					}

				default:
					//do nothing
				}
			}
			if !cluster.sme.IsInFailover() {
				cluster.sme.ClearState()
			}
		}
	}
}

func (cluster *Cluster) SwitchOver() {
	cluster.switchoverChan <- true
}

// Check that mandatory flags have correct values. This is not part of the state machine and mandatory flags
// must lead to Fatal errors if initialized with wrong values.

func (cluster *Cluster) repmgrFlagCheck() error {
	if cluster.conf.LogFile != "" {
		var err error
		cluster.logPtr, err = os.OpenFile(cluster.conf.LogFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
		if err != nil {
			cluster.LogPrint("ERROR: Error opening logfile, disabling for the rest of the session")
			cluster.conf.LogFile = ""
		}
	}
	// if slaves option has been supplied, split into a slice.
	if cluster.conf.Hosts != "" {
		cluster.hostList = strings.Split(cluster.conf.Hosts, ",")
	} else {
		cluster.LogPrint("ERROR: No hosts list specified")
		return errors.New("ERROR: No hosts list specified")
	}
	// validate users
	if cluster.conf.User == "" {
		cluster.LogPrint("ERROR: No master user/pair specified")
		return errors.New("ERROR: No master user/pair specified")
	}
	cluster.dbUser, cluster.dbPass = misc.SplitPair(cluster.conf.User)

	if cluster.conf.RplUser == "" {
		cluster.LogPrint("ERROR: No replication user/pair specified")
		return errors.New("ERROR: No replication user/pair specified")
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
		cluster.LogPrint("ERROR: prefmaster option takes exactly one argument")
		return errors.New("ERROR: prefmaster option takes exactly one argument")
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
		cluster.LogPrint("ERROR: Preferred master is not included in the hosts option")
		return errors.New("ERROR: prefmaster option takes exactly one argument")
	}
	return nil
}

func (cluster *Cluster) ToggleInteractive() {
	if cluster.conf.Interactive == true {
		cluster.conf.Interactive = false
		cluster.LogPrintf("INFO : Failover monitor switched to automatic mode")
	} else {
		cluster.conf.Interactive = true
		cluster.LogPrintf("INFO : Failover monitor switched to manual mode")
	}
}

func (cluster *Cluster) SetInteractive(check bool) {
	cluster.conf.Interactive = check
}

func (cluster *Cluster) GetActiveStatus() {
	for _, sv := range cluster.servers {
		err := dbhelper.SetStatusActiveHeartbeat(sv.Conn, cluster.runUUID, "A")
		if err == nil {
			cluster.runStatus = "A"
		}
	}
}

func (cluster *Cluster) ResetFailoverCtr() {
	cluster.failoverCtr = 0
	cluster.failoverTs = 0
}

func (cluster *Cluster) GetServers() serverList {
	return cluster.servers
}

func (cluster *Cluster) GetMaster() *ServerMonitor {
	return cluster.master
}

func (cluster *Cluster) GetConf() config.Config {
	return cluster.conf
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

func (cluster *Cluster) SetSlavesReadOnly(check bool) {
	for _, sl := range cluster.slaves {
		dbhelper.SetReadOnly(sl.Conn, check)
	}
}

func (cluster *Cluster) SetRplChecks(check bool) {
	cluster.conf.RplChecks = check
}

func (cluster *Cluster) SetCleanAll(check bool) {
	cluster.CleanAll = check
}

func (cluster *Cluster) GetRplChecks() bool {
	return cluster.conf.RplChecks
}

func (cluster *Cluster) SetFailSync(check bool) {
	cluster.conf.FailSync = check
}

func (cluster *Cluster) GetFailSync() bool {
	return cluster.conf.FailSync
}

func (cluster *Cluster) SetSwitchSync(check bool) {
	cluster.conf.SwitchSync = check
}

func (cluster *Cluster) GetSwitchSync() bool {
	return cluster.conf.SwitchSync
}

func (cluster *Cluster) SetRejoin(check bool) {
	cluster.conf.Autorejoin = check
}

func (cluster *Cluster) GetRejoin() bool {
	return cluster.conf.Autorejoin
}

func (cluster *Cluster) SetRejoinDump(check bool) {
	cluster.conf.AutorejoinMysqldump = check
}

func (cluster *Cluster) GetRejoinDump() bool {
	return cluster.conf.AutorejoinMysqldump
}

func (cluster *Cluster) SetRejoinBackupBinlog(check bool) {
	cluster.conf.AutorejoinBackupBinlog = check
}

func (cluster *Cluster) GetRejoinBackupBinlog() bool {
	return cluster.conf.AutorejoinBackupBinlog
}

func (cluster *Cluster) SetRejoinSemisync(check bool) {
	cluster.conf.AutorejoinSemisync = check
}

func (cluster *Cluster) GetRejoinSemisync() bool {
	return cluster.conf.AutorejoinSemisync
}

func (cluster *Cluster) SetRejoinFlashback(check bool) {
	cluster.conf.AutorejoinFlashback = check
}

func (cluster *Cluster) GetRejoinFlashback() bool {
	return cluster.conf.AutorejoinFlashback
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

func (cluster *Cluster) getClusterProxyConn() (*sqlx.DB, error) {
	var proxyHost string
	var proxyPort string
	proxyHost = ""
	if cluster.conf.MxsOn {
		proxyHost = cluster.conf.MxsHost
		proxyPort = strconv.Itoa(cluster.conf.MxsWritePort)

	}
	if cluster.conf.HaproxyOn {
		proxyHost = "127.0.0.1"
		proxyPort = strconv.Itoa(cluster.conf.HaproxyWritePort)
	}

	_, err := dbhelper.CheckHostAddr(proxyHost)
	if err != nil {
		errmsg := fmt.Errorf("ERROR: DNS resolution error for host %s", proxyHost)
		return nil, errmsg
	}

	params := fmt.Sprintf("?timeout=%ds", cluster.conf.Timeout)

	dsn := cluster.dbUser + ":" + cluster.dbPass + "@"
	if proxyHost != "" {
		dsn += "tcp(" + proxyHost + ":" + proxyPort + ")/" + params
	}
	cluster.LogPrint(dsn)
	return sqlx.Open("mysql", dsn)

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
