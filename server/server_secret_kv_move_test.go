// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore/vaultfake"
)

const movedKvFolder = "system/repman/acme/repman2/paris"

// serverKvWrittenAs is the name entry records.
func serverKvWrittenAs(repman *ReplicationManager, entry string) string {
	for _, e := range repman.Conf.SecretKvListAt(config.SecretKvLevelServer) {
		if e.Name == entry {
			return e.WrittenAs
		}
	}
	return ""
}

// A renamed instance: every kept version goes to the new place, oldest first, the old place
// keeps them and names the new one; until then the ordinary writes wait.
func TestServerKvMoveOnRename(t *testing.T) {
	server := vaultfake.New("kv", "role", "sid")
	defer server.Close()
	repman := serverKvTestManager(t, true, server)
	for _, v := range []string{"smtp-1", "smtp-2"} {
		if err := repman.CredentialChain().Set("mail-smtp-password", v); err != nil {
			t.Fatal(err)
		}
	}
	repman.serverKvQueue().Take()
	repman.serverKvFullPass()
	if repman.serverKvRunner().WritePass() {
		t.Fatalf("first write: %v", repman.ServerKvBehind())
	}
	if got := serverKvWrittenAs(repman, "srv-a"); got != "acme/repman1/paris" {
		t.Fatalf("written as %q", got)
	}
	kept := len(repman.serverVersionStore().Versions("mail-smtp-password"))

	// renamed (a cloud18-* name setting changed)
	repman.Conf.Cloud18SubDomain = "repman2"
	if err := repman.CredentialChain().Set("mail-smtp-password", "smtp-3"); err != nil {
		t.Fatal(err)
	}
	repman.serverKvRunner().WritePass()
	if p := server.Paths(); strings.Contains(strings.Join(p, " "), "repman2") {
		t.Fatalf("written at the new place before the move: %v", p)
	}

	// the store fails: behind, the move goes on later from where it stopped
	server.SetFail(true)
	e, _ := repman.Conf.SecretKvActiveEntryAt(config.SecretKvLevelServer, "srv-a")
	m := &serverKvMove{to: "acme/repman2/paris", place: repman.Conf.SecretKvPlace(e), written: map[string]bool{}, running: true}
	repman.serverSecretKv.mu.Lock()
	repman.serverSecretKv.moves = map[string]*serverKvMove{"srv-a": m}
	repman.serverSecretKv.mu.Unlock()
	repman.moveServerKv(e, m)
	if why := repman.ServerKvBehind()["srv-a"]; !strings.Contains(why, "moving from acme/repman1/paris to acme/repman2/paris") {
		t.Fatalf("behind %q", why)
	}
	if got := serverKvWrittenAs(repman, "srv-a"); got != "acme/repman1/paris" {
		t.Fatalf("moved while failing: %q", got)
	}

	server.SetFail(false)
	repman.serverSecretKv.mu.Lock()
	m.running = true
	repman.serverSecretKv.mu.Unlock()
	repman.moveServerKv(e, m)
	if why := repman.ServerKvBehind()["srv-a"]; why != "" {
		t.Fatalf("still behind: %q", why)
	}
	if got := serverKvWrittenAs(repman, "srv-a"); got != "acme/repman2/paris" {
		t.Fatalf("written as %q after the move", got)
	}
	got := server.Get(movedKvFolder + "/mail-smtp-password")
	if got == nil || got["mail-smtp-password"] != "smtp-3" {
		t.Fatalf("the newest is not the current value at the new place: %v", server.Paths())
	}
	if n := server.Versions(movedKvFolder + "/mail-smtp-password"); n != kept+1 {
		t.Fatalf("versions at the new place: %d, want %d", n, kept+1)
	}
	if got := server.Get(movedKvFolder + "/api-credentials"); got == nil {
		t.Fatal("api-credentials not moved")
	}
	marker := server.Get(serverKvFolder + "/" + ConstServerKvMovedTo)
	if marker == nil || marker[ConstServerKvMovedTo] != "acme/repman2/paris" {
		t.Fatalf("no moved-to at the old place: %v", marker)
	}
	if server.Get(serverKvFolder+"/mail-smtp-password") == nil {
		t.Fatal("the old place lost what it held")
	}
	// the ordinary writes go to the new place again
	if err := repman.CredentialChain().Set("mail-smtp-password", "smtp-4"); err != nil {
		t.Fatal(err)
	}
	repman.serverKvRunner().WritePass()
	if got := server.Get(movedKvFolder + "/mail-smtp-password"); got["mail-smtp-password"] != "smtp-4" {
		t.Fatal("no write after the move")
	}
}

