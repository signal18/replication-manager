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
