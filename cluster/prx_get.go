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
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	mysqldrv "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/utils/misc"
)

func (cluster *Cluster) GetProxyFromName(name string) DatabaseProxy {
	for _, pr := range cluster.Proxies {
		if pr.GetId() == name {
			return pr
		}
	}
	return nil
}

func (cluster *Cluster) GetClusterProxyConn() (*sqlx.DB, error) {
	if len(cluster.Proxies) == 0 {
		return nil, errors.New("No proxies defined")
	}
	prx := cluster.Proxies[0]
	if prx.GetHost() == "" {
		return nil, errors.New("No proxies definition")
	}

	buildDSN := func(tls bool) string {
		params := fmt.Sprintf("?timeout=%ds", cluster.Conf.Timeout)
		if tls {
			params += ConstTLSSkipVerify
		}
		return cluster.GetDbUser() + ":" + cluster.GetDbPass() + "@" +
			"tcp(" + prx.GetHost() + ":" + strconv.Itoa(prx.GetWritePort()) + ")/" + params
	}

	dsn := buildDSN(cluster.HaveAutoTLS || cluster.HaveDBTLSCert)
	conn, err := sqlx.Open("mysql", dsn)
	if err == nil {
		return conn, nil
	}
	// Auto-detect error 3159: server requires TLS — upgrade and remember for next calls.
	if driverErr, ok := err.(*mysqldrv.MySQLError); ok && driverErr.Number == 3159 && !cluster.HaveAutoTLS && !cluster.HaveDBTLSCert {
		cluster.Lock()
		cluster.HaveAutoTLS = true
		cluster.Unlock()
		conn, err = sqlx.Open("mysql", buildDSN(true))
		if err != nil {
			cluster.Lock()
			cluster.HaveAutoTLS = false
			cluster.Unlock()
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlWarn,
				"Auto-enabled TLS with InsecureSkipVerify=true for proxy cluster connection (error 3159)."+
					" Certificate authenticity is NOT verified — configure monitoring-ssl-ca/cert/key for full TLS validation.")
		}
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr,
			"Can't get a proxy %s connection: %s", dsn, err)
	}
	return conn, err
}

func (prx *Proxy) GetClusterConnection() (*sqlx.DB, error) {
	cluster := prx.ClusterGroup

	buildDSN := func(tls bool) string {
		params := fmt.Sprintf("?timeout=%ds", cluster.Conf.Timeout)
		if tls {
			params += ConstTLSSkipVerify
		}
		creds := cluster.GetDbUser() + ":" + cluster.GetDbPass()
		if cluster.Conf.MonitorWriteHeartbeatCredential != "" {
			creds = cluster.Conf.GetDecryptedValue("monitoring-write-heartbeat-credential")
		}
		dsn := creds + "@"
		if prx.Host != "" {
			if prx.Tunnel {
				dsn += "tcp(localhost:" + strconv.Itoa(prx.TunnelWritePort) + ")/" + params
			} else {
				dsn += "tcp(" + prx.Host + ":" + strconv.Itoa(prx.WritePort) + ")/" + params
			}
		}
		return dsn
	}

	conn, err := sqlx.Open("mysql", buildDSN(cluster.HaveAutoTLS || cluster.HaveDBTLSCert))
	if err == nil {
		return conn, nil
	}
	// Auto-detect error 3159: server requires TLS — upgrade and remember for next calls.
	if driverErr, ok := err.(*mysqldrv.MySQLError); ok && driverErr.Number == 3159 && !cluster.HaveAutoTLS && !cluster.HaveDBTLSCert {
		cluster.Lock()
		cluster.HaveAutoTLS = true
		cluster.Unlock()
		conn, err = sqlx.Open("mysql", buildDSN(true))
		if err != nil {
			cluster.Lock()
			cluster.HaveAutoTLS = false
			cluster.Unlock()
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlWarn,
				"Auto-enabled TLS with InsecureSkipVerify=true for proxy %s (error 3159)."+
					" Certificate authenticity is NOT verified — configure monitoring-ssl-ca/cert/key for full TLS validation.", prx.Host)
		}
	}
	return conn, err
}

