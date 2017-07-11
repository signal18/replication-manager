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
	svc.Verbose = 1

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
	err = cluster.OpenSVCProvisionProxies()

	return err
}

func (cluster *Cluster) OpenSVCProvisionProxies() error {

	svc := cluster.OpenSVCConnect()
	agents := svc.GetNodes()
	var clusteragents []opensvc.Host
	for _, node := range agents {
		cluster.LogPrintf("ERROR", "Searching %s %s ", svc.ProvProxAgents, node.Node_name)

		if strings.Contains(svc.ProvProxAgents, node.Node_name) {
			clusteragents = append(clusteragents, node)
		}
	}
	if len(clusteragents) == 0 {
		cluster.LogPrintf("ERROR", "No agent found")
		return errors.New("No agent found for Proxy on this cluster")
	}
	srvlist := make([]string, len(cluster.servers))
	for i, s := range cluster.servers {
		srvlist[i] = s.Host
	}

	for i, prx := range cluster.proxies {
		if prx.Type == proxyMaxscale {
			if strings.Contains(svc.ProvAgents, clusteragents[i%len(clusteragents)].Node_name) {
				res, err := cluster.GetMaxscaleTemplate(svc, strings.Join(srvlist, " "), clusteragents[i%len(clusteragents)], prx)
				if err != nil {
					return err
				}
				idtemplate, err := svc.CreateTemplate(prx.Id, res)
				if err != nil {
					return err
				}

				idaction, _ := svc.ProvisionTemplate(idtemplate, clusteragents[i%len(clusteragents)].Node_id, prx.Id)
				cluster.OpenSVCWaitDequeue(svc, idaction)
				task := svc.GetAction(strconv.Itoa(idaction))
				cluster.LogPrintf("INFO", "%s", task.Stderr)
			}
		}

	}

	return nil
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

			res, err := cluster.GenerateDBTemplate(svc, []string{s.Host}, []string{s.Port}, []opensvc.Host{clusteragents[i%len(clusteragents)]}, s.Id, clusteragents[i%len(clusteragents)].Node_name)
			if err != nil {
				return err
			}
			idtemplate, _ := svc.CreateTemplate(s.Id, res)
			idaction, _ := svc.ProvisionTemplate(idtemplate, clusteragents[i%len(clusteragents)].Node_id, s.Id)
			cluster.OpenSVCWaitDequeue(svc, idaction)
			task := svc.GetAction(strconv.Itoa(idaction))
			cluster.LogPrintf("INFO", "%s", task.Stderr)
			cluster.WaitMariaDBStart(s)
		}

	}

	return nil
}

