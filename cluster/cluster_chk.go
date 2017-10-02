// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/crypto"
	"github.com/signal18/replication-manager/dbhelper"
	"github.com/signal18/replication-manager/maxscale"
	"github.com/signal18/replication-manager/misc"
	"github.com/signal18/replication-manager/state"
)

func (cluster *Cluster) CheckFailed() {
	// Don't trigger a failover if a switchover is happening
	if cluster.sme.IsInFailover() {
		cluster.sme.AddState("ERR00001", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00001"]), ErrFrom: "CHECK"})
		return
	}
	if cluster.master != nil {
		if cluster.isFoundCandidateMaster() {
			if cluster.isBetweenFailoverTimeValid() {
				if cluster.isMaxMasterFailedCountReached() {
					if cluster.isActiveArbitration() {
						if cluster.isMaxClusterFailoverCountNotReached() {
							if cluster.isAutomaticFailover() {
								if cluster.isMasterFailed() {
									if cluster.isNotFirstSlave() {
										// False Positive
										if cluster.isExternalOk() == false {
											if cluster.isOneSlaveHeartbeatIncreasing() == false {
												if cluster.isMaxscaleSupectRunning() == false {
													cluster.MasterFailover(true)
													cluster.failoverCond.Send <- true
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	} else {
		if cluster.conf.LogLevel > 1 {
			cluster.LogPrintf("WARN", "Undiscovered master, skipping failover check")
		}
	}
}

func (cluster *Cluster) isAutomaticFailover() bool {
	if cluster.conf.Interactive == false {
		return true
	}
	cluster.sme.AddState("ERR00002", state.State{ErrType: "ERR00002", ErrDesc: fmt.Sprintf(clusterError["ERR00002"]), ErrFrom: "CHECK"})
	return false
}

func (cluster *Cluster) isMasterFailed() bool {
	if cluster.master.State == stateFailed {
		return true
	}
	return false
}

// isMaxMasterFailedCountReach test tentative to connect
func (cluster *Cluster) isMaxMasterFailedCountReached() bool {
	// no illimited failed count

	if cluster.master.FailCount >= cluster.conf.MaxFail {
		cluster.sme.AddState("WARN0023", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0023"]), ErrFrom: "CHECK"})
		return true
	} else {
		cluster.sme.AddState("ERR00023", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf("Constraint is blocking state %s, interactive:%t, maxfail reached:%t", cluster.master.State, cluster.conf.Interactive, cluster.isMaxMasterFailedCountReached()), ErrFrom: "CONF"})
	}
	return false
}

func (cluster *Cluster) isMaxClusterFailoverCountNotReached() bool {
	// illimited failed count
	//cluster.LogPrintf("CHECK: Failover Counter Reach")
	if cluster.conf.FailLimit == 0 {
		return true
	}
	if cluster.failoverCtr == cluster.conf.FailLimit {
		cluster.sme.AddState("ERR00027", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00027"]), ErrFrom: "CHECK"})
		return false
	}
	return true
}

func (cluster *Cluster) isBetweenFailoverTimeValid() bool {
	// illimited failed count
	rem := (cluster.failoverTs + cluster.conf.FailTime) - time.Now().Unix()
	if cluster.conf.FailTime == 0 {
		return true
	}
	//	cluster.LogPrintf("CHECK: Failover Time to short with previous failover")
	if rem > 0 {
		cluster.sme.AddState("ERR00029", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00029"]), ErrFrom: "CHECK"})
		return false
	}
	return true
}

func (cluster *Cluster) isOneSlaveHeartbeatIncreasing() bool {
	if cluster.conf.CheckFalsePositiveHeartbeat == false {
		return false
	}
	//cluster.LogPrintf("CHECK: Failover Slaves heartbeats")

	for _, s := range cluster.slaves {
		relaycheck, _ := cluster.GetMasterFromReplication(s)
		if relaycheck != nil {
			if relaycheck.IsRelay == false {
				status, _ := dbhelper.GetStatusAsInt(s.Conn)
				saveheartbeats := status["SLAVE_RECEIVED_HEARTBEATS"]
				if cluster.conf.LogLevel > 1 {
					cluster.LogPrintf("DEBUG", "SLAVE_RECEIVED_HEARTBEATS %d", saveheartbeats)
				}
				time.Sleep(time.Duration(cluster.conf.CheckFalsePositiveHeartbeatTimeout) * time.Second)
				status2, _ := dbhelper.GetStatusAsInt(s.Conn)
				if cluster.conf.LogLevel > 1 {
					cluster.LogPrintf("DEBUG", "SLAVE_RECEIVED_HEARTBEATS %d", status2["SLAVE_RECEIVED_HEARTBEATS"])
				}
				if status2["SLAVE_RECEIVED_HEARTBEATS"] > saveheartbeats {
					cluster.sme.AddState("ERR00028", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00028"], s.DSN), ErrFrom: "CHECK"})
					return true
				}
			}
		}
	}
	return false
}

func (cluster *Cluster) isMaxscaleSupectRunning() bool {
	if cluster.conf.MxsOn == false {
		return false
	}
	if cluster.conf.CheckFalsePositiveMaxscale == false {
		return false
	}
	//cluster.LogPrintf("CHECK: Failover Maxscale Master Satus")
	m := maxscale.MaxScale{Host: cluster.conf.MxsHost, Port: cluster.conf.MxsPort, User: cluster.conf.MxsUser, Pass: cluster.conf.MxsPass}
	err := m.Connect()
	if err != nil {
		cluster.LogPrintf("ERROR", "Could not connect to MaxScale:", err)
		return false
	}
	defer m.Close()
	if cluster.master.MxsServerName == "" {
		cluster.LogPrintf("INFO", "MaxScale server name undiscovered")
		return false
	}
	//disable monitoring

	var monitor string
	if cluster.conf.MxsGetInfoMethod == "maxinfo" {
		if cluster.conf.LogLevel > 1 {
			cluster.LogPrintf("DEBUG", "Getting Maxscale monitor via maxinfo")
		}
		m.GetMaxInfoMonitors("http://" + cluster.conf.MxsHost + ":" + strconv.Itoa(cluster.conf.MxsMaxinfoPort) + "/monitors")
		monitor = m.GetMaxInfoStoppedMonitor()

	} else {
		if cluster.conf.LogLevel > 1 {
			cluster.LogPrintf("DEBUG", "Getting Maxscale monitor via maxadmin")
		}
		_, err = m.ListMonitors()
		if err != nil {
			cluster.LogPrintf("ERROR", "MaxScale client could not list monitors: %s", err)
			return false
		}
		monitor = m.GetStoppedMonitor()
	}
	if monitor != "" {
		cmd := "Restart monitor \"" + monitor + "\""
		cluster.LogPrintf("INFO : %s", cmd)
		err = m.RestartMonitor(monitor)
		if err != nil {
			cluster.LogPrintf("ERROR", "MaxScale client could not startup monitor: %s", err)
			return false
		}
	} else {
		cluster.LogPrintf("INFO", "MaxScale Monitor not found")
		return false
	}

	time.Sleep(time.Duration(cluster.conf.CheckFalsePositiveMaxscaleTimeout) * time.Second)
	if strings.Contains(cluster.master.MxsServerStatus, "Running") {
		cluster.sme.AddState("ERR00030", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00030"], cluster.master.MxsServerStatus), ErrFrom: "CHECK"})
		return true
	}
	return false
}

func (cluster *Cluster) isFoundCandidateMaster() bool {

	key := cluster.electCandidate(cluster.slaves, false)
	if key == -1 {
		cluster.sme.AddState("ERR00032", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00032"]), ErrFrom: "CHECK"})
		return false
	}
	return true
}

func (cluster *Cluster) isActiveArbitration() bool {

	if cluster.conf.Arbitration == false {
		return true
	}
	//	cluster.LogPrintf("CHECK: Failover External Arbitration")

	url := "http://" + cluster.conf.ArbitrationSasHosts + "/arbitrator"
	var mst string
	if cluster.master != nil {
		mst = cluster.master.URL
	}
	var jsonStr = []byte(`{"uuid":"` + cluster.runUUID + `","secret":"` + cluster.conf.ArbitrationSasSecret + `","cluster":"` + cluster.cfgGroup + `","master":"` + mst + `","id":` + strconv.Itoa(cluster.conf.ArbitrationSasUniqueId) + `}`)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	req.Header.Set("X-Custom-Header", "myvalue")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		cluster.LogPrintf("ERROR", "%s", err.Error())
		cluster.sme.AddState("ERR00022", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00022"]), ErrFrom: "CHECK"})
		return false
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)

	type response struct {
		Arbitration string `json:"arbitration"`
	}
	var r response
	err = json.Unmarshal(body, &r)
	if err != nil {
		cluster.LogPrintf("ERROR", "Arbitrator says invalid JSON")
		cluster.sme.AddState("ERR00022", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00022"]), ErrFrom: "CHECK"})
		return false
	}
	if r.Arbitration == "winner" {
		cluster.LogPrintf("INFO", "Arbitrator says: winner")
		return true
	}
	cluster.sme.AddState("ERR00022", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00022"]), ErrFrom: "CHECK"})
	return false
}

func (cluster *Cluster) isExternalOk() bool {
	if cluster.conf.CheckFalsePositiveExternal == false {
		return false
	}
	//cluster.LogPrintf("CHECK: Failover External Request")
	if cluster.master == nil {
		return false
	}
	url := "http://" + cluster.master.Host + ":" + strconv.Itoa(cluster.conf.CheckFalsePositiveExternalPort)
	req, err := http.Get(url)
	if err != nil {
		return false
	}
	if req.StatusCode == 200 {
		cluster.sme.AddState("ERR00031", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00031"]), ErrFrom: "CHECK"})
		return true
	}
	return false
}

func (cluster *Cluster) isNotFirstSlave() bool {
	// let the failover doable if interactive or failover on first slave
	if cluster.conf.Interactive == true || cluster.conf.FailRestartUnsafe == true {
		return true
	}
	// do not failover if master info is unknowned:
	// - first replication-manager start on no topology
	// - all cluster down
	if cluster.master == nil {
		cluster.sme.AddState("ERR00026", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00026"]), ErrFrom: "CHECK"})
		return false
	}

	return true
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
