package cluster

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
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
	svc.ProvAgents = cluster.conf.ProvAgents
	svc.ProvMem = cluster.conf.ProvMem
	svc.ProvPwd = cluster.GetDbPass()
	svc.ProvIops = cluster.conf.ProvIops
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
	svc.Verbose = 1

	return svc
}

func (cluster *Cluster) OpenSVCUnprovision() {
	opensvc := cluster.OpenSVCConnect()
	agents := opensvc.GetNodes()
	for _, node := range agents {
		for _, svc := range node.Svc {
			for _, db := range cluster.servers {
				if db.Id == svc.Svc_name {
					idaction, err := opensvc.UnprovisionService(node.Node_id, svc.Svc_id)
					if err != nil {
						cluster.LogPrintf("ERROR", "Can't unprovision database %s, %s", db.Id, err)
					} else {
						err := cluster.OpenSVCWaitDequeue(opensvc, idaction)
						if err != nil {
							cluster.LogPrintf("ERROR", "Can't unprovision database %s, %s", db.Id, err)
						}
					}
					opensvc.DeleteService(svc.Svc_id)
				}
			}
			for _, prx := range cluster.proxies {
				if prx.Id == svc.Svc_name {
					idaction, err := opensvc.UnprovisionService(node.Node_id, svc.Svc_id)
					if err != nil {
						cluster.LogPrintf("ERROR", "Can't unprovision proxy %s, %s", prx.Id, err)
					} else {
						err := cluster.OpenSVCWaitDequeue(opensvc, idaction)
						if err != nil {
							cluster.LogPrintf("ERROR", "Can't unprovision proxy %s, %s", prx.Id, err)
						}
					}
					opensvc.DeleteService(svc.Svc_id)
				}
			}
		}
	}
}

func (cluster *Cluster) OpenSVCUnprovisionDatabaseService(db *ServerMonitor) {
	opensvc := cluster.OpenSVCConnect()
	agents := opensvc.GetNodes()
	for _, node := range agents {
		for _, svc := range node.Svc {
			if db.Id == svc.Svc_name {
				idaction, _ := opensvc.UnprovisionService(node.Node_id, svc.Svc_id)

				err := cluster.OpenSVCWaitDequeue(opensvc, idaction)
				if err != nil {
					cluster.LogPrintf("ERROR", "Can't unprovision database %s, %s", db.Id, err)
				}
				opensvc.DeleteService(svc.Svc_id)
			}
		}
	}
}

func (cluster *Cluster) OpenSVCUnprovisionProxyService(prx *Proxy) {
	opensvc := cluster.OpenSVCConnect()
	agents := opensvc.GetNodes()
	for _, node := range agents {
		for _, svc := range node.Svc {
			if prx.Id == svc.Svc_name {
				idaction, _ := opensvc.UnprovisionService(node.Node_id, svc.Svc_id)
				err := cluster.OpenSVCWaitDequeue(opensvc, idaction)
				if err != nil {
					cluster.LogPrintf("ERROR", "Can't unprovision proxy %s, %s", prx.Id, err)
				}
				opensvc.DeleteService(svc.Svc_id)

			}
		}
	}
}

func (cluster *Cluster) OpenSVCStopDatabaseService(server *ServerMonitor) error {
	svc := cluster.OpenSVCConnect()
	service, err := svc.GetServiceFromName(server.Id)
	if err != nil {
		return err
	}
	agent, err := cluster.FoundDatabaseAgent(server)
	if err != nil {
		return err
	}
	svc.StopService(agent.Node_id, service.Svc_id)
	return nil
}

func (cluster *Cluster) FoundDatabaseAgent(server *ServerMonitor) (opensvc.Host, error) {
	svc := cluster.OpenSVCConnect()
	agents := svc.GetNodes()
	var clusteragents []opensvc.Host
	var agent opensvc.Host
	for _, node := range agents {
		if strings.Contains(svc.ProvAgents, node.Node_name) {
			clusteragents = append(clusteragents, node)
		}
	}
	for i, srv := range cluster.servers {
		if srv.Id == server.Id {
			return clusteragents[i%len(clusteragents)], nil
		}
	}
	return agent, errors.New("Indice not found in database agent list")
}

func (cluster *Cluster) FoundProxyAgent(proxy *Proxy) (opensvc.Host, error) {
	svc := cluster.OpenSVCConnect()
	agents := svc.GetNodes()
	var clusteragents []opensvc.Host
	var agent opensvc.Host
	for _, node := range agents {
		if strings.Contains(svc.ProvProxAgents, node.Node_name) {
			clusteragents = append(clusteragents, node)
		}
	}
	for i, srv := range cluster.proxies {
		if srv.Id == proxy.Id {
			return clusteragents[i%len(clusteragents)], nil
		}
	}
	return agent, errors.New("Indice not found in proxies agent list")
}

func (cluster *Cluster) OpenSVCStartService(server *ServerMonitor) error {
	svc := cluster.OpenSVCConnect()
	service, err := svc.GetServiceFromName(server.Id)
	if err != nil {
		return err
	}
	agent, err := cluster.FoundDatabaseAgent(server)
	if err != nil {
		return err
	}
	svc.StartService(agent.Node_id, service.Svc_id)
	return nil
}

