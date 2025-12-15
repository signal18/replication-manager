package opensvc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	clientv3 "github.com/opensvc/om3/v3/core/client"
	apiv3 "github.com/opensvc/om3/v3/daemon/api"
	"github.com/opensvc/om3/v3/util/funcopt"
	log "github.com/sirupsen/logrus"

	"github.com/tidwall/gjson"
)

func (collector *Collector) IsV3() bool {
	return collector.ClusterApiVersion == "v3"
}

func (collector *Collector) SetV3() {
	collector.ClusterApiVersion = "v3"
}

func (collector *Collector) GetClientV3() (*clientv3.T, error) {
	opts := []funcopt.O{
		clientv3.WithURL(collector.Host + ":" + collector.Port),
		clientv3.WithInsecureSkipVerify(true),
		clientv3.WithUsername(collector.RplMgrUser),
		clientv3.WithPassword(collector.RplMgrPassword),
	}

	if len(collector.CertsDER) > 0 && collector.CertsDERSecret != "" {
		key, cert, _, err := collector.GeneratePemFromP12(collector.CertsDER, collector.CertsDERSecret)
		if err != nil {
			return nil, fmt.Errorf("failed to generate PEM files: %w", err)
		}

		opts = append(opts, clientv3.WithCertificate(cert), clientv3.WithKey(key))
	}

	client, err := clientv3.New(
		opts...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	return client, nil
}

func (collector *Collector) RequestCloserV3() apiv3.RequestEditorFn {
	return func(ctx context.Context, req *http.Request) error {
		req.Close = true
		return nil
	}
}

func (collector *Collector) RequestPrinterV3() apiv3.RequestEditorFn {
	return func(ctx context.Context, req *http.Request) error {
		collector.Print(log.DebugLevel, "Sending request to OpenSVC API: %s %s", req.Method, req.URL.Path)
		return nil
	}
}

func (collector *Collector) GetAuthInfoV3() error {
	client, err := collector.GetClientV3()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(collector.ContextTimeoutSecond)*time.Second)
	defer cancel()

	// Use the client to check the API version
	resp, err := client.GetAuthInfo(ctx, collector.RequestCloserV3(), collector.RequestPrinterV3())
	if err != nil {
		return fmt.Errorf("failed to check API version: %w", err)
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if !handleSuccessGroup(resp.StatusCode) {
		return fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, body)
	}

	// Set the API version in the collector if successful
	collector.SetV3()

	return nil
}

func (collector *Collector) GetNodesV3() ([]Host, error) {
	collector.Print(log.DebugLevel, "Getting nodes from OpenSVC API using V3")

	client, err := collector.GetClientV3()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(collector.ContextTimeoutSecond)*time.Second)
	defer cancel()

	// Use the client to get the nodes
	resp, err := client.GetNodes(ctx, nil, collector.RequestCloserV3(), collector.RequestPrinterV3())
	if err != nil {
		collector.Print(log.ErrorLevel, "Error getting nodes from OpenSVC API: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	startRead := time.Now()
	body, err := io.ReadAll(resp.Body)
	endRead := time.Now()
	collector.Print(log.DebugLevel, "OpenSVC Read response took: %s", endRead.Sub(startRead))
	collector.Print(log.DebugLevel, "OpenSVC API Response: %s", string(body))

	if err != nil {
		collector.Print(log.ErrorLevel, "Error reading nodes response from OpenSVC API: %v", err)
		return nil, err
	}

	if !handleSuccessGroup(resp.StatusCode) {
		collector.Print(log.ErrorLevel, "Unexpected status code getting nodes from OpenSVC API: %d, body: %s", resp.StatusCode, body)
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, body)
	}

	var hosts []Host = make([]Host, 0)
	// Process the response to extract node information
	nodes := gjson.GetBytes(body, "items.#.meta.node").Array()
	for _, node := range nodes {
		h := Host{
			Node_name: node.String(),
		}

		hosts = append(hosts, h)
	}

	collector.Print(log.DebugLevel, "Found %d nodes from OpenSVC API: %v", len(hosts), hosts)

	return hosts, nil
}

