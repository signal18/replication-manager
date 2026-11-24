// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
	"github.com/signal18/replication-manager/config/credstore/vaultfake"
	"github.com/signal18/replication-manager/utils/state"
)

// The server store follows the instance's user list edited in /etc: the restart takes the
// file's new value as the current version (TakeConfigFileValue), the start's pass writes it to
// the server store, and that value is the one the clusters sign the users in with.
func TestServerKvFollowsAnEtcEditAtRestart(t *testing.T) {
	server := vaultfake.New("kv", "role", "sid")
	defer server.Close()
	repman := serverKvTestManager(t, true, server)
	repman.Conf.SetImmuableFlag(apiCredentialsKey, "admin:first,ops:second")
	repman.takeServerConfigFileCredentials()
	repman.serverKvFullPass()
	if repman.serverKvRunner().WritePass() {
		t.Fatalf("first pass: %v", repman.ServerKvBehind())
	}

	// the operator edits admin's password in /etc and restarts: same working directory, the
	// versioned file read again, the file's value seen as an edit
	repman.serverSecrets.versions = nil
	repman.credChain = nil
	repman.Conf.SetImmuableFlag(apiCredentialsKey, "admin:changed,ops:second")
	repman.takeServerConfigFileCredentials()
	repman.applyServerSecrets()
	repman.serverKvFullPass()
	if repman.serverKvRunner().WritePass() {
		t.Fatalf("pass after the restart: %v", repman.ServerKvBehind())
	}
	got := server.Get(serverKvFolder + "/" + apiCredentialsKey)
	if got == nil || got[apiCredentialsKey] != "admin:changed,ops:second" {
		t.Fatal("the server store does not hold the list edited in /etc")
	}
	for _, cl := range repman.Clusters {
		if _, err := cl.GetAPIUser("admin", "changed"); err != nil {
			t.Fatalf("%s: the value in the store is not the one admin signs in with: %v", cl.Name, err)
		}
		if _, err := cl.GetAPIUser("admin", "first"); err == nil {
			t.Fatalf("%s: the old password still signs in", cl.Name)
		}
	}
	if len(repman.ServerKvBehind()) != 0 {
		t.Fatalf("behind after the write: %v", repman.ServerKvBehind())
	}
}

// Before every cluster is initialised the server stores are not written: an orchestrator
// store's carrier is not listed yet, the write would fail with "not monitored here", every
// cluster would raise WARN0320 and the store would wait a whole secret-kv-retry-interval. The
// pass is only asked; the first tick, after the clusters are ready, writes it.
func TestServerKvStartWaitsForTheClusters(t *testing.T) {
	server := vaultfake.New("kv", "role", "sid")
	defer server.Close()
	repman := serverKvTestManager(t, true, server)
	repman.Conf.SecretKvRetryInterval = 1
	repman.serverSecretKv.running.Store(false) // the real worker
	t.Cleanup(func() { repman.exit.Store(true) })

	// InitConfig's pass and a change queued during the start: nothing runs yet
	repman.startServerKvPass()
	repman.queueServerVersion("mail-smtp-password")
	if repman.serverSecretKv.running.Load() {
		t.Fatal("the server stores' worker runs before the clusters are initialised")
	}
	if !repman.serverSecretKv.passDue.Load() {
		t.Fatal("the start's pass was lost")
	}
	if len(repman.ServerKvBehind()) != 0 {
		t.Fatalf("a store is behind before anything was tried: %v", repman.ServerKvBehind())
	}

	// the clusters are ready: the first tick writes at once, nothing is behind
	repman.clustersReady.Store(true)
	repman.serverSecretKv.wasActive.Store(true) // the pass is already due
	repman.checkServerSecretKvStores()
	deadline := time.Now().Add(5 * time.Second)
	for server.Get(serverKvFolder+"/"+apiCredentialsKey) == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := server.Get(serverKvFolder + "/" + apiCredentialsKey); got == nil || got[apiCredentialsKey] != "admin:first,ops:second" {
		t.Fatal("the first tick after the start did not write the server store")
	}
	if len(repman.ServerKvBehind()) != 0 {
		t.Fatalf("behind: %v", repman.ServerKvBehind())
	}
	if repman.StateMachine.CurState.Search("GWARN024@srv-a") {
		t.Fatal("GWARN024 at the start")
	}
}

// WARN0320 on a cluster follows the server store: open while it is behind, gone at the next
// tick once the store holds the current value.
func TestServerKvWarn0320ClearsOnceWritten(t *testing.T) {
	server := vaultfake.New("kv", "role", "sid")
	defer server.Close()
	repman := serverKvTestManager(t, true, server)
	cl := repman.Clusters["c1"]
	cl.SecurityStateMachine = new(state.StateMachine)
	cl.SecurityStateMachine.Init()
	cl.ConfigStateMachine = new(state.StateMachine)
	cl.ConfigStateMachine.Init()
	cl.ServerGlobals.ServerStoresBehind = repman.ServerKvBehind
	cl.Status = "A"

	server.SetFail(true)
	repman.serverKvFullPass()
	if !repman.serverKvRunner().WritePass() {
		t.Fatal("a failing store wrote")
	}
	cl.CheckSecretKvStores()
	if !cl.SecurityStateMachine.CurState.Search("WARN0320@srv-a") {
		t.Fatal("no WARN0320 while the store is behind")
	}

	server.SetFail(false)
	if repman.serverKvRunner().WritePass() {
		t.Fatalf("the store does not catch up: %v", repman.ServerKvBehind())
	}
	cl.SecurityStateMachine.ClearState()
	cl.CheckSecretKvStores()
	if cl.SecurityStateMachine.CurState.Search("WARN0320@srv-a") {
		t.Fatal("WARN0320 stays once the store holds the current value")
	}
}