func (cluster *Cluster) OpenSVCProvisionCluster() error {

	err := cluster.OpenSVCProvisionOneSrvPerDB()
	err = cluster.OpenSVCProvisionProxies()
	return err
}

func (cluster *Cluster) OpenSVCProvisionProxies() error {

	for _, prx := range cluster.proxies {
		cluster.OpenSVCProvisionProxyService(prx)
	}

	return nil
}

func (cluster *Cluster) OpenSVCProvisionProxyService(prx *Proxy) error {
	svc := cluster.OpenSVCConnect()
	agent, err := cluster.FoundProxyAgent(prx)
	if err != nil {
		return err
	}
	// Unprovision if already in OpenSVC
	mysrv, err := svc.GetServiceFromName(prx.Id)
	if err == nil {
		cluster.LogPrintf("INFO", "Unprovision opensvc proxy service %s service %s", prx.Id, mysrv.Svc_id)
		cluster.UnprovisionProxyService(prx)
	}

	srvlist := make([]string, len(cluster.servers))
	for i, s := range cluster.servers {
		srvlist[i] = s.Host
	}

	if prx.Type == proxyMaxscale {
		if strings.Contains(svc.ProvAgents, agent.Node_name) {
			res, err := cluster.GetMaxscaleTemplate(svc, strings.Join(srvlist, " "), agent, prx)
			if err != nil {
				return err
			}
			idtemplate, err := svc.CreateTemplate(prx.Id, res)
			if err != nil {
				return err
			}

			idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, prx.Id)
			cluster.OpenSVCWaitDequeue(svc, idaction)
			task := svc.GetAction(strconv.Itoa(idaction))
			if task != nil {
				cluster.LogPrintf("INFO", "%s", task.Stderr)
			} else {
				cluster.LogPrintf("ERROR", "Can't fetch task")
			}
		}
	}
	if prx.Type == proxySpider {
		if strings.Contains(svc.ProvAgents, agent.Node_name) {
			res, err := cluster.GenerateDBTemplate(svc, []string{prx.Host}, []string{prx.Port}, []opensvc.Host{agent}, prx.Id, agent.Node_name)
			if err != nil {
				return err
			}
			idtemplate, err := svc.CreateTemplate(prx.Id, res)
			if err != nil {
				return err
			}

			idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, prx.Id)
			cluster.OpenSVCWaitDequeue(svc, idaction)
			task := svc.GetAction(strconv.Itoa(idaction))
			if task != nil {
				cluster.LogPrintf("INFO", "%s", task.Stderr)
			} else {
				cluster.LogPrintf("ERROR", "Can't fetch task")
			}
		}
	}
	if prx.Type == proxyHaproxy {
		if strings.Contains(svc.ProvAgents, agent.Node_name) {
			res, err := cluster.GetHaproxyTemplate(svc, strings.Join(srvlist, " "), agent, prx)
			if err != nil {
				return err
			}
			idtemplate, err := svc.CreateTemplate(prx.Id, res)
			if err != nil {
				return err
			}

			idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, prx.Id)
			cluster.OpenSVCWaitDequeue(svc, idaction)
			task := svc.GetAction(strconv.Itoa(idaction))
			if task != nil {
				cluster.LogPrintf("INFO", "%s", task.Stderr)
			} else {
				cluster.LogPrintf("ERROR", "Can't fetch task")
			}
		}
	}
	if prx.Type == proxySqlproxy {
		if strings.Contains(svc.ProvAgents, agent.Node_name) {
			res, err := cluster.GetProxysqlTemplate(svc, strings.Join(srvlist, " "), agent, prx)
			if err != nil {
				return err
			}
			idtemplate, err := svc.CreateTemplate(prx.Id, res)
			if err != nil {
				return err
			}

			idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, prx.Id)
			cluster.OpenSVCWaitDequeue(svc, idaction)
			task := svc.GetAction(strconv.Itoa(idaction))
			if task != nil {
				cluster.LogPrintf("INFO", "%s", task.Stderr)
			} else {
				cluster.LogPrintf("ERROR", "Can't fetch task")
			}
		}
	}
	return nil
}

func (cluster *Cluster) OpenSVCProvisionDatabaseService(s *ServerMonitor) error {

	svc := cluster.OpenSVCConnect()
	var taglist []string

	agent, err := cluster.FoundDatabaseAgent(s)
	if err != nil {
		return err
	}

	// Unprovision if already in OpenSVC

	mysrv, err := svc.GetServiceFromName(s.Id)
	if err == nil {
		cluster.LogPrintf("INFO", "Unprovision opensvc database service %s service %s", s.Id, mysrv.Svc_id)
		cluster.UnprovisionDatabaseService(s)
	}

	//	idsrv := mysrv.Svc_id
	//	if idsrv == "" || err != nil {
	idsrv, err := svc.CreateService(s.Id, "MariaDB")
	if err != nil {
		cluster.LogPrintf("ERROR", "Can't create OpenSVC service")
		return err
	}
	//	}
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
	res, err := cluster.GenerateDBTemplate(svc, []string{s.Host}, []string{s.Port}, []opensvc.Host{agent}, s.Id, agent.Node_name)
	if err != nil {
		return err
	}
	idtemplate, _ := svc.CreateTemplate(s.Id, res)
	idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, s.Id)
	cluster.OpenSVCWaitDequeue(svc, idaction)
	task := svc.GetAction(strconv.Itoa(idaction))
	if task != nil {
		cluster.LogPrintf("INFO", "%s", task.Stderr)
	} else {
		cluster.LogPrintf("ERROR", "Can't fetch task")
	}
	cluster.WaitDatabaseStart(s)

	return nil
}

