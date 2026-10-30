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
// The body of the authenticated calls is the one-time job body of dbjobs_new.sh (job_body):
// {"data": base64(AES-256-CBC(json)), "iv": <random, hex>, "key": "jobs"|"db"}, the AES key
// being the SHA-256 of the cluster's jobs key, else of the database password, the JSON
// carrying a timestamp, a random nonce, the server and, without a jobs key, the password as
// "secret" (cluster.OpenJobBody and SecretLoginCheck on the server): replication-manager
// accepts each body once.
package jobapi

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
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
	JobsKey string // the cluster's jobs key (JOBS_API_KEY), signs instead of the password when set
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
// PKCS#7 padded: what `openssl aes-256-cbc -a -nosalt -K sha256 -iv md5` produces (the
// legacy job body, replayable: kept for its tests and older peers).
func Encrypt(plain []byte, secret string) (string, error) {
	iv := md5.Sum([]byte(secret))
	return EncryptIV(plain, secret, iv[:])
}

// EncryptIV is Encrypt with the given 16-byte IV.
func EncryptIV(plain []byte, secret string, iv []byte) (string, error) {
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return base64.StdEncoding.EncodeToString(out), nil
}

func (c *Client) endpoint(suffix string) string {
	return strings.TrimRight(c.BaseURL, "/") + "/api/clusters/" + url.PathEscape(c.Cluster) + "/servers/" + url.PathEscape(c.Server) + "/" + url.PathEscape(c.Port) + suffix
}

// Name is how the job bodies and the stream headers name the server: host:port.
func (c *Client) Name() string {
	return c.Server + ":" + c.Port
}

// StreamSecret is the secret the bodies and the stream headers are signed with, and its
// name in the body: the jobs key, else the database password.
func (c *Client) StreamSecret() (string, string) {
	if c.JobsKey != "" {
		return c.JobsKey, "jobs"
	}
	return c.Secret, "db"
}

// Body is a one-time job body for now: random IV and nonce, timestamp.
func (c *Client) Body(now time.Time) ([]byte, error) {
	secret, keyname := c.StreamSecret()
	iv := make([]byte, 16)
	nonce := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	clear := map[string]interface{}{"ts": now.Unix(), "nonce": hex.EncodeToString(nonce), "server": c.Name()}
	if keyname == "db" {
		clear["secret"] = c.Secret
	}
	plain, _ := json.Marshal(clear)
	data, err := EncryptIV(plain, secret, iv)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"data": data, "iv": hex.EncodeToString(iv), "key": keyname})
}

// post sends the encrypted identity and returns the status and the trimmed body.
func (c *Client) post(suffix string) (int, string, error) {
	body, err := c.Body(time.Now())
	if err != nil {
		return 0, "", err
	}
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
	return c.ReceiverNamed(task, "")
}

// ReceiverNamed is Receiver for a task that receives one named file (the WAL archive).
func (c *Client) ReceiverNamed(task, name string) (string, error) {
	suffix := "/actions/receive-task/" + url.PathEscape(task)
	if name != "" {
		suffix += "?name=" + url.QueryEscape(name)
	}
	code, body, err := c.post(suffix)
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
	if strings.HasPrefix(body, "LISTEN=") {
		// "LISTEN=<port> TARGET=<host:port>": the task receives, the script parses
		return body, nil
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
