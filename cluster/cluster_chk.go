// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/maxscale"
	"github.com/signal18/replication-manager/utils/alert"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

func (cluster *Cluster) CheckFailed() {
	// Don't trigger a failover if a switchover is happening
	if cluster.StateMachine.IsInFailover() {
		cluster.SetState("ERR00001", state.State{ErrType: "WARNING", ErrDesc: clusterError["ERR00001"], ErrFrom: "CHECK"})
		return
	}
	if cluster.master == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Master not discovered, skipping failover check")
		return
	}
	cluster.SetFalsePositiveCheck("FoundCandidateMaster", cluster.isFoundCandidateMaster())
	cluster.SetFalsePositiveCheck("BetweenFailoverTimeValid", cluster.isBetweenFailoverTimeValid())
	cluster.SetFalsePositiveCheck("NotHavingMySQLErrantTransaction", cluster.IsNotHavingMySQLErrantTransaction())
	cluster.SetFalsePositiveCheck("SameWsrepUUID", cluster.IsSameWsrepUUID())
	cluster.SetFalsePositiveCheck("MaxMasterFailedCountReached", cluster.isMaxMasterFailedCountReached())
	cluster.SetFalsePositiveCheck("ActiveArbitration", cluster.isActiveArbitration())
	cluster.SetFalsePositiveCheck("MaxClusterFailoverCountNotReached", cluster.isMaxClusterFailoverCountNotReached())
	cluster.SetFalsePositiveCheck("AutomaticFailover", cluster.isAutomaticFailover())
	cluster.SetFalsePositiveCheck("MasterFailed", cluster.isMasterFailed())
	cluster.SetFalsePositiveCheck("NotFirstSlave", cluster.isNotFirstSlave())
	cluster.SetFalsePositiveCheck("ArbitratorAlive", cluster.isArbitratorAlive())
	cluster.SetFalsePositiveCheck("NotExternalOk", !cluster.isExternalOk())
	cluster.SetFalsePositiveCheck("NotOneSlaveHeartbeatIncreasing", !cluster.isOneSlaveHeartbeatIncreasing())
	cluster.SetFalsePositiveCheck("NotMaxscaleSupectRunning", !cluster.isMaxscaleSupectRunning())

	if cluster.FalsePositiveChecks["FoundCandidateMaster"] &&
		cluster.FalsePositiveChecks["BetweenFailoverTimeValid"] &&
		cluster.FalsePositiveChecks["NotHavingMySQLErrantTransaction"] &&
		cluster.FalsePositiveChecks["SameWsrepUUID"] &&
		cluster.FalsePositiveChecks["MaxMasterFailedCountReached"] &&
		cluster.FalsePositiveChecks["ActiveArbitration"] &&
		cluster.FalsePositiveChecks["MaxClusterFailoverCountNotReached"] &&
		cluster.FalsePositiveChecks["AutomaticFailover"] &&
		cluster.FalsePositiveChecks["MasterFailed"] &&
		cluster.FalsePositiveChecks["NotFirstSlave"] &&
		cluster.FalsePositiveChecks["ArbitratorAlive"] &&
		cluster.FalsePositiveChecks["NotExternalOk"] &&
		cluster.FalsePositiveChecks["NotOneSlaveHeartbeatIncreasing"] &&
		cluster.FalsePositiveChecks["NotMaxscaleSupectRunning"] {
		cluster.MasterFailover(true)
		cluster.failoverCond.Send <- true
	}
	if cluster.FalsePositiveChecks["MasterFailed"] && cluster.FalsePositiveChecks["AutomaticFailover"] {
		cluster.SetState("ERR00097", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00097"], cluster.FalsePositiveChecks), ErrFrom: "CHECK"})
	}
}

func (cluster *Cluster) isSlaveElectableForSwitchover(sl *ServerMonitor, forcingLog bool) bool {
	//Ignore if child cluster
	if sl.GetSourceClusterName() != cluster.Name {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModWriterElection, config.LvlWarn, "Slave %s is in child cluster. Skipping", sl.URL)
		return false
	}

	ss, err := sl.GetSlaveStatus(sl.ReplicationSourceName)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Error in getting slave status in testing slave electable for switchover %s: %s  ", sl.URL, err)
		return false
	}
	hasBinLogs, err := cluster.IsEqualBinlogFilters(cluster.master, sl)
	if err != nil {
		// if cluster.Conf.LogLevel > 1 || forcingLog {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModWriterElection, config.LvlWarn, "Could not check binlog filters on %s", sl.URL)
		// }
		return false
	}
	if (!cluster.Conf.SwitchLowerRelease) && (sl.DBVersion.Major < cluster.master.DBVersion.Major || (sl.DBVersion.Major == cluster.master.DBVersion.Major && sl.DBVersion.Minor < cluster.master.DBVersion.Minor)) {
		// if cluster.Conf.LogLevel > 1 || forcingLog {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModWriterElection, config.LvlWarn, "Could not elect a minor version as leader without enabing switchover-lower-release %s", sl.URL)
		// }
		return false
	}
	if hasBinLogs == false && cluster.Conf.CheckBinFilter == true {
		// if cluster.Conf.LogLevel > 1 || forcingLog {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModWriterElection, config.LvlWarn, "Binlog filters differ on master and slave %s. Skipping", sl.URL)
		// }
		return false
	}
	if cluster.IsEqualReplicationFilters(cluster.master, sl) == false && cluster.Conf.CheckReplFilter == true {
		// if cluster.Conf.LogLevel > 1 || forcingLog {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModWriterElection, config.LvlWarn, "Replication filters differ on master and slave %s. Skipping", sl.URL)
		// }
		return false
	}
	if cluster.Conf.SwitchGtidCheck && cluster.IsCurrentGTIDSync(sl, cluster.master) == false && cluster.Conf.RplChecks == true {
		// if cluster.Conf.LogLevel > 1 || forcingLog {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModWriterElection, config.LvlWarn, "Equal-GTID option is enabled and GTID position on slave %s differs from master. Skipping", sl.URL)
		// }
		return false
	}
	if sl.HaveSemiSync && sl.SemiSyncSlaveStatus == false && cluster.Conf.SwitchSync && cluster.Conf.RplChecks {
		// if cluster.Conf.LogLevel > 1 || forcingLog {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModWriterElection, config.LvlWarn, "Semi-sync slave %s is out of sync. Skipping", sl.URL)
		// }
		return false
	}

	if ss.SecondsBehindMaster.Valid == false && cluster.Conf.RplChecks == true {
		// if cluster.Conf.LogLevel > 1 || forcingLog {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModWriterElection, config.LvlWarn, "Slave %s is stopped. Skipping", sl.URL)
		// }
		return false
	}

	if sl.IsMaxscale || sl.IsRelay {
		// if cluster.Conf.LogLevel > 1 || forcingLog {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModWriterElection, config.LvlWarn, "Slave %s is a relay slave. Skipping", sl.URL)
		// }
		return false
	}

	if sl.HasErrantTransactions() {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModWriterElection, config.LvlWarn, "Slave %s has errent transactions. Skipping", sl.URL)
		return false
	}

	return true
}

