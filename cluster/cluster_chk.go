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
		cluster.LogPrintf(LvlDbg, "Master not discovered, skipping failover check")
	}
}

func (cluster *Cluster) isSlaveElectableForSwitchover(sl *ServerMonitor, forcingLog bool) bool {
	ss, err := sl.GetSlaveStatus(sl.ReplicationSourceName)
	if err != nil {
		cluster.LogPrintf(LvlDbg, "Error in getting slave status in testing slave electable for switchover %s: %s  ", sl.URL, err)
		return false
	}
	hasBinLogs, err := cluster.IsEqualBinlogFilters(cluster.master, sl)
	if err != nil {
		if cluster.Conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf(LvlWarn, "Could not check binlog filters")
		}
		return false
	}
	if hasBinLogs == false && cluster.Conf.CheckBinFilter == true {
		if cluster.Conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf(LvlWarn, "Binlog filters differ on master and slave %s. Skipping", sl.URL)
		}
		return false
	}
	if cluster.IsEqualReplicationFilters(cluster.master, sl) == false && cluster.Conf.CheckReplFilter == true {
		if cluster.Conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf(LvlWarn, "Replication filters differ on master and slave %s. Skipping", sl.URL)
		}
		return false
	}
	if cluster.Conf.SwitchGtidCheck && cluster.IsCurrentGTIDSync(sl, cluster.master) == false && cluster.Conf.RplChecks == true {
		if cluster.Conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf(LvlWarn, "Equal-GTID option is enabled and GTID position on slave %s differs from master. Skipping", sl.URL)
		}
		return false
	}
	if sl.HaveSemiSync && sl.SemiSyncSlaveStatus == false && cluster.Conf.SwitchSync && cluster.Conf.RplChecks {
		if cluster.Conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf(LvlWarn, "Semi-sync slave %s is out of sync. Skipping", sl.URL)
		}
		return false
	}
	if ss.SecondsBehindMaster.Valid == false && cluster.Conf.RplChecks == true {
		if cluster.Conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf(LvlWarn, "Slave %s is stopped. Skipping", sl.URL)
		}
		return false
	}

	if sl.IsMaxscale || sl.IsRelay {
		if cluster.Conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf(LvlWarn, "Slave %s is a relay slave. Skipping", sl.URL)
		}
		return false
	}
	return true
}

