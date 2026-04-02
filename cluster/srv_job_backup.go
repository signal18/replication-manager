// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"archive/tar"
	"bufio"
	"bytes"
	stdgzip "compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	gzip "github.com/klauspost/pgzip"
	dumplingext "github.com/pingcap/dumpling/v4/export"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/misc"
	river "github.com/signal18/replication-manager/utils/river"
	"github.com/signal18/replication-manager/utils/splitdump"
	"github.com/signal18/replication-manager/utils/state"
)

var errJobCanceledByUser = errors.New("job canceled by user")
var errBackupEncryptionUnsupported = errors.New("backup encryption supports files only")

const backupEncryptionToolOpenSSLEnc = "openssl-enc"
const backupEncryptionModeArchive = "archive"
const backupEncryptionModePerFile = "per-file"
const backupEncryptionHeaderMagic = "RMENC1\n"

type backupEncryptionKeyringConfig struct {
	ActiveKeyID        string                             `json:"activeKeyId"`
	LegacyDefaultKeyID string                             `json:"legacyDefaultKeyId,omitempty"`
	Keys               []backupEncryptionKeyringConfigKey `json:"keys"`
}

type backupEncryptionKeyringConfigKey struct {
	ID         string `json:"id"`
	Passphrase string `json:"passphrase"`
	State      string `json:"state"`
}

type backupEncryptionArtifactHeader struct {
	KeyID  string `json:"keyId"`
	Cipher string `json:"cipher"`
	Tool   string `json:"tool"`
}

type backupPassphraseSource int

const (
	backupPassphraseSourceNone backupPassphraseSource = iota
	backupPassphraseSourceEnv
	backupPassphraseSourceConfig
	backupPassphraseSourceSponsor
	backupPassphraseSourceAPIInternal
	backupPassphraseSourceAPIExternal
)

func (source backupPassphraseSource) String() string {
	switch source {
	case backupPassphraseSourceEnv:
		return "REPLICATION_MANAGER_BACKUP_PASSPHRASE env var"
	case backupPassphraseSourceConfig:
		return "backup-encryption-passphrase config"
	case backupPassphraseSourceSponsor:
		return "cloud18-sponsor-user-credentials (sponsor role password)"
	case backupPassphraseSourceAPIInternal:
		return "api-credentials (internal admin password)"
	case backupPassphraseSourceAPIExternal:
		return "api-credentials-external (external admin password)"
	default:
		return "none"
	}
}

func (server *ServerMonitor) resolveBackupEncryptionPassphrase() string {
	pass, _, _ := server.resolveBackupEncryptionPassphraseWithSource()
	return pass
}

func (server *ServerMonitor) resolveBackupEncryptionPassphraseWithSource() (passphrase string, source backupPassphraseSource, explicit bool) {
	envPassphrase := strings.TrimSpace(os.Getenv("REPLICATION_MANAGER_BACKUP_PASSPHRASE"))
	if envPassphrase != "" {
		return envPassphrase, backupPassphraseSourceEnv, true
	}
	if server != nil && server.ClusterGroup != nil {
		configPassphrase := strings.TrimSpace(server.ClusterGroup.Conf.GetDecryptedValue("backup-encryption-passphrase"))
		if configPassphrase == "" {
			configPassphrase = strings.TrimSpace(server.ClusterGroup.Conf.BackupEncryptionPassphrase)
		}
		if configPassphrase != "" {
			return configPassphrase, backupPassphraseSourceConfig, true
		}
		sponsorPassphrase := strings.TrimSpace(server.ClusterGroup.GetSponsorPass())
		if sponsorPassphrase != "" {
			return sponsorPassphrase, backupPassphraseSourceSponsor, false
		}
		adminPassphrase := server.resolveAdminAPIPasswordFromSecret("api-credentials")
		if adminPassphrase != "" {
			return adminPassphrase, backupPassphraseSourceAPIInternal, false
		}
		adminExternalPassphrase := server.resolveAdminAPIPasswordFromSecret("api-credentials-external")
		if adminExternalPassphrase != "" {
			return adminExternalPassphrase, backupPassphraseSourceAPIExternal, false
		}
	}
	return "", backupPassphraseSourceNone, false
}

func (server *ServerMonitor) resolveBackupEncryptionPassphraseForUse() (string, error) {
	passphrase, source, _ := server.resolveBackupEncryptionPassphraseWithSource()
	if passphrase == "" {
		return "", errors.New("backup encryption passphrase is empty")
	}
	server.warnIfBackupPassphraseFallback(source)
	return passphrase, nil
}

func (server *ServerMonitor) resolveAdminAPIPasswordFromSecret(secretKey string) string {
	if server == nil || server.ClusterGroup == nil {
		return ""
	}
	credentials := strings.TrimSpace(server.ClusterGroup.Conf.GetDecryptedValue(secretKey))
	if credentials == "" {
		return ""
	}
	for _, credential := range strings.Split(credentials, ",") {
		user, pass := misc.SplitPair(strings.TrimSpace(credential))
		if user == "admin" {
			return strings.TrimSpace(pass)
		}
	}
	return ""
}

func (server *ServerMonitor) resolveOldSponsorPassphrases() []string {
	if server == nil || server.ClusterGroup == nil {
		return nil
	}
	conf := server.ClusterGroup.Conf
	secret := conf.Secrets["cloud18-sponsor-user-credentials"]
	seen := make(map[string]struct{})
	var out []string
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		_, pass := misc.SplitPair(raw)
		pass = strings.TrimSpace(pass)
		if pass == "" {
			return
		}
		if _, dup := seen[pass]; dup {
			return
		}
		seen[pass] = struct{}{}
		out = append(out, pass)
	}
	// In-memory chain (current session rotations)
	add(secret.OldValue)
	for _, entry := range secret.History {
		add(entry.Value)
	}
	// Persisted chain (survives restarts)
	if raw := strings.TrimSpace(conf.GetDecryptedValue("cloud18-sponsor-credentials-history")); raw != "" {
		var persisted []config.SecretHistoryEntry
		if err := json.Unmarshal([]byte(raw), &persisted); err == nil {
			for _, entry := range persisted {
				add(entry.Value)
			}
		}
	}
	return out
}

func (server *ServerMonitor) warnIfBackupPassphraseFallback(source backupPassphraseSource) {
	if server == nil || server.ClusterGroup == nil {
		return
	}
	if source == backupPassphraseSourceNone {
		return
	}
	if source == backupPassphraseSourceEnv || source == backupPassphraseSourceConfig {
		return
	}
	cluster := server.ClusterGroup
	var sourceLabel string
	switch source {
	case backupPassphraseSourceSponsor:
		sourceLabel = "cloud18-sponsor-user-credentials (sponsor role password)"
	case backupPassphraseSourceAPIInternal:
		sourceLabel = "api-credentials (internal admin password)"
	case backupPassphraseSourceAPIExternal:
		sourceLabel = "api-credentials-external (external admin password)"
	default:
		sourceLabel = source.String()
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
		"Backup encryption using fallback passphrase source: %s (server: %s). "+
			"This source may change after credential rotation, making older backups undecryptable. "+
			"Set explicit 'backup-encryption-passphrase' or REPLICATION_MANAGER_BACKUP_PASSPHRASE env var to preserve decryptability.",
		sourceLabel, server.URL)
}

func (server *ServerMonitor) ensureOpenSSLAvailableForBackup() error {
	if server == nil || server.ClusterGroup == nil {
		return errors.New("cluster group is nil")
	}
	cluster := server.ClusterGroup
	if !cluster.Conf.BackupEncryptionEnabled {
		return nil
	}
	if _, err := exec.LookPath("openssl"); err != nil {
		return fmt.Errorf("backup encryption requires openssl binary in PATH: %w", err)
	}
	keyring, err := server.parseBackupEncryptionKeyring()
	if err != nil {
		return err
	}
	if keyring != nil {
		if pass, ok := keyring.passphraseByKeyID(keyring.ActiveKeyID); ok && strings.TrimSpace(pass) != "" {
			return nil
		}
		return fmt.Errorf("invalid backup-encryption-keyring: activeKeyId %q not found", keyring.ActiveKeyID)
	}
	passphrase, source, explicit := server.resolveBackupEncryptionPassphraseWithSource()
	if passphrase == "" {
		return errors.New("backup encryption passphrase is empty")
	}
	if cluster.Conf.BackupEncryptionRequireExplicitPassphrase && !explicit {
		return fmt.Errorf("backup encryption blocked by backup-encryption-require-explicit-passphrase: current passphrase source (%s) is a rotating credential which may change. Set explicit 'backup-encryption-passphrase' or REPLICATION_MANAGER_BACKUP_PASSPHRASE env var to enable encryption", source)
	}
	return nil
}

func normalizeBackupEncryptionDirectoryFormat(value string) string {
	format := strings.ToLower(strings.TrimSpace(value))
	switch format {
	case "", "tar.gz":
		return "tar.gz"
	case "tar":
		return "tar"
	default:
		return "tar.gz"
	}
}

func normalizeBackupEncryptionDirectoryMode(value string) string {
	mode := strings.ToLower(strings.TrimSpace(value))
	switch mode {
	case "", backupEncryptionModeArchive:
		return backupEncryptionModeArchive
	case backupEncryptionModePerFile:
		return backupEncryptionModePerFile
	default:
		return backupEncryptionModeArchive
	}
}

func normalizeBackupEncryptionKeyState(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func decodeBackupEncryptionJSONStrict(data string, target interface{}) error {
	dec := json.NewDecoder(strings.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected trailing JSON data")
	}
	return nil
}

func (server *ServerMonitor) parseBackupEncryptionKeyring() (*backupEncryptionKeyringConfig, error) {
	if server == nil || server.ClusterGroup == nil || server.ClusterGroup.Conf == nil {
		return nil, nil
	}
	clusterConf := server.ClusterGroup.Conf
	raw := strings.TrimSpace(clusterConf.GetDecryptedValue("backup-encryption-keyring"))
	if raw == "" {
		raw = strings.TrimSpace(clusterConf.BackupEncryptionKeyring)
	}
	if raw == "" {
		return nil, nil
	}

	var keyring backupEncryptionKeyringConfig
	if err := decodeBackupEncryptionJSONStrict(raw, &keyring); err != nil {
		return nil, fmt.Errorf("invalid backup-encryption-keyring JSON: %w", err)
	}

	keyring.ActiveKeyID = strings.TrimSpace(keyring.ActiveKeyID)
	keyring.LegacyDefaultKeyID = strings.TrimSpace(keyring.LegacyDefaultKeyID)
	if keyring.ActiveKeyID == "" {
		return nil, errors.New("invalid backup-encryption-keyring: activeKeyId is required")
	}
	if len(keyring.Keys) == 0 {
		return nil, errors.New("invalid backup-encryption-keyring: keys must not be empty")
	}

	seenIDs := make(map[string]struct{}, len(keyring.Keys))
	activeCount := 0
	activeFound := false
	idExists := make(map[string]struct{}, len(keyring.Keys))

	for i := range keyring.Keys {
		entry := &keyring.Keys[i]
		entry.ID = strings.TrimSpace(entry.ID)
		entry.Passphrase = strings.TrimSpace(entry.Passphrase)
		entry.State = normalizeBackupEncryptionKeyState(entry.State)

		if entry.ID == "" {
			return nil, fmt.Errorf("invalid backup-encryption-keyring: keys[%d].id is required", i)
		}
		if _, exists := seenIDs[entry.ID]; exists {
			return nil, fmt.Errorf("invalid backup-encryption-keyring: duplicate key id %q", entry.ID)
		}
		seenIDs[entry.ID] = struct{}{}
		idExists[entry.ID] = struct{}{}

		if entry.Passphrase == "" {
			return nil, fmt.Errorf("invalid backup-encryption-keyring: keys[%d].passphrase must not be empty", i)
		}

		switch entry.State {
		case "active":
			activeCount++
			if entry.ID == keyring.ActiveKeyID {
				activeFound = true
			}
		case "decrypt-only":
		default:
			return nil, fmt.Errorf("invalid backup-encryption-keyring: keys[%d].state must be active|decrypt-only", i)
		}
	}

	if activeCount != 1 {
		return nil, fmt.Errorf("invalid backup-encryption-keyring: exactly one key must be state=active (got %d)", activeCount)
	}
	if !activeFound {
		return nil, fmt.Errorf("invalid backup-encryption-keyring: activeKeyId %q must reference a key with state=active", keyring.ActiveKeyID)
	}
	if keyring.LegacyDefaultKeyID != "" {
		if _, ok := idExists[keyring.LegacyDefaultKeyID]; !ok {
			return nil, fmt.Errorf("invalid backup-encryption-keyring: legacyDefaultKeyId %q not found in keys", keyring.LegacyDefaultKeyID)
		}
	}

	return &keyring, nil
}

func (kr *backupEncryptionKeyringConfig) passphraseByKeyID(keyID string) (string, bool) {
	if kr == nil {
		return "", false
	}
	target := strings.TrimSpace(keyID)
	if target == "" {
		return "", false
	}
	for _, entry := range kr.Keys {
		if entry.ID == target {
			return entry.Passphrase, true
		}
	}
	return "", false
}

func (server *ServerMonitor) resolveBackupEncryptionPassphraseForEncrypt() (passphrase, keyID string, err error) {
	keyring, err := server.parseBackupEncryptionKeyring()
	if err != nil {
		return "", "", err
	}
	if keyring != nil {
		pass, ok := keyring.passphraseByKeyID(keyring.ActiveKeyID)
		if !ok || strings.TrimSpace(pass) == "" {
			return "", "", fmt.Errorf("invalid backup-encryption-keyring: activeKeyId %q not found", keyring.ActiveKeyID)
		}
		return pass, keyring.ActiveKeyID, nil
	}
	pass, err := server.resolveBackupEncryptionPassphraseForUse()
	if err != nil {
		return "", "", err
	}
	return pass, "", nil
}

func archiveSuffixForDirectoryFormat(format string) string {
	if format == "tar" {
		return ".tar"
	}
	return ".tar.gz"
}

func writeTarArchiveFromDirectory(sourceDir string, writer io.Writer) error {
	cleanSource := filepath.Clean(sourceDir)
	rootParent := filepath.Dir(cleanSource)

	tw := tar.NewWriter(writer)
	defer tw.Close()

	return filepath.Walk(cleanSource, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(rootParent, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		var linkTarget string
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}

		header, err := tar.FileInfoHeader(info, linkTarget)
		if err != nil {
			return err
		}
		header.Name = rel
		if info.IsDir() && !strings.HasSuffix(header.Name, "/") {
			header.Name += "/"
		}

		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, file)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}

func createDirectoryArchive(sourceDir, archivePath, format string) error {
	file, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()

	if format == "tar" {
		if err := writeTarArchiveFromDirectory(sourceDir, file); err != nil {
			return err
		}
		return file.Sync()
	}

	gzw := stdgzip.NewWriter(file)
	if err := writeTarArchiveFromDirectory(sourceDir, gzw); err != nil {
		_ = gzw.Close()
		return err
	}
	if err := gzw.Close(); err != nil {
		return err
	}
	return file.Sync()
}

func (server *ServerMonitor) encryptBackupDirectoryPerFile(sourceDir string, keepPlain bool) (int, error) {
	password, keyID, err := server.resolveBackupEncryptionPassphraseForEncrypt()
	if err != nil {
		return 0, err
	}
	return server.encryptBackupDirectoryPerFileWithPassphrase(sourceDir, keepPlain, password, keyID)
}

func (server *ServerMonitor) encryptBackupDirectoryPerFileWithPassphrase(sourceDir string, keepPlain bool, password, keyID string) (int, error) {
	cluster := server.ClusterGroup
	encryptedCount := 0

	err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"Skipping non-regular file during per-file encryption: %s", path)
			return nil
		}
		if strings.HasSuffix(strings.ToLower(path), ".enc") {
			return nil
		}

		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0o600
		}
		encPath := path + ".enc"
		if err := server.encryptBackupFileStreamWithHeader(path, encPath, password, keyID, mode); err != nil {
			return fmt.Errorf("failed to encrypt %s: %w", path, err)
		}
		encInfo, err := os.Stat(encPath)
		if err != nil {
			return err
		}
		if encInfo.Size() == 0 {
			_ = os.Remove(encPath)
			return fmt.Errorf("encrypted file is empty: %s", encPath)
		}
		if !keepPlain {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("failed to remove plaintext file %s: %w", path, err)
			}
		}
		encryptedCount++
		return nil
	})
	if err != nil {
		return encryptedCount, err
	}
	if encryptedCount == 0 {
		return 0, errors.New("no regular files encrypted in directory")
	}
	return encryptedCount, nil
}

func (server *ServerMonitor) decryptBackupDirectoryPerFile(sourceDir string) (int, error) {
	decryptedCount := 0

	err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".enc") {
			return nil
		}

		destPath := strings.TrimSuffix(path, ".enc")
		if err := server.decryptBackupFileToPathWithMetadata(path, destPath, nil); err != nil {
			return fmt.Errorf("failed to decrypt %s: %w", path, err)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("failed to remove encrypted file %s after decrypt: %w", path, err)
		}
		decryptedCount++
		return nil
	})
	if err != nil {
		return decryptedCount, err
	}
	if decryptedCount == 0 {
		return 0, errors.New("no encrypted files found for per-file decrypt")
	}
	return decryptedCount, nil
}

func (server *ServerMonitor) encryptDirectoryBackupMetadata(meta *backupmgr.BackupMetadata, backupKind string) error {
	if server == nil {
		return errors.New("server is nil")
	}
	if meta == nil {
		return errors.New("backup metadata is nil")
	}
	cluster := server.ClusterGroup
	if cluster == nil {
		return errors.New("cluster group is nil")
	}

	sourceDir := strings.TrimSpace(meta.Dest)
	if sourceDir == "" {
		return errors.New("backup destination is empty")
	}
	info, err := os.Stat(sourceDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errBackupEncryptionUnsupported
	}

	mode := normalizeBackupEncryptionDirectoryMode(cluster.Conf.BackupEncryptionDirectoryMode)
	if mode == backupEncryptionModePerFile {
		password, keyID, err := server.resolveBackupEncryptionPassphraseForEncrypt()
		if err != nil {
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Encrypting directory backup %s using per-file mode", sourceDir)
		encryptedCount, err := server.encryptBackupDirectoryPerFileWithPassphrase(sourceDir, cluster.Conf.BackupEncryptionKeepPlainDir, password, keyID)
		if err != nil {
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Encrypted %d files in directory backup %s", encryptedCount, sourceDir)
		meta.Dest = sourceDir
		meta.Encrypted = true
		meta.EncryptionAlgo = "aes-256-cbc"
		meta.EncryptionTool = backupEncryptionToolOpenSSLEnc
		meta.EncryptionMode = backupEncryptionModePerFile
		if keyID != "" {
			meta.EncryptionVersion = 1
			meta.EncryptionKeyID = keyID
		}
		if err := meta.GetSizeAndFileCount(); err != nil {
			return err
		}
		return nil
	}

	format := normalizeBackupEncryptionDirectoryFormat(cluster.Conf.BackupEncryptionDirectoryFormat)
	archivePath := filepath.Clean(sourceDir) + archiveSuffixForDirectoryFormat(format)
	archiveTmpPath := archivePath + ".tmp"
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Archiving directory backup %s using format %s", sourceDir, format)
	_ = os.Remove(archiveTmpPath)
	if err := createDirectoryArchive(sourceDir, archiveTmpPath, format); err != nil {
		return fmt.Errorf("failed to archive backup directory %s: %w", sourceDir, err)
	}
	archiveInfo, err := os.Stat(archiveTmpPath)
	if err != nil {
		_ = os.Remove(archiveTmpPath)
		return err
	}
	if archiveInfo.Size() == 0 {
		_ = os.Remove(archiveTmpPath)
		return fmt.Errorf("archived backup is empty: %s", archiveTmpPath)
	}
	if err := os.Rename(archiveTmpPath, archivePath); err != nil {
		_ = os.Remove(archiveTmpPath)
		return err
	}

	workingMeta := *meta
	workingMeta.Dest = archivePath
	workingMeta.Compressed = format == "tar.gz"
	keepPlainArchive := cluster.Conf.BackupEncryptionKeepPlainDir
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Encrypting archived directory backup %s", archivePath)
	if err := server.encryptBackupMetadataFile(&workingMeta, backupKind, keepPlainArchive); err != nil {
		return err
	}

	if !cluster.Conf.BackupEncryptionKeepPlainDir {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Removing plaintext backup directory %s after successful encryption", sourceDir)
		if err := os.RemoveAll(sourceDir); err != nil {
			return fmt.Errorf("failed to remove plaintext backup directory %s: %w", sourceDir, err)
		}
	}

	meta.Dest = workingMeta.Dest
	meta.Encrypted = workingMeta.Encrypted
	meta.EncryptionAlgo = workingMeta.EncryptionAlgo
	meta.EncryptionTool = workingMeta.EncryptionTool
	meta.EncryptionMode = backupEncryptionModeArchive
	meta.EncryptionVersion = workingMeta.EncryptionVersion
	meta.EncryptionKeyID = workingMeta.EncryptionKeyID
	meta.Compressed = workingMeta.Compressed
	meta.Size = workingMeta.Size
	meta.FileCount = workingMeta.FileCount

	return nil
}

func (server *ServerMonitor) JobBackupPhysical() error {
	return server.JobBackupPhysicalWithOptions(BackupRunOptions{})
}

func (server *ServerMonitor) AfterJobProcess(conn *sqlx.Conn, task DBTask) error {
	//Still use done=1 and state=3 to prevent unwanted changes
	query := "UPDATE replication_manager_schema.jobs SET result=CONCAT(result,'%s'), state=%d WHERE id=%d AND done=1 AND state=3"
	errStr := ""
	cluster := server.ClusterGroup
	if task.task == "" {
		return errors.New("Cannot check task. Task name is empty!")
	}

	switch task.task {
	case config.ConstBackupPhysicalTypeXtrabackup, config.ConstBackupPhysicalTypeMariaBackup:
		if server.LastBackupMeta.Physical != nil && !server.LastBackupMeta.Physical.IsAdhoc() {
			server.SetBackupPhysicalCookie(task.task)
		}
		if server.LastBackupMeta.Physical != nil {
			server.LastBackupMeta.Physical.Completed = true
		}
		errStr = "Backup completed"
	case "reseedxtrabackup", "reseedmariabackup", "flashbackxtrabackup", "flashbackmariabackup":
		if server.HasWaitResticReseedCookie() {
			if err := server.DelWaitResticReseedCookie(); err != nil {
				if cluster != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn,
						"Failed to clear restic reseed cookie after job completion for %s: %s", task.task, err)
				}
			}
		}
		defer server.cleanupResticReseedForTask(task.task, "job-finished")
		if server.HasReseedingState(task.task) {
			defer server.SetInReseedBackup("")
		}
		if !server.PointInTimeMeta.IsInPITR {
			if _, err := server.StartSlave(); err != nil {
				errStr = err.Error()
				// Only set as failed if no error connection
				if server.Conn != nil {
					// Set state as 6 to differ post-job error with in-job error (code: 5)
					server.ConnExecQueryWithTimeout(conn, JobTimeout, fmt.Sprintf(query, "\n"+errStr, JobStateErrorAfter, task.id))
				}
				return err
			}
		}
	}
	server.ConnExecQueryWithTimeout(conn, JobTimeout, fmt.Sprintf(query, errStr, JobStateSuccess, task.id))
	return nil
}

