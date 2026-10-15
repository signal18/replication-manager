// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/utils/crypto"
)

// #2039: secret setters (API, rotation, Vault), readers (the monitor) and a rebuild of the
// secrets (a cluster init) at the same moment. With a plain map this is a fatal
// concurrent map write; run with -race.
func TestSecretsConcurrentAccess(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "key")
	key, _ := crypto.Keygen()
	if err := crypto.WriteKey(key, keyPath, true); err != nil {
		t.Fatal(err)
	}
	conf := &Config{MonitoringKeyPath: keyPath}
	conf.LoadEncrytionKey()
	conf.DynamicFlagMap = map[string]interface{}{}
	conf.ImmuableFlagMap = map[string]interface{}{}
	conf.DefaultFlagMap = map[string]interface{}{}
	conf.DecryptSecretsFromConfig()

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				k := "db-servers-credential"
				if g%2 == 1 {
					k = "replication-credential"
				}
				switch i % 4 {
				case 0:
					conf.Secrets.Set(k, Secret{Value: fmt.Sprintf("u:pw%d-%d", g, i)})
				case 1:
					_ = conf.Secrets.Get(k).Value
					_ = conf.GetSecrets()
				case 2:
					for range conf.Secrets.Snapshot() {
					}
				case 3:
					if g == 0 {
						conf.DecryptSecretsFromConfig()
					}
				}
			}
		}(g)
	}
	wg.Wait()
	if conf.Secrets.Len() == 0 {
		t.Fatal("the secrets survive")
	}
}

func TestSecretMapNilReads(t *testing.T) {
	var s *SecretMap
	if s.Has("x") || s.Get("x").Value != "" || s.Len() != 0 || len(s.Snapshot()) != 0 {
		t.Fatal("a nil store reads as empty")
	}
	s.Delete("x")
	n := NewSecrets(map[string]Secret{"a": {Value: "1"}})
	if v, ok := n.Lookup("a"); !ok || v.Value != "1" {
		t.Fatal("lookup")
	}
	n.Replace(map[string]Secret{"b": {Value: "2"}})
	if n.Has("a") || n.Get("b").Value != "2" {
		t.Fatal("replace")
	}
}

// #2039 review: a cluster's Config is a copy of the server's, made after its decrypt, so
// it shares the store; its own decrypt must give it its own, never overwrite the server's
// or another cluster's
func TestSecretsCopiedConfigKeepsItsOwn(t *testing.T) {
	newConf := func() *Config {
		return &Config{DynamicFlagMap: map[string]interface{}{}, ImmuableFlagMap: map[string]interface{}{}, DefaultFlagMap: map[string]interface{}{}}
	}
	server := newConf()
	server.DynamicFlagMap["db-servers-credential"] = "root:server"
	server.DecryptSecretsFromConfig()

	a, b := *server, *server // GetClusterConfig takes the server's Config by value
	a.DynamicFlagMap = map[string]interface{}{"db-servers-credential": "root:a"}
	b.DynamicFlagMap = map[string]interface{}{"db-servers-credential": "root:b"}
	a.DecryptSecretsFromConfig()
	b.DecryptSecretsFromConfig()

	for name, c := range map[string]struct {
		conf *Config
		want string
	}{"server": {server, "root:server"}, "a": {&a, "root:a"}, "b": {&b, "root:b"}} {
		if got := c.conf.Secrets.Get("db-servers-credential").Value; got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}

	// a reload of a's own Config keeps its store (a reader holding it sees the new value)
	store := a.Secrets
	a.DynamicFlagMap["db-servers-credential"] = "root:a2"
	a.DecryptSecretsFromConfig()
	if a.Secrets != store || store.Get("db-servers-credential").Value != "root:a2" {
		t.Error("a reload replaced the store of the same Config")
	}
}

// writes on a nil store do nothing, they never crash the monitor (F2)
func TestSecretMapNilWrites(t *testing.T) {
	var s *SecretMap
	s.Set("x", Secret{Value: "1"})
	s.Replace(map[string]Secret{"x": {Value: "1"}})
	if s.Has("x") {
		t.Fatal("a nil store holds nothing")
	}
}
