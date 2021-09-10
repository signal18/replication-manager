// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

// cluster_split.go
// multi replication-manager heartbeat and arbitrator
package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/signal18/replication-manager/utils/state"
)

//Heartbeat call from main cluster loop
func (cluster *Cluster) Heartbeat(wg *sync.WaitGroup) {

	defer wg.Done()
	if cluster.Conf.Arbitration {
		if cluster.IsSplitBrain {
			err := cluster.SetArbitratorReport()
			if err != nil {
				cluster.SetState("WARN0081", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0081"], err), ErrFrom: "ARB"})
			}
			if cluster.IsSplitBrainBck != cluster.IsSplitBrain {
				time.Sleep(5 * time.Second)
			}
			i := 1
			for i <= 3 {
				i++
				err = cluster.ArbitratorElection()
				if err != nil {
					cluster.SetState("WARN0082", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0082"], err), ErrFrom: "ARB"})
				} else {
					break //break the loop on success retry 3 times
				}
			}
		}
		cluster.IsSplitBrainBck = cluster.IsSplitBrain
	}
}

func (cl *Cluster) ArbitratorElection() error {
	timeout := time.Duration(time.Duration(cl.Conf.MonitoringTicker*1000-int64(cl.Conf.ArbitrationReadTimout)) * time.Millisecond)

	url := "http://" + cl.Conf.ArbitrationSasHosts + "/arbitrator"
	if cl.IsSplitBrainBck != cl.IsSplitBrain {
		cl.LogPrintf("INFO", "Arbitrator: External check requested")
	} else {
		// don't need arbitration if split brain status did not change
		return nil
	}
	var mst string
	if cl.GetMaster() != nil {
		mst = cl.GetMaster().URL
	}

	var jsonStr = []byte(`{"uuid":"` + cl.runUUID + `","secret":"` + cl.Conf.ArbitrationSasSecret + `","cluster":"` + cl.GetName() + `","master":"` + mst + `","id":` + strconv.Itoa(cl.Conf.ArbitrationSasUniqueId) + `,"status":"` + cl.Status + `","hosts":` + strconv.Itoa(len(cl.GetServers())) + `,"failed":` + strconv.Itoa(cl.CountFailed(cl.GetServers())) + `}`)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	if err != nil {
		cl.LogPrintf("ERROR", "Could not create http request to arbitrator: %s", err)
		cl.IsFailedArbitrator = true
		return err
	}
	req.Header.Set("X-Custom-Header", "myvalue")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		cl.LogPrintf("ERROR", "Could not receive http response from arbitration: %s", err)
		cl.IsFailedArbitrator = true
		return err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	type response struct {
		Arbitration string `json:"arbitration"`
		Master      string `json:"master"`
	}
	var r response
	err = json.Unmarshal(body, &r)
	if err != nil {
		cl.LogPrintf("ERROR", "Arbitrator sent back invalid JSON, %s", body)
		cl.IsFailedArbitrator = true
		return err
	}

	cl.IsFailedArbitrator = false
	if r.Arbitration == "winner" {
		cl.SetActiveStatus(ConstMonitorActif)
		cl.SetState("WARN0083", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0083"]), ErrFrom: "ARB"})
	} else {
		cl.SetActiveStatus(ConstMonitorStandby)
		cl.SetState("ERR00068", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00068"]), ErrFrom: "ARB"})
		if cl.GetMaster() != nil {
			mst = cl.GetMaster().URL
			if r.Master != mst {
				cl.LostArbitration(r.Master)
				cl.LogPrintf("INFO", "Election Lost - Current master %s different from winner master %s, %s is split brain victim. ", mst, r.Master, mst)
			}
		}
	}
	return nil
}
