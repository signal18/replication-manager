// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/haproxy"
	"github.com/signal18/replication-manager/utils/version"
)

// TestHaproxyRuntimeAPIDynamicServerLifecycle exercises issue #1724 Phase 1
// (dynamic HAProxy read-backend server lifecycle) against the cluster's
// real, live HAProxy proxy and process.
//
// Requires haproxy-mode = "runtimeapi" and HAProxy >= 2.6 already attached;
// enables haproxy-api-bootstrap-servers itself for the run and restores the
// prior value after (save/restore pattern from TestGraphiteMetricsQueueBound).
// Fails with a specific reason rather than a false pass if those
// prerequisites aren't met — this framework has no "skip" result.
//
// Both halves poll for the cluster's own background monitor loop to react
// (this test never calls Refresh() itself):
//
//  1. Add path: drain and delete a replica's read-backend entry (one
//     currently UP, not just slaves[0]) directly via the Runtime API, then
//     poll until it reappears AND reaches UP — not merely present, which
//     would miss a row stuck in MAINT/DRAIN.
//  2. Remove path: add a synthetic "ghost" server via the Runtime API, then
//     poll until it's drained and removed.
//
// Only a replica's read-backend membership is touched, never the write path
// or master.
func (regtest *RegTest) TestHaproxyRuntimeAPIDynamicServerLifecycle(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(level, format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModHAProxy, level, format, args...)
	}

	var proxyHost, proxyPort string
	haproxyAttached := false
	for _, pri := range cl.Proxies {
		if pri.GetType() == config.ConstProxyHaproxy {
			proxyHost = pri.GetHost()
			proxyPort = pri.GetPort()
			haproxyAttached = true
			break
		}
	}
	if !haproxyAttached {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL no HAProxy proxy attached to this cluster (requires haproxy=true)")
		return false
	}

	if cl.Conf.HaproxyMode != "runtimeapi" {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL cluster haproxy-mode=%q, this feature requires \"runtimeapi\"", cl.Conf.HaproxyMode)
		return false
	}

	slaves := cl.GetSlaves()
	if len(slaves) == 0 {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL cluster has no replica to test the read-backend lifecycle against")
		return false
	}
	readBackend := cl.Conf.HaproxyAPIReadBackend

	haRuntime := haproxy.Runtime{Host: proxyHost, Port: proxyPort}

	versionOut, err := haRuntime.ApiCmd("show version")
	if err != nil {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL could not reach the HAProxy Runtime API at %s:%s : %s", proxyHost, proxyPort, err)
		return false
	}
	haVersion, _ := version.NewVersionFromString("haproxy", versionOut)
	if !haVersion.GreaterEqual("2.6") {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL HAProxy version %q is below the 2.6 floor this feature requires", strings.TrimSpace(versionOut))
		return false
	}

	// slaves[0] may be a replica the read backend intentionally excludes or
	// hasn't promoted (maintenance, DRAIN, DOWN, ignored); pick one that's
	// currently UP so the later "did it come back UP" assertion reflects the
	// lifecycle feature working, not a replica that was never expected to
	// serve traffic in the first place.
	initialStat, err := haRuntime.ApiCmd("show stat")
	if err != nil {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL could not read read backend %s state: %s", readBackend, err)
		return false
	}
	var slave *cluster.ServerMonitor
	for _, s := range slaves {
		if status, ok := haproxyStatServerStatus(initialStat, readBackend, s.Id); ok && status == "UP" {
			slave = s
			break
		}
	}
	if slave == nil {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL no replica is currently UP in read backend %s to test the add path against", readBackend)
		return false
	}

	// Save and restore: this cluster keeps running other scenarios after
	// this one.
	originalBootstrap := cl.Conf.HaproxyAPIBootstrapServers
	defer func() {
		cl.Conf.HaproxyAPIBootstrapServers = originalBootstrap

		// Best-effort safety net: if this test failed partway through and
		// left the slave's entry deleted, don't leave the cluster's real
		// read backend degraded for whatever runs next. Restores it
		// directly via the Runtime API rather than relying on the
		// cluster's own monitor loop — that loop only self-heals while
		// HaproxyAPIBootstrapServers is enabled, which is about to be
		// restored to its original (possibly false) value above.
		if !restoreReadBackendServer(haRuntime, readBackend, slave.Id, slave.Host, slave.Port, logf, 30*time.Second) {
			logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: cleanup could not restore %s/%s within 30s; it may remain missing or half-configured in the read backend", readBackend, slave.Id)
		}
	}()
	cl.Conf.HaproxyAPIBootstrapServers = true

	// --- Add path: drain + delete a real replica's entry, then wait for
	// the cluster's own monitor loop to re-add and ready it. ---
	if res, err := haRuntime.SetMaintenance(slave.Id, readBackend); err != nil || strings.TrimSpace(res) != "" {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL could not drain %s/%s ahead of deletion: err=%v res=%q", readBackend, slave.Id, err, res)
		return false
	}
	if !haproxyWaitFor(20*time.Second, func() bool {
		res, err := haRuntime.DelServer(readBackend, slave.Id)
		return err == nil && strings.TrimSpace(res) == ""
	}) {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL could not delete %s/%s to stage the add-path test (it may still have active connections)", readBackend, slave.Id)
		return false
	}
	logf("TEST", "haproxy-runtime-lifecycle: removed %s/%s directly via the Runtime API, waiting for the cluster's monitor loop to re-add it", readBackend, slave.Id)

	if !haproxyWaitFor(30*time.Second, func() bool {
		stat, err := haRuntime.ApiCmd("show stat")
		if err != nil {
			return false
		}
		status, ok := haproxyStatServerStatus(stat, readBackend, slave.Id)
		return ok && status == "UP"
	}) {
		stat, _ := haRuntime.ApiCmd("show stat")
		if status, ok := haproxyStatServerStatus(stat, readBackend, slave.Id); ok {
			logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL %s/%s was re-added but never reached UP (stuck at %q) — health checks or eligibility promotion did not complete", readBackend, slave.Id, status)
		} else {
			logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL %s/%s was not re-added by the cluster's own monitor loop within 30s", readBackend, slave.Id)
		}
		return false
	}
	logf("TEST", "haproxy-runtime-lifecycle: %s/%s was re-added and reached UP via the monitor loop — add path OK", readBackend, slave.Id)

	// --- Remove path: add a synthetic entry the cluster doesn't know
	// about, then wait for the monitor loop to drain and remove it. ---
	ghostName := "regtest_ghost_" + strconv.FormatInt(time.Now().Unix(), 10)
	if res, err := haRuntime.AddServer(readBackend, ghostName, "127.0.0.1", "1", "check"); err != nil || strings.TrimSpace(res) != "" {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL could not add synthetic ghost server %s/%s: err=%v res=%q", readBackend, ghostName, err, res)
		return false
	}
	logf("TEST", "haproxy-runtime-lifecycle: added synthetic %s/%s directly via the Runtime API, waiting for the cluster's monitor loop to remove it", readBackend, ghostName)

	if !haproxyWaitFor(30*time.Second, func() bool {
		stat, err := haRuntime.ApiCmd("show stat")
		return err == nil && !haproxyStatHasServer(stat, readBackend, ghostName)
	}) {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL synthetic %s/%s was not removed by the cluster's own monitor loop within 30s", readBackend, ghostName)
		haRuntime.SetMaintenance(ghostName, readBackend)
		haRuntime.DelServer(readBackend, ghostName)
		return false
	}
	logf("TEST", "haproxy-runtime-lifecycle: %s/%s was removed by the monitor loop — remove path OK", readBackend, ghostName)

	return true
}

