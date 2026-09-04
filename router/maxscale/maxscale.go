// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

// maxscale.go
//
// Talks to MaxScale either over its REST API (HTTP + Basic Auth, UseRest
// true) or the old MaxAdmin TCP protocol (UseRest false). MaxAdmin was
// removed from MaxScale itself starting 2.5.0, in favor of the REST API and
// maxctrl; the REST API predates that (introduced in 2.2). UseRest defaults
// on (maxscale-rest-api) for that reason, but callers on MaxScale older than
// 2.2 -- which never had a REST API to speak to -- can still opt out.
package maxscale

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type MaxScale struct {
	Host string
	Port string
	User string
	Pass string

	// UseRest selects the REST API (true) or the legacy MaxAdmin TCP
	// protocol (false). Set by the caller before Connect().
	UseRest bool

	Conn   net.Conn     // MaxAdmin only
	client *http.Client // REST only

	// Cache of the last List*/GetMaxInfo* call, read back by the matching
	// Get* methods. Instance-scoped, not package-level: each cluster has its
	// own MaxScale client and its own monitoring goroutine, so a shared
	// global here would let one cluster clobber another's cached servers.
	serverList      []Server
	monitorList     []Monitor
	serverMaxinfos  []ServerMaxinfo
	monitorMaxinfos []MonitorMaxinfo
}

type Server struct {
	Server      string
	Address     string
	Port        string
	Connections string
	Status      string
}

type ServerMaxinfo struct {
	Server      string
	Address     string
	Port        int
	Connections int
	Status      string
}

type MonitorMaxinfo struct {
	Monitor string
	Status  string
}
type Monitor struct {
	Monitor string
	Status  string
}

const (
	maxDefaultTimeout = 5 * time.Second
	// Error types
	ErrorNegotiation = "Incorrect maxscale protocol negotiation"
	ErrorReader      = "Error reading from buffer"
)

// --- REST API ---

// restResource is the common shape of a single MaxScale REST API resource
// (a server or a monitor) under "data". Only the fields actually consumed
// below are decoded.
type restResource struct {
	ID         string `json:"id"`
	Attributes struct {
		State      string `json:"state"`
		Parameters struct {
			Address string `json:"address"`
			Port    int    `json:"port"`
		} `json:"parameters"`
		Statistics struct {
			Connections int `json:"connections"`
		} `json:"statistics"`
	} `json:"attributes"`
}

type restList struct {
	Data []restResource `json:"data"`
}

func (m *MaxScale) baseURL() string {
	return "http://" + m.Host + ":" + m.Port + "/v1"
}

// request performs one REST API call, returning the raw response body. A
// non-2xx status is reported as an error carrying the response body, since
// MaxScale's REST API puts the actionable detail there (e.g. "Missing or
// invalid parameter").
func (m *MaxScale) request(method, path string, query url.Values) ([]byte, error) {
	if m.client == nil {
		m.client = &http.Client{Timeout: maxDefaultTimeout}
	}
	u := m.baseURL() + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequest(method, u, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(m.User, m.Pass)
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return body, fmt.Errorf("MaxScale REST API %s %s returned %d: %s", method, path, resp.StatusCode, string(body))
	}
	return body, nil
}

