// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"encoding/csv"
	"io"
	"net"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/haproxy"
)

// rowAtRuntime mirrors cluster/prx_haproxy.go's private serverRowAtRuntime
// (can't call it directly -- unexported, different package): returns a
// pool/name row's "show stat" status (line[17]) and address (line[73]), or
// ("", "") if the row has no entry at all. Needed here because, exactly like
// AddServer/EnableServer/EnableHealth/DelServer, this codebase's own
// SetMaintenance/DelServer/SetMaster wrappers return a plain CLI-text
// response with err == nil when HAProxy rejects the command outright --
// err alone can't tell a rejection from a real success, so every destructive
// step below must confirm the mutation actually took effect before trusting
// it and waiting for self-heal to react to it. Without this, a rejected
// DelServer/SetMaster would leave the row exactly as self-heal would want it
// anyway, and the following waitFor would pass immediately having proven
// nothing.
func rowAtRuntime(haRuntime haproxy.Runtime, pool, name string) (status, addr string) {
	res, err := haRuntime.ApiCmd("show stat")
	if err != nil {
		return "", ""
	}
	reader := csv.NewReader(strings.NewReader(res))
	reader.FieldsPerRecord = -1
	for {
		line, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", ""
		}
		if len(line) > 73 && line[1] != "FRONTEND" && line[1] != "BACKEND" && strings.EqualFold(line[0], pool) && strings.EqualFold(line[1], name) {
			return line[17], line[73]
		}
	}
	return "", ""
}

