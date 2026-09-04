// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//
//	Stephane Varoqui  <svaroqui@gmail.com>
//
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/maxscale"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
	"github.com/spf13/pflag"
)

type MaxscaleProxy struct {
	Proxy
}

func (cluster *Cluster) refreshMaxscale(proxy *MaxscaleProxy) error {
	return proxy.Refresh()
}

func NewMaxscaleProxy(placement int, cluster *Cluster, proxyHost string) *MaxscaleProxy {
	conf := cluster.Conf
	prx := new(MaxscaleProxy)
	prx.Type = config.ConstProxyMaxscale
	prx.SetPlacement(placement, conf.ProvProxAgents, conf.SlapOSMaxscalePartitions, conf.MxsHostsIPV6, conf.MxsJanitorWeights)
	prx.Port = conf.MxsPort
	prx.User = conf.MxsUser
	prx.Pass = conf.MxsPass
	prx.Pass = cluster.Conf.GetDecryptedValue("maxscale-pass")
	prx.ReadPort = conf.MxsReadPort
	prx.WritePort = conf.MxsWritePort
	prx.ReadWritePort = conf.MxsReadWritePort
	prx.Name = proxyHost
	prx.Host = proxyHost
	if conf.ProvNetCNI {
		// Falls back "local" -> "cluster.local" on Kubernetes, matching
		// NewHaproxyProxy/NewProxySQLProxy: prov-orchestrator-cluster's own
		// CLI default otherwise leaves the host one ".svc." segment short of
		// the real Service DNS name and CoreDNS never resolves it.
		domain := conf.ProvOrchestratorCluster
		if cluster.GetOrchestrator() == config.ConstOrchestratorKubernetes {
			domain = k8sClusterDomain(cluster)
		}
		prx.Host = prx.Host + "." + cluster.Name + ".svc." + domain
	}

	return prx
}

func (proxy *MaxscaleProxy) AddFlags(flags *pflag.FlagSet, conf *config.Config) {
	flags.BoolVar(&conf.MxsOn, "maxscale", false, "MaxScale proxy server is query for backend status")
	flags.BoolVar(&conf.CheckFalsePositiveMaxscale, "failover-falsepositive-maxscale", false, "Failover checks that maxscale detect failed master")
	flags.IntVar(&conf.CheckFalsePositiveMaxscaleTimeout, "failover-falsepositive-maxscale-timeout", 14, "Failover checks that maxscale detect failed master")
	flags.BoolVar(&conf.MxsBinlogOn, "maxscale-binlog", false, "Maxscale binlog server topolgy")
	flags.MarkDeprecated("maxscale-monitor", "Deprecate disable maxscale monitoring for 2 nodes cluster")
	flags.BoolVar(&conf.MxsDisableMonitor, "maxscale-disable-monitor", false, "Disable maxscale monitoring and fully drive server state")
	flags.StringVar(&conf.MxsGetInfoMethod, "maxscale-get-info-method", "maxadmin", "How to get infos from Maxscale maxinfo|maxadmin")
	flags.StringVar(&conf.MxsHost, "maxscale-servers", "", "MaxScale hosts ")
	flags.StringVar(&conf.MxsJanitorWeights, "maxscale-janitor-weights", "100", "Weight of each MariaDB maxscale inside janitor proxy")
	flags.StringVar(&conf.MxsPort, "maxscale-port", "6603", "MaxScale admin port")
	flags.StringVar(&conf.MxsUser, "maxscale-user", "admin", "MaxScale admin user")
	flags.StringVar(&conf.MxsPass, "maxscale-pass", "mariadb", "MaxScale admin password")
	flags.IntVar(&conf.MxsWritePort, "maxscale-write-port", 3306, "MaxScale read-write port to leader")
	flags.IntVar(&conf.MxsReadPort, "maxscale-read-port", 3307, "MaxScale load balance read port to all nodes")
	flags.IntVar(&conf.MxsReadWritePort, "maxscale-read-write-port", 3308, "MaxScale load balance read port to all nodes")
	flags.IntVar(&conf.MxsMaxinfoPort, "maxscale-maxinfo-port", 3309, "MaxScale maxinfo plugin http port")
	flags.IntVar(&conf.MxsBinlogPort, "maxscale-binlog-port", 3310, "MaxScale maxinfo plugin http port")
	flags.BoolVar(&conf.MxsServerMatchPort, "maxscale-server-match-port", false, "Match servers running on same host with different port")
	flags.StringVar(&conf.MxsBinaryPath, "maxscale-binary-path", "/usr/sbin/maxscale", "Maxscale binary location")
	flags.StringVar(&conf.MxsHostsIPV6, "maxscale-servers-ipv6", "", "ipv6 bind address ")
	flags.BoolVar(&conf.MxsDebug, "maxscale-debug", true, "Log Maxscale Debug")
	flags.IntVar(&conf.MxsLogLevel, "log-level-maxscale", 1, "Log Maxscale Debug Level")
	flags.StringVar(&conf.MxsMode, "maxscale-mode", "auto", "MaxScale config syntax: auto (detect from maxscale-docker-img tag, fall back to legacy), legacy (pre-2.5: cli/maxinfo routers, MySQLClient protocol), or pinloki (2.5+: pinloki binlogrouter, mariadbprotocol, no cli/maxinfo routers). Both use password= -- passwd= was already rejected as of 2.4.10, the oldest image still pullable")
	// Independent of maxscale-mode: config syntax (2.5 cutoff) and client
	// protocol (2.2 cutoff, when the REST API was introduced) are different
	// version boundaries. Off explicitly opts into the old MaxAdmin TCP
	// protocol for MaxScale older than that.
	flags.BoolVar(&conf.MxsRestApi, "maxscale-rest-api", true, "Use MaxScale's REST API to connect (MaxScale >= 2.2). Disable for older MaxScale, which falls back to the MaxAdmin TCP protocol")
	flags.IntVar(&conf.MxsRestPort, "maxscale-rest-port", 8989, "MaxScale REST API port (only used when maxscale-rest-api is on)")
}