func (collector *Collector) CreateObjectV3(namespace, kind, service string, data []byte) ([]byte, error) {
	var resp *http.Response
	var err error

	client, err := collector.GetClientV3()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(collector.ContextTimeoutSecond)*time.Second)
	defer cancel()

	oKind := apiv3.Kind(kind)
	resp, err = client.PostObjectConfigFileWithBody(ctx, namespace, oKind, service, "application/octet-stream", bytes.NewReader(data), collector.RequestCloserV3(), collector.RequestPrinterV3())
	if err != nil {
		collector.Print(log.ErrorLevel, "Error creating object in OpenSVC API %s/%s/%s: %v", namespace, kind, service, err)
		return nil, fmt.Errorf("failed to create object in %s/%s/%s: %w", namespace, kind, service, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		collector.Print(log.ErrorLevel, "Error reading create object response from OpenSVC API %s/%s/%s: %v", namespace, kind, service, err)
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if !handleSuccessGroup(resp.StatusCode) {
		collector.Print(log.ErrorLevel, "Unexpected status code creating object in OpenSVC API %s/%s/%s: %d, body: %s", namespace, kind, service, resp.StatusCode, body)
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, body)
	}

	err = collector.WaitForObjectV3(namespace, kind, service, time.Minute)
	if err != nil {
		collector.Print(log.ErrorLevel, "Error waiting for object %s/%s/%s to be available in OpenSVC API: %v", namespace, kind, service, err)
		return nil, err
	}

	return body, nil
}

type ObjectGetterFunc func([]byte) ([]byte, error)

func (collector *Collector) WaitForObjectV3(namespace, kind, service string, timeout time.Duration) error {
	start := time.Now()
	printOnce := true
	for {
		_, err := collector.GetObjectV3(namespace, kind, service, nil, printOnce)
		if err == nil {
			return nil
		}
		if time.Since(start) > timeout {
			return fmt.Errorf("timeout waiting for object %s/%s/%s: %w", namespace, kind, service, err)
		}
		time.Sleep(3 * time.Second)
		printOnce = false
	}
}

func (collector *Collector) GetObjectV3(namespace, kind, service string, getFunc ObjectGetterFunc, printRequest bool) ([]byte, error) {
	var resp *http.Response
	var err error

	client, err := collector.GetClientV3()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(collector.ContextTimeoutSecond)*time.Second)
	defer cancel()

	oKind := apiv3.Kind(kind)

	requestModifier := []apiv3.RequestEditorFn{
		collector.RequestCloserV3(),
	}

	if printRequest {
		requestModifier = append(requestModifier, collector.RequestPrinterV3())
	}

	resp, err = client.GetObject(ctx, namespace, oKind, service, requestModifier...)
	if err != nil {
		return nil, fmt.Errorf("failed to get object path in %s/%s/%s: %w", namespace, kind, service, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if !handleSuccessGroup(resp.StatusCode) {
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, body)
	}

	if getFunc == nil {
		return body, nil
	} else {
		return getFunc(body)
	}
}

func (collector *Collector) GetServiceNodeFromStateV3(svc string) ([]string, error) {
	svcparts := strings.SplitN(svc, "/", 3)
	if len(svcparts) != 3 {
		return nil, fmt.Errorf("invalid service format: %s, expected namespace/kind/name", svc)
	}

	namespace := svcparts[0]
	kind := svcparts[1]
	service := svcparts[2]

	// Agents getter function
	getfunc := func(body []byte) ([]byte, error) {
		result := gjson.GetBytes(body, `data.instances.{node:@keys,value:@values}.@group.#(value.status.avail=="up")#.node`)
		if !result.Exists() {
			return nil, fmt.Errorf("no agents found for service %s/%s/%s", namespace, kind, service)
		}

		return []byte(result.Raw), nil
	}

	data, err := collector.GetObjectV3(namespace, kind, service, getfunc, true)
	if err != nil {
		return nil, err
	}

	var agents []string
	result := gjson.ParseBytes(data)
	result.ForEach(func(key, value gjson.Result) bool {
		agents = append(agents, value.String())
		return true
	})

	return agents, nil
}

func (collector *Collector) CreateSecretV3(namespace, service, agent string) error {
	_, err := collector.CreateObjectV3(namespace, "sec", service, []byte{})
	return err
}

func (collector *Collector) CreateConfigV3(namespace, service, agent string) error {
	_, err := collector.CreateObjectV3(namespace, "cfg", service, []byte{})
	return err
}