// The name an entry holds the secrets under is saved, and forgotten when its place changes
// (not when only its login does).
func TestServerKvWrittenAsSavedAndForgotten(t *testing.T) {
	conf := serverLevelConf(t.TempDir(), "")
	conf.ServerSecretKvs = []config.SecretKvConfig{{Name: "srv", Plugin: config.ConstSecretKvPluginVault, Enabled: true, WrittenAs: "acme/h/paris",
		Settings: map[string]interface{}{"address": "https://vault.example", "path-prefix": "p"}}}
	out, err := conf.SecretKvsTomlAt(config.SecretKvLevelServer, "saved-default")
	if err != nil || !strings.Contains(out, `written-as = "acme/h/paris"`) {
		t.Fatalf("not saved:\n%s", out)
	}
	if _, err := conf.PutSecretKvAt(config.SecretKvLevelServer, config.SecretKvSpec{Name: "srv", Settings: map[string]interface{}{"role-id": "r2"}}, false); err != nil {
		t.Fatal(err)
	}
	if conf.SecretKvListAt(config.SecretKvLevelServer)[0].WrittenAs != "acme/h/paris" {
		t.Fatal("a login change forgot the place")
	}
	if _, err := conf.PutSecretKvAt(config.SecretKvLevelServer, config.SecretKvSpec{Name: "srv", Settings: map[string]interface{}{"path-prefix": "q"}}, false); err != nil {
		t.Fatal(err)
	}
	if conf.SecretKvListAt(config.SecretKvLevelServer)[0].WrittenAs != config.ConstSecretKvWrittenAsNewPlace {
		t.Fatal("a new prefix kept the old place")
	}
}

// serverKvMovedSetup is a store written under acme/repman1/paris, the instance then renamed to
// acme/repman2/paris.
func serverKvMovedSetup(t *testing.T, server *vaultfake.Vault) (*ReplicationManager, config.SecretKvConfig) {
	t.Helper()
	repman := serverKvTestManager(t, true, server)
	repman.serverKvQueue().Take()
	repman.serverKvFullPass()
	if repman.serverKvRunner().WritePass() {
		t.Fatalf("first write: %v", repman.ServerKvBehind())
	}
	repman.Conf.Cloud18SubDomain = "repman2"
	e, _ := repman.Conf.SecretKvActiveEntryAt(config.SecretKvLevelServer, "srv-a")
	return repman, e
}

// Renamed again during a move: the running move is never replaced nor doubled; it stops and
// commits nothing, and the next one goes to the newest name.
func TestServerKvMoveRenamedAgain(t *testing.T) {
	server := vaultfake.New("kv", "role", "sid")
	defer server.Close()
	repman, e := serverKvMovedSetup(t, server)
	repman.serverSecretKv.passDue.Store(false)
	m := &serverKvMove{to: "acme/repman2/paris", place: repman.Conf.SecretKvPlace(e), written: map[string]bool{}, running: true}
	repman.serverSecretKv.mu.Lock()
	repman.serverSecretKv.moves = map[string]*serverKvMove{"srv-a": m}
	repman.serverSecretKv.mu.Unlock()
	repman.Conf.Cloud18SubDomain = "repman3"
	repman.checkServerKvMoves()
	repman.serverSecretKv.mu.Lock()
	same, cancelled := repman.serverSecretKv.moves["srv-a"] == m, m.cancelled
	repman.serverSecretKv.mu.Unlock()
	if !same || !cancelled {
		t.Fatalf("the running move was replaced (%v) or not cancelled (%v)", !same, cancelled)
	}
	repman.moveServerKv(e, m)
	if got := serverKvWrittenAs(repman, "srv-a"); got != "acme/repman1/paris" {
		t.Fatalf("a cancelled move committed: %q", got)
	}
	if strings.Contains(strings.Join(server.Paths(), " "), "repman2") {
		t.Fatalf("the cancelled move wrote: %v", server.Paths())
	}
	// the next move goes to the newest name
	m2 := &serverKvMove{to: "acme/repman3/paris", place: repman.Conf.SecretKvPlace(e), written: map[string]bool{}, running: true}
	repman.serverSecretKv.mu.Lock()
	repman.serverSecretKv.moves = map[string]*serverKvMove{"srv-a": m2}
	repman.serverSecretKv.mu.Unlock()
	repman.moveServerKv(e, m2)
	if got := serverKvWrittenAs(repman, "srv-a"); got != "acme/repman3/paris" {
		t.Fatalf("written as %q", got)
	}
}