// One secret, two keys in the orchestrator object: the instance's (<prefix>_api-credentials,
// the entry's place) and the API host's (<api host>_api-credentials), written from the same
// version. The list says both. When api-public-url names another host, the old host's key is
// never deleted: it gets a moved-to marker naming the new key, and a WARN says it is no
// longer updated; the host written is saved with the entry.
func TestServerKvAliasKeyFollowsTheHost(t *testing.T) {
	repman := serverKvTestManager(t, true)
	c := repman.Conf
	c.APIPublicURL = "https://repman-a.example.com"
	repman.Clusters["c1"].Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC
	repman.ensureServerOrchestratorStores()
	name := config.ConstSecretKvMarketplacePrefix + "opensvc"

	v := credstore.Version{Value: "admin:x", At: time.Now().UTC()}
	items := repman.serverKvItems(name, apiCredentialsKey, v, false)
	var paths []string
	for _, it := range items {
		if it.Version != v {
			t.Fatalf("the two keys from two versions: %+v", it)
		}
		paths = append(paths, it.Path)
	}
	if strings.Join(paths, ",") != "acme_repman1_paris,repman-a.example.com" {
		t.Fatalf("keys %v", paths)
	}
	if st := repman.serverSecretKvList().States[name]; !strings.Contains(st.Path, "acme_repman1_paris_<key>") || strings.Contains(st.Path, "api-credentials") || st.AliasPath != "repman-a.example.com_api-credentials" {
		t.Fatalf("the list does not say both keys, each in its field: %+v", st)
	}

	// the first write records the host, no marker
	repman.serverKvQueue().Take()
	repman.recordServerKvWrittenAs(name, items)
	if e, _ := c.SecretKvEntryAt(config.SecretKvLevelServer, name); e.AliasWrittenAs != "repman-a.example.com" || e.WrittenAs != "acme/repman1/paris" {
		t.Fatalf("written as %q, alias %q", e.WrittenAs, e.AliasWrittenAs)
	}
	if repman.serverKvQueue().Len() != 0 {
		t.Fatal("a marker at the first write")
	}

	// api-public-url changes: the old key is left with a marker, never deleted
	c.APIPublicURL = "https://repman-b.example.com"
	repman.recordServerKvWrittenAs(name, repman.serverKvItems(name, apiCredentialsKey, v, false))
	queued := repman.serverKvQueue().Take()
	markers := map[string]string{}
	for _, it := range queued {
		if it.Key != ConstServerKvMovedTo {
			t.Fatalf("not a marker: %+v", it)
		}
		markers[it.Path] = it.Version.Value
	}
	if len(markers) != 2 || markers["repman-a.example.com"] != "repman-b.example.com_api-credentials" || markers["repman-b.example.com"] != "repman-b.example.com_api-credentials" {
		t.Fatalf("markers %+v", queued)
	}
	// the new host is not saved before the old host's marker is written
	if e, _ := c.SecretKvEntryAt(config.SecretKvLevelServer, name); e.AliasWrittenAs != "repman-a.example.com" {
		t.Fatalf("saved before the marker was written: %q", e.AliasWrittenAs)
	}
	// a second write while the marker waits queues nothing more (one WARN per move)
	itemsB := repman.serverKvItems(name, apiCredentialsKey, v, false)
	repman.recordServerKvWrittenAs(name, itemsB)
	if repman.serverKvQueue().Len() != 0 {
		t.Fatal("the markers queued twice")
	}
	// a restart before the marker is written: the queue and the waiting move are lost (memory),
	// the saved host is still the old one, so the next write queues the markers again
	repman.serverSecretKv.mu.Lock()
	repman.serverSecretKv.aliasPending = nil
	repman.serverSecretKv.mu.Unlock()
	repman.recordServerKvWrittenAs(name, itemsB)
	if queued = repman.serverKvQueue().Take(); len(queued) != 2 {
		t.Fatalf("markers after a restart %+v", queued)
	}
	// the old host's marker written: the new host is saved
	repman.recordServerKvWrittenAs(name, queued)
	e, _ := c.SecretKvEntryAt(config.SecretKvLevelServer, name)
	if e.AliasWrittenAs != "repman-b.example.com" {
		t.Fatalf("alias %q", e.AliasWrittenAs)
	}
	out, err := c.SecretKvsTomlAt(config.SecretKvLevelServer, "saved-default")
	if err != nil || !strings.Contains(out, `alias-written-as = "repman-b.example.com"`) {
		t.Fatalf("not saved:\n%s", out)
	}

	// back to the first host (A to B to A): A's marker names A's key again, so it does not
	// keep saying the list moved to B; B's says the list is under A
	c.APIPublicURL = "https://repman-a.example.com"
	repman.recordServerKvWrittenAs(name, repman.serverKvItems(name, apiCredentialsKey, v, false))
	markers = map[string]string{}
	queued = repman.serverKvQueue().Take()
	for _, it := range queued {
		markers[it.Path] = it.Version.Value
	}
	if markers["repman-a.example.com"] != "repman-a.example.com_api-credentials" || markers["repman-b.example.com"] != "repman-a.example.com_api-credentials" {
		t.Fatalf("flip-back markers %+v", markers)
	}
	repman.recordServerKvWrittenAs(name, queued)
	if e, _ := c.SecretKvEntryAt(config.SecretKvLevelServer, name); e.AliasWrittenAs != "repman-a.example.com" {
		t.Fatalf("flip-back saved %q", e.AliasWrittenAs)
	}
}
