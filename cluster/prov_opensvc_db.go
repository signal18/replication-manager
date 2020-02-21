// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/utils/state"
)

func (cluster *Cluster) GetDatabaseServiceConfig(s *ServerMonitor) string {

	if cluster.Conf.ProvOpensvcUseCollectorAPI {

		svc := cluster.OpenSVCConnect()
		agent, err := cluster.OpenSVCFoundDatabaseAgent(s)
		if err != nil {
			cluster.errorChan <- err
			cluster.LogPrintf(LvlErr, "Can't OpenSVCFoundDatabaseAgent in service config %s", err)
			return ""
		}

		res, err := s.GenerateDBTemplate(svc, []string{s.Host}, []string{s.Port}, []opensvc.Host{agent}, s.Id, agent.Node_name)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can't create OpenSVC config template %s", err)
			return ""
		}
		return res
	} else {
		res, err := s.GenerateDBTemplateV2()
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can't create OpenSVC config template  %s", err)
			return ""
		}
		return res
	}
	return ""
}

func (cluster *Cluster) OpenSVCProvisionDatabaseService(s *ServerMonitor) {
	svc := cluster.OpenSVCConnect()
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		var taglist []string
		agent, err := cluster.OpenSVCFoundDatabaseAgent(s)
		if err != nil {
			cluster.errorChan <- err
			return
		}
		// Unprovision if already in OpenSVC
		var idsrv string
		mysrv, err := svc.GetServiceFromName(cluster.Name + "/svc/" + s.Name)
		if err == nil {
			cluster.LogPrintf(LvlInfo, "Found opensvc database service %s service %s", cluster.Name+"/svc/"+s.Name, mysrv.Svc_id)
			idsrv = mysrv.Svc_id
		} else {
			idsrv, err = svc.CreateService(cluster.Name+"/svc/"+s.Name, "MariaDB")
			if err != nil {
				cluster.LogPrintf(LvlErr, "Can't create OpenSVC service %s", err)
				cluster.errorChan <- err
				return
			}
		}

		err = svc.DeteteServiceTags(idsrv)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can't delete service tags")
			cluster.errorChan <- err
			return
		}
		taglist = strings.Split(svc.ProvTags, ",")
		svctags, _ := svc.GetTags()
		for _, tag := range taglist {
			idtag, err := svc.GetTagIdFromTags(svctags, tag)
			if err != nil {
				idtag, _ = svc.CreateTag(tag)
			}
			svc.SetServiceTag(idtag, idsrv)
		}

		// create template && bootstrap
		res, err := s.GenerateDBTemplate(svc, []string{s.Host}, []string{s.Port}, []opensvc.Host{agent}, cluster.Name+"/svc/"+s.Name, agent.Node_name)
		if err != nil {
			cluster.errorChan <- err
			return
		}
		idtemplate, _ := svc.CreateTemplate(cluster.Name+"/svc/"+s.Name, res)
		idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, cluster.Name+"/svc/"+s.Name)
		err = cluster.OpenSVCWaitDequeue(svc, idaction)
		if err != nil {
			cluster.LogPrintf(LvlErr, "%s", err)
			cluster.errorChan <- err
			return
		}
		task := svc.GetAction(strconv.Itoa(idaction))
		if task != nil {
			cluster.LogPrintf(LvlInfo, "%s", task.Stderr)
		} else {
			cluster.LogPrintf(LvlErr, "Can't fetch task")
		}
	} else {

		res, err := s.GenerateDBTemplateV2()
		if err != nil {
			cluster.errorChan <- err
			return
		}

		agent, err := s.ClusterGroup.GetDatabaseAgent(s)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can not provision database:  %s ", err)
			cluster.errorChan <- err
			return
		}
		cluster.LogPrintf(LvlInfo, "%s", res)
		err = svc.CreateTemplateV2(cluster.Name, s.ServiceName, agent.HostName, res)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can not provision database:  %s ", err)
		}
	}
	cluster.WaitDatabaseStart(s)

	cluster.errorChan <- nil
	return
}

func (cluster *Cluster) OpenSVCStopDatabaseService(server *ServerMonitor) error {
	svc := cluster.OpenSVCConnect()
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		service, err := svc.GetServiceFromName(cluster.Name + "/svc/" + server.Name)
		if err != nil {
			return err
		}
		agent, err := cluster.OpenSVCFoundDatabaseAgent(server)
		if err != nil {
			return err
		}
		svc.StopService(agent.Node_id, service.Svc_id)
	} else {
		agent, err := cluster.GetDatabaseAgent(server)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can not stop database:  %s ", err)
			return err
		}
		err = svc.StopServiceV2(cluster.Name, server.ServiceName, agent.HostName)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can not stop database:  %s ", err)
			return err
		}
	}
	return nil
}

