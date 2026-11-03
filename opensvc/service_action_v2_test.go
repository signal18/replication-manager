package opensvc

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// #2282: the checked v2 set/unset read the agent's answer. The bodies are the ones a
// 2.1-2073 agent returned for each case.
func TestCheckServiceActionV2Body(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"applied", `{"nodes": {"onprem1": {"status": 0, "data": {"out": "", "err": "", "ret": 0}}}, "status": 0}`, ""},
		{"applied on two nodes", `{"nodes": {"onprem1": {"status": 0, "data": {"ret": 0}}, "onprem2": {"status": 0, "data": {"ret": 0}}}, "status": 0}`, ""},
		{"warning only", `{"nodes": {"onprem1": {"status": 0, "data": {"out": "", "err": "W ignored option container#app.x, driver docker\n", "ret": 0}}}, "status": 0}`, ""},
		{"o-node holds no such object", `{"nodes": {}, "status": 0}`, "no node ran the action"},
		{"unknown o-node", `{"info": "empty node selection", "status": 0}`, "empty node selection"},
		{"unknown service", `{"error": "unknown service", "status": 1}`, "unknown service"},
		{"malformed keyword", `{"status": 500, "error": "not enough values to unpack (expected 2, got 1)"}`, "not enough values"},
		{"node failed", `{"nodes": {"onprem1": {"status": 0, "data": {"err": "boom", "ret": 1}}}, "status": 0}`, "node onprem1: status 0, ret 1: boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkServiceActionV2Body([]byte(tc.body))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestServiceConfigKeysCheckedV2Request(t *testing.T) {
	var got ActionRequest
	var onode string
	answer := `{"nodes": {"onprem1": {"status": 0, "data": {"ret": 0}}}, "status": 0}`
	mux := http.NewServeMux()
	mux.HandleFunc("/service_action", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		onode = r.Header.Get("o-node")
		_, _ = w.Write([]byte(answer))
	})
	collector := newTestCollector(t, startKeysTestServer(t, mux))

	kw := []string{"container#app.configs_environment@n1=app1/* X=app1/X@n1"}
	if err := collector.SetServiceConfigKeysCheckedV2("ns/svc/app1", "ANY", kw); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got.Path != "ns/svc/app1" || got.Action != "set" || !got.Sync || onode != "ANY" {
		t.Fatalf("unexpected request %+v o-node=%q", got, onode)
	}
	if b, _ := json.Marshal(got.Options["kw"]); string(b) != `["container#app.configs_environment@n1=app1/* X=app1/X@n1"]` {
		t.Fatalf("unexpected kw %s", b)
	}

	answer = `{"nodes": {}, "status": 0}`
	if err := collector.UnsetServiceConfigKeysCheckedV2("ns/svc/app1", "ANY", []string{"container#app.configs_environment@n2"}); err == nil {
		t.Fatalf("unset: an answer no node ran must be an error")
	}
	if got.Action != "unset" {
		t.Fatalf("unexpected action %q", got.Action)
	}
}