func (server *ServerMonitor) JobBackupPhysicalWithOptions(opts BackupRunOptions) error {
	//server can be nil as no dicovered master
	if server == nil {
		return nil
	}

	if server.IsDown() {
		return nil
	}

	cluster := server.ClusterGroup
	if err := server.ensureOpenSSLAvailableForBackup(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", err)
		return err
	}
	backupLine := server.resolveBackupLine(opts)
	isAdhoc := backupLine == backupmgr.BackupLineAdhoc
	resticEnabled := server.shouldRunRestic(opts)

	if cluster.IsInBackup() {
		cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Physical", cluster.Conf.BackupPhysicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		time.Sleep(1 * time.Second)

		return server.JobBackupPhysicalWithOptions(opts)
	}

	cluster.SetInPhysicalBackupState(true)

	// CRITICAL FIX: For physical backups, Restic transition happens in JobFinishReceiveFile
	// We DON'T set InResticPhysicalBackup here because the backup hasn't completed yet
	// The flag will be set atomically in AfterJobProcess when the backup file is received

	// Prevent backing up with incompatible tools
	if server.IsMariaDB() && server.DBVersion.GreaterEqual("10.1") && cluster.Conf.BackupPhysicalType == "xtrabackup" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Master %s MariaDB version is greater than 10.1. Changing from xtrabackup to mariabackup as physical backup tools", server.URL)
		cluster.Conf.BackupPhysicalType = config.ConstBackupPhysicalTypeMariaBackup
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive physical backup %s (%s line) request for server: %s", cluster.Conf.BackupPhysicalType, backupLine, server.URL)

	now := time.Now()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Physical backup %s started at %s for: %s", cluster.Conf.BackupPhysicalType, now.Format(time.RFC3339), server.URL)
	var port string
	var err error
	var backupext string = ".xbtream"
	var dest string = server.GetMyBackupDirectory() + cluster.Conf.BackupPhysicalType
	if isAdhoc {
		dest = fmt.Sprintf("%s.%d", dest, now.Unix())
	}
	if cluster.Conf.CompressBackups {
		backupext = backupext + ".gz"
		dest = dest + backupext
		if cluster.Conf.BackupKeepUntilValid && !isAdhoc {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rename previous backup to .old")
			exec.Command("mv", dest, dest+".old").Run()
		}
		port, err = cluster.SSTRunReceiverToGZip(server, dest, ConstJobCreateFile, cluster.Conf.BackupPhysicalType)
	} else {
		dest = dest + backupext
		if cluster.Conf.BackupKeepUntilValid && !isAdhoc {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rename previous backup to .old")
			exec.Command("mv", dest, dest+".old").Run()
		}
		port, err = cluster.SSTRunReceiverToFile(server, dest, ConstJobCreateFile, cluster.Conf.BackupPhysicalType)
	}

	if err != nil {
		cluster.SetInPhysicalBackupState(false)
		return nil
	}

	// Reset last backup meta
	var prevId int64
	if !isAdhoc {
		prev := cluster.BackupMetaMap.GetPreviousBackup(cluster.Conf.BackupPhysicalType, server.URL)
		if prev != nil {
			prevId = prev.Id
		}
	}

	// Check for previous backup size
	if cluster.Conf.BackupCheckFreeSpace {
		err = cluster.CheckEstimatedBackupSize("physical")
		if err != nil {
			return err
		}
	}

	// Remove from backup list, since the file will be replaced
	if !cluster.Conf.BackupKeepUntilValid && !isAdhoc {
		cluster.BackupMetaMap.Delete(prevId)
	}

	server.LastBackupMeta.Physical = &backupmgr.BackupMetadata{
		Id:                now.Unix(),
		StartTime:         now,
		BackupMethod:      backupmgr.BackupMethodPhysical,
		BackupStrategy:    backupmgr.BackupStrategyFull,
		BackupTool:        cluster.Conf.BackupPhysicalType,
		Source:            server.URL,
		Dest:              dest,
		Compressed:        cluster.Conf.CompressBackups,
		Previous:          prevId,
		BackupLine:        backupLine,
		RetentionDays:     opts.RetentionDays,
		RetentionDuration: strings.TrimSpace(opts.RetentionDuration),
		ResticEnabled:     resticEnabled,
	}
	server.ensureBackupSessionID(server.LastBackupMeta.Physical, backupmgr.BackupMethodPhysical, now, backupLine)

	cluster.BackupMetaMap.Set(server.LastBackupMeta.Physical.Id, server.LastBackupMeta.Physical)

	_, err = server.JobInsertTask(cluster.Conf.BackupPhysicalType, port, cluster.Conf.MonitorAddress)
	if err != nil {
		cluster.SetInPhysicalBackupState(false)
	}

	return err
}

func (server *ServerMonitor) JobReseedPhysicalBackup(backtype string) error {
	cluster := server.ClusterGroup
	if backtype == "default" {
		backtype = cluster.Conf.BackupPhysicalType
	}

	// Prevent reseed with incompatible tools
	if server.IsMariaDB() && server.DBVersion.GreaterEqual("10.1") && backtype == "xtrabackup" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Node %s MariaDB version is greater than 10.1 and not compatible with xtrabackup. Cancelling reseed for data safety.", server.URL)
		return fmt.Errorf("Node %s MariaDB version is greater than 10.1 and not compatible with xtrabackup.", server.URL)
	}

	if !cluster.IsDiscovered() {
		return errors.New("Cluster not discovered yet")
	}

	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found. Cancel reseed physical backup")
	}

	useMaster := true
	backupext := ".xbtream"
	if cluster.Conf.CompressBackups {
		backupext = backupext + ".gz"
	}

	file := backtype + backupext
	backupfile := master.GetMyBackupDirectory() + file

	bckserver := cluster.GetBackupServer()
	if bckserver != nil && bckserver.HasBackupTypeCookie(backtype) {
		if _, err := os.Stat(bckserver.GetMyBackupDirectory() + file); err == nil {
			backupfile = bckserver.GetMyBackupDirectory() + file
			useMaster = false
		} else {
			//Remove false cookie
			bckserver.DelBackupTypeCookie(backtype)
		}
	}

	if useMaster {
		if _, err := os.Stat(backupfile); err != nil {
			//Remove false cookie
			master.DelBackupTypeCookie(backtype)
			return fmt.Errorf("Cancelling reseed. No backup file found on master for %s", backtype)
		}
		bckserver = master
	}

	err := cluster.CheckPhysicalBackupToolVersion(bckserver)
	if err != nil && cluster.Conf.BackupRestoreVersionStrict {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s version is not compatible with restore version on %s. Cancelling reseed for data safety.", backtype, server.URL)
		return fmt.Errorf("Node %s backup tool version is not compatible with restore version.", server.URL)
	}

	//Delete wait physical backup cookie
	server.DelWaitPhysicalBackupCookie()

	task := "reseed" + backtype
	if ok, currentTask := server.TrySetInReseedBackup(task); !ok {
		err := fmt.Errorf("Server is in reseeding state by %s", currentTask)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Concurrent reseed blocked: server %s already reseeding with %s, cannot start %s", server.URL, currentTask, task)
		return err
	}

	// If reset failed, better to stop PITR
	if server.PointInTimeMeta.IsInPITR {
		server.StopSlave()
		_, err := server.ResetSlave()
		if err != nil {
			if mysqlErr, ok := err.(*mysql.MySQLError); ok && mysqlErr.Number != 1617 {
				if server.HasReseedingState(task) {
					server.SetInReseedBackup("")
				}
				return err
			}
		}
		server.SetState(stateUnconn)

		cluster.Conf.BackupPhysicalType = backtype
	}

	_, err = server.JobInsertTask(task, server.SSTPort, cluster.Conf.MonitorAddress)
	if err != nil {
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Receive reseed physical backup %s request for server: %s %s", backtype, server.URL, err)
		return err
	}

	// Set replication master to current master if not PITR
	if !server.PointInTimeMeta.IsInPITR {
		logs, err := server.StopSlave()
		if err != nil {
			cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed stop slave on server: %s %s", server.URL, err)
		}

		logs, err = cluster.pointSlaveToMasterWithMode(server, "SLAVE_POS")
		if err != nil {
			cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Reseed can't changing master for physical backup %s request for server: %s %s", backtype, server.URL, err)
			return err
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive reseed physical backup %s request for server: %s", backtype, server.URL)

	return nil
}

func (server *ServerMonitor) JobReseedPhysicalBackupWithPayload(backtype, backupPath string, extraPayload map[string]string) error {
	cluster := server.ClusterGroup
	if backtype == "default" {
		backtype = cluster.Conf.BackupPhysicalType
	}
	if backupPath == "" {
		return errors.New("backup path is empty")
	}
	backupfile := backupPath

	// Prevent reseed with incompatible tools
	if server.IsMariaDB() && server.DBVersion.GreaterEqual("10.1") && backtype == "xtrabackup" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Node %s MariaDB version is greater than 10.1 and not compatible with xtrabackup. Cancelling reseed for data safety.", server.URL)
		return fmt.Errorf("Node %s MariaDB version is greater than 10.1 and not compatible with xtrabackup.", server.URL)
	}

	if !cluster.IsDiscovered() {
		return errors.New("Cluster not discovered yet")
	}

	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found. Cancel reseed physical backup")
	}

	err := cluster.CheckPhysicalBackupToolVersion(master)
	if err != nil && cluster.Conf.BackupRestoreVersionStrict {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s version is not compatible with restore version on %s. Cancelling reseed for data safety.", backtype, server.URL)
		return fmt.Errorf("Node %s backup tool version is not compatible with restore version.", server.URL)
	}

	//Delete wait physical backup cookie
	server.DelWaitPhysicalBackupCookie()

	task := "reseed" + backtype
	if ok, currentTask := server.TrySetInReseedBackup(task); !ok {
		err := fmt.Errorf("Server is in reseeding state by %s", currentTask)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Concurrent reseed blocked: server %s already reseeding with %s, cannot start %s", server.URL, currentTask, task)
		return err
	}

	// If reset failed, better to stop PITR
	if server.PointInTimeMeta.IsInPITR {
		server.StopSlave()
		_, err := server.ResetSlave()
		if err != nil {
			if mysqlErr, ok := err.(*mysql.MySQLError); ok && mysqlErr.Number != 1617 {
				if server.HasReseedingState(task) {
					server.SetInReseedBackup("")
				}
				return err
			}
		}
		server.SetState(stateUnconn)

		cluster.Conf.BackupPhysicalType = backtype
	}

	// Build comprehensive payload with backup metadata
	payload := map[string]string{
		"backup_path": backupfile,
		"backup_type": backtype,
		"server_url":  server.URL,
		"is_pitr":     fmt.Sprintf("%t", server.PointInTimeMeta.IsInPITR),
	}

	// Merge extra payload if provided
	for k, v := range extraPayload {
		payload[k] = v
	}

	payloadData, err := json.Marshal(payload)
	if err != nil {
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
		return fmt.Errorf("Failed to marshal payload: %v", err)
	}

	_, err = server.JobInsertTaskWithPayload(task, server.SSTPort, cluster.Conf.MonitorAddress, string(payloadData))
	if err != nil {
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Receive reseed physical backup %s request for server: %s %s", backtype, server.URL, err)
		return err
	}

	// Set replication master to current master if not PITR
	if !server.PointInTimeMeta.IsInPITR {
		logs, err := server.StopSlave()
		if err != nil {
			cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed stop slave on server: %s %s", server.URL, err)
		}

		logs, err = cluster.pointSlaveToMasterWithMode(server, "SLAVE_POS")
		if err != nil {
			cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Reseed can't changing master for physical backup %s request for server: %s %s", backtype, server.URL, err)
			return err
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive reseed physical backup %s from path %s request for server: %s", backtype, backupfile, server.URL)

	return nil
}

// JobReseedPhysicalBackupFromPath maintains backward compatibility by calling the new payload-aware function
func (server *ServerMonitor) JobReseedPhysicalBackupFromPath(backtype, backupPath string) error {
	return server.JobReseedPhysicalBackupWithPayload(backtype, backupPath, nil)
}

func (server *ServerMonitor) JobFlashbackPhysicalBackup() error {
	cluster := server.ClusterGroup

	if !cluster.IsDiscovered() {
		return errors.New("Cluster not discovered yet")
	}

	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found. Cancel flashback physical backup")
	}

	useSelfBackup := true
	backupext := ".xbtream"
	if cluster.Conf.CompressBackups {
		backupext = backupext + ".gz"
	}

	file := cluster.Conf.BackupPhysicalType + backupext
	backupfile := server.GetMyBackupDirectory() + file

	bckserver := cluster.GetBackupServer()
	if bckserver != nil && bckserver.HasBackupTypeCookie(cluster.Conf.BackupPhysicalType) {
		if _, err := os.Stat(bckserver.GetMyBackupDirectory() + file); err == nil {
			backupfile = bckserver.GetMyBackupDirectory() + file
			useSelfBackup = false
		} else {
			//Remove false cookie
			bckserver.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
		}
	}

	if useSelfBackup {
		if _, err := os.Stat(backupfile); err != nil {
			//Remove false cookie
			server.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
			return fmt.Errorf("Cancelling flashback. No backup file found on master for %s", cluster.Conf.BackupPhysicalType)
		}
	}

	//Delete wait physical backup cookie
	server.DelWaitPhysicalBackupCookie()

	task := "flashback" + cluster.Conf.BackupPhysicalType
	if ok, currentTask := server.TrySetInReseedBackup(task); !ok {
		err := fmt.Errorf("Server is in reseeding state by %s", currentTask)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Concurrent reseed blocked: server %s already reseeding with %s, cannot start %s", server.URL, currentTask, task)
		return err
	}

	_, err := server.JobInsertTask(task, server.SSTPort, cluster.Conf.MonitorAddress)
	if err != nil {
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
		return err
	}

	logs, err := server.StopSlave()
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed stop slave on server: %s %s", server.URL, err)
	}

	logs, err = cluster.pointSlaveToMasterWithMode(server, "SLAVE_POS")
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Flashback can't changing master for physical backup %s request for server: %s %s", cluster.Conf.BackupPhysicalType, server.URL, err)
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive flashback physical backup %s request for server: %s", cluster.Conf.BackupPhysicalType, server.URL)

	return nil
}

type logicalReseedPlan struct {
	task              string
	backtype          string
	backupfile        string
	meta              *backupmgr.BackupMetadata
	splitUser         bool
	splitUserOverride bool
	restoreUser       bool
	skipMetadata      bool
	isPITR            bool
	fromPath          bool
}

func buildLogicalReseedPayload(backtype, backupPath string, splitUser, splitUserOverride, skipMetadata, isPITR bool, serverURL string) (string, error) {
	payload := map[string]string{
		"backup_type":         strings.TrimSpace(backtype),
		"backup_path":         strings.TrimSpace(backupPath),
		"split_user":          fmt.Sprintf("%t", splitUser),
		"split_user_override": fmt.Sprintf("%t", splitUserOverride),
		"skip_metadata":       fmt.Sprintf("%t", skipMetadata),
		"is_pitr":             fmt.Sprintf("%t", isPITR),
		"server_url":          strings.TrimSpace(serverURL),
	}
	payloadData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("Failed to marshal logical reseed payload: %v", err)
	}
	return string(payloadData), nil
}

func snapshotLogicalBackupMeta(server *ServerMonitor) *backupmgr.BackupMetadata {
	if server == nil {
		return nil
	}
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()
	if server.LastBackupMeta.Logical == nil {
		return nil
	}
	metaCopy := *server.LastBackupMeta.Logical
	return &metaCopy
}

func (server *ServerMonitor) JobReseedLogicalBackup(ctx context.Context, backtype string) error {
	_, err := server.JobReseedLogicalBackupPrepare(ctx, backtype)
	return err
}

func (server *ServerMonitor) JobReseedLogicalBackupPrepare(ctx context.Context, backtype string) (*logicalReseedPlan, error) {
	var err error
	cluster := server.ClusterGroup
	if ctx == nil {
		ctx = context.Background()
	}
	if backtype == "default" {
		backtype = cluster.Conf.BackupLogicalType
	}
	if backtype == config.ConstBackupLogicalTypeDumpling {
		return nil, errors.New("Logical reseed with dumpling is not supported")
	}
	task := "reseed" + backtype
	isPITR := server.PointInTimeMeta.IsInPITR

	if !cluster.IsDiscovered() {
		return nil, errors.New("Cluster not discovered yet")
	}

	master := cluster.GetMaster()
	if master == nil {
		return nil, errors.New("No master found")
	}

	if _, err := os.Stat(cluster.GetMysqlclientPath()); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "File does not exist %s", cluster.GetMysqlclientPath())
		return nil, err
	}

	if backtype == config.ConstBackupLogicalTypeMydumper && cluster.VersionsMap.Get("mydumper") == nil {
		return nil, errors.New("No mydumper version found")
	}

	useMaster := true
	source := master
	var dest string
	var destCandidates []string
	switch backtype {
	case config.ConstBackupLogicalTypeMysqldump, "script":
		dest = "mysqldump.sql.gz"
		destCandidates = []string{"mysqldump.sql.gz", "splitdump"}
	case config.ConstBackupLogicalTypeMydumper:
		dest = "mydumper"
	case config.ConstBackupLogicalTypeDumpling:
		dest = "dumpling"
	}
	if len(destCandidates) == 0 {
		destCandidates = []string{dest}
	}

	var backupfile string
	bckserver := cluster.GetBackupServer()
	if bckserver != nil && bckserver.HasBackupTypeCookie(backtype) {
		if resolved, ok := resolveLogicalBackupPathFromMeta(bckserver, backtype); ok {
			backupfile = resolved
			useMaster = false
			source = bckserver
		} else if resolved, ok := findExistingBackupPath(bckserver, destCandidates); ok {
			backupfile = resolved
			useMaster = false
			source = bckserver
		} else {
			//Remove false cookie
			bckserver.DelBackupTypeCookie(backtype)
		}
	}

	if backupfile == "" {
		if resolved, ok := resolveLogicalBackupPathFromMeta(master, backtype); ok {
			backupfile = resolved
		} else if resolved, ok := findExistingBackupPath(master, destCandidates); ok {
			backupfile = resolved
		} else {
			backupfile = master.GetMyBackupDirectory() + dest
		}
	}

	if useMaster {
		if _, err := os.Stat(backupfile); err != nil {
			//Remove false cookie
			master.DelBackupTypeCookie(backtype)
			return nil, fmt.Errorf("No backup file found on master for %s", backtype)
		}

		bckserver = master
	}

	err = cluster.CheckLogicalBackupToolVersion(bckserver)
	if err != nil && cluster.Conf.BackupRestoreVersionStrict {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s version is not compatible with restore version on %s. Cancelling reseed for data safety.", backtype, server.URL)
		return nil, fmt.Errorf("Node %s backup tool version is not compatible with restore version.", server.URL)
	}

	if ok, currentTask := server.TrySetInReseedBackup(task); !ok {
		err := fmt.Errorf("Server is in reseeding state by %s", currentTask)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Concurrent reseed blocked: server %s already reseeding with %s, cannot start %s", server.URL, currentTask, task)
		return nil, err
	}

	resetReseed := func() {
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
	}

	//Delete wait logical backup cookie
	server.DelWaitLogicalBackupCookie()

	// If reset failed, better to stop PITR
	if isPITR {
		server.StopSlave()
		_, err := server.ResetSlave()
		if err != nil {
			if mysqlErr, ok := err.(*mysql.MySQLError); ok && mysqlErr.Number != 1617 {
				resetReseed()
				return nil, err
			}
		}
		server.SetState(stateUnconn)
	}

	meta := snapshotLogicalBackupMeta(source)
	splitUser := meta != nil && meta.SplitUser
	restoreUser := cluster.Conf.BackupRestoreMysqlUser && splitUser
	payload, err := buildLogicalReseedPayload(backtype, backupfile, splitUser, false, false, isPITR, server.URL)
	if err != nil {
		resetReseed()
		return nil, err
	}

	server.JobsUpdateState(task, "", 1, 0)
	server.JobsUpdatePayload(task, payload)
	cluster.SetState("WARN0075", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0075"], backtype, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})

	plan := &logicalReseedPlan{
		task:         task,
		backtype:     backtype,
		backupfile:   backupfile,
		meta:         meta,
		splitUser:    splitUser,
		restoreUser:  restoreUser,
		skipMetadata: false,
		isPITR:       isPITR,
		fromPath:     false,
	}

	return plan, nil
}

func (server *ServerMonitor) JobReseedLogicalBackupProcess(ctx context.Context, plan *logicalReseedPlan) error {
	if plan == nil {
		return errors.New("Logical reseed plan is nil")
	}
	if plan.task == "" {
		return errors.New("Logical reseed task is empty")
	}
	return server.ProcessReseedLogical(plan.task)
}

type JobReseedLogicalOptions struct {
	SplitUser    *bool
	SkipMetadata bool
}

func formatMysqlRestoreError(stderr string) string {
	const (
		maxLines = 6
		maxBytes = 2048
	)
	trimmed := strings.TrimSpace(stderr)
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		filtered = append(filtered, line)
	}
	if len(filtered) == 0 {
		return ""
	}
	var errLines []string
	for _, line := range filtered {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") || strings.Contains(lower, "fatal") {
			errLines = append(errLines, line)
		}
	}
	if len(errLines) == 0 {
		errLines = filtered
	}
	if len(errLines) > maxLines {
		errLines = errLines[len(errLines)-maxLines:]
	}
	msg := strings.Join(errLines, "\n")
	if len(msg) > maxBytes {
		msg = msg[:maxBytes-3] + "..."
	}
	return strings.TrimSpace(msg)
}

func (server *ServerMonitor) executeMysqlRestore(reader io.Reader, force bool) error {
	return server.executeMysqlRestoreContext(context.Background(), reader, force)
}

func (server *ServerMonitor) executeMysqlRestoreContext(ctx context.Context, reader io.Reader, force bool) error {
	if reader == nil {
		return fmt.Errorf("mysql restore reader is nil")
	}

	cluster := server.ClusterGroup
	if cluster == nil {
		return fmt.Errorf("cluster not available")
	}

	if ctx == nil {
		ctx = context.Background()
	}

	mysqlPath := cluster.GetMysqlclientPath()
	if strings.TrimSpace(mysqlPath) == "" {
		return fmt.Errorf("mysql client path is empty")
	}
	if _, err := os.Stat(mysqlPath); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModBackupStream,
			config.LvlErr,
			"mysql client not found at %s: %s", mysqlPath, err)
		return fmt.Errorf("mysql client not found at %s: %w", mysqlPath, err)
	}

	cliParams := append(cluster.GetDumpCredentials(server), server.GetSSLClientParam("client")...)
	cliParams = append(cliParams, strings.Split(cluster.Conf.BackupMysqlclientOptions, " ")...)
	if force && !slices.Contains(cliParams, "--force") {
		cliParams = append(cliParams, "--force")
	}
	cmd := exec.CommandContext(ctx, cluster.GetMysqlclientPath(), misc.RemoveEmptyString(cliParams)...)
	cmd.Stdin = reader
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errOutput := formatMysqlRestoreError(stderr.String())
		if errOutput == "" {
			errOutput = err.Error()
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModBackupStream,
			config.LvlErr,
			"mysql restore failed: %s", errOutput)
		return fmt.Errorf("mysql restore failed: %s: %w", errOutput, err)
	}

	return nil
}

func (server *ServerMonitor) JobReseedLogicalBackupFromPathWithOptions(ctx context.Context, backtype, backupPath string, opts JobReseedLogicalOptions) error {
	_, err := server.JobReseedLogicalBackupFromPathPrepare(ctx, backtype, backupPath, opts)
	return err
}

func (server *ServerMonitor) JobReseedLogicalBackupFromPathPrepare(ctx context.Context, backtype, backupPath string, opts JobReseedLogicalOptions) (*logicalReseedPlan, error) {
	var err error
	cluster := server.ClusterGroup
	if ctx == nil {
		ctx = context.Background()
	}
	if backtype == "default" {
		backtype = cluster.Conf.BackupLogicalType
	}
	if backtype == config.ConstBackupLogicalTypeDumpling {
		return nil, errors.New("Logical reseed with dumpling is not supported")
	}
	if backupPath == "" {
		return nil, errors.New("backup path is empty")
	}
	backupfile := backupPath
	task := "reseed" + backtype
	isPITR := server.PointInTimeMeta.IsInPITR

	if !cluster.IsDiscovered() {
		return nil, errors.New("Cluster not discovered yet")
	}

	master := cluster.GetMaster()
	if master == nil {
		return nil, errors.New("No master found")
	}

	if _, err := os.Stat(cluster.GetMysqlclientPath()); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "File does not exist %s", cluster.GetMysqlclientPath())
		return nil, err
	}

	if backtype == config.ConstBackupLogicalTypeMydumper && cluster.VersionsMap.Get("mydumper") == nil {
		return nil, errors.New("No mydumper version found")
	}

	if err = cluster.CheckLogicalBackupToolVersion(master); err != nil && cluster.Conf.BackupRestoreVersionStrict {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s version is not compatible with restore version on %s. Cancelling reseed for data safety.", backtype, server.URL)
		return nil, fmt.Errorf("Node %s backup tool version is not compatible with restore version.", server.URL)
	}

	if ok, currentTask := server.TrySetInReseedBackup(task); !ok {
		err := fmt.Errorf("Server is in reseeding state by %s", currentTask)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Concurrent reseed blocked: server %s already reseeding with %s, cannot start %s", server.URL, currentTask, task)
		return nil, err
	}
	resetReseed := func() {
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
	}

	//Delete wait logical backup cookie
	server.DelWaitLogicalBackupCookie()

	// If reset failed, better to stop PITR
	if isPITR {
		server.StopSlave()
		_, err := server.ResetSlave()
		if err != nil {
			if mysqlErr, ok := err.(*mysql.MySQLError); ok && mysqlErr.Number != 1617 {
				resetReseed()
				return nil, err
			}
		}
		server.SetState(stateUnconn)
	}

	meta := snapshotLogicalBackupMeta(master)
	splitUser := meta != nil && meta.SplitUser
	splitUserOverride := false
	if opts.SplitUser != nil {
		splitUser = *opts.SplitUser
		splitUserOverride = true
	}
	restoreUser := cluster.Conf.BackupRestoreMysqlUser && splitUser
	payload, err := buildLogicalReseedPayload(backtype, backupfile, splitUser, splitUserOverride, opts.SkipMetadata, isPITR, server.URL)
	if err != nil {
		resetReseed()
		return nil, err
	}

	_, err = server.JobInsertTaskWithPayload(task, "0", cluster.Conf.MonitorAddress, payload)
	if err != nil {
		resetReseed()
		return nil, err
	}

	plan := &logicalReseedPlan{
		task:              task,
		backtype:          backtype,
		backupfile:        backupfile,
		meta:              meta,
		splitUser:         splitUser,
		splitUserOverride: splitUserOverride,
		restoreUser:       restoreUser,
		skipMetadata:      opts.SkipMetadata,
		isPITR:            isPITR,
		fromPath:          true,
	}

	return plan, nil
}

