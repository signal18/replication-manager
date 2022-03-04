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
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/router/haproxy"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/spf13/pflag"
)

type HaproxyProxy struct {
	Proxy
}

func NewHaproxyProxy(placement int, cluster *Cluster, proxyHost string) *HaproxyProxy {
	conf := cluster.Conf
	prx := new(HaproxyProxy)
	prx.SetPlacement(placement, conf.ProvProxAgents, conf.SlapOSHaProxyPartitions, conf.HaproxyHostsIPV6)
	prx.Type = config.ConstProxyHaproxy
	prx.Port = strconv.Itoa(conf.HaproxyAPIPort)
	prx.ReadPort = conf.HaproxyReadPort
	prx.WritePort = conf.HaproxyWritePort
	prx.ReadWritePort = conf.HaproxyWritePort
	prx.Name = proxyHost
	prx.Host = proxyHost
	if conf.ProvNetCNI {
		prx.Host = prx.Host + "." + cluster.Name + ".svc." + conf.ProvOrchestratorCluster
	}

	return prx
}

func (proxy *HaproxyProxy) AddFlags(flags *pflag.FlagSet, conf *config.Config) {
	flags.BoolVar(&conf.HaproxyOn, "haproxy", false, "Wrapper to use HaProxy on same host")
	flags.StringVar(&conf.HaproxyMode, "haproxy-mode", "runtimeapi", "HaProxy mode [standby|runtimeapi|dataplaneapi]")
	flags.BoolVar(&conf.HaproxyDebug, "haproxy-debug", false, "Extra info on monitoring backend")
	flags.StringVar(&conf.HaproxyUser, "haproxy-user", "admin", "Haproxy API user")
	flags.StringVar(&conf.HaproxyPassword, "haproxy-password", "admin", "Haproxy API password")
	flags.StringVar(&conf.HaproxyHosts, "haproxy-servers", "127.0.0.1", "HaProxy hosts")
	flags.IntVar(&conf.HaproxyAPIPort, "haproxy-api-port", 1999, "HaProxy runtime api port")
	flags.IntVar(&conf.HaproxyWritePort, "haproxy-write-port", 3306, "HaProxy read-write port to leader")
	flags.IntVar(&conf.HaproxyReadPort, "haproxy-read-port", 3307, "HaProxy load balance read port to all nodes")
	flags.IntVar(&conf.HaproxyStatPort, "haproxy-stat-port", 1988, "HaProxy statistics port")
	flags.StringVar(&conf.HaproxyBinaryPath, "haproxy-binary-path", "/usr/sbin/haproxy", "HaProxy binary location")
	flags.StringVar(&conf.HaproxyReadBindIp, "haproxy-ip-read-bind", "0.0.0.0", "HaProxy input bind address for read")
	flags.StringVar(&conf.HaproxyWriteBindIp, "haproxy-ip-write-bind", "0.0.0.0", "HaProxy input bind address for write")
	flags.StringVar(&conf.HaproxyAPIReadBackend, "haproxy-api-read-backend", "service_read", "HaProxy API backend name used for read")
	flags.StringVar(&conf.HaproxyAPIWriteBackend, "haproxy-api-write-backend", "service_write", "HaProxy API backend name used for write")
	flags.StringVar(&conf.HaproxyHostsIPV6, "haproxy-servers-ipv6", "", "ipv6 bind address ")
}

