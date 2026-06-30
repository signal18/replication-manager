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
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
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

// Heartbeat call from main cluster loop
func (cluster *Cluster) Heartbeat(wg *sync.WaitGroup) {

	defer wg.Done()
	if cluster.Conf.Arbitration {
		if cluster.RepmanArbitrationRequired {
			// All clusters publish their current state to the arbitrator heartbeat table
			// so the loser-side reconciliation can resolve master for any cluster key.
			if err := cluster.SetArbitratorReport(); err != nil {
				cluster.SetState("WARN0081", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0081"], err), ErrFrom: "ARB"})
			}
		}

		if cluster.RepmanArbitrationRequired && cluster.IsAuthorityCluster {
			// Only the authority cluster drives the repman-wide election.
			// All other clusters have their role driven exclusively by syncClustersFromRepmanRole().
			if !cluster.Conf.IsEligibleForArbitration() {
				if cluster.RoleEstablished {
					// automatic split-brain recovery is subscription-gated
					cluster.SetState("ERR00104", state.State{ErrType: "ERROR", ErrDesc: clusterError["ERR00104"], ErrFrom: "ARB"})
					cluster.RepmanArbitrationRequiredBck = cluster.RepmanArbitrationRequired
					return
				}
				// bootstrap election is allowed for any plan
			}
			if cluster.RepmanArbitrationRequiredBck != cluster.RepmanArbitrationRequired {
				time.Sleep(5 * time.Second)
			}
			i := 1
			for i <= 3 {
				i++
				err := cluster.ArbitratorElection()
				if err != nil {
					cluster.SetState("WARN0082", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0082"], err), ErrFrom: "ARB"})
				} else {
					break // break the loop on success, retry 3 times
				}
			}
		}
		cluster.RepmanArbitrationRequiredBck = cluster.RepmanArbitrationRequired
	}
}

func (cl *Cluster) ForceArbitratorElection() error {
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModHeartBeat, "INFO", "Arbitrator: Forced election requested via API")
	return cl.arbitratorElection()
}

// ReconcileLostArbitrationMaster resolves the winning repman's last-known master for
// this cluster by querying the arbitrator's /winner-master endpoint and runs split-brain
// master protection if it differs from the local master.
//
// authorityCluster is the cluster key that held the repman-wide election.  The arbitrator
// looks up the elected UUID from that key, then returns the master that UUID last reported
// for this cluster — without triggering a new election.
//
// Must not modify repman.Status, cluster.Status, or RoleEstablished; it is strictly
// read/repair.
func (cl *Cluster) ReconcileLostArbitrationMaster(authorityCluster string) {
	if !cl.Conf.Arbitration {
		return
	}
	localMaster := ""
	if cl.GetMaster() != nil {
		localMaster = cl.GetMaster().URL
	}
	if localMaster == "" {
		return
	}

	timeout := time.Duration(time.Duration(cl.Conf.MonitoringTicker*1000-int64(cl.Conf.ArbitrationReadTimout)) * time.Millisecond)
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	// Build the read-only lookup URL.  We append query params to the arbitratorURL
	// base so the Cloud18 uri parameter (if any) is preserved alongside our params.
	// The secret is sent in X-Arbitration-Secret to avoid exposure in access logs.
	base := cl.arbitratorURL("/winner-master")
	sep := "&"
	if !strings.Contains(base, "?") {
		sep = "?"
	}
	reqURL := base + sep +
		"authority_cluster=" + url.QueryEscape(authorityCluster) +
		"&cluster=" + url.QueryEscape(cl.GetName())

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModHeartBeat, config.LvlErr,
			"LoserProtection: could not build arbitrator request for cluster %s: %s", cl.GetName(), err)
		return
	}
	req.Header.Set("X-Arbitration-Secret", cl.Conf.ArbitrationSasSecret)

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModHeartBeat, config.LvlWarn,
			"LoserProtection: could not reach arbitrator for cluster %s: %s", cl.GetName(), err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// No elected winner row for the authority cluster yet — skip protection.
		return
	}

	body, _ := io.ReadAll(resp.Body)

	// winnerMasterResponse mirrors server.WinnerMasterResponse; cluster cannot
	// import server (circular dependency), so the type is redeclared locally.
	type winnerMasterResponse struct {
		WinnerUUID string `json:"winner_uuid"`
		Master     string `json:"master"`
	}
	var r winnerMasterResponse
	if err := json.Unmarshal(body, &r); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModHeartBeat, config.LvlWarn,
			"LoserProtection: arbitrator returned invalid JSON for cluster %s: %s", cl.GetName(), body)
		return
	}
	if r.Master == "" {
		// Winner hasn't reported a master for this cluster yet.
		return
	}
	if r.Master != localMaster {
		if cl.GetServerFromURL(r.Master) == nil {
			cl.SetState("WARN0082", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0082"], fmt.Sprintf("LoserProtection: winner master %s not found in local server list for cluster %s — check host/IP canonicalization between repman peers", r.Master, cl.GetName())), ErrFrom: "ARB"})
			return
		}
		cl.LostArbitration(r.Master)
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModHeartBeat, config.LvlInfo,
			"LoserProtection: cluster %s local master %s differs from winner master %s, applied split-brain protection",
			cl.GetName(), localMaster, r.Master)
	}
}

