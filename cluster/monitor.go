// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"bytes"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/tanji/replication-manager/alert"
	"github.com/tanji/replication-manager/dbhelper"
	"github.com/tanji/replication-manager/graphite"
	"github.com/tanji/replication-manager/gtid"
	"github.com/tanji/replication-manager/maxscale"

	"github.com/tanji/replication-manager/misc"
)

// ServerMonitor defines a server to monitor.
type ServerMonitor struct {
	Conn                        *sqlx.DB
	URL                         string
	DSN                         string
	Host                        string
	Port                        string
	IP                          string
	BinlogPos                   *gtid.List
	Strict                      string
	ServerID                    uint
	MasterServerID              uint
	MasterHost                  string
	LogBin                      string
	UsingGtid                   string
	CurrentGtid                 *gtid.List
	SlaveGtid                   *gtid.List
	IOGtid                      *gtid.List
	IOThread                    string
	SQLThread                   string
	ReadOnly                    string
	Delay                       sql.NullInt64
	State                       string
	PrevState                   string
	IOErrno                     uint
	IOError                     string
	SQLErrno                    uint
	SQLError                    string
	FailCount                   int
	FailSuspectHeartbeat        int64
	SemiSyncMasterStatus        bool
	SemiSyncSlaveStatus         bool
	RplMasterStatus             bool
	EventScheduler              bool
	EventStatus                 []dbhelper.Event
	ClusterGroup                *Cluster
	MasterLogFile               string
	MasterLogPos                string
	MasterHeartbeatPeriod       float64
	MasterUseGtid               string
	FailoverMasterLogFile       string
	FailoverMasterLogPos        string
	FailoverSemiSyncSlaveStatus bool
	FailoverIOGtid              *gtid.List
	Process                     *os.Process
	Name                        string //Unique name given by reg test initMariaDB
	Conf                        string //Unique Conf given by reg test initMariaDB
	MxsServerName               string //Unique server Name in maxscale conf
	MxsServerStatus             string
	MxsServerConnections        int
}

type serverList []*ServerMonitor

var maxConn string

const (
	stateFailed      string = "Failed"
	stateMaster      string = "Master"
	stateSlave       string = "Slave"
	stateSlaveErr    string = "SlaveErr"
	stateSlaveLate   string = "SlaveLate"
	stateUnconn      string = "StandAlone"
	stateSuspect     string = "Suspect"
	stateShard       string = "Shard"
	stateProv        string = "Provision"
	stateMasterAlone string = "MasterAlone"
)

/* Initializes a server object */
func (cluster *Cluster) newServerMonitor(url string) (*ServerMonitor, error) {

	server := new(ServerMonitor)
	server.ClusterGroup = cluster
	server.URL = url
	server.Host, server.Port = misc.SplitHostPort(url)
	var err error
	server.IP, err = dbhelper.CheckHostAddr(server.Host)
	if err != nil {
		errmsg := fmt.Errorf("ERROR: DNS resolution error for host %s", server.Host)
		return server, errmsg
	}
	params := fmt.Sprintf("?timeout=%ds", cluster.conf.Timeout)
	mydsn := func() string {
		dsn := cluster.dbUser + ":" + cluster.dbPass + "@"
		if server.Host != "" {
			dsn += "tcp(" + server.Host + ":" + server.Port + ")/" + params
		} else {
			dsn += "unix(" + cluster.conf.Socket + ")/" + params
		}
		return dsn
	}
	server.DSN = mydsn()
	server.Conn, err = sqlx.Open("mysql", server.DSN)
	return server, err
}

