// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

// Package jobapi is the client side of the jobs' calls to replication-manager: is a task
// wanted, open a receiver for it, report its state, send log lines, the jobs check and
// upgrade, the configuration. The jobs scripts (dbjobs_new.sh, postgres_job.sh) make every
// one of them through `replication-manager-cli job`, which uses this package: there is no
// other implementation of the protocol on the jobs' side.
//
// The body of the authenticated calls is the one-time job body:
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
	"net"
	"net/http"
	"net/url"
	"strconv"
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
	// Version names the client in the User-Agent of every call ("replication-manager-cli/<version>"),
	// so replication-manager can show the CLI a host's jobs run with
	Version string
	HTTP    *http.Client
}

// UserAgentPrefix starts the User-Agent of the jobs' calls; the version follows it.
const UserAgentPrefix = "replication-manager-cli/"

// VersionFromUserAgent is the CLI version a jobs call names, "" for another client.
func VersionFromUserAgent(ua string) string {
	v, ok := strings.CutPrefix(ua, UserAgentPrefix)
	if !ok {
		return ""
	}
	if len(v) > 128 {
		v = v[:128]
	}
	return v
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
	return c.BodyWith(now, nil)
}

// BodyWith is Body carrying the members of extra in the encrypted JSON as well (the log
// lines of write-log, the restore metadata of job-state).
func (c *Client) BodyWith(now time.Time, extra map[string]interface{}) ([]byte, error) {
	secret, keyname := c.StreamSecret()
	iv := make([]byte, 16)
	nonce := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	clear := map[string]interface{}{}
	for k, v := range extra {
		clear[k] = v
	}
	clear["ts"], clear["nonce"], clear["server"] = now.Unix(), hex.EncodeToString(nonce), c.Name()
	if keyname == "db" {
		clear["secret"] = c.Secret
	}
	return SealBody(clear, secret, keyname, iv)
}

// SealBody encrypts the clear JSON of a job body with secret under iv (16 bytes) and returns
// the body {"data","iv","key": keyname}. BodyWith fills clear; tests seal their own.
func SealBody(clear map[string]interface{}, secret, keyname string, iv []byte) ([]byte, error) {
	plain, err := json.Marshal(clear)
	if err != nil {
		return nil, err
	}
	data, err := EncryptIV(plain, secret, iv)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"data": data, "iv": hex.EncodeToString(iv), "key": keyname})
}

// post sends the encrypted identity and returns the status and the trimmed body.
func (c *Client) post(suffix string) (int, string, error) {
	return c.postWith(suffix, nil)
}

// postWith is post with extra members in the encrypted body (BodyWith).
func (c *Client) postWith(suffix string, extra map[string]interface{}) (int, string, error) {
	body, err := c.BodyWith(time.Now(), extra)
	if err != nil {
		return 0, "", err
	}
	return c.do(http.MethodPost, c.endpoint(suffix), body, "")
}

// do sends one request and returns the status and the trimmed answer (64 KiB at most).
func (c *Client) do(method, url string, body []byte, token string) (int, string, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		return 0, "", err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if c.Version != "" {
		req.Header.Set("User-Agent", UserAgentPrefix+c.Version)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	return resp.StatusCode, strings.TrimSpace(string(b)), nil
}

// authed logs in (Login) and sends one request to the server's route with the token.
func (c *Client) authed(method, suffix string, body []byte) (int, string, error) {
	token, err := c.Login()
	if err != nil {
		return 0, "", err
	}
	return c.do(method, c.endpoint(suffix), body, token)
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
	// "RECEIVER_PORT=<port>", for mysqldump followed by " DUMP_ARGS=<base64>[ LOCK_DDL=1]":
	// host:port, then the rest of the answer as it came
	port, rest, _ := strings.Cut(body, " ")
	addr, err := c.receiverAddress(strings.TrimPrefix(port, "RECEIVER_PORT="), port)
	if err != nil {
		return "", fmt.Errorf("receiver for %s: %w", task, err)
	}
	if rest = strings.TrimSpace(rest); rest != "" {
		addr += " " + rest
	}
	return addr, nil
}

// receiverAddress is host:port of a receiver replication-manager opened: the host of BaseURL,
// the port of the answer field "RECEIVER_PORT=<port>" (field is the whole field, for errors).
func (c *Client) receiverAddress(port, field string) (string, error) {
	if port == field || port == "" {
		return "", fmt.Errorf("unexpected answer %q", field)
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return "", fmt.Errorf("unexpected port in %q", field)
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("no host in %q", c.BaseURL)
	}
	return net.JoinHostPort(u.Hostname(), port), nil
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
	code, body, err := c.authed(http.MethodPost, "/dbu", report)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("usage report: http %d %s", code, body)
	}
	return nil
}

// State reports the task state: processing, done, error or waiting.
func (c *Client) State(task, state string) error {
	return c.StateWith(task, state, nil)
}

// StateWith is State with the restore metadata of a physical reseed or flashback (the
// "restore" member of the job-state body), nil for none.
func (c *Client) StateWith(task, state string, restore json.RawMessage) error {
	var extra map[string]interface{}
	if len(restore) > 0 {
		if !json.Valid(restore) {
			return fmt.Errorf("state %s of %s: the restore metadata is not JSON", state, task)
		}
		extra = map[string]interface{}{"restore": restore}
	}
	code, body, err := c.postWith("/actions/job-state/"+url.PathEscape(task)+"/"+url.PathEscape(state), extra)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("state %s of %s: http %d %s", state, task, code, body)
	}
	return nil
}