func (cluster *Cluster) OpenSVCStartDatabaseService(server *ServerMonitor) error {
	svc := cluster.OpenSVCConnect()
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		service, err := svc.GetServiceFromName(cluster.Name + "/svc/" + server.Name)
		if err != nil {
			return err
		}
		agent, err := cluster.OpenSVCFoundDatabaseAgent(server)
		if err != nil {
			return err
		}
		svc.StartService(agent.Node_id, service.Svc_id)
	} else {
		agent, err := cluster.GetDatabaseAgent(server)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can not stop database:  %s ", err)
			return err
		}
		err = svc.StartServiceV2(cluster.Name, server.ServiceName, agent.HostName)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can not stop database:  %s ", err)
			return err
		}
	}

	return nil
}

func (cluster *Cluster) OpenSVCUnprovisionDatabaseService(server *ServerMonitor) {
	opensvc := cluster.OpenSVCConnect()
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		node, _ := cluster.OpenSVCFoundDatabaseAgent(server)
		for _, svc := range node.Svc {
			if cluster.Name+"/svc/"+server.Name == svc.Svc_name {
				idaction, _ := opensvc.UnprovisionService(node.Node_id, svc.Svc_id)
				err := cluster.OpenSVCWaitDequeue(opensvc, idaction)
				if err != nil {
					cluster.LogPrintf(LvlErr, "Can't unprovision database %s, %s", cluster.Name+"/svc/"+server.Name, err)
					cluster.errorChan <- err
				}
			}
		}
	} else {
		agent, err := cluster.GetDatabaseAgent(server)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can not unprovision database:  %s ", err)
			cluster.errorChan <- err
		}
		err = opensvc.PurgeServiceV2(cluster.Name, server.ServiceName, agent.HostName)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Can not unprovision database:  %s ", err)
			cluster.errorChan <- err
		}
	}
	cluster.errorChan <- nil
}

