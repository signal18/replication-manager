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
	"errors"
	"fmt"
	"hash/crc64"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	//	pkcs12 "software.sslmate.com/src/go-pkcs12"

	"golang.org/x/net/http2"
)

// ConfigKeyValueRequest représente la structure des données à envoyer
type ConfigKeyValueRequest struct {
	Path string `json:"path"`
	Key  string `json:"key"`
	Data string `json:"data"`
}

func (collector *Collector) GetHttpClient() *http.Client {
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	client := &http.Client{}
	if !collector.UseCollectorAPI {
		cert, err := collector.FromP12Bytes(collector.CertsDER, collector.CertsDERSecret)
		if err != nil {
			collector.Print(log.ErrorLevel, "ERROR ParseCertificatesDER %v", err)
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

func (collector *Collector) GetGottyServer(srv string, rid string) (string, string, error) {
	client := collector.GetHttpClient()
	//jsondata := `{"path": "` + srv + `", "rid":"` + rid + `", "timeout": "10s"}`

	//b := bytes.NewBuffer([]byte(jsondata))
	urlget := "https://" + collector.Host + ":" + collector.Port + "/object_enter?path=" + url.QueryEscape(srv) + "&rid=" + url.QueryEscape(rid) + "&timout=5s"
	req, err := http.NewRequest("GET", urlget, nil)
	if err != nil {
		// Removed to avoid multiple log entries
		return "", "", err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", "ANY")
	resp, err := client.Do(req)
	if err != nil {
		// Removed to avoid multiple log entries
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	collector.Print(log.DebugLevel, "OpenSVC API Response: ", string(body))

	//{"nodes": {"s18-fr-4": {"data": {"url": "https://user:ce860a2b-a757-4de5-8429-b3e7c9bd8124@s18-fr-42025/03/31 19:39:27 URL: https://127.0.0.1:0/i7qd0lop/"}}}, "status": 0}
	type NodeData struct {
		URL string `json:"url"`
	}

	type Node struct {
		Data NodeData `json:"data"`
	}

	type NodesMap map[string]Node

	type Response struct {
		Nodes  NodesMap `json:"nodes"`
		Status int      `json:"status"`
	}
	var r Response

	err = json.Unmarshal(body, &r)
	if err != nil {
		return "", "", err
	}
	for nodeName, node := range r.Nodes {
		return node.Data.URL, nodeName, nil
	}
	return "", "", errors.New("Not found")
}

// ActionRequest représente la structure des données à envoyer
type ActionRequest struct {
	Path         string                 `json:"path"`
	Options      map[string]interface{} `json:"options"`
	Sync         bool                   `json:"sync,omitempty"`          // Ajout de l'option sync
	Action       string                 `json:"action,omitempty"`        // Ajout de l'action
	Env          string                 `json:"env,omitempty"`           // Ajout de l'option env
	GlobalExpect string                 `json:"global_expect,omitempty"` // Ajout de l'option global_expect
}

func (collector *Collector) StartServiceV2(cluster string, srv string, node string) error {

	reqparams := ActionRequest{
		Path:    srv,
		Action:  "start",
		Options: map[string]interface{}{},
	}

	jsondata, err := json.Marshal(reqparams)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	b := bytes.NewBuffer(jsondata)
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/service_action"
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		return err
	}

	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", node)

	client := collector.GetHttpClient()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	collector.Print(log.DebugLevel, "OpenSVC API Response: %s", string(body))

	return nil
}

func (collector *Collector) RestartServiceV2(cluster string, srv string, node string, rid string) error {

	reqparams := ActionRequest{
		Path:    srv,
		Action:  "restart",
		Options: map[string]interface{}{},
	}

	// Add rid to options if provided
	if rid != "" {
		reqparams.Options["rid"] = rid
	}

	jsondata, err := json.Marshal(reqparams)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	b := bytes.NewBuffer(jsondata)
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/service_action"

	collector.Print(log.DebugLevel, "API Request: %s Payload: %s", urlpost, jsondata)

	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", node)

	client := collector.GetHttpClient()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	collector.Print(log.DebugLevel, "OpenSVC API Response: %s", string(body))

	return nil
}

func (collector *Collector) RunTaskV2(cluster string, srv string, node string, task string, parameter string) error {
	// osvccurl -o /tmp/physical.dbdump.log -X POST -H "o-node: $NODE" --data '{"path": "namespace/svc/haproxy", "action": "run", "sync": true, "options": {"rid": "task#addcert"}, "env": "DOMAIN=www.acme.com"}' ${APIURL}/object_action

	reqparams := ActionRequest{
		Path:   srv,
		Action: "run",
		Options: map[string]interface{}{
			"rid": task,
		},
		Sync: true,      // Ajout de l'option sync
		Env:  parameter, // Ajout de l'option env
	}

	jsondata, err := json.Marshal(reqparams)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	b := bytes.NewBuffer([]byte(jsondata))
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/service_action"

	collector.Print(log.DebugLevel, "API Request: %s Payload: %s", urlpost, jsondata)

	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", node)

	client := collector.GetHttpClient()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	collector.Print(log.DebugLevel, "OpenSVC API Response: %s", body)

	return nil
}

func (collector *Collector) StopServiceV2(cluster string, srv string, node string) error {

	//jsondata := `{"path": "` + srv + `", "action": "stop", "options": {}}`
	reqparams := ActionRequest{
		Path:    srv,
		Action:  "stop",
		Options: map[string]interface{}{},
	}

	jsondata, err := json.Marshal(reqparams)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	b := bytes.NewBuffer(jsondata)
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/service_action"

	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", node)

	collector.Print(log.DebugLevel, "API Request: %s Header: %v Payload: %s", urlpost, req.Header, jsondata)

	client := collector.GetHttpClient()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	collector.Print(log.DebugLevel, "OpenSVC API Response: %s", body)

	return nil
}

func (collector *Collector) PurgeServiceV2(cluster string, srv string, node string) error {

	// jsondata := `{"path": "` + srv + `", "global_expect": "purged", "options": {}}`
	reqparams := ActionRequest{
		Path:         srv,
		GlobalExpect: "purged",
		Options:      map[string]interface{}{},
	}

	jsondata, err := json.Marshal(reqparams)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	b := bytes.NewBuffer([]byte(jsondata))
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/object_monitor"

	collector.Print(log.DebugLevel, "API Request: %s Payload: %s", urlpost, jsondata)

	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", node)

	client := collector.GetHttpClient()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	collector.Print(log.DebugLevel, "OpenSVC API Response: %s", string(body))
	return nil
}

func (collector *Collector) CreateConfigKeyValueV2(namespace string, service string, key string, value string) error {
	// Construction de l'URL de manière plus propre
	urlpost := fmt.Sprintf("https://%s:%s/key", collector.Host, collector.Port)

	// Création de la structure de données
	requestData := ConfigKeyValueRequest{
		Path: fmt.Sprintf("%s/cfg/%s", namespace, service),
		Key:  key,
		Data: value,
	}

	// Sérialisation en JSON
	jsonData, err := json.Marshal(requestData)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	collector.Print(log.DebugLevel, "API Request: %s Payload: %s", urlpost, jsonData)

	client := collector.GetHttpClient()

	// Création de la requête HTTP
	req, err := http.NewRequest("POST", urlpost, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Configuration des headers
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", "ANY")

	// Exécution de la requête
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute HTTP request: %w", err)
	}
	defer resp.Body.Close()

	// Lecture de la réponse
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	collector.Print(log.DebugLevel, "OpenSVC API Response: %s", string(body))

	// Vérification du code de statut HTTP
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (collector *Collector) CreateSecretKeyValueV2(namespace string, service string, key string, value string) error {

	urlpost := fmt.Sprintf("https://%s:%s/key", collector.Host, collector.Port)

	// Création de la structure de données
	requestData := ConfigKeyValueRequest{
		Path: fmt.Sprintf("%s/sec/%s", namespace, service),
		Key:  key,
		Data: value,
	}

	// Sérialisation en JSON
	jsonData, err := json.Marshal(requestData)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	collector.Print(log.DebugLevel, "API Request: %s Payload: %s", urlpost, jsonData)

	client := collector.GetHttpClient()
	b := bytes.NewBuffer(jsonData)
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", "ANY")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	collector.Print(log.DebugLevel, "OpenSVC API Response: %s", string(body))

	return nil
}

type CreateRequest struct {
	Data      map[string]interface{} `json:"data"`
	Namespace string                 `json:"namespace,omitempty"`
	Provision bool                   `json:"provision,omitempty"`
	Sync      bool                   `json:"sync,omitempty"`
}

func (collector *Collector) CreateSecretV2(namespace string, service string, agent string) error {

	urlpost := "https://" + collector.Host + ":" + collector.Port + "/create"

	// just create or replace
	reqparams := CreateRequest{
		Data: map[string]interface{}{
			namespace + "/sec/" + service: struct{}{}, // Utilisation d'une structure vide pour créer ou remplacer
		},
	}

	jsondata, err := json.Marshal(reqparams)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	collector.Print(log.DebugLevel, "API Request: %s Payload: %s", urlpost, jsondata)

	client := collector.GetHttpClient()
	b := bytes.NewBuffer([]byte(jsondata))
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
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
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	collector.Print(log.DebugLevel, "OpenSVC API Response: %s", string(body))
	return nil
}

func (collector *Collector) CreateConfigV2(namespace string, service string, agent string) error {

	urlpost := "https://" + collector.Host + ":" + collector.Port + "/create"
	// just create or replace
	reqparams := CreateRequest{
		Data: map[string]interface{}{
			namespace + "/cfg/" + service: struct{}{}, // Utilisation d'une structure vide pour créer ou remplacer
		},
	}
	//jsondata := `{"data": {"` + namespace + `/cfg/` + service + `": {}}}`
	// Utilisation de json.Marshal pour sérialiser la structure
	jsondata, err := json.Marshal(reqparams)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	collector.Print(log.DebugLevel, "API Request: %s Payload: %s", urlpost, jsondata)

	client := collector.GetHttpClient()
	b := bytes.NewBuffer([]byte(jsondata))
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
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
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	collector.Print(log.DebugLevel, "OpenSVC API Response: %s", string(body))

	return nil
}

// CreateTemplateV2 post a template to the collector

func (collector *Collector) CreateTemplateV2(cluster string, srv string, node string, template []byte) error {

	urlpost := "https://" + collector.Host + ":" + collector.Port + "/create"

	//jsondata := `{"namespace": "` + cluster + `", "provision": true, "sync": true, "data": {"` + srv + `": "` + template + `"}}`
	// Utilisation de json.Marshal pour sérialiser la structure
	reqparams := CreateRequest{
		Data: map[string]interface{}{
			srv: "", // Utilisation de la chaîne de caractères comme valeur
		},
		Namespace: cluster,
		Provision: true,
		Sync:      true,
	}

	jsondata, err := json.Marshal(reqparams)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	jsondata, err = sjson.SetRawBytes(jsondata, fmt.Sprintf("data.%s", srv), template)
	if err != nil {
		return fmt.Errorf("failed to set JSON data: %w", err)
	}

	// Log the request if debug level is enabled
	collector.Print(log.DebugLevel, "OpenSVC API Request: %s Payload: %s", urlpost, jsondata)

	client := collector.GetHttpClient()
	b := bytes.NewBuffer([]byte(jsondata))
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

	collector.Print(log.DebugLevel, "OpenSVC API Response: %s", string(body))

	return nil
}

func (collector *Collector) CreateTemplateV2Monitor(srv string, node string) error {

	urlpost := "https://" + collector.Host + ":" + collector.Port + "/object_monitor"
	//jsondata := `{"path": "` + srv + `", "global_expect": "provisioned", "options": {}}`
	// Utilisation de json.Marshal pour sérialiser la structure
	reqparams := ActionRequest{
		Path:         srv,
		GlobalExpect: "provisioned",
		Options:      map[string]interface{}{},
	}

	jsondata, err := json.Marshal(reqparams)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	collector.Print(log.DebugLevel, "OpenSVC API Request: %s Payload: %s", urlpost, jsondata)

	client := collector.GetHttpClient()
	b := bytes.NewBuffer(jsondata)
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

	collector.Print(log.DebugLevel, "OpenSVC API Response: %s", string(body))
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

	collector.Print(log.DebugLevel, "OpenSVC API Response: %s", string(body))
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

	collector.Print(log.DebugLevel, "OpenSVC API Response: %s", string(body))
	return nil

}

func (collector *Collector) GetNodes() ([]Host, error) {

	url := "https://" + collector.Host + ":" + collector.Port + "/init/rest/api/nodes?props=id,node_id,nodename,status,cpu_cores,cpu_freq,mem_bytes,os_kernel,os_name,tz"
	if !collector.UseCollectorAPI {
		url = "https://" + collector.Host + ":" + collector.Port + "/get_node"
	}
	client := collector.GetHttpClient()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if collector.UseCollectorAPI {
		req.SetBasicAuth(collector.RplMgrUser, collector.RplMgrPassword)
		//		collector.Logrus.WithField("FROM", "OpenSVC").Printf("Info opensvc login %s %s", collector.RplMgrUser, collector.RplMgrPassword)
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
	collector.Print(log.DebugLevel, "OpenSVC Connect took: %s\n", stopConnect.Sub(startConnect))

	if err != nil {
		collector.Print(log.ErrorLevel, "OpenSVC API Error: %s", err)
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
	collector.Print(log.DebugLevel, "OpenSVC Read response took: %s\n", endRead.Sub(startRead))
	collector.Print(log.DebugLevel, "OpenSVC API Response: %s", string(body))

	if collector.UseCollectorAPI {
		type Message struct {
			Data []Host `json:"data"`
		}
		var r Message

		err = json.Unmarshal(body, &r)
		if err != nil {
			collector.Print(log.ErrorLevel, "OpenSVC API Error: %s", err)
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
		collector.Print(log.ErrorLevel, "OpenSVC API Error: %s", err)
		return nil, err
	}
	crcTable := crc64.MakeTable(crc64.ECMA)

	nhosts := make([]Host, len(r.Data), len(r.Data))
	i := 0
	for _, agent := range r.Data {
		//		collector.Logrus.WithField("FROM", "OpenSVC").Println("ERROR ", agent)
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

func (collector *Collector) GetServiceNodeFromState(svc string) ([]string, error) {
	url := fmt.Sprintf("https://%s:%s/object_status?path=%s", collector.Host, collector.Port, url.QueryEscape(svc))
	client := collector.GetHttpClient()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("content-type", "application/json")
	req.Header.Set("o-node", "*")

	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)

	defer cancel()
	req = req.WithContext(ctx)

	startConnect := time.Now()
	resp, err := client.Do(req)

	stopConnect := time.Now()

	collector.Print(log.DebugLevel, "OpenSVC Connect took: %s\n", stopConnect.Sub(startConnect))

	if err != nil {
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
	collector.Print(log.DebugLevel, "OpenSVC Read response took: %s\n", endRead.Sub(startRead))
	collector.Print(log.DebugLevel, "OpenSVC API Response: %s", string(body))

	key := `nodes.@values.#.{node:nodes.@keys,val:nodes.@values.#.status.avail}.@group.#(val=="up")#.node`
	results := gjson.GetBytes(body, key)
	if !results.Exists() {
		return nil, errors.New("Service node not found")
	}

	nodes := collector.GetUniqueNodes(results.Value())
	if len(nodes) == 0 {
		return nil, errors.New("No nodes found for service")
	}

	return nodes, nil
}

func (collector *Collector) GetUniqueNodes(slices interface{}) []string {
	uniqueMap := make(map[string]struct{})
	if slices == nil {
		return nil
	}

	slices, ok := slices.([]interface{})
	if !ok {
		collector.GetUniqueValuesFromSlicesRecursive([]interface{}{slices}, uniqueMap)
	}

	for _, item := range slices.([]interface{}) {
		if arr, ok := item.([]interface{}); ok {
			collector.GetUniqueValuesFromSlicesRecursive(arr, uniqueMap)
		}

		if str, ok := item.(string); ok && str != "" {
			uniqueMap[str] = struct{}{}
		}
	}

	uniqueSlice := make([]string, 0, len(uniqueMap))
	for key := range uniqueMap {
		uniqueSlice = append(uniqueSlice, key)
	}

	return uniqueSlice
}

func (collector *Collector) GetUniqueValuesFromSlicesRecursive(slices []interface{}, valuemap map[string]struct{}) {
	for _, item := range slices {
		if arr, ok := item.([]interface{}); ok {
			collector.GetUniqueValuesFromSlicesRecursive(arr, valuemap)
			continue
		}

		switch v := item.(type) {
		case string:
			if v != "" {
				valuemap[v] = struct{}{}
			}
		case bool:
			valuemap[strconv.FormatBool(v)] = struct{}{}
		case float64:
			valuemap[strconv.FormatFloat(v, 'f', -1, 64)] = struct{}{}
		case int:
			valuemap[strconv.Itoa(v)] = struct{}{}
		default:
			if item != nil {
				collector.Print(log.WarnLevel, "Unhandled type in GetUniqueValuesFromSlicesRecursive: %v", v)
			}
		}
	}
}

type NodeStats struct {
	Load15m   float64 `json:"load_15m"`
	MemTotal  int64   `json:"mem_total"`
	MemAvail  int64   `json:"mem_avail"`
	SwapTotal int64   `json:"swap_total"`
	SwapAvail int64   `json:"swap_avail"`
	Score     int64   `json:"score"`
}

type DaemonNodeStats struct {
	Node         string    `json:"node"`
	Stats        NodeStats `json:"stats"`
	MinAvailMem  int64     `json:"min_avail_mem"`
	MinAvailSwap int64     `json:"min_avail_swap"`
	Cores        int64     `json:"cores"`
}

func (collector *Collector) GetDaemonNodeStats() ([]DaemonNodeStats, error) {
	stats := make([]DaemonNodeStats, 0)

	// Get the list of nodes
	nodes, err := collector.GetNodes()
	if err != nil {
		return nil, err
	}

	coreList := make(map[string]int64)
	for _, node := range nodes {
		if node.Cpu_cores > 0 {
			coreList[node.Node_name] = node.Cpu_cores
		} else {
			coreList[node.Node_name] = 1
		}
	}

	urlget := fmt.Sprintf("https://%s:%s/daemon_status", collector.Host, collector.Port)

	client := collector.GetHttpClient()

	req, err := http.NewRequest("GET", urlget, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	if collector.UseCollectorAPI {
		req.SetBasicAuth(collector.RplMgrUser, collector.RplMgrPassword)
	} else {
		req.Header.Set("o-node", "ANY")
	}

	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)

	defer cancel()
	req = req.WithContext(ctx)

	startConnect := time.Now()
	resp, err := client.Do(req)

	stopConnect := time.Now()
	collector.Print(log.DebugLevel, "OpenSVC Connect took: %s\n", stopConnect.Sub(startConnect))

	if err != nil {
		return nil, err
	}

	defer client.CloseIdleConnections()
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Extract node stats using gjson
	key := "monitor.nodes.{node:@keys,stats:@values.#.stats,min_avail_mem:@values.#.min_avail_mem, min_avail_swap:@values.#.min_avail_swap}.@group"
	result := gjson.GetBytes(body, key)
	if !result.Exists() {
		return nil, errors.New("No node stats found")
	}

	err = json.Unmarshal([]byte(result.Raw), &stats)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	for i, stat := range stats {
		if cores, ok := coreList[stat.Node]; ok {
			stats[i].Cores = cores
		} else {
			stats[i].Cores = 1
		}
	}

	return stats, nil
}
