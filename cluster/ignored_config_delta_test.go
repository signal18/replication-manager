// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func newTestClusterWithServers(t *testing.T) (*Cluster, *ServerMonitor, *ServerMonitor) {
	t.Helper()

	cluster := &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{},
	}

	active := newTestServerWithCluster(t, cluster, false)
	ignored := newTestServerWithCluster(t, cluster, true)
	cluster.Servers = serverList{active, ignored}

	return cluster, active, ignored
}

func newTestServerWithCluster(t *testing.T, cluster *Cluster, ignored bool) *ServerMonitor {
	t.Helper()

	return &ServerMonitor{
		Datadir:      t.TempDir(),
		Host:         "127.0.0.1",
		Port:         "3306",
		ClusterGroup: cluster,
		Ignored:      ignored,
		VariablesMap: config.NewVariablesMap(),
	}
}

func TestIgnoredServersSkipConfigCookies(t *testing.T) {
	t.Run("SetConfigChangeCookie", func(t *testing.T) {
		cluster, active, ignored := newTestClusterWithServers(t)

		cluster.SetConfigChangeCookie()

		if !active.HasConfigCookie() {
			t.Error("expected config cookie for active server")
		}
		if !active.HasConfigRefreshCookie() {
			t.Error("expected config refresh cookie for active server")
		}
		if ignored.HasConfigCookie() {
			t.Error("ignored server should not receive config cookie")
		}
		if ignored.HasConfigRefreshCookie() {
			t.Error("ignored server should not receive config refresh cookie")
		}
	})

	t.Run("SetConfigRefreshCookie", func(t *testing.T) {
		cluster, active, ignored := newTestClusterWithServers(t)

		cluster.SetConfigRefreshCookie()

		if !active.HasConfigRefreshCookie() {
			t.Error("expected config refresh cookie for active server")
		}
		if ignored.HasConfigRefreshCookie() {
			t.Error("ignored server should not receive config refresh cookie")
		}
	})

	t.Run("SetDBConfigPathCookie", func(t *testing.T) {
		cluster, active, ignored := newTestClusterWithServers(t)

		cluster.SetDBConfigPathCookie()

		if !active.HasConfigPathCookie() {
			t.Error("expected config path cookie for active server")
		}
		if ignored.HasConfigPathCookie() {
			t.Error("ignored server should not receive config path cookie")
		}
	})
}

func TestCheckNeedConfigFetchSkipsIgnoredServers(t *testing.T) {
	cluster, active, ignored := newTestClusterWithServers(t)

	cluster.Conf.ProvDbStartFetchConfig = false
	cluster.CheckNeedConfigFetch()

	if !active.HasNoConfigFetchCookie() {
		t.Error("expected no-config-fetch cookie for active server")
	}
	if ignored.HasNoConfigFetchCookie() {
		t.Error("ignored server should not receive no-config-fetch cookie")
	}

	active.SetNoConfigFetchCookie()
	ignored.SetNoConfigFetchCookie()
	cluster.Conf.ProvDbStartFetchConfig = true
	cluster.CheckNeedConfigFetch()

	if active.HasNoConfigFetchCookie() {
		t.Error("expected no-config-fetch cookie to be removed for active server")
	}
	if !ignored.HasNoConfigFetchCookie() {
		t.Error("ignored server should keep no-config-fetch cookie")
	}
}

func TestWriteDeltaVariablesSkippedForIgnoredServer(t *testing.T) {
	cluster := &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{},
	}
	server := newTestServerWithCluster(t, cluster, true)

	server.VariablesMap.SetConfigValue("max_connections", "100")
	server.VariablesMap.SetDeployedValue("max_connections", "100")

	if err := server.WriteDeltaVariables(); err != nil {
		t.Fatalf("WriteDeltaVariables() returned error: %v", err)
	}

	deltaPath := filepath.Join(server.Datadir, "02_delta.cnf")
	if _, err := os.Stat(deltaPath); err == nil {
		t.Error("delta file should not be created for ignored server")
	} else if !os.IsNotExist(err) {
		t.Fatalf("unexpected error checking delta file: %v", err)
	}
}

func TestProcessDummyConfigSendCookieSkippedForIgnoredServer(t *testing.T) {
	cluster := &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{},
	}
	server := newTestServerWithCluster(t, cluster, true)

	if err := server.SetWaitDummyConfigSendCookie(); err != nil {
		t.Fatalf("SetWaitDummyConfigSendCookie() failed: %v", err)
	}
	if !server.HasWaitDummyConfigSendCookie() {
		t.Fatal("expected dummy config send cookie to exist")
	}

	if err := server.ProcessDummyConfigSendCookie(); err != nil {
		t.Fatalf("ProcessDummyConfigSendCookie() returned error: %v", err)
	}
	if !server.HasWaitDummyConfigSendCookie() {
		t.Error("ignored server should keep dummy config send cookie")
	}
}

func TestGetDatabaseConfigSkippedForIgnoredServer(t *testing.T) {
	cluster := &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{},
	}
	server := newTestServerWithCluster(t, cluster, true)

	if err := server.GetDatabaseConfig(); err != nil {
		t.Fatalf("GetDatabaseConfig() returned error: %v", err)
	}

	configPath := filepath.Join(server.Datadir, "config.tar.gz")
	if _, err := os.Stat(configPath); err == nil {
		t.Error("config.tar.gz should not be generated for ignored server")
	} else if !os.IsNotExist(err) {
		t.Fatalf("unexpected error checking config.tar.gz: %v", err)
	}
}
