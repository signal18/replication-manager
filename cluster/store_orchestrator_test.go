// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// TestOrchestratorOldAndSkip covers the one-deep history + unchanged-skip rule shared by both
// orchestrator sinks (#2356).
func TestOrchestratorOldAndSkip(t *testing.T) {
	cases := []struct {
		name                string
		key, newVal, curVal string
		curKnown            bool
		wantSkip, wantOld   bool
	}{
		{"current unknown -> write, no _OLD", "MYSQL_ROOT_PASSWORD", "new", "", false, false, false},
		{"_OLD slot never gets its own _OLD", "MYSQL_ROOT_PASSWORD" + oldKeySuffix, "x", "y", true, false, false},
		{"unchanged -> skip", "MYSQL_ROOT_PASSWORD", "same", "same", true, true, false},
		{"changed with prior value -> write + _OLD", "MYSQL_ROOT_PASSWORD", "new", "old", true, false, true},
		{"changed, empty prior -> write, no _OLD", "MYSQL_ROOT_PASSWORD", "new", "", true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			skip, writeOld := orchestratorOldAndSkip(c.key, c.newVal, c.curVal, c.curKnown)
			if skip != c.wantSkip || writeOld != c.wantOld {
				t.Fatalf("orchestratorOldAndSkip(%q, %q, %q, %v) = (skip=%v, writeOld=%v); want (skip=%v, writeOld=%v)",
					c.key, c.newVal, c.curVal, c.curKnown, skip, writeOld, c.wantSkip, c.wantOld)
			}
		})
	}
}

// TestOrchestratorStoreWriterSelection checks the sink is chosen from prov-orchestrator, and
// that an orchestrator-less cluster has no sink (its credentials are never copied anywhere).
func TestOrchestratorStoreWriterSelection(t *testing.T) {
	cases := []struct {
		name    string
		orch    string
		wantNil bool
		wantK8s bool
	}{
		{"opensvc", config.ConstOrchestratorOpenSVC, false, false},
		{"kubernetes", config.ConstOrchestratorKubernetes, false, true},
		{"localhost", config.ConstOrchestratorLocalhost, true, false},
		{"onpremise", config.ConstOrchestratorOnPremise, true, false},
		{"unset", "", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl := &Cluster{Name: "t", Conf: &config.Config{ProvOrchestrator: c.orch}}
			w := cl.orchestratorStoreWriter()
			if c.wantNil {
				if w != nil {
					t.Fatalf("orchestrator %q: want nil sink, got %T", c.orch, w)
				}
				return
			}
			if w == nil {
				t.Fatalf("orchestrator %q: want a sink, got nil", c.orch)
			}
			if _, isK8s := w.(*k8sStore); isK8s != c.wantK8s {
				t.Fatalf("orchestrator %q: got %T, wantK8s=%v", c.orch, w, c.wantK8s)
			}
		})
	}
}