func (cluster *Cluster) OpenSVCProvisionOneSrvPerDB() error {

	for _, s := range cluster.servers {
		err := cluster.OpenSVCProvisionDatabaseService(s)
		if err != nil {
			return err
		}
	}
	return nil
}

func (cluster *Cluster) OpenSVCWaitDequeue(svc opensvc.Collector, idaction int) error {
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
			return nil
		}
		ct++
		if ct > 200 {
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
					cluster.LogPrintf("INFO", "%s", task.Stderr)
				} else {
					cluster.LogPrintf("ERROR", "Can't fetch task")
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

func (cluster *Cluster) GetHaproxyTemplate(collector opensvc.Collector, servers string, agent opensvc.Host, prx *Proxy) (string, error) {

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
	conf = conf + cluster.GetPodDockerHaproxyTemplate(collector, pod)
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
maxscale_img = ` + collector.ProvProxDockerMaxscaleImg + `
haproxy_img = ` + collector.ProvProxDockerHaproxyImg + `
proxysql_img = ` + collector.ProvProxDockerHaproxyImg + `
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

func (cluster *Cluster) GetProxysqlTemplate(collector opensvc.Collector, servers string, agent opensvc.Host, prx *Proxy) (string, error) {

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
	conf = conf + cluster.GetPodDockerProxysqlTemplate(collector, pod)
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
maxscale_img = ` + collector.ProvProxDockerMaxscaleImg + `
haproxy_img = ` + collector.ProvProxDockerHaproxyImg + `
proxysql_img = ` + collector.ProvProxDockerProxysqlImg + `
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
	conf = conf + cluster.GetPodDockerMaxscaleTemplate(collector, pod)
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
maxscale_img = ` + collector.ProvProxDockerMaxscaleImg + `
haproxy_img = ` + collector.ProvProxDockerHaproxyImg + `
proxysql_img = ` + collector.ProvProxDockerProxysqlImg + `
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
db_img = ` + collector.ProvDockerImg + `
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
size = 100%FREE
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
docker_daemon_private = true
docker_data_dir = {env.base_dir}/docker
docker_daemon_args = --log-opt max-size=1m --storage-driver=overlay
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
run_args =  --net=container:{svcname}.container.00` + pod + `
 -e MYSQL_ROOT_PASSWORD={env.mysql_root_password}
 -e MYSQL_INITDB_SKIP_TZINFO=yes
 -v /etc/localtime:/etc/localtime:ro
 -v {env.base_dir}/pod` + pod + `/data:/var/lib/mysql:rw
 -v {env.base_dir}/pod` + pod + `/etc/mysql:/etc/mysql:rw
 -v {env.base_dir}/pod` + pod + `/init:/docker-entrypoint-initdb.d:rw
`
		if dockerMinusRm {
			vm = vm + ` --rm
`
		}
	}
	return vm
}

func (cluster *Cluster) GetPodDockerMaxscaleTemplate(collector opensvc.Collector, pod string) string {
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
		if dockerMinusRm {
			vm = vm + ` --rm
`
		}
	}
	return vm
}

func (cluster *Cluster) GetPodDockerHaproxyTemplate(collector opensvc.Collector, pod string) string {
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
run_image = {env.haproxy_img}
run_args = --net=container:{svcname}.container.00` + pod + `
    -v /etc/localtime:/etc/localtime:ro
    -v {env.base_dir}/pod` + pod + `/conf:/usr/local/etc/haproxy:rw
`
		if dockerMinusRm {
			vm = vm + ` --rm
`
		}
	}
	return vm
}

func (cluster *Cluster) GetPodDockerProxysqlTemplate(collector opensvc.Collector, pod string) string {
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
run_image = {env.proxysql_img}
run_args = --net=container:{svcname}.container.00` + pod + `
    -v /etc/localtime:/etc/localtime:ro
    -v {env.base_dir}/pod` + pod + `/conf/proxysql.cfg:/etc/proxysql.cfg:rw
`
		if dockerMinusRm {
			vm = vm + ` --rm
`
		}
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

func (cluster *Cluster) OpenSVCProvisionReloadHaproxyConf(Conf string) string {
	svc := cluster.OpenSVCConnect()
	svc.SetRulesetVariableValue("mariadb.svc.mrm.proxt.cnf.haproxy", "proxy_cnf_haproxy", Conf)
	return ""
}