func (cluster *Cluster) isAutomaticFailover() bool {
	if cluster.Conf.Interactive == false {
		return true
	}
	cluster.SetState("ERR00002", state.State{ErrType: "ERR00002", ErrDesc: clusterError["ERR00002"], ErrFrom: "CHECK"})
	return false
}

func (cluster *Cluster) isMasterFailed() bool {
	//if master not discover, we can considered it not failed
	//can cause infinity loops if set to true
	/*if cluster.master == nil {
		return false
	}*/
	if cluster.GetMaster().State == stateFailed {
		return true
	}
	return false
}

// isMaxMasterFailedCountReach test tentative to connect
func (cluster *Cluster) isMaxMasterFailedCountReached() bool {
	// no illimited failed count

	if cluster.GetMaster() != nil && cluster.GetMaster().FailCount >= cluster.Conf.MaxFail {
		cluster.SetState("WARN0023", state.State{ErrType: "WARNING", ErrDesc: clusterError["WARN0023"], ErrFrom: "CHECK"})
		return true
	} else {
		//	cluster.SetState("ERR00023", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf("Constraint is blocking state %s, interactive:%t, maxfail reached:%d", cluster.master.State, cluster.Conf.Interactive, cluster.Conf.MaxFail), ErrFrom: "CONF"})
	}
	return false
}

func (cluster *Cluster) isMaxClusterFailoverCountNotReached() bool {
	// illimited failed count
	//cluster.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModGeneral,"CHECK: Failover Counter Reach")
	if cluster.Conf.FailLimit == 0 {
		return true
	}
	if cluster.FailoverCtr == cluster.Conf.FailLimit {
		cluster.SetState("ERR00027", state.State{ErrType: config.LvlErr, ErrDesc: clusterError["ERR00027"], ErrFrom: "CHECK"})
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
	//	cluster.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModGeneral,"CHECK: Failover Time to short with previous failover")
	if rem > 0 {
		cluster.SetState("ERR00029", state.State{ErrType: config.LvlErr, ErrDesc: clusterError["ERR00029"], ErrFrom: "CHECK"})
		return false
	}
	return true
}

func (cluster *Cluster) isOneSlaveHeartbeatIncreasing() bool {
	if !cluster.Conf.CheckFalsePositiveHeartbeat || !cluster.isMasterFailed() {
		return false
	}

	timeout := time.Duration(cluster.Conf.CheckFalsePositiveHeartbeatTimeout) * time.Second
	// Total context timeout to cover all goroutines duration
	ctx, cancel := context.WithTimeout(context.Background(), timeout*3)
	defer cancel()

	var found int32
	var wg sync.WaitGroup

	for _, slave := range cluster.slaves {
		relaycheck, _ := cluster.GetMasterFromReplication(slave)
		// Skip if relaycheck is nil or it's a relay slave
		if relaycheck == nil || relaycheck.IsRelay {
			continue
		}

		wg.Add(1)
		go func(slave *ServerMonitor) {
			defer wg.Done()

			// Check if already found an increasing heartbeat
			if atomic.LoadInt32(&found) == 1 {
				return
			}

			// First heartbeat status fetch
			status1, logs, err := dbhelper.GetStatusAsInt(slave.Conn, slave.DBVersion)
			cluster.LogSQL(logs, err, slave.URL, "isOneSlaveHeartbeatIncreasing", config.LvlDbg, "Monitor")
			if err != nil || atomic.LoadInt32(&found) == 1 {
				return
			}

			hb1 := status1["SLAVE_RECEIVED_HEARTBEATS"]
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg,
				"SLAVE_RECEIVED_HEARTBEATS %d (first check)", hb1)

			// Wait for timeout or cancellation before second check
			select {
			case <-ctx.Done():
				return
			case <-time.After(timeout):
				// continue after timeout
			}

			if atomic.LoadInt32(&found) == 1 {
				return
			}

			// Second heartbeat status fetch
			status2, logs, err := dbhelper.GetStatusAsInt(slave.Conn, slave.DBVersion)
			cluster.LogSQL(logs, err, slave.URL, "isOneSlaveHeartbeatIncreasing", config.LvlDbg, "Monitor")
			if err != nil {
				return
			}

			hb2 := status2["SLAVE_RECEIVED_HEARTBEATS"]
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg,
				"SLAVE_RECEIVED_HEARTBEATS %d (second check)", hb2)

			// Check if heartbeat increased
			if hb2 > hb1 {
				cluster.SetState("ERR00028", state.State{
					ErrType: config.LvlErr,
					ErrDesc: fmt.Sprintf(clusterError["ERR00028"], slave.URL),
					ErrFrom: "CHECK",
				})
				if atomic.CompareAndSwapInt32(&found, 0, 1) {
					cancel() // cancel all other goroutines
				}
			}
		}(slave)
	}

	wg.Wait()
	return atomic.LoadInt32(&found) == 1
}

