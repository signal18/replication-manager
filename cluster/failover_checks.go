// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tanji/replication-manager/dbhelper"
	"github.com/tanji/replication-manager/maxscale"
	"github.com/tanji/replication-manager/state"
)

func (cluster *Cluster) CheckFailed() {
	// Don't trigger a failover if a switchover is happening
	if cluster.sme.IsInFailover() {
		cluster.LogPrintf("INFO : In Failover, skip checking failed master")
		return
	}
	if cluster.master != nil {
		if cluster.master.State == stateFailed {
			if cluster.conf.Interactive == false && cluster.isMaxMasterFailedCountReach() == true {
				if cluster.isExternalOk() == false && cluster.isActiveArbitration() == true && cluster.isBeetwenFailoverTimeTooShort() == false && cluster.isMaxClusterFailoverCountReach() == false && cluster.isOneSlaveHeartbeatIncreasing() == false && cluster.isMaxscaleSupectRunning() == false {
					cluster.MasterFailover(true)
					cluster.failoverCond.Send <- true
				} else {
					cluster.sme.AddState("WARN00009", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf("Constraint is blocking for failover isExternalOk %t,isActiveArbitration %t,isBeetwenFailoverTimeTooShort %t ,isMaxClusterFailoverCountReach %t, isOneSlaveHeartbeatIncreasing %t, isMaxscaleSupectRunning %t", cluster.isExternalOk(), cluster.isActiveArbitration(), cluster.isBeetwenFailoverTimeTooShort(), cluster.isMaxClusterFailoverCountReach(), cluster.isOneSlaveHeartbeatIncreasing(), cluster.isMaxscaleSupectRunning()), ErrFrom: "CHECK"})
				}
			} else {
				cluster.sme.AddState("WARN00010", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf("Constraint is blocking state %s, conf.Interactive %t cluster.isMaxMasterFailedCountReach %t", cluster.master.State, cluster.conf.Interactive, cluster.isMaxMasterFailedCountReach()), ErrFrom: "CHECK"})
			}
		}

	} else {
		if cluster.conf.LogLevel > 1 {
			cluster.LogPrintf("WARN : Undiscovered master skip failover check")
		}
	}
}

// isMaxMasterFailedCountReach test tentative to connect
func (cluster *Cluster) isMaxMasterFailedCountReach() bool {
	// no illimited failed count

	if cluster.master.FailCount >= cluster.conf.MaxFail {
		cluster.LogPrintf("DEBUG: Need failover, maximum number of master failure detection reached")
		return true
	}
	return false
}

func (cluster *Cluster) isMaxClusterFailoverCountReach() bool {
	// illimited failed count
	cluster.LogPrintf("CHECK: Failover Counter Reach")
	if cluster.conf.FailLimit == 0 {
		return false
	}
	if cluster.failoverCtr == cluster.conf.FailLimit {
		cluster.LogPrintf("ERROR: Can't failover, maximum number of cluster failover reached")
		return true
	}
	return false
}

func (cluster *Cluster) isBeetwenFailoverTimeTooShort() bool {
	// illimited failed count
	rem := (cluster.failoverTs + cluster.conf.FailTime) - time.Now().Unix()
	if cluster.conf.FailTime == 0 {
		return false
	}
	cluster.LogPrintf("CHECK: Failover Time to short with previous failover")
	if rem > 0 {
		cluster.LogPrintf("ERROR: Can failover, time between failover to short ")
		return true
	}
	return false
}