func (collector *Collector) handleObjectKeyValueV3(operation, namespace, kind, service, key, value string) ([]byte, error) {
	var resp *http.Response
	var err error

	client, err := collector.GetClientV3()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(collector.ContextTimeoutSecond)*time.Second)
	defer cancel()

	if key == "" {
		return nil, fmt.Errorf("key cannot be empty")
	}

	oKind := apiv3.Kind(kind)
	vReader := bytes.NewReader([]byte(value))

	switch strings.ToLower(operation) {
	case "get":
		resp, err = client.GetObjectDataKey(ctx, namespace, oKind, service, &apiv3.GetObjectDataKeyParams{Name: key}, collector.RequestCloserV3(), collector.RequestPrinterV3())
	case "create":
		resp, err = client.PostObjectDataKeyWithBody(ctx, namespace, oKind, service, &apiv3.PostObjectDataKeyParams{Name: key}, "application/octet-stream", vReader, collector.RequestCloserV3(), collector.RequestPrinterV3())
	case "update":
		resp, err = client.PutObjectDataKeyWithBody(ctx, namespace, oKind, service, &apiv3.PutObjectDataKeyParams{Name: key}, "application/octet-stream", vReader, collector.RequestCloserV3(), collector.RequestPrinterV3())
	case "delete":
		resp, err = client.DeleteObjectDataKey(ctx, namespace, oKind, service, &apiv3.DeleteObjectDataKeyParams{Name: key}, collector.RequestCloserV3(), collector.RequestPrinterV3())
	default:
		return nil, fmt.Errorf("unsupported operation: %s", operation)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to %s key '%s' in %s/%s/%s: %w", operation, key, namespace, kind, service, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if !handleSuccessGroup(resp.StatusCode) {
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, body)
	}

	return body, nil
}

func (collector *Collector) GetSecretKeyValueV3(namespace, service, key string) ([]byte, error) {
	return collector.handleObjectKeyValueV3("get", namespace, "sec", service, key, "")
}

func (collector *Collector) CreateSecretKeyValueV3(namespace, service, key, value string) error {
	_, err := collector.handleObjectKeyValueV3("create", namespace, "sec", service, key, value)
	return err
}

func (collector *Collector) UpdateSecretKeyValueV3(namespace, service, key, value string) error {
	_, err := collector.handleObjectKeyValueV3("update", namespace, "sec", service, key, value)
	return err
}

func (collector *Collector) DeleteSecretKeyValueV3(namespace, service, key string) error {
	_, err := collector.handleObjectKeyValueV3("delete", namespace, "sec", service, key, "")
	return err
}

func (collector *Collector) GetConfigKeyValueV3(namespace, service, key string) ([]byte, error) {
	return collector.handleObjectKeyValueV3("get", namespace, "cfg", service, key, "")
}

func (collector *Collector) CreateConfigKeyValueV3(namespace, service, key, value string) error {
	_, err := collector.handleObjectKeyValueV3("create", namespace, "cfg", service, key, value)
	return err
}

func (collector *Collector) UpdateConfigKeyValueV3(namespace, service, key, value string) error {
	_, err := collector.handleObjectKeyValueV3("update", namespace, "cfg", service, key, value)
	return err
}

func (collector *Collector) DeleteConfigKeyValueV3(namespace, service, key string) error {
	_, err := collector.handleObjectKeyValueV3("delete", namespace, "cfg", service, key, "")
	return err
}

func (collector *Collector) handleObjectActionV3(namespace, kind, service, action string, data []byte) ([]byte, error) {
	var resp *http.Response
	var err error

	client, err := collector.GetClientV3()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(collector.ContextTimeoutSecond)*time.Second)
	defer cancel()

	oKind := apiv3.Kind(kind)
	switch strings.ToLower(action) {
	case "provision":
		resp, err = client.PostObjectActionProvision(ctx, namespace, oKind, service, collector.RequestCloserV3(), collector.RequestPrinterV3())
	case "unprovision":
		resp, err = client.PostObjectActionUnprovision(ctx, namespace, oKind, service, collector.RequestCloserV3(), collector.RequestPrinterV3())
	case "start":
		resp, err = client.PostObjectActionStart(ctx, namespace, oKind, service, collector.RequestCloserV3(), collector.RequestPrinterV3())
	case "stop":
		resp, err = client.PostObjectActionStop(ctx, namespace, oKind, service, collector.RequestCloserV3(), collector.RequestPrinterV3())
	case "restart":
		resp, err = client.PostObjectActionRestartWithBody(ctx, namespace, oKind, service, "application/json", bytes.NewReader(data), collector.RequestCloserV3(), collector.RequestPrinterV3())
	case "purge":
		resp, err = client.PostObjectActionPurge(ctx, namespace, oKind, service, collector.RequestCloserV3(), collector.RequestPrinterV3())
	default:
		return nil, fmt.Errorf("unsupported action: %s", action)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to perform action '%s' on %s/%s/%s: %w", action, namespace, kind, service, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if !handleSuccessGroup(resp.StatusCode) {
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, body)
	}

	return body, nil
}