func (cluster *Cluster) isMaxscaleSupectRunning() bool {
	if cluster.Conf.MxsOn == false {
		return false
	}
	if cluster.Conf.CheckFalsePositiveMaxscale == false {
		return false
	}
	//cluster.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModGeneral,"CHECK: Failover Maxscale Master Satus")
	m := maxscale.MaxScale{Host: cluster.Conf.MxsHost, Port: cluster.Conf.MxsPort, User: cluster.Conf.MxsUser, Pass: cluster.Conf.MxsPass}
	err := m.Connect()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not connect to MaxScale:", err)
		return false
	}
	defer m.Close()
	if cluster.master.MxsServerName == "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "MaxScale server name undiscovered")
		return false
	}
	//disable monitoring

	var monitor string
	if cluster.Conf.MxsGetInfoMethod == "maxinfo" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Getting Maxscale monitor via maxinfo")
		m.GetMaxInfoMonitors("http://" + cluster.Conf.MxsHost + ":" + strconv.Itoa(cluster.Conf.MxsMaxinfoPort) + "/monitors")
		monitor = m.GetMaxInfoStoppedMonitor()

	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Getting Maxscale monitor via maxadmin")
		_, err = m.ListMonitors()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "MaxScale client could not list monitors: %s", err)
			return false
		}
		monitor = m.GetStoppedMonitor()
	}
	if monitor != "" {
		cmd := "Restart monitor \"" + monitor + "\""
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO : %s", cmd)
		err = m.RestartMonitor(monitor)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "MaxScale client could not startup monitor: %s", err)
			return false
		}
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "MaxScale Monitor not found")
		return false
	}

	time.Sleep(time.Duration(cluster.Conf.CheckFalsePositiveMaxscaleTimeout) * time.Second)
	if strings.Contains(cluster.master.MxsServerStatus, "Running") {
		cluster.SetState("ERR00030", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00030"], cluster.master.MxsServerStatus), ErrFrom: "CHECK"})
		return true
	}
	return false
}

func (cluster *Cluster) isFoundCandidateMaster() bool {

	if cluster.GetTopology() == config.TopoActivePassive {
		return true
	}
	key := -1
	if cluster.Conf.MultiMasterGrouprep {
		key = cluster.electSwitchoverGroupReplicationCandidate(cluster.slaves, true)
	} else {
		key = cluster.electFailoverCandidate(cluster.slaves, false)
	}
	if key == -1 {
		// No candidates found in slaves list
		cluster.SetState("ERR00032", state.State{ErrType: config.LvlErr, ErrDesc: clusterError["ERR00032"], ErrFrom: "CHECK"})
		return false
	}
	return true
}

func (cluster *Cluster) isActiveArbitration() bool {

	if cluster.Conf.Arbitration == false {
		return true
	}
	//	cluster.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModGeneral,"CHECK: Failover External Arbitration")

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
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s", err.Error())
		cluster.SetState("ERR00022", state.State{ErrType: config.LvlErr, ErrDesc: clusterError["ERR00022"], ErrFrom: "CHECK"})
		return false
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	type response struct {
		Arbitration string `json:"arbitration"`
	}
	var r response
	err = json.Unmarshal(body, &r)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Arbitrator sent invalid JSON")
		cluster.SetState("ERR00022", state.State{ErrType: config.LvlErr, ErrDesc: clusterError["ERR00022"], ErrFrom: "CHECK"})
		return false
	}
	if r.Arbitration == "winner" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Arbitrator says: winner")
		return true
	}
	cluster.SetState("ERR00022", state.State{ErrType: config.LvlErr, ErrDesc: clusterError["ERR00022"], ErrFrom: "CHECK"})
	return false
}

func (cluster *Cluster) isExternalOk() bool {
	if cluster.Conf.CheckFalsePositiveExternal == false {
		return false
	}
	//cluster.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModGeneral,"CHECK: Failover External Request")
	if cluster.master == nil {
		return false
	}
	url := "http://" + cluster.master.Host + ":" + strconv.Itoa(cluster.Conf.CheckFalsePositiveExternalPort)
	req, err := http.Get(url)
	if err != nil {
		return false
	}
	if req.StatusCode == 200 {
		cluster.SetState("ERR00031", state.State{ErrType: config.LvlErr, ErrDesc: clusterError["ERR00031"], ErrFrom: "CHECK"})
		return true
	}
	return false
}

func (cluster *Cluster) isArbitratorAlive() bool {
	if !cluster.Conf.Arbitration {
		return true
	}
	if cluster.IsFailedArbitrator {
		cluster.SetState("ERR00055", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00055"], cluster.Conf.ArbitrationSasHosts), ErrFrom: "CHECK"})
		return false
	}
	return true
}

func (cluster *Cluster) isNotFirstSlave() bool {
	// let the failover doable if interactive or failover on first slave
	if cluster.Conf.Interactive == true || cluster.Conf.FailRestartUnsafe == true {
		return true
	}
	// do not failover if master info is unknowned:
	// - first replication-manager start on no topology
	// - all cluster down
	if cluster.GetMaster() == nil {
		cluster.SetState("ERR00026", state.State{ErrType: config.LvlErr, ErrDesc: clusterError["ERR00026"], ErrFrom: "CHECK"})
		return false
	}

	return true
}

// Check that mandatory flags have correct values. This is not part of the state machine and mandatory flags
// must lead to Fatal errors if initialized with wrong values.

func (cluster *Cluster) isValidConfig() error {
	if cluster.Conf.LogFile != "" {
		var err error

		//cluster.logPtr, err = os.OpenFile(cluster.Conf.LogFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
		//log.
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed opening logfile, disabling for the rest of the session")
			cluster.Conf.LogFile = ""
		}
	}

	// if slaves option has been supplied, split into a slice.
	if cluster.Conf.Hosts == "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No hosts list specified")
		return errors.New("No hosts list specified")
	}

	// validate users
	if cluster.Conf.User == "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No master user/pair specified")
		return errors.New("No master user/pair specified")
	}

	if cluster.Conf.RplUser == "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No replication user/pair specified")
		return errors.New("No replication user/pair specified")
	}

	// Check if ignored servers are included in Host List
	if cluster.Conf.IgnoreSrv != "" {
		ihosts := strings.Split(cluster.Conf.IgnoreSrv, ",")
		for _, host := range ihosts {
			if !strings.Contains(cluster.Conf.Hosts, host) {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, clusterError["ERR00059"], host)
			}
		}
	}

	// Check if preferred master is included in Host List
	pfa := strings.Split(cluster.Conf.PrefMaster, ",")

	for _, host := range pfa {
		if !strings.Contains(cluster.Conf.Hosts, host) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, clusterError["ERR00074"], host)
		}
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

	if m.Variables.Get("REPLICATE_DO_TABLE") == s.Variables.Get("REPLICATE_DO_TABLE") && m.Variables.Get("REPLICATE_IGNORE_TABLE") == s.Variables.Get("REPLICATE_IGNORE_TABLE") && m.Variables.Get("REPLICATE_WILD_DO_TABLE") == s.Variables.Get("REPLICATE_WILD_DO_TABLE") && m.Variables.Get("REPLICATE_WILD_IGNORE_TABLE") == s.Variables.Get("REPLICATE_WILD_IGNORE_TABLE") && m.Variables.Get("REPLICATE_DO_DB") == s.Variables.Get("REPLICATE_DO_DB") && m.Variables.Get("REPLICATE_IGNORE_DB") == s.Variables.Get("REPLICATE_IGNORE_DB") {
		return true
	} else {
		return false
	}
}

