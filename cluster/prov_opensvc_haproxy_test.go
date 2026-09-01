// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
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

// TestOpenSVCGetHaproxyContainerSectionStandbyUsesImageDefault is
// TestOpenSVCGetHaproxyContainerSectionRuntimeAPIUsesImageDefault's
// haproxy-mode=standby counterpart: standby (now narrowed to "local,
// statically-rendered config, no external checks" per docs.signal18.io) has
// no external check either -- it's Init() (cluster/prx_haproxy.go) deciding
// read-backend membership from replication state directly -- so it must not
// get the externalcheck-only override either.
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

// TestGetPodDockerHaproxyTemplateExternalCheckRunsCheckConfig guards against
// the same regression as
// TestOpenSVCGetHaproxyContainerSectionExternalCheckRunsCheckConfig above,
// but on the OTHER OpenSVC HAProxy provisioning path:
// OpenSVCProvisionProxyService (cluster/prov_opensvc_prx.go) calls
// OpenSVCGetHaproxyContainerSection only when ProvOpensvcUseCollectorAPI is
// false; when it's true, provisioning instead goes through
// GetHaproxyTemplate -> GetPodDockerHaproxyTemplate, which independently
// builds its own container#20 block and, before this fix, never checked
// HaproxyMode at all -- so a collector-API deployment could reproduce the
// exact incident (broken replicas never excluded from the write group) the
// other function was patched for.
func TestGetPodDockerHaproxyTemplateExternalCheckRunsCheckConfig(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)

	cluster.Conf = &config.Config{
		HaproxyMode: "externalcheck",
	}
	collector := opensvc.Collector{ProvProxMicroSrv: "docker"}

	vm := cluster.GetPodDockerHaproxyTemplate(collector, "01")

	if !strings.Contains(vm, "entrypoint = /bin/sh") {
		t.Fatalf("GetPodDockerHaproxyTemplate() does not set entrypoint = /bin/sh for haproxy-mode=externalcheck:\n%s", vm)
	}
	wantCmd := `command = -c "exec haproxy -W -db -f /usr/local/etc/haproxy/haproxy_check.cfg"`
	if !strings.Contains(vm, wantCmd) {
		t.Fatalf("GetPodDockerHaproxyTemplate() does not contain %q for haproxy-mode=externalcheck:\n%s", wantCmd, vm)
	}
}

// TestGetPodDockerHaproxyTemplateRuntimeAPIUsesImageDefault is
// TestGetPodDockerHaproxyTemplateExternalCheckRunsCheckConfig's runtimeapi
// counterpart: the externalcheck-only override must not leak into
// haproxy-mode=runtimeapi, which needs the image's default entrypoint
// (haproxy.cfg, the file runtimeapi's Runtime API calls actually target).
func TestGetPodDockerHaproxyTemplateRuntimeAPIUsesImageDefault(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)

	cluster.Conf = &config.Config{
		HaproxyMode: "runtimeapi",
	}
	collector := opensvc.Collector{ProvProxMicroSrv: "docker"}

	vm := cluster.GetPodDockerHaproxyTemplate(collector, "01")

	if strings.Contains(vm, "entrypoint =") || strings.Contains(vm, "command =") {
		t.Fatalf("GetPodDockerHaproxyTemplate() must not override entrypoint/command for haproxy-mode=runtimeapi:\n%s", vm)
	}
}

// TestGetPodDockerHaproxyTemplateStandbyUsesImageDefault is
// TestGetPodDockerHaproxyTemplateRuntimeAPIUsesImageDefault's
// haproxy-mode=standby counterpart -- see
// TestOpenSVCGetHaproxyContainerSectionStandbyUsesImageDefault for why.
func TestGetPodDockerHaproxyTemplateStandbyUsesImageDefault(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)

	cluster.Conf = &config.Config{
		HaproxyMode: "standby",
	}
	collector := opensvc.Collector{ProvProxMicroSrv: "docker"}

	vm := cluster.GetPodDockerHaproxyTemplate(collector, "01")

	if strings.Contains(vm, "entrypoint =") || strings.Contains(vm, "command =") {
		t.Fatalf("GetPodDockerHaproxyTemplate() must not override entrypoint/command for haproxy-mode=standby:\n%s", vm)
	}
}