// TestHaproxyDynamicBackendSelfHeal is the T13 real-HAProxy counterpart to
// cluster/prx_haproxy_test.go's fake-TCP-harness self-heal tests. Those unit
// tests fabricate the runtime-API protocol in Go; this scenario runs against
// a real HAProxy 3.4+ instance (haproxy-mode=runtimeapi,
// haproxy-runtime-dynamic-backends=true in this cluster's config), on an
// already-provisioned, already-monitoring live cluster — not a mock. See
// doc/implementation/cluster/HAPROXY_DYNAMIC_BACKENDS.md's "Follow-up (not in
// this change)" section for why this exists as a separate scenario rather
// than folding into the unit tests.
//
// Deliberately not in the default `tests` list (regtest/regtest.go) alongside
// testStagingRecoverNoReadOnly, for the same reason: most clusters don't run
// HAProxy in runtimeapi mode with the dynamic-backends flag on, and this
// framework has no "skip" result, so including it there would hard-FAIL "ALL"
// on every cluster that isn't specifically set up for it. Run it explicitly:
// --test=testHaproxyDynamicBackendSelfHeal.
//
// This scenario deliberately breaks real runtime state (deletes/mispoints
// real HAProxy server rows via the same router/haproxy.Runtime commands
// selfHealDynamicBackends itself uses) and then waits for the cluster's own
// ambient monitor loop -- not a hand-rolled call -- to reconcile it, since
// that's what actually needs proving against a real binary: the runtime-API
// command strings, response formats, and status-transition timings this
// feature assumes (e.g. "UP (UNPUB)", transient "no check", real health-check
// settle time) are only ever confirmed against the real thing here, never
// against the fake harness's instant, hand-coded responses.
//
// Scope: covers write-leader recreation, the write-leader stale-address
// correction specifically raised in code review (a healthy "leader" row
// pointing at the wrong server -- see the comment at cluster/prx_haproxy.go's
// sawWriteLeader assignment and TestHaproxySelfHealWriteLeaderStaleAddressFixedByExistingMasterCheck
// in cluster/prx_haproxy_test.go), and read-server recreation. It does NOT
// cover backend-level add/publish recovery (deleting service_write/
// service_read entirely) -- that's a larger blast radius against a live dev
// cluster (writes/reads down until self-heal completes, with no typed
// DelBackend helper in router/haproxy to reverse it cleanly) for a path the
// doc says was already manually confirmed live; add it here later if that
// manual confirmation needs to become a repeatable test too.
func (regtest *RegTest) TestHaproxyDynamicBackendSelfHeal(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(level, format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModHAProxy, level, format, args...)
	}

	if !cl.Conf.HaproxyRuntimeDynamicBackends || cl.Conf.HaproxyMode != "runtimeapi" {
		logf(config.LvlErr, "TEST haproxy-dynamic-backend-self-heal: FAIL requires haproxy-runtime-dynamic-backends=true and haproxy-mode=runtimeapi on this cluster -- not applicable to this config")
		return false
	}

	var hprx *cluster.HaproxyProxy
	for _, p := range cl.GetProxies() {
		if h, ok := p.(*cluster.HaproxyProxy); ok {
			hprx = h
			break
		}
	}
	if hprx == nil {
		logf(config.LvlErr, "TEST haproxy-dynamic-backend-self-heal: FAIL no HaproxyProxy found on this cluster")
		return false
	}

	master := cl.GetMaster()
	if master == nil {
		logf(config.LvlErr, "TEST haproxy-dynamic-backend-self-heal: FAIL no master known, cannot exercise the write-leader path")
		return false
	}

	var slave *cluster.ServerMonitor
	for _, s := range cl.GetSlaves() {
		if s != nil && !s.IsMaintenance {
			slave = s
			break
		}
	}
	if slave == nil {
		logf(config.LvlErr, "TEST haproxy-dynamic-backend-self-heal: FAIL no non-maintenance slave found, cannot exercise the read-server path")
		return false
	}

	writeBackend := cl.Conf.HaproxyAPIWriteBackend
	readBackend := cl.Conf.HaproxyAPIReadBackend

	// Talk to the same runtime API this proxy's own Refresh()/self-heal do --
	// same Host:Port, no separate SockFile/Binary needed since ApiCmd always
	// dials Host:Port over TCP (see router/haproxy/runtime_api.go).
	haRuntime := haproxy.Runtime{Host: hprx.Host, Port: hprx.Port}

	// waitFor polls by directly calling hprx.Refresh() (not just sleeping for
	// the ambient monitor tick) since that's the same method the real monitor
	// loop calls -- but tolerates that same loop also running concurrently
	// and possibly winning a given pass, the same way
	// TestGraphiteMetricsQueueBound tolerates concurrent monitor activity
	// rather than assuming exclusive access. Self-heal's own write-leader
	// recreation isn't visible in hprx.BackendsWrite until the pass *after*
	// the one that issued the fix (see selfHealDynamicBackends' comments on
	// same-pass visibility only existing for the read side's
	// upsertHealedReadRow), so this must poll across multiple Refresh() calls
	// even in the best case, not just retry a single one.
	waitFor := func(desc string, ok func() bool) bool {
		deadline := time.Now().Add(30 * time.Second)
		for {
			if err := hprx.Refresh(); err != nil {
				logf(config.LvlErr, "TEST haproxy-dynamic-backend-self-heal: Refresh() error while waiting for %s: %s", desc, err)
			}
			if ok() {
				return true
			}
			if time.Now().After(deadline) {
				logf(config.LvlErr, "TEST haproxy-dynamic-backend-self-heal: FAIL timed out waiting for %s", desc)
				return false
			}
			time.Sleep(1 * time.Second)
		}
	}

	findWrite := func(host, port string) *cluster.Backend {
		for i := range hprx.BackendsWrite {
			b := &hprx.BackendsWrite[i]
			if b.Host == host && b.Port == port {
				return b
			}
		}
		return nil
	}
	findRead := func(svname string) *cluster.Backend {
		for i := range hprx.BackendsRead {
			b := &hprx.BackendsRead[i]
			if b.Svname == svname {
				return b
			}
		}
		return nil
	}

	// Step 1: delete the "leader" row entirely (SetMaintenance first, same
	// as the prune loop/cleanupFailedDynamicServer -- DelServer requires the
	// row to be in maintenance first, confirmed live) and confirm self-heal
	// recreates it pointing at the real master, "UP".
	if _, err := haRuntime.SetMaintenance("leader", writeBackend); err != nil {
		logf(config.LvlErr, "TEST haproxy-dynamic-backend-self-heal: FAIL could not set leader to maintenance: %s", err)
		return false
	}
	if _, err := haRuntime.DelServer("leader", writeBackend); err != nil {
		logf(config.LvlErr, "TEST haproxy-dynamic-backend-self-heal: FAIL could not delete leader row: %s", err)
		return false
	}
	// Confirm the delete actually took effect -- DelServer can be rejected
	// with plain CLI text and err == nil (e.g. the row still holds active/
	// idle connections, the same rejection cleanupFailedDynamicServer's own
	// prune loop guards against). Without this check, a rejected delete would
	// leave "leader" exactly where self-heal wants it, and the wait below
	// would pass having exercised nothing.
	if status, _ := rowAtRuntime(haRuntime, writeBackend, "leader"); status != "" {
		logf(config.LvlErr, "TEST haproxy-dynamic-backend-self-heal: FAIL deliberate break did not take effect: leader row still present (status %q) after DelServer", status)
		return false
	}
	if !waitFor("the deleted write-leader row to be recreated", func() bool {
		b := findWrite(master.Host, master.Port)
		return b != nil && strings.HasPrefix(b.PrxStatus, "UP")
	}) {
		return false
	}
	logf("TEST", "haproxy-dynamic-backend-self-heal: write-leader recreation OK (real HAProxy, %s/%s -> %s:%s)", writeBackend, "leader", master.Host, master.Port)

	// Step 2: point the (now-healthy) leader row at the slave's address
	// instead of the master's -- the exact "healthy status, stale address"
	// scenario raised in code review. Uses the same SetMaster command
	// selfHealDynamicBackends' own address-correction path (and this
	// scenario's step 3 verification) relies on, so this simulates real
	// drift rather than asserting a tautology.
	if _, err := haRuntime.SetMaster(writeBackend, slave.Host, slave.Port); err != nil {
		logf(config.LvlErr, "TEST haproxy-dynamic-backend-self-heal: FAIL could not point leader at the slave's address: %s", err)
		return false
	}
	// Confirm the repoint actually took effect before waiting for it to be
	// corrected back -- same CLI-text-rejection risk as every other runtime
	// command here. net.JoinHostPort, not bare "+":"+" concatenation: HAProxy
	// reports IPv6 addresses bracketed (confirmed live elsewhere in this
	// codebase).
	wantStaleAddr := net.JoinHostPort(slave.Host, slave.Port)
	if _, addr := rowAtRuntime(haRuntime, writeBackend, "leader"); !strings.EqualFold(addr, wantStaleAddr) {
		logf(config.LvlErr, "TEST haproxy-dynamic-backend-self-heal: FAIL deliberate break did not take effect: leader row address is %q, want %q after SetMaster", addr, wantStaleAddr)
		return false
	}
	if !waitFor("the stale write-leader address to be corrected back to master", func() bool {
		b := findWrite(master.Host, master.Port)
		return b != nil && strings.HasPrefix(b.PrxStatus, "UP")
	}) {
		return false
	}
	logf("TEST", "haproxy-dynamic-backend-self-heal: stale write-leader address correction OK (real HAProxy, %s/%s back to %s:%s)", writeBackend, "leader", master.Host, master.Port)

	// Step 3: delete the chosen slave's read-backend row and confirm
	// self-heal recreates it, "UP".
	if _, err := haRuntime.SetMaintenance(slave.Id, readBackend); err != nil {
		logf(config.LvlErr, "TEST haproxy-dynamic-backend-self-heal: FAIL could not set read server %s to maintenance: %s", slave.Id, err)
		return false
	}
	if _, err := haRuntime.DelServer(slave.Id, readBackend); err != nil {
		logf(config.LvlErr, "TEST haproxy-dynamic-backend-self-heal: FAIL could not delete read server %s: %s", slave.Id, err)
		return false
	}
	// Confirm the delete actually took effect, same reasoning as step 1.
	if status, _ := rowAtRuntime(haRuntime, readBackend, slave.Id); status != "" {
		logf(config.LvlErr, "TEST haproxy-dynamic-backend-self-heal: FAIL deliberate break did not take effect: read server %s still present (status %q) after DelServer", slave.Id, status)
		return false
	}
	if !waitFor("the deleted read-server row to be recreated", func() bool {
		b := findRead(slave.Id)
		return b != nil && strings.HasPrefix(b.PrxStatus, "UP")
	}) {
		return false
	}
	logf("TEST", "haproxy-dynamic-backend-self-heal: read-server recreation OK (real HAProxy, %s/%s -> %s:%s)", readBackend, slave.Id, slave.Host, slave.Port)

	logf("TEST", "PASS: haproxy dynamic-backend self-heal confirmed against a real HAProxy runtime API -- write-leader recreation, stale write-leader address correction, and read-server recreation")
	return true
}
