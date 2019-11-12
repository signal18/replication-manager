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

	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

var dockerMinusRm bool

func (cluster *Cluster) OpenSVCConnect() opensvc.Collector {
	var svc opensvc.Collector
	svc.UseAPI = cluster.Conf.ProvOpensvcUseCollectorAPI
	if !cluster.Conf.ProvOpensvcUseCollectorAPI {
		svc.CertsDERSecret = cluster.Conf.ProvOpensvcP12Secret
		err := svc.LoadCert(cluster.Conf.ProvOpensvcP12Certificate)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Cannot load OpenSVC cluster certificate %s ", err)
		} else {
			cluster.LogPrintf(LvlInfo, "Load OpenSVC cluster certificate %s ", cluster.Conf.ProvOpensvcP12Certificate)
		}
	}
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
	svc.ProvProxDisk = cluster.Conf.ProvProxDisk
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

func (cluster *Cluster) OpenSVCGetNodes() ([]Agent, error) {
	svc := cluster.OpenSVCConnect()
	hosts := svc.GetNodes()
	if hosts == nil {
		cluster.LogPrintf(LvlErr, "Can't Get Opensvc Agent list")
		return nil, errors.New("Can't Get Opensvc Agent list")
	}
	agents := []Agent{}
	for _, n := range hosts {
		var agent Agent
		agent.Id = n.Node_id
		agent.OsName = n.Os_name
		agent.OsKernel = n.Os_kernel
		agent.CpuCores = n.Cpu_cores
		agent.CpuFreq = n.Cpu_freq
		agent.MemBytes = n.Mem_bytes
		agent.HostName = n.Node_name
		agents = append(agents, agent)
	}
	return agents, nil
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
				cluster.LogPrintf(LvlErr, "Unprovisionning error %s on  %s", err, db.Name)
			} else {
				cluster.LogPrintf(LvlInfo, "Unprovisionning done for database %s", db.Name)
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
				cluster.LogPrintf(LvlErr, "Unprovisionning proxy error %s on  %s", err, prx.Name)
			} else {
				cluster.LogPrintf(LvlInfo, "Unprovisionning done for proxy %s", prx.Name)
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
			cluster.sme.AddState("WARN0046", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0046"]), ErrFrom: "TOPO"})
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
 command = {env.base_dir}/pod01/init/snapback
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
`
	if collector.ProvNetCNI {
		net = net + `type = cni
netns = container#00` + pod + `
network = repman
`
		// if proxy
		// expose = port/tcp
		// repman to get variable backend-network
		return net
		//expose = {env.port_pod01}/tcp:8000
	} else if collector.ProvMicroSrv == "docker" {
		net = net + `type = docker

netns = container#00` + pod + `
`

	}
	net = net + `
ipdev = ` + collector.ProvNetIface + `
ipname = {env.ip_pod` + fmt.Sprintf("%02d", i+1) + `}
netmask = {env.netmask}
network = {env.network}
gateway = {env.gateway}
`

	return net
}

func (cluster *Cluster) GetPodDiskTemplate(collector opensvc.Collector, pod string, agent string) string {

	var disk string
	var fs string
	fs = ""
	disk = ""
	//cluster.LogPrintf(LvlErr, "%s", collector.ProvFSMode)
	//cluster.LogPrintf(LvlErr, "%s", collector.ProvFSPool)
	if collector.ProvFSMode == "loopback" {

		disk = disk + "\n"
		disk = disk + "[disk#" + pod + "]\n"
		disk = disk + "type = loop\n"
		disk = disk + "file = " + collector.ProvFSPath + "/{namespace}-{svcname}_pod" + pod + ".dsk\n"
		disk = disk + "size = {env.size}g\n"
		disk = disk + "standby = true\n"
		disk = disk + "\n"

		if collector.ProvFSPool == "lvm" {
			disk = disk + "\n"
			disk = disk + "[disk#10" + pod + "]\n"
			disk = disk + "name = {namespace}-{svcname}_" + pod + "\n"
			disk = disk + "type = lvm\n"
			disk = disk + "pvs = {disk#" + pod + ".file}\n"
			disk = disk + "standby = true\n"
			disk = disk + "\n"

		}
		if collector.ProvFSPool == "zpool" {
			disk = disk + "\n"
			disk = disk + "[disk#10" + pod + "]\n"
			disk = disk + "name = zp{namespace}-{svcname}_pod" + pod + "\n"
			disk = disk + "type = zpool\n"
			disk = disk + "vdev  = {disk#" + pod + ".file}\n"
			disk = disk + "standby = true\n"
			disk = disk + "\n"

		}
	}

	if collector.ProvFSType == "directory" {
		fs = fs + "\n"
		fs = fs + "[fs#" + pod + "]\n"
		fs = fs + "type = directory\n"
		fs = fs + "path = {env.base_dir}/pod" + pod + "\n"
		fs = fs + "pre_provision = docker network create {env.subnet_name} --subnet {env.subnet_cidr}\n"
		fs = fs + "\n"
		fs = fs + "\n"
	} else {
		podpool := pod
		if collector.ProvFSPool == "lvm" || collector.ProvFSPool == "zpool" {
			podpool = "10" + pod
		}
		fs = fs + "\n"
		fs = fs + "[fs#" + pod + "]\n"
		fs = fs + "type = " + collector.ProvFSType + "\n"
		if collector.ProvFSPool == "lvm" {
			re := regexp.MustCompile("[0-9]+")
			strlvsize := re.FindAllString(collector.ProvDisk, 1)
			lvsize, _ := strconv.Atoi(strlvsize[0])
			lvsize--
			fs = fs + "dev = /dev/{namespace}-{svcname}_" + pod + "/pod" + pod + "\n"
			fs = fs + "vg = {namespace}-{svcname}_" + pod + "\n"
			fs = fs + "size = 100%FREE\n"
		} else if collector.ProvFSPool == "zpool" {
			if collector.ProvFSMode == "loopback" || collector.ProvFSMode == "physical" {
				fs = fs + "dev = {disk#" + podpool + ".name}/pod" + pod + "\n"
			} else if collector.ProvFSMode == "pool" {
				fs = fs + "dev =" + cluster.Conf.ProvDiskDevice + "/{namespace}-{svcname}_pod" + pod + "\n"
			}
			fs = fs + "size = {env.size}g\n"
			fs = fs + "mkfs_opt = -o recordsize=16K -o primarycache=metadata -o atime=off -o compression=gzip -o mountpoint=legacy\n"
		} else { //no pool
			fs = fs + "dev = {disk#" + podpool + ".file}\n"
			fs = fs + "size = {env.size}g\n"
		}
		fs = fs + "mnt = {env.base_dir}/pod" + pod + "\n"
		fs = fs + "standby = true\n"
	} // not a directory
	//cluster.LogPrintf(LvlErr, "%s", disk+fs)
	return disk + fs
}

func (cluster *Cluster) GetDockerDiskTemplate(collector opensvc.Collector) string {
	var conf string
	var disk string
	var fs string
	podpool := "00"
	if collector.ProvMicroSrv != "docker" {
		return string("")
	}
	if cluster.Conf.ProvDockerDaemonPrivate {
		conf = conf + "\ndocker_daemon_private = true\n"
	} else {
		conf = conf + "\ndocker_daemon_private = false\n"
	}
	conf = conf + "docker_data_dir = {env.base_dir}/docker\n"
	conf = conf + "docker_daemon_args = "
	if collector.ProvFSPool == "zpool" {
		conf = conf + " --storage-driver=zfs"
	} else {
		conf = conf + " --storage-driver=overlay"
	}
	if collector.ProvFSMode == "loopback" {
		disk = "\n"
		disk = disk + "[disk#00]\n"
		disk = disk + "type = loop\n"
		disk = disk + "file = " + collector.ProvFSPath + "/{namespace}-{svcname}_docker.dsk\n"
		disk = disk + "size = 2g\n"
		disk = disk + "\n"

		if collector.ProvFSPool == "zpool" {
			disk = disk + "\n"
			disk = disk + "[disk#0000]\n"
			disk = disk + "name = zp{namespace}-{svcname}_00\n"
			disk = disk + "type = zpool\n"
			disk = disk + "vdev  = {disk#00.file}\n"
			disk = disk + "standby = true\n"
			disk = disk + "\n"
		}
	}

	if collector.ProvFSPool == "lvm" || collector.ProvFSPool == "zpool" {
		podpool = "0000"
	}
	fs = "\n\n"
	fs = fs + "[fs#00]\n"
	fs = fs + "type = " + collector.ProvFSType + "\n"
	if collector.ProvFSMode == "loopback" {
		fs = fs + "dev = {disk#" + podpool + ".name}/docker\n"
	} else if collector.ProvFSMode == "pool" {
		fs = fs + "dev = " + cluster.Conf.ProvDiskDevice + "/{namespace}-{svcname}_docker\n"
	} else if collector.ProvFSPool == "none" {
		fs = fs + "dev = {disk" + podpool + ".file}\n"
	}
	if collector.ProvFSPool == "zpool" {
		fs = fs + "mkfs_opt = -o compression=gzip -o mountpoint=legacy\n"
	}
	fs = fs + "mnt = {env.base_dir}/docker\n"
	fs = fs + "size = 2g\n"
	fs = fs + "\n"

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