func findExistingBackupPath(server *ServerMonitor, candidates []string) (string, bool) {
	for _, name := range candidates {
		path := server.GetMyBackupDirectory() + name
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	return "", false
}

func resolveLogicalBackupPathFromMeta(server *ServerMonitor, backtype string) (string, bool) {
	if server == nil {
		return "", false
	}
	server.backupMetaMutex.Lock()
	meta := server.LastBackupMeta.Logical
	if meta == nil || !meta.Completed || meta.IsAdhoc() {
		server.backupMetaMutex.Unlock()
		return "", false
	}
	if meta.BackupTool != "" && meta.BackupTool != backtype {
		server.backupMetaMutex.Unlock()
		return "", false
	}
	dest := strings.TrimSpace(meta.Dest)
	server.backupMetaMutex.Unlock()
	if dest == "" {
		return "", false
	}
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(server.GetMyBackupDirectory(), dest)
	}
	if _, err := os.Stat(dest); err != nil {
		return "", false
	}
	return dest, true
}

func resolvePhysicalBackupPathFromMeta(server *ServerMonitor, backtype string) (string, bool) {
	if server == nil {
		return "", false
	}
	server.backupMetaMutex.Lock()
	meta := server.LastBackupMeta.Physical
	if meta == nil || !meta.Completed || meta.IsAdhoc() {
		server.backupMetaMutex.Unlock()
		return "", false
	}
	if meta.BackupTool != "" && meta.BackupTool != backtype {
		server.backupMetaMutex.Unlock()
		return "", false
	}
	dest := strings.TrimSpace(meta.Dest)
	server.backupMetaMutex.Unlock()
	if dest == "" {
		return "", false
	}
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(server.GetMyBackupDirectory(), dest)
	}
	if _, err := os.Stat(dest); err != nil {
		return "", false
	}
	return dest, true
}

func physicalBackupCandidatePaths(server *ServerMonitor, backtype string, compressed bool) []string {
	if server == nil {
		return nil
	}
	addCandidate := func(list []string, value string) []string {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return list
		}
		for _, existing := range list {
			if existing == trimmed {
				return list
			}
		}
		return append(list, trimmed)
	}

	backupext := ".xbtream"
	if compressed {
		backupext += ".gz"
	}
	basePath := server.GetMyBackupDirectory() + backtype + backupext

	candidates := []string{}
	candidates = addCandidate(candidates, basePath)
	candidates = addCandidate(candidates, basePath+".enc")

	if alt := alternateCompressionPath(basePath); alt != "" {
		candidates = addCandidate(candidates, alt)
		if !strings.HasSuffix(strings.ToLower(alt), ".enc") {
			candidates = addCandidate(candidates, alt+".enc")
		}
	}

	return candidates
}

func findExistingPhysicalBackupPath(server *ServerMonitor, backtype string, compressed bool) (string, bool) {
	for _, candidate := range physicalBackupCandidatePaths(server, backtype, compressed) {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
	}
	return "", false
}

func isSplitDumpDir(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}
	metadataPath := filepath.Join(path, "metadata")
	if metaInfo, err := os.Stat(metadataPath); err == nil && !metaInfo.IsDir() {
		return true, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".sql.gz") {
			return true, nil
		}
	}
	return false, nil
}

func (server *ServerMonitor) reseedMysqldumpWithSplitdump(ctx context.Context, backupPath string, restoreUser bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	isSplit, err := isSplitDumpDir(backupPath)
	if err != nil {
		return err
	}
	if isSplit {
		cluster := server.ClusterGroup
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Splitdump detected at %s; restoring with mysql client", backupPath)
		return server.JobReseedSplitdumpWithMysql(ctx, backupPath, restoreUser)
	}
	return server.JobReseedMysqldump(backupPath, restoreUser)
}

func (server *ServerMonitor) reseedMysqldumpWithMetadata(ctx context.Context, backupPath string, restoreUser bool, meta *backupmgr.BackupMetadata) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if meta != nil {
		if meta.Dest != "" {
			pathsMatch, err := comparePaths(meta.Dest, backupPath)
			if err != nil {
				meta = nil
			} else if !pathsMatch {
				meta = nil
			}
		}
	}
	if meta != nil && (meta.SplitDump || isSplitDumpName(meta.Dest)) {
		cluster := server.ClusterGroup
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Splitdump metadata detected for %s; restoring with mysql client", backupPath)
		return server.JobReseedSplitdumpWithMysql(ctx, backupPath, restoreUser)
	}
	return server.reseedMysqldumpWithSplitdump(ctx, backupPath, restoreUser)
}

func (server *ServerMonitor) restoreSplitdumpFileContextWithPreamble(ctx context.Context, path, preamble string) error {
	cluster := server.ClusterGroup

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	var reader io.Reader = file
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		parallelBlocks := cluster.getSanitizedParallelBlocks(config.ConstLogModTask)
		bufferSize := cluster.getSanitizedDecompressBufferSize(config.ConstLogModTask)
		gzReader, err := gzip.NewReaderN(file, bufferSize, parallelBlocks)
		if err != nil {
			return err
		}
		defer gzReader.Close()
		reader = gzReader
	}

	schema := splitdump.SchemaFromFilename(path)
	table := splitdump.TableFromFilename(path)
	var force bool
	if schema == "mysql" {
		if splitdump.IsGtidSlavePosDataFile(path) {
			if cluster != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlWarn,
					"Splitdump restore skipped mysql.gtid_slave_pos data file: %s", filepath.Base(path))
			}
			return nil
		}

		// We need force in system-all to prevent failed plugins
		if splitdump.IsMysqlSystemAll(path) {
			force = true
		}

		if table != "" {
			// Server path proactively checks information_schema; CLI path reacts to mysql error output instead.
			exists, err := server.tableExists(schema, table)
			if err != nil {
				return err
			}
			if !exists {
				if cluster != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlWarn,
						"Splitdump restore skipped missing mysql table %s for %s", table, filepath.Base(path))
				}
				return nil
			}
		}
	}

	if preamble != "" {
		reader = io.MultiReader(bytes.NewBufferString(preamble), reader)
	}

	return server.executeMysqlRestoreContext(ctx, reader, force)
}

func (server *ServerMonitor) tableExists(schema, table string) (bool, error) {
	if server.Conn == nil {
		cluster := server.ClusterGroup
		if cluster != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, config.LvlWarn,
				"Splitdump restore skipped mysql table check for %s.%s: server connection not available", schema, table)
		}
		return false, nil
	}
	return tableExistsQuery(func() rowScanner {
		return server.Conn.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?", schema, table)
	}, schema, table)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func tableExistsQuery(query func() rowScanner, schema, table string) (bool, error) {
	if strings.TrimSpace(schema) == "" || strings.TrimSpace(table) == "" {
		return false, fmt.Errorf("schema and table are required")
	}
	if query == nil {
		return false, fmt.Errorf("query function is required")
	}
	var count int
	row := query()
	if row == nil {
		return false, fmt.Errorf("query returned nil row")
	}
	if err := row.Scan(&count); err != nil {
		return false, fmt.Errorf("table exists query failed: %w", err)
	}
	return count > 0, nil
}

func (server *ServerMonitor) buildLogicalRestorePreamble() (string, int, error) {
	cluster := server.ClusterGroup
	if cluster == nil {
		return "", 0, fmt.Errorf("cluster not available")
	}

	// Align logical restores: reset binlog state, control binlog writes, and normalize slow log behavior.
	sqlLogBin := 0
	resetmaster := "RESET MASTER;"
	if server.DBVersion.IsMySQLOrPerconaGreater84() {
		resetmaster = "RESET BINARY LOGS AND GTIDS;"
	}

	master := cluster.GetMaster()
	if master == nil {
		return "", 0, fmt.Errorf("No master found. Cancel backup reseeding %s", server.URL)
	}
	if server.URL == master.URL {
		sqlLogBin = 1
		resetmaster = ""
	}

	cmdstring := fmt.Sprintf("%sSET sql_log_bin=%d;SET long_query_time=10;", resetmaster, sqlLogBin)
	return cmdstring, sqlLogBin, nil
}

func (server *ServerMonitor) buildSplitdumpRestorePreamble(path string, sqlLogBin int) string {
	preamble := splitdump.RestorePreamble(path)
	if sqlLogBin == 0 {
		return "SET sql_log_bin=0;\n" + preamble
	}
	return preamble
}

func (server *ServerMonitor) JobReseedSplitdumpWithMysql(ctx context.Context, backupPath string, restoreUser bool) error {
	return server.restoreSplitdumpWithMysql(ctx, backupPath, restoreUser)
}

func (server *ServerMonitor) restoreSplitdumpWithMysql(ctx context.Context, backupPath string, restoreUser bool) error {
	cluster := server.ClusterGroup
	if cluster == nil {
		return fmt.Errorf("cluster not available")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	cmdstring, sqlLogBin, err := server.buildLogicalRestorePreamble()
	if err != nil {
		return err
	}

	var meta *splitdump.Metadata
	meta, err = splitdump.ReadMetadata(backupPath)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"Splitdump metadata not found; GTID will not be applied (%s)", backupPath)
		case errors.Is(err, splitdump.ErrMetadataInvalid):
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"Splitdump metadata invalid; GTID will not be applied (%s): %v", backupPath, err)
		default:
			return err
		}
		meta = nil
	} else if meta.SourceData == 0 && meta.File == "" && meta.Position == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Splitdump metadata indicates source-data=0; GTID will not be applied (%s)", backupPath)
		meta = nil
	}

	start := time.Now()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Logical restore (splitdump+mysql) started at %s for: %s", start.Format(time.RFC3339), server.URL)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Splitdump restore sets sql_log_bin=%d for %s", sqlLogBin, server.URL)
	if err := server.executeMysqlRestoreContext(ctx, bytes.NewBufferString(cmdstring), false); err != nil {
		return err
	}

	defer server.SetInReseedBackup("")

	restoreFile := func(ctx context.Context, path string) error {
		preamble := server.buildSplitdumpRestorePreamble(path, sqlLogBin)
		return server.restoreSplitdumpFileContextWithPreamble(ctx, path, preamble)
	}

	restoreErr := splitdump.Restore(backupPath, splitdump.RestoreOptions{
		Parallel:    cluster.Conf.BackupLogicalLoadThreads,
		RestoreUser: restoreUser,
		Logger: func(level, format string, args ...any) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModBackupStream, level, format, args...)
		},
		Context:                ctx,
		RestoreFileWithContext: restoreFile,
	})
	if restoreErr != nil {
		return restoreErr
	}

	if meta != nil && meta.GTID != "" {
		gtidValue := strings.ReplaceAll(meta.GTID, "'", "''")
		switch {
		case server.IsMariaDB() && server.HaveMariaDBGTID:
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
				"Applying splitdump GTID for MariaDB on %s", server.URL)
			if err := server.ExecQueryNoBinLog("SET GLOBAL gtid_slave_pos='"+gtidValue+"'", time.Second); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
					"Failed to apply splitdump GTID for MariaDB on %s: %v", server.URL, err)
			}
		case server.HasMySQLGTID() && server.DBVersion.IsMySQLOrPerconaGreater57():
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
				"Applying splitdump GTID for MySQL on %s", server.URL)
			if err := server.ExecQueryNoBinLog("SET @@GLOBAL.GTID_PURGED='"+gtidValue+"'", time.Second); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
					"Failed to apply splitdump GTID for MySQL on %s: %v", server.URL, err)
			}
		}
	}

	elapsed := time.Since(start).Round(time.Second)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Finish logical restore (splitdump+mysql) in %s (started at %s) for: %s",
		elapsed, start.Format(time.RFC3339), server.URL)
	server.Refresh()

	return nil
}

func comparePaths(left, right string) (bool, error) {
	// Intentionally use Abs+Clean (no EvalSymlinks) so paths can be compared
	// even when the target does not exist yet.
	leftAbs, err := filepath.Abs(left)
	if err != nil {
		return false, err
	}
	rightAbs, err := filepath.Abs(right)
	if err != nil {
		return false, err
	}
	return filepath.Clean(leftAbs) == filepath.Clean(rightAbs), nil
}

func isSplitDumpName(path string) bool {
	base := filepath.Base(path)
	if base == "splitdump" {
		return true
	}
	if !strings.HasPrefix(base, "splitdump.") {
		return false
	}
	suffix := strings.TrimPrefix(base, "splitdump.")
	if suffix == "" {
		return false
	}
	_, err := strconv.ParseInt(suffix, 10, 64)
	return err == nil
}

func (server *ServerMonitor) JobFlashbackLogicalBackup() error {
	var dest, backupfile string
	var err error

	cluster := server.ClusterGroup
	backtype := cluster.Conf.BackupLogicalType
	task := "flashback" + backtype

	// Ensure the cluster is discovered before proceeding
	if !cluster.IsDiscovered() {
		return errors.New("Cluster not discovered yet")
	}

	// Get the master node of the cluster
	master := cluster.GetMaster()
	if master == nil {
		return errors.New("No master found. Cancel reseed logical backup")
	}

	// Check if the server is already reseeding (atomic check-and-set)
	if ok, currentTask := server.TrySetInReseedBackup(task); !ok {
		err = fmt.Errorf("Server is in reseeding state by %s", currentTask)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Concurrent reseed blocked: server %s already reseeding with %s, cannot start %s", server.URL, currentTask, task)
		return err
	}

	// Decide on backup filename depending on the backup type
	useMaster := true
	source := master
	var destCandidates []string
	switch backtype {
	case config.ConstBackupLogicalTypeMysqldump, "script":
		dest = "mysqldump.sql.gz"
		destCandidates = []string{"mysqldump.sql.gz", "splitdump"}
	case config.ConstBackupLogicalTypeMydumper:
		dest = "mydumper"
	case config.ConstBackupLogicalTypeDumpling:
		dest = "dumpling"
	}
	if len(destCandidates) == 0 {
		destCandidates = []string{dest}
	}

	// Skip file lookup if using custom script
	if backtype != "script" {
		// Construct backup path on master
		backupfile, _ = findExistingBackupPath(master, destCandidates)
		if backupfile == "" {
			backupfile = master.GetMyBackupDirectory() + dest
		}

		// If a backup server has a valid backup for this type, use it instead
		bckserver := cluster.GetBackupServer()
		if bckserver != nil && bckserver.HasBackupTypeCookie(backtype) {
			if resolved, ok := findExistingBackupPath(bckserver, destCandidates); ok {
				backupfile = resolved
				useMaster = false
				source = bckserver
			} else {
				// Remove invalid cookie if file does not exist
				bckserver.DelBackupTypeCookie(backtype)
			}
		}

		// If no valid backup found, abort
		if useMaster {
			if _, err := os.Stat(backupfile); err != nil {
				master.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
				return fmt.Errorf("Cancelling reseed. No backup file found on master for %s", backtype)
			}
		}
	}

	// Reseed state already set atomically above, just add cleanup defer
	defer func() {
		// Clear reseed state if task is still marked
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
	}()

	// Attempt to stop slave replication on the server
	logs, err := server.StopSlave()
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed stop slave on server: %s %s", server.URL, err)
	}

	if server.DBVersion.IsMySQLOrPerconaGreater57() {
		if server.HasMySQLGTID() {
			logs, err = cluster.pointSlaveToMasterWithMode(server, "MASTER_AUTO_POSITION")
		} else {
			logs, err = cluster.pointSlaveToMasterPositional(server)
		}
	} else {
		logs, err = cluster.pointSlaveToMasterWithMode(server, "SLAVE_POS")
	}
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "flashback can't changing master for logical backup %s request for server: %s %s", cluster.Conf.BackupLogicalType, server.URL, err)
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive flashback logical backup %s request for server: %s", backtype, server.URL)
	restoreBackupPath := backupfile
	var restoreCleanup func()

	// If a custom script is configured, use it
	if cluster.Conf.BackupLoadScript != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Using script from backup-load-script on %s", server.URL)
		if err := server.JobReseedBackupScript(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error flashback %s on %s: %s", backtype, server.URL, err.Error())
			if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
			return err
		}
		if e2 := server.JobsUpdateState(task, "Flashback completed", 3, 1); e2 != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
		}
		return nil

		// Handle mysqldump-based reseed
	} else if backtype == config.ConstBackupLogicalTypeMysqldump {
		var restorePrepErr error
		restoreBackupPath, restoreCleanup, restorePrepErr = server.prepareEncryptedDirectoryLogicalRestorePath(backupfile, backtype, source.LastBackupMeta.Logical)
		if restorePrepErr != nil {
			return restorePrepErr
		}
		if restoreCleanup != nil {
			defer restoreCleanup()
		}
		err := server.reseedMysqldumpWithMetadata(context.Background(), restoreBackupPath, cluster.Conf.BackupRestoreMysqlUser && source.LastBackupMeta.Logical != nil && source.LastBackupMeta.Logical.SplitUser, source.LastBackupMeta.Logical)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error flashback %s on %s: %s", backtype, server.URL, err.Error())
			if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
		} else {
			// Restart slave if needed
			if server.IsSlave {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Start slave after dump on %s", server.URL)
				server.StartSlave()
			}

			if e2 := server.JobsUpdateState(task, "Flashback completed", 3, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
		}

		// Handle mydumper-based reseed
	} else if backtype == config.ConstBackupLogicalTypeMydumper {
		var restorePrepErr error
		restoreBackupPath, restoreCleanup, restorePrepErr = server.prepareEncryptedDirectoryLogicalRestorePath(backupfile, backtype, source.LastBackupMeta.Logical)
		if restorePrepErr != nil {
			return restorePrepErr
		}
		if restoreCleanup != nil {
			defer restoreCleanup()
		}
		err := server.JobReseedMyLoader(restoreBackupPath, cluster.Conf.BackupRestoreMysqlUser)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error flashback %s on %s: %s", backtype, server.URL, err.Error())
			if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
		} else {
			// Parse metadata from mydumper
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Parsing mydumper metadata ")
			meta, err2 := cluster.JobMyLoaderParseMeta(restoreBackupPath)
			if err2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "MyLoader metadata parsing: %s", err2)
				err = err2
			} else {
				// Set GTID position for MariaDB
				if server.IsMariaDB() && server.HaveMariaDBGTID {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Starting slave with mydumper metadata")
					server.ExecQueryNoBinLog("SET GLOBAL gtid_slave_pos='"+meta.BinLogUuid+"'", time.Second)
				}

				if err == nil {
					server.StartSlave()
				}
			}

			if e2 := server.JobsUpdateState(task, "Flashback completed", 3, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
		}
	}

	return nil
}

func (server *ServerMonitor) JobReseedMyLoader(backupdir string, restoreUser bool) error {
	cluster := server.ClusterGroup
	start := time.Now()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical restore (myloader) started at %s for: %s", start.Format(time.RFC3339), server.URL)
	threads := strconv.Itoa(cluster.Conf.BackupLogicalLoadThreads)

	if restoreUser {
		//walk dir
		files, err := os.ReadDir(backupdir)
		if err == nil {
			if len(files) > 0 {
				for _, file := range files {
					if strings.HasPrefix(file.Name(), "mysql.user") && strings.HasSuffix(file.Name(), ".sql.gz.skip") {
						os.Rename(filepath.Join(backupdir, file.Name()), filepath.Join(backupdir, strings.TrimSuffix(file.Name(), ".skip")))
					}
				}
			}
		}
	} else {
		//walk dir
		files, err := os.ReadDir(backupdir)
		if err == nil {
			if len(files) > 0 {
				for _, file := range files {
					if strings.HasPrefix(file.Name(), "mysql.user") && strings.HasSuffix(file.Name(), ".sql.gz") {
						os.Rename(filepath.Join(backupdir, file.Name()), filepath.Join(backupdir, file.Name()+".skip"))
					}
				}
			}
		}
	}

	defer server.SetInReseedBackup("")

	master := cluster.GetMaster()
	if master == nil {
		return fmt.Errorf("No master. Cancel backup reseeding %s", server.URL)
	}

	myargs := cluster.GetMyLoaderCompatibleOptions()
	if server.URL == master.URL {
		myargs = append(myargs, "--enable-binlog")
	}

	myargs = append(myargs, cluster.GetDumpCredentials(server)...)
	myargs = append(myargs, "--directory="+backupdir, "--threads="+threads)
	dumpCmd := exec.Command(cluster.GetMyLoaderPath(), myargs...)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command: %s", strings.ReplaceAll(dumpCmd.String(), "="+cluster.GetDbPass(), "=XXXX"))

	stdoutIn, _ := dumpCmd.StdoutPipe()
	stderrIn, _ := dumpCmd.StderrPipe()
	dumpCmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	wg.Wait()
	if err := dumpCmd.Wait(); err != nil {
		return err
	}
	elapsed := time.Since(start).Round(time.Second)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Finish logical restore (myloader) in %s (started at %s) for: %s", elapsed, start.Format(time.RFC3339), server.URL)
	server.Refresh()

	return nil
}

func (server *ServerMonitor) JobReseedMysqldump(backupfile string, restoreUser bool) error {
	cluster := server.ClusterGroup
	var err error
	defer server.SetInReseedBackup("")

	start := time.Now()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical restore (mysqldump) started at %s for: %s", start.Format(time.RFC3339), server.URL)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Sending logical backup to reseed %s", server.URL)

	server.StopSlave()

	gzfile, err := os.Open(backupfile)
	if err != nil {
		return fmt.Errorf("[%s] Failed opening backup file in backup server for reseed:  %s ", server.URL, err)
	}

	// Use configurable parallel blocks for better performance
	// For restore operations, use higher default (16) for speed, matching original behavior
	parallelBlocks := cluster.getSanitizedParallelBlocks(config.ConstLogModTask)
	bufferSize := cluster.getSanitizedDecompressBufferSize(config.ConstLogModTask)
	fz, err := gzip.NewReaderN(gzfile, bufferSize, parallelBlocks)
	if err != nil {
		return fmt.Errorf("[%s] Failed to unzip backup file in backup server for reseed:  %s ", server.URL, err)
	}
	defer fz.Close()

	cliParams := append(cluster.GetDumpCredentials(server), server.GetSSLClientParam("client")...)
	cliParams = append(cliParams, strings.Split(cluster.Conf.BackupMysqlclientOptions, " ")...)
	clientCmd := exec.Command(cluster.GetMysqlclientPath(), misc.RemoveEmptyString(cliParams)...)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command: %s ", strings.Replace(clientCmd.String(), "="+cluster.GetDbPass(), "=XXXX", -1))

	cmdstring, _, err := server.buildLogicalRestorePreamble()
	if err != nil {
		return err
	}

	var usergzfile io.Reader
	if restoreUser {
		usergzfile, err = server.ReadMysqldumpUser(backupfile)
		if err != nil {
			return fmt.Errorf("Error opening mysql.user file %s", err)
		}

		clientCmd.Stdin = io.MultiReader(bytes.NewBufferString(cmdstring), usergzfile, fz) //Append mysql.user
	} else {
		clientCmd.Stdin = io.MultiReader(bytes.NewBufferString(cmdstring), fz)
	}

	stderr, _ := clientCmd.StdoutPipe()
	clientCmd.Stderr = clientCmd.Stdout

	if err := clientCmd.Start(); err != nil {
		return fmt.Errorf("Can't start mysql client:%s at %s", err, strings.ReplaceAll(clientCmd.String(), "="+cluster.GetDbPass(), "=XXXX"))
	}

	wg := sync.WaitGroup{}
	wg.Add(1)

	go func() {
		defer wg.Done()
		server.copyLogs(stderr, config.ConstLogModBackupStream, config.LvlDbg)
	}()

	wg.Wait()

	err = clientCmd.Wait()
	if err != nil {
		return fmt.Errorf("Error waiting reseed %s at %s", server.URL, err)
	}

	elapsed := time.Since(start).Round(time.Second)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Finish logical restore (mysqldump) in %s (started at %s) for: %s", elapsed, start.Format(time.RFC3339), server.URL)
	return nil
}

