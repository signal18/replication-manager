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
	"strconv"

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

	params := fmt.Sprintf("?timeout=%ds", cluster.Conf.Timeout)

	dsn := cluster.dbUser + ":" + cluster.dbPass + "@"
	if prx.GetHost() != "" {
		dsn += "tcp(" + prx.GetHost() + ":" + strconv.Itoa(prx.GetWritePort()) + ")/" + params
	} else {

		return nil, errors.New("No proxies definition")
	}
	conn, err := sqlx.Open("mysql", dsn)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can't get a proxy %s connection: %s", dsn, err)
	}
	return conn, err

}

func (prx *Proxy) GetClusterConnection() (*sqlx.DB, error) {
	cluster := prx.ClusterGroup
	params := fmt.Sprintf("?timeout=%ds", cluster.Conf.Timeout)
	dsn := cluster.dbUser + ":" + cluster.dbPass + "@"
	if cluster.Conf.MonitorWriteHeartbeatCredential != "" {
		dsn = cluster.Conf.MonitorWriteHeartbeatCredential + "@"
	}

	if prx.Host != "" {
		if prx.Tunnel {
			dsn += "tcp(localhost:" + strconv.Itoa(prx.TunnelWritePort) + ")/" + params
		} else {
			dsn += "tcp(" + prx.Host + ":" + strconv.Itoa(prx.WritePort) + ")/" + params
		}
	}
	return sqlx.Open("mysql", dsn)

}

func (proxy *Proxy) GetProxyConfig() string {
	proxy.ClusterGroup.LogPrintf(LvlInfo, "Proxy Config generation "+proxy.Datadir+"/config.tar.gz")
	err := proxy.ClusterGroup.Configurator.GenerateProxyConfig(proxy.Datadir, proxy.ClusterGroup.Conf.WorkingDir+"/"+proxy.ClusterGroup.Name, proxy.GetEnv())
	if err != nil {
		proxy.ClusterGroup.LogPrintf(LvlErr, "Proxy Config generation "+proxy.Datadir+"/config.tar.gz error: %s", err)
	}
	return ""
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
	return "0.0.0.0"
}
func (proxy *Proxy) GetBindAddressExtraIPV6() string {
	if proxy.HostIPV6 != "" {
		return proxy.HostIPV6 + ":" + strconv.Itoa(proxy.WritePort) + ";"
	}
	return ""
}
func (proxy *Proxy) GetUseSSL() string {
	if proxy.ClusterGroup.Configurator.IsFilterInProxyTags("ssl") {
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
	return "/tmp"
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
		"%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%":       proxy.ClusterGroup.dbPass,
		"%%ENV:SVC_CONF_ENV_MYSQL_ROOT_USER%%":           proxy.ClusterGroup.dbUser,
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
		"%%ENV:SVC_CONF_ENV_PORT_RW%%":                   strconv.Itoa(proxy.WritePort),
		"%%ENV:SVC_CONF_ENV_MAXSCALE_MAXINFO_PORT%%":     strconv.Itoa(proxy.ClusterGroup.Conf.MxsMaxinfoPort),
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
		"%%ENV:SVC_CONF_ENV_PROXYSQL_READER_HOSTGROUP%%": proxy.GetConfigProxySQLReaderHostgroup(),
		"%%ENV:SVC_CONF_ENV_PROXYSQL_WRITER_HOSTGROUP%%": proxy.GetConfigProxySQLWriterHostgroup(),
	}
}

func (proxy *Proxy) GetConfigProxySQLReadOnMaster() string {
	if proxy.GetCluster().Configurator.IsFilterInProxyTags("proxy.route.readonmaster") {
		return "1"
	}
	return "0"
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

		if proxy.HasDNS() {
			DNS = " init-addr last,libc,none resolvers dns"
		}
		if proxy.ClusterGroup.Conf.HaproxyMode == "runtimeapi" {
			confhaproxyread += `
    server ` + db.Id + ` ` + misc.Unbracket(db.Host) + `:` + db.Port + DNS + ` weight 100 maxconn 2000 check inter 1000`
			if db.IsMaster() {
				confhaproxywrite += `
    server leader ` + misc.Unbracket(db.Host) + `:` + db.Port + DNS + `  weight 100 maxconn 2000 check inter 1000`
			}
		} else {

			confhaproxyread += `
    server server` + strconv.Itoa(i) + ` ` + misc.Unbracket(db.Host) + `:` + db.Port + `  weight 100 maxconn 2000 check inter 1000`
			confhaproxywrite += `
    server server` + strconv.Itoa(i) + ` ` + misc.Unbracket(db.Host) + `:` + db.Port + `  weight 100 maxconn 2000 check inter 1000`
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
		confhaproxywrite += `
server leader none:3306 ` + DNS + ` weight 100 maxconn 2000 check inter 1000`
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
	return ""
}

func (p *Proxy) GetAgent() string {
	return p.Agent
}

func (p *Proxy) GetType() string {
	return p.Type
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
