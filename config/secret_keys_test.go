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
// says why. A new key matching it is tagged secret:"true", or goes here with its reason.
var notFlatSecrets = map[string]string{
	"api-credentials-acl-allow":            "ACL grants, no password",
	"api-credentials-acl-allow-external":   "ACL grants, no password",
	"api-credentials-acl-discard":          "ACL grants, no password",
	"api-credentials-acl-discard-external": "ACL grants, no password",
	"cloud18-git-token-expires":            "the git token's expiry date, not the token",
	"secret-kv":                            "structured secret-kv entries, their own secret model",
	"scheduler-rotate-passwords-cron":      "the cron of the scheduled password rotation, no password",
	"alert-slack-url":                      "an alert URL, plain: the back office reads it to find why an integration does not post",
	"cloud18-alert-slack-url":              "an alert URL, plain: the back office reads it to find why an integration does not post",
	"alert-teams-url":                      "an alert URL, plain: the back office reads it to find why an integration does not post",
	"alert-teams-proxy-url":                "an alert URL, plain: the back office reads it to find why an integration does not post",
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
			t.Errorf("%s (%s) is named like a credential: tag it secret:\"true\" with a save case, or add it to notFlatSecrets with the reason", key, f.Name)
		}
	}
}

// The tunnel and Consul credentials and the router password are secrets: masked in the API
// JSON, the mask finds each by its json name.
func TestFlatSecretsMasked(t *testing.T) {
	conf := Config{
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

// An alert URL v3.1.47 or v3.1.48 saved encrypted ("https:hash_<hex>") is read back as the URL,
// the alert URLs being plain: left as it was, the alert would post to the ciphertext. A plain
// URL is left alone, whatever it contains, and a value that cannot be read is reported, not
// replaced.
func TestDecryptLegacyEncryptedURLs(t *testing.T) {
	keyed := &Config{SecretKey: []byte("0123456789abcdef")}
	urls := map[string]string{
		"alert-slack-url":         "https://hooks.slack.example/services/T0/B0/MARKERSLACK",
		"cloud18-alert-slack-url": "http://mattermost.example:8065/hooks/MARKERMM",
		"alert-teams-url":         "https://prod.example.logic.azure.com:443/workflows/x/invoke?api-version=2016-06-01&sp=%2Ftriggers&sig=MARKERTEAMS",
		"alert-teams-proxy-url":   "http://user:MARKERPROXY@proxy.example:3128",
	}
	newConf := func(key []byte, val func(k string) string) *Config {
		c := &Config{SecretKey: key}
		c.SlackURL, c.Cloud18AlertSlackURL = val("alert-slack-url"), val("cloud18-alert-slack-url")
		c.TeamsUrl, c.TeamsProxyUrl = val("alert-teams-url"), val("alert-teams-proxy-url")
		return c
	}
	encrypted := func(k string) string { return keyed.EncryptSecretValue(urls[k]) }
	plain := func(k string) string { return urls[k] }
	got := func(c *Config) map[string]string {
		return map[string]string{"alert-slack-url": c.SlackURL, "cloud18-alert-slack-url": c.Cloud18AlertSlackURL, "alert-teams-url": c.TeamsUrl, "alert-teams-proxy-url": c.TeamsProxyUrl}
	}

	enc := newConf(keyed.SecretKey, encrypted)
	for k, v := range got(enc) {
		if !strings.Contains(v, "hash_") || strings.Contains(v, "MARKER") {
			t.Fatalf("%s is not saved encrypted by the fixture: %q", k, v)
		}
	}
	done, unread := enc.DecryptLegacyEncryptedURLs()
	if len(done) != len(urls) || len(unread) != 0 {
		t.Fatalf("decrypted %v, unread %v, want the four URLs and none unread", done, unread)
	}
	for k, v := range got(enc) {
		if v != urls[k] {
			t.Errorf("%s after the decrypt: %q, want %q", k, v, urls[k])
		}
	}
	if again, unread := enc.DecryptLegacyEncryptedURLs(); len(again) != 0 || len(unread) != 0 {
		t.Errorf("a second decrypt changed %v, unread %v: it must leave a plain URL alone", again, unread)
	}

	pl := newConf(keyed.SecretKey, plain)
	if done, unread := pl.DecryptLegacyEncryptedURLs(); len(done) != 0 || len(unread) != 0 {
		t.Errorf("plain URLs touched: decrypted %v, unread %v", done, unread)
	}

	// a plain URL that only contains "hash_" and hex digits is not the encrypted form
	const lookalike = "https://hooks.example/services/hash_0123456789abcdef0123456789abcdef01234567/x"
	la := newConf(keyed.SecretKey, func(string) string { return lookalike })
	if done, unread := la.DecryptLegacyEncryptedURLs(); len(done) != 0 || len(unread) != 0 || la.SlackURL != lookalike {
		t.Errorf("a plain URL containing hash_<hex> was touched: decrypted %v, unread %v, field %q", done, unread, la.SlackURL)
	}

	// no encryption key: the encrypted form stays, and is reported
	nokey := newConf(nil, encrypted)
	before := got(nokey)
	if done, unread := nokey.DecryptLegacyEncryptedURLs(); len(done) != 0 || len(unread) != len(urls) || !reflect.DeepEqual(before, got(nokey)) {
		t.Errorf("without the key: decrypted %v, unread %v", done, unread)
	}

	// another key: the cipher has no integrity check, the bytes it gives are not a URL
	other := newConf([]byte("fedcba9876543210"), encrypted)
	before = got(other)
	if done, unread := other.DecryptLegacyEncryptedURLs(); len(done) != 0 || len(unread) != len(urls) || !reflect.DeepEqual(before, got(other)) {
		t.Errorf("with another key: decrypted %v, unread %v", done, unread)
	}

	// an encrypted token the decrypt leaves as it is (odd number of hex digits): reported
	odd := newConf(keyed.SecretKey, func(string) string { return "https:hash_00112233445566778899aabbccddeeff001122334" })
	if done, unread := odd.DecryptLegacyEncryptedURLs(); len(done) != 0 || len(unread) != len(urls) {
		t.Errorf("an unreadable token: decrypted %v, unread %v", done, unread)
	}
}