func (server *ServerMonitor) ReadMysqldumpUser(backupfile string) (io.Reader, error) {
	cluster := server.ClusterGroup
	var err error

	dir := filepath.Dir(backupfile)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, fmt.Errorf("Directory %s does not exist", dir)
	}

	userpath := filepath.Join(dir, "mysql.user.sql.gz")
	if _, err := os.Stat(userpath); os.IsNotExist(err) {
		return nil, fmt.Errorf("File %s does not exist", userpath)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Opening mysql.user file %s", userpath)

	gzfile, err := os.Open(backupfile)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed opening backup file in backup server for reseed:  %s ", server.URL, err)
	}

	// Use configurable parallel blocks for better performance
	// For restore operations, use higher default (16) for speed, matching original behavior
	parallelBlocks := cluster.getSanitizedParallelBlocks(config.ConstLogModTask)
	bufferSize := cluster.getSanitizedDecompressBufferSize(config.ConstLogModTask)
	fz, err := gzip.NewReaderN(gzfile, bufferSize, parallelBlocks)
	if err != nil {
		return nil, fmt.Errorf("[%s] Failed to unzip backup file in backup server for reseed:  %s ", server.URL, err)
	}

	return fz, nil
}

// JobReseedBackupScript will execute the backup load script
// The script will be executed with the following parameters:
// 1. Server Host
// 2. Master Host
// 2. Server Port
// 2. Master Port
// 3. User
// 4. Password
// 5. Cluster Name
func (server *ServerMonitor) JobReseedBackupScript() error {
	cluster := server.ClusterGroup
	defer server.SetInReseedBackup("")
	start := time.Now()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical restore (script) started at %s for: %s", start.Format(time.RFC3339), server.URL)

	master := cluster.GetMaster()
	if master == nil {
		err := fmt.Errorf("No master found. Cancel backup reseeding %s", server.URL)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%v", err)
		return err
	}
	cmd := exec.Command(cluster.Conf.BackupLoadScript, misc.Unbracket(server.Host), misc.Unbracket(master.Host), server.Port, master.Port, cluster.GetDbUser(), cluster.GetDbPass(), cluster.Name)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command backup load script: %s", strings.Replace(cmd.String(), "="+cluster.GetDbPass(), "=XXXX", 1))

	stdoutIn, _ := cmd.StdoutPipe()
	stderrIn, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "My reload script start failed: %s", err)
		return err
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	wg.Wait()
	if err := cmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "My reload script: %s", err)
		return err
	}
	elapsed := time.Since(start).Round(time.Second)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Finish logical restore (script) in %s (started at %s) for: %s", elapsed, start.Format(time.RFC3339), server.URL)
	return nil
}

func (server *ServerMonitor) GetMyBackupDirectory() string {
	cluster := server.ClusterGroup
	s3dir := cluster.Conf.WorkingDir + "/" + config.ConstStreamingSubDir + "/" + cluster.Name + "/" + server.Host + "_" + server.Port

	if _, err := os.Stat(s3dir); os.IsNotExist(err) {
		err := os.MkdirAll(s3dir, os.ModePerm)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Create backup path failed: %s", s3dir, err)
		}
	}

	return s3dir + "/"

}

// JobBackupScript execute a backup script
// The script must be able to handle the following parameters:
// 1. DB Server Host
// 2. Master Host
// 3. DB Server Port
// 4. Master Port
// 5. DB User
// 6. DB Password
// 7. Cluster Name
// 8. Destination File Path
func (server *ServerMonitor) JobBackupScript(destination string) error {
	var err error
	cluster := server.ClusterGroup

	defer cluster.SetInLogicalBackupState(false)

	master := cluster.GetMaster()
	if master == nil {
		return fmt.Errorf("No master found. Cancel backup script on %s", server.URL)
	}
	scriptCmd := exec.Command(cluster.Conf.BackupSaveScript, server.Host, master.Host, server.Port, master.Port, cluster.GetDbUser(), cluster.GetDbPass(), cluster.Name, destination)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command: %s", strings.Replace(scriptCmd.String(), cluster.GetDbPass(), "XXXX", -1))
	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()

	wg.Wait()

	if err = scriptCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Backup script error: %s", err)
		return err
	}
	return err
}

// getBackupRegexPatterns returns appropriate regex patterns based on DB version
func (server *ServerMonitor) getBackupRegexPatterns() (*regexp.Regexp, *regexp.Regexp) {
	binlogRegex := regexp.MustCompile(`CHANGE MASTER TO MASTER_LOG_FILE='(.+)', MASTER_LOG_POS=(\d+)`)
	gtidRegex := regexp.MustCompile(`SET GLOBAL gtid_slave_pos='(.+)'`)

	if server.DBVersion.IsMySQLOrPerconaGreater84() {
		binlogRegex = regexp.MustCompile(`CHANGE REPLICATION SOURCE TO SOURCE_LOG_FILE='(.+)', SOURCE_LOG_POS=(\d+)`)
	}
	if server.DBVersion.IsMySQLOrPerconaGreater57() {
		gtidRegex = regexp.MustCompile(`GTID_PURGED\s*=\s*(?:/\*![0-9]*\s*'([^']+)'\*/\s*|'([^']+)')`)
	}

	return binlogRegex, gtidRegex
}

func (server *ServerMonitor) shouldParseDumpBinlogGTID() (bool, bool) {
	parseBinlog := true
	parseGTID := server.IsMariaDB() || server.DBVersion.IsMySQLOrPerconaGreater57()
	server.backupMetaMutex.Lock()
	meta := server.LastBackupMeta.Logical
	hasBinlog := meta != nil && meta.BinLogFileName != ""
	hasGTID := meta != nil && meta.BinLogGtid != ""
	server.backupMetaMutex.Unlock()
	if hasBinlog {
		parseBinlog = false
	}
	if hasGTID {
		parseGTID = false
	}
	return parseBinlog, parseGTID
}

// createGzipWriter creates a gzip writer with configurable compression
func (cluster *Cluster) createGzipWriter(filePath string, logModule int) (*os.File, *gzip.Writer, error) {
	f, err := os.Create(filePath)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, logModule, config.LvlErr,
			"Error creating file %s: %s", filePath, err.Error())
		return nil, nil, err
	}

	compressionLevel := cluster.getSanitizedCompressionLevel(logModule)
	gw, err := gzip.NewWriterLevel(f, compressionLevel)
	if err != nil {
		f.Close()
		cluster.LogModulePrintf(cluster.Conf.Verbose, logModule, config.LvlErr,
			"Error creating gzip writer: %s", err.Error())
		return nil, nil, err
	}

	return f, gw, nil
}

// spawnLogCopier spawns a goroutine to copy logs from reader to cluster logs
func (server *ServerMonitor) spawnLogCopier(wg *sync.WaitGroup, r io.Reader, module int, level string) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		server.copyLogs(r, module, level)
	}()
}

// drainErrorChannel drains all errors from a channel and combines them
func drainErrorChannel(errCh <-chan error) error {
	var combinedErr error
	for err := range errCh {
		if err != nil {
			combinedErr = errors.Join(combinedErr, err)
		}
	}
	return combinedErr
}

type splitDumpPipeline struct {
	teeWriter  io.Writer
	errCh      chan error
	pipeWriter *io.PipeWriter
	wg         *sync.WaitGroup
}

type fallbackWriter struct {
	primary  io.Writer
	fallback io.Writer
	failed   atomic.Bool
}

func (w *fallbackWriter) Write(p []byte) (int, error) {
	if w.failed.Load() {
		return w.fallback.Write(p)
	}
	if w.primary == nil {
		return w.fallback.Write(p)
	}
	n, err := w.primary.Write(p)
	if err != nil {
		w.failed.Store(true)
		return w.fallback.Write(p)
	}
	return n, nil
}

// setupSplitDumpPipeline sets up the splitdump processing pipeline
func (server *ServerMonitor) setupSplitDumpPipeline(
	ctx context.Context,
	outputDir string,
	allowRotate bool,
	cancel context.CancelFunc,
) *splitDumpPipeline {
	cluster := server.ClusterGroup

	splitDumpReader, splitDumpWriter := io.Pipe()
	splitDumpErrCh := make(chan error, 1)
	var splitDumpWG sync.WaitGroup
	splitDumpWG.Add(1)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Splitdump enabled for mysqldump output: %s", outputDir)

	// Splitdump processor goroutine
	go func() {
		defer splitDumpWG.Done()
		defer close(splitDumpErrCh)
		defer splitDumpReader.Close()
		stdoutR, stdoutW := io.Pipe()
		stderrR, stderrW := io.Pipe()

		var logWG sync.WaitGroup
		server.spawnLogCopier(&logWG, stdoutR, config.ConstLogModBackupStream, config.LvlDbg)
		server.spawnLogCopier(&logWG, stderrR, config.ConstLogModBackupStream, config.LvlDbg)

		err := cluster.SplitDumpWithCli(ctx, server, outputDir, allowRotate, splitDumpReader, stdoutW, stderrW)
		stdoutW.Close()
		stderrW.Close()
		logWG.Wait()

		if err != nil {
			splitDumpErrCh <- err
			cancel()
			_ = splitDumpWriter.CloseWithError(err)
		}
	}()

	teeWriter := splitDumpWriter

	return &splitDumpPipeline{
		teeWriter:  teeWriter,
		errCh:      splitDumpErrCh,
		pipeWriter: splitDumpWriter,
		wg:         &splitDumpWG,
	}
}

func (server *ServerMonitor) JobBackupMysqldump(ctx context.Context, task, filename string, allowRotate bool) error {
	cluster := server.ClusterGroup
	var err error
	var bckConn *sqlx.DB
	if ctx == nil {
		ctx = context.Background()
	}

	defer cluster.SetInLogicalBackupState(false)

	//Block DDL For Backup
	if server.IsMariaDB() && server.DBVersion.GreaterEqual("10.4") && cluster.Conf.BackupLockDDL {
		bckConn, err = server.GetNewDBConn()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error backup request: %s", err)
		}
		defer bckConn.Close()

		_, err = bckConn.Exec("BACKUP STAGE START")
		if err != nil {
			cluster.LogSQL("BACKUP STAGE START", err, server.URL, "JobBackupLogical", config.LvlWarn, "Failed SQL for server %s: %s ", server.URL, err)
		}
		_, err = bckConn.Exec("BACKUP STAGE BLOCK_DDL")
		if err != nil {
			cluster.LogSQL("BACKUP BLOCK_DDL", err, server.URL, "JobBackupLogical", config.LvlWarn, "Failed SQL for server %s: %s ", server.URL, err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Blocking DDL via BACKUP STAGE")
	}

	// Prepare regex patterns
	binlogRegex, gtidRegex := server.getBackupRegexPatterns()

	var bfile, bgtid string
	var bpos uint64

	dumpCtx, dumpCancel := context.WithCancel(ctx)
	defer dumpCancel()
	server.registerJobCancel(task, dumpCancel)
	defer server.clearJobCancel(task)
	dumpCmd := exec.CommandContext(dumpCtx, cluster.GetMysqlDumpPath(), cluster.GetMysqlDumpOptions(server, server.JobGetDumpGtidParameter())...)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command: %s ", strings.Replace(dumpCmd.String(), "="+cluster.GetDbPass(), "=XXXX", -1))
	// Get the stdout pipe from the command
	stdout, err := dumpCmd.StdoutPipe()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error getting stdout pipe: %s", err)
		fmt.Println()
		return err
	}

	stderrIn, _ := dumpCmd.StderrPipe()

	// Setup output writer (gzip file or splitdump)
	var teeWriter io.Writer
	var splitDumpPipeline *splitDumpPipeline
	var f *os.File
	var gw *gzip.Writer

	if cluster.Conf.BackupMysqldumpSplitDump {
		splitDumpPipeline = server.setupSplitDumpPipeline(dumpCtx, filename, allowRotate, dumpCancel)
		teeWriter = &fallbackWriter{primary: splitDumpPipeline.teeWriter, fallback: io.Discard}
	} else {
		f, gw, err = cluster.createGzipWriter(filename, config.ConstLogModTask)
		if err != nil {
			return err
		}
		defer func() {
			if err := gw.Flush(); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error flushing gzip: %s", err.Error())
			}
			if err := gw.Close(); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error closing gzip: %s", err.Error())
			}
			f.Close()
		}()
		teeWriter = gw
	}

	// Start dump command
	if err := dumpCmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error backup request: %s", err)
		if errors.Is(err, context.Canceled) && server.isJobCancelRequested(task) {
			return errors.Join(errJobCanceledByUser, err)
		}
		return err
	}

	// Process stdout stream
	teeReader := io.TeeReader(stdout, teeWriter)
	reader := bufio.NewReader(teeReader)
	buffer := make([]byte, cluster.Conf.SSTSendBuffer)
	errCh := make(chan error, 4)

	var wg sync.WaitGroup
	server.spawnLogCopier(&wg, stderrIn, config.ConstLogModBackupStream, config.LvlDbg)

	parseBinlog, parseGTID := server.shouldParseDumpBinlogGTID()
	parser := newDumpStreamParser(
		binlogRegex,
		gtidRegex,
		parseBinlog,
		parseGTID,
		func(file string, pos uint64) {
			server.backupMetaMutex.Lock()
			hasBinlog := server.LastBackupMeta.Logical != nil && server.LastBackupMeta.Logical.BinLogFileName != ""
			server.backupMetaMutex.Unlock()
			if hasBinlog {
				return
			}
			bfile = file
			bpos = pos
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
				"Binlog filename:%s, pos: %s", bfile, strconv.FormatUint(bpos, 10))
		},
		func(gtid string) {
			server.backupMetaMutex.Lock()
			hasGTID := server.LastBackupMeta.Logical != nil && server.LastBackupMeta.Logical.BinLogGtid != ""
			server.backupMetaMutex.Unlock()
			if hasGTID {
				return
			}
			bgtid = gtid
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
				"GTID:%s", bgtid)
		},
	)

	// Main reading goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(errCh)
		errSent := false

		for {
			n, err := reader.Read(buffer)
			if err != nil && err != io.EOF {
				if !errSent {
					errCh <- fmt.Errorf("Error reading buffer: %w", err)
					errSent = true
				}
				break
			}
			if n == 0 {
				break
			}
			if parser.Enabled() {
				parser.Consume(buffer[:n])
			}
		}

		parser.Flush()

		if splitDumpPipeline != nil {
			_ = splitDumpPipeline.pipeWriter.Close()
		}

		if err := dumpCmd.Wait(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "mysqldump: %s", err)
			select {
			case errCh <- fmt.Errorf("mysqldump: %w", err):
			default:
			}
		}
	}()

	wg.Wait()

	// Collect all errors
	var splitDumpErr, readErr error
	if splitDumpPipeline != nil {
		splitDumpPipeline.wg.Wait()
		splitDumpErr = drainErrorChannel(splitDumpPipeline.errCh)
		if splitDumpErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Splitdump error: %s", splitDumpErr)
		}
	}

	readErr = drainErrorChannel(errCh)
	combinedErr := errors.Join(splitDumpErr, readErr)
	if combinedErr != nil {
		if errors.Is(combinedErr, context.Canceled) && server.isJobCancelRequested(task) {
			if cluster.Conf.BackupKeepUntilValid {
				if _, statErr := os.Stat(filename); statErr == nil {
					cancelPath := filename + ".canceled"
					if _, err := os.Stat(cancelPath); err == nil {
						cancelPath = fmt.Sprintf("%s.%d", cancelPath, time.Now().Unix())
					}
					if err := os.Rename(filename, cancelPath); err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to preserve canceled backup %s: %s", filename, err)
					} else {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Preserved canceled backup at %s", cancelPath)
					}
				} else if !os.IsNotExist(statErr) {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to inspect canceled backup %s: %s", filename, statErr)
				}
			}
			return errors.Join(errJobCanceledByUser, combinedErr)
		}
		return combinedErr
	}

	server.backupMetaMutex.Lock()
	if server.LastBackupMeta.Logical != nil {
		if server.LastBackupMeta.Logical.BinLogGtid == "" && bgtid != "" {
			server.LastBackupMeta.Logical.BinLogGtid = bgtid
		}
		if server.LastBackupMeta.Logical.BinLogFileName == "" && bfile != "" {
			server.LastBackupMeta.Logical.BinLogFileName = bfile
			server.LastBackupMeta.Logical.BinLogFilePos = bpos
		}
	}
	server.backupMetaMutex.Unlock()

	return err
}

func (server *ServerMonitor) JobBackupMysqldumpUser() error {
	cluster := server.ClusterGroup
	var err error

	dir := server.GetMyBackupDirectory()
	userpath := filepath.Join(dir, "mysql.users.sql.gz")

	dumpargs := append(cluster.GetDumpCredentials(server), server.GetSSLClientParam("client-dump")...)
	dumpargs = append(dumpargs, "--insert-ignore", "--system=user")
	dumpCmd := exec.Command(cluster.GetMysqlDumpPath(), dumpargs...)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command: %s ", strings.Replace(dumpCmd.String(), "="+cluster.GetDbPass(), "=XXXX", -1))

	f, gw, err := cluster.createGzipWriter(userpath, config.ConstLogModTask)
	if err != nil {
		return err
	}

	// Buffer before gzip to improve compression
	bw := bufio.NewWriterSize(gw, 64*1024)
	defer func() {
		if err := bw.Flush(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error flushing buffer: %s", err.Error())
		}
		if err := gw.Close(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error closing gzip: %s", err.Error())
		}
		f.Close()
	}()

	dumpCmd.Stdout = gw
	stderrIn, _ := dumpCmd.StderrPipe()

	if err := dumpCmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error backup request: %s", err)
		return err
	}

	var wg sync.WaitGroup
	server.spawnLogCopier(&wg, stderrIn, config.ConstLogModBackupStream, config.LvlDbg)
	wg.Wait()

	err = dumpCmd.Wait()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "mysqldump: %s", err)
	}

	return err
}

func (server *ServerMonitor) JobBackupMyDumper(outputdir string) error {
	cluster := server.ClusterGroup
	var err error
	var bckConn *sqlx.DB

	// Mydumper is fine with split user since we can
	server.LastBackupMeta.Logical.SplitUser = true

	defer cluster.SetInLogicalBackupState(false)

	dumper := cluster.VersionsMap.Get("mydumper")
	if dumper == nil {
		if err = cluster.RefreshMyDumperVersion(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error getting MyDumper version: %s", err)
			return err
		} else {
			dumper = cluster.VersionsMap.Get("mydumper")
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "MyDumper version: %s", dumper.ToString())
		}
	}

	if server.IsMariaDB() && server.DBVersion.GreaterEqual("10.7") && dumper.LowerEqual("0.10.1") {
		cluster.SetState("WARN0133", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0133"], dumper.ToString()), ErrFrom: "JOB"})
		return fmt.Errorf("MyDumper version %s is not compatible with MariaDB 10.7", dumper.ToString())
	}

	//Block DDL For Backup
	if server.IsMariaDB() && server.DBVersion.GreaterEqual("10.4") && dumper.Lower("0.12.3") && cluster.Conf.BackupLockDDL {
		bckConn, err = server.GetNewDBConn()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error backup request: %s", err)
		}
		defer bckConn.Close()

		_, err = bckConn.Exec("BACKUP STAGE START")
		if err != nil {
			cluster.LogSQL("BACKUP STAGE START", err, server.URL, "JobBackupLogical", config.LvlWarn, "Failed SQL for server %s: %s ", server.URL, err)
		}
		_, err = bckConn.Exec("BACKUP STAGE BLOCK_DDL")
		if err != nil {
			cluster.LogSQL("BACKUP BLOCK_DDL", err, server.URL, "JobBackupLogical", config.LvlWarn, "Failed SQL for server %s: %s ", server.URL, err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Blocking DDL via BACKUP STAGE")
	}

	threads := strconv.Itoa(cluster.Conf.BackupLogicalDumpThreads)
	myargs := cluster.GetMyDumperCompatibleOptions()

	// Handle deprecated flags for mydumper >= 0.18.1
	if dumper.GreaterEqual("0.18.1") {
		// Replace deprecated flags with --trx-tables
		var updatedArgs []string
		for _, arg := range myargs {
			if arg == "--less-locking" || arg == "--trx-consistency-only" {
				// Skip deprecated flags
				continue
			}
			updatedArgs = append(updatedArgs, arg)
		}
		// Add --trx-tables if not already present
		hasTrxTablesArg := false
		for _, arg := range updatedArgs {
			if arg == "--trx-tables" || strings.HasPrefix(arg, "--trx-tables=") || arg == "--no-trx-tables" { // Check for both --trx-tables and --no-trx-tables to avoid conflicts
				hasTrxTablesArg = true
				break
			}
		}
		if !hasTrxTablesArg {
			updatedArgs = append(updatedArgs, "--trx-tables")
		}
		myargs = updatedArgs
	}

	if dumper.GreaterEqual("0.15.3") && !slices.Contains(myargs, "--clear") {
		myargs = append(myargs, "--clear")
	}
	myargs = append(myargs, "--outputdir", outputdir, "--threads", threads, "--host", misc.Unbracket(server.Host), "--port", server.Port, "--user", cluster.GetDbUser(), "--password", cluster.GetDbPass())

	if cluster.Conf.BackupMyDumperRegex != "" {
		myargs = append(myargs, "--regex", cluster.Conf.BackupMyDumperRegex)
	}

	dumpCmd := exec.Command(cluster.GetMyDumperPath(), myargs...)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "%s", strings.Replace(dumpCmd.String(), "="+cluster.GetDbPass(), "=XXXX", 1))
	stdoutIn, _ := dumpCmd.StdoutPipe()
	stderrIn, _ := dumpCmd.StderrPipe()
	dumpCmd.Start()

	var wg sync.WaitGroup
	var valid bool = true
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.myDumperCopyLogs(stdoutIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		valid = server.myDumperCopyLogs(stderrIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	wg.Wait()
	if err = dumpCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error mydumper:  %s", err)
		return err
	}
	if !valid {
		return fmt.Errorf("mydumper reported errors in output")
	}

	if e2 := cluster.JobParseMyDumperMeta(server.LastBackupMeta.Logical); e2 != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error parsing mydumper metadata: %s", e2.Error())
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Success backup data via mydumper. Setting logical cookie")
	server.SetBackupLogicalCookie(config.ConstBackupLogicalTypeMydumper)

	return err
}

func (server *ServerMonitor) JobBackupDumpling(outputdir string) error {
	var err error
	cluster := server.ClusterGroup

	defer cluster.SetInLogicalBackupState(false)

	conf := dumplingext.DefaultConfig()
	conf.Database = ""
	conf.Host = misc.Unbracket(server.Host)
	conf.User = cluster.GetDbUser()
	conf.Port, _ = strconv.Atoi(server.Port)
	conf.Password = cluster.GetDbPass()

	conf.Threads = cluster.Conf.BackupLogicalDumpThreads
	conf.FileSize = 1000
	conf.StatementSize = dumplingext.UnspecifiedSize
	conf.OutputDirPath = outputdir
	conf.Consistency = "flush"
	conf.NoViews = true
	conf.StatusAddr = ":8281"
	conf.Rows = dumplingext.UnspecifiedSize
	conf.Where = ""
	conf.EscapeBackslash = true
	conf.LogLevel = config.LvlInfo

	err = dumplingext.Dump(conf)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Dumpling %s", err)
		return err
	}

	return err
}

func (server *ServerMonitor) JobBackupRiver() error {
	var err error
	cluster := server.ClusterGroup

	defer cluster.SetInLogicalBackupState(false)

	cfg := new(river.Config)
	cfg.MyHost = server.URL
	cfg.MyUser = server.User
	cfg.MyPassword = server.Pass
	cfg.MyFlavor = server.DBVersion.Flavor
	cfg.MyVersion = server.DBVersion

	//	cfg.ESAddr = *es_addr
	cfg.StatAddr = "127.0.0.1:12800"
	cfg.DumpServerID = 1001

	cfg.DumpPath = cluster.Conf.WorkingDir + "/" + cluster.Name + "/river"
	cfg.DumpExec = cluster.GetMysqlDumpPath()
	cfg.DumpOnly = true
	cfg.DumpInit = true
	cfg.BatchMode = "CSV"
	cfg.BatchSize = 100000
	cfg.BatchTimeOut = 1
	cfg.DataDir = cluster.Conf.WorkingDir + "/" + cluster.Name + "/river"

	os.RemoveAll(cfg.DumpPath)
	server.LastBackupMeta.Logical.Dest = cfg.DumpPath

	//cfg.Sources = []river.SourceConfig{river.SourceConfig{Schema: "test", Tables: []string{"test", "[*]"}}}
	cfg.Sources = []river.SourceConfig{river.SourceConfig{Schema: "test", Tables: []string{"City"}}}

	_, err = river.NewRiver(cfg)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error river backup: %s", err)
	}

	return err
}

func (server *ServerMonitor) JobBackupLogical(ctx context.Context) error {
	return server.JobBackupLogicalWithOptions(ctx, BackupRunOptions{})
}

