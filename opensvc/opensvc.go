// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
package opensvc

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type Addr struct {
	Addr          string `json:"addr"`
	Addr_type     string `json:"addr_type"`
	Mask          string `json:"mask"`
	Net_broadcast string `json:"net_broadcast"`
	Net_gateway   string `json:"net_gateway"`
	Net_name      string `json:"net_name"`
	Net_netmask   string `json:"net_netmask"`
	Net_network   string `json:"net_network"`
	Net_id        int    `json:"id"`
	Net_intf      string `json:"intf"`
}

type Group struct {
	Id int `json:"id"`
}

type Action struct {
	Id     int    `json:"id"`
	Status string `json:"status"`
	Stderr string `json:"stderr"`
}

type Host struct {
	Id        int    `json:"id"`
	Node_id   string `json:"node_id"`
	Node_name string `json:"nodename"`
	Cpu_cores int    `json:"cpu_cores"`
	Cpu_freq  string `json:"cpu_freq"`
	Mem_bytes int    `json:"mem_bytes"`
	Os_kernel string `json:"os_kernel"`
	Os_name   string `json:"os_name"`
	Ips       []Addr
}
type Service struct {
	Id         int    `json:"id"`
	Svc_name   string `json:"svcname"`
	Svc_status string `json:"svc_status"`
	Updated    string `json:"updated"`
	Svc_id     string `json:"svc_id"`
}

type HostList []*Host

type Collector struct {
	Host               string
	Port               string
	User               string
	Pass               string
	RplMgrUser         string
	RplMgrPassword     string
	ProvAgents         string
	ProvMem            string
	ProvIops           string
	ProvDisk           string
	ProvPwd            string
	ProvNetMask        string
	ProvNetGateway     string
	ProvNetIface       string
	ProvMicroSrv       string
	ProvFSType         string
	ProvFSPool         string
	ProvFSMode         string
	ProvFSPath         string
	ProvProxAgents     string
	ProvProxDisk       string
	ProvProxNetMask    string
	ProvProxNetGateway string
	ProvProxNetIface   string
	ProvProxMicroSrv   string
	ProvProxFSType     string
	ProvProxFSPool     string
	ProvProxFSMode     string
	ProvProxFSPath     string

	Verbose int
}

//Imput template URI [system|docker].[zfs|xfs|ext4|btrfs].[none|zpool|lvm].[loopback|physical].[path-to-file|/dev/xx]

