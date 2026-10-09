// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this source distribution for more information.

package cluster

import (
	"errors"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func newBootstrapDRTestCluster(writes *[]string, fail *bool) *Cluster {
	cl := &Cluster{Name: "tamarin", Conf: &config.Config{ProvType: "docker"}}
	cl.bootstrapDR.writer = func(key, value string) error {
		if *fail {
			return errors.New("orchestrator unreachable")
		}
		*writes = append(*writes, key+"="+value)
		return nil
	}
	return cl
}

// Without active/standby (no DR URL) nothing is written nor mapped, and the
// command still works on the main URL alone.
func TestBootstrapDRURLNotMappedWithoutPeer(t *testing.T) {
	var writes []string
	fail := false
	cl := newBootstrapDRTestCluster(&writes, &fail)

	section := cl.OpenSVCGetInitContainerSection("3306")
	if strings.Contains(section["configs_environment"], bootstrapDRURLKey) {
		t.Fatalf("DR key mapped without a DR URL: %q", section["configs_environment"])
	}
	if len(writes) != 0 {
		t.Fatalf("namespace written without a DR URL: %v", writes)
	}
	if !strings.Contains(section["command"], "$REPLICATION_MANAGER_URL $REPLICATION_MANAGER_URL_DR") {
		t.Fatalf("init command does not try both URLs: %q", section["command"])
	}
}

// The key is written before it is mapped, once per value.
func TestBootstrapDRURLWrittenOncePerValueThenMapped(t *testing.T) {
	var writes []string
	fail := false
	cl := newBootstrapDRTestCluster(&writes, &fail)

	cl.SetBootstrapDRURL("https://repman.s18.svc.cloud18:10005 https://repman-dr.s18.svc.cloud18:10005")
	for i := 0; i < 3; i++ {
		section := cl.OpenSVCGetInitContainerSection("3306")
		if !strings.Contains(section["configs_environment"], "env/"+bootstrapDRURLKey) {
			t.Fatalf("render %d: DR key not mapped: %q", i, section["configs_environment"])
		}
	}
	if len(writes) != 1 || writes[0] != bootstrapDRURLKey+"=https://repman.s18.svc.cloud18:10005 https://repman-dr.s18.svc.cloud18:10005" {
		t.Fatalf("writes = %v, want the DR URL written once", writes)
	}

	cl.SetBootstrapDRURL("https://repman-dr2.s18.svc.cloud18:10005")
	cl.OpenSVCGetInitContainerSection("3306")
	if len(writes) != 2 {
		t.Fatalf("a new DR URL must be written again: %v", writes)
	}
}

// A failed write never maps a key that may not exist (a missing key fails the
// start), and the next render tries again.
func TestBootstrapDRURLFailedWriteNotMapped(t *testing.T) {
	var writes []string
	fail := true
	cl := newBootstrapDRTestCluster(&writes, &fail)
	cl.SetBootstrapDRURL("https://repman.s18.svc.cloud18:10005 https://repman-dr.s18.svc.cloud18:10005")

	section := cl.OpenSVCGetInitContainerSection("3306")
	if strings.Contains(section["configs_environment"], bootstrapDRURLKey) {
		t.Fatalf("DR key mapped after a failed write: %q", section["configs_environment"])
	}
	fail = false
	section = cl.OpenSVCGetInitContainerSection("3306")
	if !strings.Contains(section["configs_environment"], bootstrapDRURLKey) || len(writes) != 1 {
		t.Fatalf("retry after a failed write: mapped=%q writes=%v", section["configs_environment"], writes)
	}
}

// Kubernetes: the pair's URLs are baked into the init container, which picks
// the first that answers; without them the command is unchanged.
func TestK8SInitContainerFallsBackToDR(t *testing.T) {
	cl := newTestCluster("k8stest")
	cl.Conf.ProvDbImg = "mariadb:10.11"
	cl.Conf.ApiServ = true
	cl.Conf.MonitorAddress = "repman.s18.svc.cloud18"
	cl.Conf.APIPort = "10005"
	s := &ServerMonitor{Name: "db1", Port: "3306", Pass: "secret"}

	initCmd := func() string {
		return strings.Join(cl.k8sDatabaseDeployment(s, 3306, "node-a").Spec.Template.Spec.InitContainers[0].Command, " ")
	}
	without := initCmd()
	if strings.Contains(without, "$B") || strings.Contains(without, "/api/version") {
		t.Fatalf("command changed without DR URLs: %s", without)
	}

	cl.SetBootstrapDRURL("https://repman.s18.svc.cloud18:10005 https://repman-dr.s18.svc.cloud18:10005")
	with := initCmd()
	if !strings.Contains(with, "for u in https://repman.s18.svc.cloud18:10005 https://repman-dr.s18.svc.cloud18:10005 ;") {
		t.Fatalf("pair not tried in order without duplicates: %s", with)
	}
	if !strings.Contains(with, "$B/api/clusters/k8stest/servers/") || !strings.Contains(with, "$B/static/configurator/bin/replication-manager-cli") {
		t.Fatalf("config and CLI fetches do not use the picked URL: %s", with)
	}
}

// On-premise: the provisioning command tries the main URL, then each DR URL
// once, and the SSH environment carries REPLICATION_MANAGER_URL_DR.
func TestOnPremiseBootstrapCommandAndEnvCarryDR(t *testing.T) {
	cmd := onPremiseBootstrapCommand("repository/debian/mariadb")
	for _, want := range []string{
		"for u in $REPLICATION_MANAGER_URL $REPLICATION_MANAGER_URL_DR",
		"$u/static/configurator/onpremise/repository/debian/mariadb/bootstrap",
		"REPLICATION_MANAGER_URL=$u sh /tmp/replication-manager-bootstrap",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("command misses %q: %s", want, cmd)
		}
	}

	cl := newTestCluster("onprem")
	cl.SetBootstrapDRURL("https://a:10005 https://b:10005")
	s := &ServerMonitor{Host: "db1", Port: "3306", ClusterGroup: cl}
	if env := s.GetSshEnv(); !strings.Contains(env, "export REPLICATION_MANAGER_URL_DR='https://a:10005 https://b:10005'") {
		t.Fatalf("SSH env has no DR URLs: %s", env)
	}
}