func (cluster *Cluster) OpenSVCFoundDatabaseAgent(server *ServerMonitor) (opensvc.Host, error) {
	var clusteragents []opensvc.Host
	var agent opensvc.Host
	svc := cluster.OpenSVCConnect()
	agents, err := svc.GetNodes()
	if err != nil {
		cluster.SetState("ERR00082", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00082"], err), ErrFrom: "TOPO"})
	}
	if agents == nil {
		return agent, errors.New("Error getting OpenSVC node list")
	}
	for _, node := range agents {
		if strings.Contains(svc.ProvAgents, node.Node_name) {
			clusteragents = append(clusteragents, node)
		}
	}
	for i, srv := range cluster.Servers {

		if srv.Id == server.Id {
			if len(clusteragents) == 0 {
				return agent, errors.New("Indice not found in database node list")
			}
			return clusteragents[i%len(clusteragents)], nil
		}
	}
	return agent, errors.New("Indice not found in database node list")
}

func (server *ServerMonitor) OpenSVCGetDBDefaultSection() map[string]string {
	svcdefault := make(map[string]string)
	svcdefault["nodes"] = "{env.nodes}"
	if server.ClusterGroup.Conf.ProvDiskPool == "zpool" && server.ClusterGroup.Conf.AutorejoinZFSFlashback && server.IsPrefered() {
		svcdefault["cluster_type"] = "failover"
		svcdefault["rollback"] = "true"
		svcdefault["orchestrate"] = "start"
	} else {
		svcdefault["flex_primary"] = "{env.nodes[0]}"
		svcdefault["rollback"] = "false"
		svcdefault["topology"] = "flex"
	}
	svcdefault["app"] = server.ClusterGroup.Conf.ProvCodeApp
	if server.ClusterGroup.Conf.ProvType == "docker" {
		if server.ClusterGroup.Conf.ProvDockerDaemonPrivate {
			svcdefault["docker_daemon_private"] = "true"
			if server.ClusterGroup.Conf.ProvDiskType != "volume" {
				svcdefault["docker_data_dir"] = "{env.base_dir}/docker"

			} else {
				svcdefault["docker_data_dir"] = "{name}-docker/docker"
			}
			if server.ClusterGroup.Conf.ProvDiskPool == "zpool" {
				svcdefault["docker_daemon_args"] = " --storage-driver=zfs"
			} else {
				svcdefault["docker_daemon_args"] = " --storage-driver=overlay"
			}
		} else {
			svcdefault["docker_daemon_private"] = "false"
		}

	}
	return svcdefault
}

func (server *ServerMonitor) OpenSVCGetDBContainerSection() map[string]string {
	svccontainer := make(map[string]string)
	if server.ClusterGroup.Conf.ProvType == "docker" || server.ClusterGroup.Conf.ProvType == "podman" {
		svccontainer["tags"] = ""
		svccontainer["netns"] = "container#0001"
		svccontainer["image"] = "{env.db_img}"
		svccontainer["type"] = server.ClusterGroup.Conf.ProvType
		if server.ClusterGroup.Conf.ProvDiskType != "volume" {
			svccontainer["run_args"] = `-e MYSQL_ROOT_PASSWORD={env.mysql_root_password} -e MYSQL_INITDB_SKIP_TZINFO=yes -v /etc/localtime:/etc/localtime:ro -v {env.base_dir}/pod01/data:/var/lib/mysql:rw -v {env.base_dir}/pod01/etc/mysql:/etc/mysql:rw -v {env.base_dir}/pod01/init:/docker-entrypoint-initdb.d:rw`
		} else {
			svccontainer["volume_mounts"] = `/etc/localtime:/etc/localtime:ro {name}-data/data:/var/lib/mysql:rw {name}-system/data/.system:/var/lib/mysql/.system:rw {name}-temp/data/.system/tmp:/var/lib/mysql/.system/tmp:rw {name}-data/etc/mysql:/etc/mysql:rw {name}-data/init:/docker-entrypoint-initdb.d:rw`
			svccontainer["environment"] = `MYSQL_ROOT_PASSWORD={env.mysql_root_password} MYSQL_INITDB_SKIP_TZINFO=yes`
		}
		//Proceed with galera specific
		if server.ClusterGroup.GetTopology() == topoMultiMasterWsrep && server.ClusterGroup.TopologyClusterDown() {
			if server.ClusterGroup.GetMaster() == nil {
				server.ClusterGroup.vmaster = server
				svccontainer["run_command"] = "mysqld --wsrep_new_cluster"
			}
		}
	}
	return svccontainer
}

func (server *ServerMonitor) OpenSVCGetDBEnvSection() map[string]string {
	svcenv := make(map[string]string)
	agent, err := server.ClusterGroup.GetDatabaseAgent(server)
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Can not provision database:  %s ", err)
		server.ClusterGroup.errorChan <- err
		return svcenv
	}
	svcenv["nodes"] = agent.HostName
	svcenv["size"] = server.ClusterGroup.Conf.ProvDisk + "g"
	svcenv["db_img"] = server.ClusterGroup.Conf.ProvDbImg
	svcenv["mysql_root_password"] = server.Pass
	svcenv["mysql_root_user"] = server.User
	ips := strings.Split(server.ClusterGroup.Conf.ProvGateway, ".")
	masks := strings.Split(server.ClusterGroup.Conf.ProvNetmask, ".")
	for i, mask := range masks {
		if mask == "0" {
			ips[i] = "0"
		}
	}
	network := strings.Join(ips, ".")
	svcenv["ip_pod01"] = server.Host
	svcenv["port_pod01"] = server.Port
	svcenv["mrm_api_addr"] = server.ClusterGroup.Conf.MonitorAddress + ":" + server.ClusterGroup.Conf.HttpPort
	svcenv["mrm_cluster_name"] = server.ClusterGroup.GetClusterName()
	svcenv["safe_ssl_ca_uuid"] = server.ClusterGroup.Conf.ProvSSLCaUUID
	svcenv["safe_ssl_cert_uuid"] = server.ClusterGroup.Conf.ProvSSLCertUUID
	svcenv["safe_ssl_key_uuid"] = server.ClusterGroup.Conf.ProvSSLKeyUUID
	// not required for socket prov
	svcenv["network"] = network
	svcenv["gateway"] = server.ClusterGroup.Conf.ProvGateway
	svcenv["netmask"] = server.ClusterGroup.Conf.ProvNetmask
	svcenv["base_dir"] = "/srv/{namespace}-{svcname}"
	svcenv["max_iops"] = server.ClusterGroup.Conf.ProvIops
	svcenv["max_mem"] = server.ClusterGroup.Conf.ProvMem
	svcenv["max_cores"] = server.ClusterGroup.Conf.ProvCores
	svcenv["micro_srv"] = server.ClusterGroup.Conf.ProvType
	svcenv["gcomm"] = server.ClusterGroup.GetGComm()
	svcenv["server_id"] = string(server.Id[2:10])
	svcenv["innodb_buffer_pool_size"] = server.ClusterGroup.GetConfigInnoDBBPSize()
	svcenv["innodb_log_file_size"] = server.ClusterGroup.GetConfigInnoDBLogFileSize()
	svcenv["innodb_buffer_pool_instances"] = server.ClusterGroup.GetConfigInnoDBBPInstances()
	svcenv["innodb_log_buffer_size"] = "8"
	return svcenv
}

