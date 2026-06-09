package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

const (
	compatTestUser    = "testuser"
	compatTestCluster = "test-cluster"
	compatTestAppId   = "test-app"
)

func newAsymmetricRouteTestSetup(t *testing.T) (*ReplicationManager, *cluster.Cluster, *cluster.App) {
	t.Helper()

	appCnf := &config.AppConfig{
		AppHost: "app-a",
		AppPort: "8080",
		Deployment: &config.Deployment{
			Routes: config.Routes{
				{
					Mode:            "port",
					Protocol:        "tcp",
					CName:           "gw.example.com",
					SourcePort:      "9000",
					DestinationPort: "9001",
					Port:            "9000:9001",
					Primary:         true,
				},
			},
		},
	}
	app := &cluster.App{
		Id:        compatTestAppId,
		Name:      compatTestAppId,
		Host:      "app-a",
		Port:      "8080",
		AppConfig: appCnf,
		Mutex:     &sync.Mutex{},
	}
	cl := &cluster.Cluster{
		Name: compatTestCluster,
		Conf: &config.Config{
			WorkingDir:            t.TempDir(),
			Cloud18GatewayService: "",
		},
		WorkingDir: t.TempDir(),
	}
	cl.Conf.Apps = []*config.AppConfig{appCnf}
	cl.Apps = []*cluster.App{app}
	cl.ConfigManager = newConfigManagerForTest()
	cl.APIUsers = map[string]cluster.APIUser{
		compatTestUser: {
			User:     compatTestUser,
			Password: "enc",
			Grants:   map[string]bool{config.GrantAppDeployment: true},
		},
	}

	repman := &ReplicationManager{
		Clusters:    map[string]*cluster.Cluster{cl.Name: cl},
		ClusterList: []string{cl.Name},
		Conf:        &config.Config{TokenTimeout: 1},
	}
	repman.initKeys()
	return repman, cl, app
}

func issueCompatTestJWT(t *testing.T, repman *ReplicationManager) string {
	t.Helper()
	tok, err := repman.issueJWT(struct {
		Name     string
		Role     string
		Password string
	}{compatTestUser, "Member", "enc"}, "")
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}
	return tok
}

func newModifyRouteRequest(t *testing.T, repman *ReplicationManager, cl *cluster.Cluster, appId, key, value string) *http.Request {
	t.Helper()
	tok := issueCompatTestJWT(t, repman)
	body, _ := json.Marshal(map[string]string{"value": value})
	url := fmt.Sprintf("/api/clusters/%s/apps/%s/deployment/routes/index/0/%s/modify",
		cl.Name, appId, key)
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req = setMuxVars(req, map[string]string{
		"clusterName": cl.Name,
		"appName":     appId,
		"field":       "routes",
		"index":       "0",
		"key":         key,
	})
	return req
}

func TestRouteModify_AsymmetricPortEdit_ColonFormAccepted(t *testing.T) {
	repman, cl, _ := newAsymmetricRouteTestSetup(t)
	req := newModifyRouteRequest(t, repman, cl, compatTestAppId, "port", "8000:8001")

	w := httptest.NewRecorder()
	repman.handlerMuxModifyDeploymentField(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	route := cl.Apps[0].AppConfig.Deployment.Routes[0]
	if route.SourcePort != "8000" || route.DestinationPort != "8001" {
		t.Errorf("expected src=8000 dst=8001, got src=%q dst=%q", route.SourcePort, route.DestinationPort)
	}
	if route.Port != "8000:8001" {
		t.Errorf("expected Port=8000:8001 for old clients, got %q", route.Port)
	}
}

func TestRouteModify_AsymmetricPortEdit_CollapseRejected(t *testing.T) {
	repman, cl, _ := newAsymmetricRouteTestSetup(t)
	req := newModifyRouteRequest(t, repman, cl, compatTestAppId, "port", "8000")

	w := httptest.NewRecorder()
	repman.handlerMuxModifyDeploymentField(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for asymmetric collapse, got %d: %s", w.Code, w.Body.String())
	}
	route := cl.Apps[0].AppConfig.Deployment.Routes[0]
	if route.SourcePort != "9000" || route.DestinationPort != "9001" || route.Port != "9000:9001" {
		t.Errorf("route should be unchanged after reject: src=%q dst=%q port=%q",
			route.SourcePort, route.DestinationPort, route.Port)
	}
}

func TestGetDeployment_ReturnsLegacyPortForAsymmetricRoute(t *testing.T) {
	repman, cl, _ := newAsymmetricRouteTestSetup(t)
	cl.Apps[0].AppConfig.Deployment.Routes[0].Port = ""

	tok := issueCompatTestJWT(t, repman)
	url := fmt.Sprintf("/api/clusters/%s/apps/%s/deployment", cl.Name, compatTestAppId)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req = setMuxVars(req, map[string]string{
		"clusterName": cl.Name,
		"appName":     compatTestAppId,
	})

	w := httptest.NewRecorder()
	repman.handlerMuxAppDeployments(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var dep map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &dep); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	routes := dep["routes"].([]interface{})
	route := routes[0].(map[string]interface{})
	if route["port"] != "9000:9001" {
		t.Fatalf("expected port=9000:9001 in GET response for old clients, got %v", route["port"])
	}
}
