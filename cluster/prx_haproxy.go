// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/haproxy"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
	"github.com/spf13/pflag"
)

// haproxyMinVersionDynamicServers is the lowest HAProxy version that
// supports dynamic read-backend membership. 2.4/2.5 technically have "add
// server"/"del server" but require an "experimental-mode on" prefix and
// reject the "check" keyword outright, which would leave a dynamically
// added server with no health-check configuration — worse than not adding
// it. The gate starts at 2.6, where both restrictions are gone.
const haproxyMinVersionDynamicServers = "2.6"

// haproxyMinVersionWaitRemovable is the lowest HAProxy version that
// understands "wait <ms> srv-removable". Below it, removal skips straight
// to DelServer after draining; DelServer enforces the "no active/idle
// connections" precondition itself and is simply retried next pass if that
// fails, so skipping the wait is a latency trade-off, not a correctness one.
const haproxyMinVersionWaitRemovable = "3.0"

type HaproxyProxy struct {
	Proxy
	// pendingReadServers tracks read-backend svnames whose Runtime API add
	// sequence (AddServer -> SetDrain -> EnableHealth) hasn't been confirmed
	// complete. While marked, no code path in this file may promote the
	// server to ready — otherwise the generic eligibility logic could bring
	// an unmonitored server into service. Guarded by Lock (embedded Proxy).
	pendingReadServers map[string]bool
	// nonPurgeableReadServers tracks read-backend svnames HAProxy itself has
	// already told us it will never "del server" (its Runtime API response
	// contains haproxyNonPurgeableServerMsg — e.g. a "resolvers" clause on
	// that server line, config-generated or hand-written). Learned
	// reactively from HAProxy's own answer rather than guessed from
	// proxy.HasDNS(), so it also catches externally-managed haproxy.cfg
	// files repman didn't generate. Once marked, reconcileReadBackendServers
	// skips retrying DelServer on it (a config reload is required to change
	// that server's non-purgeable status, and this process has no signal
	// for one happening, so the mark is never cleared automatically).
	// Guarded by Lock (embedded Proxy).
	nonPurgeableReadServers map[string]bool
	// standbyReloadPending and lastStandbyInit debounce haproxy-mode=standby's
	// full Init() (render+reload) so a burst of state-change events (many
	// replicas flapping in the same window) triggers at most one reload per
	// standbyReloadMinInterval instead of one per event. Guarded by Lock
	// (embedded Proxy). See BackendsStateChange() and the check at the top
	// of Refresh() for how the trailing event within a debounce window is
	// still guaranteed to apply.
	standbyReloadPending bool
	lastStandbyInit      time.Time
}

// bootstrapServersCookiePath records haproxy-api-bootstrap-servers as
// actually rendered at this proxy's last (re)provision -- persisted to
// disk (not in-memory) so it survives a repman restart.
func (proxy *HaproxyProxy) bootstrapServersCookiePath() string {
	return proxy.Datadir + "/@cookie_bootstrap_servers"
}

// BootstrapServersEnabled is what this proxy was actually last provisioned
// with, not the live cluster.Conf.HaproxyAPIBootstrapServers value, which
// can change without a reprovision. Defaults false when never recorded.
func (proxy *HaproxyProxy) BootstrapServersEnabled() bool {
	data, err := os.ReadFile(proxy.bootstrapServersCookiePath())
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == "on"
}

// setProvisionedBootstrapServers snapshots the live setting at (re)provision
// time -- see BootstrapServersEnabled.
func (proxy *HaproxyProxy) setProvisionedBootstrapServers(enabled bool) {
	value := "off"
	if enabled {
		value = "on"
	}
	if err := os.WriteFile(proxy.bootstrapServersCookiePath(), []byte(value), 0644); err != nil {
		cluster := proxy.ClusterGroup
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlDbg, "Create bootstrap-servers cookie: %s", err)
	}
}

// delProvisionedBootstrapServers clears the snapshot on unprovision, so a
// later restart doesn't fall back to a stale "last deployed" value for a
// proxy that no longer exists.
func (proxy *HaproxyProxy) delProvisionedBootstrapServers() {
	if err := os.Remove(proxy.bootstrapServersCookiePath()); err != nil {
		cluster := proxy.ClusterGroup
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlDbg, "Remove bootstrap-servers cookie: %s", err)
	}
}

// standbyReloadMinInterval is the minimum spacing BackendsStateChange()
// enforces between two standby Init() (render+reload) calls. HAProxy's
// reload itself is a real process fork/exec, not a free operation, so a
// cluster with several replicas flapping in the same window (e.g. a brief
// network blip) must not turn into one reload per replica per transition.
const standbyReloadMinInterval = 2 * time.Second

func (proxy *HaproxyProxy) markPendingReadServer(svname string) {
	proxy.Lock.Lock()
	defer proxy.Lock.Unlock()
	if proxy.pendingReadServers == nil {
		proxy.pendingReadServers = make(map[string]bool)
	}
	proxy.pendingReadServers[svname] = true
}

func (proxy *HaproxyProxy) unmarkPendingReadServer(svname string) {
	proxy.Lock.Lock()
	defer proxy.Lock.Unlock()
	delete(proxy.pendingReadServers, svname)
}

func (proxy *HaproxyProxy) isPendingReadServer(svname string) bool {
	proxy.Lock.Lock()
	defer proxy.Lock.Unlock()
	return proxy.pendingReadServers[svname]
}

// haproxyNonPurgeableServerMsg is the distinctive substring of HAProxy's
// Runtime API refusal to "del server" a server another configuration
// element still references (a "resolvers" clause being the case repman can
// itself produce — see GetConfigProxyModule — but not the only possible
// one). Matched case-sensitively against the exact wording HAProxy 2.6-3.0
// use; if a future HAProxy version rewords it, the practical effect is just
// that markNonPurgeableReadServer never triggers and DelServer is retried
// every pass as before (logIfFailed already logs each failure), not a
// crash or incorrect behavior.
const haproxyNonPurgeableServerMsg = "other configuration elements pointing to it"

// haproxyNoSuchServerMsg is HAProxy's response when a Runtime API command
// targets a read-backend svname that doesn't currently exist. Seen live on
// setReadBackendMaintenance's SetReady call: ServerMonitor.DelMaintenance()
// synchronously notifies every proxy via SetMaintenance (this file's
// interface implementation), independent of the monitor loop's own
// Refresh() tick — svname there comes from GetReadBackendDetail, a snapshot
// as fresh as the last completed Refresh() but no fresher. If the row was
// removed (by reconcileReadBackendServers' own stale-entry cleanup, an
// operator, or — as in the regtest — a direct Runtime API delete) after
// that snapshot was taken but before this call runs, HAProxy correctly
// reports "No such server." — logSetStateIfFailed treats this as
// LvlDbg rather than LvlErr: there being nothing to promote to ready/maint
// is not an operational problem worth alarming over, and the next
// Refresh() pass reconciles the row's existence (or lack of it) fully
// regardless.
const haproxyNoSuchServerMsg = "No such server."

// logSetStateIfFailed is haproxyCmdFailed's logging counterpart for the
// simple "set server ... state maint/ready" calls in
// setReadBackendMaintenance: the command genuinely does reply empty on
// success (unlike AddServer/DelServer/WaitSrvRemovable — see those
// constants above), so haproxyCmdFailed's rule is correct here; the only
// adjustment needed is the log level for haproxyNoSuchServerMsg specifically.
func logSetStateIfFailed(proxy *HaproxyProxy, action string, res string, err error) bool {
	cluster := proxy.ClusterGroup
	msg, failed := haproxyCmdFailed(err, res)
	if !failed {
		return false
	}
	level := haproxySetStateLogLevel(msg, proxy.reconcileReadBackendServersActive())
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, level, "HAProxy %s: %s", action, msg)
	return true
}

// reconcileReadBackendServersActive reports whether reconcileReadBackendServers
// is actually reconciling missing members right now — mirrors every
// condition that function itself checks before attempting an add, not just
// cluster.Conf.HaproxyAPIBootstrapServers: the version/mode gate at its top
// (unsupported HAProxy version, or haproxy-mode != "runtimeapi", and it
// no-ops entirely). proxy.HasDNS() is deliberately NOT part of this check —
// whether a specific server line actually carries a "resolvers" clause is
// ground truth read fresh from HAProxy itself every Refresh() pass
// (resolverBackedPool, built in Refresh() from "show servers state"), not
// something knowable from a static condition here. All conditions here must
// hold for a missing/renamed read-backend row to actually get re-added on
// the next pass — see haproxySetStateLogLevel, the reason this exists.
func (proxy *HaproxyProxy) reconcileReadBackendServersActive() bool {
	cluster := proxy.ClusterGroup
	return proxy.BootstrapServersEnabled() &&
		proxy.supportsDynamicServers() &&
		cluster.Conf.HaproxyMode == "runtimeapi"
}

// haproxySetStateLogLevel picks the log severity for a failed "set server
// ... state maint/ready" call: LvlDbg for haproxyNoSuchServerMsg (see its
// doc comment — this is an expected, self-correcting race, not an
// operational problem) — but only when reconciliationActive is true (see
// HaproxyProxy.reconcileReadBackendServersActive). The "self-correcting"
// premise is that a missing/renamed row gets re-added on the next
// Refresh() pass; without that actually happening — bootstrap-servers off
// (the default), an unsupported HAProxy version, haproxy-mode !=
// "runtimeapi", or a resolver-backed config where adds are intentionally
// skipped — nothing corrects a persistent mismatch (wrong
// haproxy-api-read-backend name, a hand-edited config, an svname that will
// never exist), and downgrading that to LvlDbg would silently remove the
// only error-visibility signal an operator in any of those cases had for
// it. LvlErr in all other cases.
func haproxySetStateLogLevel(msg string, reconciliationActive bool) string {
	if reconciliationActive && strings.Contains(msg, haproxyNoSuchServerMsg) {
		return config.LvlDbg
	}
	return config.LvlErr
}

func (proxy *HaproxyProxy) markNonPurgeableReadServer(svname string) {
	proxy.Lock.Lock()
	defer proxy.Lock.Unlock()
	if proxy.nonPurgeableReadServers == nil {
		proxy.nonPurgeableReadServers = make(map[string]bool)
	}
	proxy.nonPurgeableReadServers[svname] = true
}

func (proxy *HaproxyProxy) isNonPurgeableReadServer(svname string) bool {
	proxy.Lock.Lock()
	defer proxy.Lock.Unlock()
	return proxy.nonPurgeableReadServers[svname]
}

func NewHaproxyProxy(placement int, cluster *Cluster, proxyHost string) *HaproxyProxy {
	conf := cluster.Conf
	prx := new(HaproxyProxy)
	prx.SetPlacement(placement, conf.ProvProxAgents, conf.SlapOSHaProxyPartitions, conf.HaproxyHostsIPV6, conf.HaproxyJanitorWeights)
	prx.Type = config.ConstProxyHaproxy
	prx.Port = strconv.Itoa(conf.HaproxyAPIPort)
	prx.ReadPort = conf.HaproxyReadPort
	prx.WritePort = conf.HaproxyWritePort
	prx.ReadWritePort = conf.HaproxyWritePort
	prx.Name = proxyHost
	prx.Host = proxyHost
	// haproxy-mode=standby always runs co-located with repman (Init(),
	// below, renders+reloads it via a local PID regardless of the cluster's
	// own orchestrator) -- prx.Host there must stay whatever
	// locally-reachable address the operator configured via haproxy-servers
	// (127.0.0.1 by default), never the CNI-rewritten Service DNS name
	// below. That rewrite is for runtimeapi/externalcheck, which genuinely
	// are deployed as a separate orchestrator-managed resource repman only
	// reaches over the network -- applying it to standby would have Init()
	// render a "stats socket ipv4@<service dns>:<port>" bind
	// (share/haproxy_config.template) that the local HAProxy process can't
	// bind to.
	if conf.ProvNetCNI && conf.HaproxyMode != "standby" {
		prx.Host = prx.Host + "." + cluster.Name + ".svc." + conf.ProvOrchestratorCluster
	}
	prx.User = conf.HaproxyUser
	prx.Pass = cluster.Conf.GetDecryptedValue("haproxy-password")

	return prx
}