type OrchestrationResponse struct {
	OrchestrationID string `json:"orchestration_id"`
}

func (collector *Collector) CreateTemplateV3(cluster string, svc string, node string, template []byte) error {

	svcparts := strings.SplitN(svc, "/", 3)
	if len(svcparts) != 3 {
		return fmt.Errorf("invalid service format: %s, expected namespace/kind/name", svc)
	}

	ns := svcparts[0]
	kind := svcparts[1]
	svcname := svcparts[2]

	body, err := collector.CreateObjectV3(ns, kind, svcname, template)
	if err != nil {
		return err
	}

	body, err = collector.handleObjectActionV3(ns, kind, svcname, "provision", nil)
	if err != nil {
		return err
	}

	collector.Print(log.DebugLevel, "CreateTemplateV3 provision response body: %s", string(body))

	var respProv OrchestrationResponse
	if err := json.Unmarshal(body, &respProv); err != nil {
		return err
	}

	collector.ReadOrchestrationLog(node, respProv.OrchestrationID)

	return nil
}

func (collector *Collector) ProvisionServiceV3(cluster, svc string) error {

	svcparts := strings.SplitN(svc, "/", 3)
	if len(svcparts) != 3 {
		return fmt.Errorf("invalid service format: %s, expected namespace/kind/name", svc)
	}

	ns := svcparts[0]
	kind := svcparts[1]
	svcname := svcparts[2]

	_, err := collector.handleObjectActionV3(ns, kind, svcname, "provision", nil)
	return err
}

func (collector *Collector) PurgeServiceV3(cluster, svc string) error {
	svcparts := strings.SplitN(svc, "/", 3)
	if len(svcparts) != 3 {
		return fmt.Errorf("invalid service format: %s, expected namespace/kind/name", svc)
	}

	ns := svcparts[0]
	kind := svcparts[1]
	svcname := svcparts[2]

	_, err := collector.handleObjectActionV3(ns, kind, svcname, "purge", nil)
	return err
}

func (collector *Collector) StartServiceV3(cluster, svc string) error {
	svcparts := strings.SplitN(svc, "/", 3)
	if len(svcparts) != 3 {
		return fmt.Errorf("invalid service format: %s, expected namespace/kind/name", svc)
	}

	ns := svcparts[0]
	kind := svcparts[1]
	svcname := svcparts[2]

	_, err := collector.handleObjectActionV3(ns, kind, svcname, "start", nil)
	return err
}

func (collector *Collector) StopServiceV3(cluster, svc string) error {

	svcparts := strings.SplitN(svc, "/", 3)
	if len(svcparts) != 3 {
		return fmt.Errorf("invalid service format: %s, expected namespace/kind/name", svc)
	}

	ns := svcparts[0]
	kind := svcparts[1]
	svcname := svcparts[2]

	_, err := collector.handleObjectActionV3(ns, kind, svcname, "stop", nil)
	return err
}

func (collector *Collector) RestartServiceV3(cluster, svc string) error {
	svcparts := strings.SplitN(svc, "/", 3)
	if len(svcparts) != 3 {
		return fmt.Errorf("invalid service format: %s, expected namespace/kind/name", svc)
	}

	ns := svcparts[0]
	kind := svcparts[1]
	svcname := svcparts[2]

	_, err := collector.handleObjectActionV3(ns, kind, svcname, "restart", nil)
	return err
}

