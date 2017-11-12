// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
package opensvc

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"

	log "github.com/sirupsen/logrus"
)

type Addr struct {
	Addr          string `json:"addr"`
	Addr_type     string `json:"addr_type"`
	Mask          string `json:"mask"`
	Net_broadcast string `json:"net_broadcast"`
	Net_gateway   string `json:"net_gateway"`
	Net_name      string `json:"net_name"`
	Net_network   string `json:"net_network"`
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

type Ruleset struct {
	Id int `json:"id"`
}

type RulesetVariable struct {
	Id int `json:"id"`
}

type Host struct {
	Id        int    `json:"id"`
	Node_id   string `json:"node_id"`
	Node_name string `json:"nodename"`
	Cpu_cores int    `json:"cpu_cores"`
	Cpu_freq  int    `json:"cpu_freq"`
	Mem_bytes int    `json:"mem_bytes"`
	Os_kernel string `json:"os_kernel"`
	Os_name   string `json:"os_name"`
	Ips       []Addr
	Svc       []Service
}
type Service struct {
	Id         int    `json:"id"`
	Svc_name   string `json:"svcname"`
	Svc_status string `json:"svc_status"`
	Updated    string `json:"updated"`
	Svc_id     string `json:"svc_id"`
}

type Tag struct {
	Tag_name string `json:"tag_name"`
	Tag_id   string `json:"tag_id"`
}

type HostList []*Host

type Collector struct {
	Host                        string
	Port                        string
	User                        string
	Pass                        string
	RplMgrUser                  string
	RplMgrPassword              string
	RplMgrCodeApp               string
	ProvAgents                  string
	ProvMem                     string
	ProvIops                    string
	ProvTags                    string
	ProvDisk                    string
	ProvPwd                     string
	ProvNetMask                 string
	ProvNetGateway              string
	ProvNetIface                string
	ProvMicroSrv                string
	ProvFSType                  string
	ProvFSPool                  string
	ProvFSMode                  string
	ProvFSPath                  string
	ProvDockerImg               string
	ProvProxAgents              string
	ProvProxDisk                string
	ProvProxNetMask             string
	ProvProxNetGateway          string
	ProvProxNetIface            string
	ProvProxMicroSrv            string
	ProvProxFSType              string
	ProvProxFSPool              string
	ProvProxFSMode              string
	ProvProxFSPath              string
	ProvProxDockerMaxscaleImg   string
	ProvProxDockerHaproxyImg    string
	ProvProxDockerProxysqlImg   string
	ProvProxDockerShardproxyImg string
	ProvProxTags                string
	ProvCores                   string
	Verbose                     int
}

//Imput template URI [system|docker].[zfs|xfs|ext4|btrfs].[none|zpool|lvm].[loopback|physical].[path-to-file|/dev/xx]

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

	appid, err := collector.CreateAppCode(collector.RplMgrCodeApp)
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
	req.SetBasicAuth(collector.RplMgrUser, collector.RplMgrPassword)
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
	req.SetBasicAuth(collector.RplMgrUser, collector.RplMgrPassword)
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

func (collector *Collector) SetServiceTag(tag_id string, service_id string) (string, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/tags/" + tag_id + "/services/" + service_id
	log.Println("INFO ", urlpost)
	data := url.Values{}
	b := bytes.NewBuffer([]byte(data.Encode()))
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	req.SetBasicAuth(collector.RplMgrUser, collector.RplMgrPassword)
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

func (collector *Collector) CreateTag(tag string) (string, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/tags"
	log.Println("INFO ", urlpost)
	data := url.Values{}
	data.Add("tag_name", tag)
	b := bytes.NewBuffer([]byte(data.Encode()))
	req, err := http.NewRequest("POST", urlpost, b)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(collector.RplMgrUser, collector.RplMgrPassword)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	type Message struct {
		Data []Tag `json:"data"`
	}
	var m Message

	err = json.Unmarshal(body, &m)
	if err != nil {
		return "", err
	}
	log.Println(string(body))
	return m.Data[0].Tag_id, nil
}

func (collector *Collector) CreateService(service string, app string) (string, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/services"
	log.Println("INFO ", urlpost)
	data := url.Values{}
	data.Add("svcname", service)
	data.Add("svc_app", app)
	b := bytes.NewBuffer([]byte(data.Encode()))
	req, err := http.NewRequest("POST", urlpost, b)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(collector.RplMgrUser, collector.RplMgrPassword)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	type Message struct {
		Data []Service `json:"data"`
	}
	var m Message

	err = json.Unmarshal(body, &m)
	if err != nil {
		return "", err
	}
	log.Println(string(body))
	if len(m.Data) == 0 {
		return "", errors.New("OpenSVC can't create service")
	}
	return m.Data[0].Svc_id, nil
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
		return nil
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
		Data []Host `json:"data"`
	}
	var r Message
	err = json.Unmarshal(body, &r)
	if err != nil {
		//	log.Println("ERROR ", err)
		return nil
	}
	for i, agent := range r.Data {
		r.Data[i].Ips, _ = collector.getNetwork(agent.Node_id)
		r.Data[i].Svc, _ = collector.getNodeServices(agent.Node_id)
	}
	return r.Data

}

func (collector *Collector) GetRuleset(RulesetName string) ([]Ruleset, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/compliance/rulesets?filters[]=ruleset_name " + RulesetName
	log.Println("INFO ", url)
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
		Rulesets []Ruleset `json:"data"`
	}
	var r Message
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return nil, err
	}
	return r.Rulesets, nil
}

