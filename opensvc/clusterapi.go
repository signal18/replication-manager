// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//
//	Stephane Varoqui  <svaroqui@gmail.com>
//
// This source code is licensed under the GNU General Public License, version 3.
package opensvc

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"hash/crc64"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/signal18/replication-manager/config"
	log "github.com/sirupsen/logrus"

	//	pkcs12 "software.sslmate.com/src/go-pkcs12"

	"golang.org/x/net/http2"
)

func (collector *Collector) GetHttpClient() *http.Client {
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	client := &http.Client{}
	if !collector.UseAPI {
		cert, err := collector.FromP12Bytes(collector.CertsDER, collector.CertsDERSecret)
		if err != nil {
			if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
				log.WithField("FROM", "OpenSVC").Errorln("ERROR ParseCertificatesDER ", err)
			}
		}

		tlsConfig = &tls.Config{
			Certificates:       []tls.Certificate{cert},
			InsecureSkipVerify: true,
		}
		client.Transport = &http2.Transport{
			TLSClientConfig: tlsConfig,
		}
	} else {

		client.Transport = &http2.Transport{
			TLSClientConfig: tlsConfig,
		}
	}
	return client

}

func (collector *Collector) StartServiceV2(cluster string, srv string, node string) error {

	client := collector.GetHttpClient()
	jsondata := `{"path": "` + srv + `", "action": "start", "options": {}}`
	b := bytes.NewBuffer([]byte(jsondata))
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/service_action"
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", node)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	log.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	return nil
}

func (collector *Collector) StopServiceV2(cluster string, srv string, node string) error {

	client := collector.GetHttpClient()
	jsondata := `{"path": "` + srv + `", "action": "stop", "options": {}}`
	b := bytes.NewBuffer([]byte(jsondata))
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/service_action"
	log.WithField("FROM", "OpenSVC").Println("API Request: ", urlpost, " Payload: ", jsondata)
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", node)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	log.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	return nil
}

func (collector *Collector) PurgeServiceV2(cluster string, srv string, node string) error {

	client := collector.GetHttpClient()
	jsondata := `{"path": "` + srv + `", "global_expect": "purged", "options": {}}`
	//jsondata := `{"path": "` + srv + `", "action": "purge", "options": {}}`
	b := bytes.NewBuffer([]byte(jsondata))
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/object_monitor"
	log.WithField("FROM", "OpenSVC").Println("API Request: ", urlpost, " Payload: ", jsondata)
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", node)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	log.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	return nil
}

func (collector *Collector) CreateConfigKeyValueV2(namespace string, service string, key string, value string) error {

	urlpost := "https://" + collector.Host + ":" + collector.Port + "/key"
	jsondata := `{"path": "` + namespace + `/cfg/` + service + `", "key":"` + key + ` ", "data": "` + value + `"}`
	if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlInfo) {
		log.WithField("FROM", "OpenSVC").Println("API Request: ", urlpost, " Payload: ", jsondata)
	}

	client := collector.GetHttpClient()
	b := bytes.NewBuffer([]byte(jsondata))
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
			log.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", "ANY")
	resp, err := client.Do(req)
	if err != nil {
		if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
			log.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	log.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	return nil
}

func (collector *Collector) CreateSecretKeyValueV2(namespace string, service string, key string, value string) error {

	urlpost := "https://" + collector.Host + ":" + collector.Port + "/key"
	jsondata := `{"path": "` + namespace + `/sec/` + service + `", "key":"` + key + ` ", "data": "` + value + `"}`
	if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlInfo) {
		log.WithField("FROM", "OpenSVC").Println("API Request: ", urlpost, " Payload: ", jsondata)
	}

	client := collector.GetHttpClient()
	b := bytes.NewBuffer([]byte(jsondata))
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
			log.WithField("FROM", "OpenSVC").Errorln("Api Error: ", err)
		}
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", "ANY")
	resp, err := client.Do(req)
	if err != nil {
		if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
			log.WithField("FROM", "OpenSVC").Errorln("Api Error: ", err)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	log.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	return nil
}

func (collector *Collector) CreateSecretV2(namespace string, service string, agent string) error {

	urlpost := "https://" + collector.Host + ":" + collector.Port + "/create"

	// just create or replace
	jsondata := `{"data": {"` + namespace + `/sec/` + service + `": {}}}`
	if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlInfo) {
		log.WithField("FROM", "OpenSVC").Println("API Request: ", urlpost, " Payload: ", jsondata)
	}

	client := collector.GetHttpClient()
	b := bytes.NewBuffer([]byte(jsondata))
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
			log.WithField("FROM", "OpenSVC").Errorln("Api Error: ", err)
		}
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	myagent := "ANY"
	if agent != "" {
		myagent = agent
	}
	req.Header.Set("o-node", myagent)
	resp, err := client.Do(req)
	if err != nil {
		if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
			log.WithField("FROM", "OpenSVC").Errorln("Api Error: ", err)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	log.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	return nil
}