func (cluster *Cluster) OpenSVCGetNamespaceContainerSection() map[string]string {
	svccontainer := make(map[string]string)
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		svccontainer["type"] = "docker"
		svccontainer["image"] = "google/pause"
		svccontainer["hostname"] = "{svcname}.{namespace}.svc.{clustername}"
		svccontainer["rm"] = "true"
	}
	return svccontainer
}

func (cluster *Cluster) OpenSVCGetInitContainerSection() map[string]string {
	svccontainer := make(map[string]string)
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		svccontainer["detach"] = "false"
		svccontainer["type"] = "docker"
		svccontainer["image"] = "busybox"
		svccontainer["netns"] = "container#0001"
		svccontainer["rm"] = "true"
		svccontainer["start_timeout"] = "30s"
		svccontainer["optional"] = "true"
		if cluster.Conf.ProvDiskType != "volume" {
			svccontainer["volume_mounts"] = "/etc/localtime:/etc/localtime:ro {env.base_dir}/pod01:/data"
		} else {
			svccontainer["volume_mounts"] = "/etc/localtime:/etc/localtime:ro {name}-data:/data"
		}
		svccontainer["command"] = "sh -c 'wget -qO- http://{env.mrm_api_addr}/api/clusters/{env.mrm_cluster_name}/servers/{env.ip_pod01}/{env.port_pod01}/config|tar xzvf - -C /data'"

	}
	return svccontainer
}

func (cluster *Cluster) OpenSVCGetTmpFsSection() map[string]string {
	svccontainer := make(map[string]string)
	svccontainer["type"] = "tmpfs"
	svccontainer["mnt"] = "{env.base_dir}/tmp"
	svccontainer["dev"] = "none"
	return svccontainer
}

func (server *ServerMonitor) OpenSVCGetTaskZFSSnapshotSection() map[string]string {
	//[task2]
	svctask := make(map[string]string)
	if !server.IsPrefered() || !server.ClusterGroup.Conf.ProvDiskSnapshot {
		return svctask
	}

	svctask["schedule"] = "@1"
	svctask["command"] = "{env.base_dir}/pod01/init/snapback"
	svctask["user"] = "root"
	return svctask
}

func (cluster *Cluster) OpenSVCGetNetSection() map[string]string {
	svcnet := make(map[string]string)
	if cluster.Conf.ProvNetCNI {
		svcnet["type"] = "cni"
		svcnet["netns"] = "container#0001"
		svcnet["network"] = cluster.Conf.ProvNetCNICluster
		return svcnet
	} else if cluster.Conf.ProvType == "docker" {
		svcnet["type"] = "docker"
		svcnet["netns"] = "container#0001"
	} else if cluster.Conf.ProvType == "podman" {
		svcnet["type"] = "podman"
		svcnet["netns"] = "container#0001"
	}
	svcnet["ipdev"] = cluster.Conf.ProvNetIface
	svcnet["ipname"] = "{env.ip_pod01}"
	svcnet["netmask"] = "{env.netmask}"
	svcnet["network"] = "{env.network}"
	svcnet["gateway"] = "{env.gateway}"
	return svcnet
}

func (cluster *Cluster) OpenSVCGetTaskJobsSection() map[string]string {
	svctask := make(map[string]string)
	svctask["schedule"] = "@1"
	svctask["command"] = "svcmgr -s {svcpath} docker exec -i {namespace}..{svcname}.container.2001 /bin/bash /docker-entrypoint-initdb.d/dbjobs"
	svctask["user"] = "root"
	svctask["run_requires"] = "fs#01(up,stdby up) container#0001(up,stdby up)"
	return svctask
}

func (cluster *Cluster) OpenSVCGetFSDockerPrivateSection() map[string]string {
	svcfs := make(map[string]string)
	podpool := "00"
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		if cluster.Conf.ProvDiskPool == "lvm" || cluster.Conf.ProvDiskPool == "zpool" {
			podpool = "0000"
		}
		svcfs["type"] = cluster.Conf.ProvType
		if cluster.Conf.ProvDiskType == "loopback" {
			svcfs["dev"] = "{disk#" + podpool + ".name}/docker"
		} else if cluster.Conf.ProvDiskType == "pool" {
			svcfs["dev"] = cluster.Conf.ProvDiskDevice + "/{namespace}-{svcname}_docker"
		} else if cluster.Conf.ProvDiskPool == "none" {
			svcfs["dev"] = "{disk" + podpool + ".file}"
		}
		if cluster.Conf.ProvDiskPool == "zpool" {
			svcfs["mkfs_opt"] = "-o compression=" + cluster.Conf.ProvDiskFSCompress + " -o mountpoint=legacy"
		}
		svcfs["mnt"] = "{env.base_dir}/docker"
		svcfs["size"] = cluster.Conf.ProvDiskDockerSize + "g"
	}
	return svcfs
}