// restoreReadBackendServer ensures svname is usable in the read backend
// again: either already UP (fully in service — the common case when this
// runs after a passing test, where the monitor loop already promoted it,
// and must be left alone rather than forced backwards through SetDrain), or
// brought to DRAIN with a non-empty check_status (confirming EnableHealth
// actually activated checks, not just that the command returned no error —
// DRAIN alone can't distinguish a fully configured row from one where
// health checks stayed disabled). Self-contained: no dependence on
// HaproxyAPIBootstrapServers or the cluster's monitor loop. Repair only
// applies to states this test's own calls could have produced — missing,
// MAINT, or DRAIN without checks; a present row in any other status (e.g.
// DOWN from an unrelated real failure) is left untouched and reported as
// not restored, since rewriting it would mask state this cleanup didn't
// cause. When repair is needed, retries AddServer -> SetDrain ->
// EnableHealth (skipping AddServer if the row is already present, so a
// stuck-in-MAINT row is reconfigured in place rather than rejected as a
// duplicate), rolling back (SetMaintenance + DelServer) and retrying from
// scratch if either post-add step fails, until fully confirmed or timeout
// elapses.
func restoreReadBackendServer(haRuntime haproxy.Runtime, pool, svname, host, port string, logf func(string, string, ...interface{}), timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		stat, statErr := haRuntime.ApiCmd("show stat")
		status, checkStatus, present := "", "", false
		if statErr == nil {
			status, checkStatus, present = haproxyStatServerFields(stat, pool, svname)
		}
		if present && (status == "UP" || (status == "DRAIN" && checkStatus != "")) {
			return true
		}
		// Only repair states this test's own SetMaintenance/DelServer/
		// AddServer/SetDrain calls could have produced: missing entirely,
		// stuck in MAINT, or DRAIN with checks never enabled. Any other
		// present status (e.g. DOWN from an unrelated real failure during
		// the test window) isn't something this cleanup caused — leave it
		// alone rather than rewrite state the test has no business touching.
		if present && status != "MAINT" && status != "DRAIN" {
			logf(config.LvlWarn, "TEST haproxy-runtime-lifecycle: cleanup found %s/%s in unexpected status %q, leaving it alone rather than rewriting unrelated state", pool, svname, status)
			return false
		}
		if !time.Now().Before(deadline) {
			return false
		}

		if !present {
			if res, err := haRuntime.AddServer(pool, svname, host, port, "check"); err != nil || strings.TrimSpace(res) != "" {
				logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: cleanup could not re-add %s/%s, retrying: err=%v res=%q", pool, svname, err, res)
				time.Sleep(1 * time.Second)
				continue
			}
		}

		drainRes, drainErr := haRuntime.SetDrain(svname, pool)
		drainFailed := drainErr != nil || strings.TrimSpace(drainRes) != ""
		healthRes, healthErr := haRuntime.EnableHealth(pool, svname)
		healthFailed := healthErr != nil || strings.TrimSpace(healthRes) != ""
		if !drainFailed && !healthFailed {
			// EnableHealth activates checks, it doesn't wait for one to
			// complete — give the next check cycle time to report a
			// check_status before re-checking via show stat.
			time.Sleep(1 * time.Second)
			continue
		}

		logf(config.LvlWarn, "TEST haproxy-runtime-lifecycle: cleanup could not fully configure %s/%s (drain failed=%v, health failed=%v), rolling back and retrying", pool, svname, drainFailed, healthFailed)
		haRuntime.SetMaintenance(svname, pool)
		haRuntime.DelServer(pool, svname)
		time.Sleep(1 * time.Second)
	}
}