func (proxy *HaproxyProxy) Init() {
	cluster := proxy.ClusterGroup
	haproxydatadir := proxy.Datadir + "/var"

	if _, err := os.Stat(haproxydatadir); os.IsNotExist(err) {
		proxy.GetProxyConfig()
		os.Symlink(proxy.Datadir+"/init/data", haproxydatadir)
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

	cluster.LogPrintf(LvlInfo, "Haproxy loading haproxy config at %s", haproxydatadir)
	err := haConfig.GetConfigFromDisk()
	if err != nil {
		cluster.LogPrintf(LvlInfo, "Haproxy did not find an haproxy config...initializing new config")
		haConfig.InitializeConfig()
	}
	few := haproxy.Frontend{Name: "my_write_frontend", Mode: "tcp", DefaultBackend: cluster.Conf.HaproxyAPIWriteBackend, BindPort: cluster.Conf.HaproxyWritePort, BindIp: cluster.Conf.HaproxyWriteBindIp}
	if err := haConfig.AddFrontend(&few); err != nil {
		cluster.LogPrintf(LvlErr, "Failed to add frontend write ")
	} else {
		if err := haConfig.AddFrontend(&few); err != nil {
			cluster.LogPrintf(LvlErr, "Haproxy should return nil on already existing frontend")
		}

	}
	if result, _ := haConfig.GetFrontend("my_write_frontend"); result.Name != "my_write_frontend" {
		cluster.LogPrintf(LvlErr, "Haproxy failed to add frontend write")
	}
	bew := haproxy.Backend{Name: cluster.Conf.HaproxyAPIWriteBackend, Mode: "tcp"}
	haConfig.AddBackend(&bew)

	if _, err := haConfig.GetServer(cluster.Conf.HaproxyAPIWriteBackend, "leader"); err != nil {
		// log.Printf("No leader")
	} else {
		// log.Printf("Found exiting leader removing")
	}

	if cluster.GetMaster() != nil {

		p, _ := strconv.Atoi(cluster.GetMaster().Port)
		s := haproxy.ServerDetail{Name: "leader", Host: cluster.GetMaster().Host, Port: p, Weight: 100, MaxConn: 2000, Check: true, CheckInterval: 1000}
		if err = haConfig.AddServer(cluster.Conf.HaproxyAPIWriteBackend, &s); err != nil {
			//	log.Printf("Failed to add server to service_write ")
		}
	} else {
		s := haproxy.ServerDetail{Name: "leader", Host: "unknown", Port: 3306, Weight: 100, MaxConn: 2000, Check: true, CheckInterval: 1000}
		if err = haConfig.AddServer(cluster.Conf.HaproxyAPIWriteBackend, &s); err != nil {
			//	log.Printf("Failed to add server to service_write ")
		}
	}
	fer := haproxy.Frontend{Name: "my_read_frontend", Mode: "tcp", DefaultBackend: cluster.Conf.HaproxyAPIReadBackend, BindPort: cluster.Conf.HaproxyReadPort, BindIp: cluster.Conf.HaproxyReadBindIp}
	if err := haConfig.AddFrontend(&fer); err != nil {
		cluster.LogPrintf(LvlErr, "Haproxy failed to add frontend read")
	} else {
		if err := haConfig.AddFrontend(&fer); err != nil {
			cluster.LogPrintf(LvlErr, "Haproxy should return nil on already existing frontend")
		}
	}
	if result, _ := haConfig.GetFrontend("my_read_frontend"); result.Name != "my_read_frontend" {
		cluster.LogPrintf(LvlErr, "Haproxy failed to get frontend")
	}
	/* End add front end */

	ber := haproxy.Backend{Name: cluster.Conf.HaproxyAPIReadBackend, Mode: "tcp"}
	if err := haConfig.AddBackend(&ber); err != nil {
		cluster.LogPrintf(LvlErr, "Haproxy failed to add backend for "+cluster.Conf.HaproxyAPIReadBackend)
	}

	//var checksum64 string
	//	crcHost := crc64.MakeTable(crc64.ECMA)
	for _, server := range cluster.Servers {
		if server.IsMaintenance == false {
			p, _ := strconv.Atoi(server.Port)
			//		checksum64 := fmt.Sprintf("%d", crc64.Checksum([]byte(server.Host+":"+server.Port), crcHost))
			s := haproxy.ServerDetail{Name: server.Id, Host: server.Host, Port: p, Weight: 100, MaxConn: 2000, Check: true, CheckInterval: 1000}
			if err := haConfig.AddServer(cluster.Conf.HaproxyAPIReadBackend, &s); err != nil {
				cluster.LogPrintf(LvlErr, "Failed to add server in Haproxy for "+cluster.Conf.HaproxyAPIReadBackend)
			}
		}
	}

	err = haConfig.Render()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Could not create haproxy config %s", err)
	}
	if cluster.Conf.HaproxyMode == "standby" {
		if err := haRuntime.SetPid(haConfig.PidFile); err != nil {
			cluster.LogPrintf(LvlInfo, "Haproxy set pid %s", err)
		} else {
			cluster.LogPrintf(LvlInfo, "Haproxy reload config on pid %s", haConfig.PidFile)
		}

		err = haRuntime.Reload(&haConfig)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can't reload haproxy config %s", err)
		}
	}
}

