// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
)

func TestFeedObjectName(t *testing.T) {
	for in, want := range map[string]string{"dev3": "repman_dev3", "Shop-EU.1": "repman_shop_eu_1", strings.Repeat("a", 80): "repman_" + strings.Repeat("a", 56)} {
		if got := FeedObjectName(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

// Each statement a capture source ran on its source is logged once, a repeated one included,
// across status lines that keep only the last ones.
func TestLogSourceStatements(t *testing.T) {
	r := &replPluginRunner{}
	var logged []string
	log := func(q string) { logged = append(logged, q) }
	r.logSourceStatements([]string{"a"}, 1, log)
	r.logSourceStatements([]string{"a"}, 1, log)
	r.logSourceStatements([]string{"a", "a"}, 2, log) // the same statement run again
	r.logSourceStatements([]string{"a", "c"}, 3, log) // the list keeps the last two
	r.gen++                                           // a new process, which ran 3 statements already
	r.logSourceStatements([]string{"d", "e", "f"}, 3, log)
	if want := []string{"a", "a", "c", "d", "e", "f"}; !reflect.DeepEqual(logged, want) {
		t.Fatalf("logged %v, want %v", logged, want)
	}
}

// A feed TOML read back is the same as the one written: no rewrite (no restart) every tick,
// with no table selected too.
func TestSameReplPluginConfigReadBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), feedTOMLName)
	for _, tables := range []string{"", "app.*,app.t"} {
		want := wire.Config{Plugin: "postgresql", Enabled: true, SourceKind: "postgresql", Rules: analyticRules(tables), SourceOptions: map[string]string{"slot": "s"}}
		if err := writeReplPluginConfig(path, want); err != nil {
			t.Fatal(err)
		}
		var got wire.Config
		if _, err := toml.DecodeFile(path, &got); err != nil {
			t.Fatal(err)
		}
		if !sameReplPluginConfig(got, want) {
			t.Fatalf("tables %q: the TOML read back differs from the one written", tables)
		}
	}
}

// A feed waiting for a new initial load is told by its State.
func TestReplFeedNeedsInit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, feedTOMLName)
	if _, wait, _ := replFeedNeedsInit(path); wait {
		t.Fatal("no State: no wait")
	}
	if err := wire.WriteState(wire.StatePath(path), wire.State{NeedsInit: true, Reason: "slot dropped", Owned: []string{"publication:p"}}); err != nil {
		t.Fatal(err)
	}
	why, wait, _ := replFeedNeedsInit(path)
	if !wait || why != "slot dropped" {
		t.Fatalf("%q %v", why, wait)
	}
	if err := writeReplPluginConfig(path, wire.Config{Plugin: "postgresql", SourceKind: "postgresql", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "replication-plugin-feed") || !strings.Contains(string(b), `source_kind = "postgresql"`) {
		t.Fatalf("feed TOML:\n%s", b)
	}
}

// An unreadable State is an error, never "no new initial load needed": the supervisor says why
// the plugin does not start (ERR00132).
func TestReplFeedNeedsInitUnreadableState(t *testing.T) {
	path := filepath.Join(t.TempDir(), feedTOMLName)
	if err := os.WriteFile(wire.StatePath(path), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, wait, err := replFeedNeedsInit(path); err == nil || wait {
		t.Fatalf("corrupt State read as %v, %v", wait, err)
	}
}

// A rebuild or a removal holds the plugin stopped: the supervisor's start skips a held TOML
// until release.
func TestHoldReplPluginAt(t *testing.T) {
	cl := &Cluster{}
	release := cl.holdReplPluginAt("/x/feed.toml")
	cl.replPluginsMu.Lock()
	held := cl.replPluginsHeld["/x/feed.toml"]
	cl.replPluginsMu.Unlock()
	if !held {
		t.Fatal("not held")
	}
	release()
	cl.replPluginsMu.Lock()
	defer cl.replPluginsMu.Unlock()
	if cl.replPluginsHeld["/x/feed.toml"] {
		t.Fatal("still held after release")
	}
}
