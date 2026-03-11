Backup Encryption Phase 2: Directory-Based Logical Backups

Status: Implemented (Phase 2 baseline)

Date: 2026-03-11

Last Updated: 2026-03-11

Implementation Summary

- Phase 2 baseline is implemented in backup, restore, and restic path selection.
- Directory logical backups are archived (`tar.gz` default or `tar`), encrypted into a single `.enc` artifact, and metadata points to the encrypted artifact.
- Restore path supports encrypted directory archives by decrypting to a temp archive, extracting to a temp directory, then running existing restore logic.
- Restic now uses logical metadata `Dest` so encrypted logical artifacts are uploaded, including non-adhoc logical backups.
- Encryption/decryption now uses OpenSSL streaming (`openssl enc`) to avoid whole-file in-memory buffering.
- Backup jobs hard-fail early when encryption is enabled but `openssl` is not available in `PATH`.

Scope

- Add backup encryption support for directory outputs from logical tools:
  - mydumper
  - dumpling
  - splitdump
- Keep existing phase 1 single-file encryption unchanged.

Objectives

- Produce one encrypted artifact (`.enc`) for directory-based logical backups.
- Preserve restore operability by decrypting then extracting before import.
- Ensure restic uploads encrypted artifacts only when encryption is enabled.

Design Decisions

1) Directory packaging strategy

- Convert directory backup output to an archive before encryption.
- Default archive format: `tar.gz`.
- Optional alternate format: `tar`.
- Final encrypted output examples:
  - `backup.logical.tar.gz.enc`
  - `backup.logical.tar.enc`

2) Encryption flow for directory backups

- After successful logical backup directory generation:
  - Create archive from directory.
  - Encrypt archive with OpenSSL AES-256-CBC stream flow.
  - Write atomically via `.enc.tmp` then rename to `.enc`.
  - Validate encrypted file is present and non-empty.
  - Remove plaintext archive and source directory only after successful validation.
- Failure behavior:
  - Mark backup as incomplete/failed.
  - Skip restic upload.
  - Keep plaintext outputs for troubleshooting/retry.

3) Passphrase resolution (unchanged)

- Use existing precedence:
  1. `REPLICATION_MANAGER_BACKUP_PASSPHRASE`
  2. `backup-encryption-passphrase`
  3. admin password from `api-credentials`
  4. admin password from `api-credentials-external`
  5. fallback `server.Pass`

OpenSSL compatibility details

- Encryption command profile uses OpenSSL-compatible options:
  - `openssl enc -aes-256-cbc -a -salt -pass pass:<passphrase>`
- Decryption command profile uses:
  - `openssl enc -d -aes-256-cbc -a -pass pass:<passphrase>`
- Requirement: `openssl` binary must be installed and available in `PATH` on backup/restore hosts.
- Metadata records encryption tool as `encryptionTool` (current value: `openssl-enc`) to support future tool migration while preserving backward compatibility.

4) Metadata rules

- Update metadata after encryption success:
  - `Dest` points to encrypted archive (`*.enc`).
  - `Encrypted = true`.
  - `EncryptionAlgo = "aes-256-cbc"`.
  - `EncryptionTool = "openssl-enc"`.
  - `EncryptionMode = "archive"|"per-file"`.
- Track archive mode in metadata extension field or log context:
  - `tar` or `tar.gz`.

5) Restore path behavior

- For encrypted directory-based logical backups:
  - Decrypt `.enc` to archive.
  - Extract archive to temporary directory.
  - Run existing directory restore flow from extracted path.
  - Clean up temporary decrypted artifacts.

6) Restic behavior

- Restic source path must use metadata `Dest` (encrypted artifact).
- Extend fallback checks to handle archive+encrypted variants where needed.

Configuration Additions

- `backup-encryption-directory-format` (string): `tar.gz` (default) or `tar`.
- `backup-encryption-directory-mode` (string): `archive` (default) or `per-file`.
- `backup-encryption-keep-plain-dir` (bool): default `false`.
  - `true` reserved for debugging and transitional operations.

Implementation Tasks (Checklist)

- [x] Add archive builder helper for directory outputs in backup job flow.
- [x] Add config + flag wiring for directory archive format.
- [x] Add config + flag wiring for keeping plaintext directory (debug only).
- [x] Integrate archive+encrypt path in `JobBackupLogicalWithOptions` for directory tools.
- [x] Wire metadata updates (`Dest`, `Encrypted`, `EncryptionAlgo`) for directory encryption.
- [x] Ensure restic path selection uses encrypted artifact destination.
- [x] Extend restore flow to decrypt+extract before directory restore.
- [x] Add cleanup guards for temp decrypted archive/directories.
- [x] Add WARN/ERR logs for each stage without leaking secrets.
- [ ] Update docs and operator runbook with directory decrypt/restore examples.

Code Locations

- `cluster/srv_job_backup.go`
  - Directory archive creation (`tar`/`tar.gz`) and encryption integration.
  - Logical backup encryption flow updates for directory destinations.
  - Encrypted directory restore preparation (decrypt + extract + temp cleanup).
  - Restore-path integration in `ProcessReseedLogical` and `JobFlashbackLogicalBackup`.
- `cluster/srv_job_restic.go`
  - Extended archive/compression fallback mapping for `.tar(.gz).enc` variants.
- `config/config.go`
  - Added `backup-encryption-directory-format` and `backup-encryption-keep-plain-dir` fields.
- `server/server.go`
  - Added CLI flags for directory encryption format and keep-plain-dir behavior.
- `server/api_cluster.go`
  - Added runtime setting/toggle support and format validation for cluster-level updates.

Known Limitations and Follow-Ups

- Config-file invalid values for `backup-encryption-directory-format` currently normalize to `tar.gz` in code paths using normalization helpers.
  - Follow-up: add strict validation on load (or explicit warning) for invalid values.

Acceptance Criteria

- Directory logical backup with encryption enabled produces one decryptable `.enc` artifact.
- Plaintext directory and plaintext archive are removed on success (unless debug keep flag is enabled).
- Encryption failure keeps plaintext data, marks backup failed, and skips restic.
- Restore from encrypted directory backup succeeds end-to-end.
- Restic snapshot content points to encrypted artifact path.

Test Plan

Unit tests

- Archive helper creates valid tar and tar.gz from sample directory trees.
- Encryption output naming and metadata fields are correct.
- Failure paths: archive create failure, encrypt failure, cleanup failure.

Integration tests

- mydumper + encryption -> `.tar.gz.enc` (or configured mode) output.
- dumpling + encryption -> `.enc` output.
- splitdump + encryption -> `.enc` output.
- restore decrypt+extract+import flow succeeds.

Current Validation

- Package tests passed after implementation:
  - `go test ./cluster/... ./server/... ./config/...`

Negative tests

- Wrong passphrase decrypt fails predictably.
- Missing passphrase source fails with actionable error.
- restic is skipped when directory encryption fails.

Operational Notes

- Directory archive + encryption increases temporary disk footprint.
- Document expected storage overhead and cleanup behavior.
- Recommend enabling in non-production first and validating restore SLA.