func (cluster *Cluster) OpenSVCGetDiskLoopbackDockerPrivateSection() map[string]string {
	svcdsk := make(map[string]string)
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		if cluster.Conf.ProvDiskType == "loopback" {
			svcdsk["type"] = "loop"
			svcdsk["file"] = cluster.Conf.ProvDiskDevice + "/{namespace}-{svcname}_docker.dsk"
			svcdsk["size"] = cluster.Conf.ProvDiskDockerSize + "g"
		}
	}
	return svcdsk
}

func (cluster *Cluster) OpenSVCGetDiskZpoolDockerPrivateSection() map[string]string {
	svcdsk := make(map[string]string)
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		if cluster.Conf.ProvDiskType == "loopback" && cluster.Conf.ProvDiskPool == "zpool" {
			svcdsk["type"] = "zpool"
			svcdsk["name"] = "zp{namespace}-{svcname}_00"
			svcdsk["vdev"] = "{disk#00.file}"
			svcdsk["standby"] = "true"
		}
	}
	return svcdsk
}

func (cluster *Cluster) OpenSVCGetDiskLoopbackPodSection() map[string]string {
	svcdsk := make(map[string]string)
	if cluster.Conf.ProvDiskType == "loopback" {
		//disk#01
		svcdsk["type"] = "loop"
		svcdsk["file"] = cluster.Conf.ProvDiskDevice + "/{namespace}-{svcname}_pod01.dsk"
		svcdsk["size"] = "{env.size}g"
		svcdsk["standby"] = "true"
	}
	return svcdsk
}

func (cluster *Cluster) OpenSVCGetDiskLoopbackSnapshotPodSection() map[string]string {
	//"[disk#1001]
	svcdsk := make(map[string]string)
	if cluster.Conf.ProvDiskType == "loopback" {
		if cluster.Conf.ProvDiskPool == "lvm" {
			svcdsk["type"] = "lvm"
			svcdsk["name"] = "name = {namespace}-{svcname}_01"
			svcdsk["pvs"] = "{disk#01.file}"
		}
		if cluster.Conf.ProvDiskPool == "zpool" {
			svcdsk["type"] = "zpool"
			svcdsk["name"] = "zp{namespace}-{svcname}_pod01"
			svcdsk["vdev"] = "{disk#01.file}"
		}
		svcdsk["standby"] = "true"
	}
	return svcdsk
}

func (cluster *Cluster) OpenSVCGetFSTmpSection() map[string]string {
	svcfs := make(map[string]string)
	svcfs["type"] = "tmpfs"
	svcfs["mnt"] = "{env.base_dir}/tmp"
	svcfs["dev"] = "none"
	return svcfs
}

func (cluster *Cluster) OpenSVCGetFSPodSection() map[string]string {
	svcfs := make(map[string]string)
	if cluster.Conf.ProvDiskFS == "directory" {
		//fs#01
		svcfs["type"] = "directory"
		svcfs["path"] = " {env.base_dir}/pod01"
		if cluster.Conf.ProvType == "docker" {
			svcfs["pre_provision"] = "docker network create {env.subnet_name} --subnet {env.subnet_cidr}"
		}
	} else {

		podpool := "01"
		if cluster.Conf.ProvDiskPool == "lvm" || cluster.Conf.ProvDiskPool == "zpool" {
			podpool = "0001"
		}
		svcfs["type"] = cluster.Conf.ProvDiskFS
		if cluster.Conf.ProvDiskPool == "lvm" {
			re := regexp.MustCompile("[0-9]+")
			strlvsize := re.FindAllString(cluster.Conf.ProvDisk, 1)
			lvsize, _ := strconv.Atoi(strlvsize[0])
			lvsize--
			svcfs["dev"] = " /dev/{namespace}-{svcname}_01/pod01"
			svcfs["vg"] = "{namespace}-{svcname}_01"
			svcfs["size"] = "100%FREE"
		} else if cluster.Conf.ProvDiskPool == "zpool" {
			if cluster.Conf.ProvDiskType == "loopback" || cluster.Conf.ProvDiskType == "physical" {
				svcfs["dev"] = "{disk#" + podpool + ".name}/pod01"
			} else if cluster.Conf.ProvDiskType == "pool" {
				svcfs["dev"] = cluster.Conf.ProvDiskDevice + "/{namespace}-{svcname}_pod01"
			}
			svcfs["size"] = "{env.size}g"
			svcfs["mkfs_opt"] = "-o recordsize=16K -o primarycache=metadata -o atime=off -o compression=" + cluster.Conf.ProvDiskFSCompress + " -o mountpoint=legacy"
		} else { //no pool
			if cluster.Conf.ProvDiskType == "loopback" {
				svcfs["dev"] = "{disk#" + podpool + ".name}/pod01"
			} else {
				svcfs["dev"] = "{disk#" + podpool + ".file}"
			}
			svcfs["size"] = "{env.size}g"
		}
		svcfs["mnt"] = "{env.base_dir}/pod01"
		svcfs["standby"] = "true"
	}
	return svcfs
}