func (proxy *HaproxyProxy) Refresh() error {
	cluster := proxy.ClusterGroup
	// if proxy.ClusterGroup.Conf.HaproxyStatHttp {

	/*
		url := "http://" + proxy.Host + ":" + proxy.Port + "/stats;csv"
		client := &http.Client{
			Timeout: time.Duration(2 * time.Second),
		}

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			cluster.sme.AddState("ERR00052", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00052"], err), ErrFrom: "MON"})
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			cluster.sme.AddState("ERR00052", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00052"], err), ErrFrom: "MON"})
			return err
		}
		defer resp.Body.Close()
		reader := csv.NewReader(resp.Body)

	*/
	//tcpAddr, err := net.ResolveTCPAddr("tcp4", proxy.Host+":"+proxy.Port)
	//cluster.LogPrintf(LvlErr, "haproxy entering  refresh: ")

	haproxydatadir := proxy.Datadir + "/var"
	haproxysockFile := "haproxy.stats.sock"

	haRuntime := haproxy.Runtime{
		Binary:   cluster.Conf.HaproxyBinaryPath,
		SockFile: filepath.Join(haproxydatadir, "/", haproxysockFile),
		Port:     proxy.Port,
		Host:     proxy.Host,
	}

	backend_ip_host := make(map[string]string)
	if proxy.HasDNS() {
		// When using FQDN map server state host->IP to locate in show stats where it's only IPs
		cmd := "show servers state"

		showleaderstate, err := haRuntime.ApiCmd(cmd)
		if err != nil {
			cluster.sme.AddState("ERR00052", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00052"], err), ErrFrom: "MON"})
			return err
		}

		// API return a first row with return code make it as comment
		showleaderstate = "# " + showleaderstate

		// API return space sparator conveting to csv
		showleaderstate = strings.Replace(showleaderstate, " ", ",", -1)
		if cluster.Conf.HaproxyDebug {
			cluster.LogPrintf(LvlInfo, "haproxy show servers state response :%s", showleaderstate)
		}
		showleaderstatereader := ioutil.NopCloser(bytes.NewReader([]byte(showleaderstate)))

		defer showleaderstatereader.Close()
		reader := csv.NewReader(showleaderstatereader)
		reader.Comment = '#'
		for {
			line, error := reader.Read()
			if error == io.EOF {
				break
			} else if error != nil {
				cluster.LogPrintf(LvlErr, "Could not read csv from haproxy response")
				return err
			}
			if len(line) > 17 {
				if cluster.Conf.HaproxyDebug {
					cluster.LogPrintf(LvlInfo, "HaProxy adding IP map %s %s", line[4], line[17])
				}
				backend_ip_host[line[4]] = line[17]
			}
		}

	}

	result, err := haRuntime.ApiCmd("show stat")

	if err != nil {
		cluster.AddSugarState("ERR00052", "MON", "", err)
		return err
	}
	if cluster.Conf.HaproxyDebug {
		cluster.LogPrintf(LvlInfo, "Haproxy show stat result: %s", result)
	}
	r := ioutil.NopCloser(bytes.NewReader([]byte(result)))
	defer r.Close()
	reader := csv.NewReader(r)

	proxy.BackendsWrite = nil
	proxy.BackendsRead = nil
	foundMasterInStat := false
	for {
		line, error := reader.Read()
		if error == io.EOF {
			break
		} else if error != nil {
			cluster.LogPrintf(LvlErr, "Could not read csv from haproxy response")
			return err
		}
		if len(line) < 73 {
			cluster.SetSugarState("WARN0078", "MON", "", err)
			return errors.New(clusterError["WARN0078"])
		}
		if strings.Contains(strings.ToLower(line[0]), "write") {
			host := line[73]
			if proxy.HasDNS() {
				// After provisioning the stats may arrive with IP:Port while sometime not
				host = strings.Split(line[73], ":")[0]
				host = backend_ip_host[host]
			}

			srv := cluster.GetServerFromURL(host)
			if cluster.Conf.HaproxyDebug {
				cluster.LogPrintf(LvlInfo, "HaProxy stat lookup writer: host %s translated to %s", line[73], host)
			}
			if srv != nil {
				foundMasterInStat = true
				proxy.BackendsWrite = append(proxy.BackendsWrite, Backend{
					Host:           srv.Host,
					Port:           srv.Port,
					Status:         srv.State,
					PrxName:        line[73],
					PrxStatus:      line[17],
					PrxConnections: line[5],
					PrxByteIn:      line[8],
					PrxByteOut:     line[9],
					PrxLatency:     line[61], //ttime: average session time in ms over the 1024 last requests
				})
				if !srv.IsMaster() {
					master := cluster.GetMaster()
					if master != nil {
						cluster.LogPrintf(LvlInfo, "Detecting wrong master server in haproxy %s fixing it to master %s %s", proxy.Host+":"+proxy.Port, master.Host, master.Port)
						haRuntime.SetMaster(master.Host, master.Port)
					}
				}
			}
		}
		if strings.Contains(strings.ToLower(line[0]), "read") {
			host := line[73]
			if proxy.HasDNS() {
				// After provisioning the stats may arrive with  IP:Port while sometime not
				host = strings.Split(line[73], ":")[0]
				host = backend_ip_host[host]
			}
			srv := cluster.GetServerFromURL(host)
			if cluster.Conf.HaproxyDebug {
				cluster.LogPrintf(LvlInfo, "HaProxy stat lookup reader: host %s translated to %s", line[73], host)
			}
			if srv != nil {

				proxy.BackendsRead = append(proxy.BackendsRead, Backend{
					Host:           srv.Host,
					Port:           srv.Port,
					Status:         srv.State,
					PrxName:        line[73],
					PrxStatus:      line[17],
					PrxConnections: line[5],
					PrxByteIn:      line[8],
					PrxByteOut:     line[9],
					PrxLatency:     line[61],
				})
				if (srv.State == stateSlaveErr || srv.State == stateRelayErr || srv.State == stateSlaveLate || srv.State == stateRelayLate || srv.IsIgnored()) && line[17] == "UP" {
					cluster.LogPrintf(LvlInfo, "HaProxy detecting broken resplication and UP state in haproxy %s drain  server %s", proxy.Host+":"+proxy.Port, srv.URL)
					haRuntime.SetDrain(srv.Id, cluster.Conf.HaproxyAPIReadBackend)
				}
				if (srv.State == stateSlave || srv.State == stateRelay) && line[17] == "DRAIN" && !srv.IsIgnored() {
					cluster.LogPrintf(LvlInfo, "HaProxy valid resplication and DRAIN state in haproxy %s enable traffic on server %s", proxy.Host+":"+proxy.Port, srv.URL)
					haRuntime.SetReady(srv.Id, cluster.Conf.HaproxyAPIReadBackend)
				}
				if srv.IsMaster() {
					if !cluster.Configurator.HasProxyReadLeader() && line[17] == "UP" {
						cluster.LogPrintf(LvlInfo, "HaProxy master is not configure as reader but state UP in haproxy %s for server %s", proxy.Host+":"+proxy.Port, srv.URL)
						haRuntime.SetDrain(srv.Id, cluster.Conf.HaproxyAPIReadBackend)
					}
					if cluster.Configurator.HasProxyReadLeader() && line[17] == "DRAIN" {
						cluster.LogPrintf(LvlInfo, "HaProxy master is  configure as reader but state DRAIN in haproxy %s for server %s", proxy.Host+":"+proxy.Port, srv.URL)
						haRuntime.SetReady(srv.Id, cluster.Conf.HaproxyAPIReadBackend)
					}

				}
				if srv.IsMaintenance && line[17] == "UP" {
					cluster.LogPrintf(LvlInfo, "HaProxy detecting server %s in maintenance but proxy %s report UP  ", srv.URL, proxy.Host+":"+proxy.Port)
					proxy.SetMaintenance(srv)
				}
				if !srv.IsMaintenance && line[17] == "MAINT" {
					cluster.LogPrintf(LvlInfo, "HaProxy detecting server %s up but proxy %s report in maintenance ", srv.URL, proxy.Host+":"+proxy.Port)
					proxy.SetMaintenance(srv)
				}
			}
		}
	}
	if !foundMasterInStat {
		master := cluster.GetMaster()
		if master != nil && master.State == stateMaster {
			res, err := haRuntime.SetMaster(master.Host, master.Port)
			cluster.LogPrintf(LvlInfo, "Haproxy have leader in cluster but not in haproxy %s fixing it to master %s return %s", proxy.Host+":"+proxy.Port, master.URL, res)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Haproxy can not add leader %s in cluster but not in haproxy %s : %s", master.URL, proxy.Host+":"+proxy.Port, err)
			}
		}
	}
	return nil
}

