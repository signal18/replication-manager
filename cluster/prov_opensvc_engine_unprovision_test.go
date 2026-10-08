package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// An engine server's unprovision purges the volume objects of its app template, a plain
// database server's the volume named after it.
func TestDatabaseVolumeObjects(t *testing.T) {
	cl := &Cluster{Conf: &config.Config{}}
	db := &ServerMonitor{Name: "db1", Host: "db1", ClusterGroup: cl}
	if got := cl.databaseVolumeObjects(db); len(got) != 1 || got[0] != "db1" {
		t.Fatalf("plain database: want [db1], got %v", got)
	}
	appcnf := &config.AppConfig{AppHost: "pg1", ProvAppConfigurator: "postgres", Deployment: &config.Deployment{}}
	appcnf.Deployment.Storages.Volumes = []*config.Volume{{Name: "pg1-drbd", PoolName: "drbd"}}
	app := &App{Name: "pg1", AppConfig: appcnf}
	cl.Apps = []*App{app}
	pg := &ServerMonitor{Name: "pg1", Host: "pg1", ClusterGroup: cl}
	got := cl.databaseVolumeObjects(pg)
	if len(got) != 1 || got[0] != "pg1-drbd" {
		t.Fatalf("engine server: want its app volume [pg1-drbd], got %v", got)
	}
}