func (proxy *Proxy) GetJanitorWeight() string {
	return proxy.Weight
}

func (proxy *Proxy) GetProxyConfig() error {
	cluster := proxy.ClusterGroup
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlInfo, "Proxy Config generation "+proxy.Datadir+"/config.tar.gz")
	err := cluster.Configurator.GenerateProxyConfig(proxy.Datadir, cluster.Conf.WorkingDir+"/"+cluster.Name, proxy.GetEnv(), cluster.RepMgrVersion)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlErr, " "+proxy.Datadir+"/config.tar.gz error: %s", err)
		return err
	}
	return nil
}

func (proxy *Proxy) GetInitContainer(collector opensvc.Collector) string {
	var vm string
	if collector.ProvMicroSrv == "docker" {
		vm = vm + `
[container#0002]
detach = false
type = docker
image = busybox
netns = container#01
start_timeout = 30s
rm = true
volume_mounts = /etc/localtime:/etc/localtime:ro {env.base_dir}/pod01:/data
command = sh -c 'wget -qO- http://{env.mrm_api_addr}/api/clusters/{env.mrm_cluster_name}/servers/{env.ip_pod01}/{env.port_pod01}/config|tar xzvf - -C /data'
optional=true

 `
	}
	return vm
}

func (proxy *Proxy) GetBindAddress() string {
	if proxy.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorSlapOS {
		return proxy.Host
	}
	if proxy.Type == config.ConstProxyHaproxy && proxy.ClusterGroup.Conf.HaproxyHostsIPV6 != "" {
		return proxy.ClusterGroup.Conf.HaproxyHostsIPV6
	}
	return "0.0.0.0"
}
func (proxy *Proxy) GetBindAddressExtraIPV6() string {
	if proxy.HostIPV6 != "" {
		return proxy.HostIPV6 + ":" + strconv.Itoa(proxy.WritePort) + ";"
	}
	return ""
}
func (proxy *Proxy) GetUseSSL() string {
	if proxy.ClusterGroup.Configurator.IsFilterInProxyTags("ssl") || proxy.ClusterGroup.HaveDBTLSCert || proxy.ClusterGroup.HaveAutoTLS {
		return "true"
	}
	return "false"
}
func (proxy *Proxy) GetUseCompression() string {
	if proxy.ClusterGroup.Configurator.IsFilterInProxyTags("nonetworkcompress") {
		return "false"
	}
	return "true"

}

func (proxy *Proxy) GetCausalRead() string {
	if proxy.ClusterGroup.Configurator.IsFilterInProxyTags("causalread") {
		return "causal_reads = local"
	}
	return ""

}

func (proxy *Proxy) GetConfigDatadir() string {
	if proxy.GetOrchestrator() == config.ConstOrchestratorSlapOS {
		return proxy.SlapOSDatadir
	}
	if proxy.GetOrchestrator() == config.ConstOrchestratorOpenSVC {
		return "/var/lib/" + proxy.Type
	}

	return "/var/lib/" + proxy.Type
}

func (proxy *Proxy) GetConfigConfigdir() string {
	if proxy.GetOrchestrator() == config.ConstOrchestratorSlapOS {
		return proxy.SlapOSDatadir + "/etc/" + proxy.GetType()
	}
	return "/etc"
}

func (proxy *Proxy) GetDatadir() string {
	return proxy.Datadir
}

func (proxy *Proxy) GetName() string {
	return proxy.Name
}

func (proxy *ProxySQLProxy) GetEnv() map[string]string {
	env := proxy.GetBaseEnv()
	return env
}

func (proxy *Proxy) GetEnv() map[string]string {
	return proxy.GetBaseEnv()
}

