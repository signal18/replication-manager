// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package credstore

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func testConf() *config.Config {
	return &config.Config{
		SecretKey:       []byte("0123456789abcdef"),
		ConfRewrite:     true,
		DynamicFlagMap:  map[string]interface{}{},
		ImmuableFlagMap: map[string]interface{}{},
		DefaultFlagMap: map[string]interface{}{
			"replication-credential":         "root:mariadb",
			"backup-verification-credential": "",
		},
	}
}

// the read rule: the dynamic config, then the pinned config, then the flag default
func TestChainReadOrder(t *testing.T) {
	conf := testConf()
	c := NewConfigChain(conf, nil)
	c.Refresh()
	if v, src := conf.GetDecryptedValue("replication-credential"), c.Source("replication-credential"); v != "root:mariadb" || src != SourceDefault {
		t.Fatalf("default: %q from %s", v, src)
	}
	conf.ImmuableFlagMap["replication-credential"] = "repl:pinned"
	c.Refresh()
	if v, src := conf.GetDecryptedValue("replication-credential"), c.Source("replication-credential"); v != "repl:pinned" || src != SourcePinned {
		t.Fatalf("pinned: %q from %s", v, src)
	}
	conf.DynamicFlagMap["replication-credential"] = "repl:dynamic"
	c.Refresh()
	if v, src := conf.GetDecryptedValue("replication-credential"), c.Source("replication-credential"); v != "repl:dynamic" || src != SourceDynamic {
		t.Fatalf("dynamic: %q from %s", v, src)
	}
}

// #2017: a value written through the chain survives a reread of the config
func TestChainSetSurvivesRefresh(t *testing.T) {
	conf := testConf()
	conf.DynamicFlagMap["replication-credential"] = "repl:loaded-at-start"
	saves := 0
	c := NewConfigChain(conf, func() { saves++ })
	c.Refresh()
	if err := c.Set("replication-credential", "repl:set-at-runtime"); err != nil {
		t.Fatal(err)
	}
	if saves != 1 {
		t.Fatalf("%d save events, want 1", saves)
	}
	if v := conf.GetDecryptedValue("replication-credential"); v != "repl:set-at-runtime" {
		t.Fatalf("cache %q after Set", v)
	}
	// stored encrypted, the user name in clear: the save writes it as it is
	stored := fmt.Sprintf("%v", conf.DynamicFlagMap["replication-credential"])
	if !strings.HasPrefix(stored, "repl:hash_") {
		t.Fatalf("stored %q, want the encrypted password", stored)
	}
	c.Refresh()
	if v := conf.GetDecryptedValue("replication-credential"); v != "repl:set-at-runtime" {
		t.Fatalf("%q after a reread, want the value set at runtime", v)
	}
	if old := conf.Secrets.Get("replication-credential").OldValue; old != "" {
		// a reread is not a change: it has no former value
		t.Fatalf("OldValue %q after a reread", old)
	}
}

// the write rule: a pinned key, or monitoring-save-config off, takes no change, and the
// reason is given
func TestChainRefusals(t *testing.T) {
	conf := testConf()
	conf.ImmuableFlagMap["replication-credential"] = "repl:pinned"
	c := NewConfigChain(conf, func() { t.Fatal("a refused change must not save") })
	c.Refresh()
	err := c.Set("replication-credential", "repl:new")
	if !errors.Is(err, ErrChangeRefused) || !strings.Contains(err.Error(), "set in a config file") {
		t.Fatalf("pinned: %v", err)
	}
	if v := conf.GetDecryptedValue("replication-credential"); v != "repl:pinned" {
		t.Fatalf("a refused change changed the cache: %q", v)
	}
	if c.Versioned("backup-verification-credential") {
		t.Fatal("the config stores keep no history")
	}
}

// monitoring-save-config sets the scope: on, a change is saved (dynamic mode); off, it lives
// in memory (static mode: the files are the source), survives a reread, is not persisted
func TestChainScopes(t *testing.T) {
	conf := testConf()
	saves := 0
	c := NewConfigChain(conf, func() { saves++ })
	c.Refresh()
	if !c.Persists("backup-verification-credential") || c.Source("backup-verification-credential") != SourceDefault {
		t.Fatal("dynamic mode: a change persists")
	}
	conf.ConfRewrite = false
	if c.Persists("backup-verification-credential") {
		t.Fatal("static mode: a change does not persist")
	}
	if err := c.Set("backup-verification-credential", "verif:runtime"); err != nil {
		t.Fatalf("static mode takes a change in memory: %v", err)
	}
	if src := c.Source("backup-verification-credential"); src != SourceRuntime {
		t.Fatalf("source %s, want %s", src, SourceRuntime)
	}
	c.Refresh()
	if v := conf.GetDecryptedValue("backup-verification-credential"); v != "verif:runtime" {
		t.Fatalf("static mode: %q after a reread", v)
	}
	conf.ImmuableFlagMap["replication-credential"] = "repl:pinned"
	if c.Persists("replication-credential") || c.CanSet("replication-credential") == nil {
		t.Fatal("a pinned key takes no change in either scope")
	}
}

// two setters and a reread at once: run with -race
func TestChainConcurrentSetters(t *testing.T) {
	conf := testConf()
	c := NewConfigChain(conf, func() {})
	c.Refresh()
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				switch g {
				case 0:
					c.Set("replication-credential", fmt.Sprintf("repl:p%d", i))
				case 1:
					c.Set("backup-verification-credential", fmt.Sprintf("verif:p%d", i))
				case 2:
					c.Refresh()
				default:
					c.Source("replication-credential")
					c.CanSet("replication-credential")
				}
			}
		}(g)
	}
	wg.Wait()
	if v := conf.GetDecryptedValue("replication-credential"); v != "repl:p99" {
		t.Fatalf("last value %q", v)
	}
}
