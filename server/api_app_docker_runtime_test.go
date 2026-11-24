package server

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// prov-app-docker-runtime is a global key: the cluster routes hand it to the global
// handler (admin), the MCP refuses it, the set-cron route takes cron keys only.
func TestAppDockerRuntimeIsGlobalOnly(t *testing.T) {
	if !config.IsScope("prov-app-docker-runtime", "server") {
		t.Fatal("prov-app-docker-runtime must be scope server")
	}
	if isCronSetting("prov-app-docker-runtime") {
		t.Fatal("set-cron must refuse prov-app-docker-runtime")
	}
	if !isCronSetting("scheduler-db-servers-logical-backup-cron") {
		t.Fatal("set-cron takes a cluster cron key")
	}
	repman := &ReplicationManager{}
	if err := repman.SetClusterSetting(nil, "prov-app-docker-runtime", "runc"); err == nil {
		t.Fatal("the MCP path must refuse a global key")
	}
}