func (proxy *HaproxyProxy) AddFlags(flags *pflag.FlagSet, conf *config.Config) {
	flags.BoolVar(&conf.HaproxyOn, "haproxy", false, "Wrapper to use HAProxy on same host")
	flags.StringVar(&conf.HaproxyMode, "haproxy-mode", "runtimeapi", "HAProxy mode [standby|runtimeapi|externalcheck|dataplaneapi] -- only takes effect on the next (re)provision of the proxy")
	flags.BoolVar(&conf.HaproxyDebug, "haproxy-debug", true, "Extra info on monitoring backend")
	flags.IntVar(&conf.HaproxyLogLevel, "log-level-haproxy", 1, "Log level for debug")
	flags.StringVar(&conf.HaproxyUser, "haproxy-user", "admin", "HAProxy API user")
	flags.StringVar(&conf.HaproxyPassword, "haproxy-password", "admin", "HAProxy API password")
	flags.StringVar(&conf.HaproxyHosts, "haproxy-servers", "127.0.0.1", "HAProxy hosts")
	flags.StringVar(&conf.HaproxyJanitorWeights, "haproxy-janitor-weights", "100", "Weight of each HAProxy host inside janitor proxy")
	flags.IntVar(&conf.HaproxyAPIPort, "haproxy-api-port", 1999, "HAProxy runtime api port")
	flags.IntVar(&conf.HaproxyWritePort, "haproxy-write-port", 3306, "HAProxy read-write port to leader")
	flags.IntVar(&conf.HaproxyReadPort, "haproxy-read-port", 3307, "HAProxy load balancer read port to all nodes")
	flags.IntVar(&conf.HaproxyStatPort, "haproxy-stat-port", 1988, "HAProxy statistics port")
	flags.StringVar(&conf.HaproxyBinaryPath, "haproxy-binary-path", "/usr/sbin/haproxy", "HAProxy binary location")
	flags.StringVar(&conf.HaproxyReadBindIp, "haproxy-ip-read-bind", "0.0.0.0", "HAProxy input bind address for read")
	flags.StringVar(&conf.HaproxyWriteBindIp, "haproxy-ip-write-bind", "0.0.0.0", "HAProxy input bind address for write")
	flags.StringVar(&conf.HaproxyAPIReadBackend, "haproxy-api-read-backend", "service_read", "HAProxy API backend name used for read")
	flags.StringVar(&conf.HaproxyAPIWriteBackend, "haproxy-api-write-backend", "service_write", "HAProxy API backend name used for write")
	flags.BoolVar(&conf.HaproxyAPIBootstrapServers, "haproxy-api-bootstrap-servers", false, "For haproxy-mode=runtimeapi: drive every backend member (read and write) by repman's own resolved server IP over the Runtime API instead of HAProxy's own DNS resolution -- generated server lines carry no \"resolvers\" clause, and adding/removing a cluster server updates the live backend at runtime instead of requiring a reload (requires HAProxy >= 2.6; silently inactive otherwise). Off (the default) keeps runtimeapi resolver-backed, identical to haproxy-mode=externalcheck/standby. Can be toggled live at any time, but each proxy keeps running its own last-provisioned value until reprovisioned.")
	flags.StringVar(&conf.HaproxyHostsIPV6, "haproxy-servers-ipv6", "", "HAProxy IPv6 bind address ")
}

func (proxy *HaproxyProxy) Init() {
	cluster := proxy.ClusterGroup
	haproxydatadir := proxy.Datadir + "/var"

	if _, err := os.Stat(haproxydatadir); os.IsNotExist(err) {
		proxy.GetProxyConfig()
		os.Symlink(proxy.Datadir+"/init/data", haproxydatadir)
	}

	// Everything below builds, renders, and reloads a *local* haproxy.cfg on
	// this (the repman server's) host. Two independent reasons a mode
	// reaches this point:
	//
	//  - The Localhost orchestrator: HAProxy always runs co-located with
	//    repman there, for every mode (runtimeapi, externalcheck, standby).
	//    externalcheck additionally gets its checkmaster/checkslave scripts
	//    written to proxy.Datadir/init/ and wired into the rendered config
	//    via external-check (writeLocalhostHaproxyCheckScripts below) --
	//    Localhost has no container image to swap the way OpenSVC/etc. do,
	//    so this is externalcheck's only broken-replica exclusion mechanism
	//    there. runtimeapi still renders a plain config with no check
	//    scripts; its ongoing state comes from Refresh()'s Runtime API
	//    calls instead.
	//  - haproxy-mode=standby specifically, regardless of orchestrator:
	//    standby always runs its HAProxy instance co-located with repman --
	//    started/reloaded via its local PID -- even when the cluster's
	//    databases are provisioned elsewhere (OpenSVC, etc.); prov.go's
	//    proxyServiceOrchestrator() already routes standby's
	//    Start/Stop/(Un)ProvisionProxyService calls to the Localhost*
	//    implementations for exactly this reason.
	//
	// Every other mode on a non-Localhost orchestrator (runtimeapi,
	// externalcheck) is deployed by the cluster's own orchestrator instead,
	// so this local render+reload has nothing to do there; those proxies
	// bootstrap once via the config-fetch tarball path
	// (server/api_database.go, GetProxyConfig() above already covers that
	// one-time fetch) and then keep current via runtimeapi's own Runtime
	// API calls or externalcheck's own remote check scripts -- never via a
	// full local re-render.
	if cluster.GetOrchestrator() != config.ConstOrchestratorLocalhost && cluster.Conf.HaproxyMode != "standby" {
		return
	}
	//haproxysockFile := "haproxy.stats.sock"

	haproxytemplateFile := "haproxy_config.template"
	haproxyconfigFile := "haproxy.cfg"
	haproxyjsonFile := "vamp_router.json"
	haproxypidFile := "haproxy.pid"
	haproxyerrorPagesDir := "error_pages"
	//	haproxymaxWorkDirSize := 50 // this value is based on (max socket path size - md5 hash length - pre and postfixes)

	haRuntime := haproxy.Runtime{
		Binary:   cluster.Conf.HaproxyBinaryPath,
		SockFile: filepath.Join(proxy.Datadir+"/var", "/haproxy.stats.sock"),
		Port:     proxy.Port,
		Host:     proxy.Host,
	}

	haConfig := haproxy.Config{
		TemplateFile:  filepath.Join(cluster.Conf.ShareDir, haproxytemplateFile),
		ConfigFile:    filepath.Join(haproxydatadir, "/", haproxyconfigFile),
		JsonFile:      filepath.Join(haproxydatadir, "/", haproxyjsonFile),
		ErrorPagesDir: filepath.Join(haproxydatadir, "/", haproxyerrorPagesDir, "/"),
		PidFile:       filepath.Join(haproxydatadir, "/", haproxypidFile),
		//	SockFile:      filepath.Join(haproxydatadir, "/", haproxysockFile),
		SockFile:   "/tmp/haproxy" + proxy.Id + ".sock",
		ApiPort:    proxy.Port,
		StatPort:   strconv.Itoa(proxy.ClusterGroup.Conf.HaproxyStatPort),
		Host:       proxy.Host,
		WorkingDir: filepath.Join(haproxydatadir + "/"),
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy loading haproxy config at %s", haproxydatadir)
	err := haConfig.GetConfigFromDisk()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy did not find an haproxy config...initializing new config")
		haConfig.InitializeConfig()
	}
	few := haproxy.Frontend{Name: "my_write_frontend", Mode: "tcp", DefaultBackend: cluster.Conf.HaproxyAPIWriteBackend, BindPort: cluster.Conf.HaproxyWritePort, BindIp: cluster.Conf.HaproxyWriteBindIp}
	if err := haConfig.AddFrontend(&few); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Failed to add frontend write ")
	} else {
		if err := haConfig.AddFrontend(&few); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy should return nil on already existing frontend")
		}

	}
	if result, _ := haConfig.GetFrontend("my_write_frontend"); result.Name != "my_write_frontend" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy failed to add frontend write")
	}
	// haproxy-mode=externalcheck: repman does nothing at runtime -- HAProxy's
	// own external-check calls back into repman's /master-status and
	// /slave-status APIs to decide backend membership, mirroring OpenSVC's
	// haproxy_check.cfg (share/opensvc/moduleset_mariadb.svc.mrm.proxy.json).
	// Localhost has no container image to swap, so the scripts are written
	// straight to proxy.Datadir/init/ instead. This is distinct from
	// haproxy-mode=standby, which has no external check either -- standby's
	// read-backend membership is decided directly by Init()'s server loop
	// below instead (see the isHaproxyReadEligible filter).
	var checkmasterPath, checkslavePath string
	if cluster.Conf.HaproxyMode == "externalcheck" {
		var err error
		checkmasterPath, checkslavePath, err = proxy.writeLocalhostHaproxyCheckScripts()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Could not write HAProxy externalcheck scripts: %s", err)
			checkmasterPath, checkslavePath = "", ""
		} else {
			haConfig.ExternalCheck = true
			haConfig.InsecureForkWanted = true
		}
	}

	bew := haproxy.Backend{Name: cluster.Conf.HaproxyAPIWriteBackend, Mode: "tcp"}
	if checkmasterPath != "" {
		bew.ExternalCheck = true
		bew.ExternalCheckPath = "/usr/bin:/bin"
		bew.ExternalCheckCommand = checkmasterPath
	}
	haConfig.AddBackend(&bew)

	fer := haproxy.Frontend{Name: "my_read_frontend", Mode: "tcp", DefaultBackend: cluster.Conf.HaproxyAPIReadBackend, BindPort: cluster.Conf.HaproxyReadPort, BindIp: cluster.Conf.HaproxyReadBindIp}
	if err := haConfig.AddFrontend(&fer); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy failed to add frontend read")
	} else {
		if err := haConfig.AddFrontend(&fer); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy should return nil on already existing frontend")
		}
	}
	if result, _ := haConfig.GetFrontend("my_read_frontend"); result.Name != "my_read_frontend" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy failed to get frontend")
	}
	/* End add front end */

	ber := haproxy.Backend{Name: cluster.Conf.HaproxyAPIReadBackend, Mode: "tcp"}
	if checkslavePath != "" {
		ber.ExternalCheck = true
		ber.ExternalCheckPath = "/usr/bin:/bin"
		ber.ExternalCheckCommand = checkslavePath
	}
	if err := haConfig.AddBackend(&ber); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy failed to add backend for "+cluster.Conf.HaproxyAPIReadBackend)
	}

	// addServerTo builds this iteration's server entry fresh and adds it to
	// backend, logging (not failing) on error -- kept as one place so the
	// read and write backends below can't drift into different server
	// details for the same server. AddServer returns *haproxy.Error, not
	// the error interface -- returning it directly here would wrap a nil
	// *Error in a non-nil error interface (Go's classic typed-nil trap),
	// making every call look like a failure even on success. The explicit
	// nil check below avoids that.
	addServerTo := func(backend string, server *ServerMonitor, port int) error {
		if err := haConfig.AddServer(backend, &haproxy.ServerDetail{
			Name: server.Id, Host: server.Host, Port: port,
			Weight: 100, MaxConn: 2000, Check: true, CheckInterval: 1000,
		}); err != nil {
			return err
		}
		return nil
	}

	// The leader needs the same proxy-servers-read-on-master / -no-slave
	// gate runtimeapi's masterShouldBeReader() applies in Refresh(), or
	// standby would always list the leader as a reader regardless of those
	// settings. Mirrors masterShouldBeReader()'s own
	// cluster.HasNoValidSlave() || !<available reader> shape, with the
	// second term computed fresh from this same pass's live cluster.Servers
	// instead of reusing proxy.HasAvailableReader(): that reads
	// proxy.BackendsRead, which only Refresh() populates -- empty/stale on
	// the very first Init() at provisioning, before any Refresh() has ever
	// run, which would wrongly count as "no reader available" and add the
	// leader even with a healthy replica already up.
	//
	// cluster.HasNoValidSlave() itself IS still consulted (unlike an earlier
	// version of this fix that dropped it entirely): it returns true
	// unconditionally for config.TopoActivePassive regardless of the
	// passive node's own state (cluster/cluster_has.go), which is exactly
	// what TestHaproxyMasterShouldBeReader's "no-slave fallback with
	// active-passive topology" case already locks in for masterShouldBeReader()
	// -- dropping it would have made standby exclude the leader in
	// active-passive whenever the passive node merely looked replication-healthy,
	// diverging from runtimeapi's documented behavior for that topology.
	//
	// standbyReadIneligible (not hasBrokenReplicationForRead() alone) is used
	// for this "is there a valid alternative reader" check specifically --
	// but deliberately NOT for the per-server skip below, which keeps
	// hasBrokenReplicationForRead() unchanged from before this fix (see its
	// comment). hasBrokenReplicationForRead()'s SlaveErr/RelayErr/... set
	// doesn't cover Failed/Suspect/ErrorAuth (IsDown()), so a Failed replica
	// would otherwise count as a "valid" reader here and wrongly suppress
	// the leader's own fallback -- runtimeapi doesn't share this gap because
	// HAProxy's own health check independently marks such a replica DOWN
	// regardless of repman's classification, but standby's render has no
	// equivalent live check to fall back on for this specific decision.
	//
	// HasValidReadSlave() (cluster/cluster_has.go) is the exported home of
	// this exact loop -- shared, via ShouldServeReadsFromMaster(), with
	// ServerMonitor.IsValidReaderCheck (srv_has.go), which needs the
	// identical no-slave-fallback rule for haproxy-mode=externalcheck's
	// checkslave HTTP handler (reached via the reader-status route, not
	// slave-status -- see that method's doc comment). Keep this comment
	// block in sync with ShouldServeReadsFromMaster's doc comment if the
	// rule ever changes.
	masterShouldRead := cluster.Configurator.HasProxyReadLeader() ||
		(cluster.Configurator.HasProxyReadLeaderNoSlave() &&
			(cluster.HasNoValidSlave() || !cluster.HasValidReadSlave()))

	// haproxy-mode=runtimeapi's write backend is a single fixed slot
	// literally named "leader" -- Refresh()'s SetMaster()/SetMasterFQDN()
	// repoint it via Runtime API ("set server service_write/leader addr/port
	// ...") on every leadership change, matching the convention K8s/OpenSVC's
	// config-fetch tarball already renders (GetConfigProxyModule's own
	// "server leader ..." line, cluster/prx_get.go). runtimeapiLeaderRendered
	// tracks whether the loop below found a leader to name it after; if none
	// did (e.g. this Init() call races leader election on first
	// provisioning), the fallback after the loop still creates the "leader"
	// slot with a placeholder address, exactly like GetConfigProxyModule's
	// own "server leader none:3306 ..." fallback -- so SetMaster() always has
	// a slot to repoint once discovery completes, instead of failing with
	// "No such server." forever.
	runtimeapiLeaderRendered := false

	for _, server := range cluster.Servers {
		if server.IsMaintenance {
			continue
		}
		p, _ := strconv.Atoi(server.Port)

		// haproxy-mode=standby has neither a Runtime API nor an
		// external-check to exclude a broken replica after the fact --
		// Init()'s own config generation is the ONLY mechanism, so a server
		// with broken replication must simply never be added to the read
		// backend in the first place. Mirrors the classification Refresh()
		// uses to DRAIN the same server under haproxy-mode=runtimeapi (see
		// the runtimeapi-only gate above), so both modes agree on what
		// "broken" means; scoped to standby only so runtimeapi's own initial
		// static render (before Runtime API add/del ever runs) still lists
		// every server for older HAProxy versions that lack dynamic
		// add/del and can only toggle an existing slot's drain state.
		//
		// Deliberately hasBrokenReplicationForRead(), not the broader
		// standbyReadIneligible() used above for the leader-fallback
		// decision: a Failed/Suspect/ErrorAuth replica's own read-backend
		// membership is unchanged, pre-existing behavior (relying on
		// HAProxy's own health check to drain it, same as it always has) --
		// only the leader's read-on-master* decision needed the wider
		// IsDown() check, to correctly treat such a replica as "not a valid
		// alternative reader" without also changing whether it gets
		// rendered into the backend itself.
		skipRead := cluster.Conf.HaproxyMode == "standby" &&
			(server.hasBrokenReplicationForRead() || (server.IsLeader() && !masterShouldRead))
		if !skipRead {
			if err := addServerTo(cluster.Conf.HaproxyAPIReadBackend, server, p); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Failed to add server %s to HAProxy backend %s: %s", server.Id, cluster.Conf.HaproxyAPIReadBackend, err)
			}
		}

		// haproxy-mode=externalcheck's write backend mirrors the read
		// backend's shape above: every non-maintenance server gets a static
		// entry, and HAProxy's own external-check (checkmaster, invoked per
		// server with that server's own address as $3/$4, same as
		// checkslave above) decides live which one is actually master and
		// reports it UP -- exactly the mechanism already used for
		// read-backend eligibility, and exactly what K8s/OpenSVC already
		// ship (GetConfigProxyModule's per-server "serverN" write-backend
		// entries, cluster/prx_get.go). Rendering every candidate here
		// instead of only today's leader closes two gaps at once: a
		// leadership change no longer needs a re-render at all (checkmaster's
		// next poll just flips which slot reports UP), and the render can
		// never race leader election on first provisioning, since inclusion
		// no longer depends on IsLeader() having resolved yet.
		//
		// standby has no external-check at all -- Init() re-render really is
		// the only mechanism there, hence Failover()/BackendsStateChange()
		// re-running it -- so it keeps the single up-to-date "leader" entry
		// instead. Delete-then-add keeps that idempotent across repeated
		// Init() calls on an unchanged leader, and actually drops a server
		// that just lost leadership instead of leaving it (and any
		// never-added replica) stuck UP in the write group.
		if cluster.Conf.HaproxyMode == "externalcheck" {
			if err := addServerTo(cluster.Conf.HaproxyAPIWriteBackend, server, p); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Failed to add server %s to HAProxy backend %s: %s", server.Id, cluster.Conf.HaproxyAPIWriteBackend, err)
			}
		} else if cluster.Conf.HaproxyMode == "runtimeapi" {
			if server.IsLeader() {
				haConfig.DeleteServer(cluster.Conf.HaproxyAPIWriteBackend, "leader")
				if err := haConfig.AddServer(cluster.Conf.HaproxyAPIWriteBackend, &haproxy.ServerDetail{
					Name: "leader", Host: server.Host, Port: p,
					Weight: 100, MaxConn: 2000, Check: true, CheckInterval: 1000,
				}); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Failed to add leader slot to HAProxy backend %s: %s", cluster.Conf.HaproxyAPIWriteBackend, err)
				} else {
					runtimeapiLeaderRendered = true
				}
			}
		} else {
			haConfig.DeleteServer(cluster.Conf.HaproxyAPIWriteBackend, server.Id)
			if server.IsLeader() {
				if err := addServerTo(cluster.Conf.HaproxyAPIWriteBackend, server, p); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Failed to add server %s to HAProxy backend %s: %s", server.Id, cluster.Conf.HaproxyAPIWriteBackend, err)
				}
			}
		}
	}

	// Fallback for when no server was resolved as leader during the loop
	// above (a startup race, or a topology mid-election): the "leader" slot
	// still needs to exist with SOME address, otherwise SetMaster() finds
	// nothing to repoint once discovery completes -- the exact bug this
	// fallback exists to prevent. GetConfigProxyModule's own equivalent
	// fallback (cluster/prx_get.go) uses the literal hostname "none", but
	// that's actually a value for HAProxy's separate init-addr *option*, not
	// a valid hostname on its own -- live-reproduced: HAProxy refuses to even
	// start ("could not resolve address 'none'") without an accompanying
	// "init-addr none", which this struct-based renderer has no field for.
	//
	// 192.0.2.1 (RFC 5737 TEST-NET-1) is used instead of a real, reachable
	// address like 127.0.0.1: it's reserved for documentation/testing and
	// guaranteed never to route to a real service anywhere, so this slot
	// only ever times out/fails closed until SetMaster() corrects it within
	// one monitoring tick -- a loopback or other live address risks silently
	// routing writes to whatever happens to be listening on that host/port
	// in the window before the real leader is discovered.
	if cluster.Conf.HaproxyMode == "runtimeapi" && !runtimeapiLeaderRendered {
		if err := haConfig.AddServer(cluster.Conf.HaproxyAPIWriteBackend, &haproxy.ServerDetail{
			Name: "leader", Host: "192.0.2.1", Port: 3306,
			Weight: 100, MaxConn: 2000, Check: true, CheckInterval: 1000,
		}); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Failed to add placeholder leader slot to HAProxy backend %s: %s", cluster.Conf.HaproxyAPIWriteBackend, err)
		}
	}

	err = haConfig.Render()
	renderErr := err
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Could not create haproxy config %s", err)
	}
	// Reaching here already implies either the Localhost orchestrator (any
	// mode) or haproxy-mode=standby (any orchestrator) -- see the early
	// return above -- so haRuntime.Reload()'s exec of a local haproxy binary
	// against this rendered config's local stats-socket bind address is
	// meaningful in every case that gets this far, not just standby.
	// Unconditional: externalcheck/runtimeapi on Localhost only ever call
	// Init() once (at provision/start -- Failover()/BackendsStateChange()/
	// setReadBackendMaintenance() only route back into Init() for standby),
	// and that one call is the only thing that ever execs the haproxy
	// binary at all for those modes on this orchestrator. Gating this on
	// haproxy-mode=="standby" the way the rest of this function's mode
	// branches do would leave externalcheck's checkmaster/checkslave wiring
	// and runtimeapi's config both fully rendered to disk but never actually
	// read by a running process.
	if err := haRuntime.SetPid(haConfig.PidFile); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy set pid %s", err)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy reload config on pid %s", haConfig.PidFile)
	}

	err = haRuntime.Reload(&haConfig)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Can't reload haproxy config %s", err)
	} else if renderErr == nil {
		// Only once the new config is both rendered and actually applied --
		// a reload "succeeding" against a config Render() failed to update
		// would otherwise flip the cookie while the running HAProxy is
		// still on the old one.
		proxy.setProvisionedBootstrapServers(cluster.Conf.HaproxyAPIBootstrapServers)
	}
}

