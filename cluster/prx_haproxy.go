// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/router/haproxy"
)

func (cluster *Cluster) initHaproxy(proxy *Proxy) {
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
		cluster.LogPrintf(LvlErr, "Could not render initial haproxy config, exiting...")
	}
	if err := haRuntime.SetPid(haConfig.PidFile); err != nil {
		cluster.LogPrintf(LvlInfo, "Haproxy reload config err %s", err.Error())
	} else {
		cluster.LogPrintf(LvlInfo, "Haproxy reload config on pid %s", haConfig.PidFile)
	}

	err = haRuntime.Reload(&haConfig)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can't Reloadhaproxy config %s"+err.Error())
	}

}

func (cluster *Cluster) refreshHaproxy(proxy *Proxy) error {

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

	result, err := haRuntime.ApiCmd("show stat")

	if err != nil {
		cluster.SetSugarState("ERR00052", "MON", "", err)
		return err
	}

	//cluster.LogPrintf(LvlInfo, "Stats: %s", result)
	r := ioutil.NopCloser(bytes.NewReader([]byte(result)))
	defer r.Close()
	reader := csv.NewReader(r)

	proxy.BackendsWrite = nil
	proxy.BackendsRead = nil

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

			srv := cluster.GetServerFromURL(line[73])
			if srv != nil {

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
						cluster.LogPrintf(LvlInfo, "Detecting wrong master server in haproxy %s fixing it to master %s", proxy.Host+":"+proxy.Port, master.URL)
						haRuntime.SetMaster(master.Host, master.Port)
					}
				}

			}

		}
		if strings.Contains(strings.ToLower(line[0]), "read") {
			srv := cluster.GetServerFromURL(line[73])
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
					cluster.LogPrintf(LvlInfo, "Detecting broken resplication and UP state in haproxy %s drain  server %s", proxy.Host+":"+proxy.Port, srv.URL)
					haRuntime.SetDrain(srv.Id, cluster.Conf.HaproxyAPIReadBackend)
				}
				if (srv.State == stateSlave || srv.State == stateRelay) && line[17] == "DRAIN" {
					cluster.LogPrintf(LvlInfo, "Detecting valid resplication and DRAIN state in haproxy %s enable traffic on server %s", proxy.Host+":"+proxy.Port, srv.URL)
					haRuntime.SetReady(srv.Id, cluster.Conf.HaproxyAPIReadBackend)
				}
			}
		}
	}

	return nil
}

func (cluster *Cluster) setMaintenanceHaproxy(pr *Proxy, server *ServerMonitor) {
	haRuntime := haproxy.Runtime{
		Binary:   cluster.Conf.HaproxyBinaryPath,
		SockFile: filepath.Join(pr.Datadir+"/var", "/haproxy.stats.sock"),
		Port:     pr.Port,
		Host:     pr.Host,
	}

	if server.IsMaintenance {
		haRuntime.SetMaintenance(server.Id, cluster.Conf.HaproxyAPIReadBackend)
	} else {
		haRuntime.SetReady(server.Id, cluster.Conf.HaproxyAPIReadBackend)
	}
	if server.IsMaster() {
		if server.IsMaintenance {
			haRuntime.SetMaintenance("leader", cluster.Conf.HaproxyAPIReadBackend)
		} else {
			haRuntime.SetReady("leader", cluster.Conf.HaproxyAPIReadBackend)
		}
	}
}
