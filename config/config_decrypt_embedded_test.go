package config

import (
	"strings"
	"testing"
)

// An encrypted token embedded in a connection string is decrypted in place; a whole
// encrypted value still decrypts; a plain value and a broken token are left alone (#1915).
func TestGetDecryptedEmbedded(t *testing.T) {
	conf := &Config{SecretKey: []byte("0123456789abcdef0123456789abcdef")}
	enc := conf.GetEncryptedString("s3cr3tPass")
	if !strings.HasPrefix(enc, "hash_") {
		t.Fatalf("encrypted value should carry the hash_ prefix: %q", enc)
	}
	dsn := "postgres://mattermost:" + enc + "@prx1:5432/mattermost?sslmode=disable"
	got := conf.GetDecryptedEmbedded("MM_SQLSETTINGS_DATASOURCE", dsn)
	want := "postgres://mattermost:s3cr3tPass@prx1:5432/mattermost?sslmode=disable"
	if got != want {
		t.Fatalf("embedded token not decrypted: got %q", got)
	}
	if conf.GetDecryptedEmbedded("k", enc) != "s3cr3tPass" {
		t.Fatal("a whole encrypted value must still decrypt")
	}
	if conf.GetDecryptedEmbedded("k", "plain-value") != "plain-value" {
		t.Fatal("a plain value must be returned unchanged")
	}
	broken := "user:hash_zz@host"
	if conf.GetDecryptedEmbedded("k", broken) != broken {
		t.Fatal("a token that is not hex is left as it is")
	}
	if conf.GetDecryptedEmbedded("k", "user:hash_00ff@host") != "user:hash_00ff@host" {
		t.Fatal("a token that does not decrypt is left as it is")
	}
}
