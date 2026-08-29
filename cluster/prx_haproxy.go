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
}

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
// no-ops entirely) and skipAddingMembers (proxy.HasDNS(), and the add
// branch specifically is skipped even though removal still runs). All
// four must hold for a missing/renamed read-backend row to actually get
// re-added on the next pass — see haproxySetStateLogLevel, the reason this
// exists.
func (proxy *HaproxyProxy) reconcileReadBackendServersActive() bool {
	cluster := proxy.ClusterGroup
	return cluster.Conf.HaproxyAPIBootstrapServers &&
		proxy.supportsDynamicServers() &&
		cluster.Conf.HaproxyMode == "runtimeapi" &&
		!proxy.HasDNS()
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
	if conf.ProvNetCNI {
		// Falls back "local" -> "cluster.local" on Kubernetes, matching
		// NewProxySQLProxy: prov-orchestrator-cluster's own CLI default
		// otherwise leaves the host one ".svc." segment short of the real
		// Service DNS name and CoreDNS never resolves it.
		domain := conf.ProvOrchestratorCluster
		if cluster.GetOrchestrator() == config.ConstOrchestratorKubernetes {
			domain = k8sClusterDomain(cluster)
		}
		prx.Host = prx.Host + "." + cluster.Name + ".svc." + domain
	}
	prx.User = conf.HaproxyUser
	prx.Pass = cluster.Conf.GetDecryptedValue("haproxy-password")

	return prx
}

func (proxy *HaproxyProxy) AddFlags(flags *pflag.FlagSet, conf *config.Config) {
	flags.BoolVar(&conf.HaproxyOn, "haproxy", false, "Wrapper to use HAProxy on same host")
	flags.StringVar(&conf.HaproxyMode, "haproxy-mode", "runtimeapi", "HAProxy mode [standby|runtimeapi|dataplaneapi]")
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
	flags.BoolVar(&conf.HaproxyAPIBootstrapServers, "haproxy-api-bootstrap-servers", false, "Add/remove cluster servers in the HAProxy read backend at runtime via the Runtime API instead of requiring a reload (requires haproxy-mode=runtimeapi and HAProxy >= 2.6; silently inactive otherwise)")
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
	// this (the repman server's) host -- only meaningful for the Localhost
	// orchestrator, where HAProxy actually runs co-located with repman.
	// Every other caller of Init() (setReadBackendMaintenance, Failover,
	// BackendsStateChange) only ever calls it when haproxy-mode=standby, to
	// reconcile that local process; LocalhostProvisionHaProxyService is the
	// only caller that runs regardless of mode, and it's Localhost-only by
	// definition. For OpenSVC/K8s/etc the real proxy's config instead comes
	// from the separate config-fetch tarball path (server/api_database.go,
	// GetProxyConfig() above already covers the one-time bootstrap of that),
	// so none of the rest has anywhere to go.
	if cluster.GetOrchestrator() != config.ConstOrchestratorLocalhost {
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
	bew := haproxy.Backend{Name: cluster.Conf.HaproxyAPIWriteBackend, Mode: "tcp"}
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

	for _, server := range cluster.Servers {
		if server.IsMaintenance {
			continue
		}
		p, _ := strconv.Atoi(server.Port)

		if err := addServerTo(cluster.Conf.HaproxyAPIReadBackend, server, p); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Failed to add server %s to HAProxy backend %s: %s", server.Id, cluster.Conf.HaproxyAPIReadBackend, err)
		}

		// Failover()/switchover for haproxy-mode=standby only ever calls
		// Init() again -- there's no Runtime API patch step afterward -- so
		// this is the one place write-backend membership can track topology
		// at all. Delete-then-add keeps it idempotent across repeated
		// Init() calls on an unchanged leader, and actually drops a server
		// that just lost leadership instead of leaving it (and any
		// never-added replica) stuck UP in the write group.
		haConfig.DeleteServer(cluster.Conf.HaproxyAPIWriteBackend, server.Id)
		if server.IsLeader() {
			if err := addServerTo(cluster.Conf.HaproxyAPIWriteBackend, server, p); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Failed to add server %s to HAProxy backend %s: %s", server.Id, cluster.Conf.HaproxyAPIWriteBackend, err)
			}
		}
	}

	err = haConfig.Render()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Could not create haproxy config %s", err)
	}
	// Reaching here already implies the Localhost orchestrator (see the
	// early return above), so haRuntime.Reload()'s exec of a local haproxy
	// binary against this rendered config's local stats-socket bind address
	// is meaningful.
	if cluster.Conf.HaproxyMode == "standby" {
		if err := haRuntime.SetPid(haConfig.PidFile); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy set pid %s", err)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy reload config on pid %s", haConfig.PidFile)
		}

		err = haRuntime.Reload(&haConfig)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Can't reload haproxy config %s", err)
		}
	}
}