func (server *ServerMonitor) JobBackupLogicalWithOptions(ctx context.Context, opts BackupRunOptions) error {
	var err error
	//server can be nil as no dicovered master
	if server == nil {
		return errors.New("No server defined")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	cluster := server.ClusterGroup
	if err := server.ensureOpenSSLAvailableForBackup(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "%s", err)
		return err
	}
	backupLine := server.resolveBackupLine(opts)
	isAdhoc := backupLine == backupmgr.BackupLineAdhoc
	resticEnabled := server.shouldRunRestic(opts)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Request logical backup %s for: %s", cluster.Conf.BackupLogicalType, server.URL)
	if server.IsDown() {
		return errors.New("Can't backup when server down")
	}

	switch cluster.Conf.BackupLogicalType {
	case config.ConstBackupLogicalTypeMysqldump:
		if _, err := os.Stat(cluster.GetMysqlDumpPath()); os.IsNotExist(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "File does not exist %s", cluster.GetMysqlDumpPath())
			return err
		}
	case config.ConstBackupLogicalTypeMydumper:
		if _, err := os.Stat(cluster.GetMyDumperPath()); os.IsNotExist(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "File does not exist %s", cluster.GetMyDumperPath())
			return err
		}
	}

	var waited bool
	//Wait for previous restic backup
	for cluster.IsInBackup() {
		waited = true
		cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Logical", cluster.Conf.BackupLogicalType, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		time.Sleep(1 * time.Second)
	}

	cluster.SetInLogicalBackupState(true)

	// Defer always clears traditional flag; Restic flag cleared by async goroutine
	defer cluster.SetInLogicalBackupState(false)

	if waited {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Resuming logical backup %s after waiting for previous backup to finish for: %s", cluster.Conf.BackupLogicalType, server.URL)
	}

	waited = false
	// Wait for logs job to finish to prevent conflicts with backup metadata updates
	for server.IsInSlowQueryCapture {
		if !waited {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Waiting for logs job to finish before starting backup on %s", server.URL)
			waited = true
		}
		time.Sleep(1 * time.Second)
	}

	// CRITICAL FIX: Set Restic flag AFTER validation passes to prevent orphaned flags
	// The defer clears InLogicalBackup immediately on return, but Restic backup
	// runs async. We must set InResticLogicalBackup NOW to maintain lock continuity.
	resticScheduled := false
	if resticEnabled {
		// Set Restic flag now to ensure atomic transition (no gap where IsInBackup() = false)
		cluster.SetInResticLogicalBackupState(true)
		defer func() {
			if !resticScheduled {
				cluster.SetInResticLogicalBackupState(false)
			}
		}()
	}

	start := time.Now()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Logical backup %s started at %s for: %s", cluster.Conf.BackupLogicalType, start.Format(time.RFC3339), server.URL)
	var prevId int64
	if !isAdhoc {
		prev := cluster.BackupMetaMap.GetPreviousBackup(cluster.Conf.BackupLogicalType, server.URL)
		if prev != nil {
			prevId = prev.Id
		}
	}

	// Check for previous backup size
	if cluster.Conf.BackupCheckFreeSpace {
		err = cluster.CheckEstimatedBackupSize("logical")
		if err != nil {
			return err
		}
	}

	// Remove from backup list, since the file will be replaced
	if !cluster.Conf.BackupKeepUntilValid && !isAdhoc {
		cluster.BackupMetaMap.Delete(prevId)
	}

	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		Id:                start.Unix(),
		StartTime:         start,
		BackupMethod:      backupmgr.BackupMethodLogical,
		BackupTool:        cluster.Conf.BackupLogicalType,
		BackupStrategy:    backupmgr.BackupStrategyFull,
		Source:            server.URL,
		Previous:          prevId,
		BackupLine:        backupLine,
		RetentionDays:     opts.RetentionDays,
		RetentionDuration: strings.TrimSpace(opts.RetentionDuration),
		ResticEnabled:     resticEnabled,
	}
	server.ensureBackupSessionID(server.LastBackupMeta.Logical, backupmgr.BackupMethodLogical, start, backupLine)

	cluster.BackupMetaMap.Set(server.LastBackupMeta.Logical.Id, server.LastBackupMeta.Logical)

	// Removing previous valid backup state and start
	if !isAdhoc {
		server.DelBackupLogicalCookie()
	}

	if cluster.Conf.BackupSplitMysqlUser {
		server.LastBackupMeta.Logical.SplitUser = true
		err := server.JobBackupMysqldumpUser()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error mysqldump backup request: %s", err.Error())
			return err
		}
	}

	//Skip other type if using backup script
	if cluster.Conf.BackupSaveScript != "" {
		task := "script"
		filename := server.GetMyBackupDirectory() + "mysqldump.sql.gz"
		if isAdhoc {
			filename = fmt.Sprintf("%smysqldump.%d.sql.gz", server.GetMyBackupDirectory(), start.Unix())
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Ad-hoc backup with backup-save-script using unique destination %s", filename)
		}

		// Override backup tool and destination
		server.LastBackupMeta.Logical.BackupTool = task
		server.LastBackupMeta.Logical.Dest = filename

		// Record task for metadata check
		server.JobsUpdateState(task, "", 1, 0)

		err = server.JobBackupScript(filename)
		if err == nil {
			server.JobsUpdateState(task, "Backup completed", 3, 1)
			server.LastBackupMeta.Logical.Completed = true
			if !isAdhoc {
				server.SetBackupLogicalCookie(task)
			}
		} else {
			server.JobsUpdateState(task, err.Error(), 5, 1)
		}
	} else {
		task := cluster.Conf.BackupLogicalType

		if cluster.Conf.MonitorScheduler {
			server.JobInsertTask(task, "0", cluster.Conf.MonitorAddress)
		} else {
			server.JobsUpdateState(task, "", 0, 0)
		}

		//Change to switch since we only allow one type of backup (for now)
		switch cluster.Conf.BackupLogicalType {
		case config.ConstBackupLogicalTypeMysqldump:
			filename, outputdir, dest, compressed := server.resolveMysqldumpDest(isAdhoc, cluster.Conf.BackupMysqldumpSplitDump, start)
			oldV, _ := cluster.GetToolsVersion("client-dump")
			if oldV != nil {
				server.LastBackupMeta.Logical.BackupToolVersion = oldV.ToString()
			}
			server.LastBackupMeta.Logical.Dest = dest
			server.LastBackupMeta.Logical.Compressed = compressed
			server.LastBackupMeta.Logical.SplitDump = cluster.Conf.BackupMysqldumpSplitDump
			if cluster.Conf.BackupKeepUntilValid && !isAdhoc {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rename previous backup to .old")
				if cluster.Conf.BackupMysqldumpSplitDump {
					exec.Command("mv", outputdir, outputdir+".old").Run()
				} else {
					exec.Command("mv", filename, filename+".old").Run()
				}
			}

			allowRotate := cluster.Conf.BackupKeepUntilValid && !isAdhoc
			if cluster.Conf.BackupMysqldumpSplitDump {
				err = server.JobBackupMysqldump(ctx, task, outputdir, allowRotate)
			} else {
				err = server.JobBackupMysqldump(ctx, task, filename, allowRotate)
			}
			if err != nil {
				result := err.Error()
				if errors.Is(err, errJobCanceledByUser) {
					result = "cancelled by user"
				}
				if e2 := server.JobsUpdateState(task, result, 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := server.JobsUpdateState(task, "Backup completed", 3, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
				checkPath := filename
				if cluster.Conf.BackupMysqldumpSplitDump {
					checkPath = outputdir
				}
				_, e3 := os.Stat(checkPath)
				if e3 == nil {
					server.LastBackupMeta.Logical.EndTime = time.Now()
					server.LastBackupMeta.Logical.GetSizeAndFileCount()
					server.LastBackupMeta.Logical.Completed = true
					if !isAdhoc {
						server.SetBackupLogicalCookie(config.ConstBackupLogicalTypeMysqldump)
					}
				}
			}
		case config.ConstBackupLogicalTypeDumpling:
			outputdir := server.GetMyBackupDirectory() + "dumpling"
			if isAdhoc {
				outputdir = fmt.Sprintf("%sdumpling.%d", server.GetMyBackupDirectory(), start.Unix())
			}
			oldV, _ := cluster.GetToolsVersion("dumpling")
			if oldV != nil {
				server.LastBackupMeta.Logical.BackupToolVersion = oldV.ToString()
			}
			server.LastBackupMeta.Logical.Dest = outputdir
			if cluster.Conf.BackupKeepUntilValid && !isAdhoc {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rename previous backup to .old")
				exec.Command("mv", outputdir, outputdir+".old").Run()
			}

			err = server.JobBackupDumpling(outputdir + "/")
			if err != nil {
				if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := server.JobsUpdateState(task, "Backup completed", 3, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
				_, e3 := os.Stat(outputdir)
				if e3 == nil {
					server.LastBackupMeta.Logical.EndTime = time.Now()
					server.LastBackupMeta.Logical.GetSizeAndFileCount()
					server.LastBackupMeta.Logical.Completed = true
					if !isAdhoc {
						server.SetBackupLogicalCookie(config.ConstBackupLogicalTypeDumpling)
					}
				}
			}
		case config.ConstBackupLogicalTypeMydumper:
			outputdir := server.GetMyBackupDirectory() + "mydumper"
			if isAdhoc {
				outputdir = fmt.Sprintf("%smydumper.%d", server.GetMyBackupDirectory(), start.Unix())
			}
			oldV, _ := cluster.GetToolsVersion("mydumper")
			if oldV != nil {
				server.LastBackupMeta.Logical.BackupToolVersion = oldV.ToString()
			}
			server.LastBackupMeta.Logical.Dest = outputdir
			server.LastBackupMeta.Logical.Compressed = true
			if cluster.Conf.BackupKeepUntilValid && !isAdhoc {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rename previous backup to .old")
				exec.Command("mv", outputdir, outputdir+".old").Run()
			}
			err = server.JobBackupMyDumper(outputdir + "/")
			if err != nil {
				if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := server.JobsUpdateState(task, "Backup completed", 3, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}

				_, e3 := os.Stat(outputdir)
				if e3 == nil {
					server.LastBackupMeta.Logical.EndTime = time.Now()
					server.LastBackupMeta.Logical.GetSizeAndFileCount()
					server.LastBackupMeta.Logical.Completed = true
					if !isAdhoc {
						server.SetBackupLogicalCookie(config.ConstBackupLogicalTypeMydumper)
					}
				}
			}
		case config.ConstBackupLogicalTypeRiver:
			//No change on river
			err = server.JobBackupRiver()
			if err != nil {
				if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			} else {
				if e2 := server.JobsUpdateState(task, "Backup completed", 3, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			}
		}
	}

	if err == nil && cluster.Conf.BackupEncryptionEnabled && server.LastBackupMeta.Logical != nil && server.LastBackupMeta.Logical.Completed {
		sourcePath := strings.TrimSpace(server.LastBackupMeta.Logical.Dest)
		sourceInfo, statErr := os.Stat(sourcePath)
		if statErr != nil {
			encryptErr := fmt.Errorf("failed to stat backup destination before encryption: %w", statErr)
			err = fmt.Errorf("logical backup encryption failed for %s: %w", server.URL, encryptErr)
			server.LastBackupMeta.Logical.Completed = false
			if e2 := server.JobsUpdateState(server.LastBackupMeta.Logical.BackupTool, err.Error(), 5, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
		} else {
			var encryptErr error
			if sourceInfo.IsDir() {
				encryptErr = server.encryptDirectoryBackupMetadata(server.LastBackupMeta.Logical, "logical")
			} else {
				encryptErr = server.encryptBackupMetadataFile(server.LastBackupMeta.Logical, "logical", false)
			}
			if encryptErr != nil {
				err = fmt.Errorf("logical backup encryption failed for %s: %w", server.URL, encryptErr)
				server.LastBackupMeta.Logical.Completed = false
				if e2 := server.JobsUpdateState(server.LastBackupMeta.Logical.BackupTool, err.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			}
		}
	}

	server.WriteBackupMetadata(backupmgr.BackupMethodLogical)
	if err == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "[SUCCESS] Finish logical backup %s for: %s", cluster.Conf.BackupLogicalType, server.URL)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "[ERROR] Finish logical backup %s for: %s", cluster.Conf.BackupLogicalType, server.URL)
	}
	elapsed := time.Since(start).Round(time.Second)
	backupLogLevel := config.LvlInfo
	if err != nil {
		backupLogLevel = config.LvlWarn
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, backupLogLevel, "Logical backup %s completed in %s (started at %s) for: %s", cluster.Conf.BackupLogicalType, elapsed, start.Format(time.RFC3339), server.URL)

	// Create restic snapshot asynchronously and update metadata when complete
	backtype := "logical"

	// NOTE: InResticLogicalBackup flag already set at function start for atomic transition
	// No need to set it again here - it's already protecting the async operation
	if resticEnabled && err == nil {
		resticPath := server.GetMyBackupDirectory()
		if server.LastBackupMeta.Logical != nil && server.LastBackupMeta.Logical.Dest != "" {
			resticPath = server.LastBackupMeta.Logical.Dest
		}
		// Note: BackupRestic handles its own error logging and flag clearing if prerequisites fail
		resticScheduled = true
		server.BackupRestic(backupmgr.BackupMethodLogical, true, resticPath, server.BuildResticTags(backtype, cluster.Conf.BackupLogicalType, backupLine, server.LastBackupMeta.Logical)...)
	} else if resticEnabled && err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Skipping restic backup because logical backup failed for: %s", server.URL)
	}

	return nil
}

func (server *ServerMonitor) copyLogs(r io.Reader, module int, level string) {
	cluster := server.ClusterGroup
	//	buf := make([]byte, 1024)
	s := bufio.NewScanner(r)
	for {
		if !s.Scan() {
			break
		} else {
			//Remove empty lines
			if strings.TrimSpace(s.Text()) != "" {
				cluster.LogModulePrintf(cluster.Conf.Verbose, module, level, "[%s] %s", server.Name, s.Text())
			}
		}
	}
}

func (server *ServerMonitor) copyLogsPrefix(r io.Reader, module int, level string, prefix ...string) {
	cluster := server.ClusterGroup
	//	buf := make([]byte, 1024)
	s := bufio.NewScanner(r)
	for {
		if !s.Scan() {
			break
		} else {
			//Remove empty lines
			found := false
			for _, p := range prefix {
				if strings.HasPrefix(s.Text(), p) {
					cluster.LogModulePrintf(cluster.Conf.Verbose, module, level, "[%s] %s", server.Name, strings.TrimPrefix(s.Text(), p))
					found = true
					break
				}
			}

			if !found && strings.Contains(s.Text(), "bash:") {
				cluster.LogModulePrintf(cluster.Conf.Verbose, module, config.LvlWarn, "[%s] %s", server.Name, s.Text()) // Warning for bash error
			}
		}
	}
}

func (server *ServerMonitor) copyTaskDebugLogs(r io.Reader, module int, task string) {
	cluster := server.ClusterGroup
	//	buf := make([]byte, 1024)
	s := bufio.NewScanner(r)
	for {
		if !s.Scan() {
			break
		} else {
			//Remove empty lines
			if strings.TrimSpace(s.Text()) != "" {
				cluster.LogTaskPrintDebug(cluster.Conf.Verbose, module, server.Name+task, "[%s] %s", server.Name, s.Text())
			}
		}
	}
}

func (server *ServerMonitor) myDumperCopyLogs(r io.Reader, module int, level string) bool {
	cluster := server.ClusterGroup
	valid := true
	//	buf := make([]byte, 1024)
	s := bufio.NewScanner(r)
	for {
		if !s.Scan() {
			break
		} else {
			stream := s.Text()
			if strings.Contains(stream, "Error") {
				if !strings.Contains(stream, "#mysql50#") {
					valid = false
				}
				cluster.LogModulePrintf(cluster.Conf.Verbose, module, config.LvlErr, "[%s] %s", server.Name, stream)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, module, level, "[%s] %s", server.Name, stream)
			}
		}
	}
	return valid
}

func (server *ServerMonitor) BackupRestic(backupMethod backupmgr.BackupMethod, updateMetadata bool, backupPath string, tags ...string) {
	cluster := server.ClusterGroup

	// Defensive checks - clear flag and return early if prerequisites not met
	if !cluster.Conf.BackupRestic {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Restic backup called but disabled for %s", server.URL)
		// Clear the flag that might have been set by caller
		switch backupMethod {
		case backupmgr.BackupMethodLogical:
			cluster.SetInResticLogicalBackupState(false)
		case backupmgr.BackupMethodPhysical:
			cluster.SetInResticPhysicalBackupState(false)
		}
		return
	}

	if cluster.ResticManager == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Restic manager not initialized for %s, cannot backup", server.URL)
		// Clear the flag that might have been set by caller
		switch backupMethod {
		case backupmgr.BackupMethodLogical:
			cluster.SetInResticLogicalBackupState(false)
		case backupmgr.BackupMethodPhysical:
			cluster.SetInResticPhysicalBackupState(false)
		}
		return
	}

	if strings.TrimSpace(backupPath) == "" {
		backupPath = server.GetMyBackupDirectory()
	}

	// Check if Restic backup is already in progress for this backup method
	// Note: The flag might already be set by the caller for atomic transition
	if cluster.IsInResticBackupForMethod(backupMethod) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Restic backup flag already set for %s, proceeding with queue", server.URL)
	} else {
		// Set flag if not already set (for binlog backups, etc.)
		switch backupMethod {
		case backupmgr.BackupMethodLogical:
			cluster.SetInResticLogicalBackupState(true)
		case backupmgr.BackupMethodPhysical:
			cluster.SetInResticPhysicalBackupState(true)
		}
	}

	var needpurge bool

	defer cluster.UpdateDiskStat(cluster.GetResticLocalDir())

	if cluster.Conf.BackupCheckFreeSpace {
		diskstat, err := cluster.GetDiskStat(cluster.GetResticLocalDir())
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error getting disk stat for restic backup dir: %s", cluster.GetResticLocalDir())
			cluster.ResticManager.PauseWorkerOnDisk()
		} else {
			// Use specific treshold if defined
			treshold := cluster.Conf.BackupDiskTresholdCrit
			if cluster.Conf.BackupResticPurgeOldestOnDiskThreshold > 0 {
				treshold = cluster.Conf.BackupResticPurgeOldestOnDiskThreshold
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Using specific restic purge treshold %d%%", treshold)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Using global backup disk treshold %d%%", treshold)
			}

			needpurge = diskstat.UsedPercent >= float64(treshold)

			if needpurge {
				if cluster.Conf.BackupResticPurgeOldestOnDiskSpace {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Restic backup disk usage %.2f%% over treshold %d%%. Purging oldest backup before new backup.", diskstat.UsedPercent, treshold)
					cluster.ResticManager.PurgeOldestBackup()
				} else {
					cluster.ResticManager.PauseWorkerOnDisk()
				}
			}
		}
	}

	resticHost := strings.TrimSpace(cluster.Conf.BackupResticHost)
	if strings.EqualFold(resticHost, "default") || strings.EqualFold(resticHost, "none") {
		resticHost = ""
	}

	// Add backup task asynchronously with callback to update metadata
	resultCh := cluster.ResticManager.AddBackupTaskWithCallback(backupPath, tags, resticHost)

	// Launch goroutine to wait for result and update metadata
	go func() {
		// Ensure flag is cleared when goroutine exits
		defer func() {
			switch backupMethod {
			case backupmgr.BackupMethodLogical:
				cluster.SetInResticLogicalBackupState(false)
			case backupmgr.BackupMethodPhysical:
				cluster.SetInResticPhysicalBackupState(false)
			}
		}()

		result := <-resultCh
		if result.Error != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Restic backup failed for %s: %s", server.URL, result.Error)
			return
		}

		if result.SnapshotID != "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Restic backup completed for %s: snapshot %s", server.URL, result.SnapshotID[:8])
			if updateMetadata {
				server.UpdateBackupMetadataWithRestic(backupMethod, result.SnapshotID)
			}
		}
	}()
}

func (server *ServerMonitor) copyAndCapture(w io.Writer, r io.Reader) ([]byte, error) {
	var out []byte
	buf := make([]byte, 1024, 1024)
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			d := buf[:n]
			out = append(out, d...)
			_, err := w.Write(d)
			if err != nil {
				return out, err
			}
		}
		if err != nil {
			// Read returns io.EOF at the end of file, which is not an error for us
			if err == io.EOF {
				err = nil
			}
			return out, err
		}
	}
}

func (server *ServerMonitor) JobBackupBinlog(binlogfile string, isPurge bool) error {
	cluster := server.ClusterGroup
	var err error

	if !server.IsMaster() {
		err = errors.New("Cancelling backup because server is not master")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "%s", err.Error())
		return err
	}
	if cluster.IsInFailover() {
		err = errors.New("Cancel job copy binlog during failover")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "%s", err.Error())
		return err
	}
	if !cluster.Conf.BackupBinlogs {
		err = errors.New("Copy binlog not enable")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "%s", err.Error())
		return err
	}
	if inReseed, task := server.GetReseedingState(); inReseed {
		err = fmt.Errorf("Server is in reseeding state by %s", task)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModPurge, config.LvlDbg, "%s", err.Error())
		return err
	}

	if _, err := os.Stat(cluster.GetMysqlBinlogPath()); os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "File does not exist %s", cluster.GetMysqlBinlogPath())
		return err
	}

	//Skip setting in backup state due to batch purging
	if !isPurge {
		if cluster.IsInBackup() {
			cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Binary Log", cluster.Conf.BinlogCopyMode, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			time.Sleep(1 * time.Second)

			return server.JobBackupBinlog(binlogfile, isPurge)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Initiating backup binlog for %s", binlogfile)
		cluster.SetInBinlogBackupState(true)
		defer cluster.SetInBinlogBackupState(false)
	}

	if cluster.Conf.BackupCheckFreeSpace {
		err = cluster.CheckEstimatedBackupSize("binlog")
		if err != nil {
			return err
		}
	}

	server.SetBackingUpBinaryLog(true)
	defer server.SetBackingUpBinaryLog(false)

	params := append(cluster.GetBinlogCredentials(server), "--read-from-remote-server", "--raw", "--server-id=10000", "--result-file="+server.GetMyBackupDirectory())
	params = append(params, server.GetSSLClientParam("client-binlog")...)
	params = append(params, binlogfile)
	cmdrun := exec.Command(cluster.GetMysqlBinlogPath(), misc.RemoveEmptyString(params)...)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "%s %s", cluster.GetMysqlBinlogPath(), strings.ReplaceAll(strings.Join(cmdrun.Args, " "), "="+cluster.GetRplPass(), "=XXXX"))

	cmdErrPipe, _ := cmdrun.StderrPipe()
	cmdOutPipe, _ := cmdrun.StdoutPipe()

	if err := cmdrun.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Failed mysqlbinlog command: %s at %s", err, strings.Replace(cmdrun.String(), "="+cluster.GetDbPass(), "=XXXX", -1))
		return err
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		server.copyLogs(cmdErrPipe, config.ConstLogModTask, config.LvlErr)
	}()

	go func() {
		defer wg.Done()
		server.copyLogs(cmdOutPipe, config.ConstLogModTask, config.LvlDbg)
	}()

	wg.Wait()

	if err := cmdrun.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Failed to backup binlogs of %s,%s", server.URL, err.Error())
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s %s", cluster.GetMysqlBinlogPath(), strings.ReplaceAll(strings.Join(cmdrun.Args, " "), "="+cluster.GetRplPass(), "=XXXX"))
		return err
	}

	//Skip copying to resting when purge due to batching
	if !isPurge {
		if idx := slices.Index(server.BinaryLogMetaToWrite, binlogfile); idx == -1 {
			server.BinaryLogMetaToWrite = append(server.BinaryLogMetaToWrite, binlogfile)
		}
		server.WriteBackupBinlogMetadata()
		// Backup to restic when no error (defer to prevent unfinished physical copy)
		backtype := "binlog"
		// Use BackupMethodLogical for binlogs (metadata won't be updated, just snapshot created)
		defer server.BackupRestic(backupmgr.BackupMethodLogical, false, server.GetMyBackupDirectory(), server.BuildResticTags(backtype, "", backupmgr.BackupLineDefault, nil)...)
	}

	return nil
}

