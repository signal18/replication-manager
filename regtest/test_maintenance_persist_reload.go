// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"os"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestMaintenancePersistReload is the real-cluster regtest for maintenance-mode
// durability (doc/implementation/cluster/MAINTENANCE_PERSISTENCE.md, issue
// #1783): maintenance set through the same entry point the API uses
// (ServerMonitor.SetMaintenance/DelMaintenance) must survive a config reload,
// and an attached proxy's read backend must stay converged across it.
//
// A regtest has no separate repman process to kill and restart, so this
// exercises cl.ReloadConfig directly. That is not a simulation of the restart
// path -- it IS the restart path: ReloadConfig -> InitFromConf ->
// newServerList -> newServerMonitor -> newProxyList is the exact sequence
// server/server.go's config-reload endpoint runs (mycluster.ReloadConfig via
// ReplicationManager.ReloadClusterConfig), and process startup's Init() calls
// the very same InitFromConf. There is only one newProxyList() call site in
// the whole codebase (inside InitFromConf), so this single reload path
// exercises the identical restoration and proxy-reconciliation code a real
// process restart runs.
func (regtest *RegTest) TestMaintenancePersistReload(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(level, format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, level, format, args...)
	}

	type target struct {
		prx      cluster.DatabaseProxy
		typeName string
	}
	// collectTargets is re-evaluated after every reload: newProxyList()
	// rebuilds cl.Proxies with fresh objects, so a target list captured
	// before a reload is stale afterward.
	collectTargets := func() []target {
		var targets []target
		for _, prx := range cl.Proxies {
			if prx == nil || prx.IsIgnored() {
				continue
			}
			if _, ok := backendReadEligible(prx.GetType(), ""); ok {
				targets = append(targets, target{prx: prx, typeName: prx.GetType()})
			}
		}
		return targets
	}
	checkAll := func(targets []target, host, port string, want bool, timeout time.Duration) bool {
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

	slaves := cl.GetSlaves()
	if len(slaves) == 0 {
		logf(config.LvlErr, "TEST maintenance-persist-reload: FAIL cluster has no replica to test against")
		return false
	}
	slave := slaves[0]
	host, port := slave.Host, slave.Port

	// Always leave the cluster clean, pass or fail.
	defer func() {
		if s := cl.GetServerFromURL(host + ":" + port); s != nil && s.IsMaintenance {
			s.DelMaintenance()
			cl.SaveConfigFile()
		}
	}()

	targetsBefore := collectTargets()
	if len(targetsBefore) == 0 {
		logf(config.LvlWarn, "TEST maintenance-persist-reload: no recognized proxy attached (haproxy, proxysql) -- proxy-backend assertions are skipped, only membership persistence and restoration are checked")
	}

	// --- 1. Set maintenance through the same entry point the API uses. ---
	slave.SetMaintenance()

	if !cl.IsInMaintenanceHosts(slave) {
		logf(config.LvlErr, "TEST maintenance-persist-reload: FAIL SetMaintenance did not write durable membership for %s", slave.URL)
		return false
	}

	if len(targetsBefore) > 0 && !checkAll(targetsBefore, host, port, false, 10*time.Second) {
		logf(config.LvlErr, "TEST maintenance-persist-reload: FAIL %s was not drained from the read backend within 10s of entering maintenance", slave.URL)
		return false
	}

	// --- 2. Force and confirm the config artifact landed on disk. ---
	savedFile := cl.Conf.WorkingDir + "/" + cl.Name + "/" + cl.Name + ".toml"
	if changed, err := cl.SaveConfigFile(); err != nil {
		logf(config.LvlErr, "TEST maintenance-persist-reload: FAIL SaveConfigFile error: %s", err)
		return false
	} else if !changed {
		logf(config.LvlErr, "TEST maintenance-persist-reload: FAIL SaveConfigFile reported no change after SetMaintenance")
		return false
	}
	data, err := os.ReadFile(savedFile)
	if err != nil {
		logf(config.LvlErr, "TEST maintenance-persist-reload: FAIL cannot read %s: %s", savedFile, err)
		return false
	}
	if !strings.Contains(string(data), "db-servers-maintenance-hosts") || !strings.Contains(string(data), slave.Host) {
		logf(config.LvlErr, "TEST maintenance-persist-reload: FAIL maintenance membership for %s was NOT persisted to %s", slave.URL, savedFile)
		return false
	}
	logf("TEST", "maintenance-persist-reload: membership for %s persisted to %s -- OK", slave.URL, savedFile)

	// --- 3. Reload: the exact InitFromConf sequence a process restart runs. ---
	cl.ReloadConfig(*cl.Conf)

	rebuilt := cl.GetServerFromURL(host + ":" + port)
	if rebuilt == nil {
		logf(config.LvlErr, "TEST maintenance-persist-reload: FAIL %s:%s not found after reload", host, port)
		return false
	}
	// IsMaintenance is the same field the API's JSON response serializes as
	// isMaintenance -- this is what GET /api/clusters/{c}/servers/{s} would
	// report after a real restart.
	if !rebuilt.IsMaintenance {
		logf(config.LvlErr, "TEST maintenance-persist-reload: FAIL isMaintenance was NOT restored to true for %s after reload", rebuilt.URL)
		return false
	}
	logf("TEST", "maintenance-persist-reload: %s restored isMaintenance=true after reload -- OK", rebuilt.URL)

	targetsAfter := collectTargets()
	if len(targetsAfter) > 0 && !checkAll(targetsAfter, host, port, false, 15*time.Second) {
		logf(config.LvlErr, "TEST maintenance-persist-reload: FAIL %s did not stay excluded from the read backend across reload", rebuilt.URL)
		return false
	}
	if len(targetsAfter) > 0 {
		logf("TEST", "maintenance-persist-reload: %s stayed excluded from the read backend across reload -- OK", rebuilt.URL)
	}

	// --- 4. Clear and reload again: must NOT resurrect maintenance. ---
	rebuilt.DelMaintenance()
	if cl.IsInMaintenanceHosts(rebuilt) {
		logf(config.LvlErr, "TEST maintenance-persist-reload: FAIL DelMaintenance did not clear durable membership for %s", rebuilt.URL)
		return false
	}
	if _, err := cl.SaveConfigFile(); err != nil {
		logf(config.LvlErr, "TEST maintenance-persist-reload: FAIL SaveConfigFile error clearing maintenance: %s", err)
		return false
	}

	cl.ReloadConfig(*cl.Conf)

	final := cl.GetServerFromURL(host + ":" + port)
	if final == nil {
		logf(config.LvlErr, "TEST maintenance-persist-reload: FAIL %s:%s not found after second reload", host, port)
		return false
	}
	if final.IsMaintenance {
		logf(config.LvlErr, "TEST maintenance-persist-reload: FAIL maintenance was resurrected for %s after clearing and reloading", final.URL)
		return false
	}
	logf("TEST", "maintenance-persist-reload: %s did not resurrect maintenance after clearing and reloading -- OK", final.URL)

	targetsFinal := collectTargets()
	if len(targetsFinal) > 0 && !checkAll(targetsFinal, host, port, true, 30*time.Second) {
		logf(config.LvlErr, "TEST maintenance-persist-reload: FAIL %s did not return to the read backend after clearing maintenance and reloading", final.URL)
		return false
	}
	if len(targetsFinal) > 0 {
		logf("TEST", "maintenance-persist-reload: %s returned to the read backend after clearing maintenance and reloading -- OK", final.URL)
	}

	return true
}