func (cluster *Cluster) setMaintenanceHaproxy(pr *Proxy, server *ServerMonitor) {
	pr.SetMaintenance(server)
}

func (proxy *Proxy) SetMaintenance(server *ServerMonitor) {
	cluster := proxy.ClusterGroup
	if !cluster.Conf.HaproxyOn {
		return
	}
	if cluster.Conf.HaproxyMode == "standby" {
		proxy.Init()
		return
	}
	//if cluster.Conf.HaproxyDebug {
	cluster.LogPrintf(LvlInfo, "HaProxy set maintenance for server %s ", server.URL)
	//}
	haRuntime := haproxy.Runtime{
		Binary:   cluster.Conf.HaproxyBinaryPath,
		SockFile: filepath.Join(proxy.Datadir+"/var", "/haproxy.stats.sock"),
		Port:     proxy.Port,
		Host:     proxy.Host,
	}

	if server.IsMaintenance {
		cluster.LogPrintf(LvlInfo, "HaProxy set server %s/%s state maint ", server.Id, cluster.Conf.HaproxyAPIReadBackend)
		res, err := haRuntime.SetMaintenance(server.Id, cluster.Conf.HaproxyAPIReadBackend)
		if err != nil {
			cluster.LogPrintf(LvlErr, "HaProxy can not set maintenance %s backend %s : %s", server.URL, cluster.Conf.HaproxyAPIReadBackend, err)
		}
		if cluster.Conf.HaproxyDebug {
			cluster.LogPrintf(LvlInfo, "HaProxy set maintenance %s backend %s result: %s", server.URL, cluster.Conf.HaproxyAPIReadBackend, res)
		}
	} else {
		cluster.LogPrintf(LvlInfo, "HaProxy set server %s/%s state ready ", server.Id, cluster.Conf.HaproxyAPIReadBackend)
		res, err := haRuntime.SetReady(server.Id, cluster.Conf.HaproxyAPIReadBackend)
		if err != nil {
			cluster.LogPrintf(LvlErr, "HaProxy can not set ready %s backend %s : %s", server.URL, cluster.Conf.HaproxyAPIReadBackend, err)
		}
		if cluster.Conf.HaproxyDebug {
			cluster.LogPrintf(LvlInfo, "HaProxy set ready %s backend %s result: %s", server.URL, cluster.Conf.HaproxyAPIReadBackend, res)
		}

	}
	if server.IsMaster() {
		if server.IsMaintenance {
			cluster.LogPrintf(LvlInfo, "HaProxy set maintenance for server %s ", server.URL)

			res, err := haRuntime.SetMaintenance("leader", cluster.Conf.HaproxyAPIWriteBackend)
			if err != nil {
				cluster.LogPrintf(LvlErr, "HaProxy can not set maintenance %s backend %s : %s", server.URL, cluster.Conf.HaproxyAPIReadBackend, err)
			}
			if cluster.Conf.HaproxyDebug {
				cluster.LogPrintf(LvlInfo, "HaProxy set maintenance result: %s", res)
			}

		} else {
			cluster.LogPrintf(LvlInfo, "HaProxy set ready for server %s ", server.URL)

			res, err := haRuntime.SetReady("leader", cluster.Conf.HaproxyAPIWriteBackend)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Haproxy can not set ready %s backend %s : %s", server.URL, cluster.Conf.HaproxyAPIWriteBackend, err)
			}
			if cluster.Conf.HaproxyDebug {
				cluster.LogPrintf(LvlInfo, "HaProxy set ready %s backend %s result: %s", server.URL, cluster.Conf.HaproxyAPIWriteBackend, res)
			}
		}
	}
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
}

func (proxy *HaproxyProxy) CertificatesReload() error {
	return nil
}