func (proxy *Proxy) GetBaseEnv() map[string]string {
	return map[string]string{
		"%%ENV:NODES_CPU_CORES%%":                        proxy.ClusterGroup.Conf.ProvCores,
		"%%ENV:SVC_CONF_ENV_MAX_CORES%%":                 proxy.ClusterGroup.Conf.ProvCores,
		"%%ENV:SVC_CONF_ENV_CRC32_ID%%":                  string(proxy.Id[2:10]),
		"%%ENV:SVC_CONF_ENV_SERVER_ID%%":                 string(proxy.Id[2:10]),
		"%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%":       proxy.ClusterGroup.GetDbPass(),
		"%%ENV:SVC_CONF_ENV_MYSQL_ROOT_USER%%":           proxy.ClusterGroup.GetDbUser(),
		"%%ENV:SERVER_IP%%":                              proxy.GetBindAddress(),
		"%%ENV:EXTRA_BIND_SERVER_IPV6%%":                 proxy.GetBindAddressExtraIPV6(),
		"%%ENV:SVC_CONF_ENV_PROXY_USE_SSL%%":             proxy.GetUseSSL(),
		"%%ENV:CAUSAL_READ%%":                            proxy.GetCausalRead(),
		"%%ENV:SVC_CONF_ENV_PROXY_USE_COMPRESS%%":        proxy.GetUseCompression(),
		"%%ENV:SERVER_PORT%%":                            proxy.Port,
		"%%ENV:SVC_NAMESPACE%%":                          proxy.ClusterGroup.Name,
		"%%ENV:SVC_NAME%%":                               proxy.Name,
		"%%ENV:SERVERS_HAPROXY_WRITE%%":                  proxy.GetConfigProxyModule("%%ENV:SERVERS_HAPROXY_WRITE%%"),
		"%%ENV:SERVERS_HAPROXY_READ%%":                   proxy.GetConfigProxyModule("%%ENV:SERVERS_HAPROXY_READ%%"),
		"%%ENV:SERVERS_HAPROXY_WRITE_BACKEND%%":          proxy.ClusterGroup.Conf.HaproxyAPIWriteBackend,
		"%%ENV:SERVERS_HAPROXY_READ_BACKEND%%":           proxy.ClusterGroup.Conf.HaproxyAPIReadBackend,
		"%%ENV:SVC_CONF_HAPROXY_DNS%%":                   proxy.GetConfigProxyDNS(),
		"%%ENV:SERVERS_PROXYSQL%%":                       proxy.GetConfigProxyModule("%%ENV:SERVERS_PROXYSQL%%"),
		"%%ENV:SERVERS%%":                                proxy.GetConfigProxyModule("%%ENV:SERVERS%%"),
		"%%ENV:SERVERS_LIST%%":                           proxy.GetConfigProxyModule("%%ENV:SERVERS_LIST%%"),
		"%%ENV:SVC_CONF_ENV_PORT_HTTP%%":                 "80",
		"%%ENV:SVC_CONF_ENV_PORT_R_LB%%":                 strconv.Itoa(proxy.ReadPort),
		"%%ENV:SVC_CONF_ENV_BIND_R_LB%%":                 proxy.ClusterGroup.Conf.HaproxyReadBindIp,
		"%%ENV:SVC_CONF_ENV_PORT_RW%%":                   strconv.Itoa(proxy.WritePort),
		"%%ENV:SVC_CONF_ENV_BIND_RW%%":                   proxy.ClusterGroup.Conf.HaproxyWriteBindIp,
		"%%ENV:SVC_CONF_ENV_MAXSCALE_MAXINFO_PORT%%":     strconv.Itoa(proxy.ClusterGroup.Conf.MxsMaxinfoPort),
		"%%ENV:SVC_CONF_ENV_MAXSCALE_REST_PORT%%":        strconv.Itoa(proxy.ClusterGroup.Conf.MxsRestPort),
		"%%ENV:SVC_CONF_ENV_PORT_RW_SPLIT%%":             strconv.Itoa(proxy.ReadWritePort),
		"%%ENV:SVC_CONF_ENV_PORT_BINLOG%%":               strconv.Itoa(proxy.ClusterGroup.Conf.MxsBinlogPort),
		"%%ENV:SVC_CONF_ENV_PORT_TELNET%%":               proxy.Port,
		"%%ENV:SVC_CONF_ENV_PORT_ADMIN%%":                proxy.Port,
		"%%ENV:SVC_CONF_ENV_USER_ADMIN%%":                proxy.User,
		"%%ENV:SVC_CONF_ENV_PASSWORD_ADMIN%%":            proxy.Pass,
		"%%ENV:SVC_CONF_ENV_SPHINX_MEM%%":                proxy.ClusterGroup.Conf.ProvSphinxMem,
		"%%ENV:SVC_CONF_ENV_SPHINX_MAX_CHILDREN%%":       proxy.ClusterGroup.Conf.ProvSphinxMaxChildren,
		"%%ENV:SVC_CONF_ENV_VIP_ADDR%%":                  proxy.ClusterGroup.Conf.ProvProxRouteAddr,
		"%%ENV:SVC_CONF_ENV_VIP_NETMASK%%":               proxy.ClusterGroup.Conf.ProvProxRouteMask,
		"%%ENV:SVC_CONF_ENV_VIP_PORT%%":                  proxy.ClusterGroup.Conf.ProvProxRoutePort,
		"%%ENV:SVC_CONF_ENV_MRM_API_ADDR%%":              proxy.ClusterGroup.Conf.MonitorAddress + ":" + proxy.ClusterGroup.Conf.HttpPort,
		"%%ENV:SVC_CONF_ENV_MRM_CLUSTER_NAME%%":          proxy.ClusterGroup.GetClusterName(),
		"%%ENV:SVC_CONF_ENV_DATADIR%%":                   proxy.GetConfigDatadir(),
		"%%ENV:SVC_CONF_ENV_CONFDIR%%":                   proxy.GetConfigConfigdir(),
		"%%ENV:SVC_CONF_ENV_PROXYSQL_READ_ON_MASTER%%":   proxy.GetConfigProxySQLReadOnMaster(),
		"%%ENV:SVC_CONF_ENV_MAXSCALE_READ_ON_MASTER%%":   proxy.GetConfigMaxscaleReadOnMaster(),
		"%%ENV:SVC_CONF_ENV_PROXYSQL_READER_HOSTGROUP%%": proxy.GetConfigProxySQLReaderHostgroup(),
		"%%ENV:SVC_CONF_ENV_PROXYSQL_WRITER_HOSTGROUP%%": proxy.GetConfigProxySQLWriterHostgroup(),
		"%%ENV:GENLINE%%":                                fmt.Sprintf("Generated by Signal18 replication-manager %s on %s \n", proxy.ClusterGroup.RepMgrVersion, time.Now().Format(time.RFC3339)),
	}
}

