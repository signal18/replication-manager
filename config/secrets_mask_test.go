// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// A configuration sent by an API carries no credential: every secret key is masked, at
// the top level (MCP cluster settings) and under "config." (REST cluster objects).
func TestMaskSecretsJSON(t *testing.T) {
	const adminCred = "admin:s3cret-admin-pw"
	const external = "alice:hash_0123456789abcdef"
	conf := &Config{
		APIUsers:         adminCred,
		APIUsersExternal: external,
		Secrets: NewSecrets(map[string]Secret{
			"api-credentials":          {Value: adminCred},
			"api-credentials-external": {Value: external},
		}),
	}

	bare, err := json.Marshal(conf)
	if err != nil {
		t.Fatal(err)
	}
	masked, err := conf.MaskSecretsJSON(bare, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"s3cret-admin-pw", "hash_0123456789abcdef"} {
		if strings.Contains(string(masked), leak) {
			t.Fatalf("bare configuration still carries %q", leak)
		}
	}
	var out map[string]any
	if err := json.Unmarshal(masked, &out); err != nil {
		t.Fatalf("masked document is not JSON: %v", err)
	}
	if out["apiCredentials"] != SecretMask || out["apiCredentialsExternal"] != SecretMask {
		t.Fatalf("credentials not masked: %v / %v", out["apiCredentials"], out["apiCredentialsExternal"])
	}

	wrapped, _ := json.Marshal(map[string]any{"name": "c1", "config": conf})
	masked, err = conf.MaskSecretsJSON(wrapped, "config.")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(masked), "s3cret-admin-pw") {
		t.Fatal("cluster object still carries the admin password under config")
	}
}