func (proxy *HaproxyProxy) Refresh() error {
	cluster := proxy.ClusterGroup

	// Trailing edge of BackendsStateChange()'s debounce: a state change that
	// landed inside standbyReloadMinInterval only marked standbyReloadPending
	// rather than calling Init() immediately. Refresh() runs continuously via
	// the monitoring loop regardless of further state changes, so checking
	// here guarantees that mark is applied within one more pass even if
	// flapping stops before the cooldown elapses and nothing else ever
	// retries it. Covers externalcheck too, for the same reason
	// BackendsStateChange() does -- see its comment.
	if (cluster.Conf.HaproxyMode == "standby" || cluster.Conf.HaproxyMode == "externalcheck") && proxy.hasStandbyReloadPending() && proxy.shouldRunStandbyInit() {
		proxy.Init()
	}

	stagingsrv := cluster.StagingServer
	if stagingsrv == nil {
		stagingsrv = cluster.SetStandaloneAsStaging()
	}
	// if proxy.ClusterGroup.Conf.HaproxyStatHttp {

	/*
		url := "http://" + proxy.Host + ":" + proxy.Port + "/stats;csv"
		client := &http.Client{
			Timeout: time.Duration(2 * time.Second),
		}

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			cluster.SetState("ERR00052", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00052"], err), ErrFrom: "MON"})
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			cluster.SetState("ERR00052", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00052"], err), ErrFrom: "MON"})
			return err
		}
		defer resp.Body.Close()
		reader := csv.NewReader(resp.Body)

	*/
	//tcpAddr, err := net.ResolveTCPAddr("tcp4", proxy.Host+":"+proxy.Port)
	//cluster.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModHAProxy,config.LvlErr, "haproxy entering  refresh: ")

	haproxydatadir := proxy.Datadir + "/var"
	haproxysockFile := "haproxy.stats.sock"

	haRuntime := haproxy.Runtime{
		Binary:   cluster.Conf.HaproxyBinaryPath,
		SockFile: filepath.Join(haproxydatadir, "/", haproxysockFile),
		Port:     proxy.Port,
		Host:     proxy.Host,
	}

	backend_ip_host := make(map[string]string)
	backend_svname_host := make(map[string]string) // svname → FQDN for DNS failure fallback
	// resolverBackedPool tracks, per "backend/svname" key, whether HAProxy
	// itself currently reports this exact entry as resolver-tracked
	// (srv_fqdn populated in "show servers state" — ground truth read fresh
	// every pass, not assumed from cluster.Conf.HaproxyAPIBootstrapServers).
	// That flag only decides what GetConfigProxyModule renders at this
	// proxy's NEXT (re)provision (cluster/prx_get.go) — it can be toggled
	// live at any time (SwitchHaproxyAPIBootstrapServers, the settings API)
	// with no reprovision required to take effect on the flag itself, so
	// reading it here instead of asking HAProxy directly would let repman's
	// idea of "is this entry resolver-backed" drift out of sync with what
	// the running HAProxy process actually has for as long as they
	// disagree — e.g. toggling the flag off on an already non-resolver-backed
	// proxy would make the write-path SetMaster calls below switch to FQDN
	// dispatch against a "leader" line that still carries no "resolvers"
	// clause, and that Runtime API call would fail. Querying live state
	// every pass instead means there is no such window at all.
	//
	// Deliberately NOT gated on proxy.HasDNS(): that predicate itself can
	// drift from the live haproxy.cfg without a reprovision -- proxy.Host is
	// fixed once the Proxy object is built, but Configurator.HaveProxyTag
	// ("dns") can be flipped live via the add/del-proxy-tag API/gRPC action
	// with no reprovision at all. Gating this fetch on HasDNS() would
	// reintroduce exactly the class of desync this ground-truth design
	// exists to eliminate: a tag removed after provisioning would silently
	// skip this query and leave resolverBackedPool empty even though the
	// running HAProxy config still has "resolvers dns"-attached entries,
	// making every lookup below wrongly report "not resolver-backed" and
	// risking the addr-vs-fqdn hazard (live-verified: "set server addr" on a
	// resolver-attached entry degrades to forced DRAIN once the "hold
	// valid" timer elapses). One extra "show servers state" round trip per
	// pass on a proxy that turns out to have nothing resolver-backed is a
	// negligible cost next to that.
	resolverBackedPool := make(map[string]bool)
	{
		// When using FQDN map server state host->IP to locate in show stats where it's only IPs
		cmd := "show servers state"

		showleaderstate, err := haRuntime.ApiCmd(cmd)
		if err != nil {
			cluster.SetState("ERR00052", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00052"], err), ErrFrom: "MON"})
			return err
		}

		// API return a first row with return code make it as comment
		showleaderstate = "# " + showleaderstate

		// API return space sparator conveting to csv
		showleaderstate = strings.ReplaceAll(showleaderstate, " ", ",")

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "haproxy show servers state response :%s", showleaderstate)

		showleaderstatereader := io.NopCloser(bytes.NewReader([]byte(showleaderstate)))

		defer showleaderstatereader.Close()
		reader := csv.NewReader(showleaderstatereader)
		reader.Comment = '#'
		for {
			line, error := reader.Read()
			if error == io.EOF {
				break
			} else if error != nil {
				// error shadows the outer err from the ApiCmd call above,
				// which is guaranteed nil here (already checked, or this
				// loop would never have started) -- "return err" would
				// silently report success on a genuine CSV parse failure.
				// SetState (not a plain log call) so a persistent parse
				// failure logs once on the OPENED transition instead of
				// flooding the log every Refresh() pass -- this runs on
				// every monitoring tick.
				cluster.SetState("ERR00052", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00052"], error), ErrFrom: "MON"})
				return error
			}
			if len(line) > 17 {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy adding IP map %s %s", line[4], line[17])
				backend_ip_host[line[4]] = line[17]
				if line[3] != "" && line[17] != "" {
					backend_svname_host[line[3]] = line[17]
				}
				// "show servers state" columns: 1=be_name, 3=srv_name,
				// 17=srv_fqdn ("-" when unset, HAProxy's placeholder for an
				// empty optional field).
				if line[1] != "" && line[3] != "" {
					resolverBackedPool[line[1]+"/"+line[3]] = line[17] != "" && line[17] != "-"
				}
			}
		}

	}

	if proxy.Version == "" {
		vstring, err := haRuntime.GetVersion()
		if err == nil {
			if vstring != "" {
				proxy.SetVersion(vstring)
			}
		}
	}

	result, err := haRuntime.ApiCmd("show stat")
	if err != nil {
		cluster.SetState("ERR00052", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00052"], err), ErrFrom: "MON"})
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy show stat result: %s", result)

	r := io.NopCloser(bytes.NewReader([]byte(result)))
	defer r.Close()
	reader := csv.NewReader(r)

	proxy.BackendsWrite = nil
	proxy.BackendsRead = nil
	foundMasterInStat := false
	masterReadFound := false
	masterReadSvname := ""
	masterReadStatus := ""
	// readBackendSvnames tracks every server entry HAProxy currently reports
	// for the read backend, including ones that don't resolve to a known
	// cluster.ServerMonitor (e.g. a decommissioned node) — used below to
	// reconcile runtime server membership via reconcileReadBackendServers.
	readBackendSvnames := make(map[string]bool)
	// readBackendAddrBySvname records the raw host:port HAProxy currently
	// has on file for each read-backend svname (pre-DNS-translation), so
	// reconcileReadBackendServers can detect a server that kept its repman
	// Id but changed address (re-IP/re-provisioning).
	readBackendAddrBySvname := make(map[string]string)
	// readBackendStatusBySvname records the raw status column ("MAINT",
	// "DRAIN", "UP", ...) HAProxy currently reports for each read-backend
	// svname, so reconcileReadBackendServers can tell a stale svname it
	// already knows is non-purgeable (isNonPurgeableReadServer) is ALSO
	// already sitting in MAINT from this same "show stat" call — at zero
	// extra Runtime API cost — and skip re-issuing SetMaintenance on it
	// every single pass. Without this, a persistently non-purgeable stale
	// entry (a real, ongoing condition — see WARN0209) got a redundant but
	// full-cost SetMaintenance round trip every pass forever, scaling
	// linearly with how many such entries exist and reintroducing the
	// unbounded-pass-time risk haproxyReconcileBudget exists to prevent.
	readBackendStatusBySvname := make(map[string]string)
	for {
		line, error := reader.Read()
		if error == io.EOF {
			break
		} else if error != nil {
			// error shadows the outer err from the "show stat" ApiCmd call
			// above, which is guaranteed nil here (already checked, or this
			// loop would never have started) -- "return err" would silently
			// report success on a genuine CSV parse failure. SetState (not
			// a plain log call) so a persistent parse failure logs once on
			// the OPENED transition instead of flooding the log every
			// Refresh() pass -- this runs on every monitoring tick.
			cluster.SetState("ERR00052", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00052"], error), ErrFrom: "MON"})
			return error
		}
		if len(line) < 73 {
			cluster.SetState("WARN0078", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0078"], err), ErrFrom: "MON"})
			return errors.New(clusterError["WARN0078"])
		}
		// Skip FRONTEND/BACKEND summary lines — only process actual server entries
		if line[1] == "FRONTEND" || line[1] == "BACKEND" {
			continue
		}
		// Exact (case-insensitive) match: a substring check would also match
		// an unrelated backend whose name contains this one (e.g.
		// "service_read_shadow" vs "service_read").
		if strings.EqualFold(line[0], cluster.Conf.HaproxyAPIWriteBackend) {
			host := line[73]
			// See resolverBackedPool's doc comment above: an entry HAProxy
			// itself doesn't report as resolver-tracked already matches
			// GetServerFromURL directly via ServerMonitor.IP, with no FQDN
			// translation to go through.
			writeRowResolverBacked := resolverBackedPool[line[0]+"/"+line[1]]
			if proxy.HasDNS() && writeRowResolverBacked {
				// After provisioning the stats may arrive with IP:Port while sometime not
				if strings.Count(host, ":") >= 2 {
					// IPV6
					host, _, _ = net.SplitHostPort(host)
					host = misc.Unbracket(host)
				} else {
					host = strings.Split(line[73], ":")[0]
				}
				host = backend_ip_host[host]
			}

			srv := cluster.GetServerFromURL(host)

			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy stat lookup writer: host %s translated to %s", line[73], host)

			if srv != nil {
				bkw := Backend{
					Host:           srv.Host,
					Port:           srv.Port,
					Status:         srv.State,
					Svname:         line[1],
					PrxName:        line[73],
					PrxStatus:      line[17],
					PrxConnections: line[5],
					PrxByteIn:      line[8],
					PrxByteOut:     line[9],
					PrxLatency:     line[61], //ttime: average session time in ms over the 1024 last requests
					// PrxHostgroup is ProxySQL-shaped (a hostgroup id), but the
					// dashboard's Proxies table already renders it generically
					// as "ID Group" — HAProxy has no hostgroup concept, so this
					// reuses that column for the backend/pool name instead of
					// leaving it blank. line[0] (pxname), not
					// cluster.Conf.HaproxyAPIWriteBackend: every other field
					// here comes straight from this stat row, and the match
					// above is case-insensitive (EqualFold), so the live value
					// isn't even guaranteed to be byte-identical to config.
					PrxHostgroup: line[0],
				}

				if bkw.PrxName != "" {
					foundMasterInStat = true
					proxy.BackendsWrite = append(proxy.BackendsWrite, bkw)

					// Runtime API write-backend mutation is runtimeapi-only.
					// standby propagates topology exclusively through
					// Init()/Failover() (full config regen + reload, see
					// setReadBackendMaintenance/Failover) -- Refresh() only
					// reports status for standby, it never patches state, so
					// there's no dependency here on checkmaster's external-check
					// working, and no separate Runtime-API-vs-Init() path to
					// keep in sync.
					if cluster.Conf.HaproxyMode == "runtimeapi" {
						if cluster.Conf.TopologyStaging && proxy.IsInStaging() {
							if !srv.IsStandAlone() {
								if stagingsrv != nil {
									cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "[Staging] Detecting wrong master server in haproxy %s fixing it to standalone %s %s", proxy.Host+":"+proxy.Port, stagingsrv.Host, stagingsrv.Port)
									// A different pool than the write-backend
									// row this block is already inside, so
									// its own resolver-backed status needs
									// its own lookup, not writeRowResolverBacked.
									stagingResolverBacked := resolverBackedPool[cluster.Conf.HaproxyStagingBackend+"/"+line[1]]
									res, err := haRuntime.SetMaster(cluster.Conf.HaproxyStagingBackend, stagingsrv.RuntimeAPIAddr(stagingResolverBacked), stagingsrv.Port)
									// A real (not misclassified, see setAddrFailed)
									// failure here is reported via SetState, not a
									// plain LogModulePrintf: Refresh() runs every
									// monitoring tick, so a persistent failure
									// (e.g. HAProxy unreachable) would otherwise log
									// an identical ERROR line forever. SetState only
									// logs once, on the OPENED/RESOLV transition —
									// same dedup as WARN0209/WARN0210 above.
									if msg, failed := setAddrFailed(err, res); failed {
										cluster.SetState("ERR00106", state.State{
											ErrType:   config.LvlErr,
											ErrDesc:   fmt.Sprintf(clusterError["ERR00106"], proxy.Host+":"+proxy.Port, cluster.Conf.HaproxyStagingBackend, stagingsrv.Host+":"+stagingsrv.Port, msg),
											ErrFrom:   "HAPROXY",
											ServerUrl: proxy.Host + ":" + proxy.Port,
										})
									} else {
										cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "%s: %s (staging: %s)", proxy.Host+":"+proxy.Port, res, stagingsrv.Host+":"+stagingsrv.Port)
									}
								}
							}
						} else {
							if !srv.IsMaster() {
								master := cluster.GetMaster()
								if master != nil {
									cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "Detecting wrong master server in haproxy %s fixing it to master %s %s", proxy.Host+":"+proxy.Port, master.Host, master.Port)
									res, err := haRuntime.SetMaster(cluster.Conf.HaproxyAPIWriteBackend, master.RuntimeAPIAddr(writeRowResolverBacked), master.Port)
									// See the staging branch above for why a real
									// failure goes through SetState, not a plain
									// LogModulePrintf.
									if msg, failed := setAddrFailed(err, res); failed {
										cluster.SetState("ERR00106", state.State{
											ErrType:   config.LvlErr,
											ErrDesc:   fmt.Sprintf(clusterError["ERR00106"], proxy.Host+":"+proxy.Port, cluster.Conf.HaproxyAPIWriteBackend, master.Host+":"+master.Port, msg),
											ErrFrom:   "HAPROXY",
											ServerUrl: proxy.Host + ":" + proxy.Port,
										})
									} else {
										cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "%s: %s (master: %s)", proxy.Host+":"+proxy.Port, res, master.Host+":"+master.Port)
									}
								}
							}
						}
					}
				}
			}
		}
		if strings.EqualFold(line[0], cluster.Conf.HaproxyAPIReadBackend) {
			if line[1] != "" {
				readBackendSvnames[line[1]] = true
				readBackendStatusBySvname[line[1]] = line[17]
				if h, p, splitErr := net.SplitHostPort(line[73]); splitErr == nil {
					// JoinHostPort re-brackets h for IPv6 (SplitHostPort
					// returns it unbracketed) so this matches expectedAddr's
					// canonical form below.
					readBackendAddrBySvname[line[1]] = net.JoinHostPort(h, p)
				}
			}
			host := line[73]
			// See resolverBackedPool's doc comment above: an entry HAProxy
			// itself doesn't report as resolver-tracked already matches
			// GetServerFromURL directly via ServerMonitor.IP, with no FQDN
			// translation to go through.
			readRowResolverBacked := resolverBackedPool[line[0]+"/"+line[1]]
			if proxy.HasDNS() && readRowResolverBacked {
				// After provisioning the stats may arrive with  IP:Port while sometime not
				if strings.Count(host, ":") >= 2 {
					// IPV6
					host, _, _ = net.SplitHostPort(host)
					host = misc.Unbracket(host)
				} else {
					host = strings.Split(line[73], ":")[0]
				}
				host = backend_ip_host[host]
			}
			srv := cluster.GetServerFromURL(host)
			if srv == nil && proxy.HasDNS() && readRowResolverBacked {
				// DNS resolution may have failed (server DOWN/MAINT) — use the
				// FQDN from show servers state via the svname→FQDN map.
				if fqdn, ok := backend_svname_host[line[1]]; ok {
					srv = cluster.GetServerFromURL(fqdn)
				}
			}

			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy stat lookup reader: host %s translated to %s", line[73], host)

			if srv != nil {
				bkr := Backend{
					Host:           srv.Host,
					Port:           srv.Port,
					Status:         srv.State,
					Svname:         line[1],
					PrxName:        line[73],
					PrxStatus:      line[17],
					PrxConnections: line[5],
					PrxByteIn:      line[8],
					PrxByteOut:     line[9],
					PrxLatency:     line[61],
					// See the write-backend Backend{} above for why this reuses
					// PrxHostgroup for the pool name, and why it's line[0]
					// rather than cluster.Conf.HaproxyAPIReadBackend.
					PrxHostgroup: line[0],
				}

				proxy.BackendsRead = append(proxy.BackendsRead, bkr)

				// Runtime API read-backend mutation is runtimeapi-only,
				// mirroring the write-backend gate above -- standby relies
				// on checkslave's external-check (option external-check,
				// HAProxy's own health check against repman's own
				// /slave-status API) to control read-backend membership.
				// Refresh() only reports status for standby; issuing a
				// competing SetDrain/SetReady here would race against
				// checkslave's own independent polling interval instead of
				// deferring to it.
				if cluster.Conf.HaproxyMode == "runtimeapi" {
					if cluster.Conf.TopologyStaging && proxy.IsInStaging() {
						if stagingsrv != nil {
							if srv.Id == stagingsrv.Id { // Only activate staging server for read
								if line[17] == "DRAIN" {
									if proxy.isPendingReadServer(bkr.Svname) {
										cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlWarn, "HAProxy skipping ready for staging server %s: its Runtime API add sequence never completed successfully", srv.URL)
									} else {
										cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy staging is DRAIN in haproxy %s for server %s", proxy.Host+":"+proxy.Port, srv.URL)
										res, err := haRuntime.SetReady(bkr.Svname, cluster.Conf.HaproxyAPIReadBackend)
										if msg, failed := haproxyCmdFailed(err, res); failed {
											cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "%s: %s (server: %s)", proxy.Host+":"+proxy.Port, msg, srv.Host+":"+srv.Port)
										} else {
											cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "%s: %s (server: %s)", proxy.Host+":"+proxy.Port, res, srv.Host+":"+srv.Port)
										}
									}
								}
							} else { // Deactivate other servers
								if line[17] == "UP" {
									cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy non-staging backend state is UP in haproxy %s for server %s", proxy.Host+":"+proxy.Port, srv.URL)
									res, err := haRuntime.SetDrain(bkr.Svname, cluster.Conf.HaproxyAPIReadBackend)
									if msg, failed := haproxyCmdFailed(err, res); failed {
										cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "%s: %s (server: %s)", proxy.Host+":"+proxy.Port, msg, srv.Host+":"+srv.Port)
									} else {
										cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "%s: %s (server: %s)", proxy.Host+":"+proxy.Port, res, srv.Host+":"+srv.Port)
									}
								}
							}
						}
					} else {
						if (srv.State == stateSlaveErr || srv.State == stateRelayErr || srv.State == stateSlaveLate || srv.State == stateRelayLate || srv.IsIgnored()) && line[17] == "UP" || srv.State == stateWsrepLate || srv.State == stateWsrepDonor {
							cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy detecting broken replication and UP state in haproxy %s drain server %s (%s)", proxy.Host+":"+proxy.Port, srv.Id, srv.URL)
							res, err := haRuntime.SetDrain(bkr.Svname, cluster.Conf.HaproxyAPIReadBackend)
							if msg, failed := haproxyCmdFailed(err, res); failed {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "%s: %s (server: %s)", proxy.Host+":"+proxy.Port, msg, srv.Host+":"+srv.Port)
							} else {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "%s: %s (server: %s)", proxy.Host+":"+proxy.Port, res, srv.Host+":"+srv.Port)
								proxy.setLastReadBackendStatus("DRAIN")
							}
						}
						if (srv.State == stateSlave || srv.State == stateRelay || (srv.State == stateWsrep && !srv.IsLeader())) && line[17] == "DRAIN" && !srv.IsIgnored() {
							if proxy.isPendingReadServer(bkr.Svname) {
								// A server stuck pending (failed add + failed
								// rollback) reports DRAIN with otherwise-healthy
								// replication; without this check it would be
								// promoted despite health checks possibly never
								// having been activated.
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlWarn, "HAProxy skipping ready for server %s: its Runtime API add sequence never completed successfully", srv.URL)
							} else {
								cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy valid replication and DRAIN state in haproxy %s enable traffic on server %s", proxy.Host+":"+proxy.Port, srv.URL)
								res, err := haRuntime.SetReady(bkr.Svname, cluster.Conf.HaproxyAPIReadBackend)
								if msg, failed := haproxyCmdFailed(err, res); failed {
									cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "%s: %s (server: %s)", proxy.Host+":"+proxy.Port, msg, srv.Host+":"+srv.Port)
								} else {
									cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "%s: %s (server: %s)", proxy.Host+":"+proxy.Port, res, srv.Host+":"+srv.Port)
									proxy.setLastReadBackendStatus("UP")
								}
							}
						}
					}
				}
				if !(cluster.Conf.TopologyStaging && proxy.IsInStaging()) {
					if srv.IsMaster() {
						masterReadFound = true
						masterReadSvname = bkr.Svname
						masterReadStatus = line[17]
					}
				}

				if srv.IsMaintenance && line[17] == "UP" {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy detecting server %s in maintenance but proxy %s reports UP  ", srv.URL, proxy.Host+":"+proxy.Port)
					// Only mirror into in-memory status if the transition
					// actually happened, not if it no-opped or failed —
					// otherwise HasAvailableReader()/masterShouldBeReader()
					// could see a status HAProxy never reached.
					if proxy.setReadBackendMaintenance(srv) {
						proxy.setLastReadBackendStatus("MAINT")
						if srv.IsMaster() {
							masterReadStatus = "MAINT"
						}
					}
				}
				if !srv.IsMaintenance && line[17] == "MAINT" {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy detecting server %s UP but proxy %s reports in maintenance ", srv.URL, proxy.Host+":"+proxy.Port)
					if proxy.setReadBackendMaintenance(srv) {
						proxy.setLastReadBackendStatus("UP")
						if srv.IsMaster() {
							masterReadStatus = "UP"
						}
					}
				}
			}
		}
	}
	// Same runtimeapi-only gate as the per-server loop above: standby
	// leaves read-backend master-reader reconciliation to checkslave's
	// external-check, not a competing Runtime API patch here.
	if masterReadFound && cluster.Conf.HaproxyMode == "runtimeapi" {
		shouldRead := proxy.masterShouldBeReader()
		if !shouldRead && masterReadStatus == "UP" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy master is not configured as reader but state is UP in haproxy %s", proxy.Host+":"+proxy.Port)
			res, err := haRuntime.SetDrain(masterReadSvname, cluster.Conf.HaproxyAPIReadBackend)
			if msg, failed := haproxyCmdFailed(err, res); failed {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "%s: %s", proxy.Host+":"+proxy.Port, msg)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "%s: %s", proxy.Host+":"+proxy.Port, res)
			}
		}
		if shouldRead && masterReadStatus == "DRAIN" && proxy.isPendingReadServer(masterReadSvname) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlWarn, "HAProxy skipping ready for master reader %s: its Runtime API add sequence never completed successfully", masterReadSvname)
		} else if shouldRead && masterReadStatus == "DRAIN" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy master is configured as reader but state is DRAIN in haproxy %s", proxy.Host+":"+proxy.Port)
			res, err := haRuntime.SetReady(masterReadSvname, cluster.Conf.HaproxyAPIReadBackend)
			if msg, failed := haproxyCmdFailed(err, res); failed {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "%s: %s", proxy.Host+":"+proxy.Port, msg)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "%s: %s", proxy.Host+":"+proxy.Port, res)
			}
		}
	}
	// Same runtimeapi-only gate as above: fires when no write-backend row
	// resolved to a known ServerMonitor at all.
	if !foundMasterInStat && cluster.Conf.HaproxyMode == "runtimeapi" {
		if cluster.Conf.TopologyStaging && proxy.IsInStaging() {
			if stagingsrv != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "[Staging] HAProxy has standalone in cluster but not in haproxy %s fixing it to standalone %s %s", proxy.Host+":"+proxy.Port, stagingsrv.Host, stagingsrv.Port)
				// No matched "show stat" row to key off here (that's the
				// whole reason this branch exists) -- the write-backend's
				// single fixed slot is always named "leader" in runtimeapi
				// mode (see GetConfigProxyModule, cluster/prx_get.go).
				stagingResolverBacked := resolverBackedPool[cluster.Conf.HaproxyStagingBackend+"/leader"]
				res, err := haRuntime.SetMaster(cluster.Conf.HaproxyStagingBackend, stagingsrv.RuntimeAPIAddr(stagingResolverBacked), stagingsrv.Port)
				// See the per-row master-fix branch above for why a real
				// failure goes through SetState, not a plain LogModulePrintf.
				if msg, failed := setAddrFailed(err, res); failed {
					cluster.SetState("ERR00106", state.State{
						ErrType:   config.LvlErr,
						ErrDesc:   fmt.Sprintf(clusterError["ERR00106"], proxy.Host+":"+proxy.Port, cluster.Conf.HaproxyStagingBackend, stagingsrv.Host+":"+stagingsrv.Port, msg),
						ErrFrom:   "HAPROXY",
						ServerUrl: proxy.Host + ":" + proxy.Port,
					})
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "%s: %s (staging: %s)", proxy.Host+":"+proxy.Port, res, stagingsrv.Host+":"+stagingsrv.Port)
				}
			}
		} else {
			master := cluster.GetMaster()
			if master != nil && master.IsLeader() {
				writeResolverBacked := resolverBackedPool[cluster.Conf.HaproxyAPIWriteBackend+"/leader"]
				res, err := haRuntime.SetMaster(cluster.Conf.HaproxyAPIWriteBackend, master.RuntimeAPIAddr(writeResolverBacked), master.Port)
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy has leader in cluster but not in %s fixing it to master %s return %s", proxy.Host+":"+proxy.Port, master.URL, res)
				// See the per-row master-fix branch above for why a real
				// failure goes through SetState, not a plain LogModulePrintf.
				if msg, failed := setAddrFailed(err, res); failed {
					cluster.SetState("ERR00106", state.State{
						ErrType:   config.LvlErr,
						ErrDesc:   fmt.Sprintf(clusterError["ERR00106"], proxy.Host+":"+proxy.Port, cluster.Conf.HaproxyAPIWriteBackend, master.URL, msg),
						ErrFrom:   "HAPROXY",
						ServerUrl: proxy.Host + ":" + proxy.Port,
					})
				}
			}
		}

	}

	proxy.reconcileReadBackendServers(haRuntime, readBackendSvnames, readBackendAddrBySvname, readBackendStatusBySvname, resolverBackedPool)

	return nil
}