func (proxy *Proxy) GetConfigProxySQLReadOnMaster() string {
	if proxy.GetCluster().Configurator.IsFilterInProxyTags("proxy.route.readonmaster") {
		return "1"
	}
	return "0"
}

// GetConfigMaxscaleReadOnMaster drives master_accept_reads in the moduleset's
// static maxscale.cnf template, using the same 1/0 literal and
// proxy-servers-read-on-master tag as ProxySQL's equivalent above (MaxScale's
// parser accepts 1/0 for booleans same as true/false).
//
// NOT fully equivalent to the live behavior: this only mirrors the always-on
// flag. It ignores proxy-servers-read-on-master-no-slave, which
// cluster.ShouldServeReadsFromMaster() (cluster_has.go) also folds in as a
// live, topology-dependent fallback -- something a static template value
// can't represent.
func (proxy *Proxy) GetConfigMaxscaleReadOnMaster() string {
	return proxy.GetConfigProxySQLReadOnMaster()
}

func (proxy *Proxy) GetConfigProxySQLReaderHostgroup() string {
	return strconv.Itoa(proxy.ReaderHostgroup)
}

func (proxy *Proxy) GetConfigProxySQLWriterHostgroup() string {
	return strconv.Itoa(proxy.WriterHostgroup)
}

func (proxy *Proxy) GetConfigProxyDNS() string {
	if proxy.HasDNS() {
		return `
resolvers dns
 parse-resolv-conf
 resolve_retries       3
 timeout resolve       1s
 timeout retry         1s
 hold other           30s
 hold refused         30s
 hold nx              30s
 hold timeout         30s
 hold valid           10s
 hold obsolete        30s
`
	}

	return ""
}

