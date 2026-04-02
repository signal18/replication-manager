package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

func newBackupKeyringTestServer(keyring string) *ServerMonitor {
	cluster := &Cluster{Conf: &config.Config{}}
	cluster.Conf.BackupEncryptionKeyring = keyring
	return &ServerMonitor{ClusterGroup: cluster}
}

func TestParseBackupEncryptionKeyring_Success(t *testing.T) {
	server := newBackupKeyringTestServer(`{
		"activeKeyId":"k2",
		"legacyDefaultKeyId":"k1",
		"keys":[
			{"id":"k1","passphrase":"old-pass","state":"decrypt-only"},
			{"id":"k2","passphrase":"new-pass","state":"active"}
		]
	}`)

	keyring, err := server.parseBackupEncryptionKeyring()
	if err != nil {
		t.Fatalf("parseBackupEncryptionKeyring() error: %v", err)
	}
	if keyring == nil {
		t.Fatal("expected non-nil keyring")
	}
	if keyring.ActiveKeyID != "k2" {
		t.Fatalf("ActiveKeyID=%q, want k2", keyring.ActiveKeyID)
	}
}

func TestParseBackupEncryptionKeyring_ValidationErrors(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		contains string
	}{
		{
			name:     "reject unknown root field",
			json:     `{"activeKeyId":"k1","keys":[{"id":"k1","passphrase":"p1","state":"active"}],"extra":true}`,
			contains: "invalid backup-encryption-keyring JSON",
		},
		{
			name:     "reject duplicate id",
			json:     `{"activeKeyId":"k1","keys":[{"id":"k1","passphrase":"p1","state":"active"},{"id":"k1","passphrase":"p2","state":"decrypt-only"}]}`,
			contains: "duplicate key id",
		},
		{
			name:     "active key id must be active",
			json:     `{"activeKeyId":"k1","keys":[{"id":"k1","passphrase":"p1","state":"decrypt-only"},{"id":"k2","passphrase":"p2","state":"active"}]}`,
			contains: "must reference a key with state=active",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newBackupKeyringTestServer(tt.json)
			_, err := server.parseBackupEncryptionKeyring()
			if err == nil {
				t.Fatalf("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.contains) {
				t.Fatalf("error=%q, want contains %q", err.Error(), tt.contains)
			}
		})
	}
}

func TestResolveBackupEncryptionPassphraseForDecrypt_MetadataKeyAfterRotation(t *testing.T) {
	server := newBackupKeyringTestServer(`{
		"activeKeyId":"k2",
		"legacyDefaultKeyId":"k1",
		"keys":[
			{"id":"k1","passphrase":"old-pass","state":"decrypt-only"},
			{"id":"k2","passphrase":"new-pass","state":"active"}
		]
	}`)

	sourcePath := filepath.Join(t.TempDir(), "backup.enc")
	if err := os.WriteFile(sourcePath, []byte("legacy-ciphertext"), 0o600); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	meta := &backupmgr.BackupMetadata{EncryptionKeyID: "k1"}
	_, passphrase, cleanup, err := server.resolveBackupEncryptionPassphraseForDecrypt(sourcePath, meta)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Fatalf("resolveBackupEncryptionPassphraseForDecrypt() error: %v", err)
	}
	if passphrase != "old-pass" {
		t.Fatalf("resolved passphrase=%q, want old-pass", passphrase)
	}
}

func TestResolveBackupEncryptionPassphraseForDecrypt_UnknownHeaderKeyIDFails(t *testing.T) {
	server := newBackupKeyringTestServer(`{
		"activeKeyId":"k1",
		"keys":[{"id":"k1","passphrase":"active-pass","state":"active"}]
	}`)

	sourcePath := filepath.Join(t.TempDir(), "header.enc")
	content := backupEncryptionHeaderMagic + `{"keyId":"missing-key","cipher":"aes-256-cbc","tool":"openssl-enc"}` + "\n\n" + "U2FsdGVkX1+dummy"
	if err := os.WriteFile(sourcePath, []byte(content), 0o600); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	_, _, cleanup, err := server.resolveBackupEncryptionPassphraseForDecrypt(sourcePath, nil)
	if cleanup != nil {
		defer cleanup()
	}
	if err == nil {
		t.Fatal("expected error for unknown header keyId")
	}
	if !strings.Contains(err.Error(), "not found in backup-encryption-keyring") {
		t.Fatalf("error=%q, expected unknown keyId message", err.Error())
	}
}