// setLastReadBackendStatus overrides the PrxStatus of the most recently
// appended BackendsRead entry to reflect the effective state after this
// pass's own SetDrain/SetReady/SetMaintenance actions, so that
// HasAvailableReader() (called later in this same Refresh() pass) does not
// act on the stale pre-action "show stat" snapshot.
func (proxy *HaproxyProxy) setLastReadBackendStatus(status string) {
	if n := len(proxy.BackendsRead); n > 0 {
		proxy.BackendsRead[n-1].PrxStatus = status
	}
}

// HasAvailableReader returns true if the read backend currently has at least
// one entry that is not the current master/leader's own row and whose
// effective HAProxy status for this pass is "UP". The master/leader's row is
// identified by host/port identity against cluster.GetMaster() rather than by
// b.Status == stateMaster, because a Galera/Wsrep leader's repman state is
// stateWsrep, not stateMaster (cluster.GetMaster() returns cluster.vmaster for
// Wsrep topologies, where cluster.master == cluster.vmaster == leader).
func (proxy *HaproxyProxy) HasAvailableReader() bool {
	cluster := proxy.ClusterGroup
	master := cluster.GetMaster()
	for _, b := range proxy.BackendsRead {
		isMasterEntry := master != nil && b.Host == master.Host && b.Port == master.Port
		if !isMasterEntry && b.PrxStatus == "UP" {
			return true
		}
	}
	return false
}

