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
	svc.Host, svc.Port = misc.SplitHostPort(cluster.Conf.ProvHost)
	svc.User, svc.Pass = misc.SplitPair(cluster.Conf.ProvAdminUser)
	svc.RplMgrUser, svc.RplMgrPassword = misc.SplitPair(cluster.Conf.ProvUser)
	svc.RplMgrCodeApp = cluster.Conf.ProvCodeApp
	svc.ProvAgents = cluster.Conf.ProvAgents
	svc.ProvMem = cluster.Conf.ProvMem
	svc.ProvPwd = cluster.GetDbPass()
	svc.ProvIops = cluster.Conf.ProvIops
	svc.ProvCores = cluster.Conf.ProvCores
	svc.ProvTags = cluster.Conf.ProvTags
	svc.ProvDisk = cluster.Conf.ProvDisk
	svc.ProvNetMask = cluster.Conf.ProvNetmask
	svc.ProvNetGateway = cluster.Conf.ProvGateway
	svc.ProvNetIface = cluster.Conf.ProvNetIface
	svc.ProvMicroSrv = cluster.Conf.ProvType
	svc.ProvFSType = cluster.Conf.ProvDiskFS
	svc.ProvFSPool = cluster.Conf.ProvDiskPool
	svc.ProvFSMode = cluster.Conf.ProvDiskType
	svc.ProvFSPath = cluster.Conf.ProvDiskDevice
	svc.ProvDockerImg = cluster.Conf.ProvDbImg
	svc.ProvProxAgents = cluster.Conf.ProvProxAgents
	svc.ProvProxDisk = cluster.Conf.ProvProxDisk
	svc.ProvProxNetMask = cluster.Conf.ProvProxNetmask
	svc.ProvProxNetGateway = cluster.Conf.ProvProxGateway
	svc.ProvProxNetIface = cluster.Conf.ProvProxNetIface
	svc.ProvProxMicroSrv = cluster.Conf.ProvProxType
	svc.ProvProxFSType = cluster.Conf.ProvProxDiskFS
	svc.ProvProxFSPool = cluster.Conf.ProvProxDiskPool
	svc.ProvProxFSMode = cluster.Conf.ProvProxDiskType
	svc.ProvProxFSPath = cluster.Conf.ProvProxDiskDevice
	svc.ProvProxDockerMaxscaleImg = cluster.Conf.ProvProxMaxscaleImg
	svc.ProvProxDockerHaproxyImg = cluster.Conf.ProvProxHaproxyImg
	svc.ProvProxDockerProxysqlImg = cluster.Conf.ProvProxProxysqlImg
	svc.ProvProxDockerShardproxyImg = cluster.Conf.ProvProxShardingImg
	svc.ProvNetCNI = cluster.Conf.ProvNetCNI
	svc.ProvProxTags = cluster.Conf.ProvProxTags
	svc.Verbose = 1

	return svc
}

func (cluster *Cluster) OpenSVCUnprovision() {
	//opensvc := cluster.OpenSVCConnect()
	//agents := opensvc.GetNodes()

	for _, db := range cluster.Servers {
		go cluster.OpenSVCUnprovisionDatabaseService(db)

	}
	for _, db := range cluster.Servers {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogPrintf(LvlErr, "Unprovisionning error %s on  %s", err, db.Id)
			} else {
				cluster.LogPrintf(LvlInfo, "Unprovisionning done for database %s", db.Id)
			}
		}
	}

	for _, prx := range cluster.Proxies {
		go cluster.OpenSVCUnprovisionProxyService(prx)

	}
	for _, prx := range cluster.Proxies {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogPrintf(LvlErr, "Unprovisionning proxy error %s on  %s", err, prx.Id)
			} else {
				cluster.LogPrintf(LvlInfo, "Unprovisionning done for proxy %s", prx.Id)
			}
		}
	}

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

func (server *ServerMonitor) GetSnapshot(collector opensvc.Collector) string {
	if !server.IsPrefered() || !server.ClusterGroup.Conf.ProvDiskSnapshot {
		return ""
	}
	conf := ""
	if server.ClusterGroup.Conf.ProvDiskPool == "zpool" {
		conf = `
[sync#2]
type = zfssnap
dataset = {disk#1001.name}/pod01
recursive = true
name = daily
schedule = 00:01-02:00@120
keep =  ` + strconv.Itoa(server.ClusterGroup.Conf.ProvDiskSnapshotKeep) + `
sync_max_delay = 1440

`
		conf = conf + `[task2]
 schedule = @1
 command = ` + collector.ProvFSPath + `/{svcname}_pod01/init/snapback
 user = root

`
	}
	return conf
}

func (cluster *Cluster) GetPodNetTemplate(collector opensvc.Collector, pod string, i int) string {
	var net string

	net = net + `
[ip#` + pod + `]
tags = sm sm.container sm.container.pod` + pod + ` pod` + pod + `
ipdev = ` + collector.ProvNetIface + `
`
	if collector.ProvNetCNI {
		net = net + `type = cni
	container_rid = container#00` + pod + `
	ipname = {svcname}
`
		//expose = {env.port_pod01}/tcp:8000
	} else if collector.ProvMicroSrv == "docker" {
		net = net + `type = docker

container_rid = container#00` + pod + `
`
	}

	net = net + `
ipname = {env.ip_pod` + fmt.Sprintf("%02d", i+1) + `}
netmask = {env.netmask}
network = {env.network}
gateway = {env.gateway}
`

	//network = <cni net name>

	//Use in gcloud
	//del_net_route = true

	return net
}

func (cluster *Cluster) GetPodDiskTemplate(collector opensvc.Collector, pod string, agent string) string {

	var disk string
	var fs string
	if collector.ProvFSMode == "loopback" {

		disk = disk + `
[disk#` + pod + `]
type = loop
file = ` + collector.ProvFSPath + `/{svcname}_pod` + pod + `.dsk
size = {env.size}
always_on = nodes

`
	}
	if collector.ProvFSPool == "lvm" {
		disk = disk + `
[disk#10` + pod + `]
name = {svcname}_` + pod + `
type = lvm
pvs = {disk#` + pod + `.file}
always_on = nodes

`
	}
	if collector.ProvFSPool == "zpool" {
		disk = disk + `
[disk#10` + pod + `]
name = zp{svcname}_pod` + pod + `
type = zpool
vdev  = {disk#` + pod + `.file}
always_on = nodes

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
always_on = nodes
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
