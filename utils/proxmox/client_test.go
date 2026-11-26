// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "repman@pve!rolling=0f0e0d0c-aaaa-bbbb-cccc-000000000001"

// fakePVE answers the endpoints the client uses, checks the token on every request, and
// records the calls.
type fakePVE struct {
	mu     sync.Mutex
	calls  []string
	status string
	tasks  map[string]int // upid -> polls before it ends
	exit   string
}

func (f *fakePVE) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "PVEAPIToken="+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		write := func(v interface{}) { json.NewEncoder(w).Encode(map[string]interface{}{"data": v}) }
		p := r.URL.Path
		switch {
		case p == "/api2/json/version":
			write(map[string]string{"version": "8.2.4", "release": "8.2"})
		case p == "/api2/json/nodes":
			write([]Node{{Node: "pve1", Status: "online", MaxCPU: 8}})
		case strings.HasSuffix(p, "/status/current"):
			write(VMStatus{Status: f.status, QMPStatus: f.status, Uptime: 42})
		case r.Method == http.MethodPost && strings.Contains(p, "/status/"):
			upid := "UPID:pve1:0001:" + p[strings.LastIndex(p, "/")+1:]
			f.tasks[upid] = 1
			write(upid)
		case strings.Contains(p, "/tasks/"):
			upid := strings.TrimSuffix(p[strings.Index(p, "/tasks/")+len("/tasks/"):], "/status")
			if f.tasks[upid] > 0 {
				f.tasks[upid]--
				write(TaskStatus{Status: "running"})
				return
			}
			write(TaskStatus{Status: "stopped", ExitStatus: f.exit})
		default:
			w.WriteHeader(http.StatusNotImplemented)
			json.NewEncoder(w).Encode(map[string]string{"message": "no such path " + p})
		}
	})
}

func newFake(t *testing.T) (*fakePVE, *Client) {
	t.Helper()
	f := &fakePVE{status: "running", tasks: map[string]int{}, exit: "OK"}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL, testToken, false, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

func TestClientCalls(t *testing.T) {
	f, c := newFake(t)
	ctx := context.Background()
	if v, err := c.Version(ctx); err != nil || v != "8.2.4" {
		t.Fatalf("version %q %v", v, err)
	}
	nodes, err := c.Nodes(ctx)
	if err != nil || len(nodes) != 1 || nodes[0].Node != "pve1" {
		t.Fatalf("nodes %+v %v", nodes, err)
	}
	st, err := c.VMStatus(ctx, "pve1", "101")
	if err != nil || st.Status != "running" || st.Uptime != 42 {
		t.Fatalf("status %+v %v", st, err)
	}
	for _, action := range []func(context.Context, string, string) (string, error){c.StartVM, c.ShutdownVM, c.RebootVM} {
		upid, err := action(ctx, "pve1", "101")
		if err != nil || !strings.HasPrefix(upid, "UPID:") {
			t.Fatalf("action: %q %v", upid, err)
		}
		if err := c.WaitTask(ctx, "pve1", upid, 10*time.Millisecond); err != nil {
			t.Fatalf("wait %s: %v", upid, err)
		}
	}
	want := []string{
		"POST /api2/json/nodes/pve1/qemu/101/status/start",
		"POST /api2/json/nodes/pve1/qemu/101/status/shutdown",
		"POST /api2/json/nodes/pve1/qemu/101/status/reboot",
	}
	joined := strings.Join(f.calls, "\n")
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("missing call %s in\n%s", w, joined)
		}
	}
	if strings.Contains(joined, "/status/stop") {
		t.Error("the hard stop must never be called")
	}
}

func TestClientTaskFailure(t *testing.T) {
	f, c := newFake(t)
	f.exit = "VM quit/powerdown failed - got timeout"
	upid, err := c.ShutdownVM(context.Background(), "pve1", "101")
	if err != nil {
		t.Fatal(err)
	}
	err = c.WaitTask(context.Background(), "pve1", upid, 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "got timeout") {
		t.Fatalf("want the task's exit status, got %v", err)
	}
}

func TestClientWaitTaskBounded(t *testing.T) {
	f, c := newFake(t)
	upid, _ := c.RebootVM(context.Background(), "pve1", "101")
	f.mu.Lock()
	f.tasks[upid] = 1 << 30 // never ends
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := c.WaitTask(ctx, "pve1", upid, 5*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want a deadline, got %v", err)
	}
}

// A refused token is an APIError naming the status; the token is in no error.
func TestClientRefusedTokenNotLeaked(t *testing.T) {
	f := &fakePVE{status: "running", tasks: map[string]int{}, exit: "OK"}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	other := "repman@pve!rolling=ffffffff-dead-beef-0000-000000000002"
	c, err := NewClient(srv.URL+"/api2/json/", other, false, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.VMStatus(context.Background(), "pve1", "101")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want a 401 APIError, got %v", err)
	}
	if strings.Contains(err.Error(), "ffffffff-dead-beef") {
		t.Fatalf("the token leaked in the error: %v", err)
	}
}

func TestNewClientValidates(t *testing.T) {
	for _, tc := range []struct{ url, token string }{
		{"", testToken},
		{"not a url", testToken},
		{"https://pve.example:8006", ""},
		{"https://pve.example:8006", "secret-without-id"},
	} {
		_, err := NewClient(tc.url, tc.token, false, 0)
		if err == nil {
			t.Errorf("NewClient(%q, %q) accepted", tc.url, tc.token)
			continue
		}
		if tc.token != "" && strings.Contains(err.Error(), tc.token) {
			t.Errorf("the token is in the error: %v", err)
		}
	}
}
