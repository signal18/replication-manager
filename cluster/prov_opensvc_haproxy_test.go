// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// TestOpenSVCGetHaproxyContainerSectionExternalCheckRunsCheckConfig guards
// against the OpenSVC HAProxy container always launching with the image's
// default entrypoint/CMD, which reads haproxy.cfg (proxy_cnf_haproxy_runtime_api,
// no external-check) regardless of haproxy-mode. For haproxy-mode=externalcheck,
// the container must instead run haproxy_check.cfg -- the file with
// checkmaster/checkslave wired in via "option external-check" -- or nothing
// ever excludes a broken replica from the write group (matching a real
// production incident: replicas in SLAVE_ERROR stayed UP in the write
// backend indefinitely). "-db" is required because haproxy_check.cfg's
// "daemon" directive would otherwise background the process and let PID 1
// exit immediately, the same reasoning as k8sProxyDeployment's externalcheck
// branch (cluster/prov_k8s_prx.go).
func TestOpenSVCGetHaproxyContainerSectionExternalCheckRunsCheckConfig(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)

	cluster.Conf = &config.Config{
		ProvProxType:     "docker",
		ProvType:         "docker",
		ProvProxDiskType: "volume",
		HaproxyMode:      "externalcheck",
	}
	proxy := &HaproxyProxy{Proxy: Proxy{ClusterGroup: cluster}}

	section := cluster.OpenSVCGetHaproxyContainerSection(proxy)

	if got, want := section["entrypoint"], "/bin/sh"; got != want {
		t.Fatalf("entrypoint = %q, want %q", got, want)
	}
	wantCmd := `-c "exec haproxy -W -db -f /usr/local/etc/haproxy/haproxy_check.cfg"`
	if got := section["command"]; got != wantCmd {
		t.Fatalf("command = %q, want %q", got, wantCmd)
	}
}

// TestOpenSVCGetHaproxyContainerSectionRuntimeAPIUsesImageDefault guards
// against the externalcheck-only command/entrypoint override leaking into
// haproxy-mode=runtimeapi, which must keep using the image's default
// entrypoint (haproxy.cfg, the file runtimeapi's Runtime API calls actually
// target).
func TestOpenSVCGetHaproxyContainerSectionRuntimeAPIUsesImageDefault(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)

	cluster.Conf = &config.Config{
		ProvProxType:     "docker",
		ProvType:         "docker",
		ProvProxDiskType: "volume",
		HaproxyMode:      "runtimeapi",
	}
	proxy := &HaproxyProxy{Proxy: Proxy{ClusterGroup: cluster}}

	section := cluster.OpenSVCGetHaproxyContainerSection(proxy)

	if _, ok := section["entrypoint"]; ok {
		t.Fatalf("entrypoint should not be set for haproxy-mode=runtimeapi, got %q", section["entrypoint"])
	}
	if _, ok := section["command"]; ok {
		t.Fatalf("command should not be set for haproxy-mode=runtimeapi, got %q", section["command"])
	}
}

// TestOpenSVCGetHaproxyContainerSectionStandbyUsesImageDefault guards against
// haproxy-mode=standby (now narrowed to "local, statically-rendered config,
// no external checks" per docs.signal18.io) picking up the externalcheck-only
// command/entrypoint override.
func TestOpenSVCGetHaproxyContainerSectionStandbyUsesImageDefault(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)

	cluster.Conf = &config.Config{
		ProvProxType:     "docker",
		ProvType:         "docker",
		ProvProxDiskType: "volume",
		HaproxyMode:      "standby",
	}
	proxy := &HaproxyProxy{Proxy: Proxy{ClusterGroup: cluster}}

	section := cluster.OpenSVCGetHaproxyContainerSection(proxy)

	if _, ok := section["entrypoint"]; ok {
		t.Fatalf("entrypoint should not be set for haproxy-mode=standby, got %q", section["entrypoint"])
	}
	if _, ok := section["command"]; ok {
		t.Fatalf("command should not be set for haproxy-mode=standby, got %q", section["command"])
	}
}
