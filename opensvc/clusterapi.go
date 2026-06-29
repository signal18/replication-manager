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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"golang.org/x/net/http2"
	//	pkcs12 "software.sslmate.com/src/go-pkcs12"
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
			if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
				collector.Logrus.WithField("FROM", "OpenSVC").Errorln("ERROR ParseCertificatesDER ", err)
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

func (collector *Collector) GetGottyServer(srv string, rid string) (string, string, error) {
	client := collector.GetHttpClient()
	//jsondata := `{"path": "` + srv + `", "rid":"` + rid + `", "timeout": "10s"}`

	//b := bytes.NewBuffer([]byte(jsondata))
	urlget := "https://" + collector.Host + ":" + collector.Port + "/object_enter?path=" + url.QueryEscape(srv) + "&rid=" + url.QueryEscape(rid) + "&timout=5s"
	req, err := http.NewRequest("GET", urlget, nil)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: Srv:" + srv + " Rid:" + rid + " Err:" + err.Error())
		}
		return "", "", err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", "ANY")
	resp, err := client.Do(req)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: Srv:" + srv + " Rid:" + rid + " Err:" + err.Error())

		}
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlInfo) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	}

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
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
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
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("JSON Marshal Error: ", err)
		}
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

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlInfo) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	}

	return nil
}

func (collector *Collector) RestartServiceV2(cluster string, srv string, node string, rid string) error {

	reqparams := ActionRequest{
		Path:    srv,
		Action:  "restart",
		Options: map[string]interface{}{},
	}

	if rid != "" {
		reqparams.Options["rid"] = rid
	}

	jsondata, err := json.Marshal(reqparams)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("JSON Marshal Error: ", err)
		}
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

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlInfo) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	}

	return nil
}

func (collector *Collector) RunTask(cluster string, srv string, node string, task string, parameter string) error {
	if collector.IsV3() {
		return collector.RunTaskV3(srv, node, task, parameter)
	}
	return collector.RunTaskV2(cluster, srv, node, task, parameter)
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
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("JSON Marshal Error: ", err)
		}
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	b := bytes.NewBuffer([]byte(jsondata))
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/service_action"

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("API Request: ", urlpost, " Payload: ", jsondata)
	}

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

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlInfo) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	}

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
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("JSON Marshal Error: ", err)
		}
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

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.Logrus.WithField("FROM", "OpenSVC").Printf("API Request: %s Header: %v Payload: %s", urlpost, req.Header, string(jsondata))
	}

	client := collector.GetHttpClient()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlInfo) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	}
	return nil
}

// SetServiceConfigKeysV2 surgically sets one or more INI keys on a live service
// via POST /service_action with action "set". Each entry in kw must be
// "section.key=value" (e.g. "container#db.image_pull_policy=always").
//
// API handler:  opensvc/daemon/handlers/object/action/post.py (b2.1)
//   https://github.com/opensvc/opensvc/blob/b2.1/opensvc/daemon/handlers/object/action/post.py
// --kw option (action="append", supports multiple entries per call):
//   https://github.com/opensvc/opensvc/blob/b2.1/opensvc/utilities/render/command.py
//   https://github.com/opensvc/opensvc/blob/b2.1/opensvc/commands/mgr/parser.py
func (collector *Collector) SetServiceConfigKeysV2(srv string, node string, kw []string) error {
	reqparams := ActionRequest{
		Path:   srv,
		Action: "set",
		Sync:   true,
		Options: map[string]interface{}{
			"kw": kw,
		},
	}
	return collector.postServiceAction(reqparams, node)
}

// UnsetServiceConfigKeysV2 removes one or more INI keys from a live service
// via POST /service_action with action "unset". Each entry in kw must be
// "section.key" (e.g. "container#db.image_pull_policy").
//
// API handler:  opensvc/daemon/handlers/object/action/post.py (b2.1)
//   https://github.com/opensvc/opensvc/blob/b2.1/opensvc/daemon/handlers/object/action/post.py
// "unset" is in ADMIN_ACTIONS and follows the standard format_command path.
func (collector *Collector) UnsetServiceConfigKeysV2(srv string, node string, kw []string) error {
	reqparams := ActionRequest{
		Path:   srv,
		Action: "unset",
		Sync:   true,
		Options: map[string]interface{}{
			"kw": kw,
		},
	}
	return collector.postServiceAction(reqparams, node)
}

