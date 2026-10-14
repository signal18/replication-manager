package config

import (
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/utils/crypto"
)

// #2017: a credential set at runtime, then a rebuild of the secrets from the config maps
// (a cluster init): the runtime value stays, not the map's older one.
func TestRememberSecretSurvivesRederive(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "key")
	key, _ := crypto.Keygen()
	if err := crypto.WriteKey(key, keyPath, true); err != nil {
		t.Fatal(err)
	}
	conf := &Config{MonitoringKeyPath: keyPath}
	conf.LoadEncrytionKey()
	conf.DynamicFlagMap = map[string]interface{}{"db-servers-credential": conf.EncryptSecretValue("root:oldpw7")}
	conf.ImmuableFlagMap = map[string]interface{}{}
	conf.DefaultFlagMap = map[string]interface{}{}
	conf.DecryptSecretsFromConfig()
	if got := conf.Secrets.Get("db-servers-credential").Value; got != "root:oldpw7" {
		t.Fatalf("loaded %q", got)
	}

	// the setter (setClusterSetting): Secrets only
	conf.Secrets.Set("db-servers-credential", Secret{Value: "root:parent88", OldValue: "root:oldpw7"})
	conf.RememberSecret("db-servers-credential")

	conf.DecryptSecretsFromConfig() // what a cluster init (InitFromConf) does; tag changes no longer do it
	if got := conf.Secrets.Get("db-servers-credential").Value; got != "root:parent88" {
		t.Fatalf("after re-derivation %q, want root:parent88", got)
	}
	if raw, _ := conf.DynamicFlagMap["db-servers-credential"].(string); raw == "" || raw == "root:parent88" {
		t.Fatalf("the map must hold the encrypted value, got %q", raw)
	}
}