func (collector *Collector) GenerateTemplate(servers []string, ports []string, agents []Host, name string) (string, error) {

	var net string
	var vm string
	var disk string
	var fs string
	var app string
	conf := `
[DEFAULT]
nodes = {env.nodes}
flex_primary = {env.nodes[0]}
cluster_type = flex
rollback = false
show_disabled = false
`
	log.Println("ProvFSMode " + collector.ProvFSMode)

	if collector.ProvMicroSrv == "docker" {
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
			fs = `
[fs#00]
type = ` + collector.ProvFSType + `
dev = {disk#00.file}
mnt = {env.base_dir}/docker
size = 2g

`
		}

	}

	ipPods := ""
	portPods := ""
	//main loop over db instances
	for i, host := range servers {
		pod := fmt.Sprintf("%02d", i+1)

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

		if collector.ProvFSType == "directory" {
			fs = fs + `
[fs#` + pod + `]
type = directory
path = {env.base_dir}/pod` + pod + `
pre_provision = docker network create {env.subnet_name} --subnet {env.subnet_cidr}

`

		} else {
			podpool := pod
			if collector.ProvFSPool == "lvm" {
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
		ipdev := collector.ProvNetIface
		net = net + `
[ip#` + pod + `]
tags = sm sm.container sm.container.pod` + pod + ` pod` + pod + `
`
		if collector.ProvMicroSrv == "docker" {
			net = net + `
type = docker
ipdev = ` + collector.ProvNetIface + `
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
		//Use in gcloud
		//del_net_route = true

		ipPods = ipPods + `ip_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + host + `
`
		portPods = portPods + `port_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + ports[i] + `
`
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
disable = true
enable_on = {nodes[$(` + strconv.Itoa(i) + `//(` + strconv.Itoa(len(servers)) + `//{#nodes}))]}

`
		}
		if collector.ProvMicroSrv == "package" {
			app = app + `
[app#` + pod + `]
script = {env.base_dir}/pod` + pod + `/init/launcher
start = 50
stop = 50
check = 50
info = 50
`
		}

	}
	conf = conf + disk
	conf = conf + fs
	conf = conf + `
post_provision = {svcmgr} -s {svcname} push service status;{svcmgr} -s {svcname} compliance fix --attach --moduleset mariadb.svc.mrm.db
`
	conf = conf + net
	conf = conf + vm
	conf = conf + app
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
nodes = ` + collector.ProvAgents + `
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

func (collector *Collector) Bootstrap(path string) error {
	userid, err := collector.CreateMRMUser(collector.RplMgrUser, collector.RplMgrPassword)
	if err != nil {
		return err
	}
	groupid, err := collector.CreateMRMGroup()
	if err != nil {
		return err
	}
	// notion de groups de privileges = notion roles
	groups, err := collector.GetGroups()
	if err != nil {
		return err
	}
	for _, grp := range groups {
		collector.SetGroupUser(grp.Id, userid)
	}
	appid, err := collector.CreateAppCode("MariaDB")
	if err != nil {
		return err
	}
	// group organization
	_, err = collector.SetGroupUser(groupid, userid)
	if err != nil {
		return err
	}
	_, err = collector.SetPrimaryGroup(groupid, userid)
	if err != nil {
		return err
	}

	_, err = collector.SetAppCodeResponsible(appid, groupid)
	if err != nil {
		return err
	}
	_, err = collector.SetAppCodePublication(appid, groupid)
	if err != nil {
		return err
	}
	collector.ImportCompliance(path + "moduleset_mariadb.node.kernel.json")
	collector.ImportCompliance(path + "moduleset_mariadb.node.network.json")
	collector.ImportCompliance(path + "moduleset_mariadb.node.opensvc.json")
	collector.ImportCompliance(path + "moduleset_mariadb.node.packages.json")
	collector.ImportCompliance(path + "moduleset_mariadb.svc.mrm.db.json")
	collector.ImportCompliance(path + "moduleset_mariadb.svc.mrm.proxy.json")

	//	collector.ImportCompliance(path + "moduleset_mariadb.svc.mrm.db.cnf.json")
	return nil
}

func (collector *Collector) CreateMRMGroup() (int, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/groups"
	log.Println("INFO ", urlpost)
	data := url.Values{}
	data.Add("role", "replication-manager")
	data.Add("privilege", "F")
	b := bytes.NewBuffer([]byte(data.Encode()))
	req, err := http.NewRequest("POST", urlpost, b)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err != nil {
		return 0, err
	}
	req.SetBasicAuth(collector.User, collector.Pass)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	type Priv struct {
		Privilege   bool   `json:"privilege"`
		Role        string `json:"role"`
		Id          int    `json:"id"`
		Description string `json:"description"`
	}
	type Message struct {
		Info string `json:"info"`
		Data []Priv `json:"data"`
	}
	var m Message
	err = json.Unmarshal(body, &m)
	if err != nil {
		return 0, err
	}

	log.Println(string(body))
	groupid := m.Data[0].Id

	return groupid, nil

}

// CreateTemplate post a template to the collector
func (collector *Collector) CreateTemplate(name string, template string) (int, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/provisioning_templates"
	log.Println("INFO ", urlpost)
	data := url.Values{}
	data.Add("tpl_definition", template)
	data.Add("tpl_name", name)

	b := bytes.NewBuffer([]byte(data.Encode()))
	req, err := http.NewRequest("POST", urlpost, b)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err != nil {

		return 0, err
	}
	req.SetBasicAuth(collector.User, collector.Pass)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	type Template struct {
		Id int `json:"id"`
	}
	type Message struct {
		Info string     `json:"info"`
		Data []Template `json:"data"`
	}
	var m Message
	err = json.Unmarshal(body, &m)
	if err != nil {
		log.Println(string(body))
		return 0, err

	}
	var tempid int
	if len(m.Data) > 0 {
		tempid = m.Data[0].Id
	} else {
		log.Println(string(body))
	}

	return tempid, nil

}

func (collector *Collector) ProvisionTemplate(id int, nodeid string, name string) (int, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/provisioning_templates/" + strconv.Itoa(id)
	log.Println("INFO ", urlpost)

	var jsonStr = []byte(`{"svcname":"` + name + `","node_id":"` + nodeid + `"}`)

	req, err := http.NewRequest("PUT", urlpost, bytes.NewBuffer(jsonStr))
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-Custom-Header", "myvalue")
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(collector.User, collector.Pass)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	log.Println(string(body))
	type Message struct {
		Data []Action `json:"data"`
	}
	var m Message
	err = json.Unmarshal(body, &m)
	if err != nil {
		log.Println(string(body))
		return 0, err

	}
	var actionid int
	if len(m.Data) > 0 {
		actionid = m.Data[0].Id
	} else {
		log.Println(string(body))
	}

	return actionid, nil
}

func (collector *Collector) CreateMRMUser(user string, password string) (int, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/users"
	log.Println("INFO ", urlpost)
	data := url.Values{}
	data.Add("email", user)
	data.Add("first_name", "replication-manager")
	data.Add("last_name", user)
	data.Add("password", password)
	b := bytes.NewBuffer([]byte(data.Encode()))
	req, err := http.NewRequest("POST", urlpost, b)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err != nil {
		return 0, err
	}
	req.SetBasicAuth(collector.User, collector.Pass)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	type User struct {
		Id int `json:"id"`
	}
	type Message struct {
		Info string `json:"info"`
		Data []User `json:"data"`
	}
	var m Message
	err = json.Unmarshal(body, &m)
	if err != nil {
		log.Println(string(body))
		return 0, err

	}
	var userid int
	if len(m.Data) > 0 {
		userid = m.Data[0].Id
	} else {
		log.Println(string(body))
	}

	return userid, nil

}

func (collector *Collector) SetAppCodeResponsible(appid int, groupid int) (string, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/apps/" + strconv.Itoa(appid) + "/responsibles/" + strconv.Itoa(groupid)
	log.Println("INFO ", urlpost)
	data := url.Values{}
	b := bytes.NewBuffer([]byte(data.Encode()))
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	req.SetBasicAuth(collector.User, collector.Pass)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	type response struct {
		Info string `json:"info"`
	}
	var r response
	err = json.Unmarshal(body, &r)
	if err != nil {
		return "", err
	}
	log.Println(string(body))
	return string(body), nil

}

func (collector *Collector) SetAppCodePublication(appid int, groupid int) (string, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/apps/" + strconv.Itoa(appid) + "/publications/" + strconv.Itoa(groupid)
	log.Println("INFO ", urlpost)
	data := url.Values{}
	b := bytes.NewBuffer([]byte(data.Encode()))
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	req.SetBasicAuth(collector.User, collector.Pass)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	type response struct {
		Info string `json:"info"`
	}
	var r response
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println(string(body))
		return "", err
	}
	log.Println(string(body))
	return string(body), nil

}

func (collector *Collector) CreateAppCode(code string) (int, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/apps"
	log.Println("INFO ", urlpost)
	data := url.Values{}
	data.Add("app", code)
	b := bytes.NewBuffer([]byte(data.Encode()))
	req, err := http.NewRequest("POST", urlpost, b)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err != nil {
		return 0, err
	}
	req.SetBasicAuth(collector.User, collector.Pass)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	type App struct {
		Id int `json:"id"`
	}
	type Message struct {
		Data []App `json:"data"`
	}
	var m Message

	err = json.Unmarshal(body, &m)
	if err != nil {
		return 0, err
	}
	log.Println(string(body))
	return m.Data[0].Id, nil

}

func (collector *Collector) SetPrimaryGroup(groupid int, userid int) (string, error) {

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/users/" + strconv.Itoa(userid) + "/primary_group/" + strconv.Itoa(groupid)
	log.Println("INFO ", urlpost)
	data := url.Values{}
	data.Add("primary_group", "T")
	b := bytes.NewBuffer([]byte(data.Encode()))
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	req.SetBasicAuth(collector.User, collector.Pass)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	type response struct {
		Info string `json:"info"`
	}
	var r response
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println(string(body))
		return "", err
	}
	log.Println(string(body))
	return string(body), nil

}