// hasBrokenReplicationForRead reports whether server's replication state
// disqualifies it from the HAProxy read backend -- the same classification
// Refresh() uses to DRAIN a server under haproxy-mode=runtimeapi (see the
// runtimeapi-only gate in Refresh()), reused here so Init()'s
// haproxy-mode=standby config generation excludes it up front instead.
func (server *ServerMonitor) hasBrokenReplicationForRead() bool {
	return server.State == stateSlaveErr || server.State == stateRelayErr ||
		server.State == stateSlaveLate || server.State == stateRelayLate ||
		server.State == stateWsrepLate || server.State == stateWsrepDonor ||
		server.IsIgnored()
}

// standbyReadIneligible is hasBrokenReplicationForRead() broadened with
// IsDown() (Failed/Suspect/ErrorAuth) for haproxy-mode=standby's read-backend
// decisions specifically (Init()'s per-server skip and its leader-fallback
// "is there a valid slave" check). runtimeapi doesn't need this broader set:
// HAProxy's own health check against the DB port independently marks a
// Failed/unreachable replica DOWN regardless of repman's own classification,
// so hasBrokenReplicationForRead() alone is enough for Refresh()'s DRAIN
// decision there. Standby's render has no equivalent live check to fall
// back on -- it IS the only mechanism -- so a Failed replica left out of
// this broader set would otherwise both count as a "valid" reader (wrongly
// excluding the leader fallback) and still get rendered into the read
// backend itself.
func (server *ServerMonitor) standbyReadIneligible() bool {
	return server.hasBrokenReplicationForRead() || server.IsDown()
}

// masterShouldBeReader reports whether the master/leader should be a member
// of the HAProxy read backend: always when proxy-servers-read-on-master is
// set, or as a fallback (proxy-servers-read-on-master-no-slave, default true)
// when there is no other valid/available slave reader.
func (proxy *HaproxyProxy) masterShouldBeReader() bool {
	cluster := proxy.ClusterGroup
	if cluster.Configurator.HasProxyReadLeader() {
		return true
	}
	return cluster.Configurator.HasProxyReadLeaderNoSlave() &&
		(cluster.HasNoValidSlave() || !proxy.HasAvailableReader())
}

// supportsDynamicServers reports whether this HAProxy instance's Runtime API
// supports "add server"/"del server" the way repman uses them (HAProxy >=
// 2.6 — see haproxyMinVersionDynamicServers for why 2.4/2.5, which
// technically have these commands, are deliberately excluded). proxy.Version
// is only known once Refresh() has successfully run "show version" at least
// once.
func (proxy *HaproxyProxy) supportsDynamicServers() bool {
	if proxy.Version == "" {
		return false
	}
	v, _ := version.NewVersionFromString("haproxy", proxy.Version)
	return v.GreaterEqual(haproxyMinVersionDynamicServers)
}

// supportsWaitRemovable reports whether this HAProxy instance understands
// "wait ... srv-removable" (HAProxy >= 3.0). Only meaningful once
// supportsDynamicServers() already holds.
func (proxy *HaproxyProxy) supportsWaitRemovable() bool {
	if proxy.Version == "" {
		return false
	}
	v, _ := version.NewVersionFromString("haproxy", proxy.Version)
	return v.GreaterEqual(haproxyMinVersionWaitRemovable)
}

// haproxySrvRemovableWait is the server-side wait budget passed to "wait
// srv-removable" before giving up on removing a stale dynamic server in a
// single Refresh() pass; the client-side socket deadline (see
// haproxy.Runtime.WaitSrvRemovable) is set higher than this so we observe
// the actual result rather than racing our own read timeout.
const haproxySrvRemovableWait = 2 * time.Second

// haproxyReconcileBudget bounds how long reconcileReadBackendServers spends
// on non-safety-critical Runtime API work per Refresh() pass — AddServer,
// the pending-add retry, and WaitSrvRemovable/DelServer inside
// removeReadBackendServer — so a batch of missing/stale servers can't
// stall the monitoring tick this runs inside of (cluster/prx.go's
// wg.Wait()), violating DEVELOPMENT_LAWS.md's F2-F4 invariant.
//
// It deliberately does NOT bound two calls that are safety-critical for
// correct traffic routing: SetMaintenance (draining a stale server not yet
// confirmed MAINT) and SetServerAddr (correcting a changed address —
// otherwise HAProxy keeps routing to the old one, possibly a reassigned,
// unrelated host). Both always run regardless of budget; see the removal
// loop and address-update branch below. So pass time isn't strictly capped
// when many servers need first-time draining/correction at once — an
// explicit tradeoff (traffic correctness over a hard ceiling), not an
// oversight. The redundant-drain skip (confirmed-MAINT, already
// non-purgeable) is what actually keeps the ongoing case bounded.
//
// Work this budget does cover, if not reached before the deadline, is
// simply retried next pass — see deadlineHit and WARN0210.
//
// A var, not a const: TestHaproxyReconcileBudgetDefersExcessWork shrinks it
// (save/restore) to exercise the deadline-exhaustion path without a real
// multi-second sleep.
var haproxyReconcileBudget = 10 * time.Second