func (proxy *Proxy) GetConfigProxyModule(variable string) string {
	confmaxscale := ""
	confmaxscaleserverlist := ""
	confhaproxyread := ""
	confhaproxywrite := ""
	confproxysql := ""
	i := 0
	DNS := ""
	for _, db := range proxy.ClusterGroup.Servers {
		if db == nil {
			continue
		}

		i++
		if i > 1 {
			confmaxscaleserverlist += ","
			confproxysql += ","
		}
		confmaxscale += `
[server` + strconv.Itoa(i) + `]
type=server
address=` + misc.Unbracket(db.Host) + `
port=` + db.Port + `
protocol=MariaDBBackend
# protocol=MySQLBackend
`

		runtimeapiAddr := misc.Unbracket(db.Host)
		// runtimeapiHostPort is only used for the bootstrap-enabled
		// runtimeapi branch below, where runtimeapiAddr becomes a literal
		// IP (real or placeholder, both possibly IPv6) that needs proper
		// bracketing in a "host:port" string -- net.JoinHostPort adds
		// brackets for IPv6 automatically, unlike the plain string
		// concatenation the rest of this function uses for the FQDN/literal
		// db.Host case (which was already storing IPv6 hosts pre-bracketed).
		runtimeapiHostPort := runtimeapiAddr + ":" + db.Port
		if proxy.HasDNS() {
			// The non-resolver, repman-IP-driven design below is scoped to
			// HaproxyAPIBootstrapServers, not to haproxy-mode=="runtimeapi"
			// alone: that flag is runtimeapi's existing opt-in for the
			// Runtime-API-driven dynamic add/del lifecycle (Phase 1 of issue
			// #1724, see reconcileReadBackendServers), and it turns out to be
			// exactly the right switch here too. With it off (the default),
			// runtimeapi must keep behaving exactly as it always has —
			// resolver-backed, identical to the else branch below — because
			// reconcileReadBackendServers itself no-ops entirely without it,
			// so nothing would ever correct a non-resolver placeholder
			// address back to the real one. Live-reproduced: rendering the
			// placeholder unconditionally left every read-backend member
			// permanently stuck at 192.0.2.1/DOWN on a real K8s cluster with
			// HaproxyAPIBootstrapServers left at its default (false) — this
			// flag is what makes the two behaviors coherent instead of one
			// silently breaking the other.
			if proxy.ClusterGroup.Conf.HaproxyMode == "runtimeapi" && proxy.ClusterGroup.Conf.HaproxyAPIBootstrapServers {
				// runtimeapi drives every backend member itself over the
				// Runtime API using its own resolved server.IP (see
				// ServerMonitor.RuntimeAPIAddr, reconcileReadBackendServers
				// in cluster/prx_haproxy.go) -- unlike standby/externalcheck,
				// it has a live control channel and doesn't need HAProxy's
				// own background DNS re-resolution to track a changed pod
				// IP, so no "resolvers dns"/"init-addr" clause is needed at
				// all here (that's exactly what makes a server line
				// resolver-attached, which blocks dynamic "add server" and
				// gets "del server" refused -- see reconcileReadBackendServers).
				//
				// db.Host itself (typically a K8s/OpenSVC FQDN) is NOT used
				// as the config-time address, even paired with "init-addr
				// none": live-reproduced against a real HAProxy 3.0 —
				// "init-addr none" leaves the server with no address family
				// assigned at all, and HAProxy's Runtime API then refuses
				// the very first "set server addr" against it ("Update for
				// the current server address family is only supported
				// through configuration file."), the exact command
				// reconcileReadBackendServers/the write-path SetMaster calls
				// need to correct it.
				//
				// If this server already has a resolved IP (db.IP -- kept
				// current by refreshResolvedIP, cluster/srv.go, or by
				// SetCredential's initial resolution), use it directly: a
				// real address needs no placeholder-then-correct dance at
				// all. Otherwise fall back to a literal placeholder that
				// still gives the line a real address family from the
				// start -- inert until corrected, but "set server addr" to
				// another address of the SAME family is allowed at
				// runtime, unlike the family-less "init-addr none" case
				// above. The placeholder's own family must match whatever
				// family this server will actually resolve to, or that
				// same "address family" refusal would just resurface the
				// first time reconcileReadBackendServers/SetMaster tries to
				// correct an IPv4 placeholder to a real IPv6 address (or
				// vice versa) -- ProvUseIpv6 is this cluster's existing
				// signal for which family to expect (see GetBindAddress).
				if db.IP != "" {
					runtimeapiAddr = misc.Unbracket(db.IP)
				} else if proxy.ClusterGroup.Conf.ProvUseIpv6 {
					// 2001:db8::1 (RFC 3849), the IPv6 documentation
					// counterpart to 192.0.2.1 below.
					runtimeapiAddr = "2001:db8::1"
				} else {
					// 192.0.2.1 (RFC 5737 TEST-NET-1, same placeholder as
					// the "no leader yet" fallback below).
					runtimeapiAddr = "192.0.2.1"
				}
				runtimeapiHostPort = net.JoinHostPort(runtimeapiAddr, db.Port)
			} else {
				DNS = " init-addr last,libc,none resolvers dns"
			}
		}
		if proxy.ClusterGroup.Conf.HaproxyMode == "runtimeapi" {
			// DNS is only ever non-empty here without the bootstrap flag
			// (the else branch above) -- with it enabled, runtimeapiHostPort
			// is already a literal placeholder/resolved-address, and DNS
			// stays "".
			confhaproxyread += `
    server ` + db.Id + ` ` + runtimeapiHostPort + DNS + ` weight 100 maxconn 2000 check inter 1000`
			if db.IsMaster() {
				confhaproxywrite += `
    server leader ` + runtimeapiHostPort + DNS + `  weight 100 maxconn 2000 check inter 1000`
			}
		} else {

			confhaproxyread += `
    server server` + strconv.Itoa(i) + ` ` + misc.Unbracket(db.Host) + `:` + db.Port + DNS + `  weight 100 maxconn 2000 check inter 1000`
			confhaproxywrite += `
    server server` + strconv.Itoa(i) + ` ` + misc.Unbracket(db.Host) + `:` + db.Port + DNS + `  weight 100 maxconn 2000 check inter 1000`
		}
		UseSSL := "0"
		if proxy.ClusterGroup.Configurator.HaveDBTag("ssl") {
			UseSSL = "1"
		}
		confproxysql += `
    { address="` + misc.Unbracket(db.Host) + `" , port=` + db.Port + ` , hostgroup=` + strconv.Itoa(proxy.ReaderHostgroup) + `, max_connections=1024, use_ssl=` + UseSSL + `}`

		confmaxscaleserverlist += "server" + strconv.Itoa(i)

	}
	if confhaproxywrite == "" && proxy.ClusterGroup.Conf.HaproxyMode == "runtimeapi" {
		// 192.0.2.1 (RFC 5737 TEST-NET-1) or, for an IPv6 deployment,
		// 2001:db8::1 (RFC 3849) -- not the literal hostname "none": HAProxy
		// refuses to start trying to resolve "none" as a real hostname
		// otherwise -- live-reproduced against HaproxyProxy.Init()'s
		// equivalent fallback in cluster/prx_haproxy.go, which this mirrors.
		// No "init-addr" clause needed: it's already a literal IP, so
		// there's nothing to defer resolving -- see runtimeapiAddr above for
		// why a hostname paired with "init-addr none" doesn't work here (the
		// Runtime API refuses to give an address-family-less server its
		// first "set server addr"). The placeholder family must match
		// ProvUseIpv6 for the same reason it does above: correcting it to a
		// real address of a DIFFERENT family would hit that same refusal.
		// Unconditional (not built from the loop's runtimeapiAddr, which is
		// never set at all when cluster.Servers is empty) so this fallback
		// is safe on its own regardless of how it's reached.
		placeholder := "192.0.2.1"
		if proxy.ClusterGroup.Conf.ProvUseIpv6 {
			placeholder = "2001:db8::1"
		}
		confhaproxywrite += `
server leader ` + net.JoinHostPort(placeholder, "3306") + ` weight 100 maxconn 2000 check inter 1000`
	}
	switch variable {
	case "%%ENV:SERVERS_HAPROXY_WRITE%%":
		return confhaproxywrite
	case "%%ENV:SERVERS_HAPROXY_READ%%":
		return confhaproxyread
	case "%%ENV:SERVERS_PROXYSQL%%":
		return confproxysql
	case "%%ENV:SERVERS%%":
		return confmaxscale
	case "%%ENV:SERVERS_LIST%%":
		return confmaxscaleserverlist
	default:
		return ""
	}
}