func (collector *Collector) postServiceAction(reqparams ActionRequest, node string) error {
	jsondata, err := json.Marshal(reqparams)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("JSON Marshal Error: ", err)
		}
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	urlpost := "https://" + collector.Host + ":" + collector.Port + "/service_action"

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.Logrus.WithField("FROM", "OpenSVC").Printf("API Request: %s Header: o-node=%s Payload: %s", urlpost, node, string(jsondata))
	}

	req, err := http.NewRequest("POST", urlpost, bytes.NewBuffer(jsondata))
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

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlInfo) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	}
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
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("JSON Marshal Error: ", err)
		}
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	b := bytes.NewBuffer([]byte(jsondata))
	urlpost := "https://" + collector.Host + ":" + collector.Port + "/object_monitor"

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("API Request: ", urlpost, " Payload: ", jsondata)
	}

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

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlInfo) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	}
	return nil
}

func (collector *Collector) CreateConfigKeyValue(namespace string, service string, key string, value string) error {
	if collector.IsV3() {
		return collector.CreateConfigKeyValueV3(namespace, service, key, value)
	}

	return collector.CreateConfigKeyValueV2(namespace, service, key, value)
}

// DeleteConfigKeyValue deletes a single key from a cfg data store object.
func (collector *Collector) DeleteConfigKeyValue(namespace, service, key string) error {
	if collector.IsV3() {
		return collector.DeleteConfigKeyValueV3(namespace, service, key)
	}
	return collector.DeleteConfigKeyValueV2(namespace, service, key)
}

// DeleteConfigKeyValueV2 deletes a key from a V2 cfg object via DELETE /key.
func (collector *Collector) DeleteConfigKeyValueV2(namespace, service, key string) error {
	urldelete := fmt.Sprintf("https://%s:%s/key", collector.Host, collector.Port)
	requestData := ConfigKeyValueRequest{
		Path: fmt.Sprintf("%s/cfg/%s", namespace, service),
		Key:  key,
	}
	jsonData, err := json.Marshal(requestData)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}
	client := collector.GetHttpClient()
	req, err := http.NewRequest("DELETE", urldelete, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", "ANY")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete key: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP DELETE /key failed with status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// ListConfigKeys returns all key names stored in a cfg data store object.
func (collector *Collector) ListConfigKeys(namespace, service string) ([]string, error) {
	if collector.IsV3() {
		return collector.ListConfigKeysV3(namespace, service)
	}
	return collector.ListConfigKeysV2(namespace, service)
}

// ListConfigKeysV2 lists keys in a V2 cfg object via GET /object_keys.
func (collector *Collector) ListConfigKeysV2(namespace, service string) ([]string, error) {
	path := fmt.Sprintf("%s/cfg/%s", namespace, service)
	urlget := fmt.Sprintf("https://%s:%s/object_keys?path=%s", collector.Host, collector.Port, url.QueryEscape(path))
	client := collector.GetHttpClient()
	req, err := http.NewRequest("GET", urlget, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", "ANY")
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to list keys: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP GET /object_keys failed with status %d: %s", resp.StatusCode, string(body))
	}
	data := gjson.GetBytes(body, "data")
	if gjson.GetBytes(body, "nodes").Exists() {
		data = gjson.GetBytes(body, "nodes.@values.0.data")
	}
	var keys []string
	for _, item := range data.Array() {
		if item.Type == gjson.String {
			keys = append(keys, item.String())
		} else if item.IsObject() {
			if name := item.Get("name"); name.Exists() {
				keys = append(keys, name.String())
			}
		}
	}
	return keys, nil
}

// checkV2ResponseBody inspects the JSON body returned by V2 write endpoints
// (/key and /create) for logical-level failures that are signalled inside a
// 2xx response via {"error":"..."} or {"status":<non-zero>}.
func checkV2ResponseBody(body []byte) error {
	if errField := gjson.GetBytes(body, "error"); errField.Exists() {
		if msg := strings.TrimSpace(errField.String()); msg != "" {
			return fmt.Errorf("opensvc API error: %s", msg)
		}
	}
	if statusField := gjson.GetBytes(body, "status"); statusField.Exists() {
		if statusField.Int() != 0 {
			return fmt.Errorf("opensvc API returned non-zero status %d", statusField.Int())
		}
	}
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
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("JSON Marshal Error: ", err)
		}
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("API Request: ", urlpost, " Payload: ", string(jsonData))
	}

	client := collector.GetHttpClient()

	// Création de la requête HTTP
	req, err := http.NewRequest("POST", urlpost, bytes.NewBuffer(jsonData))
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("HTTP Request Creation Error: ", err)
		}
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Configuration des headers
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", "ANY")

	// Exécution de la requête
	resp, err := client.Do(req)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("HTTP Request Execution Error: ", err)
		}
		return fmt.Errorf("failed to execute HTTP request: %w", err)
	}
	defer resp.Body.Close()

	// Lecture de la réponse
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("Response Body Read Error: ", err)
		}
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlInfo) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	}

	// Vérification du code de statut HTTP
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return checkV2ResponseBody(body)
}

