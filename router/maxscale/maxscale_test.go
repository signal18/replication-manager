// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package maxscale

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// newTestServer starts a fake MaxScale REST API and returns a MaxScale
// client pointed at it, along with the *httptest.Server so the caller can
// register handlers via mux.HandleFunc before making requests.
func newTestServer(t *testing.T) (*MaxScale, *http.ServeMux) {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("could not parse test server URL: %s", err)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("could not split test server host:port: %s", err)
	}
	return &MaxScale{Host: host, Port: port, User: "admin", Pass: "mariadb", UseRest: true}, mux
}

// newFakeMaxAdminServer starts a fake MaxAdmin TCP server implementing the
// handshake (4-byte prompt, 8-byte password prompt, non-"FAILED" success
// reply) plus a command/response loop ending each reply in "OK", matching
// what connectMaxAdmin/Command/readUntilOK actually expect byte-for-byte.
// handleCommand maps a received command string to the response body to send
// back (without the trailing "OK", added automatically).
func newFakeMaxAdminServer(t *testing.T, handleCommand func(cmd string) string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not start fake MaxAdmin server: %s", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 256)
				if _, err := c.Write([]byte("PWD:")); err != nil { // 4 bytes
					return
				}
				if _, err := c.Read(buf); err != nil { // username
					return
				}
				if _, err := c.Write([]byte("Password")); err != nil { // 8 bytes
					return
				}
				if _, err := c.Read(buf); err != nil { // password
					return
				}
				if _, err := c.Write([]byte("SUCCESS")); err != nil { // not "FAILED"
					return
				}
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					resp := handleCommand(string(buf[:n]))
					c.Write([]byte(resp + "OK"))
				}
			}(conn)
		}
	}()
	return ln.Addr().String()
}

func writeJSON(t *testing.T, w http.ResponseWriter, v interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("could not encode fake response: %s", err)
	}
}

func TestConnect_SucceedsWhenServersEndpointReachable(t *testing.T) {
	m, mux := newTestServer(t)
	mux.HandleFunc("/v1/servers", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"data": []any{}})
	})
	if err := m.Connect(); err != nil {
		t.Fatalf("expected Connect to succeed against a reachable fake REST API, got: %s", err)
	}
}

func TestConnect_FailsOnAuthError(t *testing.T) {
	m, mux := newTestServer(t)
	mux.HandleFunc("/v1/servers", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	if err := m.Connect(); err == nil {
		t.Fatal("expected Connect to fail when the REST API rejects the request")
	}
}

func TestListServers_ParsesRESTResponseIntoServerList(t *testing.T) {
	m, mux := newTestServer(t)
	mux.HandleFunc("/v1/servers", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"data": []map[string]any{
				{
					"id": "server1",
					"attributes": map[string]any{
						"state":      "Master, Running",
						"parameters": map[string]any{"address": "10.0.0.1", "port": 3306},
						"statistics": map[string]any{"connections": 4},
					},
				},
				{
					"id": "server2",
					"attributes": map[string]any{
						"state":      "Slave, Running",
						"parameters": map[string]any{"address": "10.0.0.2", "port": 3306},
						"statistics": map[string]any{"connections": 1},
					},
				},
			},
		})
	})

	servers, err := m.ListServers()
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if len(servers) != 2 {
		t.Fatalf("expected 2 servers, got %d: %+v", len(servers), servers)
	}
	if servers[0].Server != "server1" || servers[0].Address != "10.0.0.1" || servers[0].Port != "3306" || servers[0].Status != "Master, Running" || servers[0].Connections != "4" {
		t.Fatalf("unexpected server1 fields: %+v", servers[0])
	}

	name, status, conns := m.GetServer("10.0.0.2", "3306", true)
	if name != "server2" || status != "Slave, Running" || conns != "1" {
		t.Fatalf("expected GetServer to find server2 from the cached list, got name=%q status=%q conns=%q", name, status, conns)
	}
}

func TestListMonitors_ParsesRESTResponseAndFindsRunningMonitor(t *testing.T) {
	m, mux := newTestServer(t)
	mux.HandleFunc("/v1/monitors", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"data": []map[string]any{
				{"id": "mysql-monitor", "attributes": map[string]any{"state": "Running"}},
			},
		})
	})

	monitors, err := m.ListMonitors()
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if len(monitors) != 1 || monitors[0].Monitor != "mysql-monitor" || monitors[0].Status != "Running" {
		t.Fatalf("unexpected monitors: %+v", monitors)
	}
	if got := m.GetMonitor(); got != "mysql-monitor" {
		t.Fatalf("expected GetMonitor to return %q, got %q", "mysql-monitor", got)
	}
	if got := m.GetStoppedMonitor(); got != "" {
		t.Fatalf("expected no stopped monitor, got %q", got)
	}
}