func (collector *Collector) SetGroupUser(groupid int, userid int) (string, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/users/" + strconv.Itoa(userid) + "/groups/" + strconv.Itoa(groupid)
	log.Println("INFO ", urlpost)
	data := url.Values{}
	data.Add("primary_group", "F")
	b := bytes.NewBuffer([]byte(data.Encode()))
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	req.SetBasicAuth(collector.User, collector.Pass)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	type response struct {
		Info string `json:"info"`
	}
	var r response
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println(string(body))
		return "", err
	}
	log.Println(string(body))
	return string(body), nil

}

func (collector *Collector) ImportCompliance(path string) (string, error) {
	file, err := ioutil.ReadFile(path)
	if err != nil {
		fmt.Printf("File error: %v\n", err)
		return "", err
	}
	fmt.Printf("%s\n", string(file))
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/compliance/import"
	log.Println("INFO ", url)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(file))
	req.Header.Set("X-Custom-Header", "myvalue")
	req.Header.Set("Content-Type", "application/json")
	if err != nil {
		log.Println("ERROR ", err)
		return "", err
	}
	req.SetBasicAuth(collector.RplMgrUser, collector.RplMgrPassword)
	resp, err := client.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return "", err
	}
	return string(body), nil
}

// Dead code
func (collector *Collector) ImportForms(path string) (string, error) {
	file, err := ioutil.ReadFile(path)
	if err != nil {
		fmt.Printf("File error: %v\n", err)
		return "", err
	}
	fmt.Printf("%s\n", string(file))
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/forms"
	log.Println("INFO ", urlpost)
	data := url.Values{}
	data.Add("role", "replication-manager")
	data.Add("privilege", "F")
	b := bytes.NewBuffer([]byte(data.Encode()))
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(collector.RplMgrUser, collector.RplMgrPassword)
	resp, err := client.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return "", err
	}
	return string(body), nil
}