func (server *ServerMonitor) check(wg *sync.WaitGroup) {

	defer wg.Done()
	if server.ClusterGroup.sme.IsInFailover() {
		if server.ClusterGroup.conf.LogLevel > 2 {
			server.ClusterGroup.LogPrintf("DEBUG: Inside failover, skip server check")
		}
		return
	}
	if server.PrevState != server.State {
		server.PrevState = server.State
	}

	if server.ClusterGroup.conf.LogLevel > 2 {
		// LogPrint("DEBUG: Checking server", server.Host)
	}

	var err error
	switch server.ClusterGroup.conf.CheckType {
	case "tcp":
		err = server.Conn.Ping()
	case "agent":
		var resp *http.Response
		resp, err = http.Get("http://" + server.Host + ":10001/check/")
		if resp.StatusCode != 200 {
			// if 404, consider server down or agent killed. Don't initiate anything
			err = fmt.Errorf("HTTP Response Code Error: %d", resp.StatusCode)
		}
	}

	// Handle failure cases here
	if err != nil {
		if server.ClusterGroup.conf.LogLevel > 2 {
			server.ClusterGroup.LogPrintf("DEBUG: Failure detection handling for server %s", server.URL)
		}
		if err != sql.ErrNoRows && (server.State == stateMaster || server.State == stateSuspect) {
			server.FailCount++
			server.FailSuspectHeartbeat = server.ClusterGroup.sme.GetHeartbeats()
			if server.ClusterGroup.master != nil {
				if server.URL == server.ClusterGroup.master.URL {
					if server.ClusterGroup.master.FailCount <= server.ClusterGroup.conf.MaxFail {
						server.ClusterGroup.LogPrintf("WARN : Master Failure detected! Retry %d/%d", server.ClusterGroup.master.FailCount, server.ClusterGroup.conf.MaxFail)
					}
					if server.FailCount >= server.ClusterGroup.conf.MaxFail {
						if server.FailCount == server.ClusterGroup.conf.MaxFail {
							server.ClusterGroup.LogPrint("WARN : Declaring master as failed")
						}
						server.ClusterGroup.master.State = stateFailed
					} else {
						server.ClusterGroup.master.State = stateSuspect
					}
				}
			}
		} else {
			if server.State != stateMaster && server.State != stateFailed {
				server.FailCount++
				if server.FailCount >= server.ClusterGroup.conf.MaxFail {
					if server.FailCount == server.ClusterGroup.conf.MaxFail {
						server.ClusterGroup.LogPrintf("WARN : Deserver.ClusterGrouparing server %s as failed", server.URL)
						server.State = stateFailed
					} else {
						server.State = stateSuspect
					}
					// remove from slave list
					server.delete(&server.ClusterGroup.slaves)
				}
			}
		}
		// Send alert if state has changed
		if server.PrevState != server.State {
			//if cluster.conf.Verbose {
			server.ClusterGroup.LogPrintf("ALERT : Server %s state changed from %s to %s", server.URL, server.PrevState, server.State)
			//}
			if server.ClusterGroup.conf.MailTo != "" {
				a := alert.Alert{
					From:        server.ClusterGroup.conf.MailFrom,
					To:          server.ClusterGroup.conf.MailTo,
					Type:        server.State,
					Origin:      server.URL,
					Destination: server.ClusterGroup.conf.MailSMTPAddr,
				}
				err = a.Email()
				if err != nil {
					server.ClusterGroup.LogPrint("ERROR: Could not send econf.Mail alert: ", err)
				}
			}
		}
		return
	}
	// Reset FailCount
	/*	if conf.Verbose>0  {
		server.ClusterGroup.LogPrintf("DEBUG: State comparison %b %b %b %d %d %d ", server.State==stateMaster , server.State==stateSlave  , server.State==stateUnconn ,(server.FailCount > 0) ,((server.ClusterGroup.sme.GetHeartbeats() - server.FailSuspectHeartbeat) * conf.MonitoringTicker), conf.FailResetTime) {
	}*/
	if (server.State != stateUnconn && server.State != stateSuspect) && (server.FailCount > 0) && (((server.ClusterGroup.sme.GetHeartbeats() - server.FailSuspectHeartbeat) * server.ClusterGroup.conf.MonitoringTicker) > server.ClusterGroup.conf.FailResetTime) {
		server.FailCount = 0
		server.FailSuspectHeartbeat = 0
	}
	_, err = dbhelper.GetSlaveStatus(server.Conn)
	if err == sql.ErrNoRows {
		// If we reached this stage with a previously failed server, reintroduce
		// it as unconnected server.
		if server.PrevState == stateFailed {
			if server.ClusterGroup.conf.LogLevel > 1 {
				server.ClusterGroup.LogPrintf("DEBUG: State comparison reinitialized failed server %s as unconnected", server.URL)
			}
			server.State = stateUnconn
			server.FailCount = 0
			if server.ClusterGroup.conf.Autorejoin {
				// Check if master exists in topology before rejoining.
				if server.ClusterGroup.master != nil {
					if server.URL != server.ClusterGroup.master.URL {
						server.ClusterGroup.LogPrintf("INFO : Rejoining previously failed server %s", server.URL)
						if server.ClusterGroup.conf.AutorejoinBackupBinlog == true {
							var cmdrun *exec.Cmd
							server.ClusterGroup.LogPrintf("INFO : Backup ahead binlog events of previously failed server %s", server.URL)
							cmdrun = exec.Command(server.ClusterGroup.conf.MariaDBBinaryPath+"/mysqlbinlog", "--read-from-remote-server", "--raw", "--stop-never-slave-server-id=10000", "--user="+server.ClusterGroup.rplUser, "--password="+server.ClusterGroup.rplPass, "--host="+server.Host, "--port="+server.Port, "--result-file="+server.ClusterGroup.conf.WorkingDir+"/"+server.ClusterGroup.cfgGroup+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-", "--start-position="+server.ClusterGroup.master.FailoverMasterLogPos, server.ClusterGroup.master.FailoverMasterLogFile)
							var outrun bytes.Buffer
							cmdrun.Stdout = &outrun

							cmdrunErr := cmdrun.Run()
							if cmdrunErr != nil {
								server.ClusterGroup.LogPrintf("ERROR: Failed to backup binlogs of %s", server.URL)
								server.ClusterGroup.canFlashBack = false
							}
						}
						err = server.rejoin()
						if err != nil {
							server.ClusterGroup.LogPrintf("ERROR: Failed to autojoin previously failed server %s", server.URL)
						}
						server.ClusterGroup.rejoinCond.Send <- true
					}
				}
			}
		} else if server.State != stateMaster {
			if server.ClusterGroup.conf.LogLevel > 1 {
				server.ClusterGroup.LogPrintf("DEBUG: State unconnected set by non-master rule on server %s", server.URL)
			}
			server.State = stateUnconn
		}
		return
	}

	// In case of state change, reintroduce the server in the slave list
	if server.PrevState == stateFailed || server.PrevState == stateUnconn {
		server.State = stateSlave
		server.FailCount = 0
		server.ClusterGroup.slaves = append(server.ClusterGroup.slaves, server)
		if server.ClusterGroup.conf.ReadOnly {
			err = dbhelper.SetReadOnly(server.Conn, true)
			if err != nil {
				server.ClusterGroup.LogPrintf("ERROR: Could not set rejoining slave %s as read-only, %s", server.URL, err)
			}
		}
	}
}

/* Refresh a server object */
func (server *ServerMonitor) refresh() error {

	err := server.Conn.Ping()
	if err != nil {
		return err
	}
	sv, err := dbhelper.GetVariables(server.Conn)
	if err != nil {
		return err
	}
	if sv["EVENT_SCHEDULER"] != "ON" {
		server.EventScheduler = false
	} else {
		server.EventScheduler = true
	}
	server.BinlogPos = gtid.NewList(sv["GTID_BINLOG_POS"])
	server.Strict = sv["GTID_STRICT_MODE"]
	server.LogBin = sv["LOG_BIN"]
	server.ReadOnly = sv["READ_ONLY"]
	server.CurrentGtid = gtid.NewList(sv["GTID_CURRENT_POS"])
	server.SlaveGtid = gtid.NewList(sv["GTID_SLAVE_POS"])
	sid, _ := strconv.ParseUint(sv["SERVER_ID"], 10, 0)
	server.ServerID = uint(sid)
	err = dbhelper.SetDefaultMasterConn(server.Conn, server.ClusterGroup.conf.MasterConn)
	if err != nil {
		return err
	}
	server.EventStatus, err = dbhelper.GetEventStatus(server.Conn)
	if err != nil {
		server.ClusterGroup.LogPrintf("ERROR: Could not get events")
		return err
	}
	su, _ := dbhelper.GetStatus(server.Conn)
	if su["RPL_SEMI_SYNC_MASTER_STATUS"] == "ON" {
		server.SemiSyncMasterStatus = true
	} else {
		server.SemiSyncMasterStatus = false
	}
	if su["RPL_SEMI_SYNC_SLAVE_STATUS"] == "ON" {
		server.SemiSyncSlaveStatus = true
	} else {
		server.SemiSyncSlaveStatus = false
	}

	slaveStatus, err := dbhelper.GetSlaveStatus(server.Conn)
	if err != nil {
		server.UsingGtid = ""
		server.IOThread = "No"
		server.SQLThread = "No"
		server.Delay = sql.NullInt64{Int64: 0, Valid: false}
		server.MasterServerID = 0
		server.MasterHost = ""
		server.IOErrno = 0
		server.IOError = ""
		server.SQLError = ""
		server.SQLErrno = 0
		server.MasterLogFile = ""
		server.MasterLogPos = "0"
		server.MasterHeartbeatPeriod = 0
		server.MasterUseGtid = "No"
	} else {
		server.IOGtid = gtid.NewList(slaveStatus.Gtid_IO_Pos)
		server.UsingGtid = slaveStatus.Using_Gtid
		server.IOThread = slaveStatus.Slave_IO_Running
		server.SQLThread = slaveStatus.Slave_SQL_Running
		server.Delay = slaveStatus.Seconds_Behind_Master
		server.MasterServerID = slaveStatus.Master_Server_Id
		server.MasterHost = slaveStatus.Master_Host
		server.IOErrno = slaveStatus.Last_IO_Errno
		server.IOError = slaveStatus.Last_IO_Error
		server.SQLError = slaveStatus.Last_SQL_Error
		server.SQLErrno = slaveStatus.Last_SQL_Errno
		server.MasterLogFile = slaveStatus.Master_Log_File
		server.MasterUseGtid = slaveStatus.Using_Gtid
		server.MasterHeartbeatPeriod = slaveStatus.Slave_heartbeat_period
		server.MasterLogPos = strconv.FormatUint(uint64(slaveStatus.Read_Master_Log_Pos), 10)
	}

	//monitor haproxy
	if server.ClusterGroup.conf.HaproxyOn {
		// status, err := haproxy.parse(page)
	}

	// Initialize graphite monitoring
	if server.ClusterGroup.conf.GraphiteMetrics {
		graph, err := graphite.NewGraphite(server.ClusterGroup.conf.GraphiteCarbonHost, server.ClusterGroup.conf.GraphiteCarbonPort)

		if err == nil {
			var metrics = make([]graphite.Metric, 5)
			metrics[0] = graphite.NewMetric(fmt.Sprintf("server%d.replication.delay", server.ServerID), fmt.Sprintf("%d", server.Delay.Int64), time.Now().Unix())
			metrics[1] = graphite.NewMetric(fmt.Sprintf("server%d.status.Queries", server.ServerID), su["QUERIES"], time.Now().Unix())
			metrics[2] = graphite.NewMetric(fmt.Sprintf("server%d.status.ThreadsRunning", server.ServerID), su["THREADS_RUNNING"], time.Now().Unix())
			metrics[3] = graphite.NewMetric(fmt.Sprintf("server%d.status.BytesOut", server.ServerID), su["BYTES_SENT"], time.Now().Unix())
			metrics[4] = graphite.NewMetric(fmt.Sprintf("server%d.status.BytesIn", server.ServerID), su["BYTES_RECEIVED"], time.Now().Unix())
			//	metrics[5] = graphite.NewMetric(, time.Now().Unix())
			//	metrics[6] = graphite.NewMetric(, time.Now().Unix())
			//	metrics[7] = graphite.NewMetric(, time.Now().Unix())
			//	metrics[8] = graphite.NewMetric(, time.Now().Unix())
			graph.SendMetrics(metrics)

			graph.Disconnect()
		}
	}
	return nil
}

func (server *ServerMonitor) getMaxscaleInfos(m *maxscale.MaxScale) {

	if server.ClusterGroup.conf.MxsGetInfoMethod == "maxinfo" {

		_, err := m.GetMaxInfoServers("http://" + server.ClusterGroup.conf.MxsHost + ":" + strconv.Itoa(server.ClusterGroup.conf.MxsMaxinfoPort) + "/servers")
		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR: Could not get servers from Maxscale MaxInfo plugin")
		}
		srvport, _ := strconv.Atoi(server.Port)
		server.MxsServerName, server.MxsServerStatus, server.MxsServerConnections = m.GetMaxInfoServer(server.Host, srvport)
	} else {

		_, err := m.ListServers()
		if err != nil {
			server.ClusterGroup.LogPrint("Could not get MaxScale server list")
		} else {
			//		server.ClusterGroup.LogPrint("get MaxScale server list")
			var connections string
			server.MxsServerName, connections, server.MxsServerStatus = m.GetServer(server.IP, server.Port)
			server.MxsServerConnections, _ = strconv.Atoi(connections)
		}
	}

}