func (server *ServerMonitor) JobBackupBinlogPurge(binlogfile string) error {
	cluster := server.ClusterGroup
	if !server.IsMaster() {
		return errors.New("Purge only master binlog")
	}
	if !cluster.Conf.BackupBinlogs {
		return errors.New("Copy binlog not enable")
	}

	if cluster.IsInBackup() {
		cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Binary Log", cluster.Conf.BinlogCopyMode, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
		time.Sleep(1 * time.Second)

		return server.JobBackupBinlogPurge(binlogfile)
	}

	cluster.SetInBinlogBackupState(true)
	defer cluster.SetInBinlogBackupState(false)

	binlogfilestart, _ := strconv.Atoi(strings.Split(binlogfile, ".")[1])
	prefix := strings.Split(binlogfile, ".")[0]
	binlogfilestop := binlogfilestart - cluster.Conf.BackupBinlogsKeep
	keeping := make(map[string]int)
	for binlogfilestop < binlogfilestart {
		if binlogfilestop > 0 {
			filename := prefix + "." + fmt.Sprintf("%06d", binlogfilestop)
			if _, err := os.Stat(server.GetMyBackupDirectory() + "/" + filename); os.IsNotExist(err) {
				if _, ok := server.BinaryLogFiles.CheckAndGet(filename); ok {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup master missing binlog of %s,%s", server.URL, filename)
					//Set true to skip sending to resting multiple times
					server.InitiateJobBackupBinlog(filename, true)
				}
			}
			keeping[filename] = binlogfilestop
		}
		binlogfilestop++
	}
	files, err := os.ReadDir(server.GetMyBackupDirectory())
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Failed to read backup directory of %s,%s", server.URL, err.Error())
	}

	for _, file := range files {
		_, ok := keeping[file.Name()]
		if strings.HasPrefix(file.Name(), prefix) && !ok {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Purging binlog file from backup dir %s", file.Name())
			if err := os.Remove(server.GetMyBackupDirectory() + "/" + file.Name()); err == nil {
				server.BinaryLogMetaToRemove = append(server.BinaryLogMetaToRemove, file.Name())
			}
		}
	}

	server.WriteBackupBinlogMetadata()
	return nil
}

func (server *ServerMonitor) JobCapturePurge(path string, keep int) error {
	drop := make(map[string]int)

	files, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	i := 0
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "capture") {
			i++
			drop[file.Name()] = i
		}
	}
	for key, value := range drop {

		if value < len(drop)-keep {
			os.Remove(path + "/" + key)
		}

	}
	return nil
}

func (server *ServerMonitor) JobGetDumpGtidParameter() string {
	usegtid := ""
	// MySQL force GTID in server configuration the dump transparently include GTID pos. In MariaDB both positional or GTID is possible and so must be choose at dump
	// Issue #422
	// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask,LvlInfo, "gniac2 %s: %s,", server.URL, server.GetVersion())
	if server.GetVersion().IsMariaDB() {
		if server.HasGTIDReplication() {
			usegtid = "--gtid=true"
		} else {
			usegtid = "--gtid=false"
		}
	}
	return usegtid
}

func (cluster *Cluster) JobRejoinMysqldumpFromSource(source *ServerMonitor, dest *ServerMonitor) error {
	defer dest.SetInReseedBackup("")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Rejoining from direct mysqldump from %s", source.URL)

	dest.StopSlave()
	dumpCmd := exec.Command(cluster.GetMysqlDumpPath(), cluster.GetMysqlDumpOptions(source, dest.JobGetDumpGtidParameter())...)
	stderrIn, _ := dumpCmd.StderrPipe()

	cliParams := append(cluster.GetDumpCredentials(dest), dest.GetSSLClientParam("client")...)
	cliParams = append(cliParams, strings.Split(cluster.Conf.BackupMysqlclientOptions, " ")...)

	clientCmd := exec.Command(cluster.GetMysqlclientPath(), misc.RemoveEmptyString(cliParams)...)
	stderrOut, _ := clientCmd.StderrPipe()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Command: %s ", strings.Replace(dumpCmd.String(), "="+cluster.GetDbPass(), "=XXXX", -1))

	iodumpreader, _ := dumpCmd.StdoutPipe()

	cmdstring := "RESET MASTER;SET sql_log_bin=0;SET long_query_time=10;"
	if dest.DBVersion.IsMySQLOrPerconaGreater84() {
		cmdstring = "RESET BINARY LOGS AND GTIDS;SET sql_log_bin=0;SET long_query_time=10;"
	}
	clientCmd.Stdin = io.MultiReader(bytes.NewBufferString(cmdstring), iodumpreader)

	if err := dumpCmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Failed mysqldump command: %s at %s", err, strings.Replace(dumpCmd.String(), "="+cluster.GetDbPass(), "=XXXX", -1))
		return err
	}
	if err := clientCmd.Start(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Can't start mysql client:%s at %s", err, strings.Replace(clientCmd.String(), "="+cluster.GetDbPass(), "=XXXX", -1))
		return err
	}
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		source.copyLogs(stderrIn, config.ConstLogModBackupStream, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		dest.copyLogs(stderrOut, config.ConstLogModBackupStream, config.LvlDbg)
	}()

	wg.Wait()

	// Wait for the commands to complete
	if err := dumpCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error waiting for dump client on %s: %s", source.URL, err.Error())
		return err
	}

	if err := clientCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error waiting for db client on %s: %s", dest.URL, err.Error())
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Start slave after dump on %s", dest.URL)
	dest.StartSlave()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Reseed slave from %s to %s finished", source.URL, dest.URL)
	return nil
}

func (server *ServerMonitor) JobBackupBinlogSSH(binlogfile string, isPurge bool) error {
	cluster := server.ClusterGroup
	if !server.IsMaster() {
		return errors.New("Copy only master binlog")
	}
	if cluster.IsInFailover() {
		return errors.New("Cancel job copy binlog during failover")
	}
	if !cluster.Conf.BackupBinlogs {
		return errors.New("Copy binlog not enable")
	}
	if !cluster.Conf.OnPremiseSSH {
		return errors.New("On-premise SSH not enable, cannot backup via SSH")
	}

	//Skip setting in backup state due to batch purging
	if !isPurge {
		if cluster.IsInBackup() {
			cluster.SetState("WARN0110", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(cluster.GetErrorList()["WARN0110"], "Binary Log", cluster.Conf.BinlogCopyMode, server.URL), ErrFrom: "JOB", ServerUrl: server.URL})
			time.Sleep(1 * time.Second)

			return server.JobBackupBinlogSSH(binlogfile, isPurge)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Initiating backup binlog for %s", binlogfile)
		cluster.SetInBinlogBackupState(true)
		defer cluster.SetInBinlogBackupState(false)
	}

	server.SetBackingUpBinaryLog(true)
	defer server.SetBackingUpBinaryLog(false)

	client, err := server.GetCluster().OnPremiseConnect(server)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "OnPremise run job on %s: %s", server.URL, err)
		return err
	}
	defer client.Close()

	remotefile := server.GetBinaryLogDir() + "/" + binlogfile
	localfile := server.GetMyBackupDirectory() + "/" + binlogfile

	fileinfo, err := client.Sftp().Stat(remotefile)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error while getting binlog file [%s] stat:  %s", remotefile, err)
		return err
	}

	err = client.Sftp().Download(remotefile, localfile)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Download binlog error:  %s", err)
		return err
	}

	localinfo, err := os.Stat(localfile)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error while getting backed up binlog file [%s] stat:  %s", localfile, err)
		return err
	}

	if fileinfo.Size() != localinfo.Size() {
		err := errors.New("Remote filesize is different with downloaded filesize")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error while getting backed up binlog file [%s] stat:  %s", localfile, err)
		return err
	}

	//Skip copying to resting when purge due to batching
	if !isPurge {
		if idx := slices.Index(server.BinaryLogMetaToWrite, binlogfile); idx == -1 {
			server.BinaryLogMetaToWrite = append(server.BinaryLogMetaToWrite, binlogfile)
		}
		server.WriteBackupBinlogMetadata()

		// Backup to restic when no error (defer to prevent unfinished physical copy)
		backtype := "binlog"
		// Use BackupMethodLogical for binlogs (metadata won't be updated, just snapshot created)
		defer server.BackupRestic(backupmgr.BackupMethodLogical, false, server.GetMyBackupDirectory(), server.BuildResticTags(backtype, "", backupmgr.BackupLineDefault, nil)...)
	}
	return nil
}

func (server *ServerMonitor) InitiateJobBackupBinlog(binlogfile string, isPurge bool) error {
	cluster := server.ClusterGroup

	switch cluster.Conf.BinlogCopyMode {
	case "client", "mysqlbinlog":
		return server.JobBackupBinlog(binlogfile, isPurge)
	case "ssh":
		return server.JobBackupBinlogSSH(binlogfile, isPurge)
	case "script":
		return cluster.BinlogCopyScript(server, binlogfile, isPurge)
	}

	return errors.New("Wrong configuration for Backup Binlog Method!")
}

func (server *ServerMonitor) WaitAndSendSST(task string, filename string, uncompress bool, loop int) error {
	cluster := server.ClusterGroup

	if !server.HasReseedingState(task) {
		return fmt.Errorf("Server is not in reseeding state on %s", server.URL)
	}

	if server.Conn == nil {
		return fmt.Errorf("No connection pool on %s", server.URL)
	}

	// Use iterative loop instead of recursion to avoid stack buildup
	maxLoop := cluster.Conf.SSTWaitMaxLoop
	retryDelay := time.Second * time.Duration(cluster.Conf.SSTWaitRetryDelay)

	for attempt := loop; attempt < maxLoop; attempt++ {
		conn, err := server.GetConnNoBinlog(server.Conn)
		if err != nil {
			return fmt.Errorf("Error connecting to %s: %s", server.URL, err)
		}

		count, err := server.GetJobCount(conn, task, 2)
		conn.Close()
		if err != nil {
			return fmt.Errorf("Error getting task on %s: %s", server.URL, err)
		}

		// Check if job is ready (state=2 means JobStateHalted, waiting for SST)
		if count > 0 {
			server.JobsUpdateState(task, "processing", 1, 0)
			go func() {
				sendStart := time.Now()
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST send for %s started at %s (file: %s)", task, sendStart.Format(time.RFC3339), filename)
				err := cluster.SSTRunSender(filename, server, uncompress)
				elapsed := time.Since(sendStart).Round(time.Second)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "SST send for %s failed after %s: %s", task, elapsed, err.Error())
					server.JobsUpdateState(task, err.Error(), 5, 0)
					return
				}
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST send for %s completed in %s (started at %s)", task, elapsed, sendStart.Format(time.RFC3339))
			}()
			return nil
		}

		// Wait before retrying (not last iteration)
		if attempt < maxLoop-1 {
			time.Sleep(retryDelay)
		}
	}

	server.JobsUpdateState(task, "Waiting more than max loop", 5, 0)
	server.SetNeedRefreshJobs(true)
	return errors.New("Error: waiting for " + task + " more than max loop.")
}

func (server *ServerMonitor) WaitAndSendSSTStream(ctx context.Context, task string, sourceName string, uncompress bool, loop int, opener SSTStreamOpener) error {
	cluster := server.ClusterGroup

	if ctx == nil {
		ctx = context.Background()
	}

	if opener == nil {
		return fmt.Errorf("SST stream opener is nil")
	}

	if !server.HasReseedingState(task) {
		return fmt.Errorf("Server is not in reseeding state on %s", server.URL)
	}

	if server.Conn == nil {
		return fmt.Errorf("No connection pool on %s", server.URL)
	}

	// Use iterative loop instead of recursion to avoid stack buildup
	// and ensure responsive context cancellation
	maxLoop := cluster.Conf.SSTWaitMaxLoop
	retryDelay := time.Second * time.Duration(cluster.Conf.SSTWaitRetryDelay)

	for attempt := loop; attempt < maxLoop; attempt++ {
		// Check context cancellation at start of each iteration
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("SST stream canceled: %w", err)
		}

		conn, err := server.GetConnNoBinlog(server.Conn)
		if err != nil {
			return fmt.Errorf("Error connecting to %s: %s", server.URL, err)
		}

		count, err := server.GetJobCount(conn, task, 2)
		conn.Close()
		if err != nil {
			return fmt.Errorf("Error getting task on %s: %s", server.URL, err)
		}

		// Check if job is ready (state=2 means JobStateHalted, waiting for SST)
		if count > 0 {
			server.JobsUpdateState(task, "processing", 1, 0)
			go func() {
				sendStart := time.Now()
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST stream send for %s started at %s (source: %s)", task, sendStart.Format(time.RFC3339), sourceName)
				if err := ctx.Err(); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "SST stream for %s canceled before start: %s", task, err)
					server.JobsUpdateState(task, err.Error(), 5, 0)
					return
				}
				err = cluster.SSTRunSenderStream(sourceName, opener, server, uncompress)
				elapsed := time.Since(sendStart).Round(time.Second)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "SST stream send for %s failed after %s: %s", task, elapsed, err.Error())
					server.JobsUpdateState(task, err.Error(), 5, 0)
					return
				}
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModSST, config.LvlInfo, "SST stream send for %s completed in %s (started at %s)", task, elapsed, sendStart.Format(time.RFC3339))
			}()
			return nil
		}

		// Wait before retrying, but allow context cancellation during wait
		select {
		case <-ctx.Done():
			return fmt.Errorf("SST stream canceled: %w", ctx.Err())
		case <-time.After(retryDelay):
			// Continue to next iteration
		}
	}

	server.JobsUpdateState(task, "Waiting more than max loop", 5, 0)
	server.SetNeedRefreshJobs(true)
	return errors.New("Error: waiting for " + task + " more than max loop.")
}

func (server *ServerMonitor) ProcessReseedLogical(task string) error {
	cluster := server.ClusterGroup
	master := cluster.GetMaster()

	//Prevent multiple reseed
	if !server.HasReseedingState(task) {
		return fmt.Errorf("Server is not in %s state", task)
	}

	if master == nil {
		return errors.New("No master found")
	}

	if master != nil && cluster.Conf.SuperReadOnly && master.URL != server.URL && server.HasSuperReadOnlyCapability() {
		cluster.SetState("WARN0114", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0114"], server.URL), ErrFrom: "JOB"})
		return errors.New("Slave is in super read-only")
	}

	backupType := cluster.Conf.BackupLogicalType
	payloadBackupPath := ""
	splitUser := false
	splitUserSet := false
	splitUserOverride := false
	skipMetadata := false
	isPITR := server.PointInTimeMeta.IsInPITR

	parseBool := func(value string) (bool, bool) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return false, false
		}
		parsed, err := strconv.ParseBool(trimmed)
		if err != nil {
			return false, false
		}
		return parsed, true
	}

	if taskInfo := server.JobResults.Get(task); taskInfo != nil && taskInfo.Payload != "" {
		payload := make(map[string]string)
		if err := json.Unmarshal([]byte(taskInfo.Payload), &payload); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Invalid payload for %s on %s: %s", task, server.URL, err)
		} else {
			if v := strings.TrimSpace(payload["backup_type"]); v != "" {
				backupType = v
			}
			if v := strings.TrimSpace(payload["backup_path"]); v != "" {
				payloadBackupPath = filepath.Clean(v)
			}
			if parsed, ok := parseBool(payload["split_user"]); ok {
				splitUser = parsed
				splitUserSet = true
			}
			if parsed, ok := parseBool(payload["split_user_override"]); ok {
				splitUserOverride = parsed
			}
			if parsed, ok := parseBool(payload["skip_metadata"]); ok {
				skipMetadata = parsed
			}
			if parsed, ok := parseBool(payload["is_pitr"]); ok {
				isPITR = parsed
			}
		}
	}

	defer func() {
		if server.HasReseedingState(task) {
			server.SetInReseedBackup("")
		}
	}()

	if cluster.Conf.BackupLoadScript != "" {
		if !isPITR {
			logs, err := server.StopSlave()
			if err != nil {
				cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed stop slave on server: %s %s", server.URL, err)
			}

			if server.DBVersion.IsMySQLOrPercona() {
				if server.HasMySQLGTID() {
					cluster.pointSlaveToMasterWithMode(server, "MASTER_AUTO_POSITION")
				} else {
					cluster.pointSlaveToMasterPositional(server)
				}
			} else {
				cluster.pointSlaveToMasterWithMode(server, "SLAVE_POS")
			}
		}

		server.JobsUpdateState(task, "processing", 1, 0)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive reseed logical backup %s request for server: %s", backupType, server.URL)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Using script from backup-load-script on %s", server.URL)
		if err := server.JobReseedBackupScript(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error reseed %s on %s: %s", backupType, server.URL, err.Error())
			if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
			return err
		}
		if e2 := server.JobsUpdateState(task, "Reseed completed", 3, 1); e2 != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
		}
		return nil
	}

	if backupType == config.ConstBackupLogicalTypeDumpling {
		return errors.New("Logical reseed with dumpling is not supported")
	}
	if backupType == config.ConstBackupLogicalTypeRiver {
		return errors.New("Logical reseed with internal backup type is not supported")
	}
	if backupType != config.ConstBackupLogicalTypeMysqldump && backupType != config.ConstBackupLogicalTypeMydumper {
		return fmt.Errorf("Logical reseed backup type %s is not supported", backupType)
	}

	useMaster := true
	source := master
	var dest string
	var destCandidates []string
	switch backupType {
	case config.ConstBackupLogicalTypeMysqldump:
		dest = "mysqldump.sql.gz"
		destCandidates = []string{"mysqldump.sql.gz", "splitdump"}
	case config.ConstBackupLogicalTypeMydumper:
		dest = "mydumper"
	}
	if len(destCandidates) == 0 {
		destCandidates = []string{dest}
	}

	var backupfile string
	if payloadBackupPath != "" {
		if strings.EqualFold(payloadBackupPath, "STREAM") {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Logical reseed payload path indicates STREAM for %s; falling back to discovered backup", task)
		} else if _, err := os.Stat(payloadBackupPath); err == nil {
			backupfile = payloadBackupPath
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Using payload backup path %s for %s", backupfile, task)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Payload backup path not found for %s (%s); falling back to discovered backup", task, err)
		}
	}

	if backupfile == "" {
		bckserver := cluster.GetBackupServer()
		if bckserver != nil && bckserver.HasBackupTypeCookie(backupType) {
			if resolved, ok := resolveLogicalBackupPathFromMeta(bckserver, backupType); ok {
				backupfile = resolved
				useMaster = false
				source = bckserver
			} else if resolved, ok := findExistingBackupPath(bckserver, destCandidates); ok {
				backupfile = resolved
				useMaster = false
				source = bckserver
			} else {
				//Remove false cookie
				bckserver.DelBackupTypeCookie(backupType)
			}
		}

		if backupfile == "" {
			if resolved, ok := resolveLogicalBackupPathFromMeta(master, backupType); ok {
				backupfile = resolved
			} else if resolved, ok := findExistingBackupPath(master, destCandidates); ok {
				backupfile = resolved
			} else {
				backupfile = master.GetMyBackupDirectory() + dest
			}
		}

		if useMaster {
			if _, err := os.Stat(backupfile); err != nil {
				//Remove false cookie
				master.DelBackupTypeCookie(backupType)
				return fmt.Errorf("No backup file found on master for %s", backupType)
			}
		}
	}

	meta := snapshotLogicalBackupMeta(source)
	if !splitUserSet && meta != nil {
		splitUser = meta.SplitUser
	}
	restoreUser := cluster.Conf.BackupRestoreMysqlUser && splitUser
	restoreBackupPath, restoreCleanup, restorePrepErr := server.prepareEncryptedDirectoryLogicalRestorePath(backupfile, backupType, meta)
	if restorePrepErr != nil {
		return restorePrepErr
	}
	if restoreCleanup != nil {
		defer restoreCleanup()
	}

	// Set replication master to current master if not PITR
	if !isPITR {
		logs, err := server.StopSlave()
		if err != nil {
			cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed stop slave on server: %s %s", server.URL, err)
		}

		if server.DBVersion.IsMySQLOrPercona() {
			if server.HasMySQLGTID() {
				cluster.pointSlaveToMasterWithMode(server, "MASTER_AUTO_POSITION")
			} else {
				cluster.pointSlaveToMasterPositional(server)
			}
		} else {
			cluster.pointSlaveToMasterWithMode(server, "SLAVE_POS")
		}
	}

	ctx := context.Background()
	server.JobsUpdateState(task, "processing", 1, 0)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Receive reseed logical backup %s request for server: %s", backupType, server.URL)
	if splitUserOverride {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Using split-user override=%t for reseed logical backup from path %s on %s", splitUser, backupfile, server.URL)
	}

	var err error
	if backupType == config.ConstBackupLogicalTypeMysqldump {
		if skipMetadata {
			err = server.reseedMysqldumpWithSplitdump(ctx, restoreBackupPath, restoreUser)
		} else {
			err = server.reseedMysqldumpWithMetadata(ctx, restoreBackupPath, restoreUser, meta)
		}
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error reseed %s on %s: %s", backupType, server.URL, err.Error())
			if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
			return err
		}

		if server.IsSlave && !isPITR {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Start slave after dump on %s", server.URL)
			server.StartSlave()
		}

		if e2 := server.JobsUpdateState(task, "Reseed completed", 3, 1); e2 != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
		}
		return nil
	}

	if backupType == config.ConstBackupLogicalTypeMydumper {
		err = server.JobReseedMyLoader(restoreBackupPath, cluster.Conf.BackupRestoreMysqlUser)
		if err == nil && server.IsSlave && !isPITR {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Parsing mydumper metadata ")
			meta, err2 := cluster.JobMyLoaderParseMeta(restoreBackupPath)
			if err2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "MyLoader metadata parsing: %s", err2)
				err = err2
			} else {
				// Set GTID position for MariaDB
				if server.IsMariaDB() && server.HaveMariaDBGTID {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Starting slave with mydumper metadata")
					server.ExecQueryNoBinLog("SET GLOBAL gtid_slave_pos='"+meta.BinLogUuid+"'", time.Second)
				}
			}

			if err == nil {
				server.StartSlave()
			}
		}

		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error reseed %s on %s: %s", backupType, server.URL, err.Error())
			if e2 := server.JobsUpdateState(task, err.Error(), 5, 1); e2 != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
			}
			return err
		}

		if e2 := server.JobsUpdateState(task, "Reseed completed", 3, 1); e2 != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
		}
		return nil
	}

	return fmt.Errorf("Logical reseed backup type %s is not supported", backupType)
}

