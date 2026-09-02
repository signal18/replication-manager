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

// haproxyAddServerSuccessMsg / haproxyDelServerSuccessMsg /
// haproxyWaitSrvRemovableSuccessMsg mirror the same-named unexported
// constants in cluster/prx_haproxy.go: unlike every other admin Runtime API
// command this file calls (SetMaintenance, SetDrain, EnableHealth), "add
// server", "del server", and "wait ... srv-removable" all reply with a
// non-empty confirmation text even on success — "New server registered.",
// "Server deleted.", and "Done." respectively (verified by hand against
// haproxy:3.0; a genuinely-not-removable "wait" reliably replies "Failed."
// instead). This file's original pattern, "err == nil && res == \"\"",
// shared with cluster/prx_haproxy.go's now-fixed haproxyCmdFailed,
// misreports every successful call to these three commands as a failure.
// For "del server" specifically this surfaced as this test failing to
// stage its own delete ("could not delete ... it may still have active
// connections") even though the row had already been removed, immediately
// followed by an unrelated "No such server" error from the cluster's own
// monitor loop trying to promote the now-gone row to ready from its last
// pre-deletion snapshot.
const (
	haproxyAddServerSuccessMsg        = "New server registered."
	haproxyDelServerSuccessMsg        = "Server deleted."
	haproxyWaitSrvRemovableSuccessMsg = "Done."
)

// haproxyAddServerFailed/haproxyDelServerFailed/haproxyWaitSrvRemovableFailed
// report whether an AddServer/DelServer/WaitSrvRemovable response indicates
// a real failure — see the success-message constants above.
func haproxyAddServerFailed(res string, err error) bool {
	if err != nil {
		return true
	}
	return !strings.HasPrefix(strings.TrimSpace(res), haproxyAddServerSuccessMsg)
}

func haproxyDelServerFailed(res string, err error) bool {
	if err != nil {
		return true
	}
	return !strings.HasPrefix(strings.TrimSpace(res), haproxyDelServerSuccessMsg)
}

func haproxyWaitSrvRemovableFailed(res string, err error) bool {
	if err != nil {
		return true
	}
	return !strings.HasPrefix(strings.TrimSpace(res), haproxyWaitSrvRemovableSuccessMsg)
}

