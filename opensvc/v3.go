package opensvc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	clientv3 "github.com/opensvc/om3/v3/core/client"
	apiv3 "github.com/opensvc/om3/v3/daemon/api"
	"github.com/opensvc/om3/v3/util/funcopt"
	"github.com/signal18/replication-manager/config"
	sharedlog "github.com/signal18/replication-manager/utils/s18log/shared"

	"github.com/tidwall/gjson"
)

type StatusError struct {
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("unexpected status code: %d, body: %s", e.StatusCode, e.Body)
}

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

func (collector *Collector) GetAuthInfoV3() error {
	client, err := collector.GetClientV3()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(collector.ContextTimeoutSecond)*time.Second)
	defer cancel()

	// Use the client to check the API version
	resp, err := client.GetAuthInfo(ctx, collector.RequestCloserV3())
	if err != nil {
		return fmt.Errorf("failed to check API version: %w", err)
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.LogModulePrintf(config.ConstLogModOrchestrator, config.LvlDbg, "OpenSVC v3 auth info status=%d body=%q", resp.StatusCode, string(body))
	}

	if !handleSuccessGroup(resp.StatusCode) {
		return &StatusError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}

	// Set the API version in the collector if successful
	collector.SetV3()

	return nil
}

func (collector *Collector) GetNodesV3() ([]Host, error) {
	client, err := collector.GetClientV3()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(collector.ContextTimeoutSecond)*time.Second)
	defer cancel()

	// Use the client to get the nodes
	resp, err := client.GetNodes(ctx, nil, collector.RequestCloserV3())
	if err != nil {
		return nil, err
	}
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

	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if !handleSuccessGroup(resp.StatusCode) {
		return nil, &StatusError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}

	var hosts []Host
	// Process the response to extract node information
	nodes := gjson.GetBytes(body, "items.#.meta.node").Array()
	for _, node := range nodes {
		h := Host{
			Node_name: node.String(),
		}

		hosts = append(hosts, h)
	}

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
	resp, err = client.PostObjectConfigFileWithBody(ctx, namespace, oKind, service, "application/octet-stream", bytes.NewReader(data), collector.RequestCloserV3())
	if err != nil {
		return nil, fmt.Errorf("failed to create object in %s/%s/%s: %w", namespace, kind, service, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if !handleSuccessGroup(resp.StatusCode) {
		return nil, &StatusError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}

	return body, nil
}

type ObjectGetterFunc func([]byte) ([]byte, error)

func (collector *Collector) GetObjectV3(namespace, kind, service string, getFunc ObjectGetterFunc) ([]byte, error) {
	var resp *http.Response
	var err error

	client, err := collector.GetClientV3()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(collector.ContextTimeoutSecond)*time.Second)
	defer cancel()

	oKind := apiv3.Kind(kind)

	resp, err = client.GetObject(ctx, namespace, oKind, service, collector.RequestCloserV3())
	if err != nil {
		return nil, fmt.Errorf("failed to get object path in %s/%s/%s: %w", namespace, kind, service, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.LogModulePrintf(config.ConstLogModOrchestrator, config.LvlDbg, "OpenSVC v3 get object status=%d path=%s/%s/%s body=%q", resp.StatusCode, namespace, kind, service, string(body))
	}

	if !handleSuccessGroup(resp.StatusCode) {
		return nil, &StatusError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
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

	data, err := collector.GetObjectV3(namespace, kind, service, getfunc)
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
		resp, err = client.GetObjectDataKey(ctx, namespace, oKind, service, &apiv3.GetObjectDataKeyParams{Name: key}, collector.RequestCloserV3())
	case "create":
		resp, err = client.PostObjectDataKeyWithBody(ctx, namespace, oKind, service, &apiv3.PostObjectDataKeyParams{Name: key}, "application/octet-stream", vReader, collector.RequestCloserV3())
	case "update":
		resp, err = client.PutObjectDataKeyWithBody(ctx, namespace, oKind, service, &apiv3.PutObjectDataKeyParams{Name: key}, "application/octet-stream", vReader, collector.RequestCloserV3())
	case "delete":
		resp, err = client.DeleteObjectDataKey(ctx, namespace, oKind, service, &apiv3.DeleteObjectDataKeyParams{Name: key}, collector.RequestCloserV3())
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
		return nil, &StatusError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}

	return body, nil
}

func (collector *Collector) GetSecretKeyValueV3(namespace, service, key string) ([]byte, error) {
	return collector.handleObjectKeyValueV3("get", namespace, "sec", service, key, "")
}

func (collector *Collector) CreateSecretKeyValueV3(namespace, service, key, value string) error {
	_, err := collector.handleObjectKeyValueV3("create", namespace, "sec", service, key, value)
	var se *StatusError
	if err != nil && errors.As(err, &se) && se.StatusCode == 409 {
		return collector.UpdateSecretKeyValueV3(namespace, service, key, value)
	}
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
	var se *StatusError
	if err != nil && errors.As(err, &se) && se.StatusCode == 409 {
		return collector.UpdateConfigKeyValueV3(namespace, service, key, value)
	}
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
		resp, err = client.PostObjectActionProvision(ctx, namespace, oKind, service, collector.RequestCloserV3())
	case "unprovision":
		resp, err = client.PostObjectActionUnprovision(ctx, namespace, oKind, service, collector.RequestCloserV3())
	case "abort":
		resp, err = client.PostObjectActionAbort(ctx, namespace, oKind, service, collector.RequestCloserV3())
	case "start":
		resp, err = client.PostObjectActionStart(ctx, namespace, oKind, service, collector.RequestCloserV3())
	case "stop":
		resp, err = client.PostObjectActionStop(ctx, namespace, oKind, service, collector.RequestCloserV3())
	case "restart":
		resp, err = client.PostObjectActionRestartWithBody(ctx, namespace, oKind, service, "application/json", bytes.NewReader(data), collector.RequestCloserV3())
	case "purge":
		resp, err = client.PostObjectActionPurge(ctx, namespace, oKind, service, collector.RequestCloserV3())
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
		return nil, &StatusError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}

	return body, nil
}