// The entry's place edited during the move: the move does not say the new place holds the
// secrets.
func TestServerKvMoveEditedMeanwhile(t *testing.T) {
	server := vaultfake.New("kv", "role", "sid")
	defer server.Close()
	repman, e := serverKvMovedSetup(t, server)
	if _, err := repman.Conf.PutSecretKvAt(config.SecretKvLevelServer, config.SecretKvSpec{Name: "srv-a", Settings: map[string]interface{}{"path-prefix": "other"}}, false); err != nil {
		t.Fatal(err)
	}
	m := &serverKvMove{to: "acme/repman2/paris", place: repman.Conf.SecretKvPlace(e), written: map[string]bool{}, running: true}
	repman.moveServerKv(e, m)
	if got := serverKvWrittenAs(repman, "srv-a"); got != config.ConstSecretKvWrittenAsNewPlace {
		t.Fatalf("recorded as moved after an edit: %q", got)
	}
}

// A write queued under the old name and done after the rename records nothing.
func TestServerKvWrittenAsFromWhereItWent(t *testing.T) {
	server := vaultfake.New("kv", "role", "sid")
	defer server.Close()
	repman := serverKvTestManager(t, true, server)
	e, _ := repman.Conf.SecretKvActiveEntryAt(config.SecretKvLevelServer, "srv-a")
	old := repman.serverKvItems("srv-a", "mail-smtp-password", repman.serverSecretVersion("api-credentials"), false)
	repman.Conf.Cloud18SubDomain = "repman2"
	repman.recordServerKvWrittenAs(e.Name, old)
	if got := serverKvWrittenAs(repman, "srv-a"); got != "" {
		t.Fatalf("recorded the new name for a write at the old place: %q", got)
	}
	repman.recordServerKvWrittenAs(e.Name, repman.serverKvItems("srv-a", "api-credentials", repman.serverSecretVersion("api-credentials"), false))
	if got := serverKvWrittenAs(repman, "srv-a"); got != "acme/repman2/paris" {
		t.Fatalf("written as %q", got)
	}
}

// A failed move left over is never reused once the entry's place changed: what it had written
// says nothing of another place, a fresh move takes the history there.
func TestServerKvStaleMoveDropped(t *testing.T) {
	server := vaultfake.New("kv", "role", "sid")
	defer server.Close()
	repman, e := serverKvMovedSetup(t, server)
	server.SetFail(true)
	m := &serverKvMove{to: "acme/repman2/paris", place: repman.Conf.SecretKvPlace(e), written: map[string]bool{}, running: true}
	repman.serverSecretKv.mu.Lock()
	repman.serverSecretKv.moves = map[string]*serverKvMove{"srv-a": m}
	repman.serverSecretKv.mu.Unlock()
	repman.moveServerKv(e, m)
	repman.serverSecretKv.mu.Lock()
	kept := repman.serverSecretKv.moves["srv-a"] == m
	repman.serverSecretKv.mu.Unlock()
	if !kept {
		t.Fatal("a failed move was not kept for its retry")
	}
	if _, err := repman.Conf.PutSecretKvAt(config.SecretKvLevelServer, config.SecretKvSpec{Name: "srv-a", Settings: map[string]interface{}{"path-prefix": "other"}}, false); err != nil {
		t.Fatal(err)
	}
	server.SetFail(false)
	repman.checkServerKvMoves()
	repman.serverSecretKv.mu.Lock()
	now := repman.serverSecretKv.moves["srv-a"]
	repman.serverSecretKv.mu.Unlock()
	// the place changed: the history goes to the new place, by a fresh move whose progress
	// starts empty, never the stale one
	if now == m {
		t.Fatal("a stale move outlived the entry's place")
	}
	cur, _ := repman.Conf.SecretKvActiveEntryAt(config.SecretKvLevelServer, "srv-a")
	if now != nil && now.place != repman.Conf.SecretKvPlace(cur) {
		t.Fatalf("the new move writes to %q", now.place)
	}
}