func (cluster *Cluster) IsCurrentGTIDSync(m *ServerMonitor, s *ServerMonitor) bool {

	sGtid := s.Variables.Get("GTID_CURRENT_POS")
	mGtid := m.Variables.Get("GTID_CURRENT_POS")
	if sGtid == mGtid {
		return true
	} else {
		return false
	}
}

func (cluster *Cluster) CheckCapture(st state.State) {
	if !cluster.Conf.MonitorCapture {
		return
	}

	if strings.Contains(cluster.Conf.MonitorCaptureTrigger, st.ErrKey) {

		var cstate *state.CapturedState

		// Check captured state
		SM := cluster.GetStateMachine()
		if cs, ok := SM.GetCapturedState(st.ErrKey); ok {
			cstate = cs
		} else {
			cstate = new(state.CapturedState)
			cstate.Parse(st)
			SM.AddToCapturedState(st.ErrKey, cstate)
		}

		//Capture if the server is not logged
		if st.ServerUrl != "" && cstate.Contains(st.ServerUrl) == false {
			srv := cluster.GetServerFromURL(st.ServerUrl)
			if srv != nil {
				//Add entry to captured state
				srv.Capture(cstate)
			}
		}
	}
}

func (cluster *Cluster) CheckAlert(state state.State, resolved bool) {
	if cluster.Conf.MonitoringAlertTrigger == "" {
		return
	}

	if strings.Contains(cluster.Conf.MonitoringAlertTrigger, state.ErrKey) {
		a := alert.Alert{
			Instance: cluster.GetInstanceAddress(),
			Cluster:  cluster.Name,
			State:    state.ErrDesc,
			Resolved: resolved,
		}

		err := cluster.SendAlert(a)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Could not send alert: %s ", err)
		}

	}
}

func (cluster *Cluster) SendAlert(alert alert.Alert) error {
	if cluster.IsAlertDisable {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cancel alert caused by alert disabled from scheduler")
		return nil
	}

	if cluster.Conf.MailTo != "" {
		if cluster.Mailer == nil {
			if err := cluster.InitMailer(); err != nil {
				return err
			}
		}

		go func() {
			err := alert.EmailMessage(cluster.GetAlertRecipients(AlertRecipient{To: cluster.Conf.MailTo, All: true}), cluster.Mailer)
			if err != nil {
				if cluster.failSendCount < 3 {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMailer, config.LvlErr, "Could not send mail alert: %s", err)
				}
				cluster.failSendCount++
			} else {
				cluster.failSendCount = 0
			}
		}()
	}

	if cluster.Conf.AlertScript != "" {
		cluster.BashScriptAlert(alert)
	}

	return nil
}

func (cluster *Cluster) CheckSendMail() {
	if cluster.failSendCount > 3 {
		cluster.SetState("WARN0138", state.State{ErrType: "WARN", ErrDesc: clusterError["WARN0138"], ErrFrom: "CHECK"})
	}
}

func (cluster *Cluster) CheckAllTableChecksum() {
	for _, t := range cluster.master.Tables {
		cluster.CheckTableChecksum(t.TableSchema, t.TableName)
	}
}

func (cluster *Cluster) CheckAllTableChecksumSchema(name string) {
	for _, t := range cluster.master.Tables {
		if t.TableSchema == name {
			cluster.CheckTableChecksum(t.TableSchema, t.TableName)
		}
	}
}