func (server *ServerMonitor) ProcessReseedPhysical(task string) error {
	cluster := server.ClusterGroup
	master := cluster.GetMaster()

	//Prevent multiple reseed
	if !server.HasReseedingState(task) {
		return fmt.Errorf("Server is not in %s state", task)
	}

	if master == nil {
		return errors.New("No master found")
	}

	if master != nil && cluster.Conf.SuperReadOnly && master.URL != server.URL && server.HasSuperReadOnlyCapability() {
		cluster.SetState("WARN0114", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0114"], server.URL), ErrFrom: "JOB"})
		return errors.New("Slave is in super read-only")
	}

	backupType := cluster.Conf.BackupPhysicalType
	payloadBackupPath := ""
	resticSnapshotID := ""
	resticSourcePath := ""
	if taskInfo := server.JobResults.Get(task); taskInfo != nil && taskInfo.Payload != "" {
		payload := make(map[string]string)
		if err := json.Unmarshal([]byte(taskInfo.Payload), &payload); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Invalid payload for %s on %s: %s", task, server.URL, err)
		} else {
			if v := strings.TrimSpace(payload["backup_type"]); v != "" {
				if v != backupType {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Using payload backup type %s for %s (was %s)", v, task, backupType)
				}
				backupType = v
			}
			if v := strings.TrimSpace(payload["backup_path"]); v != "" {
				payloadBackupPath = filepath.Clean(v)
			}
			if v := strings.TrimSpace(payload["restic_snapshot_id"]); v != "" {
				resticSnapshotID = v
			}
			if v := strings.TrimSpace(payload["restic_source_path"]); v != "" {
				resticSourcePath = v
			}
		}
	}

	useMaster := true
	file := backupType + ".xbtream"
	if cluster.Conf.CompressBackups {
		file += ".gz"
	}
	backupfile := master.GetMyBackupDirectory() + file

	if payloadBackupPath != "" {
		if strings.EqualFold(payloadBackupPath, "STREAM") {
			if cluster.ResticManager == nil {
				return fmt.Errorf("Cancelling reseed. Restic manager not available for %s", task)
			}
			if resticSnapshotID == "" || resticSourcePath == "" {
				return fmt.Errorf("Cancelling reseed. Missing restic snapshot metadata for %s", task)
			}

			ctx := context.Background()
			var expectedSize int64 = -1
			if sizeBytes, ok := getSnapshotSizeBytes(cluster, resticSnapshotID); ok {
				expectedSize = int64(sizeBytes)
			}

			streamOpener := func() (io.ReadCloser, int64, error) {
				pr, pw := io.Pipe()
				go func() {
					dumpErr := cluster.ResticManager.DumpSnapshot(resticSnapshotID, resticSourcePath, pw)
					if dumpErr != nil {
						_ = pw.CloseWithError(dumpErr)
						return
					}
					_ = pw.Close()
				}()
				return pr, expectedSize, nil
			}

			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Streaming snapshot %s using dump strategy to SST (file: %s)",
				resticLogSnapshotID(cluster, resticSnapshotID), resticSourcePath)

			uncompress := cluster.shouldUncompressOnSenderForReseed()
			go func() {
				err := server.WaitAndSendSSTStream(ctx, task, resticSourcePath, uncompress, 0, streamOpener)
				if err != nil {
					if server.HasReseedingState(task) {
						server.SetInReseedBackup("")
					}
				}
			}()
			return nil
		}

		backupfile = payloadBackupPath
		if _, err := os.Stat(backupfile); err != nil {
			encCandidate := backupfile + ".enc"
			if _, encErr := os.Stat(encCandidate); encErr == nil {
				backupfile = encCandidate
			} else {
				return fmt.Errorf("Cancelling reseed. Payload backup path not found for %s: %s", task, err)
			}
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Using payload backup path %s for %s", backupfile, task)
	} else {
		bckserver := cluster.GetBackupServer()
		if bckserver != nil && bckserver.HasBackupTypeCookie(backupType) {
			if resolved, ok := resolvePhysicalBackupPathFromMeta(bckserver, backupType); ok {
				backupfile = resolved
				useMaster = false
			} else if resolved, ok := findExistingPhysicalBackupPath(bckserver, backupType, cluster.Conf.CompressBackups); ok {
				backupfile = resolved
				useMaster = false
			} else {
				//Remove false cookie
				bckserver.DelBackupTypeCookie(backupType)
			}
		}

		if useMaster {
			if resolved, ok := resolvePhysicalBackupPathFromMeta(master, backupType); ok {
				backupfile = resolved
			} else if resolved, ok := findExistingPhysicalBackupPath(master, backupType, cluster.Conf.CompressBackups); ok {
				backupfile = resolved
			} else {
				//Remove false cookie
				master.DelBackupTypeCookie(backupType)
				return fmt.Errorf("Cancelling reseed. No backup file found on master for %s", backupType)
			}
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sending master physical backup to reseed %s", server.URL)

	uncompress := cluster.shouldUncompressOnSenderForReseed()
	sourceName, streamOpener, streamCleanup, err := server.prepareEncryptedPhysicalRestoreSource(backupfile)
	if err != nil {
		return err
	}
	if streamOpener != nil {
		go func() {
			err := server.WaitAndSendSSTStream(context.Background(), task, sourceName, uncompress, 0, streamOpener)
			if err != nil {
				if streamCleanup != nil {
					streamCleanup()
				}
				if server.HasReseedingState(task) {
					server.SetInReseedBackup("")
				}
			}
		}()
		return nil
	}

	go func() {
		err := server.WaitAndSendSST(task, backupfile, uncompress, 0)
		if err != nil {
			if server.HasReseedingState(task) {
				server.SetInReseedBackup("")
			}
		}
	}()

	return nil
}

func (server *ServerMonitor) ProcessFlashbackPhysical(task string) error {

	cluster := server.ClusterGroup
	master := cluster.GetMaster()

	//Prevent multiple reseed
	if !server.HasReseedingState(task) {
		return errors.New("Server is not in physical flashback state")
	}

	if master == nil {
		return errors.New("No master found")
	}

	if master != nil && cluster.Conf.SuperReadOnly && master.URL != server.URL && server.HasSuperReadOnlyCapability() {
		cluster.SetState("WARN0114", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0114"], server.URL), ErrFrom: "JOB"})
		return errors.New("Slave is in super read-only")
	}

	useSelfBackup := true
	file := cluster.Conf.BackupPhysicalType + ".xbtream"
	if cluster.Conf.CompressBackups {
		file += ".gz"
	}
	backupfile := server.GetMyBackupDirectory() + file

	bckserver := cluster.GetBackupServer()
	if bckserver != nil && bckserver.HasBackupTypeCookie(cluster.Conf.BackupPhysicalType) {
		if resolved, ok := resolvePhysicalBackupPathFromMeta(bckserver, cluster.Conf.BackupPhysicalType); ok {
			backupfile = resolved
			useSelfBackup = false
		} else if resolved, ok := findExistingPhysicalBackupPath(bckserver, cluster.Conf.BackupPhysicalType, cluster.Conf.CompressBackups); ok {
			backupfile = resolved
			useSelfBackup = false
		} else {
			//Remove false cookie
			bckserver.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
		}
	}

	if useSelfBackup {
		if resolved, ok := resolvePhysicalBackupPathFromMeta(server, cluster.Conf.BackupPhysicalType); ok {
			backupfile = resolved
		} else if resolved, ok := findExistingPhysicalBackupPath(server, cluster.Conf.BackupPhysicalType, cluster.Conf.CompressBackups); ok {
			backupfile = resolved
		} else {
			//Remove false cookie
			server.DelBackupTypeCookie(cluster.Conf.BackupPhysicalType)
			return fmt.Errorf("Cancelling flashback. No backup file found for %s", cluster.Conf.BackupPhysicalType)
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sending physical backup to flashback %s", server.URL)

	uncompress := cluster.shouldUncompressOnSenderForReseed()
	sourceName, streamOpener, streamCleanup, err := server.prepareEncryptedPhysicalRestoreSource(backupfile)
	if err != nil {
		return err
	}
	if streamOpener != nil {
		go func() {
			err := server.WaitAndSendSSTStream(context.Background(), task, sourceName, uncompress, 0, streamOpener)
			if err != nil {
				if streamCleanup != nil {
					streamCleanup()
				}
				if server.HasReseedingState(task) {
					server.SetInReseedBackup("")
				}
			}
		}()
		return nil
	}

	go func() {
		err := server.WaitAndSendSST(task, backupfile, uncompress, 0)
		if err != nil {
			if server.HasReseedingState(task) {
				server.SetInReseedBackup("")
			}
		}
	}()

	return nil
}

func (server *ServerMonitor) WriteBackupMetadata(backtype backupmgr.BackupMethod) {
	// CRITICAL FIX: Lock to prevent concurrent metadata updates from async Restic callbacks
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()

	cluster := server.ClusterGroup
	var lastmeta *backupmgr.BackupMetadata

	defer cluster.UpdateDiskStat(server.GetMyBackupDirectory())

	switch backtype {
	case backupmgr.BackupMethodLogical:
		lastmeta = server.LastBackupMeta.Logical
		defer cluster.CheckLogicalBackupToolVersion(server) // Update backup tool version after backup
	case backupmgr.BackupMethodPhysical:
		lastmeta = server.LastBackupMeta.Physical
		defer cluster.CheckPhysicalBackupToolVersion(server) // Update backup tool version after backup
	default:
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Wrong backup type for metadata in %s", server.URL)
		return
	}
	if lastmeta == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "No metadata available to write for %s", server.URL)
		return
	}
	server.ensureBackupSessionID(lastmeta, backtype, lastmeta.StartTime, lastmeta.BackupLine)

	if _, err := os.Stat(lastmeta.Dest); err == nil {
		lastmeta.GetSizeAndFileCount()
		lastmeta.EndTime = time.Now()
	}
	if strings.TrimSpace(lastmeta.Dest) != "" {
		compressed, err := backupmgr.DetectCompressionFromDest(lastmeta.Dest)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to detect compression for %s: %s", lastmeta.Dest, err)
		} else {
			lastmeta.Compressed = compressed
		}
	}

	task := server.JobResults.Get(lastmeta.BackupTool)

	//Wait until job result changed since we're using pointer
	for task.State < 3 {
		time.Sleep(time.Second)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Continue for writing metadata for backup in %s", server.URL)

	if task.State == 3 || task.State == 4 {
		//Wait for binlog metadata sent by writelog API
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Waiting for binlog info: %v", lastmeta)
		for lastmeta.BinLogFileName == "" {
			time.Sleep(time.Second)
		}
		lastmeta.Completed = true
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Metadata completed: %v", lastmeta)
		cluster.BackupPostScript(server, backtype, lastmeta.Dest)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Error occured in backup, writing incomplete metadata for backup in %s", server.URL)
	}

	bjson, err := json.MarshalIndent(lastmeta, "", "\t")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to marshall metadata for backup in %s: %s", server.URL, err.Error())
	}

	metaPath := server.backupMetaFilePath(lastmeta)
	if metaPath == "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to resolve metadata path for backup in %s", server.URL)
		return
	}
	if lastmeta.MetaFile == "" {
		lastmeta.MetaFile = metaPath
	}

	err = os.WriteFile(metaPath, bjson, 0644)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to write metadata for backup in %s: %s", server.URL, err.Error())
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Created metadata for backup in %s", server.URL)
	}

	//Don't change river
	if cluster.Conf.BackupKeepUntilValid && lastmeta.BackupTool != config.ConstBackupLogicalTypeRiver && !lastmeta.IsAdhoc() {
		if lastmeta.Completed {
			// Delete previous meta with same type
			cluster.BackupMetaMap.Delete(lastmeta.Previous)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup valid, removing old backup.")
			exec.Command("rm", "-r", lastmeta.Dest+".old").Run()
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Error occured in backup, rolling back to old backup.")
			exec.Command("mv", lastmeta.Dest, lastmeta.Dest+".err").Run()
			exec.Command("mv", lastmeta.Dest+".old", lastmeta.Dest).Run()
			exec.Command("rm", "-r", lastmeta.Dest+".err").Run()

			// Revert to previous meta with same type
			cluster.BackupMetaMap.Delete(lastmeta.Id)
			switch backtype {
			case backupmgr.BackupMethodLogical:
				_, server.LastBackupMeta.Logical = server.GetLatestMetaForLine("logical", backupmgr.BackupLineDefault)
			case backupmgr.BackupMethodPhysical:
				_, server.LastBackupMeta.Physical = server.GetLatestMetaForLine("physical", backupmgr.BackupLineDefault)
			}
		}
	}
}

func (cluster *Cluster) CreateTmpClientConfFile() (string, error) {
	confOut, err := os.CreateTemp("", "client.cnf")
	if err != nil {
		return "", err
	}

	if _, err := confOut.Write([]byte("[client]\npassword=" + cluster.GetDbPass() + "\n")); err != nil {
		return "", err
	}
	if err := confOut.Close(); err != nil {
		return "", err
	}
	return confOut.Name(), nil

}

func (server *ServerMonitor) encryptBackupMetadataFile(meta *backupmgr.BackupMetadata, backupKind string, keepPlainSource bool) error {
	if server == nil {
		return errors.New("server is nil")
	}
	if meta == nil {
		return errors.New("backup metadata is nil")
	}
	sourcePath := strings.TrimSpace(meta.Dest)
	if sourcePath == "" {
		return errors.New("backup destination is empty")
	}

	info, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errBackupEncryptionUnsupported
	}

	password, keyID, err := server.resolveBackupEncryptionPassphraseForEncrypt()
	if err != nil {
		return err
	}

	tmpPath := sourcePath + ".enc.tmp"
	encPath := sourcePath + ".enc"
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0600
	}
	if err := server.encryptBackupFileStreamWithHeader(sourcePath, tmpPath, password, keyID, mode); err != nil {
		return err
	}
	encInfo, err := os.Stat(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if encInfo.Size() == 0 {
		_ = os.Remove(tmpPath)
		return errors.New("encrypted backup temp file is empty")
	}
	if err := os.Rename(tmpPath, encPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if !keepPlainSource {
		if err := os.Remove(sourcePath); err != nil {
			return err
		}
	}

	meta.Dest = encPath
	meta.Encrypted = true
	meta.EncryptionAlgo = "aes-256-cbc"
	meta.EncryptionTool = backupEncryptionToolOpenSSLEnc
	meta.EncryptionMode = backupEncryptionModeArchive
	if keyID != "" {
		meta.EncryptionVersion = 1
		meta.EncryptionKeyID = keyID
	}
	if err := meta.GetSizeAndFileCount(); err != nil {
		if server.ClusterGroup != nil {
			server.ClusterGroup.LogModulePrintf(server.ClusterGroup.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to refresh encrypted %s backup metadata size for %s: %s", backupKind, server.URL, err)
		}
	}

	return nil
}

func (server *ServerMonitor) runOpenSSLStream(sourcePath, destPath string, fileMode os.FileMode, args ...string) error {
	input, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer input.Close()

	output, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	defer output.Close()

	cmd := exec.Command("openssl", args...)
	cmd.Stdin = input
	cmd.Stdout = output
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		if errMsg == "" {
			if exitCode >= 0 {
				return fmt.Errorf("openssl failed (exit=%d)", exitCode)
			}
			return err
		}
		if exitCode >= 0 {
			return fmt.Errorf("openssl failed (exit=%d): %s", exitCode, errMsg)
		}
		return fmt.Errorf("openssl failed: %s", errMsg)
	}

	if err := output.Sync(); err != nil {
		return err
	}
	return nil
}

func (server *ServerMonitor) runOpenSSLStreamWithPassphrase(sourcePath, destPath string, fileMode os.FileMode, passphrase string, args ...string) error {
	input, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer input.Close()

	output, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	defer output.Close()

	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	defer pr.Close()

	if _, err := io.WriteString(pw, passphrase); err != nil {
		_ = pw.Close()
		return err
	}
	if err := pw.Close(); err != nil {
		return err
	}

	cmd := exec.Command("openssl", args...)
	cmd.Stdin = input
	cmd.Stdout = output
	cmd.ExtraFiles = []*os.File{pr}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		if errMsg == "" {
			if exitCode >= 0 {
				return fmt.Errorf("openssl failed (exit=%d)", exitCode)
			}
			return err
		}
		if exitCode >= 0 {
			return fmt.Errorf("openssl failed (exit=%d): %s", exitCode, errMsg)
		}
		return fmt.Errorf("openssl failed: %s", errMsg)
	}

	if err := output.Sync(); err != nil {
		return err
	}
	return nil
}

func writeBackupEncryptionHeader(output io.Writer, keyID string) error {
	header := backupEncryptionArtifactHeader{
		KeyID:  keyID,
		Cipher: "aes-256-cbc",
		Tool:   backupEncryptionToolOpenSSLEnc,
	}
	b, err := json.Marshal(header)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(output, backupEncryptionHeaderMagic); err != nil {
		return err
	}
	if _, err := output.Write(b); err != nil {
		return err
	}
	if _, err := io.WriteString(output, "\n\n"); err != nil {
		return err
	}
	return nil
}

func (server *ServerMonitor) encryptBackupFileStreamWithHeader(sourcePath, destPath, password, keyID string, fileMode os.FileMode) error {
	if strings.TrimSpace(keyID) == "" {
		return server.encryptBackupFileStream(sourcePath, destPath, password, fileMode)
	}
	tmpCipherPath := destPath + ".cipher.tmp"
	if err := server.encryptBackupFileStream(sourcePath, tmpCipherPath, password, fileMode); err != nil {
		return err
	}
	defer os.Remove(tmpCipherPath)

	cipherFile, err := os.Open(tmpCipherPath)
	if err != nil {
		return err
	}
	defer cipherFile.Close()

	output, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	defer output.Close()

	if err := writeBackupEncryptionHeader(output, keyID); err != nil {
		_ = os.Remove(destPath)
		return err
	}
	if _, err := io.Copy(output, cipherFile); err != nil {
		_ = os.Remove(destPath)
		return err
	}
	if err := output.Sync(); err != nil {
		_ = os.Remove(destPath)
		return err
	}
	return nil
}

func parseBackupEncryptionHeaderFromPath(sourcePath string) (*backupEncryptionArtifactHeader, string, func(), error) {
	input, err := os.Open(sourcePath)
	if err != nil {
		return nil, "", nil, err
	}
	defer input.Close()

	reader := bufio.NewReader(input)
	line1, err := reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, sourcePath, func() {}, nil
		}
		return nil, "", nil, err
	}
	if line1 != backupEncryptionHeaderMagic {
		return nil, sourcePath, func() {}, nil
	}

	line2, err := reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, "", nil, errors.New("invalid backup encryption header: truncated JSON line")
		}
		return nil, "", nil, err
	}
	line3, err := reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, "", nil, errors.New("invalid backup encryption header: missing separator line")
		}
		return nil, "", nil, err
	}
	if line3 != "\n" {
		return nil, "", nil, errors.New("invalid backup encryption header: expected blank separator line")
	}

	var header backupEncryptionArtifactHeader
	if err := decodeBackupEncryptionJSONStrict(strings.TrimSpace(line2), &header); err != nil {
		return nil, "", nil, fmt.Errorf("invalid backup encryption header JSON: %w", err)
	}
	header.KeyID = strings.TrimSpace(header.KeyID)
	header.Cipher = strings.TrimSpace(strings.ToLower(header.Cipher))
	header.Tool = strings.TrimSpace(header.Tool)
	if header.KeyID == "" {
		return nil, "", nil, errors.New("invalid backup encryption header: keyId is required")
	}

	tmpCipher, err := os.CreateTemp("", "repman-enc-payload-*.tmp")
	if err != nil {
		return nil, "", nil, err
	}
	tmpPath := tmpCipher.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := io.Copy(tmpCipher, reader); err != nil {
		_ = tmpCipher.Close()
		cleanup()
		return nil, "", nil, err
	}
	if err := tmpCipher.Close(); err != nil {
		cleanup()
		return nil, "", nil, err
	}

	return &header, tmpPath, cleanup, nil
}

func (server *ServerMonitor) encryptBackupFileStream(sourcePath, destPath, password string, fileMode os.FileMode) error {
	args := []string{"enc", "-aes-256-cbc", "-a", "-salt", "-pass", "fd:3"}
	if err := server.runOpenSSLStreamWithPassphrase(sourcePath, destPath, fileMode, password, args...); err != nil {
		_ = os.Remove(destPath)
		return err
	}
	return nil
}

func (server *ServerMonitor) decryptBackupFileStream(sourcePath, destPath, password string, fileMode os.FileMode) error {
	args := []string{"enc", "-d", "-aes-256-cbc", "-a", "-pass", "fd:3"}
	if err := server.runOpenSSLStreamWithPassphrase(sourcePath, destPath, fileMode, password, args...); err != nil {
		_ = os.Remove(destPath)
		return err
	}
	return nil
}

func isEncryptedDirectoryArchivePath(path string) bool {
	lower := strings.ToLower(strings.TrimSpace(path))
	return strings.HasSuffix(lower, ".tar.enc") || strings.HasSuffix(lower, ".tar.gz.enc")
}

func decryptedArchivePath(encPath string) string {
	trimmed := strings.TrimSpace(encPath)
	lower := strings.ToLower(trimmed)
	if strings.HasSuffix(lower, ".enc") {
		return trimmed[:len(trimmed)-4]
	}
	return trimmed + ".dec"
}

func (server *ServerMonitor) decryptBackupFileToPath(sourcePath, destPath string) error {
	return server.decryptBackupFileToPathWithMetadata(sourcePath, destPath, nil)
}

func (server *ServerMonitor) resolveBackupEncryptionPassphraseForDecrypt(sourcePath string, meta *backupmgr.BackupMetadata) (cipherPath, passphrase string, cleanup func(), err error) {
	header, strippedPath, cleanupFn, err := parseBackupEncryptionHeaderFromPath(sourcePath)
	if err != nil {
		return "", "", nil, err
	}
	cleanup = cleanupFn
	if cleanup == nil {
		cleanup = func() {}
	}

	keyring, err := server.parseBackupEncryptionKeyring()
	if err != nil {
		cleanup()
		return "", "", nil, err
	}

	if header != nil {
		if keyring == nil {
			cleanup()
			return "", "", nil, fmt.Errorf("backup encryption header keyId %q requires backup-encryption-keyring to be configured", header.KeyID)
		}
		pass, ok := keyring.passphraseByKeyID(header.KeyID)
		if !ok {
			cleanup()
			return "", "", nil, fmt.Errorf("backup encryption header keyId %q not found in backup-encryption-keyring", header.KeyID)
		}
		return strippedPath, pass, cleanup, nil
	}

	if keyring != nil && meta != nil {
		metaKeyID := strings.TrimSpace(meta.EncryptionKeyID)
		if metaKeyID != "" {
			if pass, ok := keyring.passphraseByKeyID(metaKeyID); ok {
				return strippedPath, pass, cleanup, nil
			}
		}
	}

	if keyring != nil {
		legacyID := strings.TrimSpace(keyring.LegacyDefaultKeyID)
		if legacyID != "" {
			if pass, ok := keyring.passphraseByKeyID(legacyID); ok {
				return strippedPath, pass, cleanup, nil
			}
		}
	}

	pass, err := server.resolveBackupEncryptionPassphraseForUse()
	if err != nil {
		cleanup()
		return "", "", nil, err
	}
	return strippedPath, pass, cleanup, nil
}

func (server *ServerMonitor) decryptBackupFileToPathWithMetadata(sourcePath, destPath string, meta *backupmgr.BackupMetadata) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("cannot decrypt directory path: %s", sourcePath)
	}

	cipherPath, password, cleanup, err := server.resolveBackupEncryptionPassphraseForDecrypt(sourcePath, meta)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := server.decryptBackupFileToPathWithPassword(cipherPath, destPath, password, info); err != nil {
		for _, oldPass := range server.resolveOldSponsorPassphrases() {
			if oldPass == password {
				continue
			}
			if cluster := server.ClusterGroup; cluster != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
					"Decryption failed, retrying with previous sponsor password (server: %s)", server.URL)
			}
			if retryErr := server.decryptBackupFileToPathWithPassword(cipherPath, destPath, oldPass, info); retryErr == nil {
				return nil
			}
		}
		return err
	}
	return nil
}

func (server *ServerMonitor) decryptBackupFileToPathWithPassword(sourcePath, destPath, password string, info os.FileInfo) error {
	tmpPath := destPath + ".tmp"
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o600
	}
	if err := server.decryptBackupFileStream(sourcePath, tmpPath, password, mode); err != nil {
		return err
	}
	if decInfo, err := os.Stat(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	} else if decInfo.Size() == 0 {
		_ = os.Remove(tmpPath)
		return errors.New("decrypted backup temp file is empty")
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

type perFileRestoreJournalEntry struct {
	EncPath    string `json:"enc_path"`
	EncOldPath string `json:"enc_old_path"`
	PlainPath  string `json:"plain_path"`
	TmpPath    string `json:"tmp_path"`
	State      string `json:"state"`
}

const (
	perFileRestoreStateDecryptedTmp = "decrypted_tmp"
	perFileRestoreStateEncRenamed   = "enc_renamed"
	perFileRestoreStatePlainActive  = "plain_active"
	perFileRestoreJournalFile       = ".repman-restore-journal.json"
)

var writePerFileRestoreJournal = writePerFileRestoreJournalAtomic

func writePerFileRestoreJournalAtomic(journalPath string, entries []perFileRestoreJournalEntry) error {
	data, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("failed to serialize restore journal: %w", err)
	}
	tmpPath := journalPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write restore journal: %w", err)
	}
	if err := os.Rename(tmpPath, journalPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to atomic rename restore journal: %w", err)
	}
	return nil
}

func combineRollbackFailure(opErr, rollbackErr error) error {
	if rollbackErr == nil {
		return opErr
	}
	if opErr == nil {
		return fmt.Errorf("rollback failed: %w", rollbackErr)
	}
	return fmt.Errorf("%w (rollback failed: %v)", opErr, rollbackErr)
}