// maxConfigSize bounds the config archive the init container downloads (F3): the generated
// configuration of one server is a few hundred kilobytes.
const maxConfigSize = 64 << 20

// ErrNoConfigFetch: replication-manager does not want this server to fetch its config now (an
// ignored server, or one that keeps its files: need-config-fetch answered no).
var ErrNoConfigFetch = fmt.Errorf("no config fetch needed")

// Config writes the server's generated configuration archive (config.tar.gz) to w: the init
// container's fetch, done with the jobs login instead of the admin password. It asks
// need-config-fetch first (ErrNoConfigFetch when not wanted), logs in with the one-time job body
// (a token scoped to this server), then downloads <server>/<port>/config with it.
func (c *Client) Config(w io.Writer) error {
	code, _, err := c.do(http.MethodGet, c.endpoint("/need-config-fetch"), nil, "")
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return ErrNoConfigFetch
	}
	token, err := c.Login()
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodGet, c.endpoint("/config"), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if c.Version != "" {
		req.Header.Set("User-Agent", UserAgentPrefix+c.Version)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<12))
		return fmt.Errorf("config: http %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, maxConfigSize+1))
	if err != nil {
		return err
	}
	if n > maxConfigSize {
		return fmt.Errorf("config: larger than %d bytes", maxConfigSize)
	}
	return nil
}

// MaxLogSize bounds the text one write-log call carries (F4): the jobs send their log a few
// lines at a time.
const MaxLogSize = 1 << 20

// LogLevel tells whether replication-manager prints the task's lines at that level (the
// jobs-log-level route of the cluster): the jobs send only those.
func (c *Client) LogLevel(task, level string) (bool, error) {
	u := strings.TrimRight(c.BaseURL, "/") + "/api/clusters/" + url.PathEscape(c.Cluster) + "/jobs-log-level/" + url.PathEscape(task) + "/" + url.PathEscape(level)
	code, body, err := c.do(http.MethodGet, u, nil, "")
	if err != nil {
		return false, err
	}
	switch {
	case code == http.StatusOK && body == "true":
		return true, nil
	case body == "false":
		return false, nil
	}
	return false, fmt.Errorf("log level %s of %s: http %d %s", level, task, code, body)
}

// Log sends log text of the task at the level to the job log (write-log): its lines,
// carried in the one-time job body. The caller checks LogLevel first.
func (c *Client) Log(task, level, text string) error {
	if len(text) > MaxLogSize {
		return fmt.Errorf("log of %s: larger than %d bytes", task, MaxLogSize)
	}
	code, body, err := c.postWith("/write-log/"+url.PathEscape(task), map[string]interface{}{"log": text, "level": level})
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("log of %s: http %d %s", task, code, body)
	}
	return nil
}

// JobsCheckReceiver asks replication-manager to open the receiver of the jobs check (the
// script sends itself there) and returns its address host:port.
func (c *Client) JobsCheckReceiver() (string, error) {
	code, body, err := c.post("/actions/receive-jobs-check")
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("jobs check receiver: http %d %s", code, body)
	}
	addr, err := c.receiverAddress(strings.TrimPrefix(body, "RECEIVER_PORT="), body)
	if err != nil {
		return "", fmt.Errorf("jobs check receiver: %w", err)
	}
	return addr, nil
}

// RequestJobsUpgrade asks replication-manager to send the current jobs script to this
// server's jobs listener (send-jobs-upgrade).
func (c *Client) RequestJobsUpgrade() error {
	code, body, err := c.post("/actions/send-jobs-upgrade")
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("jobs upgrade: http %d %s", code, body)
	}
	return nil
}

// NeedConfigRefresh tells whether replication-manager wants the configuration of the
// running server read again (need-config-refresh).
func (c *Client) NeedConfigRefresh() (bool, error) {
	code, _, err := c.do(http.MethodGet, c.endpoint("/need-config-refresh"), nil, "")
	if err != nil {
		return false, err
	}
	return code == http.StatusOK, nil
}

// ConfigReceiver opens the receivers of the configuration check and returns their JSON
// (config-receiver: monitor_address, dummy_config_port, current_config_port...).
func (c *Client) ConfigReceiver() ([]byte, error) {
	code, body, err := c.authed(http.MethodGet, "/config-receiver", nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK || !json.Valid([]byte(body)) {
		return nil, fmt.Errorf("config receiver: http %d %s", code, body)
	}
	return []byte(body), nil
}

// ConfigDummySend asks replication-manager to send the generated configuration to this
// server's jobs listener (config-dummy-sender) and returns the port it sends to.
func (c *Client) ConfigDummySend() (string, error) {
	code, body, err := c.authed(http.MethodPost, "/config-dummy-sender", nil)
	if err != nil {
		return "", err
	}
	var answer struct {
		SSTPort string `json:"sst_port"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &answer) != nil || answer.SSTPort == "" {
		return "", fmt.Errorf("config send: http %d %s", code, body)
	}
	return answer.SSTPort, nil
}