func (collector *Collector) CreateTemplateV3(cluster string, svc string, node string, template []byte) ([]byte, error) {

	svcparts := strings.SplitN(svc, "/", 3)
	if len(svcparts) != 3 {
		return nil, fmt.Errorf("invalid service format: %s, expected namespace/kind/name", svc)
	}

	ns := svcparts[0]
	kind := svcparts[1]
	svcname := svcparts[2]

	resp, err := collector.CreateObjectV3(ns, kind, svcname, template)
	if err != nil {
		return nil, err
	}

	return resp, nil
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

func (collector *Collector) AbortServiceV3(cluster, svc string) error {
	svcparts := strings.SplitN(svc, "/", 3)
	if len(svcparts) != 3 {
		return fmt.Errorf("invalid service format: %s, expected namespace/kind/name", svc)
	}

	ns := svcparts[0]
	kind := svcparts[1]
	svcname := svcparts[2]

	_, err := collector.handleObjectActionV3(ns, kind, svcname, "abort", nil)
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
		resp, err = client.PostInstanceActionProvision(ctx, node, namespace, oKind, service, pvparam, collector.RequestCloserV3())
	case "unprovision":
		var uparam *apiv3.PostInstanceActionUnprovisionParams
		if params != nil {
			uparam = params.ToUnprovisionParams()
		}
		resp, err = client.PostInstanceActionUnprovision(ctx, node, namespace, oKind, service, uparam, collector.RequestCloserV3())
	case "start":
		var stparams *apiv3.PostInstanceActionStartParams
		if params != nil {
			stparams = params.ToStartParams()
		}
		resp, err = client.PostInstanceActionStart(ctx, node, namespace, oKind, service, stparams, collector.RequestCloserV3())
	case "stop":
		var spparams *apiv3.PostInstanceActionStopParams
		if params != nil {
			spparams = params.ToStopParams()
		}
		resp, err = client.PostInstanceActionStop(ctx, node, namespace, oKind, service, spparams, collector.RequestCloserV3())
	case "restart":
		var rtparams *apiv3.PostInstanceActionRestartParams
		if params != nil {
			rtparams = params.ToRestartParams()
		}
		resp, err = client.PostInstanceActionRestart(ctx, node, namespace, oKind, service, rtparams, collector.RequestCloserV3())
	case "run":
		var rpparams *apiv3.PostInstanceActionRunParams
		if params != nil {
			rpparams = params.ToRunParams()
		}
		resp, err = client.PostInstanceActionRun(ctx, node, namespace, oKind, service, rpparams, collector.RequestCloserV3())
	case "clear":
		resp, err = client.PostInstanceClear(ctx, node, namespace, oKind, service, collector.RequestCloserV3())
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
		return nil, &StatusError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}

	return body, nil
}

