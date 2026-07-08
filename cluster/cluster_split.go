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
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

func ensureScheme(host string) string {
	if strings.HasPrefix(host, "https://") || strings.HasPrefix(host, "http://") {
		return host
	}
	return "http://" + host
}

func (cl *Cluster) arbitratorURL(path string) string {
	base := ensureScheme(cl.Conf.ArbitrationSasHosts) + path
	uri := cl.Conf.Cloud18Domain + "." + cl.Conf.Cloud18SubDomain + "." + cl.Conf.Cloud18SubDomainZone
	if uri != ".." {
		return base + "?uri=" + uri
	}
	return base
}

// ArbitratorHandler communicates with the external arbitrator service during split brain.
// It reports cluster state and triggers elections. Runs after TopologyDiscover to avoid
// data races on Servers.
func (cluster *Cluster) ArbitratorHandler() {
	// The split-brain simulator (cluster_splitbrain_simulator.go) does NOT force split brain — it only severs
	// links; split brain must emerge naturally so the real detection path is
	// exercised. Keep the standing warning visible while any cut is live.
	cluster.AssertSplitBrainSimulationState()
	if cluster.Conf.Arbitration {
		if cluster.IsSplitBrain {
			if !cluster.Conf.IsEligibleForArbitration() {
				cluster.SetState("ERR00104", state.State{ErrType: "ERROR", ErrDesc: clusterError["ERR00104"], ErrFrom: "ARB"})
				cluster.IsSplitBrainBck = cluster.IsSplitBrain
				return
			}
			// Per-tick chatter: elections run every tick while split brain
			// persists (the winner's arbitrator row needs refreshing — see
			// dbhelper.WriteHeartbeat), so keep this at debug level.
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModArbitration, config.LvlDbg, "ArbitratorHandler: IsSplitBrainBck=%t IsSplitBrain=%t hosts=%d", cluster.IsSplitBrainBck, cluster.IsSplitBrain, len(cluster.GetServers()))
			err := cluster.SetArbitratorReport()
			if err != nil {
				cluster.SetState("WARN0081", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0081"], err), ErrFrom: "ARB"})
			}
			err = cluster.arbitratorElection()
			if err != nil {
				cluster.SetState("WARN0082", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0082"], err), ErrFrom: "ARB"})
			}
		}
		cluster.IsSplitBrainBck = cluster.IsSplitBrain
	}
}

func (cl *Cluster) ForceArbitratorElection() error {
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlInfo, "Arbitrator: Forced election requested via API")
	return cl.arbitratorElection()
}

func (cl *Cluster) ArbitratorElection() error {
	if cl.IsSplitBrainBck != cl.IsSplitBrain {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlInfo, "Arbitrator: External check requested (IsSplitBrainBck=%t IsSplitBrain=%t hosts=%d)", cl.IsSplitBrainBck, cl.IsSplitBrain, len(cl.GetServers()))
	} else {
		return nil
	}
	return cl.arbitratorElection()
}

func (cl *Cluster) arbitratorElection() error {
	// Split-brain simulator: this node's arbitrator link is cut — it cannot
	// confirm authority, so it must fail-safe (stay/go standby).
	if cl.IsArbitratorFailureSimulated() {
		cl.IsFailedArbitrator = true
		cl.SetState("ERR00022", state.State{ErrType: config.LvlErr, ErrDesc: clusterError["ERR00022"], ErrFrom: "CHECK"})
		return errors.New("split-brain simulation: arbitrator link down")
	}
	timeout := time.Duration(time.Duration(cl.Conf.MonitoringTicker*1000-int64(cl.Conf.ArbitrationReadTimout)) * time.Millisecond)

	url := cl.arbitratorURL("/arbitrator")
	var mst string
	if cl.GetMaster() != nil {
		mst = cl.GetMaster().URL
	}

	hosts := len(cl.GetServers())
	failed := cl.CountFailed(cl.GetServers())
	var jsonStr = []byte(`{"uuid":"` + cl.runUUID + `","secret":"` + cl.Conf.ArbitrationSasSecret + `","cluster":"` + cl.GetName() + `","master":"` + mst + `","id":` + strconv.Itoa(cl.Conf.ArbitrationSasUniqueId) + `,"status":"` + cl.Status + `","hosts":` + strconv.Itoa(hosts) + `,"failed":` + strconv.Itoa(failed) + `}`)
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlDbg, "arbitratorElection: sending hosts=%d failed=%d cluster=%s", hosts, failed, cl.GetName())
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlErr, "Could not create http request to arbitrator: %s", err)
		cl.IsFailedArbitrator = true
		return err
	}
	req.Header.Set("X-Custom-Header", "myvalue")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlErr, "Could not receive http response from arbitration: %s", err)
		cl.IsFailedArbitrator = true
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	type response struct {
		Arbitration string `json:"arbitration"`
		Master      string `json:"master"`
	}
	var r response
	err = json.Unmarshal(body, &r)
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlErr, "Arbitrator sent back invalid JSON, %s", body)
		cl.IsFailedArbitrator = true
		return err
	}

	cl.IsFailedArbitrator = false
	// Per-tick RECEIVE trace: the arbitrator's verdict every tick (debug only),
	// so a flapping winner/looser reply is visible without a status transition.
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlDbg, "arbitratorElection: reply arbitration=%s winner-master=%s (my status=%s)", r.Arbitration, r.Master, cl.Status)
	// Elections repeat on every tick while split brain persists, so log the
	// outcome only on an actual status transition — the state machine keeps
	// WARN0083/ERR00068 visible in between.
	if r.Arbitration == "winner" {
		if cl.Status != ConstMonitorActif {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlInfo, "Arbitrator election won for cluster %s, switching to active", cl.GetName())
		}
		cl.SetActiveStatus(ConstMonitorActif)
		cl.SetState("WARN0083", state.State{ErrType: "WARNING", ErrDesc: clusterError["WARN0083"], ErrFrom: "ARB"})
	} else {
		if cl.Status != ConstMonitorStandby {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlInfo, "Arbitrator election lost for cluster %s, switching to standby", cl.GetName())
		}
		cl.SetActiveStatus(ConstMonitorStandby)
		cl.SetState("ERR00068", state.State{ErrType: "ERROR", ErrDesc: clusterError["ERR00068"], ErrFrom: "ARB"})
		if cl.GetMaster() != nil {
			mst = cl.GetMaster().URL
			if r.Master != mst {
				cl.LostArbitration(r.Master)
				cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlInfo, "Election lost - current master %s differs from winner master %s, %s is split brain victim", mst, r.Master, mst)
			}
		}
	}
	return nil
}