// MaxscaleUsesPinloki resolves conf.MxsMode to a concrete legacy/pinloki
// choice. "auto" parses the tag in ProvProxMaxscaleImg via utils/version:
// MaxScale's versioning went from semver (...2.4, 2.5) to calendar-based
// YY.MM (21.06, 22.08...), but every calendar major is far larger than 2, so
// a plain >= "2.5" comparison holds without special-casing the transition.
// An unparseable tag (e.g. "latest", a private-registry name) falls back to
// legacy, same as the explicit legacy mode.
func (cluster *Cluster) MaxscaleUsesPinloki() bool {
	switch cluster.Conf.MxsMode {
	case "pinloki":
		return true
	case "legacy":
		return false
	}

	// registry[:port]/repo/image[:tag]: isolate the last "/"-segment before
	// splitting on ":", so a registry port isn't read as a version tag.
	lastPart := cluster.Conf.ProvProxMaxscaleImg
	if i := strings.LastIndex(lastPart, "/"); i >= 0 {
		lastPart = lastPart[i+1:]
	}
	tag := ""
	if i := strings.LastIndex(lastPart, ":"); i >= 0 {
		tag = lastPart[i+1:]
	}
	v, tokens := version.NewVersionFromString("maxscale", tag)
	if tokens == 0 {
		return false
	}
	return v.GreaterEqual("2.5")
}