func (collector *Collector) CreateSecretKeyValue(namespace string, service string, key string, value string) error {
	if collector.IsV3() {
		return collector.CreateSecretKeyValueV3(namespace, service, key, value)
	} else {
		return collector.CreateSecretKeyValueV2(namespace, service, key, value)
	}
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
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("JSON Marshal Error: ", err)
		}
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("API Request: ", urlpost, " Payload: ", string(jsonData))
	}

	client := collector.GetHttpClient()
	b := bytes.NewBuffer(jsonData)
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("Api Error: ", err)
		}
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", "ANY")
	resp, err := client.Do(req)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("Api Error: ", err)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlInfo) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return checkV2ResponseBody(body)
}

type CreateRequest struct {
	Data      map[string]interface{} `json:"data"`
	Namespace string                 `json:"namespace,omitempty"`
	Provision bool                   `json:"provision,omitempty"`
	Sync      bool                   `json:"sync,omitempty"`
}

var ErrObjectAlreadyExists = errors.New("opensvc object already exists")
var ErrUnknownService = errors.New("unknown service")

// ObjectExistsV2 checks whether a secret or config object exists on the V2 collector
// by querying the /object_status endpoint. Returns (true, nil) when the object exists,
// (false, nil) when it is absent (404 or status!=0), and (false, err) on server errors.
// objectStatusV2Exists inspects a 2xx object_status body and reports whether
// the object actually exists. Upstream b2.1 returns 200 {"nodes":{}} for a
// missing object — HTTP 200 alone is not a sufficient existence signal.
//
// An object is considered to exist when:
//   - the top-level JSON contains fields other than "nodes" (e.g. "kind",
//     "updated", "placement"), indicating a real object record; OR
//   - the "nodes" map contains at least one entry.
//
// A non-empty "error" field is returned as an error regardless of existence.
func objectStatusV2Exists(body []byte) (bool, error) {
	if errField := gjson.GetBytes(body, "error"); errField.Exists() {
		if msg := strings.TrimSpace(errField.String()); msg != "" {
			return false, fmt.Errorf("opensvc object_status error: %s", msg)
		}
	}
	// Any top-level field other than "nodes" or "error" means the object is
	// recorded (e.g. "kind", "updated", "placement"). "error" is a response
	// meta-field, not object data.
	hasObjectFields := false
	gjson.ParseBytes(body).ForEach(func(key, _ gjson.Result) bool {
		k := key.String()
		if k != "nodes" && k != "error" {
			hasObjectFields = true
			return false
		}
		return true
	})
	if hasObjectFields {
		return true, nil
	}
	// Non-empty nodes map means the object is provisioned on at least one node.
	return len(gjson.GetBytes(body, "nodes").Map()) > 0, nil
}

func (collector *Collector) ObjectExistsV2(path string, agent string) (bool, error) {
	urlget := fmt.Sprintf("https://%s:%s/object_status?path=%s", collector.Host, collector.Port, url.QueryEscape(path))

	client := collector.GetHttpClient()
	req, err := http.NewRequest("GET", urlget, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	myagent := "ANY"
	if agent != "" {
		myagent = agent
	}
	req.Header.Set("o-node", myagent)

	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("object_status HTTP %d: %s", resp.StatusCode, string(body))
	}

	return objectStatusV2Exists(body)
}