func (collector *Collector) CreateConfigV2(namespace string, service string, agent string) error {

	urlpost := "https://" + collector.Host + ":" + collector.Port + "/create"
	jsondata := `{"data": {"` + namespace + `/cfg/` + service + `": {}}}`
	if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlInfo) {
		log.WithField("FROM", "OpenSVC").Println("API Request: ", urlpost, " Payload: ", jsondata)
	}

	client := collector.GetHttpClient()
	b := bytes.NewBuffer([]byte(jsondata))
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
			log.WithField("FROM", "OpenSVC").Errorln("Api Error: ", err)
		}
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	myagent := "ANY"
	if agent != "" {
		myagent = agent
	}
	req.Header.Set("o-node", myagent)
	resp, err := client.Do(req)
	if err != nil {
		if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
			log.WithField("FROM", "OpenSVC").Errorln("Api Error: ", err)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	log.WithField("FROM", "OpenSVC").Println("Api Response: ", string(body))
	return nil
}

// CreateTemplateV2 post a template to the collector

func (collector *Collector) CreateTemplateV2(cluster string, srv string, node string, template string) error {

	urlpost := "https://" + collector.Host + ":" + collector.Port + "/create"
	jsondata := `{"namespace": "` + cluster + `", "provision": true, "sync": true, "data": {"` + srv + `": ` + template + `}}`
	//jsondata := `{"namespace": "` + cluster + `", "sync": true, "data": {"` + srv + `": ` + template + `}}`
	if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlInfo) {
		log.WithField("FROM", "OpenSVC").Println("API Request: ", urlpost, " Payload: ", jsondata)
	}

	client := collector.GetHttpClient()
	b := bytes.NewBuffer([]byte(jsondata))
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
			log.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", node)
	resp, err := client.Do(req)
	if err != nil {
		if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
			log.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	log.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))

	//	collector.WaitServiceAvailable(srv, node)
	//	collector.WaitServicePropagate(srv, node)

	//	collector.CreateTemplateV2Monitor(srv, node)

	return nil
}