func TestSetServer_SendsStateAsQueryParamToSetEndpoint(t *testing.T) {
	m, mux := newTestServer(t)
	var gotMethod, gotPath, gotState string
	mux.HandleFunc("/v1/servers/server1/set", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotState = r.URL.Query().Get("state")
		w.WriteHeader(http.StatusNoContent)
	})

	if err := m.SetServer("server1", "master"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if gotMethod != "PUT" {
		t.Fatalf("expected PUT, got %s", gotMethod)
	}
	if gotPath != "/v1/servers/server1/set" {
		t.Fatalf("expected the set endpoint for server1, got %s", gotPath)
	}
	if gotState != "master" {
		t.Fatalf("expected state=master, got %q", gotState)
	}
}

func TestClearServer_SendsStateAsQueryParamToClearEndpoint(t *testing.T) {
	m, mux := newTestServer(t)
	var gotPath, gotState string
	mux.HandleFunc("/v1/servers/server1/clear", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotState = r.URL.Query().Get("state")
		w.WriteHeader(http.StatusNoContent)
	})

	if err := m.ClearServer("server1", "slave"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if gotPath != "/v1/servers/server1/clear" || gotState != "slave" {
		t.Fatalf("expected clear?state=slave for server1, got path=%s state=%s", gotPath, gotState)
	}
}

func TestSetServer_ReturnsErrorOnBadRequest(t *testing.T) {
	m, mux := newTestServer(t)
	mux.HandleFunc("/v1/servers/server1/set", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"errors":[{"detail":"Invalid state"}]}`))
	})
	if err := m.SetServer("server1", "not-a-real-state"); err == nil {
		t.Fatal("expected an error when the REST API rejects the state value")
	}
}

func TestSetMasterAcceptReads_SendsPatchWithJSONBooleanBody(t *testing.T) {
	m, mux := newTestServer(t)
	var gotMethod, gotPath, gotContentType string
	var gotBody map[string]any
	mux.HandleFunc("/v1/services/Read-Write-Connection-Router", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	})

	if err := m.SetMasterAcceptReads("Read-Write-Connection-Router", true); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if gotMethod != "PATCH" {
		t.Fatalf("expected PATCH, got %s", gotMethod)
	}
	if gotPath != "/v1/services/Read-Write-Connection-Router" {
		t.Fatalf("expected the Read-Write-Connection-Router service endpoint, got %s", gotPath)
	}
	if gotContentType != "application/json" {
		t.Fatalf("expected Content-Type: application/json, got %q", gotContentType)
	}
	data, _ := gotBody["data"].(map[string]any)
	attrs, _ := data["attributes"].(map[string]any)
	params, _ := attrs["parameters"].(map[string]any)
	// A real JSON boolean, not the string "true" -- MaxScale's REST API is
	// strongly typed, unlike the ini-style config file where "1"/"true" are
	// interchangeable text.
	if v, ok := params["master_accept_reads"].(bool); !ok || v != true {
		t.Fatalf("expected master_accept_reads: true (JSON bool) in the PATCH body, got %+v (%T)", params["master_accept_reads"], params["master_accept_reads"])
	}
}

func TestSetMasterAcceptReads_EscapesServiceNameInPath(t *testing.T) {
	m, mux := newTestServer(t)
	var gotPath string
	mux.HandleFunc("/v1/services/write-router", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})
	if err := m.SetMasterAcceptReads("write-router", false); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if gotPath != "/v1/services/write-router" {
		t.Fatalf("expected /v1/services/write-router, got %s", gotPath)
	}
}

func TestSetMasterAcceptReads_ReturnsErrorOnBadRequest(t *testing.T) {
	m, mux := newTestServer(t)
	mux.HandleFunc("/v1/services/Read-Write-Connection-Router", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"errors":[{"detail":"Unknown parameter"}]}`))
	})
	if err := m.SetMasterAcceptReads("Read-Write-Connection-Router", true); err == nil {
		t.Fatal("expected an error when the REST API rejects the PATCH")
	}
}