// MaxscaleUsesMaxinfo reports whether maxscale-get-info-method=maxinfo should
// actually be used for proxy. The maxinfo HTTP plugin doesn't exist in
// pinloki-mode MaxScale (2.5+ dropped it, alongside cli/debugcli -- see
// MaxscaleUsesPinloki) -- returns false there and raises WARN0211 instead of
// letting callers dial a maxinfo port nothing is listening on every tick.
func (proxy *MaxscaleProxy) MaxscaleUsesMaxinfo() bool {
	cluster := proxy.ClusterGroup
	if cluster.Conf.MxsGetInfoMethod != "maxinfo" {
		return false
	}
	if cluster.MaxscaleUsesPinloki() {
		cluster.SetState("WARN0211", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0211"], proxy.Name), ErrFrom: "MAXSCALE", ServerUrl: proxy.Name})
		return false
	}
	return true
}

// newMaxscaleClient builds the client for proxy, honoring maxscale-rest-api's
// choice of port/protocol on the non-tunnel path. TunnelPort is only ever
// forwarded to the MaxAdmin port, so the tunnel path always speaks MaxAdmin
// regardless of maxscale-rest-api.
func (proxy *MaxscaleProxy) newMaxscaleClient() maxscale.MaxScale {
	cluster := proxy.ClusterGroup
	if proxy.Tunnel {
		return maxscale.MaxScale{Host: "localhost", Port: strconv.Itoa(proxy.TunnelPort), User: proxy.User, Pass: proxy.Pass, UseRest: false}
	}
	port := proxy.Port
	if cluster.Conf.MxsRestApi {
		port = strconv.Itoa(cluster.Conf.MxsRestPort)
	}
	return maxscale.MaxScale{Host: proxy.Host, Port: port, User: proxy.User, Pass: proxy.Pass, UseRest: cluster.Conf.MxsRestApi}
}

func (proxy *MaxscaleProxy) Refresh() error {
	cluster := proxy.ClusterGroup
	if !cluster.Conf.MxsOn {
		return nil
	}
	m := proxy.newMaxscaleClient()

	if cluster.Conf.MxsOn {
		err := m.Connect()
		if err != nil {
			cluster.SetState("ERR00018", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00018"], err), ErrFrom: "CONF"})
			cluster.StateMachine.CopyOldStateFromUnknowServer(proxy.Name)
			return err
		}
		defer m.Close()
	}
	proxy.BackendsWrite = nil
	proxy.BackendsRead = nil
	for _, server := range cluster.Servers {

		var bke = Backend{
			Host:    server.Host,
			Port:    server.Port,
			Status:  server.State,
			PrxName: server.URL,
		}

		if proxy.MaxscaleUsesMaxinfo() {
			_, err := m.GetMaxInfoServers("http://" + proxy.Host + ":" + strconv.Itoa(cluster.Conf.MxsMaxinfoPort) + "/servers")
			if err != nil {
				cluster.SetState("ERR00020", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00020"], server.URL), ErrFrom: "MON", ServerUrl: proxy.Name})
			}
			srvport, _ := strconv.Atoi(server.Port)
			mxsConnections := 0
			bke.PrxName, bke.PrxStatus, mxsConnections = m.GetMaxInfoServer(server.Host, srvport, server.ClusterGroup.Conf.MxsServerMatchPort)
			bke.PrxConnections = strconv.Itoa(mxsConnections)
			server.MxsServerStatus = bke.PrxStatus
			server.MxsServerName = bke.PrxName

		} else {
			_, err := m.ListServers()
			if err != nil {
				server.ClusterGroup.StateMachine.AddState("ERR00019", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00019"], server.URL), ErrFrom: "MON", ServerUrl: proxy.Name})
			} else {

				if proxy.Tunnel {

					bke.PrxName, bke.PrxStatus, bke.PrxConnections = m.GetServer(server.Host, server.Port, server.ClusterGroup.Conf.MxsServerMatchPort)
					server.MxsServerStatus = bke.PrxStatus
					server.MxsServerName = bke.PrxName

				} else {
					bke.PrxName, bke.PrxStatus, bke.PrxConnections = m.GetServer(server.Host, server.Port, server.ClusterGroup.Conf.MxsServerMatchPort)
					server.MxsServerStatus = bke.PrxStatus
					server.MxsServerName = bke.PrxName
				}
				//server.ClusterGroup.LogModulePrintf(cluster.Conf.Verbose,config.ConstLogModMaxscale,"INFO", "Affect for server %s, %s %s  ", server.IP, server.MxsServerName, server.MxsServerStatus)
			}
		}
		// Write-Connection-Router uses router=readconnroute with router_options=master:
		// every server is a configured candidate (needed so a post-failover master is
		// already known to the router), but only the server MaxScale currently reports
		// as Master ever receives a write connection. Mirror that here instead of
		// listing every candidate as if it were an active write backend.
		if strings.Contains(bke.PrxStatus, "Master") {
			proxy.BackendsWrite = append(proxy.BackendsWrite, bke)
		}
		// Read-Write-Connection-Router's master_accept_reads is driven by
		// GetConfigMaxscaleReadOnMaster(), the same proxy-servers-read-on-master
		// tag ProxySQL and HAProxy already honor (see IsValidReaderCheck() /
		// ShouldServeReadsFromMaster()) -- not a hardcoded moduleset default.
		// Mirror that same cross-proxy policy here instead of a fixed
		// slave-only or master-included rule, so the GUI's read group tracks
		// whatever this cluster is actually configured to do. When the
		// policy excludes the master, it remains MaxScale's own undocumented,
		// unconfigurable emergency fallback if every replica is down -- no
		// per-server state distinguishes that from a healthy master, so it
		// can't be reflected here either way.
		isMasterRole := strings.Contains(bke.PrxStatus, "Master")
		isSlaveRole := strings.Contains(bke.PrxStatus, "Slave")
		if isSlaveRole || (isMasterRole && cluster.ShouldServeReadsFromMaster()) {
			proxy.BackendsRead = append(proxy.BackendsRead, bke)
		}
	}
	return nil
}

// PushMasterAcceptReads live-patches master_accept_reads on this proxy's
// read-write router to match cluster.ShouldServeReadsFromMaster() --
// master_accept_reads is documented "Dynamic: Yes" and live-verified
// (PATCH /v1/services/:name -> 204, GET reflects it immediately, no pod
// restart needed). Called from the proxy-servers-read-on-master /
// -no-slave setting/switch handlers, not from Refresh(): those handlers only
// fire when the setting actually changes, whereas Refresh() runs every
// monitoring tick and would PATCH MaxScale needlessly on every single one.
// REST only: MaxAdmin never exposed runtime parameter changes.
func (proxy *MaxscaleProxy) PushMasterAcceptReads() {
	cluster := proxy.ClusterGroup
	if !cluster.Conf.MxsOn || !cluster.Conf.MxsRestApi {
		return
	}
	m := proxy.newMaxscaleClient()
	if err := m.Connect(); err != nil {
		return
	}
	defer m.Close()
	rwService := "Read-Write-Connection-Router"
	if cluster.MaxscaleUsesPinloki() {
		rwService = "rw-split-router"
	}
	if err := m.SetMasterAcceptReads(rwService, cluster.ShouldServeReadsFromMaster()); err != nil {
		cluster.SetState("ERR00111", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00111"], proxy.Name, rwService, err), ErrFrom: "PRX", ServerUrl: proxy.Name})
	}
}

// PushMaxscaleReadOnMaster live-patches master_accept_reads on every
// MaxScale proxy monitored by this cluster. See
// MaxscaleProxy.PushMasterAcceptReads for why this is called from the
// proxy-servers-read-on-master setting handlers instead of every
// monitoring tick.
func (cluster *Cluster) PushMaxscaleReadOnMaster() {
	for _, p := range cluster.Proxies {
		if mxs, ok := p.(*MaxscaleProxy); ok {
			mxs.PushMasterAcceptReads()
		}
	}
}

func (cluster *Cluster) initMaxscale(proxy DatabaseProxy) {
	proxy.Init()
}

func (proxy *MaxscaleProxy) Init() {
	cluster := proxy.ClusterGroup
	if !cluster.Conf.MxsOn {
		return
	}

	m := proxy.newMaxscaleClient()
	err := m.Connect()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMaxscale, config.LvlErr, "Could not connect to MaxScale:%s", err)
		return
	}
	defer m.Close()
	master := cluster.GetMaster()
	if master == nil {
		return
	}
	if cluster.GetMaster() != nil && cluster.GetMaster().MxsServerName == "" {
		return
	}

	var monitor string
	if proxy.MaxscaleUsesMaxinfo() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMaxscale, config.LvlDbg, "Getting Maxscale monitor via maxinfo")
		m.GetMaxInfoMonitors("http://" + proxy.Host + ":" + strconv.Itoa(cluster.Conf.MxsMaxinfoPort) + "/monitors")
		monitor = m.GetMaxInfoMonitor()

	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMaxscale, config.LvlDbg, "Getting Maxscale monitor via maxadmin")
		_, err := m.ListMonitors()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMaxscale, config.LvlErr, "MaxScale client could not list monitors %s", err)
		}
		monitor = m.GetMonitor()
	}
	// MaxScale's REST API refuses to manually set/clear master/slave/running
	// on a server its own monitor owns -- confirmed live against a real
	// MaxScale 2.4.10: HTTP 403 "The server is monitored, so only the
	// maintenance status can be set/cleared manually. Status was not
	// modified." So repman only drives server state by hand below when
	// there's no monitor to conflict with: either none was found (the
	// ERR00017 case -- MaxScale genuinely has no monitor watching these
	// servers, so repman's manual pushes are the only thing keeping its
	// routing correct), or maxscale-disable-monitor explicitly shut a
	// running one down. A monitor left running (the default, and how both
	// legacy MaxAdmin and REST deployments normally operate -- mariadbmon/
	// galeramon actively watching) is the expected, healthy case, not an
	// error: it already drives these exact states itself, correctly and
	// continuously, without repman's help.
	driveServerStateManually := monitor == ""
	if monitor != "" {
		if cluster.Conf.MxsDisableMonitor {
			cmd := "shutdown monitor \"" + monitor + "\""
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMaxscale, config.LvlInfo, "Maxscale shutdown monitor: %s", cmd)
			err = m.ShutdownMonitor(monitor)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMaxscale, config.LvlErr, "MaxScale client could not shutdown monitor:%s", err)
			}
			m.Response()
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMaxscale, config.LvlErr, "MaxScale client could not shutdown monitor:%s", err)
			}
			driveServerStateManually = true
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMaxscale, config.LvlDbg, "MaxScale monitor %q is running and owns server state; not pushing manual server states", monitor)
		}
	} else {
		cluster.SetState("ERR00017", state.State{ErrType: "ERROR", ErrDesc: clusterError["ERR00017"], ErrFrom: "TOPO", ServerUrl: proxy.Name})
	}

	if !driveServerStateManually {
		return
	}

	err = m.SetServer(cluster.GetMaster().MxsServerName, "master")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMaxscale, config.LvlErr, "MaxScale client could not send command:%s", err)
	}
	err = m.SetServer(cluster.GetMaster().MxsServerName, "running")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMaxscale, config.LvlErr, "MaxScale client could not send command:%s", err)
	}
	err = m.ClearServer(cluster.GetMaster().MxsServerName, "slave")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMaxscale, config.LvlErr, "MaxScale client could not send command:%s", err)
	}

	if !cluster.Conf.MxsBinlogOn {
		for _, s := range cluster.Servers {
			if s != cluster.GetMaster() {

				err = m.ClearServer(s.MxsServerName, "master")
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMaxscale, config.LvlErr, "MaxScale client could not send command:%s", err)
				}

				if s.State != stateSlave {
					err = m.ClearServer(s.MxsServerName, "slave")
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMaxscale, config.LvlErr, "MaxScale client could not send command:%s", err)
					}
					err = m.ClearServer(s.MxsServerName, "running")
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMaxscale, config.LvlErr, "MaxScale client could not send command:%s", err)
					}

				} else {
					err = m.SetServer(s.MxsServerName, "slave")
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMaxscale, config.LvlErr, "MaxScale client could not send command:%s", err)
					}
					err = m.SetServer(s.MxsServerName, "running")
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMaxscale, config.LvlErr, "MaxScale client could not send command:%s", err)
					}

				}
			}
		}
	}
}

func (cluster *Cluster) setMaintenanceMaxscale(pr DatabaseProxy, server *ServerMonitor) {
	pr.SetMaintenance(server)
}

func (proxy *MaxscaleProxy) BackendsStateChange() {
	// TODO
}

func (pr *MaxscaleProxy) SetMaintenance(server *ServerMonitor) {
	cluster := pr.ClusterGroup
	if cluster.GetMaster() != nil {
		return
	}
	if cluster.Conf.MxsOn {
		return
	}
	m := pr.newMaxscaleClient()
	err := m.Connect()
	if err != nil {
		cluster.SetState("ERR00018", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00018"], err), ErrFrom: "CONF"})
	}
	if server.IsMaintenance {
		err = m.SetServer(server.MxsServerName, "maintenance")
	} else {
		err = m.ClearServer(server.MxsServerName, "maintenance")
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModMaxscale, config.LvlErr, "Could not set server %s in maintenance", err)
		m.Close()
	}
	m.Close()
}

// Failover for MaxScale simply calls Init
func (prx *MaxscaleProxy) Failover() {
	prx.Init()
}

func (proxy *MaxscaleProxy) CertificatesReload() error {
	return nil
}
