package backupmgr

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/utils/misc"
)

const (
	BackupEncryptionAlgorithm = "aes-256-cbc"
	BackupIntegrityAlgorithm  = "hmac-sha256"

	BackupEncryptionSecretSourceSponsor = "cloud18-sponsor-user-credentials"
	BackupEncryptionSecretSourceAdmin   = "api-credentials/admin"
)

var allowedBackupKeyReferenceSources = map[string]struct{}{
	BackupEncryptionSecretSourceSponsor: {},
	BackupEncryptionSecretSourceAdmin:   {},
}

// ValidateLockedBackupAlgorithms enforces Story 2.1 locked crypto contract.
func ValidateLockedBackupAlgorithms(encryptionAlgorithm string, integrityAlgorithm string) error {
	if strings.TrimSpace(strings.ToLower(encryptionAlgorithm)) != BackupEncryptionAlgorithm {
		return fmt.Errorf("unsupported backup encryption algorithm: %s", encryptionAlgorithm)
	}

	if strings.TrimSpace(strings.ToLower(integrityAlgorithm)) != BackupIntegrityAlgorithm {
		return fmt.Errorf("unsupported backup integrity algorithm: %s", integrityAlgorithm)
	}

	return nil
}

// ResolveBackupEncryptionKeyMaterial resolves key material with strict priority:
// sponsor secret first, then api-credentials/admin.
func ResolveBackupEncryptionKeyMaterial(sponsorCredentials string, apiCredentials string) (string, string, error) {
	_, sponsorPassword := misc.SplitPair(strings.TrimSpace(sponsorCredentials))
	if strings.TrimSpace(sponsorPassword) != "" {
		return sponsorPassword, BackupEncryptionSecretSourceSponsor, nil
	}

	for _, credential := range strings.Split(apiCredentials, ",") {
		user, pass := misc.SplitPair(strings.TrimSpace(credential))
		if user == "admin" && strings.TrimSpace(pass) != "" {
			return pass, BackupEncryptionSecretSourceAdmin, nil
		}
	}

	return "", "", fmt.Errorf("no backup encryption key material available from sponsor or api-credentials/admin")
}

func FormatBackupSecretKeyReference(source string, version int) (string, error) {
	source = strings.TrimSpace(source)
	if _, ok := allowedBackupKeyReferenceSources[source]; !ok {
		return "", fmt.Errorf("unsupported backup key reference source: %s", source)
	}

	if version < 1 {
		return "", fmt.Errorf("backup key reference version must be >= 1")
	}

	return fmt.Sprintf("%s:v%d", source, version), nil
}

func ParseBackupSecretKeyReference(ref string) (string, int, error) {
	trimmedRef := strings.TrimSpace(ref)
	if trimmedRef == "" {
		return "", 0, fmt.Errorf("backup key reference is empty")
	}

	idx := strings.LastIndex(trimmedRef, ":")
	if idx <= 0 || idx == len(trimmedRef)-1 {
		return "", 0, fmt.Errorf("invalid backup key reference format: %s", ref)
	}

	source := trimmedRef[:idx]
	if _, ok := allowedBackupKeyReferenceSources[source]; !ok {
		return "", 0, fmt.Errorf("unsupported backup key reference source: %s", source)
	}

	versionToken := trimmedRef[idx+1:]
	if !strings.HasPrefix(versionToken, "v") {
		return "", 0, fmt.Errorf("invalid backup key reference version token: %s", versionToken)
	}

	version, err := strconv.Atoi(strings.TrimPrefix(versionToken, "v"))
	if err != nil || version < 1 {
		return "", 0, fmt.Errorf("invalid backup key reference version: %s", versionToken)
	}

	return source, version, nil
}

func ValidateBackupSecretKeyReference(ref string) error {
	_, _, err := ParseBackupSecretKeyReference(ref)
	return err
}

// EnsureNoPlaintextEncryptionKey prevents plaintext secret persistence in
// metadata by allowing only validated key-reference format.
func (bm *BackupMetadata) EnsureNoPlaintextEncryptionKey() error {
	if bm == nil {
		return nil
	}

	bm.EncryptionKey = strings.TrimSpace(bm.EncryptionKey)
	if bm.EncryptionKey == "" {
		return nil
	}

	if err := ValidateBackupSecretKeyReference(bm.EncryptionKey); err != nil {
		bm.EncryptionKey = ""
		return err
	}

	return nil
}