func (collector *Collector) KeysExists(path string, agent string) (bool, error) {
	urlget := fmt.Sprintf("https://%s:%s/object_keys?path=%s", collector.Host, collector.Port, url.QueryEscape(path))

	client := collector.GetHttpClient()
	req, err := http.NewRequest("GET", urlget, nil)
	if err != nil {
		return false, err
	}

	req.Header.Set("Content-Type", "application/json")
	myagent := "ANY"
	if agent != "" {
		myagent = agent
	}
	req.Header.Set("o-node", myagent)

	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("HTTP request failed with status %d: %s", resp.StatusCode, string(body))
	}

	errbody := gjson.GetBytes(body, "error")
	if errbody.Exists() {
		errMsg := errbody.String()
		if errMsg == ErrUnknownService.Error() {
			return false, ErrUnknownService
		}
		return false, errors.New(errMsg)
	}

	data := gjson.GetBytes(body, "data")
	if gjson.GetBytes(body, "nodes").Exists() {
		data = gjson.GetBytes(body, "nodes.@values.0.data")
	}

	if !data.Exists() || len(data.Array()) == 0 {
		return false, nil
	}

	return true, nil
}

func (collector *Collector) CreateSecret(namespace string, service string, agent string) error {
	if collector.IsV3() {
		return collector.CreateSecretV3(namespace, service, agent)
	} else {
		return collector.CreateSecretV2(namespace, service, agent)
	}
}

func (collector *Collector) CreateSecretV2(namespace string, service string, agent string) error {
	path := fmt.Sprintf("%s/sec/%s", namespace, service)
	exists, err := collector.ObjectExistsV2(path, agent)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("%w: %s", ErrObjectAlreadyExists, path)
	}

	urlpost := "https://" + collector.Host + ":" + collector.Port + "/create"

	// create only if missing to avoid wiping existing custom values
	reqparams := CreateRequest{
		Data: map[string]interface{}{
			path: struct{}{},
		},
	}

	jsondata, err := json.Marshal(reqparams)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("JSON Marshal Error: ", err)
		}
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("API Request: ", urlpost, " Payload: ", jsondata)
	}

	client := collector.GetHttpClient()
	b := bytes.NewBuffer([]byte(jsondata))
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("Api Error: ", err)
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
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("Api Error: ", err)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlInfo) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return checkV2ResponseBody(body)
}

func (collector *Collector) CreateConfig(namespace string, service string, agent string) error {
	if collector.IsV3() {
		return collector.CreateConfigV3(namespace, service, agent)
	} else {
		return collector.CreateConfigV2(namespace, service, agent)
	}
}

func (collector *Collector) CreateConfigV2(namespace string, service string, agent string) error {
	path := fmt.Sprintf("%s/cfg/%s", namespace, service)
	exists, err := collector.ObjectExistsV2(path, agent)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("%w: %s", ErrObjectAlreadyExists, path)
	}

	urlpost := "https://" + collector.Host + ":" + collector.Port + "/create"
	// create only if missing to avoid wiping existing custom values
	reqparams := CreateRequest{
		Data: map[string]interface{}{
			path: struct{}{},
		},
	}
	//jsondata := `{"data": {"` + namespace + `/cfg/` + service + `": {}}}`
	// Utilisation de json.Marshal pour sérialiser la structure
	jsondata, err := json.Marshal(reqparams)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("JSON Marshal Error: ", err)
		}
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("API Request: ", urlpost, " Payload: ", jsondata)
	}

	client := collector.GetHttpClient()
	b := bytes.NewBuffer([]byte(jsondata))
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("Api Error: ", err)
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
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("Api Error: ", err)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlInfo) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("Api Response: ", string(body))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return checkV2ResponseBody(body)
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
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("JSON Marshal Error: ", err)
		}
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	jsondata, err = sjson.SetRawBytes(jsondata, fmt.Sprintf("data.%s", srv), template)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("JSON Set Error: ", err)
		}
		return fmt.Errorf("failed to set JSON data: %w", err)
	}

	// Log the request if debug level is enabled
	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("API Request: ", urlpost, " Payload: ", string(jsondata))
	}

	client := collector.GetHttpClient()
	b := bytes.NewBuffer([]byte(jsondata))
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", node)
	resp, err := client.Do(req)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlInfo) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	}
	//	collector.WaitServiceAvailable(srv, node)
	//	collector.WaitServicePropagate(srv, node)

	//	collector.CreateTemplateV2Monitor(srv, node)

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
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("JSON Marshal Error: ", err)
		}
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("OpenSVC API Request: ", urlpost, " Payload: ", jsondata)
	}

	client := collector.GetHttpClient()
	b := bytes.NewBuffer(jsondata)
	req, err := http.NewRequest("POST", urlpost, b)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", node)
	resp, err := client.Do(req)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlInfo) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	}
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
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", node)

	resp, err := client.Do(req)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlInfo) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	}
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
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return err
	}
	req.Close = true
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("o-node", node)

	resp, err := client.Do(req)
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlInfo) {
		collector.Logrus.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	}
	return nil

}