func (collector *Collector) ClearInstanceV3(node, svc string) error {
	svcparts := strings.SplitN(svc, "/", 3)
	if len(svcparts) != 3 {
		return fmt.Errorf("invalid service format: %s, expected namespace/kind/name", svc)
	}

	ns := svcparts[0]
	kind := svcparts[1]
	svcname := svcparts[2]

	_, err := collector.handleInstanceActionV3(node, ns, kind, svcname, "clear", nil)
	return err
}

func (collector *Collector) RunTaskV3(srv string, node string, task string, env string) error {
	svcparts := strings.SplitN(srv, "/", 3)
	if len(svcparts) != 3 {
		return fmt.Errorf("invalid service format: %s, expected namespace/kind/name", srv)
	}

	ns := svcparts[0]
	kind := svcparts[1]
	svcname := svcparts[2]

	if collector.isLoggable(config.ConstLogModOrchestrator, config.LvlDbg) {
		collector.Logrus.WithField("FROM", "OpenSVC").Printf("RunTask V3: node=%s svc=%s task=%s env=%s", node, srv, task, env)
	}

	rid := apiv3.InQueryRid(task)
	params := &InstanceActionParams{Rid: &rid}
	if env != "" {
		envs := apiv3.InQueryEnvs([]string{env})
		params.Env = &envs
	}

	_, err := collector.handleInstanceActionV3(node, ns, kind, svcname, "run", params)
	return err
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
	resp, err = client.PostInstanceResourceConsole(ctx, node, namespace, oKind, service, params, collector.RequestCloserV3())
	if err != nil {
		return nil, fmt.Errorf("failed to get console resource '%s' on %s/%s/%s: %w", rid, namespace, kind, service, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if !handleSuccessGroup(resp.StatusCode) {
		return nil, &StatusError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
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

func (collector *Collector) handleEventLogsV3(agents string, kindCloser []string, filters []string) error {
	client, err := collector.GetClientV3()
	if err != nil {
		return err
	}

	nodes := strings.Split(agents, ",")
	var wg *sync.WaitGroup = &sync.WaitGroup{}
	for _, node := range nodes {
		wg.Add(1)
		go collector.ReadNodeEventChannel(wg, client, node, filters, kindCloser...)
	}
	wg.Wait()
	return nil
}

type EventData struct {
	ID string `json:"id"`
}

func (collector *Collector) ReadNodeEventChannel(wg *sync.WaitGroup, client *clientv3.T, node string, filters []string, kindClosers ...string) {
	defer wg.Done()
	if client == nil {
		return
	}

	evclient := client.NewGetEvents()
	evclient.SetNodename(node)
	evclient.SetFilter(filters...)

	cev, err := evclient.Do()
	if err != nil {
		collector.logToCluster(config.ConstLogModOrchestrator, config.LvlErr, fmt.Sprintf("Failed to open event channel for node %s: %v", node, err))
		return
	}

	for {
		select {
		case e, ok := <-cev:
			if !ok {
				return
			}

			msg := sharedlog.Message{
				Group:     "none",
				Level:     "DEBUG",
				Timestamp: e.At.Format("2006/01/02 15:04:05"),
				Text:      string(e.Data),
				Module:    config.ConstLogModOrchestrator,
			}

			if slices.Contains(kindClosers, e.Kind) {
				msg.Level = "INFO"
			}

			if collector.MessageChan != nil {
				select {
				case collector.MessageChan <- msg:
				default:
				}
			}
		}
	}
}

func handleSuccessGroup(statusCode int) bool {
	return statusCode >= 200 && statusCode < 300
}
