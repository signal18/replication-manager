// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
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

// TestServerOrchestratorStore: a server entry writes through a cluster's connection only when
// the cluster is on that orchestrator, to the server's object, never the cluster's env secret.
func TestServerOrchestratorStore(t *testing.T) {
	osvc := &Cluster{Name: "c1", Conf: &config.Config{ProvOrchestrator: config.ConstOrchestratorOpenSVC}}
	k8s := &Cluster{Name: "c2", Conf: &config.Config{ProvOrchestrator: config.ConstOrchestratorKubernetes}}
	w, err := osvc.ServerOrchestratorStore(config.ConstSecretKvPluginOpenSVC, "")
	if err != nil {
		t.Fatal(err)
	}
	s := w.(*openSVCStore)
	if s.namespace != "system" || s.object != "repman" || !s.prefixed {
		t.Fatalf("OpenSVC server object: %+v", s)
	}
	if _, err := k8s.ServerOrchestratorStore(config.ConstSecretKvPluginOpenSVC, ""); err == nil {
		t.Fatal("an OpenSVC store through a Kubernetes cluster")
	}
	if _, err := osvc.ServerOrchestratorStore(config.ConstSecretKvPluginKubernetes, "infra"); err == nil {
		t.Fatal("a Kubernetes store through an OpenSVC cluster")
	}
	if _, err := osvc.ServerOrchestratorStore(config.ConstSecretKvPluginVault, ""); err == nil {
		t.Fatal("a vault entry through an orchestrator")
	}
	// the cluster's own sink keeps its env object and unprefixed keys
	if own := osvc.openSVCStore(); own.namespace != "c1" || own.object != "env" || own.prefixed {
		t.Fatalf("cluster env sink changed: %+v", own)
	}
	if storeKey(true, credstore.StoreItem{Path: "acme_host_paris", Key: "api-credentials"}) != "acme_host_paris_api-credentials" {
		t.Fatal("server key")
	}
	if storeKey(false, credstore.StoreItem{Path: "x", Key: "MYSQL_ROOT_PASSWORD"}) != "MYSQL_ROOT_PASSWORD" {
		t.Fatal("cluster key")
	}
}

// TestServerKubernetesStoreWritesItsSecretOnly: the server's keys land in repman-system of the
// entry's namespace, prefixed with the instance's name; the cluster's own Secret is untouched.
func TestServerKubernetesStoreWritesItsSecretOnly(t *testing.T) {
	cl := &Cluster{Name: "c2", Conf: &config.Config{ProvOrchestrator: config.ConstOrchestratorKubernetes}}
	w, err := cl.ServerOrchestratorStore(config.ConstSecretKvPluginKubernetes, "infra")
	if err != nil {
		t.Fatal(err)
	}
	client := fake.NewSimpleClientset()
	items := []credstore.StoreItem{{Entry: "k", Path: "acme_host_paris", Key: "api-credentials", Version: credstore.Version{Value: "admin:x"}}}
	if failed, err := w.(*k8sStore).writeWith(context.Background(), client, items, credstore.Timeout(5*time.Second)); err != nil || len(failed) != 0 {
		t.Fatalf("write: %v %v", failed, err)
	}
	sec, err := client.CoreV1().Secrets("infra").Get(context.Background(), "repman-system", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sec.StringData["acme_host_paris_api-credentials"] != "admin:x" || len(sec.StringData) != 1 {
		t.Fatalf("keys written: %v", keysOf(sec.StringData))
	}
	if _, err := client.CoreV1().Secrets("c2").Get(context.Background(), k8sClusterSecretName("c2"), metav1.GetOptions{}); err == nil {
		t.Fatal("the cluster's own Secret was written")
	}
}

func keysOf(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