// haproxyCmdFailed reports whether a Runtime API mutation command failed.
// HAProxy's admin-level server commands return no output on success; any
// non-empty response body is an error message even though the TCP round
// trip itself succeeded (err == nil), so callers must check both.
//
// This holds for every mutation command in this file EXCEPT "add server" —
// see addServerFailed, which has its own success text to check instead.
func haproxyCmdFailed(err error, res string) (string, bool) {
	if err != nil {
		return err.Error(), true
	}
	if msg := strings.TrimSpace(res); msg != "" {
		return msg, true
	}
	return "", false
}

// haproxyAddServerSuccessMsg is the confirmation text HAProxy's Runtime API
// replies with when "add server" succeeds — unlike every other admin server
// command in this file (SetMaintenance, SetDrain, EnableHealth, DelServer,
// WaitSrvRemovable), which reply with an empty body on success — SetServerAddr/
// FQDN is a second exception, see setAddrFailed below — making haproxyCmdFailed's
// "any non-empty response is an error" rule wrong for this one command. Verified
// by hand against a real HAProxy
// 3.0 Runtime API socket (`add server service_read/x 1.2.3.4:3306 check` ->
// "New server registered.\n\n"); routing AddServer's response through
// haproxyCmdFailed instead misclassified every successful add as a failure,
// which meant SetDrain/EnableHealth (completeOrRollbackPendingAdd) never
// ran for it — a server added this way went live in the read backend with
// health checks never enabled, not the drain-then-eligibility-checked path
// the rest of this file's add sequence is designed around. No fake-server
// unit test caught this because startFakeHaproxy replies to every command
// other than "show stat" with an empty body, which happens to be correct
// for every command except this one; see
// TestHaproxyReconcileAddServerSuccessResponseCompletesSequence, which uses
// a custom fake server that replies with the real HAProxy text instead.
const haproxyAddServerSuccessMsg = "New server registered."

// addServerFailed is haproxyCmdFailed's counterpart for "add server" calls
// specifically — see haproxyAddServerSuccessMsg for why it can't share
// haproxyCmdFailed's empty-body-means-success rule.
func addServerFailed(err error, res string) (string, bool) {
	if err != nil {
		return err.Error(), true
	}
	if strings.HasPrefix(strings.TrimSpace(res), haproxyAddServerSuccessMsg) {
		return "", false
	}
	return haproxyCmdFailed(err, res)
}

// haproxyDelServerSuccessMsg is "del server"'s success confirmation text —
// the same non-empty-on-success oddity as haproxyAddServerSuccessMsg, and
// just as easy to miss: found live, against this same haproxy-fr container,
// when reconcileReadBackendServers's own DelServer call (verified by hand:
// `del server service_read/x` -> "Server deleted.\n\n") kept getting logged
// as a failure ("HAProxy could not remove server ...: Server deleted.")
// even though the removal had genuinely succeeded. A stale/decommissioned
// entry that's actually gone doesn't reappear next pass regardless (its row
// is simply absent from the next "show stat"), so this didn't block
// anything — but it did produce a confusing, wrong error log for every
// single successful removal, and would have gone on to poison
// isNonPurgeableReadServer's next-attempt tracking (a call that "fails"
// with the empty-body check leaves the svname eligible for another attempt
// next pass, generating the same false error indefinitely) had a real
// stale entry ever been reconciled with this bug still in place.
const haproxyDelServerSuccessMsg = "Server deleted."

// delServerFailed is haproxyCmdFailed's counterpart for "del server" calls —
// see haproxyDelServerSuccessMsg for why it can't share haproxyCmdFailed's
// empty-body-means-success rule. Note this only covers the DelServer call
// itself: SetMaintenance/WaitSrvRemovable/SetDrain/EnableHealth in the same
// removal sequence really do reply empty on success, so they keep using
// haproxyCmdFailed via logIfFailed.
func delServerFailed(err error, res string) (string, bool) {
	if err != nil {
		return err.Error(), true
	}
	if strings.HasPrefix(strings.TrimSpace(res), haproxyDelServerSuccessMsg) {
		return "", false
	}
	return haproxyCmdFailed(err, res)
}

// haproxyWaitSrvRemovableSuccessMsg is "wait ... srv-removable"'s success
// text — a third instance of the same non-empty-on-success pattern as
// haproxyAddServerSuccessMsg/haproxyDelServerSuccessMsg, verified by hand
// against haproxy:3.0: draining a server (state drain, not maint) and
// waiting reliably replies "Failed.\n\n" (genuinely not removable — DRAIN
// alone isn't enough, only MAINT is), while putting it in maint first
// reliably replies "Done.\n\n" before the timeout. Both are non-empty, so
// routing either through haproxyCmdFailed reports 100% of calls as
// failures — including every real success — which is far worse than the
// AddServer/DelServer cases: this specific misreport ("HAProxy server X did
// not become removable: Done.") looks exactly like a genuine, worrying
// failure to anyone reading the log, when the server did in fact become
// removable exactly as expected.
const haproxyWaitSrvRemovableSuccessMsg = "Done."

// waitSrvRemovableFailed is haproxyCmdFailed's counterpart for "wait ...
// srv-removable" — see haproxyWaitSrvRemovableSuccessMsg. Unlike
// addServerFailed/delServerFailed, a "false" result here (genuinely not yet
// removable, e.g. "Failed.\n\n") isn't a bug to fix around: DelServer is
// attempted right after regardless, and simply fails/retries next pass if
// the server truly isn't idle yet.
func waitSrvRemovableFailed(err error, res string) (string, bool) {
	if err != nil {
		return err.Error(), true
	}
	if strings.HasPrefix(strings.TrimSpace(res), haproxyWaitSrvRemovableSuccessMsg) {
		return "", false
	}
	return haproxyCmdFailed(err, res)
}

// haproxySetAddrSuccessSubstrings is a fourth instance of the same
// non-empty-on-success pattern as haproxyAddServerSuccessMsg/
// haproxyDelServerSuccessMsg/haproxyWaitSrvRemovableSuccessMsg, for
// SetMaster/SetMasterFQDN/SetServerAddr/SetServerFQDN's "set server ...
// addr/fqdn ..." calls -- routing these through haproxyCmdFailed
// misclassified every successful master repoint/read-backend IP fix as an
// ERROR. Matched with strings.Contains, not HasPrefix: the FQDN-changed
// text is "<pool>/<name> changed its FQDN from ... by '...'" -- the
// pool/server name leads the line, so no fixed-word prefix works.
//
// Provenance: "nothing changed"/"no need to change the FDQN" (HAProxy's own
// spelling) and "IP changed from"/"changed its FQDN from" are verified
// against real HAProxy 3.0 output or production logs. "port changed from"
// and the "no need to change the addr/port" pair (HAProxy's v2.x wording,
// pre-3.0, for the same no-op case as "nothing changed") were found by
// reading HAProxy's own source (srv_update_addr_port, src/server.c) across
// 2.4/2.8/3.0/3.4, not live-verified. Unmatched text still falls through to
// haproxyCmdFailed's default (unknown non-empty response = failure).
var haproxySetAddrSuccessSubstrings = []string{
	"nothing changed",
	"no need to change the addr",
	"no need to change the port",
	"no need to change the FDQN",
	"IP changed from",
	"port changed from",
	"changed its FQDN from",
}

// setAddrFailed is haproxyCmdFailed's counterpart for SetMaster/
// SetMasterFQDN/SetServerAddr/SetServerFQDN calls — see
// haproxySetAddrSuccessSubstrings for why it can't share haproxyCmdFailed's
// empty-body-means-success rule.
func setAddrFailed(err error, res string) (string, bool) {
	if err != nil {
		return err.Error(), true
	}
	msg := strings.TrimSpace(res)
	for _, substr := range haproxySetAddrSuccessSubstrings {
		if strings.Contains(msg, substr) {
			return "", false
		}
	}
	return haproxyCmdFailed(err, res)
}

