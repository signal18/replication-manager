package cluster

import (
	"errors"
	"fmt"
	"log"
	"regexp"
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

	return svc
}

func (cluster *Cluster) OpenSVCUnprovision() {
	opensvc := cluster.OpenSVCConnect()
	agents := opensvc.GetNodes()
	for _, db := range cluster.servers {
		for _, node := range agents {
			for _, svc := range node.Svc {
				if db.Id == svc.Svc_name {
					opensvc.UnprovisionService(node.Node_id, svc.Svc_id)
				}
			}
		}
	}
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
		service, _ := svc.GetServiceFromName(srv.Id)
		if srv.Id == server.Id {
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
		if srv.Id == server.Id {
			service, _ := svc.GetServiceFromName(srv.Id)
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
	agents := svc.GetNodes()
	var clusteragents []opensvc.Host

	for _, node := range agents {
		cluster.LogPrintf("ERROR", "Searching %s %s ", svc.ProvAgents, node.Node_name)

		if strings.Contains(svc.ProvAgents, node.Node_name) {
			clusteragents = append(clusteragents, node)
		}
	}
	if len(clusteragents) == 0 {
		cluster.LogPrintf("ERROR", "No agent found")
		return errors.New("No agent found for this cluster")
	}

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

			res, err := svc.GenerateTemplate([]string{s.Host}, []string{s.Port}, []opensvc.Host{clusteragents[i%len(clusteragents)]}, s.Id)
			if err != nil {
				return err
			}

			idtemplate, _ := svc.CreateTemplate(s.Id, res)

			for _, node := range agents {
				if strings.Contains(svc.ProvAgents, node.Node_name) {
					idaction, _ := svc.ProvisionTemplate(idtemplate, node.Node_id, s.Id)
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

func (cluster *Cluster) GetMaxscaleTemplate(collector opensvc.Collector, servers string, agent opensvc.Host, prx Proxy) (string, error) {

	var net string
	var vm string
	var disk string
	var fs string
	var app string
	ipPods := ""

	conf := `
[DEFAULT]
nodes = {env.nodes}
flex_primary = {env.nodes[0]}
cluster_type = flex
rollback = false
show_disabled = false
`
	log.Println("ProvFSMode " + collector.ProvFSMode)

	if collector.ProvProxMicroSrv == "docker" {
		conf = conf + `
docker_daemon_private = false
docker_data_dir = {env.base_dir}/docker
docker_daemon_args = --log-opt max-size=1m --storage-driver=aufs
`
	}

	if collector.ProvProxMicroSrv == "docker" {
		if collector.ProvProxFSMode == "loopback" {
			disk = `
[disk#00]
type = loop
file = ` + collector.ProvProxFSPath + `/{svcname}_docker.dsk
size = {env.size}

`
			fs = `
[fs#00]
type = ` + collector.ProvProxFSType + `
dev = {disk#00.file}
mnt = {env.base_dir}/docker
size = 2g

`
		}

	}
	i := 1
	pod := fmt.Sprintf("%02d", i+1)

	if collector.ProvProxFSMode == "loopback" {

		disk = disk + `
		[disk#` + pod + `]
		type = loop
		file = ` + collector.ProvProxFSPath + `/{svcname}_pod` + pod + `.dsk
		size = {env.size}

		`
	}
	if collector.ProvProxFSPool == "lvm" {
		disk = disk + `
		[disk#10` + pod + `]
		name = {svcname}_` + pod + `
		type = lvm
		pvs = {disk#` + pod + `.file}

		`
	}

	if collector.ProvProxFSType == "directory" {
		fs = fs + `
		[fs#` + pod + `]
		type = directory
		path = {env.base_dir}/pod` + pod + `
		pre_provision = docker network create {env.subnet_name} --subnet {env.subnet_cidr}

		`

	} else {
		podpool := pod
		if collector.ProvProxFSPool == "lvm" {
			podpool = "10" + pod
		}

		fs = fs + `
		[fs#` + pod + `]
		type = ` + collector.ProvProxFSType + `
		`
		if collector.ProvProxFSPool == "lvm" {
			re := regexp.MustCompile("[0-9]+")
			strlvsize := re.FindAllString(collector.ProvProxDisk, 1)
			lvsize, _ := strconv.Atoi(strlvsize[0])
			lvsize--
			fs = fs + `
		dev = /dev/{svcname}_` + pod + `/pod` + pod + `
		vg = {svcname}_` + pod + `
		size = ` + strconv.Itoa(lvsize) + `g
		`
		} else {
			fs = fs + `
		dev = {disk#` + podpool + `.file}
		size = {env.size}
		`
		}
		fs = fs + `
		mnt = {env.base_dir}/pod` + pod + `
		disable = true
		enable_on = {nodes[$(` + strconv.Itoa(i) + `//(` + strconv.Itoa(len(servers)) + `//{#nodes}))]}

		`

	}

	ipdev := collector.ProvProxNetIface
	net = net + `
		[ip#` + pod + `]
		tags = sm sm.container sm.container.pod` + pod + ` pod` + pod + `
		`
	if collector.ProvProxMicroSrv == "docker" {
		net = net + `
		type = docker
		ipdev = ` + collector.ProvProxNetIface + `
		container_rid = container#00` + pod + `

		`
	} else {
		net = net + `
		ipdev = ` + ipdev + `
		`
	}
	net = net + `
		ipname = {env.ip_pod` + fmt.Sprintf("%02d", i+1) + `}
		netmask = {env.netmask}
		network = {env.network}
		gateway = {env.gateway}
		disable = true
		enable_on = {nodes[$(` + strconv.Itoa(i) + `//(` + strconv.Itoa(len(servers)) + `//{#nodes}))]}
		`
	ipPods = ipPods + `ip_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + prx.Host + `
		`
	if collector.ProvProxMicroSrv == "docker" {
		vm = vm + `
		[container#00` + pod + `]
		type = docker
		run_image = busybox:latest
		run_args =  --net=none  -i -t
		-v /etc/localtime:/etc/localtime:ro
		run_command = /bin/sh

		[container#20` + pod + `]
		tags = pod` + pod + `
		type = docker
		run_image =run_image = {env.maxscale_img}
		run_args = --net=container:{svcname}.container.00
		    -e MYSQL_ROOT_PASSWORD=undefined
			    -v /etc/localtime:/etc/localtime:ro
		        -v {env.base_dir}/pod` + pod + `/conf/maxscale.cnf:/etc/maxscale.cnf:rw
		        -v {env.base_dir}/pod` + pod + `/conf/keepalived.conf:/etc/keepalived/keepalived.conf:rw
		disable = true
		enable_on = {nodes[$(1//(3//{#nodes}))]}
		`
	}
	if collector.ProvProxMicroSrv == "package" {
		app = app + `
		[app#` + pod + `]
		script = {env.base_dir}/pod` + pod + `/init/launcher
		start = 50
		stop = 50
		check = 50
		info = 50
		`
	}

	conf = conf + disk
	conf = conf + fs
	conf = conf + `
		post_provision = {svcmgr} -s {svcname} push service status;{svcmgr} -s {svcname} compliance fix --attach --moduleset mariadb.svc.mrm.proxy
		`
	conf = conf + net
	conf = conf + vm
	conf = conf + app
	ips := strings.Split(collector.ProvProxNetGateway, ".")
	masks := strings.Split(collector.ProvProxNetMask, ".")
	for i, mask := range masks {
		if mask == "0" {
			ips[i] = "0"
		}
	}
	network := strings.Join(ips, ".")
	conf = conf + `
[env]
nodes = ` + collector.ProvAgents + `
size = ` + collector.ProvDisk + `
` + ipPods + `
mysql_root_password = ` + collector.ProvPwd + `
network = ` + network + `
gateway =  ` + collector.ProvProxNetGateway + `
netmask =  ` + collector.ProvProxNetMask + `
maxscale_img =  asosso/maxscale:latest
ip_pod01 = {env.network_prefix}.244
vip_addr = {env.network_prefix}.240
port_rw = ` + strconv.Itoa(prx.ReadWritePort) + `
port_rw_split =  ` + strconv.Itoa(prx.ReadWritePort) + `
port_r_lb =  ` + strconv.Itoa(prx.ReadPort) + `
port_http = 80

base_dir = /srv/{svcname}
backend_ips = ` + servers + `
`
	log.Println(conf)

	return conf, nil
}
