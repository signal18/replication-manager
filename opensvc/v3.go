package opensvc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	clientv3 "github.com/opensvc/om3/core/client"
	apiv3 "github.com/opensvc/om3/daemon/api"
	"github.com/signal18/replication-manager/utils/s18log"

	"github.com/tidwall/gjson"
)

func (collector *Collector) IsV3() bool {
	return collector.ClusterApiVersion == "v3"
}

func (collector *Collector) SetV3() {
	collector.ClusterApiVersion = "v3"
}

func (collector *Collector) GetServerURLV3() string {
	return fmt.Sprintf("http://%s:%d", collector.Host, collector.Port)
}

func (collector *Collector) GetClientV3() (*clientv3.T, error) {
	client, err := clientv3.New(
		clientv3.WithURL(collector.GetServerURLV3()),
		clientv3.WithUsername(collector.User),
		clientv3.WithPassword(collector.Pass),
		clientv3.WithTimeout(time.Duration(collector.ContextTimeoutSecond)*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	// client, err := apiv3.NewClient(collector.GetServerURLV3(), apiv3.WithHTTPClient(collector.GetHttpClient()))
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to create API client: %w", err)
	// }

	return client, nil
}

func (collector *Collector) GetAuthInfoV3() error {
	client, err := collector.GetClientV3()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(collector.ContextTimeoutSecond)*time.Second)
	defer cancel()

	// Use the client to check the API version
	resp, err := client.GetAuthInfo(ctx)
	if err != nil {
		return fmt.Errorf("failed to check API version: %w", err)
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, body)
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
	resp, err := client.GetNodes(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, body)
	}

	var hosts []Host
	// Process the response to extract node information
	nodes := gjson.GetBytes(body, "items.{nodename:#.meta.node}").Array()
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
	resp, err = client.PostObjectConfigFileWithBody(ctx, namespace, oKind, service, "application/octet-stream", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create object in %s/%s/%s: %w", namespace, kind, service, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, body)
	}

	return body, nil
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
		resp, err = client.GetObjectDataKey(ctx, namespace, oKind, service, &apiv3.GetObjectDataKeyParams{Name: key})
	case "create":
		resp, err = client.PostObjectDataKeyWithBody(ctx, namespace, oKind, service, &apiv3.PostObjectDataKeyParams{Name: key}, "application/octet-stream", vReader)
	case "update":
		resp, err = client.PutObjectDataKeyWithBody(ctx, namespace, oKind, service, &apiv3.PutObjectDataKeyParams{Name: key}, "application/octet-stream", vReader)
	case "delete":
		resp, err = client.DeleteObjectDataKey(ctx, namespace, oKind, service, &apiv3.DeleteObjectDataKeyParams{Name: key})
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

	if resp.StatusCode != 200 {
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
		resp, err = client.PostObjectActionProvision(ctx, namespace, oKind, service)
	case "unprovision":
		resp, err = client.PostObjectActionUnprovision(ctx, namespace, oKind, service)
	case "start":
		resp, err = client.PostObjectActionStart(ctx, namespace, oKind, service)
	case "stop":
		resp, err = client.PostObjectActionStop(ctx, namespace, oKind, service)
	case "restart":
		resp, err = client.PostObjectActionRestartWithBody(ctx, namespace, oKind, service, "application/json", bytes.NewReader(data))
	case "purge":
		resp, err = client.PostObjectActionPurge(ctx, namespace, oKind, service)
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

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, body)
	}

	return body, nil
}

func (collector *Collector) CreateTemplateV3(cluster string, svc string, node string, template []byte) error {

	svcparts := strings.SplitN(svc, "/", 3)
	if len(svcparts) != 3 {
		return fmt.Errorf("invalid service format: %s, expected namespace/kind/name", svc)
	}

	ns := svcparts[0]
	kind := svcparts[1]
	svcname := svcparts[2]

	_, err := collector.CreateObjectV3(ns, kind, svcname, template)
	if err != nil {
		return err
	}

	_, err = collector.handleObjectActionV3(ns, kind, svcname, "provision", nil)
	return err
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
		resp, err = client.PostInstanceActionProvision(ctx, node, namespace, oKind, service, pvparam)
	case "unprovision":
		var uparam *apiv3.PostInstanceActionUnprovisionParams
		if params != nil {
			uparam = params.ToUnprovisionParams()
		}
		resp, err = client.PostInstanceActionUnprovision(ctx, node, namespace, oKind, service, uparam)
	case "start":
		var stparams *apiv3.PostInstanceActionStartParams
		if params != nil {
			stparams = params.ToStartParams()
		}
		resp, err = client.PostInstanceActionStart(ctx, node, namespace, oKind, service, stparams)
	case "stop":
		var spparams *apiv3.PostInstanceActionStopParams
		if params != nil {
			spparams = params.ToStopParams()
		}
		resp, err = client.PostInstanceActionStop(ctx, node, namespace, oKind, service, spparams)
	case "restart":
		var rtparams *apiv3.PostInstanceActionRestartParams
		if params != nil {
			rtparams = params.ToRestartParams()
		}
		resp, err = client.PostInstanceActionRestart(ctx, node, namespace, oKind, service, rtparams)
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

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, body)
	}

	return body, nil
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
		collector.MessageChan <- s18log.HttpMessage{
			Level:     "ERROR",
			Timestamp: time.Now().Format("2006/01/02 15:04:05"),
			Text:      fmt.Sprintf("Failed to open event channel for node %s: %v", node, err),
		}
		return
	}

	for {
		select {
		case e, ok := <-cev:
			if !ok {
				return
			}

			msg := s18log.HttpMessage{
				Level:     "DEBUG",
				Timestamp: e.At.Format("2006/01/02 15:04:05"),
				Text:      string(e.Data),
			}

			if slices.Contains(kindClosers, e.Kind) {
				msg.Level = "INFO"
			}

			collector.MessageChan <- msg
		}
	}
}