func (p *Proxy) GetAgent() string {
	return p.Agent
}

func (p *Proxy) GetType() string {
	return p.Type
}

// GetVersion reads Version under p.Lock -- see SetVersion.
func (p *Proxy) GetVersion() string {
	p.Lock.Lock()
	defer p.Lock.Unlock()
	return p.Version
}

func (p *Proxy) GetHost() string {
	return p.Host
}

func (p *Proxy) GetPort() string {
	return p.Port
}

func (p *Proxy) GetWritePort() int {
	return p.WritePort
}

func (p *Proxy) GetReadWritePort() int {
	return p.ReadWritePort
}

func (p *Proxy) GetReadPort() int {
	return p.ReadPort
}

func (p *Proxy) GetId() string {
	return p.Id
}

func (p *Proxy) GetState() string {
	return p.State
}

func (p *Proxy) GetUser() string {
	return p.User
}

func (p *Proxy) GetPass() string {
	return p.Pass
}

func (p *Proxy) GetFailCount() int {
	return p.FailCount
}

func (p *Proxy) GetPrevState() string {
	return p.PrevState
}

func (p *Proxy) GetOrchestrator() string {
	return p.GetCluster().Conf.ProvOrchestrator
}

func (p *Proxy) GetServiceName() string {
	return p.GetCluster().GetName() + "/svc/" + p.GetName()
}

