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
			if !cluster.IsSplitBrainBck {
				// Remember when THIS split brain began (calm->split edge). Used to
				// guard the resolve-time peer-crash fetch (accept only a crash from
				// this split, not a stale failoverHistory entry) and a useful
				// diagnostic on its own.
				cluster.SplitBrainStartTs = time.Now().Unix()
			}
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
			// Anti-blip grace at split ENTRY, restored from 2.x/3.1.29 (the
			// versions that never flapped; removed 2026-07-01 by 06f379b25):
			// sleep once before the first election so a transient peer blip
			// heals before any authority question is even asked. Redundant
			// with the arbitrator's contest window on upgraded arbitrators,
			// but protects mixed-version deployments at the cost of one line.
			if cluster.IsSplitBrainBck != cluster.IsSplitBrain {
				time.Sleep(5 * time.Second)
			}
			err = cluster.arbitratorElection()
			if err != nil {
				cluster.SetState("WARN0082", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0082"], err), ErrFrom: "ARB"})
			}
		} else if cluster.IsSplitBrainBck {
			// Split just RESOLVED (was split, now not): release any read-lock
			// freeze so the old master can accept the writes recovery needs
			// (rejoin as slave, flashback, or reseed).
			if m := cluster.GetMaster(); m != nil && m.IsFrozen() {
				m.UnfreezeReadLock()
			}
			// Prefetch the peer's election verdict NOW, before the first
			// post-resolve tick processes any returning server: the Failed->up
			// rejoin trigger fires within seconds of resolve and needs the
			// materialized crash (URL = old master, ElectedMasterURL = winner)
			// to act. In the 2026-07-14 23:00 run RejoinMaster entered with
			// master nil and NO crash — topology's peer-designation fetched it
			// 3s too late — so it did nothing and the old master was left as a
			// second read-write master.
			if _, err := cluster.fetchMasterFromPeer(); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModArbitration, config.LvlDbg, "No peer elected master to prefetch at split resolve: %s", err)
			}
		}
		// Recovery / fleet-unlatch: IsFailedArbitrator (-> ERR00055 "Arbitrator
		// unreachable", which also fails the ArbitratorAlive failover check and makes
		// TopologyDiscover treat the cluster as a permanent minority) is otherwise only
		// cleared by a SUCCESSFUL election, which runs only while IsSplitBrain. A cluster
		// whose arbitrator link was cut but that never entered split brain (e.g. the sim's
		// fleet-wide SimulateArbitratorFailureAll on every cluster) would stay latched
		// forever after the cut expires. Once we are NOT split and no arbitrator cut is
		// simulated, clear the latch so the cluster recovers on its own.
		if !cluster.IsSplitBrain && !cluster.IsArbitratorFailureSimulated() && cluster.IsFailedArbitrator {
			cluster.IsFailedArbitrator = false
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

// arbitratorMinorityFailSafe is invoked when this node is in split brain but
// cannot reach the arbitrator to confirm authority — i.e. it is (or must assume
// it is) the minority. Two guarantees:
//
//  1. DATA SAFETY: force the master read-only. When the partition heals, the
//     majority will have elected a master and this node's old master must rejoin
//     it as a slave — any write accepted here would diverge and block the rejoin.
//     The existing SetReadWrite guard only refuses to *enable* writes; an
//     already-writable master must be actively demoted to read-only.
//  2. CONTROL: yield status to standby so this repman stops driving the cluster
//     (in the 2-repman / 1-cluster arbitration setup, status decides who drives).
//
// Idempotent — the read-only set is a no-op once already read-only, and the
// status change only logs/acts on an actual Active->Standby transition.
func (cl *Cluster) arbitratorMinorityFailSafe(reason string) {
	if m := cl.GetMaster(); m != nil && !m.IsReadOnly() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlInfo, "Minority fail-safe: %s during split brain — setting master %s read-only so it can rejoin the majority", reason, m.URL)
		if logs, err := m.SetReadOnly(); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlErr, "Minority fail-safe: could not set master %s read-only: %s (%s)", m.URL, err, logs)
		}
	}
	// True freeze (opt-in): read_only does not bind repman's own SUPER
	// connection and MariaDB has no super_read_only, so a global read lock is
	// the only way to guarantee the minority master cannot diverge (not even
	// from repman's heartbeat). Held on a dedicated connection, released when
	// the split resolves (ArbitratorHandler) or before recovery writes.
	if cl.Conf.ArbitrationMinorityFreeze {
		if m := cl.GetMaster(); m != nil && !m.IsFrozen() {
			if err := m.FreezeWithReadLock(); err != nil {
				cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlErr, "Minority fail-safe: could not freeze master %s: %s", m.URL, err)
			}
		}
	}
	if cl.Status == ConstMonitorActif {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlInfo, "Minority fail-safe: yielding cluster %s to standby", cl.GetName())
		cl.SetActiveStatus(ConstMonitorStandby)
	}
	// Track & re-evaluate (enforced every tick until resolve). GATED: only when we are
	// genuinely the isolated minority of a split — IsSplitBrain (durable, set by
	// ArbitratorHandler) AND we have already yielded to Standby AND master-slave target.
	// Without this gate the API path (ForceArbitratorElection) could reach this failsafe
	// on a HEALTHY cluster when the arbitrator is momentarily unreachable and wrongly
	// reset it. The minority cannot trust its own view: keep the master it KNOWS marked
	// as the frozen OLD master (StandAlone), enforce Suspect on every OTHER reachable
	// node (it cannot confirm their real role while isolated), then nil the master
	// pointer so the cluster REDISCOVERS the real topology at resolve (the peer-fetch
	// relearns the winner). Suspect is a state srv.go already demotes (Suspect ->
	// StandAlone once a master is re-established), so no state-machine change is needed.
	// Idempotent: SetState is a no-op when already in the target state.
	if cl.IsSplitBrain && cl.Status == ConstMonitorStandby && cl.GetTopologyFromConf() == config.TopoMasterSlave {
		if master := cl.GetMaster(); master != nil {
			if master.State != stateUnconn {
				master.SetState(stateUnconn)
			}
			// The SLAVES stay in the equation — do NOT hold them Suspect (2026-07-14
			// evening fix, was added same morning by 7fdd97a29). Suspecting them
			// emptied cluster.slaves, which killed the whole master-autodetect block
			// (len(slaves)>0) INCLUDING FailedMasterDiscovery — the normal lifecycle
			// that keeps a failed old master identified as "the master, Failed" via
			// the slaves' master_host, so its Failed->up edge drives the rejoin. The
			// minority is already prevented from inferring/promoting mid-split by the
			// !IsFailedArbitrator gates on that block; the slaves' live states and
			// replication config are needed intact for the reconciliation at resolve.
			cl.master = nil
		}
	}
}