func (collector *Collector) GetRulesetVariable(RulesetId int, VariableName string) ([]RulesetVariable, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/compliance/rulesets/" + strconv.Itoa(RulesetId) + "/variables?filters[]=var_name " + VariableName
	log.Println("INFO ", url)
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
		RulesetVariables []RulesetVariable `json:"data"`
	}
	var r Message
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return nil, err
	}
	return r.RulesetVariables, nil
}

func (collector *Collector) SetRulesetVariableValue(RulesetName string, VariableName string, Content string) (string, error) {

	rls, err := collector.GetRuleset(RulesetName)
	if err != nil {
		log.Println(string(err.Error()))
		return "", err
	}
	rlsv, err := collector.GetRulesetVariable(rls[0].Id, VariableName)
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}

	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/compliance/rulesets/" + strconv.Itoa(rls[0].Id) + "/variables/" + strconv.Itoa(rlsv[0].Id)
	log.Println("INFO SetRulesetVariableValue: ", urlpost)
	var jsonStr = []byte(`{"var_value":"{"path":"/%%ENV:SVC_CONF_ENV_BASE_DIR%%/%%ENV:POD%%/conf/haproxy.cfg","mode":"%%ENV:BINDED_DIR_PERMS%%","uid":"%%ENV:MYSQL_UID%%","gid":"%%ENV:MYSQL_UID%%","fmt":"` + Content + `"}"}`)
	req, err := http.NewRequest("POST", urlpost, bytes.NewBuffer(jsonStr))
	if err != nil {
		log.Println(string(err.Error()))
		return "", err
	}
	req.Header.Set("X-Custom-Header", "myvalue")
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	//	"{"path":"/%%ENV:SVC_CONF_ENV_BASE_DIR%%/%%ENV:POD%%/conf/haproxy.cfg","mode":"%%ENV:BINDED_DIR_PERMS%%","uid":"%%ENV:MYSQL_UID%%","gid":"%%ENV:MYSQL_UID%%","fmt":"global\n pidfile /var/lib/replication-manager/ux_dck_zpool_loop-haproxy-private.pid\n\n daemon\n maxconn 4096\n stats socket /var/lib/replication-manager/ux_dck_zpool_loop-haproxy.stats.sock level admin\n\n\n ###\n #\n # Experimental: Logging Setup\n #\n # We log to a unix socket and read that socket from the Go program\n #\n #\n log /var/run/vamp.log.sock local0\n\n defaults\n   log global\n   mode http\n   option dontlognull\n   option redispatch\n   option clitcpka\n   option srvtcpka\n\n   retries 3\n   maxconn 500000\n\n   # slowloris protection: clients should send their full http request in the configured time\n   timeout http-request 5s\n\n   timeout connect 5000ms\n   timeout client 50000s\n   timeout server 50000s\n\nlisten stats\n   bind :1988\n   mode http\n   stats enable\n   stats uri /\n   stats refresh 2s\n   stats realm Haproxy\\ Stats\n   \n### BEGIN GENERATED SECTION ###\n\nfrontend my_write_frontend\n    \n    bind 0.0.0.0:3303\n    \n\n    \n     option tcplog \n\n\n    ###\n    #\n    # Set logging and set the headers to capture\n\n    # capture request header X-Vamp-Server-CurrentTime len 50\n    # capture response header X-Vamp-Server-ResponseTime len 50\n    # capture response header X-Vamp-Server-Name len 50\n\n\n    #log-format {\\ \"timestamp\"\\ :\\ %t,\\ \"frontend\"\\ :\\ \"%f\",\\ \"method\"\\ :\\ \"%r\",\\ \"captured_request_headers\"\\ :\\ \"%hrl\",\\ \"captures_response_headers\"\\ :\\ \"%hsl\"\\ }\n\n    #\n    ###\n\n    \n\n    mode tcp\n    \n\n    ###\n    #\n    # Spike/Rate Limiting & Quota Management\n    #\n    # We use a stick table to keep track of TCP connections rates and bytes send out.\n    # On these metrics we set rules to designate upper limits. When limits are hit\n    # we reroute the traffic to a specific abusers backend\n\n     # end HTTP spike limit generation\n\n     # end spike limit generation\n\n    ###\n    # Filter Management\n    #\n    # set filters with optional negation\n    #\n\n    \n\n    default_backend service_write\n\n\nfrontend my_read_frontend\n    \n    bind 0.0.0.0:3302\n    \n\n    \n     option tcplog \n\n\n    ###\n    #\n    # Set logging and set the headers to capture\n\n    # capture request header X-Vamp-Server-CurrentTime len 50\n    # capture response header X-Vamp-Server-ResponseTime len 50\n    # capture response header X-Vamp-Server-Name len 50\n\n\n    #log-format {\\ \"timestamp\"\\ :\\ %t,\\ \"frontend\"\\ :\\ \"%f\",\\ \"method\"\\ :\\ \"%r\",\\ \"captured_request_headers\"\\ :\\ \"%hrl\",\\ \"captures_response_headers\"\\ :\\ \"%hsl\"\\ }\n\n    #\n    ###\n\n    \n\n    mode tcp\n    \n\n    ###\n    #\n    # Spike/Rate Limiting & Quota Management\n    #\n    # We use a stick table to keep track of TCP connections rates and bytes send out.\n    # On these metrics we set rules to designate upper limits. When limits are hit\n    # we reroute the traffic to a specific abusers backend\n\n     # end HTTP spike limit generation\n\n     # end spike limit generation\n\n    ###\n    # Filter Management\n    #\n    # set filters with optional negation\n    #\n\n    \n\n    default_backend service_read\n\n\n\n\n\n\nbackend service_write\n    mode tcp\n#\n# Regular HTTP/TCP backends\n#\n\n   \n    balance leastconn \n\n\n\n   \n\n   \n    \n        server leader 192.168.100.71:3306  weight 100 maxconn 2000 check inter 1000 \n    \n    \n    \n    \n    \n    \n    \n    \n    \n    \n    \n\n\n\n\n\nbackend service_read\n    mode tcp\n#\n# Regular HTTP/TCP backends\n#\n\n   \n    balance leastconn \n\n\n\n   \n\n   \n    \n        server 8194047115532167437 192.168.100.70:3306  weight 100 maxconn 2000 check inter 1000 \n        server 1167504531203395275 192.168.100.71:3306  weight 100 maxconn 2000 check inter 1000 \n    \n    \n    \n    \n    \n    \n    \n    \n    \n    \n    \n\n\n\n\n\n\n### END GENERATED SECTION ###\n"}"
	/*
		data := url.Values{}
		data.Add("var_class", "file")
		data.Add("var_value", "{\"path\":\"/%%ENV:SVC_CONF_ENV_BASE_DIR%%/%%ENV:POD%%/conf/haproxy.cfg\",\"mode\":\"%%ENV:BINDED_DIR_PERMS%%\",\"uid\":\"%%ENV:MYSQL_UID%%\",\"gid\":\"%%ENV:MYSQL_GID%%\",\"fmt\":\""+Value+"\"}")

		b := bytes.NewBuffer([]byte(data.Encode()))
		req, err := http.NewRequest("POST", urlpost, b)
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	*/

	req.SetBasicAuth(collector.User, collector.Pass)
	resp, err := client.Do(req)
	if err != nil {
		log.Println(string(err.Error()))
		return "", err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	log.Println(string(body))
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

func (collector *Collector) GetTagIdFromTags(tags []Tag, name string) (string, error) {
	for _, tag := range tags {
		if tag.Tag_name == name {
			return tag.Tag_id, nil
		}
	}
	return "", errors.New("No tag found")
}

func (collector *Collector) GetServiceTags(idSrv string) ([]Tag, error) {

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/services/" + idSrv + "/tags?limit=0"
	log.Println("INFO ", url)

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
	count, err := collector.getMetaCount(body)
	if err != nil {
		log.Println("ERROR get Meta Data count", err)
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	type Message struct {
		Tags []Tag `json:"data"`
	}
	var r Message
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return nil, err
	}
	return r.Tags, nil
}

func (collector *Collector) getMetaCount(body []byte) (int, error) {
	type ResMeta struct {
		Count int `json:"count"`
	}
	type Metadata struct {
		Meta ResMeta `json:"meta"`
	}
	var m Metadata
	err := json.Unmarshal(body, &m)
	if err != nil {
		log.Print(string(body))
		return 0, err
	}
	return m.Meta.Count, nil
}

func (collector *Collector) deteteServiceTag(idSrv string, tag Tag) error {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/tags/" + tag.Tag_id + "/services/" + idSrv
	//url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/services/" + idSrv + "/tags/" + tag.Tag_id
	log.Println("INFO ", url)

	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(collector.RplMgrUser, collector.RplMgrPassword)

	resp, err := client.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return err
	}
	defer resp.Body.Close()
	_, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return err
	}
	return nil
}

