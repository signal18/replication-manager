// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// secretLookingKey matches a config key whose name says it holds a credential.
var secretLookingKey = regexp.MustCompile(`pass|secret|token|credential|webhook|slack-url|teams-url|teams-proxy-url`)

// notFlatSecrets are string keys secretLookingKey matches that are not flat secrets: each
// says why. A new key matching it goes in SecretKeys, or here with its reason.
var notFlatSecrets = map[string]string{
	"api-credentials-acl-allow":            "ACL grants, no password",
	"api-credentials-acl-allow-external":   "ACL grants, no password",
	"api-credentials-acl-discard":          "ACL grants, no password",
	"api-credentials-acl-discard-external": "ACL grants, no password",
	"cloud18-git-token-expires":            "the git token's expiry date, not the token",
	"secret-kv":                            "structured secret-kv entries, their own secret model",
}

// Every string config key named like a credential is a flat secret (masked on the API,
// encrypted on save) or listed in notFlatSecrets with its reason.
func TestSecretLookingKeysAreSecrets(t *testing.T) {
	secret := map[string]bool{}
	for _, k := range SecretKeys {
		secret[k] = true
	}
	typ := reflect.TypeOf(Config{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		key := f.Tag.Get("mapstructure")
		if f.Type.Kind() != reflect.String || key == "" || !secretLookingKey.MatchString(key) {
			continue
		}
		if secret[key] {
			continue
		}
		if _, ok := notFlatSecrets[key]; !ok {
			t.Errorf("%s (%s) is named like a credential: add it to SecretKeys with a save case, or to notFlatSecrets with the reason", key, f.Name)
		}
	}
}

// The keys this change made secrets are masked in the API JSON: the mask finds each by its
// json name.
func TestFlatSecretURLsMasked(t *testing.T) {
	conf := Config{
		SlackURL:                 "https://hooks.slack.example/MARKER1",
		Cloud18AlertSlackURL:     "https://hooks.slack.example/MARKER2",
		TeamsUrl:                 "https://example.webhook.office.com/MARKER3",
		TeamsProxyUrl:            "http://u:MARKER4@proxy.example:3128",
		TunnelCredential:         "tunnel:MARKER5",
		RegistryConsulCredential: "consul:MARKER6",
		RegistryConsulToken:      "MARKER7",
		MysqlRouterPass:          "MARKER8",
	}
	secrets := map[string]Secret{}
	for _, k := range SecretKeys {
		secrets[k] = Secret{}
	}
	conf.Secrets = NewSecrets(secrets)
	doc, err := json.Marshal(conf)
	if err != nil {
		t.Fatal(err)
	}
	if doc, err = conf.MaskSecretsJSON(doc, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), "MARKER") {
		t.Fatalf("a secret in the API JSON: %s", regexp.MustCompile(`"[^"]*MARKER[^"]*"`).FindAllString(string(doc), -1))
	}
}

// A URL is never a Vault path: a webhook secret is not read from Vault.
func TestIsPathURL(t *testing.T) {
	conf := &Config{}
	for _, v := range []string{"https://hooks.slack.example/services/T0/B0/X", "http://u:p@proxy.example:3128/"} {
		if conf.IsPath(v) {
			t.Errorf("%q is a Vault path", v)
		}
	}
	for _, v := range []string{"secret/data/repman/db", "kv/repman/proxysql"} {
		if !conf.IsPath(v) {
			t.Errorf("%q is not a Vault path", v)
		}
	}
}
