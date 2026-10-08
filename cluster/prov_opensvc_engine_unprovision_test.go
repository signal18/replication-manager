package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// An engine server is the server an app template renders: the provision and unprovision
// of its service go through the app, never through the database template.
func TestEngineAppOfServerResolvesTheMember(t *testing.T) {
	cl := &Cluster{Conf: &config.Config{}}
	db := &ServerMonitor{Name: "db1", Host: "db1", ClusterGroup: cl}
	if cl.engineAppOfServer(db) != nil {
		t.Fatal("a plain database server has no engine app")
	}
	appcnf := &config.AppConfig{AppHost: "pg1", ProvAppConfigurator: "postgres", Deployment: &config.Deployment{}}
	cl.Apps = []*App{{Name: "pg1", AppConfig: appcnf}}
	pg := &ServerMonitor{Name: "pg1", Host: "pg1", ClusterGroup: cl}
	if app := cl.engineAppOfServer(pg); app == nil || app.Name != "pg1" {
		t.Fatalf("the member's app must be found, got %v", app)
	}
}
