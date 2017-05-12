// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
package opensvc

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strconv"
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

type HostList []*Host

type Collector struct {
	Host string
	Port string
	User string
	Pass string
}

func (collector *Collector) CreateDBAGroup() (string, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/groups"
	log.Println("INFO ", urlpost)
	data := url.Values{}
	data.Add("role", "DBA")
	data.Add("privilege", "F")
	b := bytes.NewBuffer([]byte(data.Encode()))
	req, err := http.NewRequest("POST", urlpost, b)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(collector.User, collector.Pass)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
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
		return "", err
	}

	log.Println(string(body))
	collector.setGroupUser(m.Data[0].Id, 1)
	return string(body), nil

}

func (collector *Collector) setGroupUser(groupid int, userid int) (string, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/users/" + strconv.Itoa(userid) + "/groups/" + strconv.Itoa(groupid)
	log.Println("INFO ", urlpost)
	data := url.Values{}
	b := bytes.NewBuffer([]byte(data.Encode()))
	req, err := http.NewRequest("POST", urlpost, b)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(collector.User, collector.Pass)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	type response struct {
		Info bool `json:"info"`
	}
	var r response
	err = json.Unmarshal(body, &r)
	if err != nil {
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
	req.SetBasicAuth(collector.User, collector.Pass)
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
	log.Println("INFO ", url)

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

func (collector *Collector) getNetwork(nodeid string) ([]Addr, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: tr}
	url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/nodes/" + nodeid + "/ips?props=addr,addr_type,mask,net_broadcast,net_gateway,net_name,net_netmask,net_network,net_id"
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