// A store whose place changed (an edit, an offer that moved it) gets every kept version there,
// oldest first, with no moved-to marker: the history follows, as for a rename.
func TestServerKvNewPlaceGetsTheHistory(t *testing.T) {
	server := vaultfake.New("kv", "role", "sid")
	defer server.Close()
	repman := serverKvTestManager(t, true, server)
	for _, v := range []string{"smtp-1", "smtp-2", "smtp-3"} {
		if err := repman.CredentialChain().Set("mail-smtp-password", v); err != nil {
			t.Fatal(err)
		}
	}
	repman.serverKvQueue().Take()
	repman.serverKvFullPass()
	if repman.serverKvRunner().WritePass() {
		t.Fatalf("first write: %v", repman.ServerKvBehind())
	}
	kept := len(repman.serverVersionStore().Versions("mail-smtp-password"))
	// the entry's prefix edited: another place
	if _, err := repman.Conf.PutSecretKvAt(config.SecretKvLevelServer, config.SecretKvSpec{Name: "srv-a", Settings: map[string]interface{}{"path-prefix": "newplace"}}, false); err != nil {
		t.Fatal(err)
	}
	if got := serverKvWrittenAs(repman, "srv-a"); got != config.ConstSecretKvWrittenAsNewPlace {
		t.Fatalf("written-as after the edit %q", got)
	}
	name, _ := repman.Conf.InstanceStoreName()
	e, _ := repman.Conf.SecretKvActiveEntryAt(config.SecretKvLevelServer, "srv-a")
	m := &serverKvMove{to: name, place: repman.Conf.SecretKvPlace(e), written: map[string]bool{}, running: true}
	repman.moveServerKv(e, m)
	if got := serverKvWrittenAs(repman, "srv-a"); got != name {
		t.Fatalf("written-as %q, want %q", got, name)
	}
	folder := repman.Conf.ServerSecretKvFolder(e, name)
	if n := server.Versions(folder + "/mail-smtp-password"); n != kept {
		t.Fatalf("history at the new place: %d versions, %d kept", n, kept)
	}
	if got := server.Get(folder + "/mail-smtp-password"); got == nil || got["mail-smtp-password"] != "smtp-3" {
		t.Fatalf("newest at the new place: %v", got)
	}
	for _, path := range server.Paths() {
		if strings.HasSuffix(path, "/"+ConstServerKvMovedTo) {
			t.Fatalf("a moved-to marker at %s for a new place", path)
		}
	}
}

// The commit compares the place: an entry already at "(new place)" edited to another place
// after the move wrote its versions is never recorded as holding the secrets there.
func TestServerKvCommitComparesThePlace(t *testing.T) {
	conf := serverLevelConf(t.TempDir(), "")
	conf.ServerSecretKvs = []config.SecretKvConfig{{Name: "srv", Plugin: config.ConstSecretKvPluginVault, Enabled: true, WrittenAs: "acme/h/paris",
		Settings: map[string]interface{}{"address": "https://vault.example", "path-prefix": "a"}}}
	if _, err := conf.PutSecretKvAt(config.SecretKvLevelServer, config.SecretKvSpec{Name: "srv", Settings: map[string]interface{}{"path-prefix": "b"}}, false); err != nil {
		t.Fatal(err)
	}
	e, _ := conf.SecretKvActiveEntryAt(config.SecretKvLevelServer, "srv")
	placeB := conf.SecretKvPlace(e)
	// edited again while the move to B runs: written-as stays "(new place)"
	if _, err := conf.PutSecretKvAt(config.SecretKvLevelServer, config.SecretKvSpec{Name: "srv", Settings: map[string]interface{}{"path-prefix": "c"}}, false); err != nil {
		t.Fatal(err)
	}
	if conf.SecretKvListAt(config.SecretKvLevelServer)[0].WrittenAs != config.ConstSecretKvWrittenAsNewPlace {
		t.Fatal("written-as changed")
	}
	if conf.SetSecretKvWrittenAsIfAt(config.SecretKvLevelServer, "srv", config.ConstSecretKvWrittenAsNewPlace, "acme/h/paris", placeB) {
		t.Fatal("the move to B committed while the entry is at C")
	}
	e, _ = conf.SecretKvActiveEntryAt(config.SecretKvLevelServer, "srv")
	if !conf.SetSecretKvWrittenAsIfAt(config.SecretKvLevelServer, "srv", config.ConstSecretKvWrittenAsNewPlace, "acme/h/paris", conf.SecretKvPlace(e)) {
		t.Fatal("the move to the current place did not commit")
	}
}