// haproxyWaitFor polls cond once per second until it returns true or timeout
// elapses, always checking at least once more right at the deadline.
func haproxyWaitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(1 * time.Second)
	}
	return cond()
}

// haproxyStatServerFields returns the status (column 18 / index 17, e.g.
// "UP", "DRAIN", "MAINT") and check_status (column 37 / index 36, e.g.
// "L7OK", empty when health checks aren't running) fields of a "show stat"
// pxname/svname row (exact, case-insensitive on the backend name), and
// whether the row was found at all.
func haproxyStatServerFields(statOutput, backend, svname string) (status string, checkStatus string, found bool) {
	for _, line := range strings.Split(statOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 37 {
			continue
		}
		if strings.EqualFold(fields[0], backend) && fields[1] == svname {
			return fields[17], fields[36], true
		}
	}
	return "", "", false
}

// haproxyStatServerStatus returns just the status field of
// haproxyStatServerFields.
func haproxyStatServerStatus(statOutput, backend, svname string) (string, bool) {
	status, _, found := haproxyStatServerFields(statOutput, backend, svname)
	return status, found
}

// haproxyStatHasServer reports whether a "show stat" response contains a
// pxname/svname row at all, regardless of status.
func haproxyStatHasServer(statOutput, backend, svname string) bool {
	_, _, found := haproxyStatServerFields(statOutput, backend, svname)
	return found
}
