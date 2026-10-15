package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// backup-verification-credential is never serialized: the settings API encodes the
// configuration as it is (F10).
func TestBackupVerificationCredentialNotSerialized(t *testing.T) {
	b, err := json.Marshal(Config{BackupVerificationCredential: "verifier:s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "s3cret") || strings.Contains(string(b), "backupVerificationCredential") {
		t.Fatal("backup-verification-credential is serialized")
	}
}
