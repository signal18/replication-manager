// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"encoding/json"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// backendReadEligible normalizes a Backend's PrxStatus into a single
// proxy-agnostic "is this backend currently eligible to serve reads"
// boolean, since every proxy type spells its own status differently (HAProxy:
// UP/DRAIN/MAINT/DOWN from "show stat"; ProxySQL: ONLINE/OFFLINE_SOFT/
// OFFLINE_HARD). ok=false when proxyType's vocabulary isn't one this function
// knows how to interpret — the caller should skip assertions against that
// proxy rather than guess at its semantics.
func backendReadEligible(proxyType, prxStatus string) (eligible bool, ok bool) {
	switch proxyType {
	case config.ConstProxyHaproxy:
		return prxStatus == "UP", true
	case config.ConstProxySqlproxy:
		return prxStatus == "ONLINE", true
	default:
		return false, false
	}
}

// proxyBackendsView round-trips just the two fields this test needs out of
// an arbitrary DatabaseProxy. BackendsRead/BackendsWrite live on the Proxy
// struct every concrete proxy type embeds, but DatabaseProxy (the interface
// cluster.Proxies elements are stored as) doesn't expose them directly, and
// the dynamic type behind the interface varies per proxy type (*HaproxyProxy,
// *ProxySQLProxy, ...) so a single type assertion can't reach the embedded
// field either. Every concrete type's json:"backendsRead" tag is identical,
// so marshal/unmarshal reaches it generically without a type switch.
type proxyBackendsView struct {
	BackendsRead []cluster.Backend `json:"backendsRead"`
}

func getProxyBackendsRead(prx cluster.DatabaseProxy) ([]cluster.Backend, error) {
	raw, err := json.Marshal(prx)
	if err != nil {
		return nil, err
	}
	var view proxyBackendsView
	if err := json.Unmarshal(raw, &view); err != nil {
		return nil, err
	}
	return view.BackendsRead, nil
}

func findReadBackend(backends []cluster.Backend, host, port string) (cluster.Backend, bool) {
	for _, b := range backends {
		if b.Host == host && b.Port == port {
			return b, true
		}
	}
	return cluster.Backend{}, false
}

func proxyReadBackendWaitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(1 * time.Second)
	}
	return cond()
}