/* Check replication health and return status string */
func (server *ServerMonitor) replicationCheck() string {

	if server.ClusterGroup.sme.IsInFailover() || server.State == stateMaster || server.State == stateSuspect || server.State == stateUnconn || server.State == stateFailed {
		return "Master OK"
	}
	if server.Delay.Valid == false && server.ClusterGroup.sme.CanMonitor() {
		if server.SQLThread == "Yes" && server.IOThread == "No" {
			server.State = stateSlaveErr
			return fmt.Sprintf("NOT OK, IO Stopped (%d)", server.IOErrno)
		} else if server.SQLThread == "No" && server.IOThread == "Yes" {
			server.State = stateSlaveErr
			return fmt.Sprintf("NOT OK, SQL Stopped (%d)", server.SQLErrno)
		} else if server.SQLThread == "No" && server.IOThread == "No" {
			server.State = stateSlaveErr
			return "NOT OK, ALL Stopped"
		} else if server.IOThread == "Connecting" {
			server.State = stateSlave
			return "NOT OK, IO Connecting"
		}
		server.State = stateSlave
		return "Running OK"
	}
	if server.Delay.Int64 > 0 {
		if server.Delay.Int64 > server.ClusterGroup.conf.MaxDelay && server.ClusterGroup.conf.RplChecks == true {
			server.State = stateSlaveLate
		} else {
			server.State = stateSlave
		}
		return "Behind master"
	}
	server.State = stateSlave
	return "Running OK"
}