func (cluster *Cluster) CheckTableChecksum(schema string, table string) {

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Checksum master table %s.%s %s", schema, table, cluster.master.URL)

	Conn, err := cluster.master.GetNewDBConn()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error connection in exec query no log %s", err)
		return
	}
	defer Conn.Close()
	Conn.SetConnMaxLifetime(3595 * time.Second)
	pk, _ := cluster.master.GetTablePK(schema, table)
	if pk == "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Checksum, no primary key for table %s.%s", schema, table)
		t := cluster.master.DictTables.Get(schema + "." + table)
		t.TableSync = "NA"
		cluster.master.DictTables.Set(schema+"."+table, t)
		return
	}
	if strings.Contains(pk, ",") {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Checksum, composit primary key for table %s.%s", schema, table)
	}
	Conn.Exec("CREATE DATABASE IF NOT EXISTS replication_manager_schema")
	Conn.Exec("USE replication_manager_schema")
	Conn.Exec("SET SESSION TRANSACTION ISOLATION LEVEL REPEATABLE READ")
	Conn.Exec("SET SESSION group_concat_max_len = 1000000")

	Conn.Exec("CREATE OR REPLACE TABLE replication_manager_schema.table_checksum(chunkId BIGINT,chunkMinKey VARCHAR(254),chunkMaxKey VARCHAR(254),chunkCheckSum BIGINT UNSIGNED ) ENGINE=MYISAM")
	query := "CREATE TEMPORARY TABLE replication_manager_schema.table_chunk ENGINE=MYISAM SELECT FLOOR((@rows:=@rows+1/2000)) as chunkId, MIN(CONCAT_WS('/*;*/'," + pk + ")) as chunkMinKey, MAX(CONCAT_WS('/*;*/'," + pk + ")) as chunkMaxKey from " + schema + "." + table + " , (SELECT @rows:=0 FROM DUAL) A group by chunkId"
	_, err = Conn.Exec(query)
	Conn.Exec("SET SESSION binlog_format = 'STATEMENT'")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "ERROR: Could not process chunck %s %s", query, err)
		return
	}
	var md5Sum string
	err = Conn.QueryRowx("SELECT CONCAT( \"SUM(CRC32(CONCAT(\" , GROUP_CONCAT( CONCAT( \"IFNULL(\" , COLUMN_NAME, \",'N')\")),\")))\") as fields FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA ='" + schema + "' AND TABLE_NAME='" + table + "'").Scan(&md5Sum)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "ERROR: Could not get SQL md5Sum", err)
		return
	}

	// build predicate iterating over each pk columns
	predicate := " 1=1"
	pks := strings.Split(pk, ",")
	//	var ftype string
	for i, p := range pks {
		/*	query := "SELECT (select COLUMN_TYPE from information_schema.columns C where C.TABLE_NAME=TABLE_NAME AND C.COLUMN_NAME=COLUMN_NAME AND C.TABLE_SCHEMA=TABLE_SCHEMA LIMIT 1) as TYPE   from information_schema.KEY_COLUMN_USAGE WHERE CONSTRAINT_NAME='PRIMARY' AND CONSTRAINT_SCHEMA='" + schema + "' AND TABLE_NAME='" + table + "' AND  ORDINAL_POSITION=" + strconv.Itoa(i+1)
			err := Conn.QueryRowx(query).Scan(&ftype)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModGeneral,config.LvlErr, "ERROR: Could not fetch  datatype %s %s", query, err)
				return
			}
			separator := ""
			if strings.Contains(strings.ToLower(ftype), "char") || strings.Contains(strings.ToLower(ftype), "date") || strings.Contains(strings.ToLower(ftype), "enum") || strings.Contains(strings.ToLower(ftype), "timestamp") {
				separator = "'"
			}*/
		predicate = predicate + " AND A." + p + " >= SUBSTRING_INDEX(SUBSTRING_INDEX(B.chunkMinKey,'/*;*/'," + strconv.Itoa(i+1) + "),'/*;*/',-1) and A." + p + "<= SUBSTRING_INDEX(SUBSTRING_INDEX(B.chunkMaxKey,'/*;*/'," + strconv.Itoa(i+1) + "),'/*;*/',-1)"
	}

	for true {
		query := "INSERT INTO replication_manager_schema.table_checksum SELECT chunkId, chunkMinKey , chunkMaxKey," + md5Sum + " as chunkCheckSum FROM " + schema + "." + table + " A inner join (select * from replication_manager_schema.table_chunk limit 1) B on " + predicate
		_, err := Conn.Exec(query)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "ERROR: Could not process chunck %s %s", query, err)
			return
		}

		_, err2 := Conn.Exec("DELETE FROM replication_manager_schema.table_chunk limit 1")
		if err2 != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Checksum error deleting chunck %s", err)
			return
		}

		var count int
		err3 := Conn.QueryRowx("SELECT count(*) FROM replication_manager_schema.table_chunk").Scan(&count)
		if err3 != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Checksum can't fetch rows remaining", err)
			return
		}
		if count == 0 {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Finished checksum table %s.%s", schema, table)
			break
		}
		/*	slave := cluster.GetFirstWorkingSlave()
			if slave != nil {
				if slave.GetReplicationDelay() > 5 {
					time.Sleep(time.Duration(slave.GetReplicationDelay()) * time.Second)
				}
			}*/
	}
	cluster.master.Refresh()
	masterSeq := cluster.master.CurrentGtid.GetSeqServerIdNos(uint64(cluster.master.ServerID))
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Wait sync: Master sequence %d", masterSeq)

	for _, s := range cluster.slaves {
		if !s.IsFailed() && !s.IsReplicationBroken() {
			for true {
				slaveSeq := s.SlaveGtid.GetSeqServerIdNos(uint64(cluster.master.ServerID))
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Wait sync on slave %s sequence %d", s.URL, slaveSeq)
				if slaveSeq >= masterSeq {
					break
				} else {
					cluster.SetState("WARN0086", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0086"], s.URL), ErrFrom: "MON", ServerUrl: s.URL})
				}
				time.Sleep(1 * time.Second)
			}

		}
	}
	// check slave result
	masterChecksums, logs, err := dbhelper.GetTableChecksumResult(cluster.master.Conn)
	cluster.LogSQL(logs, err, cluster.master.URL, "CheckTableChecksum", config.LvlDbg, "GetTableChecksumResult")
	for _, s := range cluster.slaves {
		slaveChecksums, logs, err := dbhelper.GetTableChecksumResult(s.Conn)
		cluster.LogSQL(logs, err, s.URL, "CheckTableChecksum", config.LvlDbg, "GetTableChecksumResult")
		checkok := true

		for _, chunk := range masterChecksums {
			if chunk.ChunkCheckSum != slaveChecksums[chunk.ChunkId].ChunkCheckSum {
				checkok = false
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Checksum table failed chunk(%s,%s) %s.%s %s", chunk.ChunkMinKey, chunk.ChunkMaxKey, schema, table, s.URL)
				t := cluster.master.DictTables.Get(schema + "." + table)
				t.TableSync = "ER"
				cluster.master.DictTables.Set(schema+"."+table, t)
			}

		}
		if checkok {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Checksum table succeed %s.%s %s", schema, table, s.URL)
			t := cluster.master.DictTables.Get(schema + "." + table)
			t.TableSync = "OK"
			cluster.master.DictTables.Set(schema+"."+table, t)
		}
	}
}