func (cluster *Cluster) OpenSVCWaitDequeue(svc opensvc.Collector, idaction int) {
	ct := 0
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
			break
		}
		ct++
		if ct > 200 {
			break
		}

	}
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
						cluster.sme.AddState("ERR0046", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0045"]), ErrFrom: "TOPO"})
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

func (cluster *Cluster) GetMaxscaleTemplate(collector opensvc.Collector, servers string, agent opensvc.Host, prx *Proxy) (string, error) {

	ipPods := ""

	conf := `
[DEFAULT]
nodes = {env.nodes}
flex_primary = {env.nodes[0]}
cluster_type = flex
rollback = false
show_disabled = false
`
	conf = conf + cluster.GetDockerDiskTemplate(collector)
	i := 0
	pod := fmt.Sprintf("%02d", i+1)
	conf = conf + cluster.GetPodDiskTemplate(collector, pod)
	conf = conf + `post_provision = {svcmgr} -s {svcname} push service status;{svcmgr} -s {svcname} compliance fix --attach --moduleset mariadb.svc.mrm.proxy
`
	conf = conf + cluster.GetPodNetTemplate(collector, pod, i)
	conf = conf + cluster.GetPodDockerProxyTemplate(collector, pod)
	conf = conf + cluster.GetPodPackageTemplate(collector, pod)
	ipPods = ipPods + `ip_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + prx.Host + `
`
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
nodes = ` + agent.Node_name + `
size = ` + collector.ProvDisk + `
` + ipPods + `
mysql_root_password = ` + collector.ProvPwd + `
network = ` + network + `
gateway =  ` + collector.ProvProxNetGateway + `
netmask =  ` + collector.ProvProxNetMask + `
maxscale_img =  asosso/maxscale:latest
vip_addr =  ` + prx.Host + `
vip_netmask =  ` + collector.ProvProxNetMask + `
port_rw = ` + strconv.Itoa(prx.WritePort) + `
port_rw_split =  ` + strconv.Itoa(prx.ReadWritePort) + `
port_r_lb =  ` + strconv.Itoa(prx.ReadPort) + `
port_http = 80
base_dir = /srv/{svcname}
backend_ips = ` + servers + `
port_binlog = ` + strconv.Itoa(cluster.conf.MxsBinlogPort) + `
port_telnet = ` + prx.Port + `
port_admin = ` + prx.Port + `
user_admin = ` + prx.User + `
password_admin = ` + prx.Pass + `
`
	log.Println(conf)
	return conf, nil
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

func (cluster *Cluster) GenerateDBTemplate(collector opensvc.Collector, servers []string, ports []string, agents []opensvc.Host, name string, agent string) (string, error) {

	ipPods := ""
	portPods := ""

	conf := `
[DEFAULT]
nodes = {env.nodes}
flex_primary = {env.nodes[0]}
cluster_type = flex
rollback = false
show_disabled = false
`
	conf = conf + cluster.GetDockerDiskTemplate(collector)
	//main loop over db instances
	for i, host := range servers {
		pod := fmt.Sprintf("%02d", i+1)
		conf = conf + cluster.GetPodDiskTemplate(collector, pod)
		conf = conf + `post_provision = {svcmgr} -s {svcname} push service status;{svcmgr} -s {svcname} compliance fix --attach --moduleset mariadb.svc.mrm.db
	`
		conf = conf + cluster.GetPodNetTemplate(collector, pod, i)
		conf = conf + cluster.GetPodDockerDBTemplate(collector, pod)
		conf = conf + cluster.GetPodPackageTemplate(collector, pod)
		ipPods = ipPods + `ip_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + host + `
	`
		portPods = portPods + `port_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + ports[i] + `
	`
	}
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
size = ` + collector.ProvDisk + `
db_img = mariadb:latest
` + ipPods + `
` + portPods + `
mysql_root_password = ` + collector.ProvPwd + `
network = ` + network + `
gateway =  ` + collector.ProvNetGateway + `
netmask =  ` + collector.ProvNetMask + `
base_dir = /srv/{svcname}
max_iops = ` + collector.ProvIops + `
max_mem = ` + collector.ProvMem + `
micro_srv = ` + collector.ProvMicroSrv + `
`
	log.Println(conf)

	return conf, nil
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
size = ` + strconv.Itoa(lvsize) + `g
`
		} else if collector.ProvFSPool == "zpool" {
			fs = fs + `
dev = {disk#` + podpool + `.name}/pod` + pod + `
size = {env.size}
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
docker_daemon_private = false
docker_data_dir = {env.base_dir}/docker
docker_daemon_args = --log-opt max-size=1m --storage-driver=aufs
`

	if collector.ProvFSMode == "loopback" {
		disk = `
[disk#00]
type = loop
file = ` + collector.ProvFSPath + `/{svcname}_docker.dsk
size = {env.size}

`
		if collector.ProvFSPool == "zpool" {
			disk = disk + `
[disk#0000]
name = zp{svcname}_00
type = zpool
vdev  = {disk#00.file}

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

func (cluster *Cluster) GetPodDockerDBTemplate(collector opensvc.Collector, pod string) string {
	var vm string
	if collector.ProvMicroSrv == "docker" {
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
run_image = {env.db_img}
run_args = --net=container:{svcname}.container.00` + pod + `
 -e MYSQL_ROOT_PASSWORD={env.mysql_root_password}
 -e MYSQL_INITDB_SKIP_TZINFO=yes
 -v /etc/localtime:/etc/localtime:ro
 -v {env.base_dir}/pod` + pod + `/data:/var/lib/mysql:rw
 -v {env.base_dir}/pod` + pod + `/etc/mysql:/etc/mysql:rw
 -v {env.base_dir}/pod` + pod + `/init:/docker-entrypoint-initdb.d:rw
`
	}
	return vm
}

func (cluster *Cluster) GetPodDockerProxyTemplate(collector opensvc.Collector, pod string) string {
	var vm string
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
run_image = {env.maxscale_img}
run_args = --net=container:{svcname}.container.00` + pod + `
    -v /etc/localtime:/etc/localtime:ro
    -v {env.base_dir}/pod` + pod + `/conf:/etc/maxscale.d:rw
`
	}
	return vm
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