// TestHaproxyRuntimeAPIDynamicServerLifecycle exercises issue #1724 Phase 1
// (dynamic HAProxy read-backend server lifecycle) against the cluster's
// real, live HAProxy proxy and process.
//
// Requires haproxy-mode = "runtimeapi" and HAProxy >= 2.6 already attached.
// haproxy-api-bootstrap-servers is read, not forced: on a resolver-backed
// proxy (HasDNS() == true — an FQDN proxy host, an explicit "dns" proxy tag,
// or an OpenSVC/Kubernetes orchestrator), GetConfigProxyModule
// (cluster/prx_get.go) only renders non-resolver, repman-IP-driven server
// lines when haproxy-api-bootstrap-servers was ALREADY enabled at the
// cluster's last (re)provision — flipping the in-memory flag mid-test
// cannot retroactively change already-rendered config, so this test can
// only validate the dynamic lifecycle on such a proxy if that flag was
// already on. On a non-resolver-backed proxy (Localhost/Docker, where
// HasDNS() is always false), the flag can be toggled freely at test time
// since no resolvers were ever involved either way — this test still
// enables it itself in that case if it wasn't already
// (save/restore pattern from TestGraphiteMetricsQueueBound). Fails with a
// specific reason rather than a false pass if those prerequisites aren't
// met — this framework has no "skip" result.
//
// Three phases poll for the cluster's own background monitor loop to react
// (this test never calls Refresh() itself):
//
//  1. Add path: marks maintenance on a replica (one currently UP with no
//     current connections when available, not just slaves[0] — an actively
//     busy replica may never drain within this test's bounded window
//     through no fault of the feature) through repman's own
//     ServerMonitor.SetMaintenance() — not the raw Runtime API — so
//     cluster.Servers stays consistent with what HAProxy reports;
//     otherwise Refresh()'s own MAINT-correction logic sees an
//     externally-applied MAINT on a server it still considers healthy and
//     races it back to ready before this test's own deletion (via the
//     Runtime API, since there's no production trigger for deleting an
//     active member — WaitSrvRemovable first when the HAProxy version
//     supports it, mirroring production's own removal sequence) can
//     complete. Clears maintenance afterward and polls until the row
//     reappears AND reaches UP — not merely present, which would miss a row
//     stuck in MAINT/DRAIN — and, once UP, asserts its weight came back at
//     100 (matching every statically-rendered sibling), not the Runtime
//     API's own default of 1 for a dynamically added server (AddServer now
//     passes "weight 100" explicitly — see cluster/prx_haproxy.go).
//  2. Re-IP path: writes a deliberately wrong-but-same-family address for
//     the same replica directly via the Runtime API (SetServerAddr),
//     simulating an address drift (e.g. a pod/container restart handing it
//     a new IP) without needing to actually restart anything, then polls
//     until the cluster's own monitor loop corrects it back
//     (reconcileReadBackendServers's address-drift branch,
//     cluster/prx_haproxy.go).
//  3. Remove path: add a synthetic "ghost" server via the Runtime API, then
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
	haproxyDNS := false
	for _, pri := range cl.Proxies {
		if pri.GetType() == config.ConstProxyHaproxy {
			proxyHost = pri.GetHost()
			proxyPort = pri.GetPort()
			haproxyAttached = true
			haproxyDNS = pri.HasDNS()
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

	// On a resolver-backed proxy (HasDNS()==true), dynamic membership only
	// works when the LIVE config was already rendered non-resolver-backed —
	// i.e. haproxy-api-bootstrap-servers was already enabled at this proxy's
	// last (re)provision (see GetConfigProxyModule, cluster/prx_get.go). The
	// ground truth for "is this row currently resolver-backed" is
	// resolverBackedPool, rebuilt fresh from HAProxy's own "show servers
	// state" on every Refresh() pass (cluster/prx_haproxy.go) — but flipping
	// the in-memory HaproxyAPIBootstrapServers flag below cannot
	// retroactively change already-rendered config, so if it's off right
	// now, this proxy's server lines still carry "resolvers dns" and real
	// HAProxy will refuse "del server" on them at runtime ("This server
	// cannot be removed at runtime due to other configuration elements
	// pointing to it"). Fail here with that specific reason instead of
	// staging the delete below and surfacing the generic, misleading "may
	// still have active connections" message.
	bootstrapAlreadyEnabled := cl.Conf.HaproxyAPIBootstrapServers
	if haproxyDNS && !bootstrapAlreadyEnabled {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL dynamic server lifecycle needs haproxy-api-bootstrap-servers=true already set for this resolver-backed (HasDNS=true) proxy's config to be non-resolver-backed — it was off at this proxy's last (re)provision, and toggling it now doesn't retroactively re-render the live config")
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
	// serve traffic in the first place. Among UP replicas, prefer one with
	// zero current connections: DelServer (and, below 3.0, its own retry
	// loop) requires the row to be idle, and an UP replica actively serving
	// real traffic may never drain within this test's bounded window
	// through no fault of the feature under test.
	initialStat, err := haRuntime.ApiCmd("show stat")
	if err != nil {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL could not read read backend %s state: %s", readBackend, err)
		return false
	}
	var slave, upBusySlave *cluster.ServerMonitor
	for _, s := range slaves {
		status, ok := haproxyStatServerStatus(initialStat, readBackend, s.Id)
		if !ok || status != "UP" {
			continue
		}
		if conns, ok := haproxyStatServerConnections(initialStat, readBackend, s.Id); ok && conns == "0" {
			slave = s
			break
		}
		if upBusySlave == nil {
			upBusySlave = s
		}
	}
	if slave == nil {
		slave = upBusySlave
	}
	if slave == nil {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL no replica is currently UP in read backend %s to test the add path against", readBackend)
		return false
	}

	// Save and restore: this cluster keeps running other scenarios after
	// this one.
	originalBootstrap := cl.Conf.HaproxyAPIBootstrapServers
	defer func() {
		// Best-effort safety net: if this test failed partway through,
		// don't leave the cluster's real read backend degraded for
		// whatever runs next. First, make sure repman's own view is
		// consistent (clears maintenance if a failure path above left it
		// set) and give the cluster's own monitor loop a chance to
		// reconcile normally — that's gated on HaproxyAPIBootstrapServers,
		// so it must stay enabled until this either succeeds or we give up
		// on it, not be restored to its original (possibly false) value
		// first.
		if slave.IsMaintenance {
			slave.DelMaintenance()
		}
		restored := haproxyWaitFor(30*time.Second, func() bool {
			stat, err := haRuntime.ApiCmd("show stat")
			if err != nil {
				return false
			}
			status, ok := haproxyStatServerStatus(stat, readBackend, slave.Id)
			return ok && status == "UP"
		})
		cl.Conf.HaproxyAPIBootstrapServers = originalBootstrap
		if restored {
			return
		}

		// Fall back to repairing it directly via the Runtime API — this
		// doesn't depend on HaproxyAPIBootstrapServers or the monitor loop,
		// so it's safe to run after the flag above has been restored.
		logf(config.LvlWarn, "TEST haproxy-runtime-lifecycle: cleanup could not confirm %s/%s reached UP via repman's own reconciliation, falling back to direct Runtime API repair", readBackend, slave.Id)
		if !restoreReadBackendServer(haRuntime, readBackend, slave.Id, slave.Host, slave.Port, logf, 30*time.Second) {
			logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: cleanup could not restore %s/%s within 30s; it may remain missing or half-configured in the read backend", readBackend, slave.Id)
		}
	}()
	cl.Conf.HaproxyAPIBootstrapServers = true

	// --- Add path: mark maintenance through repman's own API — this keeps
	// cluster.Servers consistent with what HAProxy reports, so Refresh()'s
	// MAINT-correction logic (which exists to auto-heal externally-applied
	// MAINT on a server repman still considers healthy) doesn't race the
	// staging below and flip it back to ready before deletion completes. ---
	slave.SetMaintenance()
	if !haproxyWaitFor(10*time.Second, func() bool {
		stat, err := haRuntime.ApiCmd("show stat")
		if err != nil {
			return false
		}
		status, ok := haproxyStatServerStatus(stat, readBackend, slave.Id)
		return ok && status == "MAINT"
	}) {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL could not confirm %s/%s reached MAINT via repman's own maintenance API ahead of deletion", readBackend, slave.Id)
		slave.DelMaintenance()
		return false
	}
	// There's no production trigger for deleting an active cluster member's
	// row (only reconcileReadBackendServers' removal path, which never
	// touches servers still in cluster.Servers) — this is test-only
	// staging, so it goes through the Runtime API directly. Mirrors
	// production's own removal sequence (removeReadBackendServer): wait for
	// the row to become removable before deleting, on versions that support
	// it, rather than only retrying DelServer blind.
	if haVersion.GreaterEqual("3.0") {
		if res, err := haRuntime.WaitSrvRemovable(readBackend, slave.Id, 15*time.Second); haproxyWaitSrvRemovableFailed(res, err) {
			logf(config.LvlWarn, "TEST haproxy-runtime-lifecycle: %s/%s did not report removable within 15s, attempting delete anyway: err=%v res=%q", readBackend, slave.Id, err, res)
		}
	}
	if !haproxyWaitFor(20*time.Second, func() bool {
		res, err := haRuntime.DelServer(readBackend, slave.Id)
		return !haproxyDelServerFailed(res, err)
	}) {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL could not delete %s/%s to stage the add-path test (it may still have active connections)", readBackend, slave.Id)
		slave.DelMaintenance()
		return false
	}
	logf("TEST", "haproxy-runtime-lifecycle: removed %s/%s directly via the Runtime API, waiting for the cluster's monitor loop to re-add it", readBackend, slave.Id)

	// Clear maintenance so reconcileReadBackendServers' add branch (gated on
	// !server.IsMaintenance) picks this server up as missing on its next
	// Refresh() pass.
	slave.DelMaintenance()

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

	// AddServer must pass "weight 100" explicitly (cluster/prx_haproxy.go) so
	// a dynamically re-added server matches every statically-rendered
	// sibling, rather than settling for the Runtime API's own default of 1.
	stat, err := haRuntime.ApiCmd("show stat")
	if err != nil {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL could not read %s/%s weight after re-add: %s", readBackend, slave.Id, err)
		return false
	}
	if weight, ok := haproxyStatServerWeight(stat, readBackend, slave.Id); !ok || weight != "100" {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL %s/%s came back with weight %q (found=%v), want \"100\"", readBackend, slave.Id, weight, ok)
		return false
	}

	// --- Re-IP path: simulate an address drift (e.g. a pod/container
	// restart handing this replica a new IP) by writing a deliberately
	// wrong-but-same-family address directly via the Runtime API, then poll
	// until the cluster's own monitor loop corrects it back to the
	// replica's real, resolved IP (reconcileReadBackendServers's
	// address-drift branch, cluster/prx_haproxy.go). This exercises the
	// plain "set server addr" correction path, which applies regardless of
	// whether the proxy is resolver-backed -- resolverBackedPool only skips
	// fqdn-tracked servers, never addr-tracked ones (see
	// reconcileReadBackendServers in cluster/prx_haproxy.go). ---
	wrongIP := "203.0.113.99" // TEST-NET-3 (RFC 5737), safely unroutable
	if strings.Contains(slave.IP, ":") {
		wrongIP = "2001:db8::99" // documentation-only IPv6 (RFC 3849)
	}
	if res, err := haRuntime.SetServerAddr(readBackend, slave.Id, wrongIP, slave.Port); err != nil || strings.TrimSpace(res) != "" {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL could not stage a wrong address on %s/%s to test drift correction: err=%v res=%q", readBackend, slave.Id, err, res)
		return false
	}
	logf("TEST", "haproxy-runtime-lifecycle: staged wrong address %s on %s/%s, waiting for the cluster's monitor loop to correct it back to %s", wrongIP, readBackend, slave.Id, slave.IP)

	if !haproxyWaitFor(30*time.Second, func() bool {
		showState, err := haRuntime.ApiCmd("show servers state")
		if err != nil {
			return false
		}
		addr, ok := haproxyServerStateAddr(showState, readBackend, slave.Id)
		return ok && addr == slave.IP
	}) {
		logf(config.LvlErr, "TEST haproxy-runtime-lifecycle: FAIL %s/%s address was not corrected back to %s by the cluster's own monitor loop within 30s", readBackend, slave.Id, slave.IP)
		return false
	}
	logf("TEST", "haproxy-runtime-lifecycle: %s/%s address was corrected back to %s by the monitor loop — re-IP path OK", readBackend, slave.Id, slave.IP)

	// --- Remove path: add a synthetic entry the cluster doesn't know
	// about, then wait for the monitor loop to drain and remove it. ---
	ghostName := "regtest_ghost_" + strconv.FormatInt(time.Now().Unix(), 10)
	if res, err := haRuntime.AddServer(readBackend, ghostName, "127.0.0.1", "1", "check"); haproxyAddServerFailed(res, err) {
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
			if res, err := haRuntime.AddServer(pool, svname, host, port, "check"); haproxyAddServerFailed(res, err) {
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

// haproxyStatServerWeight returns the weight field (column 19 / index 18)
// of a "show stat" pxname/svname row, and whether the row was found at all.
func haproxyStatServerWeight(statOutput, backend, svname string) (string, bool) {
	for _, line := range strings.Split(statOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 19 {
			continue
		}
		if strings.EqualFold(fields[0], backend) && fields[1] == svname {
			return fields[18], true
		}
	}
	return "", false
}

// haproxyServerStateAddr returns the srv_addr field (space-separated column
// 5 / index 4, mirroring cluster/prx_haproxy.go's own "show servers state"
// parsing) of a be_name/srv_name row, and whether the row was found at all.
func haproxyServerStateAddr(showStateOutput, backend, svname string) (string, bool) {
	for _, line := range strings.Split(showStateOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		if fields[1] == backend && fields[3] == svname {
			return fields[4], true
		}
	}
	return "", false
}

// haproxyStatServerConnections returns the current-connections field
// (column 6 / index 5 — the same field prx_haproxy.go's Refresh() reports
// as Backend.PrxConnections) of a "show stat" pxname/svname row, and
// whether the row was found at all.
func haproxyStatServerConnections(statOutput, backend, svname string) (string, bool) {
	for _, line := range strings.Split(statOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 6 {
			continue
		}
		if strings.EqualFold(fields[0], backend) && fields[1] == svname {
			return fields[5], true
		}
	}
	return "", false
}