func (server *ServerMonitor) preparePerFileEncryptedRestoreTransactional(sourceDir string) (string, func(), error) {
	cluster := server.ClusterGroup

	journalPath := filepath.Join(sourceDir, perFileRestoreJournalFile)

	rollbackAndCleanup := func() {
		server.perFileRestoreRollback(journalPath)
	}

	if err := server.perFileRestoreRollback(journalPath); err != nil {
		return "", nil, fmt.Errorf("failed to rollback stale restore journal before per-file restore: %w", err)
	}

	var entries []perFileRestoreJournalEntry

	encFiles := []string{}
	err := filepath.Walk(sourceDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(path), ".enc") {
			encFiles = append(encFiles, path)
		}
		return nil
	})
	if err != nil {
		return "", rollbackAndCleanup, fmt.Errorf("failed to scan encrypted files: %w", err)
	}

	if len(encFiles) == 0 {
		return "", nil, errors.New("no encrypted files found for per-file decrypt")
	}

	var totalEncSize int64
	for _, encPath := range encFiles {
		info, err := os.Stat(encPath)
		if err == nil {
			totalEncSize += info.Size()
		}
	}

	var statfs unix.Statfs_t
	if err := unix.Statfs(sourceDir, &statfs); err != nil {
		return "", rollbackAndCleanup, fmt.Errorf("failed to check disk space: %w", err)
	}
	availableSpace := statfs.Bavail * uint64(statfs.Bsize)
	if int64(availableSpace) < totalEncSize {
		return "", rollbackAndCleanup, fmt.Errorf("insufficient disk space for reversible per-file restore: need %d bytes, have %d bytes", totalEncSize, availableSpace)
	}

	for _, encPath := range encFiles {
		plainPath := strings.TrimSuffix(encPath, ".enc")
		tmpPath := plainPath + ".restore.tmp"
		encOldPath := encPath + ".old"

		if err := server.decryptBackupFileToPathWithMetadata(encPath, tmpPath, nil); err != nil {
			rbErr := server.perFileRestoreRollbackEntries(entries)
			return "", rollbackAndCleanup, combineRollbackFailure(fmt.Errorf("failed to decrypt %s: %w", encPath, err), rbErr)
		}

		entries = append(entries, perFileRestoreJournalEntry{
			EncPath:    encPath,
			EncOldPath: encOldPath,
			PlainPath:  plainPath,
			TmpPath:    tmpPath,
			State:      perFileRestoreStateDecryptedTmp,
		})

		if err := writePerFileRestoreJournal(journalPath, entries); err != nil {
			rbErr := server.perFileRestoreRollbackEntries(entries)
			if rbErr == nil {
				_ = os.Remove(journalPath)
			}
			return "", rollbackAndCleanup, combineRollbackFailure(fmt.Errorf("failed to persist restore journal after decrypt: %w", err), rbErr)
		}

		if err := os.Rename(encPath, encOldPath); err != nil {
			_ = os.Remove(tmpPath)
			rbErr := server.perFileRestoreRollbackEntries(entries)
			if rbErr == nil {
				_ = os.Remove(journalPath)
			}
			return "", rollbackAndCleanup, combineRollbackFailure(fmt.Errorf("failed to rename %s to .old: %w", encPath, err), rbErr)
		}

		entries[len(entries)-1].State = perFileRestoreStateEncRenamed

		if err := writePerFileRestoreJournal(journalPath, entries); err != nil {
			_ = os.Rename(encOldPath, encPath)
			rbErr := server.perFileRestoreRollbackEntries(entries)
			if rbErr == nil {
				_ = os.Remove(journalPath)
			}
			return "", rollbackAndCleanup, combineRollbackFailure(fmt.Errorf("failed to persist restore journal after rename: %w", err), rbErr)
		}

		if err := os.Rename(tmpPath, plainPath); err != nil {
			_ = os.Rename(encOldPath, encPath)
			rbErr := server.perFileRestoreRollbackEntries(entries)
			if rbErr == nil {
				_ = os.Remove(journalPath)
			}
			return "", rollbackAndCleanup, combineRollbackFailure(fmt.Errorf("failed to activate plaintext %s: %w", plainPath, err), rbErr)
		}

		entries[len(entries)-1].State = perFileRestoreStatePlainActive

		if err := writePerFileRestoreJournal(journalPath, entries); err != nil {
			_ = os.Remove(plainPath)
			_ = os.Rename(encOldPath, encPath)
			rbErr := server.perFileRestoreRollbackEntries(entries)
			if rbErr == nil {
				_ = os.Remove(journalPath)
			}
			return "", rollbackAndCleanup, combineRollbackFailure(fmt.Errorf("failed to persist restore journal after activation: %w", err), rbErr)
		}
	}

	cleanupFunc := func() {
		if err := server.perFileRestoreRollback(journalPath); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr,
				"Failed to rollback per-file restore state from journal %s: %s (journal preserved for recovery)", journalPath, err)
			return
		}
		if err := os.Remove(journalPath); err != nil && !os.IsNotExist(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"Failed to remove restore journal: %s", err)
		}
	}

	return sourceDir, cleanupFunc, nil
}

func (server *ServerMonitor) perFileRestoreRollback(journalPath string) error {
	cluster := server.ClusterGroup
	if cluster == nil {
		return errors.New("cluster group is nil")
	}

	journalData, err := os.ReadFile(journalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read restore journal: %w", err)
	}

	var entries []perFileRestoreJournalEntry
	if err := json.Unmarshal(journalData, &entries); err != nil {
		return fmt.Errorf("failed to parse restore journal: %w", err)
	}

	return server.perFileRestoreRollbackEntries(entries)
}

func (server *ServerMonitor) perFileRestoreRollbackEntries(entries []perFileRestoreJournalEntry) error {
	cluster := server.ClusterGroup
	if cluster == nil {
		return errors.New("cluster group is nil")
	}

	var rollbackErrs []error

	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]

		if entry.State == perFileRestoreStatePlainActive || entry.State == perFileRestoreStateEncRenamed {
			if _, err := os.Stat(entry.PlainPath); err == nil {
				if err := os.Remove(entry.PlainPath); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
						"Failed to remove plaintext during rollback: %s", err)
					rollbackErrs = append(rollbackErrs, fmt.Errorf("remove plaintext %s: %w", entry.PlainPath, err))
				}
			}
			if _, err := os.Stat(entry.EncOldPath); err == nil {
				if err := os.Rename(entry.EncOldPath, entry.EncPath); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
						"Failed to restore .old during rollback: %s", err)
					rollbackErrs = append(rollbackErrs, fmt.Errorf("restore encrypted backup %s from %s: %w", entry.EncPath, entry.EncOldPath, err))
				}
			}
		}

		if entry.State == perFileRestoreStateDecryptedTmp {
			if _, err := os.Stat(entry.TmpPath); err == nil {
				if err := os.Remove(entry.TmpPath); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
						"Failed to remove temp file during rollback: %s", err)
					rollbackErrs = append(rollbackErrs, fmt.Errorf("remove temp file %s: %w", entry.TmpPath, err))
				}
			}
		}
	}

	if len(rollbackErrs) > 0 {
		return errors.Join(rollbackErrs...)
	}

	return nil
}

func materializeArchiveHardlink(sourceAbs, destAbs string) error {
	info, err := os.Lstat(sourceAbs)
	if err != nil {
		return fmt.Errorf("hardlink source stat error for %s: %w", sourceAbs, err)
	}
	if info.Mode()&os.ModeType == os.ModeSymlink {
		return fmt.Errorf("hardlink source is a symlink, not a regular file: %s", sourceAbs)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("hardlink source is not a regular file: %s", sourceAbs)
	}

	err = os.Link(sourceAbs, destAbs)
	if err != nil {
		if linkErr := materializeArchiveHardlinkByCopy(sourceAbs, destAbs, info.Mode()); linkErr != nil {
			return fmt.Errorf("hardlink fallback copy failed for %s: %w", destAbs, linkErr)
		}
	}
	return nil
}

func materializeArchiveHardlinkByCopy(sourceAbs, destAbs string, mode os.FileMode) error {
	srcFile, err := os.Open(sourceAbs)
	if err != nil {
		return fmt.Errorf("failed to open hardlink source %s: %w", sourceAbs, err)
	}
	defer srcFile.Close()

	dstFile, err := os.OpenFile(destAbs, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("failed to create hardlink destination %s: %w", destAbs, err)
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return fmt.Errorf("failed to copy hardlink content for %s: %w", destAbs, err)
	}

	return dstFile.Sync()
}

// normalizeArchivePathRef applies tar-style path semantics for archive link targets.
// It treats backslashes as path separators, normalizes with path.Clean (POSIX rules),
// then converts back to platform separators for filesystem operations.
// This is intentional for cross-platform archive compatibility (e.g., Windows-generated tars).
func normalizeArchivePathRef(ref string) string {
	ref = strings.ReplaceAll(ref, "\\", "/")
	ref = path.Clean(ref)
	ref = filepath.FromSlash(ref)
	return ref
}

func validateArchiveWriteParentNoSymlink(targetBaseAbs, destAbs string) error {
	if !isPathWithinBase(targetBaseAbs, destAbs) {
		return fmt.Errorf("archive entry escapes target dir: %s", destAbs)
	}
	parent := filepath.Dir(destAbs)
	if !isPathWithinBase(targetBaseAbs, parent) {
		return fmt.Errorf("archive entry parent escapes target dir: %s", destAbs)
	}
	relParent, err := filepath.Rel(targetBaseAbs, parent)
	if err != nil {
		return err
	}
	if relParent == "." {
		return nil
	}
	current := targetBaseAbs
	parts := strings.Split(relParent, string(filepath.Separator))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("failed to validate archive parent path %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive entry parent path contains symlink component: %s", current)
		}
	}
	return nil
}

func validateArchiveDestinationNotSymlink(destAbs string) error {
	info, err := os.Lstat(destAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to validate archive destination path %s: %w", destAbs, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("archive destination path is a symlink: %s", destAbs)
	}
	return nil
}

func extractArchiveToDir(archivePath, targetDir string) (string, error) {
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer archiveFile.Close()

	var archiveReader io.Reader = archiveFile
	var gzipReader *stdgzip.Reader
	lower := strings.ToLower(archivePath)
	if strings.HasSuffix(lower, ".tar.gz") {
		gzipReader, err = stdgzip.NewReader(archiveFile)
		if err != nil {
			return "", err
		}
		defer gzipReader.Close()
		archiveReader = gzipReader
	}

	targetAbs, err := filepath.Abs(targetDir)
	if err != nil {
		return "", err
	}

	type pendingHardlink struct {
		entryName        string
		destAbs          string
		sourceCandidates []string
		linkTarget       string
	}
	var pendingHardlinks []pendingHardlink

	tr := tar.NewReader(archiveReader)
	firstComponent := ""
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		name := filepath.Clean(header.Name)
		if name == "." || name == "" {
			continue
		}
		destPath := filepath.Join(targetAbs, name)
		destAbs, err := filepath.Abs(destPath)
		if err != nil {
			return "", err
		}
		relToTarget, err := filepath.Rel(targetAbs, destAbs)
		if err != nil {
			return "", err
		}
		if relToTarget == ".." || strings.HasPrefix(relToTarget, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("archive entry escapes target dir: %s", header.Name)
		}

		parts := strings.Split(filepath.ToSlash(name), "/")
		if len(parts) > 0 && firstComponent == "" {
			firstComponent = parts[0]
		}

		if filepath.IsAbs(header.Name) {
			return "", fmt.Errorf("archive entry must be relative for %s: %s", header.Name, header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := validateArchiveWriteParentNoSymlink(targetAbs, destAbs); err != nil {
				return "", err
			}
			if err := validateArchiveDestinationNotSymlink(destAbs); err != nil {
				return "", err
			}
			if err := os.MkdirAll(destAbs, os.FileMode(header.Mode)); err != nil {
				return "", err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := validateArchiveWriteParentNoSymlink(targetAbs, destAbs); err != nil {
				return "", err
			}
			if err := validateArchiveDestinationNotSymlink(destAbs); err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
				return "", err
			}
			out, err := os.OpenFile(destAbs, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return "", err
			}
			if err := out.Close(); err != nil {
				return "", err
			}
		case tar.TypeSymlink:
			if err := validateArchiveWriteParentNoSymlink(targetAbs, destAbs); err != nil {
				return "", err
			}
			if err := validateArchiveDestinationNotSymlink(destAbs); err != nil {
				return "", err
			}
			linkTarget := header.Linkname
			normalizedLinkTarget := normalizeArchivePathRef(linkTarget)
			if strings.TrimSpace(linkTarget) == "" {
				return "", fmt.Errorf("invalid empty symlink target for %s", header.Name)
			}
			if filepath.IsAbs(normalizedLinkTarget) {
				return "", fmt.Errorf("symlink target must be relative for %s: %s", header.Name, header.Linkname)
			}

			resolvedTarget := filepath.Clean(filepath.Join(filepath.Dir(destAbs), normalizedLinkTarget))
			if !isPathWithinBase(targetAbs, resolvedTarget) {
				return "", fmt.Errorf("symlink target escapes target dir for %s: %s", header.Name, header.Linkname)
			}

			if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
				return "", err
			}
			if err := os.Symlink(linkTarget, destAbs); err != nil {
				return "", err
			}
		case tar.TypeLink:
			if err := validateArchiveWriteParentNoSymlink(targetAbs, destAbs); err != nil {
				return "", err
			}
			if err := validateArchiveDestinationNotSymlink(destAbs); err != nil {
				return "", err
			}
			linkTarget := header.Linkname
			normalizedLinkTarget := normalizeArchivePathRef(linkTarget)
			if strings.TrimSpace(linkTarget) == "" {
				return "", fmt.Errorf("invalid empty hardlink target for %s", header.Name)
			}
			if filepath.IsAbs(normalizedLinkTarget) {
				return "", fmt.Errorf("hardlink target must be relative for %s: %s", header.Name, header.Linkname)
			}

			var rootRelative, dirRelative string

			rootRelative = filepath.Clean(filepath.Join(targetAbs, normalizedLinkTarget))
			dirRelative = filepath.Clean(filepath.Join(filepath.Dir(destAbs), normalizedLinkTarget))

			var candidates []string

			// normalizedLinkTarget uses '/' separators (see normalizeArchivePathRef).
			hasPath := strings.Contains(normalizedLinkTarget, "/")
			if hasPath {
				if isPathWithinBase(targetAbs, rootRelative) {
					candidates = append(candidates, rootRelative)
				}
				if isPathWithinBase(targetAbs, dirRelative) {
					candidates = append(candidates, dirRelative)
				}
			} else {
				if isPathWithinBase(targetAbs, dirRelative) {
					candidates = append(candidates, dirRelative)
				}
				if isPathWithinBase(targetAbs, rootRelative) {
					candidates = append(candidates, rootRelative)
				}
			}

			if len(candidates) == 0 {
				return "", fmt.Errorf("hardlink target escapes target dir for %s: %s", header.Name, header.Linkname)
			}

			if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
				return "", err
			}

			resolvedSource := ""
			for _, cand := range candidates {
				if _, err := os.Stat(cand); err != nil {
					if os.IsNotExist(err) {
						continue
					}
					return "", fmt.Errorf("hardlink source stat error for %s: %w", header.Name, err)
				}
				resolvedSource = cand
				break
			}

			if resolvedSource == "" {
				pendingHardlinks = append(pendingHardlinks, pendingHardlink{
					entryName:        header.Name,
					destAbs:          destAbs,
					sourceCandidates: candidates,
					linkTarget:       linkTarget,
				})
			} else {
				if err := materializeArchiveHardlink(resolvedSource, destAbs); err != nil {
					return "", err
				}
			}
		default:
			return "", fmt.Errorf("unsupported archive entry type %d for %s", header.Typeflag, header.Name)
		}
	}

	if len(pendingHardlinks) > 0 {
		maxPasses := len(pendingHardlinks)
		for pass := 0; pass < maxPasses; pass++ {
			progress := false
			var remaining []pendingHardlink
			for _, hl := range pendingHardlinks {
				resolvedSource := ""
				for _, cand := range hl.sourceCandidates {
					if _, err := os.Stat(cand); err != nil {
						if os.IsNotExist(err) {
							continue
						}
						return "", fmt.Errorf("hardlink source stat error for %s: %w", hl.entryName, err)
					}
					resolvedSource = cand
					break
				}

				if resolvedSource == "" {
					remaining = append(remaining, hl)
				} else {
					if err := materializeArchiveHardlink(resolvedSource, hl.destAbs); err != nil {
						return "", err
					}
					progress = true
				}
			}
			pendingHardlinks = remaining
			if !progress {
				break
			}
		}
		if len(pendingHardlinks) > 0 {
			return "", fmt.Errorf("unresolved hardlink target for %s: %s", pendingHardlinks[0].entryName, pendingHardlinks[0].linkTarget)
		}
	}

	if firstComponent == "" {
		return "", errors.New("archive is empty")
	}
	extractedRoot := filepath.Join(targetAbs, firstComponent)
	if _, err := os.Stat(extractedRoot); err != nil {
		return "", err
	}
	return extractedRoot, nil
}

func inferBackupEncryptionMode(meta *backupmgr.BackupMetadata, backupPath string) string {
	if meta != nil {
		mode := normalizeBackupEncryptionDirectoryMode(meta.EncryptionMode)
		if mode == backupEncryptionModePerFile {
			return backupEncryptionModePerFile
		}
	}
	if isEncryptedDirectoryArchivePath(backupPath) {
		return backupEncryptionModeArchive
	}
	return ""
}

type cleanupOnCloseReadCloser struct {
	io.ReadCloser
	cleanupOnce *sync.Once
	cleanupFunc func()
}

func (r *cleanupOnCloseReadCloser) Close() error {
	err := r.ReadCloser.Close()
	if r.cleanupOnce != nil && r.cleanupFunc != nil {
		r.cleanupOnce.Do(r.cleanupFunc)
	}
	return err
}

func (server *ServerMonitor) prepareEncryptedSingleFileRestorePath(backupPath, tempPrefix string, meta *backupmgr.BackupMetadata) (string, func(), error) {
	cluster := server.ClusterGroup
	tmpRoot, err := os.MkdirTemp("", tempPrefix)
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		if err := os.RemoveAll(tmpRoot); err != nil && cluster != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"Failed to cleanup temporary restore path %s: %s", tmpRoot, err)
		}
	}

	decryptedPath := filepath.Join(tmpRoot, filepath.Base(decryptedArchivePath(backupPath)))
	if err := server.decryptBackupFileToPathWithMetadata(backupPath, decryptedPath, meta); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("failed to decrypt backup file %s: %w", backupPath, err)
	}

	return decryptedPath, cleanup, nil
}

func (server *ServerMonitor) prepareEncryptedPhysicalRestoreSource(backupPath string) (string, SSTStreamOpener, func(), error) {
	trimmed := strings.TrimSpace(backupPath)
	if !strings.HasSuffix(strings.ToLower(trimmed), ".enc") {
		return trimmed, nil, nil, nil
	}
	decryptedPath, cleanup, err := server.prepareEncryptedSingleFileRestorePath(trimmed, "repman-physical-restore-*", nil)
	if err != nil {
		return "", nil, nil, err
	}

	var cleanupOnce sync.Once
	streamOpener := func() (io.ReadCloser, int64, error) {
		file, err := os.Open(decryptedPath)
		if err != nil {
			cleanupOnce.Do(cleanup)
			return nil, 0, err
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			cleanupOnce.Do(cleanup)
			return nil, 0, err
		}
		return &cleanupOnCloseReadCloser{
			ReadCloser:  file,
			cleanupOnce: &cleanupOnce,
			cleanupFunc: cleanup,
		}, info.Size(), nil
	}

	return decryptedPath, streamOpener, func() { cleanupOnce.Do(cleanup) }, nil
}

func (server *ServerMonitor) prepareEncryptedDirectoryLogicalRestorePath(backupPath, backupType string, meta *backupmgr.BackupMetadata) (string, func(), error) {
	cluster := server.ClusterGroup
	if cluster == nil {
		return "", nil, errors.New("cluster group is nil")
	}
	mode := inferBackupEncryptionMode(meta, backupPath)
	if mode == "" {
		if strings.HasSuffix(strings.ToLower(strings.TrimSpace(backupPath)), ".enc") {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
				"Preparing encrypted logical single-file restore for %s from %s", backupType, backupPath)
			return server.prepareEncryptedSingleFileRestorePath(backupPath, "repman-logical-restore-file-*", meta)
		}
		return backupPath, nil, nil
	}
	if mode == backupEncryptionModePerFile {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Preparing per-file encrypted logical restore for %s from %s", backupType, backupPath)

		if cluster.Conf.BackupKeepUntilValid {
			restorePath, cleanup, err := server.preparePerFileEncryptedRestoreTransactional(backupPath)
			if err != nil {
				return "", nil, err
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
				"Prepared per-file encrypted logical restore from %s (safe reversible mode)", backupPath)
			return restorePath, cleanup, nil
		}

		if cluster.Conf.BackupEncryptionUnsafePerFileRestore {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"Unsafe per-file encrypted restore enabled for %s from %s; decrypting in place without .old rollback safety",
				backupType, backupPath)
			decryptedCount, err := server.decryptBackupDirectoryPerFile(backupPath)
			if err != nil {
				return "", nil, err
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"Decrypted %d files in-place for per-file encrypted logical restore from %s", decryptedCount, backupPath)
			return backupPath, nil, nil
		}

		return "", nil, errors.New("per-file encrypted restore requires backup-keep-until-valid=true for safe reversible staging; or set backup-encryption-unsafe-per-file-restore=true to force unsafe in-place restore (backup may become unrecoverable if restore fails)")
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Preparing encrypted directory logical restore for %s from %s", backupType, backupPath)

	decryptedArchive, cleanup, err := server.prepareEncryptedSingleFileRestorePath(backupPath, "repman-logical-restore-*", meta)
	if err != nil {
		return "", nil, fmt.Errorf("failed to decrypt backup archive %s: %w", backupPath, err)
	}
	tmpRoot := filepath.Dir(decryptedArchive)

	extractDir := filepath.Join(tmpRoot, "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		cleanup()
		return "", nil, err
	}
	extractedRoot, err := extractArchiveToDir(decryptedArchive, extractDir)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("failed to extract decrypted archive %s: %w", decryptedArchive, err)
	}

	return extractedRoot, cleanup, nil
}

func (server *ServerMonitor) JobFinishReceiveFile(task string) error {
	cluster := server.ClusterGroup

	switch task {
	case "errorlog":
		server.DelWaitErrorlogCookie()
	case "slowquery":
		server.DelWaitSlowqueryCookie()
	case "auditlog":
		server.DelWaitAuditlogCookie()
	case "sqlerrorlog":
		server.DelWaitSqlErrorlogCookie()
	case config.ConstBackupPhysicalTypeXtrabackup, config.ConstBackupPhysicalTypeMariaBackup:
		backtype := "physical"
		if cluster.Conf.BackupEncryptionEnabled && server.LastBackupMeta.Physical != nil && server.LastBackupMeta.Physical.Completed {
			encryptErr := server.encryptBackupMetadataFile(server.LastBackupMeta.Physical, backtype, false)
			if encryptErr != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Physical backup encryption failed for %s: %s", server.URL, encryptErr)
				server.LastBackupMeta.Physical.Completed = false
				if e2 := server.JobsUpdateState(task, encryptErr.Error(), 5, 1); e2 != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Task only updated in runtime. Error while writing to jobs table: %s", e2.Error())
				}
			}
		}

		server.WriteBackupMetadata(backupmgr.BackupMethodPhysical)
		if server.LastBackupMeta.Physical != nil && !server.LastBackupMeta.Physical.StartTime.IsZero() {
			backupTool := server.LastBackupMeta.Physical.BackupTool
			if backupTool == "" {
				backupTool = cluster.Conf.BackupPhysicalType
			}
			elapsed := time.Since(server.LastBackupMeta.Physical.StartTime).Round(time.Second)
			if server.LastBackupMeta.Physical.Completed {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Physical backup %s completed in %s (started at %s) for: %s", backupTool, elapsed, server.LastBackupMeta.Physical.StartTime.Format(time.RFC3339), server.URL)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Physical backup %s failed in %s (started at %s) for: %s", backupTool, elapsed, server.LastBackupMeta.Physical.StartTime.Format(time.RFC3339), server.URL)
			}
		}

		// CRITICAL: Transition from traditional backup lock to Restic lock atomically
		// Set Restic flag BEFORE clearing physical backup flag
		resticEnabled := server.LastBackupMeta.Physical != nil && server.LastBackupMeta.Physical.ResticEnabled
		backupCompleted := server.LastBackupMeta.Physical != nil && server.LastBackupMeta.Physical.Completed
		backupLine := backupmgr.BackupLineDefault
		if server.LastBackupMeta.Physical != nil && server.LastBackupMeta.Physical.BackupLine != "" {
			backupLine = server.LastBackupMeta.Physical.BackupLine
		}
		if resticEnabled && backupCompleted {
			cluster.SetInResticPhysicalBackupState(true)
			resticPath := server.GetMyBackupDirectory()
			if server.LastBackupMeta.Physical != nil && server.LastBackupMeta.Physical.IsAdhoc() && server.LastBackupMeta.Physical.Dest != "" {
				resticPath = server.LastBackupMeta.Physical.Dest
			}

			// Create restic snapshot asynchronously and update metadata when complete
			// Note: BackupRestic handles its own error logging and flag clearing if prerequisites fail
			server.BackupRestic(backupmgr.BackupMethodPhysical, true, resticPath, server.BuildResticTags(backtype, cluster.Conf.BackupPhysicalType, backupLine, server.LastBackupMeta.Physical)...)
		} else if resticEnabled && !backupCompleted {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Skipping restic backup because physical backup not completed for: %s", server.URL)
		}

		// Now safe to clear traditional backup flag
		cluster.SetInPhysicalBackupState(false)
	case "printdefault-current":
		filename := filepath.Join(server.Datadir, "current.cnf")
		os.Rename(filename, filename+".old")
		err := server.LoadFromTempConfigFile(filename+".tmp", filename)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Load from temp config error: %s", err)
			return err
		}

		err = server.ReadVariablesFromConfigFile(filename, "deployed", true)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Read variables from config error: %s", err)
			return err
		}
	case "printdefault-dummy":
		filename := filepath.Join(server.Datadir, "dummy.cnf")
		os.Rename(filename, filename+".old")

		err := server.LoadFromTempConfigFile(filename+".tmp", filename)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Load from temp config error: %s", err)
			return err
		}

		err = server.ReadVariablesFromConfigFile(filename, "config", true)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Read variables from config error: %s", err)
		}
		server.IsNeedPathCheck = true
	}
	return nil
}