func (collector *Collector) GetNodes() []Host {

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/nodes?props=id,node_id,nodename,status,cpu_cores,cpu_freq,mem_bytes,os_kernel,os_name,tz"
	if collector.Verbose == 1 {
		log.Println("INFO ", url)
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Println("ERROR ", err)

	}
	req.SetBasicAuth(collector.User, collector.Pass)

	resp, err := client.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return nil
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return nil
	}

	type Message struct {
		Data []Host `json:"data"`
	}
	var r Message
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return nil
	}
	for i, agent := range r.Data {
		r.Data[i].Ips, _ = collector.getNetwork(agent.Node_id)
	}
	return r.Data

}

func (collector *Collector) GetGroups() ([]Group, error) {

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/groups?props=role,id&filters[]=privilege T&filters[]=role !manager&limit=0"
	log.Println("INFO ", url)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Println("ERROR ", err)

	}
	req.SetBasicAuth(collector.User, collector.Pass)

	resp, err := client.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return nil, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return nil, err
	}

	type Message struct {
		Groups []Group `json:"data"`
	}
	var r Message
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return nil, err
	}
	return r.Groups, nil
}

func (collector *Collector) getNetwork(nodeid string) ([]Addr, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/nodes/" + nodeid + "/ips?props=addr,addr_type,mask,net_broadcast,net_gateway,net_name,net_netmask,net_network,net_id,intf"
	if collector.Verbose == 1 {
		log.Println("INFO ", url)
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Println("ERROR ", err)

	}
	req.SetBasicAuth(collector.User, collector.Pass)

	resp, err := client.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return nil, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return nil, err
	}
	//	log.Println(string(body))
	type Message struct {
		Data []Addr `json:"data"`
	}
	var r Message
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return nil, err
	}
	return r.Data, nil
}

