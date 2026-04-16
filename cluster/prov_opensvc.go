// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

var dockerMinusRm bool

func (cluster *Cluster) OpenSVCConnect() opensvc.Collector {
	var svc opensvc.Collector
	svc.ClusterConf = cluster.Conf
	svc.ClusterDir = cluster.WorkingDir
	svc.Logrus = cluster.Logrus
	svc.UseCollectorAPI = cluster.Conf.ProvOpensvcUseCollectorAPI
	if !cluster.Conf.ProvOpensvcUseCollectorAPI {
		svc.CertsDERSecret = cluster.Conf.GetDecryptedValue("opensvc-p12-secret")
		err := svc.LoadCert(cluster.Conf.ProvOpensvcP12Certificate)
		if err != nil {
			cluster.failLoadP12Cert = true
			cluster.SetState("WARN0099", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0099"], cluster.Conf.ProvOpensvcP12Certificate, err), ErrFrom: "OpenSVC"})
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot load OpenSVC cluster certificate %s ", err)
		} else {
			cluster.failLoadP12Cert = false
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlDbg, "Load OpenSVC cluster certificate %s ", cluster.Conf.ProvOpensvcP12Certificate)
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
	svc.EventTimeoutSecond = cluster.Conf.ProvEventTimeout

	if cluster.GetOrchestratorVersion() == "v3" {
		// Set collector to v3 if already detected
		svc.SetV3()
	} else {
		// Try to detect v3, throttled to avoid probing every call.
		if cluster.ShouldProbeOrchestratorVersion(30 * time.Second) {
			err := svc.GetAuthInfoV3()
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlDbg, "Can not connect to OpenSVC v3 API: %s ", err)
			}

			// Set OrchestratorVersion if v3 detected
			if svc.IsV3() {
				cluster.SetOrchestratorVersion("v3")
			}
		}
	}

	return svc
}

func (cluster *Cluster) GetGottyServer(srv string, rid string, agent string) (string, string, string) {
	var url, node, ver string
	var err error
	svc := cluster.OpenSVCConnect()
	if svc.IsV3() {
		ver = "v3"
		node = agent
		url, err = svc.GetGottyServerV3(agent, srv, rid)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not GetGottyServer: %s ,Params: %s %s", err, srv, rid)
			return "", "", ver
		}
	} else {
		ver = "v2"
		url, node, err = svc.GetGottyServer(srv, rid)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not GetGottyServer: %s ,Params: %s %s", err, srv, rid)
			return "", "", ver
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Response from GetGottyServer: %s %s", url, node)

	return url, node, ver
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
	var allErr error

	err := svc.CreateSecret(cluster.Name, "env", agent)
	if err != nil {
		if errors.Is(err, opensvc.ErrObjectAlreadyExists) && cluster.Conf.ProvObjectAllowOverwrite {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Secret object exists. Reuse secret env on cluster to avoid truncation of keys")
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not create secret: %s ", err)
			allErr = errors.Join(allErr, fmt.Errorf("create secret env: %w", err))
		}
	}

	errs := make(map[string]error)
	err = svc.CreateSecretKeyValue(cluster.Name, "env", "REPLICATION_MANAGER_PASSWORD", cluster.APIUsers["admin"].Password)
	if err != nil {
		errs["REPLICATION_MANAGER_PASSWORD"] = err
	}
	err = svc.CreateSecretKeyValue(cluster.Name, "env", "MYSQL_ROOT_PASSWORD", cluster.GetDbPass())
	if err != nil {
		errs["MYSQL_ROOT_PASSWORD"] = err
	}
	err = svc.CreateSecretKeyValue(cluster.Name, "env", "SHARDPROXY_ROOT_PASSWORD", cluster.GetShardPass())
	if err != nil {
		errs["SHARDPROXY_ROOT_PASSWORD"] = err
	}

	if len(errs) > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add key to secrets: %v", errs)
		for key, e := range errs {
			allErr = errors.Join(allErr, fmt.Errorf("set secret key %s: %w", key, e))
		}
	}

	err = svc.CreateConfig(cluster.Name, "env", agent)
	if err != nil {
		if errors.Is(err, opensvc.ErrObjectAlreadyExists) && cluster.Conf.ProvObjectAllowOverwrite {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Config object exists. Reuse config env on cluster to avoid truncation of keys")
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not create config: %s ", err)
			allErr = errors.Join(allErr, fmt.Errorf("create config env: %w", err))
		}
	}

	errs = make(map[string]error)
	err = svc.CreateConfigKeyValue(cluster.Name, "env", "REPLICATION_MANAGER_USER", "admin")
	if err != nil {
		errs["REPLICATION_MANAGER_USER"] = err
	}
	err = svc.CreateConfigKeyValue(cluster.Name, "env", "REPLICATION_MANAGER_URL", "https://"+cluster.Conf.MonitorAddress+":"+cluster.Conf.APIPort)
	if err != nil {
		errs["REPLICATION_MANAGER_URL"] = err
	}
	err = svc.CreateConfigKeyValue(cluster.Name, "env", "REPLICATION_MANAGER_CLUSTER_NAME", cluster.GetClusterName())
	if err != nil {
		errs["REPLICATION_MANAGER_CLUSTER_NAME"] = err
	}
	if len(errs) > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add key to config: %v", errs)
		for key, e := range errs {
			allErr = errors.Join(allErr, fmt.Errorf("set config key %s: %w", key, e))
		}
	}

	if allErr != nil {
		return allErr
	}

	return nil
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
			cluster.SetState("WARN0045", state.State{ErrType: "WARNING", ErrDesc: clusterError["WARN0045"], ErrFrom: "TOPO"})
		}
		if status == "W" {
			cluster.SetState("WARN0046", state.State{ErrType: "WARNING", ErrDesc: clusterError["WARN0046"], ErrFrom: "TOPO"})
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
	cluster := server.ClusterGroup
	if !server.IsPrefered() || !cluster.Conf.ProvDiskSnapshot {
		return ""
	}
	conf := ""
	if cluster.Conf.ProvDiskPool == "zpool" {
		conf = `
[sync#2]
type = zfssnap
dataset = {disk#1001.name}
recursive = true
name = daily
schedule = 00:01-02:00@120
keep =  ` + strconv.Itoa(cluster.Conf.ProvDiskSnapshotKeep) + `
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
	//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,config.LvlErr, "%s", collector.ProvFSMode)
	//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,config.LvlErr, "%s", collector.ProvFSPool)
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
	//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,config.LvlErr, "%s", disk+fs)
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