func (server *ServerMonitor) OpenSVCGetZFSSnapshotSection() map[string]string {
	svcsnap := make(map[string]string)
	if !server.IsPrefered() || !server.ClusterGroup.Conf.ProvDiskSnapshot {
		return svcsnap
	}
	if server.ClusterGroup.Conf.ProvDiskPool == "zpool" {
		svcsnap["type"] = "zfssnap"
		svcsnap["dataset"] = "{disk#0001.name}/pod01"
		svcsnap["recursive"] = "true"
		svcsnap["name"] = "daily"
		svcsnap["schedule"] = "00:01-02:00@120"
		svcsnap["keep"] = strconv.Itoa(server.ClusterGroup.Conf.ProvDiskSnapshotKeep)
		svcsnap["sync_max_delay"] = "1440"
	}
	return svcsnap
}

func (cluster *Cluster) OpenSVCGetVolumeTempSection() map[string]string {
	svcvol := make(map[string]string)
	svcvol["name"] = "{name}-tmp"
	svcvol["pool"] = cluster.Conf.ProvVolumeTemp
	svcvol["size"] = cluster.Conf.ProvDiskTempSize + "m"
	return svcvol
}

func (cluster *Cluster) OpenSVCGetVolumeDataSection() map[string]string {
	svcvol := make(map[string]string)
	svcvol["name"] = "{name}-data"
	svcvol["pool"] = cluster.Conf.ProvVolumeData
	svcvol["size"] = cluster.Conf.ProvDisk + "g"
	return svcvol
}

func (cluster *Cluster) OpenSVCGetVolumeSystemSection() map[string]string {
	svcvol := make(map[string]string)
	svcvol["name"] = "{name}-system"
	svcvol["pool"] = cluster.Conf.ProvVolumeSystem
	svcvol["size"] = cluster.Conf.ProvDiskSystemSize + "g"
	return svcvol
}

func (cluster *Cluster) OpenSVCGetVolumeDockerSection() map[string]string {
	svcvol := make(map[string]string)
	svcvol["name"] = "{name}-docker"
	svcvol["pool"] = cluster.Conf.ProvVolumeDocker
	svcvol["size"] = cluster.Conf.ProvDiskDockerSize + "g"
	return svcvol
}

func (server *ServerMonitor) GenerateDBTemplateV2() (string, error) {

	svcsection := make(map[string]map[string]string)
	svcsection["DEFAULT"] = server.OpenSVCGetDBDefaultSection()
	svcsection["ip#01"] = server.ClusterGroup.OpenSVCGetNetSection()
	if server.ClusterGroup.Conf.ProvDiskType != "volume" {
		svcsection["disk#0000"] = server.ClusterGroup.OpenSVCGetDiskZpoolDockerPrivateSection()
		svcsection["disk#00"] = server.ClusterGroup.OpenSVCGetDiskLoopbackDockerPrivateSection()
		svcsection["disk#01"] = server.ClusterGroup.OpenSVCGetDiskLoopbackPodSection()
		svcsection["disk#0001"] = server.ClusterGroup.OpenSVCGetDiskLoopbackSnapshotPodSection()
		svcsection["fs#00"] = server.ClusterGroup.OpenSVCGetFSDockerPrivateSection()
		svcsection["fs#01"] = server.ClusterGroup.OpenSVCGetFSPodSection()
		svcsection["fs#03"] = server.ClusterGroup.OpenSVCGetFSTmpSection()
		svcsection["sync#01"] = server.OpenSVCGetZFSSnapshotSection()
		svcsection["task#02"] = server.OpenSVCGetTaskZFSSnapshotSection()

	} else {
		if server.ClusterGroup.Conf.ProvDockerDaemonPrivate {
			svcsection["volume#00"] = server.ClusterGroup.OpenSVCGetVolumeDockerSection()
		}
		svcsection["volume#01"] = server.ClusterGroup.OpenSVCGetVolumeDataSection()
		svcsection["volume#02"] = server.ClusterGroup.OpenSVCGetVolumeSystemSection()
		svcsection["volume#03"] = server.ClusterGroup.OpenSVCGetVolumeTempSection()
	}
	svcsection["container#0001"] = server.ClusterGroup.OpenSVCGetNamespaceContainerSection()
	svcsection["container#0002"] = server.ClusterGroup.OpenSVCGetInitContainerSection()
	svcsection["container#0003"] = server.OpenSVCGetDBContainerSection()

	svcsection["task#01"] = server.ClusterGroup.OpenSVCGetTaskJobsSection()
	svcsection["env"] = server.OpenSVCGetDBEnvSection()

	svcsectionJson, err := json.MarshalIndent(svcsection, "", "\t")
	if err != nil {
		return "", err
	}
	log.Println(svcsectionJson)
	return string(svcsectionJson), nil

}

