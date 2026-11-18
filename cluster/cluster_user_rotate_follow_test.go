// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
)

// An admin password change reaches the init containers' login on OpenSVC: the env secret's
// REPLICATION_MANAGER_PASSWORD gets the new value, the old one in _OLD. An unchanged password is
// not written again.
func TestFollowAdminPasswordOpenSVC(t *testing.T) {
	agent := &kvAgent{keys: map[string]string{"REPLICATION_MANAGER_PASSWORD": "old-pw"}}
	cl := kvAgentCluster(t, agent)
	cl.APIUsers = map[string]APIUser{"admin": {User: "admin", Password: "new-pw"}}
	cl.exit.Store(true) // no background runner: the test drives the write pass
	cl.followAdminPassword("old-pw")
	cl.secretWritePass()
	v, _ := agent.get("REPLICATION_MANAGER_PASSWORD")
	old, _ := agent.get("REPLICATION_MANAGER_PASSWORD_OLD")
	if v != "new-pw" || old != "old-pw" {
		t.Fatalf("env secret not followed: value %q, _OLD %q", v, old)
	}
	// unchanged: nothing queued
	cl.followAdminPassword("new-pw")
	if n := cl.storeQueue().Len(); n != 0 {
		t.Fatalf("an unchanged admin password queued %d item(s)", n)
	}
}

// On Kubernetes the init containers log in with REPMAN_AUTH_HEADER (Basic admin:<password>) in
// <cluster>-secret, when api-credentials-secure-config is on; off, there is nothing to follow.
func TestFollowAdminPasswordKubernetes(t *testing.T) {
	cl := secretKvTestCluster(t)
	cl.Conf.ProvOrchestrator = config.ConstOrchestratorKubernetes
	cl.APIUsers = map[string]APIUser{"admin": {User: "admin", Password: "new-pw"}}
	cl.exit.Store(true) // no background runner: the queue is read as it is

	cl.followAdminPassword("old-pw") // secure config off: the init sends no header
	if items := cl.storeQueue().Take(); len(items) != 0 {
		t.Fatalf("queued without secure config: %+v", items)
	}

	cl.Conf.APISecureConfig = true
	cl.followAdminPassword("old-pw")
	want := base64.StdEncoding.EncodeToString([]byte("admin:new-pw"))
	found := false
	for _, it := range cl.storeQueue().Take() {
		if it.Entry == orchestratorStoreEntry && it.Key == k8sSecretKeyAPIAuthHeader && it.Version.Value == want {
			found = true
		}
	}
	if !found {
		t.Fatal("REPMAN_AUTH_HEADER not queued with the new admin password")
	}
}

// fakeOrchestratorSecret is an orchestrator secret for the init credential repair: ReadCurrent
// only (the writes go to the queue, read back by the test).
type fakeOrchestratorSecret struct {
	keys map[string]string
	err  error
}

func (f *fakeOrchestratorSecret) Name() string { return "fake" }
func (f *fakeOrchestratorSecret) WriteAll(_ context.Context, items []credstore.StoreItem, _ credstore.Timeout) ([]credstore.StoreItem, error) {
	return items, errors.New("not used")
}
func (f *fakeOrchestratorSecret) ReadCurrent(key string) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	v, ok := f.keys[key]
	return v, ok, nil
}

func queuedKeys(cl *Cluster) map[string]string {
	out := map[string]string{}
	for _, it := range cl.storeQueue().Take() {
		if it.Entry == orchestratorStoreEntry {
			out[it.Key] = it.Version.Value
		}
	}
	return out
}

// At activation the init and jobs logins of the orchestrator secret are repaired: a stale
// REPLICATION_MANAGER_PASSWORD is written again, a current one is left, a missing one is not
// added (no definition maps it), a missing jobs key is published. An unreadable secret is not an
// answer: read again later.
func TestRepairInitCredentialsOpenSVC(t *testing.T) {
	cl := secretKvTestCluster(t)
	cl.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC
	cl.APIUsers = map[string]APIUser{"admin": {User: "admin", Password: "cur-pw"}}
	cl.exit.Store(true)
	jk := cl.GetJobsKey()
	if jk == "" {
		t.Fatal("the test cluster has no jobs key")
	}

	stale := &fakeOrchestratorSecret{keys: map[string]string{"REPLICATION_MANAGER_PASSWORD": "old-pw"}}
	if !cl.repairInitCredentials(stale) {
		t.Fatal("an answered read reported unanswered")
	}
	q := queuedKeys(cl)
	if q["REPLICATION_MANAGER_PASSWORD"] != "cur-pw" || q["JOBS_API_KEY"] != jk {
		t.Fatalf("stale secret: queued %v", len(q))
	}

	current := &fakeOrchestratorSecret{keys: map[string]string{"REPLICATION_MANAGER_PASSWORD": "cur-pw", "JOBS_API_KEY": jk}}
	cl.repairInitCredentials(current)
	if q := queuedKeys(cl); len(q) != 0 {
		t.Fatalf("a current secret queued %d key(s)", len(q))
	}

	noLogin := &fakeOrchestratorSecret{keys: map[string]string{"JOBS_API_KEY": jk}}
	cl.repairInitCredentials(noLogin)
	if q := queuedKeys(cl); len(q) != 0 {
		t.Fatalf("a namespace without an init login got one: %d key(s)", len(q))
	}

	down := &fakeOrchestratorSecret{err: errors.New("agent unreachable")}
	if cl.repairInitCredentials(down) {
		t.Fatal("an unreachable agent counted as an answer")
	}
	if q := queuedKeys(cl); len(q) != 0 {
		t.Fatalf("an unreadable secret queued %d key(s)", len(q))
	}
}

// Kubernetes: the init login is REPMAN_AUTH_HEADER, repaired where present.
func TestRepairInitCredentialsKubernetes(t *testing.T) {
	cl := secretKvTestCluster(t)
	cl.Conf.ProvOrchestrator = config.ConstOrchestratorKubernetes
	cl.Conf.APISecureConfig = true
	cl.APIUsers = map[string]APIUser{"admin": {User: "admin", Password: "cur-pw"}}
	cl.exit.Store(true)
	stale := &fakeOrchestratorSecret{keys: map[string]string{k8sSecretKeyAPIAuthHeader: "b2xk", "JOBS_API_KEY": cl.GetJobsKey()}}
	cl.repairInitCredentials(stale)
	q := queuedKeys(cl)
	if q[k8sSecretKeyAPIAuthHeader] != base64.StdEncoding.EncodeToString([]byte("admin:cur-pw")) || len(q) != 1 {
		t.Fatalf("queued %d key(s)", len(q))
	}
}