// CheckSameServerID Check against the servers that all server id are differents
func (cluster *Cluster) CheckSameServerID() {
	for _, s := range cluster.Servers {
		if s.IsFailed() {
			continue
		}
		for _, sothers := range cluster.Servers {
			if sothers.IsFailed() || s.URL == sothers.URL {
				continue
			}
			if s.ServerID == sothers.ServerID {
				cluster.SetState("WARN0087", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["WARN0087"], s.URL, sothers.URL), ErrFrom: "MON", ServerUrl: s.URL})

			}
		}
	}
}

func (cluster *Cluster) IsSameWsrepUUID() bool {
	if cluster.GetTopology() != config.TopoMultiMasterWsrep {
		return true
	}
	for _, s := range cluster.Servers {
		// if server is failed or ignored, skip comparison
		if s.IsFailed() || s.IsIgnored() {
			continue
		}

		for _, sothers := range cluster.Servers {
			// if other server is failed or ignored, skip comparison
			if sothers.IsFailed() || s.URL == sothers.URL || sothers.IsIgnored() {
				continue
			}
			if s.Status.Get("WSREP_CLUSTER_STATE_UUID") != sothers.Status.Get("WSREP_CLUSTER_STATE_UUID") {
				cluster.SetState("ERR00083", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00083"], s.URL, s.Status.Get("WSREP_CLUSTER_STATE_UUID"), sothers.URL, sothers.Status.Get("WSREP_CLUSTER_STATE_UUID")), ErrFrom: "MON", ServerUrl: s.URL})
				return false
			}
		}
	}
	return true
}

func (cluster *Cluster) IsNotHavingMySQLErrantTransaction() bool {
	if cluster.GetMaster() == nil /*|| cluster.GetMaster().State == stateFailed */ {
		// disable check if master is crashed as the slave can get more GTID events and so slave GTID is not ubset of masetr GTID
		return true
	}
	if !(cluster.GetMaster().HasMySQLGTID()) {
		return true
	}
	if !cluster.Conf.RplCheckErrantTrx {
		return true
	}
	master_uuid := cluster.master.Variables.Get("SERVER_UUID")
	mastergtidvectors := strings.Split(cluster.master.Variables.Get("GTID_EXECUTED"), ",")
	mastergtidvectorstrim := make([]string, 0)
	for _, vector := range mastergtidvectors {
		if !strings.Contains(vector, master_uuid) {
			mastergtidvectorstrim = append(mastergtidvectorstrim, vector)
		}
	}
	mastergtid := strings.Join(mastergtidvectorstrim, ",")
	for _, s := range cluster.slaves {
		if s.IsFailed() || s.IsIgnored() {
			continue
		}
		slavegtidvectors := strings.Split(s.Variables.Get("GTID_EXECUTED"), ",")
		slavegtidvectorstrim := make([]string, 0)
		for _, vector := range slavegtidvectors {
			if !strings.Contains(vector, master_uuid) {
				slavegtidvectorstrim = append(slavegtidvectorstrim, vector)
			}
		}
		slavegtid := strings.Join(slavegtidvectorstrim, ",")

		//	hasErrantTrx, _, _ := dbhelper.HaveErrantTransactions(s.Conn, cluster.master.Variables.Get("GTID_EXECUTED"), s.Variables.Get("GTID_EXECUTED"))
		hasErrantTrx, query, _ := dbhelper.HaveErrantTransactions(s.Conn, mastergtid, slavegtid)
		if hasErrantTrx {
			cluster.SetState("WARN0091", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0091"], s.URL) + " " + query, ErrFrom: "MON", ServerUrl: s.URL})
			return false
		}
	}
	return true
}

func (cluster *Cluster) CheckCredentialRotation() {
	if cluster.inConnectVault {
		return
	}
	cluster.inConnectVault = true
	defer func() { cluster.inConnectVault = false }()
	if cluster.HasReplicationCredentialsRotation() {
		//cluster.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModGeneral,LvlInfo, "TEST checkReplicationCredentialsRotation")
		cluster.SetClusterReplicationCredentialsFromConfig()
		for _, slave := range cluster.slaves {
			ss, err := slave.GetSlaveStatus(slave.ReplicationSourceName)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No replication channel %s on slave %s : %s", slave.ReplicationSourceName, slave.URL, err)
			}
			slave.SetReplicationCredentialsRotation(ss)
		}
	}
	if cluster.HasMonitoringCredentialsRotation() {
		//cluster.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModGeneral,LvlInfo, "TEST checkCredentialsRotation")
		cluster.SetClusterMonitorCredentialsFromConfig()
		cluster.SetDbServersMonitoringCredential(cluster.Conf.Secrets["db-servers-credential"].Value)
	}
	if cluster.HasProxyCredentialsRotation() {
		cluster.SetClusterProxyCredentialsFromConfig()
		if cluster.Conf.ProxysqlOn {
			cluster.SetProxyServersCredential(cluster.Conf.ProxysqlUser+":"+cluster.Conf.Secrets["proxysql-password"].Value, config.ConstProxySqlproxy)
		}
		if cluster.Conf.MdbsProxyOn {
			cluster.SetProxyServersCredential(cluster.Conf.Secrets["shardproxy-credential"].Value, config.ConstProxySpider)
		}

	}

}

func (cluster *Cluster) CheckCanSaveDynamicConfig() {
	if cluster.Conf.SecretKey == nil && cluster.GetConf().ConfRewrite {
		cluster.SetState("ERR00090", state.State{ErrType: "WARNING", ErrDesc: clusterError["ERR00090"], ErrFrom: "CLUSTER"})
	}
}

func (cluster *Cluster) CheckIsOverwrite() {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Check overwrite conf path : %s\n", cluster.Conf.WorkingDir+"/"+cluster.Name)
	if _, err := os.Stat(cluster.Conf.WorkingDir + "/" + cluster.Name + "/overwrite.toml"); !os.IsNotExist(err) {
		input, err := os.ReadFile(cluster.Conf.WorkingDir + "/" + cluster.Name + "/overwrite.toml")
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cannot read config file %s : %s", cluster.Conf.WorkingDir+"/"+cluster.Name+"/overwrite.toml", err)
			return
		}

		lines := strings.Split(string(input), "\n")
		for i, line := range lines {
			if i == 1 {
				line = strings.ReplaceAll(line, " ", "")
				if line != "" {
					cluster.SetState("WARN0102", state.State{ErrType: "WARNING", ErrDesc: clusterError["WARN0102"], ErrFrom: "CLUSTER"})
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Check overwrite is not empty line %d : %s\n", i, line)
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Check overwrite is empty line %d : %s\n", i, line)
				}

			}
			//cluster.SetState("WARN0102", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0102"]), ErrFrom: "CLUSTER"})
		}
	}
}