func (collector *Collector) DeteteServiceTags(idSrv string) error {
	tags, err := collector.GetServiceTags(idSrv)
	if err != nil {
		return err
	}
	if tags == nil {
		return nil
	}
	for _, tag := range tags {
		err := collector.deteteServiceTag(idSrv, tag)
		if err != nil {
			return err
		}
	}
	return nil
}

func (collector *Collector) GetTags() ([]Tag, error) {

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/tags?limit=0"
	log.Println("INFO ", url)

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
		Tags []Tag `json:"data"`
	}
	var r Message
	err = json.Unmarshal(body, &r)
	if err != nil {
		log.Println("ERROR ", err)
		return nil, err
	}
	return r.Tags, nil
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
	if collector.Verbose == 1 {
		log.Println("INFO ", url)
	}

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
	if r.Data == nil {
		return "W"
	}
	if len(r.Data) == 0 {
		return "W"
	}
	return r.Data[0].Status
}

func (collector *Collector) GetAction(actionid string) *Action {

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/actions/" + actionid
	//	log.Println("INFO ", url)
	if collector.Verbose == 1 {
		log.Println("INFO ", url)
	}
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
		log.Println("JSON ERROR unmarchaling action", err)
		return nil
	}
	if len(r.Data) == 0 {
		return nil
	}
	return &r.Data[0]
}