func (server *ServerMonitor) GenerateDBTemplate(collector opensvc.Collector, servers []string, ports []string, agents []opensvc.Host, name string, agent string) (string, error) {

	ipPods := ""
	portPods := ""

	conf := ""
	//if zfs snap
	if collector.ProvFSPool == "zpool" && server.ClusterGroup.GetConf().AutorejoinZFSFlashback && server.IsPrefered() {

		conf = `
[DEFAULT]
nodes = {env.nodes}
cluster_type = failover
rollback = true
orchestrate = start
`
	} else {
		conf = `
[DEFAULT]
nodes = {env.nodes}
flex_primary = {env.nodes[0]}
topology = flex
rollback = false
`
	}
	conf += "app = " + server.ClusterGroup.Conf.ProvCodeApp
	conf = conf + server.ClusterGroup.GetDockerDiskTemplate(collector)
	//main loop over db instances
	for i, host := range servers {
		pod := fmt.Sprintf("%02d", i+1)
		conf = conf + server.ClusterGroup.GetPodDiskTemplate(collector, pod, agent)
		conf = conf + server.GetInitContainer(collector)
		//		conf = conf + `post_provision =  {svcmgr} -s  {svcpath} push status;{svcmgr} -s {svcpath} compliance fix --attach --moduleset mariadb.svc.mrm.db;
		//	`
		conf = conf + server.GetSnapshot(collector)
		conf = conf + server.ClusterGroup.GetPodNetTemplate(collector, pod, i)
		conf = conf + server.GetPodDockerDBTemplate(collector, pod, i)
		conf = conf + server.ClusterGroup.GetPodPackageTemplate(collector, pod)
		ipPods = ipPods + `ip_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + host + `
	`
		portPods = portPods + `port_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + ports[i] + `
	`
	}

	conf = conf + `[task#01]
schedule = @1
command = svcmgr -s {svcpath} docker exec -i {namespace}..{svcname}.container.2001 /bin/bash /docker-entrypoint-initdb.d/dbjobs
user = root
run_requires = fs#01(up,stdby up) container#0001(up,stdby up)

`
	ips := strings.Split(collector.ProvNetGateway, ".")
	masks := strings.Split(collector.ProvNetMask, ".")
	for i, mask := range masks {
		if mask == "0" {
			ips[i] = "0"
		}
	}
	network := strings.Join(ips, ".")
	conf = conf + `
[env]
nodes = ` + agent + `
size = ` + collector.ProvDisk + `g
db_img = ` + collector.ProvDockerImg + `
` + ipPods + `
` + portPods + `
mysql_root_password = ` + server.ClusterGroup.dbPass + `
mysql_root_user = ` + server.ClusterGroup.dbUser + `
network = ` + network + `
gateway =  ` + collector.ProvNetGateway + `
netmask =  ` + collector.ProvNetMask + `
base_dir = /srv/{namespace}-{svcname}
max_iops = ` + collector.ProvIops + `
max_mem = ` + collector.ProvMem + `
max_cores = ` + collector.ProvCores + `
micro_srv = ` + collector.ProvMicroSrv + `
gcomm	 = ` + server.ClusterGroup.GetGComm() + `
mrm_api_addr = ` + server.ClusterGroup.Conf.MonitorAddress + ":" + server.ClusterGroup.Conf.HttpPort + `
mrm_cluster_name = ` + server.ClusterGroup.GetClusterName() + `
safe_ssl_ca_uuid = ` + server.ClusterGroup.Conf.ProvSSLCaUUID + `
safe_ssl_cert_uuid = ` + server.ClusterGroup.Conf.ProvSSLCertUUID + `
safe_ssl_key_uuid = ` + server.ClusterGroup.Conf.ProvSSLKeyUUID + `
server_id = ` + string(server.Id[2:10]) + `
innodb_buffer_pool_size = ` + server.ClusterGroup.GetConfigInnoDBBPSize() + `
innodb_log_file_size = ` + server.ClusterGroup.GetConfigInnoDBLogFileSize() + `
innodb_buffer_pool_instances = ` + server.ClusterGroup.GetConfigInnoDBBPInstances() + `
innodb_log_buffer_size = 8
`
	log.Println(conf)

	return conf, nil
}

