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

	"github.com/signal18/replication-manager/cluster/replplugin/wire"
)

func TestFeedObjectName(t *testing.T) {
	for in, want := range map[string]string{"dev3": "repman_dev3", "Shop-EU.1": "repman_shop_eu_1", strings.Repeat("a", 80): "repman_" + strings.Repeat("a", 56)} {
		if got := feedObjectName(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

// Each statement a capture source ran on its source is logged once, across status lines that
// keep only the last ones.
func TestLogSourceStatements(t *testing.T) {
	r := &replPluginRunner{}
	var logged []string
	log := func(q string) { logged = append(logged, q) }
	r.logSourceStatements([]string{"a"}, log)
	r.logSourceStatements([]string{"a"}, log)
	r.logSourceStatements([]string{"a", "b"}, log)
	r.logSourceStatements([]string{"b", "c"}, log) // "a" fell out of the bounded list
	r.logSourceStatements([]string{"d"}, log)      // a new process
	if want := []string{"a", "b", "c", "d"}; !reflect.DeepEqual(logged, want) {
		t.Fatalf("logged %v, want %v", logged, want)
	}
}

// A feed waiting for a new initial load is told by its State.
func TestReplFeedNeedsInit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, feedTOMLName)
	if _, wait := replFeedNeedsInit(path); wait {
		t.Fatal("no State: no wait")
	}
	if err := wire.WriteState(wire.StatePath(path), wire.State{NeedsInit: true, Reason: "slot dropped", Owned: []string{"publication:p"}}); err != nil {
		t.Fatal(err)
	}
	why, wait := replFeedNeedsInit(path)
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
