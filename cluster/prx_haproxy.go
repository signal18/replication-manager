// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/csv"
	"errors"
	"fmt"
	"hash/crc64"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/router/haproxy"
	"github.com/signal18/replication-manager/utils/state"
)

func (cluster *Cluster) initHaproxy(proxy *Proxy) {

	haproxydatadir := proxy.Datadir + "/var"

	if _, err := os.Stat(haproxydatadir); os.IsNotExist(err) {
		proxy.GetProxyConfig()
		os.Symlink(proxy.Datadir+"/init/data", haproxydatadir)
	}
	haproxytemplateFile := "haproxy_config.template"
	haproxyconfigFile := "haproxy.cfg"
	haproxyjsonFile := "vamp_router.json"
	haproxypidFile := "haproxy.pid"
	haproxysockFile := "haproxy.stats.sock"
	haproxyerrorPagesDir := "error_pages"
	//	haproxymaxWorkDirSize := 50 // this value is based on (max socket path size - md5 hash length - pre and postfixes)

	haRuntime := haproxy.Runtime{
		Binary:   cluster.Conf.HaproxyBinaryPath,
		SockFile: filepath.Join(haproxydatadir, "/", haproxysockFile),
	}
	haConfig := haproxy.Config{
		TemplateFile:  filepath.Join(cluster.Conf.ShareDir, haproxytemplateFile),
		ConfigFile:    filepath.Join(haproxydatadir, "/", haproxyconfigFile),
		JsonFile:      filepath.Join(haproxydatadir, "/", haproxyjsonFile),
		ErrorPagesDir: filepath.Join(haproxydatadir, "/", haproxyerrorPagesDir, "/"),
		PidFile:       filepath.Join(haproxydatadir, "/", haproxypidFile),
		SockFile:      filepath.Join(haproxydatadir, "/", haproxysockFile),
		WorkingDir:    filepath.Join(haproxydatadir + "/"),
	}

	cluster.LogPrintf(LvlInfo, "Haproxy loading haproxy config at %s", haproxydatadir)
	err := haConfig.GetConfigFromDisk()
	if err != nil {
		cluster.LogPrintf(LvlInfo, "Haproxy did not find an haproxy config...initializing new config")
		haConfig.InitializeConfig()
	}
	few := haproxy.Frontend{Name: "my_write_frontend", Mode: "tcp", DefaultBackend: "service_write", BindPort: cluster.Conf.HaproxyWritePort, BindIp: cluster.Conf.HaproxyWriteBindIp}
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
	bew := haproxy.Backend{Name: "service_write", Mode: "tcp"}
	haConfig.AddBackend(&bew)

	if _, err := haConfig.GetServer("service_write", "leader"); err != nil {
		// log.Printf("No leader")
	} else {
		// log.Printf("Found exiting leader removing")
	}

	p, _ := strconv.Atoi(cluster.GetMaster().Port)
	s := haproxy.ServerDetail{Name: "leader", Host: cluster.GetMaster().Host, Port: p, Weight: 100, MaxConn: 2000, Check: true, CheckInterval: 1000}
	if err = haConfig.AddServer("service_write", &s); err != nil {
		//	log.Printf("Failed to add server to service_write ")
	}

	fer := haproxy.Frontend{Name: "my_read_frontend", Mode: "tcp", DefaultBackend: "service_read", BindPort: cluster.Conf.HaproxyReadPort, BindIp: cluster.Conf.HaproxyReadBindIp}
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

	ber := haproxy.Backend{Name: "service_read", Mode: "tcp"}
	if err := haConfig.AddBackend(&ber); err != nil {
		cluster.LogPrintf(LvlErr, "Haproxy failed to add backend for service_read")
	}

	//var checksum64 string
	crcHost := crc64.MakeTable(crc64.ECMA)
	for _, server := range cluster.Servers {
		if server.IsMaintenance == false {
			p, _ := strconv.Atoi(server.Port)
			checksum64 := fmt.Sprintf("%d", crc64.Checksum([]byte(server.Host+":"+server.Port), crcHost))
			s := haproxy.ServerDetail{Name: checksum64, Host: server.Host, Port: p, Weight: 100, MaxConn: 2000, Check: true, CheckInterval: 1000}
			if err := haConfig.AddServer("service_read", &s); err != nil {
				cluster.LogPrintf(LvlErr, "Failed to add server in Haproxy for service_read")
			}
		}
	}

	err = haConfig.Render()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Could not render initial haproxy config, exiting...")
	}
	if err := haRuntime.SetPid(haConfig.PidFile); err != nil {
		cluster.LogPrintf(LvlInfo, "Haproxy reload config on pid %s", haConfig.PidFile)
	} else {
		cluster.LogPrintf(LvlInfo, "Haproxy reload config err %s", err.Error())
	}

	err = haRuntime.Reload(&haConfig)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can't Reloadhaproxy config %s"+err.Error())
	}

}

func (cluster *Cluster) refreshHaproxy(proxy *Proxy) error {
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
	/*	moncsv, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Could not read body from peer response")
			return err

		}*/
	reader := csv.NewReader(resp.Body)

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
			cluster.sme.AddState("WARN0078", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0078"], err), ErrFrom: "MON"})
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
			}
		}
	}
	return nil
}