func (cl *Cluster) ArbitratorElection() error {
	if cl.RepmanArbitrationRequiredBck != cl.RepmanArbitrationRequired {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModHeartBeat, "INFO", "Arbitrator: External check requested")
	} else {
		return nil
	}
	return cl.arbitratorElection()
}

func (cl *Cluster) arbitratorElection() error {
	timeout := time.Duration(time.Duration(cl.Conf.MonitoringTicker*1000-int64(cl.Conf.ArbitrationReadTimout)) * time.Millisecond)
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	url := cl.arbitratorURL("/arbitrator")
	var mst string
	if cl.GetMaster() != nil {
		mst = cl.GetMaster().URL
	}

	var jsonStr = []byte(`{"uuid":"` + cl.runUUID + `","secret":"` + cl.Conf.ArbitrationSasSecret + `","cluster":"` + cl.GetName() + `","master":"` + mst + `","id":` + strconv.Itoa(cl.Conf.ArbitrationSasUniqueId) + `,"status":"` + cl.Status + `","hosts":` + strconv.Itoa(len(cl.GetServers())) + `,"failed":` + strconv.Itoa(cl.CountFailed(cl.GetServers())) + `}`)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModHeartBeat, "ERROR", "Could not create http request to arbitrator: %s", err)
		cl.IsFailedArbitrator = true
		return err
	}
	req.Header.Set("X-Custom-Header", "myvalue")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModHeartBeat, "ERROR", "Could not receive http response from arbitration: %s", err)
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
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModHeartBeat, "ERROR", "Arbitrator sent back invalid JSON, %s", body)
		cl.IsFailedArbitrator = true
		return err
	}

	cl.IsFailedArbitrator = false
	cl.RoleEstablished = true
	if r.Arbitration == "winner" {
		cl.SetActiveStatus(ConstMonitorActif)
		cl.SetState("WARN0083", state.State{ErrType: "WARNING", ErrDesc: clusterError["WARN0083"], ErrFrom: "ARB"})
	} else {
		cl.SetActiveStatus(ConstMonitorStandby)
		cl.SetState("ERR00068", state.State{ErrType: "ERROR", ErrDesc: clusterError["ERR00068"], ErrFrom: "ARB"})
		if cl.GetMaster() != nil {
			mst = cl.GetMaster().URL
			if r.Master != mst {
				cl.LostArbitration(r.Master)
				cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModHeartBeat, "INFO", "Election Lost - Current master %s different from winner master %s, %s is split brain victim. ", mst, r.Master, mst)
			}
		}
	}
	// Notify the owning ReplicationManager so it can update its repman-wide role.
	if cl.onArbitrationResult != nil {
		cl.onArbitrationResult(cl.Status)
	}
	return nil
}
