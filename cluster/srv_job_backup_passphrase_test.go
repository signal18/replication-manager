package cluster

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestResolveOldSponsorPassphrases(t *testing.T) {
	t.Run("returns OldValue password", func(t *testing.T) {
		cl := &Cluster{}
		cl.Conf = &config.Config{}
		cl.Conf.Secrets = map[string]config.Secret{
			"cloud18-sponsor-user-credentials": {Value: "sponsor:new-pass", OldValue: "sponsor:old-pass"},
		}
		server := &ServerMonitor{ClusterGroup: cl}

		got := server.resolveOldSponsorPassphrases()
		if len(got) != 1 || got[0] != "old-pass" {
			t.Errorf("expected [old-pass], got %v", got)
		}
	})

	t.Run("returns empty slice when OldValue is empty", func(t *testing.T) {
		cl := &Cluster{}
		cl.Conf = &config.Config{}
		cl.Conf.Secrets = map[string]config.Secret{
			"cloud18-sponsor-user-credentials": {Value: "sponsor:new-pass"},
		}
		server := &ServerMonitor{ClusterGroup: cl}

		got := server.resolveOldSponsorPassphrases()
		if len(got) != 0 {
			t.Errorf("expected empty, got %v", got)
		}
	})

	t.Run("returns multi-entry History in order, deduped", func(t *testing.T) {
		cl := &Cluster{}
		cl.Conf = &config.Config{}
		cl.Conf.Secrets = map[string]config.Secret{
			"cloud18-sponsor-user-credentials": {
				Value:    "sponsor:current",
				OldValue: "sponsor:pass2",
				History: []config.SecretHistoryEntry{
					{Value: "sponsor:pass1"},
					{Value: "sponsor:pass0"},
					{Value: "sponsor:pass1"}, // duplicate — should be skipped
				},
			},
		}
		server := &ServerMonitor{ClusterGroup: cl}

		got := server.resolveOldSponsorPassphrases()
		want := []string{"pass2", "pass1", "pass0"}
		if len(got) != len(want) {
			t.Fatalf("expected %v, got %v", want, got)
		}
		for i, w := range want {
			if got[i] != w {
				t.Errorf("index %d: expected %q, got %q", i, w, got[i])
			}
		}
	})

	t.Run("returns nil for nil ClusterGroup", func(t *testing.T) {
		server := &ServerMonitor{ClusterGroup: nil}
		got := server.resolveOldSponsorPassphrases()
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
}

func TestSecretRotate(t *testing.T) {
	t.Run("first rotation populates OldValue and History is empty (OldValue was empty)", func(t *testing.T) {
		s := config.Secret{Value: "user:pass1"}
		rotated := s.Rotate("user:pass2")

		if rotated.Value != "user:pass2" {
			t.Errorf("expected Value=user:pass2, got %q", rotated.Value)
		}
		if rotated.OldValue != "user:pass1" {
			t.Errorf("expected OldValue=user:pass1, got %q", rotated.OldValue)
		}
		if len(rotated.History) != 0 {
			t.Errorf("expected empty History on first rotation, got %v", rotated.History)
		}
	})

	t.Run("second rotation moves previous OldValue into History", func(t *testing.T) {
		s := config.Secret{Value: "user:pass2", OldValue: "user:pass1"}
		rotated := s.Rotate("user:pass3")

		if rotated.Value != "user:pass3" {
			t.Errorf("expected Value=user:pass3, got %q", rotated.Value)
		}
		if rotated.OldValue != "user:pass2" {
			t.Errorf("expected OldValue=user:pass2, got %q", rotated.OldValue)
		}
		if len(rotated.History) != 1 {
			t.Fatalf("expected 1 History entry, got %d", len(rotated.History))
		}
		if rotated.History[0].Value != "user:pass1" {
			t.Errorf("expected History[0].Value=user:pass1, got %q", rotated.History[0].Value)
		}
		if rotated.History[0].RotatedAt.IsZero() {
			t.Error("expected RotatedAt to be set")
		}
	})

	t.Run("accumulates history across multiple rotations", func(t *testing.T) {
		s := config.Secret{Value: "user:pass1"}
		s = s.Rotate("user:pass2")
		s = s.Rotate("user:pass3")
		s = s.Rotate("user:pass4")

		if s.Value != "user:pass4" {
			t.Errorf("expected Value=user:pass4, got %q", s.Value)
		}
		if s.OldValue != "user:pass3" {
			t.Errorf("expected OldValue=user:pass3, got %q", s.OldValue)
		}
		if len(s.History) != 2 {
			t.Fatalf("expected 2 History entries, got %d", len(s.History))
		}
		if s.History[0].Value != "user:pass2" {
			t.Errorf("expected History[0]=user:pass2, got %q", s.History[0].Value)
		}
		if s.History[1].Value != "user:pass1" {
			t.Errorf("expected History[1]=user:pass1, got %q", s.History[1].Value)
		}
	})
}

func TestResolveBackupEncryptionPassphraseSource(t *testing.T) {
	t.Run("env var takes precedence", func(t *testing.T) {
		t.Setenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE", "env-passphrase")

		server := &ServerMonitor{}
		pass, source, explicit := server.resolveBackupEncryptionPassphraseWithSource()

		if pass != "env-passphrase" {
			t.Errorf("expected env passphrase, got %q", pass)
		}
		if source != backupPassphraseSourceEnv {
			t.Errorf("expected source env, got %v", source)
		}
		if !explicit {
			t.Error("env source should be explicit")
		}
	})

	t.Run("config passphrase when env absent", func(t *testing.T) {
		os.Unsetenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE")

		cluster := &Cluster{}
		cluster.Conf = &config.Config{}
		cluster.Conf.BackupEncryptionPassphrase = "config-passphrase"

		server := &ServerMonitor{
			ClusterGroup: cluster,
		}
		pass, source, explicit := server.resolveBackupEncryptionPassphraseWithSource()

		if pass != "config-passphrase" {
			t.Errorf("expected config passphrase, got %q", pass)
		}
		if source != backupPassphraseSourceConfig {
			t.Errorf("expected source config, got %v", source)
		}
		if !explicit {
			t.Error("config source should be explicit")
		}
	})

	t.Run("server DB password no longer used as fallback", func(t *testing.T) {
		os.Unsetenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE")

		cluster := &Cluster{}
		cluster.Conf = &config.Config{}

		server := &ServerMonitor{
			ClusterGroup: cluster,
			Pass:         "server-db-pass",
		}
		pass, source, explicit := server.resolveBackupEncryptionPassphraseWithSource()

		if pass != "" {
			t.Errorf("expected empty passphrase (server.Pass no longer used), got %q", pass)
		}
		if source != backupPassphraseSourceNone {
			t.Errorf("expected source none, got %v", source)
		}
		if explicit {
			t.Error("should not be explicit")
		}
	})

	t.Run("fallback to sponsor credentials", func(t *testing.T) {
		os.Unsetenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE")

		cluster := &Cluster{}
		cluster.Conf = &config.Config{}
		cluster.Conf.Secrets = map[string]config.Secret{
			"cloud18-sponsor-user-credentials": {Value: "sponsor:sponsor-pass"},
		}

		server := &ServerMonitor{
			ClusterGroup: cluster,
		}
		pass, source, explicit := server.resolveBackupEncryptionPassphraseWithSource()

		if pass != "sponsor-pass" {
			t.Errorf("expected sponsor passphrase, got %q", pass)
		}
		if source != backupPassphraseSourceSponsor {
			t.Errorf("expected source sponsor, got %v", source)
		}
		if explicit {
			t.Error("sponsor source should not be explicit")
		}
	})

	t.Run("sponsor takes precedence over api-credentials", func(t *testing.T) {
		os.Unsetenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE")

		cluster := &Cluster{}
		cluster.Conf = &config.Config{}
		cluster.Conf.Secrets = map[string]config.Secret{
			"cloud18-sponsor-user-credentials": {Value: "sponsor:sponsor-pass"},
			"api-credentials":                  {Value: "admin:admin-pass"},
		}

		server := &ServerMonitor{
			ClusterGroup: cluster,
		}
		pass, source, explicit := server.resolveBackupEncryptionPassphraseWithSource()

		if pass != "sponsor-pass" {
			t.Errorf("expected sponsor to take precedence, got %q", pass)
		}
		if source != backupPassphraseSourceSponsor {
			t.Errorf("expected source sponsor, got %v", source)
		}
		if explicit {
			t.Error("sponsor source should not be explicit")
		}
	})

	t.Run("nil server returns empty", func(t *testing.T) {
		os.Unsetenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE")

		var server *ServerMonitor
		pass, source, explicit := server.resolveBackupEncryptionPassphraseWithSource()

		if pass != "" {
			t.Errorf("expected empty passphrase for nil server, got %q", pass)
		}
		if source != backupPassphraseSourceNone {
			t.Errorf("expected source none for nil server, got %v", source)
		}
		if explicit {
			t.Error("nil server should not be explicit")
		}
	})

	t.Run("fallback to internal api-credentials", func(t *testing.T) {
		os.Unsetenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE")

		cluster := &Cluster{}
		cluster.Conf = &config.Config{}
		cluster.Conf.Secrets = map[string]config.Secret{
			"api-credentials": {Value: "admin:internal-pass"},
		}

		server := &ServerMonitor{
			ClusterGroup: cluster,
			Pass:         "",
		}
		pass, source, explicit := server.resolveBackupEncryptionPassphraseWithSource()

		if pass != "internal-pass" {
			t.Errorf("expected internal api-credentials passphrase, got %q", pass)
		}
		if source != backupPassphraseSourceAPIInternal {
			t.Errorf("expected source api-internal, got %v", source)
		}
		if explicit {
			t.Error("api-credentials source should not be explicit")
		}
	})

	t.Run("fallback to external api-credentials", func(t *testing.T) {
		os.Unsetenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE")

		cluster := &Cluster{}
		cluster.Conf = &config.Config{}
		cluster.Conf.Secrets = map[string]config.Secret{
			"api-credentials-external": {Value: "admin:external-pass"},
		}

		server := &ServerMonitor{
			ClusterGroup: cluster,
			Pass:         "",
		}
		pass, source, explicit := server.resolveBackupEncryptionPassphraseWithSource()

		if pass != "external-pass" {
			t.Errorf("expected external api-credentials passphrase, got %q", pass)
		}
		if source != backupPassphraseSourceAPIExternal {
			t.Errorf("expected source api-external, got %v", source)
		}
		if explicit {
			t.Error("api-credentials-external source should not be explicit")
		}
	})

	t.Run("internal api-credentials takes precedence over external", func(t *testing.T) {
		os.Unsetenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE")

		cluster := &Cluster{}
		cluster.Conf = &config.Config{}
		cluster.Conf.Secrets = map[string]config.Secret{
			"api-credentials":          {Value: "admin:internal-pass"},
			"api-credentials-external": {Value: "admin:external-pass"},
		}

		server := &ServerMonitor{
			ClusterGroup: cluster,
			Pass:         "",
		}
		pass, source, explicit := server.resolveBackupEncryptionPassphraseWithSource()

		if pass != "internal-pass" {
			t.Errorf("expected internal to take precedence, got %q", pass)
		}
		if source != backupPassphraseSourceAPIInternal {
			t.Errorf("expected source api-internal, got %v", source)
		}
		if explicit {
			t.Error("internal should not be explicit")
		}
	})
}

func TestBackupPassphraseSourceString(t *testing.T) {
	tests := []struct {
		source backupPassphraseSource
		want   string
	}{
		{backupPassphraseSourceEnv, "REPLICATION_MANAGER_BACKUP_PASSPHRASE env var"},
		{backupPassphraseSourceConfig, "backup-encryption-passphrase config"},
		{backupPassphraseSourceSponsor, "cloud18-sponsor-user-credentials (sponsor role password)"},
		{backupPassphraseSourceAPIInternal, "api-credentials (internal admin password)"},
		{backupPassphraseSourceAPIExternal, "api-credentials-external (external admin password)"},
		{backupPassphraseSourceNone, "none"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.source.String(); got != tt.want {
				t.Errorf("backupPassphraseSource.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveBackupEncryptionPassphraseForUse(t *testing.T) {
	t.Run("env source returns passphrase no error", func(t *testing.T) {
		t.Setenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE", "env-passphrase")

		server := &ServerMonitor{}
		pass, err := server.resolveBackupEncryptionPassphraseForUse()

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if pass != "env-passphrase" {
			t.Errorf("expected env passphrase, got %q", pass)
		}
	})

	t.Run("config source returns passphrase no error", func(t *testing.T) {
		os.Unsetenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE")

		cluster := &Cluster{}
		cluster.Conf = &config.Config{}
		cluster.Conf.BackupEncryptionPassphrase = "config-passphrase"

		server := &ServerMonitor{
			ClusterGroup: cluster,
		}
		pass, err := server.resolveBackupEncryptionPassphraseForUse()

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if pass != "config-passphrase" {
			t.Errorf("expected config passphrase, got %q", pass)
		}
	})

	t.Run("server DB password no longer returns passphrase", func(t *testing.T) {
		os.Unsetenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE")

		cluster := &Cluster{}
		cluster.Conf = &config.Config{}

		server := &ServerMonitor{
			ClusterGroup: cluster,
			Pass:         "server-db-pass",
		}
		_, err := server.resolveBackupEncryptionPassphraseForUse()

		if err == nil {
			t.Error("expected error since server.Pass is no longer a fallback source")
		}
	})

	t.Run("none source returns error", func(t *testing.T) {
		os.Unsetenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE")

		cluster := &Cluster{}
		cluster.Conf = &config.Config{}

		server := &ServerMonitor{
			ClusterGroup: cluster,
			Pass:         "",
		}
		_, err := server.resolveBackupEncryptionPassphraseForUse()

		if err == nil {
			t.Error("expected error for empty passphrase")
		}
		if err.Error() != "backup encryption passphrase is empty" {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("nil ClusterGroup returns error without panic", func(t *testing.T) {
		os.Unsetenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE")

		server := &ServerMonitor{
			ClusterGroup: nil,
			Pass:         "server-db-pass",
		}

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("resolveBackupEncryptionPassphraseForUse panicked: %v", r)
			}
		}()

		_, err := server.resolveBackupEncryptionPassphraseForUse()
		if err == nil {
			t.Error("expected error since server.Pass is no longer a fallback source")
		}
	})

	t.Run("nil ClusterGroup with empty Pass returns error without panic", func(t *testing.T) {
		os.Unsetenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE")

		server := &ServerMonitor{
			ClusterGroup: nil,
			Pass:         "",
		}

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("resolveBackupEncryptionPassphraseForUse panicked: %v", r)
			}
		}()

		_, err := server.resolveBackupEncryptionPassphraseForUse()
		if err == nil {
			t.Error("expected error for empty passphrase")
		}
		if err.Error() != "backup encryption passphrase is empty" {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}

func TestEnsureOpenSSLAvailableForBackupStrictMode(t *testing.T) {
	t.Run("strict mode rejects fallback source", func(t *testing.T) {
		os.Unsetenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE")

		cluster := &Cluster{}
		cluster.Conf = &config.Config{}
		cluster.Conf.BackupEncryptionEnabled = true
		cluster.Conf.BackupEncryptionRequireExplicitPassphrase = true
		cluster.Conf.Secrets = map[string]config.Secret{
			"cloud18-sponsor-user-credentials": {Value: "sponsor:sponsor-pass"},
		}

		server := &ServerMonitor{
			ClusterGroup: cluster,
		}

		err := server.ensureOpenSSLAvailableForBackup()
		if err == nil {
			t.Fatal("expected error in strict mode with fallback source")
		}
		if !strings.Contains(err.Error(), "backup-encryption-require-explicit-passphrase") {
			t.Errorf("error should mention strict mode requirement, got: %v", err)
		}
	})

	t.Run("strict mode allows explicit env source", func(t *testing.T) {
		t.Setenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE", "explicit-passphrase")

		cluster := &Cluster{}
		cluster.Conf = &config.Config{}
		cluster.Conf.BackupEncryptionEnabled = true
		cluster.Conf.BackupEncryptionRequireExplicitPassphrase = true

		server := &ServerMonitor{
			ClusterGroup: cluster,
		}

		err := server.ensureOpenSSLAvailableForBackup()
		if err != nil {
			t.Errorf("unexpected error with explicit env source: %v", err)
		}
	})

	t.Run("strict mode disabled allows fallback", func(t *testing.T) {
		os.Unsetenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE")

		cluster := &Cluster{}
		cluster.Conf = &config.Config{}
		cluster.Conf.BackupEncryptionEnabled = true
		cluster.Conf.BackupEncryptionRequireExplicitPassphrase = false
		cluster.Conf.Secrets = map[string]config.Secret{
			"cloud18-sponsor-user-credentials": {Value: "sponsor:sponsor-pass"},
		}

		server := &ServerMonitor{
			ClusterGroup: cluster,
		}

		err := server.ensureOpenSSLAvailableForBackup()
		if err != nil {
			t.Errorf("unexpected error with fallback in non-strict mode: %v", err)
		}
	})

	t.Run("encryption disabled returns nil", func(t *testing.T) {
		cluster := &Cluster{}
		cluster.Conf = &config.Config{}
		cluster.Conf.BackupEncryptionEnabled = false

		server := &ServerMonitor{
			ClusterGroup: cluster,
		}

		err := server.ensureOpenSSLAvailableForBackup()
		if err != nil {
			t.Errorf("unexpected error when encryption disabled: %v", err)
		}
	})
}

func TestEncryptBackupDirectoryPerFile_NoEligibleFiles_DoesNotRequirePassphrase(t *testing.T) {
	os.Unsetenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE")

	cluster := &Cluster{}
	cluster.Conf = &config.Config{}

	server := &ServerMonitor{
		ClusterGroup: cluster,
		Pass:         "",
	}

	tmpDir := t.TempDir()

	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	encFile := filepath.Join(tmpDir, "already.enc")
	if err := os.WriteFile(encFile, []byte("encrypted content"), 0o600); err != nil {
		t.Fatalf("failed to write .enc file: %v", err)
	}

	_, err := server.encryptBackupDirectoryPerFile(tmpDir, false)
	if err == nil {
		t.Fatal("expected error for no eligible files")
	}
	if !strings.Contains(err.Error(), "no regular files encrypted in directory") {
		t.Errorf("expected 'no regular files encrypted in directory' error, got: %v", err)
	}
	if strings.Contains(err.Error(), "backup encryption passphrase is empty") {
		t.Error("should not require passphrase when no eligible files exist")
	}
}

func TestDecryptBackupDirectoryPerFile_NoEncryptedFiles_DoesNotRequirePassphrase(t *testing.T) {
	os.Unsetenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE")

	cluster := &Cluster{}
	cluster.Conf = &config.Config{}

	server := &ServerMonitor{
		ClusterGroup: cluster,
		Pass:         "",
	}

	tmpDir := t.TempDir()

	plainFile := filepath.Join(tmpDir, "data.txt")
	if err := os.WriteFile(plainFile, []byte("plain text"), 0o600); err != nil {
		t.Fatalf("failed to write plain file: %v", err)
	}

	_, err := server.decryptBackupDirectoryPerFile(tmpDir)
	if err == nil {
		t.Fatal("expected error for no encrypted files")
	}
	if !strings.Contains(err.Error(), "no encrypted files found for per-file decrypt") {
		t.Errorf("expected 'no encrypted files found for per-file decrypt' error, got: %v", err)
	}
	if strings.Contains(err.Error(), "backup encryption passphrase is empty") {
		t.Error("should not require passphrase when no encrypted files exist")
	}
}

func TestPerFileDirectoryEncryptDecryptRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl binary not available")
	}

	t.Setenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE", "roundtrip-passphrase")

	cluster := &Cluster{}
	cluster.Conf = &config.Config{}
	cluster.Conf.Verbose = false

	server := &ServerMonitor{
		ClusterGroup: cluster,
	}

	sourceDir := t.TempDir()
	nestedDir := filepath.Join(sourceDir, "nested")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	fileA := filepath.Join(sourceDir, "a.sql")
	fileB := filepath.Join(nestedDir, "b.txt")
	contentA := []byte("CREATE TABLE roundtrip_a(id INT);\n")
	contentB := []byte("roundtrip payload B\n")

	if err := os.WriteFile(fileA, contentA, 0o600); err != nil {
		t.Fatalf("write fileA: %v", err)
	}
	if err := os.WriteFile(fileB, contentB, 0o600); err != nil {
		t.Fatalf("write fileB: %v", err)
	}

	encCount, err := server.encryptBackupDirectoryPerFile(sourceDir, false)
	if err != nil {
		t.Fatalf("encryptBackupDirectoryPerFile: %v", err)
	}
	if encCount != 2 {
		t.Fatalf("encrypted count=%d, want 2", encCount)
	}

	if _, err := os.Stat(fileA); !os.IsNotExist(err) {
		t.Fatalf("plaintext fileA should be removed, err=%v", err)
	}
	if _, err := os.Stat(fileB); !os.IsNotExist(err) {
		t.Fatalf("plaintext fileB should be removed, err=%v", err)
	}
	if _, err := os.Stat(fileA + ".enc"); err != nil {
		t.Fatalf("encrypted fileA missing: %v", err)
	}
	if _, err := os.Stat(fileB + ".enc"); err != nil {
		t.Fatalf("encrypted fileB missing: %v", err)
	}

	decCount, err := server.decryptBackupDirectoryPerFile(sourceDir)
	if err != nil {
		t.Fatalf("decryptBackupDirectoryPerFile: %v", err)
	}
	if decCount != 2 {
		t.Fatalf("decrypted count=%d, want 2", decCount)
	}

	gotA, err := os.ReadFile(fileA)
	if err != nil {
		t.Fatalf("read restored fileA: %v", err)
	}
	gotB, err := os.ReadFile(fileB)
	if err != nil {
		t.Fatalf("read restored fileB: %v", err)
	}
	if string(gotA) != string(contentA) {
		t.Fatalf("restored fileA mismatch")
	}
	if string(gotB) != string(contentB) {
		t.Fatalf("restored fileB mismatch")
	}

	if _, err := os.Stat(fileA + ".enc"); !os.IsNotExist(err) {
		t.Fatalf("encrypted fileA should be removed after decrypt, err=%v", err)
	}
	if _, err := os.Stat(fileB + ".enc"); !os.IsNotExist(err) {
		t.Fatalf("encrypted fileB should be removed after decrypt, err=%v", err)
	}
}