func (collector *Collector) handleInstanceActionV3(node, namespace, kind, service, action string, params *InstanceActionParams) ([]byte, error) {
	var resp *http.Response
	var err error

	client, err := collector.GetClientV3()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(collector.ContextTimeoutSecond)*time.Second)
	defer cancel()

	oKind := apiv3.Kind(kind)
	switch strings.ToLower(action) {
	case "provision":
		var pvparam *apiv3.PostInstanceActionProvisionParams
		if params != nil {
			pvparam = params.ToProvisionParams()
		}
		resp, err = client.PostInstanceActionProvision(ctx, node, namespace, oKind, service, pvparam, collector.RequestCloserV3(), collector.RequestPrinterV3())
	case "unprovision":
		var uparam *apiv3.PostInstanceActionUnprovisionParams
		if params != nil {
			uparam = params.ToUnprovisionParams()
		}
		resp, err = client.PostInstanceActionUnprovision(ctx, node, namespace, oKind, service, uparam, collector.RequestCloserV3(), collector.RequestPrinterV3())
	case "start":
		var stparams *apiv3.PostInstanceActionStartParams
		if params != nil {
			stparams = params.ToStartParams()
		}
		resp, err = client.PostInstanceActionStart(ctx, node, namespace, oKind, service, stparams, collector.RequestCloserV3(), collector.RequestPrinterV3())
	case "stop":
		var spparams *apiv3.PostInstanceActionStopParams
		if params != nil {
			spparams = params.ToStopParams()
		}
		resp, err = client.PostInstanceActionStop(ctx, node, namespace, oKind, service, spparams, collector.RequestCloserV3(), collector.RequestPrinterV3())
	case "restart":
		var rtparams *apiv3.PostInstanceActionRestartParams
		if params != nil {
			rtparams = params.ToRestartParams()
		}
		resp, err = client.PostInstanceActionRestart(ctx, node, namespace, oKind, service, rtparams, collector.RequestCloserV3(), collector.RequestPrinterV3())
	default:
		return nil, fmt.Errorf("unsupported action: %s", action)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to perform action '%s' on %s/%s/%s: %w", action, namespace, kind, service, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if !handleSuccessGroup(resp.StatusCode) {
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, body)
	}

	return body, nil
}

func (collector *Collector) handleInstanceConsoleV3(node, namespace, kind, service, rid string) ([]byte, error) {
	var resp *http.Response
	var err error

	client, err := collector.GetClientV3()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(collector.ContextTimeoutSecond)*time.Second)
	defer cancel()

	oKind := apiv3.Kind(kind)
	oRid := apiv3.InQueryRid(rid)
	oTimeout := apiv3.InQueryGreetTimeout(fmt.Sprintf("%ds", collector.ContextTimeoutSecond*2))
	params := &apiv3.PostInstanceResourceConsoleParams{Rid: &oRid, GreetTimeout: &oTimeout}
	resp, err = client.PostInstanceResourceConsole(ctx, node, namespace, oKind, service, params, collector.RequestCloserV3(), collector.RequestPrinterV3())
	if err != nil {
		return nil, fmt.Errorf("failed to get console resource '%s' on %s/%s/%s: %w", rid, namespace, kind, service, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if !handleSuccessGroup(resp.StatusCode) {
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, body)
	}

	return []byte(resp.Header.Get("Location")), nil
}

func (collector *Collector) GetGottyServerV3(node, srv, rid string) (string, error) {
	svcparts := strings.SplitN(srv, "/", 3)
	if len(svcparts) != 3 {
		return "", fmt.Errorf("invalid service format: %s, expected namespace/kind/name", srv)
	}

	ns := svcparts[0]
	kind := svcparts[1]
	svcname := svcparts[2]
	body, err := collector.handleInstanceConsoleV3(node, ns, kind, svcname, rid)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

type LogMessage struct {
	Message string `json:"MESSAGE"`
}

func (collector *Collector) ReadOrchestrationLog(node, orchestration_id string) error {
	client, err := collector.GetClientV3()
	if err != nil {
		return err
	}

	filters := []string{"ORCHESTRATION_ID=" + orchestration_id}
	follow := true

	logclient := client.NewGetLogs(node)
	logclient.SetFilters(&filters)
	logclient.SetFollow(&follow)

	logChan, err := logclient.GetRaw()
	if err != nil {
		collector.Print(log.ErrorLevel, "Failed to open log channel for node %s orchestration %s: %v", node, orchestration_id, err)
		return err
	}

	for nodelog := range logChan {
		var logMsg LogMessage
		if err := json.Unmarshal(nodelog, &logMsg); err != nil {
			collector.Print(log.ErrorLevel, "Failed to unmarshal log message from node %s orchestration %s: %v", node, orchestration_id, err)
			continue
		}
		if strings.Contains(logMsg.Message, "orchestration is done") {
			collector.Print(log.InfoLevel, "%s", logMsg.Message)
			break
		} else if strings.Contains(logMsg.Message, "failed") {
			collector.Print(log.ErrorLevel, "%s", logMsg.Message)
		} else {
			collector.Print(log.DebugLevel, "%s", logMsg.Message)
		}
	}

	return nil
}

func handleSuccessGroup(statusCode int) bool {
	return statusCode >= 200 && statusCode < 300
}