func (cluster *Cluster) CheckInjectConfig() {
	filePath := cluster.Conf.WorkingDir + "/.pull/" + cluster.Name + "/inject.toml"

	if fileInfo, err := os.Stat(filePath); !os.IsNotExist(err) {
		//if empty, nothing to extract
		if fileInfo.Size() == 0 {
			return
		} else {
			data, err := os.ReadFile(filePath)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cannot read config file %s : %s", filePath, err)
				return
			}
			//extract all the data of inject.toml file
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				parts := strings.Split(line, "=")
				if len(parts) == 2 {
					key := strings.TrimSpace(parts[0])
					value := strings.TrimSpace(parts[1])
					cluster.Conf.DynamicFlagMap[key] = strings.ReplaceAll(value, `"`, "")
					//set data of the inject.toml
					cluster.SetInjectVariables()
				}
			}
			//then we can erase data from inject.toml file
			file, _ := os.OpenFile(filePath, os.O_WRONLY|os.O_TRUNC, 0644)
			err = file.Truncate(0)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cannot truncate config file after extraction %s : %s", filePath, err)
			}
			file.Close()
		}
	}
}

func (cluster *Cluster) CheckDefaultUser(i bool) {
	creds := make([]string, 0)
	if cluster.APIUsers["admin"].Password == "repman" {
		creds = append(creds, "admin")
	}
	if cluster.APIUsers["dba"].Password == "repman" {
		creds = append(creds, "dba")
	}
	out := strings.Join(creds, ",")
	if out != "" {
		if i {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, fmt.Sprintf(clusterError["WARN0108"], out))
		}
		cluster.SetState("WARN0108", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0108"], out), ErrFrom: "CLUSTER"})
	}
}

func (cluster *Cluster) CheckAvailableCredit() {
	if cluster.Conf.Cloud18ApplicationCreditsUsed > cluster.Conf.Cloud18ApplicationCredits {
		cluster.SetState("CREDIT01", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(config.ClusterError["CREDIT01"], cluster.Conf.Cloud18ApplicationCredits, cluster.Conf.Cloud18ApplicationCreditsUsed), ErrFrom: "CLUSTER"})
	}
}

type TresholdStat struct {
	Node     string
	Avail    int64
	Treshold int64
}