// TestProxyReadBackendReconciliation is a single, proxy-type-agnostic
// regtest covering three read-backend reconciliation paths that must keep
// working whether or not a proxy supports the Phase 1 dynamic add/remove
// feature (issue #1724, cluster/prx_haproxy.go's supportsDynamicServers,
// HAProxy >= 2.6 only): these three paths flip the PrxStatus of an
// already-bootstrapped backend row, they never add or remove one, so they
// must behave identically on an old HAProxy (2.4/2.5) that can't add/remove
// servers at runtime, a new one that can, or a different proxy type
// entirely (ProxySQL implements the same read-on-master-no-slave fallback,
// cluster/prx_proxysql.go line ~509, using the exact same
// Configurator.HasProxyReadLeaderNoSlave()/HasNoValidSlave()/
// HasAvailableReader() decision HAProxy's masterShouldBeReader() does).
//
//  1. Stop slave: STOP SLAVE on a replica must drain it from every attached
//     proxy's read backend.
//  2. Read on master when no more slave: with every replica stopped (no
//     valid reader left), the master must become eligible in the read
//     backend (proxy-servers-read-on-master-no-slave, default true).
//  3. Maintenance: explicit maintenance on a replica must drain it from the
//     read backend independently of its replication state, and clearing it
//     must restore it.
//
// Runs against whichever attached proxies this file knows how to interpret
// (HAProxy, ProxySQL) via backendReadEligible; any other attached proxy type
// is left alone rather than guessed at. Fails with a specific reason rather
// than a false pass if no recognized proxy is attached or there's no
// replica to test against — this framework has no "skip" result.
func (regtest *RegTest) TestProxyReadBackendReconciliation(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(level, format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModProxy, level, format, args...)
	}

	type target struct {
		prx      cluster.DatabaseProxy
		typeName string
	}
	var targets []target
	for _, prx := range cl.Proxies {
		if prx == nil || prx.IsIgnored() {
			continue
		}
		// Status value is irrelevant here - only ok (is prx.GetType() one this
		// file knows how to interpret) matters for this probe.
		if _, ok := backendReadEligible(prx.GetType(), ""); ok {
			targets = append(targets, target{prx: prx, typeName: prx.GetType()})
		}
	}
	if len(targets) == 0 {
		logf(config.LvlErr, "TEST proxy-read-backend-reconciliation: FAIL no attached proxy of a recognized type (haproxy, proxysql)")
		return false
	}

	slaves := cl.GetSlaves()
	if len(slaves) == 0 {
		logf(config.LvlErr, "TEST proxy-read-backend-reconciliation: FAIL cluster has no replica to test against")
		return false
	}
	master := cl.GetMaster()
	if master == nil {
		logf(config.LvlErr, "TEST proxy-read-backend-reconciliation: FAIL cluster has no master")
		return false
	}

	// checkAll polls every recognized target until either every one reports
	// host:port as eligible (want=true) or every one reports it as absent/
	// ineligible (want=false), or timeout elapses. An unrecognized proxy
	// (backendReadEligible's ok=false) is only reachable here through a
	// target already filtered into `targets` above, so ok is always true for
	// the calls inside this loop.
	checkAll := func(host, port string, want bool, timeout time.Duration) bool {
		return proxyReadBackendWaitFor(timeout, func() bool {
			for _, t := range targets {
				backends, err := getProxyBackendsRead(t.prx)
				if err != nil {
					return false
				}
				bke, found := findReadBackend(backends, host, port)
				if want {
					if !found {
						return false
					}
					if eligible, _ := backendReadEligible(t.typeName, bke.PrxStatus); !eligible {
						return false
					}
				} else if found {
					if eligible, _ := backendReadEligible(t.typeName, bke.PrxStatus); eligible {
						return false
					}
				}
			}
			return true
		})
	}

	// Pick a replica already eligible in every target as the baseline for
	// phases 1 and 3 — starting from an already-broken/excluded replica would
	// make a later "did it get drained" assertion meaningless.
	var slave *cluster.ServerMonitor
	for _, s := range slaves {
		if checkAll(s.Host, s.Port, true, 1*time.Second) {
			slave = s
			break
		}
	}
	if slave == nil {
		logf(config.LvlErr, "TEST proxy-read-backend-reconciliation: FAIL no replica is currently eligible in every recognized proxy's read backend to test against")
		return false
	}

	// --- Phase 1: stop slave -> drained everywhere, restart -> restored. ---
	if _, err := slave.StopSlave(); err != nil {
		logf(config.LvlErr, "TEST proxy-read-backend-reconciliation: FAIL could not STOP SLAVE on %s: %s", slave.URL, err)
		return false
	}
	stopOK := checkAll(slave.Host, slave.Port, false, 30*time.Second)
	if _, err := slave.StartSlave(); err != nil {
		logf(config.LvlErr, "TEST proxy-read-backend-reconciliation: FAIL could not START SLAVE on %s during cleanup: %s", slave.URL, err)
		return false
	}
	if !stopOK {
		logf(config.LvlErr, "TEST proxy-read-backend-reconciliation: FAIL %s was not drained from the read backend within 30s of STOP SLAVE", slave.URL)
		return false
	}
	logf("TEST", "proxy-read-backend-reconciliation: %s drained from the read backend after STOP SLAVE — OK", slave.URL)

	if !checkAll(slave.Host, slave.Port, true, 30*time.Second) {
		logf(config.LvlErr, "TEST proxy-read-backend-reconciliation: FAIL %s did not return to the read backend within 30s of START SLAVE", slave.URL)
		return false
	}
	logf("TEST", "proxy-read-backend-reconciliation: %s restored to the read backend after START SLAVE — OK", slave.URL)

	// --- Phase 2: no more valid slave -> master becomes a reader. ---
	originalReadOnMasterNoSlave := cl.Conf.PRXServersReadOnMasterNoSlave
	if !cl.Configurator.HasProxyReadLeaderNoSlave() {
		cl.SwitchProxyServersReadOnMasterNoSlave()
	}
	defer func() {
		if cl.Conf.PRXServersReadOnMasterNoSlave != originalReadOnMasterNoSlave {
			cl.SwitchProxyServersReadOnMasterNoSlave()
		}
	}()

	if err := cl.StopSlaves(); err != nil {
		logf(config.LvlErr, "TEST proxy-read-backend-reconciliation: FAIL could not stop all replicas: %s", err)
		return false
	}
	noSlaveOK := checkAll(master.Host, master.Port, true, 30*time.Second)
	if err := cl.StartSlaves(); err != nil {
		logf(config.LvlErr, "TEST proxy-read-backend-reconciliation: FAIL could not restart all replicas during cleanup: %s", err)
		return false
	}
	if !noSlaveOK {
		logf(config.LvlErr, "TEST proxy-read-backend-reconciliation: FAIL master %s did not become an eligible reader within 30s of every replica stopping", master.URL)
		return false
	}
	logf("TEST", "proxy-read-backend-reconciliation: master %s became an eligible reader once every replica stopped — OK", master.URL)

	// Secondary, best-effort check: once every replica is healthy again the
	// master should stop being forced as a reader. Logged, not fatal — the
	// three behaviors the user asked to cover are the ones above and below.
	if !checkAll(master.Host, master.Port, false, 30*time.Second) {
		logf(config.LvlWarn, "TEST proxy-read-backend-reconciliation: master %s was still an eligible reader 30s after every replica restarted (non-fatal, not one of the covered behaviors)", master.URL)
	}

	// --- Phase 3: maintenance -> drained, cleared -> restored. ---
	slave.SetMaintenance()
	maintOK := checkAll(slave.Host, slave.Port, false, 10*time.Second)
	slave.DelMaintenance()
	if !maintOK {
		logf(config.LvlErr, "TEST proxy-read-backend-reconciliation: FAIL %s was not drained from the read backend within 10s of entering maintenance", slave.URL)
		return false
	}
	logf("TEST", "proxy-read-backend-reconciliation: %s drained from the read backend on maintenance — OK", slave.URL)

	if !checkAll(slave.Host, slave.Port, true, 30*time.Second) {
		logf(config.LvlErr, "TEST proxy-read-backend-reconciliation: FAIL %s did not return to the read backend within 30s of clearing maintenance", slave.URL)
		return false
	}
	logf("TEST", "proxy-read-backend-reconciliation: %s restored to the read backend after clearing maintenance — OK", slave.URL)

	return true
}
