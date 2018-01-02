// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/signal18/replication-manager/misc"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/state"
)

var dockerMinusRm bool

func (cluster *Cluster) OpenSVCConnect() opensvc.Collector {
	var svc opensvc.Collector
	svc.Host, svc.Port = misc.SplitHostPort(cluster.conf.ProvHost)
	svc.User, svc.Pass = misc.SplitPair(cluster.conf.ProvAdminUser)
	svc.RplMgrUser, svc.RplMgrPassword = misc.SplitPair(cluster.conf.ProvUser)
	svc.RplMgrCodeApp = cluster.conf.ProvCodeApp
	svc.ProvAgents = cluster.conf.ProvAgents
	svc.ProvMem = cluster.conf.ProvMem
	svc.ProvPwd = cluster.GetDbPass()
	svc.ProvIops = cluster.conf.ProvIops
	svc.ProvCores = cluster.conf.ProvCores
	svc.ProvTags = cluster.conf.ProvTags
	svc.ProvDisk = cluster.conf.ProvDisk
	svc.ProvNetMask = cluster.conf.ProvNetmask
	svc.ProvNetGateway = cluster.conf.ProvGateway
	svc.ProvNetIface = cluster.conf.ProvNetIface
	svc.ProvMicroSrv = cluster.conf.ProvType
	svc.ProvFSType = cluster.conf.ProvDiskFS
	svc.ProvFSPool = cluster.conf.ProvDiskPool
	svc.ProvFSMode = cluster.conf.ProvDiskType
	svc.ProvFSPath = cluster.conf.ProvDiskDevice
	svc.ProvDockerImg = cluster.conf.ProvDbImg
	svc.ProvProxAgents = cluster.conf.ProvProxAgents
	svc.ProvProxDisk = cluster.conf.ProvProxDisk
	svc.ProvProxNetMask = cluster.conf.ProvProxNetmask
	svc.ProvProxNetGateway = cluster.conf.ProvProxGateway
	svc.ProvProxNetIface = cluster.conf.ProvProxNetIface
	svc.ProvProxMicroSrv = cluster.conf.ProvProxType
	svc.ProvProxFSType = cluster.conf.ProvProxDiskFS
	svc.ProvProxFSPool = cluster.conf.ProvProxDiskPool
	svc.ProvProxFSMode = cluster.conf.ProvProxDiskType
	svc.ProvProxFSPath = cluster.conf.ProvProxDiskDevice
	svc.ProvProxDockerMaxscaleImg = cluster.conf.ProvProxMaxscaleImg
	svc.ProvProxDockerHaproxyImg = cluster.conf.ProvProxHaproxyImg
	svc.ProvProxDockerProxysqlImg = cluster.conf.ProvProxProxysqlImg
	svc.ProvProxDockerShardproxyImg = cluster.conf.ProvProxShardingImg

	svc.ProvProxTags = cluster.conf.ProvProxTags
	svc.Verbose = 1

	return svc
}

func (cluster *Cluster) OpenSVCUnprovision() {
	//opensvc := cluster.OpenSVCConnect()
	//agents := opensvc.GetNodes()
	//for _, node := range agents {
	//	for _, svc := range node.Svc {
	for _, db := range cluster.servers {
		go cluster.OpenSVCUnprovisionDatabaseService(db)
		/*		if db.Id == svc.Svc_name {
				idaction, err := opensvc.UnprovisionService(node.Node_id, svc.Svc_id)
				if err != nil {
					cluster.LogPrintf(LvlErr, "Can't unprovision database %s, %s", db.Id, err)
				} else {
					err := cluster.OpenSVCWaitDequeue(opensvc, idaction)
					if err != nil {
						cluster.LogPrintf(LvlErr, "Can't unprovision database %s, %s", db.Id, err)
					}
				}
		*/
	}
	for _, db := range cluster.servers {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogPrintf(LvlErr, "Unprovisionning error %s on  %s", err, db.Id)
			} else {
				cluster.LogPrintf(LvlInfo, "Unprovisionning done for database %s", db.Id)
			}
		}
	}
	//}
	for _, prx := range cluster.proxies {
		go cluster.OpenSVCUnprovisionProxyService(prx)
		/*		if prx.Id == svc.Svc_name {
					idaction, err := opensvc.UnprovisionService(node.Node_id, svc.Svc_id)
					if err != nil {
						cluster.LogPrintf(LvlErr, "Can't unprovision proxy %s, %s", prx.Id, err)
					} else {
						err := cluster.OpenSVCWaitDequeue(opensvc, idaction)
						if err != nil {
							cluster.LogPrintf(LvlErr, "Can't unprovision proxy %s, %s", prx.Id, err)
						}
					}
				}
			}*/

	}
	for _, prx := range cluster.proxies {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogPrintf(LvlErr, "Unprovisionning proxy error %s on  %s", err, prx.Id)
			} else {
				cluster.LogPrintf(LvlInfo, "Unprovisionning done for proxy %s", prx.Id)
			}
		}
	}
	//	}
}

