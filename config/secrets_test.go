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
				switch i % 5 {
				case 0:
					conf.Secrets.Set(k, Secret{Value: fmt.Sprintf("u:pw%d-%d", g, i)})
				case 1:
					conf.RememberSecret(k)
				case 2:
					_ = conf.Secrets.Get(k).Value
					_ = conf.GetSecrets()
				case 3:
					for range conf.Secrets.Snapshot() {
					}
				case 4:
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
