package opensvc

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// #2282: the key list/delete calls address the sec object for secrets and keep
// addressing the cfg object for configs, on both API versions.

func startKeysTestServer(t *testing.T, mux *http.ServeMux) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{NextProtos: []string{"h2"}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func TestObjectKeysV2_ListAndDeleteByKind(t *testing.T) {
	var mu sync.Mutex
	var listPaths, deleteReqs []string
	mux := http.NewServeMux()
	mux.HandleFunc("/object_keys", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		listPaths = append(listPaths, r.URL.Query().Get("path"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":["X","X@n1"]}`))
	})
	mux.HandleFunc("/key", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req ConfigKeyValueRequest
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		deleteReqs = append(deleteReqs, r.Method+" "+req.Path+" "+req.Key)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	collector := newTestCollector(t, startKeysTestServer(t, mux))

	secKeys, err := collector.ListSecretKeys("ns", "app1")
	if err != nil {
		t.Fatalf("list sec: %v", err)
	}
	cfgKeys, err := collector.ListConfigKeys("ns", "app1")
	if err != nil {
		t.Fatalf("list cfg: %v", err)
	}
	if strings.Join(secKeys, ",") != "X,X@n1" || strings.Join(cfgKeys, ",") != "X,X@n1" {
		t.Fatalf("unexpected keys sec=%v cfg=%v", secKeys, cfgKeys)
	}
	if err := collector.DeleteSecretKeyValue("ns", "app1", "X@n1"); err != nil {
		t.Fatalf("delete sec: %v", err)
	}
	if err := collector.DeleteConfigKeyValue("ns", "app1", "X@n2"); err != nil {
		t.Fatalf("delete cfg: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got, want := strings.Join(listPaths, ","), "ns/sec/app1,ns/cfg/app1"; got != want {
		t.Fatalf("list paths: want %q, got %q", want, got)
	}
	if got, want := strings.Join(deleteReqs, ","), "DELETE ns/sec/app1 X@n1,DELETE ns/cfg/app1 X@n2"; got != want {
		t.Fatalf("delete requests: want %q, got %q", want, got)
	}
}

func TestObjectKeysV2_DeleteFailureIsReported(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/key", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`denied`))
	})
	collector := newTestCollector(t, startKeysTestServer(t, mux))
	if err := collector.DeleteSecretKeyValue("ns", "app1", "X@n1"); err == nil {
		t.Fatal("a non-2xx delete must return an error")
	}
}

func TestObjectKeysV3_ListAndDeleteByKind(t *testing.T) {
	var mu sync.Mutex
	var reqs []string
	mux := http.NewServeMux()
	for _, kind := range []string{"sec", "cfg"} {
		kind := kind
		mux.HandleFunc("/api/object/path/ns/"+kind+"/app1/data/keys", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			reqs = append(reqs, r.Method+" "+kind+" keys")
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"kind":"DataKeyList","items":[{"name":"X"},{"name":"X@n1"}]}`))
		})
		mux.HandleFunc("/api/object/path/ns/"+kind+"/app1/data/key", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			reqs = append(reqs, r.Method+" "+kind+" "+r.URL.Query().Get("name"))
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		})
	}
	collector := newTestCollector(t, startKeysTestServer(t, mux))
	collector.SetV3()
	collector.ContextTimeoutSecond = 5

	secKeys, err := collector.ListSecretKeys("ns", "app1")
	if err != nil {
		t.Fatalf("list sec: %v", err)
	}
	cfgKeys, err := collector.ListConfigKeys("ns", "app1")
	if err != nil {
		t.Fatalf("list cfg: %v", err)
	}
	if strings.Join(secKeys, ",") != "X,X@n1" || strings.Join(cfgKeys, ",") != "X,X@n1" {
		t.Fatalf("unexpected keys sec=%v cfg=%v", secKeys, cfgKeys)
	}
	if err := collector.DeleteSecretKeyValue("ns", "app1", "X@n1"); err != nil {
		t.Fatalf("delete sec: %v", err)
	}
	if err := collector.DeleteConfigKeyValue("ns", "app1", "X@n2"); err != nil {
		t.Fatalf("delete cfg: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got, want := strings.Join(reqs, ","), "GET sec keys,GET cfg keys,DELETE sec X@n1,DELETE cfg X@n2"; got != want {
		t.Fatalf("requests: want %q, got %q", want, got)
	}
}