//cycle W -> R -> T
func (collector *Collector) GetActionStatus(actionid string) string {

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/actions/" + actionid + "?props=id,status"
	//	log.Println("INFO ", url)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Println("ERROR ", err)

	}
	req.SetBasicAuth(collector.RplMgrUser, collector.RplMgrPassword)

	resp, err := client.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return "W"
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return "W"
	}

	type Message struct {
		Data []Action `json:"data"`
	}
	var r Message
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return "W"
	}
	return r.Data[0].Status
}

func (collector *Collector) GetAction(actionid string) *Action {

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/actions/" + actionid
	//	log.Println("INFO ", url)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Println("ERROR ", err)

	}
	req.SetBasicAuth(collector.RplMgrUser, collector.RplMgrPassword)

	resp, err := client.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return nil
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return nil
	}

	type Message struct {
		Data []Action `json:"data"`
	}
	var r Message
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return nil
	}
	return &r.Data[0]
}

func (collector *Collector) GetServices() ([]Service, error) {

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/services?limit=0"
	//	log.Println("INFO ", url)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Println("ERROR ", err)

	}
	req.SetBasicAuth(collector.RplMgrUser, collector.RplMgrPassword)

	resp, err := client.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return nil, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return nil, err
	}

	type Message struct {
		Services []Service `json:"data"`
	}
	var r Message
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return nil, err
	}
	return r.Services, nil
}

// GetServiceStatus 0 not provision, 1 prov and up , 2 prov & not up
func (collector *Collector) GetServiceStatus(name string) (int, error) {
	services, err := collector.GetServices()
	if err != nil {
		return 0, err
	}
	for _, srv := range services {
		if srv.Svc_name == name {
			if srv.Svc_status == "up" {
				return 1, nil
			}
			return 2, nil
		}
	}
	return 0, nil
}

func (collector *Collector) GetServiceFromName(name string) (Service, error) {
	services, err := collector.GetServices()
	var emptysrv Service
	if err != nil {
		return emptysrv, err
	}
	for _, srv := range services {
		if srv.Svc_name == name {
			return srv, nil
		}
	}
	return emptysrv, errors.New("Can't found service")
}

func (collector *Collector) StopService(nodeid string, serviceid string) (string, error) {

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/actions"
	log.Println("INFO ", urlpost)

	var jsonStr = []byte(`[{"node_id":"` + nodeid + `", "svc_id":"` + serviceid + `", "action": "stop"}]`)
	req, err := http.NewRequest("PUT", urlpost, bytes.NewBuffer(jsonStr))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(collector.RplMgrUser, collector.RplMgrPassword)
	resp, err := client.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return "", err
	}
	return string(body), nil

}

func (collector *Collector) StartService(nodeid string, serviceid string) (string, error) {

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/actions"
	log.Println("INFO ", urlpost)

	var jsonStr = []byte(`[{"node_id":"` + nodeid + `", "svc_id":"` + serviceid + `", "action": "start"}]`)
	req, err := http.NewRequest("PUT", urlpost, bytes.NewBuffer(jsonStr))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(collector.RplMgrUser, collector.RplMgrPassword)
	resp, err := client.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return "", err
	}
	log.Println("INFO ", string(body))
	return string(body), nil

}