func (cluster *Cluster) isOneSlaveHeartbeatIncreasing() bool {
	if cluster.conf.CheckFalsePositiveHeartbeat == false {
		return false
	}
	cluster.LogPrintf("CHECK: Failover Slaves heartbeats")

	for _, s := range cluster.slaves {
		relaycheck, _ := cluster.GetMasterFromReplication(s)
		if relaycheck != nil {
			if relaycheck.IsRelay == false {
				status, _ := dbhelper.GetStatusAsInt(s.Conn)
				saveheartbeats := status["SLAVE_RECEIVED_HEARTBEATS"]
				cluster.LogPrintf("SLAVE_RECEIVED_HEARTBEATS %d", saveheartbeats)
				time.Sleep(time.Duration(cluster.conf.CheckFalsePositiveHeartbeatTimeout) * time.Second)
				status2, _ := dbhelper.GetStatusAsInt(s.Conn)
				cluster.LogPrintf("SLAVE_RECEIVED_HEARTBEATS %d", status2["SLAVE_RECEIVED_HEARTBEATS"])
				if status2["SLAVE_RECEIVED_HEARTBEATS"] > saveheartbeats {
					cluster.LogPrintf("ERROR: Can't failover,  slave %s still see the master ", s.DSN)
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
	cluster.LogPrintf("CHECK: Failover Maxscale Master Satus")
	m := maxscale.MaxScale{Host: cluster.conf.MxsHost, Port: cluster.conf.MxsPort, User: cluster.conf.MxsUser, Pass: cluster.conf.MxsPass}
	err := m.Connect()
	if err != nil {
		cluster.LogPrint("ERROR: Could not connect to MaxScale:", err)
		return false
	}
	defer m.Close()
	if cluster.master.MxsServerName == "" {
		cluster.LogPrint("ERROR: MaxScale server name undiscovered")
		return false
	}
	//disable monitoring
	if cluster.conf.MxsMonitor == false {
		var monitor string
		if cluster.conf.MxsGetInfoMethod == "maxinfo" {
			if cluster.conf.LogLevel > 1 {
				cluster.LogPrint("INFO: Getting Maxscale monitor via maxinfo")
			}
			m.GetMaxInfoMonitors("http://" + cluster.conf.MxsHost + ":" + strconv.Itoa(cluster.conf.MxsMaxinfoPort) + "/monitors")
			monitor = m.GetMaxInfoStoppedMonitor()

		} else {
			if cluster.conf.LogLevel > 1 {
				cluster.LogPrint("INFO : Getting Maxscale monitor via maxadmin")
			}
			_, err := m.ListMonitors()
			if err != nil {
				cluster.LogPrint("ERROR: MaxScale client could list monitors monitor:%s", err)
				return false
			}
			monitor = m.GetStoppedMonitor()
		}
		if monitor != "" {
			cmd := "Restart monitor \"" + monitor + "\""
			cluster.LogPrintf("INFO : %s", cmd)
			err = m.RestartMonitor(monitor)
			if err != nil {
				cluster.LogPrint("ERROR: MaxScale client could not startup monitor:%s", err)
				return false
			}
		} else {
			cluster.LogPrint("INFO : MaxScale Monitor not found")
			return false
		}
	}

	time.Sleep(time.Duration(cluster.conf.CheckFalsePositiveMaxscaleTimeout) * time.Second)
	if strings.Contains(cluster.master.MxsServerStatus, "Running") {
		cluster.LogPrintf("ERROR: Can't failover Master still up for Maxscale %s", cluster.master.MxsServerStatus)
		return true
	}
	return false
}

func (cluster *Cluster) isActiveArbitration() bool {

	if cluster.conf.Arbitration == false {
		return true
	}
	cluster.LogPrintf("CHECK: Failover External Arbitration")

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
		cluster.LogPrintf("ERROR: %s", err.Error())
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
		cluster.LogPrintf("ERROR: arbitrator says invalid JSON")
		return false
	}
	if r.Arbitration == "winner" {
		cluster.LogPrintf("INFO :Arbitrator says: winner")
		return true
	}
	cluster.LogPrintf("INFO : Arbitrator says: loser")
	return false
}

func (cluster *Cluster) isExternalOk() bool {
	if cluster.conf.CheckFalsePositiveExternal == false {
		return false
	}
	cluster.LogPrintf("CHECK: Failover External Request")
	if cluster.master == nil {
		return false
	}
	url := "http://" + cluster.master.Host + ":" + strconv.Itoa(cluster.conf.CheckFalsePositiveExternalPort)
	req, err := http.Get(url)
	if err != nil {
		return false
	}
	if req.StatusCode == 200 {
		return true
	}
	return false
}