func (cluster *Cluster) CheckOpenSVCTresholds() {
	if cluster.Conf.ProvOrchestrator != "opensvc" {
		return
	}

	nodestats, _ := cluster.GetOpenSVCStats()
	if len(nodestats) == 0 {
		return
	}

	overmem := make([]TresholdStat, 0)
	overswap := make([]TresholdStat, 0)

	for _, ns := range nodestats {
		if ns.Stats.MemAvail < ns.MinAvailMem {
			overmem = append(overmem, TresholdStat{
				Node:     ns.Node,
				Avail:    ns.Stats.MemAvail,
				Treshold: ns.MinAvailMem,
			})
		}

		if ns.Stats.SwapAvail < ns.MinAvailSwap {
			overswap = append(overswap, TresholdStat{
				Node:     ns.Node,
				Avail:    ns.Stats.SwapAvail,
				Treshold: ns.MinAvailSwap,
			})
		}
	}

	if len(overmem) > 0 {
		cluster.SetState("WARN0150", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0150"], overmem), ErrFrom: "CLUSTER"})
	}
	if len(overswap) > 0 {
		cluster.SetState("WARN0151", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0151"], overswap), ErrFrom: "CLUSTER"})
	}
}

func (cluster *Cluster) CheckDBCredentials() {
	// This check is to prevent invalid credentials configuration
	// when both users are the same but passwords are different
	// which would lead to a non working replication
	if cluster.GetDbUser() == cluster.GetRplUser() && cluster.GetDbPass() != cluster.GetRplPass() {
		cluster.SetState("ERR00101", state.State{ErrType: "ERROR", ErrDesc: config.ClusterError["ERR00101"], ErrFrom: "CLUSTER"})
	}
}

func (cluster *Cluster) CheckJobsVersion() {
	if cluster.Conf.ProvOrchestrator != "opensvc" {
		return
	}

	for _, server := range cluster.Servers {
		server.CheckJobsVersion()
	}
}

func (cluster *Cluster) JobsCheckSchedulerTable() {
	for _, server := range cluster.Servers {
		err := server.JobsCheckSchedulerTable()
		if err != nil {
			cluster.SetState("WARN0153", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0153"], server.URL, err), ErrFrom: "CLUSTER"})
		}
	}
}

func (cluster *Cluster) CheckGlobalDeprecatedKeys() {
	gkeys := cluster.GetGlobalDeprecatedKeys()
	if len(gkeys) > 0 {
		cluster.SetState("WARN0159", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0159"], strings.Join(gkeys, ",")), ErrFrom: "CLUSTER"})
	}
}

func (cluster *Cluster) CheckClusterDeprecatedKeys() {
	dkeys := cluster.GetDeprecatedKeys()
	if len(dkeys) > 0 {
		cluster.SetState("WARN0160", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0160"], cluster.Name, strings.Join(dkeys, ",")), ErrFrom: "CLUSTER"})
	}
}

func (cluster *Cluster) CheckHasFailCertLoadP12() {
	if cluster.Conf.ProvOrchestrator != "opensvc" {
		return
	}

	if cluster.failLoadP12Cert {
		cluster.GetStateMachine().PreserveState("WARN0099")
	}
}

func (cluster *Cluster) CheckDisksUsage() {
	if !cluster.Conf.BackupCheckFreeSpace {
		return
	}

	reducePolicy := ""

	if cluster.Conf.BackupRestic {
		reducePolicy = "Considering reducing restic keep-* policies to save disk space."
	}

	overThreshold := cluster.DiskStatManager.GetOverThresholdPaths(float64(cluster.Conf.BackupDiskTresholdWarn), float64(cluster.Conf.BackupDiskTresholdCrit))
	for level, statlist := range overThreshold {
		if level == "critical" {
			cluster.SetState("WARN0140", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0140"], reducePolicy, statlist, cluster.Conf.BackupDiskTresholdCrit), ErrFrom: "JOB"})
		} else if level == "warning" {
			cluster.SetState("WARN0139", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0139"], statlist, cluster.Conf.BackupDiskTresholdWarn), ErrFrom: "JOB"})
		}
	}
}

func (cluster *Cluster) CheckAllBackupEstimatedSize() {
	backupTypes := []string{"logical", "physical", "binlog"}
	for _, btype := range backupTypes {
		err := cluster.CheckEstimatedBackupSize(btype)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Estimated backup size check for %s backup: %s", btype, err)
		}
	}
}

func (cluster *Cluster) CheckEstimatedBackupSize(backtype string) error {
	if !cluster.Conf.BackupEstimateSize {
		return nil
	}

	bcksrv := cluster.GetBackupServer()
	if bcksrv == nil {
		bcksrv = cluster.GetMaster()
		if bcksrv == nil {
			return fmt.Errorf("No backup server or master server found for cluster %s", cluster.Name)
		}
	}

	diskstat, err := cluster.GetDiskStat(bcksrv.GetMyBackupDirectory())
	if err != nil {
		return err
	}

	// Estimate size if disk usage is over treshold and estimate size is enabled. For binlog we will always estimate size to 2GB

	free := diskstat.Free
	required := uint64(0)

	required, err = cluster.GetEstimatedBackupSize(backtype)
	if err != nil {
		return err
	}

	if free < required {
		if backtype == "logical" {
			cluster.SetState("WARN0141", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0141"], cluster.Conf.BackupLogicalType, bcksrv.URL, diskstat.Path, humanize.Bytes(diskstat.Free), humanize.Bytes(required)), ErrFrom: "JOB", ServerUrl: bcksrv.URL})
		} else if backtype == "physical" {
			cluster.SetState("WARN0142", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0142"], cluster.Conf.BackupPhysicalType, bcksrv.URL, diskstat.Path, humanize.Bytes(diskstat.Free), humanize.Bytes(required)), ErrFrom: "JOB", ServerUrl: bcksrv.URL})
		} else if backtype == "binlog" {
			cluster.SetState("WARN0143", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0143"], bcksrv.URL, diskstat.Path, humanize.Bytes(diskstat.Free), humanize.Bytes(required)), ErrFrom: "JOB", ServerUrl: bcksrv.URL})
		}

		return fmt.Errorf("Not enough free space on %s for backup. Free: %s", diskstat.Path, humanize.Bytes(diskstat.Free))
	}

	return nil
}

func (cluster *Cluster) CheckNeedConfigFetch() {
	for _, srv := range cluster.Servers {
		if srv == nil {
			continue
		}
		srv.CheckNeedConfigFetch()
	}
}

// CheckDummyConfigSendCookies checks all servers for dummy config send cookies and sends configs
func (cluster *Cluster) CheckDummyConfigSendCookies() {
	for _, srv := range cluster.Servers {
		if srv == nil {
			continue
		}

		srv.ProcessDummyConfigSendCookie()
	}
}

// CheckRestartCookies checks all servers for restart cookies and processes them
func (cluster *Cluster) CheckRestartCookies() {
	for _, srv := range cluster.Servers {
		if srv == nil {
			continue
		}

		if srv.HasRestartCookie() {
			// Get the stored parameters
			nodeParam := srv.RestartNode
			ridParam := srv.RestartRid

			if cluster.Conf != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
					"Processing restart cookie for server %s (node: %s, rid: %s)", srv.URL, nodeParam, ridParam)
			}
			// Call the restart function with stored parameters
			err := cluster.RestartDatabaseService(srv, nodeParam, ridParam)
			if err != nil {
				if cluster.Conf != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
						"Failed to restart server %s: %s", srv.URL, err)
				}
				// Delete cookie even on error to avoid infinite retries
				srv.DelRestartCookie()
				// Clear stored parameters
				srv.RestartNode = ""
				srv.RestartRid = ""
			}
		}
	}
}

// CleanupRestartCookies removes any lingering restart cookies and clears parameters at cluster startup
// This prevents unwanted restarts from old cookies that may have been left from previous runs
func (cluster *Cluster) CleanupRestartCookies() {
	cleanedCount := 0
	for _, srv := range cluster.Servers {
		if srv == nil {
			continue
		}

		// Check if restart cookie exists
		if srv.HasRestartCookie() {
			if cluster.Conf != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
					"Cleaning up lingering restart cookie for server %s (node: %s, rid: %s)",
					srv.URL, srv.RestartNode, srv.RestartRid)
			}

			// Delete the cookie
			srv.DelRestartCookie()
			cleanedCount++
		}

		// Clear any stored parameters (whether cookie existed or not)
		srv.RestartNode = ""
		srv.RestartRid = ""
	}

	if cleanedCount > 0 && cluster.Conf != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"Cleaned up %d restart cookie(s) at cluster startup", cleanedCount)
	}
}
