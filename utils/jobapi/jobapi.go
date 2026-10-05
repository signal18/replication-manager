// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

// Package jobapi is the client side of the API jobs mode (scheduler-jobs-mode = api): the
// three calls a jobs script makes to replication-manager -- is a task wanted, open a
// receiver for it, report its state. dbjobs_new.sh does them with openssl and socat;
// `replication-manager-cli job` does them with this package, for images that ship
// neither (PostgreSQL).
//
// The body of the authenticated calls is {"data": base64(AES-256-CBC(json))}, the key
// being the SHA-256 and the IV the MD5 of the database password, the JSON carrying the
// server and that password again as "secret" (cluster.SecretLoginCheck on the server).
package jobapi

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client addresses one monitored server of one cluster.
type Client struct {
	BaseURL string // https://repman:10005
	Cluster string
	Server  string // host as replication-manager monitors it
	Port    string
	Secret  string // the database password
	HTTP    *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	// replication-manager serves its API with its own certificate, like the jobs scripts
	// (wget --no-check-certificate, socat verify=0) the certificate is not checked
	return &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
}

// Encrypt returns base64(AES-256-CBC(plain)) with the key and IV derived from secret,
// PKCS#7 padded: what `openssl aes-256-cbc -a -nosalt -K sha256 -iv md5` produces.
func Encrypt(plain []byte, secret string) (string, error) {
	key := sha256.Sum256([]byte(secret))
	iv := md5.Sum([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv[:]).CryptBlocks(out, padded)
	return base64.StdEncoding.EncodeToString(out), nil
}

func (c *Client) endpoint(suffix string) string {
	return strings.TrimRight(c.BaseURL, "/") + "/api/clusters/" + url.PathEscape(c.Cluster) + "/servers/" + url.PathEscape(c.Server) + "/" + url.PathEscape(c.Port) + suffix
}

// post sends the encrypted identity and returns the status and the trimmed body.
func (c *Client) post(suffix string) (int, string, error) {
	plain, _ := json.Marshal(map[string]string{"server": c.Server + ":" + c.Port, "secret": c.Secret})
	data, err := Encrypt(plain, c.Secret)
	if err != nil {
		return 0, "", err
	}
	body, _ := json.Marshal(map[string]string{"data": data})
	resp, err := c.http().Post(c.endpoint(suffix), "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	return resp.StatusCode, strings.TrimSpace(string(b)), nil
}

// Needs tells whether replication-manager wants the task run now. Asking consumes the
// request: a true answer is given once.
func (c *Client) Needs(task string) (bool, error) {
	code, body, err := c.post("/needs/" + url.PathEscape(task))
	if err != nil {
		return false, err
	}
	switch {
	case code == http.StatusOK && body == "true":
		return true, nil
	case body == "false":
		return false, nil
	}
	return false, fmt.Errorf("needs %s: http %d %s", task, code, body)
}

// Receiver asks replication-manager to open a receiver for the task and returns its
// address host:port ("" when the task streams nothing).
func (c *Client) Receiver(task string) (string, error) {
	code, body, err := c.post("/actions/receive-task/" + url.PathEscape(task))
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("receiver for %s: http %d %s", task, code, body)
	}
	if body == "NO_RECEIVER_NEEDED" {
		return "", nil
	}
	if target := strings.TrimPrefix(body, "TARGET="); target != body {
		// not a receiver: the address the task works against (the primary to follow)
		return target, nil
	}
	port := strings.TrimPrefix(body, "RECEIVER_PORT=")
	if port == body || port == "" {
		return "", fmt.Errorf("receiver for %s: unexpected answer %q", task, body)
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("receiver for %s: no host in %q", task, c.BaseURL)
	}
	host := u.Hostname()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return host + ":" + port, nil
}

// Login exchanges the encrypted identity for the token of the calls that need one
// (secret-login, the jobs scripts' login).
func (c *Client) Login() (string, error) {
	code, body, err := c.post("/secret-login")
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("login: http %d %s", code, body)
	}
	var answer struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil || answer.Token == "" {
		return "", fmt.Errorf("login: no token in the answer")
	}
	return answer.Token, nil
}

// ReportUsage posts the service's resource usage of one window (the dbu route: memory,
// cpu, io, disk and network read from the service cgroup by the jobs script).
func (c *Client) ReportUsage(report []byte) error {
	if !json.Valid(report) {
		return fmt.Errorf("usage report is not JSON")
	}
	token, err := c.Login()
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.endpoint("/dbu"), bytes.NewReader(report))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<12))
		return fmt.Errorf("usage report: http %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// State reports the task state: processing, done, error or waiting.
func (c *Client) State(task, state string) error {
	code, body, err := c.post("/actions/job-state/" + url.PathEscape(task) + "/" + url.PathEscape(state))
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("state %s of %s: http %d %s", state, task, code, body)
	}
	return nil
}
