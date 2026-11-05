// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "testing"

// #2268: the lock tooltips show the system config value of a setting, never a secret (F10):
// a key the secret registry knows, or whose name says it holds one, is SecretMask.
func TestImmuableFlagValues(t *testing.T) {
	conf := &Config{ImmuableFlagMap: map[string]interface{}{
		"mail-to":                  "ops@acme.example",
		"failover-limit":           3,
		"api-credentials":          "admin:secret",
		"cloud18-gitlab-password":  "pass",
		"backup-restic-aws-secret": "s3cr3t",
		"maxscale-pass":            "mx",
		"git-url":                  "https://user:t0ken@git.acme.example/repo.git",
		"api-public-url":           "https://repman.acme.example",
	}}
	got := conf.ImmuableFlagValues()
	want := map[string]string{
		"mail-to":                  "ops@acme.example",
		"failover-limit":           "3",
		"api-credentials":          SecretMask,
		"cloud18-gitlab-password":  SecretMask,
		"backup-restic-aws-secret": SecretMask,
		"maxscale-pass":            SecretMask, // "pass" in the name
		"git-url":                  SecretMask, // credentials in the URL
		"api-public-url":           "https://repman.acme.example",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d keys, want %d", len(got), len(want))
	}
}