// requestJSON is like request but sends a JSON request body -- needed for
// PATCH calls that modify parameters, which request (GET/PUT/DELETE with no
// body) can't express.
func (m *MaxScale) requestJSON(method, path string, body []byte) ([]byte, error) {
	if m.client == nil {
		m.client = &http.Client{Timeout: maxDefaultTimeout}
	}
	req, err := http.NewRequest(method, m.baseURL()+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(m.User, m.Pass)
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return respBody, fmt.Errorf("MaxScale REST API %s %s returned %d: %s", method, path, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// setServiceParamBoolREST maps to PATCH /v1/services/:name, which MaxScale
// applies live to a running router with no restart required for parameters
// documented "Dynamic: Yes" (master_accept_reads among them) -- confirmed
// live against MaxScale 2.4.10: PATCH returns 204 and a subsequent GET
// reflects the new value immediately.
func (m *MaxScale) setServiceParamBoolREST(service, param string, value bool) error {
	body, err := json.Marshal(map[string]any{
		"data": map[string]any{
			"attributes": map[string]any{
				"parameters": map[string]any{param: value},
			},
		},
	})
	if err != nil {
		return err
	}
	_, err = m.requestJSON("PATCH", "/services/"+url.PathEscape(service), body)
	return err
}

func (m *MaxScale) connectREST() error {
	m.client = &http.Client{Timeout: maxDefaultTimeout}
	if _, err := m.request("GET", "/servers", nil); err != nil {
		return err
	}
	return nil
}

func (m *MaxScale) listServersREST() ([]Server, error) {
	body, err := m.request("GET", "/servers", nil)
	if err != nil {
		return nil, err
	}
	var parsed restList
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("could not parse MaxScale servers response: %s", err)
	}
	m.serverList = make([]Server, 0, len(parsed.Data))
	for _, r := range parsed.Data {
		m.serverList = append(m.serverList, Server{
			Server:      r.ID,
			Address:     r.Attributes.Parameters.Address,
			Port:        strconv.Itoa(r.Attributes.Parameters.Port),
			Connections: strconv.Itoa(r.Attributes.Statistics.Connections),
			Status:      r.Attributes.State,
		})
	}
	return m.serverList, nil
}

func (m *MaxScale) listMonitorsREST() ([]Monitor, error) {
	body, err := m.request("GET", "/monitors", nil)
	if err != nil {
		return nil, err
	}
	var parsed restList
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("could not parse MaxScale monitors response: %s", err)
	}
	m.monitorList = make([]Monitor, 0, len(parsed.Data))
	for _, r := range parsed.Data {
		m.monitorList = append(m.monitorList, Monitor{Monitor: r.ID, Status: r.Attributes.State})
	}
	return m.monitorList, nil
}

// State values (master, slave, maintenance, running, synced, drain) are
// identical to MaxAdmin's "set server"/"clear server" commands, confirmed
// against MaxScale's own Server Resource REST API docs.
func (m *MaxScale) setServerREST(server, status string) error {
	_, err := m.request("PUT", "/servers/"+url.PathEscape(server)+"/set", url.Values{"state": {status}})
	return err
}

func (m *MaxScale) clearServerREST(server, status string) error {
	_, err := m.request("PUT", "/servers/"+url.PathEscape(server)+"/clear", url.Values{"state": {status}})
	return err
}

func (m *MaxScale) shutdownMonitorREST(monitor string) error {
	_, err := m.request("PUT", "/monitors/"+url.PathEscape(monitor)+"/stop", nil)
	return err
}

func (m *MaxScale) restartMonitorREST(monitor string) error {
	_, err := m.request("PUT", "/monitors/"+url.PathEscape(monitor)+"/start", nil)
	return err
}

// --- MaxAdmin (legacy TCP protocol, MaxScale < 2.5; removed entirely in
// 2.5+, so UseRest must be false to reach a MaxScale that old) ---

// connectMaxAdmin dials and completes the MaxAdmin login handshake, closing
// m.Conn on any failure past the dial -- callers don't Close() on a
// Connect() error, so a half-open handshake would otherwise leak the socket.
func (m *MaxScale) connectMaxAdmin() error {
	var err error
	address := net.JoinHostPort(m.Host, m.Port)
	m.Conn, err = net.DialTimeout("tcp", address, maxDefaultTimeout)
	if err != nil {
		return err
	}
	reader := bufio.NewReader(m.Conn)
	buf := make([]byte, 80)
	res, err := reader.Read(buf)
	if err != nil {
		m.Close()
		return errors.New(ErrorReader)
	}
	if res != 4 {
		m.Close()
		return errors.New(ErrorNegotiation)
	}
	writer := bufio.NewWriter(m.Conn)
	fmt.Fprint(writer, m.User)
	writer.Flush()
	res, err = reader.Read(buf)
	if err != nil {
		m.Close()
		return errors.New(ErrorReader)
	}
	if res != 8 {
		m.Close()
		return errors.New(ErrorNegotiation)
	}
	fmt.Fprint(writer, m.Pass)
	writer.Flush()
	res, err = reader.Read(buf)
	if err != nil {
		m.Close()
		return errors.New(ErrorReader)
	}
	if string(buf[0:6]) == "FAILED" {
		m.Close()
		return errors.New("Authentication failed")
	}
	return nil
}

func (m *MaxScale) Command(cmd string) error {
	if m.Conn == nil {
		return errors.New("Maxscale Connection was close")
	}
	writer := bufio.NewWriter(m.Conn)
	var err error
	if _, err = fmt.Fprint(writer, cmd); err != nil {
		return err
	}
	if writer != nil {
		err = writer.Flush()
	}
	return err
}

// readUntilOK reads from m.Conn until a chunk ends in "OK", which MaxAdmin
// appends to every reply. strict also requires a short read (res < len(buf)),
// so a full buffer that happens to end in "OK" isn't mistaken for the
// terminator: ShowServers passes true (its historical behavior); ListServers,
// ListMonitors, and Response pass false, since they must still terminate
// when "OK" lands exactly on the buffer boundary. Even with strict=false, a
// non-final chunk ending in "OK" is indistinguishable from the real
// terminator -- a pre-existing MaxAdmin framing limitation, not one strict
// introduces or can fix.
func (m *MaxScale) readUntilOK(buf []byte, strict bool) ([]byte, error) {
	reader := bufio.NewReader(m.Conn)
	var response []byte
	for {
		res, err := reader.Read(buf)
		if err != nil {
			return response, err
		}
		str := string(buf[0:res])
		if (!strict || res < len(buf)) && strings.HasSuffix(str, "OK") {
			response = append(response, buf[0:res-2]...)
			break
		}
		response = append(response, buf[0:res]...)
	}
	return response, nil
}

func (m *MaxScale) ShowServers() ([]byte, error) {
	m.Command("show serversjson")
	return m.readUntilOK(make([]byte, 80), true)
}

func (m *MaxScale) listServersMaxAdmin() ([]Server, error) {
	m.Command("list servers")
	if m.Conn == nil {
		return nil, errors.New("Tcp Connection close")
	}
	m.serverList = make([]Server, 0)
	response, err := m.readUntilOK(make([]byte, 1024), false)
	if err != nil {
		return m.serverList, nil
	}

	list := strings.Split(string(response), "\n")
	re := regexp.MustCompile(`^([[:graph:]]+)[[:space:]]*\|[[:space:]]*([[:graph:]]+)[[:space:]]*\|[[:space:]]*([0-9]+)[[:space:]]*\|[[:space:]]*([0-9]+)[[:space:]]*\|[[:space:]]*([[:ascii:]]+)*`)
	for _, line := range list {
		match := re.FindStringSubmatch(line)
		if len(match) > 0 && match[0] != "" && match[1] != "Server" {
			m.serverList = append(m.serverList, Server{Server: match[1], Address: match[2], Port: match[3], Connections: match[4], Status: match[5]})
		}
	}
	return m.serverList, nil
}

func (m *MaxScale) listMonitorsMaxAdmin() ([]Monitor, error) {
	if err := m.Command("list monitors"); err != nil {
		return nil, err
	}
	m.monitorList = make([]Monitor, 0)
	response, err := m.readUntilOK(make([]byte, 512), false)
	if err != nil {
		return m.monitorList, nil
	}

	list := strings.Split(string(response), "\n")
	re := regexp.MustCompile(`^([[:ascii:]]+)*\|[[:space:]]*([[:ascii:]]+)*`)
	for _, line := range list {
		match := re.FindStringSubmatch(line)
		if len(match) > 0 && match[0] != "" && match[1] != "Monitor" {
			m.monitorList = append(m.monitorList, Monitor{Monitor: strings.TrimRight(match[1], " "), Status: strings.TrimRight(match[2], " ")})
		}
	}
	return m.monitorList, nil
}

func (m *MaxScale) responseMaxAdmin() ([]string, error) {
	response, err := m.readUntilOK(make([]byte, 512), false)
	if err != nil {
		return nil, errors.New("Failed to read result")
	}
	return strings.Split(string(response), "\n"), nil
}

func (m *MaxScale) setServerMaxAdmin(server, status string) error {
	err := m.Command("set server " + server + " " + status)
	if err == nil {
		_, err = m.responseMaxAdmin()
	}
	return err
}

func (m *MaxScale) clearServerMaxAdmin(server, status string) error {
	err := m.Command("clear server " + server + " " + status)
	if err == nil {
		_, err = m.responseMaxAdmin()
	}
	return err
}

func (m *MaxScale) shutdownMonitorMaxAdmin(monitor string) error {
	if m.Conn == nil {
		return errors.New("Connection was close did you lost maxscale")
	}
	writer := bufio.NewWriter(m.Conn)
	if _, err := fmt.Fprintf(writer, "shutdown monitor %c%s%c\n", '"', monitor, '"'); err != nil {
		return err
	}
	return writer.Flush()
}

func (m *MaxScale) restartMonitorMaxAdmin(monitor string) error {
	if m.Conn == nil {
		return errors.New("Connection was close did you lost maxscale")
	}
	writer := bufio.NewWriter(m.Conn)
	if _, err := fmt.Fprintf(writer, "restart monitor %c%s%c\n", '"', monitor, '"'); err != nil {
		return err
	}
	return writer.Flush()
}

// --- public API: dispatches to REST or MaxAdmin per m.UseRest ---

// Connect verifies MaxScale is reachable and the credentials work. REST is
// stateless (Basic Auth per request, no session); MaxAdmin does its usual
// TCP handshake.
func (m *MaxScale) Connect() error {
	var err error
	if m.UseRest {
		err = m.connectREST()
	} else {
		err = m.connectMaxAdmin()
	}
	if err != nil {
		return fmt.Errorf("Connection failed to address %s:%s: %s", m.Host, m.Port, err)
	}
	return nil
}

// Close releases the MaxAdmin TCP connection; a no-op for REST, which holds
// no per-call connection state.
func (m *MaxScale) Close() {
	if m.Conn != nil {
		m.Conn.Close()
	}
}

func (m *MaxScale) GetMaxInfoServers(url string) ([]ServerMaxinfo, error) {
	client := &http.Client{Timeout: maxDefaultTimeout}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	monjson, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(monjson, &m.serverMaxinfos); err != nil {
		return nil, err
	}
	return m.serverMaxinfos, nil
}

func (m *MaxScale) GetMaxInfoMonitors(url string) ([]MonitorMaxinfo, error) {
	client := &http.Client{Timeout: maxDefaultTimeout}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	monjson, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(monjson, &m.monitorMaxinfos); err != nil {
		return nil, err
	}
	return m.monitorMaxinfos, nil
}

// ListServers refreshes this instance's server cache.
func (m *MaxScale) ListServers() ([]Server, error) {
	if m.UseRest {
		return m.listServersREST()
	}
	return m.listServersMaxAdmin()
}

// ListMonitors refreshes this instance's monitor cache.
func (m *MaxScale) ListMonitors() ([]Monitor, error) {
	if m.UseRest {
		return m.listMonitorsREST()
	}
	return m.listMonitorsMaxAdmin()
}

func (m *MaxScale) GetMonitor() string {
	for _, s := range m.monitorList {
		if s.Status == "Running" {
			return s.Monitor
		}
	}
	return ""
}

func (m *MaxScale) GetStoppedMonitor() string {
	for _, s := range m.monitorList {
		if s.Status == "Stopped" {
			return s.Monitor
		}
	}
	return ""
}

func (m *MaxScale) GetMaxInfoMonitor() string {
	for _, s := range m.monitorMaxinfos {
		if s.Status == "Running" {
			return s.Monitor
		}
	}
	return ""
}

func (m *MaxScale) GetMaxInfoStoppedMonitor() string {
	for _, s := range m.monitorMaxinfos {
		if s.Status == "Stopped" {
			return s.Monitor
		}
	}
	return ""
}

func (m *MaxScale) GetServer(ip string, port string, matchserverport bool) (string, string, string) {
	for _, s := range m.serverList {
		if s.Address == ip && s.Port == port {
			return s.Server, s.Status, s.Connections
		}
		if matchserverport == false && s.Address == ip {
			return s.Server, s.Status, s.Connections
		}
	}
	return "", "", ""
}

func (m *MaxScale) GetMaxInfoServer(ip string, port int, matchserverport bool) (string, string, int) {
	for _, s := range m.serverMaxinfos {
		if s.Address == ip && s.Port == port {
			return s.Server, s.Status, s.Connections
		}
		if matchserverport == false && s.Address == ip {
			return s.Server, s.Status, s.Connections
		}
	}
	return "", "", 0
}

// Response is a no-op under REST: PUT /set, /clear, /stop and /start all
// report their own success or failure directly (204 vs a REST error), unlike
// MaxAdmin where a command and reading its response are two separate steps.
func (m *MaxScale) Response() ([]string, error) {
	if m.UseRest {
		return nil, nil
	}
	return m.responseMaxAdmin()
}

// SetServer maps to the old "set server <server> <status>" MaxAdmin command
// (or its REST equivalent, PUT /v1/servers/:name/set?state=).
func (m *MaxScale) SetServer(server, status string) error {
	if m.UseRest {
		return m.setServerREST(server, status)
	}
	return m.setServerMaxAdmin(server, status)
}

// ClearServer maps to the old "clear server <server> <status>" command.
func (m *MaxScale) ClearServer(server, status string) error {
	if m.UseRest {
		return m.clearServerREST(server, status)
	}
	return m.clearServerMaxAdmin(server, status)
}

// SetMasterAcceptReads live-patches a readwritesplit service's
// master_accept_reads parameter (REST only -- MaxAdmin never exposed runtime
// parameter changes the way maxctrl/REST do, and MaxAdmin-only MaxScale
// predates 2.2, before REST existed at all).
func (m *MaxScale) SetMasterAcceptReads(service string, enabled bool) error {
	if !m.UseRest {
		return errors.New("master_accept_reads can only be live-patched over the REST API")
	}
	return m.setServiceParamBoolREST(service, "master_accept_reads", enabled)
}

// ShutdownMonitor maps to the old "shutdown monitor "<monitor>"" command.
func (m *MaxScale) ShutdownMonitor(monitor string) error {
	if m.UseRest {
		return m.shutdownMonitorREST(monitor)
	}
	return m.shutdownMonitorMaxAdmin(monitor)
}

// RestartMonitor maps to the old "restart monitor "<monitor>"" command.
func (m *MaxScale) RestartMonitor(monitor string) error {
	if m.UseRest {
		return m.restartMonitorREST(monitor)
	}
	return m.restartMonitorMaxAdmin(monitor)
}