func (proxy *HaproxyProxy) Refresh() error {
	cluster := proxy.ClusterGroup
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
	if proxy.HasDNS() {
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
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Could not read csv from haproxy response")
				return err
			}
			if len(line) > 17 {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "HAProxy adding IP map %s %s", line[4], line[17])
				backend_ip_host[line[4]] = line[17]
				if line[3] != "" && line[17] != "" {
					backend_svname_host[line[3]] = line[17]
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
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "Could not read csv from haproxy response")
			return err
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
			if proxy.HasDNS() {
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
									res, err := haRuntime.SetMaster(cluster.Conf.HaproxyStagingBackend, stagingsrv.Host, stagingsrv.Port)
									if msg, failed := haproxyCmdFailed(err, res); failed {
										cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "%s: %s (staging: %s)", proxy.Host+":"+proxy.Port, msg, stagingsrv.Host+":"+stagingsrv.Port)
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
									res, err := haRuntime.SetMaster(cluster.Conf.HaproxyAPIWriteBackend, master.Host, master.Port)
									if msg, failed := haproxyCmdFailed(err, res); failed {
										cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "%s: %s (master: %s)", proxy.Host+":"+proxy.Port, msg, master.Host+":"+master.Port)
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
			if proxy.HasDNS() {
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
			if srv == nil && proxy.HasDNS() {
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
	if masterReadFound {
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
				res, err := haRuntime.SetMaster(cluster.Conf.HaproxyStagingBackend, stagingsrv.Host, stagingsrv.Port)
				if msg, failed := haproxyCmdFailed(err, res); failed {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "%s: %s (staging: %s)", proxy.Host+":"+proxy.Port, msg, stagingsrv.Host+":"+stagingsrv.Port)
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlDbg, "%s: %s (staging: %s)", proxy.Host+":"+proxy.Port, res, stagingsrv.Host+":"+stagingsrv.Port)
				}
			}
		} else {
			master := cluster.GetMaster()
			if master != nil && master.IsLeader() {
				res, err := haRuntime.SetMaster(cluster.Conf.HaproxyAPIWriteBackend, master.Host, master.Port)
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy has leader in cluster but not in %s fixing it to master %s return %s", proxy.Host+":"+proxy.Port, master.URL, res)
				if msg, failed := haproxyCmdFailed(err, res); failed {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlErr, "HAProxy cannot add leader %s in cluster but not in %s : %s", master.URL, proxy.Host+":"+proxy.Port, msg)
				}
			}
		}

	}

	proxy.reconcileReadBackendServers(haRuntime, readBackendSvnames, readBackendAddrBySvname, readBackendStatusBySvname)

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
// WaitSrvRemovable, SetServerAddr/FQDN), which reply with an empty body on
// success, making haproxyCmdFailed's "any non-empty response is an error"
// rule wrong for this one command. Verified by hand against a real HAProxy
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
func (proxy *HaproxyProxy) reconcileReadBackendServers(haRuntime haproxy.Runtime, knownToHaproxy map[string]bool, addrBySvname map[string]string, statusBySvname map[string]string) {
	cluster := proxy.ClusterGroup
	if !cluster.Conf.HaproxyAPIBootstrapServers || !proxy.supportsDynamicServers() {
		return
	}
	// Read-backend servers are only named after server.Id in "runtimeapi"
	// mode; the OpenSVC-driven config path (cluster/prx_get.go) names them
	// positionally ("server1", "server2", ...) in standby/dataplaneapi mode,
	// where this reconciliation would treat real entries as stale.
	if cluster.Conf.HaproxyMode != "runtimeapi" {
		return
	}
	pool := cluster.Conf.HaproxyAPIReadBackend

	// Adding a new member is unsupported on a resolver-backed config:
	// GetConfigProxyModule appends "resolvers dns" to every bootstrapped
	// server line whenever proxy.HasDNS() is true, but a runtime "add
	// server" call can't attach "resolvers" itself — an entry added that
	// way would silently stop tracking DNS changes, worse than not adding
	// it. This is a blanket, proxy-level skip (unlike removal below): it
	// costs nothing but leaving a replica temporarily out of the dynamic
	// read backend, never a traffic-safety issue, so it's fine to be
	// conservative here rather than try every add and learn reactively.
	// Address updates for IP-based members (below, in the per-server loop)
	// are unaffected — they neither add nor remove anything.
	skipAddingMembers := proxy.HasDNS()
	// skippedAdds/skippedRemoves count this pass's skips for a single
	// summary SetState call below, rather than logging per server every
	// Refresh() tick: both conditions are persistent for as long as they
	// hold (proxy.HasDNS() for adds; a svname HAProxy has already told us
	// is non-purgeable, for removes), so a per-item LogModulePrintf here
	// would otherwise repeat unbounded once per monitoring-ticker for as
	// long as the server stays missing/stale — SetState instead only logs
	// on the OPENED/RESOLV transition (see cluster.SetState /
	// StateMachine.AddState).
	skippedAdds, skippedRemoves := 0, 0

	// logIfFailed runs a Runtime API call and logs+reports whether it
	// failed, either at the transport level or via a non-empty response
	// body (HAProxy's admin server commands return no output on success).
	// Do not use this for AddServer's response — see logAddServerIfFailed.
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
			if skipAddingMembers {
				// Not gated on addDeadline: WARN0209's count must reflect
				// every currently-skipped server regardless of budget, or
				// it could under-report and resolve while still unadded.
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

			res, err := haRuntime.AddServer(pool, server.Id, server.Host, server.Port, "check")
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

		// Same Id, but HAProxy's on-file address no longer matches
		// cluster.Servers (e.g. re-IP after reprovisioning). Only attempted
		// when this server's own Host is a literal IP: "show stat"'s
		// address column is always the resolved connection IP, never a
		// configured FQDN, so an FQDN-configured server.Host would never
		// canonically match and would re-issue an update every pass.
		// Reconciling an FQDN-configured member needs the svname->FQDN
		// mapping Refresh() already builds for DNS lookups, plus a
		// "resolvers" section in haproxy.cfg repman doesn't generate yet
		// for read-backend members — left as follow-up.
		if net.ParseIP(misc.Unbracket(server.Host)) != nil {
			// server.Host stores IPv6 addresses bracketed (e.g.
			// "[2001:db8::1]"); Unbracket then JoinHostPort produces the
			// same canonical form readBackendAddrBySvname above uses, so
			// this comparison isn't permanently mismatched for IPv6.
			expectedAddr := net.JoinHostPort(misc.Unbracket(server.Host), server.Port)
			if actualAddr, ok := addrBySvname[server.Id]; ok && actualAddr != "" && actualAddr != expectedAddr {
				// Not deadline-gated, like SetMaintenance below: HAProxy
				// keeps routing to actualAddr (possibly a reassigned,
				// unrelated host) until this lands — see
				// haproxyReconcileBudget's doc comment.
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModHAProxy, config.LvlInfo, "HAProxy server %s address changed from %s to %s, updating backend %s via Runtime API", server.Id, actualAddr, expectedAddr, pool)
				res, err := haRuntime.SetServerAddr(pool, server.Id, server.Host, server.Port)
				logIfFailed(fmt.Sprintf("could not update address for server %s in backend %s", server.Id, pool), res, err)
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
	if cluster.Conf.HaproxyMode == "standby" {
		proxy.Init()
	}
}

func (proxy *HaproxyProxy) BackendsStateChange() {
	proxy.Refresh()

	// Refresh() deliberately never mutates the write backend for
	// haproxy-mode=standby (see the comment in Init()'s server loop) --
	// but BackendsStateChange() fires on every meaningful server state
	// change (cluster/srv.go), not just on an actual failover/switchover.
	// A replica that breaks replication without the master ever changing
	// (e.g. Slave -> SlaveErr) would otherwise never trigger Init() at
	// all, leaving it stuck in the write backend indefinitely. Route this
	// event through Init() too so it gets reconciled the same way a
	// leadership change already does.
	if proxy.ClusterGroup.Conf.HaproxyMode == "standby" {
		proxy.Init()
	}
}

func (proxy *HaproxyProxy) CertificatesReload() error {
	return nil
}