// reconcileReadBackendServers brings the HAProxy read backend's runtime
// server membership in line with cluster.Servers via the Runtime API, so
// that a DB server joining or leaving the cluster does not require an
// HAProxy reload. It is a no-op unless both HaproxyAPIBootstrapServers is
// enabled and the HAProxy version supports it (Phase 1 of issue #1724).
//
// knownToHaproxy is every read-backend svname reported by this pass's "show
// stat" (populated unconditionally in Refresh(), even for entries that don't
// resolve to a known cluster.ServerMonitor — e.g. a decommissioned node —
// since those are exactly the stale entries that need removing).
// addrBySvname is the raw host:port HAProxy has on file for each svname,
// used to detect a server that changed address under the same Id.
// statusBySvname is the raw status column ("MAINT", "DRAIN", "UP", ...)
// HAProxy has on file for each svname this same pass, used to skip a
// redundant SetMaintenance round trip for a svname already known
// non-purgeable AND already confirmed drained — see removeReadBackendServer.
// resolverBackedPool is this same pass's ground truth (from "show servers
// state", keyed "backend/svname") for whether a given entry is currently
// resolver-tracked — see its doc comment above, in Refresh().
func (proxy *HaproxyProxy) reconcileReadBackendServers(haRuntime haproxy.Runtime, knownToHaproxy map[string]bool, addrBySvname map[string]string, statusBySvname map[string]string, resolverBackedPool map[string]bool) {
	cluster := proxy.ClusterGroup
	if !proxy.BootstrapServersEnabled() || !proxy.supportsDynamicServers() {
		return
	}
	// Read-backend servers are only named after server.Id in "runtimeapi"
	// mode; the OpenSVC-driven config path (cluster/prx_get.go) names them
	// positionally ("server1", "server2", ...) in externalcheck/dataplaneapi
	// mode, where this reconciliation would treat real entries as stale.
	// standby never reaches this code at all -- its proxy service is always
	// dispatched to the Localhost* handlers regardless of the cluster's own
	// orchestrator (proxyServiceOrchestrator, cluster/prov.go), and Init()
	// (this file) names its own servers by server.Id too, same as
	// runtimeapi -- but its topology propagation is a full local
	// re-render/reload, never this Runtime API reconciliation.
	if cluster.Conf.HaproxyMode != "runtimeapi" {
		return
	}
	pool := cluster.Conf.HaproxyAPIReadBackend

	// skippedAdds/skippedRemoves count this pass's skips for a single
	// summary SetState call below, rather than logging per server every
	// Refresh() tick: both conditions are persistent for as long as they
	// hold (an unresolved address for adds; a svname HAProxy has already
	// told us is non-purgeable, for removes), so a per-item
	// LogModulePrintf here would otherwise repeat unbounded once per
	// monitoring-ticker for as long as the server stays missing/stale —
	// SetState instead only logs on the OPENED/RESOLV transition (see
	// cluster.SetState / StateMachine.AddState).
	//
	// Note there is no blanket proxy.HasDNS() skip here (unlike an earlier
	// version of this function): GetConfigProxyModule no longer attaches
	// "resolvers dns" to runtimeapi's server lines (cluster/prx_get.go) —
	// runtimeapi drives every member's address itself via
	// ServerMonitor.RuntimeAPIAddr(), so a K8s/OpenSVC proxy is no longer
	// resolver-backed and dynamic add/del works the same as it always has
	// for the Localhost orchestrator. The only remaining reason to skip an
	// add is per-server: no literal address is available for it yet (see
	// the addr check inside the loop below).
	skippedAdds, skippedRemoves := 0, 0

	// logIfFailed runs a Runtime API call and logs+reports whether it
	// failed, either at the transport level or via a non-empty response
	// body (HAProxy's admin server commands return no output on success).
	// Do not use this for AddServer's response — see logAddServerIfFailed.
	// Do not use this for SetServerAddr's response either — see
	// logSetAddrIfFailed.
	logIfFailed := func(action string, res string, err error) bool {
		msg, failed := haproxyCmdFailed(err, res)
		if failed {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy %s: %s", action, msg)
		}
		return failed
	}

	// logAddServerIfFailed is logIfFailed's counterpart for "add server"
	// calls, which reply with a non-empty confirmation on success unlike
	// every other command here — see haproxyAddServerSuccessMsg.
	logAddServerIfFailed := func(action string, res string, err error) bool {
		msg, failed := addServerFailed(err, res)
		if failed {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy %s: %s", action, msg)
		}
		return failed
	}

	// logSetAddrIfFailed is logIfFailed's counterpart for SetServerAddr --
	// see haproxySetAddrSuccessSubstrings. A real failure goes through
	// SetState (scoped per server) instead of a plain log so a persistent
	// failure logs once per OPENED/RESOLV transition, not every pass.
	logSetAddrIfFailed := func(serverID, serverURL, res string, err error) bool {
		msg, failed := setAddrFailed(err, res)
		if failed {
			cluster.SetState("ERR00107", state.State{
				ErrType:   config.LvlErr,
				ErrDesc:   fmt.Sprintf(clusterError["ERR00107"], proxy.Host+":"+proxy.Port, serverID, pool, msg),
				ErrFrom:   "HAPROXY",
				ServerUrl: serverURL,
			})
		}
		return failed
	}

	// Built as its own pass, before any Runtime API work below, so it's
	// always complete for every cluster.Servers entry regardless of the
	// budget check further down — the removal loop's "is this svname still
	// a real cluster member" test depends on it covering all of them, not
	// just however many the add loop reached before its deadline.
	knownToCluster := make(map[string]bool, len(cluster.Servers))
	for _, server := range cluster.Servers {
		if server == nil {
			continue
		}
		knownToCluster[server.Id] = true
	}

	// See haproxyReconcileBudget. Each loop gets its own deadline, not one
	// shared between them — a shared deadline let a sustained add/update
	// backlog (which always runs first) consume the whole budget every
	// pass, starving the removal loop's safety-critical drain indefinitely.
	// This fixes that starvation; it doesn't itself cap total pass time —
	// see haproxyReconcileBudget's doc comment.
	deadlineHit := false
	addDeadline := time.Now().Add(haproxyReconcileBudget)

	for _, server := range cluster.Servers {
		if server == nil {
			continue
		}

		if server.IsMaintenance {
			continue
		}

		if !knownToHaproxy[server.Id] {
			// Not yet present in HAProxy at all, so there's no existing
			// "backend/svname" row to check ground truth against -- and it
			// wouldn't matter anyway: Runtime API "add server" can never
			// attach a "resolvers" clause regardless of what the rest of
			// this backend's entries look like, so a freshly added entry is
			// always literal-IP-only. Always false here, not a
			// resolverBackedPool lookup.
			addr := server.RuntimeAPIAddr(false)
			if net.ParseIP(misc.Unbracket(addr)) == nil {
				// No literal address to add yet — server.IP hasn't been
				// resolved (no successful Ping()/SetCredential() reconnect
				// since this server was configured with an FQDN Host), and
				// a Runtime API "add server" call needs a literal IP:port,
				// not a hostname (see RuntimeAPIAddr's doc comment: there's
				// no "resolvers" clause on runtimeapi's server lines for it
				// to resolve against). Not gated on addDeadline: WARN0209's
				// count must reflect every currently-skipped server
				// regardless of budget, or it could under-report and
				// resolve while still unadded.
				skippedAdds++
				continue
			}
			if time.Now().After(addDeadline) {
				// Only the Runtime API call is gated; cheap accounting
				// elsewhere in this loop still runs regardless.
				deadlineHit = true
				continue
			}

			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy adding server %s to read backend %s via Runtime API", server.URL, pool)

			// weight 100 matches the weight every statically-rendered
			// server line carries (GetConfigProxyModule, cluster/prx_get.go,
			// and addServerTo's ServerDetail in this file's Init()) --
			// without it, HAProxy's Runtime API "add server" falls back to
			// its own default weight of 1, so a dynamically re-added member
			// would be starved to roughly 1% of its fair share of read
			// traffic against its 100-weight siblings until an unrelated
			// full config reload happened to re-render it. Live-reproduced
			// against a real Kubernetes cluster: a replica dropped from
			// monitoring and re-added came back at weight 1/1 instead of
			// 100/100.
			res, err := haRuntime.AddServer(pool, server.Id, addr, server.Port, "check weight 100")
			if logAddServerIfFailed(fmt.Sprintf("could not add server %s to read backend %s", server.URL, pool), res, err) {
				continue
			}

			// Not promoted to ready by any code path in this file until the
			// rest of this sequence is confirmed complete — see
			// pendingReadServers.
			proxy.markPendingReadServer(server.Id)

			// AddServer leaves the server in administrative MAINT
			// regardless of the "check" keyword. It must not go straight to
			// SetReady: this pass's "show stat" loop already decided
			// ready/drain for every server it saw (replication health,
			// ignored-state, masterShouldBeReader()) — a server added this
			// pass was never in that output, so marking it ready here would
			// expose a broken replica, or an ineligible master, a tick
			// early. SetDrain clears MAINT without granting traffic; the
			// next pass sees it as a normal DRAIN entry and applies the
			// same eligibility logic as any other server, once
			// pendingReadServers is cleared below.
			done, exceeded := proxy.completeOrRollbackPendingAdd(haRuntime, pool, server.URL, server.Id, addDeadline, logIfFailed)
			if done {
				proxy.unmarkPendingReadServer(server.Id)
			}
			if exceeded {
				deadlineHit = true
			}
			continue
		}

		// The row is still sitting in HAProxy (knownToHaproxy is true) but
		// a prior add sequence and its rollback both failed. Retry
		// completing the sequence (SetDrain/EnableHealth are idempotent) or
		// removal; stays pending, and therefore out of service, until one
		// succeeds.
		if proxy.isPendingReadServer(server.Id) {
			if time.Now().After(addDeadline) {
				deadlineHit = true
				continue
			}
			done, exceeded := proxy.completeOrRollbackPendingAdd(haRuntime, pool, server.URL, server.Id, addDeadline, logIfFailed)
			if done {
				proxy.unmarkPendingReadServer(server.Id)
			}
			if exceeded {
				deadlineHit = true
			}
			continue
		}

		// Skip entirely when ground truth says this specific svname is
		// still resolver-tracked (e.g. a legacy entry from before this
		// proxy was reprovisioned onto the bootstrap-enabled design, or the
		// live HaproxyAPIBootstrapServers flag was toggled on without a
		// matching reprovision yet — see resolverBackedPool's doc comment
		// in Refresh()): HAProxy's own background resolver already keeps
		// such an entry's address current, and using a plain "addr" update
		// on one instead is actively unsafe, not just redundant —
		// live-reproduced against a real HAProxy 3.0: "set server ... addr"
		// on a resolver-attached entry is accepted immediately, but once
		// its "hold valid" timer next elapses, HAProxy's own resolver
		// reconciles against it and the entry ends up forced into DRAIN
		// with its address cleared back to unset, not simply reverted to
		// the resolved value. The safe way to redirect a resolver-attached
		// entry is "set server ... fqdn" (also live-verified: stable
		// address across multiple hold-valid cycles, no drain) — which
		// needs the FQDN, not an IP, so this function defers to Refresh()'s
		// write-path SetMaster calls (the only ones that use it, via
		// RuntimeAPIAddr's resolverBacked parameter) rather than doing it
		// here too.
		if resolverBackedPool[pool+"/"+server.Id] {
			continue
		}

		// Same Id, but HAProxy's on-file address no longer matches
		// cluster.Servers (e.g. re-IP after reprovisioning, or a
		// Kubernetes pod restart handing out a new overlay IP). Compared
		// against RuntimeAPIAddr(false) (server.IP when resolved, see its
		// doc comment), not server.Host directly: "show stat"'s address
		// column is always the resolved connection IP, never a configured
		// FQDN, so comparing against an FQDN Host would never canonically
		// match and would re-issue a (failing) update every pass. This used
		// to be gated to literal-IP-configured servers only, leaving an
		// FQDN-configured member (the K8s/OpenSVC case) unreconciled —
		// RuntimeAPIAddr() closes that gap by giving every non-resolver-backed
		// server, FQDN or not, a literal address to compare and correct
		// with, without needing a "resolvers" section (repman resolves it
		// itself).
		if addr := server.RuntimeAPIAddr(false); net.ParseIP(misc.Unbracket(addr)) != nil {
			// addr stores IPv6 addresses bracketed (e.g. "[2001:db8::1]");
			// Unbracket then JoinHostPort produces the same canonical form
			// readBackendAddrBySvname above uses, so this comparison isn't
			// permanently mismatched for IPv6.
			expectedAddr := net.JoinHostPort(misc.Unbracket(addr), server.Port)
			if actualAddr, ok := addrBySvname[server.Id]; ok && actualAddr != "" && actualAddr != expectedAddr {
				// Not deadline-gated, like SetMaintenance below: HAProxy
				// keeps routing to actualAddr (possibly a reassigned,
				// unrelated host) until this lands — see
				// haproxyReconcileBudget's doc comment.
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy server %s address changed from %s to %s, updating backend %s via Runtime API", server.Id, actualAddr, expectedAddr, pool)
				res, err := haRuntime.SetServerAddr(pool, server.Id, addr, server.Port)
				logSetAddrIfFailed(server.Id, server.URL, res, err)
			}
		}
	}

	// Remove backend entries for servers no longer part of the cluster
	// (decommissioned nodes). Verified against real HAProxy (2.6/2.8/3.0):
	// "del server" removes a statically bootstrapped entry just as well as
	// a dynamically added one, provided it's drained and idle first — there
	// is no dynamic-only restriction in practice for repman's generated
	// config (tcp mode, "balance leastconn", no per-server "track"
	// references), UNLESS the entry carries a "resolvers" clause (or any
	// other element HAProxy considers a reason to refuse "del server" —
	// see haproxyNonPurgeableServerMsg / isNonPurgeableReadServer above).
	//
	// Deliberately NOT gated on skipAddingMembers (proxy.HasDNS()) the
	// way the add branch above is: draining (SetMaintenance, inside
	// removeReadBackendServer) never touches "resolvers" and always
	// succeeds regardless of DNS config, and it's the safety-critical half
	// of removal — it's what actually stops read traffic from reaching a
	// decommissioned node. Skipping it here on a DNS-backed proxy would
	// leave a decommissioned node serving live read traffic indefinitely,
	// which is worse than the log noise this fix set out to reduce. Only
	// the deletion step is genuinely blocked by "resolvers", and even that
	// isn't proxy-wide: an entry added at runtime never gets "resolvers"
	// attached (Runtime API "add server" can't specify it), so it stays
	// deletable. proxy.isNonPurgeableReadServer, checked inside
	// removeReadBackendServer, is learned per svname from HAProxy's own
	// refusal rather than guessed from HasDNS(), so it doesn't
	// over-suppress a stale entry that's actually still removable.
	//
	// DelServer itself enforces the "no active/idle connections"
	// precondition and fails safely (non-empty response, caught by
	// logIfFailed) if that isn't met, so a stale entry that can't be
	// removed yet is simply retried on the next Refresh() pass.
	//
	// Its own deadline, not addDeadline — see the comment above addDeadline
	// for why sharing one between the two loops let a sustained add
	// backlog starve this one, the safety-critical half, indefinitely.
	removeDeadline := time.Now().Add(haproxyReconcileBudget)
	for svname := range knownToHaproxy {
		if knownToCluster[svname] {
			continue
		}

		// Not gated on the deadline check below — same reasoning as
		// skippedAdds above.
		nonPurgeable := proxy.isNonPurgeableReadServer(svname)
		if nonPurgeable {
			skippedRemoves++

			// Already known non-purgeable and this pass's own "show stat"
			// (zero extra cost) confirms it's still MAINT: nothing to do.
			// Otherwise a redundant SetMaintenance round trip every pass,
			// forever, scaling with how many such entries exist — WARN0209
			// is an ongoing condition, not a one-off. Only skips on a
			// confirmed MAINT status, not merely non-purgeable: if
			// something re-armed it, this falls through and re-drains.
			if statusBySvname[svname] == "MAINT" {
				continue
			}
		}

		// Not deadline-gated: SetMaintenance (inside removeReadBackendServer)
		// always runs for a svname not already confirmed drained — see
		// haproxyReconcileBudget's doc comment for why.
		if !nonPurgeable {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy draining stale server %s from read backend %s via Runtime API", svname, pool)
		}
		removedNow, exceeded := proxy.removeReadBackendServer(haRuntime, pool, svname, removeDeadline, logIfFailed)
		if removedNow {
			proxy.unmarkPendingReadServer(svname)
		}
		if exceeded {
			deadlineHit = true
		}
	}

	// One deduped state per pool per pass: opens once while any add/remove
	// stays skipped, resolves automatically once none do (see
	// skippedAdds/skippedRemoves comment above for why this isn't a plain
	// LogModulePrintf).
	if skippedAdds > 0 || skippedRemoves > 0 {
		cluster.SetState("WARN0209", state.State{
			ErrType: config.LvlWarn,
			ErrDesc: fmt.Sprintf(clusterError["WARN0209"], pool, skippedAdds, skippedRemoves),
			ErrFrom: "HAPROXY",
		})
	}

	// See haproxyReconcileBudget. Same OPENED/RESOLV dedup as WARN0209.
	// deadlineHit only ever covers genuinely deferrable work — AddServer,
	// the pending-add retry (pendingReadServers keeps it out of traffic),
	// and WaitSrvRemovable/DelServer (already drained) — since
	// SetMaintenance and SetServerAddr are never deadline-gated. Hence
	// LvlInfo, not LvlWarn: normal operation under load.
	if deadlineHit {
		cluster.SetState("WARN0210", state.State{
			ErrType: config.LvlInfo,
			ErrDesc: fmt.Sprintf(clusterError["WARN0210"], pool, haproxyReconcileBudget),
			ErrFrom: "HAPROXY",
		})
	}
}