// getClusterTestCredentials returns a local user holding the cluster-test grant,
// used to authenticate to the arbitration peer. Cluster-scoped — reads this
// cluster's own APIUsers. PREFERS "admin": cl.APIUsers is a map (random iteration
// order), and service accounts like sysops-cloud18 (git-sync) also carry the
// grant but lock out after failed auth — so a random pick flaps the peer login
// between working (admin) and 401 (locked service account). Deterministic admin
// preference removes the flap.
func (cl *Cluster) getClusterTestCredentials() (string, string, bool) {
	var fbUser, fbPass string
	for _, u := range cl.APIUsers {
		if u.Grants == nil || !u.Grants[config.GrantClusterTest] || u.Password == "" {
			continue
		}
		if u.User == "admin" {
			return u.User, u.Password, true
		}
		if fbUser == "" {
			fbUser, fbPass = u.User, u.Password
		}
	}
	if fbUser != "" {
		return fbUser, fbPass, true
	}
	return "", "", false
}

// fetchMasterFromPeer asks the arbitration peer (the split-brain winner) who the
// real elected master is, from the peer's crash record (crash.ElectedMasterURL).
// CLUSTER-SCOPED and self-contained: it uses THIS cluster's own peer host
// (cl.Conf.ArbitrationPeerHosts) and its own cluster-test credential — it never
// goes through the server layer, so a single-repman / no-auto-failover cluster
// simply has no peer and returns "" (the caller falls back to its own local
// crash/topology). A node that sat out a split-brain as the minority can't trust
// its own frozen topology nor the arbitrator (unreachable while isolated, stale
// after) — but once the split resolves its peer link is back and the peer holds
// the authoritative election result.
func (cl *Cluster) fetchMasterFromPeer() (string, error) {
	if cl.Conf.ArbitrationPeerHosts == "" {
		return "", nil // no peer (single repman) — caller falls back to local
	}
	user, pass, ok := cl.getClusterTestCredentials()
	if !ok {
		return "", errors.New("no local cluster-test grant user to authenticate to peer")
	}
	peer := ensureScheme(strings.TrimSpace(strings.Split(cl.Conf.ArbitrationPeerHosts, ",")[0]))
	client := &http.Client{Timeout: 5 * time.Second}

	loginBody, _ := json.Marshal(struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}{user, pass})
	resp, err := client.Post(peer+"/api/login", "application/json", bytes.NewBuffer(loginBody))
	if err != nil {
		return "", err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("peer login rejected (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.Token == "" {
		return "", errors.New("peer login returned no token")
	}

	// Read the peer's DURABLE failoverHistory, not the live crash list: the live
	// cluster.Crashes/​topology/crashes is purged once the peer thinks the DBs are
	// up, exactly when a record-less minority needs it, whereas failoverHistory
	// (StoreLastN) keeps the record with all anchors.
	req, err := http.NewRequest("GET", peer+"/api/clusters/"+cl.Name, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	resp2, err := client.Do(req)
	if err != nil {
		return "", err
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return "", fmt.Errorf("peer cluster (%d): %s", resp2.StatusCode, strings.TrimSpace(string(body2)))
	}
	var payload struct {
		FailoverHistory []Crash `json:"failoverHistory"`
	}
	if err := json.Unmarshal(body2, &payload); err != nil {
		return "", fmt.Errorf("peer failoverHistory parse: %w", err)
	}
	if len(payload.FailoverHistory) == 0 {
		return "", nil // no crash on peer — caller falls back to local
	}
	last := payload.FailoverHistory[len(payload.FailoverHistory)-1]
	// GUARD: only accept a crash produced by THIS split brain, never a stale
	// failoverHistory entry from a previous run.
	if cl.SplitBrainStartTs > 0 && last.UnixTimestamp < cl.SplitBrainStartTs {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlInfo, "Peer last crash ts %d predates this split brain (started %d) — ignoring as stale", last.UnixTimestamp, cl.SplitBrainStartTs)
		return "", nil
	}
	if last.ElectedMasterURL == "" {
		return "", nil
	}
	// Materialize the peer's crash locally so getCrashFromJoiner returns it and
	// the diverged old master's rejoin has the anchor. Drop the peer-local delta
	// paths — this node captures its own delta from the old master.
	if cl.getCrashFromJoiner(last.URL) == nil {
		mat := last
		mat.DeltaArchive, mat.DeltaDecoded, mat.DeltaFlashbackDecoded = "", "", ""
		cl.Crashes = append(cl.Crashes, &mat)
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModArbitration, config.LvlInfo, "Materialized peer crash for %s (elected master %s) from peer failoverHistory", mat.URL, mat.ElectedMasterURL)
	}
	return last.ElectedMasterURL, nil
}

func (cl *Cluster) arbitratorElection() error {
	// Split-brain simulator: this node's arbitrator link is cut — it cannot
	// confirm authority, so it must fail-safe to standby (yield), not freeze
	// Active — freezing would leave it Active while the majority also promotes,
	// i.e. dual-active. A minority that can't reach the arbitrator must step down.
	if cl.IsArbitratorFailureSimulated() {
		cl.IsFailedArbitrator = true
		cl.SetState("ERR00022", state.State{ErrType: config.LvlErr, ErrDesc: clusterError["ERR00022"], ErrFrom: "CHECK"})
		cl.arbitratorMinorityFailSafe("simulated arbitrator link cut")
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
		// Real arbitrator loss during split brain: can't confirm authority → yield.
		cl.arbitratorMinorityFailSafe("arbitrator unreachable")
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