// Regression: master_accept_reads can only be live-patched over the REST
// API -- MaxAdmin (MaxScale < 2.5, no REST at all) never exposed runtime
// parameter changes the way maxctrl/REST do. Confirms SetMasterAcceptReads
// fails fast with a clear error instead of attempting a MaxAdmin command
// that doesn't exist.
func TestSetMasterAcceptReads_ReturnsErrorWhenNotUsingRest(t *testing.T) {
	m := &MaxScale{Host: "127.0.0.1", Port: "6603", User: "admin", Pass: "mariadb", UseRest: false}
	err := m.SetMasterAcceptReads("Read-Write-Connection-Router", true)
	if err == nil {
		t.Fatal("expected an error when UseRest is false")
	}
	if !strings.Contains(err.Error(), "REST API") {
		t.Fatalf("expected the error to explain this requires the REST API, got: %s", err)
	}
}

func TestShutdownMonitor_PutsStopEndpoint(t *testing.T) {
	m, mux := newTestServer(t)
	var gotMethod, gotPath string
	mux.HandleFunc("/v1/monitors/mysql-monitor/stop", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})
	if err := m.ShutdownMonitor("mysql-monitor"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if gotMethod != "PUT" || gotPath != "/v1/monitors/mysql-monitor/stop" {
		t.Fatalf("expected PUT /v1/monitors/mysql-monitor/stop, got %s %s", gotMethod, gotPath)
	}
}

func TestRestartMonitor_PutsStartEndpoint(t *testing.T) {
	m, mux := newTestServer(t)
	var gotPath string
	mux.HandleFunc("/v1/monitors/mysql-monitor/start", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})
	if err := m.RestartMonitor("mysql-monitor"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if gotPath != "/v1/monitors/mysql-monitor/start" {
		t.Fatalf("expected PUT /v1/monitors/mysql-monitor/start, got %s", gotPath)
	}
}

func TestSetServer_EscapesServerNameInPath(t *testing.T) {
	m, mux := newTestServer(t)
	var gotEscapedPath, gotDecodedPath string
	mux.HandleFunc("/v1/servers/", func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		gotDecodedPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})
	if err := m.SetServer("server one", "master"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	// The wire form must be percent-escaped (a literal space in a URL is
	// invalid)...
	if !strings.Contains(gotEscapedPath, "%20") {
		t.Fatalf("expected the server name to be percent-escaped on the wire, got %q", gotEscapedPath)
	}
	// ...but round-trip correctly back to the exact original name once the
	// server decodes it, proving PathEscape didn't corrupt it.
	if gotDecodedPath != "/v1/servers/server one/set" {
		t.Fatalf("expected the server name to round-trip to \"server one\", got %q", gotDecodedPath)
	}
}

func TestClose_IsANoOp(t *testing.T) {
	m := &MaxScale{Host: "127.0.0.1", Port: "8989", User: "admin", Pass: "mariadb"}
	m.Close() // must not panic even without a prior Connect()
}

func TestResponse_IsANoOpUnderRest(t *testing.T) {
	m := &MaxScale{UseRest: true}
	lines, err := m.Response()
	if lines != nil || err != nil {
		t.Fatalf("expected Response() to be a harmless no-op under REST, got lines=%v err=%v", lines, err)
	}
}

// --- MaxAdmin (UseRest: false) fallback ---

func newMaxAdminTestClient(addr string) *MaxScale {
	host, port, _ := net.SplitHostPort(addr)
	return &MaxScale{Host: host, Port: port, User: "admin", Pass: "mariadb", UseRest: false}
}

func TestConnect_MaxAdminSucceedsOnHandshake(t *testing.T) {
	addr := newFakeMaxAdminServer(t, func(cmd string) string { return "" })
	m := newMaxAdminTestClient(addr)
	if err := m.Connect(); err != nil {
		t.Fatalf("expected Connect to succeed against a fake MaxAdmin server, got: %s", err)
	}
	m.Close()
}

func TestConnect_MaxAdminFailsWhenUnreachable(t *testing.T) {
	m := &MaxScale{Host: "127.0.0.1", Port: "1", User: "admin", Pass: "mariadb", UseRest: false}
	if err := m.Connect(); err == nil {
		t.Fatal("expected Connect to fail against an unreachable MaxAdmin address")
	}
}