func (collector *Collector) GetNodes() ([]Host, error) {
	if collector.UseCollectorAPI {
		return collector.GetNodesV1()
	} else if collector.IsV3() {
		return collector.GetNodesV3()
	} else {
		return collector.GetNodesV2()
	}
}

type PoolInfo struct {
	Name         string
	Shared       bool
	Capabilities []string
}

func normalizeStringList(values []string) []string {
	uniq := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		uniq[value] = struct{}{}
	}

	result := make([]string, 0, len(uniq))
	for value := range uniq {
		result = append(result, value)
	}

	sort.Strings(result)
	return result
}

func normalizePoolInfoList(values []PoolInfo) []PoolInfo {
	if len(values) == 0 {
		return values
	}

	byName := make(map[string]PoolInfo, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value.Name)
		if name == "" {
			continue
		}

		normalizedCaps := normalizeStringList(value.Capabilities)
		value.Name = name
		value.Capabilities = normalizedCaps
		value.Shared = value.Shared || slicesContains(normalizedCaps, "shared")

		if existing, ok := byName[name]; ok {
			existing.Shared = existing.Shared || value.Shared
			existing.Capabilities = normalizeStringList(append(existing.Capabilities, value.Capabilities...))
			existing.Shared = existing.Shared || slicesContains(existing.Capabilities, "shared")
			byName[name] = existing
			continue
		}

		byName[name] = value
	}

	result := make([]PoolInfo, 0, len(byName))
	for _, value := range byName {
		result = append(result, value)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result
}

func slicesContains(values []string, needle string) bool {
	for _, v := range values {
		if v == needle {
			return true
		}
	}
	return false
}

func parseCapabilities(result gjson.Result) []string {
	capabilities := make([]string, 0)
	if !result.Exists() {
		return capabilities
	}

	if result.IsArray() {
		for _, item := range result.Array() {
			if item.Type == gjson.String {
				capabilities = append(capabilities, strings.TrimSpace(item.String()))
			}
		}
	}

	return normalizeStringList(capabilities)
}

func poolInfoFromResult(result gjson.Result, fallbackName string) PoolInfo {
	name := strings.TrimSpace(result.Get("name").String())
	if name == "" {
		name = strings.TrimSpace(fallbackName)
	}

	capabilities := parseCapabilities(result.Get("capabilities"))
	shared := result.Get("shared").Bool() || slicesContains(capabilities, "shared")

	return PoolInfo{
		Name:         name,
		Shared:       shared,
		Capabilities: capabilities,
	}
}

func (collector *Collector) GetPoolInfoList() ([]PoolInfo, error) {
	if collector.IsV3() {
		return collector.GetPoolInfoListV3()
	}

	return collector.GetPoolInfoListV2()
}

func (collector *Collector) GetPoolList() ([]string, error) {
	poolInfos, err := collector.GetPoolInfoList()
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(poolInfos))
	for _, pool := range poolInfos {
		names = append(names, pool.Name)
	}

	return normalizeStringList(names), nil
}

func (collector *Collector) GetPoolListV2() ([]string, error) {
	poolInfos, err := collector.GetPoolInfoListV2()
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(poolInfos))
	for _, pool := range poolInfos {
		names = append(names, pool.Name)
	}

	return normalizeStringList(names), nil
}

