// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Package proxmox is a minimal client of the Proxmox VE REST API (/api2/json): the calls the
// proxmox orchestrator needs to drive the virtual machine under a database server (status,
// start, shutdown, reboot, task status, nodes, version). Authentication is an API token
// (user@realm!tokenid=secret), sent in the Authorization header and never in an error or a
// log line (F10). Every request is bounded by the client's timeout (F2).
//
// The Proxmox Backup Server client of the archive plugin (cluster/archiveplugin/plugins/pbs)
// talks to another product (PBS, its own API and ticket scheme) and is internal to that
// plugin, so it is not reused here.
package proxmox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout bounds one API request when the caller gives none.
const DefaultTimeout = 30 * time.Second

// Client is a Proxmox VE API client.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient builds a client of the API at apiURL (https://host:8006, with or without
// /api2/json). token is user@realm!tokenid=secret. insecure accepts a certificate without
// verification (a lab with the self-signed certificate of a fresh install).
func NewClient(apiURL string, token string, insecure bool, timeout time.Duration) (*Client, error) {
	apiURL = strings.TrimRight(strings.TrimSpace(apiURL), "/")
	if apiURL == "" {
		return nil, errors.New("proxmox-api-url is empty")
	}
	u, err := url.Parse(apiURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		// the URL is not a secret, but the parse error is enough: no echo of the value
		return nil, errors.New("proxmox-api-url is not a URL (https://host:8006)")
	}
	if !strings.HasSuffix(apiURL, "/api2/json") {
		apiURL += "/api2/json"
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("proxmox-api-token is empty")
	}
	if !strings.Contains(token, "!") || !strings.Contains(token, "=") {
		return nil, errors.New("proxmox-api-token must be user@realm!tokenid=secret")
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- proxmox-api-insecure, a lab only
	}
	return &Client{
		baseURL: apiURL,
		token:   token,
		http:    &http.Client{Timeout: timeout, Transport: transport},
	}, nil
}

// VMStatus is what /nodes/{node}/qemu/{vmid}/status/current answers.
type VMStatus struct {
	Status    string `json:"status"`    // running, stopped
	QMPStatus string `json:"qmpstatus"` // running, paused, stopped, prelaunch...
	Uptime    int64  `json:"uptime"`    // seconds
	Name      string `json:"name"`
}

// TaskStatus is what /nodes/{node}/tasks/{upid}/status answers.
type TaskStatus struct {
	Status     string `json:"status"`     // running, stopped
	ExitStatus string `json:"exitstatus"` // OK, or the error once stopped
}

// Done tells whether the task has ended.
func (t TaskStatus) Done() bool { return t.Status == "stopped" }

// OK tells whether the task ended without error.
func (t TaskStatus) OK() bool { return t.Done() && t.ExitStatus == "OK" }

// Node is one entry of /nodes.
type Node struct {
	Node   string  `json:"node"`
	Status string  `json:"status"` // online, offline
	CPU    float64 `json:"cpu"`
	MaxCPU int     `json:"maxcpu"`
	Mem    int64   `json:"mem"`
	MaxMem int64   `json:"maxmem"`
	Uptime int64   `json:"uptime"`
}

// APIError is an answer of the API with a status other than 2xx. Its message is the
// status and the API's own message, never the request's headers.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("proxmox API answered %d: %s", e.StatusCode, e.Message)
}

// Version answers /version: the API answers and accepts the token.
func (c *Client) Version(ctx context.Context) (string, error) {
	var out struct {
		Version string `json:"version"`
		Release string `json:"release"`
	}
	if err := c.do(ctx, http.MethodGet, "/version", &out); err != nil {
		return "", err
	}
	return out.Version, nil
}

// Nodes lists the cluster's nodes.
func (c *Client) Nodes(ctx context.Context) ([]Node, error) {
	var out []Node
	err := c.do(ctx, http.MethodGet, "/nodes", &out)
	return out, err
}

// VMStatus is the current status of a VM.
func (c *Client) VMStatus(ctx context.Context, node string, vmid string) (VMStatus, error) {
	var out VMStatus
	err := c.do(ctx, http.MethodGet, vmPath(node, vmid)+"/status/current", &out)
	return out, err
}

// StartVM starts a VM and returns the task id (UPID).
func (c *Client) StartVM(ctx context.Context, node string, vmid string) (string, error) {
	return c.task(ctx, vmPath(node, vmid)+"/status/start")
}

// ShutdownVM asks the guest to shut down (ACPI, or the guest agent) and returns the task id.
// The hard stop (/status/stop) is never used: the guest holds a database.
func (c *Client) ShutdownVM(ctx context.Context, node string, vmid string) (string, error) {
	return c.task(ctx, vmPath(node, vmid)+"/status/shutdown")
}

// RebootVM asks the guest to reboot (a clean shutdown, then a start) and returns the task id.
func (c *Client) RebootVM(ctx context.Context, node string, vmid string) (string, error) {
	return c.task(ctx, vmPath(node, vmid)+"/status/reboot")
}

// TaskStatus is the status of a task started on node.
func (c *Client) TaskStatus(ctx context.Context, node string, upid string) (TaskStatus, error) {
	var out TaskStatus
	err := c.do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/tasks/"+url.PathEscape(upid)+"/status", &out)
	return out, err
}

// WaitTask polls the task every interval until it ends or ctx is done. A task that ends
// with an exit status other than OK is an error.
func (c *Client) WaitTask(ctx context.Context, node string, upid string, interval time.Duration) error {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	for {
		st, err := c.TaskStatus(ctx, node, upid)
		if err == nil && st.Done() {
			if st.OK() {
				return nil
			}
			return fmt.Errorf("proxmox task %s ended: %s", upid, st.ExitStatus)
		}
		select {
		case <-ctx.Done():
			if err != nil {
				return fmt.Errorf("proxmox task %s: %w (last error: %s)", upid, ctx.Err(), err)
			}
			return fmt.Errorf("proxmox task %s: %w", upid, ctx.Err())
		case <-time.After(interval):
		}
	}
}

func vmPath(node, vmid string) string {
	return "/nodes/" + url.PathEscape(node) + "/qemu/" + url.PathEscape(vmid)
}

// task posts an action that answers a task id.
func (c *Client) task(ctx context.Context, path string) (string, error) {
	var upid string
	if err := c.do(ctx, http.MethodPost, path, &upid); err != nil {
		return "", err
	}
	return upid, nil
}

// do sends one request and decodes the "data" member of the answer into out.
func (c *Client) do(ctx context.Context, method, path string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "PVEAPIToken="+c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// *url.Error names the method and the URL, never the headers
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(resp.Status)
		var e struct {
			Message string          `json:"message"`
			Errors  json.RawMessage `json:"errors"`
		}
		if json.Unmarshal(body, &e) == nil && e.Message != "" {
			msg = strings.TrimSpace(e.Message)
		}
		return &APIError{StatusCode: resp.StatusCode, Message: msg}
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("proxmox API %s %s: invalid answer: %w", method, path, err)
	}
	if out == nil || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("proxmox API %s %s: invalid data: %w", method, path, err)
	}
	return nil
}