// Regression: connectMaxAdmin() used to leave m.Conn open on every handshake
// failure past the initial dial, relying on the caller to Close() it -- but
// several callers (e.g. SetMaintenance) treat a Connect() error as nothing
// to clean up and return without calling Close(), leaking the socket. A
// closed conn is confirmed here by a subsequent Write failing: Close() on a
// net.Conn always makes later local Write/Read calls on that same conn
// return "use of closed network connection", regardless of remote state.
func TestConnect_MaxAdminClosesConnectionOnAuthFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not start fake MaxAdmin server: %s", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 256)
		conn.Write([]byte("PWD:"))
		conn.Read(buf)
		conn.Write([]byte("Password"))
		conn.Read(buf)
		conn.Write([]byte("FAILED"))
	}()

	host, port, _ := net.SplitHostPort(ln.Addr().String())
	m := &MaxScale{Host: host, Port: port, User: "admin", Pass: "mariadb", UseRest: false}
	if err := m.Connect(); err == nil {
		t.Fatal("expected Connect to fail on auth rejection")
	}
	if m.Conn == nil {
		t.Fatal("expected m.Conn to be set after a failed handshake")
	}
	if _, err := m.Conn.Write([]byte("x")); err == nil {
		t.Fatal("expected m.Conn to be closed after a failed handshake, but Write succeeded")
	}
}

// Same leaked-socket regression as above, but for a negotiation mismatch
// (unexpected prompt length) rather than an explicit auth rejection.
func TestConnect_MaxAdminClosesConnectionOnNegotiationMismatch(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not start fake MaxAdmin server: %s", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.Write([]byte("BAD")) // 3 bytes, not the 4-byte "PWD:" prompt Connect() expects
	}()

	host, port, _ := net.SplitHostPort(ln.Addr().String())
	m := &MaxScale{Host: host, Port: port, User: "admin", Pass: "mariadb", UseRest: false}
	if err := m.Connect(); err == nil {
		t.Fatal("expected Connect to fail on negotiation mismatch")
	}
	if _, err := m.Conn.Write([]byte("x")); err == nil {
		t.Fatal("expected m.Conn to be closed after a negotiation mismatch, but Write succeeded")
	}
}

func TestListServers_MaxAdminParsesTabularResponse(t *testing.T) {
	addr := newFakeMaxAdminServer(t, func(cmd string) string {
		if strings.TrimSpace(cmd) != "list servers" {
			return ""
		}
		return "Server | Address | Port | Connections | Status\n" +
			"server1 | 10.0.0.1 | 3306 | 4 | Master, Running\n" +
			"server2 | 10.0.0.2 | 3306 | 1 | Slave, Running\n"
	})
	m := newMaxAdminTestClient(addr)
	if err := m.Connect(); err != nil {
		t.Fatalf("unexpected Connect error: %s", err)
	}
	defer m.Close()

	servers, err := m.ListServers()
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if len(servers) != 2 || servers[0].Server != "server1" || servers[0].Status != "Master, Running" {
		t.Fatalf("unexpected servers: %+v", servers)
	}

	name, status, _ := m.GetServer("10.0.0.2", "3306", true)
	if name != "server2" || status != "Slave, Running" {
		t.Fatalf("expected GetServer to find server2 from the cached list, got name=%q status=%q", name, status)
	}
}

func TestSetServer_MaxAdminSendsCommandAndReadsResponse(t *testing.T) {
	var gotCmd string
	addr := newFakeMaxAdminServer(t, func(cmd string) string {
		gotCmd = cmd
		return ""
	})
	m := newMaxAdminTestClient(addr)
	if err := m.Connect(); err != nil {
		t.Fatalf("unexpected Connect error: %s", err)
	}
	defer m.Close()

	if err := m.SetServer("server1", "master"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if gotCmd != "set server server1 master" {
		t.Fatalf("expected the MaxAdmin \"set server\" command, got %q", gotCmd)
	}
}

// callWithTimeout runs fn and fails the test if it doesn't return within d,
// instead of hanging until the whole test binary times out. Used by the
// readUntilOK boundary regressions below, where the bug under test is an
// infinite read loop. t.Cleanup on the pipe conns used by those tests
// unblocks any leftover blocked Read once the test returns, so a failure
// here doesn't leak the goroutine for the rest of the run.
func callWithTimeout(t *testing.T, d time.Duration, fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("call did not return within %s -- readUntilOK likely hung waiting for a short read that will never come", d)
		return nil
	}
}