func (sl serverList) checkAllSlavesRunning() bool {
	for _, s := range sl {
		if s.SQLThread != "Yes" || s.IOThread != "Yes" {
			return false
		}
	}
	return true
}

/* Check Consistency parameters on server */
func (server *ServerMonitor) acidTest() bool {
	syncBin, _ := dbhelper.GetVariableByName(server.Conn, "SYNC_BINLOG")
	logFlush, _ := dbhelper.GetVariableByName(server.Conn, "INNODB_FLUSH_LOG_AT_TRX_COMMIT")
	if syncBin == "1" && logFlush == "1" {
		return true
	}
	return false
}

/* Handles write freeze and existing transactions on a server */
func (server *ServerMonitor) freeze() bool {
	err := dbhelper.SetReadOnly(server.Conn, true)
	if err != nil {
		server.ClusterGroup.LogPrintf("WARN : Could not set %s as read-only: %s", server.URL, err)
		return false
	}
	for i := server.ClusterGroup.conf.WaitKill; i > 0; i -= 500 {
		threads := dbhelper.CheckLongRunningWrites(server.Conn, 0)
		if threads == 0 {
			break
		}
		server.ClusterGroup.LogPrintf("INFO : Waiting for %d write threads to complete on %s", threads, server.URL)
		time.Sleep(500 * time.Millisecond)
	}
	maxConn, err = dbhelper.GetVariableByName(server.Conn, "MAX_CONNECTIONS")
	if err != nil {
		server.ClusterGroup.LogPrint("ERROR: Could not get max_connections value on demoted leader")
	} else {
		_, err = server.Conn.Exec("SET GLOBAL max_connections=0")
		if err != nil {
			server.ClusterGroup.LogPrint("ERROR: Could not set max_connections to 0 on demoted leader")
		}
	}
	server.ClusterGroup.LogPrintf("INFO : Terminating all threads on %s", server.URL)
	dbhelper.KillThreads(server.Conn)
	return true
}