func (p *Proxy) GetCluster() *Cluster {
	return p.ClusterGroup
}

func (p *Proxy) GetURL() string {
	return p.GetHost() + ":" + p.GetPort()
}

func (p *Proxy) GetSshEnv() string {
	/*
		REPLICATION_MANAGER_USER
		REPLICATION_MANAGER_PASSWORD
		REPLICATION_MANAGER_URL
		REPLICATION_MANAGER_CLUSTER_NAME
		REPLICATION_MANAGER_HOST_NAME
		REPLICATION_MANAGER_HOST_USER
		REPLICATION_MANAGER_HOST_PASSWORD
		REPLICATION_MANAGER_HOST_PORT
		REPLICATION_MANAGER_HOST_TYPE
	*/
	adminuser := "admin"
	adminpassword := "repman"
	if user, ok := p.ClusterGroup.APIUsers[adminuser]; ok {
		adminpassword = user.Password
	}
	return "export REPLICATION_MANAGER_HOST_USER=\"" + p.GetUser() + "\";export REPLICATION_MANAGER_HOST_PASSWORD=\"" + p.GetPass() + "\";export REPLICATION_MANAGER_URL=\"https://" + p.ClusterGroup.Conf.MonitorAddress + ":" + p.ClusterGroup.Conf.APIPort + "\";export REPLICATION_MANAGER_USER=\"" + adminuser + "\";export REPLICATION_MANAGER_PASSWORD=\"" + adminpassword + "\";export REPLICATION_MANAGER_HOST_NAME=\"" + p.GetHost() + "\";export REPLICATION_MANAGER_HOST_PORT=\"" + p.GetPort() + "\";export REPLICATION_MANAGER_HOST_TYPE=\"" + p.Type + "\";export REPLICATION_MANAGER_CLUSTER_NAME=\"" + p.ClusterGroup.Name + "\"\n"
}

func (p *Proxy) GetReadBackendDetail(srv *ServerMonitor) *Backend {
	if srv == nil {
		return nil
	}

	for _, b := range p.BackendsRead {
		if b.Host == srv.Host && b.Port == srv.Port {
			return &b
		}
	}

	return nil
}

func (p *Proxy) GetWorkingAgent() string {
	p.workingAgentMu.RLock()
	defer p.workingAgentMu.RUnlock()
	return p.WorkingAgent
}
