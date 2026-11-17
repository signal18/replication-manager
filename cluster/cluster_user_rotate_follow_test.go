// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/base64"
	"testing"

	"github.com/signal18/replication-manager/config"
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