// completeOrRollbackPendingAdd finishes the SetDrain/EnableHealth half of a
// server's add sequence (both idempotent, so retrying an already-succeeded
// step is harmless). If either still fails, it removes the server instead
// of leaving it half-configured for the eligibility logic to find.
//
// Returns done=true when it's safe to call unmarkPendingReadServer: either
// the sequence completed, or the server was removed (and will be re-added
// fresh next pass). Returns done=false when neither happened — the caller
// must leave it pending.
//
// deadline is addDeadline (this function's only caller is the add/update
// loop), checked between each of this function's own Runtime API calls,
// not just once by the caller — otherwise the last server processed in a
// pass could run all of SetDrain/EnableHealth/rollback to completion
// regardless of budget. The rollback removeReadBackendServer call below
// reuses this same addDeadline rather than removeDeadline, since it's
// unwinding a failed add, not the general stale-removal loop.
// deadlineExceeded=true means this stopped early — the caller must still
// count it towards WARN0210 even as the last server processed this pass.
func (proxy *HaproxyProxy) completeOrRollbackPendingAdd(haRuntime haproxy.Runtime, pool string, url string, svname string, deadline time.Time, logIfFailed func(action string, res string, err error) bool) (done bool, deadlineExceeded bool) {
	res, err := haRuntime.SetDrain(svname, pool)
	drainFailed := logIfFailed(fmt.Sprintf("could not drain newly added server %s in backend %s", url, pool), res, err)

	if time.Now().After(deadline) {
		return false, true
	}

	res, err = haRuntime.EnableHealth(pool, svname)
	healthFailed := logIfFailed(fmt.Sprintf("could not enable health checks for server %s in backend %s", url, pool), res, err)

	if !drainFailed && !healthFailed {
		return true, false
	}

	if time.Now().After(deadline) {
		return false, true
	}

	// Either step failing would otherwise leave the server half-configured
	// (still MAINT, or DRAIN with health checks never activated) for the
	// generic eligibility logic to find and promote. Remove it instead so
	// the next pass either re-adds it fresh or retries via
	// pendingReadServers.
	proxy.ClusterGroup.LogModulePrintf(proxy.ClusterGroup.Conf.Verbose, config.ConstLogModHAProxy, config.LvlWarn, "HAProxy add sequence for server %s in backend %s did not complete (drain failed=%v, enable health failed=%v); removing it via Runtime API rather than leaving it half-configured", url, pool, drainFailed, healthFailed)
	removed, removeDeadlineExceeded := proxy.removeReadBackendServer(haRuntime, pool, svname, deadline, logIfFailed)
	if removed {
		return true, removeDeadlineExceeded
	}
	if removeDeadlineExceeded {
		return false, true
	}

	proxy.ClusterGroup.LogModulePrintf(proxy.ClusterGroup.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy could not remove server %s from backend %s after its add sequence failed; it remains blocked from serving read traffic until this succeeds", url, pool)
	return false, false
}

// removeReadBackendServer drains, optionally waits for, and deletes a read
// backend server via the Runtime API, reporting whether the deletion
// actually succeeded. Used both for servers no longer part of the cluster
// and to roll back a server whose AddServer succeeded but a later step in
// the add sequence (SetDrain/EnableHealth) failed — see the add path in
// reconcileReadBackendServers for why leaving it half-configured is unsafe
// rather than merely incomplete.
//
// Draining always runs, even for a svname already known non-purgeable: it's
// what stops read traffic reaching this server, and is never deadline-gated
// (see haproxyReconcileBudget). WaitSrvRemovable/DelServer are skipped once
// a svname is known non-purgeable, to avoid retrying a call HAProxy has
// already told us will fail. If DelServer's response matches
// haproxyNonPurgeableServerMsg for the first time, this marks the svname
// (per svname, not proxy-wide: a runtime-added entry never has "resolvers"
// attached, so it stays removable even on a proxy.HasDNS() proxy).
//
// deadline is removeDeadline (its own, separate from the add/update loop's
// addDeadline — see reconcileReadBackendServers), checked after
// SetMaintenance, before WaitSrvRemovable/DelServer. deadlineExceeded=true
// means this stopped early rather than genuinely failed; the caller must
// still count it towards WARN0210 even as the last server processed.
func (proxy *HaproxyProxy) removeReadBackendServer(haRuntime haproxy.Runtime, pool string, svname string, deadline time.Time, logIfFailed func(action string, res string, err error) bool) (removed bool, deadlineExceeded bool) {
	res, err := haRuntime.SetMaintenance(svname, pool)
	if logIfFailed(fmt.Sprintf("could not drain server %s in backend %s for removal", svname, pool), res, err) {
		return false, false
	}

	if proxy.isNonPurgeableReadServer(svname) {
		return false, false
	}

	if time.Now().After(deadline) {
		return false, true
	}

	// "wait srv-removable" needs HAProxy >= 3.0 ("Unknown command" on
	// 2.6/2.8, verified). Below that, skip straight to DelServer.
	if proxy.supportsWaitRemovable() {
		res, err = haRuntime.WaitSrvRemovable(pool, svname, haproxySrvRemovableWait)
		if msg, failed := waitSrvRemovableFailed(err, res); failed {
			proxy.ClusterGroup.LogModulePrintf(proxy.ClusterGroup.Conf.Verbose, config.ConstLogModHAProxy, config.LvlWarn, "HAProxy server %s in backend %s did not become removable: %s", svname, pool, msg)
		}
		if time.Now().After(deadline) {
			return false, true
		}
	}

	res, err = haRuntime.DelServer(pool, svname)
	if strings.Contains(res, haproxyNonPurgeableServerMsg) {
		proxy.markNonPurgeableReadServer(svname)
	}
	msg, failed := delServerFailed(err, res)
	if failed {
		proxy.ClusterGroup.LogModulePrintf(proxy.ClusterGroup.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy could not remove server %s from backend %s: %s", svname, pool, msg)
	}
	return !failed, false
}

func (cluster *Cluster) setMaintenanceHaproxy(pr *Proxy, server *ServerMonitor) {
	pr.SetMaintenance(server)
}

// SetMaintenance implements the DatabaseProxy interface (cluster/prx.go),
// which has no return value. See setReadBackendMaintenance for the
// read-backend-side success signal Refresh() needs and this interface
// shape can't carry.
func (proxy *HaproxyProxy) SetMaintenance(server *ServerMonitor) {
	proxy.setReadBackendMaintenance(server)
}

// setReadBackendMaintenance does the actual maint/ready Runtime API work for
// SetMaintenance and reports whether the read-backend-side transition
// actually happened. Callers mirroring that outcome into in-memory state
// (Refresh()'s setLastReadBackendStatus/masterReadStatus) must check this:
// the read-ready branch below can no-op (isPendingReadServer) or fail
// (Runtime API error), and forcing the in-memory status regardless could
// feed HasAvailableReader()/masterShouldBeReader() a status that never
// actually took effect in HAProxy, later in the same Refresh() pass.
func (proxy *HaproxyProxy) setReadBackendMaintenance(server *ServerMonitor) bool {
	cluster := proxy.ClusterGroup
	if !cluster.Conf.HaproxyOn {
		return false
	}
	if cluster.Conf.HaproxyMode == "standby" {
		proxy.Init()
		// Init() re-renders and reloads the whole config rather than
		// toggling this one server via the Runtime API — there's no
		// analogous per-server success signal to report here, so treat it
		// as not-confirmed rather than assume it landed.
		return false
	}
	if cluster.Conf.HaproxyMode == "externalcheck" {
		// externalcheck's read-backend eligibility is decided entirely by
		// checkslave's own external-check polling of repman's HTTP
		// handlers (docs.signal18.io: "do nothing but when provisioning,
		// set external check script ... and HAProxy config file using
		// external checks"). Issuing a Runtime API maint/ready call here
		// too, the way the runtimeapi branch below does, would race a
		// second source of truth against checkslave's own decision for
		// the same server — so this mode intentionally does nothing.
		return false
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy set maintenance for server %s ", server.URL)

	haRuntime := haproxy.Runtime{
		Binary:   cluster.Conf.HaproxyBinaryPath,
		SockFile: filepath.Join(proxy.Datadir+"/var", "/haproxy.stats.sock"),
		Port:     proxy.Port,
		Host:     proxy.Host,
	}

	svname := server.Id
	bkr := proxy.GetReadBackendDetail(server)
	if bkr != nil {
		svname = bkr.Svname
	}

	readBackendOK := false
	if server.IsMaintenance {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy set server %s/%s state maint ", server.Id, cluster.Conf.HaproxyAPIReadBackend)
		res, err := haRuntime.SetMaintenance(svname, cluster.Conf.HaproxyAPIReadBackend)
		action := fmt.Sprintf("can not set maintenance %s backend %s", server.URL, cluster.Conf.HaproxyAPIReadBackend)
		if !logSetStateIfFailed(proxy, action, res, err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy set maintenance %s backend %s result: %s", server.URL, cluster.Conf.HaproxyAPIReadBackend, res)
			readBackendOK = true
		}
	} else if proxy.isPendingReadServer(svname) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlWarn, "HAProxy skipping ready for server %s: its Runtime API add sequence never completed successfully", server.URL)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy set server %s/%s state ready ", server.Id, cluster.Conf.HaproxyAPIReadBackend)
		res, err := haRuntime.SetReady(svname, cluster.Conf.HaproxyAPIReadBackend)
		action := fmt.Sprintf("can not set ready %s backend %s", server.URL, cluster.Conf.HaproxyAPIReadBackend)
		if !logSetStateIfFailed(proxy, action, res, err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy set ready %s backend %s result: %s", server.URL, cluster.Conf.HaproxyAPIReadBackend, res)
			readBackendOK = true
		}
	}

	if server.IsMaster() {
		if server.IsMaintenance {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy set maintenance for server %s ", server.URL)

			res, err := haRuntime.SetMaintenance("leader", cluster.Conf.HaproxyAPIWriteBackend)
			if msg, failed := haproxyCmdFailed(err, res); failed {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy can not set maintenance %s backend %s : %s", server.URL, cluster.Conf.HaproxyAPIReadBackend, msg)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy set maintenance result: %s", res)
			}

		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy set ready for server %s ", server.URL)

			res, err := haRuntime.SetReady("leader", cluster.Conf.HaproxyAPIWriteBackend)
			if msg, failed := haproxyCmdFailed(err, res); failed {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy can not set ready %s backend %s : %s", server.URL, cluster.Conf.HaproxyAPIWriteBackend, msg)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy set ready %s backend %s result: %s", server.URL, cluster.Conf.HaproxyAPIWriteBackend, res)
			}
		}
	}

	return readBackendOK
}

func (proxy *HaproxyProxy) Failover() {
	cluster := proxy.ClusterGroup
	if cluster.Conf.HaproxyMode == "runtimeapi" {
		proxy.Refresh()
	}
	// standby and externalcheck both need Init() here for the same reason:
	// Refresh() deliberately never mutates the write backend for either mode
	// (see the runtimeapi-only gates in Refresh() and the comment in Init()'s
	// server loop), so Init()'s delete-then-add-the-leader pass is the ONLY
	// place write-backend membership ever tracks a new leader for them. For
	// externalcheck specifically, without this the old leader is simply left
	// to fail checkmaster after a failover/switchover while the new leader is
	// never added at all -- the write backend ends up with zero UP servers,
	// not just a stale one.
	if cluster.Conf.HaproxyMode == "standby" || cluster.Conf.HaproxyMode == "externalcheck" {
		proxy.Init()
	}
}

func (proxy *HaproxyProxy) BackendsStateChange() {
	proxy.Refresh()

	// Refresh() deliberately never mutates the write backend for
	// haproxy-mode=standby or externalcheck (see the runtimeapi-only gates in
	// Refresh() and the comment in Init()'s server loop) -- but
	// BackendsStateChange() fires on every meaningful server state change
	// (cluster/srv.go), not just on an actual failover/switchover. A replica
	// that breaks replication without the master ever changing (e.g. Slave ->
	// SlaveErr) would otherwise never trigger Init() at all, leaving it stuck
	// in the write backend indefinitely for standby. For externalcheck, the
	// same gap is worse: a leader election that completes AFTER this proxy's
	// initial Init() call (at provisioning, before cluster.Servers even has a
	// leader) would otherwise leave the write backend with zero servers
	// forever, since nothing else ever calls Init() again for externalcheck
	// (Failover() only helps on an actual failover/switchover, not on this
	// startup race). Route this event through Init() too so both cases get
	// reconciled the same way a leadership change already does.
	//
	// Debounced (standbyReloadMinInterval, despite the name also covering
	// externalcheck here) rather than calling Init() directly: this fires
	// once per server per state transition, so several replicas flapping in
	// the same window (e.g. a brief network blip) would otherwise mean one
	// full render+reload per event. A call that lands inside the cooldown
	// only marks standbyReloadPending -- the check at the top of Refresh()
	// guarantees that mark still gets applied (within one more Refresh()
	// pass, which the monitoring loop runs continuously regardless of
	// further state changes) even if flapping stops before the cooldown
	// elapses and no later BackendsStateChange() call ever arrives to retry
	// it.
	if proxy.ClusterGroup.Conf.HaproxyMode != "standby" && proxy.ClusterGroup.Conf.HaproxyMode != "externalcheck" {
		return
	}
	if proxy.shouldRunStandbyInit() {
		proxy.Init()
	}
}

// shouldRunStandbyInit reports whether a standby Init() (render+reload)
// should run now, enforcing standbyReloadMinInterval: within the cooldown it
// returns false and marks standbyReloadPending instead of firing, allowing
// the caller to skip the reload; once the cooldown has elapsed it clears
// pending, stamps lastStandbyInit, and returns true. Used identically by
// BackendsStateChange() (leading edge -- a fresh state-change event) and
// Refresh() (trailing edge -- consuming a mark a prior call left behind), so
// both agree on the same cooldown window and neither can race the other into
// double-firing or losing an update.
func (proxy *HaproxyProxy) shouldRunStandbyInit() bool {
	proxy.Lock.Lock()
	defer proxy.Lock.Unlock()
	if time.Since(proxy.lastStandbyInit) < standbyReloadMinInterval {
		proxy.standbyReloadPending = true
		return false
	}
	proxy.lastStandbyInit = time.Now()
	proxy.standbyReloadPending = false
	return true
}

// hasStandbyReloadPending reports whether a prior shouldRunStandbyInit()
// call was debounced (deferred) rather than fired.
func (proxy *HaproxyProxy) hasStandbyReloadPending() bool {
	proxy.Lock.Lock()
	defer proxy.Lock.Unlock()
	return proxy.standbyReloadPending
}

func (proxy *HaproxyProxy) CertificatesReload() error {
	return nil
}