func (collector *Collector) GetServices() ([]Service, error) {

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/services?limit=0"
	if collector.Verbose == 1 {
		log.Println("INFO ", url)
	}
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

func (collector *Collector) getNodeServices(nodeid string) ([]Service, error) {

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/nodes/" + nodeid + "/services?limit=0&props=services.svcname,services.svc_id"
	if collector.Verbose == 1 {
		log.Println("INFO ", url)
	}
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

func (collector *Collector) UnprovisionService(nodeid string, serviceid string) (int, error) {

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/actions"
	log.Println("INFO ", urlpost)

	var jsonStr = []byte(`[{"node_id":"` + nodeid + `", "svc_id":"` + serviceid + `", "action": "unprovision"}]`)
	req, err := http.NewRequest("PUT", urlpost, bytes.NewBuffer(jsonStr))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(collector.RplMgrUser, collector.RplMgrPassword)
	resp, err := client.Do(req)
	if err != nil {
		log.Println("ERROR ", err)
		return 0, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Println("ERROR ", err)
		return 0, err
	}
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
	log.Println("INFO ", string(body))
	return actionid, nil
}

func (collector *Collector) DeleteService(serviceid string) (string, error) {

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/services/" + serviceid
	log.Println("INFO Delete service: ", urlpost)

	req, err := http.NewRequest("DELETE", urlpost, nil)
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