func (server *ServerMonitor) GetInitContainer(collector opensvc.Collector) string {
	var vm string
	if collector.ProvMicroSrv == "docker" {
		vm = vm + `
[container#0002]
detach = false
type = docker
image = busybox
netns = container#0001
rm = true
start_timeout = 30s
volume_mounts = /etc/localtime:/etc/localtime:ro {env.base_dir}/pod01:/data
command = sh -c 'wget -qO- http://{env.mrm_api_addr}/api/clusters/{env.mrm_cluster_name}/servers/{env.ip_pod01}/{env.port_pod01}/config|tar xzvf - -C /data'

 `
	}
	return vm
}

func (server *ServerMonitor) GetPodDockerDBTemplate(collector opensvc.Collector, pod string, i int) string {
	var vm string
	if collector.ProvMicroSrv == "docker" {
		vm = vm + `
[container#00` + pod + `]
type = docker
hostname = {svcname}.{namespace}.svc.{clustername}
image = google/pause
rm = true


[container#20` + pod + `]
tags = pod` + pod + `
type = docker
rm = true
netns = container#00` + pod + `
run_image = {env.db_img}
run_args = -e MYSQL_ROOT_PASSWORD={env.mysql_root_password}
 -e MYSQL_INITDB_SKIP_TZINFO=yes
 -v /etc/localtime:/etc/localtime:ro
 -v {env.base_dir}/pod` + pod + `/data:/var/lib/mysql:rw
 -v {env.base_dir}/pod` + pod + `/etc/mysql:/etc/mysql:rw
 -v {env.base_dir}/pod` + pod + `/init:/docker-entrypoint-initdb.d:rw

`
		if server.ClusterGroup.GetTopology() == topoMultiMasterWsrep && server.ClusterGroup.TopologyClusterDown() {
			//Proceed with galera specific
			if server.ClusterGroup.GetMaster() == nil {
				server.ClusterGroup.vmaster = server
				vm = vm + `run_command = mysqld --wsrep_new_cluster
`
			}
		}
	}
	return vm
}

func (server *ServerMonitor) GetEnv() map[string]string {
	return map[string]string{
		"%%ENV:NODES_CPU_CORES%%":                           server.ClusterGroup.Conf.ProvCores,
		"%%ENV:SVC_CONF_ENV_MAX_CORES%%":                    server.ClusterGroup.Conf.ProvCores,
		"%%ENV:SVC_CONF_ENV_CRC32_ID%%":                     string(server.Id[2:10]),
		"%%ENV:SVC_CONF_ENV_SERVER_ID%%":                    string(server.Id[2:10]),
		"%%ENV:SVC_CONF_ENV_MYSQL_ROOT_PASSWORD%%":          server.ClusterGroup.dbPass,
		"%%ENV:SVC_CONF_ENV_MAX_MEM%%":                      server.ClusterGroup.GetConfigInnoDBBPSize(),
		"%%ENV:IBPINSTANCES%%":                              server.ClusterGroup.GetConfigInnoDBBPInstances(),
		"%%ENV:SVC_CONF_ENV_GCOMM%%":                        server.ClusterGroup.GetGComm(),
		"%%ENV:SERVER_IP%%":                                 "0.0.0.0",
		"%%ENV:SERVER_PORT%%":                               server.Port,
		"%%ENV:CHECKPOINTIOPS%%":                            server.ClusterGroup.GetConfigInnoDBIOCapacity(),
		"%%ENV:SVC_CONF_ENV_MAX_IOPS%%":                     server.ClusterGroup.GetConfigInnoDBIOCapacityMax(),
		"%%ENV:SVC_CONF_ENV_INNODB_IO_CAPACITY%%":           server.ClusterGroup.GetConfigInnoDBIOCapacity(),
		"%%ENV:SVC_CONF_ENV_INNODB_IO_CAPACITY_MAX%%":       server.ClusterGroup.GetConfigInnoDBIOCapacityMax(),
		"%%ENV:SVC_CONF_ENV_INNODB_BUFFER_POOL_INSTANCES%%": server.ClusterGroup.GetConfigInnoDBBPInstances(),
		"%%ENV:SVC_CONF_ENV_INNODB_BUFFER_POOL_SIZE%%":      server.ClusterGroup.GetConfigInnoDBBPSize(),
		"%%ENV:SVC_CONF_ENV_INNODB_LOG_BUFFER_SIZE%%":       server.ClusterGroup.GetConfigInnoDBLogFileSize(),
		"%%ENV:SVC_NAMESPACE%%":                             server.ClusterGroup.Name,
		"%%ENV:SVC_NAME%%":                                  server.Name,
		"%%ENV:SVC_CONF_ENV_SST_METHOD%%":                   server.ClusterGroup.Conf.MultiMasterWsrepSSTMethod,
		"%%ENV:SVC_CONF_ENV_DOMAIN_ID%%":                    server.ClusterGroup.Conf.ProvDomain,
	}

	//	size = ` + collector.ProvDisk + `
}
