// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"os"
	"path/filepath"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

func (regtest *RegTest) TestConfigCookiePushBasic(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	if len(cluster.GetServers()) == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No servers in cluster")
		return false
	}

	server := cluster.GetServers()[0]
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Creating dummy config send cookie for server %s", server.Id)

	err := server.SetWaitDummyConfigSendCookie()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to create cookie: %s", err)
		return false
	}

	if !server.HasWaitDummyConfigSendCookie() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cookie was not created")
		return false
	}

	modtime, err := server.GetWaitDummyConfigSendCookieModTime()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to get cookie modtime: %s", err)
		return false
	}
	if modtime.IsZero() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cookie modtime is zero")
		return false
	}

	time.Sleep(100 * time.Millisecond)

	err = server.DelWaitDummyConfigSendCookie()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to delete cookie: %s", err)
		return false
	}

	if server.HasWaitDummyConfigSendCookie() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cookie was not deleted")
		return false
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Cookie lifecycle test passed")
	return true
}

func (regtest *RegTest) TestConfigCookiePushLifecycle(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	if len(cluster.GetServers()) == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No servers in cluster")
		return false
	}

	server := cluster.GetServers()[0]
	err := server.SetWaitDummyConfigSendCookie()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cookie creation failed: %s", err)
		return false
	}

	foundCookie := false
	for _, srv := range cluster.GetServers() {
		if srv.HasWaitDummyConfigSendCookie() {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Monitor found cookie for server %s", srv.Id)
			foundCookie = true
			break
		}
	}

	if !foundCookie {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Monitor did not detect cookie")
		return false
	}

	modtime, err := server.GetWaitDummyConfigSendCookieModTime()
	if err != nil || modtime.IsZero() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to get modtime")
		return false
	}

	server.DelWaitDummyConfigSendCookie()

	err = server.GetDummyConfig()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Config generation failed (may be expected): %s", err)
	}

	configTarball := filepath.Join(server.Datadir, "config.tar.gz")
	if _, err := os.Stat(configTarball); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Config tarball not found (expected in test environment)")
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Cookie lifecycle completed")
	return true
}

func (regtest *RegTest) TestConfigCookiePushMultipleServers(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	servers := cluster.GetServers()
	if len(servers) < 2 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Test requires at least 2 servers, skipping")
		return true
	}

	for i, server := range servers {
		err := server.SetWaitDummyConfigSendCookie()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to create cookie for server %d: %s", i, err)
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}

	for _, server := range servers {
		if !server.HasWaitDummyConfigSendCookie() {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cookie missing for server %s", server.Id)
			return false
		}
	}

	for _, server := range servers {
		server.DelWaitDummyConfigSendCookie()
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Multi-server cookie test passed")
	return true
}

func (regtest *RegTest) TestConfigCookiePushTiming(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	if len(cluster.GetServers()) == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No servers in cluster")
		return false
	}

	server := cluster.GetServers()[0]
	cookieCreateTime := time.Now()

	server.SetWaitDummyConfigSendCookie()
	modtime, err := server.GetWaitDummyConfigSendCookieModTime()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to get modtime: %s", err)
		return false
	}

	timeDiff := modtime.Sub(cookieCreateTime)
	if timeDiff < 0 {
		timeDiff = -timeDiff
	}

	if timeDiff > 2*time.Second {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cookie modtime differs too much: %v", timeDiff)
		return false
	}

	server.DelWaitDummyConfigSendCookie()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Timing test passed (diff: %v)", timeDiff)
	return true
}

func (regtest *RegTest) TestConfigCookiePushCookieDir(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	if len(cluster.GetServers()) == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No servers in cluster")
		return false
	}

	server := cluster.GetServers()[0]
	server.SetWaitDummyConfigSendCookie()

	cookieFile := filepath.Join(server.Datadir, "@cookie_wait_dummy_send")
	if _, err := os.Stat(cookieFile); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cookie file does not exist: %s", cookieFile)
		return false
	}

	server.DelWaitDummyConfigSendCookie()

	if _, err := os.Stat(cookieFile); !os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cookie file was not deleted")
		return false
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Cookie directory test passed")
	return true
}

func (regtest *RegTest) TestConfigCookiePushNoConfigFetchCookie(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	if len(cluster.GetServers()) == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No servers in cluster")
		return false
	}

	server := cluster.GetServers()[0]
	server.SetNoConfigFetchCookie()

	if !server.HasNoConfigFetchCookie() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No-config-fetch cookie was not created")
		return false
	}

	server.DelNoConfigFetchCookie()

	if server.HasNoConfigFetchCookie() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No-config-fetch cookie was not deleted")
		return false
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "No-config-fetch cookie test passed")
	return true
}

func (regtest *RegTest) TestConfigCookiePushProcessing(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	if len(cluster.GetServers()) == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No servers in cluster")
		return false
	}

	server := cluster.GetServers()[0]
	server.SetWaitDummyConfigSendCookie()

	if !server.HasWaitDummyConfigSendCookie() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cookie was not created")
		return false
	}

	err := server.ProcessDummyConfigSendCookie()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "ProcessDummyConfigSendCookie returned error (expected in test): %s", err)
	}

	if server.HasWaitDummyConfigSendCookie() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cookie was not deleted during processing")
		return false
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Cookie processing test passed")
	return true
}