func (collector *Collector) GetPoolInfoListV2() ([]PoolInfo, error) {
	url := fmt.Sprintf("https://%s:%s/get_pools", collector.Host, collector.Port)

	client := collector.GetHttpClient()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("content-type", "application/json")
	req.Header.Set("o-node", "*")

	ctx, cancel := context.WithTimeout(req.Context(), time.Duration(collector.ContextTimeoutSecond)*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	startConnect := time.Now()
	resp, err := client.Do(req)
	stopConnect := time.Now()
	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.Logrus.WithField("FROM", "OpenSVC").Printf("OpenSVC Connect took: %s\n", stopConnect.Sub(startConnect))
	}
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
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
	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.Logrus.WithField("FROM", "OpenSVC").Printf("OpenSVC Read response took: %s\n", endRead.Sub(startRead))
		collector.Logrus.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	}

	if !handleSuccessGroup(resp.StatusCode) {
		return nil, &StatusError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}

	result := gjson.ParseBytes(body)
	poolInfos := make([]PoolInfo, 0)

	if result.IsArray() {
		for _, item := range result.Array() {
			if item.Type == gjson.String {
				poolInfos = append(poolInfos, PoolInfo{Name: strings.TrimSpace(item.String())})
				continue
			}
			poolInfos = append(poolInfos, poolInfoFromResult(item, ""))
		}
	}

	if nodes := result.Get("nodes"); nodes.Exists() && nodes.IsObject() {
		nodes.ForEach(func(_, nodeData gjson.Result) bool {
			nodeData.ForEach(func(poolName, poolData gjson.Result) bool {
				poolInfos = append(poolInfos, poolInfoFromResult(poolData, poolName.String()))
				return true
			})
			return true
		})
	}

	if result.IsObject() {
		// All three blocks can fire for the same response (e.g. object with both "nodes" and
		// top-level pool keys). The nodes block and this block intentionally coexist.
		// Known top-level metadata keys must be listed here; if the API adds new metadata
		// object fields in the future they must be added to avoid being misread as pool names.
		knownMetaKeys := map[string]bool{"status": true, "data": true, "error": true, "nodes": true}
		result.ForEach(func(key, value gjson.Result) bool {
			if !knownMetaKeys[key.String()] && value.IsObject() {
				poolInfos = append(poolInfos, poolInfoFromResult(value, key.String()))
			}
			return true
		})
		if data := result.Get("data"); data.Exists() && data.IsArray() {
			for _, item := range data.Array() {
				if item.Type == gjson.String {
					poolInfos = append(poolInfos, PoolInfo{Name: strings.TrimSpace(item.String())})
					continue
				}
				poolInfos = append(poolInfos, poolInfoFromResult(item, ""))
			}
		}
	}

	return normalizePoolInfoList(poolInfos), nil
}

func (collector *Collector) GetServiceNodeFromState(svc string) ([]string, error) {
	if collector.IsV3() {
		return collector.GetServiceNodeFromStateV3(svc)
	} else {
		return collector.GetServiceNodeFromStateV2(svc)
	}
}

func (collector *Collector) GetServiceNodeFromStateV2(svc string) ([]string, error) {
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
	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.Logrus.WithField("FROM", "OpenSVC").Printf("OpenSVC Connect took: %s\n", stopConnect.Sub(startConnect))
	}
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
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
	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.Logrus.WithField("FROM", "OpenSVC").Printf("OpenSVC Read response took: %s\n", endRead.Sub(startRead))
		collector.Logrus.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
	}

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
				collector.Logrus.WithField("FROM", "OpenSVC").Warnln("Unhandled type in GetUniqueValuesFromSlicesRecursive:", v)
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
		//		collector.Logrus.WithField("FROM", "OpenSVC").Printf("Info opensvc login %s %s", collector.RplMgrUser, collector.RplMgrPassword)
	} else {
		req.Header.Set("o-node", "ANY")
	}

	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)

	defer cancel()
	req = req.WithContext(ctx)

	startConnect := time.Now()
	resp, err := client.Do(req)

	stopConnect := time.Now()
	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.Logrus.WithField("FROM", "OpenSVC").Printf("OpenSVC Connect took: %s\n", stopConnect.Sub(startConnect))
	}
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

func (collector *Collector) GetNodesV2() ([]Host, error) {

	url := "https://" + collector.Host + ":" + collector.Port + "/get_node"

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
	if err != nil {
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
		return nil, err
	}

	stopConnect := time.Now()
	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.Logrus.WithField("FROM", "OpenSVC").Printf("OpenSVC Connect took: %s\n", stopConnect.Sub(startConnect))
	}

	defer client.CloseIdleConnections()
	defer resp.Body.Close()
	startRead := time.Now()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	endRead := time.Now()
	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.Logrus.WithField("FROM", "OpenSVC").Printf("OpenSVC Read response took: %s\n", endRead.Sub(startRead))
		collector.Logrus.WithField("FROM", "OpenSVC").Println("OpenSVC API Response: ", string(body))
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
		if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlErr) {
			collector.Logrus.WithField("FROM", "OpenSVC").Errorln("OpenSVC API Error: ", err)
		}
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
