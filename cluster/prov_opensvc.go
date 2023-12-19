// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
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
		svc.CertsDERSecret = cluster.Conf.GetDecryptedValue("opensvc-p12-secret")
		err := svc.LoadCert(cluster.Conf.ProvOpensvcP12Certificate)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Cannot load OpenSVC cluster certificate %s ", err)
		} else {
			if cluster.Conf.HasLogLevelPos(5, 8) || cluster.Conf.Verbose {
				cluster.LogPrintf(LvlInfo, "Load OpenSVC cluster certificate %s ", cluster.Conf.ProvOpensvcP12Certificate)
			}
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
	svc.Verbose = cluster.GetLogLevel()
	svc.ContextTimeoutSecond = 10

	return svc
}

func (cluster *Cluster) OpenSVCGetNodes() ([]Agent, error) {
	svc := cluster.OpenSVCConnect()
	hosts, err := svc.GetNodes()
	if err != nil {
		cluster.CanInitNodes = false
		return nil, err
	} else {
		cluster.CanInitNodes = true
	}
	if hosts == nil {
		return nil, errors.New("Empty Opensvc Agent list")
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

func (cluster *Cluster) OpenSVCCreateMaps(agent string) error {
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		return errors.New("No support of Maps in Collector API")
	}
	svc := cluster.OpenSVCConnect()
	err := svc.CreateSecretV2(cluster.Name, "env", agent)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can not create secret: %s ", err)
	}
	err = svc.CreateSecretKeyValueV2(cluster.Name, "env", "REPLICATION_MANAGER_PASSWORD", cluster.APIUsers["admin"].Password)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can not add key to secret: %s %s ", "REPLICATION_MANAGER_PASSWORD", err)
	}
	err = svc.CreateSecretKeyValueV2(cluster.Name, "env", "MYSQL_ROOT_PASSWORD", cluster.GetDbPass())
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can not add key to secret: %s %s ", "MYSQL_ROOT_PASSWORD", err)
	}
	err = svc.CreateSecretKeyValueV2(cluster.Name, "env", "SHARDPROXY_ROOT_PASSWORD", cluster.GetShardPass())
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can not add key to secret: %s %s ", "SHARDPROXY_ROOT_PASSWORD", err)
	}
	err = svc.CreateConfigV2(cluster.Name, "env", agent)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can not create config: %s ", err)
	}
	err = svc.CreateConfigKeyValueV2(cluster.Name, "env", "REPLICATION_MANAGER_USER", "admin")
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can not add key to config: %s %s ", "REPLICATION_MANAGER_USER", err)
	}
	err = svc.CreateConfigKeyValueV2(cluster.Name, "env", "REPLICATION_MANAGER_URL", "https://"+cluster.Conf.MonitorAddress+":"+cluster.Conf.APIPort)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can not add key to config: %s %s ", "REPLICATION_MANAGER_URL", err)
	}
	err = svc.CreateConfigKeyValueV2(cluster.Name, "env", "REPLICATION_MANAGER_CLUSTER_NAME", cluster.GetClusterName())
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can not add key to config: %s %s ", "REPLICATION_MANAGER_CLUSTER_NAME", err)
	}

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
			cluster.StateMachine.AddState("WARN0045", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0045"]), ErrFrom: "TOPO"})
		}
		if status == "W" {
			cluster.StateMachine.AddState("WARN0046", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0046"]), ErrFrom: "TOPO"})
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
dataset = {disk#1001.name}
recursive = true
name = daily
schedule = 00:01-02:00@120
keep =  ` + strconv.Itoa(server.ClusterGroup.Conf.ProvDiskSnapshotKeep) + `
sync_max_delay = 1440

`
		conf = conf + `[task2]
 schedule = @1
 command = {env.base_dir}/init/snapback
 user = root

`
	}
	return conf
}

func (cluster *Cluster) GetPodNetTemplate(collector opensvc.Collector, pod string, i int) string {
	var net string

	net = net + `
[ip#01]
`
	if collector.ProvNetCNI {
		net = net + `type = cni
netns = container#01
network =  ` + cluster.Conf.ProvNetCNICluster + `
`
		return net

	} else if collector.ProvMicroSrv == "docker" {
		net = net + `type = docker

netns = container#01
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
		disk = disk + "[disk#01]\n"
		disk = disk + "type = loop\n"
		disk = disk + "file = " + collector.ProvFSPath + "/{namespace}-{svcname}.dsk\n"
		disk = disk + "size = {env.size}g\n"
		disk = disk + "standby = true\n"
		disk = disk + "\n"

		if collector.ProvFSPool == "lvm" {
			disk = disk + "\n"
			disk = disk + "[disk#1001]\n"
			disk = disk + "name = {namespace}-{svcname}\n"
			disk = disk + "type = lvm\n"
			disk = disk + "pvs = {disk#01.file}\n"
			disk = disk + "standby = true\n"
			disk = disk + "\n"

		}
		if collector.ProvFSPool == "zpool" {
			disk = disk + "\n"
			disk = disk + "[disk#1001]\n"
			disk = disk + "name = zp{namespace}-{svcname}\n"
			disk = disk + "type = zpool\n"
			disk = disk + "vdev  = {disk#01.file}\n"
			disk = disk + "standby = true\n"
			disk = disk + "\n"

		}
	}

	if collector.ProvFSType == "directory" {
		fs = fs + "\n"
		fs = fs + "[fs#01]\n"
		fs = fs + "type = directory\n"
		fs = fs + "path = {env.base_dir}\n"
		fs = fs + "pre_provision = docker network create {env.subnet_name} --subnet {env.subnet_cidr}\n"
		fs = fs + "\n"
		fs = fs + "\n"
	} else {
		podpool := pod
		if collector.ProvFSPool == "lvm" || collector.ProvFSPool == "zpool" {
			podpool = "10" + pod
		}
		fs = fs + "\n"
		fs = fs + "[fs#01]\n"
		fs = fs + "type = " + collector.ProvFSType + "\n"
		if collector.ProvFSPool == "lvm" {
			re := regexp.MustCompile("[0-9]+")
			strlvsize := re.FindAllString(collector.ProvDisk, 1)
			lvsize, _ := strconv.Atoi(strlvsize[0])
			lvsize--
			fs = fs + "dev = /dev/{namespace}-{svcname}\n"
			fs = fs + "vg = {namespace}-{svcname}\n"
			fs = fs + "size = 100%FREE\n"
		} else if collector.ProvFSPool == "zpool" {
			if collector.ProvFSMode == "loopback" || collector.ProvFSMode == "physical" {
				fs = fs + "dev = {disk#" + podpool + ".name}\n"
			} else if collector.ProvFSMode == "pool" {
				fs = fs + "dev =" + cluster.Conf.ProvDiskDevice + "/{namespace}-{svcname\n"
			}
			fs = fs + "size = {env.size}\n"
			fs = fs + "mkfs_opt = -o recordsize=16K -o primarycache=metadata -o atime=off -o compression=" + cluster.Conf.ProvDiskFSCompress + " -o mountpoint=legacy\n"
		} else { //no pool
			fs = fs + "dev = {disk#" + podpool + ".file}\n"
			fs = fs + "size = {env.size}\n"
		}
		fs = fs + "mnt = {env.base_dir}\n"
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
		fs = fs + "mkfs_opt = -o compression=" + cluster.Conf.ProvDiskFSCompress + " -o mountpoint=legacy\n"
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
[app#01]
script = {env.base_dir}/init/launcher
start = 50
stop = 50
check = 50
info = 50
`
	}
	return vm
}

func (cluster *Cluster) OpenSVCUnprovisionSecret() {
	opensvc := cluster.OpenSVCConnect()
	if !cluster.Conf.ProvOpensvcUseCollectorAPI {
		opensvc.PurgeServiceV2(cluster.Name, cluster.Name+"/sec/env", "")
		opensvc.PurgeServiceV2(cluster.Name, cluster.Name+"/cfg/env", "")
	}
}
