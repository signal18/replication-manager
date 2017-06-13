package cluster

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/tanji/replication-manager/misc"
	"github.com/tanji/replication-manager/opensvc"
	"github.com/tanji/replication-manager/state"
)

func (cluster *Cluster) OpenSVCConnect() opensvc.Collector {
	var svc opensvc.Collector
	svc.Host, svc.Port = misc.SplitHostPort(cluster.conf.ProvHost)
	svc.User, svc.Pass = misc.SplitPair(cluster.conf.ProvAdminUser)
	svc.RplMgrUser, svc.RplMgrPassword = misc.SplitPair(cluster.conf.ProvUser)
	svc.ProvAgents = cluster.conf.ProvAgents

	svc.ProvMem = cluster.conf.ProvMem
	svc.ProvPwd = cluster.GetDbPass()
	svc.ProvIops = cluster.conf.ProvIops
	svc.ProvDisk = cluster.conf.ProvDisk
	svc.ProvNetMask = cluster.conf.ProvNetmask
	svc.ProvNetGateway = cluster.conf.ProvGateway
	svc.ProvNetIface = cluster.conf.ProvNetIface
	svc.ProvMicroSrv = cluster.conf.ProvType
	svc.ProvFSType = cluster.conf.ProvDiskFS
	svc.ProvFSPool = cluster.conf.ProvDiskPool
	svc.ProvFSMode = cluster.conf.ProvDiskType
	svc.ProvFSPath = cluster.conf.ProvDiskDevice

	return svc
}
func (cluster *Cluster) OpenSVCStopService(server *ServerMonitor) {
	svc := cluster.OpenSVCConnect()
	agents := svc.GetNodes()
	var clusteragents []opensvc.Host

	for _, node := range agents {

		if strings.Contains(svc.ProvAgents, node.Node_name) {
			clusteragents = append(clusteragents, node)
		}
	}
	for i, srv := range cluster.servers {
		agenti := i % len(clusteragents)
		service, _ := svc.GetServiceFromName(srv.Name)
		if srv.Name == server.Name {
			svc.StopService(strconv.Itoa(clusteragents[agenti].Id), strconv.Itoa(service.Id))
		}
	}
}

func (cluster *Cluster) OpenSVCStartService(server *ServerMonitor) {
	svc := cluster.OpenSVCConnect()
	agents := svc.GetNodes()
	var clusteragents []opensvc.Host

	for _, node := range agents {
		if strings.Contains(svc.ProvAgents, node.Node_name) {
			clusteragents = append(clusteragents, node)
		}
	}
	for i, srv := range cluster.servers {
		agenti := i % len(clusteragents)
		if srv.Name == server.Name {
			service, _ := svc.GetServiceFromName(srv.Name)
			svc.StartService(clusteragents[agenti].Node_id, service.Svc_id)
		}
	}
}

func (cluster *Cluster) OpenSVCProvision() error {

	//	err :=  cluster.OpenSVCProvisionOneSrv()
	err := cluster.OpenSVCProvisionOneSrvPerDB()
	return err
}