func (cluster *Cluster) isAutomaticFailover() bool {
	if cluster.Conf.Interactive == false {
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

	if cluster.master.FailCount >= cluster.Conf.MaxFail {
		cluster.sme.AddState("WARN0023", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0023"]), ErrFrom: "CHECK"})
		return true
	} else {
		//	cluster.sme.AddState("ERR00023", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf("Constraint is blocking state %s, interactive:%t, maxfail reached:%d", cluster.master.State, cluster.Conf.Interactive, cluster.Conf.MaxFail), ErrFrom: "CONF"})
	}
	return false
}

func (cluster *Cluster) isMaxClusterFailoverCountNotReached() bool {
	// illimited failed count
	//cluster.LogPrintf("CHECK: Failover Counter Reach")
	if cluster.Conf.FailLimit == 0 {
		return true
	}
	if cluster.FailoverCtr == cluster.Conf.FailLimit {
		cluster.sme.AddState("ERR00027", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00027"]), ErrFrom: "CHECK"})
		return false
	}
	return true
}

func (cluster *Cluster) isBetweenFailoverTimeValid() bool {
	// illimited failed count
	rem := (cluster.FailoverTs + cluster.Conf.FailTime) - time.Now().Unix()
	if cluster.Conf.FailTime == 0 {
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
	if cluster.Conf.CheckFalsePositiveHeartbeat == false {
		return false
	}
	//cluster.LogPrintf("CHECK: Failover Slaves heartbeats")

	for _, s := range cluster.slaves {
		relaycheck, _ := cluster.GetMasterFromReplication(s)
		if relaycheck != nil {
			if relaycheck.IsRelay == false {
				status, _ := dbhelper.GetStatusAsInt(s.Conn)
				saveheartbeats := status["SLAVE_RECEIVED_HEARTBEATS"]
				if cluster.Conf.LogLevel > 1 {
					cluster.LogPrintf(LvlDbg, "SLAVE_RECEIVED_HEARTBEATS %d", saveheartbeats)
				}
				time.Sleep(time.Duration(cluster.Conf.CheckFalsePositiveHeartbeatTimeout) * time.Second)
				status2, _ := dbhelper.GetStatusAsInt(s.Conn)
				if cluster.Conf.LogLevel > 1 {
					cluster.LogPrintf(LvlDbg, "SLAVE_RECEIVED_HEARTBEATS %d", status2["SLAVE_RECEIVED_HEARTBEATS"])
				}
				if status2["SLAVE_RECEIVED_HEARTBEATS"] > saveheartbeats {
					cluster.sme.AddState("ERR00028", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00028"], s.URL), ErrFrom: "CHECK"})
					return true
				}
			}
		}
	}
	return false
}

func (cluster *Cluster) isMaxscaleSupectRunning() bool {
	if cluster.Conf.MxsOn == false {
		return false
	}
	if cluster.Conf.CheckFalsePositiveMaxscale == false {
		return false
	}
	//cluster.LogPrintf("CHECK: Failover Maxscale Master Satus")
	m := maxscale.MaxScale{Host: cluster.Conf.MxsHost, Port: cluster.Conf.MxsPort, User: cluster.Conf.MxsUser, Pass: cluster.Conf.MxsPass}
	err := m.Connect()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Could not connect to MaxScale:", err)
		return false
	}
	defer m.Close()
	if cluster.master.MxsServerName == "" {
		cluster.LogPrintf(LvlInfo, "MaxScale server name undiscovered")
		return false
	}
	//disable monitoring

	var monitor string
	if cluster.Conf.MxsGetInfoMethod == "maxinfo" {
		cluster.LogPrintf(LvlDbg, "Getting Maxscale monitor via maxinfo")
		m.GetMaxInfoMonitors("http://" + cluster.Conf.MxsHost + ":" + strconv.Itoa(cluster.Conf.MxsMaxinfoPort) + "/monitors")
		monitor = m.GetMaxInfoStoppedMonitor()

	} else {
		cluster.LogPrintf(LvlDbg, "Getting Maxscale monitor via maxadmin")
		_, err = m.ListMonitors()
		if err != nil {
			cluster.LogPrintf(LvlErr, "MaxScale client could not list monitors: %s", err)
			return false
		}
		monitor = m.GetStoppedMonitor()
	}
	if monitor != "" {
		cmd := "Restart monitor \"" + monitor + "\""
		cluster.LogPrintf("INFO : %s", cmd)
		err = m.RestartMonitor(monitor)
		if err != nil {
			cluster.LogPrintf(LvlErr, "MaxScale client could not startup monitor: %s", err)
			return false
		}
	} else {
		cluster.LogPrintf(LvlInfo, "MaxScale Monitor not found")
		return false
	}

	time.Sleep(time.Duration(cluster.Conf.CheckFalsePositiveMaxscaleTimeout) * time.Second)
	if strings.Contains(cluster.master.MxsServerStatus, "Running") {
		cluster.sme.AddState("ERR00030", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00030"], cluster.master.MxsServerStatus), ErrFrom: "CHECK"})
		return true
	}
	return false
}

func (cluster *Cluster) isFoundCandidateMaster() bool {

	key := cluster.electFailoverCandidate(cluster.slaves, false)
	if key == -1 {
		cluster.sme.AddState("ERR00032", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00032"]), ErrFrom: "CHECK"})
		return false
	}
	return true
}

func (cluster *Cluster) isActiveArbitration() bool {

	if cluster.Conf.Arbitration == false {
		return true
	}
	//	cluster.LogPrintf("CHECK: Failover External Arbitration")

	url := "http://" + cluster.Conf.ArbitrationSasHosts + "/arbitrator"
	var mst string
	if cluster.master != nil {
		mst = cluster.master.URL
	}
	var jsonStr = []byte(`{"uuid":"` + cluster.runUUID + `","secret":"` + cluster.Conf.ArbitrationSasSecret + `","cluster":"` + cluster.Name + `","master":"` + mst + `","id":` + strconv.Itoa(cluster.Conf.ArbitrationSasUniqueId) + `}`)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	req.Header.Set("X-Custom-Header", "myvalue")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		cluster.LogPrintf(LvlErr, "%s", err.Error())
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
		cluster.LogPrintf(LvlErr, "Arbitrator sent invalid JSON")
		cluster.sme.AddState("ERR00022", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00022"]), ErrFrom: "CHECK"})
		return false
	}
	if r.Arbitration == "winner" {
		cluster.LogPrintf(LvlInfo, "Arbitrator says: winner")
		return true
	}
	cluster.sme.AddState("ERR00022", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00022"]), ErrFrom: "CHECK"})
	return false
}

func (cluster *Cluster) isExternalOk() bool {
	if cluster.Conf.CheckFalsePositiveExternal == false {
		return false
	}
	//cluster.LogPrintf("CHECK: Failover External Request")
	if cluster.master == nil {
		return false
	}
	url := "http://" + cluster.master.Host + ":" + strconv.Itoa(cluster.Conf.CheckFalsePositiveExternalPort)
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
	if cluster.Conf.Interactive == true || cluster.Conf.FailRestartUnsafe == true {
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

func (cluster *Cluster) isValidConfig() error {
	if cluster.Conf.LogFile != "" {
		var err error
		cluster.logPtr, err = os.OpenFile(cluster.Conf.LogFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Failed opening logfile, disabling for the rest of the session")
			cluster.Conf.LogFile = ""
		}
	}
	// if slaves option has been supplied, split into a slice.
	if cluster.Conf.Hosts != "" {
		cluster.hostList = strings.Split(cluster.Conf.Hosts, ",")
	} else {
		cluster.LogPrintf(LvlErr, "No hosts list specified")
		return errors.New("No hosts list specified")
	}

	// validate users
	if cluster.Conf.User == "" {
		cluster.LogPrintf(LvlErr, "No master user/pair specified")
		return errors.New("No master user/pair specified")
	}
	cluster.dbUser, cluster.dbPass = misc.SplitPair(cluster.Conf.User)

	if cluster.Conf.RplUser == "" {
		cluster.LogPrintf(LvlErr, "No replication user/pair specified")
		return errors.New("No replication user/pair specified")
	}
	cluster.rplUser, cluster.rplPass = misc.SplitPair(cluster.Conf.RplUser)

	if cluster.key != nil {
		p := crypto.Password{Key: cluster.key}
		p.CipherText = cluster.dbPass
		p.Decrypt()
		cluster.dbPass = p.PlainText
		p.CipherText = cluster.rplPass
		p.Decrypt()
		cluster.rplPass = p.PlainText
	}

	// Check if ignored servers are included in Host List
	if cluster.Conf.IgnoreSrv != "" {
		ihosts := strings.Split(cluster.Conf.IgnoreSrv, ",")
		for _, host := range ihosts {
			if !strings.Contains(cluster.Conf.Hosts, host) {
				cluster.LogPrintf(LvlErr, clusterError["ERR00059"], host)
			}
		}
	}

	// Check if preferred master is included in Host List
	pfa := strings.Split(cluster.Conf.PrefMaster, ",")
	if len(pfa) > 1 {
		cluster.LogPrintf(LvlErr, "Prefmaster option takes exactly one argument")
		return errors.New("Prefmaster option takes exactly one argument")
	}
	ret := func() bool {
		for _, v := range cluster.hostList {
			if v == cluster.Conf.PrefMaster {
				return true
			}
		}
		return false
	}
	if ret() == false && cluster.Conf.PrefMaster != "" {
		cluster.LogPrintf(LvlErr, "Preferred master is not included in the hosts option")
		return errors.New("Prefmaster option takes exactly one argument")
	}
	return nil
}

func (cluster *Cluster) IsEqualBinlogFilters(m *ServerMonitor, s *ServerMonitor) (bool, error) {

	if m.MasterStatus.Binlog_Do_DB == s.MasterStatus.Binlog_Do_DB && m.MasterStatus.Binlog_Ignore_DB == s.MasterStatus.Binlog_Ignore_DB {
		return true, nil
	}
	return false, nil
}

func (cluster *Cluster) IsEqualReplicationFilters(m *ServerMonitor, s *ServerMonitor) bool {

	if m.Variables["REPLICATE_DO_TABLE"] == s.Variables["REPLICATE_DO_TABLE"] && m.Variables["REPLICATE_IGNORE_TABLE"] == s.Variables["REPLICATE_IGNORE_TABLE"] && m.Variables["REPLICATE_WILD_DO_TABLE"] == s.Variables["REPLICATE_WILD_DO_TABLE"] && m.Variables["REPLICATE_WILD_IGNORE_TABLE"] == s.Variables["REPLICATE_WILD_IGNORE_TABLE"] && m.Variables["REPLICATE_DO_DB"] == s.Variables["REPLICATE_DO_DB"] && m.Variables["REPLICATE_IGNORE_DB"] == s.Variables["REPLICATE_IGNORE_DB"] {
		return true
	} else {
		return false
	}
}

func (cluster *Cluster) IsCurrentGTIDSync(m *ServerMonitor, s *ServerMonitor) bool {

	sGtid := s.Variables["GTID_CURRENT_POS"]
	mGtid := m.Variables["GTID_CURRENT_POS"]
	if sGtid == mGtid {
		return true
	} else {
		return false
	}
}