// newFakeMaxAdminPipe wires m.Conn directly to one end of an in-memory
// net.Pipe(), with a goroutine serving the other end via handleCommand.
// Unlike newFakeMaxAdminServer's real TCP socket, net.Pipe has no internal
// buffering: a Read call whose buffer is at least as large as the pending
// Write is guaranteed to consume that Write's bytes in one shot, with no
// dependency on OS/kernel delivery timing. That determinism matters for the
// exact-buffer-boundary regressions below, which need a single Read to
// return precisely len(buf) bytes -- over a real TCP loopback socket the
// kernel is free to split delivery across multiple reads, which would let a
// reintroduced bug slip through as a short read and pass anyway.
// Skips the login handshake entirely: ListServers/ListMonitors/SetServer
// only ever use m.Conn directly and never re-run Connect().
func newFakeMaxAdminPipe(t *testing.T, handleCommand func(cmd string) string) *MaxScale {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := server.Read(buf)
			if err != nil {
				return
			}
			if _, err := server.Write([]byte(handleCommand(string(buf[:n])) + "OK")); err != nil {
				return
			}
		}
	}()

	return &MaxScale{Conn: client, User: "admin", Pass: "mariadb", UseRest: false}
}

// Regression: readUntilOK() centralized every MaxAdmin reader behind one loop
// that only recognized the trailing "OK" on a short read (res < len(buf)) --
// the guard ShowServers() alone used historically. ListServers() never had
// that guard, so a reply whose total length (body + "OK") lands exactly on
// the 1024-byte read buffer used here would fill the buffer completely on
// the first Read and hang forever waiting for a short read that never
// arrives. Sized to hit that boundary exactly.
func TestListServers_MaxAdminHandlesReplyExactlyAtBufferBoundary(t *testing.T) {
	const bufSize = 1024
	header := "Server | Address | Port | Connections | Status\n"
	row := "server1 | 10.0.0.1 | 3306 | 4 | Master, Running\n"
	body := header + row
	body += strings.Repeat("#", bufSize-2-len(body)) // pad so body+"OK" == bufSize exactly

	m := newFakeMaxAdminPipe(t, func(cmd string) string {
		if strings.TrimSpace(cmd) != "list servers" {
			return ""
		}
		return body
	})
	defer m.Close()

	var servers []Server
	err := callWithTimeout(t, 2*time.Second, func() error {
		var err error
		servers, err = m.ListServers()
		return err
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if len(servers) != 1 || servers[0].Server != "server1" {
		t.Fatalf("unexpected servers: %+v", servers)
	}
}

// Regression: same readUntilOK() boundary issue as above, but for
// ListMonitors(), sized to the 512-byte buffer it reads with.
func TestListMonitors_MaxAdminHandlesReplyExactlyAtBufferBoundary(t *testing.T) {
	const bufSize = 512
	row := "MySQL-Monitor | Running\n"
	body := row
	body += strings.Repeat("#", bufSize-2-len(body))

	m := newFakeMaxAdminPipe(t, func(cmd string) string {
		if strings.TrimSpace(cmd) != "list monitors" {
			return ""
		}
		return body
	})
	defer m.Close()

	var monitors []Monitor
	err := callWithTimeout(t, 2*time.Second, func() error {
		var err error
		monitors, err = m.ListMonitors()
		return err
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if len(monitors) != 1 || monitors[0].Monitor != "MySQL-Monitor" || monitors[0].Status != "Running" {
		t.Fatalf("unexpected monitors: %+v", monitors)
	}
}

// Regression: same readUntilOK() boundary issue as above, but for the
// command Response() path (exercised here via SetServer(), its only public
// MaxAdmin caller), sized to the 512-byte buffer responseMaxAdmin() reads
// with.
func TestSetServer_MaxAdminHandlesReplyExactlyAtBufferBoundary(t *testing.T) {
	const bufSize = 512
	body := strings.Repeat("#", bufSize-2)

	m := newFakeMaxAdminPipe(t, func(cmd string) string {
		if strings.TrimSpace(cmd) != "set server server1 maintenance" {
			return ""
		}
		return body
	})
	defer m.Close()

	err := callWithTimeout(t, 2*time.Second, func() error {
		return m.SetServer("server1", "maintenance")
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
}
