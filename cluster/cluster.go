package cluster

import (
	"errors"
	"fmt"
	"github.com/tanji/replication-manager/config"
	"github.com/tanji/replication-manager/crypto"
	"github.com/tanji/replication-manager/dbhelper"
	"github.com/tanji/replication-manager/misc"
	"github.com/tanji/replication-manager/state"
	"github.com/tanji/replication-manager/termlog"
	"log"
	"os"
	"strings"
	"sync"
	"time"
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
	repmgrVersion        string
	repmgrHostname       string
	key                  []byte
	exitMsg              string
	exit                 bool
	cleanall             bool
}

var swChan = make(chan bool)

func (cluster *Cluster) Init(conf config.Config, cfgGroup string, tlog *termlog.TermLog, termlength int, runUUID string, repmgrVersion string, repmgrHostname string, key []byte) error {
	// Initialize the state machine at this stage where everything is fine.
	cluster.runOnceAfterTopology = true
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
	return nil
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
						cluster.runOnceAfterTopology = false
					}
				}

				if cluster.conf.LogLevel > 2 {
					cluster.LogPrint("DEBUG: Monitoring server loop")
					for k, v := range cluster.servers {
						cluster.LogPrintf("DEBUG: Server [%d]: URL: %-15s State: %6s PrevState: %6s", k, v.URL, v.State, v.PrevState)
					}
					cluster.LogPrintf("DEBUG: Master [ ]: URL: %-15s State: %6s PrevState: %6s", cluster.master.URL, cluster.master.State, cluster.master.PrevState)
					for k, v := range cluster.slaves {
						cluster.LogPrintf("DEBUG: Slave  [%d]: URL: %-15s State: %6s PrevState: %6s", k, v.URL, v.State, v.PrevState)
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
				case sig := <-swChan:
					if sig {
						cluster.MasterFailover(false)
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
	swChan <- true
}
func (cluster *Cluster) checkfailed() {
	// Don't trigger a failover if a switchover is happening
	if cluster.sme.IsInFailover() {
		cluster.LogPrintf("DEBUG: In Failover skip checking failed master")
		return
	}
	//  LogPrintf("WARN : Constraint is blocking master state %s stateFailed %s conf.Interactive %b cluster.master.FailCount %d >= maxfail %d" ,cluster.master.State,stateFailed,interactive, master.FailCount , maxfail )
	if cluster.master != nil {
		if cluster.master.State == stateFailed && cluster.conf.Interactive == false && cluster.master.FailCount >= cluster.conf.MaxFail {
			rem := (cluster.failoverTs + cluster.conf.FailTime) - time.Now().Unix()
			if (cluster.conf.FailTime == 0) || (cluster.conf.FailTime > 0 && (rem <= 0 || cluster.failoverCtr == 0)) {
				if cluster.failoverCtr == cluster.conf.FailLimit {
					cluster.sme.AddState("INF00002", state.State{ErrType: "INFO", ErrDesc: "Failover limit reached. Switching to manual mode", ErrFrom: "MON"})
					cluster.conf.Interactive = true
				}
				cluster.MasterFailover(true)
			} else if cluster.conf.FailTime > 0 && rem%10 == 0 {
				cluster.LogPrintf("WARN : Failover time limit enforced. Next failover available in %d seconds", rem)
			} else {
				cluster.LogPrintf("WARN : Constraint is blocking for failover")
			}

		} else if cluster.master.State == stateFailed && cluster.master.FailCount < cluster.conf.MaxFail {
			cluster.LogPrintf("WARN : Waiting more prove of master death")

		}
	} else {
		cluster.LogPrintf("WARN : Unknown master when checking failover")
	}
}

// Check that mandatory flags have correct values. This is not part of the state machine and mandatory flags
// must lead to Fatal errors if initialized with wrong values.

func (cluster *Cluster) repmgrFlagCheck() error {
	if cluster.conf.LogFile != "" {
		var err error
		cluster.logPtr, err = os.OpenFile(cluster.conf.LogFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
		if err != nil {
			log.Println("ERROR: Error opening logfile, disabling for the rest of the session.")
			cluster.conf.LogFile = ""
		}
	}
	// if slaves option has been supplied, split into a slice.
	if cluster.conf.Hosts != "" {
		cluster.hostList = strings.Split(cluster.conf.Hosts, ",")
	} else {
		log.Println("ERROR: No hosts list specified.")
		return errors.New("ERROR: No hosts list specified.")
	}
	// validate users
	if cluster.conf.User == "" {
		log.Println("ERROR: No master user/pair specified.")
		return errors.New("ERROR: No master user/pair specified.")
	}
	cluster.dbUser, cluster.dbPass = misc.SplitPair(cluster.conf.User)

	if cluster.conf.RplUser == "" {
		log.Println("ERROR: No replication user/pair specified.")
		return errors.New("ERROR: No replication user/pair specified.")
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
		log.Println("ERROR: prefmaster option takes exactly one argument")
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
		log.Println("ERROR: Preferred master is not included in the hosts option")
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
	cluster.cleanall = check
}

func (cluster *Cluster) GetRplChecks() bool {
	return cluster.conf.RplChecks
}

func (cluster *Cluster) SetFailSync(check bool) {
	cluster.conf.RplChecks = check
}

func (cluster *Cluster) GetFailSync() bool {
	return cluster.conf.RplChecks
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

func (cluster *Cluster) Bootstrap() error {
	cluster.sme.SetFailoverState()
	if cluster.cleanall {
		log.Println("INFO : Cleaning up replication on existing servers")
		for _, server := range cluster.servers {
			if cluster.conf.Verbose {
				log.Printf("INFO : SetDefaultMasterConn on server %s ", server.URL)
			}
			err := dbhelper.SetDefaultMasterConn(server.Conn, cluster.conf.MasterConn)
			if err != nil {
				if cluster.conf.Verbose {
					log.Printf("INFO : RemoveFailoverState on server %s ", server.URL)
				}
				cluster.sme.RemoveFailoverState()
				return err
			}
			if cluster.conf.Verbose {
				log.Printf("INFO : ResetMaster on server %s ", server.URL)
			}
			err = dbhelper.ResetMaster(server.Conn)
			if err != nil {
				cluster.sme.RemoveFailoverState()
				return err
			}
			err = dbhelper.StopAllSlaves(server.Conn)
			if err != nil {
				cluster.sme.RemoveFailoverState()
				return err
			}
			err = dbhelper.ResetAllSlaves(server.Conn)
			if err != nil {
				cluster.sme.RemoveFailoverState()
				return err
			}
			_, err = server.Conn.Exec("SET GLOBAL gtid_slave_pos=''")
			if err != nil {
				cluster.sme.RemoveFailoverState()
				return err
			}
		}
	} else {
		err := cluster.TopologyDiscover()
		if err == nil {
			cluster.sme.RemoveFailoverState()
			return errors.New("ERROR: Environment already has an existing master/slave setup")
		}
	}
	masterKey := 0
	if cluster.conf.PrefMaster != "" {
		masterKey = func() int {
			for k, server := range cluster.servers {
				if server.URL == cluster.conf.PrefMaster {
					cluster.sme.RemoveFailoverState()
					return k
				}
			}
			cluster.sme.RemoveFailoverState()
			return -1
		}()
	}
	if masterKey == -1 {
		return errors.New("ERROR: Preferred master could not be found in existing servers")
	}
	_, err := cluster.servers[masterKey].Conn.Exec("RESET MASTER")
	if err != nil {
		cluster.LogPrint("WARN : RESET MASTER failed on master")
	}
	for key, server := range cluster.servers {
		if key == masterKey {
			dbhelper.FlushTables(server.Conn)
			dbhelper.SetReadOnly(server.Conn, false)
			continue
		} else {
			stmt := fmt.Sprintf("CHANGE MASTER '%s' TO master_host='%s', master_port=%s, master_user='%s', master_password='%s', master_use_gtid=current_pos, master_connect_retry=%d", cluster.conf.MasterConn, cluster.servers[masterKey].IP, cluster.servers[masterKey].Port, cluster.rplUser, cluster.rplPass, cluster.conf.MasterConnectRetry)
			_, err := server.Conn.Exec(stmt)
			if err != nil {
				cluster.sme.RemoveFailoverState()
				return errors.New(fmt.Sprintln("ERROR:", stmt, err))
			}
			_, err = server.Conn.Exec("START SLAVE '" + cluster.conf.MasterConn + "'")
			if err != nil {
				cluster.sme.RemoveFailoverState()
				return errors.New(fmt.Sprintln("ERROR: Start slave: ", err))
			}
			dbhelper.SetReadOnly(server.Conn, true)
		}
	}
	cluster.LogPrintf("INFO : Environment bootstrapped with %s as master", cluster.servers[masterKey].URL)
	cluster.sme.RemoveFailoverState()
	return nil
}