func (cluster *Cluster) OpenSVCProvisionOneSrvPerDB() error {

	svc := cluster.OpenSVCConnect()
	servers := cluster.GetServers()
	var iplist []string
	var portlist []string
	for i, s := range servers {
		iplist = append(iplist, s.Host)
		portlist = append(portlist, s.Port)

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
			res, err := svc.GenerateTemplate([]string{s.Host}, []string{s.Port}, []opensvc.Host{clusteragents[i%len(clusteragents)]}, s.Name)
			if err != nil {
				return err
			}

			idtemplate, _ := svc.CreateTemplate(s.Name, res)

			for _, node := range agents {
				if strings.Contains(svc.ProvAgents, node.Node_name) {
					idaction, _ := svc.ProvisionTemplate(idtemplate, node.Node_id, s.Name)
					ct := 0
					for {
						time.Sleep(2 * time.Second)
						status := svc.GetActionStatus(strconv.Itoa(idaction))
						if status == "Q" {
							cluster.sme.AddState("WARN00045", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN00045"]), ErrFrom: "TOPO"})
						}
						if status == "W" {
							cluster.sme.AddState("ERR00046", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN00045"]), ErrFrom: "TOPO"})
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
					cluster.LogPrintf("INFO", "%s", task.Stderr)
				}
			}
		}
	}

	return nil
}

func (cluster *Cluster) OpenSVCProvisionOneSrv() error {

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
		res, err := svc.GenerateTemplate(iplist, portlist, clusteragents, "")
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
						cluster.sme.AddState("WARN00045", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN00045"]), ErrFrom: "TOPO"})
					}
					if status == "W" {
						cluster.sme.AddState("ERR00046", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN00045"]), ErrFrom: "TOPO"})
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
				cluster.LogPrintf("INFO", "%s", task.Stderr)
			}
		}
	}

	return nil
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

func (cluster *Cluster) GetMaxscaleTemplate(collector opensvc.Collector, srv ServerMonitor, agent opensvc.Host) (string, error) {

	conf := `
[DEFAULT]
nodes = {env.nodes}
cluster_type = flex
rollback = false
show_disabled = false
docker_data_dir = {env.base_dir}/docker
docker_daemon_args = --log-opt max-size=1m

[fs#00]
type = btrfs
dev = /dev/{env.vg}/{svcname}-docker
mnt = {env.base_dir}/docker
mnt_opt = subvolid=0
mkfs_opt = -O ^extref
vg = {env.vg}
size = 2g

[container#00]
type = docker
run_image = busybox:latest
run_args =  --net=none  -i -t
    -v /etc/localtime:/etc/localtime:ro
run_command = /bin/sh
pre_provision = {svcmgr} -s {svcname} compliance fix --attach --moduleset  mariadb.svc.mrm.proxy --force

[fs#01]
type = xfs
dev = /dev/{env.vg}/{svcname}-pod01
mnt = {env.base_dir}/pod01
size = {env.size}
disable = true
enable_on = {nodes[$(0//(3//{#nodes}))]}
vg = {env.vg}



[ip#01]
tags = sm sm.container sm.container.pod01 pod01
type = docker
ipdev = br0
ipname = {env.ip_pod01}
netmask = 255.255.255.0
network = {env.network_prefix}.0
gateway = {env.gateway}
del_net_route = true
container_rid = container#00
disable = true
enable_on = {nodes[$(0//(3//{#nodes}))]}

[container#01]
tags = pod01
type = docker
run_image = {env.maxscale_img}
run_args = --net=container:{svcname}.container.00
    -e MYSQL_ROOT_PASSWORD=undefined
	    -v /etc/localtime:/etc/localtime:ro
        -v {env.base_dir}/pod01/conf/maxscale.cnf:/etc/maxscale.cnf:rw
        -v {env.base_dir}/pod01/conf/keepalived.conf:/etc/keepalived/keepalived.conf:rw
disable = true
enable_on = {nodes[$(0//(3//{#nodes}))]}



[fs#02]
type = xfs
dev = /dev/{env.vg}/{svcname}-pod02
mnt = {env.base_dir}/pod02
size = {env.size}
disable = true
enable_on = {nodes[$(1//(3//{#nodes}))]}
vg = {env.vg}

[ip#02]
tags = sm sm.container sm.container.pod02 pod02
type = docker
ipdev = br0
ipname = {env.ip_pod02}
netmask = 255.255.255.0
network = {env.network_prefix}.0
gateway = {env.gateway}
del_net_route = true
container_rid = container#00
disable = true
enable_on = {nodes[$(1//(3//{#nodes}))]}


[container#02]
tags = pod02
type = docker
run_image = {env.maxscale_img}
run_args = --net=container:{svcname}.container.00
    -e MYSQL_ROOT_PASSWORD=undefined
	    -v /etc/localtime:/etc/localtime:ro
        -v {env.base_dir}/pod02/conf/maxscale.cnf:/etc/maxscale.cnf:rw
        -v {env.base_dir}/pod02/conf/keepalived.conf:/etc/keepalived/keepalived.conf:rw
disable = true
enable_on = {nodes[$(1//(3//{#nodes}))]}



[fs#03]
type = xfs
dev = /dev/{env.vg}/{svcname}-pod03
mnt = {env.base_dir}/pod03
size = {env.size}
disable = true
enable_on = {nodes[$(2//(3//{#nodes}))]}
vg = {env.vg}

[ip#03]
tags = sm sm.container sm.container.pod03 pod03
type = docker
ipdev = br0
ipname = {env.ip_pod03}
netmask = 255.255.255.0
network = {env.network_prefix}.0
gateway = {env.gateway}
del_net_route = true
container_rid = container#00
disable = true
enable_on = {nodes[$(2//(3//{#nodes}))]}


[container#03]
tags = pod03
type = docker
run_image = {env.replication_manager_img}
run_args = --net=container:{svcname}.container.00
        --cap-add=NET_ADMIN
        -v /etc/localtime:/etc/localtime:ro
        -v {env.base_dir}/pod03/conf/config.toml:/etc/replication-manager/config.toml:rw
disable = true
enable_on = {nodes[$(2//(3//{#nodes}))]}


[env]
vg = data
size = 4g
nodes = node1 node2 node3
network_prefix = 192.168.0
maxscale_img =  tanji/maxscale:keepalived
replication_manager_img = tanji/replication-manager
gateway = {env.network_prefix}.254
ip_pod01 = {env.network_prefix}.244
ip_pod02 = {env.network_prefix}.245
ip_pod03 = {env.network_prefix}.246
vip_addr = {env.network_prefix}.240
vip_netmask = 255.255.255.0
port_rw = 3306
port_rw_split = 3307
port_r_lb = 3308
port_http = 80
mysql_root_password = mariadb
base_dir = /srv/{svcname}
backend_ips = {env.network_prefix}.241,{env.network_prefix}.242,{env.network_prefix}.243
`
	log.Println(conf)

	return conf, nil

}