func (server *ServerMonitor) readAllRelayLogs() error {
	ss, err := dbhelper.GetSlaveStatus(server.Conn)
	if err != nil {
		return err
	}
	server.ClusterGroup.LogPrintf("INFO : Reading all relay logs on %s", server.URL)
	for ss.Master_Log_File != ss.Relay_Master_Log_File && ss.Read_Master_Log_Pos == ss.Exec_Master_Log_Pos {
		ss, err = dbhelper.GetSlaveStatus(server.Conn)
		if err != nil {
			return err
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

func (server *ServerMonitor) log() {
	server.refresh()
	server.ClusterGroup.LogPrintf("DEBUG: Server:%s Current GTID:%s Slave GTID:%s Binlog Pos:%s", server.URL, server.CurrentGtid.Sprint(), server.SlaveGtid.Sprint(), server.BinlogPos.Sprint())
	return
}

func (server *ServerMonitor) Close() {
	server.Conn.Close()
	return
}

func (server *ServerMonitor) writeState() error {
	server.log()
	f, err := os.Create("/tmp/repmgr.state")
	if err != nil {
		return err
	}
	_, err = f.WriteString(server.BinlogPos.Sprint())
	if err != nil {
		return err
	}
	return nil
}

func (server *ServerMonitor) hasSiblings(sib []*ServerMonitor) bool {
	for _, sl := range sib {
		if server.MasterServerID != sl.MasterServerID {
			return false
		}
	}
	return true
}

func (server *ServerMonitor) delete(sl *serverList) {
	lsm := *sl
	for k, s := range lsm {
		if server.URL == s.URL {
			lsm[k] = lsm[len(lsm)-1]
			lsm[len(lsm)-1] = nil
			lsm = lsm[:len(lsm)-1]
			break
		}
	}
	*sl = lsm
}

func (server *ServerMonitor) rejoin() error {
	if server.ClusterGroup.conf.ReadOnly {
		dbhelper.SetReadOnly(server.Conn, true)
		server.ClusterGroup.LogPrintf("INFO : Setting Read Only on  rejoined %s", server.URL)
	}

	if server.CurrentGtid.Sprint() == server.ClusterGroup.master.FailoverIOGtid.Sprint() {
		server.ClusterGroup.LogPrintf("INFO : Found same current GTID %s on new master %s", server.CurrentGtid.Sprint(), server.ClusterGroup.master.URL)
		cm := "CHANGE MASTER TO master_host='" + server.ClusterGroup.master.IP + "', master_port=" + server.ClusterGroup.master.Port + ", master_user='" + server.ClusterGroup.rplUser + "', master_password='" + server.ClusterGroup.rplPass + "', MASTER_USE_GTID=CURRENT_POS"
		_, err := server.Conn.Exec(cm)
		dbhelper.StartSlave(server.Conn)
		return err
	} else {

		server.ClusterGroup.LogPrintf("INFO : Found different old server GTID %s and elected GTID %s on current master %s", server.CurrentGtid.Sprint(), server.ClusterGroup.master.FailoverIOGtid.Sprint(), server.ClusterGroup.master.URL)

		server.ClusterGroup.LogPrintf("INFO : Not same GTID , no SYNC using semisync, searching for a rejoin method")
		if server.ClusterGroup.canFlashBack == true && server.ClusterGroup.conf.AutorejoinFlashback == true && server.ClusterGroup.conf.AutorejoinBackupBinlog == true {
			// Flashback here
			binlogCmd := exec.Command(server.ClusterGroup.conf.MariaDBBinaryPath+"/mysqlbinlog", "--flashback", "--to-last-log", server.ClusterGroup.conf.WorkingDir+"/"+server.ClusterGroup.cfgGroup+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-"+server.ClusterGroup.master.FailoverMasterLogFile)
			clientCmd := exec.Command(server.ClusterGroup.conf.MariaDBBinaryPath+"/mysql", "--host="+server.Host, "--port="+server.Port, "--user="+server.ClusterGroup.dbUser, "--password="+server.ClusterGroup.dbPass)
			server.ClusterGroup.LogPrintf("FlashBack: %s %s", server.ClusterGroup.conf.MariaDBBinaryPath+"/mysqlbinlog", binlogCmd.Args)
			var err error
			clientCmd.Stdin, err = binlogCmd.StdoutPipe()
			if err != nil {
				server.ClusterGroup.LogPrintf("Error opening pipe: %s", err)
				return err
			}
			if err := binlogCmd.Start(); err != nil {
				server.ClusterGroup.LogPrintf("Error in mysqlbinlog command: %s at %s", err, binlogCmd.Path)
				return err
			}
			if err := clientCmd.Run(); err != nil {
				server.ClusterGroup.LogPrintf("Error starting client:%s at %s", err, clientCmd.Path)
				return err
			}
			server.ClusterGroup.LogPrintf("INFO : SET GLOBAL gtid_slave_pos = \"%s\"", server.ClusterGroup.master.FailoverIOGtid.Sprint())
			_, err = server.Conn.Exec("SET GLOBAL gtid_slave_pos = \"" + server.ClusterGroup.master.FailoverIOGtid.Sprint() + "\"")
			if err != nil {
				return err
			}
			cm := "CHANGE MASTER TO master_host='" + server.ClusterGroup.master.IP + "', master_port=" + server.ClusterGroup.master.Port + ", master_user='" + server.ClusterGroup.rplUser + "', master_password='" + server.ClusterGroup.rplPass + "', MASTER_USE_GTID=SLAVE_POS"
			_, err2 := server.Conn.Exec(cm)
			if err2 != nil {
				return err
			}
			dbhelper.StartSlave(server.Conn)
			if server.FailoverSemiSyncSlaveStatus == true {
				server.ClusterGroup.LogPrintf("INFO : New Master %s was in sync before failover safe flashback, no lost committed events", server.ClusterGroup.master.URL)
			} else {
				t := time.Now()
				backupdir := server.ClusterGroup.conf.WorkingDir + "/crash" + t.Format("20060102150405")
				server.ClusterGroup.LogPrintf("INFO : New Master %s was not sync before failover, unsafe flashback, lost events backing up event to %s ", server.ClusterGroup.master.URL, backupdir)
				os.Mkdir(backupdir, 0777)
				os.Rename(server.ClusterGroup.conf.WorkingDir+"/"+server.ClusterGroup.cfgGroup+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-"+server.ClusterGroup.master.FailoverMasterLogFile, backupdir+"/"+server.ClusterGroup.cfgGroup+"-server"+strconv.FormatUint(uint64(server.ServerID), 10)+"-"+server.ClusterGroup.master.FailoverMasterLogFile)
			}
			return nil
		} else {
			server.ClusterGroup.LogPrintf("INFO : No flashback rejoin : binlog capture failed or wrong version %d , autorejoin-flashback %d ", server.ClusterGroup.canFlashBack, server.ClusterGroup.conf.AutorejoinFlashback)
			if server.ClusterGroup.conf.AutorejoinMysqldump == true {
				cm := "CHANGE MASTER TO master_host='" + server.ClusterGroup.master.IP + "', master_port=" + server.ClusterGroup.master.Port + ", master_user='" + server.ClusterGroup.rplUser + "', master_password='" + server.ClusterGroup.rplPass + "', MASTER_USE_GTID=SLAVE_POS"
				_, err := server.Conn.Exec(cm)
				if err != nil {
					return err
				}
				// dump here
				server.ClusterGroup.RejoinMysqldump(server.ClusterGroup.master, server)
				dbhelper.StartSlave(server.Conn)
				return nil
			}
			server.ClusterGroup.LogPrintf("INFO : No mysqldump rejoin : binlog capture failed or wrong version %d , autorejoin-mysqldump %d ", server.ClusterGroup.canFlashBack, server.ClusterGroup.conf.AutorejoinMysqldump)
			server.ClusterGroup.LogPrintf("INFO : No rejoin method found let me alone")
		}

		//}
	}

	return nil
}