func (collector *Collector) CreateTemplateV2Monitor(srv string, node string) error {

	urlpost := "https://" + collector.Host + ":" + collector.Port + "/object_monitor"
	jsondata := `{"path": "` + srv + `", "global_expect": "provisioned", "options": {}}`

	log.WithField("FROM", "OpenSVC").Println("OpenSVC API Request: ", urlpost, " Payload: ", jsondata)
	client := collector.GetHttpClient()
	b := bytes.NewBuffer([]byte(jsondata))
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
			log.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", node)
	resp, err := client.Do(req)
	if err != nil {
		if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
			log.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	log.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	return nil
}

func (collector *Collector) WaitServiceAvailable(srv string, node string) error {

	//jsondata := "{\".monitor.services.'" + srv + "'.avail=up\",   \"duration\": \"30s\"}"
	urlget := "https://" + collector.Host + ":" + collector.Port + "/wait?condition=.monitor.services.'" + srv + "'.avail&duration=30s"

	client := collector.GetHttpClient()
	//b := bytes.NewBuffer([]byte(jsondata))
	//	req, err := http.NewRequest("GET", urlget, b)
	req, err := http.NewRequest("GET", urlget, nil)
	if err != nil {
		if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
			log.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", node)

	resp, err := client.Do(req)
	if err != nil {
		if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
			log.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	log.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	return nil

}

func (collector *Collector) WaitServicePropagate(srv string, node string) error {

	//jsondata := "{\".monitor.services.'" + srv + "'.avail=up\",   \"duration\": \"30s\"}"
	urlget := "https://" + collector.Host + ":" + collector.Port + "/wait?condition=.monitor.nodes." + node + ".services.config.'" + srv + "'.csum&duration=30s"

	client := collector.GetHttpClient()
	//b := bytes.NewBuffer([]byte(jsondata))
	//	req, err := http.NewRequest("GET", urlget, b)
	req, err := http.NewRequest("GET", urlget, nil)
	if err != nil {
		if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
			log.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", node)

	resp, err := client.Do(req)
	if err != nil {
		if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
			log.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	log.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	return nil

}

func (collector *Collector) GetNodes() ([]Host, error) {

	url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/nodes?props=id,node_id,nodename,status,cpu_cores,cpu_freq,mem_bytes,os_kernel,os_name,tz"
	if !collector.UseAPI {
		url = "https://" + collector.Host + ":" + collector.Port + "/get_node"
	}
	client := collector.GetHttpClient()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if collector.UseAPI {
		req.SetBasicAuth(collector.RplMgrUser, collector.RplMgrPassword)
		//		log.WithField("FROM", "OpenSVC").Printf("Info opensvc login %s %s", collector.RplMgrUser, collector.RplMgrPassword)
	} else {
		req.Header.Set("content-type", "application/json")
		req.Header.Set("o-node", "*")
	}
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)

	defer cancel()
	req = req.WithContext(ctx)
	// Following can be use to cancel context timeout to trace API response time
	/*	trace := &httptrace.ClientTrace{
			DNSDone: func(dnsInfo httptrace.DNSDoneInfo) {
				fmt.Printf("%v DNS Info: %+v\n", time.Now(), dnsInfo)
			},
			GotConn: func(connInfo httptrace.GotConnInfo) {
				fmt.Printf("%v Got Conn: %+v\n", time.Now(), connInfo)
			},
		}
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	*/

	startConnect := time.Now()
	resp, err := client.Do(req)

	stopConnect := time.Now()
	if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlInfo) {
		log.WithField("FROM", "OpenSVC").Printf("OpenSVC Connect took: %s\n", stopConnect.Sub(startConnect))
	}
	if err != nil {
		if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
			log.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return nil, err
	}

	defer client.CloseIdleConnections()
	defer resp.Body.Close()
	startRead := time.Now()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	endRead := time.Now()
	if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlInfo) {
		log.WithField("FROM", "OpenSVC").Printf("OpenSVC Read response took: %s\n", endRead.Sub(startRead))
		log.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	}
	if collector.UseAPI {
		type Message struct {
			Data []Host `json:"data"`
		}
		var r Message

		err = json.Unmarshal(body, &r)
		if err != nil {
			if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
				log.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
			}
			return nil, err
		}
		for i, agent := range r.Data {
			r.Data[i].Ips, _ = collector.getNetwork(agent.Node_id)
			r.Data[i].Svc, _ = collector.getNodeServices(agent.Node_id)
		}
		return r.Data, nil
	}

	//Procedd with cluster VIP
	type Property struct {
		Title  string `json:"title"`
		Value  string `json:"value"`
		Source string `json:"source"`
	}
	type SHost struct {
		Nodename   Property `json:"nodename"`
		Fqdn       Property `json:"fqdn"`
		Version    Property `json:"version"`
		Osname     Property `json:"os_name"`
		Osvendor   Property `json:"os_vendor"`
		Osrelease  Property `json:"os_release"`
		Oskernel   Property `json:"os_kernel"`
		Osarch     Property `json:"os_arch"`
		Membytes   Property `json:"mem_bytes"`
		Cpufreq    Property `json:"cpu_freq"`
		Cputhreads Property `json:"cpu_threads"`
	}

	type Message struct {
		Data map[string]SHost `json:"nodes"`
	}
	var r Message

	err = json.Unmarshal(body, &r)
	if err != nil {
		if collector.ClusterConf.IsEligibleForPrinting(config.ConstLogModOrchestrator, config.LvlErr) {
			log.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return nil, err
	}
	crcTable := crc64.MakeTable(crc64.ECMA)

	nhosts := make([]Host, len(r.Data), len(r.Data))
	i := 0
	for _, agent := range r.Data {
		//		log.WithField("FROM", "OpenSVC").Println("ERROR ", agent)
		nhosts[i].Node_id = strconv.FormatUint(crc64.Checksum([]byte(agent.Nodename.Value), crcTable), 10)
		nhosts[i].Cpu_cores, _ = strconv.ParseInt(agent.Cputhreads.Value, 10, 64)
		nhosts[i].Cpu_freq, _ = strconv.ParseInt(agent.Cpufreq.Value, 10, 64)
		nhosts[i].Mem_bytes, _ = strconv.ParseInt(agent.Membytes.Value, 10, 64)
		nhosts[i].Node_name = agent.Nodename.Value
		nhosts[i].Os_kernel = agent.Oskernel.Value
		nhosts[i].Os_name = agent.Osname.Value
		//		r.Data[i].Ips, _ = collector.getNetwork(agent.Node_id)
		//		r.Data[i].Svc, _ = collector.getNodeServices(agent.Node_id)
		i++
	}
	return nhosts, nil

}
