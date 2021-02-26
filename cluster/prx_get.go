// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/utils/misc"
)

func (cluster *Cluster) GetProxyFromName(name string) *Proxy {
	for _, pr := range cluster.Proxies {
		if pr.Id == name {
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
	if prx.Host != "" {
		dsn += "tcp(" + prx.Host + ":" + strconv.Itoa(prx.WritePort) + ")/" + params
	} else {

		return nil, errors.New("No proxies definition")
	}
	conn, err := sqlx.Open("mysql", dsn)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can't get a proxy %s connection: %s", dsn, err)
	}
	return conn, err

}

func (cluster *Cluster) GetClusterThisProxyConn(prx *Proxy) (*sqlx.DB, error) {
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

	if proxy.Type == config.ConstProxySpider {
		if proxy.ShardProxy == nil {
			proxy.ClusterGroup.LogPrintf(LvlErr, "Can't get shard proxy config start monitoring")
			proxy.ClusterGroup.ShardProxyBootstrap(proxy)
			return proxy.ShardProxy.GetDatabaseConfig()
		} else {
			return proxy.ShardProxy.GetDatabaseConfig()
		}
	}
	type File struct {
		Path    string `json:"path"`
		Content string `json:"fmt"`
	}
	os.RemoveAll(proxy.Datadir + "/init")
	// Extract files
	for _, rule := range proxy.ClusterGroup.ProxyModule.Rulesets {

		if strings.Contains(rule.Name, "mariadb.svc.mrm.proxy.cnf") {

			for _, variable := range rule.Variables {

				if variable.Class == "file" || variable.Class == "fileprop" {
					var f File
					json.Unmarshal([]byte(variable.Value), &f)
					fpath := strings.Replace(f.Path, "%%ENV:SVC_CONF_ENV_BASE_DIR%%/%%ENV:POD%%", proxy.Datadir+"/init", -1)
					dir := filepath.Dir(fpath)
					//	proxy.ClusterGroup.LogPrintf(LvlInfo, "Config create %s", fpath)
					// create directory
					if _, err := os.Stat(dir); os.IsNotExist(err) {
						err := os.MkdirAll(dir, os.FileMode(0775))
						if err != nil {
							proxy.ClusterGroup.LogPrintf(LvlErr, "Compliance create directory %q: %s", dir, err)
						}
					}
					proxy.ClusterGroup.LogPrintf(LvlInfo, "rule %s filter %s %t", rule.Name, rule.Filter, proxy.IsFilterInTags(rule.Filter))
					if fpath[len(fpath)-1:] != "/" && (proxy.IsFilterInTags(rule.Filter) || rule.Filter == "") {
						content := misc.ExtractKey(f.Content, proxy.GetEnv())
						outFile, err := os.Create(fpath)
						if err != nil {
							proxy.ClusterGroup.LogPrintf(LvlErr, "Compliance create file failed %q: %s", fpath, err)
						} else {
							_, err = outFile.WriteString(content)

							if err != nil {
								proxy.ClusterGroup.LogPrintf(LvlErr, "Compliance writing file failed %q: %s", fpath, err)
							}
							outFile.Close()
							//server.ClusterGroup.LogPrintf(LvlInfo, "Variable name %s", variable.Name)

						}

					}
				}
			}
		}
	}
	// processing symlink
	type Link struct {
		Symlink string `json:"symlink"`
		Target  string `json:"target"`
	}
	for _, rule := range proxy.ClusterGroup.ProxyModule.Rulesets {
		if strings.Contains(rule.Name, "mariadb.svc.mrm.proxy.cnf") {
			for _, variable := range rule.Variables {
				if variable.Class == "symlink" {
					if proxy.IsFilterInTags(rule.Filter) || rule.Name == "mariadb.svc.mrm.proxy.cnf" {
						var f Link
						json.Unmarshal([]byte(variable.Value), &f)
						fpath := strings.Replace(f.Symlink, "%%ENV:SVC_CONF_ENV_BASE_DIR%%/%%ENV:POD%%", proxy.Datadir+"/init", -1)
						if proxy.ClusterGroup.Conf.LogLevel > 2 {
							proxy.ClusterGroup.LogPrintf(LvlInfo, "Config symlink %s", fpath)
						}
						os.Symlink(f.Target, fpath)
						//	keys := strings.Split(variable.Value, " ")
					}
				}
			}
		}
	}

	if proxy.ClusterGroup.HaveProxyTag("docker") {
		err := misc.ChownR(proxy.Datadir+"/init/data", 999, 999)
		if err != nil {
			proxy.ClusterGroup.LogPrintf(LvlErr, "Chown failed %q: %s", proxy.Datadir+"/init/data", err)
		}
	}
	proxy.ClusterGroup.TarGz(proxy.Datadir+"/config.tar.gz", proxy.Datadir+"/init")
	//server.TarAddDirectory(server.Datadir+"/data", tw)
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
	if proxy.IsFilterInTags("ssl") {
		return "true"
	}
	return "false"
}
func (proxy *Proxy) GetUseCompression() string {
	if proxy.IsFilterInTags("nonetworkcompress") {
		return "false"
	}
	return "true"

}

func (proxy *Proxy) GetDatadir() string {
	if proxy.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorSlapOS {
		return proxy.SlapOSDatadir
	}
	return "/tmp"
}

func (proxy *Proxy) GetEnv() map[string]string {
	return map[string]string{
		"%%ENV:NODES_CPU_CORES%%":                      proxy.ClusterGroup.Conf.ProvCores,
		"%%ENV:SVC_CONF_ENV_MAX_CORES%%":               proxy.ClusterGroup.Conf.ProvCores,
		"%%ENV:SVC_CONF_ENV_CRC32_ID%%":                string(proxy.Id[2:10]),
		"%%ENV:SVC_CONF_ENV_SERVER_ID%%":               string(proxy.Id[2:10]),
		"%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%":     proxy.ClusterGroup.dbPass,
		"%%ENV:SVC_CONF_ENV_MYSQL_ROOT_USER%%":         proxy.ClusterGroup.dbUser,
		"%%ENV:SERVER_IP%%":                            proxy.GetBindAddress(),
		"%%ENV:EXTRA_BIND_SERVER_IPV6%%":               proxy.GetBindAddressExtraIPV6(),
		"%%ENV:SVC_CONF_ENV_PROXY_USE_SSL%%":           proxy.GetUseSSL(),
		"%%ENV:SVC_CONF_ENV_PROXY_USE_COMPRESS%%":      proxy.GetUseCompression(),
		"%%ENV:SERVER_PORT%%":                          proxy.Port,
		"%%ENV:SVC_NAMESPACE%%":                        proxy.ClusterGroup.Name,
		"%%ENV:SVC_NAME%%":                             proxy.Name,
		"%%ENV:SERVERS_HAPROXY_WRITE%%":                proxy.GetConfigProxyModule("%%ENV:SERVERS_HAPROXY_WRITE%%"),
		"%%ENV:SERVERS_HAPROXY_READ%%":                 proxy.GetConfigProxyModule("%%ENV:SERVERS_HAPROXY_READ%%"),
		"%%ENV:SERVERS_HAPROXY_WRITE_BACKEND%%":        proxy.ClusterGroup.Conf.HaproxyAPIWriteBackend,
		"%%ENV:SERVERS_HAPROXY_READ_BACKEND%%":         proxy.ClusterGroup.Conf.HaproxyAPIReadBackend,
		"%%ENV:SERVERS_PROXYSQL%%":                     proxy.GetConfigProxyModule("%%ENV:SERVERS_PROXYSQL%%"),
		"%%ENV:SERVERS%%":                              proxy.GetConfigProxyModule("%%ENV:SERVERS%%"),
		"%%ENV:SERVERS_LIST%%":                         proxy.GetConfigProxyModule("%%ENV:SERVERS_LIST%%"),
		"%%ENV:SVC_CONF_ENV_PORT_HTTP%%":               "80",
		"%%ENV:SVC_CONF_ENV_PORT_R_LB%%":               strconv.Itoa(proxy.ReadPort),
		"%%ENV:SVC_CONF_ENV_PORT_RW%%":                 strconv.Itoa(proxy.WritePort),
		"%%ENV:SVC_CONF_ENV_MAXSCALE_MAXINFO_PORT%%":   strconv.Itoa(proxy.ClusterGroup.Conf.MxsMaxinfoPort),
		"%%ENV:SVC_CONF_ENV_PORT_RW_SPLIT%%":           strconv.Itoa(proxy.ReadWritePort),
		"%%ENV:SVC_CONF_ENV_PORT_BINLOG%%":             strconv.Itoa(proxy.ClusterGroup.Conf.MxsBinlogPort),
		"%%ENV:SVC_CONF_ENV_PORT_TELNET%%":             proxy.Port,
		"%%ENV:SVC_CONF_ENV_PORT_ADMIN%%":              proxy.Port,
		"%%ENV:SVC_CONF_ENV_USER_ADMIN%%":              proxy.User,
		"%%ENV:SVC_CONF_ENV_PASSWORD_ADMIN%%":          proxy.Pass,
		"%%ENV:SVC_CONF_ENV_SPHINX_MEM%%":              proxy.ClusterGroup.Conf.ProvSphinxMem,
		"%%ENV:SVC_CONF_ENV_SPHINX_MAX_CHILDREN%%":     proxy.ClusterGroup.Conf.ProvSphinxMaxChildren,
		"%%ENV:SVC_CONF_ENV_VIP_ADDR%%":                proxy.ClusterGroup.Conf.ProvProxRouteAddr,
		"%%ENV:SVC_CONF_ENV_VIP_NETMASK%%":             proxy.ClusterGroup.Conf.ProvProxRouteMask,
		"%%ENV:SVC_CONF_ENV_VIP_PORT%%":                proxy.ClusterGroup.Conf.ProvProxRoutePort,
		"%%ENV:SVC_CONF_ENV_MRM_API_ADDR%%":            proxy.ClusterGroup.Conf.MonitorAddress + ":" + proxy.ClusterGroup.Conf.HttpPort,
		"%%ENV:SVC_CONF_ENV_MRM_CLUSTER_NAME%%":        proxy.ClusterGroup.GetClusterName(),
		"%%ENV:SVC_CONF_ENV_PROXYSQL_READ_ON_MASTER%%": proxy.ProxySQLReadOnMaster(),
		"%%ENV:SVC_CONF_ENV_DATADIR%%":                 proxy.GetDatadir(),
	}
}

func (proxy *Proxy) GetConfigProxyModule(variable string) string {
	confmaxscale := ""
	confmaxscaleserverlist := ""
	confhaproxyread := ""
	confhaproxywrite := ""
	confproxysql := ""
	i := 0
	for _, db := range proxy.ClusterGroup.Servers {

		i++
		if i > 1 {
			confmaxscaleserverlist += ","
			confproxysql += ","
		}
		confmaxscale += `
[server` + strconv.Itoa(i) + `]
type=server
address="` + misc.Unbracket(db.Host) + `
port=` + db.Port + `
protocol=MySQLBackend
`
		if proxy.ClusterGroup.Conf.HaproxyMode == "runtimeapi" {
			confhaproxyread += `
    server ` + db.Id + ` ` + misc.Unbracket(db.Host) + `:` + db.Port + `  weight 100 maxconn 2000 check inter 1000`
			if db.IsMaster() {
				confhaproxywrite += `
    server leader ` + misc.Unbracket(db.Host) + `:` + db.Port + `  weight 100 maxconn 2000 check inter 1000`
			}
		} else {

			confhaproxyread += `
    server server` + strconv.Itoa(i) + ` ` + misc.Unbracket(db.Host) + `:` + db.Port + `  weight 100 maxconn 2000 check inter 1000`
			confhaproxywrite += `
    server server` + strconv.Itoa(i) + ` ` + misc.Unbracket(db.Host) + `:` + db.Port + `  weight 100 maxconn 2000 check inter 1000`
		}
		confproxysql += `
    { address="` + misc.Unbracket(db.Host) + `" , port=` + db.Port + ` , hostgroup=` + strconv.Itoa(proxy.ReaderHostgroup) + `, max_connections=1024 }`

		confmaxscaleserverlist += "server" + strconv.Itoa(i)

	}
	if confhaproxywrite == "" && proxy.ClusterGroup.Conf.HaproxyMode == "runtimeapi" {
		confhaproxywrite += `
server leader unknown:3306  weight 100 maxconn 2000 check inter 1000`
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