func (cluster *Cluster) OpenSVCProvisionCluster() error {

	err := cluster.OpenSVCProvisionOneSrvPerDB()
	err = cluster.OpenSVCProvisionProxies()
	return err
}

func (cluster *Cluster) OpenSVCWaitDequeue(svc opensvc.Collector, idaction int) error {
	ct := 0
	if idaction == 0 {
		return errors.New("Error Timout idaction 0")
	}
	for {
		time.Sleep(2 * time.Second)
		status := svc.GetActionStatus(strconv.Itoa(idaction))
		if status == "Q" {
			cluster.sme.AddState("WARN0045", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0045"]), ErrFrom: "TOPO"})
		}
		if status == "W" {
			cluster.sme.AddState("ERR0046", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0045"]), ErrFrom: "TOPO"})
		}
		if status == "T" {
			return nil
		}
		ct++
		if ct > 900 {
			break
		}

	}
	return errors.New("Waiting to long more 400s for OpenSVC dequeue")
}

/*func (cluster *Cluster) OpenSVCProvisionOneSrv() error {

	svc := cluster.OpenSVCConnect()
	servers := cluster.GetServers()
	var iplist []string
	var portlist []string
	for _, s := range servers {
		iplist = append(iplist, s.Host)
		portlist = append(portlist, s.Port)
	}

	srvStatus, err := svc.GetServiceStatus(cluster.GetName())
	if err != nil {
		return err
	}
	if srvStatus == 0 {
		// create template && bootstrap
		agents := svc.GetNodes()
		var clusteragents []opensvc.Host

		for _, node := range agents {
			if strings.Contains(svc.ProvAgents, node.Node_name) {
				clusteragents = append(clusteragents, node)
			}
		}
		res, err := cluster.GenerateDBTemplate(svc, iplist, portlist, clusteragents, "", svc.ProvAgents)
		if err != nil {
			return err
		}

		idtemplate, _ := svc.CreateTemplate(cluster.GetName(), res)

		for _, node := range agents {
			if strings.Contains(svc.ProvAgents, node.Node_name) {
				idaction, _ := svc.ProvisionTemplate(idtemplate, node.Node_id, cluster.GetName())
				ct := 0
				for {
					time.Sleep(2 * time.Second)
					status := svc.GetActionStatus(strconv.Itoa(idaction))
					if status == "Q" {
						cluster.sme.AddState("WARN0045", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0045"]), ErrFrom: "TOPO"})
					}
					if status == "W" {
						cluster.sme.AddState("WARN0046", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0046"]), ErrFrom: "TOPO"})
					}
					if status == "T" {
						break
					}
					ct++
					if ct > 200 {
						break
					}

				}

				task := svc.GetAction(strconv.Itoa(idaction))
				if task != nil {
					cluster.LogPrintf(LvlInfo, "%s", task.Stderr)
				} else {
					cluster.LogPrintf(LvlErr, "Can't fetch task")
				}
			}
		}
	}

	return nil
}*/

// OpenSVCSeviceStatus 0 not provision , 1 prov and up ,2 on error error
func (cluster *Cluster) GetOpenSVCSeviceStatus() (int, error) {

	svc := cluster.OpenSVCConnect()
	srvStatus, err := svc.GetServiceStatus(cluster.GetName())
	if err != nil {
		return 0, err
	}
	return srvStatus, nil
}

/* Found iface
var ipdev string
agent := agents[i%len(agents)]
log.Printf("%d,%d,%d", i, len(agents), i%len(agents))
for _, addr := range agent.Ips {
	ipsagents := strings.Split(addr.Addr, ".")
	ipsdb := strings.Split(host, ".")
	if ipsagents[0] == ipsdb[0] && ipsagents[1] == ipsdb[1] && ipsagents[2] == ipsdb[2] {
		ipdev = addr.Net_intf
	}
}*/

func (server *ServerMonitor) GetSnapshot() string {
	if !server.IsPrefered() || !server.ClusterGroup.conf.ProvDiskSnapshot {
		return ""
	}
	conf := ""
	if server.ClusterGroup.conf.ProvDiskPool == "zpool" {
		conf = `
[sync#2]
type = zfssnap
dataset = {disk#1001.name}/pod01
recursive = true
name = daily
schedule = 00:01-02:00@120
keep =  ` + strconv.Itoa(server.ClusterGroup.conf.ProvDiskSnapshotKeep) + `
sync_max_delay = 1440
`
	}
	return conf
}

func (cluster *Cluster) GetPodNetTemplate(collector opensvc.Collector, pod string, i int) string {
	var net string
	ipdev := collector.ProvNetIface
	net = net + `
[ip#` + pod + `]
tags = sm sm.container sm.container.pod` + pod + ` pod` + pod + `
`
	if collector.ProvMicroSrv == "docker" {
		net = net + `type = docker
ipdev = ` + collector.ProvNetIface + `
container_rid = container#00` + pod + `
`
	} else {
		net = net + `ipdev = ` + ipdev + `
`
	}
	net = net + `
ipname = {env.ip_pod` + fmt.Sprintf("%02d", i+1) + `}
netmask = {env.netmask}
network = {env.network}
gateway = {env.gateway}
`
	//Use in gcloud
	//del_net_route = true

	return net
}

func (cluster *Cluster) GetPodDiskTemplate(collector opensvc.Collector, pod string) string {

	var disk string
	var fs string
	if collector.ProvFSMode == "loopback" {

		disk = disk + `
[disk#` + pod + `]
type = loop
file = ` + collector.ProvFSPath + `/{svcname}_pod` + pod + `.dsk
size = {env.size}

`
	}
	if collector.ProvFSPool == "lvm" {
		disk = disk + `
[disk#10` + pod + `]
name = {svcname}_` + pod + `
type = lvm
pvs = {disk#` + pod + `.file}

`
	}
	if collector.ProvFSPool == "zpool" {
		disk = disk + `
[disk#10` + pod + `]
name = zp{svcname}_pod` + pod + `
type = zpool
vdev  = {disk#` + pod + `.file}

`
	}

	if collector.ProvFSType == "directory" {
		fs = fs + `
[fs#` + pod + `]
type = directory
path = {env.base_dir}/pod` + pod + `
pre_provision = docker network create {env.subnet_name} --subnet {env.subnet_cidr}

`

	} else {
		podpool := pod
		if collector.ProvFSPool == "lvm" || collector.ProvFSPool == "zpool" {
			podpool = "10" + pod
		}

		fs = fs + `
[fs#` + pod + `]
type = ` + collector.ProvFSType + `
`
		if collector.ProvFSPool == "lvm" {
			re := regexp.MustCompile("[0-9]+")
			strlvsize := re.FindAllString(collector.ProvDisk, 1)
			lvsize, _ := strconv.Atoi(strlvsize[0])
			lvsize--
			fs = fs + `
dev = /dev/{svcname}_` + pod + `/pod` + pod + `
vg = {svcname}_` + pod + `
size = 100%FREE
`
		} else if collector.ProvFSPool == "zpool" {
			fs = fs + `
dev = {disk#` + podpool + `.name}/pod` + pod + `
size = {env.size}
mkfs_opt = -o recordsize=16K -o primarycache=metadata -o atime=off -o compression=gzip
`

		} else {
			fs = fs + `
dev = {disk#` + podpool + `.file}
size = {env.size}
`
		}
		fs = fs + `
mnt = {env.base_dir}/pod` + pod + `
`

	}
	return disk + fs
}
func (cluster *Cluster) GetDockerDiskTemplate(collector opensvc.Collector) string {
	var conf string
	var disk string
	var fs string
	if collector.ProvMicroSrv != "docker" {
		return string("")
	}
	conf = conf + `
docker_daemon_private = true
docker_data_dir = {env.base_dir}/docker
docker_daemon_args = --log-opt max-size=1m `
	if collector.ProvFSPool == "zpool" {
		conf = conf + `--storage-driver=zfs
`
	} else {
		conf = conf + `--storage-driver=overlay
`
	}
	if collector.ProvFSMode == "loopback" {
		disk = `
[disk#00]
type = loop
file = ` + collector.ProvFSPath + `/{svcname}_docker.dsk
size = 2g

`
		if collector.ProvFSPool == "zpool" {
			disk = disk + `
[disk#0000]
name = zp{svcname}_00
type = zpool
vdev  = {disk#00.file}
standby = true

`
		}
		if collector.ProvFSPool == "zpool" {
			fs = `
[fs#00]
type = ` + collector.ProvFSType + `
dev = {disk#0000.name}/docker
mnt = {env.base_dir}/docker
size = 2g

`
		} else {
			fs = `
[fs#00]
type = ` + collector.ProvFSType + `
dev = {disk#00.file}
mnt = {env.base_dir}/docker
size = 2g

`
		}
	}

	return conf + disk + fs
}

func (cluster *Cluster) GetPodPackageTemplate(collector opensvc.Collector, pod string) string {
	var vm string

	if collector.ProvMicroSrv == "package" {
		vm = vm + `
[app#` + pod + `]
script = {env.base_dir}/pod` + pod + `/init/launcher
start = 50
stop = 50
check = 50
info = 50
`
	}
	return vm
}
