// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
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
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/router/maxscale"
	"github.com/signal18/replication-manager/utils/alert"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

func (cluster *Cluster) CheckFailed() {
	// Don't trigger a failover if a switchover is happening
	if cluster.StateMachine.IsInFailover() {
		cluster.StateMachine.AddState("ERR00001", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00001"]), ErrFrom: "CHECK"})
		return
	}
	if cluster.master == nil {
		cluster.LogPrintf(LvlDbg, "Master not discovered, skipping failover check")
	}

	if cluster.isFoundCandidateMaster() &&
		cluster.isBetweenFailoverTimeValid() &&
		cluster.IsNotHavingMySQLErrantTransaction() &&
		cluster.IsSameWsrepUUID() &&
		cluster.isMaxMasterFailedCountReached() &&
		cluster.isActiveArbitration() &&
		cluster.isMaxClusterFailoverCountNotReached() &&
		cluster.isAutomaticFailover() &&
		cluster.isMasterFailed() &&
		cluster.isNotFirstSlave() &&
		cluster.isArbitratorAlive() {

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
	if hasBinLogs == false && cluster.Conf.CheckBinFilter == true && (sl.GetSourceClusterName() == cluster.Name || sl.GetSourceClusterName() == "") {
		if cluster.Conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf(LvlWarn, "Binlog filters differ on master and slave %s. Skipping", sl.URL)
		}
		return false
	}
	if cluster.IsEqualReplicationFilters(cluster.master, sl) == false && (sl.GetSourceClusterName() == cluster.Name || sl.GetSourceClusterName() == "") && cluster.Conf.CheckReplFilter == true {
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
	cluster.StateMachine.AddState("ERR00002", state.State{ErrType: "ERR00002", ErrDesc: fmt.Sprintf(clusterError["ERR00002"]), ErrFrom: "CHECK"})
	return false
}

func (cluster *Cluster) isMasterFailed() bool {
	//if master not discover, we can considered it not failed
	//can cause infinity loops if set to true
	if cluster.master == nil {
		return false
	}
	if cluster.master.State == stateFailed {
		return true
	}
	return false
}

// isMaxMasterFailedCountReach test tentative to connect
func (cluster *Cluster) isMaxMasterFailedCountReached() bool {
	// no illimited failed count

	if cluster.GetMaster().FailCount >= cluster.Conf.MaxFail {
		cluster.StateMachine.AddState("WARN0023", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0023"]), ErrFrom: "CHECK"})
		return true
	} else {
		//	cluster.StateMachine.AddState("ERR00023", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf("Constraint is blocking state %s, interactive:%t, maxfail reached:%d", cluster.master.State, cluster.Conf.Interactive, cluster.Conf.MaxFail), ErrFrom: "CONF"})
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
		cluster.StateMachine.AddState("ERR00027", state.State{ErrType: LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00027"]), ErrFrom: "CHECK"})
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
		cluster.StateMachine.AddState("ERR00029", state.State{ErrType: LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00029"]), ErrFrom: "CHECK"})
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
				status, logs, err := dbhelper.GetStatusAsInt(s.Conn, s.DBVersion)
				cluster.LogSQL(logs, err, s.URL, "isOneSlaveHeartbeatIncreasing", LvlDbg, "GetStatusAsInt")
				saveheartbeats := status["SLAVE_RECEIVED_HEARTBEATS"]
				if cluster.Conf.LogLevel > 1 {
					cluster.LogPrintf(LvlDbg, "SLAVE_RECEIVED_HEARTBEATS %d", saveheartbeats)
				}
				time.Sleep(time.Duration(cluster.Conf.CheckFalsePositiveHeartbeatTimeout) * time.Second)
				status2, logs, err := dbhelper.GetStatusAsInt(s.Conn, s.DBVersion)
				cluster.LogSQL(logs, err, s.URL, "isOneSlaveHeartbeatIncreasing", LvlDbg, "GetStatusAsInt")
				if cluster.Conf.LogLevel > 1 {
					cluster.LogPrintf(LvlDbg, "SLAVE_RECEIVED_HEARTBEATS %d", status2["SLAVE_RECEIVED_HEARTBEATS"])
				}
				if status2["SLAVE_RECEIVED_HEARTBEATS"] > saveheartbeats {
					cluster.StateMachine.AddState("ERR00028", state.State{ErrType: LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00028"], s.URL), ErrFrom: "CHECK"})
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
		cluster.StateMachine.AddState("ERR00030", state.State{ErrType: LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00030"], cluster.master.MxsServerStatus), ErrFrom: "CHECK"})
		return true
	}
	return false
}

func (cluster *Cluster) isFoundCandidateMaster() bool {
	if cluster.GetTopology() == topoActivePassive {
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
		cluster.StateMachine.AddState("ERR00032", state.State{ErrType: LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00032"]), ErrFrom: "CHECK"})
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
		cluster.StateMachine.AddState("ERR00022", state.State{ErrType: LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00022"]), ErrFrom: "CHECK"})
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
		cluster.StateMachine.AddState("ERR00022", state.State{ErrType: LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00022"]), ErrFrom: "CHECK"})
		return false
	}
	if r.Arbitration == "winner" {
		cluster.LogPrintf(LvlInfo, "Arbitrator says: winner")
		return true
	}
	cluster.StateMachine.AddState("ERR00022", state.State{ErrType: LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00022"]), ErrFrom: "CHECK"})
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
		cluster.StateMachine.AddState("ERR00031", state.State{ErrType: LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00031"]), ErrFrom: "CHECK"})
		return true
	}
	return false
}

func (cluster *Cluster) isArbitratorAlive() bool {
	if !cluster.Conf.Arbitration {
		return true
	}
	if cluster.IsFailedArbitrator {
		cluster.StateMachine.AddState("ERR00055", state.State{ErrType: LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00055"], cluster.Conf.ArbitrationSasHosts), ErrFrom: "CHECK"})
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
	if cluster.master == nil {
		cluster.StateMachine.AddState("ERR00026", state.State{ErrType: LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00026"]), ErrFrom: "CHECK"})
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
			cluster.LogPrintf(LvlErr, "Failed opening logfile, disabling for the rest of the session")
			cluster.Conf.LogFile = ""
		}
	}

	// if slaves option has been supplied, split into a slice.
	if cluster.Conf.Hosts == "" {
		cluster.LogPrintf(LvlErr, "No hosts list specified")
		return errors.New("No hosts list specified")
	}

	// validate users
	if cluster.Conf.User == "" {
		cluster.LogPrintf(LvlErr, "No master user/pair specified")
		return errors.New("No master user/pair specified")
	}

	if cluster.Conf.RplUser == "" {
		cluster.LogPrintf(LvlErr, "No replication user/pair specified")
		return errors.New("No replication user/pair specified")
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

	for _, host := range pfa {
		if !strings.Contains(cluster.Conf.Hosts, host) {
			cluster.LogPrintf(LvlErr, clusterError["ERR00074"], host)
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

func (cluster *Cluster) CheckCapture(state state.State) {
	if !cluster.Conf.MonitorCapture {
		return
	}
	if strings.Contains(cluster.Conf.MonitorCaptureTrigger, state.ErrKey) {
		if state.ServerUrl != "" {
			srv := cluster.GetServerFromURL(state.ServerUrl)
			if srv != nil {
				srv.Capture()
			}
		}
	}
}

func (cluster *Cluster) CheckAlert(state state.State) {
	if cluster.Conf.MonitoringAlertTrigger == "" {
		return
	}

	// exit even earlier
	if cluster.Conf.MailTo == "" && cluster.Conf.AlertScript == "" {
		return
	}

	if strings.Contains(cluster.Conf.MonitoringAlertTrigger, state.ErrKey) {
		a := alert.Alert{
			State:  state.ErrKey,
			Origin: cluster.Name,
		}

		err := cluster.SendAlert(a)
		if err != nil {
			cluster.LogPrintf("ERROR", "Could not send alert: %s ", err)
		}
	}
}

func (cluster *Cluster) SendAlert(alert alert.Alert) error {
	if cluster.Conf.MailTo != "" {
		alert.From = cluster.Conf.MailFrom
		alert.To = cluster.Conf.MailTo
		alert.Destination = cluster.Conf.MailSMTPAddr
		alert.User = cluster.Conf.MailSMTPUser
		alert.Password = cluster.Conf.MailSMTPPassword
		alert.TlsVerify = cluster.Conf.MailSMTPTLSSkipVerify
		err := alert.Email()
		if err != nil {
			cluster.LogPrintf("ERROR", "Could not send mail alert: %s ", err)
		}
	}
	if cluster.Conf.AlertScript != "" {
		cluster.LogPrintf("INFO", "Calling alert script")
		var out []byte
		out, err := exec.Command(cluster.Conf.AlertScript, alert.Origin, alert.PrevState, alert.State).CombinedOutput()
		if err != nil {
			cluster.LogPrintf("ERROR", "%s", err)
		}

		cluster.LogPrintf("INFO", "Alert script complete:", string(out))
	}

	return nil
}

func (cluster *Cluster) CheckAllTableChecksum() {
	for _, t := range cluster.master.Tables {
		cluster.CheckTableChecksum(t.TableSchema, t.TableName)
	}
}

func (cluster *Cluster) CheckTableChecksum(schema string, table string) {

	cluster.LogPrintf(LvlInfo, "Checksum master table %s.%s %s", schema, table, cluster.master.URL)

	Conn, err := cluster.master.GetNewDBConn()
	if err != nil {
		cluster.master.ClusterGroup.LogPrintf(LvlErr, "Error connection in exec query no log %s", err)
		return
	}
	defer Conn.Close()
	Conn.SetConnMaxLifetime(3595 * time.Second)
	pk, _ := cluster.master.GetTablePK(schema, table)
	if pk == "" {
		cluster.master.ClusterGroup.LogPrintf(LvlErr, "Checksum, no primary key for table %s.%s", schema, table)
		t := cluster.master.DictTables[schema+"."+table]
		t.TableSync = "NA"
		cluster.master.DictTables[schema+"."+table] = t
		return
	}
	if strings.Contains(pk, ",") {
		cluster.master.ClusterGroup.LogPrintf(LvlInfo, "Checksum, composit primary key for table %s.%s", schema, table)
	}
	Conn.Exec("CREATE DATABASE IF NOT EXISTS replication_manager_schema")
	Conn.Exec("USE replication_manager_schema")
	Conn.Exec("SET SESSION TRANSACTION ISOLATION LEVEL REPEATABLE READ")
	Conn.Exec("SET SESSION group_concat_max_len = 1000000")

	Conn.Exec("CREATE OR REPLACE TABLE replication_manager_schema.table_checksum(chunkId BIGINT,chunkMinKey VARCHAR(254),chunkMaxKey VARCHAR(254),chunkCheckSum BIGINT UNSIGNED ) ENGINE=MYISAM")
	query := "CREATE TEMPORARY TABLE replication_manager_schema.table_chunck ENGINE=MYISAM SELECT FLOOR((@rows:=@rows+1/2000)) as chunkId, MIN(CONCAT_WS('/*;*/'," + pk + ")) as chunkMinKey, MAX(CONCAT_WS('/*;*/'," + pk + ")) as chunkMaxKey from " + schema + "." + table + " , (SELECT @rows:=0 FROM DUAL) A group by chunkId"
	_, err = Conn.Exec(query)
	Conn.Exec("SET SESSION binlog_format = 'STATEMENT'")
	if err != nil {
		cluster.LogPrintf(LvlErr, "ERROR: Could not process chunck %s %s", query, err)
		return
	}
	var md5Sum string
	err = Conn.QueryRowx("SELECT CONCAT( \"SUM(CRC32(CONCAT(\" , GROUP_CONCAT( CONCAT( \"IFNULL(\" , COLUMN_NAME, \",'N')\")),\")))\") as fields FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA ='" + schema + "' AND TABLE_NAME='" + table + "'").Scan(&md5Sum)
	if err != nil {
		cluster.LogPrintf(LvlErr, "ERROR: Could not get SQL md5Sum", err)
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
				cluster.LogPrintf(LvlErr, "ERROR: Could not fetch  datatype %s %s", query, err)
				return
			}
			separator := ""
			if strings.Contains(strings.ToLower(ftype), "char") || strings.Contains(strings.ToLower(ftype), "date") || strings.Contains(strings.ToLower(ftype), "enum") || strings.Contains(strings.ToLower(ftype), "timestamp") {
				separator = "'"
			}*/
		predicate = predicate + " AND A." + p + " >= SUBSTRING_INDEX(SUBSTRING_INDEX(B.chunkMinKey,'/*;*/'," + strconv.Itoa(i+1) + "),'/*;*/',-1) and A." + p + "<= SUBSTRING_INDEX(SUBSTRING_INDEX(B.chunkMaxKey,'/*;*/'," + strconv.Itoa(i+1) + "),'/*;*/',-1)"
	}

	for true {
		query := "INSERT INTO replication_manager_schema.table_checksum SELECT chunkId, chunkMinKey , chunkMaxKey," + md5Sum + " as chunkCheckSum FROM " + schema + "." + table + " A inner join (select * from replication_manager_schema.table_chunck limit 1) B on " + predicate
		_, err := Conn.Exec(query)
		if err != nil {
			cluster.LogPrintf(LvlErr, "ERROR: Could not process chunck %s %s", query, err)
			return
		}
		res, err2 := Conn.Exec("DELETE FROM replication_manager_schema.table_chunck limit 1")
		if err2 != nil {
			cluster.LogPrintf(LvlErr, "Checksum error deleting chunck %s", err)
			return
		}

		i, err3 := res.RowsAffected()
		if err3 != nil {
			cluster.LogPrintf(LvlErr, "Checksum can't fetch rows affected ", err)
			return
		}
		if i == 0 {
			cluster.LogPrintf(LvlInfo, "Finished checksum table %s.%s", schema, table)
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
	cluster.LogPrintf(LvlInfo, "Wait sync: Master sequence %d", masterSeq)

	for _, s := range cluster.slaves {
		if !s.IsFailed() && !s.IsReplicationBroken() {
			for true {
				slaveSeq := s.SlaveGtid.GetSeqServerIdNos(uint64(cluster.master.ServerID))
				cluster.LogPrintf(LvlInfo, "Wait sync on slave %s sequence %d", s.URL, slaveSeq)
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
	cluster.LogSQL(logs, err, cluster.master.URL, "CheckTableChecksum", LvlDbg, "GetTableChecksumResult")
	for _, s := range cluster.slaves {
		slaveChecksums, logs, err := dbhelper.GetTableChecksumResult(s.Conn)
		cluster.LogSQL(logs, err, s.URL, "CheckTableChecksum", LvlDbg, "GetTableChecksumResult")
		checkok := true
		for _, chunk := range masterChecksums {
			if chunk.ChunkCheckSum != slaveChecksums[chunk.ChunkId].ChunkCheckSum {
				checkok = false
				cluster.LogPrintf(LvlInfo, "Checksum table failed chunk(%s,%s) %s.%s %s", chunk.ChunkMinKey, chunk.ChunkMaxKey, schema, table, s.URL)
				t := cluster.master.DictTables[schema+"."+table]
				t.TableSync = "ER"
				cluster.master.DictTables[schema+"."+table] = t
			}

		}
		if checkok {
			cluster.LogPrintf(LvlInfo, "Checksum table succeed %s.%s %s", schema, table, s.URL)
			t := cluster.master.DictTables[schema+"."+table]
			t.TableSync = "OK"
			cluster.master.DictTables[schema+"."+table] = t
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
				cluster.SetState("WARN0087", state.State{ErrType: LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0087"], s.URL, sothers.URL), ErrFrom: "MON", ServerUrl: s.URL})

			}
		}
	}
}

func (cluster *Cluster) IsSameWsrepUUID() bool {
	if cluster.GetTopology() != topoMultiMasterWsrep {
		return true
	}
	for _, s := range cluster.Servers {
		if s.IsFailed() {
			continue
		}
		for _, sothers := range cluster.Servers {
			if sothers.IsFailed() || s.URL == sothers.URL {
				continue
			}
			if s.Status["WSREP_CLUSTER_STATE_UUID"] != sothers.Status["WSREP_CLUSTER_STATE_UUID"] {
				cluster.SetState("ERR00083", state.State{ErrType: LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00083"], s.URL, s.Status["WSREP_CLUSTER_STATE_UUID"], sothers.URL, sothers.Status["WSREP_CLUSTER_STATE_UUID"]), ErrFrom: "MON", ServerUrl: s.URL})
				return false
			}
		}
	}
	return true
}

func (cluster *Cluster) IsNotHavingMySQLErrantTransaction() bool {
	if cluster.GetMaster() == nil {
		return false
	}
	if !(cluster.GetMaster().HasMySQLGTID()) {
		return true
	}
	for _, s := range cluster.slaves {
		if s.IsFailed() || s.IsIgnored() {
			continue
		}
		hasErrantTrx, _, _ := dbhelper.HaveErrantTransactions(s.Conn, cluster.master.Variables["GTID_EXECUTED"], s.Variables["GTID_EXECUTED"])
		if hasErrantTrx {
			cluster.SetState("WARN0091", state.State{ErrType: LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0091"], s.URL), ErrFrom: "MON", ServerUrl: s.URL})
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
		//cluster.LogPrintf(LvlInfo, "TEST checkReplicationCredentialsRotation")
		cluster.SetClusterReplicationCredentialsFromConfig()
		for _, slave := range cluster.slaves {
			ss, err := slave.GetSlaveStatus(slave.ReplicationSourceName)
			if err != nil {
				cluster.LogPrintf(LvlErr, "No replication channel %s on slave %s : %s", slave.ReplicationSourceName, slave.URL, err)
			}
			slave.SetReplicationCredentialsRotation(ss)
		}
	}
	if cluster.HasMonitoringCredentialsRotation() {
		//cluster.LogPrintf(LvlInfo, "TEST checkCredentialsRotation")
		cluster.SetClusterMonitorCredentialsFromConfig()
		cluster.SetDbServersMonitoringCredential(cluster.Conf.User)
	}
}

func (cluster *Cluster) CheckCanSaveDynamicConfig() {
	_, err := cluster.GetPasswordKey(cluster.Conf.MonitoringKeyPath)
	if err != nil && cluster.GetConf().ConfRewrite {
		cluster.SetState("ERR00090", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00090"]), ErrFrom: "CLUSTER"})
	}
}

func (cluster *Cluster) CheckIsOverwrite() {
	cluster.LogPrintf(LvlDbg, "Check overwrite conf path : %s\n", cluster.Conf.WorkingDir+"/"+cluster.Name)
	if _, err := os.Stat(cluster.Conf.WorkingDir + "/" + cluster.Name + "/overwrite.toml"); !os.IsNotExist(err) {
		input, err := ioutil.ReadFile(cluster.Conf.WorkingDir + "/" + cluster.Name + "/overwrite.toml")
		if err != nil {
			cluster.LogPrintf(LvlErr, "Cannot read config file %s : %s", cluster.Conf.WorkingDir+"/"+cluster.Name+"/overwrite.toml", err)
			return
		}

		lines := strings.Split(string(input), "\n")
		for i, line := range lines {
			if i == 1 {
				line = strings.ReplaceAll(line, " ", "")
				if line != "" {
					cluster.SetState("WARN0102", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0102"]), ErrFrom: "CLUSTER"})
					cluster.LogPrintf(LvlErr, "An immutable parameter has been changed in cluster %s and is tracked in overwrite.toml. Use the config-merge command to save your changes.\n", cluster.Name)
					cluster.LogPrintf(LvlDbg, "Check overwrite is not empty line %d : %s\n", i, line)
				} else {
					cluster.LogPrintf(LvlDbg, "Check overwrite is empty line %d : %s\n", i, line)
				}

			}
			//cluster.SetState("WARN0102", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0102"]), ErrFrom: "CLUSTER"})
		}
	}
}
